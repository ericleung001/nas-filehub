package api

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/eric/nas-filehub/internal/db"
)

type recordedMail struct {
	To, Subject, Body string
}

type mailTrap struct {
	mu      sync.Mutex
	sent    []recordedMail
	fail    bool
	enabled bool
}

func (t *mailTrap) hook() func(cfg db.SMTPSettings, to, subject, body string) error {
	return func(cfg db.SMTPSettings, to, subject, body string) error {
		if t.fail {
			return errStr("smtp failed")
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		t.sent = append(t.sent, recordedMail{To: to, Subject: subject, Body: body})
		return nil
	}
}

func (t *mailTrap) messages() []recordedMail {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]recordedMail, len(t.sent))
	copy(out, t.sent)
	return out
}

func TestNotifySkippedWhenSMTPDisabled(t *testing.T) {
	s, ts, database := testServer(t)
	trap := &mailTrap{}
	s.MailSend = trap.hook()
	s.NotifySync = true
	if err := database.SetOpenRegistration(true); err != nil {
		t.Fatal(err)
	}
	_ = setUserEmail(t, database, "admin", "admin@example.com")
	st, data := doJSON(t, "POST", ts.URL+"/api/register", nil, map[string]any{
		"username": "newbie", "password": "Newbie-pass-1", "email": "new@example.com",
	})
	if st != 201 || data["pending"] != true {
		t.Fatalf("register %d %v", st, data)
	}
	if len(trap.messages()) != 0 {
		t.Fatalf("no SMTP should send nothing, got %#v", trap.messages())
	}
}

func TestNotifyPendingRegistrationAndShareGrant(t *testing.T) {
	s, ts, database := testServer(t)
	trap := &mailTrap{}
	s.MailSend = trap.hook()
	s.NotifySync = true
	enableTestSMTP(t, database)
	_ = setUserEmail(t, database, "admin", "admin@example.com")
	if err := database.SetOpenRegistration(true); err != nil {
		t.Fatal(err)
	}
	st, data := doJSON(t, "POST", ts.URL+"/api/register", nil, map[string]any{
		"username": "newbie", "password": "Newbie-pass-1", "email": "new@example.com",
	})
	if st != 201 || data["pending"] != true {
		t.Fatalf("register %d %v", st, data)
	}
	msgs := trap.messages()
	if len(msgs) != 1 || msgs[0].To != "admin@example.com" {
		t.Fatalf("pending mail %#v", msgs)
	}
	if msgs[0].Subject != "Filehub: new account pending approval" {
		t.Fatalf("subject %q", msgs[0].Subject)
	}

	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "shared/team"), 0o755)
	sh, err := database.CreateShare("Team", "shared/team", alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	admin := adminCookie(t, ts.URL)
	st, data = doJSON(t, "POST", ts.URL+"/api/admin/shares/"+itoa(sh.ID)+"/grants", admin, map[string]any{
		"userId": alice.ID, "perm": "write", "canDelete": true,
	})
	if st != 200 {
		t.Fatalf("grant %d %v", st, data)
	}
	msgs = trap.messages()
	found := false
	for _, m := range msgs {
		if m.To == "alice@example.com" && m.Subject == "Filehub: folder access granted" {
			found = true
			if !containsAll(m.Body, "Team", "write") {
				t.Fatalf("grant body %q", m.Body)
			}
		}
	}
	if !found {
		t.Fatalf("missing grant mail %#v", msgs)
	}
}

func TestNotifyFailureDoesNotFailGrant(t *testing.T) {
	s, ts, database := testServer(t)
	trap := &mailTrap{fail: true}
	s.MailSend = trap.hook()
	s.NotifySync = true
	enableTestSMTP(t, database)
	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "shared/team"), 0o755)
	sh, err := database.CreateShare("Team", "shared/team", alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	admin := adminCookie(t, ts.URL)
	st, data := doJSON(t, "POST", ts.URL+"/api/admin/shares/"+itoa(sh.ID)+"/grants", admin, map[string]any{
		"userId": alice.ID, "perm": "read",
	})
	if st != 200 {
		t.Fatalf("grant should succeed even if mail fails: %d %v", st, data)
	}
}

func enableTestSMTP(t *testing.T, database *db.DB) {
	t.Helper()
	cfg := db.SMTPSettings{Enabled: true, Host: "localhost", Port: 2525, From: "noreply@example.com"}
	if _, err := database.SetSMTPSettings(cfg, nil); err != nil {
		t.Fatal(err)
	}
}

func setUserEmail(t *testing.T, database *db.DB, username, email string) error {
	t.Helper()
	u, err := database.GetUserByUsername(username)
	if err != nil || u == nil {
		t.Fatalf("user %s: %v", username, err)
	}
	_, err = database.SQL.Exec(`UPDATE users SET email=? WHERE id=?`, email, u.ID)
	return err
}

func mustAdminID(t *testing.T, database *db.DB) int64 {
	t.Helper()
	u, err := database.GetUserByUsername("admin")
	if err != nil || u == nil {
		t.Fatal(err)
	}
	return u.ID
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !containsStr(s, p) {
			return false
		}
	}
	return true
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})())
}
