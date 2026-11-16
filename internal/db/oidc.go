package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// OIDCSettings is admin-configured single sign-on (client secret never returned in Public()).
type OIDCSettings struct {
	Enabled      bool   `json:"enabled"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"-"`
	// ClientSecretSet is true when a secret is stored (for UI); never the secret itself.
	ClientSecretSet bool   `json:"clientSecretSet"`
	AutoCreate      bool   `json:"autoCreate"`
	Label           string `json:"label"`
}

const (
	settingOIDCEnabled      = "oidc_enabled"
	settingOIDCIssuer       = "oidc_issuer"
	settingOIDCClientID     = "oidc_client_id"
	settingOIDCClientSecret = "oidc_client_secret"
	settingOIDCAutoCreate   = "oidc_auto_create"
	settingOIDCLabel        = "oidc_label"
	DefaultOIDCLabel        = "Sign in with SSO"
)

func DefaultOIDCSettings() OIDCSettings {
	return OIDCSettings{Label: DefaultOIDCLabel}
}

func NormalizeOIDCIssuer(issuer string) string {
	return strings.TrimRight(strings.TrimSpace(issuer), "/")
}

func (d *DB) ensureOIDCSchema() error {
	cols := map[string]bool{}
	rows, err := d.SQL.Query(`PRAGMA table_info(users)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	alters := []struct {
		name string
		ddl  string
	}{
		{"oidc_issuer", `ALTER TABLE users ADD COLUMN oidc_issuer TEXT NOT NULL DEFAULT ''`},
		{"oidc_subject", `ALTER TABLE users ADD COLUMN oidc_subject TEXT NOT NULL DEFAULT ''`},
	}
	for _, a := range alters {
		if cols[a.name] {
			continue
		}
		if _, err := d.SQL.Exec(a.ddl); err != nil {
			return err
		}
	}
	if _, err := d.SQL.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS users_oidc_sub_unique
		ON users(oidc_issuer, oidc_subject)
		WHERE oidc_issuer != '' AND oidc_subject != ''
	`); err != nil {
		return err
	}
	_, err = d.SQL.Exec(`
CREATE TABLE IF NOT EXISTS oidc_states (
  state TEXT PRIMARY KEY,
  nonce TEXT NOT NULL,
  code_verifier TEXT NOT NULL,
  remember_me INTEGER NOT NULL DEFAULT 0,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);
`)
	return err
}

func (d *DB) GetOIDCSettings() (OIDCSettings, error) {
	s := DefaultOIDCSettings()
	if v, err := d.GetSetting(settingOIDCEnabled); err != nil {
		return s, err
	} else {
		s.Enabled = v == "1" || strings.EqualFold(v, "true")
	}
	if v, err := d.GetSetting(settingOIDCIssuer); err != nil {
		return s, err
	} else {
		s.Issuer = NormalizeOIDCIssuer(v)
	}
	if v, err := d.GetSetting(settingOIDCClientID); err != nil {
		return s, err
	} else {
		s.ClientID = strings.TrimSpace(v)
	}
	if v, err := d.GetSetting(settingOIDCClientSecret); err != nil {
		return s, err
	} else {
		s.ClientSecret = v
		s.ClientSecretSet = v != ""
	}
	if v, err := d.GetSetting(settingOIDCAutoCreate); err != nil {
		return s, err
	} else {
		s.AutoCreate = v == "1" || strings.EqualFold(v, "true")
	}
	if v, err := d.GetSetting(settingOIDCLabel); err != nil {
		return s, err
	} else if strings.TrimSpace(v) != "" {
		s.Label = strings.TrimSpace(v)
	}
	return s, nil
}

