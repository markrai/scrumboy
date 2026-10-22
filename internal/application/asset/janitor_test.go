package asset

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"scrumboy/internal/assets/blobstore"
	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

type janitorFixture struct {
	st      *store.Store
	sqlDB   *sql.DB
	blobs   *blobstore.Store
	pending *blobstore.Pending
	dataDir string
	project store.Project
	ctx     context.Context
}

func newJanitorFixture(t *testing.T) *janitorFixture {
	t.Helper()
	dataDir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dataDir, "app.db"), db.Options{BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	ctx := context.Background()
	if err := migrate.Apply(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	st := store.New(sqlDB, nil)
	p, err := st.CreateProject(ctx, "Janitor")
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blobstore.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	return &janitorFixture{st: st, sqlDB: sqlDB, blobs: blobs, pending: blobstore.NewPending(), dataDir: dataDir, project: p, ctx: ctx}
}

func (f *janitorFixture) janitor(flag bool) *Janitor {
	return NewJanitor(JanitorOptions{Store: f.st, Blobs: f.blobs, Pending: f.pending, Logger: log.New(io.Discard, "", 0), FlagEnabled: flag, DataDir: f.dataDir})
}

// addAsset writes a file and inserts its row with the given unreferenced_at.
func (f *janitorFixture) addAsset(t *testing.T, unreferencedAt int64) string {
	t.Helper()
	id := uuid.NewString()
	if err := f.blobs.CreateExclusive(id, "png", []byte("png-bytes")); err != nil {
		t.Fatal(err)
	}
	sum := make([]byte, 32)
	_, _ = rand.Read(sum)
	if _, err := f.sqlDB.Exec(`INSERT INTO project_assets(uuid, project_id, mime_type, size_bytes, width, height, sha256, created_at, unreferenced_at) VALUES (?, ?, 'image/png', 9, 1, 1, ?, 1, ?)`,
		id, f.project.ID, hex.EncodeToString(sum), unreferencedAt); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *janitorFixture) fileExists(t *testing.T, id string) bool {
	t.Helper()
	p, err := f.blobs.Path(id, "png")
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(p)
	return err == nil
}

func (f *janitorFixture) backdateFile(t *testing.T, id string, age time.Duration) {
	t.Helper()
	p, _ := f.blobs.Path(id, "png")
	old := time.Now().Add(-age)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
}

func (f *janitorFixture) holdRemovals(t *testing.T) func() {
	t.Helper()
	hold := filepath.Join(f.blobs.Root(), blobstore.RemovalHoldName)
	if err := os.MkdirAll(f.blobs.Root(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hold, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return func() { _ = os.Remove(hold) }
}

func TestJanitorGCRemovesFileAfterCommit(t *testing.T) {
	f := newJanitorFixture(t)
	j := f.janitor(true)
	old := f.addAsset(t, 1)
	young := f.addAsset(t, time.Now().UnixMilli())
	if err := j.RunReconcile(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := j.RunGC(f.ctx); err != nil {
		t.Fatal(err)
	}
	if f.fileExists(t, old) || !f.fileExists(t, young) {
		t.Fatal("GC removed the wrong files")
	}
}

func TestJanitorGCBlockedUntilReconciled(t *testing.T) {
	f := newJanitorFixture(t)
	j := f.janitor(true)
	embedded := f.addAsset(t, 1)
	orphan := f.addAsset(t, 1)
	todo, err := f.st.CreateTodo(f.ctx, f.project.ID, store.CreateTodoInput{Title: "T", Body: "![x](/api/assets/" + embedded + ")", ColumnKey: store.DefaultColumnBacklog}, store.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an older binary: the reference row is missing and the asset looks old.
	if _, err := f.sqlDB.Exec(`DELETE FROM todo_assets WHERE todo_id = ?`, todo.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sqlDB.Exec(`UPDATE project_assets SET unreferenced_at = 1 WHERE uuid = ?`, embedded); err != nil {
		t.Fatal(err)
	}
	if err := j.RunGC(f.ctx); err != nil {
		t.Fatal(err)
	}
	if !f.fileExists(t, embedded) || !f.fileExists(t, orphan) {
		t.Fatal("GC ran before reconciliation")
	}
	if err := j.RunReconcile(f.ctx); err != nil || !j.Reconciled() {
		t.Fatalf("reconcile: %v", err)
	}
	if err := j.RunGC(f.ctx); err != nil {
		t.Fatal(err)
	}
	if !f.fileExists(t, embedded) || f.fileExists(t, orphan) {
		t.Fatal("after reconciliation: embedded must survive, orphan must go")
	}
}

func TestJanitorSweepRemovesRowlessOldFile(t *testing.T) {
	f := newJanitorFixture(t)
	j := f.janitor(true)
	id := uuid.NewString()
	if err := f.blobs.CreateExclusive(id, "png", []byte("x")); err != nil {
		t.Fatal(err)
	}
	f.backdateFile(t, id, 25*time.Hour)
	if err := j.RunSweep(f.ctx); err != nil {
		t.Fatal(err)
	}
	if f.fileExists(t, id) {
		t.Fatal("rowless old file not swept")
	}
}

func TestJanitorSweepKeepsHeldAndYoungFiles(t *testing.T) {
	f := newJanitorFixture(t)
	j := f.janitor(true)
	young := uuid.NewString()
	held := uuid.NewString()
	for _, id := range []string{young, held} {
		if err := f.blobs.CreateExclusive(id, "png", []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	f.backdateFile(t, held, 48*time.Hour)
	release := f.pending.Hold(held)
	defer release()
	if err := j.RunSweep(f.ctx); err != nil {
		t.Fatal(err)
	}
	if !f.fileExists(t, young) || !f.fileExists(t, held) {
		t.Fatal("sweep removed an in-flight or young file")
	}
}

func TestJanitorSweepKeepsFilesWithRows(t *testing.T) {
	f := newJanitorFixture(t)
	j := f.janitor(true)
	id := f.addAsset(t, time.Now().UnixMilli())
	f.backdateFile(t, id, 48*time.Hour)
	if err := j.RunSweep(f.ctx); err != nil {
		t.Fatal(err)
	}
	if !f.fileExists(t, id) {
		t.Fatal("sweep removed a file with a row")
	}
}

func TestJanitorInactiveByDefault(t *testing.T) {
	f := newJanitorFixture(t)
	j := f.janitor(false)
	if j.Active(f.ctx) {
		t.Fatal("janitor active with no flag, rows or directory")
	}
	j.Tick(f.ctx)
	j.Sweep(f.ctx)
	if f.blobs.RootExists() {
		t.Fatal("janitor created the assets directory")
	}
}

func TestRemovalHoldBlocksEveryUnlink(t *testing.T) {
	f := newJanitorFixture(t)
	j := f.janitor(true)
	gcVictim := f.addAsset(t, 1)
	orphan := uuid.NewString()
	if err := f.blobs.CreateExclusive(orphan, "png", []byte("x")); err != nil {
		t.Fatal(err)
	}
	f.backdateFile(t, orphan, 48*time.Hour)
	release := f.holdRemovals(t)
	if err := j.RunReconcile(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := j.RunGC(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := j.RunSweep(f.ctx); err != nil {
		t.Fatal(err)
	}
	j.RemoveFiles([]store.AssetFileRef{{UUID: orphan, MimeType: "image/png"}})
	if !f.fileExists(t, gcVictim) || !f.fileExists(t, orphan) {
		t.Fatal("a file was unlinked while the removal hold existed")
	}
	if n := countAssetRows(t, f.sqlDB); n != 0 {
		t.Fatalf("GC rows should still commit under the hold, %d left", n)
	}
	release()
	f.backdateFile(t, gcVictim, 48*time.Hour)
	if err := j.RunSweep(f.ctx); err != nil {
		t.Fatal(err)
	}
	if f.fileExists(t, gcVictim) || f.fileExists(t, orphan) {
		t.Fatal("files not reclaimed after the hold was lifted")
	}
}

func countAssetRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM project_assets`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestJanitorSweepsStaleImportStaging(t *testing.T) {
	f := newJanitorFixture(t)
	j := f.janitor(true)
	dir := filepath.Join(f.dataDir, ImportStagingDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "import-old.sbz")
	fresh := filepath.Join(dir, "import-new.sbz")
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(stale, old, old)
	if err := j.RunSweep(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale staging archive kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh staging archive removed")
	}
}

// Finding D: a Replace import that crashed (even on a build without inline images) leaves a
// staging project. The reap must run with the feature disabled and no asset rows or directory,
// and must never touch a batch staged by the running process.
func TestJanitorReapsLegacyStagingWhileInactive(t *testing.T) {
	f := newJanitorFixture(t)
	start := time.Now()
	j := NewJanitor(JanitorOptions{Store: f.st, Blobs: f.blobs, Pending: f.pending, Logger: log.New(io.Discard, "", 0), FlagEnabled: false, Reaper: f.st, DataDir: f.dataDir, Now: func() time.Time { return start }})
	if j.Active(f.ctx) {
		t.Fatal("precondition: janitor must be inactive")
	}
	crashed, err := f.st.CreateProject(f.ctx, "Crashed staging")
	if err != nil {
		t.Fatal(err)
	}
	live, err := f.st.CreateProject(f.ctx, "Live staging")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.sqlDB.Exec(`UPDATE projects SET import_batch_id = 'legacy-crash', last_activity_at = ? WHERE id = ?`, start.Add(-time.Hour).UnixMilli(), crashed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sqlDB.Exec(`UPDATE projects SET import_batch_id = 'in-flight', last_activity_at = ? WHERE id = ?`, start.Add(time.Second).UnixMilli(), live.ID); err != nil {
		t.Fatal(err)
	}

	j.Tick(f.ctx)

	if n := countProjects(t, f.sqlDB, `import_batch_id = 'legacy-crash'`); n != 0 {
		t.Fatalf("crashed legacy staging batch kept (%d rows)", n)
	}
	if n := countProjects(t, f.sqlDB, `import_batch_id = 'in-flight'`); n != 1 {
		t.Fatalf("current-process staging batch deleted (%d rows)", n)
	}
	if n := countProjects(t, f.sqlDB, `id = ?`, f.project.ID); n != 1 {
		t.Fatal("ordinary project deleted")
	}
	if f.blobs.RootExists() {
		t.Fatal("reap created the assets directory")
	}
	if j.Reconciled() {
		t.Fatal("inactive janitor ran reconciliation")
	}
}

func TestJanitorSweepsImportStagingWhileInactive(t *testing.T) {
	f := newJanitorFixture(t)
	j := f.janitor(false)
	dir := filepath.Join(f.dataDir, ImportStagingDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "import-old.sbz")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(stale, old, old)
	j.Sweep(f.ctx)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale staging archive kept while inactive")
	}
	if f.blobs.RootExists() {
		t.Fatal("sweep created the assets directory")
	}
}

func countProjects(t *testing.T, db *sql.DB, where string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM projects WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
