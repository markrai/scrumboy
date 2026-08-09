package workflow

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

var _ RESTMutationRoleStore = (*store.Store)(nil)

type restMutationTestContextKey struct{}

type restMutationCall struct {
	operation string
	ctx       context.Context
	projectID int64
	key       string
	name      string
	color     string
}

type restMutationRefreshCall struct {
	ctx       context.Context
	projectID int64
	reason    string
}

type restMutationFake struct {
	trace []string

	role      store.ProjectRole
	roleErr   error
	roleCalls int
	roleCtx   context.Context
	rolePID   int64
	roleUID   int64

	mutationCalls   []restMutationCall
	createResult    store.WorkflowColumn
	createErr       error
	updateErr       error
	deleteErr       error
	honorContextErr bool

	refreshCalls []restMutationRefreshCall
}

func (f *restMutationFake) GetProjectRole(
	ctx context.Context,
	projectID int64,
	userID int64,
) (store.ProjectRole, error) {
	f.trace = append(f.trace, "role")
	f.roleCalls++
	f.roleCtx = ctx
	f.rolePID = projectID
	f.roleUID = userID
	return f.role, f.roleErr
}

func (f *restMutationFake) AddWorkflowColumn(
	ctx context.Context,
	projectID int64,
	name string,
) (store.WorkflowColumn, error) {
	f.trace = append(f.trace, "create")
	f.mutationCalls = append(f.mutationCalls, restMutationCall{
		operation: "create",
		ctx:       ctx,
		projectID: projectID,
		name:      name,
	})
	if f.honorContextErr && ctx.Err() != nil {
		return store.WorkflowColumn{}, ctx.Err()
	}
	return f.createResult, f.createErr
}

func (f *restMutationFake) UpdateWorkflowColumn(
	ctx context.Context,
	projectID int64,
	key string,
	name string,
	color string,
) error {
	f.trace = append(f.trace, "update")
	f.mutationCalls = append(f.mutationCalls, restMutationCall{
		operation: "update",
		ctx:       ctx,
		projectID: projectID,
		key:       key,
		name:      name,
		color:     color,
	})
	if f.honorContextErr && ctx.Err() != nil {
		return ctx.Err()
	}
	return f.updateErr
}

func (f *restMutationFake) DeleteWorkflowColumn(
	ctx context.Context,
	projectID int64,
	key string,
) error {
	f.trace = append(f.trace, "delete")
	f.mutationCalls = append(f.mutationCalls, restMutationCall{
		operation: "delete",
		ctx:       ctx,
		projectID: projectID,
		key:       key,
	})
	if f.honorContextErr && ctx.Err() != nil {
		return ctx.Err()
	}
	return f.deleteErr
}

func (f *restMutationFake) PublishBoardRefresh(
	ctx context.Context,
	projectID int64,
	reason string,
) {
	f.trace = append(f.trace, "refresh")
	f.refreshCalls = append(f.refreshCalls, restMutationRefreshCall{
		ctx:       ctx,
		projectID: projectID,
		reason:    reason,
	})
}

func newRESTMutationTestService(f *restMutationFake) *RESTMutationService {
	return NewRESTMutationService(RESTMutationServiceDependencies{
		Roles:     f,
		Mutations: f,
		Refresh:   f,
	})
}

func restMutationActorContext(userID int64, marker string) context.Context {
	ctx := context.WithValue(context.Background(), restMutationTestContextKey{}, marker)
	return store.WithUserID(ctx, userID)
}

func assertRESTMutationTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("call trace=%v want=%v", got, want)
	}
}

