// internal/config/loader.go
// Load semua credential dari credentials/ folder.
// Tidak ada hardcode credential di sini.

package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const maxAgentRegistryBytes int64 = 2 << 20

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
	ModelRouteTarget string // Optional: route_target dari model catalog/picker
	ModelTier        string // Optional: local | cloud_proxied | cloud
	ModelAdapterID   string // Optional: adapter/provider id
	ModelOutbound    string // Optional: outbound model contoh dari catalog
	ModelProviderURL string // Optional: provider base URL non-secret
	RuntimeSupported string // Optional: true/false dari model catalog
}

type AgentRegistryLoadReport struct {
	RegistryPath   string
	UsedRegistry   bool
	UsedLegacy     bool
	MigratedLegacy bool
	CreatedDefault bool
	RegistryError  string
	AgentCount     int
}

type agentRegistryFile struct {
	Version int                           `json:"version"`
	Agents  map[string]agentRegistryEntry `json:"agents"`
}

type agentRegistryEntry struct {
	ID               string              `json:"id"`
	Label            string              `json:"label"`
	TelegramBotToken string              `json:"telegram_bot_token"`
	OpenClawModel    string              `json:"openclaw_model"`
	ProviderAPIKey   string              `json:"provider_api_key,omitempty"`
	ProviderName     string              `json:"provider_name,omitempty"`
	ModelRoute       agentModelRouteJSON `json:"model_route,omitempty"`
}

type agentModelRouteJSON struct {
	RouteTarget      string `json:"route_target,omitempty"`
	Tier             string `json:"tier,omitempty"`
	AdapterID        string `json:"adapter_id,omitempty"`
	OutboundModel    string `json:"outbound_model,omitempty"`
	ProviderBaseURL  string `json:"provider_base_url,omitempty"`
	RuntimeSupported string `json:"runtime_supported,omitempty"`
}

var validAgentIDPattern = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
var configSecretPattern = regexp.MustCompile(`(?i)(authorization|api[_-]?key|access[_-]?token|auth[_-]?token|gateway[_-]?token|password|secret|telegram[_-]?bot[_-]?token)(["'\s:=]+)([^"',\s}]+)`)
var telegramTokenPattern = regexp.MustCompile(`\b\d{6,12}:[A-Za-z0-9_-]{30,}\b`)

func redactConfigSecret(value string) string {
	value = telegramTokenPattern.ReplaceAllString(value, "[REDACTED_TELEGRAM_TOKEN]")
	return configSecretPattern.ReplaceAllString(value, "$1$2[REDACTED]")
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

func AgentRegistryPath(credentialsDir string) string {
	return filepath.Join(credentialsDir, "agents.json")
}

// WriteAgentCred menyimpan credential agent ke credentials/agents.json.
func WriteAgentCred(credentialsDir string, cred *AgentCred) error {
	if cred == nil {
		return fmt.Errorf("credential agent kosong")
	}
	normalized, err := normalizeAgentCred(cred)
	if err != nil {
		return err
	}
	registry, err := readAgentRegistryFile(credentialsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("gagal baca registry agent: %w", err)
		}
		registry = newAgentRegistryFile()
	}
	registry.Agents[normalized.ID] = agentCredToRegistryEntry(normalized)
	return writeAgentRegistryFile(credentialsDir, registry)
}

// DeleteAgentCred menghapus agent dari credentials/agents.json.
func DeleteAgentCred(credentialsDir, agentID string) error {
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if !validAgentIDPattern.MatchString(agentID) {
		return fmt.Errorf("agent id tidak valid")
	}
	registry, err := readAgentRegistryFile(credentialsDir)
	if err != nil {
		return err
	}
	delete(registry.Agents, agentID)
	return writeAgentRegistryFile(credentialsDir, registry)
}

// LoadAgentCreds membaca credentials/agents.json dengan fallback legacy .env.
func LoadAgentCreds(credentialsDir string) (map[string]*AgentCred, error) {
	creds, _, err := LoadAgentCredsWithReport(credentialsDir)
	return creds, err
}

