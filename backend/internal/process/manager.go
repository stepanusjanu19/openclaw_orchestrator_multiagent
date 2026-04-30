// internal/process/manager.go
// Process pool untuk spawn/stop/monitor Node.js bot runners.
// Go mengelola lifecycle setiap agent sebagai subprocess.

package process

import (
	"bufio"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"multiagent-platform/internal/config"
	"multiagent-platform/internal/routerruntime"
)

var validRuntimeAgentID = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// AgentProcess mewakili satu bot runner yang sedang berjalan.
type AgentProcess struct {
	ID                string
	Cmd               *exec.Cmd
	LogCh             chan string // channel untuk streaming log
	IngressURL        string
	StartedAt         time.Time
	RuntimeSourcePath string
	mu                sync.Mutex
	running           bool
}

// Manager mengelola semua agent processes.
type Manager struct {
	mu            sync.RWMutex
	agents        map[string]*AgentProcess
	cfg           *config.Config
	internalToken string
	toolToken     string
	backendURL    string
}

// AgentConfig adalah konfigurasi untuk spawn agent process.
type AgentConfig struct {
	ID               string
	Label            string
	TelegramBotToken string
	OpenClawModel    string
	GatewayURL       string
	GatewayToken     string
	BotsDir          string
	ConfigsDir       string
	SoulMDPath       string // Explicit exact path to SOUL.md
	ProviderAPIKey   string // Optional: provider API key (openrouter, gemini, dst)
	ProviderName     string // Optional: human-readable provider name untuk error
	ModelRouteTarget string // Optional: route target dari catalog model
	ModelTier        string // Optional: local/cloud/cloud_proxied
	ModelAdapterID   string // Optional: adapter id dari catalog model
	ModelOutbound    string // Optional: outbound model dari catalog model
	ModelProviderURL string // Optional: provider base URL non-secret
	RuntimeSupported string // Optional: true/false dari catalog model
}

// AgentStatus adalah status agent yang di-return ke API.
type AgentStatus struct {
	ID                 string    `json:"id"`
	Label              string    `json:"label"`
	Running            bool      `json:"running"`
	StartedAt          time.Time `json:"started_at,omitempty"`
	PID                int       `json:"pid,omitempty"`
	RuntimeSourceMTime time.Time `json:"runtime_source_mtime,omitempty"`
	RuntimeStale       bool      `json:"runtime_stale,omitempty"`
	RuntimeWarning     string    `json:"runtime_warning,omitempty"`
}

// NewManager membuat instance Manager untuk process node.
func NewManager(cfg *config.Config, internalToken, backendURL, toolToken string) *Manager {
	if cfg.NodeBin == "" {
		cfg.NodeBin = "node"
	}
	// Clean up any orphaned node bot processes to avoid polling conflicts.
	// Gunakan argv statis tanpa shell untuk menghindari command injection surface.
	exec.Command("pkill", "-f", `bots/shared/runner.js`).Run()

	return &Manager{
		agents:        make(map[string]*AgentProcess),
		cfg:           cfg,
		internalToken: internalToken,
		toolToken:     toolToken,
		backendURL:    backendURL,
	}
}

// resolveDirectModel menentukan model yang dipakai untuk Direct Ollama Bypass.
// Aturan:
//   - Model CLOUD (mengandung ':cloud', misalnya 'kimi-k2.5:cloud') → return "" (bypass dinonaktifkan)
//   - Model LOKAL (misal 'ollama/llama3.1:8b') → return model mentah tanpa prefix
//   - Model lain (openrouter, custom) → return "" (bypass dinonaktifkan, biarkan Gateway menangani)
func resolveDirectModel(openClawModel string) string {
	trimmed := strings.TrimSpace(openClawModel)
	provider, model, ok := strings.Cut(trimmed, "/")
	if !ok {
		return ""
	}
	if strings.ToLower(strings.TrimSpace(provider)) != "ollama" {
		return ""
	}
	model = strings.TrimSpace(model)
	if model == "" || strings.HasSuffix(strings.ToLower(model), ":cloud") {
		return ""
	}
	return model
}

