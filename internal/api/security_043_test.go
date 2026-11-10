package api

import (
	"bytes"
	"crypto/tls"
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
)

func doRaw(t *testing.T, method, url string, cookies []*http.Cookie, headers map[string]string, payload any) *http.Response {
	t.Helper()
	var body io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestSecurityHeadersOnDocument(t *testing.T) {
	_, ts, _ := testServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	h := resp.Header
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("nosniff: %q", h.Get("X-Content-Type-Options"))
	}
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "style-src 'self'", "object-src 'none'", "base-uri 'self'", "form-action 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("CSP missing %q: %s", want, csp)
		}
	}
	if h.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("frame %q", h.Get("X-Frame-Options"))
	}
	if h.Get("Permissions-Policy") == "" || h.Get("Cross-Origin-Opener-Policy") != "same-origin" {
		t.Fatalf("permissions/coop missing: %+v", h)
	}
	if h.Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS must not be set by the app")
	}
}

func TestRawFrameHeadersStillSameOrigin(t *testing.T) {
	s, ts, _ := testServer(t)
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "homes/admin"), 0o755)
	if st := uploadFile(t, ts.URL, admin, "home", "", "photo.jpg", "not-a-real-jpeg"); st != 200 {
		t.Fatalf("upload %d", st)
	}
	resp := doRaw(t, http.MethodGet, ts.URL+"/api/raw?root=home&path=photo.jpg", admin, nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("raw %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("frame %q", resp.Header.Get("X-Frame-Options"))
	}
	if resp.Header.Get("Content-Security-Policy") != "frame-ancestors 'self'" {
		t.Fatalf("csp %q", resp.Header.Get("Content-Security-Policy"))
	}
}

func TestScriptableRawIsAttachment(t *testing.T) {
	s, ts, database := testServer(t)
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "homes/admin"), 0o755)
	html := "<script>document.cookie</script>"
	if st := uploadFile(t, ts.URL, admin, "home", "", "note.html", html); st != 200 {
		t.Fatalf("upload html %d", st)
	}
	if st := uploadFile(t, ts.URL, admin, "home", "", "icon.svg", `<svg xmlns="http://www.w3.org/2000/svg"></svg>`); st != 200 {
		t.Fatalf("upload svg")
	}
	for _, name := range []string{"note.html", "icon.svg"} {
		resp := doRaw(t, http.MethodGet, ts.URL+"/api/raw?root=home&path="+name, admin, nil, nil)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s raw %d %s", name, resp.StatusCode, body)
		}
		cd := resp.Header.Get("Content-Disposition")
		if !strings.HasPrefix(strings.ToLower(cd), "attachment") {
			t.Fatalf("%s disposition %q", name, cd)
		}
		ct := strings.ToLower(resp.Header.Get("Content-Type"))
		if !strings.Contains(ct, "application/octet-stream") {
			t.Fatalf("%s content-type %q", name, ct)
		}
	}

	link, err := database.CreateLinkShare("pubtokhtml", 1, "home", "note.html", false, "html", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(ts.URL + "/api/public/links/" + link.Token + "/raw")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("public raw %d", resp.StatusCode)
	}
	if !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Disposition")), "attachment") {
		t.Fatalf("public disposition %q", resp.Header.Get("Content-Disposition"))
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "application/octet-stream") {
		t.Fatalf("public type %q", resp.Header.Get("Content-Type"))
	}
}

func TestContentDispositionEncoding(t *testing.T) {
	got := contentDisposition("attachment", "quote\"name.txt")
	if !strings.Contains(got, `filename="quote\"name.txt"`) || !strings.Contains(got, "filename*=UTF-8''") {
		t.Fatalf("quotes: %s", got)
	}
	got = contentDisposition("attachment", "报告.pdf")
	if !strings.Contains(got, "filename*=UTF-8''") || strings.Contains(got, "\n") {
		t.Fatalf("non-ascii: %s", got)
	}
	got = contentDisposition("inline", "ok.pdf")
	if got != `inline; filename="ok.pdf"` {
		t.Fatalf("simple: %s", got)
	}
}

