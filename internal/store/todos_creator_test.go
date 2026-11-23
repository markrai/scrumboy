package store

import (
	"errors"
	"testing"
)

func TestUpdateTodo_ReassignCreator(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, _ := setupAssigneeTestProject(t)
	defer cleanup()
	ctxM := WithUserID(ctx, maintainer.ID)
	ctxC := WithUserID(ctx, contributor.ID)

	todo, err := st.CreateTodo(ctxM, project.ID, CreateTodoInput{
		Title: "Moved ticket", ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if todo.CreatedByUserID == nil || *todo.CreatedByUserID != maintainer.ID {
		t.Fatalf("creator = %v, want %d", todo.CreatedByUserID, maintainer.ID)
	}

	patch := func(id *int64) UpdateTodoInput {
		return UpdateTodoInput{
			KeepTitle: true, KeepBody: true, KeepTags: true,
			KeepEstimationPoints: true, KeepAssigneeUserID: true,
			CreatedByUserID: id, CreatedByUserIDPresent: true,
		}
	}

	// Non-maintainers cannot reassign.
	if _, err := st.UpdateTodo(ctxC, todo.ID, patch(ptrInt64(contributor.ID)), ModeFull); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("contributor reassign err = %v, want ErrUnauthorized", err)
	}
	// Cannot clear, cannot point at a missing user.
	if _, err := st.UpdateTodo(ctxM, todo.ID, patch(nil), ModeFull); !errors.Is(err, ErrValidation) {
		t.Fatalf("clear err = %v, want ErrValidation", err)
	}
	if _, err := st.UpdateTodo(ctxM, todo.ID, patch(ptrInt64(999999)), ModeFull); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing user err = %v, want ErrValidation", err)
	}

	got, err := st.UpdateTodo(ctxM, todo.ID, patch(ptrInt64(contributor.ID)), ModeFull)
	if err != nil {
		t.Fatalf("maintainer reassign: %v", err)
	}
	if got.CreatedByUserID == nil || *got.CreatedByUserID != contributor.ID {
		t.Fatalf("returned creator = %v, want %d", got.CreatedByUserID, contributor.ID)
	}
	reread, err := st.GetTodoByLocalID(ctxM, project.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatalf("GetTodo: %v", err)
	}
	if reread.CreatedByUserID == nil || *reread.CreatedByUserID != contributor.ID {
		t.Fatalf("persisted creator = %v, want %d", reread.CreatedByUserID, contributor.ID)
	}

	// Omitting the field leaves the creator alone.
	unchanged, err := st.UpdateTodo(ctxM, todo.ID, UpdateTodoInput{
		Title: "Renamed", Body: "", Tags: nil,
		KeepBody: true, KeepTags: true, KeepEstimationPoints: true, KeepAssigneeUserID: true,
	}, ModeFull)
	if err != nil {
		t.Fatalf("plain update: %v", err)
	}
	if unchanged.CreatedByUserID == nil || *unchanged.CreatedByUserID != contributor.ID {
		t.Fatalf("creator changed by unrelated update: %v", unchanged.CreatedByUserID)
	}
}

// A contributor who is only allowed to edit the body of their assigned todo
// must get an error, not a silent success, when sending createdByUserId.
func TestUpdateTodo_ReassignCreatorRejectedForBodyOnlyScope(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, _ := setupAssigneeTestProject(t)
	defer cleanup()
	ctxM := WithUserID(ctx, maintainer.ID)
	ctxC := WithUserID(ctx, contributor.ID)

	todo, err := st.CreateTodo(ctxM, project.ID, CreateTodoInput{
		Title: "Assigned", ColumnKey: DefaultColumnBacklog, AssigneeUserID: ptrInt64(contributor.ID),
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	_, err = st.UpdateTodo(ctxC, todo.ID, UpdateTodoInput{
		KeepTitle: true, KeepBody: true, KeepTags: true,
		KeepEstimationPoints: true, KeepAssigneeUserID: true,
		CreatedByUserID: ptrInt64(contributor.ID), CreatedByUserIDPresent: true,
	}, ModeFull)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	reread, err := st.GetTodoByLocalID(ctxM, project.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatalf("GetTodoByLocalID: %v", err)
	}
	if reread.CreatedByUserID == nil || *reread.CreatedByUserID != maintainer.ID {
		t.Fatalf("creator changed: %v", reread.CreatedByUserID)
	}
}
