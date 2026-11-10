package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func adminCookie(t *testing.T, tsURL string) []*http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"username": "admin", "password": "admin-pass-123"})
	resp, err := http.Post(tsURL+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("login %d: %s", resp.StatusCode, b)
	}
	return resp.Cookies()
}

func doJSON(t *testing.T, method, url string, cookies []*http.Cookie, payload any) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if out == nil {
		out = map[string]any{"_raw": string(raw)}
	}
	return resp.StatusCode, out
}

func TestGlobalQuotaSettingsAPI(t *testing.T) {
	_, ts, database := testServer(t)
	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := database.CreateUser("bob", "bob-pass-1234", "homes/bob", false, 200, "")
	if err != nil {
		t.Fatal(err)
	}
	cookies := adminCookie(t, ts.URL)

	// GET defaults
	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/admin/settings", cookies, nil)
	if st != 200 {
		t.Fatalf("GET settings %d: %v", st, data)
	}
	if data["globalQuotaEnabled"] != false {
		t.Fatalf("expected disabled, got %v", data["globalQuotaEnabled"])
	}
	if n, _ := data["globalQuotaBytes"].(float64); n != 0 {
		t.Fatalf("expected 0 bytes, got %v", data["globalQuotaBytes"])
	}

	// Invalid value does not write
	st, data = doJSON(t, http.MethodPut, ts.URL+"/api/admin/settings", cookies, map[string]any{
		"globalQuotaEnabled": true,
		"globalQuotaBytes":   -5,
	})
	if st != 400 {
		t.Fatalf("invalid quota status %d: %v", st, data)
	}
	s, _ := database.GetGlobalQuotaSettings()
	if s.Enabled {
		t.Fatal("invalid PUT enabled the setting")
	}
	u, _ := database.GetUserByID(alice.ID)
	if u.QuotaBytes != 100 {
		t.Fatalf("alice quota changed after invalid PUT: %d", u.QuotaBytes)
	}

	const fiveGB int64 = 5 * 1024 * 1024 * 1024
	st, data = doJSON(t, http.MethodPut, ts.URL+"/api/admin/settings", cookies, map[string]any{
		"globalQuotaEnabled": true,
		"globalQuotaBytes":   fiveGB,
	})
	if st != 200 {
		t.Fatalf("enable status %d: %v", st, data)
	}
	if data["globalQuotaEnabled"] != true {
		t.Fatalf("response not enabled: %v", data)
	}
	if n, _ := data["globalQuotaBytes"].(float64); int64(n) != fiveGB {
		t.Fatalf("response bytes %v, want %d", data["globalQuotaBytes"], fiveGB)
	}
	for _, id := range []int64{alice.ID, bob.ID} {
		u, _ := database.GetUserByID(id)
		if u.QuotaBytes != fiveGB {
			t.Fatalf("user %d quota %d, want %d", id, u.QuotaBytes, fiveGB)
		}
	}
	adminUser, err := database.GetUserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	if adminUser.QuotaBytes != fiveGB {
		t.Fatalf("admin quota %d, want %d", adminUser.QuotaBytes, fiveGB)
	}

	// Admin create ignores posted quota
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/admin/users", cookies, map[string]any{
		"username":   "carol",
		"password":   "carol-pass-123",
		"quotaBytes": 999,
	})
	if st != 201 {
		t.Fatalf("create user %d: %v", st, data)
	}
	user, _ := data["user"].(map[string]any)
	if n, _ := user["quotaBytes"].(float64); int64(n) != fiveGB {
		t.Fatalf("created user quota %v, want %d", user["quotaBytes"], fiveGB)
	}

	// Open registration also gets the global quota
	st, data = doJSON(t, http.MethodPut, ts.URL+"/api/admin/settings", cookies, map[string]any{
		"openRegistration": true,
	})
	if st != 200 {
		t.Fatalf("open registration %d: %v", st, data)
	}
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/register", nil, map[string]any{
		"username": "dave",
		"password": "dave-pass-1234",
		"email":    "dave@example.com",
	})
	if st != 201 {
		t.Fatalf("register %d: %v", st, data)
	}
	dave, err := database.GetUserByUsername("dave")
	if err != nil || dave == nil {
		t.Fatalf("dave not created: %v", err)
	}
	if dave.QuotaBytes != fiveGB {
		t.Fatalf("registered user quota %d, want %d", dave.QuotaBytes, fiveGB)
	}

	// Per-user quota PATCH rejected while enabled
	st, data = doJSON(t, http.MethodPatch, ts.URL+"/api/admin/users/"+itoa(alice.ID), cookies, map[string]any{
		"quotaBytes": 1,
	})
	if st != 400 {
		t.Fatalf("expected 400 for per-user quota patch, got %d: %v", st, data)
	}
	if msg, _ := data["error"].(string); msg == "" || !containsFold(msg, "global quota") {
		t.Fatalf("unexpected error: %v", data)
	}
	alice2, _ := database.GetUserByID(alice.ID)
	if alice2.QuotaBytes != fiveGB {
		t.Fatalf("alice quota mutated: %d", alice2.QuotaBytes)
	}

	// Changing the value while enabled updates everyone
	const oneGB int64 = 1024 * 1024 * 1024
	st, data = doJSON(t, http.MethodPut, ts.URL+"/api/admin/settings", cookies, map[string]any{
		"globalQuotaEnabled": true,
		"globalQuotaBytes":   oneGB,
	})
	if st != 200 {
		t.Fatalf("change quota %d: %v", st, data)
	}
	for _, name := range []string{"admin", "alice", "bob", "carol", "dave"} {
		u, err := database.GetUserByUsername(name)
		if err != nil {
			t.Fatal(err)
		}
		if u.QuotaBytes != oneGB {
			t.Fatalf("%s quota %d, want %d", name, u.QuotaBytes, oneGB)
		}
	}

	// Disable leaves stored quotas; per-user edit works again
	st, data = doJSON(t, http.MethodPut, ts.URL+"/api/admin/settings", cookies, map[string]any{
		"globalQuotaEnabled": false,
	})
	if st != 200 {
		t.Fatalf("disable %d: %v", st, data)
	}
	if data["globalQuotaEnabled"] != false {
		t.Fatalf("not disabled: %v", data)
	}
	alice3, _ := database.GetUserByID(alice.ID)
	if alice3.QuotaBytes != oneGB {
		t.Fatalf("alice quota after disable = %d, want last global %d", alice3.QuotaBytes, oneGB)
	}
	st, data = doJSON(t, http.MethodPatch, ts.URL+"/api/admin/users/"+itoa(alice.ID), cookies, map[string]any{
		"quotaBytes": 4242,
	})
	if st != 200 {
		t.Fatalf("per-user patch after disable %d: %v", st, data)
	}
	alice4, _ := database.GetUserByID(alice.ID)
	if alice4.QuotaBytes != 4242 {
		t.Fatalf("alice quota after edit = %d", alice4.QuotaBytes)
	}
	bob2, _ := database.GetUserByID(bob.ID)
	if bob2.QuotaBytes != oneGB {
		t.Fatalf("bob should stay at last global %d, got %d", oneGB, bob2.QuotaBytes)
	}
}

func itoa(id int64) string {
	return jsonNumber(id)
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
