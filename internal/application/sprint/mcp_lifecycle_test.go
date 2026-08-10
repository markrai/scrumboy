package sprint

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/store"
)

var _ MCPAccessStore = (*store.Store)(nil)
var _ RoleStore = (*store.Store)(nil)
var _ SprintReadStore = (*store.Store)(nil)
var _ TransitionStore = (*store.Store)(nil)
var _ DeletionStore = (*store.Store)(nil)

type mcpLifecycleContextKey struct{}

type mcpLifecycleAccessCall struct {
	ctx  context.Context
	slug string
	mode store.Mode
}

type mcpLifecycleRoleCall struct {
	ctx       context.Context
	projectID int64
	userID    int64
}

type mcpLifecycleReadCall struct {
	ctx      context.Context
	sprintID int64
}

type mcpLifecycleMutationCall struct {
	ctx       context.Context
	projectID int64
	sprintID  int64
}

type mcpLifecycleFake struct {
	trace []string

	honorContext bool

	projectContext store.ProjectContext
	accessErr      error
	accessCalls    []mcpLifecycleAccessCall

	role      store.ProjectRole
	roleErr   error
	roleCalls []mcpLifecycleRoleCall

	readResults []store.Sprint
	readErrors  []error
	readCalls   []mcpLifecycleReadCall

	activateErr       error
	activateCalls     []mcpLifecycleMutationCall
	activateCommitted bool
	afterActivate     func()

	closeErr       error
	closeCalls     []mcpLifecycleMutationCall
	closeCommitted bool
	afterClose     func()

	deleteErr       error
	deleteCalls     []mcpLifecycleMutationCall
	deleteCommitted bool

	nowValue time.Time
	nowCalls int
}

var _ MCPAccessStore = (*mcpLifecycleFake)(nil)
var _ RoleStore = (*mcpLifecycleFake)(nil)
var _ SprintReadStore = (*mcpLifecycleFake)(nil)
var _ TransitionStore = (*mcpLifecycleFake)(nil)
var _ DeletionStore = (*mcpLifecycleFake)(nil)

func (f *mcpLifecycleFake) GetProjectContextBySlug(
	ctx context.Context,
	slug string,
	mode store.Mode,
) (store.ProjectContext, error) {
	f.trace = append(f.trace, "access")
	f.accessCalls = append(f.accessCalls, mcpLifecycleAccessCall{ctx: ctx, slug: slug, mode: mode})
	if f.honorContext && ctx.Err() != nil {
		return store.ProjectContext{}, ctx.Err()
	}
	if f.accessErr != nil {
		return store.ProjectContext{}, f.accessErr
	}
	return f.projectContext, nil
}

func (f *mcpLifecycleFake) GetProjectRole(
	ctx context.Context,
	projectID int64,
	userID int64,
) (store.ProjectRole, error) {
	f.trace = append(f.trace, "role")
	f.roleCalls = append(f.roleCalls, mcpLifecycleRoleCall{ctx: ctx, projectID: projectID, userID: userID})
	if f.honorContext && ctx.Err() != nil {
		return "", ctx.Err()
	}
	if f.roleErr != nil {
		return "", f.roleErr
	}
	return f.role, nil
}

func (f *mcpLifecycleFake) GetSprintByID(ctx context.Context, sprintID int64) (store.Sprint, error) {
	index := len(f.readCalls)
	stage := "target"
	if index > 0 {
		stage = "projection"
	}
	f.trace = append(f.trace, stage)
	f.readCalls = append(f.readCalls, mcpLifecycleReadCall{ctx: ctx, sprintID: sprintID})
	if f.honorContext && ctx.Err() != nil {
		return store.Sprint{}, ctx.Err()
	}
	if index < len(f.readErrors) && f.readErrors[index] != nil {
		return store.Sprint{}, f.readErrors[index]
	}
	if index < len(f.readResults) {
		return f.readResults[index], nil
	}
	return store.Sprint{}, nil
}

func (f *mcpLifecycleFake) ActivateSprint(ctx context.Context, projectID, sprintID int64) error {
	f.trace = append(f.trace, "activate")
	f.activateCalls = append(f.activateCalls, mcpLifecycleMutationCall{ctx: ctx, projectID: projectID, sprintID: sprintID})
	if f.honorContext && ctx.Err() != nil {
		return ctx.Err()
	}
	if f.activateErr != nil {
		return f.activateErr
	}
	f.activateCommitted = true
	if f.afterActivate != nil {
		f.afterActivate()
	}
	return nil
}

