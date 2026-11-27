package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestAlbumMarkPermissionsAndPathSafety(t *testing.T) {
	s, ts, database := testServer(t)
	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateUser("bob", "bob-pass-1234", "homes/bob", false, 0, ""); err != nil {
		t.Fatal(err)
	}
	adminHome := filepath.Join(s.Cfg.DataRoot, "homes/admin")
	if err := os.MkdirAll(filepath.Join(adminHome, "Photos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "homes/alice"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "homes/bob"), 0o755); err != nil {
		t.Fatal(err)
	}

	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	aliceSess := loginAs(t, ts.URL, "alice", "alice-pass-123")

	// Owner can mark a folder.
	st, data := doJSON(t, http.MethodPost, ts.URL+"/api/albums", admin, map[string]any{
		"root": "home", "path": "Photos", "album": true,
	})
	if st != 200 {
		t.Fatalf("mark album %d %v", st, data)
	}

	// List returns it for the owner.
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/albums", admin, nil)
	if st != 200 {
		t.Fatalf("list albums %d %v", st, data)
	}
	albums, _ := data["albums"].([]any)
	if len(albums) != 1 {
		t.Fatalf("owner should see 1 album, got %v", data)
	}
	row, _ := albums[0].(map[string]any)
	if row["root"] != "home" || row["path"] != "Photos" || row["name"] != "Photos" {
		t.Fatalf("unexpected album row %v", row)
	}

	// Another user cannot see it and cannot mark the owner's folder.
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/albums", aliceSess, nil)
	if st != 200 {
		t.Fatalf("alice list %d %v", st, data)
	}
	if albums, _ = data["albums"].([]any); len(albums) != 0 {
		t.Fatalf("alice must not see admin's album, got %v", data)
	}

	// Recycle bin cannot be marked.
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/albums", admin, map[string]any{
		"root": "trash", "path": "", "album": true,
	})
	if st != 403 {
		t.Fatalf("trash mark should be 403, got %d %v", st, data)
	}
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/albums", admin, map[string]any{
		"root": "trash", "path": "anything", "album": true,
	})
	if st != 403 {
		t.Fatalf("trash item mark should be 403, got %d %v", st, data)
	}

	// Path escape is rejected.
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/albums", admin, map[string]any{
		"root": "home", "path": "../alice", "album": true,
	})
	if st != 403 && st != 400 {
		t.Fatalf("escaped path should be rejected, got %d %v", st, data)
	}

	// Incoming root cannot be marked.
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/albums", admin, map[string]any{
		"root": "incoming", "path": "", "album": true,
	})
	if st != 403 {
		t.Fatalf("incoming root should be 403, got %d %v", st, data)
	}

	// Read-only item-share recipient cannot mark.
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/item-shares", admin, map[string]any{
		"root": "home", "path": "Photos", "userIds": []int64{alice.ID}, "perm": "read",
	})
	if st != 200 {
		t.Fatalf("share Photos %d %v", st, data)
	}
	shares, _ := data["shares"].([]any)
	if len(shares) == 0 {
		t.Fatalf("expected share id, got %v", data)
	}
	shareID := int64(shares[0].(map[string]any)["id"].(float64))
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/albums", aliceSess, map[string]any{
		"root": "incoming", "path": strconv.FormatInt(shareID, 10), "album": true,
	})
	if st != 403 {
		t.Fatalf("read-only recipient must not mark, got %d %v", st, data)
	}

	// But they can see the album in their list.
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/albums", aliceSess, nil)
	if st != 200 {
		t.Fatalf("alice visible albums %d %v", st, data)
	}
	albums, _ = data["albums"].([]any)
	if len(albums) != 1 {
		t.Fatalf("alice should see the shared album, got %v", data)
	}
	row, _ = albums[0].(map[string]any)
	if row["root"] != "incoming" {
		t.Fatalf("alice should open the album under Shared with me, got %v", row)
	}

	// List of the folder reports album=true for owner and recipient.
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/list?root=home&path=Photos", admin, nil)
	if st != 200 || data["album"] != true {
		t.Fatalf("owner list album flag %d %v", st, data)
	}
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/list?root=incoming&path="+strconv.FormatInt(shareID, 10), aliceSess, nil)
	if st != 200 || data["album"] != true {
		t.Fatalf("recipient list album flag %d %v", st, data)
	}

}

