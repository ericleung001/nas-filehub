package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// ItemShare is a user-to-user grant on a specific file or folder.
// Access is resolved through the owner's root and path (same idea as a public link).
type ItemShare struct {
	ID        int64     `json:"id"`
	OwnerID   int64     `json:"ownerId"`
	OwnerName string    `json:"ownerName,omitempty"`
	UserID    int64     `json:"userId"`
	Username  string    `json:"username,omitempty"`
	RootID    string    `json:"rootId"`
	Path      string    `json:"path"`
	IsDir     bool      `json:"isDir"`
	Label     string    `json:"label"`
	Perm      string    `json:"perm"`
	CreatedAt time.Time `json:"createdAt"`
}

func (d *DB) ensureItemShareTables() error {
	_, err := d.SQL.Exec(`
CREATE TABLE IF NOT EXISTS item_shares (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  root_id TEXT NOT NULL,
  path TEXT NOT NULL,
  is_dir INTEGER NOT NULL DEFAULT 0,
  label TEXT NOT NULL DEFAULT '',
  perm TEXT NOT NULL DEFAULT 'read' CHECK(perm IN ('read','write')),
  created_at TEXT NOT NULL,
  UNIQUE(owner_id, root_id, path, user_id)
);
CREATE INDEX IF NOT EXISTS idx_item_shares_user ON item_shares(user_id);
CREATE INDEX IF NOT EXISTS idx_item_shares_owner ON item_shares(owner_id);
CREATE INDEX IF NOT EXISTS idx_item_shares_target ON item_shares(root_id, path);
`)
	return err
}

const itemShareSelect = `
SELECT s.id, s.owner_id, COALESCE(o.username, ''), s.user_id, COALESCE(u.username, ''),
       s.root_id, s.path, s.is_dir, s.label, s.perm, s.created_at
FROM item_shares s
LEFT JOIN users o ON o.id = s.owner_id
LEFT JOIN users u ON u.id = s.user_id`

func scanItemShare(scanner interface {
	Scan(dest ...any) error
}) (*ItemShare, error) {
	var s ItemShare
	var isDir int
	var created string
	if err := scanner.Scan(&s.ID, &s.OwnerID, &s.OwnerName, &s.UserID, &s.Username, &s.RootID, &s.Path, &isDir, &s.Label, &s.Perm, &created); err != nil {
		return nil, err
	}
	s.IsDir = isDir == 1
	s.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return &s, nil
}

func normalizeItemSharePath(path string) string {
	return strings.TrimPrefix(strings.TrimSpace(strings.ReplaceAll(path, "\\", "/")), "/")
}

func (d *DB) UpsertItemShare(ownerID, userID int64, rootID, path string, isDir bool, label, perm string) (*ItemShare, error) {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		rootID = "home"
	}
	path = normalizeItemSharePath(path)
	label = strings.TrimSpace(label)
	perm = strings.ToLower(strings.TrimSpace(perm))
	if ownerID <= 0 || userID <= 0 || path == "" {
		return nil, fmt.Errorf("owner, user and path required")
	}
	if ownerID == userID {
		return nil, fmt.Errorf("cannot share with yourself")
	}
	if perm == "" {
		perm = "read"
	}
	if perm != "read" && perm != "write" {
		return nil, fmt.Errorf("perm must be read or write")
	}
	if !isDir {
		perm = "read"
	}
	if label == "" {
		parts := strings.Split(path, "/")
		label = parts[len(parts)-1]
	}
	dir := 0
	if isDir {
		dir = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := d.SQL.Exec(
		`INSERT INTO item_shares(owner_id, user_id, root_id, path, is_dir, label, perm, created_at)
		 VALUES(?,?,?,?,?,?,?,?)
		 ON CONFLICT(owner_id, root_id, path, user_id) DO UPDATE SET
		   is_dir=excluded.is_dir, label=excluded.label, perm=excluded.perm`,
		ownerID, userID, rootID, path, dir, label, perm, now,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		// conflict update may not report last insert id
		row := d.SQL.QueryRow(
			`SELECT id FROM item_shares WHERE owner_id=? AND root_id=? AND path=? AND user_id=?`,
			ownerID, rootID, path, userID,
		)
		if err := row.Scan(&id); err != nil {
			return nil, err
		}
	}
	return d.GetItemShare(id)
}

func (d *DB) GetItemShare(id int64) (*ItemShare, error) {
	row := d.SQL.QueryRow(itemShareSelect+` WHERE s.id=?`, id)
	s, err := scanItemShare(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return s, err
}

func collectItemShares(rows *sql.Rows, err error) ([]ItemShare, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ItemShare, 0)
	for rows.Next() {
		s, err := scanItemShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (d *DB) ListItemSharesForPath(ownerID int64, rootID, path string) ([]ItemShare, error) {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		rootID = "home"
	}
	path = normalizeItemSharePath(path)
	return collectItemShares(d.SQL.Query(itemShareSelect+` WHERE s.owner_id=? AND s.root_id=? AND s.path=? ORDER BY u.username`, ownerID, rootID, path))
}

func (d *DB) ListIncomingItemShares(userID int64) ([]ItemShare, error) {
	return collectItemShares(d.SQL.Query(itemShareSelect+` WHERE s.user_id=? ORDER BY s.created_at DESC, s.id DESC`, userID))
}

func (d *DB) DeleteItemShare(id int64) error {
	_, err := d.SQL.Exec(`DELETE FROM item_shares WHERE id=?`, id)
	return err
}

func (d *DB) DeleteItemSharesForPath(rootID, path string) (int64, error) {
	rootID = strings.TrimSpace(rootID)
	path = normalizeItemSharePath(path)
	if rootID == "" || path == "" {
		return 0, nil
	}
	res, err := d.SQL.Exec(
		`DELETE FROM item_shares WHERE root_id=? AND (path=? OR path LIKE ?)`,
		rootID, path, path+"/%",
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (d *DB) RelocateItemSharesForPath(rootID, from, to string) error {
	rootID = strings.TrimSpace(rootID)
	from = normalizeItemSharePath(from)
	to = normalizeItemSharePath(to)
	if rootID == "" || from == "" || to == "" || from == to {
		return nil
	}
	_, err := d.SQL.Exec(
		`UPDATE item_shares SET path=? WHERE root_id=? AND path=?`,
		to, rootID, from,
	)
	if err != nil {
		return err
	}
	prefix := from + "/"
	rows, err := d.SQL.Query(`SELECT id, path FROM item_shares WHERE root_id=? AND path LIKE ?`, rootID, prefix+"%")
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		id   int64
		path string
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.path); err != nil {
			return err
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range list {
		next := to + strings.TrimPrefix(r.path, from)
		if _, err := d.SQL.Exec(`UPDATE item_shares SET path=? WHERE id=?`, strings.TrimPrefix(next, "/"), r.id); err != nil {
			return err
		}
		_ = filepath.ToSlash(next)
	}
	return nil
}

// DirectoryUser is a safe listing row for the share-with-user picker.
type DirectoryUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

func (d *DB) ListDirectoryUsers(excludeID int64) ([]DirectoryUser, error) {
	rows, err := d.SQL.Query(
		`SELECT id, username FROM users
		 WHERE disabled=0 AND (status='active' OR status='' OR status IS NULL) AND id!=?
		 ORDER BY username COLLATE NOCASE`,
		excludeID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]DirectoryUser, 0)
	for rows.Next() {
		var u DirectoryUser
		if err := rows.Scan(&u.ID, &u.Username); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
