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

func TestRunPrintsHeadersAndRows(t *testing.T) {
	sqlDB := openTestDB(t)
	execSQL(t, sqlDB, `CREATE TABLE tasks(name TEXT NOT NULL, priority INTEGER NOT NULL)`)
	execSQL(t, sqlDB, `INSERT INTO tasks(name, priority) VALUES (?, ?)`, "second", 2)
	execSQL(t, sqlDB, `INSERT INTO tasks(name, priority) VALUES (?, ?)`, "first", 1)

	var buf bytes.Buffer
	err := run(context.Background(), sqlDB, `SELECT name, priority FROM tasks ORDER BY priority`, &buf)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := buf.String()

	assertContains(t, got, "name | priority")
	assertContains(t, got, "first | 1")
	assertContains(t, got, "second | 2")
	if strings.Index(got, "first | 1") > strings.Index(got, "second | 2") {
		t.Fatalf("rows were not printed in query order\noutput:\n%s", got)
	}
}

func TestRunPrintsNULLValues(t *testing.T) {
	sqlDB := openTestDB(t)
	execSQL(t, sqlDB, `CREATE TABLE tasks(name TEXT NOT NULL, note TEXT)`)
	execSQL(t, sqlDB, `INSERT INTO tasks(name, note) VALUES (?, ?)`, "first", nil)

	var buf bytes.Buffer
	err := run(context.Background(), sqlDB, `SELECT name, note FROM tasks ORDER BY name`, &buf)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	assertContains(t, buf.String(), "first | NULL")
}

func TestRunSupportsExpressionQueries(t *testing.T) {
	sqlDB := openTestDB(t)

	var buf bytes.Buffer
	err := run(context.Background(), sqlDB, `SELECT 1 AS one, 'two' AS two`, &buf)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := buf.String()

	assertContains(t, got, "one | two")
	assertContains(t, got, "1 | two")
}

func TestRunReturnsQueryError(t *testing.T) {
	sqlDB := openTestDB(t)

	var buf bytes.Buffer
	err := run(context.Background(), sqlDB, `SELECT * FROM missing_table`, &buf)
	if err == nil {
		t.Fatal("run returned nil error")
	}
	if !strings.Contains(err.Error(), "query") {
		t.Fatalf("error = %q, want substring %q", err.Error(), "query")
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
