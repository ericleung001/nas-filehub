package api

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/eric/nas-filehub/internal/config"
	"github.com/eric/nas-filehub/internal/files"
)

func jsonReq(t *testing.T, method, url string, cookies []*http.Cookie, body any, hdr map[string]string, raw []byte) (int, map[string]any, []byte) {
	t.Helper()
	var rdr io.Reader
	if raw != nil {
		rdr = bytes.NewReader(raw)
	} else if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if raw == nil && body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var data map[string]any
	if len(b) > 0 {
		_ = json.Unmarshal(b, &data)
	}
	return resp.StatusCode, data, b
}

func TestChunkedUploadResumeAndComplete(t *testing.T) {
	_, ts, _ := testServer(t)
	c := loginAs(t, ts.URL, "admin", "admin-pass-123")
	content := strings.Repeat("abcdefghij", 100) // 1000 bytes
	st, data, _ := jsonReq(t, http.MethodPost, ts.URL+"/api/upload/sessions", c, map[string]any{
		"root": "home", "path": "", "name": "big.bin", "size": len(content),
	}, nil, nil)
	if st != 200 {
		t.Fatalf("init %d %v", st, data)
	}
	id, _ := data["id"].(string)
	if id == "" {
		t.Fatalf("missing id %v", data)
	}
	first := []byte(content[:400])
	st, data, _ = jsonReq(t, http.MethodPut, ts.URL+"/api/upload/sessions/"+id, c, nil, map[string]string{"Upload-Offset": "0"}, first)
	if st != 200 || int(data["offset"].(float64)) != 400 {
		t.Fatalf("chunk1 %d %v", st, data)
	}
	// Wrong offset is rejected and does not change progress.
	st, data, _ = jsonReq(t, http.MethodPut, ts.URL+"/api/upload/sessions/"+id, c, nil, map[string]string{"Upload-Offset": "0"}, first)
	if st != 409 {
		t.Fatalf("replay offset %d %v", st, data)
	}
	st, data, _ = jsonReq(t, http.MethodGet, ts.URL+"/api/upload/sessions/"+id, c, nil, nil, nil)
	if st != 200 || int(data["offset"].(float64)) != 400 {
		t.Fatalf("status %d %v", st, data)
	}
	rest := []byte(content[400:])
	st, data, _ = jsonReq(t, http.MethodPut, ts.URL+"/api/upload/sessions/"+id, c, nil, map[string]string{"Upload-Offset": "400"}, rest)
	if st != 200 || int(data["offset"].(float64)) != len(content) {
		t.Fatalf("chunk2 %d %v", st, data)
	}
	st, data, _ = jsonReq(t, http.MethodPost, ts.URL+"/api/upload/sessions/"+id+"/complete", c, map[string]any{}, nil, nil)
	if st != 200 || data["path"] != "big.bin" {
		t.Fatalf("complete %d %v", st, data)
	}
	st, body := rawFile(t, ts.URL, c, "home", "big.bin")
	if st != 200 || string(body) != content {
		t.Fatalf("content %d len=%d", st, len(body))
	}
}