func (f *mcpLifecycleFake) CloseSprint(ctx context.Context, projectID, sprintID int64) error {
	f.trace = append(f.trace, "close")
	f.closeCalls = append(f.closeCalls, mcpLifecycleMutationCall{ctx: ctx, projectID: projectID, sprintID: sprintID})
	if f.honorContext && ctx.Err() != nil {
		return ctx.Err()
	}
	if f.closeErr != nil {
		return f.closeErr
	}
	f.closeCommitted = true
	if f.afterClose != nil {
		f.afterClose()
	}
	return nil
}

func (f *mcpLifecycleFake) DeleteSprint(ctx context.Context, projectID, sprintID int64) error {
	f.trace = append(f.trace, "delete")
	f.deleteCalls = append(f.deleteCalls, mcpLifecycleMutationCall{ctx: ctx, projectID: projectID, sprintID: sprintID})
	if f.honorContext && ctx.Err() != nil {
		return ctx.Err()
	}
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleteCommitted = true
	return nil
}

func (f *mcpLifecycleFake) Now() time.Time {
	f.trace = append(f.trace, "now")
	f.nowCalls++
	return f.nowValue
}

func newMCPLifecycleTestService(fake *mcpLifecycleFake) *MCPLifecycleService {
	return NewMCPLifecycleService(MCPLifecycleServiceDependencies{
		Access:      fake,
		Roles:       fake,
		Sprints:     fake,
		Transitions: fake,
		Now:         fake.Now,
	})
}

func newMCPDeletionTestService(fake *mcpLifecycleFake) *MCPDeletionService {
	return NewMCPDeletionService(MCPDeletionServiceDependencies{
		Access:    fake,
		Roles:     fake,
		Sprints:   fake,
		Deletions: fake,
	})
}

func newMCPLifecycleFake(state string) *mcpLifecycleFake {
	now := time.Date(2026, time.August, 8, 15, 0, 0, 0, time.UTC)
	return &mcpLifecycleFake{
		projectContext: store.ProjectContext{
			Project: store.Project{ID: 71, Slug: "resolved-slug", SprintsEnabled: true},
			Role:    store.RoleViewer,
		},
		role: store.RoleMaintainer,
		readResults: []store.Sprint{
			{
				ID:           907,
				ProjectID:    71,
				Name:         "Before mutation",
				State:        state,
				PlannedEndAt: now.Add(24 * time.Hour),
			},
			{
				ID:           907,
				ProjectID:    71,
				Name:         "After mutation",
				State:        state,
				PlannedEndAt: now.Add(24 * time.Hour),
			},
		},
		nowValue: now,
	}
}

func mcpLifecycleContext(userID int64, marker string) context.Context {
	ctx := context.WithValue(context.Background(), mcpLifecycleContextKey{}, marker)
	return store.WithUserID(ctx, userID)
}

func assertMCPLifecycleTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trace = %v, want %v", got, want)
	}
}

func assertZeroSprint(t *testing.T, got store.Sprint) {
	t.Helper()
	if !reflect.DeepEqual(got, store.Sprint{}) {
		t.Fatalf("sprint = %+v, want zero value", got)
	}
}

func TestMCPLifecycleServiceConstructionIsInertAndPublicationFree(t *testing.T) {
	fake := newMCPLifecycleFake(store.SprintStatePlanned)
	service := newMCPLifecycleTestService(fake)
	if service == nil {
		t.Fatal("NewMCPLifecycleService returned nil")
	}
	if len(fake.trace) != 0 {
		t.Fatalf("construction performed dependency work: %v", fake.trace)
	}

	depsType := reflect.TypeOf(MCPLifecycleServiceDependencies{})
	wantFields := []string{"Access", "Roles", "Sprints", "Transitions", "Now"}
	gotFields := make([]string, 0, depsType.NumField())
	for i := 0; i < depsType.NumField(); i++ {
		gotFields = append(gotFields, depsType.Field(i).Name)
	}
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("dependency fields = %v, want %v", gotFields, wantFields)
	}
	for _, field := range gotFields {
		name := strings.ToLower(field)
		if strings.Contains(name, "publish") || strings.Contains(name, "event") || strings.Contains(name, "fanout") {
			t.Fatalf("unexpected realtime dependency %q", field)
		}
	}
}