func TestRESTMutationPrepareAuthorization(t *testing.T) {
	roleFailure := errors.New("role lookup failed")
	tests := []struct {
		name          string
		withActor     bool
		role          store.ProjectRole
		roleErr       error
		wantErr       error
		wantPrepared  bool
		wantRoleCalls int
	}{
		{
			name:          "missing actor",
			wantErr:       ErrActorRequired,
			wantRoleCalls: 0,
		},
		{
			name:          "contributor",
			withActor:     true,
			role:          store.RoleContributor,
			wantErr:       ErrMaintainerRequired,
			wantRoleCalls: 1,
		},
		{
			name:          "viewer",
			withActor:     true,
			role:          store.RoleViewer,
			wantErr:       ErrMaintainerRequired,
			wantRoleCalls: 1,
		},
		{
			name:          "role lookup error",
			withActor:     true,
			roleErr:       roleFailure,
			wantErr:       ErrMaintainerRequired,
			wantRoleCalls: 1,
		},
		{
			name:          "maintainer",
			withActor:     true,
			role:          store.RoleMaintainer,
			wantPrepared:  true,
			wantRoleCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restMutationFake{role: tt.role, roleErr: tt.roleErr}
			service := newRESTMutationTestService(fake)
			ctx := context.WithValue(context.Background(), restMutationTestContextKey{}, tt.name)
			if tt.withActor {
				ctx = store.WithUserID(ctx, 41)
			}

			prepared, err := service.Prepare(ctx, ResolvedRESTMutationTarget{ProjectID: 73})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Prepare error=%v want=%v", err, tt.wantErr)
			}
			if (prepared != nil) != tt.wantPrepared {
				t.Fatalf("prepared=%v wantPresent=%v", prepared, tt.wantPrepared)
			}
			if fake.roleCalls != tt.wantRoleCalls {
				t.Fatalf("role calls=%d want=%d", fake.roleCalls, tt.wantRoleCalls)
			}
			if tt.wantRoleCalls == 1 {
				if fake.roleCtx != ctx || fake.rolePID != 73 || fake.roleUID != 41 {
					t.Fatalf("role call context/project/user mismatch: ctxSame=%v project=%d user=%d", fake.roleCtx == ctx, fake.rolePID, fake.roleUID)
				}
				assertRESTMutationTrace(t, fake.trace, "role")
			} else {
				assertRESTMutationTrace(t, fake.trace)
			}
			if len(fake.mutationCalls) != 0 || len(fake.refreshCalls) != 0 {
				t.Fatalf("preparation caused side effects: mutations=%+v refreshes=%+v", fake.mutationCalls, fake.refreshCalls)
			}
		})
	}
}

