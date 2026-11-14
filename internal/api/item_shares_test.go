package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
