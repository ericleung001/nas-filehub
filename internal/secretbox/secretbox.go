package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

const (
	// PrefixV1 marks AES-GCM ciphertext stored as v1:<base64(nonce+ciphertext)>.
	PrefixV1 = "v1:"
	keyInfo  = "nas-filehub totp aes-gcm v1"
)

// DeriveKey returns a 32-byte AES key from the app session secret.
func DeriveKey(sessionSecret string) []byte {
	h := sha256.New()
	h.Write([]byte(keyInfo))
	h.Write([]byte{0})
	h.Write([]byte(sessionSecret))
	sum := h.Sum(nil)
	return sum
}

func IsV1(s string) bool {
	return strings.HasPrefix(s, PrefixV1)
}

// Encrypt seals plaintext with AES-GCM and returns a version-prefixed value.
func Encrypt(key []byte, plaintext string) (string, error) {
	if len(key) != 32 {
		return "", fmt.Errorf("invalid key length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return PrefixV1 + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens a v1: value. The caller should treat values without the
// prefix as legacy plaintext rather than calling Decrypt on them.
func Decrypt(key []byte, value string) (string, error) {
	if !IsV1(value) {
		return "", fmt.Errorf("not a v1 ciphertext")
	}
	if len(key) != 32 {
		return "", fmt.Errorf("invalid key length")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, PrefixV1))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return "", fmt.Errorf("ciphertext too short")
	}
	plain, err := gcm.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// Open returns the plaintext secret. Legacy values (no v1: prefix) are
// returned as-is with legacy=true so the caller can re-encrypt later.
// A v1: value that cannot be decrypted returns an error (wrong key or
// corruption) rather than treating the ciphertext as a TOTP secret.
func Open(key []byte, stored string) (plain string, legacy bool, err error) {
	if stored == "" {
		return "", false, nil
	}
	if !IsV1(stored) {
		return stored, true, nil
	}
	plain, err = Decrypt(key, stored)
	if err != nil {
		return "", false, err
	}
	return plain, false, nil
}
