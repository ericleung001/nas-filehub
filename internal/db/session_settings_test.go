package db

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = d.SQL.Close() })
	return d
}

func TestDefaultSessionSettings(t *testing.T) {
	d := openTestDB(t)
	s, err := d.GetSessionSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.SessionTTLSeconds != DefaultSessionTTLSeconds {
		t.Fatalf("session ttl want %d got %d", DefaultSessionTTLSeconds, s.SessionTTLSeconds)
	}
	if s.RememberMeTTLSeconds != DefaultRememberMeTTLSeconds {
		t.Fatalf("remember ttl want %d got %d", DefaultRememberMeTTLSeconds, s.RememberMeTTLSeconds)
	}
	if !s.AllowRememberMe {
		t.Fatal("allow remember me should default true")
	}
	if s.SessionDuration(false) != 7*24*time.Hour {
		t.Fatalf("normal duration: %v", s.SessionDuration(false))
	}
	if s.SessionDuration(true) != 30*24*time.Hour {
		t.Fatalf("remember duration: %v", s.SessionDuration(true))
	}
}

func TestSetSessionSettingsAndDuration(t *testing.T) {
	d := openTestDB(t)
	in := SessionSettings{
		SessionTTLSeconds:    3600,
		RememberMeTTLSeconds: 86400,
		AllowRememberMe:      true,
	}
	if err := d.SetSessionSettings(in); err != nil {
		t.Fatal(err)
	}
	s, err := d.GetSessionSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.SessionTTLSeconds != 3600 || s.RememberMeTTLSeconds != 86400 {
		t.Fatalf("got %+v", s)
	}
	if s.SessionDuration(false) != time.Hour {
		t.Fatalf("normal: %v", s.SessionDuration(false))
	}
	if s.SessionDuration(true) != 24*time.Hour {
		t.Fatalf("remember: %v", s.SessionDuration(true))
	}

	s.AllowRememberMe = false
	if err := d.SetSessionSettings(s); err != nil {
		t.Fatal(err)
	}
	s2, _ := d.GetSessionSettings()
	if s2.AllowRememberMe {
		t.Fatal("expected allowRememberMe false")
	}
	if s2.SessionDuration(true) != time.Hour {
		t.Fatalf("remember ignored when disabled; got %v", s2.SessionDuration(true))
	}
}

func TestValidateSessionSettingsBounds(t *testing.T) {
	cases := []struct {
		name string
		s    SessionSettings
		ok   bool
	}{
		{"ok defaults", DefaultSessionSettings(), true},
		{"too short session", SessionSettings{SessionTTLSeconds: 60, RememberMeTTLSeconds: 3600, AllowRememberMe: true}, false},
		{"too long session", SessionSettings{SessionTTLSeconds: maxSessionTTLSeconds + 1, RememberMeTTLSeconds: maxSessionTTLSeconds + 1, AllowRememberMe: true}, false},
		{"remember shorter than session", SessionSettings{SessionTTLSeconds: 86400, RememberMeTTLSeconds: 3600, AllowRememberMe: true}, false},
		{"remember equal session", SessionSettings{SessionTTLSeconds: 86400, RememberMeTTLSeconds: 86400, AllowRememberMe: true}, true},
		{"max ok", SessionSettings{SessionTTLSeconds: maxSessionTTLSeconds, RememberMeTTLSeconds: maxSessionTTLSeconds, AllowRememberMe: false}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSessionSettings(tc.s)
			if tc.ok && err != nil {
				t.Fatalf("expected ok: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestPendingLoginPreservesRememberMe(t *testing.T) {
	d := openTestDB(t)
	u, err := d.CreateUser("alice", "secret-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	token := "pending-token-1"
	exp := time.Now().Add(10 * time.Minute)
	if err := d.CreatePendingLogin(token, u.ID, "totp", exp, false, true); err != nil {
		t.Fatal(err)
	}
	pend, err := d.GetPendingLogin(token)
	if err != nil || pend == nil {
		t.Fatalf("get pending: %v %#v", err, pend)
	}
	if !pend.RememberMe {
		t.Fatal("rememberMe not preserved on pending login")
	}
	if pend.Kind != "totp" || pend.UserID != u.ID {
		t.Fatalf("unexpected pending: %+v", pend)
	}
}
