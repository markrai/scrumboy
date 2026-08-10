package todo

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

var (
	_ MCPCreateAccessStore = (*store.Store)(nil)
	_ MCPCreateLookupStore = (*store.Store)(nil)
	_ CreateStore          = (*store.Store)(nil)
)

type mcpCreateAccessCall struct {
	ctx  context.Context
	slug string
	mode store.Mode
}

type mcpCreateAccessFake struct {
	calls          []mcpCreateAccessCall
	projectContext store.ProjectContext
	err            error
	trace          *[]string
}

func (f *mcpCreateAccessFake) GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error) {
	f.calls = append(f.calls, mcpCreateAccessCall{ctx: ctx, slug: slug, mode: mode})
	if f.trace != nil {
		*f.trace = append(*f.trace, "access")
	}
	if f.err != nil {
		return store.ProjectContext{}, f.err
	}
	if err := ctx.Err(); err != nil {
		return store.ProjectContext{}, err
	}
	return f.projectContext, nil
}

type mcpCreateLookupCall struct {
	ctx       context.Context
	projectID int64
	localID   int64
	mode      store.Mode
}

type mcpCreateLookupFake struct {
	calls []mcpCreateLookupCall
	todos map[int64]store.Todo
	errs  map[int64]error
	trace *[]string
}

func (f *mcpCreateLookupFake) GetTodoByLocalID(ctx context.Context, projectID int64, localID int64, mode store.Mode) (store.Todo, error) {
	f.calls = append(f.calls, mcpCreateLookupCall{ctx: ctx, projectID: projectID, localID: localID, mode: mode})
	if f.trace != nil {
		*f.trace = append(*f.trace, fmt.Sprintf("lookup:%d", localID))
	}
	if err := ctx.Err(); err != nil {
		return store.Todo{}, err
	}
	if err := f.errs[localID]; err != nil {
		return store.Todo{}, err
	}
	todo, ok := f.todos[localID]
	if !ok {
		return store.Todo{}, store.ErrNotFound
	}
	return todo, nil
}

type mcpCreateStoreCall struct {
	ctx       context.Context
	projectID int64
	input     store.CreateTodoInput
	mode      store.Mode
}

type mcpCreateStoreFake struct {
	calls []mcpCreateStoreCall
	todo  store.Todo
	err   error
	trace *[]string
}

func (f *mcpCreateStoreFake) CreateTodo(ctx context.Context, projectID int64, input store.CreateTodoInput, mode store.Mode) (store.Todo, error) {
	f.calls = append(f.calls, mcpCreateStoreCall{ctx: ctx, projectID: projectID, input: input, mode: mode})
	if f.trace != nil {
		*f.trace = append(*f.trace, "create")
	}
	if f.err != nil {
		return store.Todo{}, f.err
	}
	if err := ctx.Err(); err != nil {
		return store.Todo{}, err
	}
	return f.todo, nil
}

func TestMCPCreateServicePrepareBindsAccessTargetAndCopiesProjectContext(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "bound")
	trace := []string{}
	access := &mcpCreateAccessFake{
		projectContext: store.ProjectContext{
			Project: store.Project{ID: 7, Slug: "canonical"},
			Role:    store.RoleMaintainer,
		},
		trace: &trace,
	}
	lookup := &mcpCreateLookupFake{trace: &trace}
	creates := &mcpCreateStoreFake{trace: &trace}
	service := NewMCPCreateService(MCPCreateServiceDependencies{Access: access, Lookup: lookup, Create: creates})
	target := SlugCreateTarget{Slug: "requested", Mode: store.ModeFull}

	prepared, err := service.Prepare(ctx, target)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(access.calls) != 1 {
		t.Fatalf("access calls = %d, want 1", len(access.calls))
	}
	call := access.calls[0]
	if call.ctx != ctx || call.ctx.Value(key) != "bound" || call.slug != "requested" || call.mode != store.ModeFull {
		t.Fatalf("access call = %+v, want bound context, requested slug, full mode", call)
	}
	if len(lookup.calls) != 0 || len(creates.calls) != 0 {
		t.Fatalf("preparation performed data work: lookups=%d creates=%d", len(lookup.calls), len(creates.calls))
	}

	access.projectContext.Project.ID = 99
	access.projectContext.Project.Slug = "mutated"
	access.projectContext.Role = store.RoleViewer
	target.Slug = "mutated"
	target.Mode = store.ModeAnonymous
	if prepared.projectContext.Project.ID != 7 || prepared.projectContext.Project.Slug != "canonical" || prepared.projectContext.Role != store.RoleMaintainer || prepared.mode != store.ModeFull {
		t.Fatalf("prepared target changed after source mutation: %+v mode=%q", prepared.projectContext, prepared.mode)
	}
	if !reflect.DeepEqual(trace, []string{"access"}) {
		t.Fatalf("call trace = %#v, want access only", trace)
	}
}

