package store

import (
	"context"
	"errors"
	"testing"
)

func TestCheckCanManageProject_durableMaintainerAllowed(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "manage-owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, owner.ID)

	project, err := st.CreateProject(ctx, "Durable Manage")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := st.CheckCanManageProject(ctx, project.ID, owner.ID); err != nil {
		t.Fatalf("expected maintainer to manage project, got %v", err)
	}
}

func TestCheckCanManageProject_durableViewerForbidden(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "manage-viewer-owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, owner.ID)

	project, err := st.CreateProject(ctx, "Viewer Forbidden")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	viewer, err := st.CreateUser(ctx, "manage-viewer@test.com", "password123", "Viewer")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.AddProjectMember(ctx, owner.ID, project.ID, viewer.ID, RoleViewer); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}

	if err := st.CheckCanManageProject(ctx, project.ID, viewer.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for viewer, got %v", err)
	}
}

func TestCheckCanManageProject_durableNonMemberNotFound(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "manage-nonmember-owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, owner.ID)

	project, err := st.CreateProject(ctx, "Non Member")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	other, err := st.CreateUser(ctx, "manage-nonmember@test.com", "password123", "Other")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := st.CheckCanManageProject(ctx, project.ID, other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-member, got %v", err)
	}
}

func TestCheckCanManageProject_temporaryBoardOwnerAllowed(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "temp-manage-owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, owner.ID)

	tempBoard, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	tempBoard, err = st.GetProject(ctx, tempBoard.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if tempBoard.CreatorUserID == nil {
		t.Fatal("expected Temporary Board owner (creator_user_id set)")
	}

	if err := st.CheckCanManageProject(ctx, tempBoard.ID, owner.ID); err != nil {
		t.Fatalf("expected Temporary Board owner to manage board, got %v", err)
	}
}

func TestCheckCanManageProject_temporaryBoardOtherUserForbidden(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "temp-other-owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, owner.ID)

	tempBoard, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}

	other, err := st.CreateUser(ctx, "temp-other@test.com", "password123", "Other")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := st.CheckCanManageProject(ctx, tempBoard.ID, other.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for non-owner on temporary board, got %v", err)
	}
}

func TestCheckCanManageProject_anonymousBoardNotFound(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "anon-manage-owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}

	anonBoard, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	if anonBoard.CreatorUserID != nil {
		t.Fatal("expected anonymous board without creator_user_id")
	}

	if err := st.CheckCanManageProject(ctx, anonBoard.ID, owner.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for anonymous board project management, got %v", err)
	}
}

func TestUpdateProjectPatch_temporaryBoardOwnerSuccess(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "temp-patch-owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, owner.ID)

	tempBoard, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}

	renamed := "Temp Renamed"
	if err := st.UpdateProjectPatch(ctx, tempBoard.ID, owner.ID, UpdateProjectPatch{Name: &renamed}); err != nil {
		t.Fatalf("UpdateProjectPatch: %v", err)
	}

	updated, err := st.GetProject(ctx, tempBoard.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if updated.Name != renamed {
		t.Fatalf("expected name %q, got %q", renamed, updated.Name)
	}
}

func TestDeleteProject_temporaryBoardOwnerSuccess(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "temp-delete-owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, owner.ID)

	tempBoard, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}

	if _, err := st.DeleteProject(ctx, tempBoard.ID, owner.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := st.GetProject(ctx, tempBoard.ID); err == nil {
		t.Fatal("expected temporary board to be deleted")
	}
}

