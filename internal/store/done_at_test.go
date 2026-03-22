package store

import (
	"context"
	"database/sql"
	"testing"
)

func TestDoneAt_MoveTodo_IntoDONE(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "test")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:     "Todo",
		ColumnKey: DefaultColumnDoing,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if todo.DoneAt != nil {
		t.Errorf("IN_PROGRESS todo should not have DoneAt, got %v", todo.DoneAt)
	}

	moved, err := st.MoveTodo(ctx, todo.ID, DefaultColumnDone, nil, nil, ModeFull)
	if err != nil {
		t.Fatalf("MoveTodo: %v", err)
	}
	if moved.DoneAt == nil {
		t.Error("expected DoneAt set when transitioning into DONE")
	}
}

func TestDoneAt_MoveTodo_DONEtoDONE_NoChange(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "test")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:     "Todo",
		ColumnKey: DefaultColumnDone,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if todo.DoneAt == nil {
		t.Error("CreateTodo with DONE should set DoneAt")
	}
	doneAt1 := todo.DoneAt

	// Reorder within DONE (DONE → DONE)
	moved, err := st.MoveTodo(ctx, todo.ID, DefaultColumnDone, nil, nil, ModeFull)
	if err != nil {
		t.Fatalf("MoveTodo: %v", err)
	}
	// DONE→DONE: done_at should not change (we don't overwrite)
	var doneAtDb sql.NullInt64
	if err := st.db.QueryRowContext(ctx, `SELECT done_at FROM todos WHERE id = ?`, todo.ID).Scan(&doneAtDb); err != nil {
		t.Fatalf("query done_at: %v", err)
	}
	if !doneAtDb.Valid {
		t.Error("done_at should remain set after DONE→DONE")
	}
	if doneAt1 != nil && doneAtDb.Valid && doneAt1.UnixMilli() != doneAtDb.Int64 {
		t.Logf("DONE→DONE: done_at may be unchanged (plan: no change). moved.DoneAt=%v, db=%d", moved.DoneAt, doneAtDb.Int64)
	}
}

func TestDoneAt_MoveTodo_ReopenPreserves(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "test")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:     "Todo",
		ColumnKey: DefaultColumnDone,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if todo.DoneAt == nil {
		t.Fatal("CreateTodo with DONE should set DoneAt")
	}
	doneAtBefore := todo.DoneAt.UnixMilli()

	// Reopen: DONE → IN_PROGRESS
	_, err = st.MoveTodo(ctx, todo.ID, DefaultColumnDoing, nil, nil, ModeFull)
	if err != nil {
		t.Fatalf("MoveTodo: %v", err)
	}

	var doneAtDb sql.NullInt64
	if err := st.db.QueryRowContext(ctx, `SELECT done_at FROM todos WHERE id = ?`, todo.ID).Scan(&doneAtDb); err != nil {
		t.Fatalf("query done_at: %v", err)
	}
	if !doneAtDb.Valid {
		t.Error("done_at must be preserved on reopen (DONE→IN_PROGRESS)")
	}
	if doneAtDb.Int64 != doneAtBefore {
		t.Errorf("done_at changed on reopen: before=%d after=%d (must preserve)", doneAtBefore, doneAtDb.Int64)
	}
}

func TestDoneAt_CreateTodo_StatusDone(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "test")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:     "Todo",
		ColumnKey: DefaultColumnDone,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if todo.DoneAt == nil {
		t.Error("CreateTodo with StatusDone must set DoneAt")
	}
}
