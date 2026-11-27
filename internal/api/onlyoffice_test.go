package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eric/nas-filehub/internal/auth"
	"github.com/eric/nas-filehub/internal/db"
	"github.com/eric/nas-filehub/internal/version"
)

func TestOnlyOfficeSettingsDefaultOff(t *testing.T) {
	s, ts, _ := testServer(t)
	s.Cfg.PublicBaseURL = "https://filehub.example.com"
	resp := loginJSON(t, ts, false)
	defer resp.Body.Close()
	cookie := resp.Header.Get("Set-Cookie")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/admin/settings", nil)
	req.Header.Set("Cookie", cookie)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	oo, _ := body["onlyoffice"].(map[string]any)
	if oo == nil {
		t.Fatal("missing onlyoffice in settings")
	}
	if oo["enabled"] != false {
		t.Fatalf("enabled=%v want false", oo["enabled"])
	}
	if oo["ready"] != false {
		t.Fatalf("ready=%v want false", oo["ready"])
	}
}

func TestOnlyOfficeEnableRequiresPublicBaseURL(t *testing.T) {
	s, ts, _ := testServer(t)
	s.Cfg.PublicBaseURL = ""
	resp := loginJSON(t, ts, false)
	defer resp.Body.Close()
	cookie := resp.Header.Get("Set-Cookie")
	payload, _ := json.Marshal(map[string]any{
		"onlyofficeEnabled":   true,
		"onlyofficeUrl":       "https://oo.example.com",
		"onlyofficeJwtSecret": "secret-value",
	})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/admin/settings", bytes.NewReader(payload))
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 400 {
		b, _ := io.ReadAll(r.Body)
		t.Fatalf("status=%d body=%s", r.StatusCode, b)
	}
}

