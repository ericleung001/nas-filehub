package db

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

var errAlbumPath = errors.New("invalid album path")

// Album is a folder marked as a photo album. It is a property of the folder
// (root_id + clean relative path), not a personal bookmark. Home albums are
// scoped by the home owner's id so two users can both have a folder named
// Photos. Admin-share albums use owner_id 0 and root_id "share:N".
type Album struct {
	OwnerID   int64     `json:"ownerId"`
	RootID    string    `json:"rootId"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"createdAt"`
}

func (d *DB) ensureAlbumTable() error {
	_, err := d.SQL.Exec(`
CREATE TABLE IF NOT EXISTS albums (
  owner_id INTEGER NOT NULL DEFAULT 0,
  root_id TEXT NOT NULL,
  path TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (owner_id, root_id, path)
);
CREATE INDEX IF NOT EXISTS idx_albums_root ON albums(root_id, path);
`)
	return err
}

// NormalizeAlbumPath trims and slash-normalizes a scope-relative path, matching
// the item-share / link-share path model used elsewhere in the app.
func NormalizeAlbumPath(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	path = strings.Trim(path, "/")
	if path == "." {
		return ""
	}
	return path
}

func normalizeAlbumRoot(rootID string) string {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return "home"
	}
	return rootID
}

func (d *DB) SetAlbum(ownerID int64, rootID, path string) error {
	rootID = normalizeAlbumRoot(rootID)
	path = NormalizeAlbumPath(path)
	if rootID == "" || rootID == "trash" || rootID == "incoming" {
		return errAlbumPath
	}
	if strings.HasPrefix(rootID, "trash") {
		return errAlbumPath
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.SQL.Exec(
		`INSERT INTO albums(owner_id, root_id, path, created_at) VALUES(?,?,?,?)
		 ON CONFLICT(owner_id, root_id, path) DO NOTHING`,
		ownerID, rootID, path, now,
	)
	return err
}

func (d *DB) DeleteAlbum(ownerID int64, rootID, path string) error {
	rootID = normalizeAlbumRoot(rootID)
	path = NormalizeAlbumPath(path)
	_, err := d.SQL.Exec(
		`DELETE FROM albums WHERE owner_id=? AND root_id=? AND path=?`,
		ownerID, rootID, path,
	)
	return err
}

func (d *DB) IsAlbum(ownerID int64, rootID, path string) (bool, error) {
	rootID = normalizeAlbumRoot(rootID)
	path = NormalizeAlbumPath(path)
	var n int
	err := d.SQL.QueryRow(
		`SELECT COUNT(*) FROM albums WHERE owner_id=? AND root_id=? AND path=?`,
		ownerID, rootID, path,
	).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (d *DB) GetAlbum(ownerID int64, rootID, path string) (*Album, error) {
	rootID = normalizeAlbumRoot(rootID)
	path = NormalizeAlbumPath(path)
	row := d.SQL.QueryRow(
		`SELECT owner_id, root_id, path, created_at FROM albums WHERE owner_id=? AND root_id=? AND path=?`,
		ownerID, rootID, path,
	)
	a, err := scanAlbum(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}

func (d *DB) ListAlbums() ([]Album, error) {
	rows, err := d.SQL.Query(`SELECT owner_id, root_id, path, created_at FROM albums ORDER BY root_id, path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Album, 0)
	for rows.Next() {
		a, err := scanAlbum(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// DeleteAlbumsForPath removes the album flag on path and any album flagged under it.
func (d *DB) DeleteAlbumsForPath(ownerID int64, rootID, path string) (int64, error) {
	rootID = normalizeAlbumRoot(rootID)
	path = NormalizeAlbumPath(path)
	if rootID == "" {
		return 0, nil
	}
	var res sql.Result
	var err error
	if path == "" {
		res, err = d.SQL.Exec(`DELETE FROM albums WHERE owner_id=? AND root_id=?`, ownerID, rootID)
	} else {
		res, err = d.SQL.Exec(
			`DELETE FROM albums WHERE owner_id=? AND root_id=? AND (path=? OR path LIKE ?)`,
			ownerID, rootID, path, path+"/%",
		)
	}
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// RelocateAlbumsForPath updates album flags when a folder is renamed or moved
// within the same root, including flags on nested folders.
func (d *DB) RelocateAlbumsForPath(ownerID int64, rootID, from, to string) error {
	rootID = normalizeAlbumRoot(rootID)
	from = NormalizeAlbumPath(from)
	to = NormalizeAlbumPath(to)
	if rootID == "" || from == to {
		return nil
	}
	// Exact path (the folder itself).
	if _, err := d.SQL.Exec(
		`UPDATE albums SET path=? WHERE owner_id=? AND root_id=? AND path=?`,
		to, ownerID, rootID, from,
	); err != nil {
		return err
	}
	if from == "" {
		return nil
	}
	prefix := from + "/"
	rows, err := d.SQL.Query(
		`SELECT path FROM albums WHERE owner_id=? AND root_id=? AND path LIKE ?`,
		ownerID, rootID, prefix+"%",
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	var kids []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return err
		}
		kids = append(kids, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range kids {
		next := to
		if to != "" {
			next = to + strings.TrimPrefix(p, from)
		} else {
			next = strings.TrimPrefix(p, from+"/")
		}
		next = NormalizeAlbumPath(next)
		if _, err := d.SQL.Exec(
			`UPDATE albums SET path=? WHERE owner_id=? AND root_id=? AND path=?`,
			next, ownerID, rootID, p,
		); err != nil {
			return err
		}
	}
	return nil
}

func scanAlbum(scanner interface {
	Scan(dest ...any) error
}) (*Album, error) {
	var a Album
	var created string
	if err := scanner.Scan(&a.OwnerID, &a.RootID, &a.Path, &created); err != nil {
		return nil, err
	}
	a.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return &a, nil
}