func ingressPortForAgent(agentID string) int {
	// 19000-28999: deterministic, localhost-only, avoids per-process negotiation.
	return 19000 + int(crc32.ChecksumIEEE([]byte(agentID))%10000)
}

func runtimeSourcePath(botsDir string) string {
	return filepath.Join(botsDir, "shared", "agent-engine.js")
}

func runtimeSourceMTime(path string) time.Time {
	stat, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return stat.ModTime()
}

func secureRunnerPath(botsDir string) (string, error) {
	if strings.TrimSpace(botsDir) == "" {
		return "", fmt.Errorf("BOTS_DIR kosong")
	}
	botsAbs, err := filepath.Abs(botsDir)
	if err != nil {
		return "", err
	}
	entryPoint := filepath.Join(botsAbs, "shared", "runner.js")
	entryAbs, err := filepath.Abs(entryPoint)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(botsAbs, entryAbs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("runner path keluar dari BOTS_DIR")
	}
	st, err := os.Lstat(entryAbs)
	if err != nil {
		return "", fmt.Errorf("entry point tidak ditemukan: %s", entryAbs)
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
		return "", fmt.Errorf("entry point tidak valid: %s", entryAbs)
	}
	return entryAbs, nil
}

func validateEnvValue(name, value string) error {
	if strings.ContainsAny(value, "\x00\n\r") {
		return fmt.Errorf("%s mengandung karakter tidak valid", name)
	}
	return nil
}

func (m *Manager) aiRouterTokenForAgent() string {
	token := strings.TrimSpace(m.cfg.AIRouterToken)
	if token != "" || !m.cfg.AIRouterEnabled {
		return token
	}
	provider := strings.TrimSpace(m.cfg.AIRouterProvider)
	if provider != "9router" && provider != "omniroute" {
		return ""
	}
	if token, ok := routerruntime.APIKey(provider, ""); ok {
		return token
	}
	return ""
}

