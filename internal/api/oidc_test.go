package api

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eric/nas-filehub/internal/db"
	apptotp "github.com/eric/nas-filehub/internal/totp"
)

type mockOIDCUser struct {
	Subject             string
	Email               string
	EmailVerified       bool
	PreferredUsername   string
	OmitIDTokenEmail    bool
	OmitIDTokenUsername bool
}

type mockAuthCode struct {
	user      mockOIDCUser
	nonce     string
	challenge string
}

type mockOIDC struct {
	t            *testing.T
	server       *httptest.Server
	key          *rsa.PrivateKey
	kid          string
	clientID     string
	clientSecret string
	defaultUser  mockOIDCUser
	mu           sync.Mutex
	codes        map[string]mockAuthCode
	accessTokens map[string]mockOIDCUser
}

func newMockOIDC(t *testing.T, clientID, clientSecret string) *mockOIDC {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	m := &mockOIDC{
		t:            t,
		key:          key,
		kid:          "test-key",
		clientID:     clientID,
		clientSecret: clientSecret,
		codes:        map[string]mockAuthCode{},
		accessTokens: map[string]mockOIDCUser{},
		defaultUser: mockOIDCUser{
			Subject:           "sub-alice",
			Email:             "alice@example.com",
			EmailVerified:     true,
			PreferredUsername: "alice",
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		base := m.issuer()
		writeJSON(w, 200, map[string]any{
			"issuer":                                base,
			"authorization_endpoint":                base + "/auth",
			"token_endpoint":                        base + "/token",
			"userinfo_endpoint":                     base + "/userinfo",
			"jwks_uri":                              base + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"code_challenge_methods_supported":      []string{"S256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(m.key.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(m.key.E)).Bytes())
		writeJSON(w, 200, map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA", "use": "sig", "alg": "RS256", "kid": m.kid,
				"n": n, "e": e,
			}},
		})
	})
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("client_id") != m.clientID {
			http.Error(w, "bad client", 400)
			return
		}
		if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			http.Error(w, "pkce required", 400)
			return
		}
		code := fmt.Sprintf("code-%d", time.Now().UnixNano())
		m.mu.Lock()
		user := m.defaultUser
		m.codes[code] = mockAuthCode{user: user, nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}
		m.mu.Unlock()
		u, err := url.Parse(q.Get("redirect_uri"))
		if err != nil || q.Get("redirect_uri") == "" {
			http.Error(w, "bad redirect", 400)
			return
		}
		qq := u.Query()
		qq.Set("code", code)
		qq.Set("state", q.Get("state"))
		u.RawQuery = qq.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		cid, csec, ok := r.BasicAuth()
		if !ok {
			cid = r.FormValue("client_id")
			csec = r.FormValue("client_secret")
		}
		if cid != m.clientID || csec != m.clientSecret {
			http.Error(w, "invalid client", 401)
			return
		}
		code := r.FormValue("code")
		verifier := r.FormValue("code_verifier")
		m.mu.Lock()
		ac, ok := m.codes[code]
		if ok {
			delete(m.codes, code)
		}
		m.mu.Unlock()
		if !ok {
			http.Error(w, "invalid code", 400)
			return
		}
		sum := sha256.Sum256([]byte(verifier))
		got := base64.RawURLEncoding.EncodeToString(sum[:])
		if got != ac.challenge {
			http.Error(w, "invalid verifier", 400)
			return
		}
		idToken, err := m.signIDToken(ac)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		access := "access-" + code
		m.mu.Lock()
		m.accessTokens[access] = ac.user
		m.mu.Unlock()
		writeJSON(w, 200, map[string]any{
			"access_token": access,
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     idToken,
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		authz := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(authz, prefix) {
			http.Error(w, "unauthorized", 401)
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(authz, prefix))
		m.mu.Lock()
		user, ok := m.accessTokens[token]
		m.mu.Unlock()
		if !ok {
			http.Error(w, "unauthorized", 401)
			return
		}
		writeJSON(w, 200, map[string]any{
			"sub":                user.Subject,
			"email":              user.Email,
			"email_verified":     user.EmailVerified,
			"preferred_username": user.PreferredUsername,
		})
	})
	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

func (m *mockOIDC) issuer() string {
	if m.server == nil {
		return ""
	}
	return m.server.URL
}

