// main.go — OpenClaw Multi-Agent Backend v2
// Dengan auth middleware, dynamic agent CRUD, dan OpenClaw detection.

package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"multiagent-platform/handlers"
	"multiagent-platform/internal/auth"
	"multiagent-platform/internal/config"
	"multiagent-platform/internal/gateway"
	"multiagent-platform/internal/netmeta"
	"multiagent-platform/internal/process"
	"multiagent-platform/internal/security"
	visitorstore "multiagent-platform/internal/visitor"
)

// agentRegistry adalah shared mutable state dengan RWMutex.
type agentRegistry struct {
	mu    sync.RWMutex
	creds map[string]*config.AgentCred
}

func (r *agentRegistry) get() map[string]*config.AgentCred {
	r.mu.RLock()
	defer r.mu.RUnlock()
	// Return shallow copy to avoid external mutation
	copy := make(map[string]*config.AgentCred, len(r.creds))
	for k, v := range r.creds {
		copy[k] = v
	}
	return copy
}

func (r *agentRegistry) set(creds map[string]*config.AgentCred) {
	r.mu.Lock()
	r.creds = creds
	r.mu.Unlock()
}

func (r *agentRegistry) ids() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.creds))
	for id := range r.creds {
		ids = append(ids, id)
	}
	return ids
}

