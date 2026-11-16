package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/eric/nas-filehub/internal/auth"
	"github.com/eric/nas-filehub/internal/db"
	"github.com/eric/nas-filehub/internal/files"
	"golang.org/x/oauth2"
)

const oidcLoginTimeout = 12 * time.Second

func (s *Server) effectiveOIDC() (db.OIDCSettings, error) {
	cfg, err := s.DB.GetOIDCSettings()
	if err != nil {
		return cfg, err
	}
	if s.Cfg.OIDCIssuer != "" {
		cfg.Issuer = db.NormalizeOIDCIssuer(s.Cfg.OIDCIssuer)
	}
	if s.Cfg.OIDCClientID != "" {
		cfg.ClientID = strings.TrimSpace(s.Cfg.OIDCClientID)
	}
	if s.Cfg.OIDCClientSecret != "" {
		cfg.ClientSecret = s.Cfg.OIDCClientSecret
		cfg.ClientSecretSet = true
	}
	if s.Cfg.OIDCEnabledSet {
		cfg.Enabled = s.Cfg.OIDCEnabled
	}
	if s.Cfg.OIDCAutoCreateSet {
		cfg.AutoCreate = s.Cfg.OIDCAutoCreate
	}
	if strings.TrimSpace(cfg.Label) == "" {
		cfg.Label = db.DefaultOIDCLabel
	}
	return cfg, nil
}

func (s *Server) oidcEnvOverride() bool {
	return s.Cfg.OIDCEnabledSet || s.Cfg.OIDCIssuer != "" || s.Cfg.OIDCClientID != "" ||
		s.Cfg.OIDCClientSecret != "" || s.Cfg.OIDCAutoCreateSet
}

func (s *Server) oidcRedirectURL() string {
	base := strings.TrimRight(s.Cfg.PublicBaseURL, "/")
	if base == "" {
		return ""
	}
	return base + "/api/oidc/callback"
}

func (s *Server) oidcPublic(cfg db.OIDCSettings) map[string]any {
	return map[string]any{
		"enabled":          cfg.Enabled,
		"issuer":           cfg.Issuer,
		"clientId":         cfg.ClientID,
		"clientSecretSet":  cfg.ClientSecretSet,
		"autoCreate":       cfg.AutoCreate,
		"label":            cfg.ButtonLabel(),
		"redirectURL":      s.oidcRedirectURL(),
		"envOverride":      s.oidcEnvOverride(),
		"publicBaseURLSet": s.Cfg.PublicBaseURL != "",
	}
}

