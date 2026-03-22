package store

import (
	"context"
	"errors"
	"testing"
)

func setupMembershipsTest(t *testing.T) (*Store, func(), context.Context, Project, User, User, User) {
	t.Helper()
	st, cleanup := newTestStore(t)
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		cleanup()
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)

	project, err := st.CreateProject(ctxOwner, "Memberships Test Project")
	if err != nil {
		cleanup()
		t.Fatalf("CreateProject: %v", err)
	}

	maintainer2, err := st.CreateUser(ctx, "m2@example.com", "password", "Maintainer2")
	if err != nil {
		cleanup()
		t.Fatalf("CreateUser maintainer2: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, owner.ID, project.ID, maintainer2.ID, RoleMaintainer); err != nil {
		cleanup()
		t.Fatalf("AddProjectMember maintainer2: %v", err)
	}

	contributor, err := st.CreateUser(ctx, "contrib@example.com", "password", "Contributor")
	if err != nil {
		cleanup()
		t.Fatalf("CreateUser contributor: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, owner.ID, project.ID, contributor.ID, RoleContributor); err != nil {
		cleanup()
		t.Fatalf("AddProjectMember contributor: %v", err)
	}

	return st, cleanup, ctx, project, owner, maintainer2, contributor
}

func TestUpdateProjectMemberRole_MaintainerPromotesContributor(t *testing.T) {
	st, cleanup, ctx, project, owner, _, contributor := setupMembershipsTest(t)
	defer cleanup()

	ctxOwner := WithUserID(ctx, owner.ID)
	if err := st.UpdateProjectMemberRole(ctxOwner, owner.ID, project.ID, contributor.ID, RoleMaintainer); err != nil {
		t.Fatalf("UpdateProjectMemberRole: %v", err)
	}

	role, err := st.GetProjectRole(ctx, project.ID, contributor.ID)
	if err != nil {
		t.Fatalf("GetProjectRole: %v", err)
	}
	if role != RoleMaintainer {
		t.Fatalf("expected role maintainer, got %q", role)
	}
}

func TestUpdateProjectMemberRole_MaintainerDemotesOtherMaintainer(t *testing.T) {
	st, cleanup, ctx, project, owner, maintainer2, _ := setupMembershipsTest(t)
	defer cleanup()

	ctxOwner := WithUserID(ctx, owner.ID)
	if err := st.UpdateProjectMemberRole(ctxOwner, owner.ID, project.ID, maintainer2.ID, RoleContributor); err != nil {
		t.Fatalf("UpdateProjectMemberRole: %v", err)
	}

	role, err := st.GetProjectRole(ctx, project.ID, maintainer2.ID)
	if err != nil {
		t.Fatalf("GetProjectRole: %v", err)
	}
	if role != RoleContributor {
		t.Fatalf("expected role contributor, got %q", role)
	}
}

func TestUpdateProjectMemberRole_CannotDemoteSelf(t *testing.T) {
	st, cleanup, ctx, project, _, maintainer2, _ := setupMembershipsTest(t)
	defer cleanup()

	ctxM2 := WithUserID(ctx, maintainer2.ID)
	err := st.UpdateProjectMemberRole(ctxM2, maintainer2.ID, project.ID, maintainer2.ID, RoleContributor)
	if err == nil {
		t.Fatal("expected ErrConflict for self-demotion")
	}
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestUpdateProjectMemberRole_ContributorUnauthorized(t *testing.T) {
	st, cleanup, ctx, project, _, maintainer2, contributor := setupMembershipsTest(t)
	defer cleanup()

	ctxContrib := WithUserID(ctx, contributor.ID)
	err := st.UpdateProjectMemberRole(ctxContrib, contributor.ID, project.ID, maintainer2.ID, RoleContributor)
	if err == nil {
		t.Fatal("expected ErrUnauthorized for contributor")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestUpdateProjectMemberRole_TargetNotMember(t *testing.T) {
	st, cleanup, ctx, project, owner, _, contributor := setupMembershipsTest(t)
	defer cleanup()

	nonMember, err := st.CreateUser(ctx, "non@example.com", "password", "NonMember")
	if err != nil {
		cleanup()
		t.Fatalf("CreateUser: %v", err)
	}

	ctxOwner := WithUserID(ctx, owner.ID)
	err = st.UpdateProjectMemberRole(ctxOwner, owner.ID, project.ID, nonMember.ID, RoleMaintainer)
	if err == nil {
		t.Fatal("expected ErrNotFound for non-member")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// Ensure contributor is still contributor (we used wrong target)
	role, _ := st.GetProjectRole(ctx, project.ID, contributor.ID)
	if role != RoleContributor {
		t.Fatalf("contributor role unchanged but got %q", role)
	}
}

func TestUpdateProjectMemberRole_InvalidRole(t *testing.T) {
	st, cleanup, ctx, project, owner, _, contributor := setupMembershipsTest(t)
	defer cleanup()

	ctxOwner := WithUserID(ctx, owner.ID)
	// Use an invalid role string (not in validMemberRoles)
	err := st.UpdateProjectMemberRole(ctxOwner, owner.ID, project.ID, contributor.ID, ProjectRole("admin"))
	if err == nil {
		t.Fatal("expected ErrValidation for invalid role")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestUpdateProjectMemberRole_DemoteToViewer(t *testing.T) {
	st, cleanup, ctx, project, owner, _, contributor := setupMembershipsTest(t)
	defer cleanup()

	ctxOwner := WithUserID(ctx, owner.ID)
	if err := st.UpdateProjectMemberRole(ctxOwner, owner.ID, project.ID, contributor.ID, RoleViewer); err != nil {
		t.Fatalf("UpdateProjectMemberRole to viewer: %v", err)
	}
	role, err := st.GetProjectRole(ctx, project.ID, contributor.ID)
	if err != nil {
		t.Fatalf("GetProjectRole: %v", err)
	}
	if role != RoleViewer {
		t.Fatalf("expected role viewer, got %q", role)
	}
}

func TestUpdateProjectMemberRole_LastMaintainerCannotDemoteToViewer(t *testing.T) {
	st, cleanup, ctx, project, owner, maintainer2, _ := setupMembershipsTest(t)
	defer cleanup()

	ctxOwner := WithUserID(ctx, owner.ID)
	// Demote maintainer2 to contributor first so owner is the only maintainer
	if err := st.UpdateProjectMemberRole(ctxOwner, owner.ID, project.ID, maintainer2.ID, RoleContributor); err != nil {
		t.Fatalf("demote maintainer2 to contributor: %v", err)
	}
	// Re-promote to maintainer so we have two maintainers
	if err := st.UpdateProjectMemberRole(ctxOwner, owner.ID, project.ID, maintainer2.ID, RoleMaintainer); err != nil {
		t.Fatalf("promote maintainer2 to maintainer: %v", err)
	}
	// Demote owner to contributor so maintainer2 is the only maintainer
	if err := st.UpdateProjectMemberRole(ctxOwner, maintainer2.ID, project.ID, owner.ID, RoleContributor); err != nil {
		t.Fatalf("demote owner to contributor: %v", err)
	}
	// Now maintainer2 is the last maintainer. Demoting them to viewer should fail.
	ctxM2 := WithUserID(ctx, maintainer2.ID)
	err := st.UpdateProjectMemberRole(ctxM2, maintainer2.ID, project.ID, maintainer2.ID, RoleViewer)
	if err == nil {
		t.Fatal("expected error when demoting last maintainer to viewer")
	}
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestUpdateProjectMemberRole_NoOpUnchanged(t *testing.T) {
	st, cleanup, ctx, project, owner, _, contributor := setupMembershipsTest(t)
	defer cleanup()

	ctxOwner := WithUserID(ctx, owner.ID)
	if err := st.UpdateProjectMemberRole(ctxOwner, owner.ID, project.ID, contributor.ID, RoleContributor); err != nil {
		t.Fatalf("UpdateProjectMemberRole no-op: %v", err)
	}

	role, err := st.GetProjectRole(ctx, project.ID, contributor.ID)
	if err != nil {
		t.Fatalf("GetProjectRole: %v", err)
	}
	if role != RoleContributor {
		t.Fatalf("expected role contributor unchanged, got %q", role)
	}
}