func main() {
	// ── Flags ─────────────────────────────────────────────────────────────────
	credDir := flag.String("credentials", defaultCredentialsDir(), "Path ke folder credentials/")
	flag.Parse()

	log.Printf("🚀 OpenClaw Multi-Agent Backend v2 starting...")
	log.Printf("   Credentials dir: %s", *credDir)

	// ── Load config & credentials ─────────────────────────────────────────────
	cfg, err := config.Load(*credDir)
	if err != nil {
		log.Fatalf("❌ Gagal load config: %v", err)
	}

	initialCreds, err := config.LoadAgentCreds(*credDir)
	if err != nil {
		log.Printf("⚠️  Gagal load agent credentials: %v", err)
		initialCreds = make(map[string]*config.AgentCred)
	}

	if len(initialCreds) == 0 {
		log.Printf("ℹ️  Tidak ada agent credentials. Tambahkan via dashboard.")
	} else {
		log.Printf("✅ Loaded %d agent(s)", len(initialCreds))
	}

	// ── Init shared state ─────────────────────────────────────────────────────
	// Generate cryptographically secure 32-byte token for internal agent RPC.
	// Using crypto/rand instead of time.Now() — prevents timestamp brute-force bypass.
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		log.Fatalf("❌ Gagal generate internal RPC token: %v", err)
	}
	internalRPCToken := hex.EncodeToString(tokenBytes)
	toolTokenBytes := make([]byte, 32)
	if _, err := rand.Read(toolTokenBytes); err != nil {
		log.Fatalf("❌ Gagal generate internal tool token: %v", err)
	}
	internalToolToken := hex.EncodeToString(toolTokenBytes)

	registry := &agentRegistry{creds: initialCreds}

	// ── Feature: Secure Hot Reload Watcher ────────────────────────────────────
	// Melakukan polling perubahan file .env setiap 5 detik.
	// Aman dari Race-Condition: Mutex Write-Lock terjamin dalam agentRegistry.set()
	// Aman dari I/O Exhaustion: Interval 5 detik sangat ringan.
	go func() {
		for {
			time.Sleep(5 * time.Second)
			hotCreds, err := config.LoadAgentCreds(*credDir)
			if err == nil {
				registry.set(hotCreds)
			}
		}
	}()

	sessions := auth.NewSessionStore(cfg.DashboardPassword)
	backendURL := fmt.Sprintf("http://%s:%s", cfg.APIHost, cfg.APIPort)
	mgr := process.NewManager(cfg, internalRPCToken, backendURL, internalToolToken)
	securityMgr, err := security.NewManager(cfg)
	if err != nil {
		log.Fatalf("❌ Gagal init security manager: %v", err)
	}
	visitorStore := visitorstore.NewStore(cfg)
	if visitorStore.Enabled() {
		log.Printf("✅ Visitor PostgreSQL store enabled")
	} else {
		log.Printf("ℹ️  Visitor PostgreSQL store disabled/unavailable; gateway audit metadata still works")
	}

	appState := &handlers.App{
		Cfg:              cfg,
		Manager:          mgr,
		InternalRPCToken: internalRPCToken,
		Security:         securityMgr,
		Visitors:         visitorStore,
	}

	// ── Init Handlers ─────────────────────────────────────────────────────────
	authH := &handlers.AuthHandler{
		Sessions:      sessions,
		InternalToken: internalRPCToken,
		OCDir:         cfg.OpenClawWorkspaceDir,
		Cfg:           cfg,
	}

	agentsH := &handlers.AgentsHandler{
		App:         appState,
		CredsGetter: registry.get,
		CredsSetter: registry.set,
	}

	logsH := &handlers.LogsHandler{Manager: mgr, Security: securityMgr, FrontendOrigin: cfg.FrontendOrigin}

	personasH := &handlers.PersonasHandler{App: appState}
	workspaceH := &handlers.WorkspaceHandler{App: appState}

	ocH := &handlers.OpenClawHandler{
		GatewayURL:    cfg.GatewayURL,
		GatewayToken:  cfg.GatewayToken,
		WorkspaceDir:  cfg.OpenClawWorkspaceDir,
		Security:      securityMgr,
		KnownAgentIDs: registry.ids,
	}
	routerStatusH := &handlers.RouterStatusHandler{App: appState}

	gwManager := gateway.NewManager(*credDir, cfg.GatewayURL, securityMgr)
	gatewayH := &handlers.GatewayHandler{
		App:       appState,
		GwManager: gwManager,
		PublicURL: backendURL,
	}
	securityH := &handlers.SecurityHandler{App: appState}
	visitorsH := &handlers.VisitorsHandler{App: appState}

	// ── New: Daemon control + Orchestrator ─────────────────────────────────────
	daemonCtrlH := &handlers.OpenClawCtrlHandler{App: appState}
	orchestratorH := &handlers.OrchestratorHandler{
		App:         appState,
		CredsGetter: registry.get,
		CredsSetter: registry.set,
	}

	// ── Router ────────────────────────────────────────────────────────────────
	mux := http.NewServeMux()

	// ── Public routes ──────────────────────────────────────────────────────────
	mux.HandleFunc("/api/health", handlers.Health)
	mux.HandleFunc("/api/system/metrics", handlers.SystemMetricsHandler)
	mux.HandleFunc("/api/auth/login", authH.Login)
	mux.HandleFunc("/api/auth/logout", authH.Logout)
	mux.HandleFunc("/api/auth/me", authH.Me)
	mux.HandleFunc("/api/webhook/", gatewayH.HandleWebhook)
	hardenH := &handlers.SystemHardenHandler{App: appState}
	mux.HandleFunc("/api/system/harden/preview", hardenH.HardenPreviewHandler)
	mux.HandleFunc("/api/system/harden/execute", hardenH.HardenExecuteHandler)

	// ── Security enhancement routes ──────────────────────────────────────────
	mux.HandleFunc("/api/security/policy", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			securityH.GetPolicy(w, r)
		case http.MethodPut:
			securityH.UpdatePolicy(w, r)
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/security/masking/rulesets", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			securityH.GetRules(w, r)
		case http.MethodPut:
			securityH.UpdateRules(w, r)
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/security/audit-logs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			securityH.QueryAuditLogs(w, r)
			return
		}
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("/api/security/templates/validate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			securityH.ValidateTemplate(w, r)
			return
		}
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("/api/security/templates/render", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			securityH.RenderTemplate(w, r)
			return
		}
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("/api/security/templates/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			securityH.DeleteTemplate(w, r)
			return
		}
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("/api/security/templates", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			securityH.ListTemplates(w, r)
		case http.MethodPost, http.MethodPut:
			securityH.SaveTemplate(w, r)
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/security/internal/mask", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			securityH.InternalMask(w, r)
			return
		}
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("/api/security/internal/audit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			securityH.InternalAudit(w, r)
			return
		}
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})

	// ── Agent routes ──────────────────────────────────────────────────────────
	// GET /api/agents/models — harus didahulukan sebelum /api/agents/ wildcard
	mux.HandleFunc("/api/agents/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			agentsH.GetAvailableModels(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/agents", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			agentsH.ListAgents(w, r)
		case http.MethodPost:
			agentsH.CreateAgent(w, r)
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})

	// ── Gateway routes ────────────────────────────────────────────────────────
	mux.HandleFunc("/api/gateway", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			gatewayH.GetGateways(w, r)
		case http.MethodPost:
			gatewayH.CreateGateway(w, r)
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/gateway/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			gatewayH.DeleteGateway(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/visitors", visitorsH.Query)

	mux.HandleFunc("/api/agents/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		// Start/Stop
		case strings.HasSuffix(path, "/start") && r.Method == http.MethodPost:
			agentsH.StartAgent(w, r)
		case strings.HasSuffix(path, "/stop") && r.Method == http.MethodPost:
			agentsH.StopAgent(w, r)

		// Status
		case strings.HasSuffix(path, "/status") && r.Method == http.MethodGet:
			agentsH.GetAgentStatus(w, r)

		// SSE Logs
		case strings.HasSuffix(path, "/logs") && r.Method == http.MethodGet:
			logsH.StreamLogs(w, r)

		// Persona (SOUL.md)
		case strings.HasSuffix(path, "/persona"):
			switch r.Method {
			case http.MethodGet:
				personasH.GetPersona(w, r)
			case http.MethodPut:
				personasH.UpdatePersona(w, r)
			default:
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			}

		// Workspace Multi-File Endpoint
		case strings.HasSuffix(path, "/workspace"):
			if r.Method == http.MethodGet {
				workspaceH.GetWorkspaceFiles(w, r)
			} else {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			}

		case strings.HasSuffix(path, "/workspace/file"):
			switch r.Method {
			case http.MethodGet:
				workspaceH.GetWorkspaceFile(w, r)
			case http.MethodPut:
				workspaceH.UpdateWorkspaceFile(w, r)
			default:
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			}

		// Update Model per-agent
		case strings.HasSuffix(path, "/model") && r.Method == http.MethodPut:
			agentsH.UpdateAgentModel(w, r)

		// Delete agent (path = /api/agents/:id tanpa suffix)
		case r.Method == http.MethodDelete:
			agentsH.DeleteAgent(w, r)

		default:
			http.NotFound(w, r)
		}
	})

	// ── OpenClaw routes ───────────────────────────────────────────────────────
	mux.HandleFunc("/api/router/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			routerStatusH.GetStatus(w, r)
			return
		}
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})

	mux.HandleFunc("/api/openclaw/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			ocH.GetStatus(w, r)
			return
		}
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})

	mux.HandleFunc("/api/openclaw/commands", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			ocH.GetCommands(w, r)
			return
		}
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})

	mux.HandleFunc("/api/openclaw/commands/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			ocH.RunCommand(w, r)
			return
		}
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})

	mux.HandleFunc("/api/openclaw/import/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			ocH.ImportAgent(w, r)
			return
		}
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})

	// ── OpenClaw Daemon Control routes ────────────────────────────────────────
	mux.HandleFunc("/api/openclaw/daemon", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			daemonCtrlH.GetDaemonStatus(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/openclaw/daemon/restart", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			daemonCtrlH.RestartDaemon(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/openclaw/daemon/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			daemonCtrlH.StopDaemon(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/openclaw/daemon/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			daemonCtrlH.ReloadConfig(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})

	// ── Orchestrator routes ───────────────────────────────────────────────────
	mux.HandleFunc("/api/orchestrator/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			orchestratorH.GetOrchestratorStatus(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/orchestrator/soul", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			orchestratorH.UpdateOrchestratorSOUL(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})

	// ── Code Tools routes (Claude Code + OpenCode) ─────────────────────────────
	codeToolsH := &handlers.CodeToolsHandler{App: appState, FrontendOrigin: cfg.FrontendOrigin}
	mux.HandleFunc("/api/code-tools/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			codeToolsH.GetStatus(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/code-tools/install", func(w http.ResponseWriter, r *http.Request) {
		codeToolsH.Install(w, r)
	})
	mux.HandleFunc("/api/code-tools/uninstall", func(w http.ResponseWriter, r *http.Request) {
		codeToolsH.Uninstall(w, r)
	})
	mux.HandleFunc("/api/code-tools/start", func(w http.ResponseWriter, r *http.Request) {
		codeToolsH.Start(w, r)
	})
	mux.HandleFunc("/api/code-tools/validate", func(w http.ResponseWriter, r *http.Request) {
		codeToolsH.Validate(w, r)
	})
	mux.HandleFunc("/api/code-tools/stop", func(w http.ResponseWriter, r *http.Request) {
		codeToolsH.Stop(w, r)
	})
	mux.HandleFunc("/api/code-tools/stop-unmanaged", func(w http.ResponseWriter, r *http.Request) {
		codeToolsH.StopUnmanaged(w, r)
	})
	mux.HandleFunc("/api/code-tools/logs/", func(w http.ResponseWriter, r *http.Request) {
		codeToolsH.StreamLogs(w, r)
	})

	// ── UserBot / BotFather routes ─────────────────────────────────────────────
	userBotH := &handlers.UserBotHandler{App: appState}
	internalToolsH := &handlers.InternalToolsHandler{App: appState, Agents: agentsH, ToolToken: internalToolToken, ToolsEnabled: cfg.AgentToolsEnabled}

	mux.HandleFunc("/api/security/internal/tools/create-telegram-bot", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			internalToolsH.CreateTelegramBot(w, r)
			return
		}
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("/api/security/internal/tools/deploy-sub-agent", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			internalToolsH.DeploySubAgent(w, r)
			return
		}
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})

	// ── Provider Config routes ─────────────────────────────────────────────────
	providerCfgH := &handlers.ProviderConfigHandler{App: appState}
	mux.HandleFunc("/api/providers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			providerCfgH.GetProviders(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	// POST /api/providers/{name}  — save API key for a named provider
	mux.HandleFunc("/api/providers/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/api/providers/")
		name = strings.Trim(name, "/")
		if name == "" {
			http.Error(w, "Provider name required", http.StatusBadRequest)
			return
		}
		if strings.HasSuffix(name, "/models") {
			baseName := strings.TrimSuffix(name, "/models")
			if r.Method == http.MethodGet {
				providerCfgH.GetProviderModels(w, r, baseName)
				return
			}
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Method == http.MethodPost {
			providerCfgH.SaveProviderKey(w, r, name)
			return
		}
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})

	// Lifecycle
	mux.HandleFunc("/api/userbot/start", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			userBotH.StartUserBot(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/userbot/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			userBotH.StopUserBot(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/userbot/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			userBotH.GetUserBotStatus(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})

	// Auth (OTP flow)
	mux.HandleFunc("/api/userbot/auth/sendCode", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			userBotH.SendCode(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/userbot/auth/signIn", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			userBotH.SignIn(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/userbot/auth/signIn2FA", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			userBotH.SignIn2FA(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})

	// BotFather CRUD
	mux.HandleFunc("/api/userbot/botfather/create", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			userBotH.CreateBot(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/userbot/botfather/list", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			userBotH.ListBots(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/userbot/botfather/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			userBotH.DeleteBot(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/userbot/botfather/revoke", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			userBotH.RevokeToken(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})

	// BotFather Extended Management
	mux.HandleFunc("/api/userbot/botfather/info", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			userBotH.GetBotInfo(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/userbot/botfather/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			userBotH.GetBotToken(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/userbot/botfather/setname", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			userBotH.SetBotName(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/userbot/botfather/setdescription", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			userBotH.SetBotDescription(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/userbot/botfather/setabout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			userBotH.SetBotAbout(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/userbot/botfather/setcommands", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			userBotH.SetBotCommands(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})

	// POST /api/userbot/send — kirim pesan via UserBot (MTProto guest session, bukan bot Telegram)
	mux.HandleFunc("/api/userbot/send", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			userBotH.SendMessage(w, r)
		} else {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})

	// ── Middleware chain: BodySize → CORS → OWASP → RateLimit → Auth → Logging → Router ──────────────────────
	// maxBytes = 2MB (2 * 1024 * 1024)
	var handler http.Handler = loggingMiddleware(mux)
	handler = handlers.AuthMiddleware(sessions, internalRPCToken, handler)
	handler = rateLimitMiddleware(cfg, handler)
	handler = owaspMiddleware(handler)
	handler = corsMiddleware([]string{cfg.FrontendOrigin, backendURL}, handler)
	handler = bodySizeLimitMiddleware(2*1024*1024, handler)

	// ── Start server ──────────────────────────────────────────────────────────
	addr := fmt.Sprintf("%s:%s", cfg.APIHost, cfg.APIPort)
	log.Printf("✅ API server listening on http://%s", addr)
	log.Printf("   Auth password : %s (set DASHBOARD_PASSWORD di credentials/.env)", maskPassword(cfg.DashboardPassword))
	if cfg.DashboardPassword == "" || cfg.DashboardPassword == "admin" {
		log.Printf("⚠️  SECURITY WARNING: DASHBOARD_PASSWORD masih kosong/default. Set password kuat di credentials/.env sebelum expose dashboard.")
	}
	log.Printf("   Frontend      : %s", cfg.FrontendOrigin)
	log.Printf("   Gateway       : %s", cfg.GatewayURL)

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		// Hapus WriteTimeout: SSE log streaming membutuhkan koneksi yang abadi.
		// Jika diset, Go akan memutus HTTP connection secara paksa setelah X detik.
		IdleTimeout: 60 * time.Second,
	}

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("❌ Server error: %v", err)
	}
}

// ── Middleware ────────────────────────────────────────────────────────────────

func owaspMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; connect-src 'self' http://localhost:* http://127.0.0.1:* wss://localhost:* wss://127.0.0.1:*; img-src 'self' data: https://*.tile.openstreetmap.org; style-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Advanced OWASP Headers. Geolocation is only enabled on the public
		// consent page that records visitor GPS with browser permission.
		geoPolicy := "geolocation=()"
		if isVisitorLocationConsentRoute(r) {
			geoPolicy = "geolocation=(self)"
		}
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), "+geoPolicy+", payment=(), usb=(), screen-wake-lock=()")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Server", "undisclosed") // Masking backend technology

		// Cross-Origin Isolation Headers (mencegah Spectre / cross-origin data leaks)
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("X-Permitted-Cross-Domain-Policies", "none")

		next.ServeHTTP(w, r)
	})
}

