// handlers/personas.go
// Read/write SOUL.md persona per agent.

package handlers

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// PersonasHandler mengelola SOUL.md per agent.
type PersonasHandler struct {
	App *App
}

// GetPersona handler untuk GET /api/agents/:id/persona
func (h *PersonasHandler) GetPersona(w http.ResponseWriter, r *http.Request) {
	agentID := extractAgentID(r.URL.Path, "persona")
	if agentID == "" {
		jsonError(w, http.StatusBadRequest, "Invalid Agent ID")
		return
	}
	soulPath := ResolveSoulMDPath(h.App.Cfg.OpenClawWorkspaceDir, agentID)

	data, err := readRegularFileNoSymlink(soulPath, 2<<20)
	if os.IsNotExist(err) {
		jsonError(w, http.StatusNotFound, "SOUL.md tidak ditemukan untuk agent "+agentID)
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Gagal baca SOUL.md: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	jsonOK(w, map[string]string{
		"agent_id": agentID,
		"content":  string(data),
		"path":     soulPath,
	})
}

// UpdatePersona handler untuk PUT /api/agents/:id/persona
func (h *PersonasHandler) UpdatePersona(w http.ResponseWriter, r *http.Request) {
	agentID := extractAgentID(r.URL.Path, "persona")
	if agentID == "" {
		jsonError(w, http.StatusBadRequest, "Invalid Agent ID")
		return
	}

	// Fix CWE-400: Batasi payload maksimal 2MB untuk mencegah memory exhaustion
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "Gagal baca request body: "+err.Error())
		return
	}
	defer r.Body.Close()

	content := strings.TrimSpace(string(body))
	if content == "" {
		jsonError(w, http.StatusBadRequest, "Konten SOUL.md tidak boleh kosong")
		return
	}

	soulPath := ResolveSoulMDPath(h.App.Cfg.OpenClawWorkspaceDir, agentID)
	soulDir := filepath.Dir(soulPath)

	if err := mkdirSecure(soulDir); err != nil {
		jsonError(w, http.StatusInternalServerError, "Gagal buat direktori: "+err.Error())
		return
	}

	if err := writeFileNoSymlink(soulPath, []byte(content)); err != nil {
		jsonError(w, http.StatusInternalServerError, "Gagal tulis SOUL.md: "+err.Error())
		return
	}

	jsonOK(w, map[string]string{
		"message":  "SOUL.md berhasil diperbarui untuk agent " + agentID,
		"agent_id": agentID,
		"path":     soulPath,
	})
}
