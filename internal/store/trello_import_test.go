package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
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

func TestImportTrelloProject_PreservesProblematicLabelsAndCardAssociations(t *testing.T) {
	st, sqlDB, cleanup := newTrelloTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "trello-labels@example.com", "password123", "Trello Labels")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	userCtx := store.WithUserID(ctx, user.ID)
	longPrefix := strings.Repeat("a", store.MaxTagRunes)
	board := trelloimport.Board{
		ID:   "problematic-labels",
		Name: "Problematic Trello Labels",
		Lists: []trelloimport.List{
			{ID: "doing", Name: "Doing", Pos: 10},
			{ID: "done", Name: "Done", Pos: 20},
		},
		Labels: []trelloimport.Label{
			{ID: "punctuation", Name: "Bug/Fix", Color: "blue"},
			{ID: "modern-fallback", Color: "green_light"},
			{ID: "bug-one", Name: "Bug", Color: "red"},
			{ID: "bug-two", Name: "Bug", Color: "yellow"},
			{ID: "genuine-suffix", Name: "Bug 2", Color: "green"},
			{ID: "long-one", Name: longPrefix + "x", Color: "orange"},
			{ID: "long-two", Name: longPrefix + "y", Color: "purple"},
			{ID: "nfc", Name: "Café", Color: "pink"},
			{ID: "nfd", Name: "Cafe\u0301", Color: "lime"},
		},
		Cards: []trelloimport.Card{
			{ID: "card-a", Name: "Card A", IDList: "doing", Pos: 10, IDLabels: []string{"punctuation", "bug-one", "modern-fallback"}},
			{ID: "card-b", Name: "Card B", IDList: "doing", Pos: 20, IDLabels: []string{"bug-two", "genuine-suffix", "long-one", "nfc"}},
			{ID: "card-c", Name: "Card C", IDList: "done", Pos: 10, IDLabels: []string{"long-two", "nfd", "punctuation"}},
		},
	}
	raw, err := json.Marshal(board)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := trelloimport.BuildImportBundle(raw, time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildImportBundle: %v", err)
	}
	if len(bundle.Preview.HardErrors) != 0 {
		t.Fatalf("preview hard errors: %v", bundle.Preview.HardErrors)
	}

	project, err := st.ImportTrelloProject(userCtx, bundle.ExportData, bundle.ProjectImportMetadata, bundle.TodoImportMetadataByLocalID, store.ModeFull)
	if err != nil {
		t.Fatalf("ImportTrelloProject: %v", err)
	}
	if project.Name != board.Name || project.ID == 0 {
		t.Fatalf("imported project = %+v", project)
	}

	var projectCount, todoCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM projects WHERE id = ?`, project.ID).Scan(&projectCount); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM todos WHERE project_id = ?`, project.ID).Scan(&todoCount); err != nil {
		t.Fatal(err)
	}
	if projectCount != 1 || todoCount != len(board.Cards) {
		t.Fatalf("project count=%d todo count=%d", projectCount, todoCount)
	}

	expectedColors := make(map[string]string, len(board.Labels))
	for _, tag := range bundle.ExportData.Projects[0].Tags {
		canonical := store.CanonicalizeTag(tag.Name)
		if canonical == "" || tag.Color == nil {
			t.Fatalf("invalid expected tag: %+v", tag)
		}
		expectedColors[canonical] = *tag.Color
	}
	rows, err := sqlDB.Query(`
SELECT tg.name, COALESCE(utc.color, '')
FROM tags tg
JOIN project_tags pt ON pt.tag_id = tg.id
LEFT JOIN user_tag_colors utc ON utc.tag_id = tg.id AND utc.user_id = ?
WHERE pt.project_id = ?
ORDER BY tg.name`, user.ID, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedColors := make(map[string]string, len(board.Labels))
	for rows.Next() {
		var name, color string
		if err := rows.Scan(&name, &color); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if canonical := store.CanonicalizeTag(name); canonical == "" || canonical != name {
			rows.Close()
			t.Fatalf("stored invalid or noncanonical tag %q", name)
		}
		storedColors[name] = color
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(storedColors, expectedColors) {
		t.Fatalf("stored tag colors = %#v, want %#v", storedColors, expectedColors)
	}
	if len(storedColors) != len(board.Labels) {
		t.Fatalf("stored %d distinct tags for %d source labels", len(storedColors), len(board.Labels))
	}

	expectedTodoTags := make(map[string][]string, len(board.Cards))
	for _, todo := range bundle.ExportData.Projects[0].Todos {
		for _, name := range todo.Tags {
			expectedTodoTags[todo.Title] = append(expectedTodoTags[todo.Title], store.CanonicalizeTag(name))
		}
		sort.Strings(expectedTodoTags[todo.Title])
	}
	rows, err = sqlDB.Query(`
SELECT td.title, tg.name
FROM todos td
JOIN todo_tags tt ON tt.todo_id = td.id
JOIN tags tg ON tg.id = tt.tag_id
WHERE td.project_id = ?
ORDER BY td.title, tg.name`, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedTodoTags := make(map[string][]string, len(board.Cards))
	for rows.Next() {
		var title, name string
		if err := rows.Scan(&title, &name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		storedTodoTags[title] = append(storedTodoTags[title], name)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(storedTodoTags, expectedTodoTags) {
		t.Fatalf("stored todo tags = %#v, want %#v", storedTodoTags, expectedTodoTags)
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
