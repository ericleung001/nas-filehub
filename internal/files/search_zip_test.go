package files

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSearchNamesAndWriteZip(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "pics", "raw"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "pics", "Holiday.jpg"), []byte("img"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "pics", "raw", "holiday.CR2"), []byte("raw"), 0o644)
	_ = os.MkdirAll(filepath.Join(root, TrashDirName, "x"), 0o755)
	_ = os.WriteFile(filepath.Join(root, TrashDirName, "x", "holiday-trashed.txt"), []byte("no"), 0o644)

	hits, err := SearchNames(root, "", "holiday", 20)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, h := range hits {
		names = append(names, h.Name)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "Holiday.jpg") || !strings.Contains(strings.ToLower(joined), "holiday.cr2") {
		t.Fatalf("hits %v", names)
	}
	if strings.Contains(joined, "holiday-trashed.txt") {
		t.Fatalf("trash should be skipped: %v", names)
	}

	var buf bytes.Buffer
	n, err := WriteZip(&buf, root, "pics", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("zip file count %d", n)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range zr.File {
		if strings.Contains(f.Name, TrashDirName) {
			t.Fatalf("zip included trash path %s", f.Name)
		}
		if strings.HasSuffix(f.Name, "Holiday.jpg") {
			found = true
		}
	}
	if !found {
		t.Fatal("Holiday.jpg missing from zip")
	}
}