// Start spawn proses bot untuk agent tertentu.
func (m *Manager) Start(cfg AgentConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ap, exists := m.agents[cfg.ID]; exists && ap.running {
		return fmt.Errorf("agent %s sudah berjalan (PID %d)", cfg.ID, ap.Cmd.Process.Pid)
	}
	if !validRuntimeAgentID.MatchString(cfg.ID) {
		return fmt.Errorf("agent id tidak valid")
	}
	if err := validateEnvValue("AGENT_LABEL", cfg.Label); err != nil {
		return err
	}
	if err := validateEnvValue("OPENCLAW_MODEL", cfg.OpenClawModel); err != nil {
		return err
	}

	entryPoint, err := secureRunnerPath(cfg.BotsDir)
	if err != nil {
		return err
	}

	env := os.Environ()
	ingressPort := ingressPortForAgent(cfg.ID)
	aiRouterToken := m.aiRouterTokenForAgent()
	env = append(env,
		"TELEGRAM_BOT_TOKEN="+cfg.TelegramBotToken,
		"OPENCLAW_GW_URL="+cfg.GatewayURL,
		"OPENCLAW_GW_TOKEN="+cfg.GatewayToken,
		fmt.Sprintf("AI_ROUTER_ENABLED=%t", m.cfg.AIRouterEnabled),
		"AI_ROUTER_PROVIDER="+m.cfg.AIRouterProvider,
		"AI_ROUTER_BASE_URL="+m.cfg.AIRouterBaseURL,
		"AI_ROUTER_TOKEN="+aiRouterToken,
		fmt.Sprintf("AI_ROUTER_REQUIRE_LOOPBACK=%t", m.cfg.AIRouterRequireLoopback),
		fmt.Sprintf("AI_ROUTER_ALLOW_CLOUD=%t", m.cfg.AIRouterAllowCloud),
		"AI_ROUTER_MODEL_ALLOWLIST="+m.cfg.AIRouterModelAllowlist,
		fmt.Sprintf("AI_ROUTER_TIMEOUT_MS=%d", m.cfg.AIRouterTimeoutMS),
		fmt.Sprintf("AI_ROUTER_MAX_RESPONSE_BYTES=%d", m.cfg.AIRouterMaxResponseBytes),
		fmt.Sprintf("AI_ROUTER_ALLOW_TOOL_CALLS=%t", m.cfg.AIRouterAllowToolCalls),
		"OPENCLAW_MODEL="+cfg.OpenClawModel,
		"NODE_WORKSPACE_PROMPT=true",
		"AGENT_ID="+cfg.ID,
		"AGENT_LABEL="+cfg.Label,
		"SOUL_MD_PATH="+cfg.SoulMDPath,
		"OPENCLAW_INTERNAL_TOKEN="+m.internalToken,
		"OPENCLAW_BACKEND_URL="+m.backendURL,
		"PROVIDER_API_KEY="+cfg.ProviderAPIKey,
		"PROVIDER_NAME="+cfg.ProviderName,
		"MODEL_ROUTE_TARGET="+cfg.ModelRouteTarget,
		"MODEL_TIER="+cfg.ModelTier,
		"MODEL_ADAPTER_ID="+cfg.ModelAdapterID,
		"MODEL_OUTBOUND="+cfg.ModelOutbound,
		"MODEL_PROVIDER_BASE_URL="+cfg.ModelProviderURL,
		"MODEL_RUNTIME_SUPPORTED="+cfg.RuntimeSupported,
		"AGENT_INGRESS_HOST=127.0.0.1",
		fmt.Sprintf("AGENT_INGRESS_PORT=%d", ingressPort),
		// AGENT_DIRECT_MODEL: hanya diisi untuk model LOKAL Ollama.
		// Model cloud (mengandung ':cloud') TIDAK boleh di-bypass ke Ollama direct —
		// mereka wajib melewati Gateway OpenClaw sebagai jembatan ke server cloud.
		// Jika diisi kosong, base-bot.js akan menonaktifkan semua tier bypass.
		"AGENT_DIRECT_MODEL="+resolveDirectModel(cfg.OpenClawModel),
		"OLLAMA_DIRECT_URL="+m.cfg.OllamaDirectURL,
		// ── Cognitive Persistence Memory (pgvector + nomic-embed-text) ────────────
		// Variabel ini dikonsumsi oleh bots/shared/memory.js.
		// Terhubung ke Postgres lokal yang sudah ter-setup dengan ekstensi pgvector.
		// Jika Postgres tidak berjalan, agent tetap merespons (degradasi halus).
		"OPENCLAW_PGVECTOR_URL="+m.cfg.PgVectorURL,
		"OLLAMA_EMBED_URL="+m.cfg.OllamaEmbedURL,
		"MEMORY_ENABLED=true",
		"NTBA_FIX_319=1",
		"NTBA_FIX_350=1",
	)
	if m.cfg.AgentToolsEnabled && m.toolToken != "" {
		env = append(env, "AGENT_TOOLS_ENABLED=true", "OPENCLAW_TOOL_TOKEN="+m.toolToken)
	} else {
		env = append(env, "AGENT_TOOLS_ENABLED=false")
	}

	cmd := exec.Command(m.cfg.NodeBin, entryPoint)
	cmd.Env = env
	cmd.Dir = filepath.Dir(filepath.Dir(entryPoint))

	// Pipe stdout dan stderr ke log channel
	logCh := make(chan string, 500)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("gagal buat stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("gagal buat stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("gagal start agent %s: %w", cfg.ID, err)
	}

	ap := &AgentProcess{
		ID:                cfg.ID,
		Cmd:               cmd,
		LogCh:             logCh,
		IngressURL:        fmt.Sprintf("http://127.0.0.1:%d", ingressPort),
		StartedAt:         time.Now(),
		RuntimeSourcePath: runtimeSourcePath(cfg.BotsDir),
		running:           true,
	}
	m.agents[cfg.ID] = ap

	// Goroutine untuk pipe stdout → logCh
	go pipeToChannel(stdout, logCh, cfg.ID, "OUT")
	go pipeToChannel(stderr, logCh, cfg.ID, "ERR")

	// Watchdog goroutine — monitor process exit
	go func() {
		err := cmd.Wait()
		ap.mu.Lock()
		ap.running = false
		ap.mu.Unlock()
		if err != nil {
			logCh <- fmt.Sprintf("[%s][SYS] Process berhenti dengan error: %v", cfg.ID, err)
		} else {
			logCh <- fmt.Sprintf("[%s][SYS] Process berhenti normal", cfg.ID)
		}
		close(logCh)
	}()

	sourceMTime := runtimeSourceMTime(ap.RuntimeSourcePath)
	logCh <- fmt.Sprintf("[%s][RUNTIME] runtime_started_at=%s runtime_source_mtime=%s runtime_source=%s route_resolver_version=node-workspace-route-v3", cfg.ID, ap.StartedAt.Format(time.RFC3339Nano), sourceMTime.Format(time.RFC3339Nano), filepath.Base(ap.RuntimeSourcePath))
	fmt.Printf("✅ [manager] Agent %s started (PID %d)\n", cfg.ID, cmd.Process.Pid)
	return nil
}

