package files

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitFileName(t *testing.T) {
	cases := []struct {
		in, base, ext string
	}{
		{"photo.jpg", "photo", ".jpg"},
		{"photo", "photo", ""},
		{"archive.tar.gz", "archive.tar", ".gz"},
		{".gitignore", ".gitignore", ""},
		{"photo_1.jpg", "photo_1", ".jpg"},
	}
	for _, c := range cases {
		base, ext := SplitFileName(c.in)
		if base != c.base || ext != c.ext {
			t.Errorf("SplitFileName(%q) = %q, %q; want %q, %q", c.in, base, ext, c.base, c.ext)
		}
	}
}

func TestNextAvailableName(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := NextAvailableName(dir, "photo.jpg")
	if err != nil || got != "photo.jpg" {
		t.Fatalf("free name: got %q %v", got, err)
	}

	mustWrite("photo.jpg", "orig")
	got, err = NextAvailableName(dir, "photo.jpg")
	if err != nil || got != "photo_1.jpg" {
		t.Fatalf("first suffix: got %q %v want photo_1.jpg", got, err)
	}

	mustWrite("photo_1.jpg", "one")
	got, err = NextAvailableName(dir, "photo.jpg")
	if err != nil || got != "photo_2.jpg" {
		t.Fatalf("increment: got %q %v want photo_2.jpg", got, err)
	}

	// Incoming name is the base: photo_1.jpg taken -> photo_1_1.jpg, not photo_2.jpg.
	got, err = NextAvailableName(dir, "photo_1.jpg")
	if err != nil || got != "photo_1_1.jpg" {
		t.Fatalf("incoming base: got %q %v want photo_1_1.jpg", got, err)
	}

	mustWrite("photo_1_1.jpg", "nested")
	got, err = NextAvailableName(dir, "photo_1.jpg")
	if err != nil || got != "photo_1_2.jpg" {
		t.Fatalf("incoming base increment: got %q %v want photo_1_2.jpg", got, err)
	}

	mustWrite("notes", "x")
	got, err = NextAvailableName(dir, "notes")
	if err != nil || got != "notes_1" {
		t.Fatalf("no ext: got %q %v want notes_1", got, err)
	}

	mustWrite(".gitignore", "g")
	got, err = NextAvailableName(dir, ".gitignore")
	if err != nil || got != ".gitignore_1" {
		t.Fatalf("dotfile: got %q %v want .gitignore_1", got, err)
	}

	// A directory occupying the name is also "taken".
	if err := os.Mkdir(filepath.Join(dir, "folder.jpg"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = NextAvailableName(dir, "folder.jpg")
	if err != nil || got != "folder_1.jpg" {
		t.Fatalf("dir taken: got %q %v want folder_1.jpg", got, err)
	}
}

func TestParseConflictAction(t *testing.T) {
	if a, err := ParseConflictAction(""); err != nil || a != ConflictNone {
		t.Fatalf("empty: %q %v", a, err)
	}
	if a, err := ParseConflictAction(" Rename "); err != nil || a != ConflictRename {
		t.Fatalf("rename: %q %v", a, err)
	}
	if _, err := ParseConflictAction("delete"); err == nil {
		t.Fatal("expected invalid action error")
	}
}

func TestSaveUploadNoConflict(t *testing.T) {
	root := t.TempDir()
	res, err := SaveUpload(root, "", "hello.txt", strings.NewReader("hello"), ConflictNone)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rel != "hello.txt" || res.Name != "hello.txt" || res.Skipped || res.Overwritten || res.Renamed {
		t.Fatalf("unexpected result %#v", res)
	}
	got, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("content %q %v", got, err)
	}
}

func TestSaveUploadSkip(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := SaveUpload(root, "", "hello.txt", strings.NewReader("new"), ConflictSkip)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped || res.Rel != "hello.txt" {
		t.Fatalf("unexpected result %#v", res)
	}
	got, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil || string(got) != "keep" {
		t.Fatalf("skip must not modify existing file, got %q %v", got, err)
	}
}

func TestSaveUploadOverwrite(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := SaveUpload(root, "sub", "hello.txt", strings.NewReader("should-not-matter"), ConflictNone)
	// dest is sub/ — no conflict
	if err != nil {
		t.Fatal(err)
	}
	_ = res
	res, err = SaveUpload(root, "", "hello.txt", strings.NewReader("new"), ConflictOverwrite)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Overwritten || res.Rel != "hello.txt" || res.Renamed {
		t.Fatalf("unexpected result %#v", res)
	}
	got, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil || string(got) != "new" {
		t.Fatalf("overwrite content %q %v", got, err)
	}
}

func TestSaveUploadRename(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "photo.jpg"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := SaveUpload(root, "", "photo.jpg", strings.NewReader("b"), ConflictRename)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Renamed || res.Name != "photo_1.jpg" || res.Rel != "photo_1.jpg" {
		t.Fatalf("unexpected result %#v", res)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "photo.jpg")); string(got) != "a" {
		t.Fatalf("original changed: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "photo_1.jpg")); string(got) != "b" {
		t.Fatalf("renamed content: %q", got)
	}

	res, err = SaveUpload(root, "", "photo.jpg", strings.NewReader("c"), ConflictRename)
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "photo_2.jpg" {
		t.Fatalf("increment got %q", res.Name)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "photo_2.jpg")); string(got) != "c" {
		t.Fatalf("photo_2 content: %q", got)
	}

	res, err = SaveUpload(root, "", "photo_1.jpg", strings.NewReader("d"), ConflictRename)
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "photo_1_1.jpg" {
		t.Fatalf("incoming base got %q want photo_1_1.jpg", res.Name)
	}
}

func TestSaveUploadExistsWithoutPolicy(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := SaveUpload(root, "", "hello.txt", strings.NewReader("new"), ConflictNone)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "hello.txt"))
	if string(got) != "keep" {
		t.Fatalf("must not modify, got %q", got)
	}
}

func TestSaveUploadOverwriteDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "photo.jpg"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := SaveUpload(root, "", "photo.jpg", strings.NewReader("x"), ConflictOverwrite)
	if !errors.Is(err, ErrIsDirectory) {
		t.Fatalf("want ErrIsDirectory, got %v", err)
	}
}

func TestSaveUploadIntoSubdir(t *testing.T) {
	root := t.TempDir()
	res, err := SaveUpload(root, "a/b", "n.txt", strings.NewReader("z"), ConflictNone)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rel != "a/b/n.txt" {
		t.Fatalf("rel %q", res.Rel)
	}
	got, err := os.ReadFile(filepath.Join(root, "a/b/n.txt"))
	if err != nil || string(got) != "z" {
		t.Fatalf("content %q %v", got, err)
	}
}
