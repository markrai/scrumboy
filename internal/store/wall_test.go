package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func backupWithWallStoriesField(t *testing.T, source *ExportData, slug string, present bool, stories []WallStoryPlacementExport) *ExportData {
	t.Helper()
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatal(err)
	}
	projects, ok := tree["projects"].([]any)
	if !ok {
		t.Fatalf("projects JSON=%T", tree["projects"])
	}
	found := false
	for _, item := range projects {
		project, ok := item.(map[string]any)
		if !ok || project["slug"] != slug {
			continue
		}
		wall, ok := project["wall"].(map[string]any)
		if !ok {
			t.Fatalf("wall JSON=%T", project["wall"])
		}
		if present {
			encoded, err := json.Marshal(stories)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal(encoded, &value); err != nil {
				t.Fatal(err)
			}
			wall["stories"] = value
		} else {
			delete(wall, "stories")
		}
		found = true
		break
	}
	if !found {
		t.Fatalf("project %q not found in backup JSON", slug)
	}
	raw, err = json.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ExportData
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return &decoded
}

func setupWallStoryBackup(t *testing.T) (*Store, func(), context.Context, Project, Todo, Todo, *ExportData) {
	t.Helper()
	st, cleanup := newTestStore(t)
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, fmt.Sprintf("wall-presence-%s@example.com", strings.ReplaceAll(t.Name(), "/", "-")), "password", "Owner")
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(ownerCtx, "Wall presence")
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	first, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "First story"}, ModeFull)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	second, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "Second story"}, ModeFull)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	if _, _, err := st.PinWallStory(ownerCtx, project.ID, first.LocalID, 10, 20); err != nil {
		cleanup()
		t.Fatal(err)
	}
	exported, err := st.ExportAllProjects(ownerCtx, ModeFull)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	return st, cleanup, ownerCtx, project, first, second, exported
}

