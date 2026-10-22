package store

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
)

func imageMD(uuid string) string { return "![shot](/api/assets/" + uuid + ")" }

func todoAssetCount(t *testing.T, st *Store, todoID int64) int {
	t.Helper()
	return countRows(t, st.db, `SELECT COUNT(*) FROM todo_assets WHERE todo_id = ?`, todoID)
}

func TestSyncCreateTodoLinksOwnProjectAsset(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: "See " + imageMD(a.UUID), ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if todoAssetCount(t, st, todo.ID) != 1 || assetUnreferencedAt(t, sqlDB, a.ID).Valid {
		t.Fatal("create did not link the asset")
	}
}

func TestSyncIgnoresOtherProjectAsset(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	other, err := st.CreateProject(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	foreign := insertTestAsset(t, sqlDB, other.ID)
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: imageMD(foreign.UUID), ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if todoAssetCount(t, st, todo.ID) != 0 {
		t.Fatal("cross-project asset was linked")
	}
}

func TestSyncIgnoresCodeFencesAndInlineCode(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	body := "```\n" + imageMD(a.UUID) + "\n```\nand `" + imageMD(a.UUID) + "` and /api/assets/" + a.UUID
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: body, ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if todoAssetCount(t, st, todo.ID) != 0 {
		t.Fatal("code/prose mention created a reference")
	}
}

func TestSyncUpdateFullAddsAndRemoves(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	b := insertTestAsset(t, sqlDB, p.ID)
	todo := createAssetTestTodo(t, st, ctx, p.ID, "T")
	update := func(body string) {
		t.Helper()
		if _, err := st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{Title: "T", Body: body, KeepTags: true}, ModeFull); err != nil {
			t.Fatalf("UpdateTodo: %v", err)
		}
	}
	update(imageMD(a.UUID) + imageMD(b.UUID))
	if todoAssetCount(t, st, todo.ID) != 2 {
		t.Fatal("expected two references")
	}
	// Remove b, keep a: a must stay referenced throughout (diff-based, no flap).
	update(imageMD(a.UUID))
	if todoAssetCount(t, st, todo.ID) != 1 || assetUnreferencedAt(t, sqlDB, a.ID).Valid || !assetUnreferencedAt(t, sqlDB, b.ID).Valid {
		t.Fatal("diff sync wrong after removing one image")
	}
	update("")
	if todoAssetCount(t, st, todo.ID) != 0 || !assetUnreferencedAt(t, sqlDB, a.ID).Valid {
		t.Fatal("clearing the body must unreference")
	}
}

func TestSyncBodyOnlyContributorPath(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, _ := setupAssigneeTestProject(t)
	defer cleanup()
	defer assertAssetInvariants(t, st.db)
	ctxM := WithUserID(ctx, maintainer.ID)
	ctxC := WithUserID(ctx, contributor.ID)
	a := insertTestAsset(t, st.db, project.ID)
	todo, err := st.CreateTodo(ctxM, project.ID, CreateTodoInput{Title: "Assigned", ColumnKey: DefaultColumnBacklog, AssigneeUserID: &contributor.ID}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateTodo(ctxC, todo.ID, UpdateTodoInput{Title: todo.Title, Body: imageMD(a.UUID), KeepTags: true, KeepAssigneeUserID: true}, ModeFull); err != nil {
		t.Fatalf("contributor UpdateTodo: %v", err)
	}
	if todoAssetCount(t, st, todo.ID) != 1 {
		t.Fatal("body-only path did not sync")
	}
}

func TestSyncBodyAppend(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	b := insertTestAsset(t, sqlDB, p.ID)
	todo := createAssetTestTodo(t, st, ctx, p.ID, "T")
	var wg sync.WaitGroup
	for _, u := range []string{a.UUID, b.UUID} {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			if _, err := st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{KeepTitle: true, KeepTags: true, BodyAppend: imageMD(u)}, ModeFull); err != nil {
				t.Errorf("append: %v", err)
			}
		}(u)
	}
	wg.Wait()
	if todoAssetCount(t, st, todo.ID) != 2 {
		t.Fatalf("appends: want 2 references, got %d", todoAssetCount(t, st, todo.ID))
	}
}

func TestSyncTitleOnlyUpdateHealsStaleRows(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: imageMD(a.UUID), ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`DELETE FROM todo_assets`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{Title: "Renamed", KeepBody: true, KeepTags: true}, ModeFull); err != nil {
		t.Fatal(err)
	}
	if todoAssetCount(t, st, todo.ID) != 1 {
		t.Fatal("title-only update did not heal the stale reference")
	}
}