func (s *Server) discoverOIDCIssuer(issuer string) error {
	issuer = db.NormalizeOIDCIssuer(issuer)
	u, err := url.Parse(issuer)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Fragment != "" {
		return fmt.Errorf("invalid OIDC issuer URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), oidcLoginTimeout)
	defer cancel()
	if _, err := oidc.NewProvider(ctx, issuer); err != nil {
		return fmt.Errorf("OIDC discovery failed: %w", err)
	}
	return nil
}

func (s *Server) validateOIDCSettings(in db.OIDCSettings) error {
	if strings.TrimSpace(in.Issuer) != "" {
		u, err := url.Parse(in.Issuer)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Fragment != "" {
			return fmt.Errorf("invalid OIDC issuer URL")
		}
	}
	if !in.Enabled {
		return nil
	}
	if s.Cfg.PublicBaseURL == "" {
		return fmt.Errorf("PUBLIC_BASE_URL is required to enable OIDC")
	}
	if in.Issuer == "" || in.ClientID == "" {
		return fmt.Errorf("OIDC issuer and client ID are required")
	}
	if in.ClientSecret == "" {
		return fmt.Errorf("OIDC client secret is required")
	}
	return s.discoverOIDCIssuer(in.Issuer)
}

func (s *Server) handleOIDCStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	ip := s.clientIP(r)
	if s.Limiter != nil && !s.Limiter.Allow(ip) {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	cfg, err := s.effectiveOIDC()
	if err != nil {
		writeErr(w, 500, "db error")
		return
	}
	if !cfg.Ready() {
		http.NotFound(w, r)
		return
	}
	redirect := s.oidcRedirectURL()
	if redirect == "" {
		writeErr(w, 400, "PUBLIC_BASE_URL is required for OIDC")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), oidcLoginTimeout)
	defer cancel()
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		writeErr(w, 400, "OIDC discovery failed")
		return
	}
	state, err := auth.NewToken()
	if err != nil {
		writeErr(w, 500, "session error")
		return
	}
	nonce, err := auth.NewToken()
	if err != nil {
		writeErr(w, 500, "session error")
		return
	}
	verifier, challenge, err := newPKCE()
	if err != nil {
		writeErr(w, 500, "session error")
		return
	}
	rememberMe := parseBoolQuery(r.URL.Query().Get("rememberMe")) && s.sessionSettings().AllowRememberMe
	if err := s.DB.CreateOIDCState(state, nonce, verifier, rememberMe, time.Now().Add(10*time.Minute)); err != nil {
		writeErr(w, 500, "session error")
		return
	}
	oauthCfg := oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  redirect,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}
	authURL := oauthCfg.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	ip := s.clientIP(r)
	if s.Limiter != nil && !s.Limiter.Allow(ip) {
		s.oidcRedirectError(w, r, "too many login attempts")
		return
	}
	if msg := strings.TrimSpace(r.URL.Query().Get("error")); msg != "" {
		desc := strings.TrimSpace(r.URL.Query().Get("error_description"))
		if desc != "" {
			msg = desc
		}
		s.audit(r, 0, "", "login_failure", "oidc")
		s.oidcRedirectError(w, r, "sign-in was cancelled or failed")
		return
	}
	cfg, err := s.effectiveOIDC()
	if err != nil || !cfg.Ready() {
		s.oidcRedirectError(w, r, "single sign-on is not available")
		return
	}
	st := strings.TrimSpace(r.URL.Query().Get("state"))
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if st == "" || code == "" {
		s.audit(r, 0, "", "login_failure", "oidc")
		s.oidcRedirectError(w, r, "invalid or expired sign-in request")
		return
	}
	pending, err := s.DB.ConsumeOIDCState(st)
	if err != nil || pending == nil {
		s.audit(r, 0, "", "login_failure", "oidc")
		s.oidcRedirectError(w, r, "invalid or expired sign-in request")
		return
	}
	redirect := s.oidcRedirectURL()
	if redirect == "" {
		s.oidcRedirectError(w, r, "PUBLIC_BASE_URL is required for OIDC")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), oidcLoginTimeout)
	defer cancel()
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		s.audit(r, 0, "", "login_failure", "oidc")
		s.oidcRedirectError(w, r, "OIDC discovery failed")
		return
	}
	oauthCfg := oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  redirect,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}
	token, err := oauthCfg.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", pending.CodeVerifier))
	if err != nil {
		s.audit(r, 0, "", "login_failure", "oidc")
		s.oidcRedirectError(w, r, "sign-in failed")
		return
	}
	rawID, _ := token.Extra("id_token").(string)
	if rawID == "" {
		s.audit(r, 0, "", "login_failure", "oidc")
		s.oidcRedirectError(w, r, "sign-in failed")
		return
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})
	idToken, err := verifier.Verify(ctx, rawID)
	if err != nil {
		s.audit(r, 0, "", "login_failure", "oidc")
		s.oidcRedirectError(w, r, "sign-in failed")
		return
	}
	if idToken.Nonce != pending.Nonce {
		s.audit(r, 0, "", "login_failure", "oidc")
		s.oidcRedirectError(w, r, "invalid or expired sign-in request")
		return
	}
	var claims oidcClaims
	if err := idToken.Claims(&claims); err != nil {
		s.audit(r, 0, "", "login_failure", "oidc")
		s.oidcRedirectError(w, r, "sign-in failed")
		return
	}
	issuer := db.NormalizeOIDCIssuer(idToken.Issuer)
	subject := strings.TrimSpace(idToken.Subject)
	if issuer == "" || subject == "" {
		s.audit(r, 0, "", "login_failure", "oidc")
		s.oidcRedirectError(w, r, "sign-in failed")
		return
	}
	if claims.email() == "" || claims.preferredUsername() == "" {
		if info, err := provider.UserInfo(ctx, oauth2.StaticTokenSource(token)); err == nil {
			mergeClaimsFromUserInfo(&claims, info, subject)
		}
		// If UserInfo fails, continue with ID token claims; mapOIDCUser fails clearly if still unmapped.
	}
	u, created, mapErr := s.mapOIDCUser(issuer, subject, claims, cfg.AutoCreate)
	if mapErr != nil {
		s.audit(r, 0, claims.preferredUsername(), "login_failure", "oidc")
		s.oidcRedirectError(w, r, mapErr.Error())
		return
	}
	if u.Disabled {
		s.audit(r, u.ID, u.Username, "login_failure", "oidc")
		s.oidcRedirectError(w, r, "account disabled")
		return
	}
	if userPending(u) {
		s.audit(r, u.ID, u.Username, "login_failure", "oidc")
		s.oidcRedirectError(w, r, "Your account is pending admin approval")
		return
	}
	if !userActive(u) {
		s.audit(r, u.ID, u.Username, "login_failure", "oidc")
		s.oidcRedirectError(w, r, "account is not active")
		return
	}
	if created {
		if abs, err := files.SafeJoin(s.Cfg.DataRoot, u.HomePath); err == nil {
			_ = files.EnsureDir(abs)
		}
	}
	_ = s.DB.ClearLoginFailures(u.Username)
	if err := s.issueSessionMethod(w, r, u, pending.RememberMe, "oidc"); err != nil {
		s.oidcRedirectError(w, r, "session error")
		return
	}
	s.audit(r, u.ID, u.Username, "login_success", "oidc")
	http.Redirect(w, r, "/", http.StatusFound)
}

