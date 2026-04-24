// internal/config/loader.go
// Load semua credential dari credentials/ folder.
// Tidak ada hardcode credential di sini.

package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config adalah konfigurasi global platform.
type Config struct {
	// OpenClaw Gateway
	GatewayURL   string
	GatewayToken string

	// API Server
	APIPort string
	APIHost string

	// AI and Storage Connections (Local/Cloud defaults)
	OllamaDirectURL string
	OllamaEmbedURL  string
	PgVectorURL     string


	// Auth
	DashboardPassword string

	// Paths
	NodeBin              string
	OpenClawWorkspaceDir string
	BotsDir              string
	CredentialsDir       string

	// CORS
	FrontendOrigin string
}

// AgentCred adalah credential per agent.
type AgentCred struct {
	ID               string
	Label            string
	TelegramBotToken string
	OpenClawModel    string
	ProviderAPIKey   string // Optional: API key untuk provider external langsung
	ProviderName     string // Optional: nama human-readable provider
}

// Load membaca .env dari direktori credentials.
func Load(credentialsDir string) (*Config, error) {
	envPath := filepath.Join(credentialsDir, ".env")
	envMap, err := parseEnvFile(envPath)
	if err != nil {
		return nil, fmt.Errorf("gagal baca %s: %w", envPath, err)
	}

	cfg := &Config{
		GatewayURL:           getEnv(envMap, "OPENCLAW_GW_URL", "http://127.0.0.1:14000/v1/chat/completions"),
		GatewayToken:         getEnv(envMap, "OPENCLAW_GW_TOKEN", ""),
		APIPort:              getEnv(envMap, "API_PORT", "8080"),
		APIHost:              getEnv(envMap, "API_HOST", "127.0.0.1"),
		OllamaDirectURL:      getEnv(envMap, "OLLAMA_DIRECT_URL", "http://127.0.0.1:11434/api/chat"),
		OllamaEmbedURL:       getEnv(envMap, "OLLAMA_EMBED_URL", "http://127.0.0.1:11434"),
		PgVectorURL:          getEnv(envMap, "OPENCLAW_PGVECTOR_URL", "postgresql://lenvo_alma@127.0.0.1/openclaw"),
		DashboardPassword:    getEnv(envMap, "DASHBOARD_PASSWORD", "admin"),
		NodeBin:              getEnv(envMap, "NODE_BIN", "node"),
		OpenClawWorkspaceDir: getEnv(envMap, "OPENCLAW_WORKSPACE_DIR", os.ExpandEnv("$HOME/.openclaw")),
		BotsDir:              getEnv(envMap, "BOTS_DIR", ""),
		CredentialsDir:       credentialsDir,
		FrontendOrigin:       getEnv(envMap, "FRONTEND_ORIGIN", "http://localhost:5173"),
	}
	return cfg, nil
}

// WriteAgentCred menulis file .env untuk agent baru ke credentials/agents/<id>.env
func WriteAgentCred(credentialsDir string, cred *AgentCred) error {
	agentsDir := filepath.Join(credentialsDir, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		return fmt.Errorf("gagal buat direktori agents: %w", err)
	}

	// Security Enhancement: Strip newlines to prevent Env Injection Attacks
	sanitizeEnv := func(val string) string {
		val = strings.ReplaceAll(val, "\n", " ")
		val = strings.ReplaceAll(val, "\r", " ")
		return strings.TrimSpace(val)
	}

	envPath := filepath.Join(agentsDir, cred.ID+".env")
	content := fmt.Sprintf(`# Agent: %s
AGENT_ID=%s
AGENT_LABEL=%s
TELEGRAM_BOT_TOKEN=%s
OPENCLAW_MODEL=%s
PROVIDER_API_KEY=%s
PROVIDER_NAME=%s
`,
		sanitizeEnv(cred.Label), 
		sanitizeEnv(cred.ID), 
		sanitizeEnv(cred.Label), 
		sanitizeEnv(cred.TelegramBotToken), 
		sanitizeEnv(cred.OpenClawModel), 
		sanitizeEnv(cred.ProviderAPIKey), 
		sanitizeEnv(cred.ProviderName))

	return os.WriteFile(envPath, []byte(content), 0600)
}

// DeleteAgentCred menghapus file .env agent dari credentials/agents/<id>.env
func DeleteAgentCred(credentialsDir, agentID string) error {
	envPath := filepath.Join(credentialsDir, "agents", agentID+".env")
	return os.Remove(envPath)
}

// LoadAgentCreds membaca semua file .env di credentials/agents/
func LoadAgentCreds(credentialsDir string) (map[string]*AgentCred, error) {
	agentsDir := filepath.Join(credentialsDir, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return nil, fmt.Errorf("gagal baca direktori agents: %w", err)
	}

	creds := make(map[string]*AgentCred)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".env") {
			continue
		}

		agentID := strings.TrimSuffix(entry.Name(), ".env")
		envPath := filepath.Join(agentsDir, entry.Name())
		envMap, err := parseEnvFile(envPath)
		if err != nil {
			fmt.Printf("⚠️  Gagal baca %s: %v\n", envPath, err)
			continue
		}

		creds[agentID] = &AgentCred{
			ID:               getEnv(envMap, "AGENT_ID", agentID),
			Label:            getEnv(envMap, "AGENT_LABEL", agentID),
			TelegramBotToken: getEnv(envMap, "TELEGRAM_BOT_TOKEN", ""),
			OpenClawModel:    getEnv(envMap, "OPENCLAW_MODEL", "openclaw/"+agentID),
			ProviderAPIKey:   getEnv(envMap, "PROVIDER_API_KEY", ""),
			ProviderName:     getEnv(envMap, "PROVIDER_NAME", ""),
		}
	}

	return creds, nil
}

// parseEnvFile membaca file .env ke map key→value.
// Mendukung komentar (#) dan baris kosong.
func parseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	result := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Hapus quote opsional
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') ||
			(val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		result[key] = val
	}

	return result, scanner.Err()
}

func getEnv(m map[string]string, key, fallback string) string {
	if v, ok := m[key]; ok && v != "" {
		return v
	}
	// Juga cek environment OS
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
