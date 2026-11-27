package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/eric/nas-filehub/internal/db"
	"github.com/eric/nas-filehub/internal/files"
)

var errAlbumCover = errors.New("cover must be a photo or video in this album")

type albumView struct {
	Root      string `json:"root"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	CoverPath string `json:"coverPath,omitempty"`
}

func (sc *scope) albumOwnerID() int64 {
	if sc == nil {
		return 0
	}
	if sc.Kind == "home" {
		return sc.HomeOwnerID
	}
	return 0
}

func (sc *scope) albumKey() (ownerID int64, rootID, path string, ok bool) {
	if sc == nil || sc.Kind == "trash" || sc.Kind == "incoming" {
		return 0, "", "", false
	}
	rootID = sc.shareRootID()
	if rootID == "" || rootID == "trash" || rootID == "incoming" {
		return 0, "", "", false
	}
	if sc.Kind == "home" && sc.HomeOwnerID == 0 {
		return 0, "", "", false
	}
	return sc.albumOwnerID(), rootID, db.NormalizeAlbumPath(sc.Rel), true
}

func (s *Server) folderAlbumFlags(sc *scope) (isAlbum, inAlbum bool) {
	isAlbum, inAlbum, _, _ = s.folderAlbumInfo(sc)
	return isAlbum, inAlbum
}

func (s *Server) folderAlbumInfo(sc *scope) (isAlbum, inAlbum bool, albumPath, preferredCover string) {
	ownerID, rootID, path, ok := sc.albumKey()
	if !ok {
		return false, false, "", ""
	}
	if a, err := s.DB.GetAlbum(ownerID, rootID, path); err == nil && a != nil {
		return true, true, path, a.CoverPath
	}
	p := path
	for p != "" {
		if i := strings.LastIndex(p, "/"); i >= 0 {
			p = p[:i]
		} else {
			p = ""
		}
		if a, err := s.DB.GetAlbum(ownerID, rootID, p); err == nil && a != nil {
			return false, true, p, a.CoverPath
		}
	}
	return false, false, "", ""
}

func (s *Server) handleAlbums(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleAlbumsList(w, r)
	case http.MethodPost:
		s.handleAlbumSet(w, r)
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (s *Server) handleAlbumsList(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	list, err := s.listVisibleAlbums(u)
	if err != nil {
		writeErr(w, 500, "db error")
		return
	}
	writeJSON(w, 200, map[string]any{"albums": list})
}

func (s *Server) handleAlbumSet(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	var body struct {
		Root  string  `json:"root"`
		Path  string  `json:"path"`
		Album *bool   `json:"album"`
		Cover *string `json:"cover"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.Album == nil && body.Cover == nil {
		writeErr(w, 400, "album flag or cover required")
		return
	}
	if body.Root == "trash" || strings.HasPrefix(body.Root, "trash") {
		writeErr(w, 403, "cannot mark the Recycle bin as an album")
		return
	}
	if body.Root == "incoming" && strings.Trim(body.Path, "/") == "" {
		writeErr(w, 403, "forbidden")
		return
	}
	sc, err := s.resolveScope(u, body.Root, body.Path)
	if err != nil {
		writeErr(w, 403, "forbidden")
		return
	}
	if sc.Kind == "trash" {
		writeErr(w, 403, "cannot mark the Recycle bin as an album")
		return
	}
	if sc.Kind == "incoming" {
		writeErr(w, 403, "forbidden")
		return
	}
	if !sc.canWrite() {
		writeErr(w, 403, "forbidden")
		return
	}
	ownerID, rootID, path, ok := sc.albumKey()
	if !ok {
		writeErr(w, 403, "forbidden")
		return
	}
	full, err := files.SafeJoin(sc.Root, sc.Rel)
	if err != nil {
		writeErr(w, 400, "bad path")
		return
	}
	st, err := os.Stat(full)
	if err != nil || !st.IsDir() {
		writeErr(w, 400, "not a folder")
		return
	}
	albumOn := false
	if a, err := s.DB.GetAlbum(ownerID, rootID, path); err == nil && a != nil {
		albumOn = true
	}
	if body.Album != nil {
		if *body.Album {
			if err := s.DB.SetAlbum(ownerID, rootID, path); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			albumOn = true
		} else {
			if err := s.DB.DeleteAlbum(ownerID, rootID, path); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			albumOn = false
		}
	}
	coverPath := ""
	manual := false
	if body.Cover != nil {
		if !albumOn {
			writeErr(w, 400, "folder is not an album")
			return
		}
		cover, err := db.NormalizeAlbumCover(*body.Cover)
		if err != nil {
			writeErr(w, 400, "invalid cover path")
			return
		}
		if cover != "" {
			if err := s.validateAlbumCoverFile(sc, cover); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
		}
		if err := s.DB.SetAlbumCover(ownerID, rootID, path, cover); err != nil {
			if err == db.ErrAlbumNotFound {
				writeErr(w, 400, "folder is not an album")
				return
			}
			writeErr(w, 400, err.Error())
			return
		}
		coverPath = files.AlbumCoverRel(sc.Root, sc.Rel, cover)
		manual = cover != "" && coverPath == cover
	} else if albumOn {
		a, _ := s.DB.GetAlbum(ownerID, rootID, path)
		pref := ""
		if a != nil {
			pref = a.CoverPath
		}
		coverPath = files.AlbumCoverRel(sc.Root, sc.Rel, pref)
		manual = pref != "" && coverPath == pref
	}
	out := map[string]any{"ok": "1", "album": albumOn, "root": body.Root, "path": db.NormalizeAlbumPath(body.Path)}
	if coverPath != "" {
		out["coverPath"] = clientJoin(db.NormalizeAlbumPath(body.Path), coverPath)
	}
	if manual {
		out["manualCover"] = true
	}
	writeJSON(w, 200, out)
}

