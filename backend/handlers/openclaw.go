// handlers/openclaw.go
// Endpoint untuk probe OpenClaw gateway dan detect existing agents.

package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"multiagent-platform/internal/openclaw"
	"multiagent-platform/internal/security"
	"net/http"
	"strings"
	"time"
)

// OpenClawHandler untuk probe dan detection endpoints.
type OpenClawHandler struct {
	GatewayURL    string
	GatewayToken  string
	WorkspaceDir  string
	Security      *security.Manager
	KnownAgentIDs func() []string // callback untuk dapat daftar agent yang sudah registered
}

// OCStatusResponse adalah response dari /api/openclaw/status.
type OCStatusResponse struct {
	Reachable       bool                     `json:"reachable"`
	GatewayURL      string                   `json:"gateway_url"`
	Models          []string                 `json:"models,omitempty"`
	DetectedAgents  []openclaw.DetectedAgent `json:"detected_agents,omitempty"`
	ImportableCount int                      `json:"importable_count"`
	Error           string                   `json:"error,omitempty"`
	CheckedAt       time.Time                `json:"checked_at"`
}

// ModelsResponse adalah response dari /v1/models endpoint OpenClaw.
type ModelsResponse struct {
	Object string `json:"object"`
	Data   []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// GetStatus handler untuk GET /api/openclaw/status.
// Probe gateway, list models, dan detect agents dari openclaw.json.
func (h *OpenClawHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	resp := OCStatusResponse{
		GatewayURL: h.GatewayURL,
		CheckedAt:  time.Now(),
	}

	// ── Probe gateway ─────────────────────────────────────────────────────
	baseURL := extractBaseURL(h.GatewayURL)
	modelsURL := baseURL + "/v1/models"

	hc := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", modelsURL, nil)
	req.Header.Set("Authorization", "Bearer "+h.GatewayToken)

	res, err := hc.Do(req)
	if err != nil {
		resp.Error = "Gateway tidak dapat dihubungi: " + err.Error()
		jsonOK(w, resp)
		return
	}
	defer res.Body.Close()

	resp.Reachable = res.StatusCode < 500

	// Parse models list
	if res.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		var mr ModelsResponse
		if err := json.Unmarshal(body, &mr); err == nil {
			for _, m := range mr.Data {
				if strings.HasPrefix(m.ID, "openclaw/") {
					resp.Models = append(resp.Models, m.ID)
				}
			}
		}
	}

	// ── Baca openclaw.json untuk detect existing agents ───────────────────
	detected, err := openclaw.DetectAgents(h.WorkspaceDir)
	if err == nil {
		resp.DetectedAgents = detected

		// Hitung berapa agent yang bisa di-import (belum ada di system kita)
		knownIDs := h.KnownAgentIDs()
		knownSet := make(map[string]bool)
		for _, id := range knownIDs {
			knownSet[id] = true
		}

		for _, d := range detected {
			if !knownSet[d.ID] {
				resp.ImportableCount++
			}
		}
	} else {
		// openclaw.json mungkin belum ada, tidak fatal
		if resp.Error == "" {
			resp.Error = fmt.Sprintf("openclaw.json: %v", err)
		}
	}

	jsonOK(w, resp)
}

// ImportAgent handler untuk POST /api/openclaw/import/:id.
// Import agent yang terdeteksi dari openclaw.json ke dalam platform kita.
func (h *OpenClawHandler) ImportAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract agent ID dari path /api/openclaw/import/:id
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		jsonError(w, http.StatusBadRequest, "agent ID diperlukan")
		return
	}
	agentID := parts[3]

	// Detect agents dari openclaw.json
	detected, err := openclaw.DetectAgents(h.WorkspaceDir)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Gagal baca openclaw.json: "+err.Error())
		return
	}

	// Cari agent dengan ID yang diminta
	var found *openclaw.DetectedAgent
	for i, d := range detected {
		if d.ID == agentID {
			found = &detected[i]
			break
		}
	}

	if found == nil {
		jsonError(w, http.StatusNotFound, fmt.Sprintf("Agent '%s' tidak ditemukan di openclaw.json", agentID))
		return
	}

	// Return data agent untuk diisi oleh frontend (token mungkin sudah ada)
	jsonOK(w, map[string]interface{}{
		"agent":   found,
		"message": "Data agent berhasil dibaca dari openclaw.json",
	})
}

// GetCommands handler untuk GET /api/openclaw/commands.
func (h *OpenClawHandler) GetCommands(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]interface{}{
		"commands": openclaw.GetCommandDefinitions(),
	})
}

// RunCommand handler untuk POST /api/openclaw/commands/run.
func (h *OpenClawHandler) RunCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var req openclaw.CommandRunRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Request body tidak valid: "+err.Error())
		return
	}
	if req.Args == nil {
		req.Args = map[string]string{}
	}
	result, err := openclaw.RunCommand(r.Context(), req)
	if err != nil && result.ID == "" {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.auditOpenClawCommand(result, err)
	status := http.StatusOK
	if err != nil {
		status = http.StatusBadRequest
	}
	if result.Error == "blocked_by_policy" {
		status = http.StatusForbidden
	}
	if result.Error == "confirmation_required" {
		status = http.StatusConflict
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(result)
}

func (h *OpenClawHandler) auditOpenClawCommand(result openclaw.CommandRunResult, err error) {
	if h == nil || h.Security == nil {
		return
	}
	status := "success"
	if result.ConfirmationRequired {
		status = "confirmation_required"
	} else if result.Error != "" {
		status = "error"
		if result.Error == "blocked_by_policy" || result.Error == "sensitive_config_key_blocked" {
			status = "blocked"
		}
	}
	event := security.AuditEvent{
		Level:   "INFO",
		Channel: "openclaw_cli",
		Status:  status,
		Message: "openclaw command run",
		Metadata: map[string]interface{}{
			"command_id": result.ID,
			"exit_code":  result.ExitCode,
		},
	}
	if status == "blocked" {
		event.Level = "SECURITY"
	}
	if err != nil {
		event.Level = "ERROR"
		event.ErrorClass = security.MaskKnownSecrets(err.Error())
	}
	h.Security.EnqueueAudit(event)
}

// extractBaseURL mengambil base URL dari URL lengkap.
// Contoh: "http://127.0.0.1:14000/v1/chat/completions" → "http://127.0.0.1:14000"
func extractBaseURL(fullURL string) string {
	// Hapus path setelah port
	for i, c := range fullURL {
		if i > 8 && c == '/' { // skip "http://"
			// Cek apakah ini setelah host:port
			prefix := fullURL[:i]
			if strings.Count(prefix, "/") >= 2 {
				return prefix
			}
		}
	}

	// Fallback: cari dari 8 karakter (skip "http://")
	rest := fullURL
	if idx := strings.Index(rest[8:], "/"); idx != -1 {
		return rest[:8+idx]
	}
	return rest
}