func TestMCPCreateServiceAccessFailureReturnsNoCapability(t *testing.T) {
	wantErr := errors.New("access failed")
	trace := []string{}
	access := &mcpCreateAccessFake{err: wantErr, trace: &trace}
	lookup := &mcpCreateLookupFake{trace: &trace}
	creates := &mcpCreateStoreFake{trace: &trace}
	service := NewMCPCreateService(MCPCreateServiceDependencies{Access: access, Lookup: lookup, Create: creates})

	prepared, err := service.Prepare(context.Background(), SlugCreateTarget{Slug: "hidden", Mode: store.ModeFull})
	if err != wantErr {
		t.Fatalf("Prepare error = %v, want identical error %v", err, wantErr)
	}
	if prepared != nil {
		t.Fatalf("prepared capability = %+v, want nil", prepared)
	}
	if len(access.calls) != 1 || len(lookup.calls) != 0 || len(creates.calls) != 0 {
		t.Fatalf("calls after access failure: access=%d lookup=%d create=%d", len(access.calls), len(lookup.calls), len(creates.calls))
	}
	if !reflect.DeepEqual(trace, []string{"access"}) {
		t.Fatalf("call trace = %#v, want access only", trace)
	}
}

func TestMCPCreateServiceResolvesAnchorsInOrderAndMaterializesOnce(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "bound")
	trace := []string{}
	access := &mcpCreateAccessFake{
		projectContext: store.ProjectContext{Project: store.Project{ID: 7, Slug: "canonical"}, Role: store.RoleMaintainer},
		trace:          &trace,
	}
	lookup := &mcpCreateLookupFake{
		todos: map[int64]store.Todo{
			11: {ID: 101, ProjectID: 7, LocalID: 11, ColumnKey: "doing"},
			12: {ID: 202, ProjectID: 7, LocalID: 12, ColumnKey: "doing"},
		},
		trace: &trace,
	}
	created := store.Todo{ID: 303, ProjectID: 7, LocalID: 13, Title: "created"}
	creates := &mcpCreateStoreFake{todo: created, trace: &trace}
	service := NewMCPCreateService(MCPCreateServiceDependencies{Access: access, Lookup: lookup, Create: creates})
	prepared, err := service.Prepare(ctx, SlugCreateTarget{Slug: "requested", Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	estimation := int64(8)
	assignee := int64(42)
	sprint := int64(12)
	afterLocalID := int64(11)
	beforeLocalID := int64(12)
	tags := []string{"api", "urgent"}
	command := MCPCreateCommand{
		Values: CreateValues{
			Title:            "created",
			Body:             "body",
			Tags:             tags,
			ColumnKey:        "doing",
			EstimationPoints: &estimation,
			AssigneeUserID:   &assignee,
			SprintID:         &sprint,
		},
		AfterLocalID:  &afterLocalID,
		BeforeLocalID: &beforeLocalID,
	}

	result, err := prepared.Create(command)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(lookup.calls) != 2 {
		t.Fatalf("lookup calls = %d, want 2", len(lookup.calls))
	}
	for i, wantLocalID := range []int64{11, 12} {
		call := lookup.calls[i]
		if call.ctx != ctx || call.ctx.Value(key) != "bound" || call.projectID != 7 || call.localID != wantLocalID || call.mode != store.ModeFull {
			t.Fatalf("lookup %d = %+v, want bound project/local/mode", i, call)
		}
	}
	if len(creates.calls) != 1 {
		t.Fatalf("create calls = %d, want 1", len(creates.calls))
	}
	createCall := creates.calls[0]
	if createCall.ctx != ctx || createCall.projectID != 7 || createCall.mode != store.ModeFull {
		t.Fatalf("create target = %+v, want bound context/project/mode", createCall)
	}
	assertMCPCreateInput(t, createCall.input, tags, estimation, assignee, sprint, 101, 202)
	if !reflect.DeepEqual(trace, []string{"access", "lookup:11", "lookup:12", "create"}) {
		t.Fatalf("call trace = %#v, want access, after lookup, before lookup, create", trace)
	}

	tags[0] = "mutated"
	estimation = 80
	assignee = 420
	sprint = 120
	afterLocalID = 111
	beforeLocalID = 112
	if !reflect.DeepEqual(createCall.input.Tags, []string{"api", "urgent"}) ||
		*createCall.input.EstimationPoints != 8 ||
		*createCall.input.AssigneeUserID != 42 ||
		*createCall.input.SprintID != 12 ||
		*createCall.input.AfterID != 101 ||
		*createCall.input.BeforeID != 202 {
		t.Fatalf("store input changed after command mutation: %+v", createCall.input)
	}
	if result.Project.ID != 7 || result.Project.Slug != "canonical" || !reflect.DeepEqual(result.Todo, created) {
		t.Fatalf("result = %+v, want bound project and created todo", result)
	}
}

func TestMCPCreateServiceLocalReferenceValidationAndLookupFailures(t *testing.T) {
	tests := []struct {
		name           string
		command        MCPCreateCommand
		todos          map[int64]store.Todo
		errs           map[int64]error
		wantKind       MCPCreateValidationKind
		wantField      string
		wantLocalID    int64
		wantHasLocalID bool
		wantErr        error
		wantTrace      []string
		wantLookups    int
	}{
		{
			name:        "nonpositive after reference",
			command:     mcpCreateTestCommand(mcpCreateTestInt64(0), nil),
			wantKind:    MCPCreateInvalidLocalReference,
			wantField:   "afterLocalId",
			wantTrace:   []string{"access"},
			wantLookups: 0,
		},
		{
			name:           "missing after wins before validation",
			command:        mcpCreateTestCommand(mcpCreateTestInt64(999), mcpCreateTestInt64(0)),
			wantKind:       MCPCreateInvalidLocalReference,
			wantField:      "afterLocalId",
			wantLocalID:    999,
			wantHasLocalID: true,
			wantTrace:      []string{"access", "lookup:999"},
			wantLookups:    1,
		},
		{
			name:           "wrong column",
			command:        mcpCreateTestCommand(mcpCreateTestInt64(11), nil),
			todos:          map[int64]store.Todo{11: {ID: 101, ProjectID: 7, LocalID: 11, ColumnKey: "testing"}},
			wantKind:       MCPCreateReferenceInWrongColumn,
			wantField:      "afterLocalId",
			wantLocalID:    11,
			wantHasLocalID: true,
			wantTrace:      []string{"access", "lookup:11"},
			wantLookups:    1,
		},
		{
			name:        "unauthorized lookup passes through",
			command:     mcpCreateTestCommand(nil, mcpCreateTestInt64(12)),
			errs:        map[int64]error{12: store.ErrUnauthorized},
			wantErr:     store.ErrUnauthorized,
			wantTrace:   []string{"access", "lookup:12"},
			wantLookups: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trace := []string{}
			access := &mcpCreateAccessFake{
				projectContext: store.ProjectContext{Project: store.Project{ID: 7}},
				trace:          &trace,
			}
			lookup := &mcpCreateLookupFake{todos: tt.todos, errs: tt.errs, trace: &trace}
			creates := &mcpCreateStoreFake{trace: &trace}
			prepared, err := NewMCPCreateService(MCPCreateServiceDependencies{Access: access, Lookup: lookup, Create: creates}).Prepare(
				context.Background(),
				SlugCreateTarget{Slug: "requested", Mode: store.ModeFull},
			)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}

			result, err := prepared.Create(tt.command)
			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Fatalf("Create error = %v, want identical error %v", err, tt.wantErr)
				}
			} else {
				var validationErr *MCPCreateValidationError
				if !errors.As(err, &validationErr) {
					t.Fatalf("Create error = %T %v, want MCPCreateValidationError", err, err)
				}
				if validationErr.Kind != tt.wantKind || validationErr.Field != tt.wantField || validationErr.LocalID != tt.wantLocalID || validationErr.HasLocalID != tt.wantHasLocalID {
					t.Fatalf("validation error = %+v, want kind=%q field=%q local=%d hasLocal=%v", validationErr, tt.wantKind, tt.wantField, tt.wantLocalID, tt.wantHasLocalID)
				}
			}
			if !reflect.DeepEqual(result, CreateResult{}) {
				t.Fatalf("result = %+v, want zero value", result)
			}
			if len(lookup.calls) != tt.wantLookups || len(creates.calls) != 0 {
				t.Fatalf("calls = lookups %d creates %d, want lookups %d creates 0", len(lookup.calls), len(creates.calls), tt.wantLookups)
			}
			if !reflect.DeepEqual(trace, tt.wantTrace) {
				t.Fatalf("call trace = %#v, want %#v", trace, tt.wantTrace)
			}
		})
	}
}

