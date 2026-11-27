package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func writeTestPhoto(t *testing.T, path string, body []byte, mt time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if !mt.IsZero() {
		if err := os.Chtimes(path, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAlbumManualCoverSetListAndClear(t *testing.T) {
	s, ts, _ := testServer(t)
	home := filepath.Join(s.Cfg.DataRoot, "homes/admin")
	if err := os.MkdirAll(filepath.Join(home, "Photos"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	writeTestPhoto(t, filepath.Join(home, "Photos", "old.jpg"), []byte("old"), old)
	writeTestPhoto(t, filepath.Join(home, "Photos", "new.jpg"), []byte("new"), newer)

	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	st, data := doJSON(t, http.MethodPost, ts.URL+"/api/albums", admin, map[string]any{
		"root": "home", "path": "Photos", "album": true,
	})
	if st != 200 {
		t.Fatalf("mark album %d %v", st, data)
	}

	// Auto cover is the newest photo until a manual cover is set.
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/albums", admin, nil)
	if st != 200 {
		t.Fatal(data)
	}
	row := data["albums"].([]any)[0].(map[string]any)
	if row["coverPath"] != "Photos/new.jpg" {
		t.Fatalf("auto cover %v", row)
	}

	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/albums", admin, map[string]any{
		"root": "home", "path": "Photos", "cover": "old.jpg",
	})
	if st != 200 {
		t.Fatalf("set cover %d %v", st, data)
	}
	if data["manualCover"] != true || data["coverPath"] != "Photos/old.jpg" {
		t.Fatalf("set cover response %v", data)
	}

	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/albums", admin, nil)
	row = data["albums"].([]any)[0].(map[string]any)
	if row["coverPath"] != "Photos/old.jpg" {
		t.Fatalf("list cover after set %v", row)
	}

	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/list?root=home&path=Photos", admin, nil)
	if st != 200 {
		t.Fatal(data)
	}
	if data["album"] != true || data["coverPath"] != "Photos/old.jpg" || data["manualCover"] != true || data["albumPath"] != "Photos" {
		t.Fatalf("list folder cover fields %v", data)
	}

	// Reject a cover that is not in the album, not media, or escapes.
	for _, cover := range []string{"../secret.jpg", "missing.jpg", "notes.txt"} {
		if cover == "notes.txt" {
			writeTestPhoto(t, filepath.Join(home, "Photos", "notes.txt"), []byte("x"), newer)
		}
		st, data = doJSON(t, http.MethodPost, ts.URL+"/api/albums", admin, map[string]any{
			"root": "home", "path": "Photos", "cover": cover,
		})
		if st != 400 {
			t.Fatalf("cover %q should be 400, got %d %v", cover, st, data)
		}
	}

	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/albums", admin, map[string]any{
		"root": "home", "path": "Photos", "cover": "",
	})
	if st != 200 {
		t.Fatalf("clear cover %d %v", st, data)
	}
	if _, ok := data["manualCover"]; ok {
		t.Fatalf("cleared cover should not be manual: %v", data)
	}
	if data["coverPath"] != "Photos/new.jpg" {
		t.Fatalf("clear should fall back to auto newest, got %v", data)
	}
}

func TestAlbumCoverRenameAndDeleteFollowFile(t *testing.T) {
	s, ts, _ := testServer(t)
	home := filepath.Join(s.Cfg.DataRoot, "homes/admin")
	writeTestPhoto(t, filepath.Join(home, "Photos", "hero.jpg"), []byte("hero"), time.Now())
	writeTestPhoto(t, filepath.Join(home, "Photos", "other.jpg"), []byte("other"), time.Now().Add(-time.Hour))
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	if st, data := doJSON(t, http.MethodPost, ts.URL+"/api/albums", admin, map[string]any{
		"root": "home", "path": "Photos", "album": true, "cover": "hero.jpg",
	}); st != 200 {
		t.Fatalf("mark+cover %d %v", st, data)
	}

	st, data := doJSON(t, http.MethodPost, ts.URL+"/api/rename", admin, map[string]any{
		"root": "home", "from": "Photos/hero.jpg", "to": "Photos/cover.jpg",
	})
	if st != 200 {
		t.Fatalf("rename %d %v", st, data)
	}
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/albums", admin, nil)
	row := data["albums"].([]any)[0].(map[string]any)
	if row["coverPath"] != "Photos/cover.jpg" {
		t.Fatalf("cover after rename %v", row)
	}

	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/delete", admin, map[string]any{
		"root": "home", "path": "Photos/cover.jpg",
	})
	if st != 200 {
		t.Fatalf("delete %d %v", st, data)
	}
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/albums", admin, nil)
	row = data["albums"].([]any)[0].(map[string]any)
	if row["coverPath"] != "Photos/other.jpg" {
		t.Fatalf("cover after delete should fall back, got %v", row)
	}

	// A cover file removed outside Filehub is not a broken path — automatic cover is used.
	adminUser, err := s.DB.GetUserByUsername("admin")
	if err != nil || adminUser == nil {
		t.Fatalf("admin user: %v", err)
	}
	if err := s.DB.SetAlbumCover(adminUser.ID, "home", "Photos", "missing-outside.jpg"); err != nil {
		t.Fatal(err)
	}
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/albums", admin, nil)
	row = data["albums"].([]any)[0].(map[string]any)
	if row["coverPath"] != "Photos/other.jpg" {
		t.Fatalf("missing on disk should fall back, got %v", row)
	}
}

