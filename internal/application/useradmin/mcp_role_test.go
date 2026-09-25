package useradmin

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

type mcpRoleContextKey struct{}

type mcpRoleReadFake struct {
	label  string
	trace  *[]string
	user   store.User
	err    error
	calls  int
	userID int64
	marker any
}

func (f *mcpRoleReadFake) GetUser(ctx context.Context, userID int64) (store.User, error) {
	*f.trace = append(*f.trace, f.label)
	f.calls++
	f.userID = userID
	f.marker = ctx.Value(mcpRoleContextKey{})
	return f.user, f.err
}

type mcpRoleMutationFake struct {
	trace       *[]string
	err         error
	calls       int
	requesterID int64
	targetID    int64
	role        store.SystemRole
	marker      any
}

func (f *mcpRoleMutationFake) UpdateUserRole(
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
	f.marker = ctx.Value(mcpRoleContextKey{})
	return f.err
}

func newMCPRoleTestService(
	trace *[]string,
) (*MCPRoleService, *mcpRoleReadFake, *mcpRoleMutationFake, *mcpRoleReadFake) {
	requester := &mcpRoleReadFake{
		label: "requester-read",
		trace: trace,
		user:  store.User{ID: 23, SystemRole: store.SystemRoleOwner},
	}
	mutations := &mcpRoleMutationFake{trace: trace}
	projection := &mcpRoleReadFake{label: "projection-read", trace: trace}
	service := NewMCPRoleService(MCPRoleServiceDependencies{
		RequesterRead:  requester,
		Mutations:      mutations,
		ProjectionRead: projection,
	})
	return service, requester, mutations, projection
}

func TestMCPRoleServicePrepareRequiresActor(t *testing.T) {
	trace := []string{}
	service, requester, mutations, projection := newMCPRoleTestService(&trace)

	prepared, err := service.Prepare(context.Background(), RoleChangeCommand{
		TargetUserID: 61,
		NewRole:      store.SystemRoleAdmin,
	})
	if prepared != nil || !errors.Is(err, ErrActorRequired) {
		t.Fatalf("Prepare() = (%v, %v), want (nil, ErrActorRequired)", prepared, err)
	}
	if requester.calls != 0 || mutations.calls != 0 || projection.calls != 0 || len(trace) != 0 {
		t.Fatalf("missing actor reached dependencies: trace=%v", trace)
	}
}

func TestMCPRoleServiceRequesterReadFailure(t *testing.T) {
	trace := []string{}
	service, requester, mutations, projection := newMCPRoleTestService(&trace)
	wantErr := errors.New("requester read failed")
	requester.err = wantErr

	prepared, err := service.Prepare(
		store.WithUserID(context.Background(), 23),
		RoleChangeCommand{TargetUserID: 61, NewRole: store.SystemRoleAdmin},
	)
	if prepared != nil || err != wantErr {
		t.Fatalf("Prepare() = (%v, %v), want (nil, original error)", prepared, err)
	}
	if requester.calls != 1 || mutations.calls != 0 || projection.calls != 0 || !reflect.DeepEqual(trace, []string{"requester-read"}) {
		t.Fatalf("requester failure calls=%d/%d/%d trace=%v", requester.calls, mutations.calls, projection.calls, trace)
	}
}

func TestMCPRoleServiceRequiresExactOwner(t *testing.T) {
	for _, role := range []store.SystemRole{store.SystemRoleUser, store.SystemRoleAdmin} {
		t.Run(role.String(), func(t *testing.T) {
			trace := []string{}
			service, requester, mutations, projection := newMCPRoleTestService(&trace)
			requester.user.SystemRole = role

			prepared, err := service.Prepare(
				store.WithUserID(context.Background(), 23),
				RoleChangeCommand{TargetUserID: 61, NewRole: store.SystemRoleAdmin},
			)
			if prepared != nil || !errors.Is(err, ErrOwnerRequired) {
				t.Fatalf("Prepare() = (%v, %v), want (nil, ErrOwnerRequired)", prepared, err)
			}
			if requester.calls != 1 || mutations.calls != 0 || projection.calls != 0 || !reflect.DeepEqual(trace, []string{"requester-read"}) {
				t.Fatalf("role gate calls=%d/%d/%d trace=%v", requester.calls, mutations.calls, projection.calls, trace)
			}
		})
	}
}

