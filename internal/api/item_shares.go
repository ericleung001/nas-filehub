package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/eric/nas-filehub/internal/db"
	"github.com/eric/nas-filehub/internal/files"
)

func (s *Server) handleDirectory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	u := s.currentUser(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	all, err := s.DB.ListDirectoryUsers(u.ID)
	if err != nil {
		writeErr(w, 500, "db error")
		return
	}
	list := all
	if q != "" {
		list, err = s.DB.SearchDirectoryUsers(u.ID, q)
		if err != nil {
			writeErr(w, 500, "db error")
			return
		}
	}
	stats, err := s.DB.ListRecipientGrantStats(u.ID)
	if err != nil {
		writeErr(w, 500, "db error")
		return
	}
	frequent := db.PickFrequentRecipientIDs(all, stats, db.DefaultFrequentRecipients)
	writeJSON(w, 200, map[string]any{"users": list, "frequentUserIds": frequent})
}

func (s *Server) handleItemShares(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	switch r.Method {
	case http.MethodGet:
		rootID := r.URL.Query().Get("root")
		path := strings.TrimPrefix(strings.TrimSpace(r.URL.Query().Get("path")), "/")
		if rootID != "" && path != "" {
			if !s.canShareFrom(u, rootID, path) {
				writeErr(w, 403, "forbidden")
				return
			}
			list, err := s.DB.ListItemSharesForPath(u.ID, rootID, path)
			if err != nil {
				writeErr(w, 500, "db error")
				return
			}
			writeJSON(w, 200, map[string]any{"shares": list})
			return
		}
		incoming, err := s.DB.ListIncomingItemShares(u.ID)
		if err != nil {
			writeErr(w, 500, "db error")
			return
		}
		writeJSON(w, 200, map[string]any{"incoming": incoming})
	case http.MethodPost:
		var body struct {
			Root    string  `json:"root"`
			Path    string  `json:"path"`
			UserIDs []int64 `json:"userIds"`
			Perm    string  `json:"perm"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		body.Path = strings.TrimPrefix(strings.TrimSpace(body.Path), "/")
		if body.Path == "" {
			writeErr(w, 400, "path required")
			return
		}
		if len(body.UserIDs) == 0 {
			writeErr(w, 400, "at least one user is required")
			return
		}
		rootID := body.Root
		if rootID == "" {
			rootID = "home"
		}
		if !s.canShareFrom(u, rootID, body.Path) {
			writeErr(w, 403, "forbidden")
			return
		}
		sc, err := s.resolveScope(u, rootID, body.Path)
		if err != nil {
			writeErr(w, 403, "forbidden")
			return
		}
		full, err := files.SafeJoin(sc.Root, sc.Rel)
		if err != nil {
			writeErr(w, 400, "bad path")
			return
		}
		st, err := os.Stat(full)
		if err != nil {
			writeErr(w, 404, "not found")
			return
		}
		perm := strings.ToLower(strings.TrimSpace(body.Perm))
		if perm == "" {
			perm = "read"
		}
		if !st.IsDir() {
			perm = "read"
		}
		label := filepath.Base(body.Path)
		out := make([]db.ItemShare, 0, len(body.UserIDs))
		seen := map[int64]bool{}
		for _, uid := range body.UserIDs {
			if uid <= 0 || uid == u.ID || seen[uid] {
				continue
			}
			seen[uid] = true
			target, err := s.DB.GetUserByID(uid)
			if err != nil || target == nil || target.Disabled || !userActive(target) {
				writeErr(w, 400, "user not found")
				return
			}
			sh, err := s.DB.UpsertItemShare(u.ID, uid, rootID, body.Path, st.IsDir(), label, perm)
			if err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			if sh == nil {
				writeErr(w, 500, "db error")
				return
			}
			out = append(out, *sh)
			s.audit(r, u.ID, u.Username, "item_share_grant", "id="+itoaLocal(sh.ID)+" user="+itoaLocal(uid)+" path="+body.Path+" perm="+perm)
			s.notifyItemShared(r, u, target, label, st.IsDir())
		}
		if len(out) == 0 {
			writeErr(w, 400, "at least one valid user is required")
			return
		}
		writeJSON(w, 200, map[string]any{"shares": out})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (s *Server) handleItemShareID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeErr(w, 405, "method not allowed")
		return
	}
	u := s.currentUser(r)
	rest := strings.TrimPrefix(r.URL.Path, "/api/item-shares/")
	id, err := strconv.ParseInt(strings.Trim(rest, "/"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "bad id")
		return
	}
	sh, err := s.DB.GetItemShare(id)
	if err != nil || sh == nil {
		writeErr(w, 404, "not found")
		return
	}
	if !u.IsAdmin && sh.OwnerID != u.ID {
		writeErr(w, 403, "forbidden")
		return
	}
	if err := s.DB.DeleteItemShare(id); err != nil {
		writeErr(w, 500, "db error")
		return
	}
	s.audit(r, u.ID, u.Username, "item_share_revoke", "id="+itoaLocal(id)+" user="+itoaLocal(sh.UserID)+" path="+sh.Path)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) canShareFrom(u *db.User, rootID, path string) bool {
	if u == nil {
		return false
	}
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		rootID = "home"
	}
	if rootID == "trash" || rootID == "incoming" || strings.HasPrefix(rootID, "incoming:") {
		return false
	}
	sc, err := s.resolveScope(u, rootID, path)
	if err != nil || sc == nil || sc.Kind == "trash" {
		return false
	}
	return true
}

func splitIncomingPath(rel string) (id int64, rest string, ok bool) {
	rel = strings.TrimPrefix(strings.TrimSpace(rel), "/")
	if rel == "" {
		return 0, "", false
	}
	first, rem, _ := strings.Cut(rel, "/")
	id, err := strconv.ParseInt(first, 10, 64)
	if err != nil || id <= 0 {
		return 0, "", false
	}
	return id, rem, true
}

func (s *Server) listIncomingEntries(u *db.User) ([]map[string]any, error) {
	shares, err := s.DB.ListIncomingItemShares(u.ID)
	if err != nil {
		return nil, err
	}
	used := map[string]int{}
	out := make([]map[string]any, 0, len(shares))
	for _, sh := range shares {
		owner, err := s.DB.GetUserByID(sh.OwnerID)
		if err != nil || owner == nil || owner.Disabled || !userActive(owner) {
			continue
		}
		sc, err := s.resolveScope(owner, sh.RootID, sh.Path)
		if err != nil {
			continue
		}
		full, err := files.SafeJoin(sc.Root, sc.Rel)
		if err != nil {
			continue
		}
		st, err := os.Stat(full)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(sh.Label)
		if name == "" {
			name = filepath.Base(sh.Path)
		}
		key := strings.ToLower(name)
		if used[key] > 0 {
			name = name + " (" + sh.OwnerName + ")"
		}
		used[key]++
		out = append(out, map[string]any{
			"name":     name,
			"path":     itoaLocal(sh.ID),
			"isDir":    st.IsDir(),
			"size":     st.Size(),
			"modTime":  st.ModTime().UTC(),
			"ext":      strings.ToLower(filepath.Ext(name)),
			"kind":     files.KindOf(name, st.IsDir()),
			"sharedBy": sh.OwnerName,
			"perm":     sh.Perm,
		})
	}
	return out, nil
}

func incomingClientPath(rel string) string {
	return strings.Trim(filepath.ToSlash(rel), "/")
}

// rewriteIncomingListEntries maps owner-relative list paths onto the
// recipient's incoming path (shareID or shareID/child/...), which is what
// the UI and later /api/list requests use. files.List otherwise returns
// paths under the owner's root (e.g. "temp/chi"), and opening that as
// root=incoming fails because the first segment is not a share id.
func rewriteIncomingListEntries(clientPath string, entries []files.Entry) []files.Entry {
	base := incomingClientPath(clientPath)
	out := make([]files.Entry, 0, len(entries))
	for _, e := range entries {
		name := filepath.Base(e.Name)
		if name == "" || name == "." || name == ".." {
			continue
		}
		if base == "" {
			e.Path = name
		} else {
			e.Path = base + "/" + name
		}
		out = append(out, e)
	}
	return out
}

func incomingRootID(sh *db.ItemShare) string {
	if sh == nil {
		return "home"
	}
	if sh.RootID == "" {
		return "home"
	}
	return sh.RootID
}
