package handlers

import (
	"net/http"
	"strings"
	"time"

	"multiagent-platform/internal/airouter"
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
			RuntimeTool:   tool,
			RuntimeActive: true,
			AgentRouting:  h.App.Cfg.AIRouterEnabled,
			Error:         "router runtime non-loopback ditolak",
			CheckedAt:     time.Now(),
		})
		return
	}
	status := airouter.ProbeRuntimeStatus(h.App.Cfg, tool, snapshot.Endpoint)
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