func TestMCPRoleServiceSuccessOrderAndResult(t *testing.T) {
	trace := []string{}
	service, requester, mutations, projection := newMCPRoleTestService(&trace)
	image := "data:image/png;base64,mcp"
	want := store.User{
		ID:         61,
		Email:      "mcp-result@example.com",
		Name:       "MCP Result",
		Image:      &image,
		SystemRole: store.SystemRoleAdmin,
	}
	projection.user = want
	ctx := store.WithUserID(
		context.WithValue(context.Background(), mcpRoleContextKey{}, "mcp-context"),
		23,
	)

	prepared, err := service.Prepare(ctx, RoleChangeCommand{
		TargetUserID: 61,
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
	wantTrace := []string{"requester-read", "update", "projection-read"}
	if !reflect.DeepEqual(trace, wantTrace) {
		t.Fatalf("trace = %v, want %v", trace, wantTrace)
	}
	if requester.calls != 1 || mutations.calls != 1 || projection.calls != 1 {
		t.Fatalf("calls requester/mutation/projection = %d/%d/%d, want 1/1/1", requester.calls, mutations.calls, projection.calls)
	}
	if requester.userID != 23 || mutations.requesterID != 23 || mutations.targetID != 61 || mutations.role != store.SystemRoleAdmin {
		t.Fatalf("bound IDs/role = requester-read:%d mutation-requester:%d target:%d role:%q", requester.userID, mutations.requesterID, mutations.targetID, mutations.role)
	}
	if projection.userID != 61 || requester.marker != "mcp-context" || mutations.marker != "mcp-context" || projection.marker != "mcp-context" {
		t.Fatalf("projection/context = id:%d requester-marker:%v mutation-marker:%v projection-marker:%v", projection.userID, requester.marker, mutations.marker, projection.marker)
	}
}

func TestMCPRoleServiceMutationFailureStopsProjection(t *testing.T) {
	trace := []string{}
	service, requester, mutations, projection := newMCPRoleTestService(&trace)
	wantErr := errors.New("MCP mutation failed")
	mutations.err = wantErr

	prepared, err := service.Prepare(
		store.WithUserID(context.Background(), 23),
		RoleChangeCommand{TargetUserID: 61, NewRole: store.SystemRoleAdmin},
	)
	if err != nil {
		t.Fatalf("Prepare(): %v", err)
	}
	if _, err := prepared.Update(); err != wantErr {
		t.Fatalf("Update() error = %v, want original %v", err, wantErr)
	}
	if requester.calls != 1 || mutations.calls != 1 || projection.calls != 0 || !reflect.DeepEqual(trace, []string{"requester-read", "update"}) {
		t.Fatalf("mutation failure calls=%d/%d/%d trace=%v", requester.calls, mutations.calls, projection.calls, trace)
	}
}

type mcpRoleTypedError struct {
	message string
}

func (e *mcpRoleTypedError) Error() string {
	return e.message
}

func TestMCPRoleServicePostReadFailureClassifiesStageAndPreservesCause(t *testing.T) {
	trace := []string{}
	service, requester, mutations, projection := newMCPRoleTestService(&trace)
	wantErr := &mcpRoleTypedError{message: "MCP projection failed"}
	projection.err = wantErr

	prepared, err := service.Prepare(
		store.WithUserID(context.Background(), 23),
		RoleChangeCommand{TargetUserID: 61, NewRole: store.SystemRoleAdmin},
	)
	if err != nil {
		t.Fatalf("Prepare(): %v", err)
	}
	_, err = prepared.Update()
	if !errors.Is(err, ErrMCPRoleProjectionFailed) {
		t.Fatalf("Update() error = %v, want ErrMCPRoleProjectionFailed", err)
	}
	if !errors.Is(err, wantErr) || errors.Unwrap(err) != wantErr {
		t.Fatalf("projection cause was not preserved: error=%v unwrap=%v", err, errors.Unwrap(err))
	}
	var typedErr *mcpRoleTypedError
	if !errors.As(err, &typedErr) || typedErr.message != wantErr.message {
		t.Fatalf("errors.As() = %v, want typed cause %v", typedErr, wantErr)
	}
	if err.Error() != wantErr.Error() {
		t.Fatalf("error text = %q, want unchanged %q", err.Error(), wantErr.Error())
	}
	if requester.calls != 1 || mutations.calls != 1 || projection.calls != 1 || !reflect.DeepEqual(trace, []string{"requester-read", "update", "projection-read"}) {
		t.Fatalf("projection failure calls=%d/%d/%d trace=%v", requester.calls, mutations.calls, projection.calls, trace)
	}
}

func TestMCPRoleServicePreparedUpdateExecutesMutationAtMostOnce(t *testing.T) {
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
			projectionErr:       errors.New("MCP projection failed after commit"),
			wantProjectionCalls: 1,
		},
		{
			name:                "mutation failure",
			mutationErr:         errors.New("MCP mutation failed before projection"),
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
			service, _, mutations, projection := newMCPRoleTestService(&trace)
			mutations.err = tt.mutationErr
			projection.err = tt.projectionErr
			projection.user = store.User{ID: 61, SystemRole: store.SystemRoleAdmin}

			prepared, err := service.Prepare(
				store.WithUserID(context.Background(), 23),
				RoleChangeCommand{TargetUserID: 61, NewRole: store.SystemRoleAdmin},
			)
			if err != nil {
				t.Fatalf("Prepare(): %v", err)
			}

			firstUser, firstErr := prepared.Update()
			switch {
			case tt.projectionErr != nil:
				if !errors.Is(firstErr, ErrMCPRoleProjectionFailed) || !errors.Is(firstErr, tt.projectionErr) {
					t.Fatalf("first Update() error = %v, want classified projection cause %v", firstErr, tt.projectionErr)
				}
			case firstErr != tt.wantFirstErr:
				t.Fatalf("first Update() error = %v, want %v", firstErr, tt.wantFirstErr)
			}
			if tt.wantFirstErr == nil && firstUser.ID != 61 {
				t.Fatalf("first Update() user = %+v, want projected user", firstUser)
			}
			traceAfterFirst := append([]string(nil), trace...)

			secondUser, secondErr := prepared.Update()
			if secondUser != (store.User{}) || !errors.Is(secondErr, ErrPreparedMutationAlreadyExecuted) {
				t.Fatalf("second Update() = (%+v, %v), want zero user and ErrPreparedMutationAlreadyExecuted", secondUser, secondErr)
			}
			if errors.Is(secondErr, ErrMCPRoleProjectionFailed) {
				t.Fatalf("repeat error was incorrectly classified as projection failure: %v", secondErr)
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

func TestMCPRoleServicePreparedValuesAndContextAreOwned(t *testing.T) {
	tests := []struct {
		name   string
		target int64
		role   store.SystemRole
	}{
		{name: "owner role", target: 72, role: store.SystemRoleOwner},
		{name: "zero target", target: 0, role: store.SystemRoleAdmin},
		{name: "negative target and unknown role", target: -73, role: store.SystemRole("unknown")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trace := []string{}
			service, requester, mutations, projection := newMCPRoleTestService(&trace)
			ctx := store.WithUserID(
				context.WithValue(context.Background(), mcpRoleContextKey{}, tt.name),
				23,
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
			if requester.userID != 23 || mutations.requesterID != 23 || mutations.targetID != tt.target || mutations.role != tt.role {
				t.Fatalf("bound IDs/role = requester-read:%d mutation-requester:%d target:%d role:%q", requester.userID, mutations.requesterID, mutations.targetID, mutations.role)
			}
			if projection.userID != tt.target || requester.marker != tt.name || mutations.marker != tt.name || projection.marker != tt.name {
				t.Fatalf("bound projection/context = id:%d requester-marker:%v mutation-marker:%v projection-marker:%v", projection.userID, requester.marker, mutations.marker, projection.marker)
			}
		})
	}
}
