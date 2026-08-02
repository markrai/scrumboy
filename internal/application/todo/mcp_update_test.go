package todo

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"scrumboy/internal/store"
)

var (
	_ MCPUpdateAccessStore = (*store.Store)(nil)
	_ MCPUpdateLookupStore = (*store.Store)(nil)
	_ UpdateStore          = (*store.Store)(nil)
)

type mcpUpdateAccessFake struct {
	ctx   context.Context
	slug  string
	mode  store.Mode
	pc    store.ProjectContext
	err   error
	calls int
}

func (f *mcpUpdateAccessFake) GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error) {
	f.calls++
	f.ctx = ctx
	f.slug = slug
	f.mode = mode
	if f.err != nil {
		return store.ProjectContext{}, f.err
	}
	if err := ctx.Err(); err != nil {
		return store.ProjectContext{}, err
	}
	return f.pc, nil
}

type mcpUpdateLookupCall struct {
	ctx       context.Context
	projectID int64
	localID   int64
	mode      store.Mode
}

type mcpUpdateLookupFake struct {
	calls []mcpUpdateLookupCall
	todo  store.Todo
	err   error
}

func (f *mcpUpdateLookupFake) GetTodoByLocalID(ctx context.Context, projectID int64, localID int64, mode store.Mode) (store.Todo, error) {
	f.calls = append(f.calls, mcpUpdateLookupCall{ctx: ctx, projectID: projectID, localID: localID, mode: mode})
	if err := ctx.Err(); err != nil {
		return store.Todo{}, err
	}
	if f.err != nil {
		return store.Todo{}, f.err
	}
	return f.todo, nil
}

type mcpUpdateStoreCall struct {
	ctx       context.Context
	projectID int64
	localID   int64
	input     store.UpdateTodoInput
	mode      store.Mode
}

type mcpUpdateStoreFake struct {
	calls []mcpUpdateStoreCall
	todo  store.Todo
	err   error
}

func (f *mcpUpdateStoreFake) UpdateTodoByLocalID(
	ctx context.Context,
	projectID int64,
	localID int64,
	input store.UpdateTodoInput,
	mode store.Mode,
) (store.Todo, error) {
	f.calls = append(f.calls, mcpUpdateStoreCall{
		ctx:       ctx,
		projectID: projectID,
		localID:   localID,
		input:     input,
		mode:      mode,
	})
	if err := ctx.Err(); err != nil {
		return store.Todo{}, err
	}
	if f.err != nil {
		return store.Todo{}, f.err
	}
	return f.todo, nil
}

