package board

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type mcpBoardReadCall struct {
	operation      string
	ctx            context.Context
	slug           string
	mode           store.Mode
	projectID      int64
	sprintID       int64
	columnKey      string
	limit          int
	afterA         int64
	afterB         int64
	tagFilter      string
	searchFilter   string
	assigneeFilter store.AssigneeFilter
	sprintFilter   store.SprintFilter
	sortOrder      store.SortOrder
	err            error
}

type mcpBoardReadRecorder struct {
	calls []mcpBoardReadCall
}

func (r *mcpBoardReadRecorder) record(call mcpBoardReadCall) {
	r.calls = append(r.calls, call)
}

func (r *mcpBoardReadRecorder) operations() []string {
	operations := make([]string, 0, len(r.calls))
	for _, call := range r.calls {
		operations = append(operations, call.operation)
	}
	return operations
}

type mcpBoardReadAccessFake struct {
	recorder *mcpBoardReadRecorder
	result   store.ProjectContext
	err      error
}

func (f *mcpBoardReadAccessFake) GetProjectContextBySlug(
	ctx context.Context,
	slug string,
	mode store.Mode,
) (store.ProjectContext, error) {
	f.recorder.record(mcpBoardReadCall{
		operation: "access",
		ctx:       ctx,
		slug:      slug,
		mode:      mode,
	})
	return f.result, f.err
}

type mcpBoardReadSprintFake struct {
	recorder *mcpBoardReadRecorder
	result   store.Sprint
	err      error
}

func (f *mcpBoardReadSprintFake) GetSprintByID(ctx context.Context, sprintID int64) (store.Sprint, error) {
	f.recorder.record(mcpBoardReadCall{
		operation: "sprint",
		ctx:       ctx,
		sprintID:  sprintID,
	})
	return f.result, f.err
}

type mcpBoardReadWorkflowFake struct {
	recorder *mcpBoardReadRecorder
	result   []store.WorkflowColumn
	err      error
}

func (f *mcpBoardReadWorkflowFake) GetProjectWorkflow(
	ctx context.Context,
	projectID int64,
) ([]store.WorkflowColumn, error) {
	f.recorder.record(mcpBoardReadCall{
		operation: "workflow",
		ctx:       ctx,
		projectID: projectID,
	})
	if f.err != nil {
		return nil, f.err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]store.WorkflowColumn(nil), f.result...), nil
}

type mcpBoardReadLanePage struct {
	todos      []store.Todo
	storeToken string
	hasMore    bool
	err        error
}

type mcpBoardReadLaneFake struct {
	recorder   *mcpBoardReadRecorder
	pages      map[string]mcpBoardReadLanePage
	counts     map[string]int
	countErrs  map[string]error
	listErrs   map[string]error
	contextErr bool
}

func (f *mcpBoardReadLaneFake) ListTodosForBoardLane(
	ctx context.Context,
	projectID int64,
	columnKey string,
	limit int,
	afterA int64,
	afterB int64,
	tagFilter string,
	searchFilter string,
	assigneeFilter store.AssigneeFilter,
	sprintFilter store.SprintFilter,
	sortOrder store.SortOrder,
) ([]store.Todo, string, bool, error) {
	f.recorder.record(mcpBoardReadCall{
		operation:      "list:" + columnKey,
		ctx:            ctx,
		projectID:      projectID,
		columnKey:      columnKey,
		limit:          limit,
		afterA:         afterA,
		afterB:         afterB,
		tagFilter:      tagFilter,
		searchFilter:   searchFilter,
		assigneeFilter: assigneeFilter,
		sprintFilter:   sprintFilter,
		sortOrder:      sortOrder,
	})
	if f.contextErr {
		if err := ctx.Err(); err != nil {
			return nil, "", false, err
		}
	}
	if err := f.listErrs[columnKey]; err != nil {
		return nil, "", false, err
	}
	page := f.pages[columnKey]
	return append([]store.Todo(nil), page.todos...), page.storeToken, page.hasMore, page.err
}