type oidcClaims struct {
	Email             string          `json:"email"`
	EmailVerified     json.RawMessage `json:"email_verified"`
	PreferredUsername string          `json:"preferred_username"`
	Name              string          `json:"name"`
}

func (c oidcClaims) email() string {
	return strings.TrimSpace(c.Email)
}

func (c oidcClaims) emailVerifiedOK() bool {
	raw := strings.TrimSpace(string(c.EmailVerified))
	if raw == "" || raw == "null" {
		return true // claim absent: allow matching an existing Filehub email
	}
	if raw == "true" || raw == `"true"` || raw == "1" {
		return true
	}
	if raw == "false" || raw == `"false"` || raw == "0" {
		return false
	}
	var b bool
	if json.Unmarshal(c.EmailVerified, &b) == nil {
		return b
	}
	return false
}

func (c oidcClaims) preferredUsername() string {
	return strings.TrimSpace(c.PreferredUsername)
}

// mergeClaimsFromUserInfo fills only email / preferred_username missing from the
// verified ID token. Claims already present in the ID token are not overridden.
// If UserInfo's subject is present and does not match the ID token subject, the
// response is ignored.
func mergeClaimsFromUserInfo(claims *oidcClaims, info *oidc.UserInfo, subject string) {
	if claims == nil || info == nil {
		return
	}
	if sub := strings.TrimSpace(info.Subject); sub != "" && sub != subject {
		return
	}
	var extra oidcClaims
	_ = info.Claims(&extra)
	if claims.email() == "" {
		if e := extra.email(); e != "" {
			claims.Email = e
		} else if e := strings.TrimSpace(info.Email); e != "" {
			claims.Email = e
		}
		if raw := strings.TrimSpace(string(extra.EmailVerified)); raw != "" && raw != "null" {
			claims.EmailVerified = extra.EmailVerified
		}
	}
	if claims.preferredUsername() == "" {
		if u := extra.preferredUsername(); u != "" {
			claims.PreferredUsername = u
		}
	}
}

func (s *Server) mapOIDCUser(issuer, subject string, claims oidcClaims, autoCreate bool) (*db.User, bool, error) {
	if u, err := s.DB.GetUserByOIDC(issuer, subject); err != nil {
		return nil, false, fmt.Errorf("sign-in failed")
	} else if u != nil {
		return u, false, nil
	}

	email := claims.email()
	if email != "" && claims.emailVerifiedOK() {
		u, err := s.DB.GetUserByEmail(email)
		if err != nil {
			return nil, false, fmt.Errorf("sign-in failed")
		}
		if u != nil {
			if u.OIDCLinked() {
				if db.NormalizeOIDCIssuer(u.OIDCIssuer) == issuer && strings.TrimSpace(u.OIDCSubject) == subject {
					return u, false, nil
				}
				return nil, false, errors.New("this email is already linked to another sign-in identity")
			}
			if err := s.DB.LinkUserOIDC(u.ID, issuer, subject); err != nil {
				return nil, false, fmt.Errorf("sign-in failed")
			}
			u.OIDCIssuer = issuer
			u.OIDCSubject = subject
			return u, false, nil
		}
	}

	if !autoCreate {
		return nil, false, errors.New("no matching Filehub account exists")
	}
	username, err := s.DB.AllocateOIDCUsername(claims.preferredUsername(), email)
	if err != nil {
		return nil, false, fmt.Errorf("could not create account")
	}
	createEmail := ""
	if email != "" && claims.emailVerifiedOK() {
		createEmail = email
	}
	u, err := s.DB.CreateOIDCUser(username, createEmail, issuer, subject)
	if err != nil {
		if errors.Is(err, db.ErrEmailInUse) {
			return nil, false, errors.New("no matching Filehub account exists")
		}
		return nil, false, fmt.Errorf("could not create account")
	}
	return u, true, nil
}

func (s *Server) oidcRedirectError(w http.ResponseWriter, r *http.Request, msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = "sign-in failed"
	}
	target := "/?oidc_error=" + url.QueryEscape(msg)
	http.Redirect(w, r, target, http.StatusFound)
}

func newPKCE() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func parseBoolQuery(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}
