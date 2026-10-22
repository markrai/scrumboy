package store

import (
	"errors"
	"testing"
	"time"
)

// archiveDataFor exports the owned project and converts it into an archive manifest whose single
// image is staged under a fresh uuid.
func archiveDataFor(t *testing.T, st *Store, ctxProject func() (*ExportData, error), archiveID string) (*ExportData, *ImportAssetPlan, string) {
	t.Helper()
	data, err := ctxProject()
	if err != nil {
		t.Fatal(err)
	}
	data.Version = "1.3"
	staged := newTestAssetUUID(t)
	sha := randomSHA256Hex(t)
	data.Projects[0].Assets = []AssetExport{{ID: archiveID, MimeType: "image/png", SizeBytes: 5, Width: 1, Height: 1, SHA256: sha}}
	plan := &ImportAssetPlan{ByProject: map[int][]ImportStagedAsset{0: {{ArchiveID: archiveID, NewUUID: staged, MimeType: "image/png", SHA256: sha, SizeBytes: 5, Width: 1, Height: 1}}}}
	return data, plan, staged
}

func TestImportWithAssetsReplaceReportsRemovedAfterCommit(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetOwnedProject(t)
	old := insertTestAsset(t, sqlDB, p.ID)
	archiveID := newTestAssetUUID(t)
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: imageMD(archiveID), ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
		t.Fatal(err)
	}
	data, plan, staged := archiveDataFor(t, st, func() (*ExportData, error) { return st.ExportAllProjects(ctx, ModeFull) }, archiveID)
	res, err := st.ImportProjectsWithAssets(ctx, data, ModeFull, "replace", plan)
	if err != nil {
		t.Fatalf("replace: %v (%+v)", err, res)
	}
	if len(plan.RemovedAssets) != 1 || plan.RemovedAssets[0].UUID != old.UUID {
		t.Fatalf("RemovedAssets = %+v", plan.RemovedAssets)
	}
	if _, used := plan.UsedNewUUIDs[staged]; !used {
		t.Fatal("staged asset not used")
	}
	var body string
	if err := sqlDB.QueryRow(`SELECT body FROM todos WHERE title = 'T'`).Scan(&body); err != nil || body != imageMD(staged) {
		t.Fatalf("body not remapped: %q %v", body, err)
	}
	if countRows(t, sqlDB, `SELECT COUNT(*) FROM todo_assets`) != 1 {
		t.Fatal("remapped image not linked")
	}
}

func TestImportWithAssetsReplaceSwapFailureKeepsOldData(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetOwnedProject(t)
	old := insertTestAsset(t, sqlDB, p.ID)
	archiveID := newTestAssetUUID(t)
	data, plan, _ := archiveDataFor(t, st, func() (*ExportData, error) { return st.ExportAllProjects(ctx, ModeFull) }, archiveID)
	replaceSwapBeforeCommitHook = func() error { return errors.New("injected swap failure") }
	defer func() { replaceSwapBeforeCommitHook = nil }()
	if _, err := st.ImportProjectsWithAssets(ctx, data, ModeFull, "replace", plan); err == nil {
		t.Fatal("want swap failure")
	}
	if len(plan.RemovedAssets) != 0 {
		t.Fatal("failed swap reported removable files")
	}
	if countRows(t, sqlDB, `SELECT COUNT(*) FROM project_assets WHERE id = ?`, old.ID) != 1 {
		t.Fatal("old asset lost")
	}
	if countRows(t, sqlDB, `SELECT COUNT(*) FROM projects WHERE import_batch_id IS NOT NULL`) != 0 {
		t.Fatal("staging not cleaned after swap failure")
	}
}

func TestImportWithAssetsCrashThenReap(t *testing.T) {
	st, sqlDB, ctx, _ := setupAssetOwnedProject(t)
	archiveID := newTestAssetUUID(t)
	data, plan, staged := archiveDataFor(t, st, func() (*ExportData, error) { return st.ExportAllProjects(ctx, ModeFull) }, archiveID)
	replaceSwapBeforeCommitHook = func() error { return errors.New("crash") }
	replaceSkipStagingCleanupHook = true
	defer func() { replaceSwapBeforeCommitHook = nil; replaceSkipStagingCleanupHook = false }()
	phaseAStart := time.Now().UTC().UnixMilli() - 1
	_, _ = st.ImportProjectsWithAssets(ctx, data, ModeFull, "replace", plan)
	if countRows(t, sqlDB, `SELECT COUNT(*) FROM projects WHERE import_batch_id IS NOT NULL`) != 1 {
		t.Fatal("crash simulation should leave staging behind")
	}
	if n, _, err := st.ReapStaleImportBatches(ctx, phaseAStart); err != nil || n != 0 {
		t.Fatalf("a live batch was reaped: %d %v", n, err)
	}
	n, refs, err := st.ReapStaleImportBatches(ctx, time.Now().UTC().UnixMilli()+1)
	if err != nil || n != 1 || len(refs) != 1 || refs[0].UUID != staged {
		t.Fatalf("reap after restart = %d %+v %v", n, refs, err)
	}
}

func TestImportWithAssetsBudgetInTransaction(t *testing.T) {
	st, sqlDB, cleanup := newTestStoreWithSQL(t)
	defer cleanup()
	st.assetBudgets = AssetBudgets{ProjectBytes: 3}
	ctx := t.Context()
	p, err := st.CreateProject(ctx, "Small")
	if err != nil {
		t.Fatal(err)
	}
	archiveID := newTestAssetUUID(t)
	data, plan, _ := archiveDataFor(t, st, func() (*ExportData, error) { return st.ExportAllProjects(ctx, ModeFull) }, archiveID)
	if _, err := st.ImportProjectsWithAssets(ctx, data, ModeFull, "copy", plan); !errors.Is(err, ErrStorageBudget) {
		t.Fatalf("want budget error, got %v", err)
	}
	if countRows(t, sqlDB, `SELECT COUNT(*) FROM projects`) != 1 || countRows(t, sqlDB, `SELECT COUNT(*) FROM project_assets`) != 0 {
		t.Fatal("budget failure left data")
	}
	_ = p
}

func TestImportWithAssetsRejectsJSONVersion(t *testing.T) {
	st, _, ctx, _ := setupAssetOwnedProject(t)
	data, err := st.ExportAllProjects(ctx, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportProjectsWithAssets(ctx, data, ModeFull, "copy", &ImportAssetPlan{}); !errors.Is(err, ErrValidation) {
		t.Fatalf("1.2 data via archive path: %v", err)
	}
}
