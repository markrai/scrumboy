package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"scrumboy/internal/drawrefs"
	"scrumboy/internal/version"
)

func notesInkDoc(strokes int, color string) json.RawMessage {
	var parts []string
	for i := 0; i < strokes; i++ {
		parts = append(parts, fmt.Sprintf(`{"id":"i%d","tool":"pen","color":"%s","size":3,"opacity":1,"points":[[12,40,0.5],[300,900,0.5]],"createdAt":%d}`, i, color, i+1))
	}
	return json.RawMessage(`{"version":1,"width":640,"height":1200,"strokes":[` + strings.Join(parts, ",") + `]}`)
}

func setInk(raw json.RawMessage) NotesInkChange { return NotesInkChange{Present: true, Data: raw} }

func inkRow(t *testing.T, st *Store, todoID int64) (string, int64, bool) {
	t.Helper()
	var data string
	var v int64
	err := st.db.QueryRow(`SELECT data, version FROM todo_notes_ink WHERE todo_id = ?`, todoID).Scan(&data, &v)
	if err != nil {
		return "", 0, false
	}
	return data, v, true
}

func keepUpdate(title, body string) UpdateTodoInput {
	return UpdateTodoInput{Title: title, Body: body, KeepTags: true, KeepEstimationPoints: true, KeepAssigneeUserID: true}
}

func TestNotesInkCreateWithoutMarkdownReferenceAndReload(t *testing.T) {
	st, _, ctx, p := setupDrawingStore(t)
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: "", ColumnKey: DefaultColumnBacklog, NotesInk: setInk(notesInkDoc(2, "#e11d48"))}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, v, ok := inkRow(t, st, todo.ID); !ok || v != 1 {
		t.Fatalf("ink row missing (v=%d)", v)
	}
	raw, err := st.GetTodoNotesInkByLocalID(ctx, p.ID, todo.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	var doc DrawingDocument
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Strokes) != 2 || doc.Height != 1200 {
		t.Fatalf("doc=%+v err=%v", doc, err)
	}
}

func TestNotesInkSurvivesUnrelatedEditsAndRetriesAreIdempotent(t *testing.T) {
	st, _, ctx, p := setupDrawingStore(t)
	ink := notesInkDoc(1, "#112233")
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: "text", ColumnKey: DefaultColumnBacklog, NotesInk: setInk(ink)}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateTodo(ctx, todo.ID, keepUpdate("Renamed", "different text entirely"), ModeFull); err != nil {
		t.Fatal(err)
	}
	if _, v, ok := inkRow(t, st, todo.ID); !ok || v != 1 {
		t.Fatal("unrelated edit changed or erased ink")
	}
	retry := keepUpdate("Renamed", "different text entirely")
	retry.NotesInk = setInk(ink)
	for i := 0; i < 2; i++ {
		if _, err := st.UpdateTodo(ctx, todo.ID, retry, ModeFull); err != nil {
			t.Fatal(err)
		}
	}
	if _, v, _ := inkRow(t, st, todo.ID); v != 1 {
		t.Fatalf("identical retry bumped version to %d", v)
	}
	changed := keepUpdate("Renamed", "different text entirely")
	changed.NotesInk = setInk(notesInkDoc(3, "#112233"))
	if _, err := st.UpdateTodo(ctx, todo.ID, changed, ModeFull); err != nil {
		t.Fatal(err)
	}
	if _, v, _ := inkRow(t, st, todo.ID); v != 2 {
		t.Fatalf("changed ink version=%d", v)
	}
	clear := keepUpdate("Renamed", "different text entirely")
	clear.NotesInk = NotesInkChange{Present: true, Data: json.RawMessage("null")}
	if _, err := st.UpdateTodo(ctx, todo.ID, clear, ModeFull); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := inkRow(t, st, todo.ID); ok {
		t.Fatal("explicit clear kept ink")
	}
}

