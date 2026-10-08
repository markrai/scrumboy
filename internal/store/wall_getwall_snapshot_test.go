package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
)

// newWallSnapshotStores opens two MaxOpenConns(1) Stores against one WAL
// database so a read transaction on the primary connection can observe SQLite
// snapshot isolation while the secondary connection commits a Wall mutation.
func newWallSnapshotStores(t *testing.T) (*Store, *Store, *sql.DB) {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "wall-snapshot.db")
	options := db.Options{BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL"}
	primaryDB, err := db.Open(databasePath, options)
	if err != nil {
		t.Fatalf("open primary db: %v", err)
	}
	if err := migrate.Apply(context.Background(), primaryDB); err != nil {
		_ = primaryDB.Close()
		t.Fatalf("migrate: %v", err)
	}
	secondaryDB, err := db.Open(databasePath, options)
	if err != nil {
		_ = primaryDB.Close()
		t.Fatalf("open secondary db: %v", err)
	}
	t.Cleanup(func() {
		_ = secondaryDB.Close()
		_ = primaryDB.Close()
	})
	return New(primaryDB, nil), New(secondaryDB, nil), primaryDB
}

// TestGetWallReadTransactionSeesConsistentPlacementSnapshot proves the
// GetWall helper seam (project_walls + listWallStoryPlacementsTx) retains one
// SQLite read snapshot across both queries under WAL.
//
// Public GetWall cannot be interleaved without production test hooks, so this
// exercises the same transaction-owned path GetWall uses after BeginTx.
func TestGetWallReadTransactionSeesConsistentPlacementSnapshot(t *testing.T) {
	primary, secondary, primaryDB := newWallSnapshotStores(t)
	ctx := context.Background()
	user, err := primary.BootstrapUser(ctx, "wall-snapshot@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	project, err := primary.CreateProject(ownerCtx, "Wall snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := primary.CreateNote(ownerCtx, project.ID, CreateNoteInput{Color: "#ffd966", Text: "anchor"}); err != nil {
		t.Fatal(err)
	}
	first, err := primary.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "First pin"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	second, err := primary.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "Second pin"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := primary.PinWallStory(ownerCtx, project.ID, first.LocalID, 10, 20); err != nil {
		t.Fatal(err)
	}

	before, err := primary.GetWall(ownerCtx, project.ID)
	if err != nil {
		t.Fatalf("GetWall before: %v", err)
	}
	if before.Version <= 0 || len(before.Stories) != 1 || before.Stories[0].TodoLocalID != first.LocalID {
		t.Fatalf("precondition wall=%+v", before)
	}

	tx, err := primaryDB.BeginTx(ownerCtx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin read tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	var notesJSON, edgesJSON string
	var version, updatedAt int64
	if err := tx.QueryRowContext(ownerCtx,
		`SELECT notes, edges, version, updated_at FROM project_walls WHERE project_id = ?`,
		project.ID,
	).Scan(&notesJSON, &edgesJSON, &version, &updatedAt); err != nil {
		t.Fatalf("snapshot project_walls: %v", err)
	}
	if version != before.Version || notesJSON == "" {
		t.Fatalf("snapshot wall row version=%d notes empty=%v", version, notesJSON == "")
	}

	// Concurrent writer commits a new placement + version bump on a second
	// WAL connection while the primary read transaction remains open.
	if _, _, err := secondary.PinWallStory(ownerCtx, project.ID, second.LocalID, 30, 40); err != nil {
		t.Fatalf("concurrent PinWallStory: %v", err)
	}

	stories, err := listWallStoryPlacementsTx(ownerCtx, tx, project.ID)
	if err != nil {
		t.Fatalf("snapshot placements: %v", err)
	}
	if len(stories) != 1 || stories[0].TodoLocalID != first.LocalID || stories[0].Todo.Title != "First pin" {
		t.Fatalf("read tx mixed post-mutation placements: %+v", stories)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback read tx: %v", err)
	}

	after, err := primary.GetWall(ownerCtx, project.ID)
	if err != nil {
		t.Fatalf("GetWall after: %v", err)
	}
	if after.Version <= before.Version {
		t.Fatalf("expected version bump after concurrent pin, before=%d after=%d", before.Version, after.Version)
	}
	if len(after.Stories) != 2 {
		t.Fatalf("post-commit GetWall stories=%+v", after.Stories)
	}
}
