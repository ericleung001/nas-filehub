package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/eric/nas-filehub/internal/db"
)

func TestAuditLogMaxSettingsDefaultAndCap(t *testing.T) {
	_, ts, database := testServer(t)
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")

	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/admin/settings", admin, nil)
	if st != 200 {
		t.Fatalf("GET settings %d %v", st, data)
	}
	if int(data["auditLogMax"].(float64)) != 0 {
		t.Fatalf("default auditLogMax want 0, got %v", data["auditLogMax"])
	}

	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/admin/audit", admin, nil)
	if st != 200 {
		t.Fatalf("GET audit %d %v", st, data)
	}
	if int(data["auditLogMax"].(float64)) != 0 {
		t.Fatalf("audit payload default max want 0, got %v", data["auditLogMax"])
	}

	for i := 0; i < 5; i++ {
		if err := database.AppendAudit(1, "admin", "127.0.0.1", "login_success", fmt.Sprintf("keep-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	_, total, err := database.ListAudit(200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total < 5 {
		t.Fatalf("unlimited should keep appends, total=%d", total)
	}

	st, data = doJSON(t, http.MethodPut, ts.URL+"/api/admin/settings", admin, map[string]any{
		"auditLogMax": 3,
	})
	if st != 200 {
		t.Fatalf("set cap 3: %d %v", st, data)
	}
	if int(data["auditLogMax"].(float64)) != 3 {
		t.Fatalf("saved auditLogMax want 3, got %v", data["auditLogMax"])
	}
	events, total, err := database.ListAudit(200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(events) != 3 {
		t.Fatalf("lowering cap should prune immediately, total=%d len=%d", total, len(events))
	}

	if err := database.AppendAudit(1, "admin", "127.0.0.1", "login_success", "newest"); err != nil {
		t.Fatal(err)
	}
	events, total, err = database.ListAudit(200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("after append over cap, total=%d want 3", total)
	}
	if events[0].Detail != "newest" {
		t.Fatalf("expected newest kept first, got %+v", events)
	}

	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/admin/audit?limit=50", admin, nil)
	if st != 200 {
		t.Fatalf("GET audit after cap %d %v", st, data)
	}
	if int(data["total"].(float64)) != 3 || int(data["auditLogMax"].(float64)) != 3 {
		t.Fatalf("audit GET total/max: %v", data)
	}
}

func TestAuditLogMaxInvalidRejected(t *testing.T) {
	_, ts, database := testServer(t)
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	if err := database.SetAuditLogMax(10); err != nil {
		t.Fatal(err)
	}
	if err := database.AppendAudit(1, "admin", "127.0.0.1", "login_success", "stay"); err != nil {
		t.Fatal(err)
	}

	st, data := doJSON(t, http.MethodPut, ts.URL+"/api/admin/settings", admin, map[string]any{
		"auditLogMax": -1,
	})
	if st != 400 {
		t.Fatalf("negative cap want 400, got %d %v", st, data)
	}
	if errMsg, _ := data["error"].(string); errMsg == "" {
		t.Fatalf("expected validation error, got %v", data)
	}

	st, data = doJSON(t, http.MethodPut, ts.URL+"/api/admin/settings", admin, map[string]any{
		"auditLogMax": db.MaxAuditLogMax + 1,
	})
	if st != 400 {
		t.Fatalf("oversized cap want 400, got %d %v", st, data)
	}

	max, err := database.GetAuditLogMax()
	if err != nil {
		t.Fatal(err)
	}
	if max != 10 {
		t.Fatalf("rejected writes must leave cap at 10, got %d", max)
	}
	_, total, err := database.ListAudit(200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total < 1 {
		t.Fatalf("rejected writes must not prune, total=%d", total)
	}

	st, data = doRawJSON(t, http.MethodPut, ts.URL+"/api/admin/settings", admin, `{"auditLogMax":"abc"}`)
	if st != 400 {
		t.Fatalf("non-integer cap want 400, got %d %v", st, data)
	}
}

func TestAuditLogMaxNonAdminForbidden(t *testing.T) {
	_, ts, database := testServer(t)
	if _, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, ""); err != nil {
		t.Fatal(err)
	}
	alice := loginAs(t, ts.URL, "alice", "alice-pass-123")

	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/admin/settings", alice, nil)
	if st != 403 {
		t.Fatalf("non-admin GET settings want 403, got %d %v", st, data)
	}
	st, data = doJSON(t, http.MethodPut, ts.URL+"/api/admin/settings", alice, map[string]any{
		"auditLogMax": 2,
	})
	if st != 403 {
		t.Fatalf("non-admin PUT auditLogMax want 403, got %d %v", st, data)
	}
	max, err := database.GetAuditLogMax()
	if err != nil {
		t.Fatal(err)
	}
	if max != 0 {
		t.Fatalf("non-admin must not change cap, got %d", max)
	}
}

func doRawJSON(t *testing.T, method, url string, cookies []*http.Cookie, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
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