func TestAlbumReadOnlyUserCannotSetCoverButCanSeeIt(t *testing.T) {
	s, ts, database := testServer(t)
	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "homes/alice"), 0o755)
	home := filepath.Join(s.Cfg.DataRoot, "homes/admin")
	writeTestPhoto(t, filepath.Join(home, "Photos", "hero.jpg"), []byte("hero"), time.Now())
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	if st, data := doJSON(t, http.MethodPost, ts.URL+"/api/albums", admin, map[string]any{
		"root": "home", "path": "Photos", "album": true, "cover": "hero.jpg",
	}); st != 200 {
		t.Fatalf("mark %d %v", st, data)
	}
	// Share the album folder read-only with alice.
	st, data := doJSON(t, http.MethodPost, ts.URL+"/api/item-shares", admin, map[string]any{
		"root": "home", "path": "Photos", "userIds": []int64{alice.ID}, "perm": "read",
	})
	if st != 200 {
		t.Fatalf("share Photos %d %v", st, data)
	}
	shareID := int64(data["shares"].([]any)[0].(map[string]any)["id"].(float64))
	clientPath := strconv.FormatInt(shareID, 10)

	aliceSess := loginAs(t, ts.URL, "alice", "alice-pass-123")
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/albums", aliceSess, nil)
	if st != 200 {
		t.Fatal(data)
	}
	albums, _ := data["albums"].([]any)
	if len(albums) != 1 {
		t.Fatalf("alice should see the shared album, got %v", data)
	}
	row := albums[0].(map[string]any)
	if row["root"] != "incoming" {
		t.Fatalf("alice view %v", row)
	}
	cp, _ := row["coverPath"].(string)
	if cp != clientPath+"/hero.jpg" && cp != "hero.jpg" {
		t.Fatalf("alice should see the chosen cover, got %v", row)
	}

	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/albums", aliceSess, map[string]any{
		"root": "incoming", "path": clientPath, "cover": "hero.jpg",
	})
	if st != 403 {
		t.Fatalf("read-only user should not set cover, got %d %v", st, data)
	}
}

func TestStaleAlbumRowCleanedWhenFolderMissingOnHealthyRoot(t *testing.T) {
	s, ts, _ := testServer(t)
	home := filepath.Join(s.Cfg.DataRoot, "homes/admin")
	if err := os.MkdirAll(filepath.Join(home, "Photos"), 0o755); err != nil {
		t.Fatal(err)
	}
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	if st, data := doJSON(t, http.MethodPost, ts.URL+"/api/albums", admin, map[string]any{
		"root": "home", "path": "Photos", "album": true,
	}); st != 200 {
		t.Fatalf("mark %d %v", st, data)
	}
	if err := os.RemoveAll(filepath.Join(home, "Photos")); err != nil {
		t.Fatal(err)
	}
	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/albums", admin, nil)
	if st != 200 {
		t.Fatal(data)
	}
	if albums, _ := data["albums"].([]any); len(albums) != 0 {
		t.Fatalf("missing folder on a healthy root should be cleaned, got %v", data)
	}
	ok, err := s.DB.IsAlbum(1, "home", "Photos")
	if err != nil || ok {
		t.Fatalf("stale row should be deleted, ok=%v err=%v", ok, err)
	}
}

func TestStaleAlbumRowLeftAloneWhenRootInaccessible(t *testing.T) {
	s, ts, _ := testServer(t)
	home := filepath.Join(s.Cfg.DataRoot, "homes/admin")
	if err := os.MkdirAll(filepath.Join(home, "Photos"), 0o755); err != nil {
		t.Fatal(err)
	}
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	if st, data := doJSON(t, http.MethodPost, ts.URL+"/api/albums", admin, map[string]any{
		"root": "home", "path": "Photos", "album": true,
	}); st != 200 {
		t.Fatalf("mark %d %v", st, data)
	}
	// Unmount-style: the entire home root is gone, but the album row must stay.
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/albums", admin, nil)
	if st != 200 {
		t.Fatal(data)
	}
	if albums, _ := data["albums"].([]any); len(albums) != 0 {
		t.Fatalf("inaccessible root should omit from the sidebar but not imply a row, got %v", data)
	}
	ok, err := s.DB.IsAlbum(1, "home", "Photos")
	if err != nil || !ok {
		t.Fatalf("row must be left alone when the home root is gone, ok=%v err=%v", ok, err)
	}
}
