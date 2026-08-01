package mcp

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func TestBoardGetContract_CapabilityAndAuthenticationGates(t *testing.T) {
	h := newBoardGetContractHarness(t)

	t.Run("anonymous mode precedes store capability lookup", func(t *testing.T) {
		h.Recording.reset()
		adapter := New(h.Recording, Options{Mode: "anonymous"})
		_, _, err := adapter.handleBoardGet(context.Background(), map[string]any{"projectSlug": h.Project.Slug})

		requireBoardGetError(t, err, http.StatusForbidden, CodeCapabilityUnavailable, "board_get is unavailable in anonymous mode", map[string]any{})
		requireOperationNames(t, h.Recording)
	})

	t.Run("signed out full mode is rejected before access", func(t *testing.T) {
		h.Recording.reset()
		_, _, err := h.Adapter.handleBoardGet(context.Background(), map[string]any{"projectSlug": h.Project.Slug})

		requireBoardGetError(t, err, http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", map[string]any{})
		requireOperationNames(t, h.Recording, "countUsers")
	})

	t.Run("capability store failure is exact and prevents access", func(t *testing.T) {
		h.Recording.reset()
		h.Recording.Errors["countUsers"] = injectedBoardGetError("countUsers")
		t.Cleanup(func() { delete(h.Recording.Errors, "countUsers") })

		_, _, err := h.Adapter.handleBoardGet(h.Context, map[string]any{"projectSlug": h.Project.Slug})

		requireBoardGetError(t, err, http.StatusInternalServerError, CodeInternal, "internal error", map[string]any{
			"detail": "phase 7 injected countUsers failure",
		})
		requireOperationNames(t, h.Recording, "countUsers")
	})
}

func TestBoardGetContract_DurableAccessAndNotFoundMasking(t *testing.T) {
	t.Run("maintainer", func(t *testing.T) {
		h := newBoardGetContractHarness(t)
		data, _, err := h.call(map[string]any{"projectSlug": h.Project.Slug})
		if err != nil {
			t.Fatalf("maintainer read: %v", err)
		}
		project := data.(map[string]any)["project"].(boardProjectItem)
		if project.Role != string(store.RoleMaintainer) {
			t.Fatalf("project role = %q, want maintainer", project.Role)
		}
		if len(h.Recording.callsFor("access")) != 1 {
			t.Fatalf("access calls = %d, want 1", len(h.Recording.callsFor("access")))
		}
		requireAllBoardGetContexts(t, h.Recording, h.Context)
	})

	t.Run("viewer", func(t *testing.T) {
		h := newBoardGetContractHarness(t)
		if err := h.Store.AddProjectMember(h.Context, h.Owner.ID, h.Project.ID, h.Other.ID, store.RoleViewer); err != nil {
			t.Fatalf("add viewer: %v", err)
		}
		h.Context = store.WithUserID(context.Background(), h.Other.ID)

		data, _, err := h.call(map[string]any{"projectSlug": h.Project.Slug})
		if err != nil {
			t.Fatalf("viewer read: %v", err)
		}
		project := data.(map[string]any)["project"].(boardProjectItem)
		if project.Role != string(store.RoleViewer) {
			t.Fatalf("project role = %q, want viewer", project.Role)
		}
		requireAllBoardGetContexts(t, h.Recording, h.Context)
	})

	t.Run("contributor", func(t *testing.T) {
		h := newBoardGetContractHarness(t)
		if err := h.Store.AddProjectMember(h.Context, h.Owner.ID, h.Project.ID, h.Other.ID, store.RoleContributor); err != nil {
			t.Fatalf("add contributor: %v", err)
		}
		h.Context = store.WithUserID(context.Background(), h.Other.ID)

		data, _, err := h.call(map[string]any{"projectSlug": h.Project.Slug})
		if err != nil {
			t.Fatalf("contributor read: %v", err)
		}
		project := data.(map[string]any)["project"].(boardProjectItem)
		if project.Role != string(store.RoleContributor) {
			t.Fatalf("project role = %q, want contributor", project.Role)
		}
	})

	t.Run("non-member and missing are indistinguishable", func(t *testing.T) {
		h := newBoardGetContractHarness(t)
		h.Context = store.WithUserID(context.Background(), h.Other.ID)

		_, _, deniedErr := h.call(map[string]any{"projectSlug": h.Project.Slug})
		requireBoardGetError(t, deniedErr, http.StatusNotFound, CodeNotFound, "not found", map[string]any{})
		requireOperationNames(t, h.Recording, "countUsers", "access")

		_, _, missingErr := h.call(map[string]any{"projectSlug": "phase-7-board-missing"})
		requireBoardGetError(t, missingErr, http.StatusNotFound, CodeNotFound, "not found", map[string]any{})
		requireOperationNames(t, h.Recording, "countUsers", "access")
	})

	t.Run("access failure prevents all later operations", func(t *testing.T) {
		h := newBoardGetContractHarness(t)
		h.Recording.Errors["access"] = injectedBoardGetError("access")

		_, _, err := h.call(map[string]any{"projectSlug": h.Project.Slug})

		requireBoardGetError(t, err, http.StatusInternalServerError, CodeInternal, "internal error", map[string]any{
			"detail": "phase 7 injected access failure",
		})
		requireOperationNames(t, h.Recording, "countUsers", "access")
	})
}

func TestBoardGetContract_ExpiringBoardAccess(t *testing.T) {
	tests := []struct {
		name     string
		creator  bool
		useOther bool
	}{
		{name: "creator-owned temporary board", creator: true},
		{name: "ownerless anonymous board"},
		{name: "temporary board read by another authenticated actor", creator: true, useOther: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newBoardGetContractHarness(t)
			createCtx := context.Background()
			if tt.creator {
				createCtx = store.WithUserID(createCtx, h.Owner.ID)
			}
			project, err := h.Store.CreateAnonymousBoard(createCtx)
			if err != nil {
				t.Fatalf("create expiring board: %v", err)
			}
			if tt.useOther {
				h.Context = store.WithUserID(context.Background(), h.Other.ID)
			}

			data, _, readErr := h.call(map[string]any{"projectSlug": project.Slug})
			if readErr != nil {
				t.Fatalf("read active expiring board: %v", readErr)
			}
			resultProject := data.(map[string]any)["project"].(boardProjectItem)
			if resultProject.ProjectSlug != project.Slug || resultProject.Role != "" {
				t.Fatalf("expiring project result = %#v", resultProject)
			}
			if len(h.Recording.callsFor("access")) != 1 || len(h.Recording.callsFor("activity")) != 1 {
				t.Fatalf("access/activity calls = %d/%d, want 1/1", len(h.Recording.callsFor("access")), len(h.Recording.callsFor("activity")))
			}
		})
	}

	t.Run("expired board is masked as not found", func(t *testing.T) {
		h := newBoardGetContractHarness(t)
		project, err := h.Store.CreateAnonymousBoard(store.WithUserID(context.Background(), h.Owner.ID))
		if err != nil {
			t.Fatalf("create temporary board: %v", err)
		}
		expired := time.Now().UTC().Add(-time.Hour).UnixMilli()
		if _, err := h.DB.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, expired, project.ID); err != nil {
			t.Fatalf("expire board: %v", err)
		}

		_, _, readErr := h.call(map[string]any{"projectSlug": project.Slug})

		requireBoardGetError(t, readErr, http.StatusNotFound, CodeNotFound, "not found", map[string]any{})
		requireOperationNames(t, h.Recording, "countUsers", "access")
	})
}

func TestBoardGetContract_AccessErrorMappingExposesStoreDetail(t *testing.T) {
	h := newBoardGetContractHarness(t)
	h.Recording.Errors["access"] = errors.New("sensitive database failure")

	_, _, err := h.call(map[string]any{"projectSlug": h.Project.Slug})

	requireBoardGetError(t, err, http.StatusInternalServerError, CodeInternal, "internal error", map[string]any{
		"detail": "sensitive database failure",
	})
}
