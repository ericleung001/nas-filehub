package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eric/nas-filehub/internal/db"
)

func TestItemShareWithUsersAndEmail(t *testing.T) {
	s, ts, database := testServer(t)
	trap := &mailTrap{}
	s.MailSend = trap.hook()
	s.NotifySync = true
	enableTestSMTP(t, database)

	home := filepath.Join(s.Cfg.DataRoot, "homes/admin")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "project"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "project", "readme.md"), []byte("docs"), 0o644); err != nil {
		t.Fatal(err)
	}

	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := database.CreateUser("bob", "bob-pass-1234", "homes/bob", false, 0, "bob@example.com")
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.CreateUser("carol", "carol-pass-123", "homes/carol", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}

	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")

	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/directory", admin, nil)
	if st != 200 {
		t.Fatalf("directory %d %v", st, data)
	}
	rawUsers, _ := data["users"].([]any)
	if len(rawUsers) < 3 {
		t.Fatalf("expected other users in directory, got %v", data)
	}
	for _, u := range rawUsers {
		m, _ := u.(map[string]any)
		if _, ok := m["email"]; ok {
			t.Fatalf("directory must not include email: %v", m)
		}
		if _, ok := m["passwordHash"]; ok {
			t.Fatal("directory leaked password hash")
		}
	}

	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/item-shares", admin, map[string]any{
		"root":    "home",
		"path":    "notes.txt",
		"userIds": []int64{alice.ID, bob.ID},
		"perm":    "write", // files are forced to read
	})
	if st != 200 {
		t.Fatalf("share file %d %v", st, data)
	}
	msgs := trap.messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 emails, got %#v", msgs)
	}
	for _, m := range msgs {
		if !strings.Contains(m.Subject, "shared with you") || !strings.Contains(m.Body, "notes.txt") {
			t.Fatalf("unexpected mail: %+v", m)
		}
		if !strings.Contains(m.Body, "Shared with me") {
			t.Fatalf("mail should mention Shared with me: %s", m.Body)
		}
	}

	// Carol has no email; share still succeeds and no extra mail is required to fail the action.
	before := len(trap.messages())
	carol, _ := database.GetUserByUsername("carol")
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/item-shares", admin, map[string]any{
		"root":    "home",
		"path":    "notes.txt",
		"userIds": []int64{carol.ID},
	})
	if st != 200 {
		t.Fatalf("share with carol %d %v", st, data)
	}
	if len(trap.messages()) != before {
		t.Fatalf("carol has no email; no message should be sent")
	}

	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/item-shares", admin, map[string]any{
		"root":    "home",
		"path":    "project",
		"userIds": []int64{alice.ID},
		"perm":    "write",
	})
	if st != 200 {
		t.Fatalf("share folder %d %v", st, data)
	}

	aliceCk := loginAs(t, ts.URL, "alice", "alice-pass-123")
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/roots", aliceCk, nil)
	if st != 200 {
		t.Fatalf("roots %d %v", st, data)
	}
	foundIncoming := false
	for _, r := range data["roots"].([]any) {
		m := r.(map[string]any)
		if m["id"] == "incoming" && m["name"] == "Shared with me" {
			foundIncoming = true
		}
	}
	if !foundIncoming {
		t.Fatalf("alice missing Shared with me: %v", data)
	}

	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/list?root=incoming&path=", aliceCk, nil)
	if st != 200 {
		t.Fatalf("list incoming %d %v", st, data)
	}
	entries, _ := data["entries"].([]any)
	var fileID, folderID string
	for _, e := range entries {
		m := e.(map[string]any)
		switch m["name"] {
		case "notes.txt":
			fileID, _ = m["path"].(string)
		case "project":
			folderID, _ = m["path"].(string)
		}
	}
	if fileID == "" || folderID == "" {
		t.Fatalf("expected notes.txt and project in incoming: %v", entries)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/download?root=incoming&path="+fileID, nil)
	for _, c := range aliceCk {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "hello" {
		t.Fatalf("download %d %s", resp.StatusCode, body)
	}

	// File share is read-only even if write was requested.
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/mkdir", aliceCk, map[string]any{
		"root": "incoming", "path": fileID + "/nope",
	})
	if st != 403 {
		t.Fatalf("mkdir on file share should be 403, got %d %v", st, data)
	}

	// Write grant on folder allows mkdir inside it.
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/mkdir", aliceCk, map[string]any{
		"root": "incoming", "path": folderID + "/src",
	})
	if st != 200 {
		t.Fatalf("mkdir in writable folder %d %v", st, data)
	}
	if _, err := os.Stat(filepath.Join(home, "project", "src")); err != nil {
		t.Fatalf("expected project/src in owner home: %v", err)
	}

	// Recipients cannot delete the shared item itself.
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/delete", aliceCk, map[string]any{
		"root": "incoming", "path": folderID,
	})
	if st != 403 {
		t.Fatalf("delete share root should be 403, got %d %v", st, data)
	}

	// List grants and revoke one.
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/item-shares?root=home&path=notes.txt", admin, nil)
	if st != 200 {
		t.Fatalf("list grants %d %v", st, data)
	}
	var revokeID int64
	for _, sh := range data["shares"].([]any) {
		m := sh.(map[string]any)
		if int64(m["userId"].(float64)) == bob.ID {
			revokeID = int64(m["id"].(float64))
		}
	}
	if revokeID == 0 {
		t.Fatalf("bob grant missing: %v", data)
	}
	st, data = doJSON(t, http.MethodDelete, ts.URL+"/api/item-shares/"+itoa(revokeID), admin, nil)
	if st != 200 {
		t.Fatalf("revoke %d %v", st, data)
	}
	bobCk := loginAs(t, ts.URL, "bob", "bob-pass-1234")
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/list?root=incoming&path=", bobCk, nil)
	if st != 200 {
		t.Fatalf("bob incoming %d %v", st, data)
	}
	for _, e := range data["entries"].([]any) {
		m := e.(map[string]any)
		if m["path"] == fileID {
			t.Fatalf("bob should not still see revoked file: %v", data)
		}
	}

	// Cannot share from trash.
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/item-shares", admin, map[string]any{
		"root": "trash", "path": "x", "userIds": []int64{alice.ID},
	})
	if st != 403 {
		t.Fatalf("share from trash %d %v", st, data)
	}

	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/admin/audit?limit=50", admin, nil)
	if st != 200 {
		t.Fatalf("audit %d %v", st, data)
	}
	raw, _ := json.Marshal(data["events"])
	blob := string(raw)
	if !strings.Contains(blob, "item_share_grant") || !strings.Contains(blob, "item_share_revoke") {
		t.Fatalf("audit missing item share events: %s", blob)
	}
}