func (f *mcpBoardReadLaneFake) CountTodosForBoardLane(
	ctx context.Context,
	projectID int64,
	columnKey string,
	tagFilter string,
	searchFilter string,
	assigneeFilter store.AssigneeFilter,
	sprintFilter store.SprintFilter,
) (int, error) {
	f.recorder.record(mcpBoardReadCall{
		operation:      "count:" + columnKey,
		ctx:            ctx,
		projectID:      projectID,
		columnKey:      columnKey,
		tagFilter:      tagFilter,
		searchFilter:   searchFilter,
		assigneeFilter: assigneeFilter,
		sprintFilter:   sprintFilter,
	})
	if f.contextErr {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
	}
	if err := f.countErrs[columnKey]; err != nil {
		return 0, err
	}
	return f.counts[columnKey], nil
}

type mcpBoardReadActivityFake struct {
	recorder *mcpBoardReadRecorder
	err      error
}

func (f *mcpBoardReadActivityFake) UpdateBoardActivity(ctx context.Context, projectID int64) error {
	f.recorder.record(mcpBoardReadCall{
		operation: "activity",
		ctx:       ctx,
		projectID: projectID,
	})
	if f.err != nil {
		return f.err
	}
	return ctx.Err()
}

type mcpBoardReadHarness struct {
	recorder *mcpBoardReadRecorder
	access   *mcpBoardReadAccessFake
	sprints  *mcpBoardReadSprintFake
	workflow *mcpBoardReadWorkflowFake
	lanes    *mcpBoardReadLaneFake
	activity *mcpBoardReadActivityFake
	service  *MCPBoardReadService
}

func newMCPBoardReadHarness() *mcpBoardReadHarness {
	recorder := &mcpBoardReadRecorder{}
	access := &mcpBoardReadAccessFake{
		recorder: recorder,
		result: store.ProjectContext{
			Project: store.Project{ID: 17, Slug: "stored-slug", Name: "Board", SprintsEnabled: true},
			Role:    store.RoleMaintainer,
		},
	}
	sprints := &mcpBoardReadSprintFake{recorder: recorder}
	workflow := &mcpBoardReadWorkflowFake{
		recorder: recorder,
		result: []store.WorkflowColumn{
			{ProjectID: 17, Key: "triage", Name: "Triage", Position: 1},
			{ProjectID: 17, Key: "shipped", Name: "Shipped", Position: 2, IsDone: true},
		},
	}
	lanes := &mcpBoardReadLaneFake{
		recorder:  recorder,
		pages:     make(map[string]mcpBoardReadLanePage),
		counts:    make(map[string]int),
		countErrs: make(map[string]error),
		listErrs:  make(map[string]error),
	}
	activity := &mcpBoardReadActivityFake{recorder: recorder}
	reportActivityRefreshFailure := func(ctx context.Context, projectID int64, err error) {
		recorder.record(mcpBoardReadCall{
			operation: "activityFailure",
			ctx:       ctx,
			projectID: projectID,
			err:       err,
		})
	}

	return &mcpBoardReadHarness{
		recorder: recorder,
		access:   access,
		sprints:  sprints,
		workflow: workflow,
		lanes:    lanes,
		activity: activity,
		service: NewMCPBoardReadService(MCPBoardReadServiceDependencies{
			Access:                       access,
			Sprints:                      sprints,
			Workflow:                     workflow,
			Lanes:                        lanes,
			Activity:                     activity,
			ReportActivityRefreshFailure: reportActivityRefreshFailure,
		}),
	}
}

