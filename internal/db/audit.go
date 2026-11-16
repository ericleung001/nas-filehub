package db

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	settingAuditLogMax = "audit_log_max"

	// DefaultAuditLogMax is unlimited so existing installs keep their history.
	DefaultAuditLogMax = 0
	// MaxAuditLogMax is the largest cap an admin may set (guards typo values).
	MaxAuditLogMax = 1_000_000
)

type AuditEvent struct {
	ID            int64     `json:"id"`
	CreatedAt     time.Time `json:"createdAt"`
	ActorID       int64     `json:"actorId,omitempty"`
	ActorUsername string    `json:"actorUsername"`
	IP            string    `json:"ip"`
	Action        string    `json:"action"`
	Detail        string    `json:"detail"`
}

func (d *DB) ensureAuditLogTable() error {
	_, err := d.SQL.Exec(`
CREATE TABLE IF NOT EXISTS audit_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at TEXT NOT NULL,
  actor_id INTEGER NOT NULL DEFAULT 0,
  actor_username TEXT NOT NULL DEFAULT '',
  ip TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_log(created_at DESC);
`)
	return err
}

func (d *DB) AppendAudit(actorID int64, actorUsername, ip, action, detail string) error {
	action = strings.TrimSpace(action)
	if action == "" {
		return nil
	}
	actorUsername = truncateMeta(actorUsername, 128)
	ip = truncateMeta(ip, 128)
	detail = truncateMeta(detail, 512)
	now := time.Now().UTC().Format(time.RFC3339)

	max, err := d.GetAuditLogMax()
	if err != nil {
		return err
	}

	tx, err := d.SQL.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`INSERT INTO audit_log(created_at, actor_id, actor_username, ip, action, detail) VALUES(?,?,?,?,?,?)`,
		now, actorID, actorUsername, ip, action, detail,
	); err != nil {
		return err
	}
	if err := pruneAuditLogTx(tx, max); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) ListAudit(limit, offset int) ([]AuditEvent, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := d.SQL.QueryRow(`SELECT COUNT(*) FROM audit_log`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := d.SQL.Query(
		`SELECT id, created_at, actor_id, actor_username, ip, action, detail
		 FROM audit_log ORDER BY id DESC LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var e AuditEvent
		var created string
		if err := rows.Scan(&e.ID, &created, &e.ActorID, &e.ActorUsername, &e.IP, &e.Action, &e.Detail); err != nil {
			return nil, 0, err
		}
		e.CreatedAt = parseOptionalTime(created)
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// GetAuditLogMax returns the configured cap (0 = unlimited).
// Missing, empty, or unparseable stored values are treated as unlimited so a
// corrupt setting cannot wipe the log. Values above MaxAuditLogMax are clamped
// when read.
func (d *DB) GetAuditLogMax() (int, error) {
	v, err := d.GetSetting(settingAuditLogMax)
	if err != nil {
		return 0, err
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return DefaultAuditLogMax, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return DefaultAuditLogMax, nil
	}
	if n > MaxAuditLogMax {
		return MaxAuditLogMax, nil
	}
	return n, nil
}

// ValidateAuditLogMax rejects negative values and typo-sized caps.
func ValidateAuditLogMax(n int) error {
	if n < 0 {
		return fmt.Errorf("audit log limit must be 0 or a positive integer")
	}
	if n > MaxAuditLogMax {
		return fmt.Errorf("audit log limit must be at most %d", MaxAuditLogMax)
	}
	return nil
}

// SetAuditLogMax persists the cap (0 = unlimited) and immediately prunes
// oldest events when the current log is larger than the new cap.
func (d *DB) SetAuditLogMax(n int) error {
	if err := ValidateAuditLogMax(n); err != nil {
		return err
	}
	tx, err := d.SQL.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(
		`INSERT INTO settings(key, value) VALUES(?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		settingAuditLogMax, strconv.Itoa(n),
	); err != nil {
		return err
	}
	if err := pruneAuditLogTx(tx, n); err != nil {
		return err
	}
	return tx.Commit()
}

// PruneAuditLog deletes the oldest events when a positive cap is set and the
// log is over that cap. A cap of 0 is a no-op.
func (d *DB) PruneAuditLog() error {
	max, err := d.GetAuditLogMax()
	if err != nil {
		return err
	}
	tx, err := d.SQL.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := pruneAuditLogTx(tx, max); err != nil {
		return err
	}
	return tx.Commit()
}

func pruneAuditLogTx(tx *sql.Tx, max int) error {
	if max <= 0 {
		return nil
	}
	if max > MaxAuditLogMax {
		max = MaxAuditLogMax
	}
	// Keep the newest max rows (highest id). The derived table avoids
	// SQLite "same table" delete/subquery issues.
	_, err := tx.Exec(`
		DELETE FROM audit_log
		WHERE id < (
			SELECT MIN(id) FROM (
				SELECT id FROM audit_log ORDER BY id DESC LIMIT ?
			)
		)`, max)
	return err
}
