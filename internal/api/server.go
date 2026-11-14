package api

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eric/nas-filehub/internal/auth"
	"github.com/eric/nas-filehub/internal/config"
	"github.com/eric/nas-filehub/internal/db"
	"github.com/eric/nas-filehub/internal/files"
	"github.com/eric/nas-filehub/internal/thumb"
	"github.com/eric/nas-filehub/internal/trash"
	"github.com/eric/nas-filehub/internal/version"
)

type Server struct {
	Cfg          config.Config
	DB           *db.DB
	Thumbs       *thumb.Generator
	Limiter      *auth.LoginLimiter
	ResetLimiter *auth.LoginLimiter
	Static       fs.FS
	Uploads      *UploadManager
	// MailSend, if set, is used instead of mail.Send (tests).
	MailSend func(cfg db.SMTPSettings, to, subject, body string) error
	// NotifySync sends notification emails in-process instead of a goroutine (tests).
	NotifySync bool

	uploadsOnce sync.Once
	notifyMu    sync.Mutex
	notifyLast  map[string]time.Time
	davOnce     sync.Once
	davHandler  http.Handler
}

type scope struct {
	Root         string // absolute filesystem root for this scope
	Rel          string // relative path inside that root (requested)
	Perm         string // read|write
	CanDelete    bool
	Kind         string // home|share|trash
	ID           int64  // share id if share
	BaseRel      string // path of this root relative to DATA_ROOT
	OriginRootID string // owner's root id for incoming item shares (home or share:N)
	QuotaUser    *db.User
	ItemShareID  int64
}

func (sc *scope) canWrite() bool  { return sc != nil && sc.Perm == "write" }
func (sc *scope) canDelete() bool { return sc != nil && sc.CanDelete }

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
	mux.HandleFunc("/api/me/sessions", s.requireAuth(s.handleMeSessions))
	mux.HandleFunc("/api/me/sessions/", s.requireAuth(s.handleMeSessionID))
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
	mux.HandleFunc("/api/upload/sessions", s.requireAuth(s.handleUploadSessions))
	mux.HandleFunc("/api/upload/sessions/", s.requireAuth(s.handleUploadSessionID))
	if s.webdavEnabled() {
		mux.Handle("/dav", s.webDAVHandler())
		mux.Handle("/dav/", s.webDAVHandler())
	}
	mux.HandleFunc("/api/download", s.requireAuth(s.handleDownload))
	mux.HandleFunc("/api/raw", s.requireAuth(s.handleRaw))
	mux.HandleFunc("/api/thumb", s.requireAuth(s.handleThumb))
	mux.HandleFunc("/api/search", s.requireAuth(s.handleSearch))
	mux.HandleFunc("/api/zip", s.requireAuth(s.handleZip))
	mux.HandleFunc("/api/trash", s.requireAuth(s.handleTrash))
	mux.HandleFunc("/api/trash/restore", s.requireAuth(s.handleTrashRestore))
	mux.HandleFunc("/api/trash/purge", s.requireAuth(s.handleTrashPurge))
	mux.HandleFunc("/api/links", s.requireAuth(s.handleLinks))
	mux.HandleFunc("/api/links/", s.requireAuth(s.handleLinkID))
	mux.HandleFunc("/api/directory", s.requireAuth(s.handleDirectory))
	mux.HandleFunc("/api/item-shares", s.requireAuth(s.handleItemShares))
	mux.HandleFunc("/api/item-shares/", s.requireAuth(s.handleItemShareID))
	mux.HandleFunc("/api/public/links/", s.handlePublicLinks)
	mux.HandleFunc("/api/admin/users", s.requireAdmin(s.handleAdminUsers))
	mux.HandleFunc("/api/admin/users/", s.requireAdmin(s.handleAdminUserID))
	mux.HandleFunc("/api/admin/shares", s.requireAdmin(s.handleAdminShares))
	mux.HandleFunc("/api/admin/shares/", s.requireAdmin(s.handleAdminShareID))
	mux.HandleFunc("/api/admin/settings", s.requireAdmin(s.handleAdminSettings))
	mux.HandleFunc("/api/admin/smtp/test", s.requireAdmin(s.handleAdminSMTPTest))
	mux.HandleFunc("/api/admin/system/status", s.requireAdmin(s.handleAdminSystemStatus))
	mux.HandleFunc("/api/admin/audit", s.requireAdmin(s.handleAdminAudit))

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

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-XSS-Protection", "0")
		w.Header().Set("Permissions-Policy", permissionsPolicy)
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		if allowSameOriginFrame(r.URL.Path) {
			w.Header().Set("X-Frame-Options", "SAMEORIGIN")
			w.Header().Set("Content-Security-Policy", frameableCSP)
		} else {
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Content-Security-Policy", documentCSP)
		}
		// HSTS is not set here: LAN HTTP and first-boot must keep working.
		// Public HTTPS deployments should set Strict-Transport-Security on the reverse proxy.
		next.ServeHTTP(w, r)
	})
}

