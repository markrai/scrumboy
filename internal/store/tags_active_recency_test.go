package store

import (
	"context"
	"testing"
	"time"
)

func TestListActiveBoardTags_DurableRecencyUsesLatestUniqueActiveStoryAcrossAliases(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "tag-recency@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "Tag recency")
	if err != nil {
		t.Fatal(err)
	}

	create := func(title string) Todo {
		t.Helper()
		todo, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: title}, ModeFull)
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		return todo
	}
	old := create("old")
	done := create("done")
	duplicate := create("duplicate aliases")
	archived := create("newer but archived")

	aliasA := plInsertBoardScopedTag(t, st, project.ID, "make-space", nil)
	aliasB := plInsertBoardScopedTag(t, st, project.ID, "make space", nil)
	historical := plInsertBoardScopedTag(t, st, project.ID, "historical", nil)
	attach := func(todoID int64, tagIDs ...int64) {
		t.Helper()
		for _, tagID := range tagIDs {
			if _, err := st.db.ExecContext(ctx, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, todoID, tagID); err != nil {
				t.Fatalf("attach tag %d to todo %d: %v", tagID, todoID, err)
			}
		}
	}
	attach(old.ID, aliasA)
	attach(done.ID, aliasB)
	attach(duplicate.ID, aliasA, aliasB)
	attach(archived.ID, aliasA, historical)

	base := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	updates := []struct {
		id int64
		at time.Time
	}{
		{old.ID, base.Add(time.Minute)},
		{duplicate.ID, base.Add(2 * time.Minute)},
		{done.ID, base.Add(3 * time.Minute)},
		{archived.ID, base.Add(4 * time.Minute)},
	}
	for _, update := range updates {
		if _, err := st.db.ExecContext(ctx, `UPDATE todos SET updated_at = ? WHERE id = ?`, update.at.UnixMilli(), update.id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE todos SET column_key = ?, done_at = ? WHERE id = ?`, DefaultColumnDone, base.UnixMilli(), done.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE todos SET archived_at = ? WHERE id = ?`, base.Add(5*time.Minute).UnixMilli(), archived.ID); err != nil {
		t.Fatal(err)
	}

	tags, err := st.listActiveBoardTags(ownerCtx, project.ID, &owner.ID, nil, true, []string{"historical"})
	if err != nil {
		t.Fatal(err)
	}
	grouped := plFindTag(tags, "make-space")
	if grouped == nil {
		t.Fatalf("missing grouped alias tag: %#v", tags)
	}
	if grouped.Count != 3 {
		t.Fatalf("grouped count=%d want 3 unique active stories", grouped.Count)
	}
	if grouped.LastActiveAt == nil || !grouped.LastActiveAt.Equal(base.Add(3*time.Minute)) {
		t.Fatalf("LastActiveAt=%v want done/unarchived story time %v", grouped.LastActiveAt, base.Add(3*time.Minute))
	}
	historicalTag := plFindTag(tags, "historical")
	if historicalTag == nil || historicalTag.Count != 0 || historicalTag.LastActiveAt != nil {
		t.Fatalf("historical selected exception=%#v want count zero without recency", historicalTag)
	}
}

func TestListActiveBoardTags_TemporaryRecencyStaysPhysicalRowScoped(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "first", Tags: []string{"bug"}}, ModeAnonymous)
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "second", Tags: []string{"feature"}}, ModeAnonymous)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 16, 15, 0, 0, 0, time.UTC)
	if _, err := st.db.ExecContext(ctx, `UPDATE todos SET updated_at = ? WHERE id = ?`, base.UnixMilli(), first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE todos SET updated_at = ? WHERE id = ?`, base.Add(time.Hour).UnixMilli(), second.ID); err != nil {
		t.Fatal(err)
	}

	tags, err := st.listActiveBoardTags(ctx, project.ID, nil, nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	bug := plFindTag(tags, "bug")
	feature := plFindTag(tags, "feature")
	if bug == nil || bug.LastActiveAt == nil || !bug.LastActiveAt.Equal(base) {
		t.Fatalf("bug recency=%#v want %v", bug, base)
	}
	if feature == nil || feature.LastActiveAt == nil || !feature.LastActiveAt.Equal(base.Add(time.Hour)) {
		t.Fatalf("feature recency=%#v want %v", feature, base.Add(time.Hour))
	}
	if plFindTag(tags, "blocking") != nil {
		t.Fatalf("unused anonymous default tag leaked into active projection: %#v", tags)
	}
}
