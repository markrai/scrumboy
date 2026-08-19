package todo

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

var _ LegacyUpdateStore = (*store.Store)(nil)

type legacyUpdateStoreCall struct {
	ctx    context.Context
	todoID int64
	input  store.UpdateTodoInput
	mode   store.Mode
}

type legacyUpdateStoreFake struct {
	calls []legacyUpdateStoreCall
	todo  store.Todo
	err   error
	trace *[]string
}

func (f *legacyUpdateStoreFake) UpdateTodo(
	ctx context.Context,
	todoID int64,
	input store.UpdateTodoInput,
	mode store.Mode,
) (store.Todo, error) {
	f.calls = append(f.calls, legacyUpdateStoreCall{ctx: ctx, todoID: todoID, input: input, mode: mode})
	if f.trace != nil {
		*f.trace = append(*f.trace, "update")
	}
	if f.err != nil {
		return store.Todo{}, f.err
	}
	if err := ctx.Err(); err != nil {
		return store.Todo{}, err
	}
	return f.todo, nil
}

type legacyUpdateRefreshCall struct {
	ctx       context.Context
	projectID int64
	reason    string
}

type legacyUpdateRefreshFake struct {
	calls []legacyUpdateRefreshCall
	trace *[]string
}

func (f *legacyUpdateRefreshFake) PublishBoardRefresh(ctx context.Context, projectID int64, reason string) {
	f.calls = append(f.calls, legacyUpdateRefreshCall{ctx: ctx, projectID: projectID, reason: reason})
	if f.trace != nil {
		*f.trace = append(*f.trace, "refresh")
	}
}

func TestLegacyUpdateServicePrepareBindsContextModeAndForwardsGlobalID(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "bound")
	updates := &legacyUpdateStoreFake{todo: store.Todo{ID: 71, ProjectID: 9}}
	service := NewLegacyUpdateService(LegacyUpdateServiceDependencies{Update: updates})
	target := LegacyUpdateTarget{Mode: store.ModeAnonymous}

	prepared := service.Prepare(ctx, target)
	if len(updates.calls) != 0 {
		t.Fatalf("Prepare made %d persistence calls, want 0", len(updates.calls))
	}
	target.Mode = store.ModeFull

	result, err := prepared.Update(LegacyUpdateCommand{TodoID: 0, Title: "replacement"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updates.calls) != 1 {
		t.Fatalf("update calls = %d, want 1", len(updates.calls))
	}
	call := updates.calls[0]
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
		t.Fatalf("result = %+v, want updated Todo", result)
	}
}

func TestLegacyUpdateServiceMaterializesReplacementFields(t *testing.T) {
	estimation := int64(8)
	assignee := int64(42)
	tags := []string{"legacy", "replacement"}
	updates := &legacyUpdateStoreFake{todo: store.Todo{ProjectID: 5, AssignmentChanged: true}}
	prepared := NewLegacyUpdateService(LegacyUpdateServiceDependencies{Update: updates}).Prepare(
		context.Background(),
		LegacyUpdateTarget{Mode: store.ModeFull},
	)

	_, err := prepared.Update(LegacyUpdateCommand{
		TodoID:           7001,
		Title:            "replacement title",
		Body:             "replacement body",
		Tags:             tags,
		EstimationPoints: &estimation,
		AssigneeUserID:   &assignee,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updates.calls) != 1 {
		t.Fatalf("update calls = %d, want 1", len(updates.calls))
	}
	input := updates.calls[0].input
	if input.Title != "replacement title" || input.Body != "replacement body" {
		t.Fatalf("replacement strings = title %q body %q", input.Title, input.Body)
	}
	if !reflect.DeepEqual(input.Tags, []string{"legacy", "replacement"}) {
		t.Fatalf("replacement tags = %#v", input.Tags)
	}
	if input.EstimationPoints == nil || *input.EstimationPoints != 8 || input.AssigneeUserID == nil || *input.AssigneeUserID != 42 {
		t.Fatalf("replacement pointers = estimation %v assignee %v", input.EstimationPoints, input.AssigneeUserID)
	}
	if input.SprintID != nil || input.ClearSprint {
		t.Fatalf("legacy input acquired Sprint mutation: SprintID=%v ClearSprint=%v", input.SprintID, input.ClearSprint)
	}
	if input.Tags[0] == "" || &input.Tags[0] == &tags[0] || input.EstimationPoints == &estimation || input.AssigneeUserID == &assignee {
		t.Fatal("materialized replacement values alias the command")
	}
	tags[0] = "mutated"
	estimation = 13
	assignee = 99
	if !reflect.DeepEqual(input.Tags, []string{"legacy", "replacement"}) || *input.EstimationPoints != 8 || *input.AssigneeUserID != 42 {
		t.Fatalf("store input changed after command mutation: %+v", input)
	}
}

func TestLegacyUpdateServiceMaterializesPriorityPresence(t *testing.T) {
	key := "urgent"
	tests := []struct {
		name        string
		field       Field[*string]
		wantPresent bool
		wantKey     *string
	}{
		{name: "omitted", field: Field[*string]{Value: &key}},
		{name: "clear", field: Field[*string]{Present: true}, wantPresent: true},
		{name: "set", field: Field[*string]{Present: true, Value: &key}, wantPresent: true, wantKey: &key},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updates := &legacyUpdateStoreFake{todo: store.Todo{ProjectID: 5, AssignmentChanged: true}}
			prepared := NewLegacyUpdateService(LegacyUpdateServiceDependencies{Update: updates}).Prepare(
				context.Background(),
				LegacyUpdateTarget{Mode: store.ModeFull},
			)

			if _, err := prepared.Update(LegacyUpdateCommand{TodoID: 7, Title: "title", PriorityKey: tt.field}); err != nil {
				t.Fatalf("Update: %v", err)
			}
			input := updates.calls[0].input
			if input.PriorityKeyPresent != tt.wantPresent {
				t.Fatalf("PriorityKeyPresent = %v, want %v", input.PriorityKeyPresent, tt.wantPresent)
			}
			if tt.wantKey == nil {
				if input.PriorityKey != nil {
					t.Fatalf("PriorityKey = %q, want nil", *input.PriorityKey)
				}
			} else {
				if input.PriorityKey == nil || *input.PriorityKey != *tt.wantKey {
					t.Fatalf("PriorityKey = %v, want %q", input.PriorityKey, *tt.wantKey)
				}
				if input.PriorityKey == tt.field.Value {
					t.Fatal("materialized priority aliases the command")
				}
			}
			if input.SprintID != nil || input.ClearSprint {
				t.Fatalf("priority materialization acquired Sprint mutation: %+v", input)
			}
		})
	}
}

