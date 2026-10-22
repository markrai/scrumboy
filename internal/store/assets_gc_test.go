package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

const gcDay = int64(24 * time.Hour / time.Millisecond)

var gcOpts = GCOptions{Retention: 7 * 24 * time.Hour}

func backdateUnreferenced(t *testing.T, db *sql.DB, assetID, ms int64) {
	t.Helper()
	if _, err := db.Exec(`UPDATE project_assets SET unreferenced_at = ? WHERE id = ?`, ms, assetID); err != nil {
		t.Fatal(err)
	}
}

func assetExists(t *testing.T, db *sql.DB, assetID int64) bool {
	t.Helper()
	return countRows(t, db, `SELECT COUNT(*) FROM project_assets WHERE id = ?`, assetID) == 1
}

func TestGCRetainsAt6DaysDeletesAt8Days(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	now := time.Now().UTC().UnixMilli()
	young := insertTestAsset(t, sqlDB, p.ID)
	old := insertTestAsset(t, sqlDB, p.ID)
	backdateUnreferenced(t, sqlDB, young.ID, now-6*gcDay)
	backdateUnreferenced(t, sqlDB, old.ID, now-8*gcDay)
	res, err := st.CollectGarbageAssets(ctx, now, gcOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 || res.Deleted[0].UUID != old.UUID || res.Deleted[0].MimeType != "image/png" {
		t.Fatalf("deleted = %+v", res.Deleted)
	}
	if !assetExists(t, sqlDB, young.ID) || assetExists(t, sqlDB, old.ID) {
		t.Fatal("wrong asset deleted")
	}
}

func TestGCDraftNeverReferenced(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	// insertTestAsset stamps unreferenced_at = created_at (2023), so a never-attached draft is old.
	draft := insertTestAsset(t, sqlDB, p.ID)
	res, err := st.CollectGarbageAssets(ctx, time.Now().UTC().UnixMilli(), gcOpts)
	if err != nil || len(res.Deleted) != 1 || assetExists(t, sqlDB, draft.ID) {
		t.Fatalf("draft not collected: %+v, %v", res, err)
	}
}

func TestGCSkipsReferencedRace(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	todo := createAssetTestTodo(t, st, ctx, p.ID, "Saved concurrently")
	gcBeforeDeleteHook = func(ctx context.Context, tx *sql.Tx, assetID int64) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO todo_assets(todo_id, asset_id, created_at) VALUES (?, ?, 1)`, todo.ID, assetID); err != nil {
			t.Errorf("hook insert: %v", err)
		}
	}
	defer func() { gcBeforeDeleteHook = nil }()
	res, err := st.CollectGarbageAssets(ctx, time.Now().UTC().UnixMilli(), gcOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 0 || !assetExists(t, sqlDB, a.ID) {
		t.Fatal("GC deleted an asset referenced before its conditional delete")
	}
}

func TestGCDoesNotRetainNonCanonicalMentions(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	path := "/api/assets/" + a.UUID
	for _, body := range []string{
		"```\n![a](" + path + ")\n```",
		"inline `![a](" + path + ")` code",
		"prose " + path,
		"[link](" + path + ")",
	} {
		if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Mention", Body: body, ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
			t.Fatal(err)
		}
	}
	unrefBefore := assetUnreferencedAt(t, sqlDB, a.ID)
	res, err := st.CollectGarbageAssets(ctx, time.Now().UTC().UnixMilli(), gcOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 || assetExists(t, sqlDB, a.ID) {
		t.Fatalf("non-canonical mentions retained the asset (unreferenced_at was %+v): %+v", unrefBefore, res)
	}
}

func TestGCRepairsMissingRowForCanonicalEmbed(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Embedded", Body: imageMD(a.UUID), ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an older binary that never maintained todo_assets.
	if _, err := sqlDB.Exec(`DELETE FROM todo_assets WHERE todo_id = ?`, todo.ID); err != nil {
		t.Fatal(err)
	}
	backdateUnreferenced(t, sqlDB, a.ID, 1)
	res, err := st.CollectGarbageAssets(ctx, time.Now().UTC().UnixMilli(), gcOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 0 || res.Repaired != 1 || !assetExists(t, sqlDB, a.ID) || assetUnreferencedAt(t, sqlDB, a.ID).Valid {
		t.Fatalf("missing row not repaired: %+v", res)
	}
}

func TestGCWorkBudgetDefersButCompletes(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	first := insertTestAsset(t, sqlDB, p.ID)
	second := insertTestAsset(t, sqlDB, p.ID)
	backdateUnreferenced(t, sqlDB, first.ID, 1)
	backdateUnreferenced(t, sqlDB, second.ID, 2)
	for range 25 {
		if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "M", Body: "prose /api/assets/" + first.UUID, ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
			t.Fatal(err)
		}
	}
	opts := gcOpts
	opts.BodyBudget = 10
	res, err := st.CollectGarbageAssets(ctx, time.Now().UTC().UnixMilli(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 || res.Deleted[0].UUID != first.UUID {
		t.Fatalf("first run: %+v", res)
	}
	res, err = st.CollectGarbageAssets(ctx, time.Now().UTC().UnixMilli(), opts)
	if err != nil || len(res.Deleted) != 1 || res.Deleted[0].UUID != second.UUID {
		t.Fatalf("second run: %+v, %v", res, err)
	}
}

func TestBodyMentionOtherProjectDoesNotRetain(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	other, err := st.CreateProject(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	a := insertTestAsset(t, sqlDB, p.ID)
	if _, err := st.CreateTodo(ctx, other.ID, CreateTodoInput{Title: "Elsewhere", Body: imageMD(a.UUID), ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
		t.Fatal(err)
	}
	res, err := st.CollectGarbageAssets(ctx, time.Now().UTC().UnixMilli(), gcOpts)
	if err != nil || len(res.Deleted) != 1 {
		t.Fatalf("cross-project embed retained the asset: %+v, %v", res, err)
	}
}

func TestReconcileFixesMissingAndExtraRows(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	a := insertTestAsset(t, sqlDB, p.ID)
	b := insertTestAsset(t, sqlDB, p.ID)
	embeds, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Embeds a", Body: imageMD(a.UUID), ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	removed := createAssetTestTodo(t, st, ctx, p.ID, "No longer embeds b")
	for range 5 {
		createAssetTestTodo(t, st, ctx, p.ID, "filler")
	}
	// Downgrade drift: a's row missing, b's row stale.
	if _, err := sqlDB.Exec(`DELETE FROM todo_assets`); err != nil {
		t.Fatal(err)
	}
	linkTestAsset(t, sqlDB, removed.ID, b.ID)
	var after int64
	total, batches := 0, 0
	for {
		next, changed, done, err := st.ReconcileTodoAssets(ctx, after, 3)
		if err != nil {
			t.Fatal(err)
		}
		if next < after {
			t.Fatal("cursor moved backwards")
		}
		after, total, batches = next, total+changed, batches+1
		if done {
			break
		}
	}
	if total != 2 || batches < 3 {
		t.Fatalf("changed = %d over %d batches", total, batches)
	}
	if todoAssetCount(t, st, embeds.ID) != 1 || todoAssetCount(t, st, removed.ID) != 0 || !assetUnreferencedAt(t, sqlDB, b.ID).Valid {
		t.Fatal("reconcile did not converge")
	}
}

func TestDeleteProjectReturnsAssetFiles(t *testing.T) {
	st, sqlDB, ctx := setupAssetOwnerStore(t)
	owner, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithUserID(ctx, owner.ID)
	p, err := st.CreateProject(ctx, "Doomed")
	if err != nil {
		t.Fatal(err)
	}
	a := insertTestAsset(t, sqlDB, p.ID)
	snap, err := st.DeleteProject(ctx, p.ID, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.AssetFiles) != 1 || snap.AssetFiles[0].UUID != a.UUID {
		t.Fatalf("AssetFiles = %+v", snap.AssetFiles)
	}
}

func TestDeleteProjectFailureReturnsNothing(t *testing.T) {
	st, sqlDB, ctx := setupAssetOwnerStore(t)
	owner, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithUserID(ctx, owner.ID)
	p, err := st.CreateProject(ctx, "Undeletable")
	if err != nil {
		t.Fatal(err)
	}
	a := insertTestAsset(t, sqlDB, p.ID)
	if _, err := sqlDB.Exec(`CREATE TRIGGER trg_test_block_delete BEFORE DELETE ON projects WHEN OLD.name = 'Undeletable' BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	snap, err := st.DeleteProject(ctx, p.ID, owner.ID)
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("want blocked error, got %v", err)
	}
	if len(snap.AssetFiles) != 0 || !assetExists(t, sqlDB, a.ID) {
		t.Fatal("failed deletion must not report files and must keep the asset")
	}
}

