package todo

import (
	"context"
	"errors"
	"reflect"
	"testing"

	apprefresh "scrumboy/internal/application/refresh"
	"scrumboy/internal/store"
)

type restCreateStoreCall struct {
	ctx       context.Context
	projectID int64
	input     store.CreateTodoInput
	mode      store.Mode
}

type restCreateStoreFake struct {
	calls []restCreateStoreCall
	todo  store.Todo
	err   error
	trace *[]string
}

func (f *restCreateStoreFake) CreateTodo(
	ctx context.Context,
	projectID int64,
	input store.CreateTodoInput,
	mode store.Mode,
) (store.Todo, error) {
	f.calls = append(f.calls, restCreateStoreCall{
		ctx:       ctx,
		projectID: projectID,
		input:     input,
		mode:      mode,
	})
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

type restCreateRefreshCall struct {
	ctx       context.Context
	projectID int64
	reason    string
	entity    apprefresh.Entity
}

type restCreateRefreshFake struct {
	calls []restCreateRefreshCall
	trace *[]string
}

func (f *restCreateRefreshFake) PublishBoardRefresh(ctx context.Context, projectID int64, reason string, entity apprefresh.Entity) {
	f.calls = append(f.calls, restCreateRefreshCall{ctx: ctx, projectID: projectID, reason: reason, entity: entity})
	if f.trace != nil {
		*f.trace = append(*f.trace, "refresh")
	}
}

func TestCreateServicePreparedCreateBindsTargetAndMaterializesAdapterPreparedCommand(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "bound")

	estimation := int64(8)
	assignee := int64(42)
	sprint := int64(12)
	afterTodoID := int64(104)
	beforeTodoID := int64(209)
	tags := []string{"api", "urgent"}
	command := CreateCommand{
		Values: CreateValues{
			Title:            "create title",
			Body:             "create body",
			Tags:             tags,
			ColumnKey:        "doing",
			EstimationPoints: &estimation,
			AssigneeUserID:   &assignee,
			SprintID:         &sprint,
		},
		Position: ResolvedCreatePosition{
			AfterTodoID:  &afterTodoID,
			BeforeTodoID: &beforeTodoID,
		},
	}
	created := store.Todo{ID: 71, ProjectID: 7, LocalID: 4, Title: "create title", AssignmentChanged: false}
	trace := []string{}
	creates := &restCreateStoreFake{todo: created, trace: &trace}
	refresh := &restCreateRefreshFake{trace: &trace}
	service := NewCreateService(CreateServiceDependencies{Create: creates, Refresh: refresh})
	target := ResolvedCreateTarget{
		ProjectContext: store.ProjectContext{
			Project: store.Project{ID: 7, Slug: "canonical"},
			Role:    store.RoleMaintainer,
		},
		Mode: store.ModeFull,
	}
	prepared := service.Prepare(ctx, target)

	target.ProjectContext.Project.ID = 99
	target.ProjectContext.Project.Slug = "mutated"
	target.ProjectContext.Role = store.RoleViewer
	target.Mode = store.ModeAnonymous

	result, err := prepared.Create(command)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(creates.calls) != 1 {
		t.Fatalf("create calls = %d, want 1", len(creates.calls))
	}
	call := creates.calls[0]
	if call.ctx != ctx || call.ctx.Value(key) != "bound" {
		t.Fatalf("create context = %v, want prepared context", call.ctx)
	}
	if call.projectID != 7 || call.mode != store.ModeFull {
		t.Fatalf("create target = project %d mode %q, want project 7 mode %q", call.projectID, call.mode, store.ModeFull)
	}
	if prepared.projectContext.Role != store.RoleMaintainer {
		t.Fatalf("prepared role = %q, want %q", prepared.projectContext.Role, store.RoleMaintainer)
	}
	assertAdapterPreparedCreateInput(t, call.input, tags, estimation, assignee, sprint, afterTodoID, beforeTodoID)

	tags[0] = "mutated"
	estimation = 80
	assignee = 420
	sprint = 120
	afterTodoID = 1040
	beforeTodoID = 2090
	if !reflect.DeepEqual(call.input.Tags, []string{"api", "urgent"}) ||
		*call.input.EstimationPoints != 8 ||
		*call.input.AssigneeUserID != 42 ||
		*call.input.SprintID != 12 ||
		*call.input.AfterID != 104 ||
		*call.input.BeforeID != 209 {
		t.Fatalf("store input changed after command mutation: %+v", call.input)
	}

	if result.Project.ID != 7 || result.Project.Slug != "canonical" || !reflect.DeepEqual(result.Todo, created) {
		t.Fatalf("result = %+v, want copied project and created todo", result)
	}
	if len(refresh.calls) != 1 {
		t.Fatalf("refresh calls = %d, want 1", len(refresh.calls))
	}
	if got := refresh.calls[0]; got.ctx != ctx || got.projectID != 7 || got.reason != RefreshReasonTodoCreated || got.entity != (apprefresh.Entity{LocalID: 4, Title: "create title"}) {
		t.Fatalf("refresh call = %+v, want bound context, project 7, reason %q, entity #4 create title", got, RefreshReasonTodoCreated)
	}
	if !reflect.DeepEqual(trace, []string{"create", "refresh"}) {
		t.Fatalf("call trace = %#v, want create then refresh", trace)
	}
}

