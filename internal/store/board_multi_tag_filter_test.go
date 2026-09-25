package store

import (
	"context"
	"math"
	"strconv"
	"testing"
	"time"
)

func TestBoardMultiTagFilter_DurableANDCompositionAndMetadata(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "multi-tag-owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateUser(ctx, "multi-tag-other@example.com", "password", "Other")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	otherCtx := WithUserID(ctx, other.ID)
	project, err := st.CreateProject(ownerCtx, "multi tag durable")
	if err != nil {
		t.Fatal(err)
	}
	plAddMember(t, st, project.ID, other.ID, RoleMaintainer)
	sprint, err := st.CreateSprint(ownerCtx, project.ID, "Target", time.UnixMilli(1000), time.UnixMilli(2000))
	if err != nil {
		t.Fatal(err)
	}
	high := "high"
	match, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{
		Title:          "Needle canonical match",
		Tags:           []string{"make-space", "bug"},
		AssigneeUserID: &owner.ID,
		PriorityKey:    &high,
		SprintID:       &sprint.ID,
	}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := st.CreateTodo(otherCtx, project.ID, CreateTodoInput{Title: "legacy pair", Tags: []string{"bug"}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	legacyTagID := plInsertPersonalTagRow(t, st, other.ID, "make space")
	if _, err := st.db.ExecContext(ctx, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, legacy.ID, legacyTagID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, match.ID, legacyTagID); err != nil {
		t.Fatal(err)
	}
	plNewTodo(t, st, ownerCtx, project.ID, "canonical only", "make-space")
	plNewTodo(t, st, ownerCtx, project.ID, "bug only", "bug")
	plNewTodo(t, st, ownerCtx, project.ID, "unrelated", "feature")
	threeWay, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "three-way", Tags: []string{"make-space", "bug", "feature"}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}

	pc, err := st.GetProjectContextForRead(ownerCtx, project.ID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	wantPairs := []string{"Needle canonical match", "legacy pair", "three-way"}
	for _, selected := range [][]string{
		{"make-space", "bug"},
		{" make space ", "BUG", "make-space", "bug"},
	} {
		_, tags, _, cols, err := st.GetBoard(ownerCtx, &pc, selected, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault)
		if err != nil {
			t.Fatalf("GetBoard(%v): %v", selected, err)
		}
		if got := plBoardTitles(cols); !plEqualStrings(got, wantPairs) {
			t.Fatalf("GetBoard(%v) titles=%v want=%v", selected, got, wantPairs)
		}
		if tag := plFindTag(tags, "make-space"); tag == nil || tag.Count != 4 {
			t.Fatalf("project-wide make-space metadata=%+v, want unique count 4", tag)
		}
		if tag := plFindTag(tags, "bug"); tag == nil || tag.Count != 4 {
			t.Fatalf("project-wide bug metadata=%+v, want count 4", tag)
		}
	}
	_, _, _, threeCols, err := st.GetBoard(ownerCtx, &pc, []string{"make-space", "bug", "feature"}, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault)
	if err != nil {
		t.Fatal(err)
	}
	if got := plBoardTitles(threeCols); !plEqualStrings(got, []string{threeWay.Title}) {
		t.Fatalf("three-tag AND titles=%v want=%v", got, []string{threeWay.Title})
	}

	assignee := mustAssigneeFilter(t, strconv.FormatInt(owner.ID, 10), nil)
	priority := mustPriorityFilter(t, high)
	_, _, _, composedCols, composedMeta, err := st.GetBoardPaged(
		ownerCtx, &pc, []string{"make-space", "bug"}, "needle", assignee, priority,
		SprintFilter{Mode: "sprint", SprintID: sprint.ID}, SortOrderDefault, 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := plBoardTitles(composedCols); !plEqualStrings(got, []string{match.Title}) {
		t.Fatalf("composed filter titles=%v want=%v", got, []string{match.Title})
	}
	if got := composedMeta[DefaultColumnBacklog].TotalCount; got != 1 {
		t.Fatalf("composed lane total=%d want=1", got)
	}

	page1, cursor, hasMore, err := st.ListTodosForBoardLane(
		ownerCtx, project.ID, DefaultColumnBacklog, 2, math.MinInt64, 0,
		[]string{"make-space", "bug"}, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || !hasMore {
		t.Fatalf("first multi-tag lane page len=%d hasMore=%v", len(page1), hasMore)
	}
	afterRank, afterID := ParseLaneCursor(cursor)
	page2, _, hasMore, err := st.ListTodosForBoardLane(
		ownerCtx, project.ID, DefaultColumnBacklog, 2, afterRank, afterID,
		[]string{"make-space", "bug"}, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault,
	)
	if err != nil {
		t.Fatal(err)
	}
	if hasMore || !plEqualStrings(plTitlesOf(append(page1, page2...)), wantPairs) {
		t.Fatalf("multi-tag continuation pages=%v hasMore=%v", plTitlesOf(append(page1, page2...)), hasMore)
	}
	count, err := st.CountTodosForBoardLane(ownerCtx, project.ID, DefaultColumnBacklog, []string{"make-space", "bug"}, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"})
	if err != nil || count != 3 {
		t.Fatalf("multi-tag lane count=%d err=%v want=3", count, err)
	}

	_, _, _, emptyCols, err := st.GetBoard(ownerCtx, &pc, []string{"bug", "missing"}, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault)
	if err != nil {
		t.Fatal(err)
	}
	if got := plBoardTitles(emptyCols); len(got) != 0 {
		t.Fatalf("one unresolved group must fail closed, got %v", got)
	}

	plInsertBoardScopedTag(t, st, project.ID, "ancient", nil)
	lostID := plInsertPersonalTagRow(t, st, owner.ID, "lost")
	if _, err := st.db.ExecContext(ctx, `INSERT INTO project_tags(project_id, tag_id, created_at) VALUES (?, ?, ?)`, project.ID, lostID, time.Now().UTC().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	_, selectedTags, _, selectedCols, err := st.GetBoard(ownerCtx, &pc, []string{"ancient", "lost"}, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault)
	if err != nil {
		t.Fatal(err)
	}
	if got := plBoardTitles(selectedCols); len(got) != 0 {
		t.Fatalf("inactive selected groups returned todos: %v", got)
	}
	for _, name := range []string{"ancient", "lost"} {
		if tag := plFindTag(selectedTags, name); tag == nil || tag.Count != 0 {
			t.Fatalf("inactive selected tag %q not preserved at zero count: %+v", name, selectedTags)
		}
	}
}

func TestBoardMultiTagFilter_TemporaryBoardKeepsExactRowLabels(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "multi-tag-temp@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "multi tag temporary")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().UTC().Add(24*time.Hour).UnixMilli(), project.ID); err != nil {
		t.Fatal(err)
	}
	canonical, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "canonical", Tags: []string{"make-space", "bug"}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "legacy", Tags: []string{"bug"}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	legacyTagID := plInsertPersonalTagRow(t, st, owner.ID, "make space")
	if _, err := st.db.ExecContext(ctx, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, legacy.ID, legacyTagID); err != nil {
		t.Fatal(err)
	}
	pc, err := st.GetProjectContextForRead(ownerCtx, project.ID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		selected []string
		want     []string
	}{
		{selected: []string{"make-space", "bug"}, want: []string{canonical.Title}},
		{selected: []string{"make space", "bug"}, want: []string{legacy.Title}},
		{selected: []string{"make-space", "make space", "bug"}, want: nil},
	} {
		_, _, _, cols, err := st.GetBoard(ownerCtx, &pc, tc.selected, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault)
		if err != nil {
			t.Fatalf("GetBoard(%v): %v", tc.selected, err)
		}
		if got := plBoardTitles(cols); !plEqualStrings(got, tc.want) {
			t.Fatalf("GetBoard(%v) titles=%v want=%v", tc.selected, got, tc.want)
		}
	}
}

func TestBoardMultiTagFilter_AboveSoftCapUsesANDForPagedLaneFallback(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "multi-tag-soft-cap@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "multi tag soft cap")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "scale-1", Tags: []string{"scale-a", "scale-b"}}, ModeFull); err != nil {
		t.Fatal(err)
	}
	var tagA, tagB int64
	if err := st.db.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id = ? AND name = 'scale-a'`, owner.ID).Scan(&tagA); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id = ? AND name = 'scale-b'`, owner.ID).Scan(&tagB); err != nil {
		t.Fatal(err)
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixMilli()
	for i := 2; i <= boardTodoSoftCap+1; i++ {
		result, err := tx.ExecContext(ctx, `
INSERT INTO todos(project_id, local_id, title, body, column_key, rank, created_at, updated_at)
VALUES (?, ?, ?, '', ?, ?, ?, ?)`, project.ID, i, "scale", DefaultColumnBacklog, int64(i)*1000, now, now)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		todoID, _ := result.LastInsertId()
		if _, err := tx.ExecContext(ctx, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?), (?, ?)`, todoID, tagA, todoID, tagB); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	pc, err := st.GetProjectContextForRead(ownerCtx, project.ID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, cols, meta, err := st.GetBoardPaged(ownerCtx, &pc, []string{"scale-a", "scale-b"}, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(cols[DefaultColumnBacklog]); got != 2 {
		t.Fatalf("soft-cap fallback page len=%d want=2", got)
	}
	gotMeta := meta[DefaultColumnBacklog]
	if gotMeta.TotalCount != boardTodoSoftCap+1 || !gotMeta.HasMore || gotMeta.NextCursor == "" {
		t.Fatalf("soft-cap fallback metadata=%+v", gotMeta)
	}
}
