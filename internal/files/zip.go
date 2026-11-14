package files

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// MaxZipUncompressed is a safeguard for folder downloads (8 GiB).
	MaxZipUncompressed int64 = 8 << 30
	// MaxZipFiles is a safeguard for folder downloads.
	MaxZipFiles = 20000
	// ZipTimeout is a soft deadline checked between files.
	ZipTimeout = 10 * time.Minute
)

// WriteZip streams a directory or file tree at root/rel into w as a zip archive.
func WriteZip(w io.Writer, root, rel string, started time.Time) (int, error) {
	full, err := SafeJoin(root, rel)
	if err != nil {
		return 0, err
	}
	st, err := os.Lstat(full)
	if err != nil {
		return 0, err
	}
	zw := zip.NewWriter(w)
	defer zw.Close()

	if started.IsZero() {
		started = time.Now()
	}
	var files, bytes int64
	baseName := st.Name()
	if rel == "" || rel == "." {
		baseName = "download"
	}

	add := func(abs, name string, info os.FileInfo) error {
		if time.Since(started) > ZipTimeout {
			return fmt.Errorf("zip timed out")
		}
		if info.IsDir() {
			if !strings.HasSuffix(name, "/") {
				name += "/"
			}
			_, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store, Modified: info.ModTime()})
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		files++
		if files > MaxZipFiles {
			return fmt.Errorf("zip exceeds %d files", MaxZipFiles)
		}
		bytes += info.Size()
		if bytes > MaxZipUncompressed {
			return fmt.Errorf("zip exceeds size limit")
		}
		fh, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		fh.Name = name
		fh.Method = zip.Deflate
		fh.Modified = info.ModTime()
		cw, err := zw.CreateHeader(fh)
		if err != nil {
			return err
		}
		f, err := os.Open(abs)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(cw, f)
		return err
	}

	if !st.IsDir() {
		if err := add(full, baseName, st); err != nil {
			return 0, err
		}
		return int(files), zw.Close()
	}

	err = filepath.Walk(full, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && IsInternalDirName(info.Name()) && path != full {
			return filepath.SkipDir
		}
		relPath, rerr := filepath.Rel(full, path)
		if rerr != nil {
			return nil
		}
		name := baseName
		if relPath != "." {
			name = filepath.ToSlash(filepath.Join(baseName, relPath))
		} else {
			name = baseName + "/"
		}
		return add(path, name, info)
	})
	if err != nil {
		return int(files), err
	}
	return int(files), zw.Close()
}
