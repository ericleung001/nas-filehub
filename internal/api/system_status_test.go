package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestSummarizeUserQuotasUnlimitedNotInAssigned(t *testing.T) {
	got := summarizeUserQuotas([]userQuotaUsage{
		{Used: 100, Quota: 1000},
		{Used: 200, Quota: 0}, // unlimited
		{Used: 50, Quota: 500},
		{Used: 0, Quota: 0},
	})
	if got.UserCount != 4 {
		t.Fatalf("userCount %d", got.UserCount)
	}
	if got.UsedBytes != 350 {
		t.Fatalf("used %d, want 350", got.UsedBytes)
	}
	if got.AssignedBytes != 1500 {
		t.Fatalf("assigned %d, want 1500 (finite only)", got.AssignedBytes)
	}
	if got.UnlimitedUsers != 2 || got.LimitedUsers != 2 {
		t.Fatalf("unlimited=%d limited=%d", got.UnlimitedUsers, got.LimitedUsers)
	}
	if got.Percent == nil {
		t.Fatal("expected percent")
	}
	// 350/1500 = 23.333... -> 23.3
	if *got.Percent != 23.3 {
		t.Fatalf("percent %v, want 23.3", *got.Percent)
	}

	allUnlimited := summarizeUserQuotas([]userQuotaUsage{
		{Used: 10, Quota: 0},
		{Used: 20, Quota: 0},
	})
	if allUnlimited.UsedBytes != 30 || allUnlimited.AssignedBytes != 0 || allUnlimited.Percent != nil {
		t.Fatalf("all unlimited: %+v", allUnlimited)
	}
	if allUnlimited.UnlimitedUsers != 2 || allUnlimited.LimitedUsers != 0 {
		t.Fatalf("all unlimited counts: %+v", allUnlimited)
	}
}

func TestAdminSystemStatusAuthAndPayload(t *testing.T) {
	srv, ts, database := testServer(t)

	resp, err := http.Get(ts.URL + "/api/admin/system/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anon status %d, want 401", resp.StatusCode)
	}

	if _, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 1024, ""); err != nil {
		t.Fatal(err)
	}
	aliceBody, _ := json.Marshal(map[string]any{"username": "alice", "password": "alice-pass-123"})
	aliceResp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(aliceBody))
	if err != nil {
		t.Fatal(err)
	}
	aliceCookies := aliceResp.Cookies()
	aliceResp.Body.Close()
	if aliceResp.StatusCode != 200 {
		t.Fatalf("alice login %d", aliceResp.StatusCode)
	}
	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/admin/system/status", aliceCookies, nil)
	if st != http.StatusForbidden {
		t.Fatalf("non-admin status %d: %v", st, data)
	}

	bob, err := database.CreateUser("bob", "bob-pass-1234", "homes/bob", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(srv.Cfg.DataRoot, bob.HomePath)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "n.txt"), []byte("hello-filehub"), 0o644); err != nil {
		t.Fatal(err)
	}

	cookies := adminCookie(t, ts.URL)
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/admin/system/status", cookies, nil)
	if st != 200 {
		t.Fatalf("admin status %d: %v", st, data)
	}
	cpu, ok := data["cpu"].(map[string]any)
	if !ok {
		t.Fatalf("missing cpu: %v", data)
	}
	if _, ok := cpu["cores"]; !ok {
		t.Fatalf("cpu.cores missing: %v", cpu)
	}
	mem, _ := data["memory"].(map[string]any)
	if mem["totalBytes"] == nil || mem["usedBytes"] == nil {
		t.Fatalf("memory incomplete: %v", mem)
	}
	quotas, _ := data["quotas"].(map[string]any)
	if quotas == nil {
		t.Fatal("missing quotas")
	}
	// admin default quota 0 (unlimited), alice 1024, bob 0
	if int(quotas["userCount"].(float64)) != 3 {
		t.Fatalf("userCount %v", quotas["userCount"])
	}
	if int(quotas["unlimitedUsers"].(float64)) != 2 {
		t.Fatalf("unlimitedUsers %v", quotas["unlimitedUsers"])
	}
	if int(quotas["limitedUsers"].(float64)) != 1 {
		t.Fatalf("limitedUsers %v", quotas["limitedUsers"])
	}
	if int64(quotas["assignedBytes"].(float64)) != 1024 {
		t.Fatalf("assignedBytes %v, want 1024", quotas["assignedBytes"])
	}
	if int64(quotas["usedBytes"].(float64)) < 13 {
		t.Fatalf("usedBytes %v, want at least file size", quotas["usedBytes"])
	}
	disk, _ := data["disk"].(map[string]any)
	if disk["ok"] != true {
		t.Fatalf("disk not ok: %v", disk)
	}
	if disk["path"] != srv.Cfg.DataRoot {
		t.Fatalf("disk path %v", disk["path"])
	}
	if disk["totalBytes"] == nil || disk["freeBytes"] == nil || disk["usedBytes"] == nil {
		t.Fatalf("disk incomplete: %v", disk)
	}
}