func LoadAgentCredsWithReport(credentialsDir string) (map[string]*AgentCred, AgentRegistryLoadReport, error) {
	report := AgentRegistryLoadReport{RegistryPath: AgentRegistryPath(credentialsDir)}
	registry, err := readAgentRegistryFile(credentialsDir)
	if err == nil {
		creds := registryToAgentCreds(registry)
		report.UsedRegistry = true
		report.AgentCount = len(creds)
		return creds, report, nil
	}

	if err != nil && !os.IsNotExist(err) {
		report.RegistryError = redactConfigSecret(err.Error())
		fmt.Printf("⚠️  Gagal baca registry agent %s: %v. Fallback ke legacy .env.\n", report.RegistryPath, redactConfigSecret(err.Error()))
	}

	legacyCreds, legacyErr := loadLegacyAgentCreds(credentialsDir)
	if legacyErr != nil && !os.IsNotExist(legacyErr) {
		return nil, report, legacyErr
	}
	if len(legacyCreds) > 0 {
		report.UsedLegacy = true
		report.AgentCount = len(legacyCreds)
		if os.IsNotExist(err) {
			migratedRegistry := agentCredsToRegistry(legacyCreds)
			if writeErr := writeAgentRegistryFile(credentialsDir, migratedRegistry); writeErr != nil {
				return legacyCreds, report, fmt.Errorf("gagal migrasi legacy agent credentials: %w", writeErr)
			}
			report.MigratedLegacy = true
			report.UsedRegistry = true
			return registryToAgentCreds(migratedRegistry), report, nil
		}
		return legacyCreds, report, nil
	}

	if os.IsNotExist(err) {
		mainCred := &AgentCred{ID: "main", Label: "main", OpenClawModel: "openclaw/main"}
		defaultCreds := map[string]*AgentCred{"main": mainCred}
		if writeErr := writeAgentRegistryFile(credentialsDir, agentCredsToRegistry(defaultCreds)); writeErr != nil {
			return nil, report, fmt.Errorf("gagal buat registry default: %w", writeErr)
		}
		report.CreatedDefault = true
		report.UsedRegistry = true
		report.AgentCount = 1
		return defaultCreds, report, nil
	}

	return map[string]*AgentCred{}, report, nil
}

func loadLegacyAgentCreds(credentialsDir string) (map[string]*AgentCred, error) {
	agentsDir := filepath.Join(credentialsDir, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, err
		}
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
			fmt.Printf("⚠️  Gagal baca %s: %v\n", envPath, redactConfigSecret(err.Error()))
			continue
		}

		cred, err := normalizeAgentCred(&AgentCred{
			ID:               getEnv(envMap, "AGENT_ID", agentID),
			Label:            getEnv(envMap, "AGENT_LABEL", agentID),
			TelegramBotToken: getEnv(envMap, "TELEGRAM_BOT_TOKEN", ""),
			OpenClawModel:    getEnv(envMap, "OPENCLAW_MODEL", "openclaw/"+agentID),
			ProviderAPIKey:   getEnv(envMap, "PROVIDER_API_KEY", ""),
			ProviderName:     getEnv(envMap, "PROVIDER_NAME", ""),
			ModelRouteTarget: getEnv(envMap, "MODEL_ROUTE_TARGET", ""),
			ModelTier:        getEnv(envMap, "MODEL_TIER", ""),
			ModelAdapterID:   getEnv(envMap, "MODEL_ADAPTER_ID", ""),
			ModelOutbound:    getEnv(envMap, "MODEL_OUTBOUND", ""),
			ModelProviderURL: getEnv(envMap, "MODEL_PROVIDER_BASE_URL", ""),
			RuntimeSupported: getEnv(envMap, "MODEL_RUNTIME_SUPPORTED", ""),
		})
		if err != nil {
			fmt.Printf("⚠️  Skip legacy agent %s: %v\n", envPath, redactConfigSecret(err.Error()))
			continue
		}
		creds[cred.ID] = cred
	}

	return creds, nil
}

func newAgentRegistryFile() *agentRegistryFile {
	return &agentRegistryFile{Version: 1, Agents: map[string]agentRegistryEntry{}}
}

func readAgentRegistryFile(credentialsDir string) (*agentRegistryFile, error) {
	path := AgentRegistryPath(credentialsDir)
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("agent registry bukan regular file")
	}
	if st.Size() > maxAgentRegistryBytes {
		return nil, fmt.Errorf("agent registry terlalu besar")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxAgentRegistryBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxAgentRegistryBytes {
		return nil, fmt.Errorf("agent registry terlalu besar")
	}
	var registry agentRegistryFile
	if err := json.Unmarshal(data, &registry); err != nil {
		return nil, err
	}
	if registry.Version == 0 {
		registry.Version = 1
	}
	if registry.Agents == nil {
		registry.Agents = map[string]agentRegistryEntry{}
	}
	return &registry, nil
}

