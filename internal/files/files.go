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
	Kind    string    `json:"kind"` // dir|image|video|pdf|other
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
	case ".pdf":
		return "pdf"
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
		if IsInternalDirName(name) {
			continue
		}
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

// ConflictAction is the policy when an uploaded name is already taken.
type ConflictAction string

const (
	ConflictNone      ConflictAction = ""
	ConflictSkip      ConflictAction = "skip"
	ConflictOverwrite ConflictAction = "overwrite"
	ConflictRename    ConflictAction = "rename"
)

// ErrExists is returned when the destination name is taken and no conflict policy was given.
var ErrExists = fmt.Errorf("file already exists")

// ErrIsDirectory is returned when overwrite is requested for a name that is a folder.
var ErrIsDirectory = fmt.Errorf("a folder with this name already exists")

// ParseConflictAction accepts skip, overwrite, rename, or an empty value. Matching is case-insensitive.
func ParseConflictAction(s string) (ConflictAction, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "none":
		return ConflictNone, nil
	case "skip":
		return ConflictSkip, nil
	case "overwrite":
		return ConflictOverwrite, nil
	case "rename":
		return ConflictRename, nil
	default:
		return ConflictNone, fmt.Errorf("invalid conflict action")
	}
}

// SaveUploadResult describes what happened to an uploaded file.
type SaveUploadResult struct {
	Rel         string
	Name        string
	Skipped     bool
	Overwritten bool
	Renamed     bool
}

func joinRel(destDirRel, filename string) string {
	if destDirRel != "" && destDirRel != "." {
		return filepath.ToSlash(filepath.Join(destDirRel, filename))
	}
	return filename
}

func lstatExists(path string) (exists bool, isDir bool, err error) {
	st, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, false, nil
		}
		return false, false, err
	}
	return true, st.IsDir(), nil
}

// Exists reports whether a name is already taken under root/destDirRel.
func Exists(root, destDirRel, filename string) (exists bool, isDir bool, err error) {
	filename = filepath.Base(filename)
	if filename == "" || filename == "." || filename == ".." {
		return false, false, fmt.Errorf("invalid filename")
	}
	dir, err := SafeJoin(root, destDirRel)
	if err != nil {
		return false, false, err
	}
	return lstatExists(filepath.Join(dir, filename))
}

// SplitFileName splits a file name into a base and an extension for rename suffixes.
// The last dotted suffix is the extension, except a leading-dot name with no other
// dots (e.g. ".gitignore") is treated as a whole base so the result is ".gitignore_1".
func SplitFileName(filename string) (base, ext string) {
	ext = filepath.Ext(filename)
	base = strings.TrimSuffix(filename, ext)
	if base == "" {
		return filename, ""
	}
	return base, ext
}

// NextAvailableName returns filename if it is free in dir, otherwise base_1.ext,
// base_2.ext, and so on. The incoming filename is the base: if photo_1.jpg is
// taken, the next name is photo_1_1.jpg, not photo_2.jpg.
func NextAvailableName(dir, filename string) (string, error) {
	exists, _, err := lstatExists(filepath.Join(dir, filename))
	if err != nil {
		return "", err
	}
	if !exists {
		return filename, nil
	}
	base, ext := SplitFileName(filename)
	for n := 1; n < 10000; n++ {
		cand := fmt.Sprintf("%s_%d%s", base, n, ext)
		exists, _, err = lstatExists(filepath.Join(dir, cand))
		if err != nil {
			return "", err
		}
		if !exists {
			return cand, nil
		}
	}
	return "", fmt.Errorf("could not find a free filename")
}

