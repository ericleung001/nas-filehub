package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (d *DB) ensurePasswordResetTable() error {
	_, err := d.SQL.Exec(`
CREATE TABLE IF NOT EXISTS password_resets (
  token TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_password_resets_user ON password_resets(user_id);
`)
	return err
}

// CreatePasswordReset stores a one-time reset token (replaces any prior tokens for the user).
func (d *DB) CreatePasswordReset(token string, userID int64, expires time.Time) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("token required")
	}
	_, _ = d.SQL.Exec(`DELETE FROM password_resets WHERE user_id=?`, userID)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.SQL.Exec(
		`INSERT INTO password_resets(token, user_id, expires_at, created_at) VALUES(?,?,?,?)`,
		token, userID, expires.UTC().Format(time.RFC3339), now,
	)
	return err
}

func (d *DB) GetPasswordResetUser(token string) (*User, error) {
	var userID int64
	var expStr string
	err := d.SQL.QueryRow(
		`SELECT user_id, expires_at FROM password_resets WHERE token=?`, token,
	).Scan(&userID, &expStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	exp, err := time.Parse(time.RFC3339, expStr)
	if err != nil || time.Now().After(exp) {
		_, _ = d.SQL.Exec(`DELETE FROM password_resets WHERE token=?`, token)
		return nil, nil
	}
	return d.GetUserByID(userID)
}

func (d *DB) DeletePasswordReset(token string) error {
	_, err := d.SQL.Exec(`DELETE FROM password_resets WHERE token=?`, token)
	return err
}

func (d *DB) DeletePasswordResetsForUser(userID int64) error {
	_, err := d.SQL.Exec(`DELETE FROM password_resets WHERE user_id=?`, userID)
	return err
}

func (d *DB) CleanupPasswordResets() {
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = d.SQL.Exec(`DELETE FROM password_resets WHERE expires_at < ?`, now)
}

// GetUserByEmail returns a user by email (case-insensitive). Empty email → nil.
func (d *DB) GetUserByEmail(email string) (*User, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, nil
	}
	row := d.SQL.QueryRow(
		`SELECT `+userSelectCols+` FROM users WHERE email=? COLLATE NOCASE AND email!=''`,
		email,
	)
	u, err := scanUser(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

// FindUserByUsernameOrEmail looks up by username first, then email.
func (d *DB) FindUserByUsernameOrEmail(id string) (*User, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	u, err := d.GetUserByUsername(id)
	if err != nil {
		return nil, err
	}
	if u != nil {
		return u, nil
	}
	return d.GetUserByEmail(id)
}
