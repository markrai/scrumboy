package sprint

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

type restLifecycleRoleCall struct {
	ctx       context.Context
	projectID int64
	userID    int64
}

type restLifecycleReadCall struct {
	ctx      context.Context
	sprintID int64
}

type restLifecycleMutationCall struct {
	ctx       context.Context
	projectID int64
	sprintID  int64
}

type restLifecyclePublicationCall struct {
	ctx       context.Context
	projectID int64
	name      string
}

type restLifecycleFake struct {
	trace []string

	honorContext bool

	role    store.ProjectRole
	roleErr error
	roles   []restLifecycleRoleCall

	sprint    store.Sprint
	sprintErr error
	reads     []restLifecycleReadCall

	activateErr error
	activates   []restLifecycleMutationCall
	closeErr    error
	closes      []restLifecycleMutationCall
	deleteErr   error
	deletes     []restLifecycleMutationCall

	activated []restLifecyclePublicationCall
	closed    []restLifecyclePublicationCall
	deleted   []restLifecyclePublicationCall
}

func (f *restLifecycleFake) GetProjectRole(
	ctx context.Context,
	projectID int64,
	userID int64,
) (store.ProjectRole, error) {
	f.trace = append(f.trace, "role")
	f.roles = append(f.roles, restLifecycleRoleCall{ctx: ctx, projectID: projectID, userID: userID})
	if f.honorContext && ctx.Err() != nil {
		return "", ctx.Err()
	}
	if f.roleErr != nil {
		return "", f.roleErr
	}
	return f.role, nil
}

func (f *restLifecycleFake) GetSprintByID(ctx context.Context, sprintID int64) (store.Sprint, error) {
	f.trace = append(f.trace, "read")
	f.reads = append(f.reads, restLifecycleReadCall{ctx: ctx, sprintID: sprintID})
	if f.honorContext && ctx.Err() != nil {
		return store.Sprint{}, ctx.Err()
	}
	if f.sprintErr != nil {
		return store.Sprint{}, f.sprintErr
	}
	return f.sprint, nil
}

func (f *restLifecycleFake) ActivateSprint(ctx context.Context, projectID, sprintID int64) error {
	f.trace = append(f.trace, "activate")
	f.activates = append(f.activates, restLifecycleMutationCall{ctx: ctx, projectID: projectID, sprintID: sprintID})
	if f.honorContext && ctx.Err() != nil {
		return ctx.Err()
	}
	return f.activateErr
}

func (f *restLifecycleFake) CloseSprint(ctx context.Context, projectID, sprintID int64) error {
	f.trace = append(f.trace, "close")
	f.closes = append(f.closes, restLifecycleMutationCall{ctx: ctx, projectID: projectID, sprintID: sprintID})
	if f.honorContext && ctx.Err() != nil {
		return ctx.Err()
	}
	return f.closeErr
}

func (f *restLifecycleFake) DeleteSprint(ctx context.Context, projectID, sprintID int64) error {
	f.trace = append(f.trace, "delete")
	f.deletes = append(f.deletes, restLifecycleMutationCall{ctx: ctx, projectID: projectID, sprintID: sprintID})
	if f.honorContext && ctx.Err() != nil {
		return ctx.Err()
	}
	return f.deleteErr
}

func (f *restLifecycleFake) PublishSprintActivated(ctx context.Context, projectID int64) {
	f.trace = append(f.trace, "publish_activated")
	f.activated = append(f.activated, restLifecyclePublicationCall{ctx: ctx, projectID: projectID})
}

func (f *restLifecycleFake) PublishSprintClosed(ctx context.Context, projectID int64, name string) {
	f.trace = append(f.trace, "publish_closed")
	f.closed = append(f.closed, restLifecyclePublicationCall{ctx: ctx, projectID: projectID, name: name})
}

func (f *restLifecycleFake) PublishSprintDeleted(ctx context.Context, projectID int64, name string) {
	f.trace = append(f.trace, "publish_deleted")
	f.deleted = append(f.deleted, restLifecyclePublicationCall{ctx: ctx, projectID: projectID, name: name})
}