func isVisitorLocationConsentRoute(r *http.Request) bool {
	path := r.URL.Path
	if !strings.HasPrefix(path, "/api/webhook/") {
		return false
	}
	if strings.HasSuffix(path, "/visit") {
		return true
	}
	if r.Method != http.MethodGet {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/webhook/"), "/")
	return len(parts) == 3 && parts[1] == "http" && webhookPathLike(parts[2])
}

func webhookPathLike(path string) bool {
	if len(path) < 32 || len(path) > 64 {
		return false
	}
	for _, ch := range path {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

// ── Rate Limiter Custom (Anti Brute-force & DDoS) ─────────────────────────
type visitor struct {
	tokens int
	last   time.Time
}

// loginVisitors tracks strict per-IP rate for /api/auth/login (anti brute-force)
var (
	visitors      = make(map[string]*visitor)
	loginVisitors = make(map[string]*visitor)
	mu            sync.Mutex
)

func init() {
	// Background goroutine: hapus IP entries yang sudah stale setiap 5 menit
	// Mencegah memory leak pada deployment long-running
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		for range ticker.C {
			mu.Lock()
			now := time.Now()
			for ip, v := range visitors {
				if now.Sub(v.last) > 10*time.Minute {
					delete(visitors, ip)
				}
			}
			for ip, v := range loginVisitors {
				if now.Sub(v.last) > 10*time.Minute {
					delete(loginVisitors, ip)
				}
			}
			mu.Unlock()
		}
	}()
}

func rateLimitMiddleware(cfg *config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := rateLimitClientIP(r, cfg)

		mu.Lock()

		// ── Strict limiter untuk /api/auth/login (max 10 req/menit per IP) ──
		if r.URL.Path == "/api/auth/login" {
			v, exists := loginVisitors[ip]
			if !exists {
				loginVisitors[ip] = &visitor{tokens: 10, last: time.Now()}
				v = loginVisitors[ip]
			}
			now := time.Now()
			elapsed := now.Sub(v.last).Seconds()
			// Replenish 1 token setiap 6 detik (10 req/menit)
			v.tokens += int(elapsed / 6)
			if v.tokens > 10 {
				v.tokens = 10
			}
			v.last = now
			if v.tokens <= 0 {
				mu.Unlock()
				w.Header().Set("Retry-After", "60")
				http.Error(w, "429 Too Many Requests — Login throttled", http.StatusTooManyRequests)
				return
			}
			v.tokens--
			mu.Unlock()
			next.ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/webhook/") {
			traffic := netmeta.ClassifyTraffic(r, cfg)
			limit := webhookRatePolicy(cfg, traffic)
			if limit.block {
				mu.Unlock()
				w.Header().Set("Retry-After", "120")
				http.Error(w, "429 Too Many Requests - Abuse traffic throttled", http.StatusTooManyRequests)
				return
			}
			v, exists := visitors[ip]
			if !exists {
				visitors[ip] = &visitor{tokens: limit.burst, last: time.Now()}
				v = visitors[ip]
			}
			now := time.Now()
			elapsed := now.Sub(v.last).Seconds()
			v.tokens += int(elapsed * limit.refillPerSecond)
			if v.tokens > limit.burst {
				v.tokens = limit.burst
			}
			v.last = now
			if v.tokens <= 0 {
				mu.Unlock()
				w.Header().Set("Retry-After", limit.retryAfter)
				http.Error(w, "429 Too Many Requests - Webhook throttled", http.StatusTooManyRequests)
				return
			}
			v.tokens--
			mu.Unlock()
			next.ServeHTTP(w, r)
			return
		}

		// ── General limiter: burst=50, replenish 5/s ──────────────────────────
		v, exists := visitors[ip]
		if !exists {
			visitors[ip] = &visitor{tokens: 50, last: time.Now()} // Burst = 50
			v = visitors[ip]
		}

		now := time.Now()
		elapsed := now.Sub(v.last).Seconds()
		// Replenish 5 tokens per second (Throttling)
		v.tokens += int(elapsed * 5)
		if v.tokens > 50 {
			v.tokens = 50
		}
		v.last = now

		if v.tokens <= 0 {
			mu.Unlock()
			http.Error(w, "429 Too Many Requests - IP Throttled", http.StatusTooManyRequests)
			return
		}
		v.tokens--
		mu.Unlock()

		next.ServeHTTP(w, r)
	})
}