func writeAgentRegistryFile(credentialsDir string, registry *agentRegistryFile) error {
	if registry == nil {
		registry = newAgentRegistryFile()
	}
	if registry.Version == 0 {
		registry.Version = 1
	}
	if registry.Agents == nil {
		registry.Agents = map[string]agentRegistryEntry{}
	}
	if err := os.MkdirAll(credentialsDir, 0700); err != nil {
		return fmt.Errorf("gagal buat direktori credentials: %w", err)
	}
	if st, err := os.Lstat(credentialsDir); err != nil {
		return err
	} else if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return fmt.Errorf("credentials dir tidak valid")
	}
	if err := os.Chmod(credentialsDir, 0700); err != nil {
		return fmt.Errorf("gagal set permission credentials: %w", err)
	}

	clean := newAgentRegistryFile()
	for id, entry := range registry.Agents {
		cred := registryEntryToAgentCred(id, entry)
		normalized, err := normalizeAgentCred(cred)
		if err != nil {
			return err
		}
		clean.Agents[normalized.ID] = agentCredToRegistryEntry(normalized)
	}

	data, err := json.MarshalIndent(clean, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(credentialsDir, ".agents.json.tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, AgentRegistryPath(credentialsDir))
}

func agentCredsToRegistry(creds map[string]*AgentCred) *agentRegistryFile {
	registry := newAgentRegistryFile()
	for id, cred := range creds {
		if cred == nil {
			continue
		}
		if strings.TrimSpace(cred.ID) == "" {
			cred.ID = id
		}
		if normalized, err := normalizeAgentCred(cred); err == nil {
			registry.Agents[normalized.ID] = agentCredToRegistryEntry(normalized)
		}
	}
	return registry
}

func registryToAgentCreds(registry *agentRegistryFile) map[string]*AgentCred {
	creds := map[string]*AgentCred{}
	if registry == nil {
		return creds
	}
	for id, entry := range registry.Agents {
		cred := registryEntryToAgentCred(id, entry)
		normalized, err := normalizeAgentCred(cred)
		if err != nil {
			fmt.Printf("⚠️  Skip agent registry entry %s: %v\n", id, redactConfigSecret(err.Error()))
			continue
		}
		creds[normalized.ID] = normalized
	}
	return creds
}

func registryEntryToAgentCred(id string, entry agentRegistryEntry) *AgentCred {
	agentID := strings.TrimSpace(entry.ID)
	if agentID == "" {
		agentID = id
	}
	return &AgentCred{
		ID:               agentID,
		Label:            entry.Label,
		TelegramBotToken: entry.TelegramBotToken,
		OpenClawModel:    entry.OpenClawModel,
		ProviderAPIKey:   entry.ProviderAPIKey,
		ProviderName:     entry.ProviderName,
		ModelRouteTarget: entry.ModelRoute.RouteTarget,
		ModelTier:        entry.ModelRoute.Tier,
		ModelAdapterID:   entry.ModelRoute.AdapterID,
		ModelOutbound:    entry.ModelRoute.OutboundModel,
		ModelProviderURL: entry.ModelRoute.ProviderBaseURL,
		RuntimeSupported: entry.ModelRoute.RuntimeSupported,
	}
}

func agentCredToRegistryEntry(cred *AgentCred) agentRegistryEntry {
	return agentRegistryEntry{
		ID:               cred.ID,
		Label:            cred.Label,
		TelegramBotToken: cred.TelegramBotToken,
		OpenClawModel:    cred.OpenClawModel,
		ProviderAPIKey:   cred.ProviderAPIKey,
		ProviderName:     cred.ProviderName,
		ModelRoute: agentModelRouteJSON{
			RouteTarget:      cred.ModelRouteTarget,
			Tier:             cred.ModelTier,
			AdapterID:        cred.ModelAdapterID,
			OutboundModel:    cred.ModelOutbound,
			ProviderBaseURL:  cred.ModelProviderURL,
			RuntimeSupported: cred.RuntimeSupported,
		},
	}
}

func normalizeAgentCred(cred *AgentCred) (*AgentCred, error) {
	if cred == nil {
		return nil, fmt.Errorf("credential agent kosong")
	}
	clean := *cred
	clean.ID = strings.ToLower(sanitizeCredentialString(clean.ID))
	if !validAgentIDPattern.MatchString(clean.ID) {
		return nil, fmt.Errorf("agent id tidak valid")
	}
	clean.Label = sanitizeCredentialString(clean.Label)
	if clean.Label == "" {
		clean.Label = clean.ID
	}
	clean.TelegramBotToken = sanitizeCredentialString(clean.TelegramBotToken)
	clean.OpenClawModel = sanitizeCredentialString(clean.OpenClawModel)
	if clean.OpenClawModel == "" {
		clean.OpenClawModel = "openclaw/" + clean.ID
	}
	clean.ProviderAPIKey = sanitizeCredentialString(clean.ProviderAPIKey)
	clean.ProviderName = sanitizeCredentialString(clean.ProviderName)
	clean.ModelRouteTarget = sanitizeCredentialString(clean.ModelRouteTarget)
	clean.ModelTier = sanitizeCredentialString(clean.ModelTier)
	clean.ModelAdapterID = sanitizeCredentialString(clean.ModelAdapterID)
	clean.ModelOutbound = sanitizeCredentialString(clean.ModelOutbound)
	clean.ModelProviderURL = sanitizeCredentialString(clean.ModelProviderURL)
	clean.RuntimeSupported = sanitizeCredentialString(clean.RuntimeSupported)
	return &clean, nil
}

func sanitizeCredentialString(val string) string {
	val = strings.ReplaceAll(val, "\n", " ")
	val = strings.ReplaceAll(val, "\r", " ")
	val = strings.ReplaceAll(val, "\x00", "")
	return strings.TrimSpace(val)
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
