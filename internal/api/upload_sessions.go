package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eric/nas-filehub/internal/files"
)

const (
	defaultChunkSize   = 4 << 20
	defaultUploadTTL   = 24 * time.Hour
	uploadCleanupEvery = time.Hour
)

// UploadSession is a resumable upload in progress.
type UploadSession struct {
	ID        string               `json:"id"`
	UserID    int64                `json:"userId"`
	RootID    string               `json:"rootId"`
	DestRel   string               `json:"destRel"`
	OrigName  string               `json:"origName"`
	FinalName string               `json:"finalName"`
	Size      int64                `json:"size"`
	Offset    int64                `json:"offset"`
	Action    files.ConflictAction `json:"action"`
	Overwrite bool                 `json:"overwrite"`
	CreatedAt time.Time            `json:"createdAt"`
	UpdatedAt time.Time            `json:"updatedAt"`
}

// UploadManager stores chunked-upload metadata and temp files under DATA_ROOT.
type UploadManager struct {
	mu       sync.Mutex
	sessions map[string]*UploadSession
	dataRoot string
	ttl      time.Duration
}

func NewUploadManager(dataRoot string, ttl time.Duration) *UploadManager {
	if ttl <= 0 {
		ttl = defaultUploadTTL
	}
	m := &UploadManager{
		sessions: make(map[string]*UploadSession),
		dataRoot: dataRoot,
		ttl:      ttl,
	}
	m.loadFromDisk()
	return m
}

func (m *UploadManager) dir() string {
	return filepath.Join(m.dataRoot, files.UploadsDirName)
}

func (m *UploadManager) sessionDir(id string) string {
	return filepath.Join(m.dir(), id)
}

func (m *UploadManager) metaPath(id string) string {
	return filepath.Join(m.sessionDir(id), "meta.json")
}

func (m *UploadManager) dataPath(id string) string {
	return filepath.Join(m.sessionDir(id), "data")
}

func (m *UploadManager) loadFromDisk() {
	ents, err := os.ReadDir(m.dir())
	if err != nil {
		return
	}
	now := time.Now()
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		b, err := os.ReadFile(m.metaPath(id))
		if err != nil {
			continue
		}
		var sess UploadSession
		if json.Unmarshal(b, &sess) != nil || sess.ID == "" {
			continue
		}
		if now.Sub(sess.UpdatedAt) > m.ttl {
			_ = os.RemoveAll(m.sessionDir(id))
			continue
		}
		if st, err := os.Stat(m.dataPath(id)); err == nil {
			if st.Size() < sess.Offset {
				sess.Offset = st.Size()
			}
		} else {
			sess.Offset = 0
		}
		m.sessions[sess.ID] = &sess
	}
}

func (m *UploadManager) persist(sess *UploadSession) error {
	if err := os.MkdirAll(m.sessionDir(sess.ID), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.metaPath(sess.ID) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.metaPath(sess.ID))
}

func (m *UploadManager) Get(id string) *UploadSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess := m.sessions[id]
	if sess == nil {
		return nil
	}
	cp := *sess
	return &cp
}

func (m *UploadManager) Put(sess *UploadSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *sess
	m.sessions[sess.ID] = &cp
	return m.persist(&cp)
}

func (m *UploadManager) Delete(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
	_ = os.RemoveAll(m.sessionDir(id))
}

func (m *UploadManager) Cleanup() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	n := 0
	for id, sess := range m.sessions {
		if now.Sub(sess.UpdatedAt) > m.ttl {
			delete(m.sessions, id)
			_ = os.RemoveAll(m.sessionDir(id))
			n++
		}
	}
	// Also remove orphan staging dirs without meta or with stale mtime.
	ents, err := os.ReadDir(m.dir())
	if err != nil {
		return n
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		if _, ok := m.sessions[id]; ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > m.ttl {
			_ = os.RemoveAll(m.sessionDir(id))
			n++
		}
	}
	return n
}

func (s *Server) uploads() *UploadManager {
	s.uploadsOnce.Do(func() {
		if s.Uploads == nil {
			s.Uploads = NewUploadManager(s.Cfg.DataRoot, defaultUploadTTL)
		}
	})
	return s.Uploads
}