func prepareMCPBoardRead(t *testing.T, h *mcpBoardReadHarness, ctx context.Context) *PreparedMCPBoardRead {
	t.Helper()
	prepared, err := h.service.Prepare(ctx, MCPBoardReadTarget{
		Slug: "Caller-Slug ",
		Mode: store.ModeFull,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return prepared
}

func TestMCPBoardReadPrepareBindsContextAndAccess(t *testing.T) {
	t.Run("success binds exact context and target", func(t *testing.T) {
		h := newMCPBoardReadHarness()
		type contextKey struct{}
		ctx := context.WithValue(context.Background(), contextKey{}, "request")

		prepared := prepareMCPBoardRead(t, h, ctx)

		if prepared.ctx != ctx {
			t.Fatal("prepared read did not retain the authorization context")
		}
		if got, want := h.recorder.operations(), []string{"access"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("operations = %v, want %v", got, want)
		}
		call := h.recorder.calls[0]
		if call.ctx != ctx || call.slug != "Caller-Slug " || call.mode != store.ModeFull {
			t.Fatalf("access call = %#v", call)
		}
	})

	t.Run("access failure creates no prepared operation", func(t *testing.T) {
		h := newMCPBoardReadHarness()
		accessErr := errors.New("access failed")
		h.access.err = accessErr

		prepared, err := h.service.Prepare(context.Background(), MCPBoardReadTarget{
			Slug: "denied",
			Mode: store.ModeFull,
		})

		if prepared != nil || !errors.Is(err, accessErr) {
			t.Fatalf("Prepare = (%#v, %v), want nil/access error", prepared, err)
		}
		if got, want := h.recorder.operations(), []string{"access"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("operations = %v, want %v", got, want)
		}
	})

	t.Run("cancellation after preparation reaches read ports", func(t *testing.T) {
		h := newMCPBoardReadHarness()
		ctx, cancel := context.WithCancel(context.Background())
		prepared := prepareMCPBoardRead(t, h, ctx)
		cancel()

		_, err := prepared.Read(MCPBoardReadQuery{Limit: 20})

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Read error = %v, want context canceled", err)
		}
		if got, want := h.recorder.operations(), []string{"access", "workflow"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("operations = %v, want %v", got, want)
		}
		if h.recorder.calls[1].ctx != ctx {
			t.Fatal("workflow did not receive the context bound during preparation")
		}
	})
}

func TestMCPBoardReadSuccessfulOrchestration(t *testing.T) {
	h := newMCPBoardReadHarness()
	expiresAt := time.Now().Add(time.Hour)
	h.access.result.Project.ExpiresAt = &expiresAt
	h.sprints.result = store.Sprint{ID: 91, ProjectID: 17}
	h.lanes.pages["triage"] = mcpBoardReadLanePage{
		todos: []store.Todo{
			{ID: 101, ProjectID: 17, LocalID: 1, ColumnKey: "triage", Rank: 100},
			{ID: 102, ProjectID: 17, LocalID: 2, ColumnKey: "triage", Rank: 200},
		},
		storeToken: "store-token-must-not-be-exposed",
		hasMore:    true,
	}
	h.lanes.pages["shipped"] = mcpBoardReadLanePage{todos: []store.Todo{}}
	h.lanes.counts["triage"] = 5
	h.lanes.counts["shipped"] = 0
	actorID := int64(44)
	assignee, err := store.ParseAssigneeFilter("44", &actorID)
	if err != nil {
		t.Fatalf("ParseAssigneeFilter: %v", err)
	}
	sprintID := int64(91)
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "bound")
	prepared := prepareMCPBoardRead(t, h, ctx)

	result, err := prepared.Read(MCPBoardReadQuery{
		TagFilter:      "tag",
		SearchFilter:   "search",
		AssigneeFilter: assignee,
		SprintID:       &sprintID,
		Limit:          7,
		SortOrder:      store.SortOrderDefault,
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	wantOperations := []string{
		"access",
		"sprint",
		"workflow",
		"list:triage",
		"count:triage",
		"list:shipped",
		"count:shipped",
		"activity",
	}
	if got := h.recorder.operations(); !reflect.DeepEqual(got, wantOperations) {
		t.Fatalf("operations = %v, want %v", got, wantOperations)
	}
	for _, call := range h.recorder.calls {
		if call.ctx != ctx {
			t.Fatalf("%s received a different context", call.operation)
		}
	}

	if result.Project.ID != 17 || result.Project.Slug != "stored-slug" || result.Role != store.RoleMaintainer {
		t.Fatalf("project result = %#v role=%q", result.Project, result.Role)
	}
	if len(result.Columns) != 2 {
		t.Fatalf("columns = %#v, want two", result.Columns)
	}
	if result.Columns[0].Workflow.Key != "triage" || len(result.Columns[0].Todos) != 2 ||
		!result.Columns[0].HasMore || result.Columns[0].TotalCount != 5 {
		t.Fatalf("triage result = %#v", result.Columns[0])
	}
	wantCursor := encodeMCPBoardCursorForTest("200:102")
	if result.Columns[0].NextCursor == nil || *result.Columns[0].NextCursor != wantCursor {
		t.Fatalf("triage next cursor = %#v, want %q", result.Columns[0].NextCursor, wantCursor)
	}
	if result.Columns[1].Workflow.Key != "shipped" || len(result.Columns[1].Todos) != 0 ||
		result.Columns[1].NextCursor != nil || result.Columns[1].HasMore || result.Columns[1].TotalCount != 0 {
		t.Fatalf("shipped result = %#v", result.Columns[1])
	}

	for _, call := range h.recorder.calls {
		if call.operation != "list:triage" && call.operation != "count:triage" &&
			call.operation != "list:shipped" && call.operation != "count:shipped" {
			continue
		}
		if call.projectID != 17 || call.tagFilter != "tag" || call.searchFilter != "search" ||
			!reflect.DeepEqual(call.assigneeFilter, assignee) ||
			call.sprintFilter != (store.SprintFilter{Mode: "sprint", SprintID: 91}) {
			t.Fatalf("%s arguments = %#v", call.operation, call)
		}
	}
	for _, call := range h.recorder.calls {
		if call.operation == "list:triage" || call.operation == "list:shipped" {
			if call.limit != 7 || call.afterA != math.MinInt64 || call.afterB != 0 ||
				call.sortOrder != store.SortOrderDefault {
				t.Fatalf("%s list arguments = %#v", call.operation, call)
			}
		}
	}
}

func TestMCPBoardReadColumnKeyFilter(t *testing.T) {
	t.Run("matching key queries only that lane and omits the rest", func(t *testing.T) {
		h := newMCPBoardReadHarness()
		h.lanes.pages["triage"] = mcpBoardReadLanePage{
			todos: []store.Todo{
				{ID: 101, ProjectID: 17, LocalID: 1, ColumnKey: "triage", Rank: 100},
			},
		}
		h.lanes.counts["triage"] = 5
		prepared := prepareMCPBoardRead(t, h, context.Background())

		result, err := prepared.Read(MCPBoardReadQuery{ColumnKey: "triage", Limit: 20})
		if err != nil {
			t.Fatalf("Read: %v", err)
		}

		wantOperations := []string{"access", "workflow", "list:triage", "count:triage"}
		if got := h.recorder.operations(); !reflect.DeepEqual(got, wantOperations) {
			t.Fatalf("operations = %v, want %v (the shipped lane must not be queried)", got, wantOperations)
		}
		if len(result.Columns) != 1 || result.Columns[0].Workflow.Key != "triage" {
			t.Fatalf("columns = %#v, want only triage", result.Columns)
		}
	})

	t.Run("surrounding whitespace is trimmed before matching", func(t *testing.T) {
		h := newMCPBoardReadHarness()
		prepared := prepareMCPBoardRead(t, h, context.Background())

		result, err := prepared.Read(MCPBoardReadQuery{ColumnKey: "  shipped  ", Limit: 20})
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if len(result.Columns) != 1 || result.Columns[0].Workflow.Key != "shipped" {
			t.Fatalf("columns = %#v, want only shipped", result.Columns)
		}
	})

	t.Run("unknown key fails validation before any lane is queried", func(t *testing.T) {
		h := newMCPBoardReadHarness()
		prepared := prepareMCPBoardRead(t, h, context.Background())

		_, err := prepared.Read(MCPBoardReadQuery{ColumnKey: "does-not-exist", Limit: 20})

		if !errors.Is(err, ErrInvalidMCPBoardColumnKey) {
			t.Fatalf("Read error = %v, want ErrInvalidMCPBoardColumnKey", err)
		}
		wantOperations := []string{"access", "workflow"}
		if got := h.recorder.operations(); !reflect.DeepEqual(got, wantOperations) {
			t.Fatalf("operations = %v, want %v", got, wantOperations)
		}
	})

	t.Run("empty key preserves existing all-columns behavior", func(t *testing.T) {
		h := newMCPBoardReadHarness()
		prepared := prepareMCPBoardRead(t, h, context.Background())

		result, err := prepared.Read(MCPBoardReadQuery{ColumnKey: "", Limit: 20})
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if len(result.Columns) != 2 {
			t.Fatalf("columns = %#v, want both columns", result.Columns)
		}
	})
}

func TestMCPBoardReadSprintSemantics(t *testing.T) {
	t.Run("absent sprint skips lookup", func(t *testing.T) {
		h := newMCPBoardReadHarness()
		h.workflow.result = nil
		prepared := prepareMCPBoardRead(t, h, context.Background())

		result, err := prepared.Read(MCPBoardReadQuery{Limit: 20})

		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if result.Columns == nil {
			t.Fatal("empty workflow must produce an empty, non-nil columns slice")
		}
		if got, want := h.recorder.operations(), []string{"access", "workflow"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("operations = %v, want %v", got, want)
		}
	})

	for _, sprintID := range []int64{0, -1} {
		t.Run("non-positive sprint fails after access", func(t *testing.T) {
			h := newMCPBoardReadHarness()
			prepared := prepareMCPBoardRead(t, h, context.Background())

			_, err := prepared.Read(MCPBoardReadQuery{SprintID: &sprintID, Limit: 20})

			if !errors.Is(err, ErrInvalidMCPBoardSprintID) {
				t.Fatalf("Read error = %v, want invalid sprint sentinel", err)
			}
			if got, want := h.recorder.operations(), []string{"access"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("operations = %v, want %v", got, want)
			}
		})
	}

	t.Run("missing sprint stays not found", func(t *testing.T) {
		h := newMCPBoardReadHarness()
		h.sprints.err = store.ErrNotFound
		sprintID := int64(91)
		prepared := prepareMCPBoardRead(t, h, context.Background())

		_, err := prepared.Read(MCPBoardReadQuery{SprintID: &sprintID, Limit: 20})

		if !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("Read error = %v, want not found", err)
		}
		if got, want := h.recorder.operations(), []string{"access", "sprint"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("operations = %v, want %v", got, want)
		}
	})

	t.Run("cross-project sprint stays not found", func(t *testing.T) {
		h := newMCPBoardReadHarness()
		h.sprints.result = store.Sprint{ID: 91, ProjectID: 999}
		sprintID := int64(91)
		prepared := prepareMCPBoardRead(t, h, context.Background())

		_, err := prepared.Read(MCPBoardReadQuery{SprintID: &sprintID, Limit: 20})

		if !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("Read error = %v, want not found", err)
		}
		if got, want := h.recorder.operations(), []string{"access", "sprint"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("operations = %v, want %v", got, want)
		}
	})

	t.Run("store failure is unchanged", func(t *testing.T) {
		h := newMCPBoardReadHarness()
		storeErr := errors.New("sprint store failed")
		h.sprints.err = storeErr
		sprintID := int64(91)
		prepared := prepareMCPBoardRead(t, h, context.Background())

		_, err := prepared.Read(MCPBoardReadQuery{SprintID: &sprintID, Limit: 20})

		if !errors.Is(err, storeErr) {
			t.Fatalf("Read error = %v, want injected store error", err)
		}
	})
}

