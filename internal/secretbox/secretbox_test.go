package secretbox

import (
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := DeriveKey("unit-test-session-secret")
	if len(key) != 32 {
		t.Fatalf("key len %d", len(key))
	}
	secret := "JBSWY3DPEHPK3PXP"
	enc, err := Encrypt(key, secret)
	if err != nil {
		t.Fatal(err)
	}
	if !IsV1(enc) {
		t.Fatalf("missing v1 prefix: %q", enc[:min(8, len(enc))])
	}
	if strings.Contains(enc, secret) {
		t.Fatal("ciphertext should not contain the plaintext secret")
	}
	got, err := Decrypt(key, enc)
	if err != nil {
		t.Fatal(err)
	}
	if got != secret {
		t.Fatalf("got %q want %q", got, secret)
	}
	// Different key must fail
	if _, err := Decrypt(DeriveKey("other-secret"), enc); err == nil {
		t.Fatal("expected decrypt failure with the wrong key")
	}
}

func TestOpenLegacyPlaintext(t *testing.T) {
	key := DeriveKey("unit-test-session-secret")
	plain, legacy, err := Open(key, "JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	if !legacy || plain != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("legacy=%v plain=%q", legacy, plain)
	}
	enc, err := Encrypt(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	got, legacy, err := Open(key, enc)
	if err != nil {
		t.Fatal(err)
	}
	if legacy || got != plain {
		t.Fatalf("encrypted open legacy=%v got=%q", legacy, got)
	}
}

func TestOpenEmptyAndCorrupt(t *testing.T) {
	key := DeriveKey("unit-test-session-secret")
	plain, legacy, err := Open(key, "")
	if err != nil || plain != "" || legacy {
		t.Fatalf("empty: %q %v %v", plain, legacy, err)
	}
	if _, _, err := Open(key, "v1:not-valid-base64!!!"); err == nil {
		t.Fatal("expected error for corrupt v1 value")
	}
}

func TestDeriveKeyStableAndSeparated(t *testing.T) {
	a := DeriveKey("abc")
	b := DeriveKey("abc")
	c := DeriveKey("abcd")
	if string(a) != string(b) {
		t.Fatal("derive should be stable")
	}
	if string(a) == string(c) {
		t.Fatal("different secrets should not yield the same key")
	}
}
