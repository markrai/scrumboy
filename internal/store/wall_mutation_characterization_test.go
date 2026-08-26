package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func mustWallCharacterizationProject(t *testing.T, st *Store) Project {
	t.Helper()
	project, err := st.CreateProject(context.Background(), "Wall Mutation Characterization")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return project
}

func mustWallCharacterizationNote(t *testing.T, st *Store, projectID int64, text string) WallNote {
	t.Helper()
	note, _, err := st.CreateNote(context.Background(), projectID, CreateNoteInput{
		X: 10, Y: 20, Width: 180, Height: 140, Color: "#ffd966", Text: text,
	})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	return note
}

func TestWallMutationStoreCharacterizationReplaceRegeneratesNotesAndPreservesDanglingEdges(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := mustWallCharacterizationProject(t, st)
	a := mustWallCharacterizationNote(t, st, project.ID, "old a")
	b := mustWallCharacterizationNote(t, st, project.ID, "old b")
	edge, before, err := st.CreateEdge(ctx, project.ID, a.ID, b.ID)
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}

	replaced, err := st.ReplaceWall(ctx, project.ID, []WallNote{{
		X: -200000, Y: 200000, Width: 0, Height: 10,
		Color: "  #abcdef  ", Text: "replacement",
	}})
	if err != nil {
		t.Fatalf("ReplaceWall: %v", err)
	}
	if replaced.Version != before.Version+1 || len(replaced.Notes) != 1 || len(replaced.Edges) != 1 {
		t.Fatalf("before=%+v replaced=%+v", before, replaced)
	}
	note := replaced.Notes[0]
	if note.ID == "" || note.ID == a.ID || note.ID == b.ID || note.Version != 1 {
		t.Fatalf("replacement note identity/version=%+v old=%q,%q", note, a.ID, b.ID)
	}
	if note.X != -maxNoteCoordinate || note.Y != maxNoteCoordinate || note.Width != defaultNoteWidth || note.Height != minNoteDimension {
		t.Fatalf("replacement normalization=%+v", note)
	}
	if note.Color != "  #abcdef  " {
		t.Fatalf("replacement color=%q want accepted whitespace preserved", note.Color)
	}
	if replaced.Edges[0] != edge || replaced.Edges[0].From != a.ID || replaced.Edges[0].To != b.ID {
		t.Fatalf("replacement edge=%+v want retained dangling edge=%+v", replaced.Edges, edge)
	}

	reloaded, err := st.GetWall(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Notes) != 1 || reloaded.Notes[0] != note || len(reloaded.Edges) != 1 || reloaded.Edges[0] != edge {
		t.Fatalf("reloaded replacement=%+v want=%+v", reloaded, replaced)
	}
}

