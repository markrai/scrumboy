package store

import (
	"context"
	"testing"
	"time"
)

func TestCountCompletedTodosForProjectUsesDoneAtWindowAndFullProjectScope(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	alpha, err := st.CreateProject(ctx, "Completion Alpha")
	if err != nil {
		t.Fatalf("CreateProject alpha: %v", err)
	}
	beta, err := st.CreateProject(ctx, "Completion Beta")
	if err != nil {
		t.Fatalf("CreateProject beta: %v", err)
	}

	inside := mustCreateTodo(t, st, alpha.ID, "Inside", DefaultColumnDone)
	outside := mustCreateTodo(t, st, alpha.ID, "Outside", DefaultColumnDone)
	notDone := mustCreateTodo(t, st, alpha.ID, "Reopened", DefaultColumnDoing)
	otherProject := mustCreateTodo(t, st, beta.ID, "Other project", DefaultColumnDone)
	start := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 7)

	updates := []struct {
		id     int64
		doneAt time.Time
	}{
		{inside.ID, start.Add(24 * time.Hour)},
		{outside.ID, start.Add(-time.Millisecond)},
		{notDone.ID, start.Add(48 * time.Hour)},
		{otherProject.ID, start.Add(72 * time.Hour)},
	}
	for _, update := range updates {
		if _, err := st.db.ExecContext(ctx, `UPDATE todos SET done_at = ? WHERE id = ?`, update.doneAt.UnixMilli(), update.id); err != nil {
			t.Fatalf("set done_at for %d: %v", update.id, err)
		}
	}

	count, err := st.CountCompletedTodosForProject(ctx, alpha.ID, start, end)
	if err != nil {
		t.Fatalf("CountCompletedTodosForProject: %v", err)
	}
	if count != 1 {
		t.Fatalf("count=%d want=1", count)
	}
}

func TestCountCompletedTodosForProjectUsesHalfOpenPeriod(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project, err := st.CreateProject(ctx, "Completion Boundary")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	todo := mustCreateTodo(t, st, project.ID, "Boundary", DefaultColumnDone)
	start := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 7)
	if _, err := st.db.ExecContext(ctx, `UPDATE todos SET done_at = ? WHERE id = ?`, end.UnixMilli(), todo.ID); err != nil {
		t.Fatalf("set boundary done_at: %v", err)
	}
	count, err := st.CountCompletedTodosForProject(ctx, project.ID, start, end)
	if err != nil {
		t.Fatalf("CountCompletedTodosForProject: %v", err)
	}
	if count != 0 {
		t.Fatalf("count=%d want=0", count)
	}
}
