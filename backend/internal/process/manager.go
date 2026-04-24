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
	"strings"
	"sync"
	"time"

	"multiagent-platform/internal/config"
)

// AgentProcess mewakili satu bot runner yang sedang berjalan.
type AgentProcess struct {
	ID         string
	Cmd        *exec.Cmd
	LogCh      chan string // channel untuk streaming log
	IngressURL string
	StartedAt  time.Time
	mu         sync.Mutex
	running    bool
}

// Manager mengelola semua agent processes.
type Manager struct {
	mu            sync.RWMutex
	agents        map[string]*AgentProcess
	cfg           *config.Config
	internalToken string
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
}

// AgentStatus adalah status agent yang di-return ke API.
type AgentStatus struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Running   bool      `json:"running"`
	StartedAt time.Time `json:"started_at,omitempty"`
	PID       int       `json:"pid,omitempty"`
}

// NewManager membuat instance Manager untuk process node.
func NewManager(cfg *config.Config, internalToken, backendURL string) *Manager {
	if cfg.NodeBin == "" {
		cfg.NodeBin = "node"
	}
	// Clean up any orphaned node bot processes to avoid polling conflicts.
	// Gunakan argv statis tanpa shell untuk menghindari command injection surface.
	exec.Command("pkill", "-f", `bots/agents/.*/index.js`).Run()

	return &Manager{
		agents:        make(map[string]*AgentProcess),
		cfg:           cfg,
		internalToken: internalToken,
		backendURL:    backendURL,
	}
}

// resolveDirectModel menentukan model yang dipakai untuk Direct Ollama Bypass.
// Aturan:
//   - Model CLOUD (mengandung ':cloud', misalnya 'kimi-k2.5:cloud') → return "" (bypass dinonaktifkan)
//   - Model LOKAL (misal 'ollama/llama3.1:8b') → return model mentah tanpa prefix
//   - Model lain (openrouter, custom) → return "" (bypass dinonaktifkan, biarkan Gateway menangani)
func resolveDirectModel(openClawModel string) string {
	// Strip semua prefix provider yang dikenal
	model := strings.TrimPrefix(openClawModel, "ollama/")
	model = strings.TrimPrefix(model, "openrouter/")
	model = strings.TrimSpace(model)

	// Jika model adalah tipe cloud/remote → nonaktifkan bypass
	// Cloud model hanya bisa diakses melalui Gateway OpenClaw
	if strings.Contains(model, ":cloud") ||
		strings.Contains(openClawModel, "openrouter/") ||
		strings.Contains(openClawModel, "anthropic/") ||
		strings.Contains(openClawModel, "google/") ||
		strings.Contains(openClawModel, "mistral/") {
		return "" // Bypass dinonaktifkan — model ini butuh Gateway
	}

	return model // Model lokal → bypass aktif
}

func ingressPortForAgent(agentID string) int {
	// 19000-28999: deterministic, localhost-only, avoids per-process negotiation.
	return 19000 + int(crc32.ChecksumIEEE([]byte(agentID))%10000)
}

// Start spawn proses bot untuk agent tertentu.
func (m *Manager) Start(cfg AgentConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ap, exists := m.agents[cfg.ID]; exists && ap.running {
		return fmt.Errorf("agent %s sudah berjalan (PID %d)", cfg.ID, ap.Cmd.Process.Pid)
	}

	entryPoint := filepath.Join(cfg.BotsDir, "agents", cfg.ID, "index.js")
	if _, err := os.Stat(entryPoint); os.IsNotExist(err) {
		return fmt.Errorf("entry point tidak ditemukan: %s", entryPoint)
	}

	env := os.Environ()
	ingressPort := ingressPortForAgent(cfg.ID)
	env = append(env,
		"TELEGRAM_BOT_TOKEN="+cfg.TelegramBotToken,
		"OPENCLAW_GW_URL="+cfg.GatewayURL,
		"OPENCLAW_GW_TOKEN="+cfg.GatewayToken,
		"OPENCLAW_MODEL="+cfg.OpenClawModel,
		"AGENT_ID="+cfg.ID,
		"SOUL_MD_PATH="+cfg.SoulMDPath,
		"OPENCLAW_INTERNAL_TOKEN="+m.internalToken,
		"OPENCLAW_BACKEND_URL="+m.backendURL,
		"PROVIDER_API_KEY="+cfg.ProviderAPIKey,
		"PROVIDER_NAME="+cfg.ProviderName,
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

	cmd := exec.Command(m.cfg.NodeBin, entryPoint)
	cmd.Env = env
	cmd.Dir = cfg.BotsDir

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
		ID:         cfg.ID,
		Cmd:        cmd,
		LogCh:      logCh,
		IngressURL: fmt.Sprintf("http://127.0.0.1:%d", ingressPort),
		StartedAt:  time.Now(),
		running:    true,
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
