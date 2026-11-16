package db

import (
	"fmt"
	"net/url"
	"strings"
)

// OnlyOfficeSettings is admin-configured ONLYOFFICE Docs (JWT secret never returned in Public()).
type OnlyOfficeSettings struct {
	Enabled   bool   `json:"enabled"`
	URL       string `json:"url"`
	JWTSecret string `json:"-"`
	// JWTSecretSet is true when a secret is stored (for UI); never the secret itself.
	JWTSecretSet bool `json:"jwtSecretSet"`
}

const (
	settingOnlyOfficeEnabled   = "onlyoffice_enabled"
	settingOnlyOfficeURL       = "onlyoffice_url"
	settingOnlyOfficeJWTSecret = "onlyoffice_jwt_secret"
)

func DefaultOnlyOfficeSettings() OnlyOfficeSettings {
	return OnlyOfficeSettings{}
}

func NormalizeOnlyOfficeURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func (d *DB) GetOnlyOfficeSettings() (OnlyOfficeSettings, error) {
	s := DefaultOnlyOfficeSettings()
	if v, err := d.GetSetting(settingOnlyOfficeEnabled); err != nil {
		return s, err
	} else {
		s.Enabled = v == "1" || strings.EqualFold(v, "true")
	}
	if v, err := d.GetSetting(settingOnlyOfficeURL); err != nil {
		return s, err
	} else {
		s.URL = NormalizeOnlyOfficeURL(v)
	}
	if v, err := d.GetSetting(settingOnlyOfficeJWTSecret); err != nil {
		return s, err
	} else {
		s.JWTSecret = v
		s.JWTSecretSet = v != ""
	}
	return s, nil
}

// SetOnlyOfficeSettings writes ONLYOFFICE fields. JWT secret: nil = leave unchanged; "" = clear; otherwise set.
func (d *DB) SetOnlyOfficeSettings(in OnlyOfficeSettings, jwtSecret *string) (OnlyOfficeSettings, error) {
	in.URL = NormalizeOnlyOfficeURL(in.URL)
	if err := d.SetSetting(settingOnlyOfficeEnabled, boolSetting(in.Enabled)); err != nil {
		return OnlyOfficeSettings{}, err
	}
	if err := d.SetSetting(settingOnlyOfficeURL, in.URL); err != nil {
		return OnlyOfficeSettings{}, err
	}
	if jwtSecret != nil {
		if err := d.SetSetting(settingOnlyOfficeJWTSecret, *jwtSecret); err != nil {
			return OnlyOfficeSettings{}, err
		}
	}
	return d.GetOnlyOfficeSettings()
}

// Ready reports whether the office editor can be offered (enabled + Document Server URL + JWT secret).
// Callers must also require PUBLIC_BASE_URL.
func (s OnlyOfficeSettings) Ready() bool {
	return s.Enabled && s.URL != "" && s.JWTSecret != ""
}

// Origin returns scheme://host[:port] for CSP, or empty if URL is invalid.
func (s OnlyOfficeSettings) Origin() string {
	u, err := url.Parse(s.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func ValidateOnlyOfficeURL(raw string) error {
	raw = NormalizeOnlyOfficeURL(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Fragment != "" {
		return fmt.Errorf("invalid ONLYOFFICE Document Server URL")
	}
	return nil
}
