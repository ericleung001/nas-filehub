package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type DB struct {
	SQL *sql.DB
}

type User struct {
	ID                    int64     `json:"id"`
	Username              string    `json:"username"`
	Email                 string    `json:"email"`
	PasswordHash          string    `json:"-"`
	IsAdmin               bool      `json:"isAdmin"`
	HomePath              string    `json:"homePath"`   // relative to DATA_ROOT
	QuotaBytes            int64     `json:"quotaBytes"` // 0 = unlimited
	Disabled              bool      `json:"disabled"`
	TotpSecret            string    `json:"-"`
	TotpEnabled           bool      `json:"totpEnabled"`
	TotpRequired          bool      `json:"totpRequired"`
	PasswordChangedAt     time.Time `json:"passwordChangedAt,omitempty"`
	PasswordPolicyVersion int       `json:"passwordPolicyVersion"`
	CreatedAt             time.Time `json:"createdAt"`
}

type Share struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"` // relative to DATA_ROOT
	CreatedBy int64     `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
}

type ShareGrant struct {
	ShareID   int64  `json:"shareId"`
	UserID    int64  `json:"userId"`
	Perm      string `json:"perm"` // "read" or "write"
	CanDelete bool   `json:"canDelete"`
	Username  string `json:"username,omitempty"`
}

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	sqlDB, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	d := &DB{SQL: sqlDB}
	if err := d.migrate(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) Close() error { return d.SQL.Close() }

func (d *DB) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL,
  is_admin INTEGER NOT NULL DEFAULT 0,
  home_path TEXT NOT NULL,
  quota_bytes INTEGER NOT NULL DEFAULT 0,
  disabled INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  token TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS shares (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  path TEXT NOT NULL UNIQUE,
  created_by INTEGER NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS share_grants (
  share_id INTEGER NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  perm TEXT NOT NULL CHECK(perm IN ('read','write')),
  can_delete INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (share_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_grants_user ON share_grants(user_id);
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS pending_logins (
  token TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK(kind IN ('totp','enroll')),
  expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pending_logins_user ON pending_logins(user_id);
CREATE TABLE IF NOT EXISTS totp_backup_codes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_hash TEXT NOT NULL,
  used INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_totp_backup_user ON totp_backup_codes(user_id);
`
	if _, err := d.SQL.Exec(schema); err != nil {
		return err
	}
	if err := d.ensureUserColumns(); err != nil {
		return err
	}
	if err := d.ensurePendingLoginColumns(); err != nil {
		return err
	}
	if err := d.ensureLinkShareTables(); err != nil {
		return err
	}
	if err := d.ensureShareGrantColumns(); err != nil {
		return err
	}
	return d.ensurePasswordResetTable()
}

