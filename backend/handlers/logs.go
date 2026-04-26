// handlers/logs.go
// SSE (Server-Sent Events) streaming log per agent.

package handlers

import (
	"fmt"
	"multiagent-platform/internal/process"
	"multiagent-platform/internal/security"
	"net/http"
	"strings"
	"time"
)

// LogsHandler mengelola SSE log streaming.
type LogsHandler struct {
	Manager        *process.Manager
	Security       *security.Manager
	FrontendOrigin string // Dikonfigurasi dari FRONTEND_ORIGIN env — jangan pakai "*"
}

// StreamLogs handler untuk GET /api/agents/:id/logs (SSE)
// Client terima event stream real-time dari stdout/stderr agent.
func (h *LogsHandler) StreamLogs(w http.ResponseWriter, r *http.Request) {
	// Extract agent ID dari path: /api/agents/<id>/logs
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		http.Error(w, "agent ID diperlukan", http.StatusBadRequest)
		return
	}
	agentID := parts[2]

	logCh, ok := h.Manager.GetLogChannel(agentID)

	// Setup SSE headers first so we don't break chunked encoding
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Security: JANGAN gunakan wildcard CORS di SSE — bisa bocorkan log agent ke origin asing.
	// Gunakan origin spesifik dari config.
	allowedOrigin := h.FrontendOrigin
	if allowedOrigin == "" {
		allowedOrigin = "http://localhost:5173"
	}
	w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")

	flusher, flusherOk := w.(http.Flusher)
	if !flusherOk {
		http.Error(w, "SSE tidak didukung", http.StatusInternalServerError)
		return
	}

	if !ok {
		// Agent tidak running. Jangan tutup stream seketika agar browser tidak spam ERR_INCOMPLETE_CHUNKED_ENCODING.
		// Tahan koneksi selama 10 detik lalu biarkan browser me-reconnect otomatis perlahan.
		fmt.Fprintf(w, "data: [%s][SYS] Agent saat ini tidak aktif.\n\n", agentID)
		flusher.Flush()

		select {
		case <-r.Context().Done():
			return
		case <-time.After(10 * time.Second):
			return
		}
	}

	// Kirim event awal
	fmt.Fprintf(w, "data: [%s][SYS] Log stream dimulai...\n\n", agentID)
	flusher.Flush()

	// Heartbeat ticker agar koneksi tidak timeout
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	// Stream loop
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			// Client disconnect
			return

		case line, open := <-logCh:
			if !open {
				// Channel closed = agent berhenti
				// Kirim event "close" agar frontend bisa .close() EventSource
				fmt.Fprintf(w, "event: close\ndata: [%s][SYS] Agent proses tidak aktif, stream berakhir.\n\n", agentID)
				flusher.Flush()
				return
			}
			if h.Security != nil {
				masked := h.Security.Mask(line, "")
				line = masked.Text
			}
			// Escape newlines dalam SSE data field
			escaped := strings.ReplaceAll(line, "\n", "\\n")
			fmt.Fprintf(w, "data: %s\n\n", escaped)
			flusher.Flush()

		case <-ticker.C:
			// Heartbeat/keep-alive
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}
