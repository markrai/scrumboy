package todo

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type creatorRequestRecorder struct {
	contexts []context.Context
	requests []CreatorNotificationRequest
	trace    *[]string
}

func (r *creatorRequestRecorder) PublishCreatorNotificationRequest(ctx context.Context, request CreatorNotificationRequest) {
	r.contexts = append(r.contexts, ctx)
	r.requests = append(r.requests, request)
	if r.trace != nil {
		*r.trace = append(*r.trace, "request")
	}
}

type creatorProjectStoreFake struct {
	project store.Project
	err     error
	calls   []int64
	trace   *[]string
}

func (f *creatorProjectStoreFake) GetProject(_ context.Context, projectID int64) (store.Project, error) {
	f.calls = append(f.calls, projectID)
	if f.trace != nil {
		*f.trace = append(*f.trace, "project")
	}
	return f.project, f.err
}

func creatorRequestTestTodo(creatorID int64) store.Todo {
	return store.Todo{
		ID:              81,
		ProjectID:       7,
		LocalID:         5,
		Title:           "Committed title",
		CreatedByUserID: &creatorID,
	}
}

func TestPublishCreatorNotificationRequestEligibilityAndPayload(t *testing.T) {
	creatorID := int64(11)
	actorID := int64(22)
	expires := time.Now().UTC().Add(time.Hour)
	durable := store.Project{ID: 7, Slug: "durable"}

	tests := []struct {
		name    string
		ctx     context.Context
		project store.Project
		todo    store.Todo
		want    int
	}{
		{name: "eligible internal consideration", ctx: store.WithUserID(context.Background(), actorID), project: durable, todo: creatorRequestTestTodo(creatorID), want: 1},
		{name: "historical creator may be a removed member", ctx: store.WithUserID(context.Background(), actorID), project: durable, todo: creatorRequestTestTodo(creatorID), want: 1},
		{name: "self edit", ctx: store.WithUserID(context.Background(), creatorID), project: durable, todo: creatorRequestTestTodo(creatorID)},
		{name: "missing actor", ctx: context.Background(), project: durable, todo: creatorRequestTestTodo(creatorID)},
		{name: "nonpositive actor", ctx: store.WithUserID(context.Background(), 0), project: durable, todo: creatorRequestTestTodo(creatorID)},
		{name: "null creator", ctx: store.WithUserID(context.Background(), actorID), project: durable, todo: store.Todo{ID: 81, ProjectID: 7}},
		{name: "temporary or anonymous project", ctx: store.WithUserID(context.Background(), actorID), project: store.Project{ID: 7, Slug: "temporary", ExpiresAt: &expires}, todo: creatorRequestTestTodo(creatorID)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &creatorRequestRecorder{}
			publishCreatorNotificationRequest(tt.ctx, recorder, tt.project, tt.todo, RefreshReasonTodoUpdated, true)
			if len(recorder.requests) != tt.want {
				t.Fatalf("request calls = %d, want %d", len(recorder.requests), tt.want)
			}
			if tt.want == 0 {
				return
			}
			want := CreatorNotificationRequest{
				ProjectID:             7,
				ProjectSlug:           "durable",
				TodoID:                81,
				LocalID:               5,
				Title:                 "Committed title",
				ActivityReason:        RefreshReasonTodoUpdated,
				CreatedByUserID:       creatorID,
				ActorUserID:           actorID,
				CardActivityCandidate: true,
			}
			if !reflect.DeepEqual(recorder.requests[0], want) {
				t.Fatalf("request = %+v, want %+v", recorder.requests[0], want)
			}
		})
	}
}

