package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	"golang.org/x/crypto/bcrypt"
)

type DB struct {
	SQL *sql.DB
}

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	IsAdmin      bool      `json:"isAdmin"`
	HomePath     string    `json:"homePath"` // relative to DATA_ROOT
	CreatedAt    time.Time `json:"createdAt"`
}

type Share struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"` // relative to DATA_ROOT
	CreatedBy int64     `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
}

type ShareGrant struct {
	ShareID int64  `json:"shareId"`
	UserID  int64  `json:"userId"`
	Perm    string `json:"perm"` // "read" or "write"
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
  PRIMARY KEY (share_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_grants_user ON share_grants(user_id);
`
	_, err := d.SQL.Exec(schema)
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

func (d *DB) GetUserByUsername(username string) (*User, error) {
	u := &User{}
	var admin int
	var created string
	err := d.SQL.QueryRow(
		`SELECT id, username, password_hash, is_admin, home_path, created_at FROM users WHERE username=? COLLATE NOCASE`,
		username,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &admin, &u.HomePath, &created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.IsAdmin = admin == 1
	u.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return u, nil
}

func (d *DB) GetUserByID(id int64) (*User, error) {
	u := &User{}
	var admin int
	var created string
	err := d.SQL.QueryRow(
		`SELECT id, username, password_hash, is_admin, home_path, created_at FROM users WHERE id=?`,
		id,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &admin, &u.HomePath, &created)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.IsAdmin = admin == 1
	u.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return u, nil
}

func (d *DB) ListUsers() ([]User, error) {
	rows, err := d.SQL.Query(`SELECT id, username, password_hash, is_admin, home_path, created_at FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var admin int
		var created string
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &admin, &u.HomePath, &created); err != nil {
			return nil, err
		}
		u.IsAdmin = admin == 1
		u.CreatedAt, _ = time.Parse(time.RFC3339, created)
		u.PasswordHash = ""
		out = append(out, u)
	}
	return out, rows.Err()
}

func (d *DB) CreateUser(username, password, homePath string, isAdmin bool) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, fmt.Errorf("username and password required")
	}
	if homePath == "" {
		homePath = filepath.ToSlash(filepath.Join("homes", username))
	}
	homePath = filepath.ToSlash(filepath.Clean(homePath))
	if strings.HasPrefix(homePath, "..") || homePath == "." {
		return nil, fmt.Errorf("invalid home path")
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
	res, err := d.SQL.Exec(
		`INSERT INTO users(username, password_hash, is_admin, home_path, created_at) VALUES(?,?,?,?,?)`,
		username, hash, admin, homePath, now,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{ID: id, Username: username, IsAdmin: isAdmin, HomePath: homePath, CreatedAt: time.Now().UTC()}, nil
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
	_, _ = d.SQL.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().UTC().Format(time.RFC3339))
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

func (d *DB) SetShareGrant(shareID, userID int64, perm string) error {
	if perm != "read" && perm != "write" {
		return fmt.Errorf("perm must be read or write")
	}
	_, err := d.SQL.Exec(
		`INSERT INTO share_grants(share_id, user_id, perm) VALUES(?,?,?)
		 ON CONFLICT(share_id, user_id) DO UPDATE SET perm=excluded.perm`,
		shareID, userID, perm,
	)
	return err
}

func (d *DB) RemoveShareGrant(shareID, userID int64) error {
	_, err := d.SQL.Exec(`DELETE FROM share_grants WHERE share_id=? AND user_id=?`, shareID, userID)
	return err
}

type ShareAccess struct {
	Share Share
	Perm  string
}

func (d *DB) SharesForUser(userID int64, isAdmin bool) ([]ShareAccess, error) {
	if isAdmin {
		shares, err := d.ListShares()
		if err != nil {
			return nil, err
		}
		out := make([]ShareAccess, 0, len(shares))
		for _, s := range shares {
			out = append(out, ShareAccess{Share: s, Perm: "write"})
		}
		return out, nil
	}
	rows, err := d.SQL.Query(`
		SELECT s.id, s.name, s.path, s.created_by, s.created_at, g.perm
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
		if err := rows.Scan(&sa.Share.ID, &sa.Share.Name, &sa.Share.Path, &sa.Share.CreatedBy, &created, &sa.Perm); err != nil {
			return nil, err
		}
		sa.Share.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, sa)
	}
	return out, rows.Err()
}

func (d *DB) GrantPerm(shareID, userID int64) (string, error) {
	var perm string
	err := d.SQL.QueryRow(`SELECT perm FROM share_grants WHERE share_id=? AND user_id=?`, shareID, userID).Scan(&perm)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return perm, err
}

func (d *DB) ListGrants(shareID int64) ([]ShareGrant, error) {
	rows, err := d.SQL.Query(`SELECT share_id, user_id, perm FROM share_grants WHERE share_id=?`, shareID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ShareGrant, 0)
	for rows.Next() {
		var g ShareGrant
		if err := rows.Scan(&g.ShareID, &g.UserID, &g.Perm); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
