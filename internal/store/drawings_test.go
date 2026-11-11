package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"scrumboy/internal/drawrefs"
	"scrumboy/internal/version"
)

const testAssetPath = "/api/assets/a8b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d"

func testDrawingDoc(strokes int, color string) json.RawMessage {
	var parts []string
	for i := 0; i < strokes; i++ {
		parts = append(parts, fmt.Sprintf(`{"id":"s%d","tool":"pen","color":"%s","size":4,"opacity":1,"points":[[10,10,0.5],[20,30,0.5]],"createdAt":%d}`, i, color, 1000+i))
	}
	return json.RawMessage(`{"version":1,"width":400,"height":300,"strokes":[` + strings.Join(parts, ",") + `]}`)
}

func annotated(id string) string { return "![shot|draw:" + id + "|480](" + testAssetPath + ")" }

func standalone(slug, id string) string {
	return "![sketch|draw:" + id + "|640](/api/board/" + slug + "/drawings/" + id + ")"
}

func drawingRow(t *testing.T, db *sql.DB, id string) (todoID int64, version int64, data string, ok bool) {
	t.Helper()
	err := db.QueryRow(`SELECT todo_id, version, data FROM todo_drawings WHERE id = ?`, id).Scan(&todoID, &version, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return todoID, version, data, true
}

func requireReason(t *testing.T, err error, reason string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error, got nil", reason)
	}
	if got := ErrorReason(err); got != reason {
		t.Fatalf("reason=%q want %q (err=%v)", got, reason, err)
	}
}

func setupDrawingStore(t *testing.T) (*Store, *sql.DB, context.Context, Project) {
	t.Helper()
	return setupAssetOwnedProject(t)
}

func TestDrawingCreateWithAnnotationPersistsAtomically(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	id := drawrefs.NewID()
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: annotated(id), ColumnKey: DefaultColumnBacklog, Drawings: map[string]json.RawMessage{id: testDrawingDoc(2, "#e11d48")}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	owner, version, _, ok := drawingRow(t, db, id)
	if !ok || owner != todo.ID || version != 1 {
		t.Fatalf("row owner=%d version=%d ok=%v", owner, version, ok)
	}
	got, err := st.ListTodoDrawingsByLocalID(ctx, p.ID, todo.LocalID)
	if err != nil || len(got) != 1 {
		t.Fatalf("list=%v err=%v", got, err)
	}
	var doc DrawingDocument
	if err := json.Unmarshal(got[id], &doc); err != nil || len(doc.Strokes) != 2 || doc.Width != 400 {
		t.Fatalf("doc=%+v err=%v", doc, err)
	}
}

func TestDrawingDeltaSaveKeepsUnchangedAndByteIdenticalRetryIsNoop(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	id := drawrefs.NewID()
	doc := testDrawingDoc(1, "#112233")
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: annotated(id), ColumnKey: DefaultColumnBacklog, Drawings: map[string]json.RawMessage{id: doc}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{Title: "T2", Body: annotated(id) + "\n\nmore", KeepTags: true, KeepEstimationPoints: true, KeepAssigneeUserID: true}, ModeFull); err != nil {
		t.Fatal(err)
	}
	if _, v, _, ok := drawingRow(t, db, id); !ok || v != 1 {
		t.Fatalf("unchanged drawing touched: v=%d ok=%v", v, ok)
	}
	for i := 0; i < 2; i++ {
		if _, err := st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{Title: "T2", Body: annotated(id), KeepTags: true, KeepEstimationPoints: true, KeepAssigneeUserID: true, Drawings: map[string]json.RawMessage{id: doc}}, ModeFull); err != nil {
			t.Fatal(err)
		}
	}
	if _, v, _, _ := drawingRow(t, db, id); v != 1 {
		t.Fatalf("identical retry bumped version to %d", v)
	}
	if _, err := st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{Title: "T2", Body: annotated(id), KeepTags: true, KeepEstimationPoints: true, KeepAssigneeUserID: true, Drawings: map[string]json.RawMessage{id: testDrawingDoc(3, "#112233")}}, ModeFull); err != nil {
		t.Fatal(err)
	}
	if _, v, _, _ := drawingRow(t, db, id); v != 2 {
		t.Fatalf("changed drawing version=%d", v)
	}
}

