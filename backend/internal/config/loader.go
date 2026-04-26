// internal/config/loader.go
// Load semua credential dari credentials/ folder.
// Tidak ada hardcode credential di sini.

package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

	// Security Enhancement
	MaskingMode                 string
	LoggingMode                 string
	TemplateRenderingEnabled    bool
	SecurityStoreDir            string
	SecurityStoreKey            string
	AgentToolsEnabled           bool
	GatewayTrustedProxyCIDRs    string
	GatewayAllowCIDRs           string
	GatewayDenyCIDRs            string
	GatewayGeoIPDBPath          string
	GatewayGeoIPAPIEnabled      bool
	GatewayGeoIPAPIURLs         string
	GatewayGeoIPAPITimeoutMS    int
	GatewayVisitorStore         bool
	GatewayVisitorDNSLookup     bool
	GatewayVisitorRetentionDays int
	GatewayAbuseDefenseEnabled  bool
	GatewayAbuseThrottleScore   int
	GatewayAbuseBlockScore      int

	// Downstream AI router (9Router / OmniRoute / custom OpenAI-compatible)
	AIRouterEnabled          bool
	AIRouterProvider         string
	AIRouterBaseURL          string
	AIRouterToken            string
	AIRouterRequireLoopback  bool
	AIRouterAllowCloud       bool
	AIRouterModelAllowlist   string
	AIRouterTimeoutMS        int
	AIRouterMaxResponseBytes int
	AIRouterAllowToolCalls   bool
	AIRouterListenHost       string
	AIRouter9RouterPort      int
	AIRouterOmniRoutePort    int

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
		GatewayURL:                  getEnv(envMap, "OPENCLAW_GW_URL", "http://127.0.0.1:14000/v1/chat/completions"),
		GatewayToken:                getEnv(envMap, "OPENCLAW_GW_TOKEN", ""),
		APIPort:                     getEnv(envMap, "API_PORT", "8080"),
		APIHost:                     getEnv(envMap, "API_HOST", "127.0.0.1"),
		OllamaDirectURL:             getEnv(envMap, "OLLAMA_DIRECT_URL", "http://127.0.0.1:11434/api/chat"),
		OllamaEmbedURL:              getEnv(envMap, "OLLAMA_EMBED_URL", "http://127.0.0.1:11434"),
		PgVectorURL:                 getEnv(envMap, "OPENCLAW_PGVECTOR_URL", "postgresql://lenvo_alma@127.0.0.1/openclaw"),
		MaskingMode:                 normalizeChoice(getEnv(envMap, "MASKING_MODE", "on"), []string{"off", "on", "strict"}, "on"),
		LoggingMode:                 normalizeChoice(getEnv(envMap, "LOGGING_MODE", "audit"), []string{"minimal", "audit", "debug"}, "audit"),
		TemplateRenderingEnabled:    parseBool(getEnv(envMap, "TEMPLATE_RENDERING_ENABLED", "true")),
		SecurityStoreDir:            getEnv(envMap, "SECURITY_STORE_DIR", filepath.Join(credentialsDir, "security")),
		SecurityStoreKey:            getEnv(envMap, "SECURITY_STORE_KEY", ""),
		AgentToolsEnabled:           parseBool(getEnv(envMap, "AGENT_TOOLS_ENABLED", "false")),
		GatewayTrustedProxyCIDRs:    getEnv(envMap, "GATEWAY_TRUSTED_PROXY_CIDRS", ""),
		GatewayAllowCIDRs:           getEnv(envMap, "GATEWAY_ALLOW_CIDRS", ""),
		GatewayDenyCIDRs:            getEnv(envMap, "GATEWAY_DENY_CIDRS", ""),
		GatewayGeoIPDBPath:          getEnv(envMap, "GATEWAY_GEOIP_DB_PATH", ""),
		GatewayGeoIPAPIEnabled:      parseBool(getEnv(envMap, "GATEWAY_GEOIP_API_ENABLED", "false")),
		GatewayGeoIPAPIURLs:         getEnv(envMap, "GATEWAY_GEOIP_API_URLS", ""),
		GatewayGeoIPAPITimeoutMS:    parseIntRange(getEnv(envMap, "GATEWAY_GEOIP_API_TIMEOUT_MS", "800"), 100, 5000, 800),
		GatewayVisitorStore:         parseBool(getEnv(envMap, "GATEWAY_VISITOR_STORE", "true")),
		GatewayVisitorDNSLookup:     parseBool(getEnv(envMap, "GATEWAY_VISITOR_DNS_LOOKUP", "false")),
		GatewayVisitorRetentionDays: parseIntRange(getEnv(envMap, "GATEWAY_VISITOR_RETENTION_DAYS", "30"), 0, 3650, 30),
		GatewayAbuseDefenseEnabled:  parseBool(getEnv(envMap, "GATEWAY_ABUSE_DEFENSE_ENABLED", "true")),
		GatewayAbuseThrottleScore:   parseIntRange(getEnv(envMap, "GATEWAY_ABUSE_THROTTLE_SCORE", "35"), 0, 100, 35),
		GatewayAbuseBlockScore:      parseIntRange(getEnv(envMap, "GATEWAY_ABUSE_BLOCK_SCORE", "85"), 0, 100, 85),
		AIRouterEnabled:             parseBool(getEnv(envMap, "AI_ROUTER_ENABLED", "false")),
		AIRouterProvider:            normalizeChoice(getEnv(envMap, "AI_ROUTER_PROVIDER", "openclaw"), []string{"openclaw", "9router", "omniroute", "custom"}, "openclaw"),
		AIRouterBaseURL:             getEnv(envMap, "AI_ROUTER_BASE_URL", "http://127.0.0.1:20128/v1"),
		AIRouterToken:               getEnv(envMap, "AI_ROUTER_TOKEN", ""),
		AIRouterRequireLoopback:     parseBool(getEnv(envMap, "AI_ROUTER_REQUIRE_LOOPBACK", "true")),
		AIRouterAllowCloud:          parseBool(getEnv(envMap, "AI_ROUTER_ALLOW_CLOUD", "false")),
		AIRouterModelAllowlist:      getEnv(envMap, "AI_ROUTER_MODEL_ALLOWLIST", ""),
		AIRouterTimeoutMS:           parseIntRange(getEnv(envMap, "AI_ROUTER_TIMEOUT_MS", "30000"), 1000, 300000, 30000),
		AIRouterMaxResponseBytes:    parseIntRange(getEnv(envMap, "AI_ROUTER_MAX_RESPONSE_BYTES", "2097152"), 65536, 10485760, 2097152),
		AIRouterAllowToolCalls:      parseBool(getEnv(envMap, "AI_ROUTER_ALLOW_TOOL_CALLS", "false")),
		AIRouterListenHost:          getEnv(envMap, "AI_ROUTER_LISTEN_HOST", "127.0.0.1"),
		AIRouter9RouterPort:         parseIntRange(getEnv(envMap, "AI_ROUTER_9ROUTER_PORT", "20128"), 1024, 65535, 20128),
		AIRouterOmniRoutePort:       parseIntRange(getEnv(envMap, "AI_ROUTER_OMNIROUTE_PORT", "20129"), 1024, 65535, 20129),
		DashboardPassword:           getEnv(envMap, "DASHBOARD_PASSWORD", "admin"),
		NodeBin:                     getEnv(envMap, "NODE_BIN", "node"),
		OpenClawWorkspaceDir:        getEnv(envMap, "OPENCLAW_WORKSPACE_DIR", os.ExpandEnv("$HOME/.openclaw")),
		BotsDir:                     getEnv(envMap, "BOTS_DIR", ""),
		CredentialsDir:              credentialsDir,
		FrontendOrigin:              getEnv(envMap, "FRONTEND_ORIGIN", "http://localhost:5173"),
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

func parseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func parseIntRange(v string, min int, max int, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	if n < min || n > max {
		return fallback
	}
	return n
}

func normalizeChoice(v string, allowed []string, fallback string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return fallback
}
