package api

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/eric/nas-filehub/internal/auth"
	"github.com/eric/nas-filehub/internal/config"
	"github.com/eric/nas-filehub/internal/db"
	"github.com/eric/nas-filehub/internal/files"
	"github.com/eric/nas-filehub/internal/thumb"
)

type Server struct {
	Cfg          config.Config
	DB           *db.DB
	Thumbs       *thumb.Generator
	Limiter      *auth.LoginLimiter
	ResetLimiter *auth.LoginLimiter
	Static       fs.FS
}

type scope struct {
	Root string // absolute filesystem root for this scope
	Rel  string // relative path inside that root (requested)
	Perm string // read|write
	Kind string // home|share
	ID   int64  // share id if share
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/login/totp", s.handleLoginTotp)
	mux.HandleFunc("/api/login/totp/setup", s.handleLoginTotpSetup)
	mux.HandleFunc("/api/login/totp/enable", s.handleLoginTotpEnable)
	mux.HandleFunc("/api/auth/features", s.handleAuthFeatures)
	mux.HandleFunc("/api/register", s.handleRegister)
	mux.HandleFunc("/api/forgot-password", s.handleForgotPassword)
	mux.HandleFunc("/api/reset-password", s.handleResetPassword)
	mux.HandleFunc("/api/reset-password/meta", s.handleResetPasswordMeta)
	mux.HandleFunc("/api/logout", s.requireAuth(s.handleLogout))
	mux.HandleFunc("/api/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("/api/me/password", s.requireAuth(s.handleMePassword))
	mux.HandleFunc("/api/me/totp/setup", s.requireAuth(s.handleMeTotpSetup))
	mux.HandleFunc("/api/me/totp/confirm", s.requireAuth(s.handleMeTotpConfirm))
	mux.HandleFunc("/api/me/totp/disable", s.requireAuth(s.handleMeTotpDisable))
	mux.HandleFunc("/api/roots", s.requireAuth(s.handleRoots))
	mux.HandleFunc("/api/list", s.requireAuth(s.handleList))
	mux.HandleFunc("/api/mkdir", s.requireAuth(s.handleMkdir))
	mux.HandleFunc("/api/rename", s.requireAuth(s.handleRename))
	mux.HandleFunc("/api/delete", s.requireAuth(s.handleDelete))
	mux.HandleFunc("/api/batch/delete", s.requireAuth(s.handleBatchDelete))
	mux.HandleFunc("/api/batch/move", s.requireAuth(s.handleBatchMove))
	mux.HandleFunc("/api/batch/copy", s.requireAuth(s.handleBatchCopy))
	mux.HandleFunc("/api/upload", s.requireAuth(s.handleUpload))
	mux.HandleFunc("/api/download", s.requireAuth(s.handleDownload))
	mux.HandleFunc("/api/raw", s.requireAuth(s.handleRaw))
	mux.HandleFunc("/api/thumb", s.requireAuth(s.handleThumb))
	mux.HandleFunc("/api/links", s.requireAuth(s.handleLinks))
	mux.HandleFunc("/api/links/", s.requireAuth(s.handleLinkID))
	mux.HandleFunc("/api/public/links/", s.handlePublicLinks)
	mux.HandleFunc("/api/admin/users", s.requireAdmin(s.handleAdminUsers))
	mux.HandleFunc("/api/admin/users/", s.requireAdmin(s.handleAdminUserID))
	mux.HandleFunc("/api/admin/shares", s.requireAdmin(s.handleAdminShares))
	mux.HandleFunc("/api/admin/shares/", s.requireAdmin(s.handleAdminShareID))
	mux.HandleFunc("/api/admin/settings", s.requireAdmin(s.handleAdminSettings))
	mux.HandleFunc("/api/admin/smtp/test", s.requireAdmin(s.handleAdminSMTPTest))

	mux.Handle("/", s.spaHandler())
	return s.securityHeaders(mux)
}

func (s *Server) Handler() http.Handler { return s.routes() }

func (s *Server) spaHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.Static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		// Never hand FileServer a path of "/index.html": it 301-redirects that
		// to "./", which under /s/{token} resolves to /s/ and loops forever.
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" || path == "index.html" {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		if f, err := s.Static.Open(path); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		} else {
			_ = f.Close()
		}
		fileServer.ServeHTTP(w, r)
	})
}

