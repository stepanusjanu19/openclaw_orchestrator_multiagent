package handlers

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"multiagent-platform/internal/security"
	"multiagent-platform/internal/visitor"
)

type VisitorsHandler struct {
	App *App
}

func (h *VisitorsHandler) Query(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.App == nil || h.App.Visitors == nil || !h.App.Visitors.Enabled() {
		jsonOK(w, visitor.QueryResult{Rows: []visitor.Record{}, Limit: 25, Offset: 0})
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > visitor.MaxQueryLen {
		jsonError(w, http.StatusBadRequest, "query visitor terlalu panjang")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset > visitor.MaxOffset {
		offset = visitor.MaxOffset
	}
	result, err := h.App.Visitors.Query(r.Context(), visitor.Query{
		Q:      q,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		log.Printf("[visitor] query failed: %s", security.MaskKnownSecrets(err.Error()))
		jsonError(w, http.StatusInternalServerError, "gagal query visitor database")
		return
	}
	jsonOK(w, result)
}
