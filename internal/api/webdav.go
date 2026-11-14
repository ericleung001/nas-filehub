package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/eric/nas-filehub/internal/db"
	"github.com/eric/nas-filehub/internal/files"
	"golang.org/x/net/webdav"
)

type davUserKey struct{}

const davHomeName = "My Files"
const davIncomingName = "Shared with me"

func (s *Server) webdavEnabled() bool {
	return s.Cfg.WebDAVEnabled
}

func (s *Server) webDAVHandler() http.Handler {
	s.davOnce.Do(func() {
		h := &webdav.Handler{
			Prefix:     "/dav",
			FileSystem: &davFS{s: s},
			LockSystem: webdav.NewMemLS(),
		}
		s.davHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !s.webdavEnabled() {
				http.NotFound(w, r)
				return
			}
			if r.Method == http.MethodOptions && r.Header.Get("Authorization") == "" && r.Header.Get("Cookie") == "" {
				// Allow unauthenticated OPTIONS so clients can discover DAV.
				w.Header().Set("Allow", "OPTIONS, GET, HEAD, PUT, DELETE, MKCOL, COPY, MOVE, PROPFIND, PROPPATCH, LOCK, UNLOCK")
				w.Header().Set("DAV", "1, 2")
				w.Header().Set("MS-Author-Via", "DAV")
				w.WriteHeader(http.StatusOK)
				return
			}
			u := s.authenticateDAV(w, r)
			if u == nil {
				return
			}
			ctx := context.WithValue(r.Context(), davUserKey{}, u)
			h.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	return s.davHandler
}

func (s *Server) authenticateDAV(w http.ResponseWriter, r *http.Request) *db.User {
	if u := s.currentUser(r); u != nil && !u.Disabled && userActive(u) {
		return u
	}
	username, password, ok := r.BasicAuth()
	username = strings.TrimSpace(username)
	if !ok || username == "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="Filehub WebDAV"`)
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return nil
	}
	if locked, _, err := s.DB.LoginLockState(username); err == nil && locked {
		s.audit(r, 0, username, "webdav_auth_failure", "locked")
		writeErr(w, 401, "too many failed attempts, try again later")
		return nil
	}
	u, err := s.DB.GetUserByUsername(username)
	if err != nil || u == nil || !db.CheckPassword(u.PasswordHash, password) {
		// Only failed logins count against the login rate limit. Finder and
		// other WebDAV clients issue many PROPFIND/GET requests at once;
		// treating those like login attempts returns 429 and the folder looks empty.
		ip := s.clientIP(r)
		if s.Limiter != nil && !s.Limiter.Allow(ip) {
			writeErr(w, http.StatusTooManyRequests, "too many requests")
			return nil
		}
		_, _, _ = s.DB.RecordLoginFailure(username, s.Cfg.EffectiveLockoutLimit(), s.Cfg.EffectiveLockoutWindow())
		s.audit(r, 0, username, "webdav_auth_failure", "invalid credentials")
		w.Header().Set("WWW-Authenticate", `Basic realm="Filehub WebDAV"`)
		writeErr(w, 401, "invalid credentials")
		return nil
	}
	_ = s.DB.ClearLoginFailures(u.Username)
	if u.Disabled {
		s.audit(r, u.ID, u.Username, "webdav_auth_failure", "account disabled")
		writeErr(w, 401, "account disabled")
		return nil
	}
	if userPending(u) {
		s.audit(r, u.ID, u.Username, "webdav_auth_failure", "pending approval")
		writeErr(w, 403, "account pending approval")
		return nil
	}
	if !userActive(u) {
		s.audit(r, u.ID, u.Username, "webdav_auth_failure", "not active")
		writeErr(w, 403, "account is not active")
		return nil
	}
	return u
}

func davUser(ctx context.Context) *db.User {
	u, _ := ctx.Value(davUserKey{}).(*db.User)
	return u
}

func cleanDAVPath(name string) string {
	name = path.Clean("/" + strings.ReplaceAll(name, "\\", "/"))
	if name == "." {
		return "/"
	}
	return name
}

type davMount struct {
	rootID string
	name   string
	rel    string
	sc     *scope
}

type davFS struct {
	s *Server
}

func (fs *davFS) mounts(u *db.User) ([]davMount, error) {
	out := []davMount{{rootID: "home", name: davHomeName}, {rootID: "incoming", name: davIncomingName}}
	shares, err := fs.s.DB.SharesForUser(u.ID, u.IsAdmin)
	if err != nil {
		return nil, err
	}
	used := map[string]bool{strings.ToLower(davHomeName): true, strings.ToLower(davIncomingName): true}
	for _, sa := range shares {
		name := strings.TrimSpace(sa.Share.Name)
		if name == "" {
			name = fmt.Sprintf("Share %d", sa.Share.ID)
		}
		name = strings.ReplaceAll(name, "/", "-")
		key := strings.ToLower(name)
		if used[key] {
			name = fmt.Sprintf("%s (%d)", name, sa.Share.ID)
			key = strings.ToLower(name)
		}
		used[key] = true
		out = append(out, davMount{rootID: "share:" + itoaLocal(sa.Share.ID), name: name})
	}
	return out, nil
}

func (fs *davFS) resolve(ctx context.Context, name string) (*davMount, error) {
	u := davUser(ctx)
	if u == nil {
		return nil, os.ErrPermission
	}
	name = cleanDAVPath(name)
	if name == "/" {
		return &davMount{rootID: "", name: "/", rel: ""}, nil
	}
	trimmed := strings.Trim(name, "/")
	first, rest, _ := strings.Cut(trimmed, "/")
	mounts, err := fs.mounts(u)
	if err != nil {
		return nil, err
	}
	for _, m := range mounts {
		if m.name == first || strings.EqualFold(m.name, first) {
			if m.rootID == "incoming" {
				return fs.resolveIncoming(u, rest)
			}
			sc, err := fs.s.resolveScope(u, m.rootID, rest)
			if err != nil {
				return nil, err
			}
			if err := files.EnsureDir(sc.Root); err != nil {
				return nil, err
			}
			m.rel = rest
			m.sc = sc
			return &m, nil
		}
	}
	return nil, os.ErrNotExist
}

func (fs *davFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	m, err := fs.resolve(ctx, name)
	if err != nil {
		return err
	}
	if m.sc == nil || m.rel == "" || m.rel == "." {
		return os.ErrPermission
	}
	if !m.sc.canWrite() || m.sc.Kind == "trash" {
		return os.ErrPermission
	}
	u := davUser(ctx)
	if err := fs.s.checkScopeQuota(u, m.sc, 0); err != nil {
		return err
	}
	return files.Mkdir(m.sc.Root, m.sc.Rel)
}

func (fs *davFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	u := davUser(ctx)
	if u == nil {
		return nil, os.ErrPermission
	}
	name = cleanDAVPath(name)
	if name == "/" {
		if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC) != 0 {
			return nil, os.ErrPermission
		}
		return fs.openRoot(u)
	}
	m, err := fs.resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	if m != nil && m.rootID == "incoming" && m.sc == nil {
		if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC) != 0 {
			return nil, os.ErrPermission
		}
		return fs.openIncoming(u)
	}
	if m.sc == nil {
		return nil, os.ErrNotExist
	}
	write := flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0
	if write && (!m.sc.canWrite() || m.sc.Kind == "trash") {
		return nil, os.ErrPermission
	}
	full, err := files.SafeJoin(m.sc.Root, m.sc.Rel)
	if err != nil {
		return nil, err
	}
	creating := flag&os.O_CREATE != 0
	if write && creating {
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, err
		}
	}
	leftover := int64(-1)
	bill := fs.s.quotaUser(u, m.sc)
	limit := fs.s.effectiveQuota(bill)
	if write && limit > 0 {
		used := fs.s.usedBytes(bill)
		left := limit - used
		if st, err := os.Lstat(full); err == nil && !st.IsDir() && flag&os.O_TRUNC != 0 {
			left += st.Size()
		}
		if left < 0 {
			left = 0
		}
		if creating {
			if _, err := os.Lstat(full); os.IsNotExist(err) && left <= 0 {
				return nil, fmt.Errorf("quota exceeded")
			}
		}
		leftover = left
	}
	f, err := os.OpenFile(full, flag, perm)
	if err != nil {
		return nil, err
	}
	display := filepath.Base(name)
	if m.rel == "" || m.rel == "." {
		display = m.name
	}
	return &davFile{File: f, s: fs.s, u: u, sc: m.sc, leftover: leftover, display: display}, nil
}