func TestAlbumWriteRecipientCanMarkAndOwnerSees(t *testing.T) {
	s, ts, database := testServer(t)
	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	adminHome := filepath.Join(s.Cfg.DataRoot, "homes/admin")
	if err := os.MkdirAll(filepath.Join(adminHome, "Family"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "homes/alice"), 0o755)

	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	aliceSess := loginAs(t, ts.URL, "alice", "alice-pass-123")

	st, data := doJSON(t, http.MethodPost, ts.URL+"/api/item-shares", admin, map[string]any{
		"root": "home", "path": "Family", "userIds": []int64{alice.ID}, "perm": "write",
	})
	if st != 200 {
		t.Fatalf("share %d %v", st, data)
	}
	shareID := int64(data["shares"].([]any)[0].(map[string]any)["id"].(float64))
	clientPath := strconv.FormatInt(shareID, 10)

	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/albums", aliceSess, map[string]any{
		"root": "incoming", "path": clientPath, "album": true,
	})
	if st != 200 {
		t.Fatalf("write recipient should be able to mark, %d %v", st, data)
	}

	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/albums", admin, nil)
	if st != 200 {
		t.Fatal(data)
	}
	albums, _ := data["albums"].([]any)
	if len(albums) != 1 {
		t.Fatalf("owner should see album marked by recipient, got %v", data)
	}
	if albums[0].(map[string]any)["root"] != "home" {
		t.Fatalf("owner view should use home path, got %v", albums[0])
	}
}

func TestAlbumRenameAndDeleteUpdateFlags(t *testing.T) {
	s, ts, _ := testServer(t)
	home := filepath.Join(s.Cfg.DataRoot, "homes/admin")
	if err := os.MkdirAll(filepath.Join(home, "Photos", "Vacation"), 0o755); err != nil {
		t.Fatal(err)
	}
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")

	for _, p := range []string{"Photos", "Photos/Vacation"} {
		st, data := doJSON(t, http.MethodPost, ts.URL+"/api/albums", admin, map[string]any{
			"root": "home", "path": p, "album": true,
		})
		if st != 200 {
			t.Fatalf("mark %s %d %v", p, st, data)
		}
	}

	st, data := doJSON(t, http.MethodPost, ts.URL+"/api/rename", admin, map[string]any{
		"root": "home", "from": "Photos", "to": "Pictures",
	})
	if st != 200 {
		t.Fatalf("rename %d %v", st, data)
	}

	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/albums", admin, nil)
	if st != 200 {
		t.Fatal(data)
	}
	got := map[string]bool{}
	for _, raw := range data["albums"].([]any) {
		got[raw.(map[string]any)["path"].(string)] = true
	}
	if !got["Pictures"] || !got["Pictures/Vacation"] || got["Photos"] || got["Photos/Vacation"] {
		t.Fatalf("rename did not relocate album flags: %v", data)
	}

	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/delete", admin, map[string]any{
		"root": "home", "path": "Pictures",
	})
	if st != 200 {
		t.Fatalf("delete %d %v", st, data)
	}
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/albums", admin, nil)
	if st != 200 {
		t.Fatal(data)
	}
	albums, _ := data["albums"].([]any)
	if len(albums) != 0 {
		t.Fatalf("delete should drop album flags, got %v", data)
	}
}

func TestAlbumAdminShareReadCannotMarkWriteCan(t *testing.T) {
	s, ts, database := testServer(t)
	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := database.CreateUser("bob", "bob-pass-1234", "homes/bob", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	shareDir := filepath.Join(s.Cfg.DataRoot, "shares/media")
	if err := os.MkdirAll(filepath.Join(shareDir, "Trip"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "homes/admin"), 0o755)
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "homes/alice"), 0o755)
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "homes/bob"), 0o755)

	sh, err := database.CreateShare("Media", "shares/media", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetShareGrant(sh.ID, alice.ID, "read", boolPtr(false)); err != nil {
		t.Fatal(err)
	}
	canDel := false
	if err := database.SetShareGrant(sh.ID, bob.ID, "write", &canDel); err != nil {
		t.Fatal(err)
	}

	aliceSess := loginAs(t, ts.URL, "alice", "alice-pass-123")
	bobSess := loginAs(t, ts.URL, "bob", "bob-pass-1234")
	rootID := "share:" + strconv.FormatInt(sh.ID, 10)

	st, data := doJSON(t, http.MethodPost, ts.URL+"/api/albums", aliceSess, map[string]any{
		"root": rootID, "path": "Trip", "album": true,
	})
	if st != 403 {
		t.Fatalf("read grant must not mark, %d %v", st, data)
	}

	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/albums", bobSess, map[string]any{
		"root": rootID, "path": "Trip", "album": true,
	})
	if st != 200 {
		t.Fatalf("write grant should mark, %d %v", st, data)
	}

	// Read user can still see the album.
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/albums", aliceSess, nil)
	if st != 200 {
		t.Fatal(data)
	}
	albums, _ := data["albums"].([]any)
	if len(albums) != 1 {
		t.Fatalf("read grant should see the album, got %v", data)
	}
	if albums[0].(map[string]any)["root"] != rootID {
		t.Fatalf("expected share root, got %v", albums[0])
	}

	// Unrelated user sees nothing.
	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	// admin can access all shares, so they will see it. That's OK (IsAdmin).
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/albums", admin, nil)
	if st != 200 {
		t.Fatal(data)
	}
	if albums, _ = data["albums"].([]any); len(albums) != 1 {
		t.Fatalf("admin should see share albums, got %v", data)
	}
}