func TestPublishCreatorNotificationRequestCarriesEmailPolicyFactsInEffectContext(t *testing.T) {
	creatorID := int64(11)
	actorID := int64(22)
	assigneeID := creatorID
	todo := creatorRequestTestTodo(creatorID)
	todo.MaterialChanged = true
	todo.AssignmentChanged = true
	todo.AssigneeUserID = &assigneeID
	recorder := &creatorRequestRecorder{}

	effectCtx := publishCreatorNotificationRequest(
		store.WithUserID(context.Background(), actorID),
		recorder,
		store.Project{ID: 7, Slug: "durable"},
		todo,
		RefreshReasonTodoUpdated,
		true,
	)
	request, ok := CreatorNotificationRequestFromContext(effectCtx)
	if !ok || len(recorder.requests) != 1 || !reflect.DeepEqual(request, recorder.requests[0]) {
		t.Fatalf("effect context request=%+v ok=%v published=%+v", request, ok, recorder.requests)
	}
	if !request.MaterialChanged || !request.AssignmentChanged || !request.CardActivityCandidate ||
		request.ToAssigneeUserID == nil || *request.ToAssigneeUserID != creatorID {
		t.Fatalf("email policy facts=%+v", request)
	}
	*request.ToAssigneeUserID = 999
	if *recorder.requests[0].ToAssigneeUserID != creatorID {
		t.Fatal("context read aliased the published request assignee pointer")
	}
}

type successfulCanceledUpdateStore struct {
	todo  store.Todo
	calls int
}

func (s *successfulCanceledUpdateStore) UpdateTodoByLocalID(context.Context, int64, int64, store.UpdateTodoInput, store.Mode) (store.Todo, error) {
	s.calls++
	return s.todo, nil
}

func TestUpdateServiceSuccessfulCommitInvokesPublisherWithCancelledBoundContext(t *testing.T) {
	creatorID := int64(11)
	base := store.WithUserID(context.Background(), 22)
	ctx, cancel := context.WithCancel(base)
	cancel()

	updates := &successfulCanceledUpdateStore{todo: creatorRequestTestTodo(creatorID)}
	requests := &creatorRequestRecorder{}
	service := NewUpdateService(UpdateServiceDependencies{
		Update:          updates,
		CreatorRequests: requests,
	})
	result, err := service.Prepare(ctx, ResolvedUpdateTarget{
		ProjectContext: store.ProjectContext{Project: store.Project{ID: 7, Slug: "durable"}},
		Mode:           store.ModeFull,
	}).Update(UpdateCommand{LocalID: 5})
	if err != nil || result.Todo.ID != 81 || updates.calls != 1 {
		t.Fatalf("successful committed update = (%+v, %v), calls=%d", result, err, updates.calls)
	}
	if len(requests.contexts) != 1 || !errors.Is(requests.contexts[0].Err(), context.Canceled) {
		t.Fatalf("publisher contexts = %+v, want one cancelled bound context", requests.contexts)
	}
}

