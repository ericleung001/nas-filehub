package db

import (
	"testing"

	"github.com/eric/nas-filehub/internal/secretbox"
)

func TestTotpSecretEncryptAndLegacyMigration(t *testing.T) {
	d := openTestDB(t)
	key := secretbox.DeriveKey("db-totp-test-secret")
	d.SetTotpKey(key)

	u, err := d.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	const plain = "JBSWY3DPEHPK3PXP"
	// Write a legacy plaintext secret directly, as older versions did.
	if _, err := d.SQL.Exec(`UPDATE users SET totp_secret=?, totp_enabled=1 WHERE id=?`, plain, u.ID); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetUserByID(u.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %v %+v", err, got)
	}
	if got.TotpSecret != plain {
		t.Fatalf("decoded secret %q", got.TotpSecret)
	}
	if !got.TotpLegacy {
		t.Fatal("expected TotpLegacy for plaintext storage")
	}
	var stored string
	if err := d.SQL.QueryRow(`SELECT totp_secret FROM users WHERE id=?`, u.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != plain {
		t.Fatal("legacy row should still be plaintext before migration")
	}
	if err := d.ReencryptLegacyTotpSecrets(); err != nil {
		t.Fatal(err)
	}
	if err := d.SQL.QueryRow(`SELECT totp_secret FROM users WHERE id=?`, u.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !secretbox.IsV1(stored) {
		t.Fatalf("expected v1 ciphertext, got prefix %q", stored[:min(8, len(stored))])
	}
	if stored == plain {
		t.Fatal("ciphertext must not equal plaintext")
	}
	got, err = d.GetUserByID(u.ID)
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if got.TotpSecret != plain || got.TotpLegacy {
		t.Fatalf("after migrate secret=%q legacy=%v", got.TotpSecret, got.TotpLegacy)
	}

	// New writes should be encrypted.
	const plain2 = "KZXW6YTBOI======"
	if err := d.SetTotpSecret(u.ID, plain2); err != nil {
		t.Fatal(err)
	}
	if err := d.SQL.QueryRow(`SELECT totp_secret FROM users WHERE id=?`, u.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !secretbox.IsV1(stored) {
		t.Fatal("SetTotpSecret should store v1 ciphertext")
	}
	got, _ = d.GetUserByID(u.ID)
	if got.TotpSecret != plain2 {
		t.Fatalf("got %q", got.TotpSecret)
	}
}

func TestTotpDecryptFailsClosedOnWrongKey(t *testing.T) {
	d := openTestDB(t)
	d.SetTotpKey(secretbox.DeriveKey("right-key"))
	u, err := d.CreateUser("bob", "bob-pass-1234", "homes/bob", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetTotpSecret(u.ID, "JBSWY3DPEHPK3PXP"); err != nil {
		t.Fatal(err)
	}
	d.SetTotpKey(secretbox.DeriveKey("wrong-key"))
	got, err := d.GetUserByID(u.ID)
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if got.TotpSecret != "" {
		t.Fatalf("wrong key must not yield a usable secret, got %q", got.TotpSecret)
	}
}
