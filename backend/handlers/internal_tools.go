package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"multiagent-platform/internal/openclaw"
	"multiagent-platform/internal/security"
)

type InternalToolsHandler struct {
	App          *App
	Agents       *AgentsHandler
	ToolToken    string
	ToolsEnabled bool
}

func (h *InternalToolsHandler) ListOpenClawCommands(w http.ResponseWriter, r *http.Request) {
	if !h.requireInternal(w, r) {
		return
	}
	if !h.requireMainAgentFromQuery(w, r) {
		return
	}
	jsonOK(w, map[string]interface{}{"commands": openclaw.GetCommandDefinitions()})
}

func (h *InternalToolsHandler) RunOpenClawCommand(w http.ResponseWriter, r *http.Request) {
	if !h.requireInternal(w, r) {
		return
	}
	var body struct {
		RequestingAgentID string            `json:"requesting_agent_id"`
		ID                string            `json:"id"`
		Args              map[string]string `json:"args"`
		Confirm           bool              `json:"confirm"`
		Reason            string            `json:"reason"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "payload command tidak valid")
		return
	}
	agentID := strings.ToLower(strings.TrimSpace(body.RequestingAgentID))
	if body.Args == nil {
		body.Args = map[string]string{}
	}
	if agentID != "main" {
		req := ApprovalRequest{
			RequestingAgentID: agentID,
			ActionType:        "openclaw_command",
			ToolName:          "run_openclaw_command",
			CommandID:         strings.TrimSpace(body.ID),
			RiskLevel:         "high",
			Reason:            strings.TrimSpace(body.Reason),
			rawArgs:           body.Args,
		}
		if !validAgentID.MatchString(agentID) || agentID == "" {
			jsonError(w, http.StatusBadRequest, "requesting_agent_id tidak valid")
			return
		}
		created := h.App.Approvals.Create(req)
		h.auditInternalTool(agentID, "approval_required", body.ID, created.ID)
		jsonOK(w, map[string]interface{}{"approval_required": true, "approval": created})
		return
	}
	result, err := openclaw.RunCommand(r.Context(), openclaw.CommandRunRequest{ID: body.ID, Args: body.Args, Confirm: body.Confirm})
	if err != nil && result.ID == "" {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
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
	h.auditInternalTool(agentID, resultStatus(result, err), body.ID, "")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(result)
}

func (h *InternalToolsHandler) CreateApprovalRequest(w http.ResponseWriter, r *http.Request) {
	if !h.requireInternal(w, r) {
		return
	}
	req, err := decodeApprovalRequestBody(r)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	created := h.App.Approvals.Create(req)
	h.auditInternalTool(req.RequestingAgentID, "approval_pending", req.CommandID, created.ID)
	jsonOK(w, map[string]interface{}{"approval": created})
}

func (h *InternalToolsHandler) ListInternalApprovals(w http.ResponseWriter, r *http.Request) {
	if !h.requireInternal(w, r) || !h.requireMainAgentFromQuery(w, r) {
		return
	}
	jsonOK(w, map[string]interface{}{"approvals": h.App.Approvals.List(strings.TrimSpace(r.URL.Query().Get("status")))})
}

func (h *InternalToolsHandler) ApproveInternal(w http.ResponseWriter, r *http.Request) {
	if !h.requireInternal(w, r) || !h.requireMainAgentFromQuery(w, r) {
		return
	}
	(&ApprovalsHandler{App: h.App}).approve(w, r, approvalIDFromPath(r.URL.Path, "approve"), "main")
}

func (h *InternalToolsHandler) RejectInternal(w http.ResponseWriter, r *http.Request) {
	if !h.requireInternal(w, r) || !h.requireMainAgentFromQuery(w, r) {
		return
	}
	id := approvalIDFromPath(r.URL.Path, "reject")
	item, err := h.App.Approvals.Decide(id, "rejected", "main", nil)
	if err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	jsonOK(w, item)
}

func (h *InternalToolsHandler) CreateTelegramBot(w http.ResponseWriter, r *http.Request) {
	if !h.requireInternal(w, r) {
		return
	}
	var body struct {
		RequestingAgentID string `json:"requesting_agent_id"`
		Name              string `json:"name"`
		Username          string `json:"username"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Name == "" || body.Username == "" {
		jsonError(w, http.StatusBadRequest, "name dan username wajib diisi")
		return
	}
	if strings.ToLower(strings.TrimSpace(body.RequestingAgentID)) != "main" {
		jsonError(w, http.StatusForbidden, "hanya Main Agent yang boleh menjalankan action tool langsung")
		return
	}
	name, err := validateTelegramText(body.Name, 64, "name")
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	username, err := normalizeBotUsername(body.Username)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := ubProc.ipcRequest(map[string]interface{}{
		"cmd":      "createBot",
		"name":     name,
		"username": username,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "tool backend gagal memproses request")
		return
	}
	if resp["ok"] != true {
		jsonError(w, http.StatusBadRequest, security.MaskKnownSecrets(fmt.Sprintf("%v", resp["error"])))
		return
	}
	jsonOK(w, map[string]interface{}{
		"success":   true,
		"username":  username,
		"has_token": true,
		"message":   "bot created; token retained inside backend tool flow",
	})
}

func (h *InternalToolsHandler) DeploySubAgent(w http.ResponseWriter, r *http.Request) {
	if !h.requireInternal(w, r) {
		return
	}
	var body struct {
		RequestingAgentID string `json:"requesting_agent_id"`
		BotUsername       string `json:"bot_username"`
		AgentID           string `json:"agent_id"`
		AgentLabel        string `json:"agent_label"`
		Persona           string `json:"persona"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "payload deploy tidak valid")
		return
	}
	if strings.ToLower(strings.TrimSpace(body.RequestingAgentID)) != "main" {
		jsonError(w, http.StatusForbidden, "hanya Main Agent yang boleh menjalankan action tool langsung")
		return
	}
	body.AgentID = strings.ToLower(strings.TrimSpace(body.AgentID))
	body.AgentLabel = strings.TrimSpace(body.AgentLabel)
	if !validAgentID.MatchString(body.AgentID) {
		jsonError(w, http.StatusBadRequest, "agent_id tidak valid")
		return
	}
	if body.AgentLabel == "" {
		body.AgentLabel = body.AgentID
	}
	username, err := normalizeBotUsername(body.BotUsername)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	tokenResp, err := ubProc.ipcRequest(map[string]interface{}{"cmd": "getBotToken", "username": username})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "tool backend gagal memproses request")
		return
	}
	if tokenResp["ok"] != true {
		jsonError(w, http.StatusBadRequest, security.MaskKnownSecrets(fmt.Sprintf("%v", tokenResp["error"])))
		return
	}
	data, _ := tokenResp["data"].(map[string]interface{})
	botToken, _ := data["token"].(string)
	if botToken == "" {
		jsonError(w, http.StatusBadRequest, "token bot tidak tersedia")
		return
	}

	createPayload := map[string]interface{}{
		"id":                 body.AgentID,
		"label":              body.AgentLabel,
		"telegram_bot_token": botToken,
		"persona":            body.Persona,
	}
	createBody, _ := json.Marshal(createPayload)
	createReq := httptest.NewRequest(http.MethodPost, "/api/agents", bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	h.Agents.CreateAgent(createRec, createReq)
	if createRec.Code >= 300 {
		jsonError(w, createRec.Code, "gagal membuat agent dari tool internal")
		return
	}

	startReq := httptest.NewRequest(http.MethodPost, "/api/agents/"+body.AgentID+"/start", nil)
	startRec := httptest.NewRecorder()
	h.Agents.StartAgent(startRec, startReq)
	if startRec.Code >= 300 {
		jsonError(w, startRec.Code, "agent dibuat tetapi gagal dijalankan")
		return
	}

	jsonOK(w, map[string]interface{}{
		"success":  true,
		"agent_id": body.AgentID,
		"status":   "started",
		"message":  "sub-agent deployed; token stored in credentials",
	})
}

func (h *InternalToolsHandler) requireMainAgentFromQuery(w http.ResponseWriter, r *http.Request) bool {
	if strings.ToLower(strings.TrimSpace(r.URL.Query().Get("agent_id"))) != "main" {
		jsonError(w, http.StatusForbidden, "hanya Main Agent yang boleh memakai tool ini")
		return false
	}
	return true
}

func (h *InternalToolsHandler) auditInternalTool(agentID, status, commandID, approvalID string) {
	if h == nil || h.App == nil || h.App.Security == nil {
		return
	}
	h.App.Security.EnqueueAudit(security.AuditEvent{
		Level:   "INFO",
		AgentID: agentID,
		Channel: "agent_tool",
		Status:  status,
		Message: "internal agent tool run",
		Metadata: map[string]interface{}{
			"command_id":  commandID,
			"approval_id": approvalID,
		},
	})
}

func resultStatus(result openclaw.CommandRunResult, err error) string {
	if result.ConfirmationRequired {
		return "confirmation_required"
	}
	if result.Error != "" || err != nil {
		if result.Error == "blocked_by_policy" {
			return "blocked"
		}
		return "error"
	}
	return "success"
}

func (h *InternalToolsHandler) requireInternal(w http.ResponseWriter, r *http.Request) bool {
	if h.App.InternalRPCToken == "" || extractBearerToken(r) != h.App.InternalRPCToken {
		jsonError(w, http.StatusUnauthorized, "internal token diperlukan")
		return false
	}
	if !h.ToolsEnabled {
		jsonError(w, http.StatusForbidden, "internal agent tools disabled")
		return false
	}
	if h.ToolToken == "" || r.Header.Get("X-OpenClaw-Tool-Capability") != h.ToolToken {
		jsonError(w, http.StatusForbidden, "tool capability diperlukan")
		return false
	}
	return true
}
