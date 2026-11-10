package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
)

// LoadOrCreateSessionSecret returns secret if non-empty, otherwise loads or
// creates a persistent key file at path (mode 0600). Used when SESSION_SECRET
// is unset so TOTP secrets can still be encrypted outside the database file.
func LoadOrCreateSessionSecret(secret, path string) (string, bool, error) {
	secret = strings.TrimSpace(secret)
	if secret != "" {
		return secret, false, nil
	}
	if path == "" {
		return "", false, nil
	}
	if b, err := os.ReadFile(path); err == nil {
		s := strings.TrimSpace(string(b))
		if s != "" {
			return s, true, nil
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", false, err
	}
	s := hex.EncodeToString(buf)
	if err := os.WriteFile(path, append([]byte(s), '\n'), 0o600); err != nil {
		return "", false, err
	}
	return s, true, nil
}