func allowSameOriginFrame(path string) bool {
	if path == "/api/raw" || path == "/api/thumb" {
		return true
	}
	// Public share raw for inline image/video/PDF preview in the SPA.
	if strings.HasPrefix(path, "/api/public/links/") && strings.HasSuffix(path, "/raw") {
		return true
	}
	return false
}

func setInlineFileHeaders(w http.ResponseWriter, name string) {
	// Override global DENY so same-origin <iframe>/<embed> can preview PDFs.
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'")
	w.Header().Del("Content-Disposition")
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".pdf" {
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `inline; filename="`+filepath.Base(name)+`"`)
	}
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-XSS-Protection", "0")
		if allowSameOriginFrame(r.URL.Path) {
			w.Header().Set("X-Frame-Options", "SAMEORIGIN")
			w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'")
		} else {
			w.Header().Set("X-Frame-Options", "DENY")
		}
		// No command execution endpoint exists by design.
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) currentUser(r *http.Request) *db.User {
	c, err := r.Cookie(auth.CookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	u, err := s.DB.GetSessionUser(c.Value)
	if err != nil || u == nil {
		return nil
	}
	return u
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		if u == nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if u.Disabled {
			writeErr(w, http.StatusUnauthorized, "account disabled")
			return
		}
		path := r.URL.Path
		if s.userMustChangePassword(u) {
			allowed := path == "/api/me" ||
				path == "/api/me/password" ||
				path == "/api/logout"
			if !allowed {
				writeErr(w, http.StatusForbidden, "password change required")
				return
			}
		} else if s.userMustEnrollTotp(u) {
			allowed := path == "/api/me" ||
				path == "/api/me/totp/setup" ||
				path == "/api/me/totp/confirm" ||
				path == "/api/logout"
			if !allowed {
				writeErr(w, http.StatusForbidden, "2FA setup required")
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		if u == nil || !u.IsAdmin {
			writeErr(w, http.StatusForbidden, "admin only")
			return
		}
		next(w, r)
	})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.Cfg.SecureCookies,
		Expires:  expires,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.Cfg.SecureCookies,
		MaxAge:   -1,
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "service": "nas-filehub"})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	ip := auth.ClientIP(r)
	if !s.Limiter.Allow(ip) {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	u, err := s.DB.GetUserByUsername(body.Username)
	if err != nil || u == nil || !db.CheckPassword(u.PasswordHash, body.Password) {
		writeErr(w, 401, "invalid credentials")
		return
	}
	if u.Disabled {
		writeErr(w, 401, "account disabled")
		return
	}
	mustChangePW := s.passwordNeedsChange(u, body.Password)
	needCode, needEnroll := s.DB.UserNeedsTOTP(u, s.force2FAAll())
	if needCode || needEnroll {
		pend, err := auth.NewToken()
		if err != nil {
			writeErr(w, 500, "session error")
			return
		}
		kind := "totp"
		if needEnroll {
			kind = "enroll"
		}
		exp := time.Now().Add(10 * time.Minute)
		_ = s.DB.DeletePendingLoginsForUser(u.ID)
		if err := s.DB.CreatePendingLogin(pend, u.ID, kind, exp, mustChangePW); err != nil {
			writeErr(w, 500, "session error")
			return
		}
		out := map[string]any{
			"pendingToken": pend,
			"username":     u.Username,
		}
		if needCode {
			out["needTotp"] = true
		} else {
			out["needTotpSetup"] = true
		}
		writeJSON(w, 200, out)
		return
	}
	if err := s.issueSession(w, u); err != nil {
		writeErr(w, 500, "session error")
		return
	}
	// Refresh user so public flags see current DB state
	if u2, err := s.DB.GetUserByID(u.ID); err == nil && u2 != nil {
		u = u2
	}
	m := s.userPublic(u)
	if mustChangePW {
		m["mustChangePassword"] = true
	}
	m["usedBytes"] = s.homeUsedBytes(u)
	writeJSON(w, 200, map[string]any{"user": m})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	if c, err := r.Cookie(auth.CookieName); err == nil {
		_ = s.DB.DeleteSession(c.Value)
	}
	s.clearSessionCookie(w)
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

func (s *Server) userPublic(u *db.User) map[string]any {
	if u == nil {
		return map[string]any{}
	}
	forceAll := s.force2FAAll()
	required := forceAll || u.TotpRequired
	pol, _ := s.DB.GetPasswordPolicy()
	mustChange := s.userMustChangePassword(u)
	mustEnroll := required && !u.TotpEnabled
	// Password gate takes priority over 2FA enroll for the flag the UI reads first.
	return map[string]any{
		"id": u.ID, "username": u.Username, "email": u.Email, "isAdmin": u.IsAdmin, "homePath": u.HomePath,
		"quotaBytes": u.QuotaBytes, "disabled": u.Disabled,
		"totpEnabled": u.TotpEnabled, "totpRequired": u.TotpRequired,
		"force2faAll": forceAll, "mustEnrollTotp": mustEnroll && !mustChange,
		"mustChangePassword": mustChange,
		"passwordPolicy":     pol,
		"passwordPolicyHint": db.PasswordPolicySummary(pol),
	}
}

func (s *Server) passwordPolicy() db.PasswordPolicy {
	p, err := s.DB.GetPasswordPolicy()
	if err != nil {
		return db.DefaultPasswordPolicy()
	}
	return p
}

func (s *Server) userMustChangePassword(u *db.User) bool {
	if u == nil {
		return false
	}
	pol := s.passwordPolicy()
	return u.PasswordPolicyVersion < pol.Version
}

// passwordNeedsChange is true when plaintext fails the current policy or the
// user's stamped policy version is behind (admin tightened rules).
func (s *Server) passwordNeedsChange(u *db.User, plain string) bool {
	if s.userMustChangePassword(u) {
		return true
	}
	if db.ValidatePassword(plain, s.passwordPolicy()) != nil {
		// Stamp behind so requireAuth enforces the gate even when versions matched.
		_ = s.DB.MarkPasswordChangeRequired(u.ID)
		u.PasswordPolicyVersion = -1
		return true
	}
	return false
}

func (s *Server) homeUsedBytes(u *db.User) int64 {
	used, err := files.DirSize(s.Cfg.DataRoot, u.HomePath)
	if err != nil {
		return 0
	}
	return used
}

func (s *Server) checkHomeQuota(u *db.User, extra int64) error {
	if u == nil || u.QuotaBytes <= 0 {
		return nil
	}
	used := s.homeUsedBytes(u)
	if used+extra > u.QuotaBytes {
		return fmt.Errorf("quota exceeded: used %d + %d > limit %d bytes", used, extra, u.QuotaBytes)
	}
	return nil
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	u := s.currentUser(r)
	m := s.userPublic(u)
	m["usedBytes"] = s.homeUsedBytes(u)
	writeJSON(w, 200, map[string]any{"user": m})
}

func (s *Server) handleMePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	u := s.currentUser(r)
	var body struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if strings.TrimSpace(body.NewPassword) == "" {
		writeErr(w, 400, "new password required")
		return
	}
	if err := db.ValidatePassword(body.NewPassword, s.passwordPolicy()); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if !db.CheckPassword(u.PasswordHash, body.CurrentPassword) {
		writeErr(w, 401, "current password incorrect")
		return
	}
	keep := ""
	if c, err := r.Cookie(auth.CookieName); err == nil {
		keep = c.Value
	}
	if err := s.DB.SetUserPasswordKeepSession(u.ID, body.NewPassword, keep); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	u2, _ := s.DB.GetUserByID(u.ID)
	m := s.userPublic(u2)
	m["usedBytes"] = s.homeUsedBytes(u2)
	writeJSON(w, 200, map[string]any{"ok": "1", "user": m})
}

