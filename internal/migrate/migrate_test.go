package migrate

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"scrumboy/internal/db"
)

func openMigratedDB(t *testing.T) *sql.DB {
	t.Helper()

	sqlDB := openRawTestDB(t)
	if err := Apply(context.Background(), sqlDB); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return sqlDB
}

func openRawTestDB(t *testing.T) *sql.DB {
	t.Helper()

	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "data", "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB
}

func embeddedMigrationVersions(t *testing.T) []string {
	t.Helper()

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("ReadDir migrations: %v", err)
	}
	var versions []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		versions = append(versions, entry.Name())
	}
	sort.Strings(versions)
	return versions
}

func appliedMigrationVersions(t *testing.T, sqlDB *sql.DB) []string {
	t.Helper()

	rows, err := sqlDB.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	defer rows.Close()

	var versions []string
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan schema_migrations: %v", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("schema_migrations rows: %v", err)
	}
	return versions
}

func pragmaInt(t *testing.T, sqlDB *sql.DB, name string) int {
	t.Helper()

	var got int
	if err := sqlDB.QueryRow("PRAGMA " + name).Scan(&got); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return got
}

func tableExists(t *testing.T, sqlDB *sql.DB, name string) bool {
	t.Helper()
	return sqliteMasterObjectExists(t, sqlDB, "table", name)
}

func columnExists(t *testing.T, sqlDB *sql.DB, table, column string) bool {
	t.Helper()

	rows, err := sqlDB.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &pk); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info(%s) rows: %v", table, err)
	}
	return false
}

func indexExists(t *testing.T, sqlDB *sql.DB, name string) bool {
	t.Helper()
	return sqliteMasterObjectExists(t, sqlDB, "index", name)
}

func triggerExists(t *testing.T, sqlDB *sql.DB, name string) bool {
	t.Helper()
	return sqliteMasterObjectExists(t, sqlDB, "trigger", name)
}

func sqliteMasterObjectExists(t *testing.T, sqlDB *sql.DB, objectType, name string) bool {
	t.Helper()

	var exists bool
	if err := sqlDB.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = ? AND name = ?)`, objectType, name).Scan(&exists); err != nil {
		t.Fatalf("query sqlite_master %s %s: %v", objectType, name, err)
	}
	return exists
}

func TestApplyFreshDatabaseRecordsAllEmbeddedMigrations(t *testing.T) {
	sqlDB := openRawTestDB(t)
	if err := Apply(context.Background(), sqlDB); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	want := embeddedMigrationVersions(t)
	got := appliedMigrationVersions(t, sqlDB)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("applied versions = %v, want %v", got, want)
	}
	if len(got) == 0 {
		t.Fatal("expected at least one migration")
	}
	if got[0] != "001_init.sql" {
		t.Fatalf("first migration = %q, want 001_init.sql", got[0])
	}
	if len(got) != len(want) {
		t.Fatalf("applied count = %d, want %d", len(got), len(want))
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	sqlDB := openRawTestDB(t)
	if err := Apply(context.Background(), sqlDB); err != nil {
		t.Fatalf("Apply #1: %v", err)
	}
	first := appliedMigrationVersions(t, sqlDB)

	if err := Apply(context.Background(), sqlDB); err != nil {
		t.Fatalf("Apply #2: %v", err)
	}
	second := appliedMigrationVersions(t, sqlDB)

	if !reflect.DeepEqual(second, first) {
		t.Fatalf("second applied versions = %v, want %v", second, first)
	}
	var duplicates int
	if err := sqlDB.QueryRow(`
SELECT COUNT(*) FROM (
  SELECT version
  FROM schema_migrations
  GROUP BY version
  HAVING COUNT(*) > 1
)`).Scan(&duplicates); err != nil {
		t.Fatalf("query duplicate migrations: %v", err)
	}
	if duplicates != 0 {
		t.Fatalf("duplicate schema_migrations rows = %d, want 0", duplicates)
	}
}

func TestApplyCreatesCurrentSchemaLandmarks(t *testing.T) {
	sqlDB := openMigratedDB(t)

	for _, table := range []string{
		"projects",
		"todos",
		"users",
		"sessions",
		"project_members",
		"project_workflow_columns",
		"todo_assignee_events",
		"audit_events",
		"api_tokens",
		"user_oidc_identities",
		"webhooks",
		"push_subscriptions",
		"project_walls",
	} {
		if !tableExists(t, sqlDB, table) {
			t.Fatalf("expected table %s to exist", table)
		}
	}

	for _, tc := range []struct {
		table  string
		column string
	}{
		{table: "projects", column: "dominant_color"},
		{table: "projects", column: "import_metadata"},
		{table: "todos", column: "column_key"},
		{table: "todos", column: "done_at"},
		{table: "todos", column: "import_metadata"},
		{table: "project_walls", column: "edges"},
		{table: "users", column: "two_factor_enabled"},
	} {
		if !columnExists(t, sqlDB, tc.table, tc.column) {
			t.Fatalf("expected column %s.%s to exist", tc.table, tc.column)
		}
	}

	for _, index := range []string{
		"idx_projects_slug_production",
		"idx_todos_project_local_id",
		"idx_todos_project_column_key_rank_id",
	} {
		if !indexExists(t, sqlDB, index) {
			t.Fatalf("expected index %s to exist", index)
		}
	}

	for _, trigger := range []string{
		"trg_audit_events_no_update",
		"trg_todo_assignee_events_no_delete",
	} {
		if !triggerExists(t, sqlDB, trigger) {
			t.Fatalf("expected trigger %s to exist", trigger)
		}
	}
}

func TestApplyLeavesForeignKeysEnabled(t *testing.T) {
	sqlDB := openMigratedDB(t)

	if got := pragmaInt(t, sqlDB, "foreign_keys"); got != 1 {
		t.Fatalf("PRAGMA foreign_keys = %d, want 1", got)
	}
}

func TestApplyWithCanceledContextReturnsError(t *testing.T) {
	sqlDB := openRawTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := Apply(ctx, sqlDB); err == nil {
		t.Fatal("expected error")
	}
}