var _ RoleStore = (*restLifecycleFake)(nil)
var _ SprintReadStore = (*restLifecycleFake)(nil)
var _ TransitionStore = (*restLifecycleFake)(nil)
var _ DeletionStore = (*restLifecycleFake)(nil)
var _ RESTTransitionPublisher = (*restLifecycleFake)(nil)
var _ RESTDeletionPublisher = (*restLifecycleFake)(nil)

func newRESTLifecycleService(fake *restLifecycleFake) *RESTLifecycleService {
	return NewRESTLifecycleService(RESTLifecycleServiceDependencies{
		Roles:       fake,
		Sprints:     fake,
		Transitions: fake,
		Publisher:   fake,
	})
}

func assertRESTLifecycleTrace(t *testing.T, fake *restLifecycleFake, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(fake.trace, want) {
		t.Fatalf("trace = %v, want %v", fake.trace, want)
	}
}

func TestRESTLifecyclePrepareActivateAuthorization(t *testing.T) {
	const projectID int64 = 41
	const sprintID int64 = 907
	const actorID int64 = 13

	t.Run("missing actor", func(t *testing.T) {
		fake := &restLifecycleFake{role: store.RoleMaintainer}
		prepared, err := newRESTLifecycleService(fake).PrepareActivate(context.Background(), TransitionTarget{ProjectID: projectID, SprintID: sprintID})
		if !errors.Is(err, ErrActorRequired) || prepared != nil {
			t.Fatalf("PrepareActivate() = (%v, %v), want (nil, ErrActorRequired)", prepared, err)
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
			prepared, err := newRESTLifecycleService(fake).PrepareActivate(ctx, TransitionTarget{ProjectID: projectID, SprintID: sprintID})
			if tc.wantErr {
				if !errors.Is(err, ErrMaintainerRequired) || prepared != nil {
					t.Fatalf("PrepareActivate() = (%v, %v), want (nil, ErrMaintainerRequired)", prepared, err)
				}
			} else if err != nil || prepared == nil {
				t.Fatalf("PrepareActivate() = (%v, %v), want prepared capability", prepared, err)
			}
			assertRESTLifecycleTrace(t, fake, "role")
			if len(fake.roles) != 1 {
				t.Fatalf("role calls = %d, want 1", len(fake.roles))
			}
			call := fake.roles[0]
			if call.ctx != ctx || call.projectID != projectID || call.userID != actorID {
				t.Fatalf("role call = %+v, want exact context, project %d, actor %d", call, projectID, actorID)
			}
			if len(fake.reads) != 0 || len(fake.activates) != 0 || len(fake.activated) != 0 {
				t.Fatalf("preparation performed later work: reads=%d activates=%d publications=%d", len(fake.reads), len(fake.activates), len(fake.activated))
			}
		})
	}

	t.Run("role cancellation is collapsed", func(t *testing.T) {
		base, cancel := context.WithCancel(context.Background())
		ctx := store.WithUserID(base, actorID)
		cancel()
		fake := &restLifecycleFake{role: store.RoleMaintainer, honorContext: true}
		prepared, err := newRESTLifecycleService(fake).PrepareActivate(ctx, TransitionTarget{ProjectID: projectID, SprintID: sprintID})
		if !errors.Is(err, ErrMaintainerRequired) || prepared != nil {
			t.Fatalf("PrepareActivate() = (%v, %v), want (nil, ErrMaintainerRequired)", prepared, err)
		}
		assertRESTLifecycleTrace(t, fake, "role")
		if len(fake.roles) != 1 || fake.roles[0].ctx != ctx {
			t.Fatalf("role calls = %+v, want cancelled bound context", fake.roles)
		}
	})
}

