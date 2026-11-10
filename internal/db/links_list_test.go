package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestListLinkSharesExcludesRevokedAndDeleteRemoves(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.SQL.Close() })

	u, err := d.CreateUser("owner", "owner-pass-123", "homes/owner", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}

	active, err := d.CreateLinkShare("tok-active", u.ID, "home", "photos", true, "Photos", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := d.CreateLinkShare("tok-revoked", u.ID, "home", "old", false, "Old", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.RevokeLinkShare(revoked.ID); err != nil {
		t.Fatal(err)
	}

	list, err := d.ListLinkSharesForUser(u.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != active.ID {
		t.Fatalf("expected only active link, got %+v", list)
	}

	adminList, err := d.ListLinkSharesForUser(u.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(adminList) != 1 {
		t.Fatalf("admin list should exclude revoked, got %d", len(adminList))
	}

	if err := d.DeleteLinkShare(active.ID); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetLinkShareByID(active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected deleted link gone, got %+v", got)
	}
	list, err = d.ListLinkSharesForUser(u.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list after delete, got %+v", list)
	}

	// soft-revoked leftover still excluded after a later create
	exp := time.Now().Add(time.Hour)
	_, err = d.CreateLinkShare("tok-new", u.ID, "home", "new", false, "New", "", &exp)
	if err != nil {
		t.Fatal(err)
	}
	list, err = d.ListLinkSharesForUser(u.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Token != "tok-new" {
		t.Fatalf("unexpected list: %+v", list)
	}
}
