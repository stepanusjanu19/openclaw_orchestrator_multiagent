package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"multiagent-platform/internal/security"
)

type SecurityHandler struct {
	App *App
}

func (h *SecurityHandler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, h.App.Security.GetPolicy())
}

func (h *SecurityHandler) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	var req security.Policy
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "payload policy tidak valid")
		return
	}
	policy, err := h.App.Security.UpdatePolicy(req)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.App.Security.EnqueueAudit(security.AuditEvent{
		Level:   "SECURITY",
		Status:  "success",
		Message: "security policy updated",
	})
	jsonOK(w, policy)
}

func (h *SecurityHandler) GetRules(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, h.App.Security.GetRules())
}

func (h *SecurityHandler) UpdateRules(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rules []security.MaskingRule `json:"rules"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "payload ruleset tidak valid")
		return
	}
	rs, err := h.App.Security.UpdateRules(req.Rules)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.App.Security.EnqueueAudit(security.AuditEvent{
		Level:   "SECURITY",
		Status:  "success",
		Message: "masking rules updated",
	})
	jsonOK(w, rs)
}

func (h *SecurityHandler) QueryAuditLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	from, err := parseOptionalTime(r.URL.Query().Get("from"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "format from harus RFC3339")
		return
	}
	to, err := parseOptionalTime(r.URL.Query().Get("to"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "format to harus RFC3339")
		return
	}
	rows, err := h.App.Security.QueryAudit(security.AuditQuery{
		AgentID: strings.TrimSpace(r.URL.Query().Get("agent_id")),
		Level:   strings.TrimSpace(r.URL.Query().Get("level")),
		Channel: strings.TrimSpace(r.URL.Query().Get("channel")),
		Status:  strings.TrimSpace(r.URL.Query().Get("status")),
		Query:   strings.TrimSpace(r.URL.Query().Get("q")),
		From:    from,
		To:      to,
		Limit:   limit,
	})
	if err != nil {
		if strings.Contains(err.Error(), "tidak valid") {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonError(w, http.StatusInternalServerError, "gagal query audit log: "+err.Error())
		return
	}
	jsonOK(w, map[string]interface{}{"logs": rows})
}

func (h *SecurityHandler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]interface{}{"templates": h.App.Security.ListTemplates()})
}

func (h *SecurityHandler) SaveTemplate(w http.ResponseWriter, r *http.Request) {
	var req security.Template
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "payload template tidak valid")
		return
	}
	tpl, err := h.App.Security.UpsertTemplate(req)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.App.Security.EnqueueAudit(security.AuditEvent{
		Level:   "SECURITY",
		Status:  "success",
		Message: "template saved",
		Metadata: map[string]interface{}{
			"template_id": tpl.ID,
		},
	})
	jsonOK(w, tpl)
}

func (h *SecurityHandler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/security/templates/"), "/")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "template id diperlukan")
		return
	}
	if err := h.App.Security.DeleteTemplate(id); err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	h.App.Security.EnqueueAudit(security.AuditEvent{
		Level:   "SECURITY",
		Status:  "success",
		Message: "template deleted",
		Metadata: map[string]interface{}{
			"template_id": id,
		},
	})
	jsonOK(w, map[string]string{"message": "template deleted"})
}

func (h *SecurityHandler) ValidateTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "payload template tidak valid")
		return
	}
	jsonOK(w, security.ValidateTemplate(req.Content))
}

func (h *SecurityHandler) RenderTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TemplateID string                 `json:"template_id"`
		Values     map[string]interface{} `json:"values"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "payload render tidak valid")
		return
	}
	out, err := h.App.Security.RenderTemplate(req.TemplateID, req.Values)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	masked := h.App.Security.Mask(out, "")
	if masked.Blocked {
		jsonError(w, http.StatusForbidden, masked.Error)
		return
	}
	jsonOK(w, map[string]interface{}{"output": masked.Text, "masking": masked})
}

func (h *SecurityHandler) InternalMask(w http.ResponseWriter, r *http.Request) {
	if !h.requireInternal(w, r) {
		return
	}
	var req struct {
		Text string `json:"text"`
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "payload mask tidak valid")
		return
	}
	result := h.App.Security.Mask(req.Text, req.Mode)
	status := http.StatusOK
	if result.Blocked {
		status = http.StatusForbidden
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(result)
}

func (h *SecurityHandler) InternalAudit(w http.ResponseWriter, r *http.Request) {
	if !h.requireInternal(w, r) {
		return
	}
	var req security.AuditEvent
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "payload audit tidak valid")
		return
	}
	if _, err := security.SanitizeAuditEvent(req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.App.Security.EnqueueAudit(req)
	jsonOK(w, map[string]string{"status": "queued"})
}

func (h *SecurityHandler) requireInternal(w http.ResponseWriter, r *http.Request) bool {
	if h.App.InternalRPCToken == "" || extractBearerToken(r) != h.App.InternalRPCToken {
		jsonError(w, http.StatusUnauthorized, "internal token diperlukan")
		return false
	}
	return true
}

func parseOptionalTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, raw)
}
