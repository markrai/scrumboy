package main

import (
	"bytes"
	"context"
	"database/sql"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

func TestRunAppliesMigrationsAndLogsRewriteCount(t *testing.T) {
	sqlDB := openTestDB(t)
	var buf bytes.Buffer

	if err := run(context.Background(), sqlDB, newTestLogger(&buf)); err != nil {
		t.Fatalf("run: %v", err)
	}

	assertContains(t, buf.String(), "rewrote 0 durable project slug(s)")
	if got := schemaMigrationCount(t, sqlDB); got == 0 {
		t.Fatal("schema_migrations count = 0, want at least 1")
	}
}

func TestRunRewritesLegacyDurableProjectSlugs(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()
	if err := migrate.Apply(ctx, sqlDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(sqlDB, nil)
	p1, err := st.CreateProject(ctx, "VO2 Max Coach")
	if err != nil {
		t.Fatalf("CreateProject #1: %v", err)
	}
	p2, err := st.CreateProject(ctx, "VO2 Max Coach")
	if err != nil {
		t.Fatalf("CreateProject #2: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `UPDATE projects SET slug = ? WHERE id = ?`, "deadbeef", p1.ID); err != nil {
		t.Fatalf("set legacy slug p1: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `UPDATE projects SET slug = ? WHERE id = ?`, "cafebabe", p2.ID); err != nil {
		t.Fatalf("set legacy slug p2: %v", err)
	}

	var buf bytes.Buffer
	if err := run(ctx, sqlDB, newTestLogger(&buf)); err != nil {
		t.Fatalf("run: %v", err)
	}

	assertContains(t, buf.String(), "rewrote 2 durable project slug(s)")
	p1r, err := st.GetProject(ctx, p1.ID)
	if err != nil {
		t.Fatalf("GetProject p1: %v", err)
	}
	p2r, err := st.GetProject(ctx, p2.ID)
	if err != nil {
		t.Fatalf("GetProject p2: %v", err)
	}
	if p1r.Slug != "vo2-max-coach" {
		t.Fatalf("p1 slug = %q, want %q", p1r.Slug, "vo2-max-coach")
	}
	if p2r.Slug != "vo2-max-coach-2" {
		t.Fatalf("p2 slug = %q, want %q", p2r.Slug, "vo2-max-coach-2")
	}
}

func TestRunReturnsMigrateErrorForCanceledContext(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var buf bytes.Buffer
	err := run(ctx, sqlDB, newTestLogger(&buf))
	if err == nil {
		t.Fatal("run returned nil error")
	}
	if !strings.Contains(err.Error(), "migrate") {
		t.Fatalf("error = %q, want substring %q", err.Error(), "migrate")
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

func newTestLogger(buf *bytes.Buffer) *log.Logger {
	return log.New(buf, "", 0)
}

func schemaMigrationCount(t *testing.T, sqlDB *sql.DB) int {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	return count
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("output missing %q\noutput:\n%s", want, got)
	}
}
