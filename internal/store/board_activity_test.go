package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
)

func newTestStoreWithSQL(t *testing.T) (*Store, *sql.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "app.db"), db.Options{
		BusyTimeout:   5000,
		JournalMode:   "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	ctx := context.Background()
	if err := migrate.Apply(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}
	return New(sqlDB, nil), sqlDB, func() { _ = sqlDB.Close() }
}

func TestDurableBoardRead_DoesNotRefreshLastActivityAt(t *testing.T) {
	st, sqlDB, cleanup := newTestStoreWithSQL(t)
	defer cleanup()
	ctx := context.Background()
	p, err := st.CreateProject(ctx, "Durable")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	oldMs := time.Now().UTC().Add(-10 * time.Minute).UnixMilli()
	if _, err := sqlDB.Exec(`UPDATE projects SET last_activity_at = ? WHERE id = ?`, oldMs, p.ID); err != nil {
		t.Fatalf("set old last_activity_at: %v", err)
	}
	pc, err := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	if err != nil {
		t.Fatalf("GetProjectContextForRead: %v", err)
	}
	if _, _, _, _, err := st.GetBoard(ctx, &pc, "", "", SprintFilter{Mode: "none"}); err != nil {
		t.Fatalf("GetBoard: %v", err)
	}
	var lastMs int64
	if err := sqlDB.QueryRow(`SELECT last_activity_at FROM projects WHERE id = ?`, p.ID).Scan(&lastMs); err != nil {
		t.Fatalf("read last_activity_at: %v", err)
	}
	if lastMs != oldMs {
		t.Fatalf("durable GetBoard should not refresh last_activity_at: got %d want %d", lastMs, oldMs)
	}
}

func TestExpiringBoardRead_RefreshesLastActivityWhenStale(t *testing.T) {
	st, sqlDB, cleanup := newTestStoreWithSQL(t)
	defer cleanup()
	ctx := context.Background()
	p, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	oldMs := time.Now().UTC().Add(-10 * time.Minute).UnixMilli()
	if _, err := sqlDB.Exec(`UPDATE projects SET last_activity_at = ? WHERE id = ?`, oldMs, p.ID); err != nil {
		t.Fatalf("set old last_activity_at: %v", err)
	}
	pc, err := st.GetProjectContextForRead(ctx, p.ID, ModeAnonymous)
	if err != nil {
		t.Fatalf("GetProjectContextForRead: %v", err)
	}
	if _, _, _, _, err := st.GetBoard(ctx, &pc, "", "", SprintFilter{Mode: "none"}); err != nil {
		t.Fatalf("GetBoard: %v", err)
	}
	var lastMs int64
	if err := sqlDB.QueryRow(`SELECT last_activity_at FROM projects WHERE id = ?`, p.ID).Scan(&lastMs); err != nil {
		t.Fatalf("read last_activity_at: %v", err)
	}
	if lastMs <= oldMs {
		t.Fatalf("expiring GetBoard should refresh stale last_activity_at: got %d prev %d", lastMs, oldMs)
	}
}
