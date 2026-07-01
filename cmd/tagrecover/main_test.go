package main

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"scrumboy/internal/db"
)

func TestRunReportsRecoverySummaryAndTags(t *testing.T) {
	sqlDB := openTestDB(t)
	createTagRecoverSchema(t, sqlDB)

	execSQL(t, sqlDB, `INSERT INTO todos(project_id) VALUES (?)`, 10)
	execSQL(t, sqlDB, `INSERT INTO todos(project_id) VALUES (?)`, 20)
	execSQL(t, sqlDB, `INSERT INTO todos(project_id) VALUES (?)`, 10)
	execSQL(t, sqlDB, `INSERT INTO tags(id, name) VALUES (?, ?)`, 1, "feature")
	execSQL(t, sqlDB, `INSERT INTO tags(id, name) VALUES (?, ?)`, 2, "bug")
	execSQL(t, sqlDB, `INSERT INTO tags(id, name) VALUES (?, ?)`, 3, "zzz")

	var buf bytes.Buffer
	if err := run(context.Background(), sqlDB, &buf); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := buf.String()

	assertContains(t, got, "Total todos in database: 3")
	assertContains(t, got, "Projects that might have had these tags: 2")
	assertContains(t, got, "Available tags (these can be re-applied to todos):")
	assertInOrder(t, got,
		"  - bug (id=2)",
		"  - feature (id=1)",
		"  - zzz (id=3)",
	)
	assertContains(t, got, "⚠️  RECOVERY STATUS:")
	assertContains(t, got, "  - Tags preserved: YES (8 tags exist)")
	assertContains(t, got, "  - Tag-todo relationships: LOST (0 relationships)")
	assertContains(t, got, "  - Recovery possible: NO (relationships cannot be reconstructed without backup)")
	assertContains(t, got, "  ACTION REQUIRED:")
	assertContains(t, got, "  You will need to manually re-tag your todos using the tag names listed above.")
	assertContains(t, got, "  The tags themselves are available and ready to use.")
}

func TestRunHandlesNoTagsOrTodos(t *testing.T) {
	sqlDB := openTestDB(t)
	createTagRecoverSchema(t, sqlDB)

	var buf bytes.Buffer
	if err := run(context.Background(), sqlDB, &buf); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := buf.String()

	assertContains(t, got, "Total todos in database: 0")
	assertContains(t, got, "Projects that might have had these tags: 0")
	assertContains(t, got, "Available tags (these can be re-applied to todos):")
	assertContains(t, got, "⚠️  RECOVERY STATUS:")
	assertContains(t, got, "  ACTION REQUIRED:")
	assertNotContains(t, got, "(id=")
}

func TestRunReturnsCountTodosError(t *testing.T) {
	sqlDB := openTestDB(t)

	var buf bytes.Buffer
	err := run(context.Background(), sqlDB, &buf)
	if err == nil {
		t.Fatal("run returned nil error")
	}
	if !strings.Contains(err.Error(), "count todos") {
		t.Fatalf("error = %q, want substring %q", err.Error(), "count todos")
	}
}

func TestRunReturnsMissingTagsError(t *testing.T) {
	sqlDB := openTestDB(t)
	execSQL(t, sqlDB, `CREATE TABLE todos(project_id INTEGER)`)

	var buf bytes.Buffer
	err := run(context.Background(), sqlDB, &buf)
	if err == nil {
		t.Fatal("run returned nil error")
	}
	if !strings.Contains(err.Error(), "count todos with possible tags") {
		t.Fatalf("error = %q, want substring %q", err.Error(), "count todos with possible tags")
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "data", "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB
}

func createTagRecoverSchema(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	execSQL(t, sqlDB, `CREATE TABLE todos(project_id INTEGER)`)
	execSQL(t, sqlDB, `CREATE TABLE tags(id INTEGER PRIMARY KEY, name TEXT NOT NULL)`)
}

func execSQL(t *testing.T, sqlDB *sql.DB, stmt string, args ...any) {
	t.Helper()
	if _, err := sqlDB.ExecContext(context.Background(), stmt, args...); err != nil {
		t.Fatalf("exec %q: %v", stmt, err)
	}
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("output missing %q\noutput:\n%s", want, got)
	}
}

func assertNotContains(t *testing.T, got, unwanted string) {
	t.Helper()
	if strings.Contains(got, unwanted) {
		t.Fatalf("output unexpectedly contained %q\noutput:\n%s", unwanted, got)
	}
}

func assertInOrder(t *testing.T, got string, wants ...string) {
	t.Helper()
	last := -1
	for _, want := range wants {
		next := strings.Index(got, want)
		if next == -1 {
			t.Fatalf("output missing %q\noutput:\n%s", want, got)
		}
		if next < last {
			t.Fatalf("output item %q appeared out of order\noutput:\n%s", want, got)
		}
		last = next
	}
}
