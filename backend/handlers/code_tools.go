// handlers/code_tools.go
// Mengelola lifecycle Claude Code, OpenCode, dan local AI router CLI.
// Claude = install/uninstall/validate; OpenCode/9Router/OmniRoute = daemon optional.

package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"multiagent-platform/internal/airouter"
	"multiagent-platform/internal/config"
	"multiagent-platform/internal/security"
)

var toolInstallCmds = map[string][]string{
	"claude":    {"npm", "install", "-g", "@anthropic-ai/claude-code"},
	"opencode":  {"npm", "install", "-g", "opencode-ai"},
	"9router":   {"npm", "install", "-g", "9router"},
	"omniroute": {"npm", "install", "-g", "omniroute"},
}

var toolUninstallCmds = map[string][]string{
	"claude":    {"npm", "uninstall", "-g", "@anthropic-ai/claude-code"},
	"opencode":  {"npm", "uninstall", "-g", "opencode-ai"},
	"9router":   {"npm", "uninstall", "-g", "9router"},
	"omniroute": {"npm", "uninstall", "-g", "omniroute"},
}

var toolBinaries = map[string]string{
	"claude":    "claude",
	"opencode":  "opencode",
	"9router":   "9router",
	"omniroute": "omniroute",
}

var routerToolSet = map[string]bool{
	"9router":   true,
	"omniroute": true,
}

type toolProcess struct {
	cmd                 *exec.Cmd
	running             bool
	logs                *toolLogBuffer
	done                chan struct{}
	listenHost          string
	listenPort          int
	endpoint            string
	binaryTrusted       bool
	binaryResolvedPath  string
	runtimeIsolated     bool
	runtimeDir          string
	loopbackEnforced    bool
	processGroupEnabled bool
	securityWarnings    []string
	securityNotices     []string
	mu                  sync.Mutex
}

const maxToolLogLines = 400
const toolCommandTimeout = 10 * time.Minute

type toolLogBuffer struct {
	mu          sync.Mutex
	lines       []string
	subscribers map[chan string]struct{}
	closed      bool
}

func newToolLogBuffer() *toolLogBuffer {
	return &toolLogBuffer{subscribers: make(map[chan string]struct{})}
}

func (b *toolLogBuffer) append(line string) {
	if b == nil {
		return
	}
	line = redactToolOutput(line)
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.lines) >= maxToolLogLines {
		copy(b.lines, b.lines[1:])
		b.lines[len(b.lines)-1] = line
	} else {
		b.lines = append(b.lines, line)
	}
	for ch := range b.subscribers {
		select {
		case ch <- line:
		default:
		}
	}
}