func (fs *davFS) openRoot(u *db.User) (webdav.File, error) {
	mounts, err := fs.mounts(u)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	children := make([]os.FileInfo, 0, len(mounts))
	for _, m := range mounts {
		children = append(children, virtInfo{name: m.name, mode: os.ModeDir | 0o755, mod: now})
	}
	return &virtDir{info: virtInfo{name: "/", mode: os.ModeDir | 0o755, mod: now}, children: children}, nil
}

func (fs *davFS) RemoveAll(ctx context.Context, name string) error {
	u := davUser(ctx)
	name = cleanDAVPath(name)
	if name == "/" {
		return os.ErrPermission
	}
	m, err := fs.resolve(ctx, name)
	if err != nil {
		return err
	}
	if m.sc == nil || m.rel == "" || m.rel == "." {
		return os.ErrPermission
	}
	if !m.sc.canDelete() {
		return os.ErrPermission
	}
	return fs.s.trashDelete(u, m.sc, m.sc.Rel, m.rootID)
}

func (fs *davFS) Rename(ctx context.Context, oldName, newName string) error {
	u := davUser(ctx)
	oldName, newName = cleanDAVPath(oldName), cleanDAVPath(newName)
	src, err := fs.resolve(ctx, oldName)
	if err != nil {
		return err
	}
	dst, err := fs.resolve(ctx, newName)
	if err != nil && !os.IsNotExist(err) {
		// Destination parent may not include the final name yet; resolve parent.
		parent, base := path.Split(strings.TrimPrefix(newName, "/"))
		parent = "/" + parent
		dm, perr := fs.resolve(ctx, parent)
		if perr != nil || dm.sc == nil {
			return err
		}
		rel := base
		if dm.sc.Rel != "" && dm.sc.Rel != "." {
			rel = filepath.ToSlash(filepath.Join(dm.sc.Rel, base))
		}
		sc := *dm.sc
		sc.Rel = rel
		dst = &davMount{rootID: dm.rootID, name: dm.name, rel: rel, sc: &sc}
	}
	if src == nil || src.sc == nil || dst == nil || dst.sc == nil {
		return os.ErrPermission
	}
	if src.rel == "" || src.rel == "." || dst.rel == "" || dst.rel == "." {
		return os.ErrPermission
	}
	if !src.sc.canDelete() || !dst.sc.canWrite() {
		return os.ErrPermission
	}
	if err := files.MoveEntry(src.sc.Root, src.sc.Rel, dst.sc.Root, dst.sc.Rel); err != nil {
		return err
	}
	fs.s.afterTransfer(u, src.sc, dst.sc, src.sc.Rel, dst.sc.Rel, true)
	return nil
}