func (s *Server) validateAlbumCoverFile(sc *scope, cover string) error {
	joined := cover
	if sc.Rel != "" && sc.Rel != "." {
		joined = filepath.ToSlash(filepath.Join(sc.Rel, cover))
	}
	full, err := files.SafeJoin(sc.Root, joined)
	if err != nil {
		return errAlbumCover
	}
	st, err := os.Lstat(full)
	if err != nil || st.IsDir() {
		return errAlbumCover
	}
	k := files.KindOf(filepath.Base(full), false)
	if k != "image" && k != "video" {
		return errAlbumCover
	}
	return nil
}

func (s *Server) listVisibleAlbums(u *db.User) ([]albumView, error) {
	all, err := s.DB.ListAlbums()
	if err != nil {
		return nil, err
	}
	incoming, err := s.DB.ListIncomingItemShares(u.ID)
	if err != nil {
		return nil, err
	}
	shares, err := s.DB.SharesForUser(u.ID, u.IsAdmin)
	if err != nil {
		return nil, err
	}
	shareOK := map[string]string{} // root id -> display name
	for _, sa := range shares {
		id := "share:" + itoaLocal(sa.Share.ID)
		shareOK[id] = sa.Share.Name
	}

	out := make([]albumView, 0)
	for _, a := range all {
		if s.maybeDeleteStaleAlbum(u, a) {
			continue
		}
		views := s.clientViewsForAlbum(u, a, incoming, shareOK)
		for _, v := range views {
			if s.albumFolderExists(u, v.Root, v.Path) {
				v.CoverPath = s.albumCoverClientPath(u, v.Root, v.Path)
				out = append(out, v)
			}
		}
	}
	return out, nil
}

// maybeDeleteStaleAlbum removes an album row when this user can inspect the
// album's canonical storage root, that root is still accessible, and the
// folder path is confirmed missing on disk. If the root or share is
// inaccessible or unmounted, the row is left alone.
func (s *Server) maybeDeleteStaleAlbum(u *db.User, a db.Album) bool {
	sc, err := s.canonicalAlbumScope(u, a)
	if err != nil || sc == nil {
		return false
	}
	st, err := os.Stat(sc.Root)
	if err != nil || !st.IsDir() {
		return false
	}
	full, err := files.SafeJoin(sc.Root, sc.Rel)
	if err != nil {
		return false
	}
	st, err = os.Lstat(full)
	if err == nil {
		if st.IsDir() {
			return false
		}
		// Path exists but is no longer a folder.
		_ = s.DB.DeleteAlbum(a.OwnerID, a.RootID, a.Path)
		return true
	}
	if os.IsNotExist(err) {
		_ = s.DB.DeleteAlbum(a.OwnerID, a.RootID, a.Path)
		return true
	}
	return false
}

func (s *Server) canonicalAlbumScope(u *db.User, a db.Album) (*scope, error) {
	if u == nil {
		return nil, nil
	}
	if a.RootID == "home" {
		if a.OwnerID != u.ID {
			return nil, nil
		}
		return s.resolveScope(u, "home", a.Path)
	}
	if strings.HasPrefix(a.RootID, "share:") {
		return s.resolveScope(u, a.RootID, a.Path)
	}
	return nil, nil
}

func (s *Server) clientViewsForAlbum(u *db.User, a db.Album, incoming []db.ItemShare, shareOK map[string]string) []albumView {
	var out []albumView
	if a.RootID == "home" {
		if a.OwnerID == u.ID {
			name := albumDisplayName(a.Path, "My Files")
			out = append(out, albumView{Root: "home", Path: a.Path, Name: name})
		}
		for _, sh := range incoming {
			if !sh.IsDir || sh.OwnerID != a.OwnerID {
				continue
			}
			root := incomingRootID(&sh)
			if root != "home" {
				continue
			}
			if !albumPathCoveredByShare(a.Path, sh.Path) {
				continue
			}
			client := incomingAlbumClientPath(sh.ID, sh.Path, a.Path)
			name := albumDisplayName(a.Path, sh.Label)
			out = append(out, albumView{Root: "incoming", Path: client, Name: name})
		}
		return out
	}
	if strings.HasPrefix(a.RootID, "share:") {
		if name, ok := shareOK[a.RootID]; ok {
			out = append(out, albumView{Root: a.RootID, Path: a.Path, Name: albumDisplayName(a.Path, name)})
			return out
		}
		for _, sh := range incoming {
			if !sh.IsDir || incomingRootID(&sh) != a.RootID {
				continue
			}
			if !albumPathCoveredByShare(a.Path, sh.Path) {
				continue
			}
			client := incomingAlbumClientPath(sh.ID, sh.Path, a.Path)
			name := albumDisplayName(a.Path, sh.Label)
			out = append(out, albumView{Root: "incoming", Path: client, Name: name})
		}
	}
	return out
}

