package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eric/nas-filehub/internal/db"
	"github.com/eric/nas-filehub/internal/files"
)

func loginAs(t *testing.T, tsURL, user, pass string) []*http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"username": user, "password": pass})
	resp, err := http.Post(tsURL+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("login %s %d: %s", user, resp.StatusCode, b)
	}
	return resp.Cookies()
}

func uploadFile(t *testing.T, tsURL string, cookies []*http.Cookie, root, dir, name, content string) int {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("root", root)
	_ = mw.WriteField("path", dir)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(fw, content)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, tsURL+"/api/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestHealthVersion(t *testing.T) {
	_, ts, _ := testServer(t)
	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/health", nil, nil)
	if st != 200 || data["ok"] != true {
		t.Fatalf("health %d %v", st, data)
	}
	if data["version"] != "0.4.0" {
		t.Fatalf("version %v", data["version"])
	}
}

func TestRegistrationApprovalGate(t *testing.T) {
	_, ts, database := testServer(t)
	if err := database.SetOpenRegistration(true); err != nil {
		t.Fatal(err)
	}
	// default require approval
	on, _ := database.RegistrationRequireApproval()
	if !on {
		t.Fatal("require approval should default on")
	}
	st, data := doJSON(t, http.MethodPost, ts.URL+"/api/register", nil, map[string]any{
		"username": "newbie", "password": "Newbie-pass-1", "email": "newbie@example.com",
	})
	if st != 201 {
		t.Fatalf("register %d %v", st, data)
	}
	if data["pending"] != true {
		t.Fatalf("expected pending: %v", data)
	}
	u, err := database.GetUserByUsername("newbie")
	if err != nil || u == nil || u.Status != db.UserStatusPending {
		t.Fatalf("status %+v err %v", u, err)
	}
	body, _ := json.Marshal(map[string]any{"username": "newbie", "password": "Newbie-pass-1"})
	resp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 403 || !strings.Contains(string(raw), "pending admin approval") {
		t.Fatalf("pending login %d %s", resp.StatusCode, raw)
	}
	admin := adminCookie(t, ts.URL)
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/admin/users/"+itoa(u.ID)+"/approve", admin, map[string]any{})
	if st != 200 {
		t.Fatalf("approve %d %v", st, data)
	}
	if loginAs(t, ts.URL, "newbie", "Newbie-pass-1") == nil {
		t.Fatal("approved user should login")
	}

	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/register", nil, map[string]any{
		"username": "rejectme", "password": "Reject-pass-1", "email": "r@example.com",
	})
	if st != 201 {
		t.Fatalf("register2 %d %v", st, data)
	}
	u2, _ := database.GetUserByUsername("rejectme")
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/admin/users/"+itoa(u2.ID)+"/reject", admin, map[string]any{})
	if st != 200 {
		t.Fatalf("reject %d %v", st, data)
	}
	gone, _ := database.GetUserByUsername("rejectme")
	if gone != nil {
		t.Fatal("rejected user should be deleted so the username can be reused")
	}
}