func TestLegacyUpdateServiceSuccessSequencesUpdateBeforeRefresh(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "bound")
	trace := []string{}
	updated := store.Todo{ID: 80, ProjectID: 17, LocalID: 4}
	updates := &legacyUpdateStoreFake{todo: updated, trace: &trace}
	refresh := &legacyUpdateRefreshFake{trace: &trace}
	prepared := NewLegacyUpdateService(LegacyUpdateServiceDependencies{Update: updates, Refresh: refresh}).Prepare(
		ctx,
		LegacyUpdateTarget{Mode: store.ModeFull},
	)

	result, err := prepared.Update(LegacyUpdateCommand{TodoID: 80, Title: "updated"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !reflect.DeepEqual(trace, []string{"update", "refresh"}) {
		t.Fatalf("trace = %#v, want update then refresh", trace)
	}
	if len(updates.calls) != 1 || len(refresh.calls) != 1 {
		t.Fatalf("calls = update %d refresh %d, want 1 each", len(updates.calls), len(refresh.calls))
	}
	if got := refresh.calls[0]; got.ctx != ctx || got.ctx.Value(key) != "bound" || got.projectID != updated.ProjectID || got.reason != RefreshReasonTodoUpdated {
		t.Fatalf("refresh call = %+v, want bound context, project %d, reason %q", got, updated.ProjectID, RefreshReasonTodoUpdated)
	}
	if result.Todo.ID != updated.ID {
		t.Fatalf("result = %+v, want Todo %d", result, updated.ID)
	}
}

func TestLegacyUpdateServiceAssignmentChangeSuppressesDirectRefresh(t *testing.T) {
	updated := store.Todo{ID: 81, ProjectID: 17, AssignmentChanged: true}
	updates := &legacyUpdateStoreFake{todo: updated}
	refresh := &legacyUpdateRefreshFake{}
	prepared := NewLegacyUpdateService(LegacyUpdateServiceDependencies{Update: updates, Refresh: refresh}).Prepare(
		context.Background(),
		LegacyUpdateTarget{Mode: store.ModeFull},
	)

	result, err := prepared.Update(LegacyUpdateCommand{TodoID: 81, Title: "assignment change"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updates.calls) != 1 || len(refresh.calls) != 0 {
		t.Fatalf("calls = update %d refresh %d, want 1 and 0", len(updates.calls), len(refresh.calls))
	}
	if result.Todo.ID != updated.ID || !result.Todo.AssignmentChanged {
		t.Fatalf("result = %+v, want successful assignment update", result)
	}
}

func TestLegacyUpdateServiceStoreFailureReturnsSameErrorAndSkipsRefresh(t *testing.T) {
	wantErr := errors.New("legacy update failed")
	updates := &legacyUpdateStoreFake{err: wantErr}
	refresh := &legacyUpdateRefreshFake{}
	prepared := NewLegacyUpdateService(LegacyUpdateServiceDependencies{Update: updates, Refresh: refresh}).Prepare(
		context.Background(),
		LegacyUpdateTarget{Mode: store.ModeFull},
	)

	result, err := prepared.Update(LegacyUpdateCommand{TodoID: 91, Title: "failure"})
	if err != wantErr {
		t.Fatalf("Update error = %v, want identical %v", err, wantErr)
	}
	if !reflect.DeepEqual(result, LegacyUpdateResult{}) {
		t.Fatalf("result = %+v, want zero value", result)
	}
	if len(updates.calls) != 1 || len(refresh.calls) != 0 {
		t.Fatalf("calls = update %d refresh %d, want 1 and 0", len(updates.calls), len(refresh.calls))
	}
}

func TestLegacyUpdateServiceCancellationAfterPrepareUsesBoundContext(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "bound"))
	updates := &legacyUpdateStoreFake{}
	refresh := &legacyUpdateRefreshFake{}
	prepared := NewLegacyUpdateService(LegacyUpdateServiceDependencies{Update: updates, Refresh: refresh}).Prepare(
		ctx,
		LegacyUpdateTarget{Mode: store.ModeFull},
	)
	cancel()

	_, err := prepared.Update(LegacyUpdateCommand{TodoID: 101, Title: "cancelled"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Update error = %v, want context canceled", err)
	}
	if len(updates.calls) != 1 {
		t.Fatalf("update calls = %d, want 1", len(updates.calls))
	}
	if got := updates.calls[0].ctx; got != ctx || got.Value(key) != "bound" || !errors.Is(got.Err(), context.Canceled) {
		t.Fatalf("store context = %v, want bound cancelled context", got)
	}
	if len(refresh.calls) != 0 {
		t.Fatalf("refresh calls = %d, want 0", len(refresh.calls))
	}
}
