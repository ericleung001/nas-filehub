package api

import (
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) handleMeSessions(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	switch r.Method {
	case http.MethodGet:
		list, err := s.DB.ListUserSessions(u.ID)
		if err != nil {
			writeErr(w, 500, "db error")
			return
		}
		cur := s.sessionToken(r)
		out := make([]map[string]any, 0, len(list))
		for _, sess := range list {
			item := map[string]any{
				"id":         sess.ID,
				"createdAt":  sess.CreatedAt,
				"lastSeenAt": sess.LastSeenAt,
				"ip":         sess.IP,
				"userAgent":  sess.UserAgent,
				"expiresAt":  sess.ExpiresAt,
				"current":    cur != "" && sess.Token == cur,
			}
			out = append(out, item)
		}
		writeJSON(w, 200, map[string]any{"sessions": out})
	case http.MethodPost:
		// POST /api/me/sessions with {"revokeOthers":true} or path handled below
		writeErr(w, 405, "method not allowed")
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (s *Server) handleMeSessionID(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	rest := strings.TrimPrefix(r.URL.Path, "/api/me/sessions/")
	rest = strings.Trim(rest, "/")
	if rest == "revoke-others" {
		if r.Method != http.MethodPost && r.Method != http.MethodDelete {
			writeErr(w, 405, "method not allowed")
			return
		}
		keep := s.sessionToken(r)
		if err := s.DB.DeleteOtherSessions(u.ID, keep); err != nil {
			writeErr(w, 500, "db error")
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "bad id")
		return
	}
	if r.Method != http.MethodDelete {
		writeErr(w, 405, "method not allowed")
		return
	}
	ok, err := s.DB.DeleteSessionByID(u.ID, id)
	if err != nil {
		writeErr(w, 500, "db error")
		return
	}
	if !ok {
		writeErr(w, 404, "not found")
		return
	}
	// If the current session was revoked, clear the cookie.
	if cur := s.sessionToken(r); cur != "" {
		if sid, err := s.DB.SessionIDByToken(cur); err == nil && sid == 0 {
			s.clearSessionCookie(w)
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
