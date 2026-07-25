package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"scrumboy/internal/store"
)

const defaultBoardPath = "/api/admin/settings/default-board"

// TestAdminDefaultBoard_GetDefaultsToUnconfigured covers the unset path:
// before any admin override, GET reports customized=false and a nil projectId.
func TestAdminDefaultBoard_GetDefaultsToUnconfigured(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "owner@example.com", "password123")

	var out map[string]any
	resp, _ := doJSON(t, client, http.MethodGet, ts.URL+defaultBoardPath, nil, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if out["customized"] != false {
		t.Fatalf("expected customized=false, got %v", out["customized"])
	}
	if out["projectId"] != nil {
		t.Fatalf("expected projectId=nil, got %v", out["projectId"])
	}
}

// TestAdminDefaultBoard_PutRequiresAdminOrOwnerAndSeedsNewUsers is the core
// end-to-end behavior: only admin/owner can set the org default, and only
// users created after the change are auto-enrolled -- never retroactively.
func TestAdminDefaultBoard_PutRequiresAdminOrOwnerAndSeedsNewUsers(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	owner := newCookieClient(t)
	bootstrapUserClient(t, owner, ts.URL, "Owner", "owner@example.com", "password123")

	var project map[string]any
	resp, _ := doJSON(t, owner, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "Onboarding"}, &project)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: expected 201, got %d", resp.StatusCode)
	}
	projectID := int64(project["id"].(float64))

	// Plain user, created before any org default override exists.
	var plainUser map[string]any
	resp, _ = doJSON(t, owner, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
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
	resp, _ = doJSON(t, plain, http.MethodPut, ts.URL+defaultBoardPath, map[string]any{
		"projectId": projectID,
	}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("plain user PUT: expected 403, got %d", resp.StatusCode)
	}

	// Owner sets the org default (owner is Maintainer via CreateProject).
	var out map[string]any
	resp, _ = doJSON(t, owner, http.MethodPut, ts.URL+defaultBoardPath, map[string]any{
		"projectId": projectID,
	}, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner PUT: expected 200, got %d", resp.StatusCode)
	}
	if out["customized"] != true {
		t.Fatalf("expected customized=true, got %v", out["customized"])
	}

	// A user created AFTER the org default was set is auto-enrolled as a viewer.
	var newUser map[string]any
	resp, _ = doJSON(t, owner, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"name":     "New",
		"email":    "new@example.com",
		"password": "password123",
	}, &newUser)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create new user: expected 201, got %d", resp.StatusCode)
	}
	newUserID := int64(newUser["id"].(float64))

	var members []map[string]any
	resp, _ = doJSON(t, owner, http.MethodGet, ts.URL+fmt.Sprintf("/api/projects/%d/members", projectID), nil, &members)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list members: expected 200, got %d", resp.StatusCode)
	}
	var newUserRole string
	var plainUserRole string
	for _, m := range members {
		uid := int64(m["userId"].(float64))
		if uid == newUserID {
			newUserRole = m["role"].(string)
		}
		if uid == int64(plainUser["id"].(float64)) {
			plainUserRole = m["role"].(string)
		}
	}
	if newUserRole != "viewer" {
		t.Fatalf("expected new user seeded as viewer, got role %q (members=%v)", newUserRole, members)
	}
	// The plain user, created BEFORE any org default existed, was never seeded a
	// membership -- untouched installs invent no rows, and the later admin
	// change never retroactively enrolls existing users.
	if plainUserRole != "" {
		t.Fatalf("expected plain user (created before override) to have no membership, got role %q", plainUserRole)
	}
}

