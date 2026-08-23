package todo

import (
	"context"
	"errors"
	"fmt"
	"testing"

	apprefresh "scrumboy/internal/application/refresh"
	"scrumboy/internal/store"
)

type moveStoreCall struct {
	ctx           context.Context
	projectID     int64
	localID       int64
	toColumnKey   string
	afterLocalID  *int64
	beforeLocalID *int64
	mode          store.Mode
}

type moveStoreFake struct {
	calls []moveStoreCall
	todo  store.Todo
	err   error
}

func (f *moveStoreFake) MoveTodoByLocalID(
	ctx context.Context,
	projectID int64,
	localID int64,
	toColumnKey string,
	afterLocalID *int64,
	beforeLocalID *int64,
	mode store.Mode,
) (store.Todo, error) {
	f.calls = append(f.calls, moveStoreCall{
		ctx:           ctx,
		projectID:     projectID,
		localID:       localID,
		toColumnKey:   toColumnKey,
		afterLocalID:  afterLocalID,
		beforeLocalID: beforeLocalID,
		mode:          mode,
	})
	if f.err != nil {
		return store.Todo{}, f.err
	}
	if err := ctx.Err(); err != nil {
		return store.Todo{}, err
	}
	return f.todo, nil
}

type refreshCall struct {
	ctx       context.Context
	projectID int64
	reason    string
	entity    apprefresh.Entity
}

type refreshPublisherFake struct {
	calls []refreshCall
}

func (f *refreshPublisherFake) PublishBoardRefresh(ctx context.Context, projectID int64, reason string, entity apprefresh.Entity) {
	f.calls = append(f.calls, refreshCall{ctx: ctx, projectID: projectID, reason: reason, entity: entity})
}

func TestMoveServicePreparedMoveBindsContextAndPublishesOnce(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "bound")

	moves := &moveStoreFake{todo: store.Todo{ID: 71, ProjectID: 7, LocalID: 4, Title: "moved card", ColumnKey: "doing"}}
	refresh := &refreshPublisherFake{}
	service := NewMoveService(MoveServiceDependencies{Move: moves, Refresh: refresh})
	pc := store.ProjectContext{Project: store.Project{ID: 7, Slug: "canonical"}}
	prepared := service.Prepare(ctx, ResolvedMoveTarget{ProjectContext: pc, Mode: store.ModeFull})

	// PreparedMove owns a value copy of the authorized project context.
	pc.Project.ID = 99
	after := int64(3)
	result, err := prepared.Move(MoveCommand{
		LocalID:      4,
		ToColumnKey:  "doing",
		AfterLocalID: &after,
	})
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if result.Project.ID != 7 || result.Todo.ID != 71 {
		t.Fatalf("result = %+v, want project 7 and todo 71", result)
	}
	if len(moves.calls) != 1 {
		t.Fatalf("move calls = %d, want 1", len(moves.calls))
	}
	call := moves.calls[0]
	if call.ctx.Value(key) != "bound" || call.projectID != 7 || call.localID != 4 || call.toColumnKey != "doing" || call.mode != store.ModeFull {
		t.Fatalf("move call = %+v, want bound context and exact command", call)
	}
	if call.afterLocalID == nil || *call.afterLocalID != 3 || call.beforeLocalID != nil {
		t.Fatalf("move anchors = after %v before %v", call.afterLocalID, call.beforeLocalID)
	}
	if len(refresh.calls) != 1 {
		t.Fatalf("refresh calls = %d, want 1", len(refresh.calls))
	}
	if got := refresh.calls[0]; got.ctx.Value(key) != "bound" || got.projectID != 7 || got.reason != RefreshReasonTodoMoved || got.entity != (apprefresh.Entity{LocalID: 4, Title: "moved card"}) {
		t.Fatalf("refresh call = %+v, want project 7 reason %q entity #4 moved card", got, RefreshReasonTodoMoved)
	}
}

func TestMoveServiceStoreFailureAndCancellationSkipRefresh(t *testing.T) {
	tests := []struct {
		name    string
		prepare func() (context.Context, error)
	}{
		{
			name: "store failure",
			prepare: func() (context.Context, error) {
				return context.Background(), store.ErrConflict
			},
		},
		{
			name: "cancelled prepared context",
			prepare: func() (context.Context, error) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, moveErr := tt.prepare()
			moves := &moveStoreFake{err: moveErr}
			refresh := &refreshPublisherFake{}
			prepared := NewMoveService(MoveServiceDependencies{Move: moves, Refresh: refresh}).Prepare(
				ctx,
				ResolvedMoveTarget{ProjectContext: store.ProjectContext{Project: store.Project{ID: 7}}, Mode: store.ModeFull},
			)

			_, err := prepared.Move(MoveCommand{LocalID: 1, ToColumnKey: "doing"})
			if moveErr != nil && !errors.Is(err, moveErr) {
				t.Fatalf("Move error = %v, want %v", err, moveErr)
			}
			if moveErr == nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("Move error = %v, want context canceled", err)
			}
			if len(refresh.calls) != 0 {
				t.Fatalf("refresh calls = %d, want 0", len(refresh.calls))
			}
		})
	}
}