func TestRESTLifecyclePrepareCloseTargetGateAndBinding(t *testing.T) {
	const actorID int64 = 17
	target := TransitionTarget{ProjectID: 43, SprintID: 911}
	ctx := store.WithUserID(context.Background(), actorID)
	fake := &restLifecycleFake{
		role:   store.RoleMaintainer,
		sprint: store.Sprint{ID: target.SprintID, ProjectID: target.ProjectID, Name: "Sprint 12"},
	}
	prepared, err := newRESTLifecycleService(fake).PrepareClose(ctx, target)
	if err != nil {
		t.Fatalf("PrepareClose() error = %v", err)
	}
	if prepared == nil {
		t.Fatal("PrepareClose() returned nil capability")
	}
	assertRESTLifecycleTrace(t, fake, "role", "read")
	if len(fake.roles) != 1 || fake.roles[0].ctx != ctx || fake.roles[0].projectID != target.ProjectID || fake.roles[0].userID != actorID {
		t.Fatalf("role calls = %+v, want exact bound authorization call", fake.roles)
	}
	if len(fake.reads) != 1 || fake.reads[0].ctx != ctx || fake.reads[0].sprintID != target.SprintID {
		t.Fatalf("read calls = %+v, want exact bound target read", fake.reads)
	}

	original := target
	target.ProjectID = 99
	target.SprintID = 1001
	if err := prepared.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	assertRESTLifecycleTrace(t, fake, "role", "read", "close", "publish_closed")
	if len(fake.closes) != 1 || fake.closes[0].ctx != ctx || fake.closes[0].projectID != original.ProjectID || fake.closes[0].sprintID != original.SprintID {
		t.Fatalf("close calls = %+v, want bound project %d sprint %d", fake.closes, original.ProjectID, original.SprintID)
	}
	if len(fake.closed) != 1 || fake.closed[0].ctx != ctx || fake.closed[0].projectID != original.ProjectID || fake.closed[0].name != "Sprint 12" {
		t.Fatalf("close publications = %+v, want bound context, project %d, name Sprint 12", fake.closed, original.ProjectID)
	}
}

func TestRESTLifecyclePrepareCloseFailures(t *testing.T) {
	const projectID int64 = 43
	const sprintID int64 = 911
	ctx := store.WithUserID(context.Background(), 17)
	readErr := errors.New("read failed")
	roleErr := errors.New("role failed")

	t.Run("missing actor short circuits authorization", func(t *testing.T) {
		fake := &restLifecycleFake{role: store.RoleMaintainer}
		prepared, err := newRESTLifecycleService(fake).PrepareClose(context.Background(), TransitionTarget{ProjectID: projectID, SprintID: sprintID})
		if !errors.Is(err, ErrActorRequired) || prepared != nil {
			t.Fatalf("PrepareClose() = (%v, %v), want (nil, ErrActorRequired)", prepared, err)
		}
		assertRESTLifecycleTrace(t, fake)
	})

	for _, tc := range []struct {
		name      string
		fake      *restLifecycleFake
		wantErr   error
		wantTrace []string
	}{
		{
			name:      "authorization short circuits read",
			fake:      &restLifecycleFake{roleErr: roleErr},
			wantErr:   ErrMaintainerRequired,
			wantTrace: []string{"role"},
		},
		{
			name:      "read error passes through",
			fake:      &restLifecycleFake{role: store.RoleMaintainer, sprintErr: readErr},
			wantErr:   readErr,
			wantTrace: []string{"role", "read"},
		},
		{
			name:      "foreign project is classified",
			fake:      &restLifecycleFake{role: store.RoleMaintainer, sprint: store.Sprint{ID: sprintID, ProjectID: projectID + 1}},
			wantErr:   ErrSprintNotInProject,
			wantTrace: []string{"role", "read"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prepared, err := newRESTLifecycleService(tc.fake).PrepareClose(ctx, TransitionTarget{ProjectID: projectID, SprintID: sprintID})
			if !errors.Is(err, tc.wantErr) || prepared != nil {
				t.Fatalf("PrepareClose() = (%v, %v), want (nil, %v)", prepared, err, tc.wantErr)
			}
			if tc.wantErr == readErr && err != readErr {
				t.Fatalf("PrepareClose() wrapped read error: got %v, want identical %v", err, readErr)
			}
			assertRESTLifecycleTrace(t, tc.fake, tc.wantTrace...)
			if len(tc.fake.closes) != 0 || len(tc.fake.closed) != 0 {
				t.Fatalf("failed preparation performed operation: closes=%d publications=%d", len(tc.fake.closes), len(tc.fake.closed))
			}
		})
	}
}

