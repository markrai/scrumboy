package todo

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

type restUpdateStoreCall struct {
	ctx       context.Context
	projectID int64
	localID   int64
	input     store.UpdateTodoInput
	mode      store.Mode
}

type restUpdateStoreFake struct {
	calls []restUpdateStoreCall
	todo  store.Todo
	err   error
}

func (f *restUpdateStoreFake) UpdateTodoByLocalID(
	ctx context.Context,
	projectID int64,
	localID int64,
	input store.UpdateTodoInput,
	mode store.Mode,
) (store.Todo, error) {
	f.calls = append(f.calls, restUpdateStoreCall{
		ctx:       ctx,
		projectID: projectID,
		localID:   localID,
		input:     input,
		mode:      mode,
	})
	if f.err != nil {
		return store.Todo{}, f.err
	}
	if err := ctx.Err(); err != nil {
		return store.Todo{}, err
	}
	return f.todo, nil
}

type restUpdateRefreshCall struct {
	ctx       context.Context
	projectID int64
	reason    string
}

type restUpdateRefreshFake struct {
	calls []restUpdateRefreshCall
}

func (f *restUpdateRefreshFake) PublishBoardRefresh(ctx context.Context, projectID int64, reason string) {
	f.calls = append(f.calls, restUpdateRefreshCall{ctx: ctx, projectID: projectID, reason: reason})
}

func TestUpdateServicePreparedUpdateBindsTargetAndMaterializesNormalizedPatch(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "bound")

	estimation := int64(8)
	assignee := int64(42)
	tags := []string{"api", "urgent"}
	patch := normalizedRESTUpdatePatch(tags, &estimation, &assignee)
	updated := store.Todo{ID: 71, ProjectID: 7, LocalID: 4, Title: "new title"}
	updates := &restUpdateStoreFake{todo: updated}
	refresh := &restUpdateRefreshFake{}
	service := NewUpdateService(UpdateServiceDependencies{Update: updates, Refresh: refresh})
	target := ResolvedUpdateTarget{
		ProjectContext: store.ProjectContext{
			Project: store.Project{ID: 7, Slug: "canonical"},
			Role:    store.RoleMaintainer,
		},
		Mode: store.ModeFull,
	}
	prepared := service.Prepare(ctx, target)

	// PreparedUpdate owns value copies of the resolved target and mode.
	target.ProjectContext.Project.ID = 99
	target.ProjectContext.Project.Slug = "mutated"
	target.ProjectContext.Role = store.RoleViewer
	target.Mode = store.ModeAnonymous

	result, err := prepared.Update(UpdateCommand{LocalID: 4, Patch: patch})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updates.calls) != 1 {
		t.Fatalf("update calls = %d, want 1", len(updates.calls))
	}
	call := updates.calls[0]
	if call.ctx != ctx || call.ctx.Value(key) != "bound" {
		t.Fatalf("update context = %v, want prepared context", call.ctx)
	}
	if call.projectID != 7 || call.localID != 4 || call.mode != store.ModeFull {
		t.Fatalf("update target = project %d local %d mode %q", call.projectID, call.localID, call.mode)
	}
	assertNormalizedRESTUpdateInput(t, call.input, tags, estimation, assignee)
	if call.input.SprintID != nil || call.input.ClearSprint {
		t.Fatalf("omitted sprint requested mutation: SprintID=%v ClearSprint=%v", call.input.SprintID, call.input.ClearSprint)
	}

	if len(call.input.Tags) > 0 && &call.input.Tags[0] == &tags[0] {
		t.Fatal("materialized tags alias the normalized patch")
	}
	if call.input.EstimationPoints == &estimation || call.input.AssigneeUserID == &assignee {
		t.Fatal("materialized pointers alias the normalized patch")
	}
	tags[0] = "mutated"
	estimation = 80
	assignee = 420
	if !reflect.DeepEqual(call.input.Tags, []string{"api", "urgent"}) || *call.input.EstimationPoints != 8 || *call.input.AssigneeUserID != 42 {
		t.Fatalf("store input changed after patch mutation: %+v", call.input)
	}

	if result.Project.ID != 7 || result.Project.Slug != "canonical" || result.Todo.ID != updated.ID {
		t.Fatalf("result = %+v, want copied project and updated todo", result)
	}
	if len(refresh.calls) != 1 {
		t.Fatalf("refresh calls = %d, want 1", len(refresh.calls))
	}
	if got := refresh.calls[0]; got.ctx != ctx || got.projectID != 7 || got.reason != RefreshReasonTodoUpdated {
		t.Fatalf("refresh call = %+v, want bound context, project 7, reason %q", got, RefreshReasonTodoUpdated)
	}
}