func TestMoveServiceInvalidAgendaColumnKeyPropagatesWithoutRefresh(t *testing.T) {
	moves := &moveStoreFake{err: fmt.Errorf("%w: invalid columnKey", store.ErrValidation)}
	refresh := &refreshPublisherFake{}
	prepared := NewMoveService(MoveServiceDependencies{Move: moves, Refresh: refresh}).Prepare(
		context.Background(),
		ResolvedMoveTarget{ProjectContext: store.ProjectContext{Project: store.Project{ID: 7}}, Mode: store.ModeFull},
	)

	_, err := prepared.Move(MoveCommand{LocalID: 1, ToColumnKey: "agenda"})
	if !errors.Is(err, store.ErrValidation) {
		t.Fatalf("Move error = %v, want ErrValidation", err)
	}
	if len(moves.calls) != 1 || moves.calls[0].toColumnKey != "agenda" {
		t.Fatalf("move calls = %+v, want one agenda target", moves.calls)
	}
	if len(refresh.calls) != 0 {
		t.Fatalf("refresh calls = %d, want 0", len(refresh.calls))
	}
}

type mcpMoveAccessFake struct {
	ctx   context.Context
	slug  string
	mode  store.Mode
	pc    store.ProjectContext
	err   error
	calls int
}

func (f *mcpMoveAccessFake) GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error) {
	f.calls++
	f.ctx = ctx
	f.slug = slug
	f.mode = mode
	return f.pc, f.err
}

type mcpMoveLookupFake struct {
	ctx    context.Context
	todos  map[int64]store.Todo
	errors map[int64]error
	calls  []int64
}

func (f *mcpMoveLookupFake) GetTodoByLocalID(ctx context.Context, _ int64, localID int64, _ store.Mode) (store.Todo, error) {
	f.ctx = ctx
	f.calls = append(f.calls, localID)
	if err := ctx.Err(); err != nil {
		return store.Todo{}, err
	}
	if err := f.errors[localID]; err != nil {
		return store.Todo{}, err
	}
	return f.todos[localID], nil
}

type laneCall struct {
	ctx       context.Context
	projectID int64
	columnKey string
	afterA    int64
	afterB    int64
	sortOrder store.SortOrder
}

type mcpMoveLaneFake struct {
	calls []laneCall
	items []store.Todo
	err   error
}

func (f *mcpMoveLaneFake) ListTodosForBoardLane(
	ctx context.Context,
	projectID int64,
	columnKey string,
	_ int,
	afterA int64,
	afterB int64,
	_ string,
	_ string,
	_ store.AssigneeFilter,
	_ store.PriorityFilter,
	_ store.SprintFilter,
	sortOrder store.SortOrder,
) ([]store.Todo, string, bool, error) {
	f.calls = append(f.calls, laneCall{
		ctx:       ctx,
		projectID: projectID,
		columnKey: columnKey,
		afterA:    afterA,
		afterB:    afterB,
		sortOrder: sortOrder,
	})
	return f.items, "", false, f.err
}

func newMCPMoveHarness() (*MCPMoveService, *mcpMoveAccessFake, *mcpMoveLookupFake, *mcpMoveLaneFake, *moveStoreFake) {
	access := &mcpMoveAccessFake{pc: store.ProjectContext{Project: store.Project{ID: 7, Slug: "canonical"}}}
	lookup := &mcpMoveLookupFake{
		todos: map[int64]store.Todo{
			1: {ID: 101, ProjectID: 7, LocalID: 1, ColumnKey: "backlog", Rank: 1000},
			2: {ID: 102, ProjectID: 7, LocalID: 2, ColumnKey: "doing", Rank: 2000},
		},
		errors: map[int64]error{},
	}
	lanes := &mcpMoveLaneFake{}
	moves := &moveStoreFake{todo: store.Todo{ID: 101, ProjectID: 7, LocalID: 1, ColumnKey: "doing"}}
	service := NewMCPMoveService(MCPMoveServiceDependencies{
		Access: access,
		Lookup: lookup,
		Lanes:  lanes,
		Move:   moves,
	})
	return service, access, lookup, lanes, moves
}

func TestMCPMoveServicePrepareFailureReturnsNoCapability(t *testing.T) {
	service, access, _, _, _ := newMCPMoveHarness()
	access.err = store.ErrNotFound

	prepared, err := service.Prepare(context.Background(), SlugMoveTarget{Slug: "missing", Mode: store.ModeFull})
	if prepared != nil || !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Prepare = (%v, %v), want nil and not found", prepared, err)
	}
	if access.calls != 1 || access.slug != "missing" || access.mode != store.ModeFull {
		t.Fatalf("access = calls %d slug %q mode %q", access.calls, access.slug, access.mode)
	}
}