func TestSyncDeleteTodoStampsUnreferenced(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: imageMD(a.UUID), ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTodo(ctx, todo.ID, ModeFull); err != nil {
		t.Fatal(err)
	}
	if !assetUnreferencedAt(t, sqlDB, a.ID).Valid {
		t.Fatal("delete did not unreference")
	}
}

func TestSyncImportMergeExistingProject(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetOwnedProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	todo := createAssetTestTodo(t, st, ctx, p.ID, "T")
	data, err := st.ExportAllProjects(ctx, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	stripExportAssets(data) // a JSON 1.2 backup carries no image bytes
	for pi := range data.Projects {
		for ti := range data.Projects[pi].Todos {
			data.Projects[pi].Todos[ti].Body = imageMD(a.UUID)
		}
	}
	if _, err := st.ImportProjectsWithTarget(ctx, data, ModeFull, "merge", ""); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if todoAssetCount(t, st, todo.ID) != 1 {
		t.Fatal("merge did not link the existing project asset")
	}
}

func TestSyncImportCopyDoesNotLinkSourceAssets(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: imageMD(a.UUID), ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
		t.Fatal(err)
	}
	data, err := st.ExportAllProjects(ctx, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	stripExportAssets(data) // a JSON 1.2 backup carries no image bytes
	if _, err := st.ImportProjectsWithTarget(ctx, data, ModeFull, "copy", ""); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if countRows(t, sqlDB, `SELECT COUNT(*) FROM todo_assets`) != 1 {
		t.Fatal("copy linked the source project's asset into the new project")
	}
}

func TestSyncReplaceStaging(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetOwnedProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: imageMD(a.UUID), ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
		t.Fatal(err)
	}
	data, err := st.ExportAllProjects(ctx, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	stripExportAssets(data) // a JSON 1.2 backup carries no image bytes
	if _, err := st.ImportProjectsWithTarget(ctx, data, ModeFull, "replace", ""); err != nil {
		t.Fatalf("replace: %v", err)
	}
	// The old project (and its asset) was replaced; the JSON carried no image bytes.
	if countRows(t, sqlDB, `SELECT COUNT(*) FROM project_assets`) != 0 || countRows(t, sqlDB, `SELECT COUNT(*) FROM todo_assets`) != 0 {
		t.Fatal("replace left asset rows behind")
	}
}

func TestSyncTrelloImportExternalImages(t *testing.T) {
	st, _, ctx, _ := setupAssetTestProject(t)
	data := &ExportData{Version: "1.2", Projects: []ProjectExport{{
		Name:  "Trello",
		Todos: []TodoExport{{LocalID: 1, Title: "Card", Body: "![x](https://trello.com/img.png)", Status: "BACKLOG"}},
	}}}
	if _, err := st.ImportTrelloProject(ctx, data, "{}", nil, ModeFull); err != nil {
		t.Fatalf("ImportTrelloProject: %v", err)
	}
	if countRows(t, st.db, `SELECT COUNT(*) FROM todo_assets`) != 0 {
		t.Fatal("external image created a reference")
	}
}

func TestSyncLongImportBody(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	todo := createAssetTestTodo(t, st, ctx, p.ID, "Long")
	var b strings.Builder
	for range 600 {
		b.WriteString(imageMD(insertTestAsset(t, sqlDB, p.ID).UUID))
		b.WriteString("\n")
	}
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := syncTodoAssetsTx(ctx, tx, p.ID, todo.ID, b.String(), 1); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if todoAssetCount(t, st, todo.ID) != 600 {
		t.Fatalf("want 600 references, got %d", todoAssetCount(t, st, todo.ID))
	}
}

// setupAssetOwnedProject returns a project owned by a bootstrapped maintainer and that user's context.
func setupAssetOwnedProject(t *testing.T) (*Store, *sql.DB, context.Context, Project) {
	t.Helper()
	st, sqlDB, ctx := setupAssetOwnerStore(t)
	owner, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithUserID(ctx, owner.ID)
	p, err := st.CreateProject(ctx, "Assets")
	if err != nil {
		t.Fatal(err)
	}
	return st, sqlDB, ctx, p
}

func stripExportAssets(data *ExportData) {
	for i := range data.Projects {
		data.Projects[i].Assets = nil
	}
}
