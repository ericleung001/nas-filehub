package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/eric/nas-filehub/internal/files"
)

func ptrBool(v bool) *bool { return &v }

func loginClient(t *testing.T, ts *httptest.Server, user, pass string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Jar: jar}
	body, _ := json.Marshal(map[string]any{"username": user, "password": pass})
	resp, err := c.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("login as %s: %d", user, resp.StatusCode)
	}
	return c
}

func postJSON(t *testing.T, c *http.Client, url string, payload any) *http.Response {
	t.Helper()
	body, _ := json.Marshal(payload)
	resp, err := c.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func readJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestShareWriteWithoutDeleteCannotDelete(t *testing.T) {
	s, ts, database := testServer(t)
	admin, err := database.GetUserByUsername("admin")
	if err != nil || admin == nil {
		t.Fatal(err)
	}
	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	sh, err := database.CreateShare("team", "shared/team", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := files.SafeJoin(s.Cfg.DataRoot, sh.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := files.EnsureDir(abs); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(abs, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(abs, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := database.SetShareGrant(sh.ID, alice.ID, "write", ptrBool(false)); err != nil {
		t.Fatal(err)
	}

	client := loginClient(t, ts, "alice", "alice-pass-123")
	root := "share:" + strconv.FormatInt(sh.ID, 10)

	listResp, err := client.Get(ts.URL + "/api/list?root=" + root + "&path=")
	if err != nil {
		t.Fatal(err)
	}
	list := readJSON(t, listResp)
	if listResp.StatusCode != 200 {
		t.Fatalf("list: %d %+v", listResp.StatusCode, list)
	}
	if list["perm"] != "write" || list["canDelete"] != false {
		t.Fatalf("list perms: %+v", list)
	}

	mkdir := postJSON(t, client, ts.URL+"/api/mkdir", map[string]any{"root": root, "path": "subdir"})
	mkdirBody := readJSON(t, mkdir)
	if mkdir.StatusCode != 200 {
		t.Fatalf("write-without-delete should allow mkdir, got %d %+v", mkdir.StatusCode, mkdirBody)
	}

	del := postJSON(t, client, ts.URL+"/api/delete", map[string]any{"root": root, "path": "a.txt"})
	delBody := readJSON(t, del)
	if del.StatusCode != 403 {
		t.Fatalf("write-without-delete must not delete, got %d %+v", del.StatusCode, delBody)
	}
	if _, err := os.Stat(filepath.Join(abs, "a.txt")); err != nil {
		t.Fatalf("a.txt should still exist: %v", err)
	}

	batch := postJSON(t, client, ts.URL+"/api/batch/delete", map[string]any{"root": root, "paths": []string{"b.txt"}})
	batchBody := readJSON(t, batch)
	if batch.StatusCode != 403 {
		t.Fatalf("batch delete should be forbidden, got %d %+v", batch.StatusCode, batchBody)
	}

	// Upsert both fields: allow delete, then delete works.
	adminClient := loginClient(t, ts, "admin", "admin-pass-123")
	grant := postJSON(t, adminClient, ts.URL+"/api/admin/shares/"+strconv.FormatInt(sh.ID, 10)+"/grants", map[string]any{
		"userId": alice.ID, "perm": "write", "canDelete": true,
	})
	grantBody := readJSON(t, grant)
	if grant.StatusCode != 200 {
		t.Fatalf("grant update: %d %+v", grant.StatusCode, grantBody)
	}

	del2 := postJSON(t, client, ts.URL+"/api/delete", map[string]any{"root": root, "path": "a.txt"})
	del2Body := readJSON(t, del2)
	if del2.StatusCode != 200 {
		t.Fatalf("delete should succeed after canDelete=true, got %d %+v", del2.StatusCode, del2Body)
	}

	// Read + delete: can delete, cannot mkdir.
	grant2 := postJSON(t, adminClient, ts.URL+"/api/admin/shares/"+strconv.FormatInt(sh.ID, 10)+"/grants", map[string]any{
		"userId": alice.ID, "perm": "read", "canDelete": true,
	})
	readJSON(t, grant2)

	mkdir2 := postJSON(t, client, ts.URL+"/api/mkdir", map[string]any{"root": root, "path": "nope"})
	mkdir2Body := readJSON(t, mkdir2)
	if mkdir2.StatusCode != 403 {
		t.Fatalf("read+delete must not mkdir, got %d %+v", mkdir2.StatusCode, mkdir2Body)
	}
	del3 := postJSON(t, client, ts.URL+"/api/delete", map[string]any{"root": root, "path": "b.txt"})
	del3Body := readJSON(t, del3)
	if del3.StatusCode != 200 {
		t.Fatalf("read+delete should allow delete, got %d %+v", del3.StatusCode, del3Body)
	}
}

func TestAdminShareGrantRejectsUnknownUser(t *testing.T) {
	_, ts, _ := testServer(t)
	admin := loginClient(t, ts, "admin", "admin-pass-123")
	// need a share first
	s := postJSON(t, admin, ts.URL+"/api/admin/shares", map[string]any{"name": "x", "path": "shared/x"})
	body := readJSON(t, s)
	if s.StatusCode != 201 {
		t.Fatalf("create share: %d %+v", s.StatusCode, body)
	}
	share := body["share"].(map[string]any)
	id := int64(share["id"].(float64))
	resp := postJSON(t, admin, ts.URL+"/api/admin/shares/"+strconv.FormatInt(id, 10)+"/grants", map[string]any{
		"userId": 99999, "perm": "write", "canDelete": true,
	})
	out := readJSON(t, resp)
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for unknown user, got %d %+v", resp.StatusCode, out)
	}
}