func TestWallMutationStoreCharacterizationEmptyPatchIsVersionedWrite(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := mustWallCharacterizationProject(t, st)
	note := mustWallCharacterizationNote(t, st, project.ID, "unchanged")
	if _, err := st.db.ExecContext(ctx, `UPDATE project_walls SET updated_at = 1 WHERE project_id = ?`, project.ID); err != nil {
		t.Fatalf("set timestamp sentinel: %v", err)
	}
	before, err := st.GetWall(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.UpdatedAt != 1 {
		t.Fatalf("before updatedAt=%d want sentinel 1", before.UpdatedAt)
	}

	patched, returned, err := st.PatchNote(ctx, project.ID, note.ID, PatchNoteInput{IfVersion: note.Version})
	if err != nil {
		t.Fatalf("empty PatchNote: %v", err)
	}
	if patched.Text != note.Text || patched.Color != note.Color || patched.Version != note.Version+1 {
		t.Fatalf("patched note=%+v before=%+v", patched, note)
	}
	if returned.Version != before.Version+1 || returned.UpdatedAt <= before.UpdatedAt {
		t.Fatalf("returned wall=%+v before=%+v", returned, before)
	}
	reloaded, err := st.GetWall(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Version != before.Version+1 || reloaded.UpdatedAt <= before.UpdatedAt || reloaded.Notes[0].Version != note.Version+1 {
		t.Fatalf("reloaded wall=%+v before=%+v", reloaded, before)
	}
}

func TestWallMutationStoreCharacterizationValidationPrecedesTargetLookup(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := mustWallCharacterizationProject(t, st)
	note := mustWallCharacterizationNote(t, st, project.ID, "target")

	badColor := "not-a-color"
	if _, _, err := st.PatchNote(ctx, project.ID, "missing", PatchNoteInput{Color: &badColor}); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid patch before missing target error=%v want ErrValidation", err)
	}
	validColor := "#abcdef"
	if _, _, err := st.PatchNote(ctx, project.ID, "missing", PatchNoteInput{Color: &validColor}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("valid patch missing target error=%v want ErrNotFound", err)
	}
	if _, _, err := st.CreateEdge(ctx, project.ID, " ", "missing"); !errors.Is(err, ErrValidation) {
		t.Fatalf("blank endpoint error=%v want ErrValidation", err)
	}
	if _, _, err := st.CreateEdge(ctx, project.ID, note.ID, note.ID); !errors.Is(err, ErrValidation) {
		t.Fatalf("self edge error=%v want ErrValidation", err)
	}
	if _, _, err := st.CreateEdge(ctx, project.ID, note.ID, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing endpoint error=%v want ErrNotFound", err)
	}
}

func TestWallMutationStoreCharacterizationReplacementLimitPrecedesNoteValidationAndWrite(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := mustWallCharacterizationProject(t, st)
	mustWallCharacterizationNote(t, st, project.ID, "sentinel")
	var notesJSONBefore, edgesJSONBefore string
	var versionBefore, updatedAtBefore int64
	if err := st.db.QueryRowContext(ctx, `SELECT notes, edges, version, updated_at FROM project_walls WHERE project_id = ?`, project.ID).
		Scan(&notesJSONBefore, &edgesJSONBefore, &versionBefore, &updatedAtBefore); err != nil {
		t.Fatal(err)
	}

	tooMany := make([]WallNote, maxWallNotes+1)
	if _, err := st.ReplaceWall(ctx, project.ID, tooMany); !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "wall note limit reached") {
		t.Fatalf("ReplaceWall over limit error=%v want wall note limit validation", err)
	}

	var notesJSONAfter, edgesJSONAfter string
	var versionAfter, updatedAtAfter int64
	if err := st.db.QueryRowContext(ctx, `SELECT notes, edges, version, updated_at FROM project_walls WHERE project_id = ?`, project.ID).
		Scan(&notesJSONAfter, &edgesJSONAfter, &versionAfter, &updatedAtAfter); err != nil {
		t.Fatal(err)
	}
	if notesJSONAfter != notesJSONBefore || edgesJSONAfter != edgesJSONBefore || versionAfter != versionBefore || updatedAtAfter != updatedAtBefore {
		t.Fatalf("over-limit replacement wrote row: before=(%q,%q,%d,%d) after=(%q,%q,%d,%d)",
			notesJSONBefore, edgesJSONBefore, versionBefore, updatedAtBefore,
			notesJSONAfter, edgesJSONAfter, versionAfter, updatedAtAfter)
	}
}

