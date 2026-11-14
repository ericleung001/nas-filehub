package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPickFrequentRecipientIDs(t *testing.T) {
	eligible := []DirectoryUser{
		{ID: 1, Username: "alice"},
		{ID: 2, Username: "beau"},
		{ID: 3, Username: "cara"},
		{ID: 4, Username: "dave"},
		{ID: 5, Username: "ed"},
		{ID: 6, Username: "fay"},
		{ID: 7, Username: "gus"},
		{ID: 8, Username: "hale"},
	}
	older := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	stats := []RecipientGrantStat{
		{UserID: 4, GrantCount: 5, LastGrantedAt: older},   // dave
		{UserID: 2, GrantCount: 3, LastGrantedAt: newer},   // beau, same count as cara but more recent
		{UserID: 3, GrantCount: 3, LastGrantedAt: older},   // cara
		{UserID: 99, GrantCount: 50, LastGrantedAt: newer}, // ineligible / disabled
	}
	got := PickFrequentRecipientIDs(eligible, stats, 5)
	want := []int64{4, 2, 3, 1, 5} // dave, beau, cara, then alice, ed
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("frequent IDs %v, want %v", got, want)
		}
	}

	// Fewer than 5 eligible: return all, frequent first then A–Z fill.
	small := eligible[:3]
	got = PickFrequentRecipientIDs(small, stats, 5)
	want = []int64{2, 3, 1} // beau, cara, alice
	if len(got) != len(want) {
		t.Fatalf("small len=%d want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("small frequent IDs %v, want %v", got, want)
		}
	}

	if n := PickFrequentRecipientIDs(nil, stats, 5); len(n) != 0 {
		t.Fatalf("empty eligible: %v", n)
	}
}

func TestRecipientGrantStatsRecordAndBackfill(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.SQL.Close() })

	owner, err := d.CreateUser("owner", "owner-pass-123", "homes/owner", true, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	alice, err := d.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := d.CreateUser("bob", "bob-pass-1234", "homes/bob", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := d.UpsertItemShare(owner.ID, alice.ID, "home", "a.txt", false, "a.txt", "read"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.UpsertItemShare(owner.ID, alice.ID, "home", "b.txt", false, "b.txt", "read"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.UpsertItemShare(owner.ID, bob.ID, "home", "a.txt", false, "a.txt", "read"); err != nil {
		t.Fatal(err)
	}

	stats, err := d.ListRecipientGrantStats(owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	byUser := map[int64]RecipientGrantStat{}
	for _, s := range stats {
		byUser[s.UserID] = s
	}
	if byUser[alice.ID].GrantCount != 2 {
		t.Fatalf("alice grant_count=%d want 2", byUser[alice.ID].GrantCount)
	}
	if byUser[bob.ID].GrantCount != 1 {
		t.Fatalf("bob grant_count=%d want 1", byUser[bob.ID].GrantCount)
	}

	// Revoke does not decrease historical grant counts.
	shares, err := d.ListItemSharesForPath(owner.ID, "home", "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, sh := range shares {
		if sh.UserID == bob.ID {
			if err := d.DeleteItemShare(sh.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	stats, err = d.ListRecipientGrantStats(owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	byUser = map[int64]RecipientGrantStat{}
	for _, s := range stats {
		byUser[s.UserID] = s
	}
	if byUser[bob.ID].GrantCount != 1 {
		t.Fatalf("revoke should keep bob grant_count=1, got %d", byUser[bob.ID].GrantCount)
	}

	found, err := d.SearchDirectoryUsers(owner.ID, "alice@example")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != alice.ID || found[0].Username != "alice" {
		t.Fatalf("email search: %+v", found)
	}

	// LIKE metacharacters in the query should not match everything.
	found, err = d.SearchDirectoryUsers(owner.ID, "%")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("literal %% should not wildcard, got %+v", found)
	}
}

func TestRecipientGrantStatsBackfillOnOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := d.CreateUser("owner", "owner-pass-123", "homes/owner", true, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	alice, err := d.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := d.SQL.Exec(
		`INSERT INTO item_shares(owner_id, user_id, root_id, path, is_dir, label, perm, created_at)
		 VALUES(?,?,?,?,0,?, 'read', ?)`,
		owner.ID, alice.ID, "home", "legacy.txt", "legacy.txt", now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SQL.Exec(`DELETE FROM item_share_recipient_stats`); err != nil {
		t.Fatal(err)
	}
	_ = d.SQL.Close()

	d2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d2.SQL.Close() })
	stats, err := d2.ListRecipientGrantStats(owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].UserID != alice.ID || stats[0].GrantCount != 1 {
		t.Fatalf("backfill stats: %+v", stats)
	}
}

func TestUpsertItemShareRegrantSameItem(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.SQL.Close() })

	owner, err := d.CreateUser("owner", "owner-pass-123", "homes/owner", true, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	alice, err := d.CreateUser("alice", "alice-pass-123", "homes/alice", false, 0, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}

	first, err := d.UpsertItemShare(owner.ID, alice.ID, "home", "project", true, "project", "read")
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || first.ID == 0 || first.Perm != "read" {
		t.Fatalf("first grant: %+v", first)
	}

	// Leave last_insert_rowid pointing at a different table, then delete
	// those rows so a stale item_shares lookup would miss. This is the
	// failure mode that produced a 502 on re-grant.
	if _, err := d.SQL.Exec(`CREATE TABLE IF NOT EXISTS test_rowids (id INTEGER PRIMARY KEY AUTOINCREMENT)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err := d.SQL.Exec(`INSERT INTO test_rowids DEFAULT VALUES`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.SQL.Exec(`DELETE FROM test_rowids`); err != nil {
		t.Fatal(err)
	}

	again, err := d.UpsertItemShare(owner.ID, alice.ID, "home", "project", true, "Project folder", "write")
	if err != nil {
		t.Fatalf("re-grant: %v", err)
	}
	if again == nil {
		t.Fatal("re-grant returned nil share")
	}
	if again.ID != first.ID {
		t.Fatalf("re-grant id=%d, want existing %d", again.ID, first.ID)
	}
	if again.Perm != "write" || again.Label != "Project folder" {
		t.Fatalf("re-grant should update perm and label, got %+v", again)
	}

	list, err := d.ListItemSharesForPath(owner.ID, "home", "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != first.ID || list[0].UserID != alice.ID || list[0].Perm != "write" {
		t.Fatalf("expected a single updated grant, got %+v", list)
	}
}