func TestDrawingRemovedReferenceIsPrunedAndUnreferencedSubmissionIgnored(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	id := drawrefs.NewID()
	stray := drawrefs.NewID()
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: annotated(id), ColumnKey: DefaultColumnBacklog, Drawings: map[string]json.RawMessage{id: testDrawingDoc(1, "#112233"), stray: testDrawingDoc(1, "#112233")}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := drawingRow(t, db, stray); ok {
		t.Fatal("unreferenced submission was stored")
	}
	if _, err := st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{Title: "T", Body: "no images", KeepTags: true, KeepEstimationPoints: true, KeepAssigneeUserID: true}, ModeFull); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := drawingRow(t, db, id); ok {
		t.Fatal("dereferenced drawing not pruned")
	}
}

func TestDrawingUnresolvedReferenceFailsWholeSave(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: "original", ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{Title: "T", Body: annotated(drawrefs.NewID()), KeepTags: true, KeepEstimationPoints: true, KeepAssigneeUserID: true}, ModeFull)
	requireReason(t, err, ReasonUnresolvedDrawingReference)
	var body string
	if err := db.QueryRow(`SELECT body FROM todos WHERE id = ?`, todo.ID).Scan(&body); err != nil || body != "original" {
		t.Fatalf("body changed after failed save: %q", body)
	}
	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "New", Body: annotated(drawrefs.NewID()), ColumnKey: DefaultColumnBacklog}, ModeFull)
	requireReason(t, err, ReasonUnresolvedDrawingReference)
	if countRows(t, db, `SELECT COUNT(*) FROM todos WHERE title = 'New'`) != 0 {
		t.Fatal("failed create left a todo")
	}
}

func TestDrawingOwnedElsewhereIsNeverOverwritten(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	id := drawrefs.NewID()
	a, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "A", Body: annotated(id), ColumnKey: DefaultColumnBacklog, Drawings: map[string]json.RawMessage{id: testDrawingDoc(1, "#112233")}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	_, _, before, _ := drawingRow(t, db, id)
	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "B", Body: annotated(id), ColumnKey: DefaultColumnBacklog, Drawings: map[string]json.RawMessage{id: testDrawingDoc(4, "#ff0000")}}, ModeFull)
	requireReason(t, err, ReasonDrawingOwnedElsewhere)
	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "C", Body: annotated(id), ColumnKey: DefaultColumnBacklog}, ModeFull)
	requireReason(t, err, ReasonUnresolvedDrawingReference)
	owner, _, after, _ := drawingRow(t, db, id)
	if owner != a.ID || after != before {
		t.Fatal("foreign save modified another story's drawing")
	}
	other, err := st.CreateProject(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ListTodoDrawingsByLocalID(ctx, other.ID, a.LocalID); err == nil {
		t.Fatal("cross-project list succeeded")
	}
	if _, err := st.GetProjectDrawing(ctx, other.ID, id, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project read err=%v", err)
	}
}

func TestDrawingValidationFailureRollsBackNoteAndDrawings(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	good := drawrefs.NewID()
	bad := drawrefs.NewID()
	_, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: annotated(good) + "\n\n" + annotated(bad), ColumnKey: DefaultColumnBacklog, Drawings: map[string]json.RawMessage{
		good: testDrawingDoc(1, "#112233"),
		bad:  json.RawMessage(`{"version":1,"width":400,"height":300,"strokes":[{"id":"x","tool":"pen","color":"javascript:alert(1)","size":4,"opacity":1,"points":[[1,1,0.5]],"createdAt":1}]}`),
	}}, ModeFull)
	requireReason(t, err, ReasonInvalidDrawing)
	if countRows(t, db, `SELECT COUNT(*) FROM todo_drawings`) != 0 || countRows(t, db, `SELECT COUNT(*) FROM todos`) != 0 {
		t.Fatal("partial write after validation failure")
	}
}

