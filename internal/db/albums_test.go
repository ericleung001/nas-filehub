package db

import (
	"path/filepath"
	"testing"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.SQL.Close() })
	return d
}

func TestAlbumPersistenceAndPathNormalize(t *testing.T) {
	d := testDB(t)
	if err := d.SetAlbum(1, "home", "/Photos\\Vacation/"); err != nil {
		t.Fatal(err)
	}
	ok, err := d.IsAlbum(1, "home", "Photos/Vacation")
	if err != nil || !ok {
		t.Fatalf("expected album after set, ok=%v err=%v", ok, err)
	}
	a, err := d.GetAlbum(1, "home", "Photos/Vacation")
	if err != nil || a == nil {
		t.Fatalf("GetAlbum: %v %v", a, err)
	}
	if a.Path != "Photos/Vacation" || a.RootID != "home" || a.OwnerID != 1 {
		t.Fatalf("unexpected album %+v", a)
	}
	// Idempotent mark.
	if err := d.SetAlbum(1, "home", "Photos/Vacation"); err != nil {
		t.Fatal(err)
	}
	list, err := d.ListAlbums()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 album, got %d", len(list))
	}
	if err := d.DeleteAlbum(1, "home", "Photos/Vacation"); err != nil {
		t.Fatal(err)
	}
	ok, err = d.IsAlbum(1, "home", "Photos/Vacation")
	if err != nil || ok {
		t.Fatalf("expected flag cleared, ok=%v err=%v", ok, err)
	}
}

func TestAlbumHomePathsDoNotCollideAcrossUsers(t *testing.T) {
	d := testDB(t)
	if err := d.SetAlbum(1, "home", "Photos"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetAlbum(2, "home", "Photos"); err != nil {
		t.Fatal(err)
	}
	ok1, _ := d.IsAlbum(1, "home", "Photos")
	ok2, _ := d.IsAlbum(2, "home", "Photos")
	if !ok1 || !ok2 {
		t.Fatalf("both users should have their own Photos album, %v %v", ok1, ok2)
	}
	if err := d.DeleteAlbum(1, "home", "Photos"); err != nil {
		t.Fatal(err)
	}
	ok1, _ = d.IsAlbum(1, "home", "Photos")
	ok2, _ = d.IsAlbum(2, "home", "Photos")
	if ok1 || !ok2 {
		t.Fatalf("deleting user 1 must not remove user 2, %v %v", ok1, ok2)
	}
}

func TestAlbumRejectsTrashAndIncomingRoots(t *testing.T) {
	d := testDB(t)
	if err := d.SetAlbum(1, "trash", "item"); err == nil {
		t.Fatal("expected error for trash root")
	}
	if err := d.SetAlbum(1, "incoming", "1"); err == nil {
		t.Fatal("expected error for incoming root")
	}
	list, err := d.ListAlbums()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("no albums should be stored, got %v", list)
	}
}

func TestRelocateAndDeleteAlbumsForPath(t *testing.T) {
	d := testDB(t)
	if err := d.SetAlbum(1, "home", "Photos"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetAlbum(1, "home", "Photos/Vacation"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetAlbum(1, "home", "Other"); err != nil {
		t.Fatal(err)
	}
	if err := d.RelocateAlbumsForPath(1, "home", "Photos", "Pictures"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := d.IsAlbum(1, "home", "Photos"); ok {
		t.Fatal("old path should be gone")
	}
	if ok, _ := d.IsAlbum(1, "home", "Pictures"); !ok {
		t.Fatal("renamed album missing")
	}
	if ok, _ := d.IsAlbum(1, "home", "Pictures/Vacation"); !ok {
		t.Fatal("nested album path not updated")
	}
	if ok, _ := d.IsAlbum(1, "home", "Other"); !ok {
		t.Fatal("unrelated album was changed")
	}
	n, err := d.DeleteAlbumsForPath(1, "home", "Pictures")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 rows deleted, got %d", n)
	}
	if ok, _ := d.IsAlbum(1, "home", "Pictures"); ok {
		t.Fatal("parent album still present")
	}
	if ok, _ := d.IsAlbum(1, "home", "Pictures/Vacation"); ok {
		t.Fatal("child album still present")
	}
	if ok, _ := d.IsAlbum(1, "home", "Other"); !ok {
		t.Fatal("unrelated album should remain")
	}
}

func TestRelocateAlbumsDoesNotCrossOwners(t *testing.T) {
	d := testDB(t)
	_ = d.SetAlbum(1, "home", "Photos")
	_ = d.SetAlbum(2, "home", "Photos")
	if err := d.RelocateAlbumsForPath(1, "home", "Photos", "Pictures"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := d.IsAlbum(2, "home", "Photos"); !ok {
		t.Fatal("other owner's album path must stay")
	}
	if ok, _ := d.IsAlbum(1, "home", "Pictures"); !ok {
		t.Fatal("owner 1 relocate failed")
	}
}