func (d *DB) ensureUserColumns() error {
	cols := map[string]bool{}
	rows, err := d.SQL.Query(`PRAGMA table_info(users)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	alters := []struct {
		name string
		ddl  string
	}{
		{"quota_bytes", `ALTER TABLE users ADD COLUMN quota_bytes INTEGER NOT NULL DEFAULT 0`},
		{"disabled", `ALTER TABLE users ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0`},
		{"totp_secret", `ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT ''`},
		{"totp_enabled", `ALTER TABLE users ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0`},
		{"totp_required", `ALTER TABLE users ADD COLUMN totp_required INTEGER NOT NULL DEFAULT 0`},
		{"password_changed_at", `ALTER TABLE users ADD COLUMN password_changed_at TEXT NOT NULL DEFAULT ''`},
		{"password_policy_version", `ALTER TABLE users ADD COLUMN password_policy_version INTEGER NOT NULL DEFAULT 0`},
		{"email", `ALTER TABLE users ADD COLUMN email TEXT NOT NULL DEFAULT ''`},
	}
	for _, a := range alters {
		if cols[a.name] {
			continue
		}
		if _, err := d.SQL.Exec(a.ddl); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) ensurePendingLoginColumns() error {
	cols := map[string]bool{}
	rows, err := d.SQL.Query(`PRAGMA table_info(pending_logins)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !cols["must_change_password"] {
		if _, err := d.SQL.Exec(`ALTER TABLE pending_logins ADD COLUMN must_change_password INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if !cols["remember_me"] {
		if _, err := d.SQL.Exec(`ALTER TABLE pending_logins ADD COLUMN remember_me INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) ensureShareGrantColumns() error {
	cols := map[string]bool{}
	rows, err := d.SQL.Query(`PRAGMA table_info(share_grants)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if cols["can_delete"] {
		return nil
	}
	if _, err := d.SQL.Exec(`ALTER TABLE share_grants ADD COLUMN can_delete INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	// Existing write grants could delete; keep that behavior.
	_, err = d.SQL.Exec(`UPDATE share_grants SET can_delete=1 WHERE perm='write'`)
	return err
}

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

func (d *DB) EnsureAdmin(username, password string) error {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil
	}
	var n int
	if err := d.SQL.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin=1`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	home := filepath.ToSlash(filepath.Join("homes", username))
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = d.SQL.Exec(
		`INSERT INTO users(username, password_hash, is_admin, home_path, created_at) VALUES(?,?,1,?,?)`,
		username, hash, home, now,
	)
	return err
}

func scanUser(scanner interface {
	Scan(dest ...any) error
}) (*User, error) {
	u := &User{}
	var admin, disabled, totpEnabled, totpRequired int
	var created, pwChanged string
	err := scanner.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &admin, &u.HomePath, &u.QuotaBytes, &disabled, &u.TotpSecret, &totpEnabled, &totpRequired, &pwChanged, &u.PasswordPolicyVersion, &created)
	if err != nil {
		return nil, err
	}
	u.IsAdmin = admin == 1
	u.Disabled = disabled == 1
	u.TotpEnabled = totpEnabled == 1
	u.TotpRequired = totpRequired == 1
	if pwChanged != "" {
		u.PasswordChangedAt, _ = time.Parse(time.RFC3339, pwChanged)
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return u, nil
}

const userSelectCols = `id, username, email, password_hash, is_admin, home_path, quota_bytes, disabled, totp_secret, totp_enabled, totp_required, password_changed_at, password_policy_version, created_at`

func (d *DB) GetUserByUsername(username string) (*User, error) {
	row := d.SQL.QueryRow(
		`SELECT `+userSelectCols+` FROM users WHERE username=? COLLATE NOCASE`,
		username,
	)
	u, err := scanUser(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (d *DB) GetUserByID(id int64) (*User, error) {
	row := d.SQL.QueryRow(`SELECT `+userSelectCols+` FROM users WHERE id=?`, id)
	u, err := scanUser(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (d *DB) ListUsers() ([]User, error) {
	rows, err := d.SQL.Query(`SELECT ` + userSelectCols + ` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		u.PasswordHash = ""
		out = append(out, *u)
	}
	return out, rows.Err()
}

func sanitizeHomePath(homePath, username string) (string, error) {
	if homePath == "" {
		homePath = filepath.ToSlash(filepath.Join("homes", username))
	}
	homePath = filepath.ToSlash(filepath.Clean(homePath))
	if homePath == "" || homePath == "." || strings.HasPrefix(homePath, "..") || strings.Contains(homePath, "../") {
		return "", fmt.Errorf("invalid home path")
	}
	if strings.HasPrefix(homePath, "/") {
		return "", fmt.Errorf("home path must be relative to DATA_ROOT")
	}
	return homePath, nil
}

func (d *DB) CreateUser(username, password, homePath string, isAdmin bool, quotaBytes int64, email string) (*User, error) {
	username = strings.TrimSpace(username)
	email = normalizeEmail(email)
	if username == "" || password == "" {
		return nil, fmt.Errorf("username and password required")
	}
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if quotaBytes < 0 {
		return nil, fmt.Errorf("quotaBytes must be >= 0")
	}
	if err := validateEmailOptional(email); err != nil {
		return nil, err
	}
	var err error
	homePath, err = sanitizeHomePath(homePath, username)
	if err != nil {
		return nil, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	admin := 0
	if isAdmin {
		admin = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	pol, _ := d.GetPasswordPolicy()
	res, err := d.SQL.Exec(
		`INSERT INTO users(username, email, password_hash, is_admin, home_path, quota_bytes, disabled, password_changed_at, password_policy_version, created_at) VALUES(?,?,?,?,?,?,0,?,?,?)`,
		username, email, hash, admin, homePath, quotaBytes, now, pol.Version, now,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{
		ID: id, Username: username, Email: email, IsAdmin: isAdmin, HomePath: homePath,
		QuotaBytes: quotaBytes, PasswordChangedAt: time.Now().UTC(),
		PasswordPolicyVersion: pol.Version, CreatedAt: time.Now().UTC(),
	}, nil
}

// UpdateUser patches admin-managed fields. Empty username keeps current; empty homePath keeps current.
// Pass clearDisabled/setDisabled via disabled pointer; nil means leave unchanged for optional fields
// that use pointers in the API layer — here we take concrete values with "set" flags.
type UserUpdate struct {
	Username     *string
	Email        *string
	IsAdmin      *bool
	HomePath     *string
	QuotaBytes   *int64
	Disabled     *bool
	TotpRequired *bool
}

func (d *DB) UpdateUser(id int64, upd UserUpdate) (*User, error) {
	cur, err := d.GetUserByID(id)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, fmt.Errorf("user not found")
	}
	username := cur.Username
	if upd.Username != nil {
		username = strings.TrimSpace(*upd.Username)
		if username == "" {
			return nil, fmt.Errorf("username required")
		}
	}
	email := cur.Email
	if upd.Email != nil {
		email = normalizeEmail(*upd.Email)
		if err := validateEmailOptional(email); err != nil {
			return nil, err
		}
	}
	isAdmin := cur.IsAdmin
	if upd.IsAdmin != nil {
		isAdmin = *upd.IsAdmin
	}
	homePath := cur.HomePath
	if upd.HomePath != nil {
		homePath, err = sanitizeHomePath(*upd.HomePath, username)
		if err != nil {
			return nil, err
		}
	}
	quota := cur.QuotaBytes
	if upd.QuotaBytes != nil {
		if *upd.QuotaBytes < 0 {
			return nil, fmt.Errorf("quotaBytes must be >= 0")
		}
		quota = *upd.QuotaBytes
	}
	disabled := cur.Disabled
	if upd.Disabled != nil {
		disabled = *upd.Disabled
	}
	totpRequired := cur.TotpRequired
	if upd.TotpRequired != nil {
		totpRequired = *upd.TotpRequired
	}
	admin := 0
	if isAdmin {
		admin = 1
	}
	dis := 0
	if disabled {
		dis = 1
	}
	req := 0
	if totpRequired {
		req = 1
	}
	_, err = d.SQL.Exec(
		`UPDATE users SET username=?, email=?, is_admin=?, home_path=?, quota_bytes=?, disabled=?, totp_required=? WHERE id=?`,
		username, email, admin, homePath, quota, dis, req, id,
	)
	if err != nil {
		return nil, err
	}
	if disabled && !cur.Disabled {
		_ = d.DeleteUserSessions(id)
	}
	return d.GetUserByID(id)
}

func (d *DB) SetUserPassword(id int64, password string) error {
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("password required")
	}
	u, err := d.GetUserByID(id)
	if err != nil {
		return err
	}
	if u == nil {
		return fmt.Errorf("user not found")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	pol, err := d.GetPasswordPolicy()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := d.SQL.Exec(
		`UPDATE users SET password_hash=?, password_changed_at=?, password_policy_version=? WHERE id=?`,
		hash, now, pol.Version, id,
	); err != nil {
		return err
	}
	_ = d.DeletePasswordResetsForUser(id)
	return d.DeleteUserSessions(id)
}

// SetUserPasswordKeepSession updates the password hash, clears other sessions for the user,
// but keeps the given session token (so a self-service change does not log the user out).
func (d *DB) SetUserPasswordKeepSession(id int64, password, keepToken string) error {
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("password required")
	}
	u, err := d.GetUserByID(id)
	if err != nil {
		return err
	}
	if u == nil {
		return fmt.Errorf("user not found")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	pol, err := d.GetPasswordPolicy()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := d.SQL.Exec(
		`UPDATE users SET password_hash=?, password_changed_at=?, password_policy_version=? WHERE id=?`,
		hash, now, pol.Version, id,
	); err != nil {
		return err
	}
	if keepToken == "" {
		return d.DeleteUserSessions(id)
	}
	_, err = d.SQL.Exec(`DELETE FROM sessions WHERE user_id=? AND token!=?`, id, keepToken)
	return err
}

func (d *DB) MarkPasswordChangeRequired(userID int64) error {
	_, err := d.SQL.Exec(`UPDATE users SET password_policy_version=-1 WHERE id=?`, userID)
	return err
}

func (d *DB) DeleteUserSessions(userID int64) error {
	_, err := d.SQL.Exec(`DELETE FROM sessions WHERE user_id=?`, userID)
	return err
}

func (d *DB) DeleteUser(id int64) error {
	_, err := d.SQL.Exec(`DELETE FROM users WHERE id=?`, id)
	return err
}

func (d *DB) CreateSession(token string, userID int64, expires time.Time) error {
	_, err := d.SQL.Exec(
		`INSERT INTO sessions(token, user_id, expires_at) VALUES(?,?,?)`,
		token, userID, expires.UTC().Format(time.RFC3339),
	)
	return err
}

func (d *DB) GetSessionUser(token string) (*User, error) {
	var userID int64
	var expStr string
	err := d.SQL.QueryRow(`SELECT user_id, expires_at FROM sessions WHERE token=?`, token).Scan(&userID, &expStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	exp, err := time.Parse(time.RFC3339, expStr)
	if err != nil || time.Now().After(exp) {
		_, _ = d.SQL.Exec(`DELETE FROM sessions WHERE token=?`, token)
		return nil, nil
	}
	return d.GetUserByID(userID)
}

func (d *DB) DeleteSession(token string) error {
	_, err := d.SQL.Exec(`DELETE FROM sessions WHERE token=?`, token)
	return err
}

func (d *DB) CleanupSessions() {
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = d.SQL.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now)
	_, _ = d.SQL.Exec(`DELETE FROM pending_logins WHERE expires_at < ?`, now)
	d.CleanupPasswordResets()
	d.CleanupLinkUnlocks()
}

func (d *DB) CreateShare(name, path string, createdBy int64) (*Share, error) {
	name = strings.TrimSpace(name)
	path = filepath.ToSlash(filepath.Clean(path))
	if name == "" || path == "" || strings.HasPrefix(path, "..") {
		return nil, fmt.Errorf("invalid share")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := d.SQL.Exec(
		`INSERT INTO shares(name, path, created_by, created_at) VALUES(?,?,?,?)`,
		name, path, createdBy, now,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Share{ID: id, Name: name, Path: path, CreatedBy: createdBy, CreatedAt: time.Now().UTC()}, nil
}

func (d *DB) ListShares() ([]Share, error) {
	rows, err := d.SQL.Query(`SELECT id, name, path, created_by, created_at FROM shares ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Share
	for rows.Next() {
		var s Share
		var created string
		if err := rows.Scan(&s.ID, &s.Name, &s.Path, &s.CreatedBy, &created); err != nil {
			return nil, err
		}
		s.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) GetShare(id int64) (*Share, error) {
	s := &Share{}
	var created string
	err := d.SQL.QueryRow(`SELECT id, name, path, created_by, created_at FROM shares WHERE id=?`, id).
		Scan(&s.ID, &s.Name, &s.Path, &s.CreatedBy, &created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return s, nil
}

func (d *DB) DeleteShare(id int64) error {
	_, err := d.SQL.Exec(`DELETE FROM shares WHERE id=?`, id)
	return err
}

func (d *DB) SetShareGrant(shareID, userID int64, perm string, canDelete *bool) error {
	perm = strings.ToLower(strings.TrimSpace(perm))
	if perm != "read" && perm != "write" {
		return fmt.Errorf("perm must be read or write")
	}
	if userID <= 0 {
		return fmt.Errorf("user required")
	}
	u, err := d.GetUserByID(userID)
	if err != nil {
		return err
	}
	if u == nil {
		return fmt.Errorf("user not found")
	}
	sh, err := d.GetShare(shareID)
	if err != nil {
		return err
	}
	if sh == nil {
		return fmt.Errorf("share not found")
	}
	deleteFlag := 0
	if canDelete != nil {
		if *canDelete {
			deleteFlag = 1
		}
	} else {
		var existing int
		err = d.SQL.QueryRow(`SELECT can_delete FROM share_grants WHERE share_id=? AND user_id=?`, shareID, userID).Scan(&existing)
		if err == sql.ErrNoRows {
			// New grant with flag omitted: keep historical write=can-delete default.
			if perm == "write" {
				deleteFlag = 1
			}
		} else if err != nil {
			return err
		} else {
			deleteFlag = existing
		}
	}
	_, err = d.SQL.Exec(
		`INSERT INTO share_grants(share_id, user_id, perm, can_delete) VALUES(?,?,?,?)
		 ON CONFLICT(share_id, user_id) DO UPDATE SET perm=excluded.perm, can_delete=excluded.can_delete`,
		shareID, userID, perm, deleteFlag,
	)
	return err
}

func (d *DB) RemoveShareGrant(shareID, userID int64) error {
	_, err := d.SQL.Exec(`DELETE FROM share_grants WHERE share_id=? AND user_id=?`, shareID, userID)
	return err
}

type ShareAccess struct {
	Share     Share
	Perm      string
	CanDelete bool
}

func (d *DB) SharesForUser(userID int64, isAdmin bool) ([]ShareAccess, error) {
	if isAdmin {
		shares, err := d.ListShares()
		if err != nil {
			return nil, err
		}
		out := make([]ShareAccess, 0, len(shares))
		for _, s := range shares {
			out = append(out, ShareAccess{Share: s, Perm: "write", CanDelete: true})
		}
		return out, nil
	}
	rows, err := d.SQL.Query(`
		SELECT s.id, s.name, s.path, s.created_by, s.created_at, g.perm, g.can_delete
		FROM share_grants g
		JOIN shares s ON s.id = g.share_id
		WHERE g.user_id=?
		ORDER BY s.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShareAccess
	for rows.Next() {
		var sa ShareAccess
		var created string
		var canDel int
		if err := rows.Scan(&sa.Share.ID, &sa.Share.Name, &sa.Share.Path, &sa.Share.CreatedBy, &created, &sa.Perm, &canDel); err != nil {
			return nil, err
		}
		sa.CanDelete = canDel != 0
		sa.Share.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, sa)
	}
	return out, rows.Err()
}

func (d *DB) GrantAccess(shareID, userID int64) (perm string, canDelete bool, err error) {
	var canDel int
	err = d.SQL.QueryRow(`SELECT perm, can_delete FROM share_grants WHERE share_id=? AND user_id=?`, shareID, userID).Scan(&perm, &canDel)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return perm, canDel != 0, nil
}

func (d *DB) ListGrants(shareID int64) ([]ShareGrant, error) {
	rows, err := d.SQL.Query(`
		SELECT g.share_id, g.user_id, g.perm, g.can_delete, COALESCE(u.username, '')
		FROM share_grants g
		LEFT JOIN users u ON u.id = g.user_id
		WHERE g.share_id=?
		ORDER BY u.username`, shareID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ShareGrant, 0)
	for rows.Next() {
		var g ShareGrant
		var canDel int
		if err := rows.Scan(&g.ShareID, &g.UserID, &g.Perm, &canDel, &g.Username); err != nil {
			return nil, err
		}
		g.CanDelete = canDel != 0
		out = append(out, g)
	}
	return out, rows.Err()
}

// --- TOTP / settings ---

func (d *DB) GetSetting(key string) (string, error) {
	var v string
	err := d.SQL.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (d *DB) SetSetting(key, value string) error {
	_, err := d.SQL.Exec(
		`INSERT INTO settings(key, value) VALUES(?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value,
	)
	return err
}

func (d *DB) Force2FAAll() (bool, error) {
	v, err := d.GetSetting("force_2fa_all")
	if err != nil {
		return false, err
	}
	return v == "1" || strings.EqualFold(v, "true"), nil
}

func (d *DB) SetForce2FAAll(on bool) error {
	if on {
		return d.SetSetting("force_2fa_all", "1")
	}
	return d.SetSetting("force_2fa_all", "0")
}

func (d *DB) OpenRegistration() (bool, error) {
	v, err := d.GetSetting("open_registration")
	if err != nil {
		return false, err
	}
	return v == "1" || strings.EqualFold(v, "true"), nil
}

func (d *DB) SetOpenRegistration(on bool) error {
	if on {
		return d.SetSetting("open_registration", "1")
	}
	return d.SetSetting("open_registration", "0")
}

// UserNeedsTOTP reports whether this user must pass a TOTP step at login
// (already enrolled) or must enroll before full access.
func (d *DB) UserNeedsTOTP(u *User, forceAll bool) (needCode bool, needEnroll bool) {
	if u == nil {
		return false, false
	}
	required := forceAll || u.TotpRequired
	if u.TotpEnabled && u.TotpSecret != "" {
		return true, false
	}
	if required {
		return false, true
	}
	return false, false
}

func (d *DB) CreatePendingLogin(token string, userID int64, kind string, expires time.Time, mustChangePassword, rememberMe bool) error {
	if kind != "totp" && kind != "enroll" {
		return fmt.Errorf("invalid pending login kind")
	}
	flag := 0
	if mustChangePassword {
		flag = 1
	}
	remember := 0
	if rememberMe {
		remember = 1
	}
	_, err := d.SQL.Exec(
		`INSERT INTO pending_logins(token, user_id, kind, expires_at, must_change_password, remember_me) VALUES(?,?,?,?,?,?)`,
		token, userID, kind, expires.UTC().Format(time.RFC3339), flag, remember,
	)
	return err
}

type PendingLogin struct {
	Token              string
	UserID             int64
	Kind               string
	MustChangePassword bool
	RememberMe         bool
}

func (d *DB) GetPendingLogin(token string) (*PendingLogin, error) {
	var p PendingLogin
	var expStr string
	var mustChange int
	var remember int
	err := d.SQL.QueryRow(
		`SELECT token, user_id, kind, expires_at, must_change_password, remember_me FROM pending_logins WHERE token=?`, token,
	).Scan(&p.Token, &p.UserID, &p.Kind, &expStr, &mustChange, &remember)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.MustChangePassword = mustChange == 1
	p.RememberMe = remember == 1
	exp, err := time.Parse(time.RFC3339, expStr)
	if err != nil || time.Now().After(exp) {
		_, _ = d.SQL.Exec(`DELETE FROM pending_logins WHERE token=?`, token)
		return nil, nil
	}
	return &p, nil
}

func (d *DB) DeletePendingLogin(token string) error {
	_, err := d.SQL.Exec(`DELETE FROM pending_logins WHERE token=?`, token)
	return err
}

func (d *DB) DeletePendingLoginsForUser(userID int64) error {
	_, err := d.SQL.Exec(`DELETE FROM pending_logins WHERE user_id=?`, userID)
	return err
}

func (d *DB) SetTotpSecret(userID int64, secret string) error {
	_, err := d.SQL.Exec(`UPDATE users SET totp_secret=? WHERE id=?`, secret, userID)
	return err
}

func (d *DB) EnableTotp(userID int64, secret string) error {
	_, err := d.SQL.Exec(
		`UPDATE users SET totp_secret=?, totp_enabled=1 WHERE id=?`,
		secret, userID,
	)
	return err
}

func (d *DB) DisableTotp(userID int64) error {
	tx, err := d.SQL.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE users SET totp_secret='', totp_enabled=0 WHERE id=?`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM totp_backup_codes WHERE user_id=?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) ReplaceBackupCodes(userID int64, codeHashes []string) error {
	tx, err := d.SQL.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM totp_backup_codes WHERE user_id=?`, userID); err != nil {
		return err
	}
	for _, h := range codeHashes {
		if _, err := tx.Exec(
			`INSERT INTO totp_backup_codes(user_id, code_hash, used) VALUES(?,?,0)`,
			userID, h,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ConsumeBackupCode marks a matching unused backup code as used. Returns true if consumed.
func (d *DB) ConsumeBackupCode(userID int64, plainCode string) (bool, error) {
	rows, err := d.SQL.Query(
		`SELECT id, code_hash FROM totp_backup_codes WHERE user_id=? AND used=0`, userID,
	)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var hash string
		if err := rows.Scan(&id, &hash); err != nil {
			return false, err
		}
		if CheckPassword(hash, plainCode) {
			res, err := d.SQL.Exec(`UPDATE totp_backup_codes SET used=1 WHERE id=? AND used=0`, id)
			if err != nil {
				return false, err
			}
			n, _ := res.RowsAffected()
			return n == 1, nil
		}
	}
	return false, rows.Err()
}

func normalizeEmail(email string) string {
	return strings.TrimSpace(strings.ToLower(email))
}

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{1,31}$`)

// ValidateUsername enforces a conservative public signup / admin create username.
func ValidateUsername(username string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("username required")
	}
	if len(username) < 2 || len(username) > 32 {
		return fmt.Errorf("username must be 2–32 characters")
	}
	if !usernamePattern.MatchString(username) {
		return fmt.Errorf("username may only use letters, digits, ., _, - and must start with a letter or digit")
	}
	return nil
}

func ValidateEmailRequired(email string) error {
	email = normalizeEmail(email)
	if email == "" {
		return fmt.Errorf("email required")
	}
	return validateEmailOptional(email)
}

func validateEmailOptional(email string) error {
	if email == "" {
		return nil
	}
	if len(email) > 254 {
		return fmt.Errorf("email too long")
	}
	at := strings.IndexByte(email, '@')
	if at < 1 || at == len(email)-1 || strings.Contains(email, " ") {
		return fmt.Errorf("invalid email")
	}
	return nil
}
