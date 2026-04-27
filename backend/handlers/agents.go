// handlers/agents.go
// CRUD endpoints untuk agent management — list, start, stop, create, delete.
// Menggunakan App struct untuk akses shared state dengan thread-safety.

package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"multiagent-platform/internal/config"
	"multiagent-platform/internal/openclaw"
	"multiagent-platform/internal/process"
	"multiagent-platform/internal/security"
	"multiagent-platform/internal/visitor"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// App adalah shared application state yang diakses oleh semua handlers.
type App struct {
	Cfg              *config.Config
	InternalRPCToken string
	Creds            map[string]*config.AgentCred // hot-reloadable
	CredsLock        interface {
		RLock()
		RUnlock()
		Lock()
		Unlock()
	}
	Manager  *process.Manager
	Security *security.Manager
	Visitors *visitor.Store
}

// AgentInfo adalah info lengkap satu agent untuk response API.
type AgentInfo struct {
	ID       string              `json:"id"`
	Label    string              `json:"label"`
	Model    string              `json:"model"`
	HasToken bool                `json:"has_token"`
	Status   process.AgentStatus `json:"status"`
}

// CreateAgentRequest adalah body untuk POST /api/agents.
type CreateAgentRequest struct {
	ID               string            `json:"id"`
	Label            string            `json:"label"`
	TelegramBotToken string            `json:"telegram_bot_token"`
	OpenClawModel    string            `json:"openclaw_model,omitempty"`
	TemplateProfile  string            `json:"template_profile,omitempty"`
	ToneProfile      string            `json:"tone_profile,omitempty"`
	ModelRoute       ModelRouteRequest `json:"model_route,omitempty"`
	Persona          string            `json:"persona,omitempty"`
	WorkspaceFiles   map[string]string `json:"workspace_files,omitempty"`
}

type ModelRouteRequest struct {
	RouteTarget      string `json:"route_target,omitempty"`
	Tier             string `json:"tier,omitempty"`
	AdapterID        string `json:"adapter_id,omitempty"`
	OutboundModel    string `json:"outbound_model,omitempty"`
	ProviderBaseURL  string `json:"provider_base_url,omitempty"`
	RuntimeSupported *bool  `json:"runtime_supported,omitempty"`
}

// AgentsHandler mengelola semua request ke /api/agents/...
type AgentsHandler struct {
	App         *App
	CredsGetter func() map[string]*config.AgentCred
	CredsSetter func(map[string]*config.AgentCred)
}

var validAgentID = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

func applyModelRouteMetadata(cred *config.AgentCred, meta ModelRouteRequest) {
	if cred == nil {
		return
	}
	cred.ModelRouteTarget = strings.TrimSpace(meta.RouteTarget)
	cred.ModelTier = strings.TrimSpace(meta.Tier)
	cred.ModelAdapterID = strings.TrimSpace(meta.AdapterID)
	cred.ModelOutbound = strings.TrimSpace(meta.OutboundModel)
	cred.ModelProviderURL = strings.TrimSpace(meta.ProviderBaseURL)
	if meta.RuntimeSupported == nil {
		cred.RuntimeSupported = ""
	} else if *meta.RuntimeSupported {
		cred.RuntimeSupported = "true"
	} else {
		cred.RuntimeSupported = "false"
	}
}

