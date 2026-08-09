package sprint

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/store"
)

var _ MCPAccessStore = (*store.Store)(nil)
var _ SprintReadStore = (*store.Store)(nil)

type mcpDefinitionContextKey struct{}

type mcpDefinitionAccessCall struct {
	ctx  context.Context
	slug string
	mode store.Mode
}

type mcpDefinitionRoleCall struct {
	ctx       context.Context
	projectID int64
	userID    int64
}

type mcpDefinitionReadCall struct {
	ctx      context.Context
	sprintID int64
}

type mcpDefinitionCreateCall struct {
	ctx            context.Context
	projectID      int64
	name           string
	plannedStartAt time.Time
	plannedEndAt   time.Time
}

type mcpDefinitionUpdateCall struct {
	ctx      context.Context
	sprintID int64
	input    store.UpdateSprintInput
}

type mcpDefinitionFake struct {
	trace []string

	projectContext  store.ProjectContext
	sprintsDisabled bool
	accessErr       error
	accessCalls     []mcpDefinitionAccessCall

	role      store.ProjectRole
	roleErr   error
	roleCalls []mcpDefinitionRoleCall

	readResults         []store.Sprint
	readErrors          []error
	honorReadContextErr bool
	readCalls           []mcpDefinitionReadCall

	createResult          store.Sprint
	createErr             error
	honorCreateContextErr bool
	createCalls           []mcpDefinitionCreateCall

	updateErr             error
	honorUpdateContextErr bool
	updateCalls           []mcpDefinitionUpdateCall
}

var _ MCPAccessStore = (*mcpDefinitionFake)(nil)
var _ RoleStore = (*mcpDefinitionFake)(nil)
var _ DefinitionStore = (*mcpDefinitionFake)(nil)
var _ SprintReadStore = (*mcpDefinitionFake)(nil)

func (f *mcpDefinitionFake) GetProjectContextBySlug(
	ctx context.Context,
	slug string,
	mode store.Mode,
) (store.ProjectContext, error) {
	f.trace = append(f.trace, "access")
	f.accessCalls = append(f.accessCalls, mcpDefinitionAccessCall{ctx: ctx, slug: slug, mode: mode})
	if f.accessErr != nil {
		return store.ProjectContext{}, f.accessErr
	}
	return f.projectContext, nil
}

func (f *mcpDefinitionFake) GetProjectRole(
	ctx context.Context,
	projectID int64,
	userID int64,
) (store.ProjectRole, error) {
	f.trace = append(f.trace, "role")
	f.roleCalls = append(f.roleCalls, mcpDefinitionRoleCall{
		ctx:       ctx,
		projectID: projectID,
		userID:    userID,
	})
	if f.roleErr != nil {
		return "", f.roleErr
	}
	return f.role, nil
}

func (f *mcpDefinitionFake) GetSprintByID(
	ctx context.Context,
	sprintID int64,
) (store.Sprint, error) {
	callIndex := len(f.readCalls)
	stage := "target"
	if callIndex > 0 {
		stage = "projection"
	}
	f.trace = append(f.trace, stage)
	f.readCalls = append(f.readCalls, mcpDefinitionReadCall{ctx: ctx, sprintID: sprintID})
	if f.honorReadContextErr && ctx.Err() != nil {
		return store.Sprint{}, ctx.Err()
	}
	if callIndex < len(f.readErrors) && f.readErrors[callIndex] != nil {
		return store.Sprint{}, f.readErrors[callIndex]
	}
	if callIndex < len(f.readResults) {
		return f.readResults[callIndex], nil
	}
	return store.Sprint{}, nil
}

func (f *mcpDefinitionFake) CreateSprint(
	ctx context.Context,
	projectID int64,
	name string,
	plannedStartAt time.Time,
	plannedEndAt time.Time,
) (store.Sprint, error) {
	f.trace = append(f.trace, "create")
	f.createCalls = append(f.createCalls, mcpDefinitionCreateCall{
		ctx:            ctx,
		projectID:      projectID,
		name:           name,
		plannedStartAt: plannedStartAt,
		plannedEndAt:   plannedEndAt,
	})
	if f.honorCreateContextErr && ctx.Err() != nil {
		return store.Sprint{}, ctx.Err()
	}
	return f.createResult, f.createErr
}

