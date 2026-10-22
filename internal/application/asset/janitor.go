// Package asset coordinates inline-image asset files (internal/assets/blobstore) with their
// SQLite metadata (internal/store): uploads, and the lifecycle janitor.
package asset

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"scrumboy/internal/assets/blobstore"
	"scrumboy/internal/store"
)

const (
	// DraftRetention is how long an asset stays after its last reference disappears (or after
	// upload, if it was never referenced). It is finite: a draft left unsaved longer may lose its
	// uploads.
	DraftRetention = 7 * 24 * time.Hour
	// OrphanFileGrace protects files without a row (in-flight writes, crash leftovers) from the sweep.
	OrphanFileGrace = 24 * time.Hour
	// ImportStagingDir holds uploaded backup archives while they are processed.
	ImportStagingDir = "import-staging"
)

// JanitorStore is the store surface the janitor needs.
type JanitorStore interface {
	HasAnyAssets(ctx context.Context) (bool, error)
	CollectGarbageAssets(ctx context.Context, nowMs int64, opts store.GCOptions) (store.GCResult, error)
	ReconcileTodoAssets(ctx context.Context, afterTodoID int64, limit int) (int64, int, bool, error)
	ExistingAssetUUIDs(ctx context.Context, uuids []string) (map[string]struct{}, error)
}

// StagingReaper reaps Replace-import staging batches left by a crash (optional; Phase 09).
type StagingReaper interface {
	ReapStaleImportBatches(ctx context.Context, startedBeforeMs int64) (int64, []store.AssetFileRef, error)
}

// Janitor reclaims asset files and rows: per-start reference reconciliation, GC of unreferenced
// assets (gated on reconciliation), post-commit file removal and the orphan-file sweep.
type Janitor struct {
	store       JanitorStore
	blobs       *blobstore.Store
	pending     *blobstore.Pending
	log         *log.Logger
	now         func() time.Time
	flagEnabled bool
	reaper      StagingReaper
	dataDir     string
	startedAt   time.Time

	busy sync.Mutex

	mu              sync.Mutex
	reconcileCursor int64
	reconciled      bool
}

// JanitorOptions configures a Janitor.
type JanitorOptions struct {
	Store       JanitorStore
	Blobs       *blobstore.Store
	Pending     *blobstore.Pending
	Logger      *log.Logger
	FlagEnabled bool
	Reaper      StagingReaper
	// DataDir is used for the import staging sweep (optional).
	DataDir string
	Now     func() time.Time
}

// NewJanitor constructs a Janitor.
func NewJanitor(opts JanitorOptions) *Janitor {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &Janitor{
		store:       opts.Store,
		blobs:       opts.Blobs,
		pending:     opts.Pending,
		log:         logger,
		now:         now,
		flagEnabled: opts.FlagEnabled,
		reaper:      opts.Reaper,
		dataDir:     opts.DataDir,
		startedAt:   now(),
	}
}

// Active reports whether there is any asset work to do: the feature is enabled, rows exist, or
// the assets directory exists. With the feature never used, the janitor stays inert.
func (j *Janitor) Active(ctx context.Context) bool {
	if j == nil {
		return false
	}
	if j.flagEnabled || j.blobs.RootExists() {
		return true
	}
	ok, err := j.store.HasAnyAssets(ctx)
	if err != nil {
		j.log.Printf("asset janitor: check assets: %v", err)
		return false
	}
	return ok
}

// Reconciled reports whether this process has completed its reconciliation pass.
func (j *Janitor) Reconciled() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.reconciled
}

// RemoveFiles removes files whose rows a committed transaction deleted. It is best-effort: files
// it cannot remove (including under a DR removal hold) are reclaimed later by the sweep.
func (j *Janitor) RemoveFiles(refs []store.AssetFileRef) {
	if j == nil {
		return
	}
	for _, ref := range refs {
		ext, ok := store.AssetExtensionForMIME(ref.MimeType)
		if !ok {
			continue
		}
		if err := j.blobs.Remove(ref.UUID, ext); err != nil && !errors.Is(err, blobstore.ErrRemovalHeld) {
			j.log.Printf("asset janitor: remove %s: %v", ref.UUID, err)
		}
	}
}

// RunReconcile continues this process's reconciliation pass until it completes or ctx ends.
func (j *Janitor) RunReconcile(ctx context.Context) error {
	if !j.busy.TryLock() {
		return nil
	}
	defer j.busy.Unlock()
	return j.runReconcile(ctx)
}

func (j *Janitor) runReconcile(ctx context.Context) error {
	j.mu.Lock()
	cursor, done := j.reconcileCursor, j.reconciled
	j.mu.Unlock()
	if done {
		return nil
	}
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		next, changed, finished, err := j.store.ReconcileTodoAssets(ctx, cursor, 200)
		if err != nil {
			return err
		}
		total += changed
		cursor = next
		j.mu.Lock()
		j.reconcileCursor = cursor
		if finished {
			j.reconciled = true
		}
		j.mu.Unlock()
		if finished {
			if total > 0 {
				j.log.Printf("asset janitor: reconciliation repaired %d note image references", total)
			}
			return nil
		}
	}
}

