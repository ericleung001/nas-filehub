package db

import (
	"path/filepath"
	"testing"
)

func TestGlobalQuotaDefaultDisabled(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.SQL.Close() })
	s, err := d.GetGlobalQuotaSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Enabled || s.Bytes != 0 {
		t.Fatalf("default should be disabled/0, got %+v", s)
	}
}

func TestSetGlobalQuotaUpdatesAllUsersAndRejectsInvalid(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.SQL.Close() })

	admin, err := d.CreateUser("admin", "admin-pass-123", "homes/admin", true, 111, "")
	if err != nil {
		t.Fatal(err)
	}
	alice, err := d.CreateUser("alice", "alice-pass-123", "homes/alice", false, 222, "")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := d.CreateUser("bob", "bob-pass-1234", "homes/bob", false, 333, "")
	if err != nil {
		t.Fatal(err)
	}

	// Invalid value must not write settings or change users.
	neg := int64(-1)
	en := true
	if _, err := d.SetGlobalQuotaSettings(&en, &neg); err == nil {
		t.Fatal("expected error for negative quota")
	}
	s, err := d.GetGlobalQuotaSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Enabled || s.Bytes != 0 {
		t.Fatalf("invalid set wrote settings: %+v", s)
	}
	for _, id := range []int64{admin.ID, alice.ID, bob.ID} {
		u, err := d.GetUserByID(id)
		if err != nil {
			t.Fatal(err)
		}
		if u.QuotaBytes == 0 && id != 0 {
			// keep original non-zero quotas
		}
	}
	gotAdmin, _ := d.GetUserByID(admin.ID)
	gotAlice, _ := d.GetUserByID(alice.ID)
	gotBob, _ := d.GetUserByID(bob.ID)
	if gotAdmin.QuotaBytes != 111 || gotAlice.QuotaBytes != 222 || gotBob.QuotaBytes != 333 {
		t.Fatalf("quotas changed after invalid set: admin=%d alice=%d bob=%d", gotAdmin.QuotaBytes, gotAlice.QuotaBytes, gotBob.QuotaBytes)
	}

	bytes := int64(5 * 1024 * 1024 * 1024)
	if _, err := d.SetGlobalQuotaSettings(&en, &bytes); err != nil {
		t.Fatal(err)
	}
	s, err = d.GetGlobalQuotaSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Enabled || s.Bytes != bytes {
		t.Fatalf("settings not saved: %+v", s)
	}
	for _, id := range []int64{admin.ID, alice.ID, bob.ID} {
		u, err := d.GetUserByID(id)
		if err != nil || u == nil {
			t.Fatal(err)
		}
		if u.QuotaBytes != bytes {
			t.Fatalf("user %d quota = %d, want %d", id, u.QuotaBytes, bytes)
		}
	}

	// New user gets the global quota, ignoring the passed value.
	carol, err := d.CreateUser("carol", "carol-pass-123", "homes/carol", false, 999, "")
	if err != nil {
		t.Fatal(err)
	}
	if carol.QuotaBytes != bytes {
		t.Fatalf("new user quota = %d, want global %d", carol.QuotaBytes, bytes)
	}

	// Per-user patch is rejected while enabled.
	want := int64(1)
	if _, err := d.UpdateUser(alice.ID, UserUpdate{QuotaBytes: &want}); err == nil {
		t.Fatal("expected per-user quota update to be rejected")
	}
	alice2, _ := d.GetUserByID(alice.ID)
	if alice2.QuotaBytes != bytes {
		t.Fatalf("alice quota changed while global on: %d", alice2.QuotaBytes)
	}

	// Disable leaves stored quotas in place and allows per-user edits.
	off := false
	if _, err := d.SetGlobalQuotaSettings(&off, nil); err != nil {
		t.Fatal(err)
	}
	s, _ = d.GetGlobalQuotaSettings()
	if s.Enabled {
		t.Fatal("expected disabled")
	}
	if s.Bytes != bytes {
		t.Fatalf("stored global bytes should remain %d, got %d", bytes, s.Bytes)
	}
	for _, id := range []int64{admin.ID, alice.ID, bob.ID, carol.ID} {
		u, _ := d.GetUserByID(id)
		if u.QuotaBytes != bytes {
			t.Fatalf("quota should stay at last global value for user %d, got %d", id, u.QuotaBytes)
		}
	}
	custom := int64(4242)
	if _, err := d.UpdateUser(alice.ID, UserUpdate{QuotaBytes: &custom}); err != nil {
		t.Fatal(err)
	}
	alice3, _ := d.GetUserByID(alice.ID)
	if alice3.QuotaBytes != custom {
		t.Fatalf("alice quota after disable = %d, want %d", alice3.QuotaBytes, custom)
	}
	// Other users unchanged
	bob2, _ := d.GetUserByID(bob.ID)
	if bob2.QuotaBytes != bytes {
		t.Fatalf("bob should still be %d, got %d", bytes, bob2.QuotaBytes)
	}
}