func TestDrawingBoundsAreEnforced(t *testing.T) {
	st, _, ctx, p := setupDrawingStore(t)
	id := drawrefs.NewID()
	create := func(doc json.RawMessage) error {
		_, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: annotated(id), ColumnKey: DefaultColumnBacklog, Drawings: map[string]json.RawMessage{id: doc}}, ModeFull)
		return err
	}
	requireReason(t, create(testDrawingDoc(MaxDrawingStrokes+1, "#112233")), ReasonDrawingLimit)
	requireReason(t, create(json.RawMessage(`{"version":1,"width":400,"height":300,"strokes":[{"id":"s","tool":"pen","color":"#112233","size":4,"opacity":1,"points":[[500,10,0.5]],"createdAt":1}]}`)), ReasonInvalidDrawing)
	requireReason(t, create(json.RawMessage(`{"version":1,"width":400,"height":300,"strokes":[{"id":"s","tool":"pen","color":"#112233","size":4,"opacity":1,"points":[[1,1,0.5,9]],"createdAt":1}]}`)), ReasonInvalidDrawing)
	requireReason(t, create(json.RawMessage(`{"version":1,"width":400,"height":300,"strokes":[],"extra":1}`)), ReasonInvalidDrawing)
	var pts []string
	for i := 0; i < MaxDrawingStrokePoints+1; i++ {
		pts = append(pts, "[1,1,0.5]")
	}
	requireReason(t, create(json.RawMessage(`{"version":1,"width":400,"height":300,"strokes":[{"id":"s","tool":"pen","color":"#112233","size":4,"opacity":1,"points":[`+strings.Join(pts, ",")+`],"createdAt":1}]}`)), ReasonInvalidDrawing)
	big := make(map[string]json.RawMessage)
	body := ""
	for i := 0; i < 4; i++ {
		did := drawrefs.NewID()
		body += annotated(did) + "\n\n"
		big[did] = json.RawMessage(strings.Repeat(" ", 200*1024) + `{}`)
	}
	_, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: body, ColumnKey: DefaultColumnBacklog, Drawings: big}, ModeFull)
	requireReason(t, err, ReasonDrawingTooLarge)
}

func TestDrawingCodeSpansNeverCountAsReferences(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	id := drawrefs.NewID()
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: "`" + annotated(id) + "`\n\n```\n" + annotated(id) + "\n```", ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
		t.Fatalf("code spans should not need drawings: %v", err)
	}
	if countRows(t, db, `SELECT COUNT(*) FROM todo_drawings`) != 0 {
		t.Fatal("unexpected rows")
	}
}

func TestDrawingDisabledStorePreservesRowsAndRejectsWrites(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	id := drawrefs.NewID()
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: annotated(id), ColumnKey: DefaultColumnBacklog, Drawings: map[string]json.RawMessage{id: testDrawingDoc(1, "#112233")}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	st.drawingsDisabled = true
	defer func() { st.drawingsDisabled = false }()
	if _, err := st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{Title: "T", Body: "image removed", KeepTags: true, KeepEstimationPoints: true, KeepAssigneeUserID: true}, ModeFull); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := drawingRow(t, db, id); !ok {
		t.Fatal("disabled flag pruned a drawing")
	}
	_, err = st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{Title: "T", Body: annotated(id), KeepTags: true, KeepEstimationPoints: true, KeepAssigneeUserID: true, Drawings: map[string]json.RawMessage{id: testDrawingDoc(2, "#112233")}}, ModeFull)
	requireReason(t, err, ReasonDrawingsDisabled)
	if _, err := st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{Title: "T", Body: annotated(id), KeepTags: true, KeepEstimationPoints: true, KeepAssigneeUserID: true}, ModeFull); err != nil {
		t.Fatalf("restoring the reference while disabled: %v", err)
	}
}

func TestDrawingStandaloneReadsRequireLiveReference(t *testing.T) {
	st, _, ctx, p := setupDrawingStore(t)
	id := drawrefs.NewID()
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: standalone(p.Slug, id), ColumnKey: DefaultColumnBacklog, Drawings: map[string]json.RawMessage{id: testDrawingDoc(1, "#112233")}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetProjectDrawing(ctx, p.ID, id, true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetProjectDrawing(ctx, p.ID, "../etc", true); !errors.Is(err, ErrNotFound) {
		t.Fatal("malformed id served")
	}
	if _, err := st.ArchiveTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull); err == nil {
		if _, err := st.GetProjectDrawing(ctx, p.ID, id, false); !errors.Is(err, ErrNotFound) {
			t.Fatalf("archived story drawing served publicly: %v", err)
		}
	}
	if _, err := st.ListPublicTodoDrawings(ctx, p.ID, p.Slug, todo.LocalID); err == nil {
		t.Fatal("non-public project served drawings")
	}
}

func TestDrawingRowsCascadeWithStoryDeletion(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	id := drawrefs.NewID()
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: annotated(id), ColumnKey: DefaultColumnBacklog, Drawings: map[string]json.RawMessage{id: testDrawingDoc(1, "#112233")}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTodo(ctx, todo.ID, ModeFull); err != nil {
		t.Fatal(err)
	}
	if countRows(t, db, `SELECT COUNT(*) FROM todo_drawings`) != 0 {
		t.Fatal("drawing survived story deletion")
	}
}

