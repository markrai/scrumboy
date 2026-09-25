package useradmin

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

type restRoleContextKey struct{}

type restRoleMutationFake struct {
	trace       *[]string
	err         error
	calls       int
	requesterID int64
	targetID    int64
	role        store.SystemRole
	marker      any
}

func (f *restRoleMutationFake) UpdateUserRole(
	ctx context.Context,
	requesterID int64,
	targetUserID int64,
	newRole store.SystemRole,
) error {
	*f.trace = append(*f.trace, "update")
	f.calls++
	f.requesterID = requesterID
	f.targetID = targetUserID
	f.role = newRole
	f.marker = ctx.Value(restRoleContextKey{})
	return f.err
}

type restRoleReadFake struct {
	trace  *[]string
	user   store.User
	err    error
	calls  int
	userID int64
	marker any
}

func (f *restRoleReadFake) GetUser(ctx context.Context, userID int64) (store.User, error) {
	*f.trace = append(*f.trace, "projection-read")
	f.calls++
	f.userID = userID
	f.marker = ctx.Value(restRoleContextKey{})
	return f.user, f.err
}

func newRESTRoleTestService(
	trace *[]string,
) (*RESTRoleService, *restRoleMutationFake, *restRoleReadFake) {
	mutations := &restRoleMutationFake{trace: trace}
	projection := &restRoleReadFake{trace: trace}
	service := NewRESTRoleService(RESTRoleServiceDependencies{
		Mutations:      mutations,
		ProjectionRead: projection,
	})
	return service, mutations, projection
}

func TestRESTRoleServicePrepareRequiresActor(t *testing.T) {
	trace := []string{}
	service, mutations, projection := newRESTRoleTestService(&trace)

	prepared, err := service.Prepare(context.Background(), RoleChangeCommand{
		TargetUserID: 41,
		NewRole:      store.SystemRoleAdmin,
	})
	if prepared != nil || !errors.Is(err, ErrActorRequired) {
		t.Fatalf("Prepare() = (%v, %v), want (nil, ErrActorRequired)", prepared, err)
	}
	if mutations.calls != 0 || projection.calls != 0 || len(trace) != 0 {
		t.Fatalf("missing actor reached dependencies: trace=%v", trace)
	}
}

func TestRESTRoleServiceSuccessOrderAndResult(t *testing.T) {
	trace := []string{}
	service, mutations, projection := newRESTRoleTestService(&trace)
	image := "data:image/png;base64,rest"
	want := store.User{
		ID:         41,
		Email:      "rest-result@example.com",
		Name:       "REST Result",
		Image:      &image,
		SystemRole: store.SystemRoleAdmin,
	}
	projection.user = want
	ctx := store.WithUserID(
		context.WithValue(context.Background(), restRoleContextKey{}, "rest-context"),
		17,
	)

	prepared, err := service.Prepare(ctx, RoleChangeCommand{
		TargetUserID: 41,
		NewRole:      store.SystemRoleAdmin,
	})
	if err != nil {
		t.Fatalf("Prepare(): %v", err)
	}
	got, err := prepared.Update()
	if err != nil {
		t.Fatalf("Update(): %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Update() user = %+v, want %+v", got, want)
	}
	if wantTrace := []string{"update", "projection-read"}; !reflect.DeepEqual(trace, wantTrace) {
		t.Fatalf("trace = %v, want %v", trace, wantTrace)
	}
	if mutations.calls != 1 || projection.calls != 1 {
		t.Fatalf("calls mutation/projection = %d/%d, want 1/1", mutations.calls, projection.calls)
	}
	if mutations.requesterID != 17 || mutations.targetID != 41 || mutations.role != store.SystemRoleAdmin {
		t.Fatalf("mutation args = requester:%d target:%d role:%q", mutations.requesterID, mutations.targetID, mutations.role)
	}
	if projection.userID != 41 || mutations.marker != "rest-context" || projection.marker != "rest-context" {
		t.Fatalf("projection/context = id:%d mutation-marker:%v projection-marker:%v", projection.userID, mutations.marker, projection.marker)
	}
}

func TestRESTRoleServiceMutationFailureStopsProjection(t *testing.T) {
	trace := []string{}
	service, mutations, projection := newRESTRoleTestService(&trace)
	wantErr := errors.New("REST mutation failed")
	mutations.err = wantErr
	ctx := store.WithUserID(context.Background(), 17)

	prepared, err := service.Prepare(ctx, RoleChangeCommand{TargetUserID: 41, NewRole: store.SystemRoleAdmin})
	if err != nil {
		t.Fatalf("Prepare(): %v", err)
	}
	if _, err := prepared.Update(); err != wantErr {
		t.Fatalf("Update() error = %v, want original %v", err, wantErr)
	}
	if mutations.calls != 1 || projection.calls != 0 || !reflect.DeepEqual(trace, []string{"update"}) {
		t.Fatalf("failure sequence calls=%d/%d trace=%v", mutations.calls, projection.calls, trace)
	}
}

