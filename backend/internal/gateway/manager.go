// internal/gateway/manager.go
// Gateway manager — menyimpan dan mengelola gateway multi-platform per agent.

package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Platform types yang didukung
const (
	PlatformTelegram = "telegram"
	PlatformWhatsApp = "whatsapp"
	PlatformHTTP     = "http"
)

// Gateway mewakili satu konfigurasi gateway yang terdaftar
type Gateway struct {
	ID            string            `json:"id"`
	AgentID       string            `json:"agent_id"`
	Platform      string            `json:"platform"`
	Label         string            `json:"label"`
	Config        map[string]string `json:"config"`
	Active        bool              `json:"active"`
	CreatedAt     time.Time         `json:"created_at"`
	ExpiresAt     *time.Time        `json:"expires_at,omitempty"`
	WebhookPath   string            `json:"webhook_path,omitempty"`
	WebhookSecret string            `json:"webhook_secret"` // HMAC-SHA256 signing secret
}

type SecretStore interface {
	LoadGatewaySecret(id string) (webhookSecret string, config map[string]string, ok bool)
	SaveGatewaySecret(id, webhookSecret string, config map[string]string) error
	DeleteGatewaySecret(id string) error
}

// WebhookURL menghasilkan URL publik webhook untuk gateway ini
func (g *Gateway) WebhookURL(baseURL string) string {
	if g.WebhookPath != "" {
		return fmt.Sprintf("%s/api/webhook/%s/%s/%s", baseURL, g.AgentID, g.Platform, g.WebhookPath)
	}
	return fmt.Sprintf("%s/api/webhook/%s/%s", baseURL, g.AgentID, g.Platform)
}

func (g *Gateway) Expired(now time.Time) bool {
	return g.ExpiresAt != nil && !g.ExpiresAt.IsZero() && now.After(*g.ExpiresAt)
}

// Manager handles all gateway state
type Manager struct {
	mu         sync.RWMutex
	gateways   map[string]*Gateway
	storePath  string
	gatewayURL string // OpenClaw/LLM gateway URL for routing
	secrets    SecretStore
}

func NewManager(credentialsDir, gatewayURL string, secretStore SecretStore) *Manager {
	m := &Manager{
		gateways:   make(map[string]*Gateway),
		storePath:  filepath.Join(credentialsDir, "gateways.json"),
		gatewayURL: gatewayURL,
		secrets:    secretStore,
	}
	m.load()
	return m
}

func (m *Manager) load() {
	data, err := os.ReadFile(m.storePath)
	if err != nil {
		return // file doesn't exist yet, that's fine
	}
	var gateways []*Gateway
	if err := json.Unmarshal(data, &gateways); err != nil {
		return
	}
	migrated := false
	for _, g := range gateways {
		if g.Config == nil {
			g.Config = map[string]string{}
		}
		if m.secrets != nil {
			if secret, cfg, ok := m.secrets.LoadGatewaySecret(g.ID); ok {
				if secret != "" {
					g.WebhookSecret = secret
				}
				for k, v := range cfg {
					g.Config[k] = v
				}
				if hasSensitiveConfig(g.Config) {
					_ = m.saveGatewaySecrets(g)
					migrated = true
				}
			} else if g.WebhookSecret != "" || hasSensitiveConfig(g.Config) {
				_ = m.saveGatewaySecrets(g)
				migrated = true
			}
		}
		if g.WebhookSecret == "" {
			g.WebhookSecret = generateWebhookSecret()
			if m.secrets != nil {
				_ = m.saveGatewaySecrets(g)
				migrated = true
			}
		}
		m.gateways[g.ID] = g
	}
	if migrated {
		_ = m.save()
	}
}

func (m *Manager) save() error {
	var list []*Gateway
	for _, g := range m.gateways {
		if err := m.saveGatewaySecrets(g); err != nil {
			return err
		}
		list = append(list, scrubGatewayForDisk(g))
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := m.storePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmpPath, m.storePath)
}

func (m *Manager) saveGatewaySecrets(g *Gateway) error {
	if m.secrets == nil || g == nil {
		return nil
	}
	return m.secrets.SaveGatewaySecret(g.ID, g.WebhookSecret, sensitiveConfig(g.Config))
}