func setupTemporaryBoardOwner(t *testing.T, st *Store) (context.Context, Project, int64) {
	t.Helper()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "temp-settings-owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, owner.ID)
	tempBoard, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	tempBoard, err = st.GetProject(ctx, tempBoard.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if tempBoard.CreatorUserID == nil {
		t.Fatal("expected Temporary Board owner")
	}
	return ctx, tempBoard, owner.ID
}

func TestUpdateProjectName_temporaryBoardOwnerSuccess(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx, tempBoard, ownerID := setupTemporaryBoardOwner(t, st)

	if err := st.UpdateProjectName(ctx, tempBoard.ID, ownerID, "Temporary Board Renamed"); err != nil {
		t.Fatalf("UpdateProjectName: %v", err)
	}
	updated, err := st.GetProject(ctx, tempBoard.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if updated.Name != "Temporary Board Renamed" {
		t.Fatalf("expected renamed board, got %q", updated.Name)
	}
}

func TestUpdateProjectName_temporaryBoardNonOwnerForbidden(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx, tempBoard, _ := setupTemporaryBoardOwner(t, st)

	other, err := st.CreateUser(ctx, "temp-rename-other@test.com", "password123", "Other")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.UpdateProjectName(ctx, tempBoard.ID, other.ID, "Should Not Stick"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestUpdateProjectName_anonymousBoardRenameUnchanged(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	anonBoard, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	if err := st.UpdateProjectName(ctx, anonBoard.ID, 0, "Anonymous Renamed"); err != nil {
		t.Fatalf("UpdateProjectName: %v", err)
	}
	updated, err := st.GetProject(ctx, anonBoard.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if updated.Name != "Anonymous Renamed" {
		t.Fatalf("expected anonymous rename to work, got %q", updated.Name)
	}
}

func TestUpdateProjectImage_temporaryBoardOwnerSuccess(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx, tempBoard, ownerID := setupTemporaryBoardOwner(t, st)

	image := "data:image/png;base64,aaaa"
	if err := st.UpdateProjectImage(ctx, tempBoard.ID, ownerID, &image, "#123456"); err != nil {
		t.Fatalf("UpdateProjectImage: %v", err)
	}
}

func TestUpdateProjectImage_temporaryBoardNonOwnerForbidden(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx, tempBoard, _ := setupTemporaryBoardOwner(t, st)

	other, err := st.CreateUser(ctx, "temp-image-other@test.com", "password123", "Other")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	image := "data:image/png;base64,aaaa"
	if err := st.UpdateProjectImage(ctx, tempBoard.ID, other.ID, &image, "#123456"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestUpdateProjectImage_anonymousBoardNotFound(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	anonBoard, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	image := "data:image/png;base64,aaaa"
	if err := st.UpdateProjectImage(ctx, anonBoard.ID, 1, &image, "#123456"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateProjectDefaultSprintWeeks_temporaryBoardOwnerSuccess(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx, tempBoard, ownerID := setupTemporaryBoardOwner(t, st)

	if err := st.UpdateProjectDefaultSprintWeeks(ctx, tempBoard.ID, ownerID, 1); err != nil {
		t.Fatalf("UpdateProjectDefaultSprintWeeks: %v", err)
	}
}

func TestUpdateProjectDefaultSprintWeeks_temporaryBoardNonOwnerForbidden(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx, tempBoard, _ := setupTemporaryBoardOwner(t, st)

	other, err := st.CreateUser(ctx, "temp-weeks-other@test.com", "password123", "Other")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.UpdateProjectDefaultSprintWeeks(ctx, tempBoard.ID, other.ID, 1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestUpdateProjectDefaultSprintWeeks_anonymousBoardNotFound(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	anonBoard, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	if err := st.UpdateProjectDefaultSprintWeeks(ctx, anonBoard.ID, 1, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateProjectName_durableViewerForbidden(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "rename-viewer-owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ctx, "Rename Viewer Forbidden")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	viewer, err := st.CreateUser(ctx, "rename-viewer@test.com", "password123", "Viewer")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.AddProjectMember(ctx, owner.ID, project.ID, viewer.ID, RoleViewer); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}
	if err := st.UpdateProjectName(ctx, project.ID, viewer.ID, "Nope"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestUpdateProjectDefaultSprintWeeks_temporaryBoardNonOwnerInvalidWeeksForbidden(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx, tempBoard, _ := setupTemporaryBoardOwner(t, st)

	other, err := st.CreateUser(ctx, "temp-weeks-invalid-other@test.com", "password123", "Other")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	err = st.UpdateProjectDefaultSprintWeeks(ctx, tempBoard.ID, other.ID, 3)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden before validation, got %v", err)
	}
}

func TestUpdateProjectDefaultSprintWeeks_durableNonMemberInvalidWeeksNotFound(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "weeks-nonmember-owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ctx, "Weeks Non Member")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	other, err := st.CreateUser(ctx, "weeks-nonmember@test.com", "password123", "Other")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	err = st.UpdateProjectDefaultSprintWeeks(ctx, project.ID, other.ID, 3)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound before validation, got %v", err)
	}
}

func TestUpdateProjectDefaultSprintWeeks_temporaryBoardOwnerInvalidWeeksValidation(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx, tempBoard, ownerID := setupTemporaryBoardOwner(t, st)

	err := st.UpdateProjectDefaultSprintWeeks(ctx, tempBoard.ID, ownerID, 3)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation after auth, got %v", err)
	}
}

func TestUpdateProjectDefaultSprintWeeks_durableMaintainerInvalidWeeksValidation(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "weeks-maintainer@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ctx, "Weeks Maintainer Invalid")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	err = st.UpdateProjectDefaultSprintWeeks(ctx, project.ID, owner.ID, 3)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation after auth, got %v", err)
	}
}

func TestUpdateProjectDefaultSprintWeeks_durableMaintainerValidWeeksSuccess(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "weeks-maintainer-valid@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ctx, "Weeks Maintainer Valid")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := st.UpdateProjectDefaultSprintWeeks(ctx, project.ID, owner.ID, 1); err != nil {
		t.Fatalf("UpdateProjectDefaultSprintWeeks: %v", err)
	}
	updated, err := st.GetProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if updated.DefaultSprintWeeks != 1 {
		t.Fatalf("expected defaultSprintWeeks 1, got %d", updated.DefaultSprintWeeks)
	}
}