func TestOnlyOfficeJWTRoundTripAndConfig(t *testing.T) {
	s, ts, database := testServer(t)
	s.Cfg.PublicBaseURL = "https://filehub.example.com"
	secret := "test-oo-jwt-secret"
	if _, err := database.SetOnlyOfficeSettings(db.OnlyOfficeSettings{
		Enabled:   true,
		URL:       "https://oo.example.com",
		JWTSecret: secret,
	}, &secret); err != nil {
		t.Fatal(err)
	}
	u, err := database.GetUserByUsername("admin")
	if err != nil || u == nil {
		t.Fatal(err)
	}
	home := filepath.Join(s.Cfg.DataRoot, u.HomePath)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(home, "note.docx")
	if err := os.WriteFile(doc, []byte("PK fake docx"), 0o644); err != nil {
		t.Fatal(err)
	}

	tok, err := signHS256JWT(secret, map[string]any{"typ": "oo-content", "uid": u.ID, "root": "home", "path": "note.docx", "exp": time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifyHS256JWT(secret, tok)
	if err != nil {
		t.Fatal(err)
	}
	if claimString(claims, "typ") != "oo-content" {
		t.Fatalf("typ=%v", claims["typ"])
	}

	resp := loginJSON(t, ts, false)
	defer resp.Body.Close()
	cookie := resp.Header.Get("Set-Cookie")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/onlyoffice/config?root=home&path=note.docx", nil)
	req.Header.Set("Cookie", cookie)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		b, _ := io.ReadAll(r.Body)
		t.Fatalf("config status=%d body=%s", r.StatusCode, b)
	}
	var cfgResp map[string]any
	if err := json.NewDecoder(r.Body).Decode(&cfgResp); err != nil {
		t.Fatal(err)
	}
	config, _ := cfgResp["config"].(map[string]any)
	if config == nil || config["token"] == "" {
		t.Fatalf("missing config token: %v", cfgResp)
	}
	docObj, _ := config["document"].(map[string]any)
	key1, _ := docObj["key"].(string)
	if key1 == "" {
		t.Fatal("missing document key")
	}
	req2, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/onlyoffice/config?root=home&path=note.docx", nil)
	req2.Header.Set("Cookie", cookie)
	r2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	var cfg2 map[string]any
	_ = json.NewDecoder(r2.Body).Decode(&cfg2)
	key2 := cfg2["config"].(map[string]any)["document"].(map[string]any)["key"].(string)
	if key1 != key2 {
		t.Fatalf("keys differ %s vs %s", key1, key2)
	}

	urlStr, _ := docObj["url"].(string)
	if !strings.Contains(urlStr, "/api/onlyoffice/content?token=") {
		t.Fatalf("bad content url %s", urlStr)
	}
	uParsed, err := url.Parse(urlStr)
	if err != nil {
		t.Fatal(err)
	}
	cr, err := http.Get(ts.URL + uParsed.RequestURI())
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Body.Close()
	if cr.StatusCode != 200 {
		b, _ := io.ReadAll(cr.Body)
		t.Fatalf("content status=%d body=%s", cr.StatusCode, b)
	}
	body, _ := io.ReadAll(cr.Body)
	if string(body) != "PK fake docx" {
		t.Fatalf("content=%q", body)
	}
}

func TestOnlyOfficeConfigDeniedWhenOff(t *testing.T) {
	_, ts, _ := testServer(t)
	resp := loginJSON(t, ts, false)
	defer resp.Body.Close()
	cookie := resp.Header.Get("Set-Cookie")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/onlyoffice/config?root=home&path=a.docx", nil)
	req.Header.Set("Cookie", cookie)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 400 {
		t.Fatalf("status=%d", r.StatusCode)
	}
}

func TestOnlyOfficeCallbackSaveAndPermission(t *testing.T) {
	s, ts, database := testServer(t)
	s.Cfg.PublicBaseURL = "https://filehub.example.com"
	secret := "callback-secret"
	if _, err := database.SetOnlyOfficeSettings(db.OnlyOfficeSettings{
		Enabled: true, URL: "https://oo.example.com", JWTSecret: secret,
	}, &secret); err != nil {
		t.Fatal(err)
	}
	u, _ := database.GetUserByUsername("admin")
	home := filepath.Join(s.Cfg.DataRoot, u.HomePath)
	_ = os.MkdirAll(home, 0o755)
	doc := filepath.Join(home, "edit.docx")
	if err := os.WriteFile(doc, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	savedTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("new-content-from-oo"))
	}))
	t.Cleanup(savedTS.Close)

	cbTok, err := signHS256JWT(secret, map[string]any{
		"typ": "oo-callback", "uid": u.ID, "root": "home", "path": "edit.docx",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{
		"status": 2,
		"url":    savedTS.URL + "/saved.docx",
	})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/onlyoffice/callback?token="+url.QueryEscape(cbTok), bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(r.Body).Decode(&out)
	if int(out["error"].(float64)) != 0 {
		t.Fatalf("callback error=%v", out)
	}
	got, _ := os.ReadFile(doc)
	if string(got) != "new-content-from-oo" {
		t.Fatalf("saved=%q", got)
	}

	other, err := database.CreateUser("bob", "bob-pass-12345", "homes/bob", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	roTok, _ := signHS256JWT(secret, map[string]any{
		"typ": "oo-callback", "uid": other.ID, "root": "home", "path": "edit.docx",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	badPayload, _ := json.Marshal(map[string]any{"status": 2, "url": savedTS.URL + "/saved.docx"})
	req2, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/onlyoffice/callback?token="+url.QueryEscape(roTok), bytes.NewReader(badPayload))
	req2.Header.Set("Content-Type", "application/json")
	r2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	var out2 map[string]any
	_ = json.NewDecoder(r2.Body).Decode(&out2)
	if int(out2["error"].(float64)) != 1 {
		t.Fatalf("want error 1 got %v", out2)
	}
}

func TestOnlyOfficeTrashRejected(t *testing.T) {
	s, ts, database := testServer(t)
	s.Cfg.PublicBaseURL = "https://filehub.example.com"
	secret := "trash-secret"
	if _, err := database.SetOnlyOfficeSettings(db.OnlyOfficeSettings{
		Enabled: true, URL: "https://oo.example.com", JWTSecret: secret,
	}, &secret); err != nil {
		t.Fatal(err)
	}
	resp := loginJSON(t, ts, false)
	defer resp.Body.Close()
	cookie := resp.Header.Get("Set-Cookie")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/onlyoffice/config?root=trash&path=x.docx", nil)
	req.Header.Set("Cookie", cookie)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 403 {
		b, _ := io.ReadAll(r.Body)
		t.Fatalf("status=%d body=%s", r.StatusCode, b)
	}
}

func TestOnlyOfficeCreateDocumentAndCoEditKey(t *testing.T) {
	s, ts, database := testServer(t)
	s.Cfg.PublicBaseURL = "https://filehub.example.com"
	secret := "create-secret"
	if _, err := database.SetOnlyOfficeSettings(db.OnlyOfficeSettings{
		Enabled: true, URL: "https://oo.example.com", JWTSecret: secret,
	}, &secret); err != nil {
		t.Fatal(err)
	}
	u, err := database.GetUserByUsername("admin")
	if err != nil || u == nil {
		t.Fatal(err)
	}
	home := filepath.Join(s.Cfg.DataRoot, u.HomePath)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}

	resp := loginJSON(t, ts, false)
	defer resp.Body.Close()
	cookie := resp.Header.Get("Set-Cookie")

	payload, _ := json.Marshal(map[string]any{
		"root": "home",
		"path": "",
		"name": "Hello.docx",
		"kind": "document",
	})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/onlyoffice/create", bytes.NewReader(payload))
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		b, _ := io.ReadAll(r.Body)
		t.Fatalf("create status=%d body=%s", r.StatusCode, b)
	}
	var created map[string]any
	if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	path, _ := created["path"].(string)
	if path == "" {
		t.Fatalf("missing path: %v", created)
	}
	full := filepath.Join(home, path)
	raw, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 4 || raw[0] != 'P' || raw[1] != 'K' {
		t.Fatalf("not a zip: %v", raw[:min(8, len(raw))])
	}

	req2, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/onlyoffice/config?root=home&path="+url.QueryEscape(path), nil)
	req2.Header.Set("Cookie", cookie)
	r2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	var cfg1 map[string]any
	_ = json.NewDecoder(r2.Body).Decode(&cfg1)
	key1 := cfg1["documentKey"].(string)
	ed := cfg1["config"].(map[string]any)["editorConfig"].(map[string]any)
	if ed["coEditing"] == nil {
		t.Fatalf("missing coEditing: %v", ed)
	}
	user := ed["user"].(map[string]any)
	if user["name"] != "admin" {
		t.Fatalf("user name=%v", user["name"])
	}

	req3, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/onlyoffice/config?root=home&path="+url.QueryEscape(path), nil)
	req3.Header.Set("Cookie", cookie)
	r3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	defer r3.Body.Close()
	var cfg2 map[string]any
	_ = json.NewDecoder(r3.Body).Decode(&cfg2)
	if cfg2["documentKey"] != key1 {
		t.Fatalf("co-edit keys differ %v vs %v", key1, cfg2["documentKey"])
	}

	// read-only trash rejected
	payload2, _ := json.Marshal(map[string]any{"root": "trash", "name": "x.docx", "kind": "document"})
	req4, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/onlyoffice/create", bytes.NewReader(payload2))
	req4.Header.Set("Cookie", cookie)
	req4.Header.Set("Content-Type", "application/json")
	r4, err := http.DefaultClient.Do(req4)
	if err != nil {
		t.Fatal(err)
	}
	defer r4.Body.Close()
	if r4.StatusCode != 403 {
		t.Fatalf("trash create status=%d", r4.StatusCode)
	}
}

func TestOnlyOfficeConfigIncomingItemShareWriteDocx(t *testing.T) {
	s, ts, database := testServer(t)
	s.Cfg.PublicBaseURL = "https://filehub.example.com"
	secret := "test-oo-incoming-secret"
	if _, err := database.SetOnlyOfficeSettings(db.OnlyOfficeSettings{
		Enabled:   true,
		URL:       "https://oo.example.com",
		JWTSecret: secret,
	}, &secret); err != nil {
		t.Fatal(err)
	}

	home := filepath.Join(s.Cfg.DataRoot, "homes/admin")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(home, "shared-edit.docx")
	if err := os.WriteFile(docPath, []byte("PK fake docx"), 0o644); err != nil {
		t.Fatal(err)
	}

	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	st, data := doJSON(t, http.MethodPost, ts.URL+"/api/item-shares", admin, map[string]any{
		"root": "home", "path": "shared-edit.docx", "userIds": []int64{alice.ID}, "perm": "write",
	})
	if st != 200 {
		t.Fatalf("share write docx %d %v", st, data)
	}
	shares, _ := data["shares"].([]any)
	if len(shares) == 0 {
		t.Fatalf("missing share row: %v", data)
	}
	shareID := itoa(int64(shares[0].(map[string]any)["id"].(float64)))

	aliceCk := loginAs(t, ts.URL, "alice", "alice-pass-123")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/onlyoffice/config?root=incoming&path="+url.QueryEscape(shareID), nil)
	for _, c := range aliceCk {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("incoming config status=%d body=%s", resp.StatusCode, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	cfg, _ := out["config"].(map[string]any)
	doc, _ := cfg["document"].(map[string]any)
	if doc["fileType"] != "docx" {
		t.Fatalf("fileType=%v want docx; full=%v", doc["fileType"], out)
	}
	if doc["title"] != "shared-edit.docx" {
		t.Fatalf("title=%v want shared-edit.docx", doc["title"])
	}
	ed, _ := cfg["editorConfig"].(map[string]any)
	if ed["mode"] != "edit" {
		t.Fatalf("mode=%v want edit for write share", ed["mode"])
	}
	if out["mode"] != "edit" {
		t.Fatalf("top-level mode=%v want edit", out["mode"])
	}
}

func TestOnlyOfficeDocumentKeySameFileHomeAndIncoming(t *testing.T) {
	s, ts, database := testServer(t)
	s.Cfg.PublicBaseURL = "https://filehub.example.com"
	secret := "test-oo-coedit-key"
	if _, err := database.SetOnlyOfficeSettings(db.OnlyOfficeSettings{
		Enabled: true, URL: "https://oo.example.com", JWTSecret: secret,
	}, &secret); err != nil {
		t.Fatal(err)
	}

	home := filepath.Join(s.Cfg.DataRoot, "homes/admin")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "coedit.docx"), []byte("PK coedit-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "other.docx"), []byte("PK coedit-b"), 0o644); err != nil {
		t.Fatal(err)
	}

	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	st, data := doJSON(t, http.MethodPost, ts.URL+"/api/item-shares", admin, map[string]any{
		"root": "home", "path": "coedit.docx", "userIds": []int64{alice.ID}, "perm": "write",
	})
	if st != 200 {
		t.Fatalf("share %d %v", st, data)
	}
	shareID := itoa(int64(data["shares"].([]any)[0].(map[string]any)["id"].(float64)))

	fetchKey := func(cookies []*http.Cookie, root, path string) (string, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/onlyoffice/config?root="+url.QueryEscape(root)+"&path="+url.QueryEscape(path), nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("config %s/%s status=%d body=%s", root, path, resp.StatusCode, body)
		}
		var out map[string]any
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatal(err)
		}
		return out["documentKey"].(string), out
	}

	ownerKey, ownerOut := fetchKey(admin, "home", "coedit.docx")
	aliceCk := loginAs(t, ts.URL, "alice", "alice-pass-123")
	recvKey, recvOut := fetchKey(aliceCk, "incoming", shareID)
	if ownerKey != recvKey {
		t.Fatalf("co-edit keys differ for same file: owner=%s recipient=%s", ownerKey, recvKey)
	}

	ownerUser := ownerOut["config"].(map[string]any)["editorConfig"].(map[string]any)["user"].(map[string]any)
	recvUser := recvOut["config"].(map[string]any)["editorConfig"].(map[string]any)["user"].(map[string]any)
	if ownerUser["id"] == recvUser["id"] || ownerUser["name"] == recvUser["name"] {
		t.Fatalf("expected distinct collaboration users: owner=%v recipient=%v", ownerUser, recvUser)
	}

	otherKey, _ := fetchKey(admin, "home", "other.docx")
	if otherKey == ownerKey {
		t.Fatalf("different files must not share documentKey: %s", otherKey)
	}

	// Unit-level: same full path+mtime/size → same key; different size → different.
	full := filepath.Join(home, "coedit.docx")
	fi, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	k1 := onlyOfficeDocumentKey(full, fi)
	k2 := onlyOfficeDocumentKey(filepath.Clean(full), fi)
	if k1 != k2 || k1 != ownerKey {
		t.Fatalf("direct key mismatch k1=%s k2=%s owner=%s", k1, k2, ownerKey)
	}
}

