package todo

import (
	"context"
	"errors"
	"reflect"
	"testing"

	apprefresh "scrumboy/internal/application/refresh"
	"scrumboy/internal/store"
)

var _ LegacyMoveStore = (*store.Store)(nil)

type legacyMoveStoreCall struct {
	ctx          context.Context
	todoID       int64
	toColumnKey  string
	afterTodoID  *int64
	beforeTodoID *int64
	mode         store.Mode
}

type legacyMoveStoreFake struct {
	calls []legacyMoveStoreCall
	todo  store.Todo
	err   error
	trace *[]string
}

func (f *legacyMoveStoreFake) MoveTodo(
	ctx context.Context,
	todoID int64,
	toColumnKey string,
	afterTodoID *int64,
	beforeTodoID *int64,
	mode store.Mode,
) (store.Todo, error) {
	f.calls = append(f.calls, legacyMoveStoreCall{
		ctx:          ctx,
		todoID:       todoID,
		toColumnKey:  toColumnKey,
		afterTodoID:  afterTodoID,
		beforeTodoID: beforeTodoID,
		mode:         mode,
	})
	if f.trace != nil {
		*f.trace = append(*f.trace, "move")
	}
	if f.err != nil {
		return store.Todo{}, f.err
	}
	if err := ctx.Err(); err != nil {
		return store.Todo{}, err
	}
	return f.todo, nil
}

type legacyMoveRefreshCall struct {
	ctx       context.Context
	projectID int64
	reason    string
	entity    apprefresh.Entity
}

type legacyMoveRefreshFake struct {
	calls []legacyMoveRefreshCall
	trace *[]string
}

func (f *legacyMoveRefreshFake) PublishBoardRefresh(ctx context.Context, projectID int64, reason string, entity apprefresh.Entity) {
	f.calls = append(f.calls, legacyMoveRefreshCall{ctx: ctx, projectID: projectID, reason: reason, entity: entity})
	if f.trace != nil {
		*f.trace = append(*f.trace, "refresh")
	}
}

func legacyMoveInt64(value int64) *int64 {
	return &value
}

func TestLegacyMoveServicePrepareBindsContextModeWithoutPersistence(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "bound")
	moves := &legacyMoveStoreFake{todo: store.Todo{ID: 71, ProjectID: 9}}
	service := NewLegacyMoveService(LegacyMoveServiceDependencies{Move: moves})
	target := LegacyMoveTarget{Mode: store.ModeAnonymous}

	prepared := service.Prepare(ctx, target)
	if len(moves.calls) != 0 {
		t.Fatalf("Prepare made %d persistence calls, want 0", len(moves.calls))
	}
	target.Mode = store.ModeFull

	result, err := prepared.Move(LegacyMoveCommand{TodoID: 0})
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if len(moves.calls) != 1 {
		t.Fatalf("move calls = %d, want 1", len(moves.calls))
	}
	call := moves.calls[0]
	if call.ctx != ctx || call.ctx.Value(key) != "bound" {
		t.Fatalf("store context = %v, want prepared context", call.ctx)
	}
	if call.todoID != 0 {
		t.Fatalf("global Todo ID = %d, want unvalidated 0", call.todoID)
	}
	if call.mode != store.ModeAnonymous {
		t.Fatalf("store mode = %q, want bound %q", call.mode, store.ModeAnonymous)
	}
	if result.Todo.ID != 71 {
		t.Fatalf("result = %+v, want moved Todo", result)
	}
}

