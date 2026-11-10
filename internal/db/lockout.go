package db

import (
	"database/sql"
	"strings"
	"time"
)

func normalizeLockUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func (d *DB) ensureLoginLockoutTable() error {
	_, err := d.SQL.Exec(`
CREATE TABLE IF NOT EXISTS login_lockouts (
  username TEXT PRIMARY KEY COLLATE NOCASE,
  failed_count INTEGER NOT NULL DEFAULT 0,
  locked_until TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL DEFAULT ''
);
`)
	return err
}

// LoginLockState reports whether the username is currently locked.
// Expired windows are treated as unlocked (the next failure starts a new count).
func (d *DB) LoginLockState(username string) (locked bool, until time.Time, err error) {
	username = normalizeLockUsername(username)
	if username == "" {
		return false, time.Time{}, nil
	}
	var untilStr string
	err = d.SQL.QueryRow(`SELECT locked_until FROM login_lockouts WHERE username=?`, username).Scan(&untilStr)
	if err == sql.ErrNoRows {
		return false, time.Time{}, nil
	}
	if err != nil {
		return false, time.Time{}, err
	}
	until = parseOptionalTime(untilStr)
	if until.IsZero() || !time.Now().Before(until) {
		return false, time.Time{}, nil
	}
	return true, until, nil
}

// RecordLoginFailure increments the failure count for username. If the
// account is already locked, the window is not extended and locked is true.
// After limit failures, a new window of duration window starts.
func (d *DB) RecordLoginFailure(username string, limit int, window time.Duration) (locked bool, until time.Time, err error) {
	username = normalizeLockUsername(username)
	if username == "" {
		return false, time.Time{}, nil
	}
	if limit <= 0 {
		limit = 10
	}
	if window <= 0 {
		window = 15 * time.Minute
	}
	now := time.Now()
	var count int
	var untilStr string
	err = d.SQL.QueryRow(`SELECT failed_count, locked_until FROM login_lockouts WHERE username=?`, username).Scan(&count, &untilStr)
	if err != nil && err != sql.ErrNoRows {
		return false, time.Time{}, err
	}
	missing := err == sql.ErrNoRows
	until = parseOptionalTime(untilStr)
	if !until.IsZero() && now.Before(until) {
		return true, until, nil
	}
	// Expired or no lock: start or continue the failure count.
	if !until.IsZero() && !now.Before(until) {
		count = 0
	}
	count++
	untilStr = ""
	if count >= limit {
		until = now.Add(window)
		untilStr = until.UTC().Format(time.RFC3339)
		locked = true
	}
	ts := now.UTC().Format(time.RFC3339)
	if missing {
		_, err = d.SQL.Exec(
			`INSERT INTO login_lockouts(username, failed_count, locked_until, updated_at) VALUES(?,?,?,?)`,
			username, count, untilStr, ts,
		)
	} else {
		_, err = d.SQL.Exec(
			`UPDATE login_lockouts SET failed_count=?, locked_until=?, updated_at=? WHERE username=?`,
			count, untilStr, ts, username,
		)
	}
	return locked, until, err
}

func (d *DB) ClearLoginFailures(username string) error {
	username = normalizeLockUsername(username)
	if username == "" {
		return nil
	}
	_, err := d.SQL.Exec(`DELETE FROM login_lockouts WHERE username=?`, username)
	return err
}