func (f *mcpDefinitionFake) UpdateSprint(
	ctx context.Context,
	sprintID int64,
	input store.UpdateSprintInput,
) error {
	f.trace = append(f.trace, "update")
	f.updateCalls = append(f.updateCalls, mcpDefinitionUpdateCall{
		ctx:      ctx,
		sprintID: sprintID,
		input:    input,
	})
	if f.honorUpdateContextErr && ctx.Err() != nil {
		return ctx.Err()
	}
	return f.updateErr
}

func newMCPDefinitionTestService(fake *mcpDefinitionFake) *MCPDefinitionService {
	if !fake.sprintsDisabled {
		fake.projectContext.Project.SprintsEnabled = true
	}
	return NewMCPDefinitionService(MCPDefinitionServiceDependencies{
		Access:      fake,
		Roles:       fake,
		Definitions: fake,
		Sprints:     fake,
	})
}

func mcpDefinitionActorContext(userID int64, marker string) context.Context {
	ctx := context.WithValue(context.Background(), mcpDefinitionContextKey{}, marker)
	return store.WithUserID(ctx, userID)
}

func mcpDefinitionExisting() store.Sprint {
	return store.Sprint{
		ID:        907,
		ProjectID: 41,
		Number:    12,
		Name:      "Existing sprint",
		State:     "PLANNED",
	}
}

func assertMCPDefinitionTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trace = %v, want %v", got, want)
	}
}

func TestMCPDefinitionServiceConstructionIsInertAndPublicationFree(t *testing.T) {
	fake := &mcpDefinitionFake{}
	service := newMCPDefinitionTestService(fake)
	if service == nil {
		t.Fatal("NewMCPDefinitionService returned nil")
	}
	if len(fake.trace) != 0 {
		t.Fatalf("construction performed dependency work: %v", fake.trace)
	}

	depsType := reflect.TypeOf(MCPDefinitionServiceDependencies{})
	wantDependencyFields := []string{"Access", "Roles", "Definitions", "Sprints"}
	gotDependencyFields := make([]string, 0, depsType.NumField())
	for i := 0; i < depsType.NumField(); i++ {
		gotDependencyFields = append(gotDependencyFields, depsType.Field(i).Name)
	}
	if !reflect.DeepEqual(gotDependencyFields, wantDependencyFields) {
		t.Fatalf("dependency fields = %v, want %v", gotDependencyFields, wantDependencyFields)
	}
	for _, field := range gotDependencyFields {
		lower := strings.ToLower(field)
		if strings.Contains(lower, "publish") || strings.Contains(lower, "event") ||
			strings.Contains(lower, "runtime") || strings.Contains(lower, "fanout") {
			t.Fatalf("unexpected realtime dependency %q", field)
		}
	}
}

