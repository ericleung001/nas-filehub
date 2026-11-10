package db

import (
	"strings"
	"time"
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
	_, err := d.SQL.Exec(
		`INSERT INTO audit_log(created_at, actor_id, actor_username, ip, action, detail) VALUES(?,?,?,?,?,?)`,
		now, actorID, actorUsername, ip, action, detail,
	)
	return err
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
