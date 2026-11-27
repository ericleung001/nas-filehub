package thumb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInvalidateRemovesCachedThumb(t *testing.T) {
	dir := t.TempDir()
	g := &Generator{CacheDir: dir, Enable: true}
	src := filepath.Join(dir, "photo.jpg")
	out := g.PathFor(src)
	if err := os.WriteFile(out, []byte("cached-thumb"), 0o644); err != nil {
		t.Fatal(err)
	}
	g.Invalidate(src)
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("cached thumb should be removed, stat err=%v", err)
	}
	// Missing cache and nil/disabled generators must not panic.
	g.Invalidate(src)
	(*Generator)(nil).Invalidate(src)
	disabled := &Generator{CacheDir: dir, Enable: false}
	if err := os.WriteFile(out, []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	disabled.Invalidate(src)
	if b, err := os.ReadFile(out); err != nil || string(b) != "kept" {
		t.Fatalf("disabled generator must not remove cache: %q %v", b, err)
	}
}