func TestRESTLifecyclePrepareActivateBindsTargetAndPublishesAfterPersistence(t *testing.T) {
	target := TransitionTarget{ProjectID: 47, SprintID: 919}
	original := target
	ctx := store.WithUserID(context.Background(), 19)
	fake := &restLifecycleFake{role: store.RoleMaintainer}
	prepared, err := newRESTLifecycleService(fake).PrepareActivate(ctx, target)
	if err != nil {
		t.Fatalf("PrepareActivate() error = %v", err)
	}
	target.ProjectID = 101
	target.SprintID = 103

	if err := prepared.Activate(); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	assertRESTLifecycleTrace(t, fake, "role", "activate", "publish_activated")
	if len(fake.activates) != 1 || fake.activates[0].ctx != ctx || fake.activates[0].projectID != original.ProjectID || fake.activates[0].sprintID != original.SprintID {
		t.Fatalf("activate calls = %+v, want exact bound context, project %d, sprint %d", fake.activates, original.ProjectID, original.SprintID)
	}
	if len(fake.activated) != 1 || fake.activated[0].ctx != ctx || fake.activated[0].projectID != original.ProjectID {
		t.Fatalf("activation publications = %+v, want exact bound context and project %d", fake.activated, original.ProjectID)
	}
	if len(fake.reads) != 0 || len(fake.closed) != 0 || len(fake.deleted) != 0 {
		t.Fatalf("activation used unrelated capabilities: reads=%d closed=%d deleted=%d", len(fake.reads), len(fake.closed), len(fake.deleted))
	}
}

func TestRESTLifecycleRepeatedActivationSuccessStillPublishes(t *testing.T) {
	ctx := store.WithUserID(context.Background(), 23)
	fake := &restLifecycleFake{role: store.RoleMaintainer}
	prepared, err := newRESTLifecycleService(fake).PrepareActivate(ctx, TransitionTarget{ProjectID: 53, SprintID: 929})
	if err != nil {
		t.Fatalf("PrepareActivate() error = %v", err)
	}

	// The fake's nil result models the store's successful idempotent return. The
	// service has no state input and must treat every successful return alike.
	if err := prepared.Activate(); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	assertRESTLifecycleTrace(t, fake, "role", "activate", "publish_activated")
	if len(fake.activates) != 1 || len(fake.activated) != 1 {
		t.Fatalf("calls = activate %d publish %d, want 1 and 1", len(fake.activates), len(fake.activated))
	}
}

func TestRESTLifecycleMutationFailuresPassThroughWithoutPublication(t *testing.T) {
	activateErr := errors.New("activate failed")
	closeErr := errors.New("close failed")

	t.Run("activate", func(t *testing.T) {
		fake := &restLifecycleFake{role: store.RoleMaintainer, activateErr: activateErr}
		prepared, err := newRESTLifecycleService(fake).PrepareActivate(store.WithUserID(context.Background(), 29), TransitionTarget{ProjectID: 59, SprintID: 937})
		if err != nil {
			t.Fatalf("PrepareActivate() error = %v", err)
		}
		err = prepared.Activate()
		if err != activateErr {
			t.Fatalf("Activate() error = %v, want %v", err, activateErr)
		}
		assertRESTLifecycleTrace(t, fake, "role", "activate")
		if len(fake.activates) != 1 || len(fake.activated) != 0 {
			t.Fatalf("calls = activate %d publish %d, want 1 and 0", len(fake.activates), len(fake.activated))
		}
	})

	t.Run("close", func(t *testing.T) {
		fake := &restLifecycleFake{
			role:     store.RoleMaintainer,
			sprint:   store.Sprint{ID: 941, ProjectID: 61},
			closeErr: closeErr,
		}
		prepared, err := newRESTLifecycleService(fake).PrepareClose(store.WithUserID(context.Background(), 31), TransitionTarget{ProjectID: 61, SprintID: 941})
		if err != nil {
			t.Fatalf("PrepareClose() error = %v", err)
		}
		err = prepared.Close()
		if err != closeErr {
			t.Fatalf("Close() error = %v, want %v", err, closeErr)
		}
		assertRESTLifecycleTrace(t, fake, "role", "read", "close")
		if len(fake.closes) != 1 || len(fake.closed) != 0 {
			t.Fatalf("calls = close %d publish %d, want 1 and 0", len(fake.closes), len(fake.closed))
		}
	})
}