func TestPreparedRESTMutationBindsContextAndProjectIDByValue(t *testing.T) {
	fake := &restMutationFake{role: store.RoleMaintainer}
	service := newRESTMutationTestService(fake)
	ctx := restMutationActorContext(51, "bound")
	target := ResolvedRESTMutationTarget{ProjectID: 101}

	prepared, err := service.Prepare(ctx, target)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	target.ProjectID = 202

	if err := prepared.Delete(DeleteCommand{Key: "  review_queue  "}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(fake.mutationCalls) != 1 {
		t.Fatalf("mutation calls=%d want=1", len(fake.mutationCalls))
	}
	call := fake.mutationCalls[0]
	if call.operation != "delete" || call.projectID != 101 || call.key != "  review_queue  " || call.ctx != ctx {
		t.Fatalf("delete call=%+v", call)
	}
	if got := call.ctx.Value(restMutationTestContextKey{}); got != "bound" {
		t.Fatalf("bound context marker=%v want=bound", got)
	}
	if len(fake.refreshCalls) != 1 {
		t.Fatalf("refresh calls=%d want=1", len(fake.refreshCalls))
	}
	refresh := fake.refreshCalls[0]
	if refresh.ctx != ctx || refresh.projectID != 101 || refresh.reason != refreshReasonWorkflowColumnDeleted {
		t.Fatalf("refresh=%+v", refresh)
	}
	assertRESTMutationTrace(t, fake.trace, "role", "delete", "refresh")
}

func TestPreparedRESTMutationCreate(t *testing.T) {
	storeFailure := errors.New("create failed")
	wantColumn := store.WorkflowColumn{
		ID:        17,
		ProjectID: 71,
		Key:       "review_queue",
		Name:      "Review Queue",
		Color:     "#123456",
		Position:  4,
	}

	t.Run("success persists before refresh", func(t *testing.T) {
		fake := &restMutationFake{role: store.RoleMaintainer, createResult: wantColumn}
		service := newRESTMutationTestService(fake)
		ctx := restMutationActorContext(61, "create")
		prepared, err := service.Prepare(ctx, ResolvedRESTMutationTarget{ProjectID: 71})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		got, err := prepared.Create(CreateCommand{Name: "  Review Queue  "})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if !reflect.DeepEqual(got, wantColumn) {
			t.Fatalf("created column=%+v want=%+v", got, wantColumn)
		}
		if len(fake.mutationCalls) != 1 {
			t.Fatalf("create calls=%d want=1", len(fake.mutationCalls))
		}
		call := fake.mutationCalls[0]
		if call.operation != "create" || call.ctx != ctx || call.projectID != 71 || call.name != "  Review Queue  " {
			t.Fatalf("create call=%+v", call)
		}
		if len(fake.refreshCalls) != 1 {
			t.Fatalf("refresh calls=%d want=1", len(fake.refreshCalls))
		}
		refresh := fake.refreshCalls[0]
		if refresh.ctx != ctx || refresh.projectID != 71 || refresh.reason != refreshReasonWorkflowColumnAdded {
			t.Fatalf("refresh=%+v", refresh)
		}
		assertRESTMutationTrace(t, fake.trace, "role", "create", "refresh")
	})

	t.Run("store failure suppresses refresh", func(t *testing.T) {
		fake := &restMutationFake{role: store.RoleMaintainer, createErr: storeFailure}
		service := newRESTMutationTestService(fake)
		prepared, err := service.Prepare(restMutationActorContext(62, "create failure"), ResolvedRESTMutationTarget{ProjectID: 72})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		got, err := prepared.Create(CreateCommand{Name: "Review"})
		if err != storeFailure {
			t.Fatalf("Create error=%v want=%v", err, storeFailure)
		}
		if got != (store.WorkflowColumn{}) {
			t.Fatalf("created column=%+v want zero", got)
		}
		if len(fake.mutationCalls) != 1 || len(fake.refreshCalls) != 0 {
			t.Fatalf("calls: mutations=%d refreshes=%d", len(fake.mutationCalls), len(fake.refreshCalls))
		}
		assertRESTMutationTrace(t, fake.trace, "role", "create")
	})

	t.Run("nil publisher is a no-op", func(t *testing.T) {
		fake := &restMutationFake{role: store.RoleMaintainer, createResult: wantColumn}
		service := NewRESTMutationService(RESTMutationServiceDependencies{
			Roles:     fake,
			Mutations: fake,
		})
		prepared, err := service.Prepare(restMutationActorContext(63, "nil publisher"), ResolvedRESTMutationTarget{ProjectID: 73})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		got, err := prepared.Create(CreateCommand{Name: "Review"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if !reflect.DeepEqual(got, wantColumn) || len(fake.mutationCalls) != 1 {
			t.Fatalf("result=%+v mutations=%d", got, len(fake.mutationCalls))
		}
		assertRESTMutationTrace(t, fake.trace, "role", "create")
	})
}

func TestPreparedRESTMutationUpdate(t *testing.T) {
	storeFailure := errors.New("update failed")

	t.Run("success persists before refresh", func(t *testing.T) {
		fake := &restMutationFake{role: store.RoleMaintainer}
		service := newRESTMutationTestService(fake)
		ctx := restMutationActorContext(71, "update")
		prepared, err := service.Prepare(ctx, ResolvedRESTMutationTarget{ProjectID: 81})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		err = prepared.Update(UpdateCommand{
			Key:   "  review_queue  ",
			Name:  "  Ready for review  ",
			Color: "  #A1B2C3  ",
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if len(fake.mutationCalls) != 1 {
			t.Fatalf("update calls=%d want=1", len(fake.mutationCalls))
		}
		call := fake.mutationCalls[0]
		if call.operation != "update" || call.ctx != ctx || call.projectID != 81 ||
			call.key != "  review_queue  " || call.name != "  Ready for review  " || call.color != "  #A1B2C3  " {
			t.Fatalf("update call=%+v", call)
		}
		if len(fake.refreshCalls) != 1 {
			t.Fatalf("refresh calls=%d want=1", len(fake.refreshCalls))
		}
		refresh := fake.refreshCalls[0]
		if refresh.ctx != ctx || refresh.projectID != 81 || refresh.reason != refreshReasonWorkflowColumnUpdated {
			t.Fatalf("refresh=%+v", refresh)
		}
		assertRESTMutationTrace(t, fake.trace, "role", "update", "refresh")
	})

	t.Run("store failure suppresses refresh", func(t *testing.T) {
		fake := &restMutationFake{role: store.RoleMaintainer, updateErr: storeFailure}
		service := newRESTMutationTestService(fake)
		prepared, err := service.Prepare(restMutationActorContext(72, "update failure"), ResolvedRESTMutationTarget{ProjectID: 82})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		if err := prepared.Update(UpdateCommand{Key: "doing", Name: "Doing", Color: "#123456"}); err != storeFailure {
			t.Fatalf("Update error=%v want=%v", err, storeFailure)
		}
		if len(fake.mutationCalls) != 1 || len(fake.refreshCalls) != 0 {
			t.Fatalf("calls: mutations=%d refreshes=%d", len(fake.mutationCalls), len(fake.refreshCalls))
		}
		assertRESTMutationTrace(t, fake.trace, "role", "update")
	})
}

func TestPreparedRESTMutationDeleteFailureSuppressesRefresh(t *testing.T) {
	storeFailure := errors.New("delete failed")
	fake := &restMutationFake{role: store.RoleMaintainer, deleteErr: storeFailure}
	service := newRESTMutationTestService(fake)
	prepared, err := service.Prepare(restMutationActorContext(81, "delete failure"), ResolvedRESTMutationTarget{ProjectID: 91})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if err := prepared.Delete(DeleteCommand{Key: "review"}); err != storeFailure {
		t.Fatalf("Delete error=%v want=%v", err, storeFailure)
	}
	if len(fake.mutationCalls) != 1 || len(fake.refreshCalls) != 0 {
		t.Fatalf("calls: mutations=%d refreshes=%d", len(fake.mutationCalls), len(fake.refreshCalls))
	}
	assertRESTMutationTrace(t, fake.trace, "role", "delete")
}

func TestPreparedRESTMutationUsesCancelledBoundContext(t *testing.T) {
	fake := &restMutationFake{role: store.RoleMaintainer, honorContextErr: true}
	service := newRESTMutationTestService(fake)
	ctx, cancel := context.WithCancel(restMutationActorContext(91, "cancelled"))
	prepared, err := service.Prepare(ctx, ResolvedRESTMutationTarget{ProjectID: 111})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	cancel()

	_, err = prepared.Create(CreateCommand{Name: "Review"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Create error=%v want=%v", err, context.Canceled)
	}
	if len(fake.mutationCalls) != 1 || fake.mutationCalls[0].ctx != ctx {
		t.Fatalf("mutation calls=%+v", fake.mutationCalls)
	}
	if len(fake.refreshCalls) != 0 {
		t.Fatalf("refresh calls=%+v want none", fake.refreshCalls)
	}
	assertRESTMutationTrace(t, fake.trace, "role", "create")
}

func TestBoardRefreshPublisherFuncNilIsNoop(t *testing.T) {
	var publish BoardRefreshPublisherFunc
	publish.PublishBoardRefresh(context.Background(), 1, "ignored")
}
