package db

import (
	"fmt"
	"testing"
)

func TestAuditLogMaxDefaultUnlimited(t *testing.T) {
	d := openTestDB(t)
	max, err := d.GetAuditLogMax()
	if err != nil {
		t.Fatal(err)
	}
	if max != 0 {
		t.Fatalf("default cap want 0 (unlimited), got %d", max)
	}
	for i := 0; i < 25; i++ {
		if err := d.AppendAudit(1, "admin", "127.0.0.1", "login_success", fmt.Sprintf("n=%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	_, total, err := d.ListAudit(200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 25 {
		t.Fatalf("unlimited default should keep all 25 events, got %d", total)
	}
}

func TestAuditLogMaxZeroKeepsAll(t *testing.T) {
	d := openTestDB(t)
	if err := d.SetAuditLogMax(0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := d.AppendAudit(1, "admin", "127.0.0.1", "login_success", fmt.Sprintf("n=%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	events, total, err := d.ListAudit(200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 10 || len(events) != 10 {
		t.Fatalf("cap 0 should keep all, total=%d len=%d", total, len(events))
	}
}

func TestAuditLogMaxPrunesOldestOnAppend(t *testing.T) {
	d := openTestDB(t)
	if err := d.SetAuditLogMax(5); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if err := d.AppendAudit(1, "admin", "127.0.0.1", "login_success", fmt.Sprintf("n=%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	events, total, err := d.ListAudit(200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(events) != 5 {
		t.Fatalf("cap 5 after 8 appends: total=%d len=%d", total, len(events))
	}
	// Newest first: n=7 .. n=3. Oldest n=0,1,2 must be gone.
	if events[0].Detail != "n=7" || events[4].Detail != "n=3" {
		t.Fatalf("expected newest n=7 .. n=3, got %+v", events)
	}
	for _, e := range events {
		if e.Detail == "n=0" || e.Detail == "n=1" || e.Detail == "n=2" {
			t.Fatalf("oldest event still present: %+v", e)
		}
	}
}

func TestAuditLogMaxLoweringPrunesImmediately(t *testing.T) {
	d := openTestDB(t)
	if err := d.SetAuditLogMax(10); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := d.AppendAudit(1, "admin", "127.0.0.1", "login_success", fmt.Sprintf("n=%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.SetAuditLogMax(3); err != nil {
		t.Fatal(err)
	}
	events, total, err := d.ListAudit(200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(events) != 3 {
		t.Fatalf("lowering cap to 3 should prune immediately, total=%d len=%d", total, len(events))
	}
	if events[0].Detail != "n=9" || events[1].Detail != "n=8" || events[2].Detail != "n=7" {
		t.Fatalf("expected newest 3 kept, got %+v", events)
	}
}

func TestAuditLogMaxRaisingDoesNotDelete(t *testing.T) {
	d := openTestDB(t)
	if err := d.SetAuditLogMax(3); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := d.AppendAudit(1, "admin", "127.0.0.1", "login_success", fmt.Sprintf("n=%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.SetAuditLogMax(20); err != nil {
		t.Fatal(err)
	}
	_, total, err := d.ListAudit(200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("raising cap should keep existing rows, total=%d", total)
	}
}

func TestValidateAndSetAuditLogMaxRejectsInvalid(t *testing.T) {
	d := openTestDB(t)
	if err := d.SetAuditLogMax(5); err != nil {
		t.Fatal(err)
	}
	if err := d.AppendAudit(1, "admin", "127.0.0.1", "login_success", "keep-me"); err != nil {
		t.Fatal(err)
	}

	if err := ValidateAuditLogMax(-1); err == nil {
		t.Fatal("expected error for negative cap")
	}
	if err := d.SetAuditLogMax(-1); err == nil {
		t.Fatal("SetAuditLogMax(-1) should fail")
	}
	if err := d.SetAuditLogMax(MaxAuditLogMax + 1); err == nil {
		t.Fatal("SetAuditLogMax above max should fail")
	}
	if err := ValidateAuditLogMax(MaxAuditLogMax); err != nil {
		t.Fatalf("max value should be accepted: %v", err)
	}
	if err := ValidateAuditLogMax(0); err != nil {
		t.Fatalf("0 should be accepted: %v", err)
	}

	// Failed saves must not change the stored cap or prune existing rows.
	max, err := d.GetAuditLogMax()
	if err != nil {
		t.Fatal(err)
	}
	if max != 5 {
		t.Fatalf("invalid set should leave cap at 5, got %d", max)
	}
	_, total, err := d.ListAudit(200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("invalid set should not prune, total=%d", total)
	}
}

func TestGetAuditLogMaxClampsCorruptStoredValue(t *testing.T) {
	d := openTestDB(t)
	if err := d.SetSetting(settingAuditLogMax, "not-a-number"); err != nil {
		t.Fatal(err)
	}
	max, err := d.GetAuditLogMax()
	if err != nil {
		t.Fatal(err)
	}
	if max != 0 {
		t.Fatalf("unparseable stored cap should read as unlimited, got %d", max)
	}
	if err := d.SetSetting(settingAuditLogMax, "-9"); err != nil {
		t.Fatal(err)
	}
	max, err = d.GetAuditLogMax()
	if err != nil {
		t.Fatal(err)
	}
	if max != 0 {
		t.Fatalf("negative stored cap should read as unlimited, got %d", max)
	}
	if err := d.SetSetting(settingAuditLogMax, fmt.Sprintf("%d", MaxAuditLogMax+5)); err != nil {
		t.Fatal(err)
	}
	max, err = d.GetAuditLogMax()
	if err != nil {
		t.Fatal(err)
	}
	if max != MaxAuditLogMax {
		t.Fatalf("oversized stored cap should clamp to %d, got %d", MaxAuditLogMax, max)
	}
}