func TestRESTLifecycleCancellationAfterPreparationUsesBoundContext(t *testing.T) {
	t.Run("activate", func(t *testing.T) {
		base, cancel := context.WithCancel(context.Background())
		ctx := store.WithUserID(base, 37)
		fake := &restLifecycleFake{role: store.RoleMaintainer, honorContext: true}
		prepared, err := newRESTLifecycleService(fake).PrepareActivate(ctx, TransitionTarget{ProjectID: 67, SprintID: 947})
		if err != nil {
			t.Fatalf("PrepareActivate() error = %v", err)
		}
		cancel()
		if err := prepared.Activate(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Activate() error = %v, want context.Canceled", err)
		}
		assertRESTLifecycleTrace(t, fake, "role", "activate")
		if len(fake.activates) != 1 || fake.activates[0].ctx != ctx || len(fake.activated) != 0 {
			t.Fatalf("cancellation calls = %+v publications=%d", fake.activates, len(fake.activated))
		}
	})

	t.Run("close", func(t *testing.T) {
		base, cancel := context.WithCancel(context.Background())
		ctx := store.WithUserID(base, 41)
		fake := &restLifecycleFake{
			role:         store.RoleMaintainer,
			sprint:       store.Sprint{ID: 953, ProjectID: 71},
			honorContext: true,
		}
		prepared, err := newRESTLifecycleService(fake).PrepareClose(ctx, TransitionTarget{ProjectID: 71, SprintID: 953})
		if err != nil {
			t.Fatalf("PrepareClose() error = %v", err)
		}
		cancel()
		if err := prepared.Close(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Close() error = %v, want context.Canceled", err)
		}
		assertRESTLifecycleTrace(t, fake, "role", "read", "close")
		if len(fake.reads) != 1 || len(fake.closes) != 1 || fake.closes[0].ctx != ctx || len(fake.closed) != 0 {
			t.Fatalf("cancellation calls = reads %d closes %+v publications=%d", len(fake.reads), fake.closes, len(fake.closed))
		}
	})
}

func TestRESTLifecycleNilPublisherIsNoop(t *testing.T) {
	t.Run("activate", func(t *testing.T) {
		fake := &restLifecycleFake{role: store.RoleMaintainer}
		service := NewRESTLifecycleService(RESTLifecycleServiceDependencies{Roles: fake, Sprints: fake, Transitions: fake})
		prepared, err := service.PrepareActivate(store.WithUserID(context.Background(), 43), TransitionTarget{ProjectID: 73, SprintID: 967})
		if err != nil {
			t.Fatalf("PrepareActivate() error = %v", err)
		}
		if err := prepared.Activate(); err != nil {
			t.Fatalf("Activate() error = %v", err)
		}
		assertRESTLifecycleTrace(t, fake, "role", "activate")
	})

	t.Run("close", func(t *testing.T) {
		fake := &restLifecycleFake{role: store.RoleMaintainer, sprint: store.Sprint{ID: 971, ProjectID: 79}}
		service := NewRESTLifecycleService(RESTLifecycleServiceDependencies{Roles: fake, Sprints: fake, Transitions: fake})
		prepared, err := service.PrepareClose(store.WithUserID(context.Background(), 47), TransitionTarget{ProjectID: 79, SprintID: 971})
		if err != nil {
			t.Fatalf("PrepareClose() error = %v", err)
		}
		if err := prepared.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		assertRESTLifecycleTrace(t, fake, "role", "read", "close")
	})
}