func TestUpdateServicePreparedUpdateMaterializesSprintState(t *testing.T) {
	sprint := int64(12)
	tests := []struct {
		name        string
		field       Field[*int64]
		wantSprint  *int64
		wantCleared bool
	}{
		{name: "set", field: Field[*int64]{Present: true, Value: &sprint}, wantSprint: &sprint},
		{name: "clear", field: Field[*int64]{Present: true}, wantCleared: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updates := &restUpdateStoreFake{todo: store.Todo{AssignmentChanged: true}}
			prepared := NewUpdateService(UpdateServiceDependencies{Update: updates}).Prepare(
				context.Background(),
				ResolvedUpdateTarget{ProjectContext: store.ProjectContext{Project: store.Project{ID: 7}}, Mode: store.ModeFull},
			)
			patch := normalizedRESTUpdatePatch([]string{"tag"}, restUpdateInt64(3), restUpdateInt64(21))
			patch.SprintID = tt.field

			if _, err := prepared.Update(UpdateCommand{LocalID: 4, Patch: patch}); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if len(updates.calls) != 1 {
				t.Fatalf("update calls = %d, want 1", len(updates.calls))
			}
			input := updates.calls[0].input
			if input.ClearSprint != tt.wantCleared {
				t.Fatalf("ClearSprint = %v, want %v", input.ClearSprint, tt.wantCleared)
			}
			if tt.wantSprint == nil {
				if input.SprintID != nil {
					t.Fatalf("SprintID = %v, want nil", *input.SprintID)
				}
			} else {
				if input.SprintID == nil || *input.SprintID != *tt.wantSprint {
					t.Fatalf("SprintID = %v, want %d", input.SprintID, *tt.wantSprint)
				}
				if input.SprintID == tt.field.Value {
					t.Fatal("materialized sprint aliases the normalized patch")
				}
			}
		})
	}
}