func (s *Server) handleRoots(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	type root struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
		Perm string `json:"perm"`
	}
	out := []root{{ID: "home", Name: "My Files", Kind: "home", Perm: "write"}}
	shares, err := s.DB.SharesForUser(u.ID, u.IsAdmin)
	if err != nil {
		writeErr(w, 500, "db error")
		return
	}
	for _, sa := range shares {
		out = append(out, root{
			ID:   "share:" + strconv.FormatInt(sa.Share.ID, 10),
			Name: sa.Share.Name,
			Kind: "share",
			Perm: sa.Perm,
		})
	}
	writeJSON(w, 200, map[string]any{"roots": out})
}

func (s *Server) resolveScope(u *db.User, rootID, rel string) (*scope, error) {
	rel = strings.TrimPrefix(rel, "/")
	if rootID == "" || rootID == "home" {
		abs, err := files.SafeJoin(s.Cfg.DataRoot, u.HomePath)
		if err != nil {
			return nil, err
		}
		return &scope{Root: abs, Rel: rel, Perm: "write", Kind: "home"}, nil
	}
	if strings.HasPrefix(rootID, "share:") {
		idStr := strings.TrimPrefix(rootID, "share:")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return nil, err
		}
		share, err := s.DB.GetShare(id)
		if err != nil || share == nil {
			return nil, os.ErrNotExist
		}
		perm := "write"
		if !u.IsAdmin {
			p, err := s.DB.GrantPerm(id, u.ID)
			if err != nil || p == "" {
				return nil, os.ErrPermission
			}
			perm = p
		}
		abs, err := files.SafeJoin(s.Cfg.DataRoot, share.Path)
		if err != nil {
			return nil, err
		}
		return &scope{Root: abs, Rel: rel, Perm: perm, Kind: "share", ID: id}, nil
	}
	return nil, os.ErrNotExist
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	rootID := r.URL.Query().Get("root")
	rel := r.URL.Query().Get("path")
	sc, err := s.resolveScope(u, rootID, rel)
	if err != nil {
		writeErr(w, 403, "forbidden")
		return
	}
	entries, err := files.List(sc.Root, sc.Rel)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"path": sc.Rel, "perm": sc.Perm, "entries": entries})
}

