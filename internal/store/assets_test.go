package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"scrumboy/internal/migrate"
)

func setupAssetTestProject(t *testing.T) (*Store, *sql.DB, context.Context, Project) {
	t.Helper()
	st, sqlDB, cleanup := newTestStoreWithSQL(t)
	t.Cleanup(func() {
		assertAssetInvariants(t, sqlDB)
		cleanup()
	})
	ctx := context.Background()
	p, err := st.CreateProject(ctx, "Assets")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return st, sqlDB, ctx, p
}

func createAssetTestTodo(t *testing.T, st *Store, ctx context.Context, projectID int64, title string) Todo {
	t.Helper()
	todo, err := st.CreateTodo(ctx, projectID, CreateTodoInput{Title: title, ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	return todo
}

func TestMigration078AppliesOnFreshDB(t *testing.T) {
	_, sqlDB, _, _ := setupAssetTestProject(t)
	for _, name := range []string{
		"project_assets", "todo_assets",
		"idx_project_assets_project_sha256", "idx_project_assets_gc", "idx_todo_assets_asset",
		"trg_todo_assets_same_project", "trg_todo_assets_referenced", "trg_todo_assets_unreferenced", "trg_project_assets_immutable",
	} {
		if countRows(t, sqlDB, `SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, name) != 1 {
			t.Fatalf("schema object %s missing", name)
		}
	}
}

func TestMigration078AppliesOnUpgradedDB(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	todo := createAssetTestTodo(t, st, ctx, p.ID, "Existing data")
	// Roll the database back to the 077 schema, keeping existing data.
	for _, stmt := range []string{
		`DROP TRIGGER trg_todo_assets_same_project`,
		`DROP TRIGGER trg_todo_assets_referenced`,
		`DROP TRIGGER trg_todo_assets_unreferenced`,
		`DROP TRIGGER trg_project_assets_immutable`,
		`DROP TABLE todo_assets`,
		`DROP TABLE project_assets`,
		`DELETE FROM schema_migrations WHERE version = '078_add_project_assets.sql'`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := migrate.Apply(ctx, sqlDB); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if countRows(t, sqlDB, `SELECT COUNT(*) FROM todos WHERE id = ?`, todo.ID) != 1 {
		t.Fatal("existing todo lost during upgrade")
	}
	a := insertTestAsset(t, sqlDB, p.ID)
	linkTestAsset(t, sqlDB, todo.ID, a.ID)
}

func TestProjectAssetChecks(t *testing.T) {
	_, sqlDB, ctx, p := setupAssetTestProject(t)
	valid := NewProjectAsset{UUID: newTestAssetUUID(t), ProjectID: p.ID, MimeType: "image/png", SizeBytes: 1, Width: 1, Height: 1, SHA256: randomSHA256Hex(t)}
	cases := map[string]func(*NewProjectAsset){
		"uppercase uuid": func(in *NewProjectAsset) { in.UUID = strings.ToUpper(in.UUID) },
		"v1 uuid":        func(in *NewProjectAsset) { in.UUID = "6fa459ea-ee8a-11ca-8fa6-00a0c91e6bf6" },
		"short uuid":     func(in *NewProjectAsset) { in.UUID = in.UUID[:35] },
		"svg mime":       func(in *NewProjectAsset) { in.MimeType = "image/svg+xml" },
		"zero size":      func(in *NewProjectAsset) { in.SizeBytes = 0 },
		"zero width":     func(in *NewProjectAsset) { in.Width = 0 },
		"bad sha":        func(in *NewProjectAsset) { in.SHA256 = strings.ToUpper(in.SHA256) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := valid
			mutate(&in)
			tx, err := sqlDB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := insertProjectAssetTx(ctx, tx, in, 1); !errors.Is(err, ErrValidation) {
				t.Fatalf("want ErrValidation, got %v", err)
			}
		})
	}
	// Schema CHECKs hold independently of Go validation.
	if _, err := sqlDB.Exec(`INSERT INTO project_assets(uuid, project_id, mime_type, size_bytes, width, height, sha256, created_at) VALUES ('NOT-A-UUID', ?, 'image/png', 1, 1, 1, ?, 1)`, p.ID, randomSHA256Hex(t)); err == nil {
		t.Fatal("schema accepted a non-canonical uuid")
	}
	if _, err := sqlDB.Exec(`INSERT INTO project_assets(uuid, project_id, mime_type, size_bytes, width, height, sha256, created_at) VALUES (?, ?, 'text/html', 1, 1, 1, ?, 1)`, newTestAssetUUID(t), p.ID, randomSHA256Hex(t)); err == nil {
		t.Fatal("schema accepted text/html")
	}
	if _, err := sqlDB.Exec(`INSERT INTO project_assets(uuid, project_id, mime_type, size_bytes, width, height, sha256, original_name, created_at) VALUES (?, ?, 'image/png', 1, 1, 1, ?, ?, 1)`, newTestAssetUUID(t), p.ID, randomSHA256Hex(t), strings.Repeat("x", 256)); err == nil {
		t.Fatal("schema accepted a 256-byte original_name")
	}
	// Duplicate uuid maps to ErrConflict.
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := insertProjectAssetTx(ctx, tx, valid, 1); err != nil {
		t.Fatalf("insert valid: %v", err)
	}
	if _, err := insertProjectAssetTx(ctx, tx, valid, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate uuid: want ErrConflict, got %v", err)
	}
}

func TestSanitizeAssetOriginalName(t *testing.T) {
	cases := map[string]string{
		"  shot.png ":            "shot.png",
		"../../etc/passwd":       "....etcpasswd",
		"a\\b\x00c\nd":           "abcd",
		strings.Repeat("é", 200): strings.Repeat("é", 127),
	}
	for in, want := range cases {
		if got := SanitizeAssetOriginalName(in); got != want {
			t.Errorf("SanitizeAssetOriginalName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProjectAssetImmutable(t *testing.T) {
	_, sqlDB, _, p := setupAssetTestProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	for _, stmt := range []string{
		`UPDATE project_assets SET uuid = '` + newTestAssetUUID(t) + `' WHERE id = ?`,
		`UPDATE project_assets SET sha256 = '` + randomSHA256Hex(t) + `' WHERE id = ?`,
		`UPDATE project_assets SET size_bytes = 999 WHERE id = ?`,
	} {
		if _, err := sqlDB.Exec(stmt, a.ID); err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Fatalf("%s: want immutable abort, got %v", stmt, err)
		}
	}
	if _, err := sqlDB.Exec(`UPDATE project_assets SET unreferenced_at = 5 WHERE id = ?`, a.ID); err != nil {
		t.Fatalf("unreferenced_at must be mutable: %v", err)
	}
}

func TestTodoAssetsCrossProjectRejected(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	other, err := st.CreateProject(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	todo := createAssetTestTodo(t, st, ctx, p.ID, "A")
	foreign := insertTestAsset(t, sqlDB, other.ID)
	if _, err := sqlDB.Exec(`INSERT INTO todo_assets(todo_id, asset_id, created_at) VALUES (?, ?, 1)`, todo.ID, foreign.ID); err == nil || !strings.Contains(err.Error(), "cross-project") {
		t.Fatalf("want cross-project abort, got %v", err)
	}
}

func TestUnreferencedAtTransitions(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	if v := assetUnreferencedAt(t, sqlDB, a.ID); !v.Valid || v.Int64 != 1_700_000_000_000 {
		t.Fatalf("new asset: unreferenced_at = %+v, want insert time", v)
	}
	ta := createAssetTestTodo(t, st, ctx, p.ID, "A")
	tb := createAssetTestTodo(t, st, ctx, p.ID, "B")

	linkTestAsset(t, sqlDB, ta.ID, a.ID)
	if assetUnreferencedAt(t, sqlDB, a.ID).Valid {
		t.Fatal("first reference must clear unreferenced_at")
	}
	linkTestAsset(t, sqlDB, tb.ID, a.ID)
	if _, err := sqlDB.Exec(`DELETE FROM todo_assets WHERE todo_id = ?`, ta.ID); err != nil {
		t.Fatal(err)
	}
	if assetUnreferencedAt(t, sqlDB, a.ID).Valid {
		t.Fatal("removing one of several references must keep unreferenced_at NULL")
	}
	if _, err := sqlDB.Exec(`DELETE FROM todo_assets WHERE todo_id = ?`, tb.ID); err != nil {
		t.Fatal(err)
	}
	v := assetUnreferencedAt(t, sqlDB, a.ID)
	if !v.Valid || v.Int64 <= 1_700_000_000_000 {
		t.Fatalf("final unlink must stamp now, got %+v", v)
	}
	linkTestAsset(t, sqlDB, ta.ID, a.ID)
	if assetUnreferencedAt(t, sqlDB, a.ID).Valid {
		t.Fatal("reattach must clear unreferenced_at")
	}
}

// U1: triggers on todo_assets fire for rows removed by ON DELETE CASCADE from todos.
func TestTodoAssetsTriggerFiresOnTodoCascade(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	todo := createAssetTestTodo(t, st, ctx, p.ID, "Doomed")
	linkTestAsset(t, sqlDB, todo.ID, a.ID)
	if err := st.DeleteTodo(ctx, todo.ID, ModeFull); err != nil {
		t.Fatalf("DeleteTodo: %v", err)
	}
	if countRows(t, sqlDB, `SELECT COUNT(*) FROM todo_assets WHERE todo_id = ?`, todo.ID) != 0 {
		t.Fatal("todo_assets did not cascade")
	}
	if !assetUnreferencedAt(t, sqlDB, a.ID).Valid {
		t.Fatal("cascade delete did not fire the unreferenced trigger")
	}
}

func TestArchiveRestoreKeepsReferences(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	todo := createAssetTestTodo(t, st, ctx, p.ID, "Archive me")
	linkTestAsset(t, sqlDB, todo.ID, a.ID)
	if _, err := st.ArchiveTodosByLocalID(ctx, p.ID, []int64{todo.LocalID}, ModeFull); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if countRows(t, sqlDB, `SELECT COUNT(*) FROM todo_assets WHERE todo_id = ?`, todo.ID) != 1 || assetUnreferencedAt(t, sqlDB, a.ID).Valid {
		t.Fatal("archive must keep references")
	}
	if _, err := st.RestoreTodosByLocalID(ctx, p.ID, []int64{todo.LocalID}, ModeFull); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if countRows(t, sqlDB, `SELECT COUNT(*) FROM todo_assets WHERE todo_id = ?`, todo.ID) != 1 {
		t.Fatal("restore must keep references")
	}
}

func TestDeleteProjectCascadesAssets(t *testing.T) {
	st, sqlDB, ctx := setupAssetOwnerStore(t)
	owner, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)
	p, err := st.CreateProject(ctxOwner, "Delete me")
	if err != nil {
		t.Fatal(err)
	}
	a := insertTestAsset(t, sqlDB, p.ID)
	todo := createAssetTestTodo(t, st, ctxOwner, p.ID, "With image")
	linkTestAsset(t, sqlDB, todo.ID, a.ID)
	if _, err := st.DeleteProject(ctxOwner, p.ID, owner.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if countRows(t, sqlDB, `SELECT COUNT(*) FROM project_assets`) != 0 || countRows(t, sqlDB, `SELECT COUNT(*) FROM todo_assets`) != 0 {
		t.Fatal("asset rows did not cascade with the project")
	}
}

func TestDeleteExpiredProjectsCascadesAssets(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	todo := createAssetTestTodo(t, st, ctx, p.ID, "Expiring")
	linkTestAsset(t, sqlDB, todo.ID, a.ID)
	if _, err := sqlDB.Exec(`UPDATE projects SET expires_at = 1 WHERE id = ?`, p.ID); err != nil {
		t.Fatal(err)
	}
	n, err := st.DeleteExpiredProjects(ctx)
	if err != nil || n != 1 {
		t.Fatalf("DeleteExpiredProjects = %d, %v", n, err)
	}
	if countRows(t, sqlDB, `SELECT COUNT(*) FROM project_assets`) != 0 {
		t.Fatal("asset rows did not cascade with the expired project")
	}
}

func TestReplaceStagingCleanupCascadesAssets(t *testing.T) {
	_, sqlDB, _, p := setupAssetTestProject(t)
	if _, err := sqlDB.Exec(`UPDATE projects SET import_batch_id = 'batch-1' WHERE id = ?`, p.ID); err != nil {
		t.Fatal(err)
	}
	insertTestAsset(t, sqlDB, p.ID)
	if _, err := sqlDB.Exec(`DELETE FROM projects WHERE import_batch_id = ?`, "batch-1"); err != nil {
		t.Fatal(err)
	}
	if countRows(t, sqlDB, `SELECT COUNT(*) FROM project_assets`) != 0 {
		t.Fatal("staging cleanup did not cascade assets")
	}
}

func setupAssetOwnerStore(t *testing.T) (*Store, *sql.DB, context.Context) {
	t.Helper()
	st, sqlDB, cleanup := newTestStoreWithSQL(t)
	t.Cleanup(func() {
		assertAssetInvariants(t, sqlDB)
		cleanup()
	})
	return st, sqlDB, context.Background()
}

func TestListProjectAssetUUIDsTx(t *testing.T) {
	_, sqlDB, ctx, p := setupAssetTestProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	b := insertTestAsset(t, sqlDB, p.ID)
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	refs, err := listProjectAssetUUIDsTx(ctx, tx, []int64{p.ID, 9999})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].UUID != a.UUID || refs[1].UUID != b.UUID || refs[0].MimeType != "image/png" {
		t.Fatalf("refs = %+v", refs)
	}
	got, err := getProjectAssetByUUIDTx(ctx, tx, a.UUID)
	if err != nil || got.ID != a.ID || got.UnreferencedAt == nil {
		t.Fatalf("getProjectAssetByUUIDTx = %+v, %v", got, err)
	}
	if _, err := getProjectAssetByUUIDTx(ctx, tx, newTestAssetUUID(t)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing uuid: want ErrNotFound, got %v", err)
	}
}

func TestAssetHelpers(t *testing.T) {
	for mime, ext := range map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif"} {
		if got, ok := AssetExtensionForMIME(mime); !ok || got != ext {
			t.Errorf("AssetExtensionForMIME(%s) = %s, %v", mime, got, ok)
		}
	}
	if _, ok := AssetExtensionForMIME("image/svg+xml"); ok {
		t.Error("svg must not map")
	}
	if !IsCanonicalAssetUUID("550e8400-e29b-41d4-a716-446655440000") {
		t.Error("valid v4 rejected")
	}
	for _, bad := range []string{"550E8400-E29B-41D4-A716-446655440000", "550e8400-e29b-11d4-a716-446655440000", "550e8400-e29b-41d4-c716-446655440000", "550e8400e29b41d4a716446655440000", "../../../../../../../../../etc/passwd"} {
		if IsCanonicalAssetUUID(bad) {
			t.Errorf("IsCanonicalAssetUUID(%q) = true", bad)
		}
	}
}