// TestAdminDefaultBoard_AdminWithoutMembershipRejected returns 404 (no
// existence leak) when a system Admin lacks Maintainer on the target project.
func TestAdminDefaultBoard_AdminWithoutMembershipRejected(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	owner := newCookieClient(t)
	bootstrapUserClient(t, owner, ts.URL, "Owner", "owner@example.com", "password123")

	var project map[string]any
	resp, _ := doJSON(t, owner, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "Private"}, &project)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: expected 201, got %d", resp.StatusCode)
	}
	projectID := int64(project["id"].(float64))

	var adminUser map[string]any
	resp, _ = doJSON(t, owner, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"name": "Admin", "email": "admin@example.com", "password": "password123",
	}, &adminUser)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create admin user: expected 201, got %d", resp.StatusCode)
	}
	resp, _ = doJSON(t, owner, http.MethodPatch, ts.URL+fmt.Sprintf("/api/admin/users/%d/role", int64(adminUser["id"].(float64))), map[string]any{
		"role": "admin",
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("promote admin: expected 200, got %d", resp.StatusCode)
	}

	admin := newCookieClient(t)
	loginUserClient(t, admin, ts.URL, "admin@example.com", "password123")

	resp, _ = doJSON(t, admin, http.MethodPut, ts.URL+defaultBoardPath, map[string]any{
		"projectId": projectID,
	}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("admin without membership PUT: expected 404, got %d", resp.StatusCode)
	}
}

// TestAdminDefaultBoard_AdminWhoIsMaintainerSucceeds covers system Admin who
// is also Maintainer of the selected durable project.
func TestAdminDefaultBoard_AdminWhoIsMaintainerSucceeds(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	owner := newCookieClient(t)
	bootstrapUserClient(t, owner, ts.URL, "Owner", "owner@example.com", "password123")

	var project map[string]any
	resp, _ := doJSON(t, owner, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "Shared"}, &project)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: expected 201, got %d", resp.StatusCode)
	}
	projectID := int64(project["id"].(float64))

	var adminUser map[string]any
	resp, _ = doJSON(t, owner, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"name": "Admin", "email": "admin@example.com", "password": "password123",
	}, &adminUser)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create admin user: expected 201, got %d", resp.StatusCode)
	}
	adminID := int64(adminUser["id"].(float64))
	resp, _ = doJSON(t, owner, http.MethodPatch, ts.URL+fmt.Sprintf("/api/admin/users/%d/role", adminID), map[string]any{
		"role": "admin",
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("promote admin: expected 200, got %d", resp.StatusCode)
	}
	resp, _ = doJSON(t, owner, http.MethodPost, ts.URL+fmt.Sprintf("/api/projects/%d/members", projectID), map[string]any{
		"user_id": adminID,
		"role":    "maintainer",
	}, nil)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("add admin as maintainer: expected 200/201, got %d", resp.StatusCode)
	}

	admin := newCookieClient(t)
	loginUserClient(t, admin, ts.URL, "admin@example.com", "password123")

	var out map[string]any
	resp, _ = doJSON(t, admin, http.MethodPut, ts.URL+defaultBoardPath, map[string]any{
		"projectId": projectID,
	}, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin+maintainer PUT: expected 200, got %d", resp.StatusCode)
	}
	if out["customized"] != true {
		t.Fatalf("expected customized=true, got %v", out["customized"])
	}
}