func TestShareDirectoryFrequentRecipientsAndSearch(t *testing.T) {
	_, ts, database := testServer(t)

	admin, err := database.GetUserByUsername("admin")
	if err != nil || admin == nil {
		t.Fatalf("admin: %v", err)
	}

	type person struct {
		name, email string
		id          int64
	}
	names := []person{
		{name: "alice"},
		{name: "beau"},
		{name: "cara"},
		{name: "dave"},
		{name: "ed"},
		{name: "fay"},
		{name: "gus", email: "hidden-gus@example.com"},
		{name: "hale"},
	}
	byName := map[string]*db.User{}
	for _, p := range names {
		u, err := database.CreateUser(p.name, p.name+"-pass-123", "homes/"+p.name, false, 0, p.email)
		if err != nil {
			t.Fatal(err)
		}
		byName[p.name] = u
	}
	pending, err := database.CreateUser("pending", "pending-pass-1", "homes/pending", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetUserStatus(pending.ID, db.UserStatusPending); err != nil {
		t.Fatal(err)
	}
	disabled, err := database.CreateUser("zzzdisabled", "disabled-pass-1", "homes/disabled", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	off := true
	if _, err := database.UpdateUser(disabled.ID, db.UserUpdate{Disabled: &off}); err != nil {
		t.Fatal(err)
	}

	grant := func(user string, paths ...string) {
		u := byName[user]
		for _, path := range paths {
			if _, err := database.UpsertItemShare(admin.ID, u.ID, "home", path, false, path, "read"); err != nil {
				t.Fatalf("grant %s %s: %v", user, path, err)
			}
		}
	}
	// dave has the most grants; beau and cara are tied, with beau more recent.
	grant("cara", "c1.txt", "c2.txt", "c3.txt")
	grant("dave", "d1.txt", "d2.txt", "d3.txt", "d4.txt", "d5.txt")
	grant("beau", "b1.txt", "b2.txt", "b3.txt")
	// Disabled user has even more grants but must not appear.
	for i := 0; i < 8; i++ {
		if _, err := database.UpsertItemShare(admin.ID, disabled.ID, "home", "x"+string(rune('a'+i))+".txt", false, "x", "read"); err != nil {
			t.Fatal(err)
		}
	}

	adminCk := loginAs(t, ts.URL, "admin", "admin-pass-123")
	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/directory", adminCk, nil)
	if st != 200 {
		t.Fatalf("directory %d %v", st, data)
	}
	rawUsers, _ := data["users"].([]any)
	if len(rawUsers) != 8 {
		t.Fatalf("expected 8 eligible users, got %v", data)
	}
	for _, u := range rawUsers {
		m, _ := u.(map[string]any)
		if _, ok := m["email"]; ok {
			t.Fatalf("directory must not include email: %v", m)
		}
		name, _ := m["username"].(string)
		if name == "admin" || name == "pending" || name == "zzzdisabled" {
			t.Fatalf("ineligible user in directory: %v", m)
		}
	}
	var freq []int64
	for _, v := range data["frequentUserIds"].([]any) {
		freq = append(freq, int64(v.(float64)))
	}
	want := []int64{byName["dave"].ID, byName["beau"].ID, byName["cara"].ID, byName["alice"].ID, byName["ed"].ID}
	if len(freq) != 5 {
		t.Fatalf("frequentUserIds %v", freq)
	}
	for i := range want {
		if freq[i] != want[i] {
			t.Fatalf("frequentUserIds %v, want %v (dave,beau,cara,alice,ed)", freq, want)
		}
	}

	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/directory?q=hidden-gus", adminCk, nil)
	if st != 200 {
		t.Fatalf("search %d %v", st, data)
	}
	rawUsers, _ = data["users"].([]any)
	if len(rawUsers) != 1 {
		t.Fatalf("email search should return gus only: %v", data)
	}
	m := rawUsers[0].(map[string]any)
	if m["username"] != "gus" {
		t.Fatalf("expected gus, got %v", m)
	}
	if _, ok := m["email"]; ok {
		t.Fatalf("search must not include email: %v", m)
	}
	// frequent IDs stay based on the full eligible set, not the filtered rows.
	freq = freq[:0]
	for _, v := range data["frequentUserIds"].([]any) {
		freq = append(freq, int64(v.(float64)))
	}
	for i := range want {
		if freq[i] != want[i] {
			t.Fatalf("search frequentUserIds %v, want %v", freq, want)
		}
	}

	// Recipients do not see the owner's grant frequency as their own.
	aliceCk := loginAs(t, ts.URL, "alice", "alice-pass-123")
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/directory", aliceCk, nil)
	if st != 200 {
		t.Fatalf("alice directory %d %v", st, data)
	}
	for _, u := range data["users"].([]any) {
		m := u.(map[string]any)
		if m["username"] == "alice" {
			t.Fatalf("directory included self: %v", data)
		}
	}
}

func TestItemShareRegrantSameUser(t *testing.T) {
	s, ts, database := testServer(t)

	home := filepath.Join(s.Cfg.DataRoot, "homes/admin")
	if err := os.MkdirAll(filepath.Join(home, "project"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}

	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	body := map[string]any{
		"root":    "home",
		"path":    "notes.txt",
		"userIds": []int64{alice.ID},
	}
	st, data := doJSON(t, http.MethodPost, ts.URL+"/api/item-shares", admin, body)
	if st != 200 {
		t.Fatalf("first share %d %v", st, data)
	}
	shares, _ := data["shares"].([]any)
	if len(shares) != 1 {
		t.Fatalf("first share payload: %v", data)
	}
	firstID := int64(shares[0].(map[string]any)["id"].(float64))

	// Extra inserts so a pooled connection's last_insert_rowid is unlikely
	// to still be the item_shares row, matching the live re-grant path.
	if _, err := database.SQL.Exec(`CREATE TABLE IF NOT EXISTS test_rowids (id INTEGER PRIMARY KEY AUTOINCREMENT)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err := database.SQL.Exec(`INSERT INTO test_rowids DEFAULT VALUES`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.SQL.Exec(`DELETE FROM test_rowids`); err != nil {
		t.Fatal(err)
	}

	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/item-shares", admin, body)
	if st != 200 {
		t.Fatalf("re-grant same user must succeed, got %d %v", st, data)
	}
	shares, _ = data["shares"].([]any)
	if len(shares) != 1 {
		t.Fatalf("re-grant payload: %v", data)
	}
	againID := int64(shares[0].(map[string]any)["id"].(float64))
	if againID != firstID {
		t.Fatalf("re-grant id=%d, want existing %d", againID, firstID)
	}

	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/item-shares?root=home&path=notes.txt", admin, nil)
	if st != 200 {
		t.Fatalf("list grants %d %v", st, data)
	}
	listed, _ := data["shares"].([]any)
	if len(listed) != 1 || int64(listed[0].(map[string]any)["id"].(float64)) != firstID {
		t.Fatalf("expected one grant after re-share, got %v", data)
	}

	// Re-granting a folder with a new permission updates the existing row.
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/item-shares", admin, map[string]any{
		"root": "home", "path": "project", "userIds": []int64{alice.ID}, "perm": "read",
	})
	if st != 200 {
		t.Fatalf("folder share %d %v", st, data)
	}
	folderID := int64(data["shares"].([]any)[0].(map[string]any)["id"].(float64))
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/item-shares", admin, map[string]any{
		"root": "home", "path": "project", "userIds": []int64{alice.ID}, "perm": "write",
	})
	if st != 200 {
		t.Fatalf("folder re-grant %d %v", st, data)
	}
	if int64(data["shares"].([]any)[0].(map[string]any)["id"].(float64)) != folderID {
		t.Fatalf("folder re-grant should keep id %d: %v", folderID, data)
	}
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/item-shares?root=home&path=project", admin, nil)
	if st != 200 {
		t.Fatalf("list folder grants %d %v", st, data)
	}
	listed, _ = data["shares"].([]any)
	if len(listed) != 1 {
		t.Fatalf("expected one folder grant, got %v", data)
	}
	m := listed[0].(map[string]any)
	if int64(m["id"].(float64)) != folderID || m["perm"] != "write" {
		t.Fatalf("folder grant should be write on the same row: %v", data)
	}
}

func entryByName(entries []any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, e := range entries {
		m, _ := e.(map[string]any)
		if m == nil {
			continue
		}
		name, _ := m["name"].(string)
		if name != "" {
			out[name] = m
		}
	}
	return out
}

func TestItemShareNestedFolderListing(t *testing.T) {
	s, ts, database := testServer(t)
	home := filepath.Join(s.Cfg.DataRoot, "homes/admin")
	// temp/chi/next/notes.txt and a sibling under chi, plus a file outside the share.
	for _, dir := range []string{
		filepath.Join(home, "temp", "chi", "next"),
		filepath.Join(home, "temp", "chi", "also"),
		filepath.Join(home, "secret"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "temp", "chi", "next", "notes.txt"), []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "temp", "chi", "also", "pic.txt"), []byte("also"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "secret", "hidden.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := database.CreateUser("bob", "bob-pass-1234", "homes/bob", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}

	admin := loginAs(t, ts.URL, "admin", "admin-pass-123")
	st, data := doJSON(t, http.MethodPost, ts.URL+"/api/item-shares", admin, map[string]any{
		"root": "home", "path": "temp", "userIds": []int64{alice.ID}, "perm": "read",
	})
	if st != 200 {
		t.Fatalf("share temp read %d %v", st, data)
	}
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/item-shares", admin, map[string]any{
		"root": "home", "path": "temp", "userIds": []int64{bob.ID}, "perm": "write",
	})
	if st != 200 {
		t.Fatalf("share temp write %d %v", st, data)
	}

	aliceCk := loginAs(t, ts.URL, "alice", "alice-pass-123")
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/list?root=incoming&path=", aliceCk, nil)
	if st != 200 {
		t.Fatalf("list incoming root %d %v", st, data)
	}
	rootEntries := entryByName(data["entries"].([]any))
	temp, ok := rootEntries["temp"]
	if !ok {
		t.Fatalf("expected temp in Shared with me: %v", data)
	}
	tempID, _ := temp["path"].(string)
	if tempID == "" || strings.Contains(tempID, "/") || tempID == "temp" {
		t.Fatalf("shared folder path should be the share id, got %q", tempID)
	}

	// List the share root: must expose child paths as {id}/chi, not owner path temp/chi.
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/list?root=incoming&path="+tempID, aliceCk, nil)
	if st != 200 {
		t.Fatalf("list share root %d %v", st, data)
	}
	if data["path"] != tempID {
		t.Fatalf("list path should be incoming path %s, got %v", tempID, data["path"])
	}
	if data["perm"] != "read" {
		t.Fatalf("read grant perm %v", data["perm"])
	}
	children := entryByName(data["entries"].([]any))
	chi, ok := children["chi"]
	if !ok || chi["isDir"] != true {
		t.Fatalf("expected folder chi under temp: %v", data)
	}
	wantChi := tempID + "/chi"
	if chi["path"] != wantChi {
		t.Fatalf("chi path %v, want %s (owner path would break the next list)", chi["path"], wantChi)
	}

	// Opening chi must show the next level of folders.
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/list?root=incoming&path="+wantChi, aliceCk, nil)
	if st != 200 {
		t.Fatalf("list chi %d %v", st, data)
	}
	if data["path"] != wantChi {
		t.Fatalf("chi list path %v, want %s", data["path"], wantChi)
	}
	grand := entryByName(data["entries"].([]any))
	for _, name := range []string{"next", "also"} {
		e, ok := grand[name]
		if !ok || e["isDir"] != true {
			t.Fatalf("expected folder %s under chi: %v", name, data)
		}
		want := wantChi + "/" + name
		if e["path"] != want {
			t.Fatalf("%s path %v, want %s", name, e["path"], want)
		}
	}

	// Grandchild file.
	nextPath := wantChi + "/next"
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/list?root=incoming&path="+nextPath, aliceCk, nil)
	if st != 200 {
		t.Fatalf("list next %d %v", st, data)
	}
	files := entryByName(data["entries"].([]any))
	notes, ok := files["notes.txt"]
	if !ok || notes["isDir"] != false {
		t.Fatalf("expected notes.txt under chi/next: %v", data)
	}
	if notes["path"] != nextPath+"/notes.txt" {
		t.Fatalf("notes path %v, want %s", notes["path"], nextPath+"/notes.txt")
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/download?root=incoming&path="+nextPath+"/notes.txt", nil)
	for _, c := range aliceCk {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "nested" {
		t.Fatalf("download nested %d %s", resp.StatusCode, body)
	}

	// Must not escape the share root into a sibling of temp.
	for _, p := range []string{
		tempID + "/../secret",
		tempID + "/chi/../../secret",
		tempID + "/chi/../../../homes/admin/secret",
	} {
		st, data = doJSON(t, http.MethodGet, ts.URL+"/api/list?root=incoming&path="+p, aliceCk, nil)
		if st != 403 {
			t.Fatalf("escape list %s: got %d %v, want 403", p, st, data)
		}
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/download?root=incoming&path="+p+"/hidden.txt", nil)
		for _, c := range aliceCk {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 403 && resp.StatusCode != 404 {
			t.Fatalf("escape download %s: got %d, want 403/404", p, resp.StatusCode)
		}
	}

	// Read grant cannot mkdir; write grant can create a folder at depth and then list it.
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/mkdir", aliceCk, map[string]any{
		"root": "incoming", "path": wantChi + "/from-alice",
	})
	if st != 403 {
		t.Fatalf("alice mkdir should be 403, got %d %v", st, data)
	}

	bobCk := loginAs(t, ts.URL, "bob", "bob-pass-1234")
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/list?root=incoming&path=", bobCk, nil)
	if st != 200 {
		t.Fatalf("bob incoming %d %v", st, data)
	}
	bobTemp := entryByName(data["entries"].([]any))["temp"]
	if bobTemp == nil {
		t.Fatalf("bob missing temp: %v", data)
	}
	bobID, _ := bobTemp["path"].(string)
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/list?root=incoming&path="+bobID+"/chi", bobCk, nil)
	if st != 200 || data["perm"] != "write" {
		t.Fatalf("bob list chi %d perm=%v %v", st, data["perm"], data)
	}
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/mkdir", bobCk, map[string]any{
		"root": "incoming", "path": bobID + "/chi/from-bob",
	})
	if st != 200 {
		t.Fatalf("bob mkdir in nested folder %d %v", st, data)
	}
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/list?root=incoming&path="+bobID+"/chi", bobCk, nil)
	if st != 200 {
		t.Fatalf("bob re-list chi %d %v", st, data)
	}
	created := entryByName(data["entries"].([]any))["from-bob"]
	if created == nil || created["isDir"] != true || created["path"] != bobID+"/chi/from-bob" {
		t.Fatalf("bob should see from-bob under chi: %v", data)
	}
	if _, err := os.Stat(filepath.Join(home, "temp", "chi", "from-bob")); err != nil {
		t.Fatalf("expected from-bob in owner home: %v", err)
	}
}

func TestAdminSharedFolderNestedListing(t *testing.T) {
	s, ts, database := testServer(t)
	admin, err := database.GetUserByUsername("admin")
	if err != nil || admin == nil {
		t.Fatal(err)
	}
	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := database.CreateUser("bob", "bob-pass-1234", "homes/bob", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(s.Cfg.DataRoot, "shared", "other", "hidden")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "hidden.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	sh, err := database.CreateShare("temp", "shared/temp", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(s.Cfg.DataRoot, "shared", "temp")
	for _, dir := range []string{
		filepath.Join(root, "chi", "next"),
		filepath.Join(root, "chi", "also"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "chi", "next", "notes.txt"), []byte("shared-nested"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := database.SetShareGrant(sh.ID, alice.ID, "read", ptrBool(false)); err != nil {
		t.Fatal(err)
	}
	if err := database.SetShareGrant(sh.ID, bob.ID, "write", ptrBool(false)); err != nil {
		t.Fatal(err)
	}

	rootID := "share:" + itoa(sh.ID)
	aliceCk := loginAs(t, ts.URL, "alice", "alice-pass-123")

	st, data := doJSON(t, http.MethodGet, ts.URL+"/api/list?root="+rootID+"&path=", aliceCk, nil)
	if st != 200 {
		t.Fatalf("list share root %d %v", st, data)
	}
	children := entryByName(data["entries"].([]any))
	chi, ok := children["chi"]
	if !ok || chi["isDir"] != true || chi["path"] != "chi" {
		t.Fatalf("expected chi at share root: %v", data)
	}

	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/list?root="+rootID+"&path=chi", aliceCk, nil)
	if st != 200 {
		t.Fatalf("list chi %d %v", st, data)
	}
	grand := entryByName(data["entries"].([]any))
	for _, name := range []string{"next", "also"} {
		e, ok := grand[name]
		if !ok || e["isDir"] != true || e["path"] != "chi/"+name {
			t.Fatalf("expected folder %s under chi: %v", name, data)
		}
	}

	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/list?root="+rootID+"&path=chi/next", aliceCk, nil)
	if st != 200 {
		t.Fatalf("list next %d %v", st, data)
	}
	notes := entryByName(data["entries"].([]any))["notes.txt"]
	if notes == nil || notes["isDir"] != false || notes["path"] != "chi/next/notes.txt" {
		t.Fatalf("expected notes.txt under chi/next: %v", data)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/download?root="+rootID+"&path=chi/next/notes.txt", nil)
	for _, c := range aliceCk {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "shared-nested" {
		t.Fatalf("download nested %d %s", resp.StatusCode, body)
	}

	for _, p := range []string{"../other", "chi/../../other", "../other/hidden"} {
		st, data = doJSON(t, http.MethodGet, ts.URL+"/api/list?root="+rootID+"&path="+p, aliceCk, nil)
		if st != 403 && st != 400 {
			t.Fatalf("escape list %s: got %d %v, want 403/400", p, st, data)
		}
	}

	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/mkdir", aliceCk, map[string]any{
		"root": rootID, "path": "chi/from-alice",
	})
	if st != 403 {
		t.Fatalf("alice mkdir should be 403, got %d %v", st, data)
	}

	bobCk := loginAs(t, ts.URL, "bob", "bob-pass-1234")
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/mkdir", bobCk, map[string]any{
		"root": rootID, "path": "chi/from-bob",
	})
	if st != 200 {
		t.Fatalf("bob mkdir in nested folder %d %v", st, data)
	}
	st, data = doJSON(t, http.MethodGet, ts.URL+"/api/list?root="+rootID+"&path=chi", bobCk, nil)
	if st != 200 || data["perm"] != "write" {
		t.Fatalf("bob list chi %d perm=%v %v", st, data["perm"], data)
	}
	created := entryByName(data["entries"].([]any))["from-bob"]
	if created == nil || created["isDir"] != true || created["path"] != "chi/from-bob" {
		t.Fatalf("bob should see from-bob under chi: %v", data)
	}
}
