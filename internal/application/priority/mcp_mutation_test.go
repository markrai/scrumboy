package priority

import (
	"context"
	"errors"
	"testing"

	"scrumboy/internal/store"
)

var _ MCPMutationAccessStore = (*store.Store)(nil)

type mcpMutationFake struct {
	projectContext store.ProjectContext
	accessErr      error

	createResult store.PriorityTier
	createErr    error
	updateErr    error
	deleteErr    error

	tiers   []store.PriorityTier
	readErr error
}

func (f *mcpMutationFake) GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error) {
	if f.accessErr != nil {
		return store.ProjectContext{}, f.accessErr
	}
	return f.projectContext, nil
}

func (f *mcpMutationFake) AddPriorityTier(ctx context.Context, projectID int64, name string) (store.PriorityTier, error) {
	return f.createResult, f.createErr
}

func (f *mcpMutationFake) UpdatePriorityTier(ctx context.Context, projectID int64, key, name, color string) error {
	return f.updateErr
}

func (f *mcpMutationFake) DeletePriorityTier(ctx context.Context, projectID int64, key string) error {
	return f.deleteErr
}

func (f *mcpMutationFake) GetProjectPriorities(ctx context.Context, projectID int64) ([]store.PriorityTier, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.tiers, nil
}

func newMCPMutationTestService(f *mcpMutationFake) *MCPMutationService {
	return NewMCPMutationService(MCPMutationServiceDependencies{Access: f, Mutations: f, Priority: f})
}

func TestMCPMutationPrepareAuthorization(t *testing.T) {
	accessFailure := errors.New("access lookup failed")

	t.Run("access error propagates", func(t *testing.T) {
		fake := &mcpMutationFake{accessErr: accessFailure}
		service := newMCPMutationTestService(fake)
		if _, err := service.Prepare(context.Background(), MCPMutationTarget{ProjectSlug: "proj"}); err != accessFailure {
			t.Fatalf("Prepare error=%v want=%v", err, accessFailure)
		}
	})

	t.Run("non-maintainer rejected", func(t *testing.T) {
		fake := &mcpMutationFake{projectContext: store.ProjectContext{
			Project: store.Project{ID: 5},
			Role:    store.RoleContributor,
		}}
		service := newMCPMutationTestService(fake)
		if _, err := service.Prepare(context.Background(), MCPMutationTarget{ProjectSlug: "proj"}); !errors.Is(err, ErrMaintainerRequired) {
			t.Fatalf("Prepare error=%v want=%v", err, ErrMaintainerRequired)
		}
	})

	t.Run("maintainer prepared", func(t *testing.T) {
		fake := &mcpMutationFake{projectContext: store.ProjectContext{
			Project: store.Project{ID: 5},
			Role:    store.RoleMaintainer,
		}}
		service := newMCPMutationTestService(fake)
		prepared, err := service.Prepare(context.Background(), MCPMutationTarget{ProjectSlug: "proj"})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if prepared == nil {
			t.Fatal("expected prepared mutation")
		}
	})
}

func TestMCPMutationUpdateProjection(t *testing.T) {
	maintainerCtx := store.ProjectContext{Project: store.Project{ID: 5}, Role: store.RoleMaintainer}

	t.Run("success returns updated tier from post-read", func(t *testing.T) {
		fake := &mcpMutationFake{
			projectContext: maintainerCtx,
			tiers: []store.PriorityTier{
				{Key: "low", Name: "Low"},
				{Key: "high", Name: "Urgent-ish", Color: "#ff0000"},
			},
		}
		service := newMCPMutationTestService(fake)
		prepared, err := service.Prepare(context.Background(), MCPMutationTarget{ProjectSlug: "proj"})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		got, err := prepared.Update(UpdateCommand{Key: "high", Name: "Urgent-ish", Color: "#ff0000"})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if got.Key != "high" || got.Name != "Urgent-ish" {
			t.Fatalf("got=%+v", got)
		}
	})

	t.Run("update store failure short-circuits before read", func(t *testing.T) {
		storeFailure := errors.New("update failed")
		fake := &mcpMutationFake{projectContext: maintainerCtx, updateErr: storeFailure}
		service := newMCPMutationTestService(fake)
		prepared, err := service.Prepare(context.Background(), MCPMutationTarget{ProjectSlug: "proj"})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		if _, err := prepared.Update(UpdateCommand{Key: "low", Name: "Low", Color: "#000000"}); err != storeFailure {
			t.Fatalf("Update error=%v want=%v", err, storeFailure)
		}
	})

	t.Run("post-write read failure wraps as projection error", func(t *testing.T) {
		readFailure := errors.New("read failed")
		fake := &mcpMutationFake{projectContext: maintainerCtx, readErr: readFailure}
		service := newMCPMutationTestService(fake)
		prepared, err := service.Prepare(context.Background(), MCPMutationTarget{ProjectSlug: "proj"})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		_, err = prepared.Update(UpdateCommand{Key: "low", Name: "Low", Color: "#000000"})
		if !errors.Is(err, ErrPriorityProjectionFailed) {
			t.Fatalf("Update error=%v want wrapped %v", err, ErrPriorityProjectionFailed)
		}
		if !errors.Is(err, readFailure) {
			t.Fatalf("Update error=%v want to unwrap to %v", err, readFailure)
		}
	})

	t.Run("updated key missing from post-read is a distinct projection error", func(t *testing.T) {
		fake := &mcpMutationFake{projectContext: maintainerCtx, tiers: []store.PriorityTier{{Key: "low"}}}
		service := newMCPMutationTestService(fake)
		prepared, err := service.Prepare(context.Background(), MCPMutationTarget{ProjectSlug: "proj"})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		_, err = prepared.Update(UpdateCommand{Key: "gone", Name: "Gone", Color: "#000000"})
		if !errors.Is(err, ErrPriorityProjectionTierMissing) {
			t.Fatalf("Update error=%v want=%v", err, ErrPriorityProjectionTierMissing)
		}
		if !errors.Is(err, ErrPriorityProjectionFailed) {
			t.Fatalf("Update error=%v should also satisfy %v", err, ErrPriorityProjectionFailed)
		}
	})
}

func TestMCPMutationCreateAndDeleteNoProjection(t *testing.T) {
	maintainerCtx := store.ProjectContext{Project: store.Project{ID: 5}, Role: store.RoleMaintainer}
	wantTier := store.PriorityTier{Key: "critical", Name: "Critical"}
	fake := &mcpMutationFake{projectContext: maintainerCtx, createResult: wantTier}
	service := newMCPMutationTestService(fake)
	prepared, err := service.Prepare(context.Background(), MCPMutationTarget{ProjectSlug: "proj"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	got, err := prepared.Create(CreateCommand{Name: "Critical"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got != wantTier {
		t.Fatalf("Create result=%+v want=%+v", got, wantTier)
	}

	if err := prepared.Delete(DeleteCommand{Key: "critical"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