func (s *Server) handleMkdir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	u := s.currentUser(r)
	var body struct {
		Root string `json:"root"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	sc, err := s.resolveScope(u, body.Root, body.Path)
	if err != nil || sc.Perm != "write" {
		writeErr(w, 403, "forbidden")
		return
	}
	if sc.Kind == "home" {
		if err := s.checkHomeQuota(u, 0); err != nil {
			writeErr(w, 403, err.Error())
			return
		}
	}
	if err := files.Mkdir(sc.Root, sc.Rel); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	u := s.currentUser(r)
	var body struct {
		Root string `json:"root"`
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	sc, err := s.resolveScope(u, body.Root, body.From)
	if err != nil || sc.Perm != "write" {
		writeErr(w, 403, "forbidden")
		return
	}
	if err := files.Rename(sc.Root, body.From, body.To); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	u := s.currentUser(r)
	var body struct {
		Root string `json:"root"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	sc, err := s.resolveScope(u, body.Root, body.Path)
	if err != nil || sc.Perm != "write" {
		writeErr(w, 403, "forbidden")
		return
	}
	if err := files.Delete(sc.Root, sc.Rel); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	rootID := body.Root
	if rootID == "" {
		rootID = "home"
	}
	_, _ = s.DB.DeleteLinkSharesForPath(rootID, sc.Rel)
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	u := s.currentUser(r)
	max := s.Cfg.MaxUploadMB << 20
	r.Body = http.MaxBytesReader(w, r.Body, max+1<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, 400, "upload too large or bad multipart")
		return
	}
	rootID := r.FormValue("root")
	dir := r.FormValue("path")
	sc, err := s.resolveScope(u, rootID, dir)
	if err != nil || sc.Perm != "write" {
		writeErr(w, 403, "forbidden")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "file required")
		return
	}
	defer file.Close()
	if sc.Kind == "home" {
		extra := hdr.Size
		if extra < 0 {
			extra = 0
		}
		if err := s.checkHomeQuota(u, extra); err != nil {
			writeErr(w, 403, err.Error())
			return
		}
	}
	saved, err := files.SaveUpload(sc.Root, sc.Rel, hdr.Filename, file)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"path": saved})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	sc, err := s.resolveScope(u, r.URL.Query().Get("root"), r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, 403, "forbidden")
		return
	}
	f, st, err := files.Open(sc.Root, sc.Rel)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(st.Name())+`"`)
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	sc, err := s.resolveScope(u, r.URL.Query().Get("root"), r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, 403, "forbidden")
		return
	}
	f, st, err := files.Open(sc.Root, sc.Rel)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	defer f.Close()
	setInlineFileHeaders(w, st.Name())
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	sc, err := s.resolveScope(u, r.URL.Query().Get("root"), r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, 403, "forbidden")
		return
	}
	full, err := files.SafeJoin(sc.Root, sc.Rel)
	if err != nil {
		writeErr(w, 400, "bad path")
		return
	}
	out, err := s.Thumbs.Get(full)
	if err != nil {
		// graceful fallback: redirect to raw for images
		http.Redirect(w, r, "/api/raw?root="+r.URL.Query().Get("root")+"&path="+r.URL.Query().Get("path"), http.StatusFound)
		return
	}
	http.ServeFile(w, r, out)
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		users, err := s.DB.ListUsers()
		if err != nil {
			writeErr(w, 500, "db error")
			return
		}
		out := make([]map[string]any, 0, len(users))
		for i := range users {
			u := &users[i]
			m := s.userPublic(u)
			m["usedBytes"] = s.homeUsedBytes(u)
			m["createdAt"] = u.CreatedAt
			out = append(out, m)
		}
		writeJSON(w, 200, map[string]any{"users": out})
	case http.MethodPost:
		var body struct {
			Username   string `json:"username"`
			Password   string `json:"password"`
			HomePath   string `json:"homePath"`
			IsAdmin    bool   `json:"isAdmin"`
			QuotaBytes int64  `json:"quotaBytes"`
			Email      string `json:"email"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		if err := db.ValidatePassword(body.Password, s.passwordPolicy()); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		u, err := s.DB.CreateUser(body.Username, body.Password, body.HomePath, body.IsAdmin, body.QuotaBytes, body.Email)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		abs, err := files.SafeJoin(s.Cfg.DataRoot, u.HomePath)
		if err == nil {
			_ = files.EnsureDir(abs)
		}
		m := s.userPublic(u)
		m["usedBytes"] = int64(0)
		m["createdAt"] = u.CreatedAt
		writeJSON(w, 201, map[string]any{"user": m})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (s *Server) handleAdminUserID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/users/")
	parts := strings.Split(rest, "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || parts[0] == "" {
		writeErr(w, 400, "bad id")
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			u, err := s.DB.GetUserByID(id)
			if err != nil || u == nil {
				writeErr(w, 404, "not found")
				return
			}
			u.PasswordHash = ""
			m := s.userPublic(u)
			m["usedBytes"] = s.homeUsedBytes(u)
			m["createdAt"] = u.CreatedAt
			writeJSON(w, 200, map[string]any{"user": m})
		case http.MethodPatch, http.MethodPut:
			var body struct {
				Username     *string `json:"username"`
				Email        *string `json:"email"`
				IsAdmin      *bool   `json:"isAdmin"`
				HomePath     *string `json:"homePath"`
				QuotaBytes   *int64  `json:"quotaBytes"`
				Disabled     *bool   `json:"disabled"`
				TotpRequired *bool   `json:"totpRequired"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
				writeErr(w, 400, "bad json")
				return
			}
			me := s.currentUser(r)
			if body.Disabled != nil && *body.Disabled && me.ID == id {
				writeErr(w, 400, "cannot disable yourself")
				return
			}
			if body.IsAdmin != nil && !*body.IsAdmin && me.ID == id {
				writeErr(w, 400, "cannot remove your own admin flag")
				return
			}
			u, err := s.DB.UpdateUser(id, db.UserUpdate{
				Username: body.Username, Email: body.Email, IsAdmin: body.IsAdmin, HomePath: body.HomePath,
				QuotaBytes: body.QuotaBytes, Disabled: body.Disabled, TotpRequired: body.TotpRequired,
			})
			if err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			u.PasswordHash = ""
			m := s.userPublic(u)
			m["usedBytes"] = s.homeUsedBytes(u)
			m["createdAt"] = u.CreatedAt
			writeJSON(w, 200, map[string]any{"user": m})
		case http.MethodDelete:
			me := s.currentUser(r)
			if me.ID == id {
				writeErr(w, 400, "cannot delete yourself")
				return
			}
			if err := s.DB.DeleteUser(id); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, map[string]string{"ok": "1"})
		default:
			writeErr(w, 405, "method not allowed")
		}
		return
	}
	if parts[1] == "password" && len(parts) == 2 {
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		if err := db.ValidatePassword(body.Password, s.passwordPolicy()); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		if err := s.DB.SetUserPassword(id, body.Password); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"ok": "1"})
		return
	}
	writeErr(w, 404, "not found")
}