func TestMCPMoveServicePreservesMissingColumnPrecedence(t *testing.T) {
	service, _, lookup, lanes, moves := newMCPMoveHarness()
	prepared, err := service.Prepare(context.Background(), SlugMoveTarget{Slug: "canonical", Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	_, err = prepared.Move(MoveCommand{LocalID: 1})
	var validationErr *MCPMoveValidationError
	if !errors.As(err, &validationErr) || validationErr.Kind != MCPMoveMissingColumn {
		t.Fatalf("Move error = %v, want missing-column validation", err)
	}
	if len(lookup.calls) != 1 || lookup.calls[0] != 1 {
		t.Fatalf("lookup calls = %v, want moving todo lookup before validation", lookup.calls)
	}
	if len(lanes.calls) != 0 || len(moves.calls) != 0 {
		t.Fatalf("lane calls = %d move calls = %d, want 0/0", len(lanes.calls), len(moves.calls))
	}
}

func TestMCPMoveServiceRejectsAmbiguousAfterAnchorBeforeMove(t *testing.T) {
	service, _, _, lanes, moves := newMCPMoveHarness()
	lanes.items = []store.Todo{{ID: 103, LocalID: 3, ColumnKey: "doing", Rank: 3000}}
	prepared, err := service.Prepare(context.Background(), SlugMoveTarget{Slug: "canonical", Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	after := int64(2)

	_, err = prepared.Move(MoveCommand{LocalID: 1, ToColumnKey: "doing", AfterLocalID: &after})
	var validationErr *MCPMoveValidationError
	if !errors.As(err, &validationErr) || validationErr.Kind != MCPMoveAmbiguousAfterReference || !validationErr.HasLocalID || validationErr.LocalID != 2 {
		t.Fatalf("Move error = %#v, want ambiguous afterLocalId=2", err)
	}
	if len(lanes.calls) != 1 {
		t.Fatalf("lane calls = %d, want 1", len(lanes.calls))
	}
	call := lanes.calls[0]
	if call.projectID != 7 || call.columnKey != "doing" || call.afterA != 2000 || call.afterB != 102 || call.sortOrder != store.SortOrderDefault {
		t.Fatalf("lane call = %+v, want exact after-anchor boundary query", call)
	}
	if len(moves.calls) != 0 {
		t.Fatalf("move calls = %d, want 0", len(moves.calls))
	}
}

func TestMCPMoveServicePreservesAnchorReadErrorSource(t *testing.T) {
	service, _, _, lanes, moves := newMCPMoveHarness()
	lanes.err = store.ErrUnauthorized
	prepared, err := service.Prepare(context.Background(), SlugMoveTarget{Slug: "canonical", Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	after := int64(2)

	_, err = prepared.Move(MoveCommand{LocalID: 1, ToColumnKey: "doing", AfterLocalID: &after})
	var anchorReadErr *MCPMoveAnchorReadError
	if !errors.As(err, &anchorReadErr) || !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("Move error = %#v, want wrapped anchor read authorization error", err)
	}
	if len(lanes.calls) != 1 || len(moves.calls) != 0 {
		t.Fatalf("lane calls = %d move calls = %d, want 1/0", len(lanes.calls), len(moves.calls))
	}
}

func TestMCPMoveServiceNoAnchorSkipsLaneLookupAndMoves(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "bound")
	service, _, lookup, lanes, moves := newMCPMoveHarness()
	prepared, err := service.Prepare(ctx, SlugMoveTarget{Slug: "canonical", Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	result, err := prepared.Move(MoveCommand{LocalID: 1, ToColumnKey: "doing"})
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if result.Project.ID != 7 || result.Todo.LocalID != 1 {
		t.Fatalf("result = %+v", result)
	}
	if lookup.ctx.Value(key) != "bound" || len(lanes.calls) != 0 || len(moves.calls) != 1 {
		t.Fatalf("lookup context = %v lane calls = %d move calls = %d", lookup.ctx.Value(key), len(lanes.calls), len(moves.calls))
	}
}

func TestMCPMoveServiceCancellationAfterPrepareUsesBoundContext(t *testing.T) {
	service, _, _, lanes, moves := newMCPMoveHarness()
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := service.Prepare(ctx, SlugMoveTarget{Slug: "canonical", Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	cancel()

	_, err = prepared.Move(MoveCommand{LocalID: 1, ToColumnKey: "doing"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Move error = %v, want context canceled", err)
	}
	if len(lanes.calls) != 0 || len(moves.calls) != 0 {
		t.Fatalf("lane calls = %d move calls = %d, want 0/0", len(lanes.calls), len(moves.calls))
	}
}