func exportArchive(t *testing.T, st *Store, ctx context.Context) *ExportData {
	t.Helper()
	data, err := st.ExportAllProjects(ctx, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	data.Version = version.ArchiveExportFormatVersion
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var round ExportData
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	return &round
}

func TestDrawingBackupExportCarriesRecordsAndCopyImportUsesFreshIdentities(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	a := drawrefs.NewID()
	b := drawrefs.NewID()
	body := annotated(a) + "\n\n" + annotated(a) + "\n\n" + standalone(p.Slug, b) + "\n\n`draw:" + a + "`"
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: body, ColumnKey: DefaultColumnBacklog, Drawings: map[string]json.RawMessage{a: testDrawingDoc(1, "#112233"), b: testDrawingDoc(2, "#445566")}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	data := exportArchive(t, st, ctx)
	if !ExportHasDrawings(data) || len(data.Projects[0].Todos[0].Drawings) != 2 {
		t.Fatalf("export drawings=%+v", data.Projects[0].Todos[0].Drawings)
	}
	if _, err := st.ImportProjectsWithAssets(ctx, data, ModeFull, "copy", &ImportAssetPlan{}); err != nil {
		t.Fatal(err)
	}
	if countRows(t, db, `SELECT COUNT(*) FROM todo_drawings`) != 4 {
		t.Fatalf("rows=%d", countRows(t, db, `SELECT COUNT(*) FROM todo_drawings`))
	}
	var copyBody, copySlug string
	var copyTodoID, copyProjectID int64
	if err := db.QueryRow(`SELECT t.id, t.body, p.slug, p.id FROM todos t JOIN projects p ON p.id = t.project_id WHERE t.id != ?`, todo.ID).Scan(&copyTodoID, &copyBody, &copySlug, &copyProjectID); err != nil {
		t.Fatal(err)
	}
	refs := drawrefs.Extract(copyBody)
	if len(refs) != 2 || refs[0] == a || refs[1] == b {
		t.Fatalf("copy refs=%v", refs)
	}
	if !strings.Contains(copyBody, "`draw:"+a+"`") {
		t.Fatal("code span text was rewritten")
	}
	if !strings.Contains(copyBody, "/api/board/"+copySlug+"/drawings/"+refs[1]) {
		t.Fatalf("standalone destination not rewritten to destination slug: %s", copyBody)
	}
	for _, id := range refs {
		owner, _, _, ok := drawingRow(t, db, id)
		if !ok || owner != copyTodoID {
			t.Fatalf("copy drawing %s owner=%d ok=%v", id, owner, ok)
		}
	}
	if _, err := st.UpdateTodo(ctx, copyTodoID, UpdateTodoInput{Title: "T", Body: copyBody, KeepTags: true, KeepEstimationPoints: true, KeepAssigneeUserID: true, Drawings: map[string]json.RawMessage{refs[0]: testDrawingDoc(5, "#000000")}}, ModeFull); err != nil {
		t.Fatal(err)
	}
	if _, v, _, _ := drawingRow(t, db, a); v != 1 {
		t.Fatal("editing the copy changed the source drawing")
	}
}

func TestDrawingMergeImportTwiceConvergesWithoutOrphans(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	a := drawrefs.NewID()
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: annotated(a), ColumnKey: DefaultColumnBacklog, Drawings: map[string]json.RawMessage{a: testDrawingDoc(1, "#112233")}}, ModeFull); err != nil {
		t.Fatal(err)
	}
	data := exportArchive(t, st, ctx)
	for i := 0; i < 2; i++ {
		if _, err := st.ImportProjectsWithAssets(ctx, data, ModeFull, "merge", &ImportAssetPlan{}); err != nil {
			t.Fatal(err)
		}
		if n := countRows(t, db, `SELECT COUNT(*) FROM todo_drawings`); n != 1 {
			t.Fatalf("merge %d rows=%d", i, n)
		}
	}
	var body string
	var todoID int64
	if err := db.QueryRow(`SELECT id, body FROM todos`).Scan(&todoID, &body); err != nil {
		t.Fatal(err)
	}
	refs := drawrefs.Extract(body)
	if len(refs) != 1 || refs[0] == a {
		t.Fatalf("merge kept archive id: %v", refs)
	}
	if owner, _, _, ok := drawingRow(t, db, refs[0]); !ok || owner != todoID {
		t.Fatal("merged reference has no owned row")
	}
}