func (m *mockOIDC) setUser(u mockOIDCUser) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaultUser = u
}

func (m *mockOIDC) signIDToken(ac mockAuthCode) (string, error) {
	now := time.Now().Unix()
	claims := map[string]any{
		"iss":   m.issuer(),
		"sub":   ac.user.Subject,
		"aud":   m.clientID,
		"exp":   now + 300,
		"iat":   now,
		"nonce": ac.nonce,
	}
	if !ac.user.OmitIDTokenEmail && ac.user.Email != "" {
		claims["email"] = ac.user.Email
		claims["email_verified"] = ac.user.EmailVerified
	}
	if !ac.user.OmitIDTokenUsername && ac.user.PreferredUsername != "" {
		claims["preferred_username"] = ac.user.PreferredUsername
	}
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": m.kid}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	head := base64.RawURLEncoding.EncodeToString(hb)
	body := base64.RawURLEncoding.EncodeToString(cb)
	sigInput := head + "." + body
	sum := sha256.Sum256([]byte(sigInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, m.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return sigInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func noFollowClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func oidcStateCookieFrom(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == oidcStateCookieName && c.Value != "" {
			return c
		}
	}
	t.Fatalf("missing %s cookie in %v", oidcStateCookieName, resp.Cookies())
	return nil
}

func assertOIDCStateCookie(t *testing.T, c *http.Cookie, wantState string) {
	t.Helper()
	if c.Value != wantState {
		t.Fatalf("state cookie %q want %q", c.Value, wantState)
	}
	if !c.HttpOnly {
		t.Fatalf("state cookie should be HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("state cookie SameSite=%v want Lax", c.SameSite)
	}
	if c.Path != "/" && c.Path != "/api/oidc" && c.Path != "/api/oidc/callback" && c.Path != "" {
		t.Fatalf("state cookie Path=%q does not cover callback", c.Path)
	}
	if c.MaxAge != oidcStateCookieMaxAge && c.MaxAge != 0 {
		// httptest may surface MaxAge; require ~10 minutes when set
		if c.MaxAge < 9*60 || c.MaxAge > 10*60 {
			t.Fatalf("state cookie MaxAge=%d want ~10 minutes", c.MaxAge)
		}
	}
	if c.Secure {
		t.Fatalf("state cookie Secure=true unexpectedly (SECURE_COOKIES off in tests)")
	}
}

func getNoFollow(t *testing.T, rawURL string, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cookies {
		if c != nil {
			req.AddCookie(c)
		}
	}
	resp, err := noFollowClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func enableTestOIDC(t *testing.T, s *Server, ts *httptest.Server, m *mockOIDC, autoCreate bool) {
	t.Helper()
	s.Cfg.PublicBaseURL = ts.URL
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	st, data := doJSON(t, http.MethodPut, ts.URL+"/api/admin/settings", admin, map[string]any{
		"oidcEnabled":      true,
		"oidcIssuer":       m.issuer(),
		"oidcClientId":     m.clientID,
		"oidcClientSecret": m.clientSecret,
		"oidcAutoCreate":   autoCreate,
		"oidcLabel":        "Sign in with Test IdP",
	})
	if st != 200 {
		t.Fatalf("enable oidc %d %v", st, data)
	}
}

func oidcLogin(t *testing.T, tsURL string) []*http.Cookie {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 8 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	resp, err := client.Get(tsURL + "/api/oidc/start")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("oidc login ended %d %s at %s", resp.StatusCode, b, resp.Request.URL)
	}
	// Final page should be Filehub "/" with a session cookie.
	u, _ := url.Parse(tsURL + "/")
	cookies := jar.Cookies(u)
	if len(cookies) == 0 {
		// Some clients attach cookies only to the last request host; also check the response.
		cookies = resp.Cookies()
	}
	var out []*http.Cookie
	for _, c := range cookies {
		if c.Name == "filehub_session" && c.Value != "" {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		t.Fatalf("oidc login produced no session cookie; final url=%s status=%d", resp.Request.URL, resp.StatusCode)
	}
	return out
}

func TestOIDCDisabledStartUnavailable(t *testing.T) {
	s, ts, _ := testServer(t)
	s.Cfg.PublicBaseURL = ts.URL
	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/auth/features", nil, nil)
	if st != 200 {
		t.Fatalf("features %d %v", st, data)
	}
	if data["oidcEnabled"] != false {
		t.Fatalf("oidcEnabled=%v want false", data["oidcEnabled"])
	}
	resp, err := noFollowClient().Get(ts.URL + "/api/oidc/start")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("start when disabled %d, want 404", resp.StatusCode)
	}
}

func TestOIDCStartRedirectsWithStateAndPKCE(t *testing.T) {
	s, ts, _ := testServer(t)
	m := newMockOIDC(t, "filehub", "s3cret")
	enableTestOIDC(t, s, ts, m, false)

	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/auth/features", nil, nil)
	if st != 200 || data["oidcEnabled"] != true {
		t.Fatalf("features after enable %d %v", st, data)
	}
	if data["oidcLabel"] != "Sign in with Test IdP" {
		t.Fatalf("oidcLabel %v", data["oidcLabel"])
	}

	resp, err := noFollowClient().Get(ts.URL + "/api/oidc/start")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 302 {
		t.Fatalf("start %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(loc, m.issuer()+"/auth") {
		t.Fatalf("location %s", loc)
	}
	q := u.Query()
	if q.Get("client_id") != "filehub" {
		t.Fatalf("client_id %s", q.Get("client_id"))
	}
	if q.Get("response_type") != "code" {
		t.Fatalf("response_type %s", q.Get("response_type"))
	}
	if q.Get("state") == "" || q.Get("nonce") == "" {
		t.Fatalf("missing state/nonce: %s", loc)
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		t.Fatalf("missing pkce: %s", loc)
	}
	if !strings.Contains(q.Get("scope"), "openid") {
		t.Fatalf("scope %s", q.Get("scope"))
	}
	wantRedir := ts.URL + "/api/oidc/callback"
	if q.Get("redirect_uri") != wantRedir {
		t.Fatalf("redirect_uri %s want %s", q.Get("redirect_uri"), wantRedir)
	}
	ck := oidcStateCookieFrom(t, resp)
	assertOIDCStateCookie(t, ck, q.Get("state"))
}

func TestOIDCCallbackLinksExistingEmailAndKeepsLocalLogin(t *testing.T) {
	s, ts, database := testServer(t)
	if _, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	m := newMockOIDC(t, "filehub", "s3cret")
	m.setUser(mockOIDCUser{Subject: "idp-alice", Email: "alice@example.com", EmailVerified: true, PreferredUsername: "alice"})
	enableTestOIDC(t, s, ts, m, false)

	cookies := oidcLogin(t, ts.URL)
	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/me", cookies, nil)
	if st != 200 {
		t.Fatalf("me %d %v", st, data)
	}
	user, _ := data["user"].(map[string]any)
	if user["username"] != "alice" {
		t.Fatalf("linked user %v", user)
	}
	if user["oidcLinked"] != true || user["oidcSession"] != true {
		t.Fatalf("oidc flags %v", user)
	}
	if user["hasLocalPassword"] != true {
		t.Fatalf("hasLocalPassword %v", user)
	}
	u, err := database.GetUserByOIDC(m.issuer(), "idp-alice")
	if err != nil || u == nil || u.Username != "alice" {
		t.Fatalf("stored link: %+v %v", u, err)
	}

	// Local password login still works.
	local := loginAs(t, ts.URL, "alice", "alice-pass-123")
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/me", local, nil)
	if st != 200 {
		t.Fatalf("local me %d %v", st, data)
	}
	user, _ = data["user"].(map[string]any)
	if user["username"] != "alice" {
		t.Fatalf("local user %v", user)
	}
	if user["oidcSession"] != false {
		t.Fatalf("local login should not be an OIDC session: %v", user)
	}
}

func TestOIDCAutoCreateOnUnknownUser(t *testing.T) {
	s, ts, database := testServer(t)
	m := newMockOIDC(t, "filehub", "s3cret")
	m.setUser(mockOIDCUser{Subject: "idp-new", Email: "newbie@example.com", EmailVerified: true, PreferredUsername: "newbie"})
	enableTestOIDC(t, s, ts, m, true)

	cookies := oidcLogin(t, ts.URL)
	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/me", cookies, nil)
	if st != 200 {
		t.Fatalf("me %d %v", st, data)
	}
	user, _ := data["user"].(map[string]any)
	if user["username"] != "newbie" || user["email"] != "newbie@example.com" {
		t.Fatalf("created user %v", user)
	}
	if user["hasLocalPassword"] != false {
		t.Fatalf("auto-created user should have no local password: %v", user)
	}
	u, err := database.GetUserByOIDC(m.issuer(), "idp-new")
	if err != nil || u == nil || u.HasLocalPassword() || u.Status != db.UserStatusActive {
		t.Fatalf("db user %+v %v", u, err)
	}
}

func TestOIDCAutoCreateOffRejectsUnknownUser(t *testing.T) {
	s, ts, _ := testServer(t)
	m := newMockOIDC(t, "filehub", "s3cret")
	m.setUser(mockOIDCUser{Subject: "idp-unknown", Email: "unknown@example.com", EmailVerified: true, PreferredUsername: "unknown"})
	enableTestOIDC(t, s, ts, m, false)

	resp := oidcFollowToCallback(t, ts.URL)
	defer resp.Body.Close()
	if resp.StatusCode != 302 {
		t.Fatalf("callback %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "oidc_error=") || !strings.Contains(loc, "no+matching+Filehub+account") && !strings.Contains(loc, "no matching Filehub account") && !strings.Contains(urlUnescape(t, loc), "no matching Filehub account exists") {
		t.Fatalf("expected no-matching-account error, got %s", loc)
	}
}

func urlUnescape(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("oidc_error")
}

func TestOIDCDisabledUserRejected(t *testing.T) {
	s, ts, database := testServer(t)
	u, err := database.CreateUser("disabledone", "Disabled-pass-1", "homes/disabledone", false, 0, "disabled@example.com")
	if err != nil {
		t.Fatal(err)
	}
	dis := true
	if _, err := database.UpdateUser(u.ID, db.UserUpdate{Disabled: &dis}); err != nil {
		t.Fatal(err)
	}
	m := newMockOIDC(t, "filehub", "s3cret")
	m.setUser(mockOIDCUser{Subject: "idp-dis", Email: "disabled@example.com", EmailVerified: true, PreferredUsername: "disabledone"})
	enableTestOIDC(t, s, ts, m, false)

	resp := oidcFollowToCallback(t, ts.URL)
	defer resp.Body.Close()
	loc := resp.Header.Get("Location")
	if urlUnescape(t, loc) != "account disabled" {
		t.Fatalf("want account disabled, got %s", loc)
	}
}

func TestOIDCInvalidStateRejected(t *testing.T) {
	s, ts, _ := testServer(t)
	m := newMockOIDC(t, "filehub", "s3cret")
	enableTestOIDC(t, s, ts, m, true)

	resp, err := noFollowClient().Get(ts.URL + "/api/oidc/callback?code=nope")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 302 {
		t.Fatalf("missing state %d", resp.StatusCode)
	}
	if urlUnescape(t, resp.Header.Get("Location")) != "invalid or expired sign-in request" {
		t.Fatalf("missing state loc %s", resp.Header.Get("Location"))
	}

	resp, err = noFollowClient().Get(ts.URL + "/api/oidc/callback?code=nope&state=bogus")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if urlUnescape(t, resp.Header.Get("Location")) != "invalid or expired sign-in request" {
		t.Fatalf("bogus state loc %s", resp.Header.Get("Location"))
	}
}

func TestOIDCSessionSkipsTOTPEnrollment(t *testing.T) {
	s, ts, database := testServer(t)
	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	req := true
	if _, err := database.UpdateUser(alice.ID, db.UserUpdate{TotpRequired: &req}); err != nil {
		t.Fatal(err)
	}
	secret, _, err := apptotp.GenerateSecret("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetTotpSecret(alice.ID, secret); err != nil {
		t.Fatal(err)
	}
	if err := database.EnableTotp(alice.ID, secret); err != nil {
		t.Fatal(err)
	}

	m := newMockOIDC(t, "filehub", "s3cret")
	m.setUser(mockOIDCUser{Subject: "idp-alice", Email: "alice@example.com", EmailVerified: true, PreferredUsername: "alice"})
	enableTestOIDC(t, s, ts, m, false)

	cookies := oidcLogin(t, ts.URL)
	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/me", cookies, nil)
	if st != 200 {
		t.Fatalf("me %d %v", st, data)
	}
	user, _ := data["user"].(map[string]any)
	if user["mustEnrollTotp"] != false {
		t.Fatalf("OIDC session should skip TOTP: %v", user)
	}
	if user["oidcSession"] != true {
		t.Fatalf("expected oidc session: %v", user)
	}
	// File listing should work without a TOTP challenge.
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/roots", cookies, nil)
	if st != 200 {
		t.Fatalf("roots via OIDC session %d %v", st, data)
	}
}

func TestOIDCOnlyUserWebDAVRequiresPassword(t *testing.T) {
	s, ts, _ := testServer(t)
	m := newMockOIDC(t, "filehub", "s3cret")
	m.setUser(mockOIDCUser{Subject: "idp-dav", Email: "davuser@example.com", EmailVerified: true, PreferredUsername: "davuser"})
	enableTestOIDC(t, s, ts, m, true)

	cookies := oidcLogin(t, ts.URL)
	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/me", cookies, nil)
	if st != 200 {
		t.Fatalf("me %d %v", st, data)
	}
	user, _ := data["user"].(map[string]any)
	if user["hasLocalPassword"] != false {
		t.Fatalf("expected no local password: %v", user)
	}

	code, body, _ := davDo(t, "PROPFIND", ts.URL+"/dav/", "davuser", "anything", "", "", "0")
	if code != 401 {
		t.Fatalf("webdav without password %d %s", code, body)
	}

	// Setting a password without current password should work.
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/me/password", cookies, map[string]any{
		"newPassword": "Dav-pass-1234",
	})
	if st != 200 {
		t.Fatalf("set password %d %v", st, data)
	}
	user, _ = data["user"].(map[string]any)
	if user["hasLocalPassword"] != true {
		t.Fatalf("password should now be set: %v", user)
	}

	code, body, _ = davDo(t, "PROPFIND", ts.URL+"/dav/", "davuser", "Dav-pass-1234", "", "", "1")
	if code != 207 && code != 200 {
		t.Fatalf("webdav after password %d %s", code, body)
	}
}

func TestOIDCSettingsSecretWriteOnly(t *testing.T) {
	s, ts, _ := testServer(t)
	m := newMockOIDC(t, "filehub", "s3cret")
	enableTestOIDC(t, s, ts, m, false)
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/admin/settings", admin, nil)
	if st != 200 {
		t.Fatalf("settings %d %v", st, data)
	}
	raw, _ := json.Marshal(data)
	if strings.Contains(string(raw), "s3cret") {
		t.Fatalf("client secret leaked in settings: %s", raw)
	}
	oidc, _ := data["oidc"].(map[string]any)
	if oidc["clientSecretSet"] != true {
		t.Fatalf("clientSecretSet %v", oidc)
	}
	if _, ok := oidc["clientSecret"]; ok {
		t.Fatalf("clientSecret field should be omitted: %v", oidc)
	}
	if data["oidcRedirectURL"] != ts.URL+"/api/oidc/callback" {
		t.Fatalf("redirect %v", data["oidcRedirectURL"])
	}
}

func oidcFollowToCallback(t *testing.T, tsURL string) *http.Response {
	t.Helper()
	start, loc, stateCookie := oidcStartAndCallbackURL(t, tsURL)
	_ = start
	resp := getNoFollow(t, loc, stateCookie)
	return resp
}

// oidcStartAndCallbackURL starts SSO and follows the IdP once, returning the
// Filehub start response, the callback URL, and the state cookie to replay.
func oidcStartAndCallbackURL(t *testing.T, tsURL string) (start *http.Response, callback string, stateCookie *http.Cookie) {
	t.Helper()
	start = getNoFollow(t, tsURL+"/api/oidc/start")
	idpLoc := start.Header.Get("Location")
	stateCookie = oidcStateCookieFrom(t, start)
	start.Body.Close()
	if idpLoc == "" {
		t.Fatalf("start missing Location (status %d)", start.StatusCode)
	}
	resp, err := noFollowClient().Get(idpLoc)
	if err != nil {
		t.Fatal(err)
	}
	callback = resp.Header.Get("Location")
	resp.Body.Close()
	if callback == "" {
		t.Fatalf("idp missing callback Location (status %d)", resp.StatusCode)
	}
	return start, callback, stateCookie
}

func sessionCookieIssued(resp *http.Response) bool {
	for _, c := range resp.Cookies() {
		if c.Name == "filehub_session" && c.Value != "" && c.MaxAge != -1 {
			return true
		}
	}
	return false
}

func TestOIDCCallbackRequiresStateCookie(t *testing.T) {
	s, ts, _ := testServer(t)
	m := newMockOIDC(t, "filehub", "s3cret")
	m.setUser(mockOIDCUser{Subject: "idp-ck", Email: "cookieuser@example.com", EmailVerified: true, PreferredUsername: "cookieuser"})
	enableTestOIDC(t, s, ts, m, true)

	// Happy path: start sets the cookie; callback with that cookie succeeds.
	_, cb, ck := oidcStartAndCallbackURL(t, ts.URL)
	resp := getNoFollow(t, cb, ck)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 302 {
		t.Fatalf("happy callback %d %s", resp.StatusCode, body)
	}
	if loc := resp.Header.Get("Location"); loc != "/" && loc != ts.URL+"/" {
		t.Fatalf("happy callback loc %s", loc)
	}
	if !sessionCookieIssued(resp) {
		t.Fatalf("happy callback issued no session cookie: %v", resp.Cookies())
	}
	cleared := false
	for _, c := range resp.Cookies() {
		if c.Name == oidcStateCookieName && (c.MaxAge < 0 || c.Value == "") {
			cleared = true
		}
	}
	if !cleared {
		t.Fatalf("happy callback should clear %s: %v", oidcStateCookieName, resp.Cookies())
	}

	// Missing cookie: valid state/code, but rejected and no session.
	_, cb, ck = oidcStartAndCallbackURL(t, ts.URL)
	resp = getNoFollow(t, cb)
	resp.Body.Close()
	if urlUnescape(t, resp.Header.Get("Location")) != "invalid or expired sign-in request" {
		t.Fatalf("missing cookie loc %s", resp.Header.Get("Location"))
	}
	if sessionCookieIssued(resp) {
		t.Fatalf("missing cookie issued a session: %v", resp.Cookies())
	}
	// State is consumed, so retrying with the real cookie still fails.
	retry := getNoFollow(t, cb, ck)
	retry.Body.Close()
	if urlUnescape(t, retry.Header.Get("Location")) != "invalid or expired sign-in request" {
		t.Fatalf("consumed-state retry loc %s", retry.Header.Get("Location"))
	}
	if sessionCookieIssued(retry) {
		t.Fatalf("consumed-state retry issued a session: %v", retry.Cookies())
	}

	// Wrong cookie value is rejected and does not issue a session.
	_, cb, ck = oidcStartAndCallbackURL(t, ts.URL)
	wrong := *ck
	wrong.Value = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	resp = getNoFollow(t, cb, &wrong)
	resp.Body.Close()
	if urlUnescape(t, resp.Header.Get("Location")) != "invalid or expired sign-in request" {
		t.Fatalf("wrong cookie loc %s", resp.Header.Get("Location"))
	}
	if sessionCookieIssued(resp) {
		t.Fatalf("wrong cookie issued a session: %v", resp.Cookies())
	}
	retry = getNoFollow(t, cb, ck)
	retry.Body.Close()
	if sessionCookieIssued(retry) {
		t.Fatalf("wrong-cookie state should be consumed; retry issued a session: %v", retry.Cookies())
	}
}

func TestOIDCDoesNotLinkExistingAccountByUsernameAlone(t *testing.T) {
	s, ts, database := testServer(t)
	admin, err := database.GetUserByUsername("admin")
	if err != nil || admin == nil {
		t.Fatalf("admin: %+v %v", admin, err)
	}

	m := newMockOIDC(t, "filehub", "s3cret")
	m.setUser(mockOIDCUser{
		Subject:           "idp-attacker",
		Email:             "attacker@example.com",
		EmailVerified:     true,
		PreferredUsername: "admin",
	})
	enableTestOIDC(t, s, ts, m, false)

	resp := oidcFollowToCallback(t, ts.URL)
	defer resp.Body.Close()
	got := urlUnescape(t, resp.Header.Get("Location"))
	if got != "no matching Filehub account exists" {
		t.Fatalf("want no matching account, got %q loc=%s", got, resp.Header.Get("Location"))
	}
	if u, err := database.GetUserByOIDC(m.issuer(), "idp-attacker"); err != nil || u != nil {
		t.Fatalf("admin must not be linked: %+v %v", u, err)
	}
	again, err := database.GetUserByUsername("admin")
	if err != nil || again == nil || again.OIDCLinked() {
		t.Fatalf("admin should stay unlinked: %+v %v", again, err)
	}
}

func TestOIDCAutoCreateDoesNotTakeOverUsername(t *testing.T) {
	s, ts, database := testServer(t)
	m := newMockOIDC(t, "filehub", "s3cret")
	m.setUser(mockOIDCUser{
		Subject:           "idp-attacker",
		Email:             "attacker@example.com",
		EmailVerified:     true,
		PreferredUsername: "admin",
	})
	enableTestOIDC(t, s, ts, m, true)

	cookies := oidcLogin(t, ts.URL)
	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/me", cookies, nil)
	if st != 200 {
		t.Fatalf("me %d %v", st, data)
	}
	user, _ := data["user"].(map[string]any)
	if user["username"] == "admin" {
		t.Fatalf("must not sign in as admin: %v", user)
	}
	if user["username"] != "admin2" {
		t.Fatalf("expected uniquified username admin2, got %v", user)
	}
	if user["email"] != "attacker@example.com" {
		t.Fatalf("created email %v", user)
	}

	admin, err := database.GetUserByUsername("admin")
	if err != nil || admin == nil || admin.OIDCLinked() {
		t.Fatalf("admin should stay unlinked: %+v %v", admin, err)
	}
	created, err := database.GetUserByOIDC(m.issuer(), "idp-attacker")
	if err != nil || created == nil || created.Username == "admin" || created.ID == admin.ID {
		t.Fatalf("distinct user not created: %+v %v", created, err)
	}
}

func TestOIDCRejectsEmailMatchWhenAlreadyLinkedToDifferentSubject(t *testing.T) {
	s, ts, database := testServer(t)
	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	m := newMockOIDC(t, "filehub", "s3cret")
	m.setUser(mockOIDCUser{Subject: "idp-alice-a", Email: "alice@example.com", EmailVerified: true, PreferredUsername: "alice"})
	enableTestOIDC(t, s, ts, m, false)

	cookies := oidcLogin(t, ts.URL)
	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/me", cookies, nil)
	if st != 200 {
		t.Fatalf("first link me %d %v", st, data)
	}

	m.setUser(mockOIDCUser{Subject: "idp-alice-b", Email: "alice@example.com", EmailVerified: true, PreferredUsername: "alice"})
	resp := oidcFollowToCallback(t, ts.URL)
	defer resp.Body.Close()
	got := urlUnescape(t, resp.Header.Get("Location"))
	if got != "this email is already linked to another sign-in identity" {
		t.Fatalf("want already-linked error, got %q loc=%s", got, resp.Header.Get("Location"))
	}

	orig, err := database.GetUserByOIDC(m.issuer(), "idp-alice-a")
	if err != nil || orig == nil || orig.ID != alice.ID {
		t.Fatalf("original link missing: %+v %v", orig, err)
	}
	if hijack, err := database.GetUserByOIDC(m.issuer(), "idp-alice-b"); err != nil || hijack != nil {
		t.Fatalf("subject B must not be linked: %+v %v", hijack, err)
	}
	again, err := database.GetUserByUsername("alice")
	if err != nil || again == nil || again.OIDCSubject != "idp-alice-a" {
		t.Fatalf("alice link changed: %+v %v", again, err)
	}
}

func TestOIDCLinksByEmailFromUserInfoWhenIDTokenOmitsEmail(t *testing.T) {
	s, ts, database := testServer(t)
	if _, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	m := newMockOIDC(t, "filehub", "s3cret")
	m.setUser(mockOIDCUser{
		Subject:           "idp-alice",
		Email:             "alice@example.com",
		EmailVerified:     true,
		PreferredUsername: "alice",
		OmitIDTokenEmail:  true,
	})
	enableTestOIDC(t, s, ts, m, false)

	cookies := oidcLogin(t, ts.URL)
	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/me", cookies, nil)
	if st != 200 {
		t.Fatalf("me %d %v", st, data)
	}
	user, _ := data["user"].(map[string]any)
	if user["username"] != "alice" {
		t.Fatalf("linked user %v", user)
	}
	u, err := database.GetUserByOIDC(m.issuer(), "idp-alice")
	if err != nil || u == nil || u.Username != "alice" {
		t.Fatalf("stored link: %+v %v", u, err)
	}
}
