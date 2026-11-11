package migrate

import (
	"context"
	"testing"
)

func TestMigration087And088AddDrawingStorageAdditively(t *testing.T) {
	ctx := context.Background()
	database := openRawTestDB(t)
	applyMigrationsBefore(t, database, "087_add_todo_drawings.sql")
	if _, err := database.ExecContext(ctx, `INSERT INTO projects(id, name, slug, created_at, updated_at) VALUES (1, 'P', 'p', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO project_walls(project_id, notes, edges, version, updated_at) VALUES (1, '[{"id":"n"}]', '[]', 4, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, database); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"id", "todo_id", "project_id", "data", "version", "created_at", "updated_at"} {
		if !columnExists(t, database, "todo_drawings", col) {
			t.Fatalf("todo_drawings.%s missing", col)
		}
	}
	for _, index := range []string{"idx_todo_drawings_todo_id", "idx_todo_drawings_project_id"} {
		if !indexExists(t, database, index) {
			t.Fatalf("%s missing", index)
		}
	}
	var notes, drawings string
	var version int64
	if err := database.QueryRowContext(ctx, `SELECT notes, drawings, version FROM project_walls WHERE project_id = 1`).Scan(&notes, &drawings, &version); err != nil {
		t.Fatal(err)
	}
	if notes != `[{"id":"n"}]` || drawings != "[]" || version != 4 {
		t.Fatalf("existing wall changed: notes=%s drawings=%s version=%d", notes, drawings, version)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO todos(id, project_id, local_id, title, body, column_key, rank, created_at, updated_at) VALUES (5, 1, 1, 't', '', 'backlog', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO todo_drawings(id, todo_id, project_id, data, version, created_at, updated_at) VALUES ('drw_abcdefgh', 5, 1, '{}', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM todos WHERE id = 5`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM todo_drawings`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("cascade rows=%d err=%v", n, err)
	}
}

func TestMigration089AddsNotesInkTableAdditively(t *testing.T) {
	ctx := context.Background()
	database := openRawTestDB(t)
	applyMigrationsBefore(t, database, "089_add_todo_notes_ink.sql")
	mustExec := func(q string) {
		t.Helper()
		if _, err := database.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	mustExec("INSERT INTO projects(id, name, slug, created_at, updated_at) VALUES (1, 'P', 'p', 1, 1)")
	mustExec("INSERT INTO todos(id, project_id, local_id, title, body, column_key, rank, created_at, updated_at) VALUES (5, 1, 1, 't', '', 'backlog', 1, 1, 1)")
	mustExec("INSERT INTO todo_drawings(id, todo_id, project_id, data, version, created_at, updated_at) VALUES ('drw_abcdefgh', 5, 1, '{}', 1, 1, 1)")
	if err := Apply(ctx, database); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"todo_id", "project_id", "data", "version", "created_at", "updated_at"} {
		if !columnExists(t, database, "todo_notes_ink", col) {
			t.Fatalf("todo_notes_ink.%s missing", col)
		}
	}
	var n int
	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM todo_drawings").Scan(&n); err != nil || n != 1 {
		t.Fatalf("existing drawing rows changed: %d %v", n, err)
	}
	mustExec("INSERT INTO todo_notes_ink(todo_id, project_id, data, version, created_at, updated_at) VALUES (5, 1, '{}', 1, 1, 1)")
	if _, err := database.ExecContext(ctx, "INSERT INTO todo_notes_ink(todo_id, project_id, data, version, created_at, updated_at) VALUES (5, 1, '{}', 1, 1, 1)"); err == nil {
		t.Fatal("second ink row for one story accepted")
	}
	mustExec("DELETE FROM todos WHERE id = 5")
	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM todo_notes_ink").Scan(&n); err != nil || n != 0 {
		t.Fatalf("cascade rows=%d err=%v", n, err)
	}
}
