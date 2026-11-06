package files

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"` // relative within scope
	IsDir   bool      `json:"isDir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Ext     string    `json:"ext"`
	Kind    string    `json:"kind"` // dir|image|video|other
}

func SafeJoin(root, rel string) (string, error) {
	rel = filepath.ToSlash(rel)
	rel = strings.TrimPrefix(rel, "/")
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." {
		clean = ""
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	full := absRoot
	if clean != "" {
		full = filepath.Join(absRoot, clean)
	}
	full, err = filepath.Abs(full)
	if err != nil {
		return "", err
	}
	relCheck, err := filepath.Rel(absRoot, full)
	if err != nil || strings.HasPrefix(relCheck, "..") {
		return "", fmt.Errorf("path escapes root")
	}
	return full, nil
}

func EnsureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func KindOf(name string, isDir bool) string {
	if isDir {
		return "dir"
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg":
		return "image"
	case ".mp4", ".webm", ".ogg", ".mov", ".mkv", ".m4v":
		return "video"
	default:
		return "other"
	}
}

func List(root, rel string) ([]Entry, error) {
	full, err := SafeJoin(root, rel)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("not a directory")
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	baseRel := filepath.ToSlash(strings.TrimPrefix(rel, "/"))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		name := e.Name()
		p := name
		if baseRel != "" && baseRel != "." {
			p = filepath.ToSlash(filepath.Join(baseRel, name))
		}
		out = append(out, Entry{
			Name:    name,
			Path:    p,
			IsDir:   e.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().UTC(),
			Ext:     strings.ToLower(filepath.Ext(name)),
			Kind:    KindOf(name, e.IsDir()),
		})
	}
	return out, nil
}

func Mkdir(root, rel string) error {
	full, err := SafeJoin(root, rel)
	if err != nil {
		return err
	}
	return os.MkdirAll(full, 0o755)
}

func Rename(root, fromRel, toRel string) error {
	from, err := SafeJoin(root, fromRel)
	if err != nil {
		return err
	}
	to, err := SafeJoin(root, toRel)
	if err != nil {
		return err
	}
	return os.Rename(from, to)
}

func Delete(root, rel string) error {
	full, err := SafeJoin(root, rel)
	if err != nil {
		return err
	}
	absRoot, _ := filepath.Abs(root)
	if full == absRoot {
		return fmt.Errorf("cannot delete root")
	}
	return os.RemoveAll(full)
}

func SaveUpload(root, destDirRel, filename string, r io.Reader) (string, error) {
	filename = filepath.Base(filename)
	if filename == "" || filename == "." || filename == ".." {
		return "", fmt.Errorf("invalid filename")
	}
	dir, err := SafeJoin(root, destDirRel)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, filename)
	// ensure dest still under root
	if _, err := SafeJoin(root, filepath.ToSlash(filepath.Join(destDirRel, filename))); err != nil {
		return "", err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return "", err
	}
	rel := filename
	if destDirRel != "" && destDirRel != "." {
		rel = filepath.ToSlash(filepath.Join(destDirRel, filename))
	}
	return rel, nil
}

func Open(root, rel string) (*os.File, fs.FileInfo, error) {
	full, err := SafeJoin(root, rel)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if st.IsDir() {
		f.Close()
		return nil, nil, fmt.Errorf("is a directory")
	}
	return f, st, nil
}

// DirSize returns total size of regular files under root/rel (symlink-safe via SafeJoin root only).
// Symlinks are not followed into (filepath.WalkDir does not follow dir symlinks by default for the walk root children... actually WalkDir does not follow symlinks).
func DirSize(root, rel string) (int64, error) {
	full, err := SafeJoin(root, rel)
	if err != nil {
		return 0, err
	}
	st, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if !st.IsDir() {
		return st.Size(), nil
	}
	var total int64
	err = filepath.WalkDir(full, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// skip unreadable entries
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// skip symlinks to avoid escaping / double-count
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total, err
}

