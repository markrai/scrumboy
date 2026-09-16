package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestGetBoard_NoHang is a regression test for the listTagCounts hang issue.
// The hang was caused by using OR over LEFT JOINs with GROUP BY in SQLite.
// This test ensures GetBoard completes without timeout.
func TestGetBoard_NoHang(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Create a project with some todos and tags
	p, err := st.CreateProject(ctx, "Test Board")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Create a few todos
	for i := 0; i < 5; i++ {
		_, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
			Title:     "Todo " + string(rune('A'+i)),
			Body:      "Test body",
			Tags:      []string{"tag1", "tag2"},
			ColumnKey: DefaultColumnBacklog,
		}, ModeFull)
		if err != nil {
			t.Fatalf("CreateTodo %d: %v", i, err)
		}
	}

	// GetBoard should complete within a reasonable time (not hang indefinitely)
	done := make(chan struct{})
	var boardErr error
	go func() {
		defer close(done)
		pc, _ := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
		_, _, _, _, err := st.GetBoard(ctx, &pc, "", "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault)
		boardErr = err
	}()

	select {
	case <-done:
		if boardErr != nil {
			t.Fatalf("GetBoard failed: %v", boardErr)
		}
		// Success - GetBoard completed
	case <-time.After(5 * time.Second):
		t.Fatal("GetBoard hung for >5 seconds (regression: listTagCounts query hanging)")
	}
}

func TestBoardArchivalVisibilityBelowSoftCapAndLaneContinuation(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.CreateUser(ctx, "archive-board@example.com", "password123", "Archive Board")
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithUserID(ctx, user.ID)
	p, err := st.CreateProject(ctx, "archive board paths")
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "active first", Tags: []string{"focus"}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "archived searchable", Tags: []string{"focus", "archive-only"}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	last, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "active last", Tags: []string{"focus"}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchiveTodoByLocalID(ctx, p.ID, archived.LocalID, ModeFull); err != nil {
		t.Fatal(err)
	}
	pc, err := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	_, tags, _, columns, meta, err := st.GetBoardPaged(ctx, &pc, "", "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(columns[DefaultColumnBacklog]) != 1 || columns[DefaultColumnBacklog][0].ID != first.ID || meta[DefaultColumnBacklog].TotalCount != 2 || !meta[DefaultColumnBacklog].HasMore {
		t.Fatalf("initial archive board page=%+v meta=%+v", columns[DefaultColumnBacklog], meta[DefaultColumnBacklog])
	}
	tagCounts := map[string]int{}
	for _, tag := range tags {
		tagCounts[TagGroupKey(tag.Name)] = tag.Count
	}
	if tagCounts["focus"] != 2 || tagCounts["archive-only"] != 0 {
		t.Fatalf("active board tag counts=%v", tagCounts)
	}
	a, b := ParseLaneCursor(meta[DefaultColumnBacklog].NextCursor)
	continued, _, more, err := st.ListTodosForBoardLane(ctx, p.ID, DefaultColumnBacklog, 10, a, b, "", "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault)
	if err != nil {
		t.Fatal(err)
	}
	if more || len(continued) != 1 || continued[0].ID != last.ID {
		t.Fatalf("archive lane continuation=%+v more=%v", continued, more)
	}
	_, _, _, searched, _, err := st.GetBoardPaged(ctx, &pc, "", "archived searchable", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(searched[DefaultColumnBacklog]) != 0 {
		t.Fatalf("search leaked archived todo: %+v", searched[DefaultColumnBacklog])
	}
	_, _, _, filtered, filteredMeta, err := st.GetBoardPaged(ctx, &pc, "archive-only", "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered[DefaultColumnBacklog]) != 0 || filteredMeta[DefaultColumnBacklog].TotalCount != 0 {
		t.Fatalf("tag filter leaked archived todo: items=%+v meta=%+v", filtered[DefaultColumnBacklog], filteredMeta[DefaultColumnBacklog])
	}
}

func TestBoardArchivalVisibilityAboveSoftCapFallback(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, err := st.CreateProject(ctx, "archive board fallback")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixMilli()
	activeCount := boardTodoSoftCap + 1
	for i := 1; i <= activeCount; i++ {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO todos(project_id, local_id, title, body, column_key, rank, created_at, updated_at)
VALUES (?, ?, ?, '', ?, ?, ?, ?)`, p.ID, i, fmt.Sprintf("active %d", i), DefaultColumnBacklog, int64(i)*1000, now, now); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO todos(project_id, local_id, title, body, column_key, rank, created_at, updated_at, archived_at)
VALUES (?, ?, 'archived fallback', '', ?, ?, ?, ?, ?)`, p.ID, activeCount+1, DefaultColumnBacklog, int64(activeCount+1)*1000, now, now, now); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	pc, err := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, columns, meta, err := st.GetBoardPaged(ctx, &pc, "", "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(columns[DefaultColumnBacklog]) != 20 || meta[DefaultColumnBacklog].TotalCount != activeCount || !meta[DefaultColumnBacklog].HasMore {
		t.Fatalf("fallback page len=%d meta=%+v want total=%d", len(columns[DefaultColumnBacklog]), meta[DefaultColumnBacklog], activeCount)
	}
	for _, todo := range columns[DefaultColumnBacklog] {
		if todo.ArchivedAt != nil || todo.Title == "archived fallback" {
			t.Fatalf("fallback leaked archived todo: %+v", todo)
		}
	}
}
