// handlers/orchestrator.go
// Orchestrator panel — status terpadu main agent sebagai Lead Orchestrator
// beserta daftar sub-agent yang berada di bawah komandonya.

package handlers

import (
	"fmt"
	"multiagent-platform/internal/config"
	"multiagent-platform/internal/process"
	"net/http"
	"path/filepath"
	"strings"
)

// OrchestratorHandler mengelola endpoint orchestrator.
type OrchestratorHandler struct {
	App         *App
	CredsGetter func() map[string]*config.AgentCred
	CredsSetter func(map[string]*config.AgentCred)
}

// SubAgentInfo menyimpan info satu sub-agent untuk orchestrator panel.
type SubAgentInfo struct {
	ID       string              `json:"id"`
	Label    string              `json:"label"`
	Model    string              `json:"model"`
	HasToken bool                `json:"has_token"`
	Status   process.AgentStatus `json:"status"`
	Role     string              `json:"role"` // dari SOUL.md jika bisa dibaca
}

// OrchestratorStatus adalah response dari GET /api/orchestrator/status.
type OrchestratorStatus struct {
	MainAgent    AgentInfo      `json:"main_agent"`
	SubAgents    []SubAgentInfo `json:"sub_agents"`
	TotalSubs    int            `json:"total_sub_agents"`
	RunningCount int            `json:"running_count"`
}

// GetOrchestratorStatus — GET /api/orchestrator/status
// Mengembalikan status main agent + seluruh sub-agent dalam satu response.
func (h *OrchestratorHandler) GetOrchestratorStatus(w http.ResponseWriter, r *http.Request) {
	creds := h.CredsGetter()
	mainCred, hasMain := creds["main"]

	var mainInfo AgentInfo
	if hasMain {
		status := h.App.Manager.GetStatus("main")
		status.ID = "main"
		status.Label = mainCred.Label
		mainInfo = agentInfoFromCred("main", mainCred, status)
	} else {
		// main mungkin langsung dikonfigurasi di openclaw.json tanpa cred file
		mainStatus := h.App.Manager.GetStatus("main")
		mainStatus.ID = "main"
		mainStatus.Label = "Main Orchestrator"
		mainInfo = agentInfoFromCred("main", &config.AgentCred{ID: "main", Label: "Main Orchestrator", OpenClawModel: "openclaw/main"}, mainStatus)
	}

	// Kumpulkan semua sub-agent (bukan main)
	var subAgents []SubAgentInfo
	runningCount := 0
	for id, cred := range creds {
		if id == "main" {
			continue
		}
		status := h.App.Manager.GetStatus(id)
		status.ID = id
		status.Label = cred.Label

		// Coba baca role dari SOUL.md
		role := extractRoleFromSoul(h.App.Cfg.OpenClawWorkspaceDir, id)

		sub := SubAgentInfo{
			ID:       id,
			Label:    cred.Label,
			Model:    cred.OpenClawModel,
			HasToken: cred.TelegramBotToken != "" && !strings.HasPrefix(cred.TelegramBotToken, "GANTI"),
			Status:   status,
			Role:     role,
		}
		subAgents = append(subAgents, sub)
		if status.Running {
			runningCount++
		}
	}

	jsonOK(w, OrchestratorStatus{
		MainAgent:    mainInfo,
		SubAgents:    subAgents,
		TotalSubs:    len(subAgents),
		RunningCount: runningCount,
	})
}

// UpdateOrchestratorSOUL — PUT /api/orchestrator/soul
// Update SOUL.md main agent sekaligus inject daftar sub-agent terkini.
func (h *OrchestratorHandler) UpdateOrchestratorSOUL(w http.ResponseWriter, r *http.Request) {
	creds := h.CredsGetter()

	// Bangun daftar sub-agent yang aktif
	var subList strings.Builder
	subList.WriteString("\n## Sub-Agents Under Command\n\n")
	for id, cred := range creds {
		if id == "main" {
			continue
		}
		role := extractRoleFromSoul(h.App.Cfg.OpenClawWorkspaceDir, id)
		if role == "" {
			role = "Specialist"
		}
		status := h.App.Manager.GetStatus(id)
		statusIcon := "🔴"
		if status.Running {
			statusIcon = "🟢"
		}
		subList.WriteString(fmt.Sprintf("- %s **%s** (`%s`) — Model: `%s` — Role: %s\n",
			statusIcon, cred.Label, id, cred.OpenClawModel, role))
	}

	orchestratorSoul := fmt.Sprintf(`# SOUL.md — Main Agent (Lead Orchestrator)

## Core Identity
- Saya adalah **Lead Orchestrator** dari ekosistem multi-agent ini.
- Tugas utama saya: **menerima instruksi kompleks → memecah → mendelegasikan ke sub-agent spesialis → agregasi hasil → laporan ke user**.
- Saya **TIDAK mengerjakan tugas teknis secara langsung** kecuali tidak ada sub-agent yang tepat.

## Orchestration Protocol
1. **Analisis request** — Tentukan domain: coding, riset, data, creative, dll.
2. **Pilih sub-agent** — Match domain dengan spesialisasi sub-agent yang tersedia.
3. **Delegasikan via pesan Telegram** — Format: "@{agent_id} TASK: {deskripsi tugas}"
4. **Monitor** — Tunggu konfirmasi dari sub-agent atau set deadline.
5. **Agregasi** — Kumpulkan hasil, buat laporan terpadu untuk user.

## Vibe
- Bersikap seperti **Technical Lead / Manager** berpengalaman.
- Terstruktur, direktif, efisien. Tidak bertele-tele.
- Berikan ownership yang jelas ke setiap sub-agent.

## Boundaries
- Keputusan eksternal / aksi irreversible → konfirmasi ke Human User.
- Pantau scope setiap sub-agent, pastikan tidak overlap.
- Jangan klaim tugas selesai sebelum ada laporan dari sub-agent.
%s
## Fallback
Jika tidak ada sub-agent yang sesuai → kerjakan sendiri dengan notifikasi ke user.
`, subList.String())

	// Resolve path SOUL.md main
	soulPath := ResolveSoulMDPath(h.App.Cfg.OpenClawWorkspaceDir, "main")
	if err := writeFileNoSymlink(soulPath, []byte(orchestratorSoul)); err != nil {
		jsonError(w, http.StatusInternalServerError, "Gagal update SOUL.md: "+err.Error())
		return
	}

	jsonOK(w, map[string]string{
		"message": "SOUL.md Lead Orchestrator berhasil diperbarui dengan daftar sub-agent terkini",
		"path":    soulPath,
	})
}

// extractRoleFromSoul membaca baris pertama yang mengandung "Role:" dari SOUL.md subagent.
func extractRoleFromSoul(ocWorkspaceDir, agentID string) string {
	soulPath := ResolveSoulMDPath(ocWorkspaceDir, agentID)
	data, err := readRegularFileNoSymlink(soulPath, 2<<20)
	if err != nil {
		// Coba IDENTITY.md
		identPath := filepath.Join(filepath.Dir(soulPath), "IDENTITY.md")
		data, err = readRegularFileNoSymlink(identPath, 2<<20)
		if err != nil {
			return ""
		}
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "Role:") || strings.Contains(line, "**Role:**") {
			role := strings.TrimSpace(line)
			role = strings.TrimPrefix(role, "- **Role:**")
			role = strings.TrimPrefix(role, "- Role:")
			role = strings.TrimPrefix(role, "**Role:**")
			return strings.TrimSpace(role)
		}
	}
	return ""
}
