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
	"os/exec"
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
	Persona          string            `json:"persona,omitempty"`
	WorkspaceFiles   map[string]string `json:"workspace_files,omitempty"`
}

// AgentsHandler mengelola semua request ke /api/agents/...
type AgentsHandler struct {
	App         *App
	CredsGetter func() map[string]*config.AgentCred
	CredsSetter func(map[string]*config.AgentCred)
}

var validAgentID = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

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

	// Tulis credentials/agents/<id>.env
	newCred := &config.AgentCred{
		ID:               req.ID,
		Label:            req.Label,
		TelegramBotToken: req.TelegramBotToken,
		OpenClawModel:    req.OpenClawModel,
	}
	if err := config.WriteAgentCred(h.App.Cfg.CredentialsDir, newCred); err != nil {
		jsonError(w, http.StatusInternalServerError, "Gagal tulis credentials: "+err.Error())
		return
	}

	// Timpa SOUL.md default buatan OpenClaw dengan persona kustom dari Dashboard/User
	persona := req.Persona
	if persona == "" {
		persona = fmt.Sprintf("# SOUL.md — %s\n\n## Core Truths\n- Saya adalah agent %s.\n- Saya membantu dengan ramah dan efektif.\n\n## Vibe\n- Langsung ke inti, tidak berputar-putar.\n- Jujur, tidak sugarcoat.\n\n## Boundaries\n- Private things stay private.\n- Konfirmasi sebelum aksi eksternal.\n", req.Label, req.Label)
	}

	soulFile := ResolveSoulMDPath(h.App.Cfg.OpenClawWorkspaceDir, req.ID)
	soulDir := filepath.Dir(soulFile)
	os.MkdirAll(soulDir, 0755)

	// [SCAFFOLDING MANUAL] Karena CLI OpenClaw `agents add` mungkin gagal/tidak tersambung
	// Kita akan menyalin paksa seluruh file bawaan (AGENTS.md, TOOLS.md, dll) dari workspace induk
	mainWorkspace := filepath.Join(h.App.Cfg.OpenClawWorkspaceDir, "workspace")
	if soulDir != mainWorkspace {
		// [SCAFFOLDING LENGKAP] Scaffold semua 7 file workspace standar OpenClaw untuk subagent.
		// Strategi: Salin dari main sebagai baseline (no-clobber) agar AGENTS.md, dsb terisi standar,
		// kemudian OVERWRITE semua file kritis dengan konten subagent-aware yang dihasilkan secara dynamic.
		exec.Command("cp", "-r", "-n", mainWorkspace+"/.", soulDir+"/").Run()

		// BOOT.md — file system rules/boot context, disalin verbatim dari main (tidak dimodifikasi)
		// Cek nama file dengan huruf kecil/besar karena OpenClaw mungkin menyimpannya sebagai 'boot.md'
		bootSrc := filepath.Join(mainWorkspace, "boot.md")
		if _, err := os.Stat(bootSrc); os.IsNotExist(err) {
			bootSrc = filepath.Join(mainWorkspace, "BOOT.md")
		}
		if bootData, err := os.ReadFile(bootSrc); err == nil {
			os.WriteFile(filepath.Join(soulDir, "boot.md"), bootData, 0644)
			fmt.Printf("[Agent] ✅ boot.md disalin ke workspace subagent '%s'\n", req.ID)
		}

		// 1. IDENTITY.md — Identitas subagent yang terstruktur penuh
		identityContent := fmt.Sprintf(`# IDENTITY.md — %s

- **Subagent ID:** %s
- **Display Name:** %s  
- **Type:** Specialized Subagent
- **Reports To:** main (Lead Orchestra)
- **Emoji:** 🤖
- **Role:** *(Tentukan keahlian khusus Anda — e.g. Researcher, Coder, Analyst)*

---

*"Saya bertugas pada domain spesifik saya. Hasil kerja saya dilaporkan kembali ke Main Agent."*
`, req.Label, req.ID, req.Label)
		os.WriteFile(filepath.Join(soulDir, "IDENTITY.md"), []byte(identityContent), 0644)

		// 2. USER.md — Diisi info user yang sama dengan main, tapi subagent tahu konteks hierarkinya
		userContent := fmt.Sprintf(`# USER.md — Context for %s

- **Human Owner:** *(Inherited from main agent — lihat workspace main untuk detail)*
- **Timezone:** Asia/Jakarta (GMT+7)
- **Subagent Context:** Anda melayani request yang didelegasikan oleh Main Agent (%s → main).
- **Direct Interaction:** Mungkin terjadi via group chat atau forward dari Main Agent.

## Cara Menerima Tugas

Tugas datang dari Main Agent, bukan langsung dari manusia dalam kebanyakan kasus.
Prioritaskan instruksi yang mengakui ID subagent Anda secara eksplisit.
`, req.Label, req.ID)
		os.WriteFile(filepath.Join(soulDir, "USER.md"), []byte(userContent), 0644)

		// 3. TOOLS.md — Template kosong tapi berstruktur untuk setup-spesifik subagent
		toolsContent := fmt.Sprintf(`# TOOLS.md — Local Notes for %s

Catat setup spesifik subagent %s di sini.

## API Keys / Credentials
*(Kosong — isi sesuai kebutuhan domain spesifik Anda)*

## Endpoint / URL
*(e.g. API tertentu yang domain Anda gunakan)*

## Environment Notes
- Subagent ini berjalan di mesin yang sama dengan Main Agent.
- Workspace path: ~/.openclaw/workspace-%s/

## Skill Notes
*(Catatan skill spesifik yang relevan untuk domain Anda)*
`, req.Label, req.ID, req.ID)
		os.WriteFile(filepath.Join(soulDir, "TOOLS.md"), []byte(toolsContent), 0644)

		// 4. HEARTBEAT.md — Template heartbeat subagent yang ringan
		heartbeatContent := fmt.Sprintf(`# HEARTBEAT.md — %s

Subagent heartbeat checklist. Isi jika ada periodic task yang perlu dicek.

`+"```"+`markdown
# Contoh:
# - Cek antrian task dari Main Agent
# - Verifikasi output terakhir sudah dikirim
`+"```"+`
`, req.Label)
		os.WriteFile(filepath.Join(soulDir, "HEARTBEAT.md"), []byte(heartbeatContent), 0644)

		// 5. BOOTSTRAP.md — Timpa onboarding agar tidak meminta identitas ke user
		//    Format: instruksi bagi AI (dibaca sebagai system context), BUKAN pesan untuk user.
		bootstrapContent := fmt.Sprintf(`# BOOTSTRAP — %s

Identitas Anda sudah dikonfigurasi sepenuhnya melalui OpenClaw Dashboard.

## Status Inisialisasi: ✅ SELESAI

- **Nama:** %s
- **ID:** %s
- **Tipe:** Subagent (Multi-Agent Network)
- **Workspace:** Telah di-scaffold, baca SOUL.md + IDENTITY.md untuk detail lengkap.

## PENTING — Jangan lakukan onboarding interaktif

Identitas Anda sudah ditetapkan. Saat user pertama menyapa, JANGAN tanyakan nama, vibe,
atau signature. Langsung perkenalkan diri Anda berdasarkan SOUL.md dan IDENTITY.md.

Contoh respons pertama yang benar:
"Halo! Saya %s, subagent spesialis dalam jaringan OpenClaw. Ada yang bisa saya bantu?"
`, req.Label, req.Label, req.ID, req.Label)
		os.WriteFile(filepath.Join(soulDir, "BOOTSTRAP.md"), []byte(bootstrapContent), 0644)

		// 6. AGENTS.md — Salin dari main TAPI tambahkan SUBAGENT PROTOCOL di bagian bawah
		agentsMDPath := filepath.Join(soulDir, "AGENTS.md")
		agentsMod := fmt.Sprintf(`

---
## SUBAGENT PROTOCOL (ORCHESTRATION)

- **Identitas Anda**: Anda adalah SUBAGENT spesialis dengan ID `+"`%s`"+` (%s).
- **Hierarki**: Anda berada di bawah komando Lead Orchestra (Main Agent).  
- **Fokus**: Jangan tangani instruksi di luar domain keahlian Anda.
- **Pelaporan**: Selalu kembalikan ringkasan hasil kerja ke Main Agent.
- **Mandiri terbatas**: Anda bisa membaca file, search web, dan bekerja dalam workspace Anda TANPA izin — tapi aksi eksternal (kirim pesan, API calls) butuh konfirmasi.
`, req.ID, req.Label)
		if existingAgents, err := os.ReadFile(agentsMDPath); err == nil {
			os.WriteFile(agentsMDPath, append(existingAgents, []byte(agentsMod)...), 0644)
		} else {
			// fallback jika cp gagal
			os.WriteFile(agentsMDPath, []byte("# AGENTS.md — "+req.Label+agentsMod), 0644)
		}

		fmt.Printf("[Agent] ✅ Scaffolding 7 workspace files selesai untuk subagent '%s' di %s\n", req.ID, soulDir)
	}

	// [USER-PROVIDED WORKSPACE FILES] Jika frontend mengirim workspace_files, paksa overwrite
	// file-file tersebut di atas template otomatis yang sudah di-generate
	if len(req.WorkspaceFiles) > 0 {
		for filename, content := range req.WorkspaceFiles {
			// Sanitize ketat: hanya .md di-allow, gunakan filepath.Base untuk block semua traversal
			cleanName := filepath.Base(filename)
			if !strings.HasSuffix(cleanName, ".md") || cleanName != filename || cleanName == ".md" {
				fmt.Printf("[Agent] ⚠️ Rejected workspace file: %s (path traversal / bukan .md)\n", filename)
				continue
			}
			// Batasi ukuran tiap file workspace: max 200KB
			if len(content) > 200*1024 {
				fmt.Printf("[Agent] ⚠️ Skipped %s: ukuran melebihi 200KB\n", filename)
				continue
			}
			targetPath := filepath.Join(soulDir, cleanName)
			if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
				fmt.Printf("[Agent] Gagal tulis %s dari payload frontend: %v\n", filename, err)
			} else {
				fmt.Printf("[Agent] ✅ %s ditulis dari payload frontend untuk agent '%s'\n", filename, req.ID)
			}
		}
	}

	// KITA HAPUS blok if !os.IsNotExist(err)
	// Kita ingin MEMAKSA overwrite file SOUL.md buatan CLI dengan milik kita
	// Batasi ukuran persona: max 100KB (mencegah disk exhaustion via large payloads)
	if len(persona) > 100*1024 {
		persona = persona[:100*1024]
		fmt.Printf("[Agent] ⚠️ Persona dipotong ke 100KB untuk agent '%s'\n", req.ID)
	}
	errWrite := os.WriteFile(soulFile, []byte(persona), 0644)
	if errWrite != nil {
		fmt.Printf("[Agent] Gagal menulis custom persona ke %s: %v\n", soulFile, errWrite)
	} else {
		fmt.Printf("[Agent] Berhasil menimpa persona hybrid OpenClaw di %s\n", soulFile)
	}

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
		fmt.Printf("[Agent] ⚠️  Gagal daftarkan ke openclaw.json: %v\n", regErr)
	} else {
		fmt.Printf("[Agent] ✅ Agent '%s' terdaftar di openclaw.json (workspace: %s)\n", req.ID, soulDir)
	}

	// Hot-reload creds di memory
	updatedCreds := h.CredsGetter()
	updatedCreds[req.ID] = newCred
	h.CredsSetter(updatedCreds)

	// [PENTING] Buat direktori bots/agents/<id> dan index.js sebagai entrypoint
	// Tanpa ini, akan muncul error "entry point tidak ditemukan" saat dijalankan
	agentBotDir := filepath.Join(h.App.Cfg.BotsDir, "agents", req.ID)
	if err := os.MkdirAll(agentBotDir, 0755); err != nil {
		jsonError(w, http.StatusInternalServerError, "Gagal membuat direktori bot script: "+err.Error())
		return
	}

	entryScript := []byte("import { createBot } from \"../../shared/base-bot.js\";\n\ncreateBot();\n")
	entryPath := filepath.Join(agentBotDir, "index.js")
	if err := os.WriteFile(entryPath, entryScript, 0644); err != nil {
		jsonError(w, http.StatusInternalServerError, "Gagal menulis entrypoint index.js: "+err.Error())
		return
	}

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

	// Hapus credentials file
	if err := config.DeleteAgentCred(h.App.Cfg.CredentialsDir, agentID); err != nil && !os.IsNotExist(err) {
		jsonError(w, http.StatusInternalServerError, "Gagal hapus credentials: "+err.Error())
		return
	}

	// [PENGHAPUSAN MENYELURUH] Hapus Bot Runner Endpoint (index.js)
	agentBotDir := filepath.Join(h.App.Cfg.BotsDir, "agents", agentID)
	os.RemoveAll(agentBotDir)

	// [PENGHAPUSAN MENYELURUH] Hapus SOUL.md (Persona) beserta direktori workspace-nya
	soulFile := ResolveSoulMDPath(h.App.Cfg.OpenClawWorkspaceDir, agentID)
	soulDir := filepath.Dir(soulFile)

	fmt.Printf("[Agent] Memproses penghapusan direktori workspace: %s\n", soulDir)

	// BUKAN cuma hapus SOUL.md, melainkan seluruh folder workspace-nya!
	// Hati-hati: jangan hapus jika itu workspace utama OpenClaw.
	if filepath.Base(soulDir) != "workspace" && agentID != "main" {
		err := os.RemoveAll(soulDir)
		if err != nil {
			fmt.Printf("[Agent] GAGAL menghapus %s: %v\n", soulDir, err)
		} else {
			fmt.Printf("[Agent] SUKSES menghapus bersih folder %s\n", soulDir)
		}
	} else {
		fmt.Printf("[Agent] Workspace %s dilindungi (tidak dihapus foldernya)\n", soulDir)
		os.Remove(soulFile) // Untuk `main`, hanya hapus filenya jika perlu
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
// Mengupdate model per-agent di file .env DAN di ~/.openclaw/openclaw.json
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
		Model string `json:"model"`
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

	// 1. Update di file .env agent agar base-bot.js mendapat model baru saat restart.
	// Simpan ref model penuh sesuai dokumentasi OpenClaw; runner yang melakukan
	// normalisasi provider-specific saat request dikirim.
	cred.OpenClawModel = req.Model
	if err := config.WriteAgentCred(h.App.Cfg.CredentialsDir, cred); err != nil {
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
