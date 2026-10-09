package migrate

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"scrumboy/internal/db"
)

const publicViewingMigration = "075_add_project_public_viewing.sql"

func applyMigrationsBefore(t *testing.T, sqlDB *sql.DB, target string) {
	t.Helper()
	ctx := context.Background()
	if _, err := sqlDB.ExecContext(ctx, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	for _, version := range embeddedMigrationVersions(t) {
		if version == target {
			return
		}
		if err := applyOne(ctx, sqlDB, version); err != nil {
			t.Fatalf("apply %s: %v", version, err)
		}
	}
	t.Fatalf("target migration %q not embedded", target)
}

func TestMigration075PreservesExistingProjectsAndDefaultsPrivate(t *testing.T) {
	ctx := context.Background()
	sqlDB := openRawTestDB(t)
	applyMigrationsBefore(t, sqlDB, publicViewingMigration)

	now := time.Now().UTC().UnixMilli()
	expires := now + int64(24*time.Hour/time.Millisecond)
	if _, err := sqlDB.ExecContext(ctx, `
INSERT INTO users(id, email, created_at, name, system_role)
VALUES (1, 'owner@example.com', ?, 'Owner', 'owner')`, now); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `
INSERT INTO projects(id, name, image, slug, dominant_color, estimation_mode, default_sprint_weeks, sprints_enabled, owner_user_id, creator_user_id, last_activity_at, expires_at, created_at, updated_at)
VALUES
  (11, 'Durable', '/durable.png', 'durable', '#123456', 'MODIFIED_FIBONACCI', 1, 0, 1, 1, ?, NULL, ?, ?),
  (12, 'Temporary', '/temporary.png', 'temporary', '#234567', 'MODIFIED_FIBONACCI', 2, 1, NULL, 1, ?, ?, ?, ?),
  (13, 'Anonymous', '/anonymous.png', 'anonymous', '#345678', 'MODIFIED_FIBONACCI', 2, 1, NULL, NULL, ?, ?, ?, ?)`,
		now-30, now-300, now-30,
		now-20, expires, now-200, now-20,
		now-10, expires, now-100, now-10,
	); err != nil {
		t.Fatalf("insert pre-075 projects: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO project_members(project_id, user_id, role, created_at) VALUES (11, 1, 'maintainer', ?)`, now); err != nil {
		t.Fatalf("insert membership: %v", err)
	}

	if err := applyOne(ctx, sqlDB, publicViewingMigration); err != nil {
		t.Fatalf("apply %s: %v", publicViewingMigration, err)
	}

	type snapshot struct {
		id       int64
		name     string
		slug     string
		owner    sql.NullInt64
		creator  sql.NullInt64
		activity int64
		expires  sql.NullInt64
		created  int64
		updated  int64
		public   int
	}
	wants := []snapshot{
		{id: 11, name: "Durable", slug: "durable", owner: sql.NullInt64{Int64: 1, Valid: true}, creator: sql.NullInt64{Int64: 1, Valid: true}, activity: now - 30, created: now - 300, updated: now - 30},
		{id: 12, name: "Temporary", slug: "temporary", creator: sql.NullInt64{Int64: 1, Valid: true}, activity: now - 20, expires: sql.NullInt64{Int64: expires, Valid: true}, created: now - 200, updated: now - 20},
		{id: 13, name: "Anonymous", slug: "anonymous", activity: now - 10, expires: sql.NullInt64{Int64: expires, Valid: true}, created: now - 100, updated: now - 10},
	}
	for _, want := range wants {
		var got snapshot
		err := sqlDB.QueryRowContext(ctx, `
SELECT id, name, slug, owner_user_id, creator_user_id, last_activity_at, expires_at, created_at, updated_at, public_view_enabled
FROM projects WHERE id = ?`, want.id).Scan(
			&got.id, &got.name, &got.slug, &got.owner, &got.creator, &got.activity, &got.expires, &got.created, &got.updated, &got.public,
		)
		if err != nil {
			t.Fatalf("read migrated project %d: %v", want.id, err)
		}
		if got.id != want.id || got.name != want.name || got.slug != want.slug || got.owner != want.owner || got.creator != want.creator || got.activity != want.activity || got.expires != want.expires || got.created != want.created || got.updated != want.updated {
			t.Fatalf("project %d changed during migration: got %+v want %+v", want.id, got, want)
		}
		if got.public != 0 {
			t.Fatalf("project %d public_view_enabled = %d, want 0", want.id, got.public)
		}
	}

	var memberships int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_members WHERE project_id = 11 AND user_id = 1 AND role = 'maintainer'`).Scan(&memberships); err != nil {
		t.Fatalf("count preserved memberships: %v", err)
	}
	if memberships != 1 {
		t.Fatalf("preserved membership count = %d, want 1", memberships)
	}
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO projects(name, slug, last_activity_at, created_at, updated_at) VALUES ('New', 'new', ?, ?, ?)`, now, now, now); err != nil {
		t.Fatalf("insert post-075 project: %v", err)
	}
	var newPublic int
	if err := sqlDB.QueryRowContext(ctx, `SELECT public_view_enabled FROM projects WHERE slug = 'new'`).Scan(&newPublic); err != nil {
		t.Fatalf("read new project default: %v", err)
	}
	if newPublic != 0 {
		t.Fatalf("new project public_view_enabled = %d, want 0", newPublic)
	}
	if _, err := sqlDB.ExecContext(ctx, `UPDATE projects SET public_view_enabled = 2 WHERE id = 11`); err == nil {
		t.Fatal("expected CHECK constraint to reject public_view_enabled = 2")
	}
}

func TestMigration075FreshInstallAndReopenPreservePublicationState(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	open := func() *sql.DB {
		sqlDB, err := db.Open(path, db.Options{BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL"})
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		return sqlDB
	}

	sqlDB := open()
	if err := Apply(ctx, sqlDB); err != nil {
		t.Fatalf("Apply fresh: %v", err)
	}
	if !columnExists(t, sqlDB, "projects", "public_view_enabled") {
		t.Fatal("fresh schema missing projects.public_view_enabled")
	}
	now := time.Now().UTC().UnixMilli()
	res, err := sqlDB.ExecContext(ctx, `INSERT INTO projects(name, slug, last_activity_at, created_at, updated_at) VALUES ('Persistent', 'persistent', ?, ?, ?)`, now, now, now)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	projectID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `UPDATE projects SET public_view_enabled = 1 WHERE id = ?`, projectID); err != nil {
		t.Fatalf("enable publication state: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	sqlDB = open()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Apply(ctx, sqlDB); err != nil {
		t.Fatalf("Apply reopened: %v", err)
	}
	var enabled int
	if err := sqlDB.QueryRowContext(ctx, `SELECT public_view_enabled FROM projects WHERE id = ?`, projectID).Scan(&enabled); err != nil {
		t.Fatalf("read reopened publication state: %v", err)
	}
	if enabled != 1 {
		t.Fatalf("reopened public_view_enabled = %d, want 1", enabled)
	}
}
