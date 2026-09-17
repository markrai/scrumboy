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
		_, _, _, _, err := st.GetBoard(ctx, &pc, []string{""}, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault)
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
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "done only", Tags: []string{"done-only"}, ColumnKey: DefaultColumnDone}, ModeFull); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchiveTodoByLocalID(ctx, p.ID, archived.LocalID, ModeFull); err != nil {
		t.Fatal(err)
	}
	pc, err := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	_, tags, _, columns, meta, err := st.GetBoardPaged(ctx, &pc, []string{""}, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault, 1)
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
	if tagCounts["focus"] != 2 || tagCounts["done-only"] != 1 {
		t.Fatalf("active board tag counts=%v", tagCounts)
	}
	if _, exists := tagCounts["archive-only"]; exists {
		t.Fatalf("archive-only tag leaked into active board projection: %v", tagCounts)
	}
	a, b := ParseLaneCursor(meta[DefaultColumnBacklog].NextCursor)
	continued, _, more, err := st.ListTodosForBoardLane(ctx, p.ID, DefaultColumnBacklog, 10, a, b, []string{""}, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault)
	if err != nil {
		t.Fatal(err)
	}
	if more || len(continued) != 1 || continued[0].ID != last.ID {
		t.Fatalf("archive lane continuation=%+v more=%v", continued, more)
	}
	_, _, _, searched, _, err := st.GetBoardPaged(ctx, &pc, []string{""}, "archived searchable", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(searched[DefaultColumnBacklog]) != 0 {
		t.Fatalf("search leaked archived todo: %+v", searched[DefaultColumnBacklog])
	}
	_, filteredTags, _, filtered, filteredMeta, err := st.GetBoardPaged(ctx, &pc, []string{"archive-only"}, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered[DefaultColumnBacklog]) != 0 || filteredMeta[DefaultColumnBacklog].TotalCount != 0 {
		t.Fatalf("tag filter leaked archived todo: items=%+v meta=%+v", filtered[DefaultColumnBacklog], filteredMeta[DefaultColumnBacklog])
	}
	selectedFound := false
	for _, tag := range filteredTags {
		if tag.Name == "archive-only" && tag.Count == 0 {
			selectedFound = true
		}
	}
	if !selectedFound {
		t.Fatalf("selected inactive tag projection=%+v, want archive-only count zero", filteredTags)
	}
	if _, err := st.RestoreTodoByLocalID(ctx, p.ID, archived.LocalID, ModeFull); err != nil {
		t.Fatal(err)
	}
	_, restoredTags, _, _, _, err := st.GetBoardPaged(ctx, &pc, []string{""}, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault, 20)
	if err != nil {
		t.Fatal(err)
	}
	restoredCounts := map[string]int{}
	for _, tag := range restoredTags {
		restoredCounts[TagGroupKey(tag.Name)] = tag.Count
	}
	if restoredCounts["archive-only"] != 1 {
		t.Fatalf("restored active board tag counts=%v", restoredCounts)
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
	activeTagResult, err := tx.ExecContext(ctx, `INSERT INTO tags(project_id, name, created_at) VALUES (?, 'scale-active', ?)`, p.ID, now)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	activeTagID, _ := activeTagResult.LastInsertId()
	archiveTagResult, err := tx.ExecContext(ctx, `INSERT INTO tags(project_id, name, created_at) VALUES (?, 'scale-archive', ?)`, p.ID, now)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	archiveTagID, _ := archiveTagResult.LastInsertId()
	activeCount := boardTodoSoftCap + 1
	for i := 1; i <= activeCount; i++ {
		result, err := tx.ExecContext(ctx, `
INSERT INTO todos(project_id, local_id, title, body, column_key, rank, created_at, updated_at)
	VALUES (?, ?, ?, '', ?, ?, ?, ?)`, p.ID, i, fmt.Sprintf("active %d", i), DefaultColumnBacklog, int64(i)*1000, now, now)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if i == 1 {
			todoID, _ := result.LastInsertId()
			if _, err := tx.ExecContext(ctx, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, todoID, activeTagID); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
		}
	}
	archivedResult, err := tx.ExecContext(ctx, `
INSERT INTO todos(project_id, local_id, title, body, column_key, rank, created_at, updated_at, archived_at)
	VALUES (?, ?, 'archived fallback', '', ?, ?, ?, ?, ?)`, p.ID, activeCount+1, DefaultColumnBacklog, int64(activeCount+1)*1000, now, now, now)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	archivedID, _ := archivedResult.LastInsertId()
	if _, err := tx.ExecContext(ctx, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, archivedID, archiveTagID); err != nil {
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
	_, tags, _, columns, meta, err := st.GetBoardPaged(ctx, &pc, []string{""}, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault, 20)
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
	tagCounts := map[string]int{}
	for _, tag := range tags {
		tagCounts[tag.Name] = tag.Count
	}
	if tagCounts["scale-active"] != 1 {
		t.Fatalf("above-cap active tag projection=%v", tagCounts)
	}
	if _, exists := tagCounts["scale-archive"]; exists {
		t.Fatalf("above-cap projection leaked archive-only tag: %v", tagCounts)
	}
	_, selectedTags, _, _, _, err := st.GetBoardPaged(ctx, &pc, []string{"scale-archive"}, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault, 20)
	if err != nil {
		t.Fatal(err)
	}
	selectedFound := false
	for _, tag := range selectedTags {
		selectedFound = selectedFound || (tag.Name == "scale-archive" && tag.Count == 0)
	}
	if !selectedFound {
		t.Fatalf("above-cap selected inactive projection=%+v", selectedTags)
	}
}

func TestActiveBoardTagsCanonicalGroupingCountsUniqueTodos(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.CreateUser(ctx, "canonical-owner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithUserID(ctx, owner.ID)
	p, err := st.CreateProject(ctx, "canonical active tags")
	if err != nil {
		t.Fatal(err)
	}
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "one", Tags: []string{"make-space"}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateUser(ctx, "canonical-other@example.com", "password123", "Other")
	if err != nil {
		t.Fatal(err)
	}
	result, err := st.db.ExecContext(ctx, `INSERT INTO tags(user_id, name, created_at) VALUES (?, 'make space', ?)`, other.ID, time.Now().UTC().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	legacyTagID, _ := result.LastInsertId()
	if _, err := st.db.ExecContext(ctx, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, todo.ID, legacyTagID); err != nil {
		t.Fatal(err)
	}
	pc, err := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	_, tags, _, _, err := st.GetBoard(ctx, &pc, []string{""}, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0].Name != "make-space" || tags[0].Count != 1 {
		t.Fatalf("canonical active tags=%+v, want one unique todo", tags)
	}
}
