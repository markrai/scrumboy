package store

import (
	"context"
	"errors"
	"testing"
)

func TestSystemRoles_RequireOwner(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create owner user
	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	if owner.SystemRole != SystemRoleOwner {
		t.Fatalf("expected owner role, got %v", owner.SystemRole)
	}

	// Create regular user
	user, err := st.CreateUser(ctx, "user@test.com", "password123", "User")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.SystemRole != SystemRoleUser {
		t.Fatalf("expected user role, got %v", user.SystemRole)
	}

	// Owner should pass
	if err := st.requireOwner(ctx, owner.ID); err != nil {
		t.Fatalf("requireOwner(owner) should pass: %v", err)
	}

	// Regular user should fail
	if err := st.requireOwner(ctx, user.ID); err != ErrUnauthorized {
		t.Fatalf("requireOwner(user) should return ErrUnauthorized, got: %v", err)
	}
}

func TestSystemRoles_RequireAdmin(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create owner
	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}

	// Create admin
	admin, err := st.CreateUser(ctx, "admin@test.com", "password123", "Admin")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.UpdateUserRole(ctx, owner.ID, admin.ID, SystemRoleAdmin); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}

	// Create regular user
	user, err := st.CreateUser(ctx, "user@test.com", "password123", "User")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Owner should pass
	if err := st.requireAdmin(ctx, owner.ID); err != nil {
		t.Fatalf("requireAdmin(owner) should pass: %v", err)
	}

	// Admin should pass
	if err := st.requireAdmin(ctx, admin.ID); err != nil {
		t.Fatalf("requireAdmin(admin) should pass: %v", err)
	}

	// Regular user should fail
	if err := st.requireAdmin(ctx, user.ID); err != ErrUnauthorized {
		t.Fatalf("requireAdmin(user) should return ErrUnauthorized, got: %v", err)
	}
}

func TestSystemRoles_ListUsers_RequiresAdmin(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create owner
	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}

	// Create admin
	admin, err := st.CreateUser(ctx, "admin@test.com", "password123", "Admin")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.UpdateUserRole(ctx, owner.ID, admin.ID, SystemRoleAdmin); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}

	// Create regular user
	user, err := st.CreateUser(ctx, "user@test.com", "password123", "User")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Owner can list users
	users, err := st.ListUsers(ctx, owner.ID)
	if err != nil {
		t.Fatalf("ListUsers(owner) should pass: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("expected 3 users, got %d", len(users))
	}

	// Admin can list users
	users, err = st.ListUsers(ctx, admin.ID)
	if err != nil {
		t.Fatalf("ListUsers(admin) should pass: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("expected 3 users, got %d", len(users))
	}

	// Regular user cannot list users
	_, err = st.ListUsers(ctx, user.ID)
	if err != ErrUnauthorized {
		t.Fatalf("ListUsers(user) should return ErrUnauthorized, got: %v", err)
	}
}

func TestSystemRoles_DeleteUser_OwnerOnly(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create owner
	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}

	// Create admin
	admin, err := st.CreateUser(ctx, "admin@test.com", "password123", "Admin")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.UpdateUserRole(ctx, owner.ID, admin.ID, SystemRoleAdmin); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}

	// Create regular user
	user, err := st.CreateUser(ctx, "user@test.com", "password123", "User")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Owner can delete user
	if err := st.DeleteUser(ctx, owner.ID, user.ID); err != nil {
		t.Fatalf("DeleteUser(owner, user) should pass: %v", err)
	}

	// Verify user is deleted
	_, err = st.GetUser(ctx, user.ID)
	if err != ErrNotFound {
		t.Fatalf("user should be deleted, got: %v", err)
	}

	// Admin cannot delete users
	user2, err := st.CreateUser(ctx, "user2@test.com", "password123", "User2")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	err = st.DeleteUser(ctx, admin.ID, user2.ID)
	if err != ErrUnauthorized {
		t.Fatalf("DeleteUser(admin, user2) should return ErrUnauthorized, got: %v", err)
	}
}