func TestChunkedUploadConflictAndOverwriteTrash(t *testing.T) {
	_, ts, _ := testServer(t)
	c := loginAs(t, ts.URL, "admin", "admin-pass-123")
	if st, _ := uploadFileConflict(t, ts.URL, c, "home", "", "clip.mp4", "old", ""); st != 200 {
		t.Fatal("seed")
	}
	st, data, _ := jsonReq(t, http.MethodPost, ts.URL+"/api/upload/sessions", c, map[string]any{
		"root": "home", "name": "clip.mp4", "size": 3,
	}, nil, nil)
	if st != 409 {
		t.Fatalf("no policy %d %v", st, data)
	}
	st, data, _ = jsonReq(t, http.MethodPost, ts.URL+"/api/upload/sessions", c, map[string]any{
		"root": "home", "name": "clip.mp4", "size": 3, "conflict": "skip",
	}, nil, nil)
	if st != 200 || data["skipped"] != true {
		t.Fatalf("skip init %d %v", st, data)
	}
	st, data, _ = jsonReq(t, http.MethodPost, ts.URL+"/api/upload/sessions", c, map[string]any{
		"root": "home", "name": "clip.mp4", "size": 3, "conflict": "rename",
	}, nil, nil)
	if st != 200 || data["name"] != "clip_1.mp4" {
		t.Fatalf("rename init %d %v", st, data)
	}
	id, _ := data["id"].(string)
	st, _, _ = jsonReq(t, http.MethodPut, ts.URL+"/api/upload/sessions/"+id, c, nil, map[string]string{"Upload-Offset": "0"}, []byte("new"))
	if st != 200 {
		t.Fatalf("rename chunk %d", st)
	}
	st, data, _ = jsonReq(t, http.MethodPost, ts.URL+"/api/upload/sessions/"+id+"/complete", c, map[string]any{}, nil, nil)
	if st != 200 || data["name"] != "clip_1.mp4" {
		t.Fatalf("rename complete %d %v", st, data)
	}

	st, data, _ = jsonReq(t, http.MethodPost, ts.URL+"/api/upload/sessions", c, map[string]any{
		"root": "home", "name": "clip.mp4", "size": 3, "conflict": "overwrite",
	}, nil, nil)
	if st != 200 {
		t.Fatalf("overwrite init %d %v", st, data)
	}
	id, _ = data["id"].(string)
	st, _, _ = jsonReq(t, http.MethodPut, ts.URL+"/api/upload/sessions/"+id, c, nil, map[string]string{"Upload-Offset": "0"}, []byte("zzz"))
	if st != 200 {
		t.Fatalf("overwrite chunk %d", st)
	}
	st, data, _ = jsonReq(t, http.MethodPost, ts.URL+"/api/upload/sessions/"+id+"/complete", c, map[string]any{}, nil, nil)
	if st != 200 || data["overwritten"] != true {
		t.Fatalf("overwrite complete %d %v", st, data)
	}
	st, body := rawFile(t, ts.URL, c, "home", "clip.mp4")
	if st != 200 || string(body) != "zzz" {
		t.Fatalf("overwritten body %q", body)
	}
	items := listTrash(t, ts.URL, c)
	if len(items) != 1 || items[0]["name"] != "clip.mp4" {
		t.Fatalf("expected trashed original, got %#v", items)
	}
}

func TestChunkedUploadAuthAndCleanup(t *testing.T) {
	s, ts, database := testServer(t)
	if _, err := database.CreateUser("bob", "bob-pass-123", "homes/bob", false, 0, ""); err != nil {
		t.Fatal(err)
	}
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	st, data, _ := jsonReq(t, http.MethodPost, ts.URL+"/api/upload/sessions", admin, map[string]any{
		"root": "home", "name": "a.bin", "size": 2,
	}, nil, nil)
	if st != 200 {
		t.Fatalf("init %d %v", st, data)
	}
	id, _ := data["id"].(string)
	st, _, _ = jsonReq(t, http.MethodGet, ts.URL+"/api/upload/sessions/"+id, nil, nil, nil, nil)
	if st != 401 {
		t.Fatalf("unauth status %d", st)
	}
	bob := loginAs(t, ts.URL, "bob", "bob-pass-123")
	st, _, _ = jsonReq(t, http.MethodGet, ts.URL+"/api/upload/sessions/"+id, bob, nil, nil, nil)
	if st != 404 {
		t.Fatalf("other user should not see session %d", st)
	}
	// Expire and cleanup.
	sess := s.uploads().Get(id)
	if sess == nil {
		t.Fatal("missing session")
	}
	sess.UpdatedAt = time.Now().Add(-48 * time.Hour)
	_ = s.uploads().Put(sess)
	if n := s.uploads().Cleanup(); n < 1 {
		t.Fatalf("cleanup removed %d, want at least 1", n)
	}
	if s.uploads().Get(id) != nil {
		t.Fatal("session should be gone")
	}
	// Incomplete complete is rejected.
	st, data, _ = jsonReq(t, http.MethodPost, ts.URL+"/api/upload/sessions", admin, map[string]any{
		"root": "home", "name": "b.bin", "size": 4,
	}, nil, nil)
	id, _ = data["id"].(string)
	st, _, _ = jsonReq(t, http.MethodPut, ts.URL+"/api/upload/sessions/"+id, admin, nil, map[string]string{"Upload-Offset": "0"}, []byte("ab"))
	if st != 200 {
		t.Fatalf("partial %d", st)
	}
	st, data, _ = jsonReq(t, http.MethodPost, ts.URL+"/api/upload/sessions/"+id+"/complete", admin, map[string]any{}, nil, nil)
	if st != 400 || !strings.Contains(str(data["error"]), "incomplete") {
		t.Fatalf("incomplete complete %d %v", st, data)
	}
}

