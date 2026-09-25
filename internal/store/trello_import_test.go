package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
	"scrumboy/internal/trelloimport"
)

func newTrelloTestStore(t *testing.T) (*store.Store, *sql.DB, func()) {
	t.Helper()

	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}
	return store.New(sqlDB, nil), sqlDB, func() { _ = sqlDB.Close() }
}

func simpleTrelloBoardJSON() []byte {
	return []byte(`{
		"id":"board-store-1",
		"name":"Imported Trello Board",
		"lists":[
			{"id":"list-open","name":"Doing","pos":10,"closed":false},
			{"id":"list-done","name":"Done","pos":20,"closed":false}
		],
		"cards":[
			{"id":"card-1","name":"Card One","idList":"list-open","pos":10,"closed":false,"idLabels":[],"idMembers":[]}
		]
	}`)
}

func buildBundle(t *testing.T) *trelloimport.Bundle {
	t.Helper()
	bundle, err := trelloimport.BuildImportBundle(simpleTrelloBoardJSON(), time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildImportBundle: %v", err)
	}
	if len(bundle.Preview.HardErrors) != 0 {
		t.Fatalf("unexpected hard errors: %v", bundle.Preview.HardErrors)
	}
	return bundle
}

func TestImportTrelloProject_CreatesDistinctSlugOnDuplicateImport(t *testing.T) {
	st, sqlDB, cleanup := newTrelloTestStore(t)
	defer cleanup()

	bundle := buildBundle(t)
	ctx := context.Background()

	first, err := st.ImportTrelloProject(ctx, bundle.ExportData, bundle.ProjectImportMetadata, bundle.TodoImportMetadataByLocalID, store.ModeFull)
	if err != nil {
		t.Fatalf("first ImportTrelloProject: %v", err)
	}
	second, err := st.ImportTrelloProject(ctx, bundle.ExportData, bundle.ProjectImportMetadata, bundle.TodoImportMetadataByLocalID, store.ModeFull)
	if err != nil {
		t.Fatalf("second ImportTrelloProject: %v", err)
	}

	if first.Slug == second.Slug {
		t.Fatalf("expected distinct slugs, both were %q", first.Slug)
	}

	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM projects WHERE name = ?`, "Imported Trello Board").Scan(&count); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 imported projects, got %d", count)
	}
}

func TestImportTrelloProject_PersistsImportMetadata(t *testing.T) {
	st, sqlDB, cleanup := newTrelloTestStore(t)
	defer cleanup()

	bundle := buildBundle(t)
	project, err := st.ImportTrelloProject(context.Background(), bundle.ExportData, bundle.ProjectImportMetadata, bundle.TodoImportMetadataByLocalID, store.ModeFull)
	if err != nil {
		t.Fatalf("ImportTrelloProject: %v", err)
	}

	var projectMetadata string
	if err := sqlDB.QueryRow(`SELECT import_metadata FROM projects WHERE id = ?`, project.ID).Scan(&projectMetadata); err != nil {
		t.Fatalf("project import_metadata: %v", err)
	}
	if !strings.Contains(projectMetadata, `"source":"trello"`) {
		t.Fatalf("unexpected project import_metadata: %s", projectMetadata)
	}

	var todoMetadata string
	if err := sqlDB.QueryRow(`SELECT import_metadata FROM todos WHERE project_id = ? AND local_id = 1`, project.ID).Scan(&todoMetadata); err != nil {
		t.Fatalf("todo import_metadata: %v", err)
	}
	if !strings.Contains(todoMetadata, `"trelloCardId":"card-1"`) {
		t.Fatalf("unexpected todo import_metadata: %s", todoMetadata)
	}
}

func TestImportTrelloProject_PersistsClosedCardAsFirstClassArchiveWithoutTitleMarker(t *testing.T) {
	st, sqlDB, cleanup := newTrelloTestStore(t)
	defer cleanup()
	raw := []byte(`{
		"id":"board-closed-card",
		"name":"Closed Card Import",
		"lists":[
			{"id":"list-open","name":"Doing","pos":10,"closed":false},
			{"id":"list-done","name":"Done","pos":20,"closed":false}
		],
		"cards":[
			{"id":"card-closed","name":"Original title","idList":"list-open","pos":10,"closed":true,"idLabels":[],"idMembers":[]}
		]
	}`)
	bundle, err := trelloimport.BuildImportBundle(raw, time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.ImportTrelloProject(context.Background(), bundle.ExportData, bundle.ProjectImportMetadata, bundle.TodoImportMetadataByLocalID, store.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	var title string
	var archivedAt sql.NullInt64
	if err := sqlDB.QueryRow(`SELECT title, archived_at FROM todos WHERE project_id = ? AND local_id = 1`, project.ID).Scan(&title, &archivedAt); err != nil {
		t.Fatal(err)
	}
	if title != "Original title" || !archivedAt.Valid {
		t.Fatalf("closed card title=%q archivedAt=%+v", title, archivedAt)
	}
}

func TestImportTrelloProject_RollsBackOnMetadataMismatch(t *testing.T) {
	st, sqlDB, cleanup := newTrelloTestStore(t)
	defer cleanup()

	bundle := buildBundle(t)
	badMetadata := make(map[int64]string, len(bundle.TodoImportMetadataByLocalID)+1)
	for localID, metadata := range bundle.TodoImportMetadataByLocalID {
		badMetadata[localID] = metadata
	}
	badMetadata[999] = `{"source":"trello"}`

	if _, err := st.ImportTrelloProject(context.Background(), bundle.ExportData, bundle.ProjectImportMetadata, badMetadata, store.ModeFull); err == nil {
		t.Fatal("expected metadata mismatch error")
	}

	var projectCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&projectCount); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if projectCount != 0 {
		t.Fatalf("expected rollback to leave 0 projects, got %d", projectCount)
	}

	var todoCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM todos`).Scan(&todoCount); err != nil {
		t.Fatalf("count todos: %v", err)
	}
	if todoCount != 0 {
		t.Fatalf("expected rollback to leave 0 todos, got %d", todoCount)
	}
}

