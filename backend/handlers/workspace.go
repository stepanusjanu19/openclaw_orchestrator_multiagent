// handlers/workspace.go
// Read/write multi-file workspace per agent.

package handlers

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type WorkspaceHandler struct {
	App *App
}

func sanitizeFilename(name string) string {
	name = filepath.Clean(name)
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return ""
	}
	if !strings.HasSuffix(name, ".md") {
		return "" // strictly markdown files
	}
	return name
}

// GetWorkspaceFiles GET /api/agents/:id/workspace
func (h *WorkspaceHandler) GetWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
	agentID := extractAgentID(r.URL.Path, "workspace")
	if agentID == "" {
		jsonError(w, http.StatusBadRequest, "Invalid Agent ID")
		return
	}
	soulPath := ResolveSoulMDPath(h.App.Cfg.OpenClawWorkspaceDir, agentID)
	soulDir := filepath.Dir(soulPath)

	files, err := filepath.Glob(filepath.Join(soulDir, "*.md"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Gagal scan workspace: "+err.Error())
		return
	}

	var filenames []string
	for _, f := range files {
		st, err := os.Lstat(f)
		if err != nil || st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
			continue
		}
		filenames = append(filenames, filepath.Base(f))
	}

	w.Header().Set("Content-Type", "application/json")
	jsonOK(w, map[string]interface{}{
		"agent_id": agentID,
		"files":    filenames,
		"dir":      soulDir,
	})
}

// GetWorkspaceFile GET /api/agents/:id/workspace/file?name=AGENTS.md
func (h *WorkspaceHandler) GetWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	agentID := extractAgentID(r.URL.Path, "workspace")
	if agentID == "" {
		jsonError(w, http.StatusBadRequest, "Invalid Agent ID")
		return
	}
	filename := r.URL.Query().Get("name")

	if filename == "" {
		filename = "SOUL.md" // fallback
	}

	cleanName := sanitizeFilename(filename)
	if cleanName == "" {
		jsonError(w, http.StatusBadRequest, "Invalid filename")
		return
	}

	soulPath := ResolveSoulMDPath(h.App.Cfg.OpenClawWorkspaceDir, agentID)
	soulDir := filepath.Dir(soulPath)
	targetPath := filepath.Join(soulDir, cleanName)

	data, err := readRegularFileNoSymlink(targetPath, 5<<20)
	if os.IsNotExist(err) {
		jsonError(w, http.StatusNotFound, fmt.Sprintf("File %s tidak ditemukan di workspace agent %s", cleanName, agentID))
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Gagal baca file: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	jsonOK(w, map[string]string{
		"agent_id": agentID,
		"filename": cleanName,
		"content":  string(data),
	})
}

// UpdateWorkspaceFile PUT /api/agents/:id/workspace/file?name=AGENTS.md
func (h *WorkspaceHandler) UpdateWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	agentID := extractAgentID(r.URL.Path, "workspace")
	if agentID == "" {
		jsonError(w, http.StatusBadRequest, "Invalid Agent ID")
		return
	}
	filename := r.URL.Query().Get("name")

	if filename == "" {
		filename = "SOUL.md"
	}

	cleanName := sanitizeFilename(filename)
	if cleanName == "" {
		jsonError(w, http.StatusBadRequest, "Invalid filename")
		return
	}

	// Fix CWE-400: Batasi ukuran file workspace maksimal 5MB untuk mencegah memory exhaustion
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "Gagal baca request body: "+err.Error())
		return
	}
	defer r.Body.Close()

	content := strings.TrimSpace(string(body))

	soulPath := ResolveSoulMDPath(h.App.Cfg.OpenClawWorkspaceDir, agentID)
	soulDir := filepath.Dir(soulPath)
	targetPath := filepath.Join(soulDir, cleanName)

	if err := mkdirSecure(soulDir); err != nil {
		jsonError(w, http.StatusInternalServerError, "Gagal buat direktori: "+err.Error())
		return
	}

	if err := writeFileNoSymlink(targetPath, []byte(content)); err != nil {
		jsonError(w, http.StatusInternalServerError, "Gagal tulis file: "+err.Error())
		return
	}

	jsonOK(w, map[string]string{
		"message":  fmt.Sprintf("%s berhasil diperbarui untuk agent %s", cleanName, agentID),
		"agent_id": agentID,
		"filename": cleanName,
	})
}