func rateLimitClientIP(r *http.Request, cfg *config.Config) string {
	return netmeta.ClientIPString(r, cfg)
}

type webhookLimitPolicy struct {
	burst           int
	refillPerSecond float64
	retryAfter      string
	block           bool
}

func webhookRatePolicy(cfg *config.Config, traffic netmeta.TrafficAssessment) webhookLimitPolicy {
	if cfg != nil && !cfg.GatewayAbuseDefenseEnabled {
		return webhookLimitPolicy{burst: 20, refillPerSecond: 2, retryAfter: "30"}
	}
	if traffic.Score >= gatewayAbuseBlockScore(cfg) {
		return webhookLimitPolicy{burst: 0, refillPerSecond: 0, retryAfter: "120", block: true}
	}
	switch traffic.Class {
	case "human_likely":
		return webhookLimitPolicy{burst: 20, refillPerSecond: 2, retryAfter: "30"}
	case "automation_likely":
		return webhookLimitPolicy{burst: 10, refillPerSecond: 1, retryAfter: "60"}
	case "scraper_likely":
		return webhookLimitPolicy{burst: 6, refillPerSecond: 0.5, retryAfter: "90"}
	case "abuse_likely":
		return webhookLimitPolicy{burst: 3, refillPerSecond: 0.2, retryAfter: "120"}
	default:
		if traffic.Score >= gatewayAbuseThrottleScore(cfg) {
			return webhookLimitPolicy{burst: 10, refillPerSecond: 1, retryAfter: "60"}
		}
		return webhookLimitPolicy{burst: 20, refillPerSecond: 2, retryAfter: "30"}
	}
}