func TestDeleteExpiredProjectsCollectingAssets(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	keep, err := st.CreateProject(ctx, "Durable")
	if err != nil {
		t.Fatal(err)
	}
	a := insertTestAsset(t, sqlDB, p.ID)
	kept := insertTestAsset(t, sqlDB, keep.ID)
	if _, err := sqlDB.Exec(`UPDATE projects SET expires_at = 1 WHERE id = ?`, p.ID); err != nil {
		t.Fatal(err)
	}
	n, refs, err := st.DeleteExpiredProjectsCollectingAssets(ctx)
	if err != nil || n != 1 || len(refs) != 1 || refs[0].UUID != a.UUID {
		t.Fatalf("= %d, %+v, %v", n, refs, err)
	}
	if !assetExists(t, sqlDB, kept.ID) {
		t.Fatal("durable project's asset deleted")
	}
	n, refs, err = st.DeleteExpiredProjectsCollectingAssets(ctx)
	if err != nil || n != 0 || refs != nil {
		t.Fatalf("second run = %d, %+v, %v", n, refs, err)
	}
}

func TestExistingAssetUUIDsAndHasAny(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	if ok, err := st.HasAnyAssets(ctx); err != nil || ok {
		t.Fatalf("HasAnyAssets = %v, %v", ok, err)
	}
	a := insertTestAsset(t, sqlDB, p.ID)
	missing := newTestAssetUUID(t)
	got, err := st.ExistingAssetUUIDs(ctx, []string{a.UUID, missing})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got[a.UUID]; !ok || len(got) != 1 {
		t.Fatalf("ExistingAssetUUIDs = %v", got)
	}
	if ok, _ := st.HasAnyAssets(ctx); !ok {
		t.Fatal("HasAnyAssets false with a row")
	}
}

func TestReapStaleImportBatches(t *testing.T) {
	st, sqlDB, ctx, p := setupAssetTestProject(t)
	live, err := st.CreateProject(ctx, "Live staging")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`UPDATE projects SET import_batch_id = 'crashed', last_activity_at = 1000 WHERE id = ?`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`UPDATE projects SET import_batch_id = 'running', last_activity_at = 5000 WHERE id = ?`, live.ID); err != nil {
		t.Fatal(err)
	}
	a := insertTestAsset(t, sqlDB, p.ID)
	insertTestAsset(t, sqlDB, live.ID)
	n, refs, err := st.ReapStaleImportBatches(ctx, 2000)
	if err != nil || n != 1 || len(refs) != 1 || refs[0].UUID != a.UUID {
		t.Fatalf("reap = %d, %+v, %v", n, refs, err)
	}
	if countRows(t, sqlDB, `SELECT COUNT(*) FROM projects WHERE import_batch_id = 'running'`) != 1 {
		t.Fatal("a batch staged after process start was reaped")
	}
	if n, _, err := st.ReapStaleImportBatches(ctx, 2000); err != nil || n != 0 {
		t.Fatalf("second reap = %d, %v", n, err)
	}
}