func TestMCPBoardReadDisabledSprintProjection(t *testing.T) {
	h := newMCPBoardReadHarness()
	h.access.result.Project.SprintsEnabled = false
	sprintID := int64(91)
	h.lanes.pages["triage"] = mcpBoardReadLanePage{
		todos: []store.Todo{{ID: 101, ProjectID: 17, LocalID: 1, ColumnKey: "triage", SprintID: &sprintID}},
	}
	h.lanes.pages["shipped"] = mcpBoardReadLanePage{todos: []store.Todo{}}
	prepared := prepareMCPBoardRead(t, h, context.Background())

	result, err := prepared.Read(MCPBoardReadQuery{Limit: 20})
	if err != nil {
		t.Fatalf("Read without sprint filter: %v", err)
	}
	if len(result.Columns) == 0 || len(result.Columns[0].Todos) == 0 || result.Columns[0].Todos[0].SprintID != nil {
		t.Fatalf("disabled projection exposed sprint assignment: %+v", result.Columns)
	}

	prepared = prepareMCPBoardRead(t, h, context.Background())
	_, err = prepared.Read(MCPBoardReadQuery{SprintID: &sprintID, Limit: 20})
	if !errors.Is(err, store.ErrSprintsDisabled) {
		t.Fatalf("filtered Read error = %v, want ErrSprintsDisabled", err)
	}
}

