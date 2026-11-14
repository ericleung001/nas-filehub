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

	// Per-user override is allowed while the default is enabled.
	want := int64(1)
	if _, err := d.UpdateUser(alice.ID, UserUpdate{QuotaBytes: &want}); err != nil {
		t.Fatalf("per-user override: %v", err)
	}
	alice2, _ := d.GetUserByID(alice.ID)
	if alice2.QuotaBytes != want || !alice2.QuotaCustom {
		t.Fatalf("alice override not stored: bytes=%d custom=%v", alice2.QuotaBytes, alice2.QuotaCustom)
	}
	if got := d.EffectiveQuota(alice2); got != want {
		t.Fatalf("alice effective quota %d, want %d", got, want)
	}
	bobOn, _ := d.GetUserByID(bob.ID)
	if d.EffectiveQuota(bobOn) != bytes || bobOn.QuotaCustom {
		t.Fatalf("bob should stay on default %d, got stored=%d custom=%v effective=%d", bytes, bobOn.QuotaBytes, bobOn.QuotaCustom, d.EffectiveQuota(bobOn))
	}

	// Changing the default does not overwrite a custom user.
	smaller := int64(1024)
	en2 := true
	if _, err := d.SetGlobalQuotaSettings(&en2, &smaller); err != nil {
		t.Fatal(err)
	}
	aliceKeep, _ := d.GetUserByID(alice.ID)
	if aliceKeep.QuotaBytes != want || !aliceKeep.QuotaCustom {
		t.Fatalf("custom alice overwritten: bytes=%d custom=%v", aliceKeep.QuotaBytes, aliceKeep.QuotaCustom)
	}
	bobAfter, _ := d.GetUserByID(bob.ID)
	if bobAfter.QuotaBytes != smaller {
		t.Fatalf("bob default stored %d, want %d", bobAfter.QuotaBytes, smaller)
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
	if s.Bytes != smaller {
		t.Fatalf("stored global bytes should remain %d, got %d", smaller, s.Bytes)
	}
	alice3, _ := d.GetUserByID(alice.ID)
	if alice3.QuotaBytes != want || !alice3.QuotaCustom {
		t.Fatalf("alice custom should remain after disable: bytes=%d custom=%v", alice3.QuotaBytes, alice3.QuotaCustom)
	}
	bob2, _ := d.GetUserByID(bob.ID)
	if bob2.QuotaBytes != smaller {
		t.Fatalf("bob should stay at last default %d, got %d", smaller, bob2.QuotaBytes)
	}
	custom := int64(4242)
	offCustom := false
	if _, err := d.UpdateUser(alice.ID, UserUpdate{QuotaBytes: &custom, QuotaCustom: &offCustom}); err != nil {
		t.Fatal(err)
	}
	alice4, _ := d.GetUserByID(alice.ID)
	if alice4.QuotaBytes != custom {
		t.Fatalf("alice quota after edit = %d, want %d", alice4.QuotaBytes, custom)
	}
}
