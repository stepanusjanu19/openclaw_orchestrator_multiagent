package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"multiagent-platform/internal/airouter"
	"multiagent-platform/internal/config"
	"multiagent-platform/internal/routerruntime"
)

type RouterStatusHandler struct {
	App *App
}

func (h *RouterStatusHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	tool := strings.TrimSpace(r.URL.Query().Get("tool"))
	if tool != "" {
		h.getRuntimeToolStatus(w, tool)
		return
	}
	jsonOK(w, airouter.ProbeStatus(h.App.Cfg))
}

func (h *RouterStatusHandler) ActivateAgentRouting(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tool string `json:"tool"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "payload router tidak valid")
		return
	}
	tool := strings.TrimSpace(req.Tool)
	if !routerToolSet[tool] {
		jsonError(w, http.StatusBadRequest, "router tool harus 9router atau omniroute")
		return
	}
	snapshot, ok := runningRouterSnapshot(tool)
	if !ok {
		jsonError(w, http.StatusConflict, "router runtime tidak berjalan")
		return
	}
	if !airouter.IsLoopbackHost(snapshot.ListenHost) {
		jsonError(w, http.StatusBadRequest, "router runtime non-loopback ditolak")
		return
	}

	nextCfg := *h.App.Cfg
	nextCfg.AIRouterEnabled = true
	nextCfg.AIRouterProvider = tool
	nextCfg.AIRouterBaseURL = snapshot.Endpoint
	token, tokenOK := routerruntime.APIKey(tool, snapshot.RuntimeDir)
	tokenSource := "runtime"
	if strings.TrimSpace(token) == "" {
		jsonError(w, http.StatusConflict, "API key lokal router tidak ditemukan. Buat API key di dashboard "+routerruntime.DisplayName(tool)+", lalu refresh dan aktifkan ulang agent routing.")
		return
	}
	if !tokenOK {
		jsonError(w, http.StatusConflict, "API key lokal router tidak ditemukan. Buat API key di dashboard "+routerruntime.DisplayName(tool)+", lalu refresh dan aktifkan ulang agent routing.")
		return
	}
	nextCfg.AIRouterToken = token
	validation := airouter.Validate(&nextCfg)
	if !validation.OK {
		jsonError(w, http.StatusBadRequest, "router config ditolak: "+strings.Join(validation.Errors, "; "))
		return
	}
	status := airouter.ProbeRuntimeStatus(&nextCfg, tool, snapshot.Endpoint)
	if !status.RuntimeSupported || !status.Reachable || status.Error != "" {
		message := "router /v1/models belum reachable"
		if status.Error != "" {
			message += ": " + status.Error
		}
		jsonError(w, http.StatusConflict, message)
		return
	}

	if err := config.UpdateEnvValues(h.App.Cfg.CredentialsDir, map[string]string{
		"AI_ROUTER_ENABLED":  "true",
		"AI_ROUTER_PROVIDER": tool,
		"AI_ROUTER_BASE_URL": snapshot.Endpoint,
		"AI_ROUTER_TOKEN":    token,
	}); err != nil {
		jsonError(w, http.StatusInternalServerError, "gagal menyimpan konfigurasi router: "+err.Error())
		return
	}
	h.App.Cfg.AIRouterEnabled = true
	h.App.Cfg.AIRouterProvider = tool
	h.App.Cfg.AIRouterBaseURL = snapshot.Endpoint
	h.App.Cfg.AIRouterToken = token

	jsonOK(w, map[string]interface{}{
		"message":                 "AI Router agent routing aktif",
		"provider":                tool,
		"base_url":                airouter.MaskURL(snapshot.Endpoint),
		"token_configured":        true,
		"token_source":            tokenSource,
		"agent_routing_enabled":   true,
		"restart_agents_required": true,
		"models":                  status.ModelCount,
	})
}

func (h *RouterStatusHandler) getRuntimeToolStatus(w http.ResponseWriter, tool string) {
	if !routerToolSet[tool] {
		jsonError(w, http.StatusBadRequest, "router tool harus 9router atau omniroute")
		return
	}
	snapshot, ok := runningRouterSnapshot(tool)
	if !ok {
		jsonOK(w, airouter.Status{
			Enabled:       false,
			Provider:      tool,
			TokenSource:   tokenSource(h.App.Cfg.AIRouterToken),
			RuntimeTool:   tool,
			RuntimeActive: false,
			AgentRouting:  h.App.Cfg.AIRouterEnabled,
			Error:         "router runtime tidak berjalan",
			CheckedAt:     time.Now(),
		})
		return
	}
	if !airouter.IsLoopbackHost(snapshot.ListenHost) {
		jsonOK(w, airouter.Status{
			Enabled:       false,
			Provider:      tool,
			TokenSource:   tokenSource(h.App.Cfg.AIRouterToken),
			RuntimeTool:   tool,
			RuntimeActive: true,
			AgentRouting:  h.App.Cfg.AIRouterEnabled,
			Error:         "router runtime non-loopback ditolak",
			CheckedAt:     time.Now(),
		})
		return
	}
	probeCfg := *h.App.Cfg
	token, tokenSource := resolveRouterToken(tool, snapshot.RuntimeDir, probeCfg.AIRouterToken)
	if tokenSource == "runtime" {
		probeCfg.AIRouterToken = token
	} else if tokenSource == "missing" {
		status := airouter.ProbeRuntimeStatus(&probeCfg, tool, snapshot.Endpoint)
		status.TokenSource = "missing"
		status.Warnings = append(status.Warnings, "API key lokal router tidak ditemukan. Buat API key di dashboard "+routerruntime.DisplayName(tool)+", lalu refresh.")
		status.BinaryTrusted = snapshot.BinaryTrusted
		status.BinaryPath = snapshot.BinaryResolvedPath
		status.RuntimeIsolated = snapshot.RuntimeIsolated
		status.RuntimeDir = snapshot.RuntimeDir
		status.LoopbackEnforced = snapshot.LoopbackEnforced
		status.ProcessGroup = snapshot.ProcessGroupEnabled
		status.SecurityWarnings = append([]string{}, snapshot.SecurityWarnings...)
		status.SecurityNotices = append([]string{}, snapshot.SecurityNotices...)
		if status.Error != "" {
			status.Warnings = append(status.Warnings, "runtime /v1/models belum reachable: "+status.Error)
		}
		jsonOK(w, status)
		return
	}
	status := airouter.ProbeRuntimeStatus(&probeCfg, tool, snapshot.Endpoint)
	status.TokenSource = tokenSource
	status.TokenConfigured = tokenSource != "missing"
	status.BinaryTrusted = snapshot.BinaryTrusted
	status.BinaryPath = snapshot.BinaryResolvedPath
	status.RuntimeIsolated = snapshot.RuntimeIsolated
	status.RuntimeDir = snapshot.RuntimeDir
	status.LoopbackEnforced = snapshot.LoopbackEnforced
	status.ProcessGroup = snapshot.ProcessGroupEnabled
	status.SecurityWarnings = append([]string{}, snapshot.SecurityWarnings...)
	status.SecurityNotices = append([]string{}, snapshot.SecurityNotices...)
	if status.Error != "" {
		status.Warnings = append(status.Warnings, "runtime /v1/models belum reachable: "+status.Error)
	}
	jsonOK(w, status)
}

func resolveRouterToken(tool, runtimeDir, configToken string) (string, string) {
	token := strings.TrimSpace(configToken)
	if token != "" {
		return token, "config"
	}
	if token, ok := routerruntime.APIKey(tool, runtimeDir); ok {
		return token, "runtime"
	}
	return "", "missing"
}

func tokenSource(configToken string) string {
	if strings.TrimSpace(configToken) == "" {
		return "missing"
	}
	return "config"
}

type routerRuntimeSnapshot struct {
	Endpoint            string
	ListenHost          string
	BinaryTrusted       bool
	BinaryResolvedPath  string
	RuntimeIsolated     bool
	RuntimeDir          string
	LoopbackEnforced    bool
	ProcessGroupEnabled bool
	SecurityWarnings    []string
	SecurityNotices     []string
}

func runningRouterSnapshot(tool string) (routerRuntimeSnapshot, bool) {
	toolProcesses.RLock()
	tp, exists := toolProcesses.m[tool]
	toolProcesses.RUnlock()
	if !exists || tp == nil {
		return routerRuntimeSnapshot{}, false
	}
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if !tp.running || tp.endpoint == "" {
		return routerRuntimeSnapshot{}, false
	}
	return routerRuntimeSnapshot{
		Endpoint:            tp.endpoint,
		ListenHost:          tp.listenHost,
		BinaryTrusted:       tp.binaryTrusted,
		BinaryResolvedPath:  tp.binaryResolvedPath,
		RuntimeIsolated:     tp.runtimeIsolated,
		RuntimeDir:          tp.runtimeDir,
		LoopbackEnforced:    tp.loopbackEnforced,
		ProcessGroupEnabled: tp.processGroupEnabled,
		SecurityWarnings:    append([]string{}, tp.securityWarnings...),
		SecurityNotices:     append([]string{}, tp.securityNotices...),
	}, true
}