func TestMCPUpdateServicePrepareBindsAccessTargetAndCopiesProjectContext(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "bound")
	access := &mcpUpdateAccessFake{pc: store.ProjectContext{
		Project: store.Project{ID: 7, Slug: "canonical"},
		Role:    store.RoleMaintainer,
	}}
	lookup := &mcpUpdateLookupFake{}
	updates := &mcpUpdateStoreFake{}
	service := NewMCPUpdateService(MCPUpdateServiceDependencies{Access: access, Lookup: lookup, Update: updates})

	prepared, err := service.Prepare(ctx, SlugUpdateTarget{Slug: "requested", Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if prepared == nil {
		t.Fatal("Prepare returned nil capability")
	}
	if access.calls != 1 || access.ctx != ctx || access.ctx.Value(key) != "bound" || access.slug != "requested" || access.mode != store.ModeFull {
		t.Fatalf("access = calls %d ctx %v slug %q mode %q", access.calls, access.ctx, access.slug, access.mode)
	}
	if len(lookup.calls) != 0 || len(updates.calls) != 0 {
		t.Fatalf("prepare called later stages: lookup %d update %d", len(lookup.calls), len(updates.calls))
	}

	access.pc.Project.ID = 99
	access.pc.Project.Slug = "mutated"
	access.pc.Role = store.RoleViewer
	if prepared.projectContext.Project.ID != 7 || prepared.projectContext.Project.Slug != "canonical" || prepared.projectContext.Role != store.RoleMaintainer {
		t.Fatalf("prepared project changed with source fixture: %+v", prepared.projectContext)
	}
	if prepared.ctx != ctx || prepared.mode != store.ModeFull {
		t.Fatalf("prepared binding = ctx %v mode %q", prepared.ctx, prepared.mode)
	}
}

func TestMCPUpdateServiceAccessFailureReturnsNoCapability(t *testing.T) {
	wantErr := errors.New("access failed")
	access := &mcpUpdateAccessFake{err: wantErr}
	lookup := &mcpUpdateLookupFake{}
	updates := &mcpUpdateStoreFake{}
	service := NewMCPUpdateService(MCPUpdateServiceDependencies{Access: access, Lookup: lookup, Update: updates})

	prepared, err := service.Prepare(context.Background(), SlugUpdateTarget{Slug: "missing", Mode: store.ModeFull})
	if prepared != nil || err != wantErr {
		t.Fatalf("Prepare = (%v, %v), want nil and identical error", prepared, err)
	}
	if access.calls != 1 || len(lookup.calls) != 0 || len(updates.calls) != 0 {
		t.Fatalf("calls = access %d lookup %d update %d", access.calls, len(lookup.calls), len(updates.calls))
	}
}

func TestPreparedMCPUpdatePrepareTodoCopiesExistingAndEmptyPatchSkipsUpdate(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "bound")
	existing := fullyPopulatedMCPUpdateTodo()
	access := &mcpUpdateAccessFake{pc: store.ProjectContext{Project: store.Project{ID: 7, Slug: "canonical"}}}
	lookup := &mcpUpdateLookupFake{todo: existing}
	updates := &mcpUpdateStoreFake{}
	service := NewMCPUpdateService(MCPUpdateServiceDependencies{Access: access, Lookup: lookup, Update: updates})
	prepared, err := service.Prepare(ctx, SlugUpdateTarget{Slug: "requested", Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	preparedTodo, err := prepared.PrepareTodo(4)
	if err != nil {
		t.Fatalf("PrepareTodo: %v", err)
	}
	if len(lookup.calls) != 1 {
		t.Fatalf("lookup calls = %d, want 1", len(lookup.calls))
	}
	call := lookup.calls[0]
	if call.ctx != ctx || call.ctx.Value(key) != "bound" || call.projectID != 7 || call.localID != 4 || call.mode != store.ModeFull {
		t.Fatalf("lookup call = %+v, want bound target", call)
	}
	if len(updates.calls) != 0 {
		t.Fatalf("update calls before Update = %d, want 0", len(updates.calls))
	}
	assertMCPUpdateTodoDoesNotAlias(t, preparedTodo.existing, lookup.todo)

	lookup.todo.Tags[0] = "mutated tag"
	*lookup.todo.EstimationPoints = 101
	*lookup.todo.AssigneeUserID = 102
	*lookup.todo.SprintID = 103
	*lookup.todo.DoneAt = lookup.todo.DoneAt.Add(time.Hour)

	result, err := preparedTodo.Update(UpdatePatch{})
	if err != nil {
		t.Fatalf("Update empty patch: %v", err)
	}
	if len(updates.calls) != 0 {
		t.Fatalf("empty patch update calls = %d, want 0", len(updates.calls))
	}
	assertMCPUpdateBoundTodoValues(t, result.Todo)
	if result.Project.ID != 7 || result.Project.Slug != "canonical" {
		t.Fatalf("result project = %+v", result.Project)
	}
	assertMCPUpdateTodoDoesNotAlias(t, result.Todo, preparedTodo.existing)
}

func TestPreparedMCPUpdatePrepareTodoFailureReturnsNoCapability(t *testing.T) {
	wantErr := errors.New("lookup failed")
	access := &mcpUpdateAccessFake{pc: store.ProjectContext{Project: store.Project{ID: 7}}}
	lookup := &mcpUpdateLookupFake{err: wantErr}
	updates := &mcpUpdateStoreFake{}
	service := NewMCPUpdateService(MCPUpdateServiceDependencies{Access: access, Lookup: lookup, Update: updates})
	prepared, err := service.Prepare(context.Background(), SlugUpdateTarget{Slug: "project", Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	preparedTodo, err := prepared.PrepareTodo(4)
	if preparedTodo != nil || err != wantErr {
		t.Fatalf("PrepareTodo = (%v, %v), want nil and identical error", preparedTodo, err)
	}
	if len(lookup.calls) != 1 || len(updates.calls) != 0 {
		t.Fatalf("calls = lookup %d update %d", len(lookup.calls), len(updates.calls))
	}
}

func TestPreparedMCPTodoUpdateSparsePatchMaterializesBoundExisting(t *testing.T) {
	existing := fullyPopulatedMCPUpdateTodo()
	updated := store.Todo{
		ID:                81,
		ProjectID:         7,
		LocalID:           4,
		Title:             "existing title",
		Body:              "new body",
		Tags:              []string{"existing-tag"},
		AssignmentChanged: true,
	}
	access := &mcpUpdateAccessFake{pc: store.ProjectContext{Project: store.Project{ID: 7, Slug: "canonical"}}}
	lookup := &mcpUpdateLookupFake{todo: existing}
	updates := &mcpUpdateStoreFake{todo: updated}
	service := NewMCPUpdateService(MCPUpdateServiceDependencies{Access: access, Lookup: lookup, Update: updates})
	prepared, err := service.Prepare(context.Background(), SlugUpdateTarget{Slug: "requested", Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	preparedTodo, err := prepared.PrepareTodo(4)
	if err != nil {
		t.Fatalf("PrepareTodo: %v", err)
	}

	lookup.todo.Tags[0] = "mutated source"
	*lookup.todo.EstimationPoints = 101
	*lookup.todo.AssigneeUserID = 102
	*lookup.todo.SprintID = 103
	result, err := preparedTodo.Update(UpdatePatch{Body: Field[string]{Present: true, Value: "new body"}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updates.calls) != 1 {
		t.Fatalf("update calls = %d, want 1", len(updates.calls))
	}
	call := updates.calls[0]
	if call.projectID != 7 || call.localID != 4 || call.mode != store.ModeFull {
		t.Fatalf("update target = project %d local %d mode %q", call.projectID, call.localID, call.mode)
	}
	if call.input.Title != "existing title" || call.input.Body != "new body" || !reflect.DeepEqual(call.input.Tags, []string{"existing-tag"}) {
		t.Fatalf("sparse replacements = %+v", call.input)
	}
	assertMCPUpdatePointerValue(t, "estimation", call.input.EstimationPoints, 3)
	assertMCPUpdatePointerValue(t, "assignee", call.input.AssigneeUserID, 21)
	if call.input.SprintID != nil || call.input.ClearSprint {
		t.Fatalf("omitted sprint requested mutation: SprintID=%v ClearSprint=%v", call.input.SprintID, call.input.ClearSprint)
	}
	if len(call.input.Tags) > 0 && &call.input.Tags[0] == &preparedTodo.existing.Tags[0] {
		t.Fatal("materialized tags alias the bound todo")
	}
	if call.input.EstimationPoints == preparedTodo.existing.EstimationPoints || call.input.AssigneeUserID == preparedTodo.existing.AssigneeUserID {
		t.Fatal("materialized pointers alias the bound todo")
	}
	if result.Project.ID != 7 || !reflect.DeepEqual(result.Todo, updated) {
		t.Fatalf("result = %+v", result)
	}
}

func TestPreparedMCPTodoUpdateExplicitClearsAndSprintSet(t *testing.T) {
	tests := []struct {
		name   string
		patch  UpdatePatch
		assert func(*testing.T, store.UpdateTodoInput)
	}{
		{
			name: "nullable fields clear",
			patch: UpdatePatch{
				EstimationPoints: Field[*int64]{Present: true},
				AssigneeUserID:   Field[*int64]{Present: true},
				SprintID:         Field[*int64]{Present: true},
			},
			assert: func(t *testing.T, input store.UpdateTodoInput) {
				if input.EstimationPoints != nil || input.AssigneeUserID != nil || input.SprintID != nil || !input.ClearSprint {
					t.Fatalf("clear input = %+v", input)
				}
			},
		},
		{
			name:  "sprint sets",
			patch: UpdatePatch{SprintID: Field[*int64]{Present: true, Value: mcpUpdateInt64(12)}},
			assert: func(t *testing.T, input store.UpdateTodoInput) {
				assertMCPUpdatePointerValue(t, "sprint", input.SprintID, 12)
				if input.ClearSprint {
					t.Fatal("sprint set requested clear")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			access := &mcpUpdateAccessFake{pc: store.ProjectContext{Project: store.Project{ID: 7}}}
			lookup := &mcpUpdateLookupFake{todo: fullyPopulatedMCPUpdateTodo()}
			updates := &mcpUpdateStoreFake{todo: store.Todo{ID: 71, LocalID: 4}}
			service := NewMCPUpdateService(MCPUpdateServiceDependencies{Access: access, Lookup: lookup, Update: updates})
			prepared, err := service.Prepare(context.Background(), SlugUpdateTarget{Slug: "project", Mode: store.ModeFull})
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			preparedTodo, err := prepared.PrepareTodo(4)
			if err != nil {
				t.Fatalf("PrepareTodo: %v", err)
			}
			if _, err := preparedTodo.Update(tt.patch); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if len(updates.calls) != 1 {
				t.Fatalf("update calls = %d, want 1", len(updates.calls))
			}
			tt.assert(t, updates.calls[0].input)
		})
	}
}

func TestPreparedMCPTodoUpdateSemanticNoOpStillPersistsOnce(t *testing.T) {
	existing := fullyPopulatedMCPUpdateTodo()
	access := &mcpUpdateAccessFake{pc: store.ProjectContext{Project: store.Project{ID: 7}}}
	lookup := &mcpUpdateLookupFake{todo: existing}
	updates := &mcpUpdateStoreFake{todo: existing}
	service := NewMCPUpdateService(MCPUpdateServiceDependencies{Access: access, Lookup: lookup, Update: updates})
	prepared, err := service.Prepare(context.Background(), SlugUpdateTarget{Slug: "project", Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	preparedTodo, err := prepared.PrepareTodo(4)
	if err != nil {
		t.Fatalf("PrepareTodo: %v", err)
	}

	result, err := preparedTodo.Update(UpdatePatch{Title: Field[string]{Present: true, Value: existing.Title}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updates.calls) != 1 {
		t.Fatalf("semantic no-op update calls = %d, want 1", len(updates.calls))
	}
	if result.Project.ID != 7 || result.Todo.ID != existing.ID {
		t.Fatalf("result = %+v", result)
	}
}

func TestPreparedMCPTodoUpdateStoreFailureReturnsSameErrorWithoutRetry(t *testing.T) {
	wantErr := errors.New("update failed")
	access := &mcpUpdateAccessFake{pc: store.ProjectContext{Project: store.Project{ID: 7}}}
	lookup := &mcpUpdateLookupFake{todo: fullyPopulatedMCPUpdateTodo()}
	updates := &mcpUpdateStoreFake{err: wantErr}
	service := NewMCPUpdateService(MCPUpdateServiceDependencies{Access: access, Lookup: lookup, Update: updates})
	prepared, err := service.Prepare(context.Background(), SlugUpdateTarget{Slug: "project", Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	preparedTodo, err := prepared.PrepareTodo(4)
	if err != nil {
		t.Fatalf("PrepareTodo: %v", err)
	}

	result, err := preparedTodo.Update(UpdatePatch{Body: Field[string]{Present: true, Value: "changed"}})
	if err != wantErr {
		t.Fatalf("Update error = %v, want identical %v", err, wantErr)
	}
	if !reflect.DeepEqual(result, UpdateResult{}) {
		t.Fatalf("result = %+v, want zero value", result)
	}
	if len(updates.calls) != 1 {
		t.Fatalf("update calls = %d, want 1", len(updates.calls))
	}
}

func TestMCPUpdateServiceCancellationAfterAccessUsesBoundContext(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "bound"))
	access := &mcpUpdateAccessFake{pc: store.ProjectContext{Project: store.Project{ID: 7}}}
	lookup := &mcpUpdateLookupFake{todo: fullyPopulatedMCPUpdateTodo()}
	updates := &mcpUpdateStoreFake{}
	service := NewMCPUpdateService(MCPUpdateServiceDependencies{Access: access, Lookup: lookup, Update: updates})
	prepared, err := service.Prepare(ctx, SlugUpdateTarget{Slug: "project", Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	cancel()

	preparedTodo, err := prepared.PrepareTodo(4)
	if preparedTodo != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("PrepareTodo = (%v, %v), want nil and context canceled", preparedTodo, err)
	}
	if len(lookup.calls) != 1 {
		t.Fatalf("lookup calls = %d, want 1", len(lookup.calls))
	}
	if got := lookup.calls[0].ctx; got != ctx || got.Value(key) != "bound" || !errors.Is(got.Err(), context.Canceled) {
		t.Fatalf("lookup context = %v, want bound cancelled context", got)
	}
	if len(updates.calls) != 0 {
		t.Fatalf("update calls = %d, want 0", len(updates.calls))
	}
}

func TestMCPUpdateServiceCancellationAfterTodoPreparationUsesBoundContext(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "bound"))
	access := &mcpUpdateAccessFake{pc: store.ProjectContext{Project: store.Project{ID: 7}}}
	lookup := &mcpUpdateLookupFake{todo: fullyPopulatedMCPUpdateTodo()}
	updates := &mcpUpdateStoreFake{}
	service := NewMCPUpdateService(MCPUpdateServiceDependencies{Access: access, Lookup: lookup, Update: updates})
	prepared, err := service.Prepare(ctx, SlugUpdateTarget{Slug: "project", Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	preparedTodo, err := prepared.PrepareTodo(4)
	if err != nil {
		t.Fatalf("PrepareTodo: %v", err)
	}
	cancel()

	_, err = preparedTodo.Update(UpdatePatch{Body: Field[string]{Present: true, Value: "changed"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Update error = %v, want context canceled", err)
	}
	if len(updates.calls) != 1 {
		t.Fatalf("update calls = %d, want 1", len(updates.calls))
	}
	if got := updates.calls[0].ctx; got != ctx || got.Value(key) != "bound" || !errors.Is(got.Err(), context.Canceled) {
		t.Fatalf("update context = %v, want bound cancelled context", got)
	}
}

func fullyPopulatedMCPUpdateTodo() store.Todo {
	estimation := int64(3)
	assignee := int64(21)
	sprint := int64(5)
	doneAt := time.Unix(1700000000, 0)
	return store.Todo{
		ID:               71,
		ProjectID:        7,
		LocalID:          4,
		Title:            "existing title",
		Body:             "existing body",
		Tags:             []string{"existing-tag"},
		EstimationPoints: &estimation,
		AssigneeUserID:   &assignee,
		SprintID:         &sprint,
		DoneAt:           &doneAt,
	}
}

func assertMCPUpdateBoundTodoValues(t *testing.T, todo store.Todo) {
	t.Helper()
	if todo.Title != "existing title" || todo.Body != "existing body" || !reflect.DeepEqual(todo.Tags, []string{"existing-tag"}) {
		t.Fatalf("bound todo replacements = %+v", todo)
	}
	assertMCPUpdatePointerValue(t, "estimation", todo.EstimationPoints, 3)
	assertMCPUpdatePointerValue(t, "assignee", todo.AssigneeUserID, 21)
	assertMCPUpdatePointerValue(t, "sprint", todo.SprintID, 5)
	if todo.DoneAt == nil || !todo.DoneAt.Equal(time.Unix(1700000000, 0)) {
		t.Fatalf("doneAt = %v, want original value", todo.DoneAt)
	}
}

func assertMCPUpdateTodoDoesNotAlias(t *testing.T, got, source store.Todo) {
	t.Helper()
	if len(got.Tags) > 0 && len(source.Tags) > 0 && &got.Tags[0] == &source.Tags[0] {
		t.Fatal("tags alias source todo")
	}
	if got.EstimationPoints == source.EstimationPoints || got.AssigneeUserID == source.AssigneeUserID || got.SprintID == source.SprintID || got.DoneAt == source.DoneAt {
		t.Fatal("pointer field aliases source todo")
	}
}

func assertMCPUpdatePointerValue(t *testing.T, name string, got *int64, want int64) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("%s = %v, want %d", name, got, want)
	}
}

func mcpUpdateInt64(value int64) *int64 {
	return &value
}