func TestCreateServiceAssignmentChangedSuppressesDirectRefreshDespiteNilRequestedAssignee(t *testing.T) {
	created := store.Todo{ID: 81, ProjectID: 7, LocalID: 5, AssignmentChanged: true}
	trace := []string{}
	creates := &restCreateStoreFake{todo: created, trace: &trace}
	refresh := &restCreateRefreshFake{trace: &trace}
	prepared := NewCreateService(CreateServiceDependencies{Create: creates, Refresh: refresh}).Prepare(
		context.Background(),
		ResolvedCreateTarget{ProjectContext: store.ProjectContext{Project: store.Project{ID: 7}}, Mode: store.ModeFull},
	)

	result, err := prepared.Create(CreateCommand{Values: CreateValues{
		Title:          "created",
		ColumnKey:      "backlog",
		AssigneeUserID: nil,
	}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(creates.calls) != 1 {
		t.Fatalf("create calls = %d, want 1", len(creates.calls))
	}
	if len(refresh.calls) != 0 {
		t.Fatalf("direct refresh calls = %d, want 0 for AssignmentChanged", len(refresh.calls))
	}
	if !reflect.DeepEqual(trace, []string{"create"}) {
		t.Fatalf("call trace = %#v, want only create", trace)
	}
	if result.Project.ID != 7 || !reflect.DeepEqual(result.Todo, created) {
		t.Fatalf("result = %+v, want successful assigned create", result)
	}
}

func TestCreateServiceStoreFailureReturnsSameErrorAndSkipsRefresh(t *testing.T) {
	wantErr := errors.New("create failed")
	trace := []string{}
	creates := &restCreateStoreFake{err: wantErr, trace: &trace}
	refresh := &restCreateRefreshFake{trace: &trace}
	prepared := NewCreateService(CreateServiceDependencies{Create: creates, Refresh: refresh}).Prepare(
		context.Background(),
		ResolvedCreateTarget{ProjectContext: store.ProjectContext{Project: store.Project{ID: 7}}, Mode: store.ModeFull},
	)

	result, err := prepared.Create(CreateCommand{Values: CreateValues{Title: "created", ColumnKey: "backlog"}})
	if err != wantErr {
		t.Fatalf("Create error = %v, want identical error %v", err, wantErr)
	}
	if !reflect.DeepEqual(result, CreateResult{}) {
		t.Fatalf("result = %+v, want zero value", result)
	}
	if len(creates.calls) != 1 {
		t.Fatalf("create calls = %d, want 1", len(creates.calls))
	}
	if len(refresh.calls) != 0 {
		t.Fatalf("refresh calls = %d, want 0", len(refresh.calls))
	}
	if !reflect.DeepEqual(trace, []string{"create"}) {
		t.Fatalf("call trace = %#v, want only create", trace)
	}
}

func TestCreateServiceCancellationAfterPreparationUsesBoundContext(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "bound"))
	trace := []string{}
	creates := &restCreateStoreFake{trace: &trace}
	refresh := &restCreateRefreshFake{trace: &trace}
	prepared := NewCreateService(CreateServiceDependencies{Create: creates, Refresh: refresh}).Prepare(
		ctx,
		ResolvedCreateTarget{ProjectContext: store.ProjectContext{Project: store.Project{ID: 7}}, Mode: store.ModeFull},
	)
	cancel()

	_, err := prepared.Create(CreateCommand{Values: CreateValues{Title: "created", ColumnKey: "backlog"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Create error = %v, want context canceled", err)
	}
	if len(creates.calls) != 1 {
		t.Fatalf("create calls = %d, want 1", len(creates.calls))
	}
	if got := creates.calls[0].ctx; got != ctx || got.Value(key) != "bound" || !errors.Is(got.Err(), context.Canceled) {
		t.Fatalf("store context = %v, want bound cancelled context", got)
	}
	if len(refresh.calls) != 0 {
		t.Fatalf("refresh calls = %d, want 0", len(refresh.calls))
	}
	if !reflect.DeepEqual(trace, []string{"create"}) {
		t.Fatalf("call trace = %#v, want only create", trace)
	}
}

func TestCreateServiceNilRefreshDependencyIsNoOp(t *testing.T) {
	created := store.Todo{ID: 91, ProjectID: 7, LocalID: 6, AssignmentChanged: false}
	creates := &restCreateStoreFake{todo: created}
	prepared := NewCreateService(CreateServiceDependencies{Create: creates}).Prepare(
		context.Background(),
		ResolvedCreateTarget{ProjectContext: store.ProjectContext{Project: store.Project{ID: 7}}, Mode: store.ModeFull},
	)

	result, err := prepared.Create(CreateCommand{Values: CreateValues{Title: "created", ColumnKey: "backlog"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(creates.calls) != 1 {
		t.Fatalf("create calls = %d, want 1", len(creates.calls))
	}
	if !reflect.DeepEqual(result.Todo, created) || result.Project.ID != 7 {
		t.Fatalf("result = %+v, want successful create", result)
	}
}

func assertAdapterPreparedCreateInput(
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
	if input.Title != "create title" || input.Body != "create body" || input.ColumnKey != "doing" {
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
		t.Fatal("materialized tags alias the adapter-prepared command")
	}
}
