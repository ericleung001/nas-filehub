package db

import (
	"testing"
	"time"
)

func TestSessionListAndRevoke(t *testing.T) {
	d := openTestDB(t)
	u, err := d.CreateUser("sam", "sam-pass-1234", "homes/sam", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Hour)
	if err := d.CreateSession("token-current", u.ID, exp, "203.0.113.1", "Mozilla/5.0 Test"); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateSession("token-other", u.ID, exp, "203.0.113.2", "OtherAgent"); err != nil {
		t.Fatal(err)
	}
	list, err := d.ListUserSessions(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(list))
	}
	var other UserSession
	for _, s := range list {
		if s.Token == "token-other" {
			other = s
		}
		if s.Token == "" || s.ID == 0 {
			t.Fatalf("missing id/token internally: %+v", s)
		}
	}
	ok, err := d.DeleteSessionByID(u.ID, other.ID)
	if err != nil || !ok {
		t.Fatalf("revoke other: ok=%v err=%v", ok, err)
	}
	if err := d.CreateSession("token-third", u.ID, exp, "203.0.113.3", "Third"); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteOtherSessions(u.ID, "token-current"); err != nil {
		t.Fatal(err)
	}
	list, _ = d.ListUserSessions(u.ID)
	if len(list) != 1 || list[0].Token != "token-current" {
		t.Fatalf("expected only current left: %+v", list)
	}
}

func TestAuditAppendAndList(t *testing.T) {
	d := openTestDB(t)
	if err := d.AppendAudit(1, "admin", "127.0.0.1", "login_success", "username=admin"); err != nil {
		t.Fatal(err)
	}
	if err := d.AppendAudit(0, "nobody", "127.0.0.1", "login_failure", "username=nobody"); err != nil {
		t.Fatal(err)
	}
	events, total, err := d.ListAudit(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(events) != 2 {
		t.Fatalf("total=%d len=%d", total, len(events))
	}
	if events[0].Action != "login_failure" || events[1].Action != "login_success" {
		t.Fatalf("expected newest first: %+v", events)
	}
}

func TestLoginLockout(t *testing.T) {
	d := openTestDB(t)
	window := 15 * time.Minute
	for i := 0; i < 9; i++ {
		locked, _, err := d.RecordLoginFailure("Alice", 10, window)
		if err != nil || locked {
			t.Fatalf("attempt %d locked=%v err=%v", i+1, locked, err)
		}
	}
	locked, until, err := d.RecordLoginFailure("alice", 10, window)
	if err != nil || !locked {
		t.Fatalf("10th should lock: locked=%v err=%v", locked, err)
	}
	if until.IsZero() || !until.After(time.Now()) {
		t.Fatalf("until %v", until)
	}
	firstUntil := until
	locked, until2, err := d.RecordLoginFailure("ALICE", 10, window)
	if err != nil || !locked {
		t.Fatal("further attempt should stay locked")
	}
	// Window must not be extended (allow 2s clock slack).
	if until2.Sub(firstUntil) > 2*time.Second {
		t.Fatalf("window extended: %v -> %v", firstUntil, until2)
	}
	st, until3, err := d.LoginLockState("alice")
	if err != nil || !st || until3.IsZero() {
		t.Fatalf("state locked=%v until=%v err=%v", st, until3, err)
	}
	if err := d.ClearLoginFailures("alice"); err != nil {
		t.Fatal(err)
	}
	st, _, err = d.LoginLockState("alice")
	if err != nil || st {
		t.Fatal("cleared user should not be locked")
	}

	// Expired lock should not block and the next failure starts a new count.
	_, err = d.SQL.Exec(
		`INSERT INTO login_lockouts(username, failed_count, locked_until, updated_at) VALUES(?,?,?,?)`,
		"bob", 10, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		t.Fatal(err)
	}
	st, _, err = d.LoginLockState("bob")
	if err != nil || st {
		t.Fatal("expired lock must be treated as unlocked")
	}
	locked, _, err = d.RecordLoginFailure("bob", 10, window)
	if err != nil || locked {
		t.Fatalf("first failure after expiry should not lock, locked=%v err=%v", locked, err)
	}

	// Unknown usernames are tracked the same way (no extra leak surface).
	for i := 0; i < 10; i++ {
		locked, _, err = d.RecordLoginFailure("nosuch", 10, window)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !locked {
		t.Fatal("unknown username should lock after N failures too")
	}
}