func TestMCPLifecyclePreparationAuthorization(t *testing.T) {
	accessErr := errors.New("private access failure")
	roleErr := errors.New("private role failure")
	type operation struct {
		name    string
		state   string
		prepare func(*MCPLifecycleService, *MCPDeletionService, context.Context) (bool, error)
		success []string
	}
	operations := []operation{
		{
			name:  "activate",
			state: store.SprintStatePlanned,
			prepare: func(lifecycle *MCPLifecycleService, _ *MCPDeletionService, ctx context.Context) (bool, error) {
				prepared, err := lifecycle.PrepareActivate(ctx, MCPLifecycleTarget{ProjectSlug: " requested ", SprintID: 907, Mode: store.ModeFull})
				return prepared != nil, err
			},
			success: []string{"access", "role", "target", "now"},
		},
		{
			name:  "close",
			state: store.SprintStateActive,
			prepare: func(lifecycle *MCPLifecycleService, _ *MCPDeletionService, ctx context.Context) (bool, error) {
				prepared, err := lifecycle.PrepareClose(ctx, MCPLifecycleTarget{ProjectSlug: " requested ", SprintID: 907, Mode: store.ModeFull})
				return prepared != nil, err
			},
			success: []string{"access", "role", "target"},
		},
		{
			name:  "delete",
			state: store.SprintStateClosed,
			prepare: func(_ *MCPLifecycleService, deletion *MCPDeletionService, ctx context.Context) (bool, error) {
				prepared, err := deletion.PrepareDelete(ctx, MCPDeletionTarget{ProjectSlug: " requested ", SprintID: 907, Mode: store.ModeFull})
				return prepared != nil, err
			},
			success: []string{"access", "role", "target"},
		},
	}
	cases := []struct {
		name         string
		actor        bool
		role         store.ProjectRole
		accessErr    error
		roleErr      error
		wantErr      error
		wantPrepared bool
		trace        []string
	}{
		{name: "access failure", actor: true, role: store.RoleMaintainer, accessErr: accessErr, wantErr: accessErr, trace: []string{"access"}},
		{name: "access cancellation", actor: true, role: store.RoleMaintainer, accessErr: context.Canceled, wantErr: context.Canceled, trace: []string{"access"}},
		{name: "missing actor", role: store.RoleMaintainer, wantErr: ErrActorRequired, trace: []string{"access"}},
		{name: "role failure", actor: true, role: store.RoleMaintainer, roleErr: roleErr, wantErr: roleErr, trace: []string{"access", "role"}},
		{name: "role cancellation", actor: true, role: store.RoleMaintainer, roleErr: context.Canceled, wantErr: context.Canceled, trace: []string{"access", "role"}},
		{name: "viewer", actor: true, role: store.RoleViewer, wantErr: ErrMaintainerRequired, trace: []string{"access", "role"}},
		{name: "contributor", actor: true, role: store.RoleContributor, wantErr: ErrMaintainerRequired, trace: []string{"access", "role"}},
		{name: "nonmember", actor: true, role: "", wantErr: ErrMaintainerRequired, trace: []string{"access", "role"}},
		{name: "maintainer", actor: true, role: store.RoleMaintainer, wantPrepared: true},
		{name: "compatible owner", actor: true, role: store.RoleOwner, wantPrepared: true},
	}

	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					fake := newMCPLifecycleFake(op.state)
					fake.role = tc.role
					fake.accessErr = tc.accessErr
					fake.roleErr = tc.roleErr
					ctx := context.WithValue(context.Background(), mcpLifecycleContextKey{}, op.name+"/"+tc.name)
					if tc.actor {
						ctx = store.WithUserID(ctx, 83)
					}

					prepared, err := op.prepare(newMCPLifecycleTestService(fake), newMCPDeletionTestService(fake), ctx)
					if err != tc.wantErr {
						t.Fatalf("error = %v, want exact %v", err, tc.wantErr)
					}
					if prepared != tc.wantPrepared {
						t.Fatalf("prepared = %v, want %v", prepared, tc.wantPrepared)
					}
					wantTrace := tc.trace
					if tc.wantPrepared {
						wantTrace = op.success
					}
					assertMCPLifecycleTrace(t, fake.trace, wantTrace...)
					if len(fake.accessCalls) != 1 || fake.accessCalls[0].ctx != ctx || fake.accessCalls[0].slug != " requested " || fake.accessCalls[0].mode != store.ModeFull {
						t.Fatalf("access calls = %+v, want exact bound arguments", fake.accessCalls)
					}
					if tc.actor && tc.accessErr == nil && len(fake.roleCalls) == 1 {
						call := fake.roleCalls[0]
						if call.ctx != ctx || call.projectID != 71 || call.userID != 83 {
							t.Fatalf("role call = %+v, want bound context/project/actor", call)
						}
					}
					if tc.wantPrepared {
						if len(fake.readCalls) != 1 || fake.readCalls[0].ctx != ctx || fake.readCalls[0].sprintID != 907 {
							t.Fatalf("target calls = %+v, want exact bound context/requested sprint", fake.readCalls)
						}
					}
				})
			}
		})
	}
}

