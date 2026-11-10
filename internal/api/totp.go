package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/eric/nas-filehub/internal/auth"
	"github.com/eric/nas-filehub/internal/db"
	apptotp "github.com/eric/nas-filehub/internal/totp"
)

func (s *Server) force2FAAll() bool {
	if s.Cfg.Force2FAAll {
		return true
	}
	on, err := s.DB.Force2FAAll()
	if err != nil {
		return false
	}
	return on
}

func (s *Server) userMustEnrollTotp(u *db.User) bool {
	if u == nil || u.TotpEnabled {
		return false
	}
	return s.force2FAAll() || u.TotpRequired
}

func (s *Server) sessionSettings() db.SessionSettings {
	cfg, err := s.DB.GetSessionSettings()
	if err != nil {
		return db.DefaultSessionSettings()
	}
	return cfg
}

func (s *Server) sessionTTL(rememberMe bool) time.Duration {
	cfg := s.sessionSettings()
	d := cfg.SessionDuration(rememberMe)
	if d <= 0 {
		return s.Cfg.SessionTTL()
	}
	return d
}

func (s *Server) issueSession(w http.ResponseWriter, u *db.User, rememberMe bool) error {
	token, err := auth.NewToken()
	if err != nil {
		return err
	}
	exp := time.Now().Add(s.sessionTTL(rememberMe))
	if err := s.DB.CreateSession(token, u.ID, exp); err != nil {
		return err
	}
	s.setSessionCookie(w, token, exp)
	return nil
}

func (s *Server) handleLoginTotp(w http.ResponseWriter, r *http.Request) {
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
		PendingToken string `json:"pendingToken"`
		Code         string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	pend, err := s.DB.GetPendingLogin(body.PendingToken)
	if err != nil || pend == nil || pend.Kind != "totp" {
		writeErr(w, 401, "invalid or expired login challenge")
		return
	}
	u, err := s.DB.GetUserByID(pend.UserID)
	if err != nil || u == nil || u.Disabled {
		_ = s.DB.DeletePendingLogin(body.PendingToken)
		writeErr(w, 401, "invalid credentials")
		return
	}
	code := strings.TrimSpace(body.Code)
	ok := apptotp.Validate(u.TotpSecret, code)
	if !ok {
		norm := apptotp.NormalizeBackupCode(code)
		used, err := s.DB.ConsumeBackupCode(u.ID, norm)
		if err != nil || !used {
			writeErr(w, 401, "invalid authentication code")
			return
		}
	}
	mustChange := pend.MustChangePassword || s.userMustChangePassword(u)
	_ = s.DB.DeletePendingLogin(body.PendingToken)
	if err := s.issueSession(w, u, pend.RememberMe); err != nil {
		writeErr(w, 500, "session error")
		return
	}
	if u2, err := s.DB.GetUserByID(u.ID); err == nil && u2 != nil {
		u = u2
	}
	m := s.userPublic(u)
	if mustChange {
		m["mustChangePassword"] = true
		m["mustEnrollTotp"] = false
	}
	m["usedBytes"] = s.homeUsedBytes(u)
	writeJSON(w, 200, map[string]any{"user": m})
}