func (h *AgentsHandler) auditAgentRegistry(agentID, operation, status string, err error, metadata map[string]interface{}) {
	if h == nil || h.App == nil || h.App.Security == nil {
		return
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	metadata["operation"] = operation
	metadata["registry_path"] = config.AgentRegistryPath(h.App.Cfg.CredentialsDir)
	event := security.AuditEvent{
		Level:    "INFO",
		AgentID:  agentID,
		Channel:  "agent_registry",
		Status:   status,
		Message:  "agent registry lifecycle",
		Metadata: metadata,
	}
	if err != nil {
		event.Level = "ERROR"
		event.ErrorClass = security.MaskKnownSecrets(err.Error())
	}
	h.App.Security.EnqueueAudit(event)
}

// ListAgents handler untuk GET /api/agents
func (h *AgentsHandler) ListAgents(w http.ResponseWriter, r *http.Request) {
	creds := h.CredsGetter()
	agents := make([]AgentInfo, 0, len(creds))
	for id, cred := range creds {
		status := h.App.Manager.GetStatus(id)
		status.ID = id
		status.Label = cred.Label
		agents = append(agents, AgentInfo{
			ID:       id,
			Label:    cred.Label,
			Model:    cred.OpenClawModel,
			HasToken: cred.TelegramBotToken != "" && !strings.HasPrefix(cred.TelegramBotToken, "GANTI"),
			Status:   status,
		})
	}
	jsonOK(w, agents)
}

// CreateAgent handler untuk POST /api/agents — buat agent baru dari UI.
func (h *AgentsHandler) CreateAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreateAgentRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Request body tidak valid: "+err.Error())
		return
	}

	// Normalize ID
	req.ID = strings.ToLower(strings.TrimSpace(req.ID))
	req.Label = strings.TrimSpace(req.Label)

	// Validasi
	if !validAgentID.MatchString(req.ID) {
		jsonError(w, http.StatusBadRequest, "Agent ID hanya boleh mengandung huruf kecil, angka, - dan _")
		return
	}
	if req.Label == "" {
		req.Label = req.ID
	}
	if req.OpenClawModel == "" {
		req.OpenClawModel = "openclaw/" + req.ID
	}

	// Cek apakah ID sudah ada
	creds := h.CredsGetter()
	if _, exists := creds[req.ID]; exists {
		jsonError(w, http.StatusConflict, fmt.Sprintf("Agent '%s' sudah ada", req.ID))
		return
	}

	newCred := &config.AgentCred{
		ID:               req.ID,
		Label:            req.Label,
		TelegramBotToken: req.TelegramBotToken,
		OpenClawModel:    req.OpenClawModel,
	}
	applyModelRouteMetadata(newCred, req.ModelRoute)

	soulFile := ResolveSoulMDPath(h.App.Cfg.OpenClawWorkspaceDir, req.ID)
	soulDir := filepath.Dir(soulFile)
	mainWorkspace := filepath.Join(h.App.Cfg.OpenClawWorkspaceDir, "workspace")
	workspaceOverrides := make(map[string]string, len(req.WorkspaceFiles)+1)
	for name, content := range req.WorkspaceFiles {
		workspaceOverrides[name] = content
	}
	if strings.TrimSpace(req.Persona) != "" {
		workspaceOverrides["SOUL.md"] = req.Persona
	}
	renderedWorkspace, err := renderAgentWorkspaceFiles(agentTemplateRenderOptions{
		ID:              req.ID,
		Label:           req.Label,
		TemplateProfile: req.TemplateProfile,
		ToneProfile:     req.ToneProfile,
		MainWorkspace:   mainWorkspace,
		WorkspaceDir:    soulDir,
		WorkspaceFiles:  workspaceOverrides,
		UseBaseline:     true,
	})
	if err != nil {
		h.auditAgentRegistry(req.ID, "create", "error", err, map[string]interface{}{"stage": "workspace_render"})
		jsonError(w, http.StatusBadRequest, "Template workspace tidak valid: "+err.Error())
		return
	}
	createdWorkspace := isManagedAgentWorkspace(h.App.Cfg.OpenClawWorkspaceDir, soulDir, req.ID)
	success := false
	registeredOC := false
	defer func() {
		if success || !createdWorkspace {
			return
		}
		if registeredOC {
			_ = openclaw.UnregisterAgentFromOCConfig(h.App.Cfg.OpenClawWorkspaceDir, req.ID)
		}
		_ = os.RemoveAll(soulDir)
	}()
	if err := mkdirSecure(soulDir); err != nil {
		h.auditAgentRegistry(req.ID, "create", "error", err, map[string]interface{}{"stage": "workspace_mkdir"})
		jsonError(w, http.StatusInternalServerError, "Gagal buat direktori workspace: "+err.Error())
		return
	}

	for _, name := range agentWorkspaceFiles {
		content, ok := renderedWorkspace[name]
		if !ok {
			continue
		}
		if err := writeFileNoSymlink(filepath.Join(soulDir, name), []byte(content)); err != nil {
			h.auditAgentRegistry(req.ID, "create", "error", err, map[string]interface{}{"stage": "workspace_write", "file": name})
			jsonError(w, http.StatusInternalServerError, "Gagal tulis "+name+": "+err.Error())
			return
		}
	}
	fmt.Printf("[Agent] ✅ Workspace template '%s/%s' selesai untuk subagent '%s' di %s\n", normalizeRoleProfile(req.TemplateProfile).ID, normalizeToneProfile(req.ToneProfile).ID, req.ID, soulDir)

	// ── [KRITIS] Daftarkan agent ke openclaw.json ──────────────────────────
	// Ini memastikan OpenClaw daemon memuat workspace YANG BENAR untuk agent ini.
	// Tanpa ini, OpenClaw fallback ke workspace default (main), loading BOOTSTRAP.md main
	// yang berisi sesi onboarding interaktif → bot bertanya nama/vibe ke user.
	ocDir := h.App.Cfg.OpenClawWorkspaceDir // ~/.openclaw
	regErr := openclaw.RegisterAgentInOCConfig(ocDir, openclaw.RegisterAgentParams{
		ID:           req.ID,
		WorkspaceDir: soulDir,
		Model:        req.OpenClawModel,
		BotToken:     req.TelegramBotToken,
	})
	if regErr != nil {
		h.auditAgentRegistry(req.ID, "create", "error", regErr, map[string]interface{}{"stage": "openclaw_register"})
		fmt.Printf("[Agent] ⚠️  Gagal daftarkan ke openclaw.json: %v\n", regErr)
		jsonError(w, http.StatusInternalServerError, "Gagal daftarkan agent ke OpenClaw: "+security.MaskKnownSecrets(regErr.Error()))
		return
	} else {
		registeredOC = true
		fmt.Printf("[Agent] ✅ Agent '%s' terdaftar di openclaw.json (workspace: %s)\n", req.ID, soulDir)
	}

	// Tulis registry credentials/agents.json setelah workspace dan OpenClaw registration siap.
	if err := config.WriteAgentCred(h.App.Cfg.CredentialsDir, newCred); err != nil {
		h.auditAgentRegistry(req.ID, "create", "error", err, map[string]interface{}{"stage": "registry_write"})
		jsonError(w, http.StatusInternalServerError, "Gagal tulis credentials: "+err.Error())
		return
	}

	// Hot-reload creds di memory
	updatedCreds := h.CredsGetter()
	updatedCreds[req.ID] = newCred
	h.CredsSetter(updatedCreds)

	h.auditAgentRegistry(req.ID, "create", "success", nil, map[string]interface{}{
		"model": req.OpenClawModel,
	})
	success = true

	jsonOK(w, map[string]interface{}{
		"message": fmt.Sprintf("Agent '%s' berhasil dibuat", req.ID),
		"agent": AgentInfo{
			ID:       req.ID,
			Label:    req.Label,
			Model:    req.OpenClawModel,
			HasToken: req.TelegramBotToken != "" && !strings.HasPrefix(req.TelegramBotToken, "GANTI"),
			Status:   process.AgentStatus{ID: req.ID, Running: false},
		},
	})
}

