package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func davDo(t *testing.T, method, url, user, pass, body, dest string, depth string) (int, string, http.Header) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	if dest != "" {
		req.Header.Set("Destination", dest)
	}
	if depth != "" {
		req.Header.Set("Depth", depth)
	}
	if method == "PROPFIND" || method == "PROPPATCH" {
		req.Header.Set("Content-Type", "application/xml")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestWebDAVAuthAndCRUD(t *testing.T) {
	_, ts, database := testServer(t)
	if _, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "alice@example.com"); err != nil {
		t.Fatal(err)
	}

	st, _, _ := davDo(t, "PROPFIND", ts.URL+"/dav/", "", "", "", "", "1")
	if st != 401 {
		t.Fatalf("anon propfind %d", st)
	}
	st, _, hdr := davDo(t, http.MethodOptions, ts.URL+"/dav/", "", "", "", "", "")
	if st != 200 {
		t.Fatalf("anon options %d", st)
	}
	if !strings.Contains(hdr.Get("DAV"), "1") {
		t.Fatalf("DAV header %v", hdr)
	}
	st, _, _ = davDo(t, "PUT", ts.URL+"/dav/My%20Files/secret.txt", "", "", "nope", "", "")
	if st != 401 {
		t.Fatalf("anon put %d", st)
	}
	st, _, _ = davDo(t, "PROPFIND", ts.URL+"/dav/", "alice", "wrong-pass", "", "", "0")
	if st != 401 {
		t.Fatalf("bad password %d", st)
	}

	st, body, _ := davDo(t, "PROPFIND", ts.URL+"/dav/", "alice", "alice-pass-123", "", "", "1")
	if st != 207 && st != 200 {
		t.Fatalf("propfind root %d %s", st, body)
	}
	if !strings.Contains(body, "My Files") && !strings.Contains(body, "My%20Files") {
		t.Fatalf("root listing missing My Files: %s", body)
	}

	st, body, _ = davDo(t, "MKCOL", ts.URL+"/dav/My%20Files/album", "alice", "alice-pass-123", "", "", "")
	if st != 201 && st != 200 {
		t.Fatalf("mkcol %d %s", st, body)
	}
	st, body, _ = davDo(t, "PUT", ts.URL+"/dav/My%20Files/album/note.txt", "alice", "alice-pass-123", "hello-dav", "", "")
	if st != 201 && st != 200 && st != 204 {
		t.Fatalf("put %d %s", st, body)
	}
	st, body, _ = davDo(t, http.MethodGet, ts.URL+"/dav/My%20Files/album/note.txt", "alice", "alice-pass-123", "", "", "")
	if st != 200 || body != "hello-dav" {
		t.Fatalf("get %d %q", st, body)
	}

	dest := ts.URL + "/dav/My%20Files/album/copy.txt"
	st, body, _ = davDo(t, "COPY", ts.URL+"/dav/My%20Files/album/note.txt", "alice", "alice-pass-123", "", dest, "")
	if st != 201 && st != 204 && st != 200 {
		t.Fatalf("copy %d %s", st, body)
	}
	dest = ts.URL + "/dav/My%20Files/album/moved.txt"
	st, body, _ = davDo(t, "MOVE", ts.URL+"/dav/My%20Files/album/copy.txt", "alice", "alice-pass-123", "", dest, "")
	if st != 201 && st != 204 && st != 200 {
		t.Fatalf("move %d %s", st, body)
	}

	st, body, _ = davDo(t, "DELETE", ts.URL+"/dav/My%20Files/album/note.txt", "alice", "alice-pass-123", "", "", "")
	if st != 204 && st != 200 {
		t.Fatalf("delete %d %s", st, body)
	}
	c := loginAs(t, ts.URL, "alice", "alice-pass-123")
	items := listTrash(t, ts.URL, c)
	if len(items) != 1 || items[0]["name"] != "note.txt" {
		t.Fatalf("webdav delete should trash, got %#v", items)
	}
}

func TestWebDAVSharePermissions(t *testing.T) {
	s, ts, database := testServer(t)
	alice, err := database.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := database.CreateUser("bob", "bob-pass-123", "homes/bob", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(s.Cfg.DataRoot, "shared/team"), 0o755)
	if err := os.WriteFile(filepath.Join(s.Cfg.DataRoot, "shared/team/readme.txt"), []byte("team"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh, err := database.CreateShare("Team", "shared/team", alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.SetShareGrant(sh.ID, alice.ID, "write", boolPtr(true)); err != nil {
		t.Fatal(err)
	}
	if err := database.SetShareGrant(sh.ID, bob.ID, "read", boolPtr(false)); err != nil {
		t.Fatal(err)
	}
	_ = alice
	st, body, _ := davDo(t, http.MethodGet, ts.URL+"/dav/Team/readme.txt", "bob", "bob-pass-123", "", "", "")
	if st != 200 || body != "team" {
		t.Fatalf("bob get %d %q", st, body)
	}
	st, _, _ = davDo(t, "PUT", ts.URL+"/dav/Team/new.txt", "bob", "bob-pass-123", "x", "", "")
	// x/net/webdav maps OpenFile permission errors to 404.
	if st != 403 && st != 404 && st != 405 {
		t.Fatalf("bob put on read share %d", st)
	}
	st, _, _ = davDo(t, "PUT", ts.URL+"/dav/Team/new.txt", "alice", "alice-pass-123", "ok", "", "")
	if st != 201 && st != 200 && st != 204 {
		t.Fatalf("alice put %d", st)
	}
}

func TestWebDAVDisabled(t *testing.T) {
	s, ts, _ := testServer(t)
	s.Cfg.WebDAVEnabled = false
	st, _, _ := davDo(t, "PROPFIND", ts.URL+"/dav/", "admin", "admin-pass-123", "", "", "0")
	if st != 404 {
		t.Fatalf("disabled webdav %d", st)
	}
}
