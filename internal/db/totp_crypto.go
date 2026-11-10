package db

import (
	"github.com/eric/nas-filehub/internal/secretbox"
)

// SetTotpKey installs the AES key used to encrypt TOTP secrets at rest.
// A missing or wrong-length key leaves existing values as stored (plaintext
// or already encrypted) and skips new encryption.
func (d *DB) SetTotpKey(key []byte) {
	if d == nil {
		return
	}
	if len(key) != 32 {
		d.totpKey = nil
		return
	}
	d.totpKey = append([]byte(nil), key...)
}

func (d *DB) encodeTotpSecret(plain string) string {
	if d == nil || plain == "" || len(d.totpKey) != 32 {
		return plain
	}
	enc, err := secretbox.Encrypt(d.totpKey, plain)
	if err != nil {
		return plain
	}
	return enc
}

func (d *DB) decodeTotpSecret(stored string) (plain string, legacy bool) {
	if stored == "" {
		return "", false
	}
	if d == nil || len(d.totpKey) != 32 {
		if secretbox.IsV1(stored) {
			// Encrypted in the DB but we have no key; do not treat ciphertext as a TOTP secret.
			return "", false
		}
		return stored, true
	}
	plain, legacy, err := secretbox.Open(d.totpKey, stored)
	if err != nil {
		return "", false
	}
	return plain, legacy
}

// ReencryptLegacyTotpSecrets writes v1 ciphertext for any remaining plaintext secrets.
func (d *DB) ReencryptLegacyTotpSecrets() error {
	if d == nil || len(d.totpKey) != 32 {
		return nil
	}
	rows, err := d.SQL.Query(`SELECT id, totp_secret FROM users WHERE totp_secret != ''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		id     int64
		stored string
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.stored); err != nil {
			return err
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range list {
		if secretbox.IsV1(r.stored) {
			continue
		}
		enc := d.encodeTotpSecret(r.stored)
		if enc == r.stored {
			continue
		}
		if _, err := d.SQL.Exec(`UPDATE users SET totp_secret=? WHERE id=? AND totp_secret=?`, enc, r.id, r.stored); err != nil {
			return err
		}
	}
	return nil
}

// ReencryptUserTotpSecret re-writes the user's current in-memory plaintext secret.
func (d *DB) ReencryptUserTotpSecret(userID int64, plain string) error {
	if plain == "" {
		return nil
	}
	return d.SetTotpSecret(userID, plain)
}
