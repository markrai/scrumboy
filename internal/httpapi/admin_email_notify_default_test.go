package httpapi

import (
	"fmt"
	"net/http"
	"testing"
)

// TestAdminEmailNotifyDefault_GetDefaultsToHardcodedFallback covers the unset
// path: before any admin override, GET reports the hardcoded DefaultEmailNotifyPref
// with customized=false.
func TestAdminEmailNotifyDefault_GetDefaultsToHardcodedFallback(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "owner@example.com", "password123")

	var out map[string]any
	resp, _ := doJSON(t, client, http.MethodGet, ts.URL+"/api/admin/settings/email-notify-default", nil, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if out["customized"] != false {
		t.Fatalf("expected customized=false, got %v", out["customized"])
	}
	if out["value"] != `{"v":1,"enabled":false,"assigned":true,"cardActivity":false,"sprintActivity":false,"projectActivity":false,"addedToProject":true}` {
		t.Fatalf("unexpected default value: %v", out["value"])
	}
}

// TestAdminEmailNotifyDefault_PutRequiresAdminOrOwnerAndSeedsNewUsers is the core
// end-to-end behavior: only admin/owner can set the org default,
// and it seeds only users created after the change -- never retroactively.
func TestAdminEmailNotifyDefault_PutRequiresAdminOrOwnerAndSeedsNewUsers(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	owner := newCookieClient(t)
	bootstrapUserClient(t, owner, ts.URL, "Owner", "owner@example.com", "password123")

	// Plain user, created before any org default override exists.
	var plainUser map[string]any
	resp, _ := doJSON(t, owner, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"name":     "Plain",
		"email":    "plain@example.com",
		"password": "password123",
	}, &plainUser)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create plain user: expected 201, got %d", resp.StatusCode)
	}

	plain := newCookieClient(t)
	loginUserClient(t, plain, ts.URL, "plain@example.com", "password123")

	// Plain (non-admin) user cannot set the org default.
	resp, _ = doJSON(t, plain, http.MethodPut, ts.URL+"/api/admin/settings/email-notify-default", map[string]any{
		"value": `{"enabled":true}`,
	}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("plain user PUT: expected 403, got %d", resp.StatusCode)
	}

	// Owner sets the org default.
	var out map[string]any
	resp, _ = doJSON(t, owner, http.MethodPut, ts.URL+"/api/admin/settings/email-notify-default", map[string]any{
		"value": `{"enabled":true,"projectActivity":true}`,
	}, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner PUT: expected 200, got %d", resp.StatusCode)
	}
	if out["customized"] != true {
		t.Fatalf("expected customized=true, got %v", out["customized"])
	}

	// A user created AFTER the org default was set inherits it as their initial preference.
	var newUser map[string]any
	resp, _ = doJSON(t, owner, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"name":     "New",
		"email":    "new@example.com",
		"password": "password123",
	}, &newUser)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create new user: expected 201, got %d", resp.StatusCode)
	}

	newClient := newCookieClient(t)
	loginUserClient(t, newClient, ts.URL, "new@example.com", "password123")

	var pref map[string]any
	resp, _ = doJSON(t, newClient, http.MethodGet, ts.URL+"/api/user/preferences?key=emailNotifications", nil, &pref)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get new user preferences: expected 200, got %d", resp.StatusCode)
	}
	if pref["value"] != `{"v":1,"enabled":true,"assigned":true,"cardActivity":false,"sprintActivity":false,"projectActivity":true,"addedToProject":true}` {
		t.Fatalf("new user should have inherited org default, got %v", pref["value"])
	}

	// The plain user, created BEFORE any org default existed, was never seeded a
	// row (compatibility guarantee: untouched installs invent no rows), so their
	// stored value is empty and resolves to the hardcoded default lazily. The
	// later admin change does not retroactively affect them.
	var plainPref map[string]any
	resp, _ = doJSON(t, plain, http.MethodGet, ts.URL+"/api/user/preferences?key=emailNotifications", nil, &plainPref)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get plain user preferences: expected 200, got %d", resp.StatusCode)
	}
	if plainPref["value"] != "" {
		t.Fatalf("plain user created before any override should have no stored preference, got %v", plainPref["value"])
	}
}

