package api

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/eric/nas-filehub/internal/db"
	"github.com/eric/nas-filehub/internal/files"
	"github.com/eric/nas-filehub/internal/trash"
)

func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	switch r.Method {
	case http.MethodGet:
		items, err := trash.List(s.Cfg.DataRoot, u.ID, u.IsAdmin)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"items": items})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (s *Server) handleTrashRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	u := s.currentUser(r)
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || strings.TrimSpace(body.ID) == "" {
		writeErr(w, 400, "id required")
		return
	}
	it, err := trash.Load(s.Cfg.DataRoot, body.ID)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	if !u.IsAdmin && it.OwnerID != u.ID && it.DeletedBy != u.ID {
		writeErr(w, 403, "forbidden")
		return
	}
	destRoot, destRel, destScope, err := s.restoreTarget(u, it)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	_, finalRel, err := trash.Restore(s.Cfg.DataRoot, it.ID, destRoot, destRel)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// Ledger: leave home usage to DirSize; keep or move share attribution.
	_ = s.DB.DeleteFileOwnerPrefix(trash.DirName + "/" + it.ID)
	if destScope != nil && destScope.Kind == "share" {
		owner := it.OwnerID
		if owner <= 0 {
			owner = u.ID
		}
		sizes, _ := files.ListFileSizes(destRoot, finalRel)
		for _, f := range sizes {
			_ = s.DB.SetFileOwner(destScope.dataRel(f.Rel), owner, f.Size)
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "path": finalRel, "root": it.OriginRoot})
}

func (s *Server) restoreTarget(u *db.User, it trash.Item) (destRoot, destRel string, sc *scope, err error) {
	sc, err = s.resolveScope(u, it.OriginRoot, it.OriginPath)
	if err == nil && sc.Kind != "trash" {
		return sc.Root, it.OriginPath, sc, nil
	}
	// Fall back to the user's home/Restored/<name>
	home, err := files.SafeJoin(s.Cfg.DataRoot, u.HomePath)
	if err != nil {
		return "", "", nil, err
	}
	rel := filepath.ToSlash(filepath.Join("Restored", it.Name))
	sc = &scope{Root: home, Rel: rel, Perm: "write", CanDelete: true, Kind: "home", BaseRel: u.HomePath}
	return home, rel, sc, nil
}

func (s *Server) handleTrashPurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	u := s.currentUser(r)
	var body struct {
		ID    string `json:"id"`
		Empty bool   `json:"empty"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	if body.Empty || strings.EqualFold(r.URL.Query().Get("empty"), "1") {
		items, err := trash.List(s.Cfg.DataRoot, u.ID, false) // never empty everyone else's bin
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		n := 0
		for _, it := range items {
			if _, err := trash.Purge(s.Cfg.DataRoot, it.ID); err == nil {
				_ = s.DB.DeleteFileOwnerPrefix(trash.DirName + "/" + it.ID)
				n++
			}
		}
		writeJSON(w, 200, map[string]any{"ok": true, "purged": n})
		return
	}
	if strings.TrimSpace(body.ID) == "" {
		writeErr(w, 400, "id required")
		return
	}
	it, err := trash.Load(s.Cfg.DataRoot, body.ID)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	if !u.IsAdmin && it.OwnerID != u.ID && it.DeletedBy != u.ID {
		writeErr(w, 403, "forbidden")
		return
	}
	if _, err := trash.Purge(s.Cfg.DataRoot, it.ID); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	_ = s.DB.DeleteFileOwnerPrefix(trash.DirName + "/" + it.ID)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) listTrashEntries(u *db.User) ([]map[string]any, error) {
	items, err := trash.List(s.Cfg.DataRoot, u.ID, u.IsAdmin)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]any{
			"name": it.Name, "path": it.ID, "isDir": it.IsDir, "size": it.Size,
			"modTime": it.DeletedAt, "ext": filepath.Ext(it.Name),
			"kind": files.KindOf(it.Name, it.IsDir), "originRoot": it.OriginRoot,
			"originPath": it.OriginPath, "deletedBy": it.DeletedBy, "ownerId": it.OwnerID,
		})
	}
	if out == nil {
		out = []map[string]any{}
	}
	_ = time.Time{}
	return out, nil
}