func (s *Server) handleUploadSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleUploadSessionInit(w, r)
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (s *Server) handleUploadSessionID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/upload/sessions/")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		s.handleUploadSessions(w, r)
		return
	}
	parts := strings.Split(rest, "/")
	id := parts[0]
	if len(parts) == 2 && parts[1] == "complete" {
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		s.handleUploadSessionComplete(w, r, id)
		return
	}
	if len(parts) != 1 {
		writeErr(w, 404, "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleUploadSessionStatus(w, r, id)
	case http.MethodPut:
		s.handleUploadSessionChunk(w, r, id)
	case http.MethodDelete:
		s.handleUploadSessionAbort(w, r, id)
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (s *Server) handleUploadSessionInit(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	var body struct {
		Root     string `json:"root"`
		Path     string `json:"path"`
		Name     string `json:"name"`
		Size     int64  `json:"size"`
		Conflict string `json:"conflict"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	action, err := files.ParseConflictAction(body.Conflict)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeErr(w, 400, "name required")
		return
	}
	if body.Size < 0 {
		writeErr(w, 400, "invalid size")
		return
	}
	max := s.maxUploadBytes()
	if body.Size > max {
		writeErr(w, 400, s.uploadTooLargeMessage())
		return
	}
	sc, err := s.resolveScope(u, body.Root, body.Path)
	if err != nil || !sc.canWrite() || sc.Kind == "trash" {
		writeErr(w, 403, "forbidden")
		return
	}
	filename := filepath.Base(body.Name)
	chosen, overwrite, skipped, err := s.resolveConflict(sc, filename, action)
	if err != nil {
		if errors.Is(err, files.ErrExists) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "file already exists",
				"code":  "exists",
				"name":  filename,
			})
			return
		}
		if errors.Is(err, files.ErrIsDirectory) {
			writeErr(w, 400, err.Error())
			return
		}
		writeErr(w, 400, err.Error())
		return
	}
	rel := joinDest(sc.Rel, chosen)
	if skipped {
		writeJSON(w, 200, map[string]any{"path": joinDest(sc.Rel, filename), "name": filename, "skipped": true})
		return
	}
	if err := s.checkScopeQuota(u, sc, body.Size); err != nil {
		writeErr(w, 403, err.Error())
		return
	}
	id, err := s.newStagingID()
	if err != nil {
		writeErr(w, 500, "could not start upload")
		return
	}
	now := time.Now().UTC()
	rootID := body.Root
	if sc != nil && sc.OriginRootID != "" {
		rootID = sc.OriginRootID
	}
	if rootID == "" {
		rootID = "home"
	}
	sess := &UploadSession{
		ID:        id,
		UserID:    u.ID,
		RootID:    rootID,
		DestRel:   sc.Rel,
		OrigName:  filename,
		FinalName: chosen,
		Size:      body.Size,
		Offset:    0,
		Action:    action,
		Overwrite: overwrite,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := os.MkdirAll(s.uploads().sessionDir(id), 0o700); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// Pre-create the data file so resume can append.
	f, err := os.OpenFile(s.uploads().dataPath(id), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		s.uploads().Delete(id)
		writeErr(w, 500, err.Error())
		return
	}
	_ = f.Close()
	if err := s.uploads().Put(sess); err != nil {
		s.uploads().Delete(id)
		writeErr(w, 500, err.Error())
		return
	}
	out := map[string]any{
		"id":        sess.ID,
		"offset":    sess.Offset,
		"size":      sess.Size,
		"name":      sess.FinalName,
		"chunkSize": defaultChunkSize,
		"path":      rel,
	}
	if sess.Overwrite {
		out["overwritten"] = true
	}
	if sess.FinalName != sess.OrigName {
		out["renamed"] = true
	}
	writeJSON(w, 200, out)
}

func (s *Server) sessionForUser(r *http.Request, id string) (*UploadSession, int, string) {
	u := s.currentUser(r)
	sess := s.uploads().Get(id)
	if sess == nil {
		return nil, 404, "not found"
	}
	if sess.UserID != u.ID {
		return nil, 404, "not found"
	}
	if time.Since(sess.UpdatedAt) > s.uploads().ttl {
		s.uploads().Delete(id)
		return nil, 404, "not found"
	}
	return sess, 0, ""
}

func (s *Server) handleUploadSessionStatus(w http.ResponseWriter, r *http.Request, id string) {
	sess, code, msg := s.sessionForUser(r, id)
	if sess == nil {
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, 200, map[string]any{
		"id":     sess.ID,
		"offset": sess.Offset,
		"size":   sess.Size,
		"name":   sess.FinalName,
		"path":   joinDest(sess.DestRel, sess.FinalName),
	})
}

func (s *Server) handleUploadSessionChunk(w http.ResponseWriter, r *http.Request, id string) {
	sess, code, msg := s.sessionForUser(r, id)
	if sess == nil {
		writeErr(w, code, msg)
		return
	}
	offset := sess.Offset
	if v := r.Header.Get("Upload-Offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeErr(w, 400, "invalid Upload-Offset")
			return
		}
		if n != sess.Offset {
			writeErr(w, http.StatusConflict, fmt.Sprintf("offset mismatch: have %d", sess.Offset))
			return
		}
		offset = n
	}
	if offset != sess.Offset {
		writeErr(w, http.StatusConflict, fmt.Sprintf("offset mismatch: have %d", sess.Offset))
		return
	}
	remaining := sess.Size - sess.Offset
	if remaining < 0 {
		remaining = 0
	}
	// Limit this request to the remaining bytes, plus a tiny slop check.
	r.Body = http.MaxBytesReader(w, r.Body, remaining+1)
	f, err := os.OpenFile(s.uploads().dataPath(id), os.O_WRONLY, 0o600)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if _, err := f.Seek(sess.Offset, io.SeekStart); err != nil {
		_ = f.Close()
		writeErr(w, 500, err.Error())
		return
	}
	n, copyErr := io.Copy(f, r.Body)
	closeErr := f.Close()
	if copyErr != nil {
		writeErr(w, 400, "chunk write failed")
		return
	}
	if closeErr != nil {
		writeErr(w, 500, closeErr.Error())
		return
	}
	if sess.Offset+n > sess.Size {
		// Should have been rejected by MaxBytesReader; trim and error.
		writeErr(w, 400, "chunk exceeds declared size")
		return
	}
	sess.Offset += n
	sess.UpdatedAt = time.Now().UTC()
	if err := s.uploads().Put(sess); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": sess.ID, "offset": sess.Offset, "size": sess.Size})
}

func (s *Server) handleUploadSessionComplete(w http.ResponseWriter, r *http.Request, id string) {
	u := s.currentUser(r)
	sess, code, msg := s.sessionForUser(r, id)
	if sess == nil {
		writeErr(w, code, msg)
		return
	}
	if sess.Offset != sess.Size {
		writeErr(w, 400, fmt.Sprintf("upload incomplete: %d of %d bytes", sess.Offset, sess.Size))
		return
	}
	sc, err := s.resolveScope(u, sess.RootID, sess.DestRel)
	if err != nil || !sc.canWrite() || sc.Kind == "trash" {
		writeErr(w, 403, "forbidden")
		return
	}
	// Re-check conflict in case the tree changed; keep the original policy.
	chosen, overwrite, skipped, err := s.resolveConflict(sc, sess.OrigName, sess.Action)
	if err != nil {
		if errors.Is(err, files.ErrExists) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "file already exists",
				"code":  "exists",
				"name":  sess.OrigName,
			})
			return
		}
		writeErr(w, 400, err.Error())
		return
	}
	if skipped {
		s.uploads().Delete(id)
		writeJSON(w, 200, map[string]any{"path": joinDest(sc.Rel, sess.OrigName), "name": sess.OrigName, "skipped": true})
		return
	}
	// If rename target is now taken, pick a new free name.
	if sess.Action == files.ConflictRename && chosen != sess.FinalName {
		sess.FinalName = chosen
	} else if !overwrite && chosen != sess.FinalName && sess.Action != files.ConflictRename {
		// Destination appeared; treat as a conflict for "none".
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "file already exists",
			"code":  "exists",
			"name":  sess.OrigName,
		})
		return
	} else {
		sess.FinalName = chosen
		sess.Overwrite = overwrite
	}
	if err := s.checkScopeQuota(u, sc, sess.Size); err != nil {
		writeErr(w, 403, err.Error())
		return
	}
	if err := s.commitStaging(u, sc, sess.RootID, sess.OrigName, sess.FinalName, sess.Overwrite, sess.ID); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.uploads().mu.Lock()
	delete(s.uploads().sessions, sess.ID)
	s.uploads().mu.Unlock()
	out := map[string]any{"path": joinDest(sc.Rel, sess.FinalName), "name": sess.FinalName}
	if sess.Overwrite {
		out["overwritten"] = true
	}
	if sess.FinalName != sess.OrigName {
		out["renamed"] = true
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleUploadSessionAbort(w http.ResponseWriter, r *http.Request, id string) {
	sess, code, msg := s.sessionForUser(r, id)
	if sess == nil {
		writeErr(w, code, msg)
		return
	}
	s.uploads().Delete(id)
	writeJSON(w, 200, map[string]any{"ok": true})
}