func TestSystemRoles_DeleteUser_PreventsLastOwnerDeletion(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create owner
	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}

	// Create second owner
	owner2, err := st.CreateUser(ctx, "owner2@test.com", "password123", "Owner2")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.UpdateUserRole(ctx, owner.ID, owner2.ID, SystemRoleOwner); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}

	// Can delete one owner when there are two
	if err := st.DeleteUser(ctx, owner.ID, owner2.ID); err != nil {
		t.Fatalf("DeleteUser(owner, owner2) should pass when 2 owners exist: %v", err)
	}

	// Cannot delete the last owner
	err = st.DeleteUser(ctx, owner.ID, owner.ID)
	if err == nil {
		t.Fatalf("DeleteUser(owner, owner) should fail when last owner")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got: %v", err)
	}

	// Verify owner still exists
	_, err = st.GetUser(ctx, owner.ID)
	if err != nil {
		t.Fatalf("owner should still exist: %v", err)
	}
}

func TestSystemRoles_UpdateUserRole_OwnerOnly(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create owner
	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}

	// Create admin
	admin, err := st.CreateUser(ctx, "admin@test.com", "password123", "Admin")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.UpdateUserRole(ctx, owner.ID, admin.ID, SystemRoleAdmin); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}

	// Create regular user
	user, err := st.CreateUser(ctx, "user@test.com", "password123", "User")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Owner can promote user to admin
	if err := st.UpdateUserRole(ctx, owner.ID, user.ID, SystemRoleAdmin); err != nil {
		t.Fatalf("UpdateUserRole(owner, user, admin) should pass: %v", err)
	}

	// Verify role was updated
	updated, err := st.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if updated.SystemRole != SystemRoleAdmin {
		t.Fatalf("expected admin role, got %v", updated.SystemRole)
	}

	// Admin cannot promote users
	user2, err := st.CreateUser(ctx, "user2@test.com", "password123", "User2")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	err = st.UpdateUserRole(ctx, admin.ID, user2.ID, SystemRoleAdmin)
	if err != ErrUnauthorized {
		t.Fatalf("UpdateUserRole(admin, user2, admin) should return ErrUnauthorized, got: %v", err)
	}
}

func TestSystemRoles_UpdateUserRole_PreventsLastOwnerDemotion(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create owner
	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}

	// Cannot demote the last owner
	err = st.UpdateUserRole(ctx, owner.ID, owner.ID, SystemRoleAdmin)
	if err == nil {
		t.Fatalf("UpdateUserRole(owner, owner, admin) should fail when last owner")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got: %v", err)
	}

	// Verify owner still has owner role
	u, err := st.GetUser(ctx, owner.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if u.SystemRole != SystemRoleOwner {
		t.Fatalf("owner should still have owner role, got %v", u.SystemRole)
	}

	// Create second owner
	owner2, err := st.CreateUser(ctx, "owner2@test.com", "password123", "Owner2")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.UpdateUserRole(ctx, owner.ID, owner2.ID, SystemRoleOwner); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}

	// Can demote one owner when there are two
	if err := st.UpdateUserRole(ctx, owner.ID, owner2.ID, SystemRoleAdmin); err != nil {
		t.Fatalf("UpdateUserRole(owner, owner2, admin) should pass when 2 owners exist: %v", err)
	}
}

func TestSystemRoles_CannotDeleteSelf(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create owner
	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}

	// Cannot delete self
	err = st.DeleteUser(ctx, owner.ID, owner.ID)
	if err == nil {
		t.Fatalf("DeleteUser(owner, owner) should fail")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got: %v", err)
	}
}

