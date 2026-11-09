package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type LinkShare struct {
	ID           int64      `json:"id"`
	Token        string     `json:"token"`
	OwnerID      int64      `json:"ownerId"`
	RootID       string     `json:"rootId"`
	Path         string     `json:"path"`
	IsDir        bool       `json:"isDir"`
	Label        string     `json:"label"`
	HasPassword  bool       `json:"hasPassword"`
	PasswordHash string     `json:"-"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
	Revoked      bool       `json:"revoked"`
	CreatedAt    time.Time  `json:"createdAt"`
}

func (d *DB) ensureLinkShareTables() error {
	schema := `
CREATE TABLE IF NOT EXISTS link_shares (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  token TEXT NOT NULL UNIQUE,
  owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  root_id TEXT NOT NULL,
  path TEXT NOT NULL,
  is_dir INTEGER NOT NULL DEFAULT 0,
  label TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  expires_at TEXT,
  revoked INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_link_shares_owner ON link_shares(owner_id);
CREATE INDEX IF NOT EXISTS idx_link_shares_token ON link_shares(token);
CREATE TABLE IF NOT EXISTS link_unlocks (
  token TEXT PRIMARY KEY,
  link_id INTEGER NOT NULL REFERENCES link_shares(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_link_unlocks_link ON link_unlocks(link_id);
`
	_, err := d.SQL.Exec(schema)
	return err
}

func scanLinkShare(scanner interface {
	Scan(dest ...any) error
}) (*LinkShare, error) {
	var l LinkShare
	var isDir, revoked int
	var exp sql.NullString
	var created string
	err := scanner.Scan(
		&l.ID, &l.Token, &l.OwnerID, &l.RootID, &l.Path, &isDir, &l.Label,
		&l.PasswordHash, &exp, &revoked, &created,
	)
	if err != nil {
		return nil, err
	}
	l.IsDir = isDir == 1
	l.Revoked = revoked == 1
	l.HasPassword = l.PasswordHash != ""
	if exp.Valid && exp.String != "" {
		t, err := time.Parse(time.RFC3339, exp.String)
		if err == nil {
			l.ExpiresAt = &t
		}
	}
	l.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return &l, nil
}

const linkSelectCols = `id, token, owner_id, root_id, path, is_dir, label, password_hash, expires_at, revoked, created_at`

func (d *DB) CreateLinkShare(token string, ownerID int64, rootID, path string, isDir bool, label, passwordHash string, expiresAt *time.Time) (*LinkShare, error) {
	token = strings.TrimSpace(token)
	rootID = strings.TrimSpace(rootID)
	path = strings.TrimPrefix(strings.TrimSpace(path), "/")
	label = strings.TrimSpace(label)
	if token == "" || rootID == "" || path == "" {
		return nil, fmt.Errorf("token, root and path required")
	}
	if label == "" {
		parts := strings.Split(path, "/")
		label = parts[len(parts)-1]
	}
	dir := 0
	if isDir {
		dir = 1
	}
	var exp any
	if expiresAt != nil {
		exp = expiresAt.UTC().Format(time.RFC3339)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := d.SQL.Exec(
		`INSERT INTO link_shares(token, owner_id, root_id, path, is_dir, label, password_hash, expires_at, revoked, created_at)
		 VALUES(?,?,?,?,?,?,?,?,0,?)`,
		token, ownerID, rootID, path, dir, label, passwordHash, exp, now,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return d.GetLinkShareByID(id)
}

func (d *DB) GetLinkShareByID(id int64) (*LinkShare, error) {
	row := d.SQL.QueryRow(`SELECT `+linkSelectCols+` FROM link_shares WHERE id=?`, id)
	l, err := scanLinkShare(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return l, err
}

func (d *DB) GetLinkShareByToken(token string) (*LinkShare, error) {
	row := d.SQL.QueryRow(`SELECT `+linkSelectCols+` FROM link_shares WHERE token=?`, token)
	l, err := scanLinkShare(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return l, err
}

func (d *DB) ListLinkSharesForUser(ownerID int64, isAdmin bool) ([]LinkShare, error) {
	var rows *sql.Rows
	var err error
	if isAdmin {
		rows, err = d.SQL.Query(`SELECT ` + linkSelectCols + ` FROM link_shares ORDER BY created_at DESC`)
	} else {
		rows, err = d.SQL.Query(`SELECT `+linkSelectCols+` FROM link_shares WHERE owner_id=? ORDER BY created_at DESC`, ownerID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LinkShare, 0)
	for rows.Next() {
		l, err := scanLinkShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

func (d *DB) RevokeLinkShare(id int64) error {
	_, err := d.SQL.Exec(`UPDATE link_shares SET revoked=1 WHERE id=?`, id)
	return err
}

func (d *DB) DeleteLinkShare(id int64) error {
	_, err := d.SQL.Exec(`DELETE FROM link_shares WHERE id=?`, id)
	return err
}

func (l *LinkShare) IsExpired() bool {
	if l == nil {
		return true
	}
	if l.Revoked {
		return true
	}
	if l.ExpiresAt != nil && time.Now().After(*l.ExpiresAt) {
		return true
	}
	return false
}

func (d *DB) CreateLinkUnlock(token string, linkID int64, expires time.Time) error {
	_, err := d.SQL.Exec(
		`INSERT INTO link_unlocks(token, link_id, expires_at) VALUES(?,?,?)`,
		token, linkID, expires.UTC().Format(time.RFC3339),
	)
	return err
}

func (d *DB) GetLinkUnlock(token string) (linkID int64, ok bool, err error) {
	var expStr string
	err = d.SQL.QueryRow(`SELECT link_id, expires_at FROM link_unlocks WHERE token=?`, token).Scan(&linkID, &expStr)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	exp, err := time.Parse(time.RFC3339, expStr)
	if err != nil || time.Now().After(exp) {
		_, _ = d.SQL.Exec(`DELETE FROM link_unlocks WHERE token=?`, token)
		return 0, false, nil
	}
	return linkID, true, nil
}

func (d *DB) CleanupLinkUnlocks() {
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = d.SQL.Exec(`DELETE FROM link_unlocks WHERE expires_at < ?`, now)
}