func (fs *davFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	name = cleanDAVPath(name)
	if name == "/" {
		return virtInfo{name: "/", mode: os.ModeDir | 0o755, mod: time.Now()}, nil
	}
	m, err := fs.resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	if m != nil && m.rootID == "incoming" && m.sc == nil {
		return virtInfo{name: davIncomingName, mode: os.ModeDir | 0o755, mod: time.Now()}, nil
	}
	if m.sc == nil {
		return nil, os.ErrNotExist
	}
	full, err := files.SafeJoin(m.sc.Root, m.sc.Rel)
	if err != nil {
		return nil, err
	}
	st, err := os.Lstat(full)
	if err != nil {
		return nil, err
	}
	// Present the last path segment (My Files, share name, or file name).
	display := filepath.Base(name)
	if display == "." || display == "/" {
		display = m.name
	}
	return namedInfo{FileInfo: st, name: display}, nil
}

type namedInfo struct {
	os.FileInfo
	name string
}

func (n namedInfo) Name() string { return n.name }

type virtInfo struct {
	name string
	mode os.FileMode
	mod  time.Time
	size int64
}

func (i virtInfo) Name() string       { return i.name }
func (i virtInfo) Size() int64        { return i.size }
func (i virtInfo) Mode() os.FileMode  { return i.mode }
func (i virtInfo) ModTime() time.Time { return i.mod }
func (i virtInfo) IsDir() bool        { return i.mode.IsDir() }
func (i virtInfo) Sys() any           { return nil }

