package files

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestKindOfTreatsHEICAsImage(t *testing.T) {
	for _, name := range []string{"IMG_0001.heic", "photo.HEIF", "clip.heic"} {
		if k := KindOf(name, false); k != "image" {
			t.Fatalf("KindOf(%q) = %q, want image", name, k)
		}
	}
	if KindOf("notes.bin", false) != "other" {
		t.Fatal("bin should stay other")
	}
}

func writeFile(t *testing.T, path string, body []byte, mt time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if !mt.IsZero() {
		if err := os.Chtimes(path, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAlbumCoverRelPreferredAndFallback(t *testing.T) {
	root := t.TempDir()
	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	writeFile(t, filepath.Join(root, "Photos", "old.jpg"), []byte("old"), old)
	writeFile(t, filepath.Join(root, "Photos", "new.jpg"), []byte("new"), newer)
	writeFile(t, filepath.Join(root, "Photos", "notes.txt"), []byte("x"), newer.Add(time.Hour))

	if got := AlbumCoverRel(root, "Photos", ""); got != "new.jpg" {
		t.Fatalf("auto cover = %q, want new.jpg", got)
	}
	if got := AlbumCoverRel(root, "Photos", "old.jpg"); got != "old.jpg" {
		t.Fatalf("preferred cover = %q", got)
	}
	// Missing preferred file falls back to automatic (do not return a broken path).
	if got := AlbumCoverRel(root, "Photos", "gone.jpg"); got != "new.jpg" {
		t.Fatalf("missing preferred should fall back, got %q", got)
	}
	// Non-media preferred is ignored.
	if got := AlbumCoverRel(root, "Photos", "notes.txt"); got != "new.jpg" {
		t.Fatalf("non-media preferred should fall back, got %q", got)
	}
	// Escape attempts are ignored.
	if got := AlbumCoverRel(root, "Photos", "../secret.jpg"); got != "new.jpg" {
		t.Fatalf("escape preferred should fall back, got %q", got)
	}

	// Subfolder preferred path.
	writeFile(t, filepath.Join(root, "Photos", "Trip", "hero.jpg"), []byte("hero"), newer.Add(2*time.Hour))
	if got := AlbumCoverRel(root, "Photos", "Trip/hero.jpg"); got != "Trip/hero.jpg" {
		t.Fatalf("subfolder preferred = %q", got)
	}

	// HEIC counts as cover media.
	heic, err := os.ReadFile(filepath.Join("testdata", "sample.heic"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "Photos", "iphone.heic"), heic, newer.Add(3*time.Hour))
	if got := AlbumCoverRel(root, "Photos", ""); got != "iphone.heic" {
		t.Fatalf("auto cover should pick newest HEIC, got %q", got)
	}
}
