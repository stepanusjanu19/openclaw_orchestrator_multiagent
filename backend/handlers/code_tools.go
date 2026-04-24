// handlers/code_tools.go
// Mengelola lifecycle Claude Code CLI dan OpenCode CLI dengan mode yang jujur:
// Claude = install/uninstall/validate, OpenCode = daemon optional.

package handlers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var toolInstallCmds = map[string][]string{
	"claude":   {"npm", "install", "-g", "@anthropic-ai/claude-code"},
	"opencode": {"npm", "install", "-g", "opencode-ai"},
}

var toolUninstallCmds = map[string][]string{
	"claude":   {"npm", "uninstall", "-g", "@anthropic-ai/claude-code"},
	"opencode": {"npm", "uninstall", "-g", "opencode-ai"},
}

var toolBinaries = map[string]string{
	"claude":   "claude",
	"opencode": "opencode",
}

type toolProcess struct {
	cmd     *exec.Cmd
	running bool
	logCh   chan string
	mu      sync.Mutex
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
	Tool              string `json:"tool"`
	Installed         bool   `json:"installed"`
	Running           bool   `json:"running"`
	Version           string `json:"version,omitempty"`
	BinPath           string `json:"bin_path,omitempty"`
	Ready             bool   `json:"ready"`
	ValidationMessage string `json:"validation_message,omitempty"`
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
			out, verErr := exec.Command(binPath, "--version").Output()
			if verErr == nil {
				resp.Version = strings.TrimSpace(string(out))
			}
		}

		toolProcesses.RLock()
		if tp, exists := toolProcesses.m[tool]; exists {
			tp.mu.Lock()
			resp.Running = tp.running
			tp.mu.Unlock()
		}
		toolProcesses.RUnlock()

		toolValidations.RLock()
		if validation, ok := toolValidations.m[tool]; ok {
			resp.Ready = validation.Ready
			resp.ValidationMessage = validation.ValidationMessage
		}
		toolValidations.RUnlock()

		if tool == "claude" && resp.Installed && resp.ValidationMessage == "" {
			resp.ValidationMessage = "Gunakan Validate untuk mengecek ANTHROPIC_API_KEY."
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
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("Tool '%s' tidak dikenali. Hanya: claude, opencode", req.Tool))
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
		fmt.Fprintf(w, "data: %s\n\n", msg)
		flusher.Flush()
	}

	sendEvent(fmt.Sprintf("🚀 Memulai instalasi %s...", req.Tool))
	sendEvent(fmt.Sprintf("📦 Perintah: %s", strings.Join(installArgs, " ")))

	cmd := exec.Command(installArgs[0], installArgs[1:]...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		sendEvent(fmt.Sprintf("❌ Gagal memulai instalasi: %v", err))
		fmt.Fprintf(w, "event: done\ndata: error\n\n")
		flusher.Flush()
		return
	}

	go streamPipe(stdout, sendEvent)
	streamPipe(stderr, sendEvent)

	if err := cmd.Wait(); err != nil {
		sendEvent(fmt.Sprintf("❌ Instalasi gagal: %v", err))
		fmt.Fprintf(w, "event: done\ndata: error\n\n")
	} else {
		sendEvent(fmt.Sprintf("✅ %s berhasil diinstall!", req.Tool))
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
		fmt.Fprintf(w, "data: %s\n\n", msg)
		if flusher != nil {
			flusher.Flush()
		}
	}

	sendEvent(fmt.Sprintf("🗑️ Menghapus %s...", req.Tool))

	cmd := exec.Command(uninstallArgs[0], uninstallArgs[1:]...)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		sendEvent(string(out))
	}

	if err != nil {
		sendEvent(fmt.Sprintf("⚠️ Gagal uninstall: %v", err))
		fmt.Fprintf(w, "event: done\ndata: error\n\n")
	} else {
		sendEvent(fmt.Sprintf("✅ %s berhasil dihapus.", req.Tool))
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
		Tool     string `json:"tool"`
		APIKey   string `json:"api_key"`
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Request tidak valid")
		return
	}
	if req.Tool == "claude" {
		jsonError(w, http.StatusBadRequest, "Claude Code tidak dijalankan sebagai daemon. Gunakan validate.")
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

	cmd := exec.Command(binPath)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	env := os.Environ()
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

	if err := cmd.Start(); err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("Gagal menjalankan %s: %v", req.Tool, err))
		return
	}

	logCh := make(chan string, 256)
	tp := &toolProcess{cmd: cmd, running: true, logCh: logCh}
	toolProcesses.Lock()
	toolProcesses.m[req.Tool] = tp
	toolProcesses.Unlock()

	go pipeToolLogs(req.Tool, "OUT", stdout, logCh)
	go pipeToolLogs(req.Tool, "ERR", stderr, logCh)
	go func() {
		cmd.Wait()
		tp.mu.Lock()
		tp.running = false
		tp.mu.Unlock()
		close(logCh)
		log.Printf("[CodeTools] %s daemon berhenti (PID %d)", req.Tool, cmd.Process.Pid)
	}()

	log.Printf("[CodeTools] ✅ %s dimulai (PID %d)", req.Tool, cmd.Process.Pid)
	jsonOK(w, map[string]interface{}{
		"ok":      true,
		"message": fmt.Sprintf("%s berhasil dijalankan (PID %d)", req.Tool, cmd.Process.Pid),
		"pid":     cmd.Process.Pid,
	})
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
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
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
	versionOut, verErr := exec.Command(binPath, "--version").Output()
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

	if err := tp.cmd.Process.Signal(os.Interrupt); err != nil {
		_ = tp.cmd.Process.Kill()
	}

	done := make(chan struct{})
	go func() {
		_ = tp.cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
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

	if !exists || tp.logCh == nil {
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

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case line, open := <-tp.logCh:
			if !open {
				fmt.Fprintf(w, "event: close\ndata: [%s] Stream berakhir.\n\n", tool)
				flusher.Flush()
				return
			}
			line = strings.ReplaceAll(line, "\n", "\\n")
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		}
	}
}

func streamPipe(r io.Reader, sink func(string)) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		sink(scanner.Text())
	}
}

func pipeToolLogs(tool, stream string, r io.Reader, ch chan string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		select {
		case ch <- fmt.Sprintf("[%s][%s] %s", tool, stream, scanner.Text()):
		default:
		}
	}
}