func TestDrawingReplaceImportOverExistingIDs(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	a := drawrefs.NewID()
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: annotated(a), ColumnKey: DefaultColumnBacklog, Drawings: map[string]json.RawMessage{a: testDrawingDoc(1, "#112233")}}, ModeFull); err != nil {
		t.Fatal(err)
	}
	data := exportArchive(t, st, ctx)
	if _, err := st.ImportProjectsWithAssets(ctx, data, ModeFull, "replace", &ImportAssetPlan{}); err != nil {
		t.Fatal(err)
	}
	if countRows(t, db, `SELECT COUNT(*) FROM todo_drawings WHERE id = ?`, a) != 0 || countRows(t, db, `SELECT COUNT(*) FROM todo_drawings`) != 1 {
		t.Fatal("replace did not restore under a fresh id")
	}
}

func TestDrawingImportRejectsMissingConflictingAndMislabeledRecords(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	a := drawrefs.NewID()
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: annotated(a), ColumnKey: DefaultColumnBacklog, Drawings: map[string]json.RawMessage{a: testDrawingDoc(1, "#112233")}}, ModeFull); err != nil {
		t.Fatal(err)
	}
	before := countRows(t, db, `SELECT COUNT(*) FROM todos`)

	missing := exportArchive(t, st, ctx)
	missing.Projects[0].Todos[0].Drawings = nil
	_, err := st.ImportProjectsWithAssets(ctx, missing, ModeFull, "copy", &ImportAssetPlan{})
	requireReason(t, err, ReasonUnresolvedDrawingReference)

	conflicting := exportArchive(t, st, ctx)
	rec := conflicting.Projects[0].Todos[0].Drawings[0]
	conflicting.Projects[0].Todos[0].Drawings = append(conflicting.Projects[0].Todos[0].Drawings, TodoDrawingExport{ID: rec.ID, Data: testDrawingDoc(3, "#000000")})
	_, err = st.ImportProjectsWithAssets(ctx, conflicting, ModeFull, "copy", &ImportAssetPlan{})
	requireReason(t, err, ReasonConflictingDrawingRecord)

	legacy := exportArchive(t, st, ctx)
	legacy.Version = version.LegacyArchiveExportFormatVersion
	if _, err := st.ImportProjectsWithAssets(ctx, legacy, ModeFull, "copy", &ImportAssetPlan{}); err == nil || !strings.Contains(err.Error(), "drawings require") {
		t.Fatalf("1.3 archive with drawings err=%v", err)
	}

	jsonData := exportArchive(t, st, ctx)
	jsonData.Version = version.ExportFormatVersion
	if _, err := st.ImportProjects(ctx, jsonData, ModeFull, "copy"); err == nil {
		t.Fatal("JSON import with drawings accepted")
	}
	jsonData.Projects[0].Todos[0].Drawings = nil
	jsonData.Projects[0].Todos[0].DrawingsPresent = false
	_, err = st.ImportProjects(ctx, jsonData, ModeFull, "copy")
	requireReason(t, err, ReasonUnresolvedDrawingReference)

	if countRows(t, db, `SELECT COUNT(*) FROM todos`) != before || countRows(t, db, `SELECT COUNT(*) FROM todo_drawings`) != 1 {
		t.Fatal("rejected import wrote data")
	}
}

func TestDrawingLegacyArchiveWithoutDrawingsStillImports(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Plain", Body: "no drawings", ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
		t.Fatal(err)
	}
	data := exportArchive(t, st, ctx)
	data.Version = version.LegacyArchiveExportFormatVersion
	if _, err := st.ImportProjectsWithAssets(ctx, data, ModeFull, "copy", &ImportAssetPlan{}); err != nil {
		t.Fatal(err)
	}
	if countRows(t, db, `SELECT COUNT(*) FROM todos WHERE title = 'Plain'`) != 2 || countRows(t, db, `SELECT COUNT(*) FROM todo_drawings`) != 0 {
		t.Fatal("legacy archive import changed")
	}
}

func TestDrawingExportFailsClosedOnDanglingReference(t *testing.T) {
	st, db, ctx, p := setupDrawingStore(t)
	a := drawrefs.NewID()
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "T", Body: annotated(a), ColumnKey: DefaultColumnBacklog, Drawings: map[string]json.RawMessage{a: testDrawingDoc(1, "#112233")}}, ModeFull); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM todo_drawings`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ExportAllProjects(ctx, ModeFull); err == nil {
		t.Fatal("export silently omitted a referenced drawing")
	}
}