// SetOIDCSettings writes OIDC fields. Client secret: nil = leave unchanged; "" = clear; otherwise set.
func (d *DB) SetOIDCSettings(in OIDCSettings, clientSecret *string) (OIDCSettings, error) {
	in.Issuer = NormalizeOIDCIssuer(in.Issuer)
	in.ClientID = strings.TrimSpace(in.ClientID)
	in.Label = strings.TrimSpace(in.Label)
	if in.Label == "" {
		in.Label = DefaultOIDCLabel
	}
	if err := d.SetSetting(settingOIDCEnabled, boolSetting(in.Enabled)); err != nil {
		return OIDCSettings{}, err
	}
	if err := d.SetSetting(settingOIDCIssuer, in.Issuer); err != nil {
		return OIDCSettings{}, err
	}
	if err := d.SetSetting(settingOIDCClientID, in.ClientID); err != nil {
		return OIDCSettings{}, err
	}
	if err := d.SetSetting(settingOIDCAutoCreate, boolSetting(in.AutoCreate)); err != nil {
		return OIDCSettings{}, err
	}
	if err := d.SetSetting(settingOIDCLabel, in.Label); err != nil {
		return OIDCSettings{}, err
	}
	if clientSecret != nil {
		if err := d.SetSetting(settingOIDCClientSecret, *clientSecret); err != nil {
			return OIDCSettings{}, err
		}
	}
	return d.GetOIDCSettings()
}

func (s OIDCSettings) Ready() bool {
	return s.Enabled && s.Issuer != "" && s.ClientID != "" && s.ClientSecret != ""
}

func (s OIDCSettings) ButtonLabel() string {
	if strings.TrimSpace(s.Label) == "" {
		return DefaultOIDCLabel
	}
	return strings.TrimSpace(s.Label)
}

type OIDCState struct {
	State        string
	Nonce        string
	CodeVerifier string
	RememberMe   bool
	ExpiresAt    time.Time
}

