package db

import (
	"path/filepath"
	"testing"
)

func TestShareGrantAppearsForGranteeOnly(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.SQL.Close() })

	admin, err := d.CreateUser("admin", "admin-pass-123", "homes/admin", true, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	alice, err := d.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := d.CreateUser("bob", "bob-pass-1234", "homes/bob", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}

	sh, err := d.CreateShare("team", "shared/team", admin.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := d.SetShareGrant(sh.ID, 0, "read"); err == nil {
		t.Fatal("expected error for missing user")
	}
	if err := d.SetShareGrant(sh.ID, 99999, "read"); err == nil {
		t.Fatal("expected error for unknown user")
	}
	if err := d.SetShareGrant(sh.ID, alice.ID, "Read"); err != nil {
		t.Fatalf("normalized perm should be accepted: %v", err)
	}

	aliceShares, err := d.SharesForUser(alice.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceShares) != 1 || aliceShares[0].Share.ID != sh.ID || aliceShares[0].Perm != "read" {
		t.Fatalf("alice should see the share with read, got %+v", aliceShares)
	}

	bobShares, err := d.SharesForUser(bob.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(bobShares) != 0 {
		t.Fatalf("bob should not see the share, got %+v", bobShares)
	}

	if err := d.SetShareGrant(sh.ID, alice.ID, "write"); err != nil {
		t.Fatal(err)
	}
	aliceShares, err = d.SharesForUser(alice.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceShares) != 1 || aliceShares[0].Perm != "write" {
		t.Fatalf("alice perm should update to write, got %+v", aliceShares)
	}

	grants, err := d.ListGrants(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 || grants[0].Username != "alice" || grants[0].Perm != "write" {
		t.Fatalf("list grants: %+v", grants)
	}

	if err := d.RemoveShareGrant(sh.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	aliceShares, err = d.SharesForUser(alice.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceShares) != 0 {
		t.Fatalf("alice should lose the share after revoke, got %+v", aliceShares)
	}
}
