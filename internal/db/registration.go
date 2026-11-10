package db

import (
	"fmt"
	"strings"
)

const (
	UserStatusActive   = "active"
	UserStatusPending  = "pending"
	UserStatusRejected = "rejected"
)

func NormalizeUserStatus(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case UserStatusPending, UserStatusRejected:
		return s
	default:
		return UserStatusActive
	}
}

func (d *DB) SetUserStatus(id int64, status string) error {
	status = NormalizeUserStatus(status)
	res, err := d.SQL.Exec(`UPDATE users SET status=? WHERE id=?`, status, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

// RegistrationRequireApproval defaults to true when the setting has never been stored.
func (d *DB) RegistrationRequireApproval() (bool, error) {
	v, err := d.GetSetting("registration_require_approval")
	if err != nil {
		return true, err
	}
	if v == "" {
		return true, nil
	}
	return v == "1" || strings.EqualFold(v, "true"), nil
}

func (d *DB) SetRegistrationRequireApproval(on bool) error {
	if on {
		return d.SetSetting("registration_require_approval", "1")
	}
	return d.SetSetting("registration_require_approval", "0")
}