func TestRESTUpdateAndMoveExplicitlyRequestAfterSuccess(t *testing.T) {
	creatorID := int64(11)
	ctx := store.WithUserID(context.Background(), 22)
	projectContext := store.ProjectContext{Project: store.Project{ID: 7, Slug: "durable"}}

	t.Run("update assignment change still requests and preserves refresh gate", func(t *testing.T) {
		updated := creatorRequestTestTodo(creatorID)
		updated.AssignmentChanged = true
		updates := &restUpdateStoreFake{todo: updated}
		refresh := &restUpdateRefreshFake{}
		requests := &creatorRequestRecorder{}
		service := NewUpdateService(UpdateServiceDependencies{Update: updates, Refresh: refresh, CreatorRequests: requests})
		if _, err := service.Prepare(ctx, ResolvedUpdateTarget{ProjectContext: projectContext, Mode: store.ModeFull}).Update(UpdateCommand{LocalID: 5}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if len(requests.requests) != 1 || requests.requests[0].ActivityReason != RefreshReasonTodoUpdated {
			t.Fatalf("requests = %+v, want one todo_updated", requests.requests)
		}
		if len(refresh.calls) != 0 {
			t.Fatalf("refresh calls = %d, want assignment gate unchanged", len(refresh.calls))
		}
	})

	t.Run("move requests and preserves refresh", func(t *testing.T) {
		moved := creatorRequestTestTodo(creatorID)
		moved.ColumnKey = "doing"
		moves := &moveStoreFake{todo: moved}
		refresh := &refreshPublisherFake{}
		requests := &creatorRequestRecorder{}
		service := NewMoveService(MoveServiceDependencies{Move: moves, Refresh: refresh, CreatorRequests: requests})
		if _, err := service.Prepare(ctx, ResolvedMoveTarget{ProjectContext: projectContext, Mode: store.ModeFull}).Move(MoveCommand{LocalID: 5, ToColumnKey: "doing"}); err != nil {
			t.Fatalf("Move: %v", err)
		}
		if len(requests.requests) != 1 || requests.requests[0].ActivityReason != RefreshReasonTodoMoved {
			t.Fatalf("requests = %+v, want one todo_moved", requests.requests)
		}
		if len(refresh.calls) != 1 {
			t.Fatalf("refresh calls = %d, want 1", len(refresh.calls))
		}
	})
}

func TestLegacyCreatorRequestProjectLookupIsPostCommitAndAncillary(t *testing.T) {
	creatorID := int64(11)
	ctx := store.WithUserID(context.Background(), 22)

	t.Run("update sequences mutation project request refresh", func(t *testing.T) {
		trace := []string{}
		updated := creatorRequestTestTodo(creatorID)
		updates := &legacyUpdateStoreFake{todo: updated, trace: &trace}
		projects := &creatorProjectStoreFake{project: store.Project{ID: 7, Slug: "durable"}, trace: &trace}
		requests := &creatorRequestRecorder{trace: &trace}
		refresh := &legacyUpdateRefreshFake{trace: &trace}
		service := NewLegacyUpdateService(LegacyUpdateServiceDependencies{
			Update: updates, Refresh: refresh, Projects: projects, CreatorRequests: requests,
		})
		if _, err := service.Prepare(ctx, LegacyUpdateTarget{Mode: store.ModeFull}).Update(LegacyUpdateCommand{TodoID: 81}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if !reflect.DeepEqual(trace, []string{"update", "project", "request", "refresh"}) {
			t.Fatalf("trace = %v", trace)
		}
	})

	t.Run("move project failure skips only request", func(t *testing.T) {
		trace := []string{}
		moved := creatorRequestTestTodo(creatorID)
		moves := &legacyMoveStoreFake{todo: moved, trace: &trace}
		projects := &creatorProjectStoreFake{err: errors.New("project read failed"), trace: &trace}
		requests := &creatorRequestRecorder{trace: &trace}
		refresh := &legacyMoveRefreshFake{trace: &trace}
		service := NewLegacyMoveService(LegacyMoveServiceDependencies{
			Move: moves, Refresh: refresh, Projects: projects, CreatorRequests: requests,
		})
		result, err := service.Prepare(ctx, LegacyMoveTarget{Mode: store.ModeFull}).Move(LegacyMoveCommand{TodoID: 81, ToColumnKey: "doing"})
		if err != nil || result.Todo.ID != moved.ID {
			t.Fatalf("Move = (%+v, %v), want success", result, err)
		}
		if !reflect.DeepEqual(trace, []string{"move", "project", "refresh"}) || len(requests.requests) != 0 {
			t.Fatalf("trace = %v requests=%+v", trace, requests.requests)
		}
	})
}

func TestMCPUpdateAndMoveExplicitlyRequestWithoutBoardRefresh(t *testing.T) {
	creatorID := int64(11)
	ctx := store.WithUserID(context.Background(), 22)
	project := store.Project{ID: 7, Slug: "durable"}

	t.Run("update empty patch silent and present semantic no-op requests", func(t *testing.T) {
		existing := creatorRequestTestTodo(creatorID)
		access := &mcpUpdateAccessFake{pc: store.ProjectContext{Project: project}}
		lookup := &mcpUpdateLookupFake{todo: existing}
		updates := &mcpUpdateStoreFake{todo: existing}
		requests := &creatorRequestRecorder{}
		service := NewMCPUpdateService(MCPUpdateServiceDependencies{Access: access, Lookup: lookup, Update: updates, CreatorRequests: requests})
		prepared, err := service.Prepare(ctx, SlugUpdateTarget{Slug: "durable", Mode: store.ModeFull})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		preparedTodo, err := prepared.PrepareTodo(existing.LocalID)
		if err != nil {
			t.Fatalf("PrepareTodo: %v", err)
		}
		if _, err := preparedTodo.Update(UpdatePatch{}); err != nil {
			t.Fatalf("empty Update: %v", err)
		}
		if len(requests.requests) != 0 || len(updates.calls) != 0 {
			t.Fatalf("empty patch requests=%d updates=%d", len(requests.requests), len(updates.calls))
		}
		if _, err := preparedTodo.Update(UpdatePatch{Title: Field[string]{Present: true, Value: existing.Title}}); err != nil {
			t.Fatalf("semantic no-op Update: %v", err)
		}
		if len(requests.requests) != 1 || len(updates.calls) != 1 {
			t.Fatalf("semantic no-op requests=%d updates=%d, want 1/1", len(requests.requests), len(updates.calls))
		}
	})

	t.Run("move requests through explicit MCP dependency", func(t *testing.T) {
		moving := creatorRequestTestTodo(creatorID)
		moving.ColumnKey = "backlog"
		moved := moving
		moved.ColumnKey = "doing"
		access := &mcpMoveAccessFake{pc: store.ProjectContext{Project: project}}
		lookup := &mcpMoveLookupFake{todos: map[int64]store.Todo{moving.LocalID: moving}, errors: map[int64]error{}}
		lanes := &mcpMoveLaneFake{}
		moves := &moveStoreFake{todo: moved}
		requests := &creatorRequestRecorder{}
		service := NewMCPMoveService(MCPMoveServiceDependencies{
			Access: access, Lookup: lookup, Lanes: lanes, Move: moves, CreatorRequests: requests,
		})
		prepared, err := service.Prepare(ctx, SlugMoveTarget{Slug: "durable", Mode: store.ModeFull})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if _, err := prepared.Move(MoveCommand{LocalID: moving.LocalID, ToColumnKey: "doing"}); err != nil {
			t.Fatalf("Move: %v", err)
		}
		if len(requests.requests) != 1 || requests.requests[0].ActivityReason != RefreshReasonTodoMoved {
			t.Fatalf("requests = %+v", requests.requests)
		}
	})
}

func TestFailedTodoMutationsDoNotRequestCreatorConsideration(t *testing.T) {
	creatorID := int64(11)
	ctx := store.WithUserID(context.Background(), 22)
	project := store.Project{ID: 7, Slug: "durable"}
	wantErr := errors.New("mutation failed")

	t.Run("REST update", func(t *testing.T) {
		requests := &creatorRequestRecorder{}
		refresh := &restUpdateRefreshFake{}
		service := NewUpdateService(UpdateServiceDependencies{
			Update: &restUpdateStoreFake{err: wantErr}, Refresh: refresh, CreatorRequests: requests,
		})
		_, err := service.Prepare(ctx, ResolvedUpdateTarget{
			ProjectContext: store.ProjectContext{Project: project}, Mode: store.ModeFull,
		}).Update(UpdateCommand{LocalID: 5})
		if err != wantErr || len(requests.requests) != 0 || len(refresh.calls) != 0 {
			t.Fatalf("error=%v requests=%d refreshes=%d", err, len(requests.requests), len(refresh.calls))
		}
	})

	t.Run("REST move", func(t *testing.T) {
		requests := &creatorRequestRecorder{}
		refresh := &refreshPublisherFake{}
		service := NewMoveService(MoveServiceDependencies{
			Move: &moveStoreFake{err: wantErr}, Refresh: refresh, CreatorRequests: requests,
		})
		_, err := service.Prepare(ctx, ResolvedMoveTarget{
			ProjectContext: store.ProjectContext{Project: project}, Mode: store.ModeFull,
		}).Move(MoveCommand{LocalID: 5, ToColumnKey: "doing"})
		if err != wantErr || len(requests.requests) != 0 || len(refresh.calls) != 0 {
			t.Fatalf("error=%v requests=%d refreshes=%d", err, len(requests.requests), len(refresh.calls))
		}
	})

	t.Run("legacy update", func(t *testing.T) {
		requests := &creatorRequestRecorder{}
		projects := &creatorProjectStoreFake{project: project}
		refresh := &legacyUpdateRefreshFake{}
		service := NewLegacyUpdateService(LegacyUpdateServiceDependencies{
			Update: &legacyUpdateStoreFake{err: wantErr}, Refresh: refresh,
			Projects: projects, CreatorRequests: requests,
		})
		_, err := service.Prepare(ctx, LegacyUpdateTarget{Mode: store.ModeFull}).Update(LegacyUpdateCommand{TodoID: 81})
		if err != wantErr || len(projects.calls) != 0 || len(requests.requests) != 0 || len(refresh.calls) != 0 {
			t.Fatalf("error=%v projectReads=%d requests=%d refreshes=%d", err, len(projects.calls), len(requests.requests), len(refresh.calls))
		}
	})

	t.Run("legacy move", func(t *testing.T) {
		requests := &creatorRequestRecorder{}
		projects := &creatorProjectStoreFake{project: project}
		refresh := &legacyMoveRefreshFake{}
		service := NewLegacyMoveService(LegacyMoveServiceDependencies{
			Move: &legacyMoveStoreFake{err: wantErr}, Refresh: refresh,
			Projects: projects, CreatorRequests: requests,
		})
		_, err := service.Prepare(ctx, LegacyMoveTarget{Mode: store.ModeFull}).Move(LegacyMoveCommand{TodoID: 81, ToColumnKey: "doing"})
		if err != wantErr || len(projects.calls) != 0 || len(requests.requests) != 0 || len(refresh.calls) != 0 {
			t.Fatalf("error=%v projectReads=%d requests=%d refreshes=%d", err, len(projects.calls), len(requests.requests), len(refresh.calls))
		}
	})

	t.Run("MCP update", func(t *testing.T) {
		existing := creatorRequestTestTodo(creatorID)
		requests := &creatorRequestRecorder{}
		service := NewMCPUpdateService(MCPUpdateServiceDependencies{
			Access: &mcpUpdateAccessFake{pc: store.ProjectContext{Project: project}},
			Lookup: &mcpUpdateLookupFake{todo: existing}, Update: &mcpUpdateStoreFake{err: wantErr},
			CreatorRequests: requests,
		})
		prepared, err := service.Prepare(ctx, SlugUpdateTarget{Slug: project.Slug, Mode: store.ModeFull})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		preparedTodo, err := prepared.PrepareTodo(existing.LocalID)
		if err != nil {
			t.Fatalf("PrepareTodo: %v", err)
		}
		_, err = preparedTodo.Update(UpdatePatch{Body: Field[string]{Present: true, Value: "changed"}})
		if err != wantErr || len(requests.requests) != 0 {
			t.Fatalf("error=%v requests=%d", err, len(requests.requests))
		}
	})

	t.Run("MCP move", func(t *testing.T) {
		existing := creatorRequestTestTodo(creatorID)
		requests := &creatorRequestRecorder{}
		service := NewMCPMoveService(MCPMoveServiceDependencies{
			Access: &mcpMoveAccessFake{pc: store.ProjectContext{Project: project}},
			Lookup: &mcpMoveLookupFake{todos: map[int64]store.Todo{existing.LocalID: existing}, errors: map[int64]error{}},
			Lanes:  &mcpMoveLaneFake{}, Move: &moveStoreFake{err: wantErr}, CreatorRequests: requests,
		})
		prepared, err := service.Prepare(ctx, SlugMoveTarget{Slug: project.Slug, Mode: store.ModeFull})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		_, err = prepared.Move(MoveCommand{LocalID: existing.LocalID, ToColumnKey: "doing"})
		if err != wantErr || len(requests.requests) != 0 {
			t.Fatalf("error=%v requests=%d", err, len(requests.requests))
		}
	})
}