func TestMCPBoardReadFailureShortCircuiting(t *testing.T) {
	workflowErr := errors.New("workflow failed")
	listErr := errors.New("list failed")
	countErr := errors.New("count failed")

	tests := []struct {
		name           string
		configure      func(*mcpBoardReadHarness)
		wantErr        error
		wantOperations []string
	}{
		{
			name: "workflow",
			configure: func(h *mcpBoardReadHarness) {
				h.workflow.err = workflowErr
			},
			wantErr:        workflowErr,
			wantOperations: []string{"access", "workflow"},
		},
		{
			name: "first lane list",
			configure: func(h *mcpBoardReadHarness) {
				h.lanes.listErrs["triage"] = listErr
			},
			wantErr:        listErr,
			wantOperations: []string{"access", "workflow", "list:triage"},
		},
		{
			name: "first lane count",
			configure: func(h *mcpBoardReadHarness) {
				h.lanes.countErrs["triage"] = countErr
			},
			wantErr:        countErr,
			wantOperations: []string{"access", "workflow", "list:triage", "count:triage"},
		},
		{
			name: "later lane list",
			configure: func(h *mcpBoardReadHarness) {
				h.lanes.listErrs["shipped"] = listErr
			},
			wantErr: listErr,
			wantOperations: []string{
				"access", "workflow", "list:triage", "count:triage", "list:shipped",
			},
		},
		{
			name: "later lane count",
			configure: func(h *mcpBoardReadHarness) {
				h.lanes.countErrs["shipped"] = countErr
			},
			wantErr: countErr,
			wantOperations: []string{
				"access", "workflow", "list:triage", "count:triage", "list:shipped", "count:shipped",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newMCPBoardReadHarness()
			tt.configure(h)
			prepared := prepareMCPBoardRead(t, h, context.Background())

			_, err := prepared.Read(MCPBoardReadQuery{Limit: 20})

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Read error = %v, want %v", err, tt.wantErr)
			}
			if got := h.recorder.operations(); !reflect.DeepEqual(got, tt.wantOperations) {
				t.Fatalf("operations = %v, want %v", got, tt.wantOperations)
			}
		})
	}
}