func TestMCPDefinitionPreparationAuthorization(t *testing.T) {
	accessErr := errors.New("configured private access failure")
	roleErr := errors.New("configured private role failure")
	operations := []struct {
		name    string
		prepare func(*MCPDefinitionService, context.Context) (bool, error)
		success []string
	}{
		{
			name: "create",
			prepare: func(service *MCPDefinitionService, ctx context.Context) (bool, error) {
				prepared, err := service.PrepareCreate(ctx, MCPProjectTarget{
					ProjectSlug: "  requested-slug  ",
					Mode:        store.ModeFull,
				})
				return prepared != nil, err
			},
			success: []string{"access", "role"},
		},
		{
			name: "update",
			prepare: func(service *MCPDefinitionService, ctx context.Context) (bool, error) {
				prepared, err := service.PrepareUpdate(ctx, MCPSprintTarget{
					ProjectSlug: "  requested-slug  ",
					SprintID:    907,
					Mode:        store.ModeFull,
				})
				return prepared != nil, err
			},
			success: []string{"access", "role", "target"},
		},
	}
	tests := []struct {
		name         string
		actor        bool
		accessErr    error
		role         store.ProjectRole
		roleErr      error
		wantPrepared bool
		wantErr      error
		wantTrace    []string
	}{
		{name: "access error remains raw", actor: true, accessErr: accessErr, wantErr: accessErr, wantTrace: []string{"access"}},
		{name: "access cancellation remains raw", actor: true, accessErr: context.Canceled, wantErr: context.Canceled, wantTrace: []string{"access"}},
		{name: "missing actor", wantErr: ErrActorRequired, wantTrace: []string{"access"}},
		{name: "role error remains raw", actor: true, roleErr: roleErr, wantErr: roleErr, wantTrace: []string{"access", "role"}},
		{name: "role cancellation remains raw", actor: true, roleErr: context.Canceled, wantErr: context.Canceled, wantTrace: []string{"access", "role"}},
		{name: "contributor", actor: true, role: store.RoleContributor, wantErr: ErrMaintainerRequired, wantTrace: []string{"access", "role"}},
		{name: "viewer", actor: true, role: store.RoleViewer, wantErr: ErrMaintainerRequired, wantTrace: []string{"access", "role"}},
		{name: "non-member", actor: true, wantErr: ErrMaintainerRequired, wantTrace: []string{"access", "role"}},
		{name: "maintainer", actor: true, role: store.RoleMaintainer, wantPrepared: true},
		{name: "owner", actor: true, role: store.RoleOwner, wantPrepared: true},
	}

	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					fake := &mcpDefinitionFake{
						projectContext: store.ProjectContext{
							Project: store.Project{ID: 41},
							Role:    store.RoleMaintainer,
						},
						accessErr:   tt.accessErr,
						role:        tt.role,
						roleErr:     tt.roleErr,
						readResults: []store.Sprint{mcpDefinitionExisting()},
					}
					ctx := context.WithValue(context.Background(), mcpDefinitionContextKey{}, tt.name)
					if tt.actor {
						ctx = store.WithUserID(ctx, 73)
					}

					prepared, err := operation.prepare(newMCPDefinitionTestService(fake), ctx)
					if prepared != tt.wantPrepared {
						t.Fatalf("prepared = %v, want %v", prepared, tt.wantPrepared)
					}
					if err != tt.wantErr {
						t.Fatalf("error = %v, want exact %v", err, tt.wantErr)
					}
					wantTrace := tt.wantTrace
					if tt.wantPrepared {
						wantTrace = operation.success
					}
					assertMCPDefinitionTrace(t, fake.trace, wantTrace...)

					if len(fake.accessCalls) != 1 {
						t.Fatalf("access calls = %d, want 1", len(fake.accessCalls))
					}
					accessCall := fake.accessCalls[0]
					if accessCall.ctx != ctx || accessCall.slug != "  requested-slug  " || accessCall.mode != store.ModeFull {
						t.Fatalf("access call = %#v", accessCall)
					}
					if len(fake.roleCalls) > 0 {
						roleCall := fake.roleCalls[0]
						if roleCall.ctx != ctx || roleCall.projectID != 41 || roleCall.userID != 73 {
							t.Fatalf("role call = %#v", roleCall)
						}
					}
					if tt.roleErr != nil && errors.Is(err, ErrMaintainerRequired) {
						t.Fatalf("raw role error %v was collapsed", err)
					}
					if len(fake.createCalls) != 0 || len(fake.updateCalls) != 0 {
						t.Fatalf("preparation performed mutation: create=%d update=%d", len(fake.createCalls), len(fake.updateCalls))
					}
				})
			}
		})
	}
}

func TestMCPDefinitionPreparationUsesFreshRole(t *testing.T) {
	tests := []struct {
		name         string
		contextRole  store.ProjectRole
		freshRole    store.ProjectRole
		wantPrepared bool
		wantErr      error
	}{
		{
			name:        "context maintainer fresh viewer denied",
			contextRole: store.RoleMaintainer,
			freshRole:   store.RoleViewer,
			wantErr:     ErrMaintainerRequired,
		},
		{
			name:         "context viewer fresh maintainer allowed",
			contextRole:  store.RoleViewer,
			freshRole:    store.RoleMaintainer,
			wantPrepared: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &mcpDefinitionFake{
				projectContext: store.ProjectContext{
					Project: store.Project{ID: 41},
					Role:    tt.contextRole,
				},
				role: tt.freshRole,
			}
			prepared, err := newMCPDefinitionTestService(fake).PrepareCreate(
				mcpDefinitionActorContext(73, tt.name),
				MCPProjectTarget{ProjectSlug: "project", Mode: store.ModeFull},
			)
			if (prepared != nil) != tt.wantPrepared || err != tt.wantErr {
				t.Fatalf("prepared = %v error = %v, want present %v error %v", prepared != nil, err, tt.wantPrepared, tt.wantErr)
			}
			assertMCPDefinitionTrace(t, fake.trace, "access", "role")
		})
	}
}

