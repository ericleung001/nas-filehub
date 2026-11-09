package db

import (
	"fmt"
	"strconv"
	"strings"
)

// SMTPSettings is admin-configured outbound mail (password never returned in Public()).
type SMTPSettings struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"-"`
	From     string `json:"from"`
	// PasswordSet is true when a password is stored (for UI); never the secret itself.
	PasswordSet bool `json:"passwordSet"`
}

const (
	settingSMTPEnabled  = "smtp_enabled"
	settingSMTPHost     = "smtp_host"
	settingSMTPPort     = "smtp_port"
	settingSMTPUsername = "smtp_username"
	settingSMTPPassword = "smtp_password"
	settingSMTPFrom     = "smtp_from"
)

func DefaultSMTPSettings() SMTPSettings {
	return SMTPSettings{Port: 587}
}

func (d *DB) GetSMTPSettings() (SMTPSettings, error) {
	s := DefaultSMTPSettings()
	if v, err := d.GetSetting(settingSMTPEnabled); err != nil {
		return s, err
	} else {
		s.Enabled = v == "1" || strings.EqualFold(v, "true")
	}
	if v, err := d.GetSetting(settingSMTPHost); err != nil {
		return s, err
	} else {
		s.Host = v
	}
	if v, err := d.GetSetting(settingSMTPPort); err != nil {
		return s, err
	} else if v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 65535 {
			s.Port = n
		}
	}
	if v, err := d.GetSetting(settingSMTPUsername); err != nil {
		return s, err
	} else {
		s.Username = v
	}
	if v, err := d.GetSetting(settingSMTPPassword); err != nil {
		return s, err
	} else {
		s.Password = v
		s.PasswordSet = v != ""
	}
	if v, err := d.GetSetting(settingSMTPFrom); err != nil {
		return s, err
	} else {
		s.From = v
	}
	return s, nil
}

// SetSMTPSettings writes SMTP fields. Password: nil = leave unchanged; "" = clear; otherwise set.
func (d *DB) SetSMTPSettings(in SMTPSettings, password *string) (SMTPSettings, error) {
	if in.Port <= 0 {
		in.Port = 587
	}
	if in.Port > 65535 {
		return SMTPSettings{}, fmt.Errorf("invalid SMTP port")
	}
	if err := d.SetSetting(settingSMTPEnabled, boolSetting(in.Enabled)); err != nil {
		return SMTPSettings{}, err
	}
	if err := d.SetSetting(settingSMTPHost, strings.TrimSpace(in.Host)); err != nil {
		return SMTPSettings{}, err
	}
	if err := d.SetSetting(settingSMTPPort, strconv.Itoa(in.Port)); err != nil {
		return SMTPSettings{}, err
	}
	if err := d.SetSetting(settingSMTPUsername, strings.TrimSpace(in.Username)); err != nil {
		return SMTPSettings{}, err
	}
	if err := d.SetSetting(settingSMTPFrom, strings.TrimSpace(in.From)); err != nil {
		return SMTPSettings{}, err
	}
	if password != nil {
		if err := d.SetSetting(settingSMTPPassword, *password); err != nil {
			return SMTPSettings{}, err
		}
	}
	return d.GetSMTPSettings()
}

// SMTPReady reports whether outbound mail can be used (enabled + host + from).
func (s SMTPSettings) Ready() bool {
	return s.Enabled && strings.TrimSpace(s.Host) != "" && strings.TrimSpace(s.From) != "" && s.Port > 0
}