// DeleteAgent handler untuk DELETE /api/agents/:id
func (h *AgentsHandler) DeleteAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	agentID := extractAgentID(r.URL.Path, "")

	// Stop jika sedang running
	status := h.App.Manager.GetStatus(agentID)
	if status.Running {
		h.App.Manager.Stop(agentID)
	}

	// Hapus registry credentials entry
	if err := config.DeleteAgentCred(h.App.Cfg.CredentialsDir, agentID); err != nil && !os.IsNotExist(err) {
		h.auditAgentRegistry(agentID, "delete", "error", err, nil)
		jsonError(w, http.StatusInternalServerError, "Gagal hapus credentials: "+err.Error())
		return
	}

	// [PENGHAPUSAN MENYELURUH] Hapus SOUL.md (Persona) beserta direktori workspace-nya
	soulFile := ResolveSoulMDPath(h.App.Cfg.OpenClawWorkspaceDir, agentID)
	soulDir := filepath.Dir(soulFile)

	fmt.Printf("[Agent] Memproses penghapusan direktori workspace: %s\n", soulDir)

	// BUKAN cuma hapus SOUL.md, melainkan seluruh folder workspace-nya.
	// Hanya hapus workspace managed ~/.openclaw/workspace-<agentID> agar config
	// OpenClaw yang salah/malicious tidak membuat path arbitrer terhapus.
	if isManagedAgentWorkspace(h.App.Cfg.OpenClawWorkspaceDir, soulDir, agentID) {
		err := os.RemoveAll(soulDir)
		if err != nil {
			fmt.Printf("[Agent] GAGAL menghapus %s: %v\n", soulDir, err)
		} else {
			fmt.Printf("[Agent] SUKSES menghapus bersih folder %s\n", soulDir)
		}
	} else {
		fmt.Printf("[Agent] Workspace %s dilindungi (tidak dihapus foldernya)\n", soulDir)
	}

	// Hot-reload: hapus dari memory
	creds := h.CredsGetter()
	delete(creds, agentID)
	h.CredsSetter(creds)

	// Hapus dari openclaw.json agar daemon tidak memuat workspace yang sudah dihapus
	ocDir := h.App.Cfg.OpenClawWorkspaceDir
	if unregErr := openclaw.UnregisterAgentFromOCConfig(ocDir, agentID); unregErr != nil {
		fmt.Printf("[Agent] ⚠️  Gagal unregister '%s' dari openclaw.json: %v\n", agentID, unregErr)
	} else {
		fmt.Printf("[Agent] ✅ Agent '%s' dihapus dari openclaw.json\n", agentID)
	}
	h.auditAgentRegistry(agentID, "delete", "success", nil, nil)

	jsonOK(w, map[string]string{
		"message": fmt.Sprintf("Agent '%s' berhasil dihapus", agentID),
	})
}

