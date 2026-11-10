package db

import (
	"database/sql"
	"path/filepath"
	"strings"
)

// NormalizeDataRel returns a slash-separated path relative to DATA_ROOT.
func NormalizeDataRel(p string) string {
	p = filepath.ToSlash(filepath.Clean(p))
	p = strings.TrimPrefix(p, "/")
	if p == "." {
		return ""
	}
	return p
}

func likeEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

func (d *DB) ensureFileOwnerTable() error {
	_, err := d.SQL.Exec(`
CREATE TABLE IF NOT EXISTS file_owners (
  path TEXT PRIMARY KEY,
  owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  size INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_file_owners_owner ON file_owners(owner_id);
`)
	return err
}

// SetFileOwner attributes a data-root-relative file (not a directory) to a user.
func (d *DB) SetFileOwner(path string, ownerID, size int64) error {
	path = NormalizeDataRel(path)
	if path == "" || ownerID <= 0 {
		return nil
	}
	if size < 0 {
		size = 0
	}
	_, err := d.SQL.Exec(
		`INSERT INTO file_owners(path, owner_id, size) VALUES(?,?,?)
		 ON CONFLICT(path) DO UPDATE SET owner_id=excluded.owner_id, size=excluded.size`,
		path, ownerID, size,
	)
	return err
}

func (d *DB) GetFileOwner(path string) (ownerID, size int64, ok bool, err error) {
	path = NormalizeDataRel(path)
	err = d.SQL.QueryRow(`SELECT owner_id, size FROM file_owners WHERE path=?`, path).Scan(&ownerID, &size)
	if err == sql.ErrNoRows {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	return ownerID, size, true, nil
}

func (d *DB) DeleteFileOwner(path string) error {
	path = NormalizeDataRel(path)
	if path == "" {
		return nil
	}
	_, err := d.SQL.Exec(`DELETE FROM file_owners WHERE path=?`, path)
	return err
}

// DeleteFileOwnerPrefix removes the path and every descendant.
func (d *DB) DeleteFileOwnerPrefix(path string) error {
	path = NormalizeDataRel(path)
	if path == "" {
		return nil
	}
	_, err := d.SQL.Exec(
		`DELETE FROM file_owners WHERE path=? OR path LIKE ? ESCAPE '\'`,
		path, likeEscape(path)+`/%`,
	)
	return err
}

// RelocateFileOwnerPrefix rewrites path and descendants from oldPrefix to newPrefix.
func (d *DB) RelocateFileOwnerPrefix(oldPrefix, newPrefix string) error {
	oldPrefix = NormalizeDataRel(oldPrefix)
	newPrefix = NormalizeDataRel(newPrefix)
	if oldPrefix == "" || oldPrefix == newPrefix {
		return nil
	}
	rows, err := d.SQL.Query(
		`SELECT path, owner_id, size FROM file_owners WHERE path=? OR path LIKE ? ESCAPE '\'`,
		oldPrefix, likeEscape(oldPrefix)+`/%`,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		path    string
		ownerID int64
		size    int64
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.path, &r.ownerID, &r.size); err != nil {
			return err
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	tx, err := d.SQL.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, r := range list {
		if _, err := tx.Exec(`DELETE FROM file_owners WHERE path=?`, r.path); err != nil {
			return err
		}
		next := newPrefix
		if r.path != oldPrefix {
			rest := strings.TrimPrefix(r.path, oldPrefix+"/")
			next = NormalizeDataRel(newPrefix + "/" + rest)
		}
		if next == "" {
			continue
		}
		if _, err := tx.Exec(
			`INSERT INTO file_owners(path, owner_id, size) VALUES(?,?,?)
			 ON CONFLICT(path) DO UPDATE SET owner_id=excluded.owner_id, size=excluded.size`,
			next, r.ownerID, r.size,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SumOwnedBytes is the ledger total for a user (share writes + trash, never home).
func (d *DB) SumOwnedBytes(ownerID int64) (int64, error) {
	var n sql.NullInt64
	err := d.SQL.QueryRow(`SELECT COALESCE(SUM(size),0) FROM file_owners WHERE owner_id=?`, ownerID).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n.Int64, nil
}