func TestMCPDefinitionPrepareUpdateTargetGate(t *testing.T) {
	targetErr := errors.New("configured private target failure")
	tests := []struct {
		name         string
		readResult   store.Sprint
		readErr      error
		wantPrepared bool
		wantErr      error
	}{
		{name: "target read error remains raw", readErr: targetErr, wantErr: targetErr},
		{name: "target cancellation remains raw", readErr: context.Canceled, wantErr: context.Canceled},
		{name: "foreign project", readResult: store.Sprint{ID: 907, ProjectID: 99, Number: 12}, wantErr: ErrSprintNotInProject},
		{name: "verified target", readResult: mcpDefinitionExisting(), wantPrepared: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &mcpDefinitionFake{
				projectContext: store.ProjectContext{Project: store.Project{ID: 41}},
				role:           store.RoleMaintainer,
				readResults:    []store.Sprint{tt.readResult},
				readErrors:     []error{tt.readErr},
			}
			ctx := mcpDefinitionActorContext(73, tt.name)
			prepared, err := newMCPDefinitionTestService(fake).PrepareUpdate(ctx, MCPSprintTarget{
				ProjectSlug: "project",
				SprintID:    907,
				Mode:        store.ModeFull,
			})
			if (prepared != nil) != tt.wantPrepared || err != tt.wantErr {
				t.Fatalf("prepared = %v error = %v, want present %v error %v", prepared != nil, err, tt.wantPrepared, tt.wantErr)
			}
			assertMCPDefinitionTrace(t, fake.trace, "access", "role", "target")
			if len(fake.readCalls) != 1 || fake.readCalls[0].ctx != ctx || fake.readCalls[0].sprintID != 907 {
				t.Fatalf("target calls = %#v", fake.readCalls)
			}
			if len(fake.createCalls) != 0 || len(fake.updateCalls) != 0 {
				t.Fatalf("target preparation mutated: create=%d update=%d", len(fake.createCalls), len(fake.updateCalls))
			}
		})
	}

	errText := ErrSprintNotInProject.Error()
	if errText != "sprint definition target not in project" {
		t.Fatalf("mismatch text = %q", errText)
	}
	if strings.Contains(errText, "41") || strings.Contains(errText, "907") || errors.Unwrap(ErrSprintNotInProject) != nil {
		t.Fatalf("mismatch error leaks identity or wraps a cause: %q", errText)
	}
}

func TestMCPDefinitionDisabledCapabilityFollowsAuthorizationAndTargetChecks(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		fake := &mcpDefinitionFake{
			projectContext:  store.ProjectContext{Project: store.Project{ID: 41}},
			sprintsDisabled: true,
			role:            store.RoleMaintainer,
		}
		prepared, err := newMCPDefinitionTestService(fake).PrepareCreate(
			mcpDefinitionActorContext(73, "disabled-create"),
			MCPProjectTarget{ProjectSlug: "project", Mode: store.ModeFull},
		)
		if prepared != nil || !errors.Is(err, store.ErrSprintsDisabled) {
			t.Fatalf("PrepareCreate() = %#v, %v; want nil, ErrSprintsDisabled", prepared, err)
		}
		assertMCPDefinitionTrace(t, fake.trace, "access", "role")
	})

	t.Run("empty update", func(t *testing.T) {
		fake := &mcpDefinitionFake{
			projectContext:  store.ProjectContext{Project: store.Project{ID: 41}},
			sprintsDisabled: true,
			role:            store.RoleMaintainer,
			readResults:     []store.Sprint{mcpDefinitionExisting()},
		}
		prepared, err := newMCPDefinitionTestService(fake).PrepareUpdate(
			mcpDefinitionActorContext(73, "disabled-update"),
			MCPSprintTarget{ProjectSlug: "project", SprintID: 907, Mode: store.ModeFull},
		)
		if prepared != nil || !errors.Is(err, store.ErrSprintsDisabled) {
			t.Fatalf("PrepareUpdate() = %#v, %v; want nil, ErrSprintsDisabled", prepared, err)
		}
		assertMCPDefinitionTrace(t, fake.trace, "access", "role", "target")
		if len(fake.updateCalls) != 0 {
			t.Fatalf("disabled empty update mutated: %#v", fake.updateCalls)
		}
	})
}

