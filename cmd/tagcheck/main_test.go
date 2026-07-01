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

func TestRunReportsTagSummaryAndSamples(t *testing.T) {
	sqlDB := openTestDB(t)
	createTagCheckSchema(t, sqlDB)

	execSQL(t, sqlDB, `INSERT INTO tags(id, name, scope, project_id) VALUES (?, ?, ?, ?)`, 1, "feature", "GLOBAL", nil)
	execSQL(t, sqlDB, `INSERT INTO tags(id, name, scope, project_id) VALUES (?, ?, ?, ?)`, 2, "bug", nil, nil)
	execSQL(t, sqlDB, `INSERT INTO tags(id, name, scope, project_id) VALUES (?, ?, ?, ?)`, 3, "techdebt", "PROJECT", 42)
	execSQL(t, sqlDB, `INSERT INTO tags(id, name, scope, project_id) VALUES (?, ?, ?, ?)`, 4, "unused", "GLOBAL", nil)
	execSQL(t, sqlDB, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, 100, 1)
	execSQL(t, sqlDB, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, 101, 2)
	execSQL(t, sqlDB, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, 102, 3)
	execSQL(t, sqlDB, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, 103, 999)

	var buf bytes.Buffer
	if err := run(context.Background(), sqlDB, &buf); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := buf.String()

	assertContains(t, got, "Total tags in database: 4")
	assertContains(t, got, "Tags by scope:")
	assertContains(t, got, "  scope='GLOBAL': 2 tags")
	assertContains(t, got, "  scope=NULL: 1 tags")
	assertContains(t, got, "  scope='PROJECT': 1 tags")
	assertContains(t, got, "GLOBAL tags (scope='GLOBAL' AND project_id IS NULL): 2")
	assertContains(t, got, "Total todo_tags relationships: 4")
	assertContains(t, got, "Orphaned todo_tags (referencing non-existent tags): 1")
	assertContains(t, got, "Tags that are actually used by todos: 3")
	assertContains(t, got, "Sample tags (first 10):")
	assertContains(t, got, "  id=1 name='feature' scope=GLOBAL project_id=NULL todo_count=1")
	assertContains(t, got, "  id=2 name='bug' scope=NULL project_id=NULL todo_count=1")
	assertContains(t, got, "  id=3 name='techdebt' scope=PROJECT project_id=42 todo_count=1")
	assertContains(t, got, "  id=4 name='unused' scope=GLOBAL project_id=NULL todo_count=0")
}

func TestRunWarnsWhenUsedTagsHaveWrongScope(t *testing.T) {
	sqlDB := openTestDB(t)
	createTagCheckSchema(t, sqlDB)

	execSQL(t, sqlDB, `INSERT INTO tags(id, name, scope, project_id) VALUES (?, ?, ?, ?)`, 1, "bug", nil, nil)
	execSQL(t, sqlDB, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, 100, 1)

	var buf bytes.Buffer
	if err := run(context.Background(), sqlDB, &buf); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := buf.String()

	assertContains(t, got, "WARNING: 1 tags used by todos have wrong scope")
	assertContains(t, got, "   These tags exist but won't show up in full mode queries!")
}

func TestRunOmitsWrongScopeWarningWhenUsedTagsAreGlobal(t *testing.T) {
	sqlDB := openTestDB(t)
	createTagCheckSchema(t, sqlDB)

	execSQL(t, sqlDB, `INSERT INTO tags(id, name, scope, project_id) VALUES (?, ?, ?, ?)`, 1, "feature", "GLOBAL", nil)
	execSQL(t, sqlDB, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, 100, 1)

	var buf bytes.Buffer
	if err := run(context.Background(), sqlDB, &buf); err != nil {
		t.Fatalf("run: %v", err)
	}

	assertNotContains(t, buf.String(), "WARNING:")
}

func TestRunReturnsQueryError(t *testing.T) {
	sqlDB := openTestDB(t)

	var buf bytes.Buffer
	err := run(context.Background(), sqlDB, &buf)
	if err == nil {
		t.Fatal("run returned nil error")
	}
	if !strings.Contains(err.Error(), "count tags") {
		t.Fatalf("error = %q, want substring %q", err.Error(), "count tags")
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

func createTagCheckSchema(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	execSQL(t, sqlDB, `CREATE TABLE tags(id INTEGER PRIMARY KEY, name TEXT NOT NULL, scope TEXT, project_id INTEGER)`)
	execSQL(t, sqlDB, `CREATE TABLE todo_tags(todo_id INTEGER NOT NULL, tag_id INTEGER NOT NULL)`)
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