func TestLegacyMoveServiceForwardsGlobalAnchorsWithoutTranslationOrPrevalidation(t *testing.T) {
	tests := []struct {
		name         string
		command      LegacyMoveCommand
		wantTodoID   int64
		wantColumn   string
		wantAfterID  *int64
		wantBeforeID *int64
	}{
		{
			name:        "after_global_id_only",
			command:     LegacyMoveCommand{TodoID: 7001, ToColumnKey: "doing", AfterTodoID: legacyMoveInt64(9103)},
			wantTodoID:  7001,
			wantColumn:  "doing",
			wantAfterID: legacyMoveInt64(9103),
		},
		{
			name:         "before_global_id_only",
			command:      LegacyMoveCommand{TodoID: 7001, ToColumnKey: "doing", BeforeTodoID: legacyMoveInt64(12107)},
			wantTodoID:   7001,
			wantColumn:   "doing",
			wantBeforeID: legacyMoveInt64(12107),
		},
		{
			name:         "both_global_ids",
			command:      LegacyMoveCommand{TodoID: 7001, ToColumnKey: "doing", AfterTodoID: legacyMoveInt64(9103), BeforeTodoID: legacyMoveInt64(12107)},
			wantTodoID:   7001,
			wantColumn:   "doing",
			wantAfterID:  legacyMoveInt64(9103),
			wantBeforeID: legacyMoveInt64(12107),
		},
		{
			name:        "self_anchor_forwarded",
			command:     LegacyMoveCommand{TodoID: 7001, ToColumnKey: "doing", AfterTodoID: legacyMoveInt64(7001)},
			wantTodoID:  7001,
			wantColumn:  "doing",
			wantAfterID: legacyMoveInt64(7001),
		},
		{
			name:         "reversed_global_ids_forwarded",
			command:      LegacyMoveCommand{TodoID: 7001, ToColumnKey: "doing", AfterTodoID: legacyMoveInt64(12107), BeforeTodoID: legacyMoveInt64(9103)},
			wantTodoID:   7001,
			wantColumn:   "doing",
			wantAfterID:  legacyMoveInt64(12107),
			wantBeforeID: legacyMoveInt64(9103),
		},
		{
			name:         "empty_destination_and_unvalidated_anchors_forwarded",
			command:      LegacyMoveCommand{TodoID: 0, AfterTodoID: legacyMoveInt64(-9), BeforeTodoID: legacyMoveInt64(0)},
			wantTodoID:   0,
			wantAfterID:  legacyMoveInt64(-9),
			wantBeforeID: legacyMoveInt64(0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			moves := &legacyMoveStoreFake{todo: store.Todo{ID: tt.wantTodoID, ProjectID: 17}}
			prepared := NewLegacyMoveService(LegacyMoveServiceDependencies{Move: moves}).Prepare(
				context.Background(),
				LegacyMoveTarget{Mode: store.ModeFull},
			)

			if _, err := prepared.Move(tt.command); err != nil {
				t.Fatalf("Move: %v", err)
			}
			if len(moves.calls) != 1 {
				t.Fatalf("move calls = %d, want 1", len(moves.calls))
			}
			call := moves.calls[0]
			if call.todoID != tt.wantTodoID || call.toColumnKey != tt.wantColumn || call.mode != store.ModeFull {
				t.Fatalf("move call = %+v, want global Todo %d column %q mode %q", call, tt.wantTodoID, tt.wantColumn, store.ModeFull)
			}
			if !reflect.DeepEqual(call.afterTodoID, tt.wantAfterID) || !reflect.DeepEqual(call.beforeTodoID, tt.wantBeforeID) {
				t.Fatalf("global anchors = after %v before %v, want after %v before %v", call.afterTodoID, call.beforeTodoID, tt.wantAfterID, tt.wantBeforeID)
			}
		})
	}
}