func TestPreparedMCPDefinitionBindsTargetsByValue(t *testing.T) {
	t.Run("create binds resolved project", func(t *testing.T) {
		fake := &mcpDefinitionFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 41}},
			role:           store.RoleMaintainer,
		}
		ctx := mcpDefinitionActorContext(73, "create-copy")
		target := MCPProjectTarget{ProjectSlug: "original", Mode: store.ModeFull}
		prepared, err := newMCPDefinitionTestService(fake).PrepareCreate(ctx, target)
		if err != nil {
			t.Fatalf("PrepareCreate: %v", err)
		}
		target.ProjectSlug = "replacement"
		target.Mode = store.ModeAnonymous

		if _, err := prepared.Create(CreateCommand{Name: "created"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if len(fake.createCalls) != 1 || fake.createCalls[0].ctx != ctx || fake.createCalls[0].projectID != 41 {
			t.Fatalf("create calls = %#v", fake.createCalls)
		}
		if fake.accessCalls[0].slug != "original" || fake.accessCalls[0].mode != store.ModeFull {
			t.Fatalf("access call = %#v", fake.accessCalls[0])
		}
	})

	t.Run("update binds requested verified stored ID", func(t *testing.T) {
		existing := mcpDefinitionExisting()
		updated := store.Sprint{ID: 907, ProjectID: 41, Number: 12, Name: "Updated"}
		fake := &mcpDefinitionFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 41}},
			role:           store.RoleMaintainer,
			readResults:    []store.Sprint{existing, updated},
		}
		ctx := mcpDefinitionActorContext(73, "update-copy")
		target := MCPSprintTarget{ProjectSlug: "original", SprintID: 907, Mode: store.ModeFull}
		prepared, err := newMCPDefinitionTestService(fake).PrepareUpdate(ctx, target)
		if err != nil {
			t.Fatalf("PrepareUpdate: %v", err)
		}
		target.ProjectSlug = "replacement"
		target.SprintID = 888
		target.Mode = store.ModeAnonymous

		name := "Updated"
		got, err := prepared.Update(UpdateCommand{Name: &name})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if !reflect.DeepEqual(got, updated) {
			t.Fatalf("result = %#v, want %#v", got, updated)
		}
		if existing.ID != 907 || existing.Number == existing.ID {
			t.Fatalf("test identities are not distinct enough: %#v", existing)
		}
		if len(fake.updateCalls) != 1 || fake.updateCalls[0].sprintID != 907 {
			t.Fatalf("update calls = %#v", fake.updateCalls)
		}
		if len(fake.readCalls) != 2 || fake.readCalls[0].sprintID != 907 || fake.readCalls[1].sprintID != 907 {
			t.Fatalf("read calls = %#v", fake.readCalls)
		}
	})
}

func TestPreparedMCPCreateDelegatesAndReturnsExactResult(t *testing.T) {
	start := time.Date(2026, time.September, 1, 2, 3, 4, 5, time.FixedZone("input", 3600))
	end := start.Add(14 * 24 * time.Hour)
	want := store.Sprint{ID: 907, ProjectID: 41, Number: 12, Name: "Returned", PlannedStartAt: start, PlannedEndAt: end}
	fake := &mcpDefinitionFake{
		projectContext: store.ProjectContext{Project: store.Project{ID: 41}},
		role:           store.RoleMaintainer,
		createResult:   want,
	}
	ctx := mcpDefinitionActorContext(73, "create-success")
	prepared, err := newMCPDefinitionTestService(fake).PrepareCreate(ctx, MCPProjectTarget{
		ProjectSlug: "project",
		Mode:        store.ModeFull,
	})
	if err != nil {
		t.Fatalf("PrepareCreate: %v", err)
	}

	got, err := prepared.Create(CreateCommand{Name: "Input", PlannedStartAt: start, PlannedEndAt: end})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("result = %#v, want exact %#v", got, want)
	}
	assertMCPDefinitionTrace(t, fake.trace, "access", "role", "create")
	if len(fake.createCalls) != 1 || len(fake.readCalls) != 0 || len(fake.updateCalls) != 0 {
		t.Fatalf("calls: create=%d read=%d update=%d", len(fake.createCalls), len(fake.readCalls), len(fake.updateCalls))
	}
	call := fake.createCalls[0]
	if call.ctx != ctx || call.projectID != 41 || call.name != "Input" ||
		!call.plannedStartAt.Equal(start) || call.plannedStartAt.Location() != start.Location() ||
		!call.plannedEndAt.Equal(end) || call.plannedEndAt.Location() != end.Location() {
		t.Fatalf("create call = %#v", call)
	}
}