func TestUploadManagerTTL(t *testing.T) {
	dir := t.TempDir()
	m := NewUploadManager(dir, time.Millisecond)
	sess := &UploadSession{ID: "abc", UserID: 1, OrigName: "x", FinalName: "x", Size: 1, UpdatedAt: time.Now().Add(-time.Second), CreatedAt: time.Now().Add(-time.Second)}
	if err := m.Put(sess); err != nil {
		t.Fatal(err)
	}
	if files.IsInternalDirName(files.UploadsDirName) != true {
		t.Fatal("uploads dir should be internal")
	}
	time.Sleep(2 * time.Millisecond)
	if n := m.Cleanup(); n < 1 {
		t.Fatalf("cleanup %d", n)
	}
}

func TestMaxUploadBytesLimit(t *testing.T) {
	s := &Server{Cfg: config.Config{MaxUploadMB: 2048}}
	if got, want := s.maxUploadBytes(), int64(2048)<<20; got != want {
		t.Fatalf("2048 MiB: got %d want %d", got, want)
	}
	s.Cfg.MaxUploadMB = 0
	if s.maxUploadBytes() != math.MaxInt64 {
		t.Fatalf("0 should be unlimited, got %d", s.maxUploadBytes())
	}
	s.Cfg.MaxUploadMB = -1
	if s.maxUploadBytes() != int64(config.DefaultMaxUploadMB)<<20 {
		t.Fatalf("negative should use default, got %d", s.maxUploadBytes())
	}
	s.Cfg.MaxUploadMB = config.DefaultMaxUploadMB
	const threeGiB = int64(3) << 30
	if s.maxUploadBytes() < threeGiB {
		t.Fatalf("default limit %d is below 3 GiB", s.maxUploadBytes())
	}
	if formatUploadLimit(102400) != "100 GiB" {
		t.Fatalf("format %q", formatUploadLimit(102400))
	}
	if formatUploadLimit(2048) != "2 GiB" {
		t.Fatalf("format 2048 %q", formatUploadLimit(2048))
	}
	s.Cfg.MaxUploadMB = 2048
	if msg := s.uploadTooLargeMessage(); msg != "upload too large (max 2 GiB)" {
		t.Fatalf("message %q", msg)
	}
}

func TestChunkedUploadSizeLimit(t *testing.T) {
	s, ts, _ := testServer(t)
	c := loginAs(t, ts.URL, "admin", "admin-pass-123")
	const threeGiB = int64(3) << 30
	s.Cfg.MaxUploadMB = 2048
	st, data, _ := jsonReq(t, http.MethodPost, ts.URL+"/api/upload/sessions", c, map[string]any{
		"root": "home", "path": "", "name": "movie.bin", "size": threeGiB,
	}, nil, nil)
	if st != 400 || !strings.Contains(str(data["error"]), "upload too large") {
		t.Fatalf("2 GiB cap should reject 3 GiB: %d %v", st, data)
	}

	s.Cfg.MaxUploadMB = config.DefaultMaxUploadMB
	st, data, _ = jsonReq(t, http.MethodPost, ts.URL+"/api/upload/sessions", c, map[string]any{
		"root": "home", "path": "", "name": "movie.bin", "size": threeGiB,
	}, nil, nil)
	if st != 200 {
		t.Fatalf("default cap should accept 3 GiB declared size: %d %v", st, data)
	}
	id, _ := data["id"].(string)
	if id == "" {
		t.Fatalf("missing id %v", data)
	}
	st, _, _ = jsonReq(t, http.MethodDelete, ts.URL+"/api/upload/sessions/"+id, c, nil, nil, nil)
	if st != 200 && st != 204 {
		// abort should succeed; ignore exact code if handler returns 200
		if st >= 300 {
			t.Fatalf("abort %d", st)
		}
	}

	s.Cfg.MaxUploadMB = 0
	st, data, _ = jsonReq(t, http.MethodPost, ts.URL+"/api/upload/sessions", c, map[string]any{
		"root": "home", "path": "", "name": "huge.bin", "size": threeGiB,
	}, nil, nil)
	if st != 200 {
		t.Fatalf("MAX_UPLOAD_MB=0 should accept 3 GiB: %d %v", st, data)
	}
	id, _ = data["id"].(string)
	if id != "" {
		_, _, _ = jsonReq(t, http.MethodDelete, ts.URL+"/api/upload/sessions/"+id, c, nil, nil, nil)
	}
}
