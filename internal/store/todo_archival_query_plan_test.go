package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func explainTodoArchivalPlan(t *testing.T, st *Store, query string, args ...any) string {
	t.Helper()
	rows, err := st.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain %q: %v", query, err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(details, " | ")
}

func TestTodoArchivalPartialIndexesServeScaleQueryShapes(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.CreateUser(ctx, "archive-plan@example.com", "password123", "Archive Plan")
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithUserID(ctx, user.ID)
	p, err := st.CreateProject(ctx, "archive query plans")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixMilli()
	tagResult, err := tx.ExecContext(ctx, `INSERT INTO tags(user_id, project_id, name, created_at) VALUES (?, NULL, 'scale', ?)`, user.ID, now)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	tagID, _ := tagResult.LastInsertId()
	for i := 1; i <= 10050; i++ {
		column := DefaultColumnBacklog
		if i%2 == 0 {
			column = DefaultColumnDoing
		}
		var archivedAt any
		if i <= 9500 {
			archivedAt = now - int64(i)
		}
		result, err := tx.ExecContext(ctx, `
INSERT INTO todos(project_id, local_id, title, body, column_key, rank, assignee_user_id, created_at, updated_at, archived_at)
VALUES (?, ?, ?, '', ?, ?, ?, ?, ?, ?)`, p.ID, i, fmt.Sprintf("scale %d", i), column, int64(i)*1000, user.ID, now-int64(i), now-int64(i), archivedAt)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if i%50 == 0 {
			todoID, _ := result.LastInsertId()
			if _, err := tx.ExecContext(ctx, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, todoID, tagID); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, query string
		indexes     []string
		args        []any
	}{
		{
			name:  "active lane read",
			query: `SELECT id FROM todos WHERE project_id = ? AND column_key = ? AND archived_at IS NULL ORDER BY rank, id LIMIT ?`,
			args:  []any{p.ID, DefaultColumnBacklog, 20}, indexes: []string{"idx_todos_active_project_column_rank_id"},
		},
		{
			name:  "active lane count",
			query: `SELECT COUNT(*) FROM todos WHERE project_id = ? AND column_key = ? AND archived_at IS NULL`,
			args:  []any{p.ID, DefaultColumnBacklog}, indexes: []string{
				"idx_todos_active_project_column_rank_id",
				"idx_todos_active_project_column_created_at",
			},
		},
		{
			name:  "active sprint lane read",
			query: `SELECT id FROM todos WHERE project_id = ? AND column_key = ? AND sprint_id = ? AND archived_at IS NULL ORDER BY rank, id LIMIT ?`,
			args:  []any{p.ID, DefaultColumnBacklog, 7, 20}, indexes: []string{"idx_todos_active_project_column_sprint_rank_id"},
		},
		{
			name:  "active chronological lane read",
			query: `SELECT id FROM todos WHERE project_id = ? AND column_key = ? AND archived_at IS NULL ORDER BY created_at, id LIMIT ?`,
			args:  []any{p.ID, DefaultColumnBacklog, 20}, indexes: []string{"idx_todos_active_project_column_created_at"},
		},
		{
			name:  "current dashboard",
			query: `SELECT id FROM todos WHERE assignee_user_id = ? AND archived_at IS NULL ORDER BY updated_at DESC, id DESC LIMIT ?`,
			args:  []any{user.ID, 20}, indexes: []string{"idx_todos_active_assignee_updated"},
		},
		{
			name:  "default link search",
			query: `SELECT id FROM todos WHERE project_id = ? AND archived_at IS NULL ORDER BY updated_at DESC, local_id ASC LIMIT ?`,
			args:  []any{p.ID, 20}, indexes: []string{"idx_todos_active_project_updated_local_id"},
		},
		{
			name:  "archive page",
			query: `SELECT id FROM todos WHERE project_id = ? AND archived_at IS NOT NULL ORDER BY archived_at DESC, id DESC LIMIT ?`,
			args:  []any{p.ID, 51}, indexes: []string{"idx_todos_archived_project_archived_at_id"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := explainTodoArchivalPlan(t, st, tc.query, tc.args...)
			matched := false
			for _, index := range tc.indexes {
				matched = matched || strings.Contains(plan, index)
			}
			if !matched {
				t.Fatalf("query plan=%q want one of indexes %v", plan, tc.indexes)
			}
		})
	}

	// Characterize the tag overlay at scale: the join may begin from todo_tags,
	// but active archive filtering must still be present and the result correct.
	counts, err := st.activeBoardTagCounts(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if counts["scale"] != 11 {
		t.Fatalf("active tagged todo count=%d want 11", counts["scale"])
	}
}