func TestAdminDefaultBoard_RejectsNonexistentProject(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "owner@example.com", "password123")

	resp, _ := doJSON(t, client, http.MethodPut, ts.URL+defaultBoardPath, map[string]any{
		"projectId": 999999,
	}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestAdminDefaultBoard_RejectsTemporaryBoard(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	st := store.New(sqlDB, nil)

	owner := newCookieClient(t)
	ownerUser := bootstrapUserClient(t, owner, ts.URL, "Owner", "owner@example.com", "password123")
	ownerID := int64(ownerUser["id"].(float64))

	tempBoard, err := st.CreateAnonymousBoard(store.WithUserID(context.Background(), ownerID))
	if err != nil {
		t.Fatalf("CreateAnonymousBoard(temp): %v", err)
	}
	if err := st.EnsureMaintainerMembership(context.Background(), tempBoard.ID, ownerID); err != nil {
		t.Fatalf("EnsureMaintainerMembership: %v", err)
	}

	resp, _ := doJSON(t, owner, http.MethodPut, ts.URL+defaultBoardPath, map[string]any{
		"projectId": tempBoard.ID,
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("temporary board PUT: expected 400, got %d", resp.StatusCode)
	}
}

func TestAdminDefaultBoard_RejectsAnonymousBoard(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	st := store.New(sqlDB, nil)

	owner := newCookieClient(t)
	ownerUser := bootstrapUserClient(t, owner, ts.URL, "Owner", "owner@example.com", "password123")
	ownerID := int64(ownerUser["id"].(float64))

	anonBoard, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard(anon): %v", err)
	}
	if err := st.EnsureMaintainerMembership(context.Background(), anonBoard.ID, ownerID); err != nil {
		t.Fatalf("EnsureMaintainerMembership: %v", err)
	}

	resp, _ := doJSON(t, owner, http.MethodPut, ts.URL+defaultBoardPath, map[string]any{
		"projectId": anonBoard.ID,
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("anonymous board PUT: expected 400, got %d", resp.StatusCode)
	}
}

func TestAdminDefaultBoard_UnauthenticatedRejected(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := &http.Client{}
	resp, _ := doJSON(t, client, http.MethodGet, ts.URL+defaultBoardPath, nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// TestAdminDefaultBoard_DeleteResetsToUnset verifies that after setting and
// then deleting the override, GET reports customized=false, a user created
// after the reset is not enrolled, and a user enrolled before the reset keeps
// their membership.
func TestAdminDefaultBoard_DeleteResetsToUnset(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	owner := newCookieClient(t)
	bootstrapUserClient(t, owner, ts.URL, "Owner", "owner@example.com", "password123")

	var project map[string]any
	resp, _ := doJSON(t, owner, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "Onboarding"}, &project)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: expected 201, got %d", resp.StatusCode)
	}
	projectID := int64(project["id"].(float64))

	resp, _ = doJSON(t, owner, http.MethodPut, ts.URL+defaultBoardPath, map[string]any{"projectId": projectID}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT override: expected 200, got %d", resp.StatusCode)
	}

	var beforeUser map[string]any
	resp, _ = doJSON(t, owner, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"name": "Before", "email": "before@example.com", "password": "password123",
	}, &beforeUser)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create before user: expected 201, got %d", resp.StatusCode)
	}

	// Reset.
	resp, _ = doJSON(t, owner, http.MethodDelete, ts.URL+defaultBoardPath, nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE: expected 204, got %d", resp.StatusCode)
	}

	var out map[string]any
	resp, _ = doJSON(t, owner, http.MethodGet, ts.URL+defaultBoardPath, nil, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET after reset: expected 200, got %d", resp.StatusCode)
	}
	if out["customized"] != false {
		t.Fatalf("expected customized=false after reset, got %v", out["customized"])
	}

	var afterUser map[string]any
	resp, _ = doJSON(t, owner, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"name": "After", "email": "after@example.com", "password": "password123",
	}, &afterUser)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create after user: expected 201, got %d", resp.StatusCode)
	}

	var members []map[string]any
	resp, _ = doJSON(t, owner, http.MethodGet, ts.URL+fmt.Sprintf("/api/projects/%d/members", projectID), nil, &members)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list members: expected 200, got %d", resp.StatusCode)
	}
	beforeID := int64(beforeUser["id"].(float64))
	afterID := int64(afterUser["id"].(float64))
	var beforeRole, afterRole string
	for _, m := range members {
		uid := int64(m["userId"].(float64))
		if uid == beforeID {
			beforeRole = m["role"].(string)
		}
		if uid == afterID {
			afterRole = m["role"].(string)
		}
	}
	if beforeRole != "viewer" {
		t.Fatalf("expected before-reset user to keep their viewer membership, got %q", beforeRole)
	}
	if afterRole != "" {
		t.Fatalf("expected after-reset user to have no membership, got %q", afterRole)
	}
}