func SaveUpload(root, destDirRel, filename string, r io.Reader, action ConflictAction) (SaveUploadResult, error) {
	filename = filepath.Base(filename)
	if filename == "" || filename == "." || filename == ".." {
		return SaveUploadResult{}, fmt.Errorf("invalid filename")
	}
	dir, err := SafeJoin(root, destDirRel)
	if err != nil {
		return SaveUploadResult{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return SaveUploadResult{}, err
	}
	if _, err := SafeJoin(root, joinRel(destDirRel, filename)); err != nil {
		return SaveUploadResult{}, err
	}

	chosen := filename
	overwritten := false
	renamed := false

	exists, isDir, err := lstatExists(filepath.Join(dir, filename))
	if err != nil {
		return SaveUploadResult{}, err
	}
	if exists {
		switch action {
		case ConflictSkip:
			return SaveUploadResult{Rel: joinRel(destDirRel, filename), Name: filename, Skipped: true}, nil
		case ConflictOverwrite:
			if isDir {
				return SaveUploadResult{}, ErrIsDirectory
			}
			overwritten = true
		case ConflictRename:
			chosen, err = NextAvailableName(dir, filename)
			if err != nil {
				return SaveUploadResult{}, err
			}
			renamed = chosen != filename
		default:
			return SaveUploadResult{}, ErrExists
		}
	}

	// Retry rename on a race where the chosen name appears between the check and create.
	for attempt := 0; attempt < 10000; attempt++ {
		if _, err := SafeJoin(root, joinRel(destDirRel, chosen)); err != nil {
			return SaveUploadResult{}, err
		}
		dest := filepath.Join(dir, chosen)
		flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		if !overwritten {
			flags = os.O_CREATE | os.O_WRONLY | os.O_EXCL
		}
		f, err := os.OpenFile(dest, flags, 0o644)
		if err != nil {
			if !overwritten && os.IsExist(err) {
				if action == ConflictRename {
					next, nerr := NextAvailableName(dir, filename)
					if nerr != nil {
						return SaveUploadResult{}, nerr
					}
					chosen = next
					renamed = chosen != filename
					continue
				}
				return SaveUploadResult{}, ErrExists
			}
			return SaveUploadResult{}, err
		}
		_, copyErr := io.Copy(f, r)
		closeErr := f.Close()
		if copyErr != nil {
			if !overwritten {
				_ = os.Remove(dest)
			}
			return SaveUploadResult{}, copyErr
		}
		if closeErr != nil {
			if !overwritten {
				_ = os.Remove(dest)
			}
			return SaveUploadResult{}, closeErr
		}
		return SaveUploadResult{
			Rel:         joinRel(destDirRel, chosen),
			Name:        chosen,
			Overwritten: overwritten,
			Renamed:     renamed,
		}, nil
	}
	return SaveUploadResult{}, fmt.Errorf("could not find a free filename")
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
			if IsInternalDirName(d.Name()) {
				return filepath.SkipDir
			}
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

// CopyEntry copies a file or directory from srcRoot/srcRel to dstRoot/dstNameRel
// (dstNameRel is the full destination path including the final name).
func CopyEntry(srcRoot, srcRel, dstRoot, dstNameRel string) error {
	src, err := SafeJoin(srcRoot, srcRel)
	if err != nil {
		return err
	}
	dst, err := SafeJoin(dstRoot, dstNameRel)
	if err != nil {
		return err
	}
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	// Prevent copying a directory into itself or a subdirectory.
	if st.IsDir() {
		absSrc, _ := filepath.Abs(src)
		absDst, _ := filepath.Abs(dst)
		if absDst == absSrc || strings.HasPrefix(absDst+string(os.PathSeparator), absSrc+string(os.PathSeparator)) {
			return fmt.Errorf("cannot copy a folder into itself")
		}
	}
	if st.IsDir() {
		return copyDir(src, dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return copyFile(src, dst, st.Mode())
}

// MoveEntry moves src to dst (full dest path including name). Same-root uses rename;
// cross-root copies then deletes.
func MoveEntry(srcRoot, srcRel, dstRoot, dstNameRel string) error {
	src, err := SafeJoin(srcRoot, srcRel)
	if err != nil {
		return err
	}
	dst, err := SafeJoin(dstRoot, dstNameRel)
	if err != nil {
		return err
	}
	if src == dst {
		return nil
	}
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if st.IsDir() {
		absSrc, _ := filepath.Abs(src)
		absDst, _ := filepath.Abs(dst)
		if absDst == absSrc || strings.HasPrefix(absDst+string(os.PathSeparator), absSrc+string(os.PathSeparator)) {
			return fmt.Errorf("cannot move a folder into itself")
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// Cross-device fallback
	if err := CopyEntry(srcRoot, srcRel, dstRoot, dstNameRel); err != nil {
		return err
	}
	return Delete(srcRoot, srcRel)
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		info, err := e.Info()
		if err != nil {
			return err
		}
		if e.IsDir() {
			if err := copyDir(s, d); err != nil {
				return err
			}
			continue
		}
		if e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		if err := copyFile(s, d, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

// EntrySize returns size of a file, or recursive dir size for quota checks.
func EntrySize(root, rel string) (int64, error) {
	return DirSize(root, rel)
}