func TestRESTRoleServicePostReadFailurePreservesCommittedSequence(t *testing.T) {
	trace := []string{}
	service, mutations, projection := newRESTRoleTestService(&trace)
	wantErr := errors.New("REST projection failed")
	projection.err = wantErr
	ctx := store.WithUserID(context.Background(), 17)

	prepared, err := service.Prepare(ctx, RoleChangeCommand{TargetUserID: 41, NewRole: store.SystemRoleAdmin})
	if err != nil {
		t.Fatalf("Prepare(): %v", err)
	}
	if _, err := prepared.Update(); err != wantErr {
		t.Fatalf("Update() error = %v, want original %v", err, wantErr)
	}
	if mutations.calls != 1 || projection.calls != 1 || !reflect.DeepEqual(trace, []string{"update", "projection-read"}) {
		t.Fatalf("post-read failure calls=%d/%d trace=%v", mutations.calls, projection.calls, trace)
	}
}

func TestRESTRoleServicePreparedUpdateExecutesMutationAtMostOnce(t *testing.T) {
	tests := []struct {
		name                string
		mutationErr         error
		projectionErr       error
		wantFirstErr        error
		wantProjectionCalls int
	}{
		{name: "successful mutation and projection", wantProjectionCalls: 1},
		{
			name:                "successful mutation and failed projection",
			projectionErr:       errors.New("REST projection failed after commit"),
			wantProjectionCalls: 1,
		},
		{
			name:                "mutation failure",
			mutationErr:         errors.New("REST mutation failed before projection"),
			wantProjectionCalls: 0,
		},
	}
	for index := range tests {
		test := &tests[index]
		if test.projectionErr != nil {
			test.wantFirstErr = test.projectionErr
		}
		if test.mutationErr != nil {
			test.wantFirstErr = test.mutationErr
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trace := []string{}
			service, mutations, projection := newRESTRoleTestService(&trace)
			mutations.err = tt.mutationErr
			projection.err = tt.projectionErr
			projection.user = store.User{ID: 41, SystemRole: store.SystemRoleAdmin}

			prepared, err := service.Prepare(
				store.WithUserID(context.Background(), 17),
				RoleChangeCommand{TargetUserID: 41, NewRole: store.SystemRoleAdmin},
			)
			if err != nil {
				t.Fatalf("Prepare(): %v", err)
			}

			firstUser, firstErr := prepared.Update()
			if firstErr != tt.wantFirstErr {
				t.Fatalf("first Update() error = %v, want %v", firstErr, tt.wantFirstErr)
			}
			if tt.wantFirstErr == nil && firstUser.ID != 41 {
				t.Fatalf("first Update() user = %+v, want projected user", firstUser)
			}
			traceAfterFirst := append([]string(nil), trace...)

			secondUser, secondErr := prepared.Update()
			if secondUser != (store.User{}) || !errors.Is(secondErr, ErrPreparedMutationAlreadyExecuted) {
				t.Fatalf("second Update() = (%+v, %v), want zero user and ErrPreparedMutationAlreadyExecuted", secondUser, secondErr)
			}
			if mutations.calls != 1 || projection.calls != tt.wantProjectionCalls {
				t.Fatalf("calls after repeat mutation/projection = %d/%d, want 1/%d", mutations.calls, projection.calls, tt.wantProjectionCalls)
			}
			if !reflect.DeepEqual(trace, traceAfterFirst) {
				t.Fatalf("second Update() changed trace: before=%v after=%v", traceAfterFirst, trace)
			}
		})
	}
}

func TestRESTRoleServicePreparedValuesAndContextAreOwned(t *testing.T) {
	tests := []struct {
		name   string
		target int64
		role   store.SystemRole
	}{
		{name: "owner role", target: 52, role: store.SystemRoleOwner},
		{name: "zero target", target: 0, role: store.SystemRoleAdmin},
		{name: "negative target and unknown role", target: -53, role: store.SystemRole("unknown")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trace := []string{}
			service, mutations, projection := newRESTRoleTestService(&trace)
			ctx := store.WithUserID(
				context.WithValue(context.Background(), restRoleContextKey{}, tt.name),
				19,
			)
			command := RoleChangeCommand{TargetUserID: tt.target, NewRole: tt.role}

			prepared, err := service.Prepare(ctx, command)
			if err != nil {
				t.Fatalf("Prepare(): %v", err)
			}
			command.TargetUserID = 999
			command.NewRole = store.SystemRoleUser
			ctx = context.Background()

			if _, err := prepared.Update(); err != nil {
				t.Fatalf("Update(): %v", err)
			}
			if mutations.requesterID != 19 || mutations.targetID != tt.target || mutations.role != tt.role {
				t.Fatalf("bound mutation = requester:%d target:%d role:%q", mutations.requesterID, mutations.targetID, mutations.role)
			}
			if projection.userID != tt.target || mutations.marker != tt.name || projection.marker != tt.name {
				t.Fatalf("bound projection/context = id:%d mutation-marker:%v projection-marker:%v", projection.userID, mutations.marker, projection.marker)
			}
		})
	}
}
