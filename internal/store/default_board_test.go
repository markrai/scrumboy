package store

import (
	"context"
	"errors"
	"testing"
)

func TestGetDefaultBoardOrgSetting_UnsetIsNotCustomized(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	projectID, customized, err := st.GetDefaultBoardOrgSetting(ctx)
	if err != nil {
		t.Fatalf("GetDefaultBoardOrgSetting: %v", err)
	}
	if customized {
		t.Fatalf("expected customized=false when no admin override set")
	}
	if projectID != 0 {
		t.Fatalf("expected projectID=0 when unset, got %d", projectID)
	}
}

func TestSetDefaultBoardOrgSetting_RequiresAdminOrOwner(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	user, err := st.CreateUser(ctx, "user@test.com", "password123", "User")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	project, err := st.CreateProject(WithUserID(ctx, owner.ID), "Onboarding")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := st.SetDefaultBoardOrgSetting(ctx, user.ID, project.ID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for plain user, got %v", err)
	}

	if err := st.SetDefaultBoardOrgSetting(ctx, owner.ID, project.ID); err != nil {
		t.Fatalf("SetDefaultBoardOrgSetting(owner): %v", err)
	}

	got, customized, err := st.GetDefaultBoardOrgSetting(ctx)
	if err != nil {
		t.Fatalf("GetDefaultBoardOrgSetting: %v", err)
	}
	if !customized {
		t.Fatalf("expected customized=true after admin override")
	}
	if got != project.ID {
		t.Fatalf("expected projectID=%d, got %d", project.ID, got)
	}
}

func TestSetDefaultBoardOrgSetting_RejectsMissingProject(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}

	if err := st.SetDefaultBoardOrgSetting(ctx, owner.ID, 999999); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for nonexistent project, got %v", err)
	}
	if err := st.SetDefaultBoardOrgSetting(ctx, owner.ID, 0); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for projectID=0, got %v", err)
	}
	if err := st.SetDefaultBoardOrgSetting(ctx, owner.ID, -1); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for negative projectID, got %v", err)
	}
}

// TestCreateUser_SeedsDefaultBoardMembership is the core Phase 1 behavior: a
// user created after an admin configures a default board is auto-enrolled as
// a viewer on that board.
func TestCreateUser_SeedsDefaultBoardMembership(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	project, err := st.CreateProject(WithUserID(ctx, owner.ID), "Onboarding")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := st.SetDefaultBoardOrgSetting(ctx, owner.ID, project.ID); err != nil {
		t.Fatalf("SetDefaultBoardOrgSetting: %v", err)
	}

	newUser, err := st.CreateUser(ctx, "new@test.com", "password123", "New User")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	role, err := st.GetProjectRole(ctx, project.ID, newUser.ID)
	if err != nil {
		t.Fatalf("GetProjectRole: %v", err)
	}
	if role != RoleViewer {
		t.Fatalf("expected new user seeded as RoleViewer, got %q", role)
	}
}

// TestCreateUser_NoDefaultBoardConfiguredSeedsNothing documents that an
// untouched instance (no admin override) behaves identically to before this
// feature existed: no project_members row at all.
func TestCreateUser_NoDefaultBoardConfiguredSeedsNothing(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	project, err := st.CreateProject(WithUserID(ctx, owner.ID), "Onboarding")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	newUser, err := st.CreateUser(ctx, "new@test.com", "password123", "New User")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	role, err := st.GetProjectRole(ctx, project.ID, newUser.ID)
	if err != nil {
		t.Fatalf("GetProjectRole: %v", err)
	}
	if role != "" {
		t.Fatalf("expected no membership seeded when no default board is configured, got %q", role)
	}
}

