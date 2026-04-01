package store

import (
	"context"
	"testing"
)

func TestCountTodosByColumnKey_EmptyProject(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "count-empty")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	m, err := st.CountTodosByColumnKey(ctx, p.ID)
	if err != nil {
		t.Fatalf("CountTodosByColumnKey: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("expected empty map, got %#v", m)
	}
}

func TestCountTodosByColumnKey_MultipleLanes(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "count-multi")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	added, err := st.AddWorkflowColumn(ctx, p.ID, "Review")
	if err != nil {
		t.Fatalf("AddWorkflowColumn: %v", err)
	}
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "A", ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "B", ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "C", ColumnKey: added.Key}, ModeFull); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	m, err := st.CountTodosByColumnKey(ctx, p.ID)
	if err != nil {
		t.Fatalf("CountTodosByColumnKey: %v", err)
	}
	if m[DefaultColumnBacklog] != 2 {
		t.Fatalf("backlog got %d want 2", m[DefaultColumnBacklog])
	}
	if m[added.Key] != 1 {
		t.Fatalf("custom lane got %d want 1", m[added.Key])
	}
}

func TestCountTodosByColumnKey_MissingKeyMeansZero(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "count-missing")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	added, err := st.AddWorkflowColumn(ctx, p.ID, "EmptyLane")
	if err != nil {
		t.Fatalf("AddWorkflowColumn: %v", err)
	}
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Only", ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	m, err := st.CountTodosByColumnKey(ctx, p.ID)
	if err != nil {
		t.Fatalf("CountTodosByColumnKey: %v", err)
	}
	if m[DefaultColumnBacklog] != 1 {
		t.Fatalf("backlog count %d", m[DefaultColumnBacklog])
	}
	if _, ok := m[added.Key]; ok {
		t.Fatalf("expected no row for empty lane %q (missing key => 0)", added.Key)
	}
}