func (b *toolLogBuffer) subscribe() ([]string, <-chan string, func(), bool) {
	ch := make(chan string, 128)
	if b == nil {
		close(ch)
		return nil, ch, func() {}, true
	}
	b.mu.Lock()
	snapshot := append([]string{}, b.lines...)
	closed := b.closed
	if closed {
		close(ch)
	} else {
		b.subscribers[ch] = struct{}{}
	}
	b.mu.Unlock()

	unsubscribe := func() {
		b.mu.Lock()
		if _, ok := b.subscribers[ch]; ok {
			delete(b.subscribers, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
	return snapshot, ch, unsubscribe, closed
}

func (b *toolLogBuffer) close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for ch := range b.subscribers {
		close(ch)
		delete(b.subscribers, ch)
	}
}

var toolProcesses = struct {
	sync.RWMutex
	m map[string]*toolProcess
}{m: make(map[string]*toolProcess)}

type CodeToolsHandler struct {
	App            *App
	FrontendOrigin string
}

type ToolStatusResponse struct {
	Tool                string   `json:"tool"`
	Installed           bool     `json:"installed"`
	Running             bool     `json:"running"`
	Managed             bool     `json:"managed"`
	ControlState        string   `json:"control_state,omitempty"`
	Version             string   `json:"version,omitempty"`
	BinPath             string   `json:"bin_path,omitempty"`
	Ready               bool     `json:"ready"`
	ValidationMessage   string   `json:"validation_message,omitempty"`
	ListenHost          string   `json:"listen_host,omitempty"`
	ListenPort          int      `json:"listen_port,omitempty"`
	Endpoint            string   `json:"endpoint,omitempty"`
	PortAvailable       bool     `json:"port_available"`
	PortOwner           string   `json:"port_owner,omitempty"`
	UnmanagedPID        int      `json:"unmanaged_pid,omitempty"`
	UnmanagedCommand    string   `json:"unmanaged_command,omitempty"`
	CanStopUnmanaged    bool     `json:"can_stop_unmanaged"`
	ConflictMessage     string   `json:"conflict_message,omitempty"`
	BinaryTrusted       bool     `json:"binary_trusted"`
	BinaryResolvedPath  string   `json:"binary_resolved_path,omitempty"`
	RuntimeIsolated     bool     `json:"runtime_isolated"`
	RuntimeDir          string   `json:"runtime_dir,omitempty"`
	LoopbackEnforced    bool     `json:"loopback_enforced"`
	ProcessGroupEnabled bool     `json:"process_group_enabled"`
	SecurityWarnings    []string `json:"security_warnings,omitempty"`
	SecurityNotices     []string `json:"security_notices,omitempty"`
}

var toolValidations = struct {
	sync.RWMutex
	m map[string]ToolStatusResponse
}{m: make(map[string]ToolStatusResponse)}

func (h *CodeToolsHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	results := []ToolStatusResponse{}

	for tool, bin := range toolBinaries {
		resp := ToolStatusResponse{Tool: tool}
		binPath, err := exec.LookPath(bin)
		if err == nil {
			resp.Installed = true
			resp.BinPath = binPath
			posture := buildToolPosture(tool, binPath, h.App.Cfg)
			applyPostureToStatus(&resp, posture)
			if !routerToolSet[tool] || posture.BinaryTrusted {
				out, verErr := commandOutputLimited(3*time.Second, posture.BinaryResolvedPath, "--version")
				if verErr == nil {
					resp.Version = strings.TrimSpace(string(out))
				}
			}
		} else if routerToolSet[tool] {
			applyPostureToStatus(&resp, buildToolPosture(tool, "", h.App.Cfg))
		}

		toolProcesses.RLock()
		if tp, exists := toolProcesses.m[tool]; exists {
			tp.mu.Lock()
			resp.Running = tp.running
			resp.Managed = tp.running
			resp.ListenHost = tp.listenHost
			resp.ListenPort = tp.listenPort
			resp.Endpoint = tp.endpoint
			resp.BinaryTrusted = tp.binaryTrusted
			resp.BinaryResolvedPath = tp.binaryResolvedPath
			resp.RuntimeIsolated = tp.runtimeIsolated
			resp.RuntimeDir = tp.runtimeDir
			resp.LoopbackEnforced = tp.loopbackEnforced
			resp.ProcessGroupEnabled = tp.processGroupEnabled
			resp.SecurityWarnings = append([]string{}, tp.securityWarnings...)
			resp.SecurityNotices = append([]string{}, tp.securityNotices...)
			tp.mu.Unlock()
		}
		toolProcesses.RUnlock()
		if routerToolSet[tool] && resp.ListenPort == 0 {
			resp.ListenHost = routerListenHost(h.App.Cfg.AIRouterListenHost)
			resp.ListenPort = defaultRouterPort(h.App.Cfg, tool)
			resp.Endpoint = routerEndpoint(resp.ListenHost, resp.ListenPort)
		}
		applyControlState(&resp)

		toolValidations.RLock()
		if validation, ok := toolValidations.m[tool]; ok {
			resp.Ready = validation.Ready
			resp.ValidationMessage = validation.ValidationMessage
		}
		toolValidations.RUnlock()

		if tool == "claude" && resp.Installed && resp.ValidationMessage == "" {
			resp.ValidationMessage = "Gunakan Validate untuk mengecek ANTHROPIC_API_KEY."
		} else if routerToolSet[tool] && resp.Installed && resp.ValidationMessage == "" {
			resp.ValidationMessage = fmt.Sprintf("Jalankan sebagai private downstream router di %s.", resp.Endpoint)
		}

		results = append(results, resp)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"tools": results})
}

func (h *CodeToolsHandler) Install(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Tool string `json:"tool"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Request tidak valid")
		return
	}

	installArgs, ok := toolInstallCmds[req.Tool]
	if !ok {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("Tool '%s' tidak dikenali. Hanya: %s", req.Tool, strings.Join(knownTools(), ", ")))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", h.FrontendOrigin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	sendEvent := func(msg string) {
		fmt.Fprintf(w, "data: %s\n\n", redactToolOutput(msg))
		flusher.Flush()
	}

	sendEvent(fmt.Sprintf("🚀 Memulai instalasi %s...", req.Tool))
	sendEvent(fmt.Sprintf("📦 Perintah: %s", strings.Join(installArgs, " ")))

	ctx, cancel := context.WithTimeout(context.Background(), toolCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, installArgs[0], installArgs[1:]...)
	cmd.Env = safeToolEnv()
	processGroupEnabled := enableChildProcessGroup(cmd)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		sendEvent(fmt.Sprintf("❌ Gagal memulai instalasi: %v", err))
		h.auditCodeTool("install", req.Tool, "error", err, nil)
		fmt.Fprintf(w, "event: done\ndata: error\n\n")
		flusher.Flush()
		return
	}
	h.auditCodeTool("install", req.Tool, "started", nil, nil)

	go streamPipe(stdout, sendEvent)
	streamPipe(stderr, sendEvent)

	if err := cmd.Wait(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			if processGroupEnabled {
				_ = killChildProcessGroup(cmd)
			}
			sendEvent("❌ Instalasi dihentikan: command timeout")
			h.auditCodeTool("install", req.Tool, "timeout", ctx.Err(), nil)
			fmt.Fprintf(w, "event: done\ndata: error\n\n")
			flusher.Flush()
			return
		}
		sendEvent(fmt.Sprintf("❌ Instalasi gagal: %v", err))
		h.auditCodeTool("install", req.Tool, "error", err, nil)
		fmt.Fprintf(w, "event: done\ndata: error\n\n")
	} else {
		sendEvent(fmt.Sprintf("✅ %s berhasil diinstall!", req.Tool))
		h.auditCodeTool("install", req.Tool, "success", nil, nil)
		fmt.Fprintf(w, "event: done\ndata: success\n\n")
	}
	flusher.Flush()
}

func (h *CodeToolsHandler) Uninstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Tool string `json:"tool"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Request tidak valid")
		return
	}

	uninstallArgs, ok := toolUninstallCmds[req.Tool]
	if !ok {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("Tool '%s' tidak dikenali", req.Tool))
		return
	}

	_ = h.stopTool(req.Tool)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", h.FrontendOrigin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	flusher, _ := w.(http.Flusher)

	sendEvent := func(msg string) {
		fmt.Fprintf(w, "data: %s\n\n", redactToolOutput(msg))
		if flusher != nil {
			flusher.Flush()
		}
	}

	sendEvent(fmt.Sprintf("🗑️ Menghapus %s...", req.Tool))

	ctx, cancel := context.WithTimeout(context.Background(), toolCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, uninstallArgs[0], uninstallArgs[1:]...)
	cmd.Env = safeToolEnv()
	processGroupEnabled := enableChildProcessGroup(cmd)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		sendEvent(fmt.Sprintf("⚠️ Gagal memulai uninstall: %v", err))
		h.auditCodeTool("uninstall", req.Tool, "error", err, nil)
		fmt.Fprintf(w, "event: done\ndata: error\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		return
	}
	h.auditCodeTool("uninstall", req.Tool, "started", nil, nil)
	go streamPipe(stdout, sendEvent)
	streamPipe(stderr, sendEvent)

	if err := cmd.Wait(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			if processGroupEnabled {
				_ = killChildProcessGroup(cmd)
			}
			sendEvent("⚠️ Uninstall dihentikan: command timeout")
			h.auditCodeTool("uninstall", req.Tool, "timeout", ctx.Err(), nil)
			fmt.Fprintf(w, "event: done\ndata: error\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		sendEvent(fmt.Sprintf("⚠️ Gagal uninstall: %v", err))
		h.auditCodeTool("uninstall", req.Tool, "error", err, nil)
		fmt.Fprintf(w, "event: done\ndata: error\n\n")
	} else {
		sendEvent(fmt.Sprintf("✅ %s berhasil dihapus.", req.Tool))
		h.auditCodeTool("uninstall", req.Tool, "success", nil, nil)
		fmt.Fprintf(w, "event: done\ndata: success\n\n")
	}
	if flusher != nil {
		flusher.Flush()
	}
}