func (s *Server) handleAdminShares(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		shares, err := s.DB.ListShares()
		if err != nil {
			writeErr(w, 500, "db error")
			return
		}
		type shareOut struct {
			db.Share
			Grants []db.ShareGrant `json:"grants"`
		}
		out := make([]shareOut, 0, len(shares))
		for _, sh := range shares {
			grants, _ := s.DB.ListGrants(sh.ID)
			out = append(out, shareOut{Share: sh, Grants: grants})
		}
		writeJSON(w, 200, map[string]any{"shares": out})
	case http.MethodPost:
		var body struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		me := s.currentUser(r)
		sh, err := s.DB.CreateShare(body.Name, body.Path, me.ID)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		abs, err := files.SafeJoin(s.Cfg.DataRoot, sh.Path)
		if err == nil {
			_ = files.EnsureDir(abs)
		}
		writeJSON(w, 201, map[string]any{"share": sh})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (s *Server) handleAdminShareID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/shares/")
	parts := strings.Split(rest, "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	if len(parts) == 1 {
		if r.Method == http.MethodDelete {
			if err := s.DB.DeleteShare(id); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, map[string]string{"ok": "1"})
			return
		}
		writeErr(w, 405, "method not allowed")
		return
	}
	if parts[1] == "grants" {
		switch r.Method {
		case http.MethodPost:
			var body struct {
				UserID int64  `json:"userId"`
				Perm   string `json:"perm"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeErr(w, 400, "bad json")
				return
			}
			if err := s.DB.SetShareGrant(id, body.UserID, body.Perm); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, map[string]string{"ok": "1"})
		case http.MethodDelete:
			uid, _ := strconv.ParseInt(r.URL.Query().Get("userId"), 10, 64)
			if err := s.DB.RemoveShareGrant(id, uid); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, map[string]string{"ok": "1"})
		default:
			writeErr(w, 405, "method not allowed")
		}
		return
	}
	writeErr(w, 404, "not found")
}