// Stop menghentikan proses agent.
func (m *Manager) Stop(agentID string) error {
	m.mu.Lock()

	ap, exists := m.agents[agentID]
	if !exists || !ap.running {
		m.mu.Unlock()
		return fmt.Errorf("agent %s tidak sedang berjalan", agentID)
	}

	// Kirim SIGINT (graceful), fallback ke SIGKILL
	if err := ap.Cmd.Process.Signal(os.Interrupt); err != nil {
		ap.Cmd.Process.Kill()
	}

	ap.mu.Lock()
	ap.running = false
	ap.mu.Unlock()

	// Hapus dari registry — mutex DILEPAS segera agar API lain tidak ngantri
	delete(m.agents, agentID)
	m.mu.Unlock()

	// ── Cooldown anti-Telegram-409 ─────────────────────────────────────────
	// Dijalankan SETELAH mutex dilepas sehingga health-check, status polling,
	// dan request lain dari frontend TIDAK ikut terblokir selama jeda ini.
	// Telegram membutuhkan ≈3 detik untuk melepas getUpdates lock sesi lama.
	log.Printf("[Manager] Agent %s dihentikan — cooldown 3s sebelum slot siap...", agentID)
	time.Sleep(3 * time.Second)
	log.Printf("[Manager] Slot %s siap digunakan kembali.", agentID)

	return nil
}

// GetStatus mengembalikan status semua agent yang terdaftar.
func (m *Manager) GetStatus(agentID string) AgentStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ap, exists := m.agents[agentID]
	if !exists {
		return AgentStatus{ID: agentID, Running: false}
	}

	ap.mu.Lock()
	defer ap.mu.Unlock()

	status := AgentStatus{
		ID:        agentID,
		Running:   ap.running,
		StartedAt: ap.StartedAt,
	}
	status.RuntimeSourceMTime = runtimeSourceMTime(ap.RuntimeSourcePath)
	if ap.running && !status.RuntimeSourceMTime.IsZero() && ap.StartedAt.Before(status.RuntimeSourceMTime) {
		status.RuntimeStale = true
		status.RuntimeWarning = "Agent runtime stale: source lebih baru dari proses aktif. Restart agent."
	}
	if ap.running && ap.Cmd.Process != nil {
		status.PID = ap.Cmd.Process.Pid
	}
	return status
}

// GetLogChannel mengembalikan log channel untuk streaming.
func (m *Manager) GetLogChannel(agentID string) (chan string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ap, exists := m.agents[agentID]
	if !exists {
		return nil, false
	}
	return ap.LogCh, true
}

func (m *Manager) GetIngressURL(agentID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ap, exists := m.agents[agentID]
	if !exists {
		return "", false
	}
	ap.mu.Lock()
	defer ap.mu.Unlock()
	if !ap.running || ap.IngressURL == "" {
		return "", false
	}
	return ap.IngressURL, true
}

// pipeToChannel membaca dari reader dan kirim ke channel dengan prefix.
func pipeToChannel(r io.Reader, ch chan string, agentID, stream string) {
	scanner := bufio.NewScanner(r)
	// Security/Stability: default Scanner token size = 64KB.
	// Log lines dari LLM/tool output bisa lebih dari itu → panic tanpa buffer eksplisit.
	// Set 1MB buffer untuk menangani output panjang tanpa crash.
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		select {
		case ch <- fmt.Sprintf("[%s][%s] %s", agentID, stream, line):
		default:
			// Channel full, drop log line (jangan block)
		}
	}
}
