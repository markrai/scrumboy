package store

import (
	"context"
	"errors"
	"testing"
)

func TestTodoArchivalPreservesLifecycleAndIsIdempotent(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, err := st.CreateProject(ctx, "archive")
	if err != nil {
		t.Fatal(err)
	}
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "done", ColumnKey: DefaultColumnDone}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	beforeDone, beforeUpdated := todo.DoneAt, todo.UpdatedAt
	r, err := st.ArchiveTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if r.TransitionedCount != 1 || r.UnchangedCount != 0 {
		t.Fatalf("result=%+v", r)
	}
	got, err := st.GetTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if got.ArchivedAt == nil || got.DoneAt == nil || got.DoneAt.UnixMilli() != beforeDone.UnixMilli() || got.UpdatedAt.UnixMilli() != beforeUpdated.UnixMilli() {
		t.Fatalf("lifecycle changed: before=%+v after=%+v", todo, got)
	}
	noOp, err := st.ArchiveTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if noOp.TransitionedCount != 0 || noOp.UnchangedCount != 1 || noOp.TransitionedAt != nil {
		t.Fatalf("noop=%+v", noOp)
	}
	if _, err := st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{Title: "x", Body: "x", Tags: []string{}}, ModeFull); !errors.Is(err, ErrConflict) || ErrorReason(err) != ReasonTodoArchived {
		t.Fatalf("update archived err=%v reason=%q", err, ErrorReason(err))
	}
	restored, err := st.RestoreTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if restored.TransitionedCount != 1 {
		t.Fatalf("restore=%+v", restored)
	}
	got, err = st.GetTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if got.ArchivedAt != nil {
		t.Fatal("restore did not clear archived state")
	}
}

func TestBoardExcludesArchivedAndArchiveListIncludes(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, _ := st.CreateProject(ctx, "board")
	todo, _ := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "a"}, ModeFull)
	_, _ = st.ArchiveTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull)
	pc, err := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, cols, err := st.GetBoard(ctx, &pc, "", "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{}, SortOrderDefault)
	if err != nil {
		t.Fatal(err)
	}
	for _, items := range cols {
		if len(items) != 0 {
			t.Fatalf("archived story leaked to board: %+v", items)
		}
	}
	items, _, _, err := st.ListArchivedTodos(ctx, p.ID, 50, nil, nil, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ArchivedAt == nil {
		t.Fatalf("archive list=%+v", items)
	}
}