type virtDir struct {
	info     os.FileInfo
	children []os.FileInfo
	off      int
}

func (d *virtDir) Close() error                   { return nil }
func (d *virtDir) Read([]byte) (int, error)       { return 0, os.ErrInvalid }
func (d *virtDir) Write([]byte) (int, error)      { return 0, os.ErrPermission }
func (d *virtDir) Seek(int64, int) (int64, error) { return 0, os.ErrInvalid }
func (d *virtDir) Stat() (os.FileInfo, error)     { return d.info, nil }
func (d *virtDir) Readdir(count int) ([]os.FileInfo, error) {
	if d.off >= len(d.children) {
		if count <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	if count <= 0 {
		all := d.children[d.off:]
		d.off = len(d.children)
		return all, nil
	}
	end := d.off + count
	if end > len(d.children) {
		end = len(d.children)
	}
	out := d.children[d.off:end]
	d.off = end
	if len(out) == 0 {
		return nil, io.EOF
	}
	return out, nil
}

type davFile struct {
	*os.File
	s        *Server
	u        *db.User
	sc       *scope
	leftover int64
	written  int64
	display  string
}

func (f *davFile) Stat() (os.FileInfo, error) {
	st, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	if f.display != "" {
		return namedInfo{FileInfo: st, name: f.display}, nil
	}
	return st, nil
}

func (f *davFile) Write(p []byte) (int, error) {
	if f.leftover >= 0 && f.written+int64(len(p)) > f.leftover {
		return 0, fmt.Errorf("quota exceeded")
	}
	n, err := f.File.Write(p)
	f.written += int64(n)
	return n, err
}

func (f *davFile) Close() error {
	err := f.File.Close()
	if f.written > 0 && f.sc != nil && f.u != nil {
		name := filepath.Base(f.Name())
		rel := name
		if f.sc.Rel != "" && f.sc.Rel != "." {
			// OpenFile uses sc.Rel as the file path (not the parent).
			rel = f.sc.Rel
		}
		f.s.attributeWrite(f.u, f.sc, rel)
	}
	return err
}

func (fs *davFS) resolveIncoming(u *db.User, rest string) (*davMount, error) {
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return &davMount{rootID: "incoming", name: davIncomingName, rel: ""}, nil
	}
	first, rem, _ := strings.Cut(rest, "/")
	entries, err := fs.s.listIncomingEntries(u)
	if err != nil {
		return nil, err
	}
	var idStr string
	for _, e := range entries {
		name, _ := e["name"].(string)
		if name == first || strings.EqualFold(name, first) {
			idStr, _ = e["path"].(string)
			break
		}
	}
	if idStr == "" {
		return nil, os.ErrNotExist
	}
	rel := idStr
	if rem != "" {
		rel = idStr + "/" + rem
	}
	sc, err := fs.s.resolveScope(u, "incoming", rel)
	if err != nil {
		return nil, err
	}
	return &davMount{rootID: "incoming", name: davIncomingName, rel: rel, sc: sc}, nil
}

func (fs *davFS) openIncoming(u *db.User) (webdav.File, error) {
	entries, err := fs.s.listIncomingEntries(u)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	children := make([]os.FileInfo, 0, len(entries))
	for _, e := range entries {
		name, _ := e["name"].(string)
		if name == "" {
			continue
		}
		isDir, _ := e["isDir"].(bool)
		var size int64
		switch v := e["size"].(type) {
		case int64:
			size = v
		case int:
			size = int64(v)
		}
		mod := now
		if t, ok := e["modTime"].(time.Time); ok {
			mod = t
		}
		mode := os.FileMode(0o644)
		if isDir {
			mode = os.ModeDir | 0o755
		}
		children = append(children, virtInfo{name: name, mode: mode, size: size, mod: mod})
	}
	return &virtDir{info: virtInfo{name: davIncomingName, mode: os.ModeDir | 0o755, mod: now}, children: children}, nil
}