func TestLegacyMoveServiceSuccessSequencesMoveBeforeRefresh(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "bound")
	trace := []string{}
	moved := store.Todo{ID: 7001, ProjectID: 17, LocalID: 4, Title: "moved card", ColumnKey: "doing"}
	moves := &legacyMoveStoreFake{todo: moved, trace: &trace}
	refresh := &legacyMoveRefreshFake{trace: &trace}
	prepared := NewLegacyMoveService(LegacyMoveServiceDependencies{Move: moves, Refresh: refresh}).Prepare(
		ctx,
		LegacyMoveTarget{Mode: store.ModeFull},
	)

	result, err := prepared.Move(LegacyMoveCommand{TodoID: 7001, ToColumnKey: "doing"})
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if !reflect.DeepEqual(trace, []string{"move", "refresh"}) {
		t.Fatalf("trace = %#v, want move then refresh", trace)
	}
	if len(moves.calls) != 1 || len(refresh.calls) != 1 {
		t.Fatalf("calls = move %d refresh %d, want 1 each", len(moves.calls), len(refresh.calls))
	}
	if got := refresh.calls[0]; got.ctx != ctx || got.ctx.Value(key) != "bound" || got.projectID != moved.ProjectID || got.reason != RefreshReasonTodoMoved || got.entity != (apprefresh.Entity{LocalID: 4, Title: "moved card"}) {
		t.Fatalf("refresh call = %+v, want bound context, project %d, reason %q, entity #4 moved card", got, moved.ProjectID, RefreshReasonTodoMoved)
	}
	if !reflect.DeepEqual(result, LegacyMoveResult{Todo: moved}) {
		t.Fatalf("result = %+v, want moved Todo %+v", result, moved)
	}
}

func TestLegacyMoveServiceStoreFailureReturnsSameErrorAndSkipsRefresh(t *testing.T) {
	wantErr := errors.New("legacy move failed")
	moves := &legacyMoveStoreFake{err: wantErr}
	refresh := &legacyMoveRefreshFake{}
	prepared := NewLegacyMoveService(LegacyMoveServiceDependencies{Move: moves, Refresh: refresh}).Prepare(
		context.Background(),
		LegacyMoveTarget{Mode: store.ModeFull},
	)

	result, err := prepared.Move(LegacyMoveCommand{TodoID: 7001, ToColumnKey: "doing"})
	if err != wantErr {
		t.Fatalf("Move error = %v, want identical %v", err, wantErr)
	}
	if !reflect.DeepEqual(result, LegacyMoveResult{}) {
		t.Fatalf("result = %+v, want zero value", result)
	}
	if len(moves.calls) != 1 || len(refresh.calls) != 0 {
		t.Fatalf("calls = move %d refresh %d, want 1 and 0", len(moves.calls), len(refresh.calls))
	}
}

func TestLegacyMoveServiceCancellationAfterPrepareUsesBoundContext(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "bound"))
	moves := &legacyMoveStoreFake{}
	refresh := &legacyMoveRefreshFake{}
	prepared := NewLegacyMoveService(LegacyMoveServiceDependencies{Move: moves, Refresh: refresh}).Prepare(
		ctx,
		LegacyMoveTarget{Mode: store.ModeFull},
	)
	cancel()

	_, err := prepared.Move(LegacyMoveCommand{TodoID: 7001, ToColumnKey: "doing"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Move error = %v, want context canceled", err)
	}
	if len(moves.calls) != 1 {
		t.Fatalf("move calls = %d, want 1", len(moves.calls))
	}
	if got := moves.calls[0].ctx; got != ctx || got.Value(key) != "bound" || !errors.Is(got.Err(), context.Canceled) {
		t.Fatalf("store context = %v, want bound cancelled context", got)
	}
	if len(refresh.calls) != 0 {
		t.Fatalf("refresh calls = %d, want 0", len(refresh.calls))
	}
}

func TestLegacyMoveServiceNilRefreshIsSafe(t *testing.T) {
	moved := store.Todo{ID: 7001, ProjectID: 17, LocalID: 4, ColumnKey: "doing"}
	moves := &legacyMoveStoreFake{todo: moved}
	prepared := NewLegacyMoveService(LegacyMoveServiceDependencies{Move: moves}).Prepare(
		context.Background(),
		LegacyMoveTarget{Mode: store.ModeFull},
	)

	result, err := prepared.Move(LegacyMoveCommand{TodoID: 7001, ToColumnKey: "doing"})
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if len(moves.calls) != 1 {
		t.Fatalf("move calls = %d, want 1", len(moves.calls))
	}
	if !reflect.DeepEqual(result, LegacyMoveResult{Todo: moved}) {
		t.Fatalf("result = %+v, want moved Todo %+v", result, moved)
	}
}
