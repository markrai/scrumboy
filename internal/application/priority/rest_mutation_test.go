package priority

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

var _ RESTMutationRoleStore = (*store.Store)(nil)

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

	mutationCalls []restMutationCall
	createResult  store.PriorityTier
	createErr     error
	updateErr     error
	deleteErr     error

	refreshCalls []restMutationRefreshCall
}

func (f *restMutationFake) GetProjectRole(ctx context.Context, projectID int64, userID int64) (store.ProjectRole, error) {
	f.trace = append(f.trace, "role")
	f.roleCalls++
	f.roleCtx = ctx
	f.rolePID = projectID
	f.roleUID = userID
	return f.role, f.roleErr
}

func (f *restMutationFake) AddPriorityTier(ctx context.Context, projectID int64, name string) (store.PriorityTier, error) {
	f.trace = append(f.trace, "create")
	f.mutationCalls = append(f.mutationCalls, restMutationCall{operation: "create", ctx: ctx, projectID: projectID, name: name})
	return f.createResult, f.createErr
}

func (f *restMutationFake) UpdatePriorityTier(ctx context.Context, projectID int64, key, name, color string) error {
	f.trace = append(f.trace, "update")
	f.mutationCalls = append(f.mutationCalls, restMutationCall{operation: "update", ctx: ctx, projectID: projectID, key: key, name: name, color: color})
	return f.updateErr
}

func (f *restMutationFake) DeletePriorityTier(ctx context.Context, projectID int64, key string) error {
	f.trace = append(f.trace, "delete")
	f.mutationCalls = append(f.mutationCalls, restMutationCall{operation: "delete", ctx: ctx, projectID: projectID, key: key})
	return f.deleteErr
}

func (f *restMutationFake) PublishBoardRefresh(ctx context.Context, projectID int64, reason string) {
	f.trace = append(f.trace, "refresh")
	f.refreshCalls = append(f.refreshCalls, restMutationRefreshCall{ctx: ctx, projectID: projectID, reason: reason})
}

func newRESTMutationTestService(f *restMutationFake) *RESTMutationService {
	return NewRESTMutationService(RESTMutationServiceDependencies{Roles: f, Mutations: f, Refresh: f})
}

func restMutationActorContext(userID int64) context.Context {
	return store.WithUserID(context.Background(), userID)
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
		name         string
		withActor    bool
		role         store.ProjectRole
		roleErr      error
		wantErr      error
		wantPrepared bool
	}{
		{name: "missing actor", wantErr: ErrActorRequired},
		{name: "contributor", withActor: true, role: store.RoleContributor, wantErr: ErrMaintainerRequired},
		{name: "role lookup error", withActor: true, roleErr: roleFailure, wantErr: ErrMaintainerRequired},
		{name: "maintainer", withActor: true, role: store.RoleMaintainer, wantPrepared: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restMutationFake{role: tt.role, roleErr: tt.roleErr}
			service := newRESTMutationTestService(fake)
			ctx := context.Background()
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
			if len(fake.mutationCalls) != 0 || len(fake.refreshCalls) != 0 {
				t.Fatalf("preparation caused side effects: mutations=%+v refreshes=%+v", fake.mutationCalls, fake.refreshCalls)
			}
		})
	}
}

func TestPreparedRESTMutationCreate(t *testing.T) {
	storeFailure := errors.New("create failed")
	wantTier := store.PriorityTier{ID: 17, ProjectID: 71, Key: "critical", Name: "Critical", Color: "#123456", Position: 4}

	t.Run("success persists before refresh", func(t *testing.T) {
		fake := &restMutationFake{role: store.RoleMaintainer, createResult: wantTier}
		service := newRESTMutationTestService(fake)
		ctx := restMutationActorContext(61)
		prepared, err := service.Prepare(ctx, ResolvedRESTMutationTarget{ProjectID: 71})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		got, err := prepared.Create(CreateCommand{Name: "Critical"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if !reflect.DeepEqual(got, wantTier) {
			t.Fatalf("created tier=%+v want=%+v", got, wantTier)
		}
		if len(fake.refreshCalls) != 1 || fake.refreshCalls[0].reason != refreshReasonPriorityTierAdded {
			t.Fatalf("refresh calls=%+v", fake.refreshCalls)
		}
		assertRESTMutationTrace(t, fake.trace, "role", "create", "refresh")
	})

	t.Run("store failure suppresses refresh", func(t *testing.T) {
		fake := &restMutationFake{role: store.RoleMaintainer, createErr: storeFailure}
		service := newRESTMutationTestService(fake)
		prepared, err := service.Prepare(restMutationActorContext(62), ResolvedRESTMutationTarget{ProjectID: 72})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		_, err = prepared.Create(CreateCommand{Name: "Critical"})
		if err != storeFailure {
			t.Fatalf("Create error=%v want=%v", err, storeFailure)
		}
		if len(fake.refreshCalls) != 0 {
			t.Fatalf("refresh calls=%+v want none", fake.refreshCalls)
		}
		assertRESTMutationTrace(t, fake.trace, "role", "create")
	})
}

func TestPreparedRESTMutationUpdate(t *testing.T) {
	storeFailure := errors.New("update failed")

	t.Run("success persists before refresh", func(t *testing.T) {
		fake := &restMutationFake{role: store.RoleMaintainer}
		service := newRESTMutationTestService(fake)
		ctx := restMutationActorContext(71)
		prepared, err := service.Prepare(ctx, ResolvedRESTMutationTarget{ProjectID: 81})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		if err := prepared.Update(UpdateCommand{Key: "low", Name: "Chill", Color: "#A1B2C3"}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if len(fake.refreshCalls) != 1 || fake.refreshCalls[0].reason != refreshReasonPriorityTierUpdated {
			t.Fatalf("refresh calls=%+v", fake.refreshCalls)
		}
		assertRESTMutationTrace(t, fake.trace, "role", "update", "refresh")
	})

	t.Run("store failure suppresses refresh", func(t *testing.T) {
		fake := &restMutationFake{role: store.RoleMaintainer, updateErr: storeFailure}
		service := newRESTMutationTestService(fake)
		prepared, err := service.Prepare(restMutationActorContext(72), ResolvedRESTMutationTarget{ProjectID: 82})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		if err := prepared.Update(UpdateCommand{Key: "low", Name: "Chill", Color: "#123456"}); err != storeFailure {
			t.Fatalf("Update error=%v want=%v", err, storeFailure)
		}
		if len(fake.refreshCalls) != 0 {
			t.Fatalf("refresh calls=%+v want none", fake.refreshCalls)
		}
		assertRESTMutationTrace(t, fake.trace, "role", "update")
	})
}

func TestPreparedRESTMutationDeleteFailureSuppressesRefresh(t *testing.T) {
	storeFailure := errors.New("delete failed")
	fake := &restMutationFake{role: store.RoleMaintainer, deleteErr: storeFailure}
	service := newRESTMutationTestService(fake)
	prepared, err := service.Prepare(restMutationActorContext(81), ResolvedRESTMutationTarget{ProjectID: 91})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if err := prepared.Delete(DeleteCommand{Key: "low"}); err != storeFailure {
		t.Fatalf("Delete error=%v want=%v", err, storeFailure)
	}
	if len(fake.refreshCalls) != 0 {
		t.Fatalf("refresh calls=%+v want none", fake.refreshCalls)
	}
	assertRESTMutationTrace(t, fake.trace, "role", "delete")
}

func TestBoardRefreshPublisherFuncNilIsNoop(t *testing.T) {
	var publish BoardRefreshPublisherFunc
	publish.PublishBoardRefresh(context.Background(), 1, "ignored")
}