// List returns all gateways, optionally filtered by agentID
func (m *Manager) List(agentID string) []*Gateway {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Gateway
	for _, g := range m.gateways {
		if agentID == "" || g.AgentID == agentID {
			result = append(result, g)
		}
	}
	return result
}

func (m *Manager) ActiveByAgentPlatform(agentID, platform string) (*Gateway, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	now := time.Now()
	for _, g := range m.gateways {
		if g.AgentID == agentID && g.Platform == platform && g.Active && !g.Expired(now) {
			return g, true
		}
	}
	return nil, false
}

// generateWebhookSecret membuat HMAC signing secret 32-byte acak (crypto-safe)
func generateWebhookSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// fallback ke uuid jika rand gagal (tidak normal)
		return uuid.New().String() + uuid.New().String()
	}
	return hex.EncodeToString(b)
}

func GenerateWebhookPath() string {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return uuid.New().String() + uuid.New().String()[:8]
	}
	return hex.EncodeToString(b)
}

// Add creates and persists a new gateway
func (m *Manager) Add(agentID, platform, label string, config map[string]string, expiresAt *time.Time) (*Gateway, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if platform == PlatformHTTP {
		now := time.Now()
		for _, existing := range m.gateways {
			if existing.AgentID == agentID && existing.Platform == PlatformHTTP && existing.Active && !existing.Expired(now) {
				return nil, fmt.Errorf("agent sudah punya HTTP gateway aktif: %s", existing.ID)
			}
		}
	}

	g := &Gateway{
		ID:            uuid.New().String()[:8],
		AgentID:       agentID,
		Platform:      platform,
		Label:         label,
		Config:        config,
		Active:        true,
		CreatedAt:     time.Now(),
		ExpiresAt:     expiresAt,
		WebhookPath:   GenerateWebhookPath(),
		WebhookSecret: generateWebhookSecret(),
	}
	m.gateways[g.ID] = g
	return g, m.save()
}

// Delete removes a gateway
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.gateways[id]; !ok {
		return fmt.Errorf("gateway %s tidak ditemukan", id)
	}
	delete(m.gateways, id)
	if m.secrets != nil {
		if err := m.secrets.DeleteGatewaySecret(id); err != nil {
			return err
		}
	}
	return m.save()
}

// Get returns a specific gateway
func (m *Manager) Get(id string) (*Gateway, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	g, ok := m.gateways[id]
	return g, ok
}

func (m *Manager) FindActive(agentID, platform string) (*Gateway, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, g := range m.gateways {
		if g.AgentID == agentID && g.Platform == platform && g.Active && g.WebhookPath == "" && !g.Expired(time.Now()) {
			return g, true
		}
	}
	return nil, false
}

func (m *Manager) FindActiveByPath(agentID, platform, webhookPath string) (*Gateway, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	now := time.Now()
	for _, g := range m.gateways {
		if g.AgentID == agentID && g.Platform == platform && g.WebhookPath == webhookPath && g.Active && !g.Expired(now) {
			return g, true
		}
	}
	return nil, false
}

func (m *Manager) FindByPath(agentID, platform, webhookPath string) (*Gateway, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, g := range m.gateways {
		if g.AgentID == agentID && g.Platform == platform && g.WebhookPath == webhookPath {
			return g, true
		}
	}
	return nil, false
}

// GetGatewayURL returns the LLM gateway URL
func (m *Manager) GetGatewayURL() string {
	return m.gatewayURL
}

func scrubGatewayForDisk(g *Gateway) *Gateway {
	clone := *g
	clone.Config = map[string]string{}
	for k, v := range g.Config {
		if isSensitiveConfigKey(k) {
			continue
		}
		clone.Config[k] = v
	}
	clone.WebhookSecret = ""
	return &clone
}

func sensitiveConfig(config map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range config {
		if isSensitiveConfigKey(k) {
			out[k] = v
		}
	}
	return out
}

func hasSensitiveConfig(config map[string]string) bool {
	for k, v := range config {
		if isSensitiveConfigKey(k) && v != "" {
			return true
		}
	}
	return false
}

func isSensitiveConfigKey(key string) bool {
	switch key {
	case "access_token", "auth_token", "api_key", "app_secret", "verify_token", "webhook_secret", "gateway_token", "password", "secret":
		return true
	default:
		return false
	}
}
