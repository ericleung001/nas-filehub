package db

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	settingGlobalQuotaEnabled = "global_quota_enabled"
	settingGlobalQuotaBytes   = "global_quota_bytes"
)

// GlobalQuotaSettings is the admin policy that, when enabled, applies one
// storage quota to every user (including admins) and to new accounts.
type GlobalQuotaSettings struct {
	Enabled bool  `json:"globalQuotaEnabled"`
	Bytes   int64 `json:"globalQuotaBytes"` // 0 = unlimited
}

func (d *DB) GetGlobalQuotaSettings() (GlobalQuotaSettings, error) {
	var s GlobalQuotaSettings
	if v, err := d.GetSetting(settingGlobalQuotaEnabled); err != nil {
		return s, err
	} else {
		s.Enabled = v == "1" || strings.EqualFold(v, "true")
	}
	if v, err := d.GetSetting(settingGlobalQuotaBytes); err != nil {
		return s, err
	} else if v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return s, fmt.Errorf("invalid stored global quota")
		}
		s.Bytes = n
	}
	return s, nil
}

// EffectiveQuota returns the quota that should be enforced for u.
// When the all-users default is enabled and the user has no custom override,
// the default applies. Otherwise the stored per-user value is used (0 = unlimited).
func (d *DB) EffectiveQuota(u *User) int64 {
	if u == nil {
		return 0
	}
	if u.QuotaCustom {
		return u.QuotaBytes
	}
	gq, err := d.GetGlobalQuotaSettings()
	if err != nil || !gq.Enabled {
		return u.QuotaBytes
	}
	return gq.Bytes
}

// SetGlobalQuotaSettings persists the global quota policy in one transaction.
// When the resulting policy is enabled, users without a custom override have
// their stored quota_bytes updated to Bytes so the value remains if the default
// is later turned off. Users marked custom are left unchanged.
// When disabled, stored quotas are left as-is.
// A negative Bytes is rejected and no settings or user rows are written.
func (d *DB) SetGlobalQuotaSettings(enabled *bool, bytes *int64) (GlobalQuotaSettings, error) {
	cur, err := d.GetGlobalQuotaSettings()
	if err != nil {
		return cur, err
	}
	next := cur
	if bytes != nil {
		if *bytes < 0 {
			return cur, fmt.Errorf("invalid quota: quotaBytes must be >= 0")
		}
		next.Bytes = *bytes
	}
	if enabled != nil {
		next.Enabled = *enabled
	}

	tx, err := d.SQL.Begin()
	if err != nil {
		return cur, err
	}
	defer func() { _ = tx.Rollback() }()

	enVal := "0"
	if next.Enabled {
		enVal = "1"
	}
	if _, err := tx.Exec(
		`INSERT INTO settings(key, value) VALUES(?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		settingGlobalQuotaEnabled, enVal,
	); err != nil {
		return cur, err
	}
	if _, err := tx.Exec(
		`INSERT INTO settings(key, value) VALUES(?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		settingGlobalQuotaBytes, strconv.FormatInt(next.Bytes, 10),
	); err != nil {
		return cur, err
	}
	if next.Enabled {
		if _, err := tx.Exec(`UPDATE users SET quota_bytes=? WHERE quota_custom=0`, next.Bytes); err != nil {
			return cur, err
		}
	}
	if err := tx.Commit(); err != nil {
		return cur, err
	}
	return next, nil
}

// GlobalQuotaEnabled is a convenience wrapper around GetGlobalQuotaSettings.
func (d *DB) GlobalQuotaEnabled() (bool, error) {
	s, err := d.GetGlobalQuotaSettings()
	if err != nil {
		return false, err
	}
	return s.Enabled, nil
}