func albumPathCoveredByShare(albumPath, sharePath string) bool {
	albumPath = db.NormalizeAlbumPath(albumPath)
	sharePath = db.NormalizeAlbumPath(sharePath)
	if sharePath == "" {
		return true
	}
	if albumPath == sharePath {
		return true
	}
	return strings.HasPrefix(albumPath, sharePath+"/")
}

func incomingAlbumClientPath(shareID int64, sharePath, albumPath string) string {
	sharePath = db.NormalizeAlbumPath(sharePath)
	albumPath = db.NormalizeAlbumPath(albumPath)
	id := itoaLocal(shareID)
	if albumPath == sharePath {
		return id
	}
	rest := strings.TrimPrefix(albumPath, sharePath+"/")
	if rest == albumPath {
		// not actually under the share (shouldn't happen)
		return id
	}
	if rest == "" {
		return id
	}
	return id + "/" + rest
}

func albumDisplayName(path, fallback string) string {
	path = db.NormalizeAlbumPath(path)
	if path == "" {
		if fallback != "" {
			return fallback
		}
		return "Album"
	}
	return filepath.Base(path)
}

func (s *Server) albumFolderExists(u *db.User, rootID, path string) bool {
	sc, err := s.resolveScope(u, rootID, path)
	if err != nil || sc == nil || sc.Kind == "trash" || sc.Kind == "incoming" {
		return false
	}
	full, err := files.SafeJoin(sc.Root, sc.Rel)
	if err != nil {
		return false
	}
	st, err := os.Stat(full)
	return err == nil && st.IsDir()
}

func (s *Server) albumCoverClientPath(u *db.User, rootID, path string) string {
	sc, err := s.resolveScope(u, rootID, path)
	if err != nil || sc == nil {
		return ""
	}
	pref := ""
	if ownerID, keyRoot, keyPath, ok := sc.albumKey(); ok {
		if a, err := s.DB.GetAlbum(ownerID, keyRoot, keyPath); err == nil && a != nil {
			pref = a.CoverPath
		}
	}
	rel := files.AlbumCoverRel(sc.Root, sc.Rel, pref)
	if rel == "" {
		return ""
	}
	return clientJoin(db.NormalizeAlbumPath(path), rel)
}

func clientJoin(base, rel string) string {
	base = db.NormalizeAlbumPath(base)
	rel = strings.Trim(strings.ReplaceAll(rel, "\\", "/"), "/")
	if base == "" {
		return rel
	}
	if rel == "" {
		return base
	}
	return base + "/" + rel
}

func clientAlbumPath(currentOwner, currentClient, albumOwner string) string {
	currentOwner = db.NormalizeAlbumPath(currentOwner)
	currentClient = db.NormalizeAlbumPath(currentClient)
	albumOwner = db.NormalizeAlbumPath(albumOwner)
	if currentOwner == albumOwner {
		return currentClient
	}
	if albumOwner != "" && strings.HasPrefix(currentOwner, albumOwner+"/") {
		rest := strings.TrimPrefix(currentOwner, albumOwner+"/")
		if strings.HasSuffix(currentClient, "/"+rest) {
			return strings.TrimSuffix(currentClient, "/"+rest)
		}
		if currentClient == rest {
			return ""
		}
	}
	return currentClient
}

func (s *Server) markDirAlbums(sc *scope, entries []files.Entry, attachCover bool) []files.Entry {
	ownerID, rootID, _, ok := sc.albumKey()
	if !ok {
		return entries
	}
	parent := db.NormalizeAlbumPath(sc.Rel)
	for i := range entries {
		if !entries[i].IsDir {
			continue
		}
		childOwnerPath := entries[i].Name
		if parent != "" {
			childOwnerPath = parent + "/" + entries[i].Name
		}
		childOwnerPath = db.NormalizeAlbumPath(childOwnerPath)
		if yes, err := s.DB.IsAlbum(ownerID, rootID, childOwnerPath); err == nil && yes {
			entries[i].Album = true
		}
		if attachCover {
			pref := ""
			if a, err := s.DB.GetAlbum(ownerID, rootID, childOwnerPath); err == nil && a != nil {
				pref = a.CoverPath
			}
			cover := files.AlbumCoverRel(sc.Root, childOwnerPath, pref)
			if cover != "" {
				base := db.NormalizeAlbumPath(entries[i].Path)
				if base == "" {
					entries[i].CoverPath = cover
				} else {
					entries[i].CoverPath = base + "/" + cover
				}
			}
		}
	}
	return entries
}
