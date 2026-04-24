// internal/auth/session.go
// Hardened in-memory session store dengan dual-mode authentication:
//   Mode A: Password (DASHBOARD_PASSWORD dari env)
//   Mode B: Gateway Token dari ~/.openclaw/openclaw.json
//
// Security features:
//   - Constant-time comparison (anti timing-attack)
//   - Account lockout: 5 gagal → lockout 15 menit per IP
//   - Concurrent session limit: max 10 session aktif
//   - Session cleanup otomatis per Login
//   - LoginWithGatewayToken: live-read dari openclaw.json

package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"multiagent-platform/internal/openclaw"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	TokenTTL             = 24 * time.Hour
	MaxActiveSessions    = 10
	MaxFailedAttempts    = 5
	LockoutDuration      = 15 * time.Minute
	FailedWindowDuration = 5 * time.Minute
)

// failedAttempt menyimpan waktu satu percobaan login gagal.
type failedAttempt struct {
	at time.Time
}

// lockoutEntry menyimpan status lockout untuk satu IP.
type lockoutEntry struct {
	lockedUntil time.Time
	attempts    []failedAttempt
}

type SessionCredentials struct {
	Token string
	CSRF  string
}

type sessionEntry struct {
	Expiry time.Time
	CSRF   string
}

// SessionStore manages dashboard login sessions.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]sessionEntry // token → session metadata
	password string

	// Account lockout per IP
	lockMu   sync.Mutex
	lockouts map[string]*lockoutEntry
}

// NewSessionStore membuat session store baru.
// Jika password kosong, gunakan default "admin".
func NewSessionStore(password string) *SessionStore {
	if password == "" {
		password = "admin"
	}
	return &SessionStore{
		sessions: make(map[string]sessionEntry),
		password: password,
		lockouts: make(map[string]*lockoutEntry),
	}
}

// IsLockedOut mengecek apakah IP sedang dalam masa lockout.
func (s *SessionStore) IsLockedOut(ip string) (bool, time.Duration) {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()

	entry, ok := s.lockouts[ip]
	if !ok {
		return false, 0
	}
	if time.Now().Before(entry.lockedUntil) {
		return true, time.Until(entry.lockedUntil)
	}
	// Lockout sudah lewat — reset
	delete(s.lockouts, ip)
	return false, 0
}

// RecordFailedAttempt mencatat percobaan login gagal untuk IP.
// Jika melebihi MaxFailedAttempts dalam FailedWindowDuration → lockout.
func (s *SessionStore) RecordFailedAttempt(ip string) {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()

	now := time.Now()
	entry, ok := s.lockouts[ip]
	if !ok {
		entry = &lockoutEntry{}
		s.lockouts[ip] = entry
	}

	// Buang attempt yang sudah keluar window
	fresh := entry.attempts[:0]
	for _, a := range entry.attempts {
		if now.Sub(a.at) < FailedWindowDuration {
			fresh = append(fresh, a)
		}
	}
	fresh = append(fresh, failedAttempt{at: now})
	entry.attempts = fresh

	if len(entry.attempts) >= MaxFailedAttempts {
		entry.lockedUntil = now.Add(LockoutDuration)
		entry.attempts = nil // reset counter setelah lockout
	}
}

// ResetFailedAttempts menghapus counter gagal untuk IP (dipanggil saat login berhasil).
func (s *SessionStore) ResetFailedAttempts(ip string) {
	s.lockMu.Lock()
	delete(s.lockouts, ip)
	s.lockMu.Unlock()
}

// Login memvalidasi password dan mengembalikan session token.
func (s *SessionStore) Login(password string) (SessionCredentials, error) {
	// Constant-time comparison mencegah timing attack (OWASP A07)
	if subtle.ConstantTimeCompare([]byte(password), []byte(s.password)) != 1 {
		return SessionCredentials{}, fmt.Errorf("password salah")
	}
	return s.issueToken()
}

