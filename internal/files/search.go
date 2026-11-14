package files

import (
	"os"
	"path/filepath"
	"strings"
)

type SearchHit struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
	Kind  string `json:"kind"`
}

// SearchNames walks root/rel and returns filename matches (case-insensitive).
// Internal directories (recycle bin, upload staging) are skipped. max limits the result count.
func SearchNames(root, rel, query string, max int) ([]SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if max <= 0 {
		max = 200
	}
	full, err := SafeJoin(root, rel)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	var hits []SearchHit
	err = filepath.Walk(full, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && IsInternalDirName(info.Name()) {
			return filepath.SkipDir
		}
		if path == full {
			return nil
		}
		if len(hits) >= max {
			return filepath.SkipAll
		}
		name := info.Name()
		if !strings.Contains(strings.ToLower(name), q) {
			return nil
		}
		relPath, rerr := filepath.Rel(full, path)
		if rerr != nil {
			return nil
		}
		p := filepath.ToSlash(relPath)
		base := filepath.ToSlash(strings.TrimPrefix(rel, "/"))
		if base != "" && base != "." {
			p = base + "/" + p
		}
		hits = append(hits, SearchHit{
			Name: name, Path: p, IsDir: info.IsDir(), Size: info.Size(),
			Kind: KindOf(name, info.IsDir()),
		})
		return nil
	})
	if err != nil {
		return hits, err
	}
	return hits, nil
}