func (s *Server) handleLoginTotpSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		PendingToken string `json:"pendingToken"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	pend, err := s.DB.GetPendingLogin(body.PendingToken)
	if err != nil || pend == nil || pend.Kind != "enroll" {
		writeErr(w, 401, "invalid or expired enrollment challenge")
		return
	}
	u, err := s.DB.GetUserByID(pend.UserID)
	if err != nil || u == nil || u.Disabled {
		writeErr(w, 401, "invalid credentials")
		return
	}
	if u.TotpEnabled {
		writeErr(w, 400, "2FA already enabled")
		return
	}
	secret, otpURL, err := apptotp.GenerateSecret(u.Username)
	if err != nil {
		writeErr(w, 500, "totp error")
		return
	}
	if err := s.DB.SetTotpSecret(u.ID, secret); err != nil {
		writeErr(w, 500, "db error")
		return
	}
	qr, err := apptotp.QRDataURL(otpURL)
	if err != nil {
		writeErr(w, 500, "qr error")
		return
	}
	writeJSON(w, 200, map[string]any{
		"secret":     secret,
		"otpauthUrl": otpURL,
		"qrDataUrl":  qr,
	})
}

func (s *Server) handleLoginTotpEnable(w http.ResponseWriter, r *http.Request) {
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
		PendingToken string `json:"pendingToken"`
		Code         string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	pend, err := s.DB.GetPendingLogin(body.PendingToken)
	if err != nil || pend == nil || pend.Kind != "enroll" {
		writeErr(w, 401, "invalid or expired enrollment challenge")
		return
	}
	u, err := s.DB.GetUserByID(pend.UserID)
	if err != nil || u == nil || u.Disabled {
		writeErr(w, 401, "invalid credentials")
		return
	}
	if u.TotpSecret == "" || !apptotp.Validate(u.TotpSecret, body.Code) {
		writeErr(w, 400, "invalid authentication code")
		return
	}
	if err := s.DB.EnableTotp(u.ID, u.TotpSecret); err != nil {
		writeErr(w, 500, "db error")
		return
	}
	codes, _, err := s.makeBackupCodes(u.ID)
	if err != nil {
		writeErr(w, 500, "backup codes error")
		return
	}
	mustChange := pend.MustChangePassword || s.userMustChangePassword(u)
	_ = s.DB.DeletePendingLogin(body.PendingToken)
	u2, _ := s.DB.GetUserByID(u.ID)
	if u2 == nil {
		u2 = u
		u2.TotpEnabled = true
	}
	if err := s.issueSession(w, u2, pend.RememberMe); err != nil {
		writeErr(w, 500, "session error")
		return
	}
	m := s.userPublic(u2)
	if mustChange {
		m["mustChangePassword"] = true
	}
	m["usedBytes"] = s.homeUsedBytes(u2)
	writeJSON(w, 200, map[string]any{"user": m, "backupCodes": codes})
}

func (s *Server) makeBackupCodes(userID int64) (plain []string, hashes []string, err error) {
	plain, err = apptotp.GenerateBackupCodes(8)
	if err != nil {
		return nil, nil, err
	}
	hashes = make([]string, 0, len(plain))
	for _, c := range plain {
		h, err := db.HashPassword(apptotp.NormalizeBackupCode(c))
		if err != nil {
			return nil, nil, err
		}
		hashes = append(hashes, h)
	}
	if err := s.DB.ReplaceBackupCodes(userID, hashes); err != nil {
		return nil, nil, err
	}
	formatted := make([]string, len(plain))
	for i, c := range plain {
		formatted[i] = apptotp.FormatBackupCode(c)
	}
	return formatted, hashes, nil
}

func (s *Server) handleMeTotpSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	u := s.currentUser(r)
	if u.TotpEnabled {
		writeErr(w, 400, "2FA already enabled")
		return
	}
	secret, otpURL, err := apptotp.GenerateSecret(u.Username)
	if err != nil {
		writeErr(w, 500, "totp error")
		return
	}
	if err := s.DB.SetTotpSecret(u.ID, secret); err != nil {
		writeErr(w, 500, "db error")
		return
	}
	qr, err := apptotp.QRDataURL(otpURL)
	if err != nil {
		writeErr(w, 500, "qr error")
		return
	}
	writeJSON(w, 200, map[string]any{
		"secret":     secret,
		"otpauthUrl": otpURL,
		"qrDataUrl":  qr,
	})
}

func (s *Server) handleMeTotpConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	u := s.currentUser(r)
	if u.TotpEnabled {
		writeErr(w, 400, "2FA already enabled")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if u.TotpSecret == "" || !apptotp.Validate(u.TotpSecret, body.Code) {
		writeErr(w, 400, "invalid authentication code")
		return
	}
	if err := s.DB.EnableTotp(u.ID, u.TotpSecret); err != nil {
		writeErr(w, 500, "db error")
		return
	}
	codes, _, err := s.makeBackupCodes(u.ID)
	if err != nil {
		writeErr(w, 500, "backup codes error")
		return
	}
	u2, _ := s.DB.GetUserByID(u.ID)
	m := s.userPublic(u2)
	m["usedBytes"] = s.homeUsedBytes(u2)
	writeJSON(w, 200, map[string]any{"user": m, "backupCodes": codes})
}

func (s *Server) handleMeTotpDisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	u := s.currentUser(r)
	if !u.TotpEnabled {
		writeErr(w, 400, "2FA is not enabled")
		return
	}
	if s.force2FAAll() || u.TotpRequired {
		writeErr(w, 403, "2FA is required for this account; cannot disable")
		return
	}
	var body struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	ok := false
	if body.Password != "" && db.CheckPassword(u.PasswordHash, body.Password) {
		ok = true
	}
	if !ok && body.Code != "" {
		if apptotp.Validate(u.TotpSecret, body.Code) {
			ok = true
		} else {
			used, err := s.DB.ConsumeBackupCode(u.ID, apptotp.NormalizeBackupCode(body.Code))
			if err == nil && used {
				ok = true
			}
		}
	}
	if !ok {
		writeErr(w, 401, "password or authentication code required")
		return
	}
	if err := s.DB.DisableTotp(u.ID); err != nil {
		writeErr(w, 500, "db error")
		return
	}
	u2, _ := s.DB.GetUserByID(u.ID)
	m := s.userPublic(u2)
	m["usedBytes"] = s.homeUsedBytes(u2)
	writeJSON(w, 200, map[string]any{"user": m})
}

func (s *Server) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	settingsPayload := func() map[string]any {
		pol := s.passwordPolicy()
		smtpCfg, _ := s.effectiveSMTP()
		sess := s.sessionSettings()
		gq, _ := s.DB.GetGlobalQuotaSettings()
		return map[string]any{
			"force2faAll":          s.force2FAAll(),
			"force2faAllEnv":       s.Cfg.Force2FAAll,
			"openRegistration":     s.openRegistration(),
			"sessionTtlSeconds":    sess.SessionTTLSeconds,
			"rememberMeTtlSeconds": sess.RememberMeTTLSeconds,
			"allowRememberMe":      sess.AllowRememberMe,
			"globalQuotaEnabled":   gq.Enabled,
			"globalQuotaBytes":     gq.Bytes,
			"passwordPolicy":       pol,
			"passwordPolicyHint":   db.PasswordPolicySummary(pol),
			"smtp":                 s.smtpPublic(smtpCfg),
			"publicBaseURL":        s.Cfg.PublicBaseURL,
		}
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, settingsPayload())
	case http.MethodPut, http.MethodPatch, http.MethodPost:
		var body struct {
			Force2FAAll          *bool   `json:"force2faAll"`
			OpenRegistration     *bool   `json:"openRegistration"`
			SessionTtlSeconds    *int    `json:"sessionTtlSeconds"`
			RememberMeTtlSeconds *int    `json:"rememberMeTtlSeconds"`
			AllowRememberMe      *bool   `json:"allowRememberMe"`
			PasswordMinLength    *int    `json:"passwordMinLength"`
			RequireLetters       *bool   `json:"passwordRequireLetters"`
			RequireDigits        *bool   `json:"passwordRequireDigits"`
			RequireSymbols       *bool   `json:"passwordRequireSymbols"`
			SMTPEnabled          *bool   `json:"smtpEnabled"`
			SMTPHost             *string `json:"smtpHost"`
			SMTPPort             *int    `json:"smtpPort"`
			SMTPUsername         *string `json:"smtpUsername"`
			SMTPPassword         *string `json:"smtpPassword"`
			SMTPFrom             *string `json:"smtpFrom"`
			GlobalQuotaEnabled   *bool   `json:"globalQuotaEnabled"`
			GlobalQuotaBytes     *int64  `json:"globalQuotaBytes"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		quotaTouch := body.GlobalQuotaEnabled != nil || body.GlobalQuotaBytes != nil
		if quotaTouch && body.GlobalQuotaBytes != nil && *body.GlobalQuotaBytes < 0 {
			writeErr(w, 400, "invalid quota: quotaBytes must be >= 0")
			return
		}
		touched := false
		if body.Force2FAAll != nil {
			if err := s.DB.SetForce2FAAll(*body.Force2FAAll); err != nil {
				writeErr(w, 500, "db error")
				return
			}
			touched = true
		}
		if body.OpenRegistration != nil {
			if err := s.DB.SetOpenRegistration(*body.OpenRegistration); err != nil {
				writeErr(w, 500, "db error")
				return
			}
			touched = true
		}
		sessTouch := body.SessionTtlSeconds != nil || body.RememberMeTtlSeconds != nil || body.AllowRememberMe != nil
		if sessTouch {
			cur := s.sessionSettings()
			if body.SessionTtlSeconds != nil {
				cur.SessionTTLSeconds = *body.SessionTtlSeconds
			}
			if body.RememberMeTtlSeconds != nil {
				cur.RememberMeTTLSeconds = *body.RememberMeTtlSeconds
			}
			if body.AllowRememberMe != nil {
				cur.AllowRememberMe = *body.AllowRememberMe
			}
			if err := s.DB.SetSessionSettings(cur); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			touched = true
		}
		polTouch := body.PasswordMinLength != nil || body.RequireLetters != nil ||
			body.RequireDigits != nil || body.RequireSymbols != nil
		if polTouch {
			cur := s.passwordPolicy()
			if body.PasswordMinLength != nil {
				cur.MinLength = *body.PasswordMinLength
			}
			if body.RequireLetters != nil {
				cur.RequireLetters = *body.RequireLetters
			}
			if body.RequireDigits != nil {
				cur.RequireDigits = *body.RequireDigits
			}
			if body.RequireSymbols != nil {
				cur.RequireSymbols = *body.RequireSymbols
			}
			if _, err := s.DB.SetPasswordPolicy(cur); err != nil {
				writeErr(w, 500, "db error")
				return
			}
			touched = true
		}
		smtpTouch := body.SMTPEnabled != nil || body.SMTPHost != nil || body.SMTPPort != nil ||
			body.SMTPUsername != nil || body.SMTPPassword != nil || body.SMTPFrom != nil
		if smtpTouch {
			cur, err := s.DB.GetSMTPSettings()
			if err != nil {
				writeErr(w, 500, "db error")
				return
			}
			if body.SMTPEnabled != nil {
				cur.Enabled = *body.SMTPEnabled
			}
			if body.SMTPHost != nil {
				cur.Host = *body.SMTPHost
			}
			if body.SMTPPort != nil {
				cur.Port = *body.SMTPPort
			}
			if body.SMTPUsername != nil {
				cur.Username = *body.SMTPUsername
			}
			if body.SMTPFrom != nil {
				cur.From = *body.SMTPFrom
			}
			var pw *string
			if body.SMTPPassword != nil {
				pw = body.SMTPPassword
			}
			if _, err := s.DB.SetSMTPSettings(cur, pw); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			touched = true
		}
		if quotaTouch {
			if _, err := s.DB.SetGlobalQuotaSettings(body.GlobalQuotaEnabled, body.GlobalQuotaBytes); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			touched = true
		}
		if !touched {
			writeErr(w, 400, "no settings fields provided")
			return
		}
		writeJSON(w, 200, settingsPayload())
	default:
		writeErr(w, 405, "method not allowed")
	}
}