// RunGC deletes unreferenced assets past DraftRetention. It does nothing until this process's
// reconciliation pass has completed, so stale references can never cause a deletion.
func (j *Janitor) RunGC(ctx context.Context) error {
	if !j.busy.TryLock() {
		return nil
	}
	defer j.busy.Unlock()
	if !j.Reconciled() {
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		res, err := j.store.CollectGarbageAssets(ctx, j.now().UTC().UnixMilli(), store.GCOptions{Retention: DraftRetention, Limit: 200})
		if err != nil {
			return err
		}
		j.RemoveFiles(res.Deleted)
		if len(res.Deleted) > 0 || res.Repaired > 0 {
			j.log.Printf("asset janitor: collected %d unreferenced images, repaired %d references", len(res.Deleted), res.Repaired)
		}
		if res.Considered < 200 {
			return nil
		}
	}
}

// RunSweep removes files that have no row, are not in flight, and are older than OrphanFileGrace.
// It does nothing while the DR removal hold exists.
func (j *Janitor) RunSweep(ctx context.Context) error {
	if !j.busy.TryLock() {
		return nil
	}
	defer j.busy.Unlock()
	if j.blobs.RemovalHeld() {
		return nil
	}
	cutoff := j.now().Add(-OrphanFileGrace)
	type file struct{ uuid, ext string }
	var batch []file
	removed := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		ids := make([]string, len(batch))
		for i, f := range batch {
			ids[i] = f.uuid
		}
		existing, err := j.store.ExistingAssetUUIDs(ctx, ids)
		if err != nil {
			return err
		}
		for _, f := range batch {
			if _, ok := existing[f.uuid]; ok || j.pending.Held(f.uuid) {
				continue
			}
			if err := j.blobs.Remove(f.uuid, f.ext); err != nil {
				if errors.Is(err, blobstore.ErrRemovalHeld) {
					return nil
				}
				j.log.Printf("asset janitor: sweep remove %s: %v", f.uuid, err)
				continue
			}
			removed++
		}
		batch = batch[:0]
		return nil
	}
	err := j.blobs.Walk(ctx, func(uuid, ext string, mod time.Time) error {
		if mod.After(cutoff) || j.pending.Held(uuid) {
			return nil
		}
		batch = append(batch, file{uuid, ext})
		if len(batch) >= 500 {
			return flush()
		}
		return nil
	})
	if err == nil {
		err = flush()
	}
	if removed > 0 {
		j.log.Printf("asset janitor: swept %d orphan image files", removed)
	}
	j.sweepImportStaging(cutoff)
	return err
}

// sweepImportStaging removes uploaded backup archives left behind by a crash.
func (j *Janitor) sweepImportStaging(cutoff time.Time) {
	if j.dataDir == "" {
		return
	}
	dir := filepath.Join(j.dataDir, ImportStagingDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

// RunReap reaps Replace-import staging batches staged before this process started.
func (j *Janitor) RunReap(ctx context.Context) error {
	if j.reaper == nil {
		return nil
	}
	n, refs, err := j.reaper.ReapStaleImportBatches(ctx, j.startedAt.UTC().UnixMilli())
	if err != nil {
		return err
	}
	j.RemoveFiles(refs)
	if n > 0 {
		j.log.Printf("asset janitor: removed %d stale import staging projects", n)
	}
	return nil
}

// Tick runs one janitor cycle: reap, reconcile, then GC, each with its own deadline. The sweep
// runs separately (see RunSweep) on a slower cadence.
//
// The reap always runs: crashed Replace imports leave staging projects whether or not inline
// images were ever enabled. Only reconcile and GC wait for Active.
func (j *Janitor) Tick(parent context.Context) {
	if j == nil {
		return
	}
	run := func(name string, fn func(context.Context) error) {
		ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
		defer cancel()
		if err := fn(ctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			j.log.Printf("asset janitor: %s: %v", name, err)
		}
	}
	run("reap", j.RunReap)
	if !j.Active(parent) {
		return
	}
	run("reconcile", j.RunReconcile)
	run("gc", j.RunGC)
}

// Sweep runs RunSweep with a deadline when the janitor is active. Uploaded backup archives left
// in the import staging directory by a crash are removed either way.
func (j *Janitor) Sweep(parent context.Context) {
	if j == nil {
		return
	}
	if !j.Active(parent) {
		j.sweepImportStaging(j.now().Add(-OrphanFileGrace))
		return
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	if err := j.RunSweep(ctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		j.log.Printf("asset janitor: sweep: %v", err)
	}
}
