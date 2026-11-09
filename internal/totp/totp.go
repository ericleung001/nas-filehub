package totp

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image/png"
	"net/url"
	"strings"
	"time"

	"github.com/pquerna/otp"
	gotp "github.com/pquerna/otp/totp"
	qrcode "github.com/skip2/go-qrcode"
)

const Issuer = "Filehub"

// GenerateSecret creates a new TOTP key for username. Returns base32 secret and otpauth URL.
func GenerateSecret(username string) (secret string, otpauthURL string, err error) {
	key, err := gotp.Generate(gotp.GenerateOpts{
		Issuer:      Issuer,
		AccountName: username,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

func Validate(secret, code string) bool {
	secret = strings.TrimSpace(secret)
	code = strings.TrimSpace(code)
	if secret == "" || code == "" {
		return false
	}
	ok, err := gotp.ValidateCustom(code, secret, time.Now(), gotp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && ok
}

// QRDataURL returns a PNG data URL for the otpauth URL.
func QRDataURL(otpauthURL string) (string, error) {
	pngBytes, err := qrcode.Encode(otpauthURL, qrcode.Medium, 256)
	if err != nil {
		return "", err
	}
	if _, err := png.Decode(bytes.NewReader(pngBytes)); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes), nil
}

// GenerateBackupCodes returns n plaintext codes (8 hex chars each) for one-time display.
func GenerateBackupCodes(n int) ([]string, error) {
	if n <= 0 {
		n = 8
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		b := make([]byte, 4)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		out = append(out, strings.ToUpper(hex.EncodeToString(b)))
	}
	return out, nil
}

func NormalizeBackupCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
}

func FormatBackupCode(code string) string {
	c := NormalizeBackupCode(code)
	if len(c) == 8 {
		return c[:4] + "-" + c[4:]
	}
	return c
}

func OtpauthURL(secret, username string) string {
	label := url.PathEscape(Issuer + ":" + username)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", Issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	return fmt.Sprintf("otpauth://totp/%s?%s", label, q.Encode())
}
