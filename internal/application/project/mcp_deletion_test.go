package project

import (
	"context"
	"errors"
	"testing"

	"scrumboy/internal/store"
)

type mcpDeletionFake struct {
	*mcpManagedProjectFake

	deleteCalls     int
	deleteCtx       context.Context
	deleteProjectID int64
	deleteActorID   int64
	deleteResult    store.DeletedProjectSnapshot
	deleteErr       error
}

func (f *mcpDeletionFake) DeleteProject(
	ctx context.Context,
	projectID int64,
	actorID int64,
) (store.DeletedProjectSnapshot, error) {
	f.trace.add("delete")
	f.deleteCalls++
	f.deleteCtx = ctx
	f.deleteProjectID = projectID
	f.deleteActorID = actorID
	return f.deleteResult, f.deleteErr
}

func newMCPDeletionTestService(fake *mcpDeletionFake) *MCPDeletionService {
	return NewMCPDeletionService(MCPDeletionServiceDependencies{
		Access:   fake,
		Manage:   fake,
		Projects: fake,
	})
}

func TestMCPDeletionPreservesManagedDeleteSequenceAndPreReadProjection(t *testing.T) {
	ctx := store.WithUserID(context.Background(), 81)
	managed := &mcpManagedProjectFake{projectContext: store.ProjectContext{
		Project: store.Project{ID: 82, Slug: "canonical-slug"},
		Role:    store.RoleOwner,
	}}
	fake := &mcpDeletionFake{
		mcpManagedProjectFake: managed,
		deleteResult:          store.DeletedProjectSnapshot{ProjectID: 82, Name: "Deleted"},
	}
	prepared, err := newMCPDeletionTestService(fake).Prepare(
		ctx,
		MCPDeletionCommand{Project: ProjectSlugTarget{ProjectSlug: "requested-slug", Mode: store.ModeFull}},
	)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	assertProjectServiceTrace(t, &managed.trace, "access", "manage")
	if fake.deleteCalls != 0 {
		t.Fatalf("Prepare() performed deletion %d times", fake.deleteCalls)
	}

	managed.projectContext.Project.Slug = "changed-after-prepare"
	result, err := prepared.Delete()
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertProjectServiceTrace(t, &managed.trace, "access", "manage", "delete")
	if fake.deleteCalls != 1 || fake.deleteProjectID != 82 || fake.deleteActorID != 81 {
		t.Fatalf("delete captures = %+v", fake)
	}
	assertProjectServiceContext(t, fake.deleteCtx, ctx)
	if result.ProjectID != 82 || result.ProjectSlug != "canonical-slug" {
		t.Fatalf("result = %+v", result)
	}
}

func TestMCPDeletionStopsAtPreparationAndMutationFailures(t *testing.T) {
	t.Run("access failure prevents deletion", func(t *testing.T) {
		wantErr := errors.New("access failed")
		managed := &mcpManagedProjectFake{accessErr: wantErr}
		fake := &mcpDeletionFake{mcpManagedProjectFake: managed}
		prepared, err := newMCPDeletionTestService(fake).Prepare(
			store.WithUserID(context.Background(), 83),
			MCPDeletionCommand{Project: ProjectSlugTarget{ProjectSlug: "project", Mode: store.ModeFull}},
		)
		if prepared != nil || err != wantErr || fake.deleteCalls != 0 {
			t.Fatalf("Prepare() = %+v, %v; delete calls = %d", prepared, err, fake.deleteCalls)
		}
	})

	t.Run("missing actor prevents manage and deletion", func(t *testing.T) {
		managed := &mcpManagedProjectFake{projectContext: store.ProjectContext{Project: store.Project{ID: 84}}}
		fake := &mcpDeletionFake{mcpManagedProjectFake: managed}
		prepared, err := newMCPDeletionTestService(fake).Prepare(
			context.Background(),
			MCPDeletionCommand{Project: ProjectSlugTarget{ProjectSlug: "project", Mode: store.ModeFull}},
		)
		if prepared != nil || !errors.Is(err, ErrActorRequired) || fake.deleteCalls != 0 {
			t.Fatalf("Prepare() = %+v, %v; delete calls = %d", prepared, err, fake.deleteCalls)
		}
		assertProjectServiceTrace(t, &managed.trace, "access")
	})

	t.Run("manage failure prevents deletion", func(t *testing.T) {
		wantErr := errors.New("manage failed")
		managed := &mcpManagedProjectFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 85}},
			manageErr:      wantErr,
		}
		fake := &mcpDeletionFake{mcpManagedProjectFake: managed}
		prepared, err := newMCPDeletionTestService(fake).Prepare(
			store.WithUserID(context.Background(), 86),
			MCPDeletionCommand{Project: ProjectSlugTarget{ProjectSlug: "project", Mode: store.ModeFull}},
		)
		if prepared != nil || err != wantErr || fake.deleteCalls != 0 {
			t.Fatalf("Prepare() = %+v, %v; delete calls = %d", prepared, err, fake.deleteCalls)
		}
		assertProjectServiceTrace(t, &managed.trace, "access", "manage")
	})

	t.Run("deletion error is unchanged and not retried", func(t *testing.T) {
		wantErr := errors.New("delete failed")
		managed := &mcpManagedProjectFake{projectContext: store.ProjectContext{Project: store.Project{ID: 87}}}
		fake := &mcpDeletionFake{mcpManagedProjectFake: managed, deleteErr: wantErr}
		prepared, err := newMCPDeletionTestService(fake).Prepare(
			store.WithUserID(context.Background(), 88),
			MCPDeletionCommand{Project: ProjectSlugTarget{ProjectSlug: "project", Mode: store.ModeFull}},
		)
		if err != nil {
			t.Fatalf("Prepare() error = %v", err)
		}
		_, err = prepared.Delete()
		if err != wantErr {
			t.Fatalf("Delete() error = %v, want exact deletion error", err)
		}
		assertProjectServiceTrace(t, &managed.trace, "access", "manage", "delete")
		if fake.deleteCalls != 1 {
			t.Fatalf("delete calls = %d, want 1", fake.deleteCalls)
		}
	})
}
