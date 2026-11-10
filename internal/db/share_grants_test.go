package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func ptrBool(v bool) *bool { return &v }

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

	if err := d.SetShareGrant(sh.ID, 0, "read", nil); err == nil {
		t.Fatal("expected error for missing user")
	}
	if err := d.SetShareGrant(sh.ID, 99999, "read", nil); err == nil {
		t.Fatal("expected error for unknown user")
	}
	if err := d.SetShareGrant(sh.ID, alice.ID, "Read", nil); err != nil {
		t.Fatalf("normalized perm should be accepted: %v", err)
	}

	aliceShares, err := d.SharesForUser(alice.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceShares) != 1 || aliceShares[0].Share.ID != sh.ID || aliceShares[0].Perm != "read" || aliceShares[0].CanDelete {
		t.Fatalf("alice should see the share with read and no delete, got %+v", aliceShares)
	}

	bobShares, err := d.SharesForUser(bob.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(bobShares) != 0 {
		t.Fatalf("bob should not see the share, got %+v", bobShares)
	}

	// Omitted canDelete on update keeps the existing flag (false).
	if err := d.SetShareGrant(sh.ID, alice.ID, "write", nil); err != nil {
		t.Fatal(err)
	}
	aliceShares, err = d.SharesForUser(alice.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceShares) != 1 || aliceShares[0].Perm != "write" || aliceShares[0].CanDelete {
		t.Fatalf("alice perm should update to write and keep canDelete=false, got %+v", aliceShares)
	}

	if err := d.SetShareGrant(sh.ID, alice.ID, "write", ptrBool(true)); err != nil {
		t.Fatal(err)
	}
	aliceShares, err = d.SharesForUser(alice.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceShares) != 1 || !aliceShares[0].CanDelete {
		t.Fatalf("alice should have canDelete=true, got %+v", aliceShares)
	}

	grants, err := d.ListGrants(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 || grants[0].Username != "alice" || grants[0].Perm != "write" || !grants[0].CanDelete {
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

	// New write grant with omitted canDelete defaults to true (historical write=delete).
	if err := d.SetShareGrant(sh.ID, bob.ID, "write", nil); err != nil {
		t.Fatal(err)
	}
	bobShares, err = d.SharesForUser(bob.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(bobShares) != 1 || bobShares[0].Perm != "write" || !bobShares[0].CanDelete {
		t.Fatalf("new write grant should default canDelete=true, got %+v", bobShares)
	}

	adminShares, err := d.SharesForUser(admin.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(adminShares) != 1 || adminShares[0].Perm != "write" || !adminShares[0].CanDelete {
		t.Fatalf("admin listing should be write+delete, got %+v", adminShares)
	}
}

func TestShareGrantCanDeleteBackfill(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`
CREATE TABLE users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  is_admin INTEGER NOT NULL DEFAULT 0,
  home_path TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE shares (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  path TEXT NOT NULL UNIQUE,
  created_by INTEGER NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE share_grants (
  share_id INTEGER NOT NULL,
  user_id INTEGER NOT NULL,
  perm TEXT NOT NULL,
  PRIMARY KEY (share_id, user_id)
);
INSERT INTO users(username, password_hash, is_admin, home_path, created_at) VALUES
  ('admin','x',1,'homes/admin','2020-01-01T00:00:00Z'),
  ('alice','x',0,'homes/alice','2020-01-01T00:00:00Z');
INSERT INTO shares(name, path, created_by, created_at) VALUES ('team','shared/team',1,'2020-01-01T00:00:00Z');
INSERT INTO share_grants(share_id, user_id, perm) VALUES (1, 2, 'write'), (1, 1, 'read');
`)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.SQL.Close() })

	grants, err := d.ListGrants(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 2 {
		t.Fatalf("expected 2 grants, got %+v", grants)
	}
	byUser := map[string]ShareGrant{}
	for _, g := range grants {
		byUser[g.Username] = g
	}
	if g, ok := byUser["alice"]; !ok || g.Perm != "write" || !g.CanDelete {
		t.Fatalf("legacy write grant should backfill canDelete=true, got %+v", grants)
	}
	if g, ok := byUser["admin"]; !ok || g.Perm != "read" || g.CanDelete {
		t.Fatalf("legacy read grant should backfill canDelete=false, got %+v", grants)
	}
}
