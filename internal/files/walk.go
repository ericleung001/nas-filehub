package files

import (
	"os"
	"path/filepath"
	"strings"
)

// TrashDirName is the recycle-bin folder under DATA_ROOT.
const TrashDirName = ".filehub-trash"

// UploadsDirName holds in-progress chunked uploads under DATA_ROOT.
const UploadsDirName = ".filehub-uploads"

// IsInternalDirName reports whether a directory should be hidden from browsing,
// search, zip, and size walks.
func IsInternalDirName(name string) bool {
	return name == TrashDirName || name == UploadsDirName
}

func IsTrashPath(rel string) bool {
	rel = filepath.ToSlash(rel)
	rel = strings.TrimPrefix(rel, "/")
	if rel == TrashDirName {
		return true
	}
	return strings.HasPrefix(rel, TrashDirName+"/")
}

func IsInternalPath(rel string) bool {
	rel = filepath.ToSlash(rel)
	rel = strings.TrimPrefix(rel, "/")
	for _, name := range []string{TrashDirName, UploadsDirName} {
		if rel == name || strings.HasPrefix(rel, name+"/") {
			return true
		}
	}
	return false
}

type FileSize struct {
	Rel  string
	Size int64
}

// ListFileSizes walks a file or directory and returns every regular file with size.
func ListFileSizes(root, rel string) ([]FileSize, error) {
	full, err := SafeJoin(root, rel)
	if err != nil {
		return nil, err
	}
	st, err := os.Lstat(full)
	if err != nil {
		return nil, err
	}
	var out []FileSize
	if !st.IsDir() {
		if st.Mode().IsRegular() {
			out = append(out, FileSize{Rel: filepath.ToSlash(rel), Size: st.Size()})
		}
		return out, nil
	}
	base := filepath.ToSlash(strings.TrimPrefix(rel, "/"))
	err = filepath.Walk(full, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if IsInternalDirName(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relPath, rerr := filepath.Rel(full, path)
		if rerr != nil {
			return nil
		}
		p := filepath.ToSlash(relPath)
		if base != "" && base != "." {
			p = base + "/" + p
		}
		out = append(out, FileSize{Rel: p, Size: info.Size()})
		return nil
	})
	return out, err
}
