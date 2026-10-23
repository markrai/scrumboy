package migrate

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	projectAssetsMigration = "078_add_project_assets.sql"
	customFieldsMigration  = "079_add_custom_fields.sql"
)

func TestMigrationVersionPrefixesAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, version := range embeddedMigrationVersions(t) {
		prefix, _, ok := strings.Cut(version, "_")
		if !ok {
			t.Fatalf("migration %q has no numeric prefix", version)
		}
		if prev, dup := seen[prefix]; dup {
			t.Fatalf("migrations %q and %q share prefix %s", prev, version, prefix)
		}
		seen[prefix] = version
	}
}

func TestMigration079FollowsProjectAssets(t *testing.T) {
	versions := embeddedMigrationVersions(t)
	assets, fields := slices.Index(versions, projectAssetsMigration), slices.Index(versions, customFieldsMigration)
	if assets < 0 || fields < 0 {
		t.Fatalf("migrations %s and %s must both be embedded: %v", projectAssetsMigration, customFieldsMigration, versions)
	}
	if fields != assets+1 {
		t.Fatalf("%s at %d must directly follow %s at %d", customFieldsMigration, fields, projectAssetsMigration, assets)
	}
}

func TestMigration079UpgradesDatabaseWith078Applied(t *testing.T) {
	ctx := context.Background()
	sqlDB := openRawTestDB(t)
	applyMigrationsBefore(t, sqlDB, customFieldsMigration)

	if !tableExists(t, sqlDB, "project_assets") || !tableExists(t, sqlDB, "todo_assets") {
		t.Fatal("078 asset tables missing before 079")
	}
	if tableExists(t, sqlDB, "project_custom_fields") || tableExists(t, sqlDB, "todo_custom_field_values") {
		t.Fatal("custom field tables exist before 079")
	}

	now := time.Now().UTC().UnixMilli()
	if _, err := sqlDB.ExecContext(ctx, `
INSERT INTO users(id, email, created_at, name, system_role)
VALUES (1, 'owner@example.com', ?, 'Owner', 'owner')`, now); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `
INSERT INTO projects(id, name, image, slug, dominant_color, estimation_mode, default_sprint_weeks, sprints_enabled, owner_user_id, creator_user_id, last_activity_at, expires_at, created_at, updated_at)
VALUES (1, 'Durable', '/durable.png', 'durable', '#123456', 'MODIFIED_FIBONACCI', 1, 0, 1, 1, ?, NULL, ?, ?)`, now, now, now); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `
INSERT INTO todos(id, project_id, local_id, title, body, column_key, rank, created_at, updated_at)
VALUES (1, 1, 1, 'With image', '![shot](/api/assets/6fa459ea-ee8a-4ca4-894e-db77e160355e)', 'backlog', 1000, ?, ?)`, now, now); err != nil {
		t.Fatalf("insert todo: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `
INSERT INTO project_assets(id, uuid, project_id, uploader_user_id, mime_type, size_bytes, width, height, sha256, created_at, unreferenced_at)
VALUES (1, '6fa459ea-ee8a-4ca4-894e-db77e160355e', 1, 1, 'image/png', 10, 1, 1, ?, ?, ?)`, strings.Repeat("a", 64), now, now); err != nil {
		t.Fatalf("insert asset: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO todo_assets(todo_id, asset_id, created_at) VALUES (1, 1, ?)`, now); err != nil {
		t.Fatalf("insert todo asset: %v", err)
	}

	if err := Apply(ctx, sqlDB); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	applied := appliedMigrationVersions(t, sqlDB)
	if !slices.Equal(applied, embeddedMigrationVersions(t)) {
		t.Fatalf("applied versions = %v", applied)
	}
	if !tableExists(t, sqlDB, "project_custom_fields") || !tableExists(t, sqlDB, "todo_custom_field_values") {
		t.Fatal("custom field tables missing after 079")
	}
	if !indexExists(t, sqlDB, "idx_todo_custom_field_values_field") {
		t.Fatal("custom field value index missing after 079")
	}

	var referencedCleared bool
	if err := sqlDB.QueryRowContext(ctx, `SELECT unreferenced_at IS NULL FROM project_assets WHERE id = 1`).Scan(&referencedCleared); err != nil {
		t.Fatalf("read asset: %v", err)
	}
	if !referencedCleared {
		t.Fatal("asset reference state lost across 079")
	}

	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO project_custom_fields(id, project_id, key, name, type, position) VALUES (1, 1, 'ticket', 'Ticket', 'url', 0)`); err != nil {
		t.Fatalf("insert custom field: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO todo_custom_field_values(todo_id, field_id, value_text) VALUES (1, 1, 'https://example.com/T-1')`); err != nil {
		t.Fatalf("insert custom field value on todo with image: %v", err)
	}

	if _, err := sqlDB.ExecContext(ctx, `DELETE FROM todos WHERE id = 1`); err != nil {
		t.Fatalf("delete todo: %v", err)
	}
	var values, refs int
	if err := sqlDB.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM todo_custom_field_values), (SELECT COUNT(*) FROM todo_assets)`).Scan(&values, &refs); err != nil {
		t.Fatalf("count dependents: %v", err)
	}
	if values != 0 || refs != 0 {
		t.Fatalf("todo delete left %d custom field values and %d asset refs", values, refs)
	}

	if err := Apply(ctx, sqlDB); err != nil {
		t.Fatalf("re-Apply: %v", err)
	}
}