func TestNotesInkFailedSaveLeavesBodyAndInkUnchanged(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	ink := notesInkDoc(1, "#112233")
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: "original", ColumnKey: DefaultColumnBacklog, NotesInk: setInk(ink)}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	before, _, _ := inkRow(t, st, todo.ID)
	bad := keepUpdate("T", "edited")
	bad.NotesInk = setInk(json.RawMessage(`{"version":1,"width":640,"height":1200,"strokes":[{"id":"x","tool":"pen","color":"url(#x)","size":3,"opacity":1,"points":[[1,1,0.5]],"createdAt":1}]}`))
	_, err = st.UpdateTodo(ctx, todo.ID, bad, ModeFull)
	requireReason(t, err, ReasonInvalidDrawing)
	unresolved := keepUpdate("T", annotated(drawrefs.NewID()))
	unresolved.NotesInk = setInk(notesInkDoc(4, "#000000"))
	_, err = st.UpdateTodo(ctx, todo.ID, unresolved, ModeFull)
	requireReason(t, err, ReasonUnresolvedDrawingReference)
	var body string
	if err := db.QueryRow(`SELECT body FROM todos WHERE id = ?`, todo.ID).Scan(&body); err != nil || body != "original" {
		t.Fatalf("body=%q", body)
	}
	if after, _, _ := inkRow(t, st, todo.ID); after != before {
		t.Fatal("failed save changed ink")
	}
}

func TestNotesInkBoundsAndDisabledFlag(t *testing.T) {
	st, _, ctx, p := setupDrawingStore(t)
	_, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", ColumnKey: DefaultColumnBacklog, NotesInk: setInk(notesInkDoc(MaxNotesInkStrokes+1, "#112233"))}, ModeFull)
	requireReason(t, err, ReasonDrawingLimit)
	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", ColumnKey: DefaultColumnBacklog, NotesInk: setInk(json.RawMessage(`{"version":1,"width":640,"height":100,"strokes":[{"id":"x","tool":"pen","color":"#112233","size":3,"opacity":1,"points":[[1,500,0.5]],"createdAt":1}]}`))}, ModeFull)
	requireReason(t, err, ReasonInvalidDrawing)
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", ColumnKey: DefaultColumnBacklog, NotesInk: setInk(notesInkDoc(1, "#112233"))}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	st.drawingsDisabled = true
	defer func() { st.drawingsDisabled = false }()
	if _, err := st.UpdateTodo(ctx, todo.ID, keepUpdate("T2", "x"), ModeFull); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := inkRow(t, st, todo.ID); !ok {
		t.Fatal("disabled flag removed ink")
	}
	clear := keepUpdate("T2", "x")
	clear.NotesInk = NotesInkChange{Present: true, Data: json.RawMessage("null")}
	_, err = st.UpdateTodo(ctx, todo.ID, clear, ModeFull)
	requireReason(t, err, ReasonDrawingsDisabled)
	if _, _, ok := inkRow(t, st, todo.ID); !ok {
		t.Fatal("rejected clear removed ink")
	}
}

func TestNotesInkIsIndependentOfReferenceDrawingsAndCascades(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	ref := drawrefs.NewID()
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: annotated(ref), ColumnKey: DefaultColumnBacklog, Drawings: map[string]json.RawMessage{ref: testDrawingDoc(1, "#112233")}, NotesInk: setInk(notesInkDoc(1, "#112233"))}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateTodo(ctx, todo.ID, keepUpdate("T", "image removed"), ModeFull); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := drawingRow(t, db, ref); ok {
		t.Fatal("reference drawing not pruned")
	}
	if _, _, ok := inkRow(t, st, todo.ID); !ok {
		t.Fatal("reference pruning removed notes ink")
	}
	if err := st.DeleteTodo(ctx, todo.ID, ModeFull); err != nil {
		t.Fatal(err)
	}
	if countRows(t, db, `SELECT COUNT(*) FROM todo_notes_ink`) != 0 {
		t.Fatal("ink survived story deletion")
	}
}

func TestNotesInkReadsAreScopedAndPublicBoundaryHolds(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", ColumnKey: DefaultColumnBacklog, NotesInk: setInk(notesInkDoc(1, "#112233"))}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateProject(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetTodoNotesInkByLocalID(ctx, other.ID, todo.LocalID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project read err=%v", err)
	}
	if _, err := st.GetPublicTodoNotesInk(ctx, p.ID, p.Slug, todo.LocalID); err == nil {
		t.Fatal("non-public project served ink")
	}
	if _, err := db.Exec(`UPDATE projects SET public_view_enabled = 1 WHERE id = ?`, p.ID); err != nil {
		t.Fatal(err)
	}
	if raw, err := st.GetPublicTodoNotesInk(ctx, p.ID, p.Slug, todo.LocalID); err != nil || len(raw) == 0 {
		t.Fatalf("public read raw=%s err=%v", raw, err)
	}
	if _, err := st.ArchiveTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetPublicTodoNotesInk(ctx, p.ID, p.Slug, todo.LocalID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archived story ink served publicly: %v", err)
	}
}