func gatewayAbuseThrottleScore(cfg *config.Config) int {
	if cfg == nil || cfg.GatewayAbuseThrottleScore <= 0 {
		return 35
	}
	return cfg.GatewayAbuseThrottleScore
}

func gatewayAbuseBlockScore(cfg *config.Config) int {
	if cfg == nil || cfg.GatewayAbuseBlockScore <= 0 {
		return 85
	}
	return cfg.GatewayAbuseBlockScore
}

// ── Body Size Limiter (Anti Memory Exhaustion) ────────────────────────────
func bodySizeLimitMiddleware(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next.ServeHTTP(w, r)
	})
}

func corsMiddleware(allowedOrigins []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Strict Origin Validation (Mitigation for Host Header Injection)
		isValid := false
		if origin != "" {
			for _, allowed := range allowedOrigins {
				if origin == allowed {
					isValid = true
					break
				}
			}
		}

		if isValid {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin") // Penting untuk proxy cache (OWASP)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token, X-OpenClaw-Timestamp, X-OpenClaw-Nonce, X-OpenClaw-Signature")

		// Secure Handling of Null Origin or Invalid Origin
		// Preflight requires explicit matching otherwise drop
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/logs") {
			// Hapus secret dari query string sebelum dicatat ke log (prevent credential leakage)
			logPath := r.URL.Path
			if r.URL.RawQuery != "" {
				q := r.URL.Query()
				for key := range q {
					if isSensitiveQueryKey(key) {
						q.Set(key, "[REDACTED]")
					}
				}
				logPath = r.URL.Path + "?" + q.Encode()
			}
			log.Printf("→ %s %s", r.Method, logPath)
		}
		next.ServeHTTP(w, r)
	})
}

func isSensitiveQueryKey(key string) bool {
	k := strings.ToLower(key)
	sensitive := []string{"token", "api_key", "apikey", "password", "secret", "access_token", "auth_token", "gateway_token"}
	for _, part := range sensitive {
		if strings.Contains(k, part) {
			return true
		}
	}
	return false
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func defaultCredentialsDir() string {
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "..", "credentials")
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Clean(candidate)
		}
	}
	wd, _ := os.Getwd()
	return filepath.Join(wd, "..", "credentials")
}

func maskPassword(p string) string {
	if len(p) <= 2 {
		return "***"
	}
	return string(p[0]) + strings.Repeat("*", len(p)-2) + string(p[len(p)-1])
}
