package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"multiagent-platform/internal/openclaw"
	"multiagent-platform/internal/security"
)

type ApprovalRequest struct {
	ID                string                     `json:"id"`
	RequestingAgentID string                     `json:"requesting_agent_id"`
	ActionType        string                     `json:"action_type"`
	ToolName          string                     `json:"tool_name"`
	CommandID         string                     `json:"command_id,omitempty"`
	ArgsPreview       map[string]string          `json:"args_preview,omitempty"`
	RiskLevel         string                     `json:"risk_level"`
	Reason            string                     `json:"reason,omitempty"`
	Status            string                     `json:"status"`
	DecisionBy        string                     `json:"decision_by,omitempty"`
	CreatedAt         time.Time                  `json:"created_at"`
	ExpiresAt         time.Time                  `json:"expires_at"`
	DecidedAt         *time.Time                 `json:"decided_at,omitempty"`
	Result            *openclaw.CommandRunResult `json:"result,omitempty"`
	rawArgs           map[string]string
}

type ApprovalStore struct {
	mu    sync.Mutex
	items map[string]*ApprovalRequest
}

func NewApprovalStore() *ApprovalStore {
	return &ApprovalStore{items: map[string]*ApprovalRequest{}}
}

func (s *ApprovalStore) Create(req ApprovalRequest) ApprovalRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	req.ID = fmt.Sprintf("apr-%d", now.UnixNano())
	req.Status = "pending"
	req.CreatedAt = now
	req.ExpiresAt = now.Add(30 * time.Minute)
	req.ArgsPreview = redactedArgs(req.rawArgs)
	copied := req
	s.items[req.ID] = &copied
	return copied
}

func (s *ApprovalStore) List(status string) []ApprovalRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	out := []ApprovalRequest{}
	for _, item := range s.items {
		if item.Status == "pending" && now.After(item.ExpiresAt) {
			item.Status = "expired"
		}
		if status == "" || item.Status == status {
			out = append(out, *item)
		}
	}
	return out
}

func (s *ApprovalStore) Get(id string) (*ApprovalRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok {
		return nil, false
	}
	if item.Status == "pending" && time.Now().After(item.ExpiresAt) {
		item.Status = "expired"
	}
	copied := *item
	return &copied, true
}

func (s *ApprovalStore) Decide(id, status, decisionBy string, result *openclaw.CommandRunResult) (*ApprovalRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok {
		return nil, fmt.Errorf("approval tidak ditemukan")
	}
	if item.Status == "pending" && time.Now().After(item.ExpiresAt) {
		item.Status = "expired"
	}
	if item.Status != "pending" {
		return nil, fmt.Errorf("approval sudah %s", item.Status)
	}
	now := time.Now()
	item.Status = status
	item.DecisionBy = decisionBy
	item.DecidedAt = &now
	item.Result = result
	copied := *item
	return &copied, nil
}

type ApprovalsHandler struct {
	App *App
}

func (h *ApprovalsHandler) ListApprovals(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]interface{}{"approvals": h.App.Approvals.List(strings.TrimSpace(r.URL.Query().Get("status")))})
}

func (h *ApprovalsHandler) ApproveDashboard(w http.ResponseWriter, r *http.Request) {
	id := approvalIDFromPath(r.URL.Path, "approve")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "approval id diperlukan")
		return
	}
	h.approve(w, r, id, "dashboard")
}

func (h *ApprovalsHandler) RejectDashboard(w http.ResponseWriter, r *http.Request) {
	id := approvalIDFromPath(r.URL.Path, "reject")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "approval id diperlukan")
		return
	}
	item, err := h.App.Approvals.Decide(id, "rejected", "dashboard", nil)
	if err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	h.auditApproval(*item, "rejected")
	jsonOK(w, item)
}

func (h *ApprovalsHandler) approve(w http.ResponseWriter, r *http.Request, id, decisionBy string) {
	item, ok := h.App.Approvals.Get(id)
	if !ok {
		jsonError(w, http.StatusNotFound, "approval tidak ditemukan")
		return
	}
	result, err := executeApprovedAction(r, item)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if result != nil && result.Error == "blocked_by_policy" {
		updated, decideErr := h.App.Approvals.Decide(id, "rejected", decisionBy, result)
		if decideErr != nil {
			jsonError(w, http.StatusConflict, decideErr.Error())
			return
		}
		h.auditApproval(*updated, "blocked")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(updated)
		return
	}
	updated, err := h.App.Approvals.Decide(id, "executed", decisionBy, result)
	if err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	h.auditApproval(*updated, "executed")
	jsonOK(w, updated)
}

func (h *ApprovalsHandler) auditApproval(item ApprovalRequest, status string) {
	if h.App == nil || h.App.Security == nil {
		return
	}
	h.App.Security.EnqueueAudit(security.AuditEvent{
		Level:   "INFO",
		AgentID: item.RequestingAgentID,
		Channel: "approval",
		Status:  status,
		Message: "agent approval lifecycle",
		Metadata: map[string]interface{}{
			"approval_id": item.ID,
			"tool_name":   item.ToolName,
			"command_id":  item.CommandID,
			"decision_by": item.DecisionBy,
		},
	})
}

func executeApprovedAction(r *http.Request, item *ApprovalRequest) (*openclaw.CommandRunResult, error) {
	if item.ActionType != "openclaw_command" && item.ToolName != "run_openclaw_command" {
		return nil, fmt.Errorf("approval action belum punya executor: %s", item.ToolName)
	}
	result, err := openclaw.RunCommand(r.Context(), openclaw.CommandRunRequest{
		ID:      item.CommandID,
		Args:    item.rawArgs,
		Confirm: true,
	})
	if err != nil && result.ID == "" {
		return nil, err
	}
	return &result, nil
}

func approvalIDFromPath(path, action string) string {
	path = strings.Trim(path, "/")
	suffix := "/" + action
	if !strings.HasSuffix(path, suffix) {
		return ""
	}
	parts := strings.Split(path[:len(path)-len(suffix)], "/")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

func redactedArgs(args map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range args {
		out[k] = openclaw.RedactCommandOutput(v)
	}
	return out
}

func decodeApprovalRequestBody(r *http.Request) (ApprovalRequest, error) {
	var body struct {
		RequestingAgentID string            `json:"requesting_agent_id"`
		ActionType        string            `json:"action_type"`
		ToolName          string            `json:"tool_name"`
		CommandID         string            `json:"command_id"`
		Args              map[string]string `json:"args"`
		RiskLevel         string            `json:"risk_level"`
		Reason            string            `json:"reason"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		return ApprovalRequest{}, err
	}
	agentID := strings.ToLower(strings.TrimSpace(body.RequestingAgentID))
	if !validAgentID.MatchString(agentID) || agentID == "main" {
		return ApprovalRequest{}, fmt.Errorf("requesting_agent_id tidak valid")
	}
	if body.Args == nil {
		body.Args = map[string]string{}
	}
	actionType := strings.TrimSpace(body.ActionType)
	if actionType == "" {
		actionType = "openclaw_command"
	}
	toolName := strings.TrimSpace(body.ToolName)
	if toolName == "" {
		toolName = "run_openclaw_command"
	}
	risk := strings.TrimSpace(body.RiskLevel)
	if risk == "" {
		risk = "medium"
	}
	return ApprovalRequest{
		RequestingAgentID: agentID,
		ActionType:        actionType,
		ToolName:          toolName,
		CommandID:         strings.TrimSpace(body.CommandID),
		RiskLevel:         risk,
		Reason:            strings.TrimSpace(body.Reason),
		rawArgs:           body.Args,
	}, nil
}