// TestCreateUser_ExistingUsersUnaffectedByLaterDefaultBoardChange proves the
// default board is only ever a seed at creation time, never applied
// retroactively -- setting or changing it never touches existing users.
func TestCreateUser_ExistingUsersUnaffectedByLaterDefaultBoardChange(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	project, err := st.CreateProject(WithUserID(ctx, owner.ID), "Onboarding")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	earlyUser, err := st.CreateUser(ctx, "early@test.com", "password123", "Early User")
	if err != nil {
		t.Fatalf("CreateUser(early): %v", err)
	}

	if err := st.SetDefaultBoardOrgSetting(ctx, owner.ID, project.ID); err != nil {
		t.Fatalf("SetDefaultBoardOrgSetting: %v", err)
	}

	// Existing early user is not retroactively enrolled by the later change.
	role, err := st.GetProjectRole(ctx, project.ID, earlyUser.ID)
	if err != nil {
		t.Fatalf("GetProjectRole(early): %v", err)
	}
	if role != "" {
		t.Fatalf("expected early user to remain unenrolled after later default board change, got %q", role)
	}

	lateUser, err := st.CreateUser(ctx, "late@test.com", "password123", "Late User")
	if err != nil {
		t.Fatalf("CreateUser(late): %v", err)
	}
	role, err = st.GetProjectRole(ctx, project.ID, lateUser.ID)
	if err != nil {
		t.Fatalf("GetProjectRole(late): %v", err)
	}
	if role != RoleViewer {
		t.Fatalf("expected late user seeded as RoleViewer, got %q", role)
	}

	if err := st.ClearDefaultBoardOrgSetting(ctx, owner.ID); err != nil {
		t.Fatalf("ClearDefaultBoardOrgSetting: %v", err)
	}
	afterClearUser, err := st.CreateUser(ctx, "afterclear@test.com", "password123", "After Clear")
	if err != nil {
		t.Fatalf("CreateUser(afterClear): %v", err)
	}
	role, err = st.GetProjectRole(ctx, project.ID, afterClearUser.ID)
	if err != nil {
		t.Fatalf("GetProjectRole(afterClear): %v", err)
	}
	if role != "" {
		t.Fatalf("expected no membership seeded after clearing the default board, got %q", role)
	}

	// Clearing never touches the already-enrolled late user either.
	role, err = st.GetProjectRole(ctx, project.ID, lateUser.ID)
	if err != nil {
		t.Fatalf("GetProjectRole(late, after clear): %v", err)
	}
	if role != RoleViewer {
		t.Fatalf("expected late user's membership to remain after clearing the default, got %q", role)
	}
}

// TestCreateUserOIDC_SeedsDefaultBoardMembership mirrors
// TestCreateUser_SeedsDefaultBoardMembership for the OIDC user-creation path,
// but only for a non-bootstrap OIDC user (an owner already exists here via
// BootstrapUser first).
func TestCreateUserOIDC_SeedsDefaultBoardMembership(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	st.configuredOIDCIssuer = "https://idp.example"

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	project, err := st.CreateProject(WithUserID(ctx, owner.ID), "Onboarding")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := st.SetDefaultBoardOrgSetting(ctx, owner.ID, project.ID); err != nil {
		t.Fatalf("SetDefaultBoardOrgSetting: %v", err)
	}

	oidcUser, err := st.CreateUserOIDC(ctx, st.configuredOIDCIssuer, st.configuredOIDCIssuer, "sub-1", "sso@test.com", "SSO User")
	if err != nil {
		t.Fatalf("CreateUserOIDC: %v", err)
	}
	if oidcUser.IsBootstrap {
		t.Fatalf("expected non-bootstrap OIDC user since an owner already exists")
	}

	role, err := st.GetProjectRole(ctx, project.ID, oidcUser.ID)
	if err != nil {
		t.Fatalf("GetProjectRole: %v", err)
	}
	if role != RoleViewer {
		t.Fatalf("expected OIDC user seeded as RoleViewer, got %q", role)
	}
}

// TestBootstrapUser_NotSeededIntoDefaultBoard documents that the first
// (bootstrap) owner is never auto-enrolled -- it already has implicit access
// to every project via its system role.
func TestBootstrapUser_NotSeededIntoDefaultBoard(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	project, err := st.CreateProject(WithUserID(ctx, owner.ID), "Onboarding")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	role, err := st.GetProjectRole(ctx, project.ID, owner.ID)
	if err != nil {
		t.Fatalf("GetProjectRole: %v", err)
	}
	// The owner is the project creator, seeded as maintainer by CreateProject
	// itself -- not by seedDefaultBoardMembershipTx (which never runs for the
	// bootstrap user). Confirm it's maintainer, not the viewer role
	// auto-enrollment would have produced.
	if role != RoleMaintainer {
		t.Fatalf("expected owner to be RoleMaintainer as project creator, got %q", role)
	}
}

// TestCreateUser_DeletedDefaultBoardProjectSkipsSeedingWithoutFailingCreation
// covers the case where the configured default board project is deleted after
// the org setting was set: account creation must still succeed, just without
// seeding a membership for a project that no longer exists.
func TestCreateUser_DeletedDefaultBoardProjectSkipsSeedingWithoutFailingCreation(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ctxOwner, "Onboarding")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := st.SetDefaultBoardOrgSetting(ctx, owner.ID, project.ID); err != nil {
		t.Fatalf("SetDefaultBoardOrgSetting: %v", err)
	}
	if _, err := st.DeleteProject(ctxOwner, project.ID, owner.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	newUser, err := st.CreateUser(ctx, "new@test.com", "password123", "New User")
	if err != nil {
		t.Fatalf("CreateUser should still succeed when the configured default board was deleted: %v", err)
	}
	if newUser.ID == 0 {
		t.Fatalf("expected a valid created user")
	}
}
