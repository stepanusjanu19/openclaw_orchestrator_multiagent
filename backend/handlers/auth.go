// handlers/auth.go
// Login/logout endpoints dan auth middleware.
// Mendukung Dual-Mode Authentication:
//   Mode A: { "password": "..." }
//   Mode B: { "gateway_token": "..." }  — dibaca live dari openclaw.json

package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"multiagent-platform/internal/auth"
	"multiagent-platform/internal/config"
	"multiagent-platform/internal/netmeta"
	"net/http"
	"strings"
)

const (
	sessionCookieName = "ocmap_session"
	csrfCookieName    = "ocmap_csrf"
)

type AuthHandler struct {
	Sessions      *auth.SessionStore
	InternalToken string
	OCDir         string // Path ke ~/.openclaw untuk baca gateway token live
	Cfg           *config.Config
}

// LoginRequest adalah body untuk POST /api/auth/login.
// Mendukung dua mode: password atau gateway_token.
type LoginRequest struct {
	Password     string `json:"password"`
	GatewayToken string `json:"gateway_token"`
}

// Login handler untuk POST /api/auth/login.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Ekstrak IP client dengan trusted proxy policy agar lockout tidak bisa di-spoof via X-Forwarded-For.
	ip := netmeta.ClientIPString(r, h.Cfg)

	// ── Cek lockout sebelum menerima request ─────────────────────────────────
	if locked, remaining := h.Sessions.IsLockedOut(ip); locked {
		mins := int(remaining.Minutes()) + 1
		w.Header().Set("Retry-After", fmt.Sprintf("%d", mins*60))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("Akun terkunci karena terlalu banyak percobaan gagal. Coba lagi dalam %d menit.", mins),
		})
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Request body tidak valid")
		return
	}

	// ── Mode B: Gateway Token ────────────────────────────────────────────────
	if req.GatewayToken != "" {
		creds, err := h.Sessions.LoginWithGatewayToken(req.GatewayToken, h.OCDir)
		if err != nil {
			h.Sessions.RecordFailedAttempt(ip)
			log.Printf("[AUTH] ❌ Gateway token login GAGAL dari IP %s: %v", ip, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Token tidak valid"})
			return
		}
		h.Sessions.ResetFailedAttempts(ip)
		setSessionCookies(w, r, creds)
		log.Printf("[AUTH] ✅ Login via gateway_token dari IP %s", ip)
		jsonOK(w, map[string]interface{}{
			"message": "Login berhasil via OpenClaw Gateway Token",
			"mode":    "gateway_token",
		})
		return
	}

	// ── Mode A: Password ─────────────────────────────────────────────────────
	if req.Password != "" {
		creds, err := h.Sessions.Login(req.Password)
		if err != nil {
			h.Sessions.RecordFailedAttempt(ip)
			log.Printf("[AUTH] ❌ Password login GAGAL dari IP %s", ip)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Password salah"})
			return
		}
		h.Sessions.ResetFailedAttempts(ip)
		setSessionCookies(w, r, creds)
		log.Printf("[AUTH] ✅ Login via password dari IP %s", ip)
		jsonOK(w, map[string]interface{}{
			"message": "Login berhasil",
			"mode":    "password",
		})
		return
	}

	// Tidak ada credential apapun yang diberikan
	jsonError(w, http.StatusBadRequest, "Diperlukan 'password' atau 'gateway_token'")
}

// Logout handler untuk POST /api/auth/logout.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	token := extractSessionToken(r)
	if token != "" {
		h.Sessions.Logout(token)
	}
	clearSessionCookies(w, r)
	jsonOK(w, map[string]string{"message": "Logout berhasil"})
}

// Me handler untuk GET /api/auth/me — validasi token.
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	token := extractSessionToken(r)
	if !h.Sessions.Validate(token) {
		jsonError(w, http.StatusUnauthorized, "Token tidak valid atau sudah expired")
		return
	}
	jsonOK(w, map[string]interface{}{
		"status":          "authenticated",
		"active_sessions": h.Sessions.ActiveSessionCount(),
	})
}

// AuthMiddleware melindungi route yang memerlukan auth.
func AuthMiddleware(sessions *auth.SessionStore, internalToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Selalu allow OPTIONS (preflight CORS)
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		// Rute publik yang tidak perlu auth
		if isPublicRoute(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		token := extractSessionToken(r)
		authHeaderToken := extractBearerToken(r)

		// Internal bearer is only for explicitly internal machine-to-machine APIs.
		// Do not allow it to bypass dashboard auth/CSRF for admin routes.
		if internalToken != "" && authHeaderToken == internalToken && isInternalBearerRoute(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		if !sessions.Validate(token) {
			jsonError(w, http.StatusUnauthorized, "Autentikasi diperlukan")
			return
		}
		if requiresCSRF(r) {
			csrfHeader := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
			csrfCookie := readCookieValue(r, csrfCookieName)
			if csrfHeader == "" || csrfCookie == "" || subtleCompare(csrfHeader, csrfCookie) == false || !sessions.ValidateCSRF(token, csrfHeader) {
				jsonError(w, http.StatusForbidden, "CSRF token tidak valid")
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func subtleCompare(a, b string) bool {
	return len(a) == len(b) && len(a) > 0 && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func extractBearerToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}
	return ""
}

func extractSessionToken(r *http.Request) string {
	return readCookieValue(r, sessionCookieName)
}

func readCookieValue(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}

func requiresCSRF(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
		return !isPublicRoute(r.URL.Path)
	default:
		return false
	}
}

func setSessionCookies(w http.ResponseWriter, r *http.Request, creds auth.SessionCredentials) {
	http.SetCookie(w, newCookie(r, sessionCookieName, creds.Token, true))
	http.SetCookie(w, newCookie(r, csrfCookieName, creds.CSRF, false))
}

func clearSessionCookies(w http.ResponseWriter, r *http.Request) {
	expired := func(name string, httpOnly bool) *http.Cookie {
		c := newCookie(r, name, "", httpOnly)
		c.MaxAge = -1
		return c
	}
	http.SetCookie(w, expired(sessionCookieName, true))
	http.SetCookie(w, expired(csrfCookieName, false))
}

func newCookie(r *http.Request, name, value string, httpOnly bool) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: httpOnly,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(auth.TokenTTL.Seconds()),
	}
}

// isPublicRoute menentukan route yang tidak memerlukan auth.
func isPublicRoute(path string) bool {
	if strings.HasPrefix(path, "/api/webhook/") {
		return true
	}

	publicPaths := []string{
		"/api/auth/login",
		"/api/health",
		// NOTE: /api/system/harden/* telah DIPINDAHKAN ke rute protected (memerlukan sesi auth).
		// Sebelumnya terdaftar sebagai public — ini adalah kerentanan kritis (IDOR + privesc tanpa auth).
	}
	for _, p := range publicPaths {
		if path == p {
			return true
		}
	}
	return false
}

func isInternalBearerRoute(path string) bool {
	return strings.HasPrefix(path, "/api/security/internal/")
}
