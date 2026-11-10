package db

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	settingSessionTTLSeconds    = "session_ttl_seconds"
	settingRememberMeTTLSeconds = "remember_me_ttl_seconds"
	settingAllowRememberMe      = "allow_remember_me"

	DefaultSessionTTLSeconds    = 7 * 24 * 60 * 60   // 7 days
	DefaultRememberMeTTLSeconds = 30 * 24 * 60 * 60  // 30 days
	minSessionTTLSeconds        = 60 * 60            // 1 hour
	maxSessionTTLSeconds        = 365 * 24 * 60 * 60 // 365 days
)

// SessionSettings is the admin-configurable session lifetime policy.
type SessionSettings struct {
	SessionTTLSeconds    int  `json:"sessionTtlSeconds"`
	RememberMeTTLSeconds int  `json:"rememberMeTtlSeconds"`
	AllowRememberMe      bool `json:"allowRememberMe"`
}

func DefaultSessionSettings() SessionSettings {
	return SessionSettings{
		SessionTTLSeconds:    DefaultSessionTTLSeconds,
		RememberMeTTLSeconds: DefaultRememberMeTTLSeconds,
		AllowRememberMe:      true,
	}
}

func (d *DB) GetSessionSettings() (SessionSettings, error) {
	s := DefaultSessionSettings()
	if v, err := d.GetSetting(settingSessionTTLSeconds); err != nil {
		return s, err
	} else if v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			s.SessionTTLSeconds = n
		}
	}
	if v, err := d.GetSetting(settingRememberMeTTLSeconds); err != nil {
		return s, err
	} else if v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			s.RememberMeTTLSeconds = n
		}
	}
	if v, err := d.GetSetting(settingAllowRememberMe); err != nil {
		return s, err
	} else if v != "" {
		s.AllowRememberMe = v == "1" || strings.EqualFold(v, "true")
	}
	return s, nil
}

// ValidateSessionSettings checks bounds. Remember-me TTL must be >= session TTL
// (and at least 1 hour). Returns a user-facing error string suitable for HTTP 400.
func ValidateSessionSettings(s SessionSettings) error {
	if s.SessionTTLSeconds < minSessionTTLSeconds {
		return fmt.Errorf("session TTL must be at least 1 hour (%d seconds)", minSessionTTLSeconds)
	}
	if s.SessionTTLSeconds > maxSessionTTLSeconds {
		return fmt.Errorf("session TTL must be at most 365 days (%d seconds)", maxSessionTTLSeconds)
	}
	if s.RememberMeTTLSeconds < minSessionTTLSeconds {
		return fmt.Errorf("stay-signed-in TTL must be at least 1 hour (%d seconds)", minSessionTTLSeconds)
	}
	if s.RememberMeTTLSeconds > maxSessionTTLSeconds {
		return fmt.Errorf("stay-signed-in TTL must be at most 365 days (%d seconds)", maxSessionTTLSeconds)
	}
	if s.RememberMeTTLSeconds < s.SessionTTLSeconds {
		return fmt.Errorf("stay-signed-in TTL must be greater than or equal to session TTL")
	}
	return nil
}

func (d *DB) SetSessionSettings(s SessionSettings) error {
	if err := ValidateSessionSettings(s); err != nil {
		return err
	}
	if err := d.SetSetting(settingSessionTTLSeconds, strconv.Itoa(s.SessionTTLSeconds)); err != nil {
		return err
	}
	if err := d.SetSetting(settingRememberMeTTLSeconds, strconv.Itoa(s.RememberMeTTLSeconds)); err != nil {
		return err
	}
	flag := "0"
	if s.AllowRememberMe {
		flag = "1"
	}
	return d.SetSetting(settingAllowRememberMe, flag)
}

// SessionDuration returns the TTL for a new login. When rememberMe is true and
// allow_remember_me is enabled, the longer stay-signed-in TTL is used.
func (s SessionSettings) SessionDuration(rememberMe bool) time.Duration {
	if rememberMe && s.AllowRememberMe {
		return time.Duration(s.RememberMeTTLSeconds) * time.Second
	}
	return time.Duration(s.SessionTTLSeconds) * time.Second
}
