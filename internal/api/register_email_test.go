package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/eric/nas-filehub/internal/db"
)

func TestRegisterEmailMustBeUnique(t *testing.T) {
	_, ts, database := testServer(t)
	if err := database.SetOpenRegistration(true); err != nil {
		t.Fatal(err)
	}

	// Approval required (default): first signup occupies the email, even while pending.
	st, data := doJSON(t, http.MethodPost, ts.URL+"/api/register", nil, map[string]any{
		"username": "firstuser", "password": "First-pass-1", "email": "Same@Example.COM",
	})
	if st != 201 || data["pending"] != true {
		t.Fatalf("first register %d %v", st, data)
	}
	msg, _ := data["message"].(string)
	if !strings.Contains(strings.ToLower(msg), "waiting for admin approval") {
		t.Fatalf("pending message missing: %v", data["message"])
	}

	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/register", nil, map[string]any{
		"username": "seconduser", "password": "Second-pass-1", "email": "same@example.com",
	})
	if st != 409 {
		t.Fatalf("duplicate email while pending: %d %v", st, data)
	}
	if errStr, _ := data["error"].(string); !strings.Contains(strings.ToLower(errStr), "email already in use") {
		t.Fatalf("expected email already in use, got %v", data)
	}

	// After reject, the email can be reused.
	u, err := database.GetUserByUsername("firstuser")
	if err != nil || u == nil {
		t.Fatalf("lookup firstuser: %v %v", u, err)
	}
	admin := adminCookie(t, ts.URL)
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/admin/users/"+itoa(u.ID)+"/reject", admin, map[string]any{})
	if st != 200 {
		t.Fatalf("reject %d %v", st, data)
	}

	// Approval off: unique email still enforced, and the user can sign in immediately.
	if err := database.SetRegistrationRequireApproval(false); err != nil {
		t.Fatal(err)
	}
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/register", nil, map[string]any{
		"username": "thirduser", "password": "Third-pass-1", "email": "same@example.com",
	})
	if st != 201 || data["pending"] == true {
		t.Fatalf("reuse after reject %d %v", st, data)
	}
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/register", nil, map[string]any{
		"username": "fourthuser", "password": "Fourth-pass-1", "email": "same@example.com",
	})
	if st != 409 {
		t.Fatalf("duplicate email without approval: %d %v", st, data)
	}

	// Two admin-created users may both have an empty email.
	if _, err := database.CreateUser("emptyone", "Empty-pass-1", "", false, 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateUser("emptytwo", "Empty-pass-2", "", false, 0, ""); err != nil {
		t.Fatal(err)
	}

	// Admin create / edit must not reuse a taken email.
	st, data = doJSON(t, http.MethodPost, ts.URL+"/api/admin/users", admin, map[string]any{
		"username": "adminmade", "password": "Admin-pass-1", "email": "same@example.com",
	})
	if st != 409 {
		t.Fatalf("admin create taken email %d %v", st, data)
	}
	other, err := database.CreateUser("otheruser", "Other-pass-1", "", false, 0, "other@example.com")
	if err != nil {
		t.Fatal(err)
	}
	email := "same@example.com"
	st, data = doJSON(t, http.MethodPatch, ts.URL+"/api/admin/users/"+itoa(other.ID), admin, map[string]any{
		"email": email,
	})
	if st != 409 {
		t.Fatalf("admin edit taken email %d %v", st, data)
	}
	// Same user may keep their own email.
	keep := "other@example.com"
	st, data = doJSON(t, http.MethodPatch, ts.URL+"/api/admin/users/"+itoa(other.ID), admin, map[string]any{
		"email": keep,
	})
	if st != 200 {
		t.Fatalf("admin edit same email %d %v", st, data)
	}
	got, _ := database.GetUserByID(other.ID)
	if got == nil || got.Email != "other@example.com" {
		t.Fatalf("email changed unexpectedly: %+v", got)
	}
}

func TestEmailInUseIncludesDisabled(t *testing.T) {
	_, ts, database := testServer(t)
	if err := database.SetOpenRegistration(true); err != nil {
		t.Fatal(err)
	}
	if err := database.SetRegistrationRequireApproval(false); err != nil {
		t.Fatal(err)
	}
	u, err := database.CreateUser("disableduser", "Disabled-pass-1", "", false, 0, "gone@example.com")
	if err != nil {
		t.Fatal(err)
	}
	dis := true
	if _, err := database.UpdateUser(u.ID, db.UserUpdate{Disabled: &dis}); err != nil {
		t.Fatal(err)
	}
	st, data := doJSON(t, http.MethodPost, ts.URL+"/api/register", nil, map[string]any{
		"username": "newclaim", "password": "Newclaim-pass-1", "email": "gone@example.com",
	})
	if st != 409 {
		t.Fatalf("disabled email still in use: %d %v", st, data)
	}
}