func TestMCPLifecyclePreparationUsesFreshRole(t *testing.T) {
	operations := []struct {
		name    string
		state   string
		prepare func(*mcpLifecycleFake) (bool, error)
	}{
		{name: "activate", state: store.SprintStatePlanned, prepare: func(fake *mcpLifecycleFake) (bool, error) {
			prepared, err := newMCPLifecycleTestService(fake).PrepareActivate(mcpLifecycleContext(89, "activate"), MCPLifecycleTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull})
			return prepared != nil, err
		}},
		{name: "close", state: store.SprintStateActive, prepare: func(fake *mcpLifecycleFake) (bool, error) {
			prepared, err := newMCPLifecycleTestService(fake).PrepareClose(mcpLifecycleContext(89, "close"), MCPLifecycleTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull})
			return prepared != nil, err
		}},
		{name: "delete", state: store.SprintStateClosed, prepare: func(fake *mcpLifecycleFake) (bool, error) {
			prepared, err := newMCPDeletionTestService(fake).PrepareDelete(mcpLifecycleContext(89, "delete"), MCPDeletionTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull})
			return prepared != nil, err
		}},
	}
	cases := []struct {
		name        string
		contextRole store.ProjectRole
		freshRole   store.ProjectRole
		wantErr     error
	}{
		{name: "stale maintainer denied by fresh viewer", contextRole: store.RoleMaintainer, freshRole: store.RoleViewer, wantErr: ErrMaintainerRequired},
		{name: "stale viewer allowed by fresh maintainer", contextRole: store.RoleViewer, freshRole: store.RoleMaintainer},
	}
	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					fake := newMCPLifecycleFake(op.state)
					fake.projectContext.Role = tc.contextRole
					fake.role = tc.freshRole
					prepared, err := op.prepare(fake)
					if err != tc.wantErr {
						t.Fatalf("error = %v, want exact %v", err, tc.wantErr)
					}
					if prepared != (tc.wantErr == nil) {
						t.Fatalf("prepared = %v for error %v", prepared, err)
					}
				})
			}
		})
	}
}

func TestMCPLifecyclePreparationTargetFailures(t *testing.T) {
	readErr := errors.New("private target read failure")
	cases := []struct {
		name    string
		prepare func(*mcpLifecycleFake) (bool, error)
	}{
		{name: "activate", prepare: func(fake *mcpLifecycleFake) (bool, error) {
			prepared, err := newMCPLifecycleTestService(fake).PrepareActivate(mcpLifecycleContext(97, "activate"), MCPLifecycleTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull})
			return prepared != nil, err
		}},
		{name: "close", prepare: func(fake *mcpLifecycleFake) (bool, error) {
			prepared, err := newMCPLifecycleTestService(fake).PrepareClose(mcpLifecycleContext(97, "close"), MCPLifecycleTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull})
			return prepared != nil, err
		}},
		{name: "delete", prepare: func(fake *mcpLifecycleFake) (bool, error) {
			prepared, err := newMCPDeletionTestService(fake).PrepareDelete(mcpLifecycleContext(97, "delete"), MCPDeletionTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull})
			return prepared != nil, err
		}},
	}
	for _, tc := range cases {
		for _, failure := range []struct {
			name string
			err  error
		}{{name: "read failure", err: readErr}, {name: "read cancellation", err: context.Canceled}} {
			t.Run(tc.name+" "+failure.name, func(t *testing.T) {
				fake := newMCPLifecycleFake(store.SprintStateActive)
				fake.readErrors = []error{failure.err}
				prepared, err := tc.prepare(fake)
				if prepared || err != failure.err {
					t.Fatalf("prepared/error = %v/%v, want false/exact %v", prepared, err, failure.err)
				}
				assertMCPLifecycleTrace(t, fake.trace, "access", "role", "target")
			})
		}
		t.Run(tc.name+" project mismatch", func(t *testing.T) {
			fake := newMCPLifecycleFake(store.SprintStateActive)
			fake.readResults[0].ProjectID = 72
			prepared, err := tc.prepare(fake)
			if prepared || err != ErrSprintNotInProject {
				t.Fatalf("prepared/error = %v/%v, want false/%v", prepared, err, ErrSprintNotInProject)
			}
			assertMCPLifecycleTrace(t, fake.trace, "access", "role", "target")
		})
	}
}