func (h *CodeToolsHandler) Start(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Tool       string `json:"tool"`
		APIKey     string `json:"api_key"`
		Provider   string `json:"provider"`
		ListenPort int    `json:"listen_port,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Request tidak valid")
		return
	}
	if req.Tool == "claude" {
		jsonError(w, http.StatusBadRequest, "Claude Code tidak dijalankan sebagai daemon. Gunakan validate.")
		return
	}
	if routerToolSet[req.Tool] && strings.TrimSpace(req.APIKey) != "" {
		jsonError(w, http.StatusBadRequest, "API key router harus dikelola di dashboard router, bukan dikirim via Code Tools")
		return
	}
	bin, ok := toolBinaries[req.Tool]
	if !ok {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("Tool '%s' tidak dikenali", req.Tool))
		return
	}

	toolProcesses.RLock()
	if tp, exists := toolProcesses.m[req.Tool]; exists {
		tp.mu.Lock()
		isRunning := tp.running
		tp.mu.Unlock()
		toolProcesses.RUnlock()
		if isRunning {
			jsonError(w, http.StatusConflict, fmt.Sprintf("%s sudah berjalan", req.Tool))
			return
		}
	} else {
		toolProcesses.RUnlock()
	}

	binPath, err := exec.LookPath(bin)
	if err != nil {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("%s belum terinstall. Silakan install terlebih dahulu.", req.Tool))
		return
	}
	posture := buildToolPosture(req.Tool, binPath, h.App.Cfg)
	if routerToolSet[req.Tool] && !posture.BinaryTrusted {
		message := "binary router tidak trusted"
		if len(posture.SecurityWarnings) > 0 {
			message += ": " + strings.Join(posture.SecurityWarnings, "; ")
		}
		h.auditCodeTool("start", req.Tool, "blocked", fmt.Errorf("%s", message), nil)
		jsonError(w, http.StatusBadRequest, message)
		return
	}

	listenHost := ""
	listenPort := 0
	endpoint := ""
	if routerToolSet[req.Tool] {
		listenHost = routerListenHost(h.App.Cfg.AIRouterListenHost)
		if !airouter.IsLoopbackHost(listenHost) {
			jsonError(w, http.StatusBadRequest, "AI router listen host harus loopback/localhost")
			return
		}
		listenPort = req.ListenPort
		if listenPort == 0 {
			listenPort = defaultRouterPort(h.App.Cfg, req.Tool)
		}
		if !validRouterPort(listenPort) {
			jsonError(w, http.StatusBadRequest, "listen_port harus 1024..65535")
			return
		}
		if runningRouter, ok := runningRouterOnPort(req.Tool, listenHost, listenPort); ok {
			jsonError(w, http.StatusConflict, fmt.Sprintf("%s sudah berjalan di port %d", runningRouter, listenPort))
			return
		}
		probe := inspectPort(listenHost, listenPort)
		if !probe.Available {
			message := portConflictMessage(req.Tool, listenHost, listenPort, probe)
			h.auditCodeTool("start", req.Tool, "blocked", fmt.Errorf("%s", message), map[string]interface{}{
				"listen_host": listenHost,
				"listen_port": listenPort,
				"owner_pid":   probe.PID,
				"owner":       probe.Command,
			})
			jsonError(w, http.StatusConflict, message)
			return
		}
		endpoint = routerEndpoint(listenHost, listenPort)
	}

	cmd := exec.Command(posture.BinaryResolvedPath)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	env := safeToolEnv()
	if routerToolSet[req.Tool] {
		if !posture.RuntimeIsolated {
			message := "runtime directory router gagal diisolasi"
			if len(posture.SecurityWarnings) > 0 {
				message += ": " + strings.Join(posture.SecurityWarnings, "; ")
			}
			h.auditCodeTool("start", req.Tool, "blocked", fmt.Errorf("%s", message), nil)
			jsonError(w, http.StatusInternalServerError, message)
			return
		}
		cmd.Dir = posture.RuntimeDir
		env = append(env,
			"HOST="+listenHost,
			"HOSTNAME="+listenHost,
			"PORT="+strconv.Itoa(listenPort),
			"BASE_URL=http://"+net.JoinHostPort(listenHost, strconv.Itoa(listenPort)),
			"AI_ROUTER_REQUIRE_LOOPBACK=true",
		)
		if req.Tool == "omniroute" {
			env = append(env, "OMNIROUTE_PORT="+strconv.Itoa(listenPort))
		}
	}
	if req.APIKey != "" {
		switch req.Provider {
		case "anthropic":
			env = append(env, "ANTHROPIC_API_KEY="+req.APIKey)
		case "openai":
			env = append(env, "OPENAI_API_KEY="+req.APIKey)
		case "openrouter":
			env = append(env, "OPENROUTER_API_KEY="+req.APIKey)
		default:
			env = append(env, "OPENAI_API_KEY="+req.APIKey)
		}
	}
	cmd.Env = env
	if routerToolSet[req.Tool] {
		cmd.Env = routerRuntimeEnv(req.Tool, posture.RuntimeDir, cmd.Env...)
	}
	processGroupEnabled := enableChildProcessGroup(cmd)
	posture.ProcessGroupEnabled = processGroupEnabled

	if err := cmd.Start(); err != nil {
		h.auditCodeTool("start", req.Tool, "error", err, map[string]interface{}{"endpoint": endpoint})
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("Gagal menjalankan %s: %v", req.Tool, err))
		return
	}

	logs := newToolLogBuffer()
	done := make(chan struct{})
	tp := &toolProcess{
		cmd:                 cmd,
		running:             true,
		logs:                logs,
		done:                done,
		listenHost:          listenHost,
		listenPort:          listenPort,
		endpoint:            endpoint,
		binaryTrusted:       posture.BinaryTrusted,
		binaryResolvedPath:  posture.BinaryResolvedPath,
		runtimeIsolated:     posture.RuntimeIsolated,
		runtimeDir:          posture.RuntimeDir,
		loopbackEnforced:    posture.LoopbackEnforced,
		processGroupEnabled: posture.ProcessGroupEnabled,
		securityWarnings:    append([]string{}, posture.SecurityWarnings...),
		securityNotices:     append([]string{}, posture.SecurityNotices...),
	}
	toolProcesses.Lock()
	toolProcesses.m[req.Tool] = tp
	toolProcesses.Unlock()

	go pipeToolLogs(req.Tool, "OUT", stdout, logs.append)
	go pipeToolLogs(req.Tool, "ERR", stderr, logs.append)
	go func() {
		_ = cmd.Wait()
		tp.mu.Lock()
		tp.running = false
		tp.mu.Unlock()
		logs.close()
		close(done)
		log.Printf("[CodeTools] %s daemon berhenti (PID %d)", req.Tool, cmd.Process.Pid)
	}()

	h.auditCodeTool("start", req.Tool, "success", nil, map[string]interface{}{"endpoint": endpoint, "pid": cmd.Process.Pid})
	log.Printf("[CodeTools] ✅ %s dimulai (PID %d)", req.Tool, cmd.Process.Pid)
	jsonOK(w, map[string]interface{}{
		"ok":       true,
		"message":  fmt.Sprintf("%s berhasil dijalankan (PID %d)", req.Tool, cmd.Process.Pid),
		"pid":      cmd.Process.Pid,
		"endpoint": endpoint,
	})
}

func (h *CodeToolsHandler) StopUnmanaged(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Tool       string `json:"tool"`
		PID        int    `json:"pid,omitempty"`
		ListenPort int    `json:"listen_port,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Request tidak valid")
		return
	}
	if !routerToolSet[req.Tool] {
		jsonError(w, http.StatusBadRequest, "stop external hanya untuk 9router atau omniroute")
		return
	}
	host := routerListenHost(h.App.Cfg.AIRouterListenHost)
	port := defaultRouterPort(h.App.Cfg, req.Tool)
	if req.ListenPort != 0 {
		if !validRouterPort(req.ListenPort) {
			jsonError(w, http.StatusBadRequest, "listen_port harus 1024..65535")
			return
		}
		port = req.ListenPort
	}
	status := ToolStatusResponse{
		Tool:       req.Tool,
		ListenHost: host,
		ListenPort: port,
		Endpoint:   routerEndpoint(host, port),
	}
	applyControlState(&status)
	if !status.CanStopUnmanaged || status.UnmanagedPID == 0 {
		jsonError(w, http.StatusForbidden, "external process tidak aman untuk dihentikan dari dashboard")
		return
	}
	if req.PID != 0 && req.PID != status.UnmanagedPID {
		jsonError(w, http.StatusConflict, "PID external process berubah, refresh status terlebih dahulu")
		return
	}
	if err := stopExternalProcess(status.UnmanagedPID); err != nil {
		h.auditCodeTool("stop_unmanaged", req.Tool, "error", err, map[string]interface{}{"pid": status.UnmanagedPID})
		jsonError(w, http.StatusInternalServerError, "gagal menghentikan external process: "+err.Error())
		return
	}
	h.auditCodeTool("stop_unmanaged", req.Tool, "success", nil, map[string]interface{}{"pid": status.UnmanagedPID})
	jsonOK(w, map[string]interface{}{"ok": true, "message": fmt.Sprintf("%s external process dihentikan (PID %d)", req.Tool, status.UnmanagedPID)})
}

