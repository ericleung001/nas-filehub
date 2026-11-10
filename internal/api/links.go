package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/eric/nas-filehub/internal/auth"
	"github.com/eric/nas-filehub/internal/db"
	"github.com/eric/nas-filehub/internal/files"
)

const linkUnlockCookie = "filehub_link_unlock"

func (s *Server) handleLinks(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	switch r.Method {
	case http.MethodGet:
		list, err := s.DB.ListLinkSharesForUser(u.ID, u.IsAdmin)
		if err != nil {
			writeErr(w, 500, "db error")
			return
		}
		writeJSON(w, 200, map[string]any{"links": list})
	case http.MethodPost:
		var body struct {
			Root      string `json:"root"`
			Path      string `json:"path"`
			Password  string `json:"password"`
			ExpiresAt string `json:"expiresAt"` // RFC3339 or empty
			Label     string `json:"label"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		body.Path = strings.TrimPrefix(strings.TrimSpace(body.Path), "/")
		if body.Path == "" {
			writeErr(w, 400, "path required")
			return
		}
		sc, err := s.resolveScope(u, body.Root, body.Path)
		if err != nil {
			writeErr(w, 403, "forbidden")
			return
		}
		full, err := files.SafeJoin(sc.Root, sc.Rel)
		if err != nil {
			writeErr(w, 400, "bad path")
			return
		}
		st, err := os.Stat(full)
		if err != nil {
			writeErr(w, 404, "not found")
			return
		}
		token, err := auth.NewToken()
		if err != nil {
			writeErr(w, 500, "token error")
			return
		}
		var exp *time.Time
		if strings.TrimSpace(body.ExpiresAt) != "" {
			t, err := time.Parse(time.RFC3339, body.ExpiresAt)
			if err != nil {
				writeErr(w, 400, "invalid expiresAt")
				return
			}
			if t.Before(time.Now()) {
				writeErr(w, 400, "expiresAt must be in the future")
				return
			}
			utc := t.UTC()
			exp = &utc
		}
		pwHash := ""
		if strings.TrimSpace(body.Password) != "" {
			h, err := db.HashPassword(body.Password)
			if err != nil {
				writeErr(w, 500, "hash error")
				return
			}
			pwHash = h
		}
		rootID := body.Root
		if rootID == "" {
			rootID = "home"
		}
		link, err := s.DB.CreateLinkShare(token, u.ID, rootID, body.Path, st.IsDir(), body.Label, pwHash, exp)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		detail := "path=" + body.Path
		if pwHash != "" {
			detail += " password=1"
		}
		if exp != nil {
			detail += " expires=1"
		}
		s.audit(r, u.ID, u.Username, "share_link_create", detail)
		writeJSON(w, 201, map[string]any{
			"link": link,
			"url":  "/s/" + link.Token,
		})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (s *Server) handleLinkID(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	rest := strings.TrimPrefix(r.URL.Path, "/api/links/")
	idStr := strings.Trim(rest, "/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || idStr == "" {
		writeErr(w, 400, "bad id")
		return
	}
	link, err := s.DB.GetLinkShareByID(id)
	if err != nil || link == nil {
		writeErr(w, 404, "not found")
		return
	}
	if !u.IsAdmin && link.OwnerID != u.ID {
		writeErr(w, 403, "forbidden")
		return
	}
	switch r.Method {
	case http.MethodDelete:
		if err := s.DB.DeleteLinkShare(id); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		s.audit(r, u.ID, u.Username, "share_link_revoke", "id="+itoaLocal(id))
		writeJSON(w, 200, map[string]string{"ok": "1"})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (s *Server) publicLink(token string) (*db.LinkShare, int, string) {
	link, err := s.DB.GetLinkShareByToken(token)
	if err != nil || link == nil {
		return nil, 404, "not found"
	}
	if link.Revoked {
		return nil, 410, "link revoked"
	}
	if link.ExpiresAt != nil && time.Now().After(*link.ExpiresAt) {
		return nil, 410, "link expired"
	}
	return link, 200, ""
}

func (s *Server) linkUnlocked(r *http.Request, link *db.LinkShare) bool {
	if link == nil || !link.HasPassword {
		return true
	}
	c, err := r.Cookie(linkUnlockCookie)
	if err != nil || c.Value == "" {
		return false
	}
	id, ok, err := s.DB.GetLinkUnlock(c.Value)
	if err != nil || !ok {
		return false
	}
	return id == link.ID
}

func (s *Server) resolvePublicLinkPath(link *db.LinkShare, sub string) (absRoot, rel string, err error) {
	owner, err := s.DB.GetUserByID(link.OwnerID)
	if err != nil || owner == nil || owner.Disabled {
		return "", "", os.ErrPermission
	}
	sub = strings.TrimPrefix(strings.TrimSpace(sub), "/")
	target := link.Path
	if link.IsDir && sub != "" {
		// sub must stay under the shared folder
		target = filepath.ToSlash(filepath.Join(link.Path, sub))
		cleanSub := filepath.ToSlash(filepath.Clean(sub))
		if cleanSub == ".." || strings.HasPrefix(cleanSub, "../") {
			return "", "", os.ErrPermission
		}
	} else if !link.IsDir && sub != "" && sub != filepath.Base(link.Path) {
		return "", "", os.ErrNotExist
	}
	sc, err := s.resolveScope(owner, link.RootID, target)
	if err != nil {
		return "", "", err
	}
	return sc.Root, sc.Rel, nil
}

func (s *Server) handlePublicLinkMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	token := strings.TrimPrefix(r.URL.Path, "/api/public/links/")
	token = strings.TrimSuffix(token, "/meta")
	token = strings.Trim(token, "/")
	if i := strings.IndexByte(token, '/'); i >= 0 {
		token = token[:i]
	}
	link, code, msg := s.publicLink(token)
	if link == nil {
		writeErr(w, code, msg)
		return
	}
	needPW := link.HasPassword && !s.linkUnlocked(r, link)
	writeJSON(w, 200, map[string]any{
		"label":        link.Label,
		"isDir":        link.IsDir,
		"hasPassword":  link.HasPassword,
		"needPassword": needPW,
		"expiresAt":    link.ExpiresAt,
		"pathName":     filepath.Base(link.Path),
	})
}

func (s *Server) handlePublicLinkUnlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	ip := s.clientIP(r)
	if s.Limiter != nil && !s.Limiter.Allow(ip) {
		writeErr(w, http.StatusTooManyRequests, "too many unlock attempts")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/public/links/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 || parts[1] != "unlock" {
		writeErr(w, 404, "not found")
		return
	}
	token := parts[0]
	link, code, msg := s.publicLink(token)
	if link == nil {
		writeErr(w, code, msg)
		return
	}
	if !link.HasPassword {
		writeJSON(w, 200, map[string]string{"ok": "1"})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if !db.CheckPassword(link.PasswordHash, body.Password) {
		writeErr(w, 401, "invalid password")
		return
	}
	unlock, err := auth.NewToken()
	if err != nil {
		writeErr(w, 500, "token error")
		return
	}
	exp := time.Now().Add(12 * time.Hour)
	if err := s.DB.CreateLinkUnlock(unlock, link.ID, exp); err != nil {
		writeErr(w, 500, "db error")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     linkUnlockCookie,
		Value:    unlock,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.Cfg.SecureCookies,
		Expires:  exp,
	})
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

func (s *Server) requirePublicLink(w http.ResponseWriter, r *http.Request, token string) *db.LinkShare {
	link, code, msg := s.publicLink(token)
	if link == nil {
		writeErr(w, code, msg)
		return nil
	}
	if link.HasPassword && !s.linkUnlocked(r, link) {
		writeErr(w, 401, "password required")
		return nil
	}
	return link
}

func (s *Server) handlePublicLinkList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/public/links/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 || parts[1] != "list" {
		writeErr(w, 404, "not found")
		return
	}
	link := s.requirePublicLink(w, r, parts[0])
	if link == nil {
		return
	}
	sub := r.URL.Query().Get("path")
	if !link.IsDir {
		// Single file share: return one synthetic entry
		root, rel, err := s.resolvePublicLinkPath(link, "")
		if err != nil {
			writeErr(w, 403, "forbidden")
			return
		}
		full, err := files.SafeJoin(root, rel)
		if err != nil {
			writeErr(w, 400, "bad path")
			return
		}
		st, err := os.Stat(full)
		if err != nil {
			writeErr(w, 404, "not found")
			return
		}
		name := filepath.Base(rel)
		entry := files.Entry{
			Name:    name,
			Path:    name,
			IsDir:   false,
			Size:    st.Size(),
			ModTime: st.ModTime().UTC(),
			Ext:     strings.ToLower(filepath.Ext(name)),
			Kind:    files.KindOf(name, false),
		}
		writeJSON(w, 200, map[string]any{"path": "", "isDir": false, "entries": []files.Entry{entry}})
		return
	}
	root, rel, err := s.resolvePublicLinkPath(link, sub)
	if err != nil {
		writeErr(w, 403, "forbidden")
		return
	}
	entries, err := files.List(root, rel)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// Rewrite paths relative to the shared folder root for the public UI
	base := strings.TrimPrefix(link.Path, "/")
	out := make([]files.Entry, 0, len(entries))
	for _, e := range entries {
		p := e.Path
		if base != "" && strings.HasPrefix(p, base+"/") {
			p = strings.TrimPrefix(p, base+"/")
		} else if p == base {
			p = e.Name
		}
		e.Path = p
		out = append(out, e)
	}
	pubPath := strings.TrimPrefix(sub, "/")
	writeJSON(w, 200, map[string]any{"path": pubPath, "isDir": true, "entries": out})
}

func (s *Server) handlePublicLinkDownload(w http.ResponseWriter, r *http.Request) {
	s.servePublicFile(w, r, true)
}

func (s *Server) handlePublicLinkRaw(w http.ResponseWriter, r *http.Request) {
	s.servePublicFile(w, r, false)
}

func (s *Server) servePublicFile(w http.ResponseWriter, r *http.Request, asAttachment bool) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/public/links/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 {
		writeErr(w, 404, "not found")
		return
	}
	action := parts[1]
	if action != "download" && action != "raw" {
		writeErr(w, 404, "not found")
		return
	}
	link := s.requirePublicLink(w, r, parts[0])
	if link == nil {
		return
	}
	sub := r.URL.Query().Get("path")
	if !link.IsDir {
		sub = ""
	}
	root, rel, err := s.resolvePublicLinkPath(link, sub)
	if err != nil {
		writeErr(w, 403, "forbidden")
		return
	}
	f, st, err := files.Open(root, rel)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	defer f.Close()
	if asAttachment {
		setFileDisposition(w, st.Name(), false)
	} else {
		setInlineFileHeaders(w, st.Name())
	}
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

// handlePublicLinks routes /api/public/links/{token}/...
func (s *Server) handlePublicLinks(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/public/links/")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		writeErr(w, 404, "not found")
		return
	}
	parts := strings.Split(rest, "/")
	token := parts[0]
	if len(parts) == 1 || parts[1] == "meta" {
		// rewrite path for meta handler
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/api/public/links/" + token + "/meta"
		s.handlePublicLinkMeta(w, r2)
		return
	}
	switch parts[1] {
	case "unlock":
		s.handlePublicLinkUnlock(w, r)
	case "list":
		s.handlePublicLinkList(w, r)
	case "download":
		s.handlePublicLinkDownload(w, r)
	case "raw":
		s.handlePublicLinkRaw(w, r)
	default:
		writeErr(w, 404, "not found")
	}
}
