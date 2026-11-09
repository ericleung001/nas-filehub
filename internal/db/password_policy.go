package db

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// PasswordPolicy is the admin-configured password rules stored in settings.
type PasswordPolicy struct {
	MinLength      int  `json:"minLength"`
	RequireLetters bool `json:"requireLetters"`
	RequireDigits  bool `json:"requireDigits"`
	RequireSymbols bool `json:"requireSymbols"`
	Version        int  `json:"version"`
}

const (
	settingPWMinLength      = "password_min_length"
	settingPWRequireLetters = "password_require_letters"
	settingPWRequireDigits  = "password_require_digits"
	settingPWRequireSymbols = "password_require_symbols"
	settingPWPolicyVersion  = "password_policy_version"
)

func DefaultPasswordPolicy() PasswordPolicy {
	return PasswordPolicy{MinLength: 4, Version: 0}
}

func (d *DB) GetPasswordPolicy() (PasswordPolicy, error) {
	p := DefaultPasswordPolicy()
	if v, err := d.GetSetting(settingPWMinLength); err != nil {
		return p, err
	} else if v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			p.MinLength = n
		}
	}
	if v, err := d.GetSetting(settingPWRequireLetters); err != nil {
		return p, err
	} else {
		p.RequireLetters = v == "1" || strings.EqualFold(v, "true")
	}
	if v, err := d.GetSetting(settingPWRequireDigits); err != nil {
		return p, err
	} else {
		p.RequireDigits = v == "1" || strings.EqualFold(v, "true")
	}
	if v, err := d.GetSetting(settingPWRequireSymbols); err != nil {
		return p, err
	} else {
		p.RequireSymbols = v == "1" || strings.EqualFold(v, "true")
	}
	if v, err := d.GetSetting(settingPWPolicyVersion); err != nil {
		return p, err
	} else if v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n >= 0 {
			p.Version = n
		}
	}
	if p.MinLength < 4 {
		p.MinLength = 4
	}
	if p.MinLength > 128 {
		p.MinLength = 128
	}
	return p, nil
}

// SetPasswordPolicy writes policy fields. When any rule changes, Version is bumped
// so existing users must change their password on next access.
func (d *DB) SetPasswordPolicy(p PasswordPolicy) (PasswordPolicy, error) {
	cur, err := d.GetPasswordPolicy()
	if err != nil {
		return cur, err
	}
	if p.MinLength < 4 {
		p.MinLength = 4
	}
	if p.MinLength > 128 {
		p.MinLength = 128
	}
	changed := p.MinLength != cur.MinLength ||
		p.RequireLetters != cur.RequireLetters ||
		p.RequireDigits != cur.RequireDigits ||
		p.RequireSymbols != cur.RequireSymbols
	ver := cur.Version
	if changed {
		ver = cur.Version + 1
	}
	if err := d.SetSetting(settingPWMinLength, strconv.Itoa(p.MinLength)); err != nil {
		return cur, err
	}
	if err := d.SetSetting(settingPWRequireLetters, boolSetting(p.RequireLetters)); err != nil {
		return cur, err
	}
	if err := d.SetSetting(settingPWRequireDigits, boolSetting(p.RequireDigits)); err != nil {
		return cur, err
	}
	if err := d.SetSetting(settingPWRequireSymbols, boolSetting(p.RequireSymbols)); err != nil {
		return cur, err
	}
	if err := d.SetSetting(settingPWPolicyVersion, strconv.Itoa(ver)); err != nil {
		return cur, err
	}
	return d.GetPasswordPolicy()
}

func boolSetting(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

// ValidatePassword checks a plaintext password against the policy.
func ValidatePassword(pw string, p PasswordPolicy) error {
	if strings.TrimSpace(pw) == "" {
		return fmt.Errorf("password required")
	}
	min := p.MinLength
	if min < 4 {
		min = 4
	}
	if len(pw) < min {
		return fmt.Errorf("password too short (min %d)", min)
	}
	if len(pw) > 256 {
		return fmt.Errorf("password too long (max 256)")
	}
	var hasLetter, hasDigit, hasSymbol bool
	for _, r := range pw {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			hasDigit = true
		default:
			// Symbol / punctuation / other non-letter non-digit
			if !unicode.IsSpace(r) {
				hasSymbol = true
			}
		}
	}
	if p.RequireLetters && !hasLetter {
		return fmt.Errorf("password must include a letter")
	}
	if p.RequireDigits && !hasDigit {
		return fmt.Errorf("password must include a digit")
	}
	if p.RequireSymbols && !hasSymbol {
		return fmt.Errorf("password must include a symbol")
	}
	return nil
}

// PasswordPolicySummary returns a short English description for UI hints.
func PasswordPolicySummary(p PasswordPolicy) string {
	parts := []string{fmt.Sprintf("min %d characters", p.MinLength)}
	if p.RequireLetters {
		parts = append(parts, "letters")
	}
	if p.RequireDigits {
		parts = append(parts, "digits")
	}
	if p.RequireSymbols {
		parts = append(parts, "symbols")
	}
	return strings.Join(parts, ", ")
}