func TestMCPLifecycleDisabledCapabilityFollowsTargetCheckAndPrecedesStatePolicy(t *testing.T) {
	t.Run("activate", func(t *testing.T) {
		fake := newMCPLifecycleFake(store.SprintStateClosed)
		fake.projectContext.Project.SprintsEnabled = false
		prepared, err := newMCPLifecycleTestService(fake).PrepareActivate(
			mcpLifecycleContext(73, "disabled-activate"),
			MCPLifecycleTarget{ProjectSlug: "project", SprintID: 907, Mode: store.ModeFull},
		)
		if prepared != nil || !errors.Is(err, store.ErrSprintsDisabled) {
			t.Fatalf("PrepareActivate() = %#v, %v; want nil, ErrSprintsDisabled", prepared, err)
		}
		assertMCPLifecycleTrace(t, fake.trace, "access", "role", "target")
	})

	t.Run("delete", func(t *testing.T) {
		fake := newMCPLifecycleFake(store.SprintStateClosed)
		fake.projectContext.Project.SprintsEnabled = false
		prepared, err := newMCPDeletionTestService(fake).PrepareDelete(
			mcpLifecycleContext(73, "disabled-delete"),
			MCPDeletionTarget{ProjectSlug: "project", SprintID: 907, Mode: store.ModeFull},
		)
		if prepared != nil || !errors.Is(err, store.ErrSprintsDisabled) {
			t.Fatalf("PrepareDelete() = %#v, %v; want nil, ErrSprintsDisabled", prepared, err)
		}
		assertMCPLifecycleTrace(t, fake.trace, "access", "role", "target")
	})
}

func TestMCPLifecyclePrepareActivatePolicyAndClock(t *testing.T) {
	cases := []struct {
		name        string
		state       string
		endOffset   time.Duration
		wantErr     error
		wantNow     int
		wantTrace   []string
		wantPrepare bool
	}{
		{name: "active", state: store.SprintStateActive, wantErr: ErrSprintMustBePlanned, wantTrace: []string{"access", "role", "target"}},
		{name: "closed", state: store.SprintStateClosed, wantErr: ErrSprintMustBePlanned, wantTrace: []string{"access", "role", "target"}},
		{name: "end equal now", state: store.SprintStatePlanned, wantErr: ErrSprintEndNotAfterNow, wantNow: 1, wantTrace: []string{"access", "role", "target", "now"}},
		{name: "end before now", state: store.SprintStatePlanned, endOffset: -time.Second, wantErr: ErrSprintEndNotAfterNow, wantNow: 1, wantTrace: []string{"access", "role", "target", "now"}},
		{name: "end after now", state: store.SprintStatePlanned, endOffset: time.Second, wantNow: 1, wantPrepare: true, wantTrace: []string{"access", "role", "target", "now"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newMCPLifecycleFake(tc.state)
			fake.readResults[0].PlannedEndAt = fake.nowValue.Add(tc.endOffset)
			prepared, err := newMCPLifecycleTestService(fake).PrepareActivate(
				mcpLifecycleContext(101, tc.name),
				MCPLifecycleTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull},
			)
			if err != tc.wantErr || (prepared != nil) != tc.wantPrepare {
				t.Fatalf("prepared/error = %v/%v, want %v/%v", prepared != nil, err, tc.wantPrepare, tc.wantErr)
			}
			if fake.nowCalls != tc.wantNow {
				t.Fatalf("clock calls = %d, want %d", fake.nowCalls, tc.wantNow)
			}
			assertMCPLifecycleTrace(t, fake.trace, tc.wantTrace...)
		})
	}

	t.Run("nil clock uses production default", func(t *testing.T) {
		fake := newMCPLifecycleFake(store.SprintStatePlanned)
		fake.readResults[0].PlannedEndAt = time.Now().UTC().Add(24 * time.Hour)
		service := NewMCPLifecycleService(MCPLifecycleServiceDependencies{
			Access: fake, Roles: fake, Sprints: fake, Transitions: fake,
		})
		prepared, err := service.PrepareActivate(mcpLifecycleContext(103, "nil clock"), MCPLifecycleTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull})
		if err != nil || prepared == nil {
			t.Fatalf("PrepareActivate() = %v, %v; want capability, nil", prepared, err)
		}
		assertMCPLifecycleTrace(t, fake.trace, "access", "role", "target")
	})
}