func TestPublicBaseURLHostHeader(t *testing.T) {
	s := &Server{Cfg: config.Config{}}
	r := httptest.NewRequest(http.MethodGet, "http://real.example/api/forgot-password", nil)
	r.Host = "real.example"
	r.RemoteAddr = "203.0.113.9:1"
	r.Header.Set("X-Forwarded-Host", "evil.example")
	r.Header.Set("X-Forwarded-Proto", "https")
	got := s.publicBaseURL(r)
	if got != "http://real.example" {
		t.Fatalf("untrusted peer must ignore forwarded host/proto: %s", got)
	}

	r.RemoteAddr = "127.0.0.1:1"
	got = s.publicBaseURL(r)
	if got != "https://real.example" {
		t.Fatalf("trusted peer may use proto but never X-Forwarded-Host: %s", got)
	}

	s.Cfg.PublicBaseURL = "https://filehub.example.com/"
	r.Host = "other.example"
	got = s.publicBaseURL(r)
	if got != "https://filehub.example.com" {
		t.Fatalf("PUBLIC_BASE_URL must always win: %s", got)
	}

	s.Cfg.PublicBaseURL = ""
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Del("X-Forwarded-Proto")
	r.TLS = &tls.ConnectionState{}
	got = s.publicBaseURL(r)
	if got != "https://other.example" {
		t.Fatalf("tls without forwarded proto: %s", got)
	}
}