// TestImportTrelloProject_RejectsMislabeledAndUnsupportedVersions confirms the Trello entry
// point shares the single backup version gate rather than carrying its own. A Trello bundle
// is always stamped 1.2 and always declares archive state, so relabelling it 1.1 is exactly
// the mislabeled-payload case the gate exists to reject.
func TestImportTrelloProject_RejectsMislabeledAndUnsupportedVersions(t *testing.T) {
	st, _, cleanup := newTrelloTestStore(t)
	defer cleanup()
	raw := []byte(`{
		"id":"board-version",
		"name":"Version Gate",
		"lists":[
			{"id":"list-open","name":"Doing","pos":10,"closed":false},
			{"id":"list-done","name":"Done","pos":20,"closed":false}
		],
		"cards":[
			{"id":"card-closed","name":"Closed card","idList":"list-open","pos":10,"closed":true,"idLabels":[],"idMembers":[]}
		]
	}`)

	for _, version := range []string{"1.1", "1.0", "1.3"} {
		t.Run("rejects "+version, func(t *testing.T) {
			bundle, err := trelloimport.BuildImportBundle(raw, time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			bundle.ExportData.Version = version
			if _, err := st.ImportTrelloProject(context.Background(), bundle.ExportData, bundle.ProjectImportMetadata, bundle.TodoImportMetadataByLocalID, store.ModeFull); !errors.Is(err, store.ErrValidation) {
				t.Fatalf("version %q err=%v want validation", version, err)
			}
		})
	}

	// The version the transform actually produces still imports.
	bundle, err := trelloimport.BuildImportBundle(raw, time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportTrelloProject(context.Background(), bundle.ExportData, bundle.ProjectImportMetadata, bundle.TodoImportMetadataByLocalID, store.ModeFull); err != nil {
		t.Fatalf("native Trello bundle rejected: %v", err)
	}
}