func TestMCPLifecyclePrepareClosePolicy(t *testing.T) {
	for _, state := range []string{store.SprintStatePlanned, store.SprintStateClosed} {
		t.Run(state, func(t *testing.T) {
			fake := newMCPLifecycleFake(state)
			prepared, err := newMCPLifecycleTestService(fake).PrepareClose(mcpLifecycleContext(107, state), MCPLifecycleTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull})
			if prepared != nil || err != ErrSprintMustBeActive {
				t.Fatalf("PrepareClose() = %v, %v; want nil, %v", prepared, err, ErrSprintMustBeActive)
			}
			if fake.nowCalls != 0 {
				t.Fatalf("close consulted clock %d times", fake.nowCalls)
			}
			assertMCPLifecycleTrace(t, fake.trace, "access", "role", "target")
		})
	}
}

func TestMCPLifecycleSuccessfulTransitionsBindIdentityAndReturnProjection(t *testing.T) {
	type operation struct {
		name      string
		state     string
		prepare   func(*MCPLifecycleService, context.Context, MCPLifecycleTarget) (func() (store.Sprint, error), error)
		trace     []string
		calls     func(*mcpLifecycleFake) []mcpLifecycleMutationCall
		committed func(*mcpLifecycleFake) bool
	}
	operations := []operation{
		{
			name: "activate", state: store.SprintStatePlanned,
			prepare: func(service *MCPLifecycleService, ctx context.Context, target MCPLifecycleTarget) (func() (store.Sprint, error), error) {
				prepared, err := service.PrepareActivate(ctx, target)
				if err != nil {
					return nil, err
				}
				return prepared.Activate, nil
			},
			trace:     []string{"access", "role", "target", "now", "activate", "projection"},
			calls:     func(fake *mcpLifecycleFake) []mcpLifecycleMutationCall { return fake.activateCalls },
			committed: func(fake *mcpLifecycleFake) bool { return fake.activateCommitted },
		},
		{
			name: "close", state: store.SprintStateActive,
			prepare: func(service *MCPLifecycleService, ctx context.Context, target MCPLifecycleTarget) (func() (store.Sprint, error), error) {
				prepared, err := service.PrepareClose(ctx, target)
				if err != nil {
					return nil, err
				}
				return prepared.Close, nil
			},
			trace:     []string{"access", "role", "target", "close", "projection"},
			calls:     func(fake *mcpLifecycleFake) []mcpLifecycleMutationCall { return fake.closeCalls },
			committed: func(fake *mcpLifecycleFake) bool { return fake.closeCommitted },
		},
	}
	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			fake := newMCPLifecycleFake(op.state)
			ctx := mcpLifecycleContext(109, op.name)
			target := MCPLifecycleTarget{ProjectSlug: "original", SprintID: 907, Mode: store.ModeFull}
			execute, err := op.prepare(newMCPLifecycleTestService(fake), ctx, target)
			if err != nil {
				t.Fatalf("prepare: %v", err)
			}
			target.ProjectSlug = "replacement"
			target.SprintID = 999
			target.Mode = store.ModeAnonymous

			got, err := execute()
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if !reflect.DeepEqual(got, fake.readResults[1]) {
				t.Fatalf("result = %+v, want exact projection %+v", got, fake.readResults[1])
			}
			calls := op.calls(fake)
			if len(calls) != 1 || calls[0].ctx != ctx || calls[0].projectID != 71 || calls[0].sprintID != 907 {
				t.Fatalf("mutation calls = %+v, want one bound call", calls)
			}
			if len(fake.readCalls) != 2 || fake.readCalls[1].ctx != ctx || fake.readCalls[1].sprintID != 907 {
				t.Fatalf("read calls = %+v, want target and bound projection", fake.readCalls)
			}
			if !op.committed(fake) {
				t.Fatal("mutation did not commit")
			}
			assertMCPLifecycleTrace(t, fake.trace, op.trace...)
		})
	}
}

