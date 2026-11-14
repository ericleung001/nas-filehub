package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eric/nas-filehub/internal/auth"
	"github.com/eric/nas-filehub/internal/config"
	"github.com/eric/nas-filehub/internal/db"
	"github.com/eric/nas-filehub/internal/secretbox"
)

func testServer(t *testing.T) (*Server, *httptest.Server, *db.DB) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.SQL.Close() })
	if _, err := database.CreateUser("admin", "admin-pass-123", "homes/admin", true, 0, ""); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Cfg: config.Config{
			DataRoot:       filepath.Join(dir, "files"),
			SecureCookies:  false,
			SessionSecret:  "test-session-secret",
			LockoutLimit:   10,
			LockoutMinutes: 15,
			MaxUploadMB:    2048,
			WebDAVEnabled:  true,
		},
		DB:           database,
		Limiter:      auth.NewLoginLimiter(1000),
		ResetLimiter: auth.NewLoginLimiter(1000),
		Static:       os.DirFS(dir),
	}
	database.SetTotpKey(secretbox.DeriveKey("test-session-secret"))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts, database
}

func loginJSON(t *testing.T, ts *httptest.Server, remember bool) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"username":   "admin",
		"password":   "admin-pass-123",
		"rememberMe": remember,
	})
	resp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func sessionExpiryFromDB(t *testing.T, d *db.DB) time.Time {
	t.Helper()
	var expStr string
	err := d.SQL.QueryRow(`SELECT expires_at FROM sessions ORDER BY rowid DESC LIMIT 1`).Scan(&expStr)
	if err != nil {
		t.Fatal(err)
	}
	exp, err := time.Parse(time.RFC3339, expStr)
	if err != nil {
		t.Fatal(err)
	}
	return exp
}

func cookieMaxAge(resp *http.Response) int {
	for _, c := range resp.Cookies() {
		if c.Name == auth.CookieName {
			return c.MaxAge
		}
	}
	return -1
}

func TestLoginUsesDefaultSessionTTL(t *testing.T) {
	_, ts, database := testServer(t)
	before := time.Now()
	resp := loginJSON(t, ts, false)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	exp := sessionExpiryFromDB(t, database)
	delta := exp.Sub(before)
	if delta < 7*24*time.Hour-time.Minute || delta > 7*24*time.Hour+time.Minute {
		t.Fatalf("expected ~7d expiry, got %v", delta)
	}
	ma := cookieMaxAge(resp)
	if ma < int((7*24*time.Hour - time.Minute).Seconds()) {
		t.Fatalf("cookie MaxAge too small: %d", ma)
	}
}

func TestLoginUsesAdminSessionTTLAndRememberMe(t *testing.T) {
	srv, ts, database := testServer(t)
	if err := database.SetSessionSettings(db.SessionSettings{
		SessionTTLSeconds:    3600,
		RememberMeTTLSeconds: 86400,
		AllowRememberMe:      true,
	}); err != nil {
		t.Fatal(err)
	}

	before := time.Now()
	resp := loginJSON(t, ts, false)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	exp := sessionExpiryFromDB(t, database)
	delta := exp.Sub(before)
	if delta < time.Hour-time.Minute || delta > time.Hour+time.Minute {
		t.Fatalf("expected ~1h expiry, got %v", delta)
	}

	before = time.Now()
	resp = loginJSON(t, ts, true)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("remember status %d: %s", resp.StatusCode, body)
	}
	exp = sessionExpiryFromDB(t, database)
	delta = exp.Sub(before)
	if delta < 24*time.Hour-time.Minute || delta > 24*time.Hour+time.Minute {
		t.Fatalf("expected ~1d remember expiry, got %v", delta)
	}

	// Disabled remember-me ignores client flag.
	if err := database.SetSessionSettings(db.SessionSettings{
		SessionTTLSeconds:    3600,
		RememberMeTTLSeconds: 86400,
		AllowRememberMe:      false,
	}); err != nil {
		t.Fatal(err)
	}
	before = time.Now()
	resp = loginJSON(t, ts, true)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	exp = sessionExpiryFromDB(t, database)
	delta = exp.Sub(before)
	if delta < time.Hour-time.Minute || delta > time.Hour+time.Minute {
		t.Fatalf("remember ignored when disabled; got %v", delta)
	}

	// Admin settings validation via API
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/admin/settings", strings.NewReader(`{"sessionTtlSeconds":60}`))
	req.Header.Set("Content-Type", "application/json")
	// Need admin session cookie from last login
	for _, c := range resp.Cookies() {
		req.AddCookie(c)
	}
	// last resp body closed; re-login for cookie
	resp = loginJSON(t, ts, false)
	cookies := resp.Cookies()
	resp.Body.Close()
	req, _ = http.NewRequest(http.MethodPut, ts.URL+"/api/admin/settings", strings.NewReader(`{"sessionTtlSeconds":60,"rememberMeTtlSeconds":3600,"allowRememberMe":true}`))
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	bad, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(bad.Body)
	bad.Body.Close()
	if bad.StatusCode != 400 {
		t.Fatalf("expected 400 for short TTL, got %d %s", bad.StatusCode, b)
	}
	if !strings.Contains(string(b), "1 hour") {
		t.Fatalf("expected clear validation message, got %s", b)
	}

	// features exposes allowRememberMe
	fresp, err := http.Get(ts.URL + "/api/auth/features")
	if err != nil {
		t.Fatal(err)
	}
	var feat map[string]any
	_ = json.NewDecoder(fresp.Body).Decode(&feat)
	fresp.Body.Close()
	if feat["allowRememberMe"] != false {
		t.Fatalf("allowRememberMe should be false: %#v", feat["allowRememberMe"])
	}
	_ = srv // silence if unused path changes
}
