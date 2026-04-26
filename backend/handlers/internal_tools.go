package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"multiagent-platform/internal/security"
)

type InternalToolsHandler struct {
	App          *App
	Agents       *AgentsHandler
	ToolToken    string
	ToolsEnabled bool
}

func (h *InternalToolsHandler) CreateTelegramBot(w http.ResponseWriter, r *http.Request) {
	if !h.requireInternal(w, r) {
		return
	}
	var body struct {
		Name     string `json:"name"`
		Username string `json:"username"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Name == "" || body.Username == "" {
		jsonError(w, http.StatusBadRequest, "name dan username wajib diisi")
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
		BotUsername string `json:"bot_username"`
		AgentID     string `json:"agent_id"`
		AgentLabel  string `json:"agent_label"`
		Persona     string `json:"persona"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "payload deploy tidak valid")
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
