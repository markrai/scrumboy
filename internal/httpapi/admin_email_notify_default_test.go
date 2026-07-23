package httpapi

import (
	"net/http"
	"testing"
)

// TestAdminEmailNotifyDefault_GetDefaultsToHardcodedFallback covers #169 Phase 1:
// before any admin override, GET reports the hardcoded DefaultEmailNotifyPref
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
// end-to-end behavior for #169 Phase 1: only admin/owner can set the org default,
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

	// The plain user, created BEFORE the org default change, was seeded from
	// whatever the org default was AT THAT TIME (the hardcoded fallback, since no
	// override existed yet) and is unaffected by the later admin change.
	var plainPref map[string]any
	resp, _ = doJSON(t, plain, http.MethodGet, ts.URL+"/api/user/preferences?key=emailNotifications", nil, &plainPref)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get plain user preferences: expected 200, got %d", resp.StatusCode)
	}
	if plainPref["value"] != `{"v":1,"enabled":false,"assigned":true,"cardActivity":false,"sprintActivity":false,"projectActivity":false,"addedToProject":true}` {
		t.Fatalf("plain user's preference should be the pre-change hardcoded default, got %v", plainPref["value"])
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
