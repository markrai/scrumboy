package store

import (
	"context"
	"errors"
	"testing"
)

func TestUpdateProjectPatch_twoFieldsSuccess(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "patch-owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, owner.ID)

	project, err := st.CreateProject(ctx, "Patch Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	renamed := "Renamed Project"
	weeks := 1
	if err := st.UpdateProjectPatch(ctx, project.ID, owner.ID, UpdateProjectPatch{
		Name:               &renamed,
		DefaultSprintWeeks: &weeks,
	}); err != nil {
		t.Fatalf("UpdateProjectPatch: %v", err)
	}

	updated, err := st.GetProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if updated.Name != renamed {
		t.Fatalf("expected name %q, got %q", renamed, updated.Name)
	}
	if updated.DefaultSprintWeeks != 1 {
		t.Fatalf("expected defaultSprintWeeks 1, got %d", updated.DefaultSprintWeeks)
	}
}

func TestUpdateProjectPatch_invalidWeeksLeavesNameUnchanged(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "patch-atomic@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, owner.ID)

	project, err := st.CreateProject(ctx, "Atomic Patch Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	originalName := project.Name

	renamed := "Should Not Stick"
	weeks := 3
	err = st.UpdateProjectPatch(ctx, project.ID, owner.ID, UpdateProjectPatch{
		Name:               &renamed,
		DefaultSprintWeeks: &weeks,
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}

	updated, err := st.GetProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if updated.Name != originalName {
		t.Fatalf("expected name unchanged %q, got %q", originalName, updated.Name)
	}
	if updated.DefaultSprintWeeks != 2 {
		t.Fatalf("expected defaultSprintWeeks unchanged at 2, got %d", updated.DefaultSprintWeeks)
	}
}