func TestPreparedMCPCreateFailureReturnsNoPartialResult(t *testing.T) {
	createErr := errors.New("configured create return-read failure")
	fake := &mcpDefinitionFake{
		projectContext: store.ProjectContext{Project: store.Project{ID: 41}},
		role:           store.RoleMaintainer,
		createResult:   store.Sprint{ID: 907, ProjectID: 41, Name: "must not escape"},
		createErr:      createErr,
	}
	prepared, err := newMCPDefinitionTestService(fake).PrepareCreate(
		mcpDefinitionActorContext(73, "create-failure"),
		MCPProjectTarget{ProjectSlug: "project", Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("PrepareCreate: %v", err)
	}

	got, err := prepared.Create(CreateCommand{Name: "Input"})
	if err != createErr {
		t.Fatalf("error = %v, want exact %v", err, createErr)
	}
	if got != (store.Sprint{}) {
		t.Fatalf("partial result escaped: %#v", got)
	}
	assertMCPDefinitionTrace(t, fake.trace, "access", "role", "create")
	if len(fake.createCalls) != 1 || len(fake.readCalls) != 0 || len(fake.updateCalls) != 0 {
		t.Fatalf("calls: create=%d read=%d update=%d", len(fake.createCalls), len(fake.readCalls), len(fake.updateCalls))
	}
}

func TestPreparedMCPUpdateEmptyReturnsRetainedSprint(t *testing.T) {
	existing := mcpDefinitionExisting()
	fake := &mcpDefinitionFake{
		projectContext: store.ProjectContext{Project: store.Project{ID: 41}},
		role:           store.RoleMaintainer,
		readResults:    []store.Sprint{existing},
	}
	ctx, cancel := context.WithCancel(mcpDefinitionActorContext(73, "empty"))
	prepared, err := newMCPDefinitionTestService(fake).PrepareUpdate(ctx, MCPSprintTarget{
		ProjectSlug: "project",
		SprintID:    907,
		Mode:        store.ModeFull,
	})
	if err != nil {
		t.Fatalf("PrepareUpdate: %v", err)
	}
	cancel()

	got, err := prepared.Update(UpdateCommand{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !reflect.DeepEqual(got, existing) {
		t.Fatalf("result = %#v, want retained %#v", got, existing)
	}
	assertMCPDefinitionTrace(t, fake.trace, "access", "role", "target")
	if len(fake.readCalls) != 1 || len(fake.updateCalls) != 0 || len(fake.createCalls) != 0 {
		t.Fatalf("calls: read=%d update=%d create=%d", len(fake.readCalls), len(fake.updateCalls), len(fake.createCalls))
	}
}

func TestPreparedMCPUpdateDelegatesThenReadsExactResult(t *testing.T) {
	start := time.Date(2026, time.October, 2, 3, 4, 5, 6, time.FixedZone("patch", -7200))
	end := start.Add(21 * 24 * time.Hour)
	name := "Renamed"
	existing := mcpDefinitionExisting()
	updated := existing
	updated.Name = name
	updated.PlannedStartAt = start
	updated.PlannedEndAt = end
	fake := &mcpDefinitionFake{
		projectContext: store.ProjectContext{Project: store.Project{ID: 41}},
		role:           store.RoleMaintainer,
		readResults:    []store.Sprint{existing, updated},
	}
	ctx := mcpDefinitionActorContext(73, "update-success")
	prepared, err := newMCPDefinitionTestService(fake).PrepareUpdate(ctx, MCPSprintTarget{
		ProjectSlug: "project",
		SprintID:    907,
		Mode:        store.ModeFull,
	})
	if err != nil {
		t.Fatalf("PrepareUpdate: %v", err)
	}
	command := UpdateCommand{Name: &name, PlannedStartAt: &start, PlannedEndAt: &end}

	got, err := prepared.Update(command)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !reflect.DeepEqual(got, updated) {
		t.Fatalf("result = %#v, want exact %#v", got, updated)
	}
	assertMCPDefinitionTrace(t, fake.trace, "access", "role", "target", "update", "projection")
	if len(fake.updateCalls) != 1 || len(fake.readCalls) != 2 {
		t.Fatalf("calls: update=%d read=%d", len(fake.updateCalls), len(fake.readCalls))
	}
	call := fake.updateCalls[0]
	if call.ctx != ctx || call.sprintID != 907 || fake.readCalls[1].ctx != ctx || fake.readCalls[1].sprintID != 907 {
		t.Fatalf("update = %#v projection = %#v", call, fake.readCalls[1])
	}
	if call.input.Name == nil || *call.input.Name != name || call.input.Name == command.Name ||
		call.input.PlannedStartAt == nil || !call.input.PlannedStartAt.Equal(start) || call.input.PlannedStartAt == command.PlannedStartAt ||
		call.input.PlannedEndAt == nil || !call.input.PlannedEndAt.Equal(end) || call.input.PlannedEndAt == command.PlannedEndAt {
		t.Fatalf("materialized input = %#v", call.input)
	}
}

func TestPreparedMCPUpdatePreservesExplicitEmptyAndZero(t *testing.T) {
	emptyName := ""
	zeroTime := time.Time{}
	existing := mcpDefinitionExisting()
	fake := &mcpDefinitionFake{
		projectContext: store.ProjectContext{Project: store.Project{ID: 41}},
		role:           store.RoleMaintainer,
		readResults:    []store.Sprint{existing, existing},
	}
	prepared, err := newMCPDefinitionTestService(fake).PrepareUpdate(
		mcpDefinitionActorContext(73, "explicit-empty"),
		MCPSprintTarget{ProjectSlug: "project", SprintID: 907, Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("PrepareUpdate: %v", err)
	}

	if _, err := prepared.Update(UpdateCommand{Name: &emptyName, PlannedStartAt: &zeroTime}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	input := fake.updateCalls[0].input
	if input.Name == nil || *input.Name != "" || input.PlannedStartAt == nil || !input.PlannedStartAt.IsZero() || input.PlannedEndAt != nil {
		t.Fatalf("input = %#v", input)
	}
}

func TestPreparedMCPUpdateFailuresReturnNoPartialResult(t *testing.T) {
	t.Run("mutation failure suppresses projection", func(t *testing.T) {
		updateErr := errors.New("configured update failure")
		existing := mcpDefinitionExisting()
		fake := &mcpDefinitionFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 41}},
			role:           store.RoleMaintainer,
			readResults:    []store.Sprint{existing},
			updateErr:      updateErr,
		}
		prepared, err := newMCPDefinitionTestService(fake).PrepareUpdate(
			mcpDefinitionActorContext(73, "update-failure"),
			MCPSprintTarget{ProjectSlug: "project", SprintID: 907, Mode: store.ModeFull},
		)
		if err != nil {
			t.Fatalf("PrepareUpdate: %v", err)
		}

		got, err := prepared.Update(UpdateCommand{Name: stringPointer("changed")})
		if err != updateErr {
			t.Fatalf("error = %v, want exact %v", err, updateErr)
		}
		if got != (store.Sprint{}) {
			t.Fatalf("partial result escaped: %#v", got)
		}
		assertMCPDefinitionTrace(t, fake.trace, "access", "role", "target", "update")
		if len(fake.updateCalls) != 1 || len(fake.readCalls) != 1 {
			t.Fatalf("calls: update=%d read=%d", len(fake.updateCalls), len(fake.readCalls))
		}
	})

	t.Run("committed update projection failure", func(t *testing.T) {
		projectionErr := errors.New("configured private projection failure")
		existing := mcpDefinitionExisting()
		fake := &mcpDefinitionFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 41}},
			role:           store.RoleMaintainer,
			readResults:    []store.Sprint{existing, {ID: 907, ProjectID: 41, Name: "must not escape"}},
			readErrors:     []error{nil, projectionErr},
		}
		prepared, err := newMCPDefinitionTestService(fake).PrepareUpdate(
			mcpDefinitionActorContext(73, "projection-failure"),
			MCPSprintTarget{ProjectSlug: "project", SprintID: 907, Mode: store.ModeFull},
		)
		if err != nil {
			t.Fatalf("PrepareUpdate: %v", err)
		}

		got, err := prepared.Update(UpdateCommand{Name: stringPointer("committed")})
		if err != projectionErr {
			t.Fatalf("error = %v, want exact %v", err, projectionErr)
		}
		if got != (store.Sprint{}) {
			t.Fatalf("partial result escaped: %#v", got)
		}
		assertMCPDefinitionTrace(t, fake.trace, "access", "role", "target", "update", "projection")
		if len(fake.updateCalls) != 1 || len(fake.readCalls) != 2 {
			t.Fatalf("calls: update=%d read=%d", len(fake.updateCalls), len(fake.readCalls))
		}
	})
}

