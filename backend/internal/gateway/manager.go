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
	WebhookSecret string            `json:"webhook_secret"` // HMAC-SHA256 signing secret
}

// WebhookURL menghasilkan URL publik webhook untuk gateway ini
func (g *Gateway) WebhookURL(baseURL string) string {
	return fmt.Sprintf("%s/api/webhook/%s/%s", baseURL, g.AgentID, g.Platform)
}

// Manager handles all gateway state
type Manager struct {
	mu         sync.RWMutex
	gateways   map[string]*Gateway
	storePath  string
	gatewayURL string // OpenClaw/LLM gateway URL for routing
}

func NewManager(credentialsDir, gatewayURL string) *Manager {
	m := &Manager{
		gateways:   make(map[string]*Gateway),
		storePath:  filepath.Join(credentialsDir, "gateways.json"),
		gatewayURL: gatewayURL,
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
	for _, g := range gateways {
		m.gateways[g.ID] = g
	}
}

func (m *Manager) save() error {
	var list []*Gateway
	for _, g := range m.gateways {
		list = append(list, g)
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

// generateWebhookSecret membuat HMAC signing secret 32-byte acak (crypto-safe)
func generateWebhookSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// fallback ke uuid jika rand gagal (tidak normal)
		return uuid.New().String() + uuid.New().String()
	}
	return hex.EncodeToString(b)
}

// Add creates and persists a new gateway
func (m *Manager) Add(agentID, platform, label string, config map[string]string) (*Gateway, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	g := &Gateway{
		ID:            uuid.New().String()[:8],
		AgentID:       agentID,
		Platform:      platform,
		Label:         label,
		Config:        config,
		Active:        true,
		CreatedAt:     time.Now(),
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
		if g.AgentID == agentID && g.Platform == platform && g.Active {
			return g, true
		}
	}
	return nil, false
}

// GetGatewayURL returns the LLM gateway URL
func (m *Manager) GetGatewayURL() string {
	return m.gatewayURL
}
