package mcp

import (
	"testing"

	"scrumboy/internal/store"
)

func TestParseAdminUpdatableSystemRole_admin(t *testing.T) {
	t.Parallel()
	role, aerr := parseAdminUpdatableSystemRole("admin")
	if aerr != nil {
		t.Fatalf("parseAdminUpdatableSystemRole: %v", aerr)
	}
	if role != store.SystemRoleAdmin {
		t.Fatalf("expected SystemRoleAdmin, got %q", role)
	}
}

func TestParseAdminUpdatableSystemRole_user(t *testing.T) {
	t.Parallel()
	role, aerr := parseAdminUpdatableSystemRole("user")
	if aerr != nil {
		t.Fatalf("parseAdminUpdatableSystemRole: %v", aerr)
	}
	if role != store.SystemRoleUser {
		t.Fatalf("expected SystemRoleUser, got %q", role)
	}
}

func TestParseAdminUpdatableSystemRole_ownerRejected(t *testing.T) {
	t.Parallel()
	// Promotion to owner is deliberately not exposed through this tool,
	// matching internal/httpapi/routing_admin.go's handleAdminUsersUpdateRole.
	_, aerr := parseAdminUpdatableSystemRole("owner")
	if aerr == nil {
		t.Fatal("expected adapter error for role=owner")
	}
	if aerr.Code != CodeValidationError {
		t.Fatalf("expected validation error code, got %q", aerr.Code)
	}
}

func TestParseAdminUpdatableSystemRole_unknownRejected(t *testing.T) {
	t.Parallel()
	_, aerr := parseAdminUpdatableSystemRole("superadmin")
	if aerr == nil {
		t.Fatal("expected adapter error for unknown role")
	}
	if aerr.Code != CodeValidationError {
		t.Fatalf("expected validation error code, got %q", aerr.Code)
	}
}

func TestRequesterHasAnySystemRole(t *testing.T) {
	t.Parallel()
	owner := store.User{SystemRole: store.SystemRoleOwner}
	admin := store.User{SystemRole: store.SystemRoleAdmin}
	user := store.User{SystemRole: store.SystemRoleUser}

	if !requesterHasAnySystemRole(owner, store.SystemRoleOwner, store.SystemRoleAdmin) {
		t.Fatal("expected owner to satisfy admin-or-owner gate")
	}
	if !requesterHasAnySystemRole(admin, store.SystemRoleOwner, store.SystemRoleAdmin) {
		t.Fatal("expected admin to satisfy admin-or-owner gate")
	}
	if requesterHasAnySystemRole(user, store.SystemRoleOwner, store.SystemRoleAdmin) {
		t.Fatal("expected regular user to fail admin-or-owner gate")
	}
}
