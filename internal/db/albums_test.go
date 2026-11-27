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

func TestAlbumCoverPersistenceAndClear(t *testing.T) {
	d := testDB(t)
	if err := d.SetAlbum(1, "home", "Photos"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetAlbumCover(1, "home", "Photos", "/Vacation\\sunset.jpg"); err != nil {
		t.Fatal(err)
	}
	a, err := d.GetAlbum(1, "home", "Photos")
	if err != nil || a == nil {
		t.Fatalf("GetAlbum: %v %v", a, err)
	}
	if a.CoverPath != "Vacation/sunset.jpg" {
		t.Fatalf("cover path = %q", a.CoverPath)
	}
	if err := d.SetAlbumCover(1, "home", "Photos", ""); err != nil {
		t.Fatal(err)
	}
	a, err = d.GetAlbum(1, "home", "Photos")
	if err != nil || a == nil {
		t.Fatal(err)
	}
	if a.CoverPath != "" {
		t.Fatalf("expected cleared cover, got %q", a.CoverPath)
	}
}

func TestAlbumCoverRejectsEscapeAndMissingAlbum(t *testing.T) {
	d := testDB(t)
	if err := d.SetAlbumCover(1, "home", "Photos", "x.jpg"); err != ErrAlbumNotFound {
		t.Fatalf("expected ErrAlbumNotFound, got %v", err)
	}
	if err := d.SetAlbum(1, "home", "Photos"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetAlbumCover(1, "home", "Photos", "../outside.jpg"); err == nil {
		t.Fatal("expected error for escaping cover path")
	}
	if err := d.SetAlbumCover(1, "home", "Photos", "ok/../../x.jpg"); err == nil {
		t.Fatal("expected error for nested escape")
	}
}

func TestAlbumCoverMigratesFromLegacyTable(t *testing.T) {
	dir := t.TempDir()
	legacy, err := Open(filepath.Join(dir, "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.SQL.Exec(`DROP TABLE albums`); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.SQL.Exec(`
CREATE TABLE albums (
  owner_id INTEGER NOT NULL DEFAULT 0,
  root_id TEXT NOT NULL,
  path TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (owner_id, root_id, path)
)`); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.SQL.Exec(
		`INSERT INTO albums(owner_id, root_id, path, created_at) VALUES(1,'home','Photos','2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatal(err)
	}
	_ = legacy.SQL.Close()

	d, err := Open(filepath.Join(dir, "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.SQL.Close() })
	a, err := d.GetAlbum(1, "home", "Photos")
	if err != nil || a == nil {
		t.Fatalf("migrated GetAlbum: %v %v", a, err)
	}
	if a.CoverPath != "" {
		t.Fatalf("legacy cover should be empty, got %q", a.CoverPath)
	}
	if err := d.SetAlbumCover(1, "home", "Photos", "hero.jpg"); err != nil {
		t.Fatal(err)
	}
	a, err = d.GetAlbum(1, "home", "Photos")
	if err != nil || a == nil || a.CoverPath != "hero.jpg" {
		t.Fatalf("cover after migrate: %+v %v", a, err)
	}
}

func TestRelocateAndClearAlbumCoversForPath(t *testing.T) {
	d := testDB(t)
	if err := d.SetAlbum(1, "home", "Photos"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetAlbum(1, "home", "Photos/Trip"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetAlbumCover(1, "home", "Photos", "Trip/hero.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetAlbumCover(1, "home", "Photos/Trip", "hero.jpg"); err != nil {
		t.Fatal(err)
	}
	// Rename the album folder first, then relocate covers (same order as afterRename).
	if err := d.RelocateAlbumsForPath(1, "home", "Photos/Trip", "Photos/Vacation"); err != nil {
		t.Fatal(err)
	}
	if err := d.RelocateAlbumCoversForPath(1, "home", "Photos/Trip", "Photos/Vacation"); err != nil {
		t.Fatal(err)
	}
	parent, _ := d.GetAlbum(1, "home", "Photos")
	if parent == nil || parent.CoverPath != "Vacation/hero.jpg" {
		t.Fatalf("parent cover not relocated: %+v", parent)
	}
	child, _ := d.GetAlbum(1, "home", "Photos/Vacation")
	if child == nil || child.CoverPath != "hero.jpg" {
		t.Fatalf("child cover should stay relative after folder rename: %+v", child)
	}

	// Rename the file itself.
	if err := d.RelocateAlbumCoversForPath(1, "home", "Photos/Vacation/hero.jpg", "Photos/Vacation/cover.jpg"); err != nil {
		t.Fatal(err)
	}
	parent, _ = d.GetAlbum(1, "home", "Photos")
	if parent.CoverPath != "Vacation/cover.jpg" {
		t.Fatalf("file rename: parent cover %q", parent.CoverPath)
	}
	child, _ = d.GetAlbum(1, "home", "Photos/Vacation")
	if child.CoverPath != "cover.jpg" {
		t.Fatalf("file rename: child cover %q", child.CoverPath)
	}

	// Move the file out of the parent album.
	if err := d.RelocateAlbumCoversForPath(1, "home", "Photos/Vacation/cover.jpg", "Other/cover.jpg"); err != nil {
		t.Fatal(err)
	}
	parent, _ = d.GetAlbum(1, "home", "Photos")
	if parent.CoverPath != "" {
		t.Fatalf("cover should clear when it leaves the album, got %q", parent.CoverPath)
	}
	child, _ = d.GetAlbum(1, "home", "Photos/Vacation")
	if child.CoverPath != "" {
		t.Fatalf("child cover should clear when the file leaves, got %q", child.CoverPath)
	}

	if err := d.SetAlbumCover(1, "home", "Photos", "keep.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := d.ClearAlbumCoversForPath(1, "home", "Photos/keep.jpg"); err != nil {
		t.Fatal(err)
	}
	parent, _ = d.GetAlbum(1, "home", "Photos")
	if parent.CoverPath != "" {
		t.Fatalf("clear on delete: %q", parent.CoverPath)
	}
}

func TestRelocateAlbumCoversDoesNotCrossOwners(t *testing.T) {
	d := testDB(t)
	_ = d.SetAlbum(1, "home", "Photos")
	_ = d.SetAlbum(2, "home", "Photos")
	_ = d.SetAlbumCover(1, "home", "Photos", "a.jpg")
	_ = d.SetAlbumCover(2, "home", "Photos", "a.jpg")
	if err := d.RelocateAlbumCoversForPath(1, "home", "Photos/a.jpg", "Photos/b.jpg"); err != nil {
		t.Fatal(err)
	}
	a1, _ := d.GetAlbum(1, "home", "Photos")
	a2, _ := d.GetAlbum(2, "home", "Photos")
	if a1.CoverPath != "b.jpg" {
		t.Fatalf("owner 1 cover %q", a1.CoverPath)
	}
	if a2.CoverPath != "a.jpg" {
		t.Fatalf("owner 2 cover should stay, got %q", a2.CoverPath)
	}
}