func TestWallGetIsSideEffectFree(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	wall, err := st.GetWall(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetWall: %v", err)
	}
	if len(wall.Notes) != 0 {
		t.Fatalf("expected synthetic empty wall, got %d notes", len(wall.Notes))
	}
	if len(wall.Edges) != 0 || len(wall.Stories) != 0 {
		t.Fatalf("expected empty edges/stories on synthetic wall, got edges=%d stories=%d", len(wall.Edges), len(wall.Stories))
	}
	if wall.Version != 0 {
		t.Fatalf("expected version 0 for synthetic wall, got %d", wall.Version)
	}

	// Second GET must still not create a row.
	var count int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_walls WHERE project_id = ?`, p.ID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("GET should not materialize row, got count=%d", count)
	}
}

func TestGetWallHydratesPinnedStoriesWithNotesAndVersion(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "wall-get-hydrate@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(ownerCtx, "Wall get hydrate")
	if err != nil {
		t.Fatal(err)
	}
	note, _, err := st.CreateNote(ownerCtx, project.ID, CreateNoteInput{Color: "#ffd966", Text: "note"})
	if err != nil {
		t.Fatal(err)
	}
	todo, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "Pinned"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.PinWallStory(ownerCtx, project.ID, todo.LocalID, 11, 22); err != nil {
		t.Fatal(err)
	}

	wall, err := st.GetWall(ownerCtx, project.ID)
	if err != nil {
		t.Fatalf("GetWall: %v", err)
	}
	if wall.Version <= 0 {
		t.Fatalf("expected persisted wall version, got %d", wall.Version)
	}
	if len(wall.Notes) != 1 || wall.Notes[0].ID != note.ID {
		t.Fatalf("notes=%+v", wall.Notes)
	}
	if len(wall.Stories) != 1 {
		t.Fatalf("stories=%+v", wall.Stories)
	}
	story := wall.Stories[0]
	if story.TodoLocalID != todo.LocalID || story.X != 11 || story.Y != 22 {
		t.Fatalf("placement=%+v", story)
	}
	if story.Todo.Title != "Pinned" || story.Todo.ID != todo.ID {
		t.Fatalf("hydrated todo=%+v", story.Todo)
	}
}

func TestGetWallMissingRowIgnoresOrphanPlacements(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "wall-get-orphan@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(ownerCtx, "Wall get orphan")
	if err != nil {
		t.Fatal(err)
	}
	todo, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "Orphan pin"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ownerCtx, `
INSERT INTO wall_story_placements (project_id, todo_id, x, y, version)
VALUES (?, ?, 1, 2, 1)`, project.ID, todo.ID); err != nil {
		t.Fatalf("insert orphan placement: %v", err)
	}

	wall, err := st.GetWall(ownerCtx, project.ID)
	if err != nil {
		t.Fatalf("GetWall: %v", err)
	}
	if wall.Version != 0 || len(wall.Notes) != 0 || len(wall.Edges) != 0 || len(wall.Stories) != 0 {
		t.Fatalf("missing project_walls must stay synthetic-empty, got %+v", wall)
	}
	var wallRows int
	if err := st.db.QueryRowContext(ownerCtx, `SELECT COUNT(*) FROM project_walls WHERE project_id = ?`, project.ID).Scan(&wallRows); err != nil {
		t.Fatal(err)
	}
	if wallRows != 0 {
		t.Fatalf("GetWall materialized project_walls, count=%d", wallRows)
	}
	var placementRows int
	if err := st.db.QueryRowContext(ownerCtx, `SELECT COUNT(*) FROM wall_story_placements WHERE project_id = ?`, project.ID).Scan(&placementRows); err != nil {
		t.Fatal(err)
	}
	if placementRows != 1 {
		t.Fatalf("orphan placement should remain stored, count=%d", placementRows)
	}
}

func TestWallStoryPlacementBackupRoundTripUsesPortableLocalID(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "wall-backup@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(ownerCtx, "Wall backup")
	if err != nil {
		t.Fatal(err)
	}
	todo, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "Portable story"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.PinWallStory(ownerCtx, project.ID, todo.LocalID, 123, -456); err != nil {
		t.Fatal(err)
	}

	exported, err := st.ExportAllProjects(ownerCtx, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	var projectExport *ProjectExport
	for i := range exported.Projects {
		if exported.Projects[i].Slug == project.Slug {
			projectExport = &exported.Projects[i]
			break
		}
	}
	if projectExport == nil || projectExport.Wall == nil || len(projectExport.Wall.Stories) != 1 {
		t.Fatalf("exported wall=%+v", projectExport)
	}
	placement := projectExport.Wall.Stories[0]
	if placement.LocalID != todo.LocalID || placement.X != 123 || placement.Y != -456 {
		t.Fatalf("portable placement=%+v", placement)
	}
	raw, err := json.Marshal(projectExport.Wall)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "todoId") || strings.Contains(string(raw), "Portable story") {
		t.Fatalf("wall backup copied internal/canonical Todo data: %s", raw)
	}

	if _, err := st.ImportProjects(ownerCtx, exported, ModeFull, "replace"); err != nil {
		t.Fatal(err)
	}
	restoredProject, err := st.GetProjectBySlug(ownerCtx, project.Slug)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := st.GetWall(ownerCtx, restoredProject.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Stories) != 1 || restored.Stories[0].TodoLocalID != todo.LocalID || restored.Stories[0].Todo.Title != "Portable story" {
		t.Fatalf("restored stories=%+v", restored.Stories)
	}

	projectExport.Wall.Stories[0].LocalID = 999999
	result, err := st.ImportProjects(ownerCtx, exported, ModeFull, "replace")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) == 0 || !strings.Contains(strings.Join(result.Warnings, "\n"), "dangling Wall placement") {
		t.Fatalf("dangling placement warnings=%v", result.Warnings)
	}
}

func TestWallStoryPlacementMergeLegacyWallPreservesExistingPlacements(t *testing.T) {
	st, cleanup, ctx, project, first, _, exported := setupWallStoryBackup(t)
	defer cleanup()
	legacy := backupWithWallStoriesField(t, exported, project.Slug, false, nil)
	if legacy.Projects[0].Wall.StoriesPresent {
		t.Fatal("legacy Wall unexpectedly marked stories present")
	}
	if _, err := st.ImportProjects(ctx, legacy, ModeFull, "merge"); err != nil {
		t.Fatal(err)
	}
	wall, err := st.GetWall(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(wall.Stories) != 1 || wall.Stories[0].TodoLocalID != first.LocalID {
		t.Fatalf("legacy merge placements=%+v", wall.Stories)
	}
}

func TestWallStoryPlacementMergeExplicitEmptyClearsPlacements(t *testing.T) {
	st, cleanup, ctx, project, _, _, exported := setupWallStoryBackup(t)
	defer cleanup()
	current := backupWithWallStoriesField(t, exported, project.Slug, true, []WallStoryPlacementExport{})
	if !current.Projects[0].Wall.StoriesPresent {
		t.Fatal("explicit empty stories not marked present")
	}
	if _, err := st.ImportProjects(ctx, current, ModeFull, "merge"); err != nil {
		t.Fatal(err)
	}
	wall, err := st.GetWall(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(wall.Stories) != 0 {
		t.Fatalf("explicit empty merge placements=%+v", wall.Stories)
	}
}

func TestWallStoryPlacementMergeExplicitSetReplacesPlacements(t *testing.T) {
	st, cleanup, ctx, project, first, second, exported := setupWallStoryBackup(t)
	defer cleanup()
	current := backupWithWallStoriesField(t, exported, project.Slug, true, []WallStoryPlacementExport{{
		LocalID: second.LocalID, X: 333, Y: -444, Version: 7,
	}})
	if _, err := st.ImportProjects(ctx, current, ModeFull, "merge"); err != nil {
		t.Fatal(err)
	}
	wall, err := st.GetWall(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(wall.Stories) != 1 || wall.Stories[0].TodoLocalID != second.LocalID || wall.Stories[0].X != 333 || wall.Stories[0].Y != -444 || wall.Stories[0].Version != 7 {
		t.Fatalf("replacement placements=%+v (old localId=%d)", wall.Stories, first.LocalID)
	}
}

func TestWallStoryPlacementLegacyReplaceAndCopyStartEmpty(t *testing.T) {
	t.Run("replace", func(t *testing.T) {
		st, cleanup, ctx, project, _, _, exported := setupWallStoryBackup(t)
		defer cleanup()
		legacy := backupWithWallStoriesField(t, exported, project.Slug, false, nil)
		if _, err := st.ImportProjects(ctx, legacy, ModeFull, "replace"); err != nil {
			t.Fatal(err)
		}
		restored, err := st.GetProjectBySlug(ctx, project.Slug)
		if err != nil {
			t.Fatal(err)
		}
		wall, err := st.GetWall(ctx, restored.ID)
		if err != nil || len(wall.Stories) != 0 {
			t.Fatalf("legacy replace placements=%+v err=%v", wall.Stories, err)
		}
	})

	t.Run("copy", func(t *testing.T) {
		st, cleanup, ctx, project, first, _, exported := setupWallStoryBackup(t)
		defer cleanup()
		legacy := backupWithWallStoriesField(t, exported, project.Slug, false, nil)
		if _, err := st.ImportProjects(ctx, legacy, ModeFull, "copy"); err != nil {
			t.Fatal(err)
		}
		copied, err := st.GetProjectBySlug(ctx, project.Slug+"-imported")
		if err != nil {
			t.Fatal(err)
		}
		copiedWall, err := st.GetWall(ctx, copied.ID)
		if err != nil || len(copiedWall.Stories) != 0 {
			t.Fatalf("legacy copy placements=%+v err=%v", copiedWall.Stories, err)
		}
		originalWall, err := st.GetWall(ctx, project.ID)
		if err != nil || len(originalWall.Stories) != 1 || originalWall.Stories[0].TodoLocalID != first.LocalID {
			t.Fatalf("copy changed original placements=%+v err=%v", originalWall.Stories, err)
		}
	})
}

func TestWallExportRoundTripKeepsExplicitEmptyStoriesDistinctFromLegacyOmission(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "wall-empty-stories@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(ownerCtx, "Empty story export")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateNote(ownerCtx, project.ID, CreateNoteInput{X: 0, Y: 0, Width: 180, Height: 140, Color: "#FFFFFF", Text: "keep wall present"}); err != nil {
		t.Fatal(err)
	}
	exported, err := st.ExportAllProjects(ownerCtx, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"stories":[]`) {
		t.Fatalf("current export omitted explicit empty stories: %s", raw)
	}
	var roundTripped ExportData
	if err := json.Unmarshal(raw, &roundTripped); err != nil {
		t.Fatal(err)
	}
	if roundTripped.Projects[0].Wall == nil || !roundTripped.Projects[0].Wall.StoriesPresent || roundTripped.Projects[0].Wall.Stories == nil {
		t.Fatalf("round-tripped current Wall=%+v", roundTripped.Projects[0].Wall)
	}

	var legacy WallExport
	if err := json.Unmarshal([]byte(`{"notes":[]}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.StoriesPresent || legacy.Stories != nil {
		t.Fatalf("legacy Wall presence=%v stories=%v", legacy.StoriesPresent, legacy.Stories)
	}
	legacyRaw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacyRaw), `"stories"`) {
		t.Fatalf("legacy round trip invented stories field: %s", legacyRaw)
	}
}

func TestWallCreateMaterializesRow(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	note, wall, err := st.CreateNote(ctx, p.ID, CreateNoteInput{
		X: 10, Y: 20, Width: 180, Height: 140,
		Color: "#ffd966", Text: "hello",
	})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if note.ID == "" {
		t.Fatal("expected note id")
	}
	if note.Version != 1 {
		t.Fatalf("expected note.Version=1, got %d", note.Version)
	}
	if wall.Version != 1 {
		t.Fatalf("expected wall.Version=1, got %d", wall.Version)
	}

	reloaded, err := st.GetWall(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetWall: %v", err)
	}
	if len(reloaded.Notes) != 1 || reloaded.Notes[0].ID != note.ID {
		t.Fatalf("expected persisted note, got %#v", reloaded)
	}
}

func TestWallStoryPlacementLifecycleIsIdempotentAndCanonical(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project, err := st.CreateProject(ctx, "wall stories")
	if err != nil {
		t.Fatal(err)
	}
	todo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title: "Canonical title", Tags: []string{"wall", "shared"}, ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}

	placement, created, err := st.PinWallStory(ctx, project.ID, todo.LocalID, 12.5, -8)
	if err != nil || !created {
		t.Fatalf("first pin placement=%+v created=%v err=%v", placement, created, err)
	}
	again, created, err := st.PinWallStory(ctx, project.ID, todo.LocalID, 999, 999)
	if err != nil || created {
		t.Fatalf("second pin placement=%+v created=%v err=%v", again, created, err)
	}
	if again.X != placement.X || again.Y != placement.Y || again.Version != placement.Version {
		t.Fatalf("idempotent pin moved placement: first=%+v second=%+v", placement, again)
	}

	wall, err := st.GetWall(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(wall.Stories) != 1 || wall.Stories[0].Todo.ID != todo.ID || wall.Stories[0].Todo.Title != "Canonical title" {
		t.Fatalf("wall canonical projection=%+v", wall.Stories)
	}
	updated, err := st.PatchWallStory(ctx, project.ID, todo.LocalID, placement.Version, 45, 67)
	if err != nil || updated.Version != placement.Version+1 || updated.X != 45 || updated.Y != 67 {
		t.Fatalf("patch placement=%+v err=%v", updated, err)
	}
	if _, err := st.PatchWallStory(ctx, project.ID, todo.LocalID, placement.Version, 1, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale patch err=%v want conflict", err)
	}
	if err := st.UnpinWallStory(ctx, project.ID, todo.LocalID); err != nil {
		t.Fatal(err)
	}
	wall, err = st.GetWall(ctx, project.ID)
	if err != nil || len(wall.Stories) != 0 {
		t.Fatalf("wall after unpin=%+v err=%v", wall.Stories, err)
	}
	if persisted, err := st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, ModeFull); err != nil || persisted.ID != todo.ID {
		t.Fatalf("unpin changed canonical todo=%+v err=%v", persisted, err)
	}
}

func TestWallStoryPlacementRejectsCrossProjectAndCascadesOnTodoDelete(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	wallProject, _ := st.CreateProject(ctx, "wall")
	otherProject, _ := st.CreateProject(ctx, "other")
	otherTodo, err := st.CreateTodo(ctx, otherProject.ID, CreateTodoInput{Title: "Other"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.PinWallStory(ctx, wallProject.ID, otherTodo.LocalID, 0, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project pin err=%v want not found", err)
	}

	todo, err := st.CreateTodo(ctx, wallProject.ID, CreateTodoInput{Title: "Delete me"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.PinWallStory(ctx, wallProject.ID, todo.LocalID, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchiveTodoByLocalID(ctx, wallProject.ID, todo.LocalID, ModeFull); err != nil {
		t.Fatal(err)
	}
	archivedWall, err := st.GetWall(ctx, wallProject.ID)
	if err != nil || len(archivedWall.Stories) != 1 || archivedWall.Stories[0].Todo.ArchivedAt == nil {
		t.Fatalf("archived story projection=%+v err=%v", archivedWall.Stories, err)
	}
	if err := st.DeleteTodo(ctx, todo.ID, ModeFull); err != nil {
		t.Fatal(err)
	}
	wall, err := st.GetWall(ctx, wallProject.ID)
	if err != nil || len(wall.Stories) != 0 {
		t.Fatalf("cascade wall=%+v err=%v", wall.Stories, err)
	}
}

func TestWallPatchConflictsOnVersionMismatch(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	note, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	text := "updated"
	if _, _, err := st.PatchNote(ctx, p.ID, note.ID, PatchNoteInput{IfVersion: note.Version, Text: &text}); err != nil {
		t.Fatalf("PatchNote first: %v", err)
	}

	// Second patch using the stale version must conflict.
	text2 := "stale"
	_, _, err = st.PatchNote(ctx, p.ID, note.ID, PatchNoteInput{IfVersion: note.Version, Text: &text2})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestWallPatchOnDifferentNotesDoesNotConflict(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	a, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	if err != nil {
		t.Fatalf("CreateNote a: %v", err)
	}
	b, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	if err != nil {
		t.Fatalf("CreateNote b: %v", err)
	}

	ta := "a text"
	tb := "b text"
	if _, _, err := st.PatchNote(ctx, p.ID, a.ID, PatchNoteInput{IfVersion: a.Version, Text: &ta}); err != nil {
		t.Fatalf("PatchNote a: %v", err)
	}
	if _, _, err := st.PatchNote(ctx, p.ID, b.ID, PatchNoteInput{IfVersion: b.Version, Text: &tb}); err != nil {
		t.Fatalf("PatchNote b: %v", err)
	}
}

func TestWallDeleteNote(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	note, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	wall, err := st.DeleteNote(ctx, p.ID, note.ID)
	if err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	if len(wall.Notes) != 0 {
		t.Fatalf("expected 0 notes after delete, got %d", len(wall.Notes))
	}

	// Deleting again yields ErrNotFound.
	_, err = st.DeleteNote(ctx, p.ID, note.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestWallEdgeCreateRejectsSelfLoopAndUnknownNote(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	a, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	if err != nil {
		t.Fatalf("CreateNote a: %v", err)
	}

	if _, _, err := st.CreateEdge(ctx, p.ID, a.ID, a.ID); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for self-loop, got %v", err)
	}
	if _, _, err := st.CreateEdge(ctx, p.ID, a.ID, "n_does_not_exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown endpoint, got %v", err)
	}
}

func TestWallEdgeCreateIsIdempotent(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	a, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	if err != nil {
		t.Fatalf("CreateNote a: %v", err)
	}
	b, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	if err != nil {
		t.Fatalf("CreateNote b: %v", err)
	}

	first, w1, err := st.CreateEdge(ctx, p.ID, a.ID, b.ID)
	if err != nil {
		t.Fatalf("CreateEdge first: %v", err)
	}
	if first.ID == "" {
		t.Fatal("expected edge id")
	}
	if len(w1.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(w1.Edges))
	}

	// Same direction.
	again, w2, err := st.CreateEdge(ctx, p.ID, a.ID, b.ID)
	if err != nil {
		t.Fatalf("CreateEdge dupe forward: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("expected idempotent dupe, got new edge %s vs %s", again.ID, first.ID)
	}
	if w2.Version != w1.Version {
		t.Fatalf("dupe must not bump wall version (was %d, got %d)", w1.Version, w2.Version)
	}

	// Reverse direction is the same undirected edge.
	again2, _, err := st.CreateEdge(ctx, p.ID, b.ID, a.ID)
	if err != nil {
		t.Fatalf("CreateEdge dupe reverse: %v", err)
	}
	if again2.ID != first.ID {
		t.Fatalf("expected reverse to dedupe, got %s vs %s", again2.ID, first.ID)
	}
}

func TestWallDeleteEdgeAndCascadeOnNoteDelete(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	a, _, _ := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	b, _, _ := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	c, _, _ := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	eAB, _, err := st.CreateEdge(ctx, p.ID, a.ID, b.ID)
	if err != nil {
		t.Fatalf("CreateEdge AB: %v", err)
	}
	if _, _, err := st.CreateEdge(ctx, p.ID, b.ID, c.ID); err != nil {
		t.Fatalf("CreateEdge BC: %v", err)
	}

	w, err := st.DeleteEdge(ctx, p.ID, eAB.ID)
	if err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}
	if len(w.Edges) != 1 {
		t.Fatalf("expected 1 edge remaining, got %d", len(w.Edges))
	}
	if _, err := st.DeleteEdge(ctx, p.ID, eAB.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on second DeleteEdge, got %v", err)
	}

	// Deleting note B must cascade and remove the BC edge.
	w2, err := st.DeleteNote(ctx, p.ID, b.ID)
	if err != nil {
		t.Fatalf("DeleteNote b: %v", err)
	}
	if len(w2.Edges) != 0 {
		t.Fatalf("expected dependent edges removed, got %d", len(w2.Edges))
	}
}

func TestWallRejectsInvalidColor(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	_, _, err = st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "not-a-color"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}