func TestNotesInkBackupRoundTripAndVersionGates(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Inked", Body: "plain notes", ColumnKey: DefaultColumnBacklog, NotesInk: setInk(notesInkDoc(2, "#112233"))}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	data := exportArchive(t, st, ctx)
	if !ExportHasDrawings(data) || !hasNotesInk(data.Projects[0].Todos[0]) {
		t.Fatal("export omitted notes ink")
	}
	if _, err := st.ImportProjectsWithAssets(ctx, data, ModeFull, "copy", &ImportAssetPlan{}); err != nil {
		t.Fatal(err)
	}
	if countRows(t, db, `SELECT COUNT(*) FROM todo_notes_ink`) != 2 {
		t.Fatal("copy did not restore ink")
	}
	var copyID int64
	if err := db.QueryRow(`SELECT id FROM todos WHERE title = 'Inked' AND id != ?`, todo.ID).Scan(&copyID); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := inkRow(t, st, copyID); !ok {
		t.Fatal("ink not bound to the imported story")
	}
	for i := 0; i < 2; i++ {
		if _, err := st.ImportProjectsWithAssets(ctx, data, ModeFull, "merge", &ImportAssetPlan{}); err != nil {
			t.Fatal(err)
		}
	}
	if countRows(t, db, `SELECT COUNT(*) FROM todo_notes_ink`) != 2 {
		t.Fatal("merge duplicated or dropped ink")
	}
	inkFree := exportArchive(t, st, ctx)
	for pi := range inkFree.Projects {
		for i := range inkFree.Projects[pi].Todos {
			inkFree.Projects[pi].Todos[i].NotesInk = nil
			inkFree.Projects[pi].Todos[i].NotesInkPresent = false
		}
	}
	original, _, _ := inkRow(t, st, todo.ID)
	if _, err := st.ImportProjectsWithAssets(ctx, inkFree, ModeFull, "merge", &ImportAssetPlan{}); err != nil {
		t.Fatal(err)
	}
	if kept, _, ok := inkRow(t, st, todo.ID); !ok || kept != original {
		t.Fatal("merging an archive without notes ink erased the story's ink")
	}
	recolored := exportArchive(t, st, ctx)
	for pi := range recolored.Projects {
		for i := range recolored.Projects[pi].Todos {
			if recolored.Projects[pi].Slug == p.Slug && recolored.Projects[pi].Todos[i].LocalID == todo.LocalID {
				recolored.Projects[pi].Todos[i].NotesInk = notesInkDoc(1, "#445566")
			}
		}
	}
	if _, err := st.ImportProjectsWithAssets(ctx, recolored, ModeFull, "merge", &ImportAssetPlan{}); err != nil {
		t.Fatal(err)
	}
	if replaced, _, _ := inkRow(t, st, todo.ID); !strings.Contains(replaced, "#445566") || strings.Contains(replaced, "#112233") {
		t.Fatalf("merge with archive ink did not replace it: %s", replaced)
	}
	legacy := exportArchive(t, st, ctx)
	legacy.Version = version.LegacyArchiveExportFormatVersion
	if _, err := st.ImportProjectsWithAssets(ctx, legacy, ModeFull, "copy", &ImportAssetPlan{}); err == nil {
		t.Fatal("1.3 archive with notes ink accepted")
	}
	jsonData := exportArchive(t, st, ctx)
	jsonData.Version = version.ExportFormatVersion
	if _, err := st.ImportProjects(ctx, jsonData, ModeFull, "copy"); err == nil {
		t.Fatal("JSON import with notes ink accepted")
	}
	corrupt := exportArchive(t, st, ctx)
	corrupt.Projects[0].Todos[0].NotesInk = json.RawMessage(`{"version":1,"width":10,"height":10,"strokes":[{"id":"x","tool":"pen","color":"#112233","size":3,"opacity":1,"points":[[50,50,0.5]],"createdAt":1}]}`)
	before := countRows(t, db, `SELECT COUNT(*) FROM todos`)
	if _, err := st.ImportProjectsWithAssets(ctx, corrupt, ModeFull, "copy", &ImportAssetPlan{}); err == nil {
		t.Fatal("corrupt notes ink accepted")
	}
	if countRows(t, db, `SELECT COUNT(*) FROM todos`) != before {
		t.Fatal("rejected import wrote stories")
	}
}