// TestSystemRoles_Invariant_AlwaysAtLeastOneOwner ensures that after any user mutation,
// the system always has at least one owner. This is a critical safety invariant.
func TestSystemRoles_Invariant_AlwaysAtLeastOneOwner(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Helper to verify owner count
	verifyOwnerCount := func(minOwners int, msg string) {
		count, err := st.countOwners(ctx)
		if err != nil {
			t.Fatalf("%s: countOwners failed: %v", msg, err)
		}
		if count < minOwners {
			t.Fatalf("%s: expected at least %d owners, got %d", msg, minOwners, count)
		}
	}

	// Create initial owner
	owner1, err := st.BootstrapUser(ctx, "owner1@test.com", "password123", "Owner1")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	verifyOwnerCount(1, "after bootstrap")

	// Create second owner
	owner2, err := st.CreateUser(ctx, "owner2@test.com", "password123", "Owner2")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.UpdateUserRole(ctx, owner1.ID, owner2.ID, SystemRoleOwner); err != nil {
		t.Fatalf("UpdateUserRole to owner: %v", err)
	}
	verifyOwnerCount(2, "after creating second owner")

	// Create admin
	admin, err := st.CreateUser(ctx, "admin@test.com", "password123", "Admin")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.UpdateUserRole(ctx, owner1.ID, admin.ID, SystemRoleAdmin); err != nil {
		t.Fatalf("UpdateUserRole to admin: %v", err)
	}
	verifyOwnerCount(2, "after creating admin")

	// Create regular user
	user, err := st.CreateUser(ctx, "user@test.com", "password123", "User")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	verifyOwnerCount(2, "after creating user")

	// Can delete regular user (should not affect owner count)
	if err := st.DeleteUser(ctx, owner1.ID, user.ID); err != nil {
		t.Fatalf("DeleteUser(regular user) should succeed: %v", err)
	}
	verifyOwnerCount(2, "after deleting regular user")

	// Can delete admin (should not affect owner count)
	if err := st.DeleteUser(ctx, owner1.ID, admin.ID); err != nil {
		t.Fatalf("DeleteUser(admin) should succeed: %v", err)
	}
	verifyOwnerCount(2, "after deleting admin")

	// Can delete one owner when multiple exist
	if err := st.DeleteUser(ctx, owner1.ID, owner2.ID); err != nil {
		t.Fatalf("DeleteUser(owner2) should succeed when 2 owners exist: %v", err)
	}
	verifyOwnerCount(1, "after deleting one owner (2 existed)")

	// Cannot delete the last owner
	err = st.DeleteUser(ctx, owner1.ID, owner1.ID)
	if err == nil {
		t.Fatalf("DeleteUser(last owner) should fail")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got: %v", err)
	}
	verifyOwnerCount(1, "after attempting to delete last owner")

	// Cannot demote the last owner
	err = st.UpdateUserRole(ctx, owner1.ID, owner1.ID, SystemRoleAdmin)
	if err == nil {
		t.Fatalf("UpdateUserRole(last owner to admin) should fail")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got: %v", err)
	}
	verifyOwnerCount(1, "after attempting to demote last owner")

	// Create another owner and verify we can then delete the first
	owner3, err := st.CreateUser(ctx, "owner3@test.com", "password123", "Owner3")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.UpdateUserRole(ctx, owner1.ID, owner3.ID, SystemRoleOwner); err != nil {
		t.Fatalf("UpdateUserRole to owner: %v", err)
	}
	verifyOwnerCount(2, "after creating third owner")

	// Now can delete the original owner
	if err := st.DeleteUser(ctx, owner3.ID, owner1.ID); err != nil {
		t.Fatalf("DeleteUser(owner1) should succeed when 2 owners exist: %v", err)
	}
	verifyOwnerCount(1, "after deleting original owner (2 existed)")

	// Final verification: system still has exactly one owner
	count, err := st.countOwners(ctx)
	if err != nil {
		t.Fatalf("final countOwners failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 owner at end, got %d", count)
	}
}