func TestOnlyOfficeCreatePresentation(t *testing.T) {
	s, ts, database := testServer(t)
	s.Cfg.PublicBaseURL = "https://filehub.example.com"
	secret := "test-oo-pptx-secret"
	if _, err := database.SetOnlyOfficeSettings(db.OnlyOfficeSettings{
		Enabled: true, URL: "https://oo.example.com", JWTSecret: secret,
	}, &secret); err != nil {
		t.Fatal(err)
	}
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	st, data := doJSON(t, http.MethodPost, ts.URL+"/api/onlyoffice/create", admin, map[string]any{
		"root": "home", "path": "", "name": "Deck.pptx", "kind": "presentation",
	})
	if st != 200 {
		t.Fatalf("create presentation %d %v", st, data)
	}
	if data["kind"] != "presentation" || data["name"] != "Deck.pptx" {
		t.Fatalf("create payload %v", data)
	}
	u, _ := database.GetUserByUsername("admin")
	full := filepath.Join(s.Cfg.DataRoot, u.HomePath, "Deck.pptx")
	b, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 4 || string(b[:2]) != "PK" {
		t.Fatalf("pptx should be a zip, got %d bytes", len(b))
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/onlyoffice/config?root=home&path=Deck.pptx", nil)
	for _, c := range admin {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("config %d %s", resp.StatusCode, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	ft := out["config"].(map[string]any)["document"].(map[string]any)["fileType"]
	if ft != "pptx" {
		t.Fatalf("fileType=%v want pptx", ft)
	}
}

func TestOnlyOfficeConfigRateLimit(t *testing.T) {
	s, ts, database := testServer(t)
	s.Cfg.PublicBaseURL = "https://filehub.example.com"
	s.OOConfigLimiter = auth.NewLoginLimiter(2)
	secret := "test-oo-rl-secret"
	if _, err := database.SetOnlyOfficeSettings(db.OnlyOfficeSettings{
		Enabled: true, URL: "https://oo.example.com", JWTSecret: secret,
	}, &secret); err != nil {
		t.Fatal(err)
	}
	u, err := database.GetUserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(s.Cfg.DataRoot, u.HomePath)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "a.docx"), []byte("PK"), 0o644); err != nil {
		t.Fatal(err)
	}
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	codes := []int{}
	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/onlyoffice/config?root=home&path=a.docx", nil)
		for _, c := range admin {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		codes = append(codes, resp.StatusCode)
	}
	if codes[0] != 200 || codes[1] != 200 {
		t.Fatalf("first two should be 200, got %v", codes)
	}
	if codes[2] != 429 {
		t.Fatalf("third should be 429, got %v", codes)
	}
}

func TestIndexHTMLCacheBust(t *testing.T) {
	dir := t.TempDir()
	html := []byte(`<!DOCTYPE html>
<html data-filehub-version="__FILEHUB_VERSION__">
<head>
  <link rel="stylesheet" href="/style.css" />
</head>
<body>
  <div class="about-card">
    <h3>About</h3>
    <p class="muted">Version __FILEHUB_VERSION__</p>
  </div>
  <script src="/app.js"></script>
</body>
</html>
`)
	if err := os.WriteFile(filepath.Join(dir, "index.html"), html, 0o644); err != nil {
		t.Fatal(err)
	}
	s, ts, _ := testServer(t)
	s.Static = os.DirFS(dir)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	wantVer := version.Version
	if strings.Contains(text, "__FILEHUB_VERSION__") {
		t.Fatalf("placeholder was not replaced, got %s", text)
	}
	if !strings.Contains(text, "Version "+wantVer) {
		t.Fatalf("expected About to show Version %s, got %s", wantVer, text)
	}
	if !strings.Contains(text, `data-filehub-version="`+wantVer+`"`) {
		t.Fatalf("expected data-filehub-version=%s, got %s", wantVer, text)
	}
	if !strings.Contains(text, `/style.css?v=`+wantVer) || !strings.Contains(text, `/app.js?v=`+wantVer) {
		t.Fatalf("expected css/js cache-bust ?v=%s, got %s", wantVer, text)
	}
}

func TestServeVersionedHTMLFromStaticSource(t *testing.T) {
	src := filepath.Join("..", "..", "web", "static")
	if _, err := os.Stat(filepath.Join(src, "index.html")); err != nil {
		t.Fatalf("static index.html not found at %s: %v", src, err)
	}
	raw, err := os.ReadFile(filepath.Join(src, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	if strings.Contains(source, "/style.css?v=") || strings.Contains(source, "/app.js?v=") || strings.Contains(source, "/photo-adjust.js?v=") {
		t.Fatalf("index.html source must use unversioned /style.css, /photo-adjust.js and /app.js so serveVersionedHTML can inject the current version, got versioned refs")
	}
	if !strings.Contains(source, `src="/photo-adjust.js"`) {
		t.Fatal("index.html must include unversioned /photo-adjust.js")
	}
	if !strings.Contains(source, "Version __FILEHUB_VERSION__") {
		t.Fatal("index.html About must use Version __FILEHUB_VERSION__ placeholder")
	}
	s, ts, _ := testServer(t)
	s.Static = os.DirFS(src)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	wantVer := version.Version
	if strings.Contains(text, "__FILEHUB_VERSION__") {
		t.Fatal("served index.html still contains the version placeholder")
	}
	if !strings.Contains(text, "Version "+wantVer) {
		t.Fatalf("expected served About to show Version %s", wantVer)
	}
	if !strings.Contains(text, `/style.css?v=`+wantVer) || !strings.Contains(text, `/app.js?v=`+wantVer) || !strings.Contains(text, `/photo-adjust.js?v=`+wantVer) {
		t.Fatalf("expected served css/js ?v=%s, got snippet around assets missing", wantVer)
	}
	if strings.Contains(text, "?v=0.4.24") {
		t.Fatal("served HTML still cache-busts with stale ?v=0.4.24")
	}
}