func (h *CodeToolsHandler) Stop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Tool string `json:"tool"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Request tidak valid")
		return
	}
	if _, ok := toolBinaries[req.Tool]; !ok {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("Tool '%s' tidak dikenali", req.Tool))
		return
	}
	if req.Tool == "claude" {
		jsonError(w, http.StatusBadRequest, "Claude Code tidak memiliki mode stop daemon")
		return
	}

	if err := h.stopTool(req.Tool); err != nil {
		h.auditCodeTool("stop", req.Tool, "error", err, nil)
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.auditCodeTool("stop", req.Tool, "success", nil, nil)
	jsonOK(w, map[string]interface{}{"ok": true, "message": fmt.Sprintf("%s dihentikan", req.Tool)})
}

func (h *CodeToolsHandler) Validate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Tool   string `json:"tool"`
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Request tidak valid")
		return
	}
	if req.Tool != "claude" {
		jsonError(w, http.StatusBadRequest, "Validate saat ini hanya untuk Claude Code")
		return
	}

	binPath, err := exec.LookPath(toolBinaries[req.Tool])
	if err != nil {
		jsonError(w, http.StatusBadRequest, "Claude Code belum terinstall")
		return
	}
	versionOut, verErr := commandOutputLimited(3*time.Second, binPath, "--version")
	if verErr != nil {
		jsonError(w, http.StatusInternalServerError, "Gagal membaca versi Claude Code: "+verErr.Error())
		return
	}

	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	}

	resp := ToolStatusResponse{
		Tool:      "claude",
		Installed: true,
		BinPath:   binPath,
		Version:   strings.TrimSpace(string(versionOut)),
	}
	if apiKey == "" {
		resp.Ready = false
		resp.ValidationMessage = "ANTHROPIC_API_KEY belum diberikan."
	} else if len(apiKey) < 20 || !strings.HasPrefix(apiKey, "sk-ant-") {
		resp.Ready = false
		resp.ValidationMessage = "Format ANTHROPIC_API_KEY tidak valid."
	} else {
		resp.Ready = true
		resp.ValidationMessage = "Claude Code siap digunakan."
	}

	toolValidations.Lock()
	toolValidations.m["claude"] = resp
	toolValidations.Unlock()
	jsonOK(w, resp)
}

func (h *CodeToolsHandler) stopTool(tool string) error {
	toolProcesses.Lock()
	defer toolProcesses.Unlock()

	tp, exists := toolProcesses.m[tool]
	if !exists {
		return fmt.Errorf("%s tidak sedang berjalan", tool)
	}

	tp.mu.Lock()
	isRunning := tp.running
	tp.mu.Unlock()
	if !isRunning {
		delete(toolProcesses.m, tool)
		return fmt.Errorf("%s tidak aktif", tool)
	}

	if tp.processGroupEnabled {
		if err := interruptChildProcessGroup(tp.cmd); err != nil {
			_ = killChildProcessGroup(tp.cmd)
		}
	} else if err := tp.cmd.Process.Signal(os.Interrupt); err != nil {
		_ = tp.cmd.Process.Kill()
	}

	if tp.done != nil {
		select {
		case <-tp.done:
		case <-time.After(5 * time.Second):
			if tp.processGroupEnabled {
				_ = killChildProcessGroup(tp.cmd)
			} else {
				_ = tp.cmd.Process.Kill()
			}
			select {
			case <-tp.done:
			case <-time.After(2 * time.Second):
			}
		}
	} else {
		_ = tp.cmd.Process.Kill()
	}

	tp.mu.Lock()
	tp.running = false
	tp.mu.Unlock()
	delete(toolProcesses.m, tool)
	return nil
}

func (h *CodeToolsHandler) StreamLogs(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "Tool ID diperlukan", http.StatusBadRequest)
		return
	}
	tool := parts[3]
	if _, ok := toolBinaries[tool]; !ok {
		http.Error(w, "Tool tidak dikenal", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", h.FrontendOrigin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	toolProcesses.RLock()
	tp, exists := toolProcesses.m[tool]
	toolProcesses.RUnlock()

	if !exists || tp.logs == nil {
		fmt.Fprintf(w, "data: [%s] Tool tidak aktif.\n\n", tool)
		flusher.Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		return
	}

	fmt.Fprintf(w, "data: [%s] Log stream dimulai...\n\n", tool)
	flusher.Flush()
	snapshot, logCh, unsubscribe, closed := tp.logs.subscribe()
	defer unsubscribe()
	for _, line := range snapshot {
		line = strings.ReplaceAll(redactToolOutput(line), "\n", "\\n")
		fmt.Fprintf(w, "data: %s\n\n", line)
		flusher.Flush()
	}
	if closed {
		fmt.Fprintf(w, "event: close\ndata: [%s] Stream berakhir.\n\n", tool)
		flusher.Flush()
		return
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case line, open := <-logCh:
			if !open {
				fmt.Fprintf(w, "event: close\ndata: [%s] Stream berakhir.\n\n", tool)
				flusher.Flush()
				return
			}
			line = strings.ReplaceAll(redactToolOutput(line), "\n", "\\n")
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		}
	}
}

func (h *CodeToolsHandler) auditCodeTool(operation, tool, status string, err error, metadata map[string]interface{}) {
	if h == nil || h.App == nil || h.App.Security == nil {
		return
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	metadata["tool"] = tool
	metadata["operation"] = operation
	event := security.AuditEvent{
		Level:    "INFO",
		Channel:  "code_tools",
		Status:   status,
		Message:  "code tools lifecycle",
		Metadata: metadata,
	}
	if err != nil {
		event.Level = "ERROR"
		event.ErrorClass = security.MaskKnownSecrets(err.Error())
	}
	h.App.Security.EnqueueAudit(event)
}

func streamPipe(r io.Reader, sink func(string)) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		sink(redactToolOutput(scanner.Text()))
	}
}

func pipeToolLogs(tool, stream string, r io.Reader, sink func(string)) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		sink(fmt.Sprintf("[%s][%s] %s", tool, stream, scanner.Text()))
	}
}

func commandOutputLimited(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = safeToolEnv()
	processGroupEnabled := enableChildProcessGroup(cmd)
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		if processGroupEnabled {
			_ = killChildProcessGroup(cmd)
		}
		return nil, fmt.Errorf("command timeout")
	}
	if len(out) > 4096 {
		out = out[:4096]
	}
	return out, err
}

func safeToolEnv(extra ...string) []string {
	allowed := []string{
		"PATH",
		"HOME",
		"USER",
		"LOGNAME",
		"SHELL",
		"TMPDIR",
		"TEMP",
		"TMP",
		"XDG_CACHE_HOME",
		"XDG_CONFIG_HOME",
		"XDG_DATA_HOME",
		"NPM_CONFIG_PREFIX",
		"npm_config_prefix",
	}
	env := make([]string, 0, len(allowed)+len(extra))
	seen := make(map[string]bool, len(allowed)+len(extra))
	for _, key := range allowed {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
			seen[key] = true
		}
	}
	for _, item := range extra {
		key, _, ok := strings.Cut(item, "=")
		if !ok || key == "" || seen[key] {
			continue
		}
		env = append(env, item)
		seen[key] = true
	}
	return env
}

type toolSecurityPosture struct {
	BinaryTrusted       bool
	BinaryResolvedPath  string
	RuntimeIsolated     bool
	RuntimeDir          string
	LoopbackEnforced    bool
	ProcessGroupEnabled bool
	SecurityWarnings    []string
	SecurityNotices     []string
}

func buildToolPosture(tool, binPath string, cfg *config.Config) toolSecurityPosture {
	posture := toolSecurityPosture{
		BinaryResolvedPath:  binPath,
		LoopbackEnforced:    routerToolSet[tool],
		ProcessGroupEnabled: routerToolSet[tool],
	}
	if routerToolSet[tool] {
		runtimeDir, err := routerRuntimeDir(tool)
		if err != nil {
			posture.SecurityWarnings = append(posture.SecurityWarnings, "runtime directory gagal dibuat")
		} else {
			posture.RuntimeDir = runtimeDir
			posture.RuntimeIsolated = isRuntimeDirIsolated(runtimeDir, cfg)
			if !posture.RuntimeIsolated {
				posture.SecurityWarnings = append(posture.SecurityWarnings, "runtime directory tidak terisolasi dari workspace/credentials")
			}
		}
	}
	if strings.TrimSpace(binPath) == "" {
		posture.SecurityWarnings = append(posture.SecurityWarnings, "binary belum ditemukan")
		return posture
	}
	resolved, warnings, notices, trusted := validateResolvedBinary(binPath, cfg)
	posture.BinaryResolvedPath = resolved
	posture.BinaryTrusted = trusted
	posture.SecurityWarnings = append(posture.SecurityWarnings, warnings...)
	posture.SecurityNotices = append(posture.SecurityNotices, notices...)
	return posture
}

func applyPostureToStatus(resp *ToolStatusResponse, posture toolSecurityPosture) {
	if resp == nil {
		return
	}
	resp.BinaryTrusted = posture.BinaryTrusted
	resp.BinaryResolvedPath = posture.BinaryResolvedPath
	resp.RuntimeIsolated = posture.RuntimeIsolated
	resp.RuntimeDir = posture.RuntimeDir
	resp.LoopbackEnforced = posture.LoopbackEnforced
	resp.ProcessGroupEnabled = posture.ProcessGroupEnabled
	resp.SecurityWarnings = append([]string{}, posture.SecurityWarnings...)
	resp.SecurityNotices = append([]string{}, posture.SecurityNotices...)
}

func applyControlState(resp *ToolStatusResponse) {
	if resp == nil {
		return
	}
	resp.ControlState = "not_installed"
	resp.PortAvailable = true
	if resp.Installed {
		resp.ControlState = "stopped"
	}
	if !routerToolSet[resp.Tool] {
		if resp.Running {
			resp.Managed = true
			resp.ControlState = "managed"
		}
		return
	}
	if resp.Managed && resp.Running {
		resp.ControlState = "managed"
		resp.PortAvailable = false
		resp.PortOwner = "dashboard-managed"
		return
	}
	if resp.ListenHost == "" || resp.ListenPort == 0 {
		return
	}
	probe := inspectPort(resp.ListenHost, resp.ListenPort)
	resp.PortAvailable = probe.Available
	if probe.Available {
		return
	}
	resp.UnmanagedPID = probe.PID
	resp.UnmanagedCommand = probe.Command
	resp.PortOwner = portOwnerLabel(probe)
	if externalToolProcessMatches(resp.Tool, probe) {
		resp.Running = true
		resp.ControlState = "unmanaged_running"
		resp.CanStopUnmanaged = canStopExternalTool(resp.Tool, probe)
		resp.ConflictMessage = fmt.Sprintf("%s berjalan di luar dashboard pada %s:%d", resp.Tool, resp.ListenHost, resp.ListenPort)
		return
	}
	resp.ControlState = "port_conflict"
	resp.ConflictMessage = portConflictMessage(resp.Tool, resp.ListenHost, resp.ListenPort, probe)
}

func validateResolvedBinary(binPath string, cfg *config.Config) (string, []string, []string, bool) {
	warnings := []string{}
	notices := []string{}
	absPath, err := filepath.Abs(binPath)
	if err != nil || !filepath.IsAbs(binPath) {
		return binPath, append(warnings, "binary path harus absolute"), notices, false
	}
	info, err := os.Lstat(absPath)
	if err != nil {
		return absPath, append(warnings, "binary tidak dapat dibaca"), notices, false
	}
	resolved := absPath
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(absPath)
		if err != nil {
			return absPath, append(warnings, "symlink binary tidak dapat diresolve"), notices, false
		}
		resolved = target
		notices = append(notices, "binary memakai symlink yang diresolve sebelum start")
	}
	resolvedAbs, err := filepath.Abs(resolved)
	if err != nil || !filepath.IsAbs(resolved) {
		return resolved, append(warnings, "resolved binary path harus absolute"), notices, false
	}
	targetInfo, err := os.Stat(resolvedAbs)
	if err != nil {
		return resolvedAbs, append(warnings, "resolved binary tidak dapat dibaca"), notices, false
	}
	if !targetInfo.Mode().IsRegular() {
		return resolvedAbs, append(warnings, "resolved binary bukan file regular"), notices, false
	}
	if targetInfo.Mode().Perm()&0111 == 0 {
		return resolvedAbs, append(warnings, "resolved binary tidak executable"), notices, false
	}
	for _, root := range sensitiveBinaryRoots(cfg) {
		if pathInside(root, absPath) || pathInside(root, resolvedAbs) {
			warnings = append(warnings, "binary dari workspace/credentials ditolak")
			return resolvedAbs, warnings, notices, false
		}
	}
	if worldWritableAncestor(absPath) || worldWritableAncestor(resolvedAbs) {
		warnings = append(warnings, "binary berada di directory world-writable")
		return resolvedAbs, warnings, notices, false
	}
	return resolvedAbs, warnings, notices, true
}

func routerRuntimeDir(tool string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = filepath.Join(os.TempDir(), "openclaw-control-cache")
	}
	dir := filepath.Join(base, "openclaw-control", "router-runtime", sanitizeToolName(tool))
	for _, sub := range []string{"", "home", "cache", "config", "data", "tmp"} {
		path := dir
		if sub != "" {
			path = filepath.Join(dir, sub)
		}
		if err := os.MkdirAll(path, 0700); err != nil {
			return "", err
		}
		_ = os.Chmod(path, 0700)
	}
	return dir, nil
}

func routerRuntimeEnv(tool, runtimeDir string, base ...string) []string {
	if strings.TrimSpace(runtimeDir) == "" {
		return base
	}
	homeDir := filepath.Join(runtimeDir, "home")
	entries := append([]string{}, base...)
	entries = setEnvValue(entries, "HOME", homeDir)
	entries = setEnvValue(entries, "XDG_CACHE_HOME", filepath.Join(runtimeDir, "cache"))
	entries = setEnvValue(entries, "XDG_CONFIG_HOME", filepath.Join(runtimeDir, "config"))
	entries = setEnvValue(entries, "XDG_DATA_HOME", filepath.Join(runtimeDir, "data"))
	entries = setEnvValue(entries, "TMPDIR", filepath.Join(runtimeDir, "tmp"))
	entries = setEnvValue(entries, "OPENCLAW_ROUTER_RUNTIME", "true")
	entries = setEnvValue(entries, "OPENCLAW_ROUTER_TOOL", tool)
	return entries
}

func setEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	for i, item := range env {
		if strings.HasPrefix(item, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func sensitiveBinaryRoots(cfg *config.Config) []string {
	roots := []string{}
	if wd, err := os.Getwd(); err == nil && wd != "" {
		roots = append(roots, wd)
		if filepath.Base(wd) == "backend" {
			roots = append(roots, filepath.Dir(wd))
		}
	}
	if cfg != nil && cfg.CredentialsDir != "" {
		roots = append(roots, cfg.CredentialsDir)
	}
	return roots
}

func isRuntimeDirIsolated(dir string, cfg *config.Config) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	for _, root := range sensitiveBinaryRoots(cfg) {
		if pathInside(root, dir) {
			return false
		}
	}
	info, err := os.Stat(dir)
	return err == nil && info.IsDir() && info.Mode().Perm()&0077 == 0
}

func pathInside(root, target string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(target) == "" {
		return false
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rootAbs = filepath.Clean(rootAbs)
	targetAbs = filepath.Clean(targetAbs)
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func worldWritableAncestor(path string) bool {
	dir := filepath.Dir(filepath.Clean(path))
	for {
		info, err := os.Stat(dir)
		if err == nil && info.IsDir() && info.Mode().Perm()&0002 != 0 {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

func sanitizeToolName(tool string) string {
	tool = strings.ToLower(strings.TrimSpace(tool))
	var b strings.Builder
	for _, r := range tool {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "tool"
	}
	return b.String()
}

func knownTools() []string {
	out := make([]string, 0, len(toolBinaries))
	for tool := range toolBinaries {
		out = append(out, tool)
	}
	return out
}

func runningRouterOnPort(except string, host string, port int) (string, bool) {
	toolProcesses.RLock()
	defer toolProcesses.RUnlock()
	for tool, tp := range toolProcesses.m {
		if tool == except || !routerToolSet[tool] {
			continue
		}
		tp.mu.Lock()
		running := tp.running
		samePort := tp.listenPort == port
		sameHost := tp.listenHost == host
		tp.mu.Unlock()
		if running && samePort && sameHost {
			return tool, true
		}
	}
	return "", false
}

func routerListenHost(raw string) string {
	host := strings.TrimSpace(raw)
	if host == "" {
		return "127.0.0.1"
	}
	return host
}

func defaultRouterPort(cfg *config.Config, tool string) int {
	switch tool {
	case "omniroute":
		return cfg.AIRouterOmniRoutePort
	default:
		return cfg.AIRouter9RouterPort
	}
}

func validRouterPort(port int) bool {
	return port >= 1024 && port <= 65535
}

func routerEndpoint(host string, port int) string {
	if port == 0 {
		return ""
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/v1"
}

func portConflictMessage(tool, host string, port int, probe portInspection) string {
	owner := portOwnerLabel(probe)
	if externalToolProcessMatches(tool, probe) {
		return fmt.Sprintf("%s sudah berjalan di luar dashboard pada %s:%d (%s)", tool, host, port, owner)
	}
	return fmt.Sprintf("port %d di %s sedang dipakai atau tidak dapat dibuka (%s)", port, host, owner)
}

func portOwnerLabel(probe portInspection) string {
	if probe.PID > 0 && strings.TrimSpace(probe.Command) != "" {
		return fmt.Sprintf("PID %d: %s", probe.PID, probe.Command)
	}
	if probe.PID > 0 {
		return fmt.Sprintf("PID %d", probe.PID)
	}
	if strings.TrimSpace(probe.Error) != "" {
		return probe.Error
	}
	return "process tidak dikenal"
}

func externalToolProcessMatches(tool string, probe portInspection) bool {
	tool = strings.ToLower(strings.TrimSpace(tool))
	if tool == "" || probe.PID <= 0 || probe.PID == os.Getpid() {
		return false
	}
	if strings.EqualFold(probe.RouterTool, tool) && probe.OpenClawRouter {
		return true
	}
	cmd := strings.ToLower(probe.Command + " " + probe.Executable)
	switch tool {
	case "9router":
		return strings.Contains(cmd, "9router")
	case "omniroute":
		return strings.Contains(cmd, "omniroute")
	default:
		return strings.Contains(cmd, tool)
	}
}

func canStopExternalTool(tool string, probe portInspection) bool {
	if !externalToolProcessMatches(tool, probe) || probe.PID <= 0 || probe.PID == os.Getpid() {
		return false
	}
	if probe.UIDKnown && probe.UID != os.Geteuid() {
		return false
	}
	cmd := strings.ToLower(probe.Command + " " + probe.Executable)
	if strings.Contains(cmd, "multiagent-platform") || strings.Contains(cmd, "openclaw-control") {
		return false
	}
	return true
}

var toolKeyValueSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|API_KEY|PRIVATE_KEY|STORE_KEY|DATABASE_URL|DSN)[A-Z0-9_]*)(["'\s:=]+)([^"',\s}]+)`),
	regexp.MustCompile(`(?i)\b(authorization|api[_-]?key|access[_-]?token|refresh[_-]?token|auth[_-]?token|jwt[_-]?secret|password|secret)(["'\s:=]+)([^"',\s}]+)`),
}
var toolRawSecretPattern = regexp.MustCompile(`\b(sk-ant-[A-Za-z0-9_\-]{16,}|sk-[A-Za-z0-9_\-]{20,}|sk-or-[A-Za-z0-9_\-]{16,}|AIza[A-Za-z0-9_\-]{16,}|ghp_[A-Za-z0-9_]{20,})\b`)

func redactToolOutput(line string) string {
	out := strings.ReplaceAll(line, "\r", " ")
	out = strings.ReplaceAll(out, "\x00", "")
	for _, re := range toolKeyValueSecretPatterns {
		out = re.ReplaceAllString(out, "$1$2[REDACTED]")
	}
	out = toolRawSecretPattern.ReplaceAllString(out, "[REDACTED]")
	if len(out) > 2000 {
		return out[:2000] + "...[truncated]"
	}
	return out
}