func TestMCPLifecycleMutationFailuresPassThroughWithoutProjection(t *testing.T) {
	mutationErr := errors.New("private transition failure")
	cases := []struct {
		name    string
		state   string
		prepare func(*MCPLifecycleService, context.Context) (func() (store.Sprint, error), error)
	}{
		{name: "activate", state: store.SprintStatePlanned, prepare: func(service *MCPLifecycleService, ctx context.Context) (func() (store.Sprint, error), error) {
			prepared, err := service.PrepareActivate(ctx, MCPLifecycleTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull})
			if err != nil {
				return nil, err
			}
			return prepared.Activate, nil
		}},
		{name: "close", state: store.SprintStateActive, prepare: func(service *MCPLifecycleService, ctx context.Context) (func() (store.Sprint, error), error) {
			prepared, err := service.PrepareClose(ctx, MCPLifecycleTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull})
			if err != nil {
				return nil, err
			}
			return prepared.Close, nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newMCPLifecycleFake(tc.state)
			if tc.name == "activate" {
				fake.activateErr = mutationErr
			} else {
				fake.closeErr = mutationErr
			}
			execute, err := tc.prepare(newMCPLifecycleTestService(fake), mcpLifecycleContext(113, tc.name))
			if err != nil {
				t.Fatalf("prepare: %v", err)
			}
			got, err := execute()
			if err != mutationErr {
				t.Fatalf("error = %v, want exact %v", err, mutationErr)
			}
			assertZeroSprint(t, got)
			if len(fake.readCalls) != 1 {
				t.Fatalf("read calls = %d, want target only", len(fake.readCalls))
			}
			want := []string{"access", "role", "target", tc.name}
			if tc.name == "activate" {
				want = []string{"access", "role", "target", "now", "activate"}
			}
			assertMCPLifecycleTrace(t, fake.trace, want...)
		})
	}
}

func TestMCPLifecycleActivateStoreTimeFailureRemainsRaw(t *testing.T) {
	fake := newMCPLifecycleFake(store.SprintStatePlanned)
	storeTimeErr := fmt.Errorf("activate sprint: %w: sprint end date is on or before now", store.ErrValidation)
	fake.activateErr = storeTimeErr
	prepared, err := newMCPLifecycleTestService(fake).PrepareActivate(mcpLifecycleContext(127, "store time"), MCPLifecycleTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	got, err := prepared.Activate()
	if err != storeTimeErr {
		t.Fatalf("error = %v, want exact raw store error %v", err, storeTimeErr)
	}
	if errors.Is(err, ErrSprintEndNotAfterNow) {
		t.Fatalf("store-time failure was converted to preparation sentinel: %v", err)
	}
	if !errors.Is(err, store.ErrValidation) {
		t.Fatalf("error no longer exposes store validation cause: %v", err)
	}
	assertZeroSprint(t, got)
	if fake.nowCalls != 1 {
		t.Fatalf("clock calls = %d, want preparation-only call", fake.nowCalls)
	}
	assertMCPLifecycleTrace(t, fake.trace, "access", "role", "target", "now", "activate")
}

func TestMCPLifecycleProjectionFailuresOccurAfterCommittedMutation(t *testing.T) {
	projectionErr := errors.New("private projection failure")
	cases := []struct{ name, state string }{{"activate", store.SprintStatePlanned}, {"close", store.SprintStateActive}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newMCPLifecycleFake(tc.state)
			fake.readErrors = []error{nil, projectionErr}
			service := newMCPLifecycleTestService(fake)
			var execute func() (store.Sprint, error)
			if tc.name == "activate" {
				prepared, err := service.PrepareActivate(mcpLifecycleContext(131, tc.name), MCPLifecycleTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull})
				if err != nil {
					t.Fatalf("prepare: %v", err)
				}
				execute = prepared.Activate
			} else {
				prepared, err := service.PrepareClose(mcpLifecycleContext(131, tc.name), MCPLifecycleTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull})
				if err != nil {
					t.Fatalf("prepare: %v", err)
				}
				execute = prepared.Close
			}
			got, err := execute()
			if err != projectionErr {
				t.Fatalf("error = %v, want exact %v", err, projectionErr)
			}
			assertZeroSprint(t, got)
			if tc.name == "activate" && !fake.activateCommitted {
				t.Fatal("activation was not committed before projection failure")
			}
			if tc.name == "close" && !fake.closeCommitted {
				t.Fatal("close was not committed before projection failure")
			}
			want := []string{"access", "role", "target", tc.name, "projection"}
			if tc.name == "activate" {
				want = []string{"access", "role", "target", "now", "activate", "projection"}
			}
			assertMCPLifecycleTrace(t, fake.trace, want...)
		})
	}
}