func TestShareQuotaCountsWriter(t *testing.T) {
	s, ts, database := testServer(t)
	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 80, "")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := database.CreateUser("bob", "bob-pass-1234", "homes/bob", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "homes/alice"), 0o755)
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "homes/bob"), 0o755)
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "shared/team"), 0o755)
	// Pre-existing unbilled share file
	if err := os.WriteFile(filepath.Join(s.Cfg.DataRoot, "shared/team/legacy.txt"), bytes.Repeat([]byte("x"), 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	sh, err := database.CreateShare("Team", "shared/team", alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetShareGrant(sh.ID, alice.ID, "write", boolPtr(true)); err != nil {
		t.Fatal(err)
	}
	if err := database.SetShareGrant(sh.ID, bob.ID, "write", boolPtr(true)); err != nil {
		t.Fatal(err)
	}

	aliceC := loginAs(t, ts.URL, "alice", "alice-pass-123")
	root := "share:" + itoa(sh.ID)
	// 40-byte write billed to alice
	if st := uploadFile(t, ts.URL, aliceC, root, "", "a.txt", strings.Repeat("a", 40)); st != 200 {
		t.Fatalf("alice upload %d", st)
	}
	if used := s.usedBytes(alice); used != 40 {
		t.Fatalf("alice used %d want 40 (legacy unbilled)", used)
	}
	// 20 more ok (60 total), 30 more would exceed 80
	if st := uploadFile(t, ts.URL, aliceC, root, "", "b.txt", strings.Repeat("b", 20)); st != 200 {
		t.Fatalf("alice second upload %d", st)
	}
	if st := uploadFile(t, ts.URL, aliceC, root, "", "c.txt", strings.Repeat("c", 30)); st != 403 {
		t.Fatalf("alice over-quota upload status %d", st)
	}
	bobC := loginAs(t, ts.URL, "bob", "bob-pass-1234")
	if st := uploadFile(t, ts.URL, bobC, root, "", "bob.txt", strings.Repeat("z", 80)); st != 200 {
		t.Fatalf("bob upload %d", st)
	}
	if used := s.usedBytes(alice); used != 60 {
		t.Fatalf("alice should stay 60, got %d (bob's write must not bill alice)", used)
	}
	if used := s.usedBytes(bob); used != 80 {
		t.Fatalf("bob used %d want 80", used)
	}
}

func TestTrashRestoreAndQuota(t *testing.T) {
	s, ts, database := testServer(t)
	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(s.Cfg.DataRoot, "homes/alice")
	_ = os.MkdirAll(home, 0o755)
	if err := os.WriteFile(filepath.Join(home, "keep.txt"), []byte("hello-trash"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := loginAs(t, ts.URL, "alice", "alice-pass-123")
	before := s.usedBytes(alice)
	st, data := doJSON(t, http.MethodPost, ts.URL+"/api/delete", c, map[string]any{"root": "home", "path": "keep.txt"})
	if st != 200 {
		t.Fatalf("delete %d %v", st, data)
	}
	if _, err := os.Stat(filepath.Join(home, "keep.txt")); !os.IsNotExist(err) {
		t.Fatal("file should have left home")
	}
	afterDel := s.usedBytes(alice)
	if afterDel != before {
		t.Fatalf("trash should still count: before %d after %d", before, afterDel)
	}
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/trash", c, nil)
	if st != 200 {
		t.Fatalf("list trash %d %v", st, data)
	}
	items, _ := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items %v", data)
	}
	item := items[0].(map[string]any)
	id, _ := item["id"].(string)
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/trash/restore", c, map[string]any{"id": id})
	if st != 200 {
		t.Fatalf("restore %d %v", st, data)
	}
	if _, err := os.Stat(filepath.Join(home, "keep.txt")); err != nil {
		t.Fatalf("restored file missing: %v", err)
	}
	if s.usedBytes(alice) != before {
		t.Fatalf("usage after restore %d want %d", s.usedBytes(alice), before)
	}
	st, _ = doJSON(t, http.MethodPost, ts.URL+"/api/delete", c, map[string]any{"root": "home", "path": "keep.txt"})
	if st != 200 {
		t.Fatal("second delete")
	}
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/trash", c, nil)
	id, _ = data["items"].([]any)[0].(map[string]any)["id"].(string)
	st, _ = doJSON(t, http.MethodPost, ts.URL+"/api/trash/purge", c, map[string]any{"id": id})
	if st != 200 {
		t.Fatal("purge")
	}
	if s.usedBytes(alice) != 0 {
		t.Fatalf("purged file should not count, used %d", s.usedBytes(alice))
	}
	ents, _ := os.ReadDir(filepath.Join(s.Cfg.DataRoot, files.TrashDirName))
	if len(ents) != 0 {
		t.Fatalf("trash dir should be empty, got %d", len(ents))
	}
}

func TestSearchRespectsShares(t *testing.T) {
	s, ts, database := testServer(t)
	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := database.CreateUser("bob", "bob-pass-1234", "homes/bob", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "homes/alice"), 0o755)
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "homes/bob"), 0o755)
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "shared/team"), 0o755)
	_ = os.WriteFile(filepath.Join(s.Cfg.DataRoot, "homes/alice", "secret-report.txt"), []byte("a"), 0o644)
	_ = os.WriteFile(filepath.Join(s.Cfg.DataRoot, "shared/team", "shared-report.txt"), []byte("b"), 0o644)
	sh, err := database.CreateShare("Team", "shared/team", alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetShareGrant(sh.ID, bob.ID, "read", boolPtr(false)); err != nil {
		t.Fatal(err)
	}
	bobC := loginAs(t, ts.URL, "bob", "bob-pass-1234")
	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/search?q=report", bobC, nil)
	if st != 200 {
		t.Fatalf("search %d %v", st, data)
	}
	results, _ := data["results"].([]any)
	var names []string
	for _, r := range results {
		m := r.(map[string]any)
		names = append(names, m["name"].(string))
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "shared-report.txt") {
		t.Fatalf("bob should see share hit: %v", names)
	}
	if strings.Contains(joined, "secret-report.txt") {
		t.Fatalf("bob must not see alice home: %v", names)
	}
}

func TestFolderZipDownload(t *testing.T) {
	s, ts, database := testServer(t)
	_, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.Cfg.DataRoot, "homes/alice", "pics")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("AAA"), 0o644)
	c := loginAs(t, ts.URL, "alice", "alice-pass-123")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/zip?root=home&path=pics", nil)
	for _, ck := range c {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("zip %d %s", resp.StatusCode, b)
	}
	raw, _ := io.ReadAll(resp.Body)
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("zip parse: %v", err)
	}
	found := false
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "a.txt") {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			if string(b) != "AAA" {
				t.Fatalf("content %q", b)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("a.txt missing in zip, files=%v", func() []string {
			var n []string
			for _, f := range zr.File {
				n = append(n, f.Name)
			}
			return n
		}())
	}
}

func boolPtr(v bool) *bool { return &v }
