package migrate

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

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
		{table: "oauth_auth_codes", column: "resource"},
		{table: "oauth_access_tokens", column: "resource"},
		{table: "oauth_refresh_tokens", column: "resource"},
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

func TestMigration057InvalidatesUnboundArtifactsAndPreservesClients(t *testing.T) {
	ctx := context.Background()
	sqlDB := openRawTestDB(t)
	if _, err := sqlDB.ExecContext(ctx, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	for _, version := range embeddedMigrationVersions(t) {
		if version == "057_bind_oauth_tokens_to_mcp_resource.sql" {
			break
		}
		if err := applyOne(ctx, sqlDB, version); err != nil {
			t.Fatalf("apply %s: %v", version, err)
		}
	}

	now := time.Now().UTC().UnixMilli()
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO users(id, email, created_at, name, system_role) VALUES (1, 'owner@example.com', ?, 'Owner', 'owner')`, now); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO oauth_clients(id, client_name, redirect_uri, created_at) VALUES ('client-1', 'Client', 'http://127.0.0.1/callback', ?)`, now); err != nil {
		t.Fatalf("insert client: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO oauth_auth_codes(code_hash, client_id, user_id, redirect_uri, code_challenge, code_challenge_method, created_at, expires_at) VALUES ('code', 'client-1', 1, 'http://127.0.0.1/callback', 'challenge', 'S256', ?, ?)`, now, now+60000); err != nil {
		t.Fatalf("insert code: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO oauth_access_tokens(token_hash, client_id, user_id, created_at, expires_at) VALUES ('access', 'client-1', 1, ?, ?)`, now, now+60000); err != nil {
		t.Fatalf("insert access token: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO oauth_refresh_tokens(token_hash, client_id, user_id, created_at, expires_at) VALUES ('refresh', 'client-1', 1, ?, ?)`, now, now+60000); err != nil {
		t.Fatalf("insert refresh token: %v", err)
	}

	if err := applyOne(ctx, sqlDB, "057_bind_oauth_tokens_to_mcp_resource.sql"); err != nil {
		t.Fatalf("apply migration 057: %v", err)
	}
	for _, table := range []string{"oauth_auth_codes", "oauth_access_tokens", "oauth_refresh_tokens"} {
		var count int
		if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d unbound artifacts", table, count)
		}
		if !columnExists(t, sqlDB, table, "resource") {
			t.Fatalf("%s.resource is missing", table)
		}
	}
	for _, table := range []string{"users", "oauth_clients"} {
		var count int
		if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("%s count = %d, want 1", table, count)
		}
	}
	if got := pragmaInt(t, sqlDB, "foreign_keys"); got != 1 {
		t.Fatalf("foreign_keys = %d, want 1", got)
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

// TestMigration059ClassifiesExistingPreferencesAsLegacy proves that a
// user_preferences row written before migration 059 is classified as 'legacy'
// (unknown writer, never auto-updated by a future bulk-apply), and that the
// migration leaves the row's value and updated_at untouched.
func TestMigration059ClassifiesExistingPreferencesAsLegacy(t *testing.T) {
	ctx := context.Background()
	sqlDB := openRawTestDB(t)
	if _, err := sqlDB.ExecContext(ctx, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	const target = "059_add_user_preferences_provenance.sql"
	for _, version := range embeddedMigrationVersions(t) {
		if version == target {
			break
		}
		if err := applyOne(ctx, sqlDB, version); err != nil {
			t.Fatalf("apply %s: %v", version, err)
		}
	}

	now := time.Now().UTC().UnixMilli()
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO users(id, email, created_at, name, system_role) VALUES (1, 'owner@example.com', ?, 'Owner', 'owner')`, now); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	const value = `{"v":1,"enabled":true,"assigned":true,"cardActivity":false,"sprintActivity":false,"projectActivity":false,"addedToProject":true}`
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO user_preferences(user_id, key, value, updated_at) VALUES (1, 'emailNotifications', ?, ?)`, value, now); err != nil {
		t.Fatalf("insert pre-059 preference: %v", err)
	}

	if err := applyOne(ctx, sqlDB, target); err != nil {
		t.Fatalf("apply migration 059: %v", err)
	}

	var gotValue, gotProvenance string
	var gotUpdatedAt int64
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT value, provenance, updated_at FROM user_preferences WHERE user_id = 1 AND key = 'emailNotifications'`,
	).Scan(&gotValue, &gotProvenance, &gotUpdatedAt); err != nil {
		t.Fatalf("read migrated preference: %v", err)
	}
	if gotProvenance != "legacy" {
		t.Fatalf("expected provenance 'legacy', got %q", gotProvenance)
	}
	if gotValue != value {
		t.Fatalf("value changed by migration: got %q, want %q", gotValue, value)
	}
	if gotUpdatedAt != now {
		t.Fatalf("updated_at changed by migration: got %d, want %d", gotUpdatedAt, now)
	}
}