// GetAvailableModels handler untuk GET /api/agents/models
// Mengembalikan daftar model beserta grouping cloud/cloud_proxied/local
func (h *AgentsHandler) GetAvailableModels(w http.ResponseWriter, r *http.Request) {
	ocDir := h.App.Cfg.OpenClawWorkspaceDir
	all, groups, err := openclaw.GetAvailableModels(ocDir)
	if err != nil {
		// Fallback: kembalikan list kosong agar UI tidak crash
		jsonOK(w, map[string]interface{}{
			"models": []openclaw.ModelEntry{},
			"groups": openclaw.ModelGroups{},
		})
		return
	}
	jsonOK(w, map[string]interface{}{
		"models": all,
		"groups": groups,
	})
}

// UpdateAgentModel handler untuk PUT /api/agents/:id/model
// Mengupdate model per-agent di registry JSON DAN di ~/.openclaw/openclaw.json
func (h *AgentsHandler) UpdateAgentModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	agentID := extractAgentID(r.URL.Path, "model")
	if agentID == "" {
		jsonError(w, http.StatusBadRequest, "Agent ID tidak valid")
		return
	}

	var req struct {
		Model      string            `json:"model"`
		ModelRoute ModelRouteRequest `json:"model_route,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || req.Model == "" {
		jsonError(w, http.StatusBadRequest, "Field 'model' wajib diisi")
		return
	}

	// Validasi model string: max 200 char, no newlines, no null bytes (prevent log injection / path abuse)
	if len(req.Model) > 200 || strings.ContainsAny(req.Model, "\n\r\x00") {
		jsonError(w, http.StatusBadRequest, "Format model tidak valid")
		return
	}

	creds := h.CredsGetter()
	cred, ok := creds[agentID]
	if !ok {
		jsonError(w, http.StatusNotFound, "Agent tidak ditemukan: "+agentID)
		return
	}

	// 1. Update di registry agent agar base-bot.js mendapat model baru saat restart.
	// Simpan ref model penuh sesuai dokumentasi OpenClaw; runner yang melakukan
	// normalisasi provider-specific saat request dikirim.
	cred.OpenClawModel = req.Model
	applyModelRouteMetadata(cred, req.ModelRoute)
	if err := config.WriteAgentCred(h.App.Cfg.CredentialsDir, cred); err != nil {
		h.auditAgentRegistry(agentID, "update_model", "error", err, nil)
		jsonError(w, http.StatusInternalServerError, "Gagal update credentials: "+err.Error())
		return
	}

	// 2. Sync update ke openclaw.json agar Gateway/Daemon OpenClaw juga mengetahuinya
	ocDir := h.App.Cfg.OpenClawWorkspaceDir
	if err := openclaw.UpdateAgentModelInOCConfig(ocDir, agentID, req.Model); err != nil {
		// Non-fatal: log saja, credentials sudah berhasil terupdate
		fmt.Printf("[Agent] ⚠️  Gagal update model di openclaw.json untuk '%s': %v\n", agentID, err)
	} else {
		fmt.Printf("[Agent] ✅ Model '%s' diupdate → '%s' di openclaw.json\n", agentID, req.Model)
	}

	// Hot-reload di memory
	updatedCreds := h.CredsGetter()
	updatedCreds[agentID] = cred
	h.CredsSetter(updatedCreds)
	h.auditAgentRegistry(agentID, "update_model", "success", nil, map[string]interface{}{
		"model": req.Model,
	})

	jsonOK(w, map[string]interface{}{
		"message": fmt.Sprintf("Model agent '%s' diperbarui ke '%s'", agentID, req.Model),
		"model":   req.Model,
	})
}

// StartAgent handler untuk POST /api/agents/:id/start
func (h *AgentsHandler) StartAgent(w http.ResponseWriter, r *http.Request) {
	agentID := extractAgentID(r.URL.Path, "start")
	if agentID == "" {
		jsonError(w, http.StatusBadRequest, "Invalid Agent ID")
		return
	}

	creds := h.CredsGetter()
	cred, ok := creds[agentID]
	if !ok {
		jsonError(w, http.StatusNotFound, "agent tidak ditemukan: "+agentID)
		return
	}

	if strings.HasPrefix(cred.TelegramBotToken, "GANTI") || cred.TelegramBotToken == "" {
		jsonError(w, http.StatusBadRequest, "Telegram bot token belum dikonfigurasi untuk agent ini")
		return
	}

	agentCfg := process.AgentConfig{
		ID:               cred.ID,
		Label:            cred.Label,
		TelegramBotToken: cred.TelegramBotToken,
		OpenClawModel:    cred.OpenClawModel,
		GatewayURL:       h.App.Cfg.GatewayURL,
		GatewayToken:     h.App.Cfg.GatewayToken,
		BotsDir:          h.App.Cfg.BotsDir,
		SoulMDPath:       ResolveSoulMDPath(h.App.Cfg.OpenClawWorkspaceDir, agentID),
		ProviderAPIKey:   cred.ProviderAPIKey,
		ProviderName:     cred.ProviderName,
		ModelRouteTarget: cred.ModelRouteTarget,
		ModelTier:        cred.ModelTier,
		ModelAdapterID:   cred.ModelAdapterID,
		ModelOutbound:    cred.ModelOutbound,
		ModelProviderURL: cred.ModelProviderURL,
		RuntimeSupported: cred.RuntimeSupported,
	}
	if agentCfg.ProviderAPIKey == "" {
		if provider, _, ok := strings.Cut(cred.OpenClawModel, "/"); ok {
			if key, err := openclaw.ResolveProviderAPIKey(h.App.Cfg.OpenClawWorkspaceDir, provider); err == nil && key != "" {
				agentCfg.ProviderAPIKey = key
				if agentCfg.ProviderName == "" {
					agentCfg.ProviderName = provider
				}
			}
		}
	}

	// Auto-detect & override Gateway Token from openclaw.json if possible
	if globalOC, err := openclaw.ReadGlobalOCInfo(h.App.Cfg.OpenClawWorkspaceDir); err == nil && globalOC.GatewayToken != "" {
		if agentCfg.GatewayToken == "" {
			agentCfg.GatewayToken = globalOC.GatewayToken
		}
	}

	if err := h.App.Manager.Start(agentCfg); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	status := h.App.Manager.GetStatus(agentID)
	jsonOK(w, map[string]interface{}{
		"message": "Agent " + agentID + " berhasil distart",
		"status":  status,
	})
}

// StopAgent handler untuk POST /api/agents/:id/stop
func (h *AgentsHandler) StopAgent(w http.ResponseWriter, r *http.Request) {
	agentID := extractAgentID(r.URL.Path, "stop")
	if agentID == "" {
		jsonError(w, http.StatusBadRequest, "Invalid Agent ID")
		return
	}

	if err := h.App.Manager.Stop(agentID); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonOK(w, map[string]string{
		"message": "Agent " + agentID + " berhasil dihentikan",
	})
}

// GetAgentStatus handler untuk GET /api/agents/:id/status
func (h *AgentsHandler) GetAgentStatus(w http.ResponseWriter, r *http.Request) {
	agentID := extractAgentID(r.URL.Path, "status")
	if agentID == "" {
		jsonError(w, http.StatusBadRequest, "Invalid Agent ID")
		return
	}

	creds := h.CredsGetter()
	cred, ok := creds[agentID]
	if !ok {
		jsonError(w, http.StatusNotFound, "agent tidak ditemukan")
		return
	}

	status := h.App.Manager.GetStatus(agentID)
	status.Label = cred.Label
	jsonOK(w, status)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func extractAgentID(path, action string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 3 {
		return ""
	}
	id := parts[2]
	if !validAgentID.MatchString(id) {
		return "" // Invalid ID format (LFI attempt protection)
	}
	return id
}

func jsonOK(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ResolveSoulMDPath melacak lokasi SOUL.md eksisting milik agent.
func ResolveSoulMDPath(ocWorkspaceDir, agentID string) string {
	var foundWorkspace string

	// 1. Coba baca workspace spesifik untuk agent tertentu atau default dari openclaw.json
	detectedAgents, errDetect := openclaw.DetectAgents(ocWorkspaceDir)
	globalOC, errGlobal := openclaw.ReadGlobalOCInfo(ocWorkspaceDir)

	if errDetect == nil {
		for _, da := range detectedAgents {
			if da.ID == agentID {
				foundWorkspace = da.Workspace
				break
			}
		}
	}

	// Khusus untuk agen default/utama (biasanya "main"), kita pertahankan menggunakan Native Default Workspace
	if foundWorkspace == "" && errGlobal == nil && (agentID == "main" || agentID == "default") {
		foundWorkspace = globalOC.DefaultWorkspace
	}

	// 2. Jika ada direktori terdeteksi native di openclaw.json
	if foundWorkspace != "" {
		return filepath.Join(foundWorkspace, "SOUL.md")
	}

	// 3. Fallback: ~/.openclaw/workspace-<agentID>/SOUL.md
	agentWorkspace := filepath.Join(ocWorkspaceDir, "workspace-"+agentID)
	return filepath.Join(agentWorkspace, "SOUL.md")
}
