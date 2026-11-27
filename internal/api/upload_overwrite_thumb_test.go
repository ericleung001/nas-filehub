package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eric/nas-filehub/internal/thumb"
)

func TestUploadOverwriteInvalidatesThumbCache(t *testing.T) {
	s, ts, _ := testServer(t)
	cacheDir := filepath.Join(t.TempDir(), "thumbs")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s.Thumbs = &thumb.Generator{CacheDir: cacheDir, Enable: true}

	c := loginAs(t, ts.URL, "admin", "admin-pass-123")
	st, data := uploadFileConflict(t, ts.URL, c, "home", "", "pic.jpg", "old-bytes", "")
	if st != 200 {
		t.Fatalf("first upload %d %v", st, data)
	}
	src := filepath.Join(s.Cfg.DataRoot, "homes/admin/pic.jpg")
	cached := s.Thumbs.PathFor(src)
	if err := os.WriteFile(cached, []byte("stale-thumb"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, data = uploadFileConflict(t, ts.URL, c, "home", "", "pic.jpg", "new-bytes", "overwrite")
	if st != 200 || data["overwritten"] != true {
		t.Fatalf("overwrite %d %v", st, data)
	}
	if _, err := os.Stat(cached); !os.IsNotExist(err) {
		t.Fatalf("overwrite should invalidate thumb cache, stat err=%v", err)
	}
	got, err := os.ReadFile(src)
	if err != nil || string(got) != "new-bytes" {
		t.Fatalf("overwritten file %q %v", got, err)
	}
	items := listTrash(t, ts.URL, c)
	if len(items) != 1 || items[0]["name"] != "pic.jpg" {
		t.Fatalf("original should be in recycle bin, got %#v", items)
	}
}