func TestPreparedMCPDefinitionUsesCancelledBoundContext(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		fake := &mcpDefinitionFake{
			projectContext:        store.ProjectContext{Project: store.Project{ID: 41}},
			role:                  store.RoleMaintainer,
			honorCreateContextErr: true,
		}
		ctx, cancel := context.WithCancel(mcpDefinitionActorContext(73, "cancel-create"))
		prepared, err := newMCPDefinitionTestService(fake).PrepareCreate(ctx, MCPProjectTarget{ProjectSlug: "project", Mode: store.ModeFull})
		if err != nil {
			t.Fatalf("PrepareCreate: %v", err)
		}
		cancel()

		got, err := prepared.Create(CreateCommand{Name: "cancelled"})
		if err != context.Canceled || got != (store.Sprint{}) {
			t.Fatalf("result = %#v error = %v, want zero/context.Canceled", got, err)
		}
		assertMCPDefinitionTrace(t, fake.trace, "access", "role", "create")
		if len(fake.createCalls) != 1 || fake.createCalls[0].ctx != ctx {
			t.Fatalf("create calls = %#v", fake.createCalls)
		}
	})

	t.Run("update mutation", func(t *testing.T) {
		existing := mcpDefinitionExisting()
		fake := &mcpDefinitionFake{
			projectContext:        store.ProjectContext{Project: store.Project{ID: 41}},
			role:                  store.RoleMaintainer,
			readResults:           []store.Sprint{existing},
			honorUpdateContextErr: true,
		}
		ctx, cancel := context.WithCancel(mcpDefinitionActorContext(73, "cancel-update"))
		prepared, err := newMCPDefinitionTestService(fake).PrepareUpdate(ctx, MCPSprintTarget{ProjectSlug: "project", SprintID: 907, Mode: store.ModeFull})
		if err != nil {
			t.Fatalf("PrepareUpdate: %v", err)
		}
		cancel()

		got, err := prepared.Update(UpdateCommand{Name: stringPointer("cancelled")})
		if err != context.Canceled || got != (store.Sprint{}) {
			t.Fatalf("result = %#v error = %v, want zero/context.Canceled", got, err)
		}
		assertMCPDefinitionTrace(t, fake.trace, "access", "role", "target", "update")
		if len(fake.updateCalls) != 1 || fake.updateCalls[0].ctx != ctx || len(fake.readCalls) != 1 {
			t.Fatalf("update calls = %#v reads = %#v", fake.updateCalls, fake.readCalls)
		}
	})

	t.Run("update projection", func(t *testing.T) {
		existing := mcpDefinitionExisting()
		fake := &mcpDefinitionFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 41}},
			role:           store.RoleMaintainer,
			readResults:    []store.Sprint{existing},
			readErrors:     []error{nil, context.Canceled},
		}
		ctx := mcpDefinitionActorContext(73, "cancel-projection")
		prepared, err := newMCPDefinitionTestService(fake).PrepareUpdate(ctx, MCPSprintTarget{ProjectSlug: "project", SprintID: 907, Mode: store.ModeFull})
		if err != nil {
			t.Fatalf("PrepareUpdate: %v", err)
		}

		got, err := prepared.Update(UpdateCommand{Name: stringPointer("committed")})
		if err != context.Canceled || got != (store.Sprint{}) {
			t.Fatalf("result = %#v error = %v, want zero/context.Canceled", got, err)
		}
		assertMCPDefinitionTrace(t, fake.trace, "access", "role", "target", "update", "projection")
		if len(fake.updateCalls) != 1 || len(fake.readCalls) != 2 || fake.readCalls[1].ctx != ctx {
			t.Fatalf("update calls = %#v reads = %#v", fake.updateCalls, fake.readCalls)
		}
	})
}