func TestUpdateServiceAssignmentChangeSuppressesDirectRefresh(t *testing.T) {
	updated := store.Todo{ID: 81, ProjectID: 7, LocalID: 5, AssignmentChanged: true}
	updates := &restUpdateStoreFake{todo: updated}
	refresh := &restUpdateRefreshFake{}
	prepared := NewUpdateService(UpdateServiceDependencies{Update: updates, Refresh: refresh}).Prepare(
		context.Background(),
		ResolvedUpdateTarget{ProjectContext: store.ProjectContext{Project: store.Project{ID: 7}}, Mode: store.ModeFull},
	)

	result, err := prepared.Update(UpdateCommand{
		LocalID: 5,
		Patch:   normalizedRESTUpdatePatch([]string{"tag"}, restUpdateInt64(5), restUpdateInt64(31)),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updates.calls) != 1 {
		t.Fatalf("update calls = %d, want 1", len(updates.calls))
	}
	if len(refresh.calls) != 0 {
		t.Fatalf("direct refresh calls = %d, want 0 for assignment change", len(refresh.calls))
	}
	if result.Project.ID != 7 || result.Todo.ID != updated.ID || !result.Todo.AssignmentChanged {
		t.Fatalf("result = %+v, want successful assignment update", result)
	}
}

func TestUpdateServiceStoreFailureReturnsSameErrorAndSkipsRefresh(t *testing.T) {
	wantErr := errors.New("update failed")
	updates := &restUpdateStoreFake{err: wantErr}
	refresh := &restUpdateRefreshFake{}
	prepared := NewUpdateService(UpdateServiceDependencies{Update: updates, Refresh: refresh}).Prepare(
		context.Background(),
		ResolvedUpdateTarget{ProjectContext: store.ProjectContext{Project: store.Project{ID: 7}}, Mode: store.ModeFull},
	)

	result, err := prepared.Update(UpdateCommand{
		LocalID: 4,
		Patch:   normalizedRESTUpdatePatch([]string{"tag"}, restUpdateInt64(3), restUpdateInt64(21)),
	})
	if err != wantErr {
		t.Fatalf("Update error = %v, want identical error %v", err, wantErr)
	}
	if !reflect.DeepEqual(result, UpdateResult{}) {
		t.Fatalf("result = %+v, want zero value", result)
	}
	if len(updates.calls) != 1 {
		t.Fatalf("update calls = %d, want 1", len(updates.calls))
	}
	if len(refresh.calls) != 0 {
		t.Fatalf("refresh calls = %d, want 0", len(refresh.calls))
	}
}

func TestUpdateServiceCancellationAfterPreparationUsesBoundContext(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "bound"))
	updates := &restUpdateStoreFake{}
	refresh := &restUpdateRefreshFake{}
	prepared := NewUpdateService(UpdateServiceDependencies{Update: updates, Refresh: refresh}).Prepare(
		ctx,
		ResolvedUpdateTarget{ProjectContext: store.ProjectContext{Project: store.Project{ID: 7}}, Mode: store.ModeFull},
	)
	cancel()

	_, err := prepared.Update(UpdateCommand{
		LocalID: 4,
		Patch:   normalizedRESTUpdatePatch([]string{"tag"}, restUpdateInt64(3), restUpdateInt64(21)),
	})
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

func TestUpdateServiceNilRefreshDependencyIsNoOp(t *testing.T) {
	updated := store.Todo{ID: 91, ProjectID: 7, LocalID: 6, AssignmentChanged: false}
	updates := &restUpdateStoreFake{todo: updated}
	prepared := NewUpdateService(UpdateServiceDependencies{Update: updates}).Prepare(
		context.Background(),
		ResolvedUpdateTarget{ProjectContext: store.ProjectContext{Project: store.Project{ID: 7}}, Mode: store.ModeFull},
	)

	result, err := prepared.Update(UpdateCommand{
		LocalID: 6,
		Patch:   normalizedRESTUpdatePatch([]string{"tag"}, restUpdateInt64(2), restUpdateInt64(22)),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updates.calls) != 1 {
		t.Fatalf("update calls = %d, want 1", len(updates.calls))
	}
	if result.Todo.ID != updated.ID {
		t.Fatalf("result = %+v, want todo %d", result, updated.ID)
	}
}

func normalizedRESTUpdatePatch(tags []string, estimation, assignee *int64) UpdatePatch {
	return UpdatePatch{
		Title:            Field[string]{Present: true, Value: "new title"},
		Body:             Field[string]{Present: true, Value: "new body"},
		Tags:             Field[[]string]{Present: true, Value: tags},
		EstimationPoints: Field[*int64]{Present: true, Value: estimation},
		AssigneeUserID:   Field[*int64]{Present: true, Value: assignee},
	}
}

func assertNormalizedRESTUpdateInput(t *testing.T, input store.UpdateTodoInput, wantTags []string, wantEstimation, wantAssignee int64) {
	t.Helper()
	if input.Title != "new title" || input.Body != "new body" {
		t.Fatalf("replacement strings = title %q body %q", input.Title, input.Body)
	}
	if !reflect.DeepEqual(input.Tags, wantTags) {
		t.Fatalf("tags = %#v, want %#v", input.Tags, wantTags)
	}
	if input.EstimationPoints == nil || *input.EstimationPoints != wantEstimation {
		t.Fatalf("estimation = %v, want %d", input.EstimationPoints, wantEstimation)
	}
	if input.AssigneeUserID == nil || *input.AssigneeUserID != wantAssignee {
		t.Fatalf("assignee = %v, want %d", input.AssigneeUserID, wantAssignee)
	}
}

func restUpdateInt64(value int64) *int64 {
	return &value
}