func (d *DB) CreateOIDCState(state, nonce, codeVerifier string, rememberMe bool, expires time.Time) error {
	if strings.TrimSpace(state) == "" || strings.TrimSpace(nonce) == "" || strings.TrimSpace(codeVerifier) == "" {
		return fmt.Errorf("oidc state required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rm := 0
	if rememberMe {
		rm = 1
	}
	_, err := d.SQL.Exec(
		`INSERT INTO oidc_states(state, nonce, code_verifier, remember_me, expires_at, created_at) VALUES(?,?,?,?,?,?)`,
		state, nonce, codeVerifier, rm, expires.UTC().Format(time.RFC3339), now,
	)
	return err
}

func (d *DB) ConsumeOIDCState(state string) (*OIDCState, error) {
	state = strings.TrimSpace(state)
	if state == "" {
		return nil, nil
	}
	var nonce, verifier, expStr string
	var rm int
	// Single statement so a replay cannot read the same state twice.
	// Expired rows are still deleted, then treated as missing.
	err := d.SQL.QueryRow(
		`DELETE FROM oidc_states WHERE state=? RETURNING nonce, code_verifier, remember_me, expires_at`,
		state,
	).Scan(&nonce, &verifier, &rm, &expStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	exp, err := time.Parse(time.RFC3339, expStr)
	if err != nil || time.Now().After(exp) {
		return nil, nil
	}
	return &OIDCState{
		State:        state,
		Nonce:        nonce,
		CodeVerifier: verifier,
		RememberMe:   rm == 1,
		ExpiresAt:    exp,
	}, nil
}

func (d *DB) CleanupOIDCStates() {
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = d.SQL.Exec(`DELETE FROM oidc_states WHERE expires_at < ?`, now)
}

func (u *User) HasLocalPassword() bool {
	return u != nil && strings.TrimSpace(u.PasswordHash) != ""
}

func (u *User) OIDCLinked() bool {
	return u != nil && strings.TrimSpace(u.OIDCIssuer) != "" && strings.TrimSpace(u.OIDCSubject) != ""
}

func (d *DB) GetUserByOIDC(issuer, subject string) (*User, error) {
	issuer = NormalizeOIDCIssuer(issuer)
	subject = strings.TrimSpace(subject)
	if issuer == "" || subject == "" {
		return nil, nil
	}
	row := d.SQL.QueryRow(
		`SELECT `+userSelectCols+` FROM users WHERE oidc_issuer=? AND oidc_subject=? AND oidc_issuer!='' AND oidc_subject!=''`,
		issuer, subject,
	)
	u, err := d.scanUser(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (d *DB) LinkUserOIDC(id int64, issuer, subject string) error {
	issuer = NormalizeOIDCIssuer(issuer)
	subject = strings.TrimSpace(subject)
	if issuer == "" || subject == "" {
		return fmt.Errorf("OIDC issuer and subject required")
	}
	_, err := d.SQL.Exec(`UPDATE users SET oidc_issuer=?, oidc_subject=? WHERE id=?`, issuer, subject, id)
	if err != nil && isUniqueConstraint(err) {
		return fmt.Errorf("OIDC identity already linked to another account")
	}
	return err
}

// CreateOIDCUser creates an approved, active user with no local password.
func (d *DB) CreateOIDCUser(username, email, issuer, subject string) (*User, error) {
	username = strings.TrimSpace(username)
	email = normalizeEmail(email)
	issuer = NormalizeOIDCIssuer(issuer)
	subject = strings.TrimSpace(subject)
	if username == "" || issuer == "" || subject == "" {
		return nil, fmt.Errorf("username and OIDC identity required")
	}
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if err := validateEmailOptional(email); err != nil {
		return nil, err
	}
	if email != "" {
		taken, err := d.EmailInUse(email, 0)
		if err != nil {
			return nil, err
		}
		if taken {
			return nil, ErrEmailInUse
		}
	}
	quotaBytes := int64(0)
	if gq, err := d.GetGlobalQuotaSettings(); err != nil {
		return nil, err
	} else if gq.Enabled {
		quotaBytes = gq.Bytes
	}
	homePath, err := sanitizeHomePath("", username)
	if err != nil {
		return nil, err
	}
	pol, _ := d.GetPasswordPolicy()
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := d.SQL.Exec(
		`INSERT INTO users(username, email, password_hash, is_admin, home_path, quota_bytes, quota_custom, disabled, password_changed_at, password_policy_version, status, created_at, oidc_issuer, oidc_subject)
		 VALUES(?,?,?,?,?,?,0,0,'',?,?,?,?,?)`,
		username, email, "", 0, homePath, quotaBytes, pol.Version, UserStatusActive, now, issuer, subject,
	)
	if err != nil {
		if isUniqueConstraint(err) && strings.Contains(strings.ToLower(err.Error()), "email") {
			return nil, ErrEmailInUse
		}
		if isUniqueConstraint(err) && strings.Contains(strings.ToLower(err.Error()), "oidc") {
			return nil, fmt.Errorf("OIDC identity already linked to another account")
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{
		ID:                    id,
		Username:              username,
		Email:                 email,
		HomePath:              homePath,
		QuotaBytes:            quotaBytes,
		PasswordPolicyVersion: pol.Version,
		Status:                UserStatusActive,
		OIDCIssuer:            issuer,
		OIDCSubject:           subject,
		CreatedAt:             time.Now().UTC(),
	}, nil
}

// AllocateOIDCUsername returns a valid, unused username derived from preferred_username or an email local-part.
func (d *DB) AllocateOIDCUsername(preferred, email string) (string, error) {
	base := sanitizeOIDCUsername(preferred)
	if base == "" {
		local := email
		if i := strings.IndexByte(email, '@'); i > 0 {
			local = email[:i]
		}
		base = sanitizeOIDCUsername(local)
	}
	if base == "" {
		base = "user"
	}
	if err := ValidateUsername(base); err != nil {
		base = "user"
	}
	for i := 0; i < 1000; i++ {
		candidate := base
		if i > 0 {
			suffix := fmt.Sprintf("%d", i+1)
			trimAt := 32 - len(suffix)
			if trimAt < 1 {
				return "", fmt.Errorf("could not allocate username")
			}
			root := base
			if len(root) > trimAt {
				root = root[:trimAt]
			}
			root = strings.TrimRight(root, "._-")
			if root == "" {
				root = "user"
			}
			candidate = root + suffix
		}
		if err := ValidateUsername(candidate); err != nil {
			continue
		}
		u, err := d.GetUserByUsername(candidate)
		if err != nil {
			return "", err
		}
		if u == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not allocate username")
}

func sanitizeOIDCUsername(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if i := strings.IndexByte(raw, '@'); i > 0 {
		raw = raw[:i]
	}
	var b strings.Builder
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		if b.Len() > 0 && (r == '.' || r == '_' || r == '-') {
			b.WriteRune(r)
		}
	}
	s := strings.TrimRight(b.String(), "._-")
	if len(s) > 32 {
		s = strings.TrimRight(s[:32], "._-")
	}
	return s
}