func TestMCPBoardReadActivityRefreshFailureIsBestEffortAndReported(t *testing.T) {
	h := newMCPBoardReadHarness()
	expiresAt := time.Now().Add(time.Hour)
	h.access.result.Project.ExpiresAt = &expiresAt
	activityErr := errors.New("activity failed")
	h.activity.err = activityErr
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "request")
	prepared := prepareMCPBoardRead(t, h, ctx)

	result, err := prepared.Read(MCPBoardReadQuery{Limit: 20})

	if err != nil {
		t.Fatalf("Read returned ancillary activity error: %v", err)
	}
	if result.Project.ID != 17 || len(result.Columns) != 2 {
		t.Fatalf("Read result = %#v, want completed board projection", result)
	}
	wantOperations := []string{
		"access", "workflow", "list:triage", "count:triage",
		"list:shipped", "count:shipped", "activity", "activityFailure",
	}
	if got := h.recorder.operations(); !reflect.DeepEqual(got, wantOperations) {
		t.Fatalf("operations = %v, want %v", got, wantOperations)
	}
	report := h.recorder.calls[len(h.recorder.calls)-1]
	if report.ctx != ctx || report.projectID != 17 || !errors.Is(report.err, activityErr) {
		t.Fatalf("activity failure report = %#v, want exact context, project, and cause", report)
	}
}
