package store

import (
	"errors"
	"slices"
	"testing"
)

func exportedAssetIDs(t *testing.T, data *ExportData) []string {
	t.Helper()
	var ids []string
	for _, p := range data.Projects {
		for _, a := range p.Assets {
			ids = append(ids, a.ID)
		}
	}
	slices.Sort(ids)
	return ids
}

func sortedIDs(ids ...string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}

// Finding B: right after a downgrade, an older binary may have written bodies that embed images
// without todo_assets rows, and reconciliation has not run yet. The export must still carry them.
func TestExportIncludesImagesWithoutLinkRows(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetOwnedProject(t)
	linked := insertTestAsset(t, sqlDB, p.ID)
	unlinked := insertTestAsset(t, sqlDB, p.ID)
	entity := insertTestAsset(t, sqlDB, p.ID)
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: imageMD(linked.UUID), ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	// What an older binary leaves behind: link rows gone, bodies changed without sync.
	if _, err := sqlDB.Exec(`DELETE FROM todo_assets`); err != nil {
		t.Fatal(err)
	}
	encoded := "![e](/api/assets/&#x" + hexByte(entity.UUID[0]) + ";" + entity.UUID[1:] + ")"
	if _, err := sqlDB.Exec(`UPDATE todos SET body = ? WHERE id = ?`, imageMD(linked.UUID)+"\n\n"+imageMD(unlinked.UUID)+"\n\n"+encoded, todo.ID); err != nil {
		t.Fatal(err)
	}
	data, err := st.ExportAllProjects(ctx, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := exportedAssetIDs(t, data), sortedIDs(linked.UUID, unlinked.UUID, entity.UUID); !slices.Equal(got, want) {
		t.Fatalf("exported assets = %v, want %v", got, want)
	}
}

// Finding B: the asset list comes from the exported bodies, so an edit between the body read and
// the asset read cannot drop an image the exported body embeds.
func TestExportAssetsMatchBodySnapshotUnderConcurrentEdit(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetOwnedProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	b := insertTestAsset(t, sqlDB, p.ID)
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: imageMD(a.UUID), ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	exportAssetsBeforeVerifyHook = func() {
		exportAssetsBeforeVerifyHook = nil
		body := imageMD(b.UUID)
		if _, err := st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{Title: "T", Body: body}, ModeFull); err != nil {
			t.Errorf("concurrent edit: %v", err)
		}
	}
	defer func() { exportAssetsBeforeVerifyHook = nil }()
	data, err := st.ExportAllProjects(ctx, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if data.Projects[0].Todos[0].Body != imageMD(a.UUID) {
		t.Fatal("test precondition: export should hold the pre-edit body")
	}
	if got := exportedAssetIDs(t, data); !slices.Equal(got, []string{a.UUID}) {
		t.Fatalf("exported assets = %v, want the image the exported body embeds", got)
	}
}

// Finding B: if an embedded image's row disappears after the body snapshot (an edit removed it
// and it was collected), the export fails visibly instead of omitting it.
func TestExportFailsWhenEmbeddedImageVanishesAfterEdit(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetOwnedProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: imageMD(a.UUID), ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	exportAssetsBeforeVerifyHook = func() {
		exportAssetsBeforeVerifyHook = nil
		if _, err := st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{Title: "T", Body: "gone"}, ModeFull); err != nil {
			t.Errorf("concurrent edit: %v", err)
		}
		if _, err := sqlDB.Exec(`DELETE FROM project_assets WHERE id = ?`, a.ID); err != nil {
			t.Errorf("simulated collection: %v", err)
		}
	}
	defer func() { exportAssetsBeforeVerifyHook = nil }()
	if _, err := st.ExportAllProjects(ctx, ModeFull); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	// A retry sees the new body and succeeds.
	data, err := st.ExportAllProjects(ctx, ModeFull)
	if err != nil || len(exportedAssetIDs(t, data)) != 0 {
		t.Fatalf("retry: %v", err)
	}
}

// Compatibility: a reference to an image that never belonged to the project (typed by hand,
// another project's image, or a JSON import from another instance) keeps exporting as before.
func TestExportDanglingReferenceStaysExportable(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetOwnedProject(t)
	other, err := st.CreateProject(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	foreign := insertTestAsset(t, sqlDB, other.ID)
	missing := newTestAssetUUID(t)
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: imageMD(missing) + " " + imageMD(foreign.UUID), ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
		t.Fatal(err)
	}
	data, err := st.ExportAllProjects(ctx, ModeFull)
	if err != nil {
		t.Fatalf("dangling reference broke export: %v", err)
	}
	for _, pe := range data.Projects {
		if pe.Name == "Assets" && len(pe.Assets) != 0 {
			t.Fatalf("dangling or foreign images exported: %+v", pe.Assets)
		}
	}
}

// Finding A (defense in depth): the store refuses an archive plan that leaves out an image a
// story embeds, before anything commits.
func TestImportWithAssetsRejectsPlanMissingEmbeddedImage(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetOwnedProject(t)
	archiveID := newTestAssetUUID(t)
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: imageMD(archiveID), ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
		t.Fatal(err)
	}
	data, _, _ := archiveDataFor(t, st, func() (*ExportData, error) { return st.ExportAllProjects(ctx, ModeFull) }, archiveID)
	for _, mode := range []string{"copy", "merge", "replace"} {
		before := countRows(t, sqlDB, `SELECT COUNT(*) FROM projects`)
		_, err := st.ImportProjectsWithAssets(ctx, data, ModeFull, mode, &ImportAssetPlan{ByProject: map[int][]ImportStagedAsset{}})
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("%s: want ErrValidation, got %v", mode, err)
		}
		if countRows(t, sqlDB, `SELECT COUNT(*) FROM projects`) != before || countRows(t, sqlDB, `SELECT COUNT(*) FROM projects WHERE id = ?`, p.ID) != 1 {
			t.Fatalf("%s: failed import changed projects", mode)
		}
	}
}

func hexByte(c byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[c>>4], digits[c&0xf]})
}

// The store's own remap also refuses a reference definition shared by an image and an ordinary
// link, even with a complete plan, before anything commits.
func TestImportWithAssetsRejectsSharedLinkDefinition(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetOwnedProject(t)
	archiveID := newTestAssetUUID(t)
	body := "![Screenshot][asset]\n\n[Open original][asset]\n\n[asset]: /api/assets/" + archiveID
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: body, ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"copy", "merge", "replace"} {
		data, plan, _ := archiveDataFor(t, st, func() (*ExportData, error) { return st.ExportAllProjects(ctx, ModeFull) }, archiveID)
		before := countRows(t, sqlDB, `SELECT COUNT(*) FROM projects`)
		if _, err := st.ImportProjectsWithAssets(ctx, data, ModeFull, mode, plan); !errors.Is(err, ErrValidation) {
			t.Fatalf("%s: want ErrValidation, got %v", mode, err)
		}
		var got string
		if err := sqlDB.QueryRow(`SELECT body FROM todos WHERE project_id = ?`, p.ID).Scan(&got); err != nil || got != body {
			t.Fatalf("%s: existing story changed: %v", mode, err)
		}
		if countRows(t, sqlDB, `SELECT COUNT(*) FROM projects`) != before || countRows(t, sqlDB, `SELECT COUNT(*) FROM project_assets`) != 0 {
			t.Fatalf("%s: failed import left data", mode)
		}
	}
}