func TestMCPCreateServiceStoreFailureReturnsSameErrorWithoutRetry(t *testing.T) {
	wantErr := errors.New("create failed")
	trace := []string{}
	access := &mcpCreateAccessFake{
		projectContext: store.ProjectContext{Project: store.Project{ID: 7}},
		trace:          &trace,
	}
	lookup := &mcpCreateLookupFake{trace: &trace}
	creates := &mcpCreateStoreFake{err: wantErr, trace: &trace}
	prepared, err := NewMCPCreateService(MCPCreateServiceDependencies{Access: access, Lookup: lookup, Create: creates}).Prepare(
		context.Background(),
		SlugCreateTarget{Slug: "requested", Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	result, err := prepared.Create(mcpCreateTestCommand(nil, nil))
	if err != wantErr {
		t.Fatalf("Create error = %v, want identical error %v", err, wantErr)
	}
	if !reflect.DeepEqual(result, CreateResult{}) {
		t.Fatalf("result = %+v, want zero value", result)
	}
	if len(lookup.calls) != 0 || len(creates.calls) != 1 {
		t.Fatalf("calls = lookups %d creates %d, want 0 and 1", len(lookup.calls), len(creates.calls))
	}
	if !reflect.DeepEqual(trace, []string{"access", "create"}) {
		t.Fatalf("call trace = %#v, want access then create", trace)
	}
}

func TestMCPCreateServiceCancellationAfterPreparationUsesBoundContext(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "bound"))
	trace := []string{}
	access := &mcpCreateAccessFake{
		projectContext: store.ProjectContext{Project: store.Project{ID: 7}},
		trace:          &trace,
	}
	lookup := &mcpCreateLookupFake{trace: &trace}
	creates := &mcpCreateStoreFake{trace: &trace}
	prepared, err := NewMCPCreateService(MCPCreateServiceDependencies{Access: access, Lookup: lookup, Create: creates}).Prepare(
		ctx,
		SlugCreateTarget{Slug: "requested", Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	cancel()

	_, err = prepared.Create(mcpCreateTestCommand(mcpCreateTestInt64(11), nil))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Create error = %v, want context canceled", err)
	}
	if len(lookup.calls) != 1 {
		t.Fatalf("lookup calls = %d, want 1", len(lookup.calls))
	}
	if got := lookup.calls[0].ctx; got != ctx || got.Value(key) != "bound" || !errors.Is(got.Err(), context.Canceled) {
		t.Fatalf("lookup context = %v, want bound cancelled context", got)
	}
	if len(creates.calls) != 0 {
		t.Fatalf("create calls = %d, want 0", len(creates.calls))
	}
	if !reflect.DeepEqual(trace, []string{"access", "lookup:11"}) {
		t.Fatalf("call trace = %#v, want access then lookup", trace)
	}
}

func mcpCreateTestCommand(afterLocalID, beforeLocalID *int64) MCPCreateCommand {
	return MCPCreateCommand{
		Values: CreateValues{
			Title:     "created",
			ColumnKey: "doing",
		},
		AfterLocalID:  afterLocalID,
		BeforeLocalID: beforeLocalID,
	}
}

func mcpCreateTestInt64(value int64) *int64 {
	return &value
}

func assertMCPCreateInput(
	t *testing.T,
	input store.CreateTodoInput,
	wantTags []string,
	wantEstimation int64,
	wantAssignee int64,
	wantSprint int64,
	wantAfter int64,
	wantBefore int64,
) {
	t.Helper()
	if input.Title != "created" || input.Body != "body" || input.ColumnKey != "doing" {
		t.Fatalf("scalar values = title %q body %q column %q", input.Title, input.Body, input.ColumnKey)
	}
	if !reflect.DeepEqual(input.Tags, wantTags) {
		t.Fatalf("tags = %#v, want %#v", input.Tags, wantTags)
	}
	assertCreatePointerValue(t, "estimation", input.EstimationPoints, wantEstimation)
	assertCreatePointerValue(t, "assignee", input.AssigneeUserID, wantAssignee)
	assertCreatePointerValue(t, "sprint", input.SprintID, wantSprint)
	assertCreatePointerValue(t, "after todo ID", input.AfterID, wantAfter)
	assertCreatePointerValue(t, "before todo ID", input.BeforeID, wantBefore)
	if len(input.Tags) > 0 && len(wantTags) > 0 && &input.Tags[0] == &wantTags[0] {
		t.Fatal("materialized tags alias the MCP command")
	}
}