func TestAdminEmailNotifyDefault_RejectsInvalidJSON(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "owner@example.com", "password123")

	resp, _ := doJSON(t, client, http.MethodPut, ts.URL+"/api/admin/settings/email-notify-default", map[string]any{
		"value": `{"unknown":true}`,
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAdminEmailNotifyDefault_UnauthenticatedRejected(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := &http.Client{}
	resp, _ := doJSON(t, client, http.MethodGet, ts.URL+"/api/admin/settings/email-notify-default", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

const emailNotifyDefaultPath = "/api/admin/settings/email-notify-default"

// TestAdminEmailNotifyDefault_DeleteAuthorization covers the reset endpoint's
// authorization matrix and idempotency.
func TestAdminEmailNotifyDefault_DeleteAuthorization(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	owner := newCookieClient(t)
	bootstrapUserClient(t, owner, ts.URL, "Owner", "owner@example.com", "password123")

	// Create a plain user and an admin (promoted from a plain user).
	var plainUser map[string]any
	resp, _ := doJSON(t, owner, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"name": "Plain", "email": "plain@example.com", "password": "password123",
	}, &plainUser)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create plain user: expected 201, got %d", resp.StatusCode)
	}

	var adminUser map[string]any
	resp, _ = doJSON(t, owner, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"name": "Admin", "email": "admin@example.com", "password": "password123",
	}, &adminUser)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create admin user: expected 201, got %d", resp.StatusCode)
	}
	resp, _ = doJSON(t, owner, http.MethodPatch, ts.URL+fmt.Sprintf("/api/admin/users/%d/role", int64(adminUser["id"].(float64))), map[string]any{"role": "admin"}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("promote admin: expected 200, got %d", resp.StatusCode)
	}

	// Unauthenticated DELETE -> 401.
	anon := &http.Client{}
	resp, _ = doJSON(t, anon, http.MethodDelete, ts.URL+emailNotifyDefaultPath, nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anon DELETE: expected 401, got %d", resp.StatusCode)
	}

	// Ordinary user DELETE -> 403.
	plain := newCookieClient(t)
	loginUserClient(t, plain, ts.URL, "plain@example.com", "password123")
	resp, _ = doJSON(t, plain, http.MethodDelete, ts.URL+emailNotifyDefaultPath, nil, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("plain DELETE: expected 403, got %d", resp.StatusCode)
	}

	// Admin DELETE -> 204 (bodyless).
	admin := newCookieClient(t)
	loginUserClient(t, admin, ts.URL, "admin@example.com", "password123")
	resp, body := doJSON(t, admin, http.MethodDelete, ts.URL+emailNotifyDefaultPath, nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("admin DELETE: expected 204, got %d", resp.StatusCode)
	}
	if len(body) != 0 {
		t.Fatalf("expected empty body on 204, got %q", string(body))
	}

	// Owner DELETE -> 204, and repeated DELETE still 204 (idempotent).
	resp, _ = doJSON(t, owner, http.MethodDelete, ts.URL+emailNotifyDefaultPath, nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("owner DELETE: expected 204, got %d", resp.StatusCode)
	}
	resp, _ = doJSON(t, owner, http.MethodDelete, ts.URL+emailNotifyDefaultPath, nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("repeated owner DELETE: expected 204, got %d", resp.StatusCode)
	}
}

// TestAdminEmailNotifyDefault_DeleteResetsToUnset verifies that after setting and
// then deleting the override, GET reports customized=false and a user created
// afterward is not seeded, while a user created before the reset is unchanged.
func TestAdminEmailNotifyDefault_DeleteResetsToUnset(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	owner := newCookieClient(t)
	bootstrapUserClient(t, owner, ts.URL, "Owner", "owner@example.com", "password123")

	// Set an override, then create a user who inherits it.
	resp, _ := doJSON(t, owner, http.MethodPut, ts.URL+emailNotifyDefaultPath, map[string]any{
		"value": `{"enabled":true,"projectActivity":true}`,
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT override: expected 200, got %d", resp.StatusCode)
	}
	resp, _ = doJSON(t, owner, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"name": "Before", "email": "before@example.com", "password": "password123",
	}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create before user: expected 201, got %d", resp.StatusCode)
	}

	// Reset.
	resp, _ = doJSON(t, owner, http.MethodDelete, ts.URL+emailNotifyDefaultPath, nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE: expected 204, got %d", resp.StatusCode)
	}

	// GET now reports customized=false.
	var out map[string]any
	resp, _ = doJSON(t, owner, http.MethodGet, ts.URL+emailNotifyDefaultPath, nil, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET after reset: expected 200, got %d", resp.StatusCode)
	}
	if out["customized"] != false {
		t.Fatalf("expected customized=false after reset, got %v", out["customized"])
	}

	// The user created before reset keeps their inherited preference.
	before := newCookieClient(t)
	loginUserClient(t, before, ts.URL, "before@example.com", "password123")
	var beforePref map[string]any
	resp, _ = doJSON(t, before, http.MethodGet, ts.URL+"/api/user/preferences?key=emailNotifications", nil, &beforePref)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("before user prefs: expected 200, got %d", resp.StatusCode)
	}
	if beforePref["value"] != `{"v":1,"enabled":true,"assigned":true,"cardActivity":false,"sprintActivity":false,"projectActivity":true,"addedToProject":true}` {
		t.Fatalf("before user's inherited preference should be unchanged, got %v", beforePref["value"])
	}

	// A user created after reset has no stored value (rowless lazy default).
	resp, _ = doJSON(t, owner, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"name": "After", "email": "after@example.com", "password": "password123",
	}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create after user: expected 201, got %d", resp.StatusCode)
	}
	after := newCookieClient(t)
	loginUserClient(t, after, ts.URL, "after@example.com", "password123")
	var afterPref map[string]any
	resp, _ = doJSON(t, after, http.MethodGet, ts.URL+"/api/user/preferences?key=emailNotifications", nil, &afterPref)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("after user prefs: expected 200, got %d", resp.StatusCode)
	}
	if afterPref["value"] != "" {
		t.Fatalf("expected empty stored value for user created after reset, got %v", afterPref["value"])
	}
}
