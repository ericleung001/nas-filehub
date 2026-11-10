package api

import (
	"net/http"
	"strconv"
)

func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	events, total, err := s.DB.ListAudit(limit, offset)
	if err != nil {
		writeErr(w, 500, "db error")
		return
	}
	if events == nil {
		events = nil
	}
	writeJSON(w, 200, map[string]any{
		"events": events,
		"total":  total,
		"limit":  clampAuditLimit(limit),
		"offset": offset,
	})
}

func clampAuditLimit(limit int) int {
	if limit <= 0 || limit > 200 {
		return 50
	}
	return limit
}