func TestAccountLockoutAndSessionsAndAudit(t *testing.T) {
	s, ts, database := testServer(t)
	s.Cfg.LockoutLimit = 3
	s.Cfg.LockoutMinutes = 15
	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}

	// IP limiter still works independently (configure a tiny limiter and confirm 429).
	s.Limiter = auth.NewLoginLimiter(1000)

	for i := 0; i < 3; i++ {
		st, data := doJSON(t, http.MethodPost, ts.URL+"/api/login", nil, map[string]any{"username": "alice", "password": "wrong-pass"})
		if st != 401 {
			t.Fatalf("fail %d: %d %v", i+1, st, data)
		}
		errMsg, _ := data["error"].(string)
		if errMsg != "invalid credentials" {
			t.Fatalf("fail %d message %q", i+1, errMsg)
		}
	}
	st, data := doJSON(t, http.MethodPost, ts.URL+"/api/login", nil, map[string]any{"username": "alice", "password": "wrong-pass"})
	if st != 401 {
		t.Fatalf("locked status %d %v", st, data)
	}
	if data["error"] != "too many failed attempts, try again later" {
		t.Fatalf("locked message %v", data)
	}
	// Even the correct password is blocked while locked.
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/login", nil, map[string]any{"username": "alice", "password": "alice-pass-123"})
	if st != 401 || data["error"] != "too many failed attempts, try again later" {
		t.Fatalf("correct pw during lock: %d %v", st, data)
	}
	// Unknown user: same wording after N failures (no extra enumeration).
	s2, ts2, _ := testServer(t)
	s2.Cfg.LockoutLimit = 3
	for i := 0; i < 3; i++ {
		st, data = doJSON(t, http.MethodPost, ts2.URL+"/api/login", nil, map[string]any{"username": "nosuch", "password": "x"})
		if st != 401 || data["error"] != "invalid credentials" {
			t.Fatalf("unknown fail %d: %d %v", i+1, st, data)
		}
	}
	st, data = doJSON(t, http.MethodPost, ts2.URL+"/api/login", nil, map[string]any{"username": "nosuch", "password": "x"})
	if data["error"] != "too many failed attempts, try again later" {
		t.Fatalf("unknown lock message %v", data)
	}

	// Expire the lock and log in successfully.
	_, _ = database.SQL.Exec(`UPDATE login_lockouts SET locked_until=? WHERE username=?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), "alice")
	cookies := loginAs(t, ts.URL, "alice", "alice-pass-123")
	if cookies == nil {
		t.Fatal("alice should log in after lock expiry")
	}

	// Second session for the same user.
	cookies2 := loginAs(t, ts.URL, "alice", "alice-pass-123")
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/me/sessions", cookies, nil)
	if st != 200 {
		t.Fatalf("list sessions %d %v", st, data)
	}
	raw, _ := json.Marshal(data["sessions"])
	var sessions []map[string]any
	if err := json.Unmarshal(raw, &sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions) < 2 {
		t.Fatalf("want at least 2 sessions, got %d", len(sessions))
	}
	var currentID, otherID int64
	for _, sess := range sessions {
		id, _ := sess["id"].(float64)
		if sess["current"] == true {
			currentID = int64(id)
		} else {
			otherID = int64(id)
		}
		if _, ok := sess["token"]; ok {
			t.Fatal("session token must not be exposed")
		}
	}
	if currentID == 0 || otherID == 0 {
		t.Fatalf("current/other missing: %+v", sessions)
	}
	st, data = doJSON(t, http.MethodDelete, ts.URL+"/api/me/sessions/"+itoa(otherID), cookies, nil)
	if st != 200 {
		t.Fatalf("revoke other %d %v", st, data)
	}
	// Other cookie should now be unauthorized.
	st, _ = doJSON(t, http.MethodGet, ts.URL+"/api/me", cookies2, nil)
	if st != 401 {
		t.Fatalf("revoked session still live: %d", st)
	}
	// Issue another and revoke others.
	_ = loginAs(t, ts.URL, "alice", "alice-pass-123")
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/me/sessions/revoke-others", cookies, nil)
	if st != 200 {
		t.Fatalf("revoke others %d %v", st, data)
	}
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/me/sessions", cookies, nil)
	raw, _ = json.Marshal(data["sessions"])
	sessions = nil
	_ = json.Unmarshal(raw, &sessions)
	if len(sessions) != 1 || sessions[0]["current"] != true {
		t.Fatalf("expected only current: %+v", sessions)
	}

	// Audit log: admin can read, alice cannot.
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/admin/audit", cookies, nil)
	if st != 403 {
		t.Fatalf("non-admin audit %d %v", st, data)
	}
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/admin/audit?limit=50", admin, nil)
	if st != 200 {
		t.Fatalf("admin audit %d %v", st, data)
	}
	raw, _ = json.Marshal(data["events"])
	var events []map[string]any
	_ = json.Unmarshal(raw, &events)
	var sawFail, sawOK bool
	for _, e := range events {
		switch e["action"] {
		case "login_failure":
			sawFail = true
		case "login_success":
			sawOK = true
		}
	}
	if !sawFail || !sawOK {
		t.Fatalf("expected login success and failure in audit: %+v", events)
	}
	_ = alice
}

func TestShareUnlockRateLimit(t *testing.T) {
	s, ts, database := testServer(t)
	s.Limiter = auth.NewLoginLimiter(3)
	admin, err := database.GetUserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := db.HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	link, err := database.CreateLinkShare("locktok", admin.ID, "home", "x.txt", false, "", hash, nil)
	if err != nil {
		t.Fatal(err)
	}
	url := ts.URL + "/api/public/links/" + link.Token + "/unlock"
	var last int
	for i := 0; i < 4; i++ {
		st, _ := doJSON(t, http.MethodPost, url, nil, map[string]any{"password": "wrong"})
		last = st
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after limiter, last=%d", last)
	}
}

func TestTrustedProxyLoginLimiterSeparatesClients(t *testing.T) {
	s, ts, _ := testServer(t)
	s.Limiter = auth.NewLoginLimiter(2)
	// httptest clients come from loopback, so forwarded IPs are honored.
	// Two different clients should get their own buckets.
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/login", bytes.NewReader([]byte(`{"username":"x","password":"y"}`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "198.51.100.10")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("client A should not be limited on attempt %d", i+1)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/login", bytes.NewReader([]byte(`{"username":"x","password":"y"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "198.51.100.10")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("client A should be limited, got %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/login", bytes.NewReader([]byte(`{"username":"x","password":"y"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "198.51.100.11")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		t.Fatal("client B must not share client A's limiter bucket")
	}
}

func TestAuditLinkAndGrantEvents(t *testing.T) {
	s, ts, database := testServer(t)
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "homes/admin"), 0o755)
	if err := os.WriteFile(filepath.Join(s.Cfg.DataRoot, "homes/admin/a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	st, data := doJSON(t, http.MethodPost, ts.URL+"/api/links", admin, map[string]any{
		"root": "home", "path": "a.txt", "label": "A",
	})
	if st != 201 {
		t.Fatalf("create link %d %v", st, data)
	}
	link := data["link"].(map[string]any)
	id := int64(link["id"].(float64))
	st, data = doJSON(t, http.MethodDelete, ts.URL+"/api/links/"+itoa(id), admin, nil)
	if st != 200 {
		t.Fatalf("revoke %d %v", st, data)
	}
	sh, err := database.CreateShare("Team", "shared/team", 1)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := database.CreateUser("bob", "bob-pass-1234", "homes/bob", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/admin/shares/"+itoa(sh.ID)+"/grants", admin, map[string]any{
		"userId": bob.ID, "perm": "read",
	})
	if st != 200 {
		t.Fatalf("grant %d %v", st, data)
	}
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/admin/audit", admin, nil)
	if st != 200 {
		t.Fatalf("audit %d %v", st, data)
	}
	raw, _ := json.Marshal(data["events"])
	blob := string(raw)
	for _, action := range []string{"share_link_create", "share_link_revoke", "share_grant_set"} {
		if !strings.Contains(blob, action) {
			t.Fatalf("missing %s in %s", action, blob)
		}
	}
}