// LoginWithGatewayToken memvalidasi token dari openclaw.json gateway.auth.token
// secara live-read — token selalu dibaca segar tiap kali dipanggil.
func (s *SessionStore) LoginWithGatewayToken(providedToken, ocDir string) (SessionCredentials, error) {
	if providedToken == "" {
		return SessionCredentials{}, fmt.Errorf("gateway token kosong")
	}

	gwToken, err := readGatewayToken(ocDir)
	if err != nil {
		return SessionCredentials{}, fmt.Errorf("gagal baca openclaw.json: %w", err)
	}
	if gwToken == "" {
		return SessionCredentials{}, fmt.Errorf("gateway.auth.token tidak dikonfigurasi di openclaw.json")
	}

	// Constant-time compare — mencegah timing attack
	if subtle.ConstantTimeCompare([]byte(providedToken), []byte(gwToken)) != 1 {
		return SessionCredentials{}, fmt.Errorf("gateway token tidak valid")
	}
	return s.issueToken()
}

// issueToken membuat session token baru dan menyimpannya di store.
func (s *SessionStore) issueToken() (SessionCredentials, error) {
	token := generateToken()
	csrf := generateToken()
	s.mu.Lock()
	defer s.mu.Unlock()

	// Cleanup expired sessions
	s.cleanup()

	// Concurrent session limit — cegah session explosion
	if len(s.sessions) >= MaxActiveSessions {
		// Buang session tertua
		var oldest string
		var oldestExp time.Time
		for tok, entry := range s.sessions {
			if oldest == "" || entry.Expiry.Before(oldestExp) {
				oldest = tok
				oldestExp = entry.Expiry
			}
		}
		delete(s.sessions, oldest)
	}

	s.sessions[token] = sessionEntry{
		Expiry: time.Now().Add(TokenTTL),
		CSRF:   csrf,
	}
	return SessionCredentials{Token: token, CSRF: csrf}, nil
}

// Validate mengecek apakah token valid dan belum expired.
func (s *SessionStore) Validate(token string) bool {
	if token == "" {
		return false
	}
	s.mu.RLock()
	entry, ok := s.sessions[token]
	s.mu.RUnlock()
	return ok && time.Now().Before(entry.Expiry)
}

func (s *SessionStore) ValidateCSRF(token, csrf string) bool {
	if token == "" || csrf == "" {
		return false
	}
	s.mu.RLock()
	entry, ok := s.sessions[token]
	s.mu.RUnlock()
	if !ok || time.Now().After(entry.Expiry) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(entry.CSRF), []byte(csrf)) == 1
}

// Logout menghapus session token.
func (s *SessionStore) Logout(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// InvalidateAll mencabut SEMUA session aktif (emergency revoke).
func (s *SessionStore) InvalidateAll() {
	s.mu.Lock()
	s.sessions = make(map[string]sessionEntry)
	s.mu.Unlock()
}

// ActiveSessionCount mengembalikan jumlah session aktif saat ini.
func (s *SessionStore) ActiveSessionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanup()
	return len(s.sessions)
}

// cleanup menghapus session yang sudah expired.
// HARUS dipanggil dalam kondisi mu.Lock() sudah dipegang.
func (s *SessionStore) cleanup() {
	now := time.Now()
	for tok, entry := range s.sessions {
		if now.After(entry.Expiry) {
			delete(s.sessions, tok)
		}
	}
}

// generateToken membuat random 32-byte token dalam base64 URL encoding.
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return base64.URLEncoding.EncodeToString(b)
}

// readGatewayToken membaca gateway.auth.token dari openclaw.json secara live.
func readGatewayToken(ocDir string) (string, error) {
	configPath := filepath.Join(ocDir, "openclaw.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", err
	}

	var doc struct {
		Gateway struct {
			Auth struct {
				Token json.RawMessage `json:"token"`
			} `json:"auth"`
		} `json:"gateway"`
	}
	if err := openclaw.DecodeConfigJSON(data, &doc); err != nil {
		return "", err
	}
	return resolveGatewayToken(doc.Gateway.Auth.Token)
}

func resolveGatewayToken(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}

	var token string
	if err := json.Unmarshal(raw, &token); err == nil {
		return token, nil
	}

	var ref struct {
		Source string `json:"source"`
		ID     string `json:"id"`
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		return "", err
	}
	if ref.Source == "env" && ref.ID != "" {
		return os.Getenv(ref.ID), nil
	}
	return "", nil
}
