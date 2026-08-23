package sprint

import (
	"context"
	"errors"
	"testing"

	"scrumboy/internal/store"
)

func newRESTDeletionService(fake *restLifecycleFake) *RESTDeletionService {
	return NewRESTDeletionService(RESTDeletionServiceDependencies{
		Roles:     fake,
		Deletions: fake,
		Publisher: fake,
	})
}

func TestRESTDeletionPrepareAuthorization(t *testing.T) {
	const projectID int64 = 83
	const sprintID int64 = 977
	const actorID int64 = 53

	t.Run("missing actor", func(t *testing.T) {
		fake := &restLifecycleFake{role: store.RoleMaintainer}
		prepared, err := newRESTDeletionService(fake).PrepareDelete(context.Background(), DeletionTarget{ProjectID: projectID, SprintID: sprintID})
		if !errors.Is(err, ErrActorRequired) || prepared != nil {
			t.Fatalf("PrepareDelete() = (%v, %v), want (nil, ErrActorRequired)", prepared, err)
		}
		assertRESTLifecycleTrace(t, fake)
	})

	roleDependencyErr := errors.New("role dependency failed")
	for _, tc := range []struct {
		name    string
		role    store.ProjectRole
		roleErr error
		wantErr bool
	}{
		{name: "maintainer", role: store.RoleMaintainer},
		{name: "compatible owner", role: store.RoleOwner},
		{name: "contributor", role: store.RoleContributor, wantErr: true},
		{name: "viewer", role: store.RoleViewer, wantErr: true},
		{name: "nonmember", wantErr: true},
		{name: "role dependency failure", roleErr: roleDependencyErr, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := store.WithUserID(context.Background(), actorID)
			fake := &restLifecycleFake{role: tc.role, roleErr: tc.roleErr}
			prepared, err := newRESTDeletionService(fake).PrepareDelete(ctx, DeletionTarget{ProjectID: projectID, SprintID: sprintID})
			if tc.wantErr {
				if !errors.Is(err, ErrMaintainerRequired) || prepared != nil {
					t.Fatalf("PrepareDelete() = (%v, %v), want (nil, ErrMaintainerRequired)", prepared, err)
				}
			} else if err != nil || prepared == nil {
				t.Fatalf("PrepareDelete() = (%v, %v), want prepared capability", prepared, err)
			}
			assertRESTLifecycleTrace(t, fake, "role")
			if len(fake.roles) != 1 || fake.roles[0].ctx != ctx || fake.roles[0].projectID != projectID || fake.roles[0].userID != actorID {
				t.Fatalf("role calls = %+v, want exact context, project %d, actor %d", fake.roles, projectID, actorID)
			}
			if len(fake.reads) != 0 || len(fake.deletes) != 0 || len(fake.deleted) != 0 {
				t.Fatalf("preparation used later or read capabilities: reads=%d deletes=%d publications=%d", len(fake.reads), len(fake.deletes), len(fake.deleted))
			}
		})
	}
}

func TestRESTDeletionBindsTargetAndPublishesAfterPersistence(t *testing.T) {
	target := DeletionTarget{ProjectID: 89, SprintID: 983, Name: "Sprint 12"}
	original := target
	ctx := store.WithUserID(context.Background(), 59)
	fake := &restLifecycleFake{role: store.RoleMaintainer}
	prepared, err := newRESTDeletionService(fake).PrepareDelete(ctx, target)
	if err != nil {
		t.Fatalf("PrepareDelete() error = %v", err)
	}
	target.ProjectID = 107
	target.SprintID = 109

	if err := prepared.Delete(); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertRESTLifecycleTrace(t, fake, "role", "delete", "publish_deleted")
	if len(fake.deletes) != 1 || fake.deletes[0].ctx != ctx || fake.deletes[0].projectID != original.ProjectID || fake.deletes[0].sprintID != original.SprintID {
		t.Fatalf("delete calls = %+v, want exact bound context, project %d, sprint %d", fake.deletes, original.ProjectID, original.SprintID)
	}
	if len(fake.deleted) != 1 || fake.deleted[0].ctx != ctx || fake.deleted[0].projectID != original.ProjectID || fake.deleted[0].name != "Sprint 12" {
		t.Fatalf("deletion publications = %+v, want bound context, project %d, name Sprint 12", fake.deleted, original.ProjectID)
	}
	if len(fake.reads) != 0 || len(fake.activated) != 0 || len(fake.closed) != 0 {
		t.Fatalf("deletion used unrelated capabilities: reads=%d activated=%d closed=%d", len(fake.reads), len(fake.activated), len(fake.closed))
	}
}

func TestRESTDeletionFailurePassesThroughWithoutPublication(t *testing.T) {
	deleteErr := errors.New("delete failed")
	fake := &restLifecycleFake{role: store.RoleMaintainer, deleteErr: deleteErr}
	prepared, err := newRESTDeletionService(fake).PrepareDelete(store.WithUserID(context.Background(), 61), DeletionTarget{ProjectID: 97, SprintID: 991})
	if err != nil {
		t.Fatalf("PrepareDelete() error = %v", err)
	}
	if err := prepared.Delete(); err != deleteErr {
		t.Fatalf("Delete() error = %v, want %v", err, deleteErr)
	}
	assertRESTLifecycleTrace(t, fake, "role", "delete")
	if len(fake.deletes) != 1 || len(fake.deleted) != 0 {
		t.Fatalf("calls = delete %d publish %d, want 1 and 0", len(fake.deletes), len(fake.deleted))
	}
}

func TestRESTDeletionCancellationAfterPreparationUsesBoundContext(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	ctx := store.WithUserID(base, 67)
	fake := &restLifecycleFake{role: store.RoleMaintainer, honorContext: true}
	prepared, err := newRESTDeletionService(fake).PrepareDelete(ctx, DeletionTarget{ProjectID: 101, SprintID: 997})
	if err != nil {
		t.Fatalf("PrepareDelete() error = %v", err)
	}
	cancel()
	if err := prepared.Delete(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete() error = %v, want context.Canceled", err)
	}
	assertRESTLifecycleTrace(t, fake, "role", "delete")
	if len(fake.deletes) != 1 || fake.deletes[0].ctx != ctx || len(fake.deleted) != 0 {
		t.Fatalf("cancellation calls = %+v publications=%d", fake.deletes, len(fake.deleted))
	}
}

func TestRESTDeletionNilPublisherIsNoop(t *testing.T) {
	fake := &restLifecycleFake{role: store.RoleMaintainer}
	service := NewRESTDeletionService(RESTDeletionServiceDependencies{Roles: fake, Deletions: fake})
	prepared, err := service.PrepareDelete(store.WithUserID(context.Background(), 71), DeletionTarget{ProjectID: 103, SprintID: 1009})
	if err != nil {
		t.Fatalf("PrepareDelete() error = %v", err)
	}
	if err := prepared.Delete(); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertRESTLifecycleTrace(t, fake, "role", "delete")
	if len(fake.deletes) != 1 {
		t.Fatalf("delete calls = %d, want 1", len(fake.deletes))
	}
}
