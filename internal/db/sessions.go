package db

import (
	"database/sql"
	"strings"
	"time"
)

func truncateMeta(s string, n int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\x00", "")
	if n > 0 && len(s) > n {
		return s[:n]
	}
	return s
}

func (d *DB) ensureSessionMetaColumns() error {
	cols := map[string]bool{}
	rows, err := d.SQL.Query(`PRAGMA table_info(sessions)`)
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
		{"created_at", `ALTER TABLE sessions ADD COLUMN created_at TEXT NOT NULL DEFAULT ''`},
		{"last_seen_at", `ALTER TABLE sessions ADD COLUMN last_seen_at TEXT NOT NULL DEFAULT ''`},
		{"ip", `ALTER TABLE sessions ADD COLUMN ip TEXT NOT NULL DEFAULT ''`},
		{"user_agent", `ALTER TABLE sessions ADD COLUMN user_agent TEXT NOT NULL DEFAULT ''`},
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

// UserSession is a session listing row. Token is never serialized.
type UserSession struct {
	ID         int64     `json:"id"`
	CreatedAt  time.Time `json:"createdAt,omitempty"`
	LastSeenAt time.Time `json:"lastSeenAt,omitempty"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"userAgent"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Current    bool      `json:"current"`
	Token      string    `json:"-"`
}

func parseOptionalTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (d *DB) ListUserSessions(userID int64) ([]UserSession, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := d.SQL.Query(
		`SELECT rowid, token, created_at, last_seen_at, ip, user_agent, expires_at
		 FROM sessions WHERE user_id=? AND expires_at >= ? ORDER BY last_seen_at DESC, rowid DESC`,
		userID, now,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserSession
	for rows.Next() {
		var s UserSession
		var created, seen, exp string
		if err := rows.Scan(&s.ID, &s.Token, &created, &seen, &s.IP, &s.UserAgent, &exp); err != nil {
			return nil, err
		}
		s.CreatedAt = parseOptionalTime(created)
		s.LastSeenAt = parseOptionalTime(seen)
		s.ExpiresAt = parseOptionalTime(exp)
		out = append(out, s)
	}
	if out == nil {
		out = []UserSession{}
	}
	return out, rows.Err()
}

func (d *DB) DeleteSessionByID(userID, id int64) (bool, error) {
	res, err := d.SQL.Exec(`DELETE FROM sessions WHERE user_id=? AND rowid=?`, userID, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (d *DB) DeleteOtherSessions(userID int64, keepToken string) error {
	if keepToken == "" {
		return d.DeleteUserSessions(userID)
	}
	_, err := d.SQL.Exec(`DELETE FROM sessions WHERE user_id=? AND token!=?`, userID, keepToken)
	return err
}

func (d *DB) SessionIDByToken(token string) (int64, error) {
	var id int64
	err := d.SQL.QueryRow(`SELECT rowid FROM sessions WHERE token=?`, token).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// TouchSession updates last-seen (and IP / user agent when provided).
// Updates are skipped when last_seen_at is less than a minute old and metadata is unchanged,
// to avoid a write on every authenticated request.
func (d *DB) TouchSession(token, ip, userAgent string) {
	if token == "" {
		return
	}
	ip = truncateMeta(ip, 128)
	userAgent = truncateMeta(userAgent, 512)
	var seen, curIP, curUA string
	err := d.SQL.QueryRow(`SELECT last_seen_at, ip, user_agent FROM sessions WHERE token=?`, token).Scan(&seen, &curIP, &curUA)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	if t := parseOptionalTime(seen); !t.IsZero() && now.Sub(t) < time.Minute && (ip == "" || ip == curIP) && (userAgent == "" || userAgent == curUA) {
		return
	}
	if ip == "" {
		ip = curIP
	}
	if userAgent == "" {
		userAgent = curUA
	}
	_, _ = d.SQL.Exec(`UPDATE sessions SET last_seen_at=?, ip=?, user_agent=? WHERE token=?`, now.Format(time.RFC3339), ip, userAgent, token)
}
