package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/eric/nas-filehub/internal/auth"
	"github.com/eric/nas-filehub/internal/db"
	"github.com/eric/nas-filehub/internal/mail"
)

func (s *Server) effectiveSMTP() (db.SMTPSettings, error) {
	cfg, err := s.DB.GetSMTPSettings()
	if err != nil {
		return cfg, err
	}
	// Env overrides take precedence when set.
	if s.Cfg.SMTPHost != "" {
		cfg.Host = s.Cfg.SMTPHost
	}
	if s.Cfg.SMTPPort > 0 {
		cfg.Port = s.Cfg.SMTPPort
	}
	if s.Cfg.SMTPUsername != "" {
		cfg.Username = s.Cfg.SMTPUsername
	}
	if s.Cfg.SMTPPassword != "" {
		cfg.Password = s.Cfg.SMTPPassword
	}
	if s.Cfg.SMTPFrom != "" {
		cfg.From = s.Cfg.SMTPFrom
	}
	if s.Cfg.SMTPEnabled {
		cfg.Enabled = true
	}
	cfg.PasswordSet = cfg.Password != ""
	return cfg, nil
}

func (s *Server) smtpPublic(cfg db.SMTPSettings) map[string]any {
	envOverride := s.Cfg.SMTPHost != "" || s.Cfg.SMTPEnabled
	return map[string]any{
		"enabled":      cfg.Enabled,
		"host":         cfg.Host,
		"port":         cfg.Port,
		"username":     cfg.Username,
		"from":         cfg.From,
		"passwordSet":  cfg.PasswordSet,
		"ready":        cfg.Ready(),
		"envOverride":  envOverride,
	}
}

func (s *Server) handleAuthFeatures(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	cfg, err := s.effectiveSMTP()
	if err != nil {
		writeErr(w, 500, "db error")
		return
	}
	writeJSON(w, 200, map[string]any{
		"forgotPassword": cfg.Ready(),
	})
}

func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	ip := auth.ClientIP(r)
	if !s.ResetLimiter.Allow(ip) {
		writeErr(w, http.StatusTooManyRequests, "too many reset requests")
		return
	}
	var body struct {
		UsernameOrEmail string `json:"usernameOrEmail"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	id := strings.TrimSpace(body.UsernameOrEmail)
	if id == "" {
		writeErr(w, 400, "username or email required")
		return
	}

	// Always return the same message to avoid account enumeration.
	const generic = "If an account with that username or email exists and mail is configured, a reset link has been sent."

	smtpCfg, err := s.effectiveSMTP()
	if err != nil || !smtpCfg.Ready() {
		writeJSON(w, 200, map[string]any{"ok": true, "message": generic})
		return
	}

	u, err := s.DB.FindUserByUsernameOrEmail(id)
	if err != nil || u == nil || u.Disabled || strings.TrimSpace(u.Email) == "" {
		writeJSON(w, 200, map[string]any{"ok": true, "message": generic})
		return
	}

	token, err := auth.NewToken()
	if err != nil {
		writeErr(w, 500, "token error")
		return
	}
	exp := time.Now().Add(time.Hour)
	if err := s.DB.CreatePasswordReset(token, u.ID, exp); err != nil {
		writeErr(w, 500, "db error")
		return
	}

	base := s.publicBaseURL(r)
	link := fmt.Sprintf("%s/reset?token=%s", base, token)
	subject := "Filehub password reset"
	bodyText := fmt.Sprintf(
		"Hello %s,\n\nSomeone requested a password reset for your Filehub account.\n"+
			"Open this link within 1 hour to set a new password:\n\n%s\n\n"+
			"If you did not request this, you can ignore this email.\n",
		u.Username, link,
	)
	if err := mail.Send(smtpCfg, u.Email, subject, bodyText); err != nil {
		_ = s.DB.DeletePasswordReset(token)
		// Still generic to the client; log server-side
		writeJSON(w, 200, map[string]any{"ok": true, "message": generic})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": generic})
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	ip := auth.ClientIP(r)
	if !s.ResetLimiter.Allow(ip) {
		writeErr(w, http.StatusTooManyRequests, "too many reset requests")
		return
	}
	var body struct {
		Token       string `json:"token"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	token := strings.TrimSpace(body.Token)
	if token == "" {
		writeErr(w, 400, "token required")
		return
	}
	if err := db.ValidatePassword(body.NewPassword, s.passwordPolicy()); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	u, err := s.DB.GetPasswordResetUser(token)
	if err != nil {
		writeErr(w, 500, "db error")
		return
	}
	if u == nil || u.Disabled {
		writeErr(w, 400, "invalid or expired reset link")
		return
	}
	if err := s.DB.SetUserPassword(u.ID, body.NewPassword); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	_ = s.DB.DeletePasswordReset(token)
	_ = s.DB.DeletePasswordResetsForUser(u.ID)
	writeJSON(w, 200, map[string]any{
		"ok":      true,
		"message": "Password updated. You can log in with your new password.",
	})
}

func (s *Server) handleResetPasswordMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		writeErr(w, 400, "token required")
		return
	}
	u, err := s.DB.GetPasswordResetUser(token)
	if err != nil {
		writeErr(w, 500, "db error")
		return
	}
	if u == nil || u.Disabled {
		writeErr(w, 400, "invalid or expired reset link")
		return
	}
	pol := s.passwordPolicy()
	writeJSON(w, 200, map[string]any{
		"ok":                 true,
		"username":           u.Username,
		"passwordPolicy":     pol,
		"passwordPolicyHint": db.PasswordPolicySummary(pol),
	})
}

func (s *Server) publicBaseURL(r *http.Request) string {
	if s.Cfg.PublicBaseURL != "" {
		return strings.TrimRight(s.Cfg.PublicBaseURL, "/")
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return proto + "://" + host
}

func (s *Server) handleAdminSMTPTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		To string `json:"to"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	to := strings.TrimSpace(body.To)
	me := s.currentUser(r)
	if to == "" && me != nil {
		to = me.Email
	}
	if to == "" {
		writeErr(w, 400, "recipient email required")
		return
	}
	cfg, err := s.effectiveSMTP()
	if err != nil {
		writeErr(w, 500, "db error")
		return
	}
	if !cfg.Ready() {
		writeErr(w, 400, "SMTP is not configured or not enabled")
		return
	}
	err = mail.Send(cfg, to, "Filehub SMTP test", "This is a test message from Filehub. SMTP is working.\n")
	if err != nil {
		writeErr(w, 400, "send failed: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "Test email sent"})
}
