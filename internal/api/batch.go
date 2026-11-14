package api

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/eric/nas-filehub/internal/files"
	"github.com/eric/nas-filehub/internal/trash"
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
	if err != nil || !sc.canDelete() {
		writeErr(w, 403, "forbidden")
		return
	}
	rootID := body.Root
	if rootID == "" {
		rootID = "home"
	}
	var failed []map[string]string
	ok := 0
	for _, pth := range paths {
		itemScope, err := s.resolveScope(u, body.Root, pth)
		if err != nil {
			failed = append(failed, map[string]string{"path": pth, "error": "forbidden"})
			continue
		}
		if sc.Kind == "trash" {
			it, err := trash.Load(s.Cfg.DataRoot, pth)
			if err != nil {
				failed = append(failed, map[string]string{"path": pth, "error": "not found"})
				continue
			}
			if !u.IsAdmin && it.OwnerID != u.ID && it.DeletedBy != u.ID {
				failed = append(failed, map[string]string{"path": pth, "error": "forbidden"})
				continue
			}
			if _, err := trash.Purge(s.Cfg.DataRoot, pth); err != nil {
				failed = append(failed, map[string]string{"path": pth, "error": err.Error()})
				continue
			}
			_ = s.DB.DeleteFileOwnerPrefix(trash.DirName + "/" + pth)
			ok++
			continue
		}
		delRoot := rootID
		if itemScope.OriginRootID != "" {
			delRoot = itemScope.OriginRootID
		}
		if err := s.trashDelete(u, itemScope, itemScope.Rel, delRoot); err != nil {
			failed = append(failed, map[string]string{"path": pth, "error": err.Error()})
			continue
		}
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
	destRoot := body.DestRoot
	if destRoot == "" {
		destRoot = body.Root
	}
	dstScope, err := s.resolveScope(u, destRoot, body.DestPath)
	if err != nil || !dstScope.canWrite() {
		writeErr(w, 403, "forbidden")
		return
	}
	destDir := dstScope.Rel

	var failed []map[string]string
	ok := 0
	for _, p := range paths {
		srcScope, err := s.resolveScope(u, body.Root, p)
		if err != nil {
			failed = append(failed, map[string]string{"path": p, "error": "forbidden"})
			continue
		}
		if move && !srcScope.canDelete() {
			failed = append(failed, map[string]string{"path": p, "error": "forbidden"})
			continue
		}
		base := filepath.Base(srcScope.Rel)
		if base == "" || base == "." {
			base = filepath.Base(p)
		}
		destName := joinDest(destDir, base)
		// Skip no-op move into same path
		if move && srcScope.Root == dstScope.Root && srcScope.Rel == destName {
			ok++
			continue
		}
		// Prevent moving/copying a folder into its own descendant when same filesystem root
		if srcScope.Root == dstScope.Root {
			if destName == srcScope.Rel || strings.HasPrefix(destName+"/", srcScope.Rel+"/") {
				failed = append(failed, map[string]string{"path": p, "error": "cannot move/copy into itself"})
				continue
			}
		}
		if dstScope.Kind == "trash" || srcScope.Kind == "trash" {
			failed = append(failed, map[string]string{"path": p, "error": "cannot move or copy recycle bin items here"})
			continue
		}
		sameHomeMove := move && srcScope.Kind == "home" && dstScope.Kind == "home" && srcScope.Root == dstScope.Root
		shareToShareMove := move && srcScope.Kind == "share" && dstScope.Kind == "share"
		homeToShareMove := move && srcScope.Kind == "home" && dstScope.Kind == "share"
		if !sameHomeMove && !shareToShareMove && !homeToShareMove {
			extra, err := files.EntrySize(srcScope.Root, srcScope.Rel)
			if err != nil {
				failed = append(failed, map[string]string{"path": p, "error": err.Error()})
				continue
			}
			if err := s.checkScopeQuota(u, dstScope, extra); err != nil {
				failed = append(failed, map[string]string{"path": p, "error": err.Error()})
				continue
			}
		}
		var opErr error
		if move {
			opErr = files.MoveEntry(srcScope.Root, srcScope.Rel, dstScope.Root, destName)
		} else {
			opErr = files.CopyEntry(srcScope.Root, srcScope.Rel, dstScope.Root, destName)
		}
		if opErr != nil {
			failed = append(failed, map[string]string{"path": p, "error": opErr.Error()})
			continue
		}
		s.afterTransfer(u, srcScope, dstScope, srcScope.Rel, destName, move)
		ok++
	}
	writeJSON(w, 200, map[string]any{"ok": ok, "failed": failed})
}