func TestWallMutationStoreCharacterizationSQLWriteFailureLeavesPersistedWallUnchanged(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := mustWallCharacterizationProject(t, st)
	note := mustWallCharacterizationNote(t, st, project.ID, "before forced failure")
	var notesJSONBefore, edgesJSONBefore string
	var versionBefore, updatedAtBefore int64
	if err := st.db.QueryRowContext(ctx, `SELECT notes, edges, version, updated_at FROM project_walls WHERE project_id = ?`, project.ID).
		Scan(&notesJSONBefore, &edgesJSONBefore, &versionBefore, &updatedAtBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `
CREATE TRIGGER phase26_reject_wall_update
BEFORE UPDATE ON project_walls
BEGIN
  SELECT RAISE(ABORT, 'forced wall write failure');
END`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	defer func() {
		if _, err := st.db.ExecContext(context.Background(), `DROP TRIGGER phase26_reject_wall_update`); err != nil {
			t.Errorf("drop failure trigger: %v", err)
		}
	}()

	text := "must not persist"
	if _, _, err := st.PatchNote(ctx, project.ID, note.ID, PatchNoteInput{IfVersion: note.Version, Text: &text}); err == nil || !strings.Contains(err.Error(), "forced wall write failure") {
		t.Fatalf("PatchNote forced write error=%v", err)
	}

	var notesJSONAfter, edgesJSONAfter string
	var versionAfter, updatedAtAfter int64
	if err := st.db.QueryRowContext(ctx, `SELECT notes, edges, version, updated_at FROM project_walls WHERE project_id = ?`, project.ID).
		Scan(&notesJSONAfter, &edgesJSONAfter, &versionAfter, &updatedAtAfter); err != nil {
		t.Fatal(err)
	}
	if notesJSONAfter != notesJSONBefore || edgesJSONAfter != edgesJSONBefore || versionAfter != versionBefore || updatedAtAfter != updatedAtBefore {
		t.Fatalf("failed wall write changed row: before=(%q,%q,%d,%d) after=(%q,%q,%d,%d)",
			notesJSONBefore, edgesJSONBefore, versionBefore, updatedAtBefore,
			notesJSONAfter, edgesJSONAfter, versionAfter, updatedAtAfter)
	}
}

func TestWallMutationStoreCharacterizationDuplicateEdgePerformsNoWrite(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := mustWallCharacterizationProject(t, st)
	a := mustWallCharacterizationNote(t, st, project.ID, "a")
	b := mustWallCharacterizationNote(t, st, project.ID, "b")
	edge, before, err := st.CreateEdge(ctx, project.ID, a.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	var notesJSONBefore, edgesJSONBefore string
	var versionBefore, updatedAtBefore int64
	if err := st.db.QueryRowContext(ctx, `SELECT notes, edges, version, updated_at FROM project_walls WHERE project_id = ?`, project.ID).
		Scan(&notesJSONBefore, &edgesJSONBefore, &versionBefore, &updatedAtBefore); err != nil {
		t.Fatal(err)
	}

	duplicate, returned, err := st.CreateEdge(ctx, project.ID, "  "+b.ID+"  ", "  "+a.ID+"  ")
	if err != nil {
		t.Fatal(err)
	}
	if duplicate != edge || returned.Version != before.Version || len(returned.Edges) != 1 {
		t.Fatalf("duplicate=%+v returned=%+v before=%+v", duplicate, returned, before)
	}
	var notesJSONAfter, edgesJSONAfter string
	var versionAfter, updatedAtAfter int64
	if err := st.db.QueryRowContext(ctx, `SELECT notes, edges, version, updated_at FROM project_walls WHERE project_id = ?`, project.ID).
		Scan(&notesJSONAfter, &edgesJSONAfter, &versionAfter, &updatedAtAfter); err != nil {
		t.Fatal(err)
	}
	if notesJSONAfter != notesJSONBefore || edgesJSONAfter != edgesJSONBefore || versionAfter != versionBefore || updatedAtAfter != updatedAtBefore {
		t.Fatalf("duplicate wrote row: before=(%q,%q,%d,%d) after=(%q,%q,%d,%d)",
			notesJSONBefore, edgesJSONBefore, versionBefore, updatedAtBefore,
			notesJSONAfter, edgesJSONAfter, versionAfter, updatedAtAfter)
	}
}

func TestWallMutationStoreCharacterizationConcurrentDifferentNotePatchesSerializeWithoutConflict(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := mustWallCharacterizationProject(t, st)
	a := mustWallCharacterizationNote(t, st, project.ID, "a")
	b := mustWallCharacterizationNote(t, st, project.ID, "b")
	before, err := st.GetWall(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, patch := range []struct {
		note WallNote
		text string
	}{{a, "patched a"}, {b, "patched b"}} {
		patch := patch
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := st.PatchNote(ctx, project.ID, patch.note.ID, PatchNoteInput{
				IfVersion: patch.note.Version,
				Text:      &patch.text,
			})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent different-note patch: %v", err)
		}
	}
	after, err := st.GetWall(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != before.Version+2 || len(after.Notes) != 2 {
		t.Fatalf("before=%+v after=%+v", before, after)
	}
	texts := map[string]string{}
	versions := map[string]int64{}
	for _, note := range after.Notes {
		texts[note.ID] = note.Text
		versions[note.ID] = note.Version
	}
	if texts[a.ID] != "patched a" || texts[b.ID] != "patched b" || versions[a.ID] != a.Version+1 || versions[b.ID] != b.Version+1 {
		t.Fatalf("concurrent notes=%+v", after.Notes)
	}
}

func TestWallMutationStoreCharacterizationConcurrentSameNoteAllowsOneWinner(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := mustWallCharacterizationProject(t, st)
	note := mustWallCharacterizationNote(t, st, project.ID, "before")

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, value := range []string{"winner a", "winner b"} {
		value := value
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := st.PatchNote(ctx, project.ID, note.ID, PatchNoteInput{IfVersion: note.Version, Text: &value})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	successes, conflicts := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent same-note error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d want 1/1", successes, conflicts)
	}
	after, err := st.GetWall(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Notes) != 1 || after.Notes[0].Version != note.Version+1 || after.Version != 2 {
		t.Fatalf("after concurrent same-note patches=%+v", after)
	}
}