func TestMCPLifecycleCancellationAfterPreparation(t *testing.T) {
	cases := []struct{ name, state string }{{"activate", store.SprintStatePlanned}, {"close", store.SprintStateActive}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newMCPLifecycleFake(tc.state)
			fake.honorContext = true
			ctx, cancel := context.WithCancel(mcpLifecycleContext(137, tc.name))
			service := newMCPLifecycleTestService(fake)
			var execute func() (store.Sprint, error)
			if tc.name == "activate" {
				prepared, err := service.PrepareActivate(ctx, MCPLifecycleTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull})
				if err != nil {
					t.Fatalf("prepare: %v", err)
				}
				execute = prepared.Activate
			} else {
				prepared, err := service.PrepareClose(ctx, MCPLifecycleTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull})
				if err != nil {
					t.Fatalf("prepare: %v", err)
				}
				execute = prepared.Close
			}
			cancel()
			got, err := execute()
			if err != context.Canceled {
				t.Fatalf("error = %v, want context.Canceled", err)
			}
			assertZeroSprint(t, got)
			if len(fake.readCalls) != 1 {
				t.Fatalf("read calls = %d, want no projection", len(fake.readCalls))
			}
			calls := fake.activateCalls
			if tc.name == "close" {
				calls = fake.closeCalls
			}
			if len(calls) != 1 || calls[0].ctx != ctx {
				t.Fatalf("mutation calls = %+v, want exact cancelled bound context", calls)
			}
		})
	}
}

func TestMCPLifecycleProjectionCancellationOccursAfterCommittedMutation(t *testing.T) {
	cases := []struct{ name, state string }{{"activate", store.SprintStatePlanned}, {"close", store.SprintStateActive}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newMCPLifecycleFake(tc.state)
			fake.honorContext = true
			ctx, cancel := context.WithCancel(mcpLifecycleContext(139, tc.name))
			if tc.name == "activate" {
				fake.afterActivate = cancel
			} else {
				fake.afterClose = cancel
			}
			service := newMCPLifecycleTestService(fake)
			var execute func() (store.Sprint, error)
			if tc.name == "activate" {
				prepared, err := service.PrepareActivate(ctx, MCPLifecycleTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull})
				if err != nil {
					t.Fatalf("prepare: %v", err)
				}
				execute = prepared.Activate
			} else {
				prepared, err := service.PrepareClose(ctx, MCPLifecycleTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull})
				if err != nil {
					t.Fatalf("prepare: %v", err)
				}
				execute = prepared.Close
			}
			got, err := execute()
			if err != context.Canceled {
				t.Fatalf("error = %v, want context.Canceled", err)
			}
			assertZeroSprint(t, got)
			if len(fake.readCalls) != 2 || fake.readCalls[1].ctx != ctx {
				t.Fatalf("read calls = %+v, want cancelled projection", fake.readCalls)
			}
			if tc.name == "activate" && !fake.activateCommitted {
				t.Fatal("activation was not committed")
			}
			if tc.name == "close" && !fake.closeCommitted {
				t.Fatal("close was not committed")
			}
		})
	}
}