func (s *Server) trustedProxies() *auth.TrustedProxies {
	return auth.ParseTrustedProxies(s.Cfg.TrustedProxies)
}

func (s *Server) clientIP(r *http.Request) string {
	return auth.ClientIP(r, s.trustedProxies())
}

func (s *Server) audit(r *http.Request, actorID int64, actorName, action, detail string) {
	if s == nil || s.DB == nil {
		return
	}
	ip := ""
	if r != nil {
		ip = s.clientIP(r)
	}
	_ = s.DB.AppendAudit(actorID, actorName, ip, action, detail)
}

func (s *Server) sessionToken(r *http.Request) string {
	c, err := r.Cookie(auth.CookieName)
	if err != nil {
		return ""
	}
	return c.Value
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
	s.DB.TouchSession(c.Value, s.clientIP(r), r.UserAgent())
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
		if userPending(u) {
			writeErr(w, http.StatusUnauthorized, "account pending approval")
			return
		}
		if db.NormalizeUserStatus(u.Status) != db.UserStatusActive {
			writeErr(w, http.StatusUnauthorized, "account is not active")
			return
		}
		path := r.URL.Path
		if s.userMustChangePassword(u) {
			allowed := path == "/api/me" ||
				path == "/api/me/password" ||
				path == "/api/logout" ||
				strings.HasPrefix(path, "/api/me/sessions")
			if !allowed {
				writeErr(w, http.StatusForbidden, "password change required")
				return
			}
		} else if s.userMustEnrollTotp(u) {
			allowed := path == "/api/me" ||
				path == "/api/me/totp/setup" ||
				path == "/api/me/totp/confirm" ||
				path == "/api/logout" ||
				strings.HasPrefix(path, "/api/me/sessions")
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
	maxAge := int(time.Until(expires).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.Cfg.SecureCookies,
		Expires:  expires,
		MaxAge:   maxAge,
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
	writeJSON(w, 200, map[string]any{"ok": true, "service": "nas-filehub", "version": version.Version})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	ip := s.clientIP(r)
	if s.Limiter != nil && !s.Limiter.Allow(ip) {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var body struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		RememberMe bool   `json:"rememberMe"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	username := strings.TrimSpace(body.Username)
	if locked, _, err := s.DB.LoginLockState(username); err == nil && locked {
		s.audit(r, 0, username, "login_failure", "locked")
		writeErr(w, 401, "too many failed attempts, try again later")
		return
	}
	u, err := s.DB.GetUserByUsername(body.Username)
	if err != nil || u == nil || !db.CheckPassword(u.PasswordHash, body.Password) {
		_, _, _ = s.DB.RecordLoginFailure(username, s.Cfg.EffectiveLockoutLimit(), s.Cfg.EffectiveLockoutWindow())
		s.audit(r, 0, username, "login_failure", "invalid credentials")
		writeErr(w, 401, "invalid credentials")
		return
	}
	_ = s.DB.ClearLoginFailures(u.Username)
	if u.Disabled {
		s.audit(r, u.ID, u.Username, "login_failure", "account disabled")
		writeErr(w, 401, "account disabled")
		return
	}
	if userPending(u) {
		s.audit(r, u.ID, u.Username, "login_failure", "pending approval")
		writeErr(w, 403, "Your account is pending admin approval")
		return
	}
	if db.NormalizeUserStatus(u.Status) != db.UserStatusActive {
		s.audit(r, u.ID, u.Username, "login_failure", "not active")
		writeErr(w, 403, "account is not active")
		return
	}
	rememberMe := body.RememberMe && s.sessionSettings().AllowRememberMe
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
		if err := s.DB.CreatePendingLogin(pend, u.ID, kind, exp, mustChangePW, rememberMe); err != nil {
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
		s.audit(r, u.ID, u.Username, "login_success", "pending="+kind)
		writeJSON(w, 200, out)
		return
	}
	if err := s.issueSession(w, r, u, rememberMe); err != nil {
		writeErr(w, 500, "session error")
		return
	}
	s.audit(r, u.ID, u.Username, "login_success", "")
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
		"quotaBytes": s.effectiveQuota(u), "quotaCustom": u.QuotaCustom,
		"disabled": u.Disabled, "status": db.NormalizeUserStatus(u.Status),
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

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	u := s.currentUser(r)
	m := s.userPublic(u)
	m["usedBytes"] = s.homeUsedBytes(u)
	m["webdav"] = map[string]any{"enabled": s.webdavEnabled(), "path": "/dav/"}
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
		ID        string `json:"id"`
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		Perm      string `json:"perm"`
		CanDelete bool   `json:"canDelete"`
	}
	out := []root{{ID: "home", Name: "My Files", Kind: "home", Perm: "write", CanDelete: true}}
	out = append(out, root{ID: "incoming", Name: "Shared with me", Kind: "incoming", Perm: "read", CanDelete: false})
	shares, err := s.DB.SharesForUser(u.ID, u.IsAdmin)
	if err != nil {
		writeErr(w, 500, "db error")
		return
	}
	for _, sa := range shares {
		out = append(out, root{
			ID:        "share:" + strconv.FormatInt(sa.Share.ID, 10),
			Name:      sa.Share.Name,
			Kind:      "share",
			Perm:      sa.Perm,
			CanDelete: sa.CanDelete,
		})
	}
	out = append(out, root{ID: "trash", Name: "Recycle bin", Kind: "trash", Perm: "write", CanDelete: true})
	count, err := trash.Count(s.Cfg.DataRoot, u.ID, u.IsAdmin)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"roots": out, "trashCount": count})
}

func (s *Server) resolveScope(u *db.User, rootID, rel string) (*scope, error) {
	rel = strings.TrimPrefix(rel, "/")
	if rootID == "" || rootID == "home" {
		abs, err := files.SafeJoin(s.Cfg.DataRoot, u.HomePath)
		if err != nil {
			return nil, err
		}
		return &scope{Root: abs, Rel: rel, Perm: "write", CanDelete: true, Kind: "home", BaseRel: u.HomePath}, nil
	}
	if rootID == "trash" {
		return &scope{Root: trash.Root(s.Cfg.DataRoot), Rel: rel, Perm: "write", CanDelete: true, Kind: "trash", BaseRel: files.TrashDirName}, nil
	}
	if rootID == "incoming" {
		return s.resolveIncomingScope(u, rel)
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
		canDelete := true
		if !u.IsAdmin {
			p, del, err := s.DB.GrantAccess(id, u.ID)
			if err != nil || p == "" {
				return nil, os.ErrPermission
			}
			perm = p
			canDelete = del
		}
		abs, err := files.SafeJoin(s.Cfg.DataRoot, share.Path)
		if err != nil {
			return nil, err
		}
		return &scope{Root: abs, Rel: rel, Perm: perm, CanDelete: canDelete, Kind: "share", ID: id, BaseRel: share.Path}, nil
	}
	return nil, os.ErrNotExist
}

func (s *Server) resolveIncomingScope(u *db.User, rel string) (*scope, error) {
	if strings.Trim(rel, "/") == "" {
		return &scope{Rel: "", Perm: "read", CanDelete: false, Kind: "incoming"}, nil
	}
	id, rest, ok := splitIncomingPath(rel)
	if !ok {
		return nil, os.ErrNotExist
	}
	sh, err := s.DB.GetItemShare(id)
	if err != nil || sh == nil {
		return nil, os.ErrNotExist
	}
	if !u.IsAdmin && sh.UserID != u.ID && sh.OwnerID != u.ID {
		return nil, os.ErrPermission
	}
	owner, err := s.DB.GetUserByID(sh.OwnerID)
	if err != nil || owner == nil || owner.Disabled || !userActive(owner) {
		return nil, os.ErrPermission
	}
	target := sh.Path
	if sh.IsDir && rest != "" {
		cleanRest := filepath.ToSlash(filepath.Clean(rest))
		if cleanRest == ".." || strings.HasPrefix(cleanRest, "../") {
			return nil, os.ErrPermission
		}
		target = filepath.ToSlash(filepath.Join(sh.Path, rest))
	} else if !sh.IsDir && rest != "" && rest != filepath.Base(sh.Path) {
		return nil, os.ErrNotExist
	}
	sc, err := s.resolveScope(owner, sh.RootID, target)
	if err != nil {
		return nil, err
	}
	perm := sh.Perm
	if perm != "write" || !sh.IsDir {
		perm = "read"
	}
	canDelete := perm == "write" && rest != ""
	if rest == "" {
		// The shared item itself cannot be renamed, deleted, or replaced by the recipient.
		canDelete = false
		if !sh.IsDir {
			perm = "read"
		}
	}
	sc.Perm = perm
	sc.CanDelete = canDelete
	sc.OriginRootID = incomingRootID(sh)
	sc.ItemShareID = sh.ID
	if sc.Kind == "home" {
		sc.QuotaUser = owner
	}
	return sc, nil
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
	if rootID == "incoming" && strings.Trim(rel, "/") == "" {
		entries, err := s.listIncomingEntries(u)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"path": "", "perm": "read", "canDelete": false, "entries": entries, "kind": "incoming"})
		return
	}
	if sc.Kind == "trash" {
		entries, err := s.listTrashEntries(u)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"path": "", "perm": "write", "canDelete": true, "entries": entries, "kind": "trash"})
		return
	}
	// A shared file listed as incoming/{id} is not a directory.
	if rootID == "incoming" {
		full, err := files.SafeJoin(sc.Root, sc.Rel)
		if err == nil {
			if st, err := os.Stat(full); err == nil && !st.IsDir() {
				name := filepath.Base(sc.Rel)
				entry := files.Entry{
					Name: name, Path: rel, IsDir: false, Size: st.Size(),
					ModTime: st.ModTime().UTC(), Ext: strings.ToLower(filepath.Ext(name)), Kind: files.KindOf(name, false),
				}
				writeJSON(w, 200, map[string]any{"path": sc.Rel, "perm": sc.Perm, "canDelete": sc.CanDelete, "entries": []files.Entry{entry}})
				return
			}
		}
	}
	entries, err := files.List(sc.Root, sc.Rel)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"path": sc.Rel, "perm": sc.Perm, "canDelete": sc.CanDelete, "entries": entries})
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
	if err != nil || !sc.canWrite() || sc.Kind == "trash" {
		writeErr(w, 403, "forbidden")
		return
	}
	if err := s.checkScopeQuota(u, sc, 0); err != nil {
		writeErr(w, 403, err.Error())
		return
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
	if err != nil || !sc.canWrite() || sc.Kind == "trash" {
		writeErr(w, 403, "forbidden")
		return
	}
	if _, rest, ok := splitIncomingPath(body.From); body.Root == "incoming" && (!ok || rest == "") {
		writeErr(w, 403, "forbidden")
		return
	}
	dst, err := s.resolveScope(u, body.Root, body.To)
	if err != nil || dst == nil || dst.Root != sc.Root {
		writeErr(w, 403, "forbidden")
		return
	}
	if err := files.Rename(sc.Root, sc.Rel, dst.Rel); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.afterRename(sc, sc.Rel, dst.Rel)
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
	if err != nil || !sc.canDelete() {
		writeErr(w, 403, "forbidden")
		return
	}
	if sc.Kind == "trash" {
		it, err := trash.Load(s.Cfg.DataRoot, sc.Rel)
		if err != nil {
			writeErr(w, 404, "not found")
			return
		}
		if !u.IsAdmin && it.OwnerID != u.ID && it.DeletedBy != u.ID {
			writeErr(w, 403, "forbidden")
			return
		}
		if _, err := trash.Purge(s.Cfg.DataRoot, sc.Rel); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		_ = s.DB.DeleteFileOwnerPrefix(trash.DirName + "/" + sc.Rel)
		writeJSON(w, 200, map[string]string{"ok": "1"})
		return
	}
	rootID := body.Root
	if sc.OriginRootID != "" {
		rootID = sc.OriginRootID
	}
	if rootID == "" {
		rootID = "home"
	}
	if err := s.trashDelete(u, sc, sc.Rel, rootID); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
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
	if err != nil || !sc.canWrite() || sc.Kind == "trash" {
		writeErr(w, 403, "forbidden")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "file required")
		return
	}
	defer file.Close()
	action, err := files.ParseConflictAction(r.FormValue("conflict"))
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	filename := hdr.Filename
	if rootID == "" {
		rootID = "home"
	}
	extra := hdr.Size
	if extra < 0 {
		extra = 0
	}
	saved, err := s.applyUploadedFile(u, sc, rootID, filename, file, extra, action)
	if err != nil {
		if errors.Is(err, files.ErrExists) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "file already exists",
				"code":  "exists",
				"name":  filepath.Base(filename),
			})
			return
		}
		if errors.Is(err, files.ErrIsDirectory) {
			writeErr(w, 400, err.Error())
			return
		}
		msg := err.Error()
		if strings.Contains(strings.ToLower(msg), "quota exceeded") {
			writeErr(w, 403, msg)
			return
		}
		writeErr(w, 400, msg)
		return
	}
	if saved.Skipped {
		writeJSON(w, 200, map[string]any{"path": saved.Rel, "name": saved.Name, "skipped": true})
		return
	}
	out := map[string]any{"path": saved.Rel, "name": saved.Name}
	if saved.Overwritten {
		out["overwritten"] = true
	}
	if saved.Renamed {
		out["renamed"] = true
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
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
	st, err := os.Lstat(full)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	if st.IsDir() {
		s.streamZip(w, sc)
		return
	}
	f, st2, err := files.Open(sc.Root, sc.Rel)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	defer f.Close()
	setFileDisposition(w, st2.Name(), false)
	http.ServeContent(w, r, st2.Name(), st2.ModTime(), f)
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
			if errors.Is(err, db.ErrEmailInUse) {
				writeErr(w, 409, "email already in use")
				return
			}
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
				QuotaCustom  *bool   `json:"quotaCustom"`
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
				QuotaBytes: body.QuotaBytes, QuotaCustom: body.QuotaCustom, Disabled: body.Disabled, TotpRequired: body.TotpRequired,
			})
			if err != nil {
				if errors.Is(err, db.ErrEmailInUse) {
					writeErr(w, 409, "email already in use")
					return
				}
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
	if (parts[1] == "approve" || parts[1] == "reject") && len(parts) == 2 {
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		u, err := s.DB.GetUserByID(id)
		if err != nil || u == nil {
			writeErr(w, 404, "not found")
			return
		}
		if parts[1] == "approve" {
			if err := s.DB.SetUserStatus(id, db.UserStatusActive); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			u.Status = db.UserStatusActive
			m := s.userPublic(u)
			m["usedBytes"] = s.homeUsedBytes(u)
			writeJSON(w, 200, map[string]any{"user": m})
			return
		}
		me := s.currentUser(r)
		if me.ID == id {
			writeErr(w, 400, "cannot reject yourself")
			return
		}
		if err := s.DB.DeleteUser(id); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "rejected": true})
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
				UserID    int64  `json:"userId"`
				Perm      string `json:"perm"`
				CanDelete *bool  `json:"canDelete"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeErr(w, 400, "bad json")
				return
			}
			body.Perm = strings.ToLower(strings.TrimSpace(body.Perm))
			if body.UserID <= 0 {
				writeErr(w, 400, "user required")
				return
			}
			if err := s.DB.SetShareGrant(id, body.UserID, body.Perm, body.CanDelete); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			me := s.currentUser(r)
			canDel := false
			if body.CanDelete != nil {
				canDel = *body.CanDelete
			}
			s.audit(r, me.ID, me.Username, "share_grant_set", "share="+itoaLocal(id)+" user="+itoaLocal(body.UserID)+" perm="+body.Perm+" canDelete="+boolStr(canDel))
			s.notifyShareGranted(body.UserID, id, body.Perm, canDel)
			writeJSON(w, 200, map[string]string{"ok": "1"})
		case http.MethodDelete:
			uid, _ := strconv.ParseInt(r.URL.Query().Get("userId"), 10, 64)
			if err := s.DB.RemoveShareGrant(id, uid); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			me := s.currentUser(r)
			s.audit(r, me.ID, me.Username, "share_grant_remove", "share="+itoaLocal(id)+" user="+itoaLocal(uid))
			writeJSON(w, 200, map[string]string{"ok": "1"})
		default:
			writeErr(w, 405, "method not allowed")
		}
		return
	}
	writeErr(w, 404, "not found")
}

func itoaLocal(id int64) string { return strconv.FormatInt(id, 10) }

func boolStr(v bool) string {
	if v {
		return "1"
	}
	return "0"
}
