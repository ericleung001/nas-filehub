package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func uploadFileConflict(t *testing.T, tsURL string, cookies []*http.Cookie, root, dir, name, content, conflict string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("root", root)
	_ = mw.WriteField("path", dir)
	if conflict != "" {
		_ = mw.WriteField("conflict", conflict)
	}
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
	body, _ := io.ReadAll(resp.Body)
	var data map[string]any
	if len(body) > 0 {
		_ = json.Unmarshal(body, &data)
	}
	return resp.StatusCode, data
}

func rawFile(t *testing.T, tsURL string, cookies []*http.Cookie, root, path string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, tsURL+"/api/raw?root="+root+"&path="+path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestUploadNoConflictUnchanged(t *testing.T) {
	_, ts, _ := testServer(t)
	c := loginAs(t, ts.URL, "admin", "admin-pass-123")
	st, data := uploadFileConflict(t, ts.URL, c, "home", "", "hello.txt", "hello", "")
	if st != 200 {
		t.Fatalf("status %d %v", st, data)
	}
	if data["path"] != "hello.txt" || data["name"] != "hello.txt" {
		t.Fatalf("response %v", data)
	}
	if _, ok := data["skipped"]; ok {
		t.Fatalf("unexpected skipped: %v", data)
	}
	if _, ok := data["renamed"]; ok {
		t.Fatalf("unexpected renamed: %v", data)
	}
	if _, ok := data["overwritten"]; ok {
		t.Fatalf("unexpected overwritten: %v", data)
	}
	st, body := rawFile(t, ts.URL, c, "home", "hello.txt")
	if st != 200 || string(body) != "hello" {
		t.Fatalf("raw %d %q", st, body)
	}
}

func TestUploadConflictRequiresPolicy(t *testing.T) {
	_, ts, _ := testServer(t)
	c := loginAs(t, ts.URL, "admin", "admin-pass-123")
	st, _ := uploadFileConflict(t, ts.URL, c, "home", "", "hello.txt", "first", "")
	if st != 200 {
		t.Fatalf("first upload %d", st)
	}
	st, data := uploadFileConflict(t, ts.URL, c, "home", "", "hello.txt", "second", "")
	if st != 409 || data["code"] != "exists" {
		t.Fatalf("want 409 exists, got %d %v", st, data)
	}
	st, body := rawFile(t, ts.URL, c, "home", "hello.txt")
	if st != 200 || string(body) != "first" {
		t.Fatalf("existing file must be unchanged, raw %d %q", st, body)
	}
}

func TestUploadConflictSkip(t *testing.T) {
	_, ts, _ := testServer(t)
	c := loginAs(t, ts.URL, "admin", "admin-pass-123")
	st, _ := uploadFileConflict(t, ts.URL, c, "home", "", "hello.txt", "keep", "")
	if st != 200 {
		t.Fatalf("first upload %d", st)
	}
	st, data := uploadFileConflict(t, ts.URL, c, "home", "", "hello.txt", "new", "skip")
	if st != 200 || data["skipped"] != true {
		t.Fatalf("skip %d %v", st, data)
	}
	st, body := rawFile(t, ts.URL, c, "home", "hello.txt")
	if st != 200 || string(body) != "keep" {
		t.Fatalf("skip must not modify, raw %d %q", st, body)
	}
}

func TestUploadConflictOverwrite(t *testing.T) {
	_, ts, _ := testServer(t)
	c := loginAs(t, ts.URL, "admin", "admin-pass-123")
	st, _ := uploadFileConflict(t, ts.URL, c, "home", "", "hello.txt", "old", "")
	if st != 200 {
		t.Fatalf("first upload %d", st)
	}
	st, data := uploadFileConflict(t, ts.URL, c, "home", "", "hello.txt", "new", "overwrite")
	if st != 200 || data["overwritten"] != true || data["path"] != "hello.txt" {
		t.Fatalf("overwrite %d %v", st, data)
	}
	st, body := rawFile(t, ts.URL, c, "home", "hello.txt")
	if st != 200 || string(body) != "new" {
		t.Fatalf("overwrite content %d %q", st, body)
	}
}

func TestUploadConflictRename(t *testing.T) {
	_, ts, _ := testServer(t)
	c := loginAs(t, ts.URL, "admin", "admin-pass-123")
	if st, _ := uploadFileConflict(t, ts.URL, c, "home", "", "photo.jpg", "a", ""); st != 200 {
		t.Fatalf("first %d", st)
	}
	st, data := uploadFileConflict(t, ts.URL, c, "home", "", "photo.jpg", "b", "rename")
	if st != 200 || data["renamed"] != true || data["name"] != "photo_1.jpg" || data["path"] != "photo_1.jpg" {
		t.Fatalf("rename _1 %d %v", st, data)
	}
	st, data = uploadFileConflict(t, ts.URL, c, "home", "", "photo.jpg", "c", "rename")
	if st != 200 || data["name"] != "photo_2.jpg" {
		t.Fatalf("rename _2 %d %v", st, data)
	}
	st, data = uploadFileConflict(t, ts.URL, c, "home", "", "photo_1.jpg", "d", "rename")
	if st != 200 || data["name"] != "photo_1_1.jpg" {
		t.Fatalf("incoming base %d %v want photo_1_1.jpg", st, data)
	}

	st, body := rawFile(t, ts.URL, c, "home", "photo.jpg")
	if st != 200 || string(body) != "a" {
		t.Fatalf("original %d %q", st, body)
	}
	st, body = rawFile(t, ts.URL, c, "home", "photo_1.jpg")
	if st != 200 || string(body) != "b" {
		t.Fatalf("photo_1 %d %q", st, body)
	}
	st, body = rawFile(t, ts.URL, c, "home", "photo_2.jpg")
	if st != 200 || string(body) != "c" {
		t.Fatalf("photo_2 %d %q", st, body)
	}
	st, body = rawFile(t, ts.URL, c, "home", "photo_1_1.jpg")
	if st != 200 || string(body) != "d" {
		t.Fatalf("photo_1_1 %d %q", st, body)
	}
}

func TestUploadConflictInvalidAction(t *testing.T) {
	_, ts, _ := testServer(t)
	c := loginAs(t, ts.URL, "admin", "admin-pass-123")
	st, data := uploadFileConflict(t, ts.URL, c, "home", "", "x.txt", "x", "delete")
	if st != 400 || !strings.Contains(str(data["error"]), "invalid") {
		t.Fatalf("want 400 invalid, got %d %v", st, data)
	}
}

func TestUploadConflictInSharedFolder(t *testing.T) {
	s, ts, database := testServer(t)
	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "homes/alice"), 0o755)
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "shared/team"), 0o755)
	if err := os.WriteFile(filepath.Join(s.Cfg.DataRoot, "shared/team/photo.jpg"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh, err := database.CreateShare("Team", "shared/team", alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetShareGrant(sh.ID, alice.ID, "write", boolPtr(true)); err != nil {
		t.Fatal(err)
	}
	c := loginAs(t, ts.URL, "alice", "alice-pass-123")
	root := "share:" + itoa(sh.ID)

	st, data := uploadFileConflict(t, ts.URL, c, root, "", "photo.jpg", "new", "")
	if st != 409 {
		t.Fatalf("no policy %d %v", st, data)
	}
	st, data = uploadFileConflict(t, ts.URL, c, root, "", "photo.jpg", "new", "skip")
	if st != 200 || data["skipped"] != true {
		t.Fatalf("skip %d %v", st, data)
	}
	if got, _ := os.ReadFile(filepath.Join(s.Cfg.DataRoot, "shared/team/photo.jpg")); string(got) != "old" {
		t.Fatalf("skip changed share file: %q", got)
	}
	st, data = uploadFileConflict(t, ts.URL, c, root, "", "photo.jpg", "b", "rename")
	if st != 200 || data["name"] != "photo_1.jpg" {
		t.Fatalf("rename in share %d %v", st, data)
	}
	if got, _ := os.ReadFile(filepath.Join(s.Cfg.DataRoot, "shared/team/photo_1.jpg")); string(got) != "b" {
		t.Fatalf("renamed share content %q", got)
	}
	st, data = uploadFileConflict(t, ts.URL, c, root, "", "photo.jpg", "c", "overwrite")
	if st != 200 || data["overwritten"] != true {
		t.Fatalf("overwrite in share %d %v", st, data)
	}
	if got, _ := os.ReadFile(filepath.Join(s.Cfg.DataRoot, "shared/team/photo.jpg")); string(got) != "c" {
		t.Fatalf("overwritten share content %q", got)
	}
}

func TestUploadConflictSkipDoesNotNeedQuota(t *testing.T) {
	s, ts, database := testServer(t)
	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "homes/alice"), 0o755)
	if err := os.WriteFile(filepath.Join(s.Cfg.DataRoot, "homes/alice/full.txt"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = alice
	c := loginAs(t, ts.URL, "alice", "alice-pass-123")
	// Already at quota; skip must still succeed and leave the file alone.
	st, data := uploadFileConflict(t, ts.URL, c, "home", "", "full.txt", strings.Repeat("x", 50), "skip")
	if st != 200 || data["skipped"] != true {
		t.Fatalf("skip at quota %d %v", st, data)
	}
	got, _ := os.ReadFile(filepath.Join(s.Cfg.DataRoot, "homes/alice/full.txt"))
	if string(got) != "0123456789" {
		t.Fatalf("file changed: %q", got)
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
