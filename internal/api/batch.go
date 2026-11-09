package api

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/eric/nas-filehub/internal/files"
)

type batchBody struct {
	Root     string   `json:"root"`
	Paths    []string `json:"paths"`
	DestRoot string   `json:"destRoot"`
	DestPath string   `json:"destPath"`
}

func normalizeBatchPaths(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		p = strings.TrimPrefix(strings.TrimSpace(p), "/")
		if p == "" || p == "." {
			continue
		}
		if strings.Contains(p, "..") {
			return nil, errBadPath
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, errNoPaths
	}
	return out, nil
}

var (
	errBadPath = errStr("invalid path")
	errNoPaths = errStr("paths required")
)

type errStr string

func (e errStr) Error() string { return string(e) }

func joinDest(destDir, name string) string {
	name = filepath.Base(name)
	destDir = strings.TrimPrefix(strings.TrimSpace(destDir), "/")
	if destDir == "" || destDir == "." {
		return name
	}
	return filepath.ToSlash(filepath.Join(destDir, name))
}

func (s *Server) handleBatchDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	u := s.currentUser(r)
	var body batchBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	paths, err := normalizeBatchPaths(body.Paths)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	sc, err := s.resolveScope(u, body.Root, "")
	if err != nil || sc.Perm != "write" {
		writeErr(w, 403, "forbidden")
		return
	}
	var failed []map[string]string
	ok := 0
	for _, p := range paths {
		if _, err := s.resolveScope(u, body.Root, p); err != nil {
			failed = append(failed, map[string]string{"path": p, "error": "forbidden"})
			continue
		}
		if err := files.Delete(sc.Root, p); err != nil {
			failed = append(failed, map[string]string{"path": p, "error": err.Error()})
			continue
		}
		rootID := body.Root
		if rootID == "" {
			rootID = "home"
		}
		_, _ = s.DB.DeleteLinkSharesForPath(rootID, p)
		ok++
	}
	writeJSON(w, 200, map[string]any{"ok": ok, "failed": failed})
}

func (s *Server) handleBatchMove(w http.ResponseWriter, r *http.Request) {
	s.handleBatchTransfer(w, r, true)
}

func (s *Server) handleBatchCopy(w http.ResponseWriter, r *http.Request) {
	s.handleBatchTransfer(w, r, false)
}

func (s *Server) handleBatchTransfer(w http.ResponseWriter, r *http.Request, move bool) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	u := s.currentUser(r)
	var body batchBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	paths, err := normalizeBatchPaths(body.Paths)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	srcScope, err := s.resolveScope(u, body.Root, "")
	if err != nil {
		writeErr(w, 403, "forbidden")
		return
	}
	if move && srcScope.Perm != "write" {
		writeErr(w, 403, "forbidden")
		return
	}
	destRoot := body.DestRoot
	if destRoot == "" {
		destRoot = body.Root
	}
	dstScope, err := s.resolveScope(u, destRoot, body.DestPath)
	if err != nil || dstScope.Perm != "write" {
		writeErr(w, 403, "forbidden")
		return
	}
	destDir := strings.TrimPrefix(body.DestPath, "/")

	var failed []map[string]string
	ok := 0
	for _, p := range paths {
		base := filepath.Base(p)
		destName := joinDest(destDir, base)
		// Skip no-op move into same path
		if move && body.Root == destRoot && p == destName {
			ok++
			continue
		}
		// Prevent moving/copying a folder into its own descendant when same root
		if body.Root == destRoot {
			if destName == p || strings.HasPrefix(destName+"/", p+"/") {
				failed = append(failed, map[string]string{"path": p, "error": "cannot move/copy into itself"})
				continue
			}
		}
		if dstScope.Kind == "home" {
			extra, err := files.EntrySize(srcScope.Root, p)
			if err != nil {
				failed = append(failed, map[string]string{"path": p, "error": err.Error()})
				continue
			}
			// Move within same home does not grow usage.
			if !(move && body.Root == destRoot && srcScope.Kind == "home") {
				if err := s.checkHomeQuota(u, extra); err != nil {
					failed = append(failed, map[string]string{"path": p, "error": err.Error()})
					continue
				}
			}
		}
		var opErr error
		if move {
			opErr = files.MoveEntry(srcScope.Root, p, dstScope.Root, destName)
		} else {
			opErr = files.CopyEntry(srcScope.Root, p, dstScope.Root, destName)
		}
		if opErr != nil {
			failed = append(failed, map[string]string{"path": p, "error": opErr.Error()})
			continue
		}
		ok++
	}
	writeJSON(w, 200, map[string]any{"ok": ok, "failed": failed})
}
