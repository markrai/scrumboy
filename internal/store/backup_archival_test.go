package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func archivalBackupProject(project Project, todo TodoExport) ProjectExport {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return ProjectExport{
		Slug:      project.Slug,
		Name:      project.Name,
		CreatedAt: now,
		UpdatedAt: now,
		Todos:     []TodoExport{todo},
	}
}

func archivalBackupTodo(localID int64, title string) TodoExport {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return TodoExport{
		LocalID:   localID,
		Title:     title,
		Status:    "BACKLOG",
		Rank:      1000,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestBackupArchivalMergeVersionSemantics(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	base := context.Background()
	owner, err := st.BootstrapUser(base, "backup-archive@example.com", "password123", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithUserID(base, owner.ID)
	project, err := st.CreateProject(ctx, "Archive merge")
	if err != nil {
		t.Fatal(err)
	}
	todo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "existing"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchiveTodoByLocalID(ctx, project.ID, todo.LocalID, ModeFull); err != nil {
		t.Fatal(err)
	}

	legacyTodo := archivalBackupTodo(todo.LocalID, "legacy merge")
	legacy := &ExportData{Version: "1.1", ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full", Projects: []ProjectExport{archivalBackupProject(project, legacyTodo)}}
	if _, err := st.ImportProjects(ctx, legacy, ModeFull, "merge"); err != nil {
		t.Fatalf("1.1 merge: %v", err)
	}
	got, err := st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "legacy merge" || got.ArchivedAt == nil {
		t.Fatalf("1.1 merge did not preserve archive state: %+v", got)
	}

	clearTodo := archivalBackupTodo(todo.LocalID, "explicit null")
	clearTodo.ArchivedAtPresent = true
	clear := &ExportData{Version: "1.2", ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full", Projects: []ProjectExport{archivalBackupProject(project, clearTodo)}}
	if _, err := st.ImportProjects(ctx, clear, ModeFull, "merge"); err != nil {
		t.Fatalf("1.2 explicit null merge: %v", err)
	}
	got, err = st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if got.ArchivedAt != nil {
		t.Fatalf("explicit null did not restore target: %+v", got)
	}

	archivedMs := time.Now().UTC().Add(-time.Hour).UnixMilli()
	archiveTodo := archivalBackupTodo(todo.LocalID, "explicit timestamp")
	archiveTodo.ArchivedAtPresent = true
	archiveTodo.ArchivedAt = &archivedMs
	archive := &ExportData{Version: "1.2", ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full", Projects: []ProjectExport{archivalBackupProject(project, archiveTodo)}}
	if _, err := st.ImportProjects(ctx, archive, ModeFull, "merge"); err != nil {
		t.Fatalf("1.2 timestamp merge: %v", err)
	}
	got, err = st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if got.ArchivedAt == nil || got.ArchivedAt.UnixMilli() != archivedMs {
		t.Fatalf("timestamp merge archive state=%+v want %d", got.ArchivedAt, archivedMs)
	}
}

func TestBackupArchivalRoundTripReplaceAndCopy(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	base := context.Background()
	owner, err := st.BootstrapUser(base, "backup-archive-roundtrip@example.com", "password123", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithUserID(base, owner.ID)
	project, err := st.CreateProject(ctx, "Archive round trip")
	if err != nil {
		t.Fatal(err)
	}
	active, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "active"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "archived"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchiveTodoByLocalID(ctx, project.ID, archived.LocalID, ModeFull); err != nil {
		t.Fatal(err)
	}
	exported, err := st.ExportAllProjects(ctx, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if exported.Version != "1.2" {
		t.Fatalf("export version=%q", exported.Version)
	}
	raw, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"archivedAt":null`) {
		t.Fatalf("1.2 export does not explicitly represent both archive states: %s", raw)
	}
	var decoded ExportData
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Projects) != 1 || len(decoded.Projects[0].Todos) != 2 {
		t.Fatalf("decoded export shape=%+v", decoded.Projects)
	}
	presence := map[string]TodoExport{}
	for _, exportedTodo := range decoded.Projects[0].Todos {
		presence[exportedTodo.Title] = exportedTodo
	}
	if !presence["active"].ArchivedAtPresent || presence["active"].ArchivedAt != nil || !presence["archived"].ArchivedAtPresent || presence["archived"].ArchivedAt == nil {
		t.Fatalf("decoded archive presence=%+v", presence)
	}
	if _, err := st.ImportProjects(ctx, &decoded, ModeFull, "replace"); err != nil {
		t.Fatalf("staged/bulk replace round trip: %v", err)
	}
	restoredProject, err := st.GetProjectBySlug(ctx, project.Slug)
	if err != nil {
		t.Fatal(err)
	}
	activeAfter, err := st.GetTodoByLocalID(ctx, restoredProject.ID, active.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	archivedAfter, err := st.GetTodoByLocalID(ctx, restoredProject.ID, archived.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if activeAfter.ArchivedAt != nil || archivedAfter.ArchivedAt == nil {
		t.Fatalf("replace round trip active=%+v archived=%+v", activeAfter.ArchivedAt, archivedAfter.ArchivedAt)
	}

	copyData, err := st.ExportAllProjects(ctx, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportProjects(ctx, copyData, ModeFull, "copy"); err != nil {
		t.Fatalf("copy archival import: %v", err)
	}
	var copiedArchived int
	if err := st.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM todos t
JOIN projects p ON p.id = t.project_id
WHERE p.id != ? AND t.title = 'archived' AND t.archived_at IS NOT NULL`, restoredProject.ID).Scan(&copiedArchived); err != nil {
		t.Fatal(err)
	}
	if copiedArchived != 1 {
		t.Fatalf("copied archived todos=%d want 1", copiedArchived)
	}
}

func TestBackupArchivalValidationAndLegacyImport(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := st.PreviewImport(ctx, nil, ModeFull, "copy"); !errors.Is(err, ErrValidation) {
		t.Fatalf("nil preview err=%v want validation", err)
	}
	legacyTodo := archivalBackupTodo(1, "legacy active")
	legacy := &ExportData{Version: "1.1", ExportedAt: now, Mode: "full", Scope: "full", Projects: []ProjectExport{{Slug: "legacy-active", Name: "Legacy Active", CreatedAt: now, UpdatedAt: now, Todos: []TodoExport{legacyTodo}}}}
	if _, err := st.ImportProjects(ctx, legacy, ModeFull, "copy"); err != nil {
		t.Fatalf("ordinary 1.1 import: %v", err)
	}
	var archiveNull int
	if err := st.db.QueryRowContext(ctx, `SELECT archived_at IS NULL FROM todos WHERE title = 'legacy active'`).Scan(&archiveNull); err != nil {
		t.Fatal(err)
	}
	if archiveNull != 1 {
		t.Fatal("1.1 import created an archived todo")
	}

	negative := int64(-1)
	badTimestampTodo := archivalBackupTodo(2, "negative")
	badTimestampTodo.ArchivedAtPresent = true
	badTimestampTodo.ArchivedAt = &negative
	badTimestamp := &ExportData{Version: "1.2", ExportedAt: now, Mode: "full", Scope: "full", Projects: []ProjectExport{{Slug: "negative", Name: "Negative", CreatedAt: now, UpdatedAt: now, Todos: []TodoExport{badTimestampTodo}}}}
	if _, err := st.ImportProjects(ctx, badTimestamp, ModeFull, "copy"); !errors.Is(err, ErrValidation) {
		t.Fatalf("negative import err=%v want validation", err)
	}
	if _, err := st.PreviewImport(ctx, badTimestamp, ModeFull, "copy"); !errors.Is(err, ErrValidation) {
		t.Fatalf("negative preview err=%v want validation", err)
	}

	mislabeledTodo := archivalBackupTodo(3, "mislabeled")
	mislabeledTodo.ArchivedAtPresent = true
	mislabeled := &ExportData{Version: "1.1", ExportedAt: now, Mode: "full", Scope: "full", Projects: []ProjectExport{{Slug: "mislabeled", Name: "Mislabeled", CreatedAt: now, UpdatedAt: now, Todos: []TodoExport{mislabeledTodo}}}}
	if _, err := st.ImportProjects(ctx, mislabeled, ModeFull, "copy"); !errors.Is(err, ErrValidation) {
		t.Fatalf("mislabeled 1.1 import err=%v want validation", err)
	}
	if _, err := st.PreviewImport(ctx, mislabeled, ModeFull, "copy"); !errors.Is(err, ErrValidation) {
		t.Fatalf("mislabeled 1.1 preview err=%v want validation", err)
	}
	future := *legacy
	future.Version = "1.3"
	if _, err := st.ImportProjects(ctx, &future, ModeFull, "copy"); !errors.Is(err, ErrValidation) {
		t.Fatalf("future version import err=%v want validation", err)
	}
}

// TestBackupArchivalRejectsImplausibleFutureTimestamps complements the negative-timestamp
// guard. A far-future archivedAt is storable but would sit at the head of the newest-first
// archive page permanently, and it is the shape a seconds/nanoseconds unit mistake takes.
func TestBackupArchivalRejectsImplausibleFutureTimestamps(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	build := func(slug string, archivedAt int64) *ExportData {
		todo := archivalBackupTodo(1, slug)
		todo.ArchivedAtPresent = true
		todo.ArchivedAt = &archivedAt
		return &ExportData{Version: "1.2", ExportedAt: now, Mode: "full", Scope: "full", Projects: []ProjectExport{{Slug: slug, Name: slug, CreatedAt: now, UpdatedAt: now, Todos: []TodoExport{todo}}}}
	}

	for name, value := range map[string]int64{
		"absurd":               99999999999999999,
		"nanoseconds mistaken": now.UnixNano(),
		"just beyond slack":    now.UnixMilli() + importArchivedAtFutureSlackMs + 60_000,
	} {
		t.Run(name, func(t *testing.T) {
			data := build("future-"+strings.ReplaceAll(name, " ", "-"), value)
			if _, err := st.ImportProjects(ctx, data, ModeFull, "copy"); !errors.Is(err, ErrValidation) {
				t.Fatalf("import err=%v want validation", err)
			}
			if _, err := st.PreviewImport(ctx, data, ModeFull, "copy"); !errors.Is(err, ErrValidation) {
				t.Fatalf("preview err=%v want validation", err)
			}
		})
	}

	// Clock skew inside the allowed slack must still import.
	ok := build("within-slack", now.UnixMilli()+60_000)
	if _, err := st.ImportProjects(ctx, ok, ModeFull, "copy"); err != nil {
		t.Fatalf("slightly-ahead archivedAt rejected: %v", err)
	}
}

// TestBackupArchivalVersionGateRejectsLegacyAndUnknown pins the accepted version set from
// both directions. 1.0 predates the supported range and 1.3 postdates it; only 1.1 and 1.2
// import, and the gate is identical for import and preview.
func TestBackupArchivalVersionGateRejectsLegacyAndUnknown(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	for _, version := range []string{"1.0", "1.3", "2.0", "", "abc"} {
		t.Run("rejects "+version, func(t *testing.T) {
			data := &ExportData{Version: version, ExportedAt: now, Mode: "full", Scope: "full", Projects: []ProjectExport{{Slug: "v", Name: "V", CreatedAt: now, UpdatedAt: now, Todos: []TodoExport{archivalBackupTodo(1, "t")}}}}
			if _, err := st.ImportProjects(ctx, data, ModeFull, "copy"); !errors.Is(err, ErrValidation) {
				t.Fatalf("import err=%v want validation", err)
			}
			if _, err := st.PreviewImport(ctx, data, ModeFull, "copy"); !errors.Is(err, ErrValidation) {
				t.Fatalf("preview err=%v want validation", err)
			}
		})
	}
	for _, version := range []string{"1.1", "1.2"} {
		t.Run("accepts "+version, func(t *testing.T) {
			data := &ExportData{Version: version, ExportedAt: now, Mode: "full", Scope: "full", Projects: []ProjectExport{{Slug: "accept-" + version, Name: "Accept", CreatedAt: now, UpdatedAt: now, Todos: []TodoExport{archivalBackupTodo(1, "accept "+version)}}}}
			if _, err := st.PreviewImport(ctx, data, ModeFull, "copy"); err != nil {
				t.Fatalf("preview rejected supported version: %v", err)
			}
			if _, err := st.ImportProjects(ctx, data, ModeFull, "copy"); err != nil {
				t.Fatalf("import rejected supported version: %v", err)
			}
		})
	}
}

// TestBackupArchivalCopyAndImportIntoBoardInsertPaths covers the two insert paths the
// existing archival tests miss: a 1.2 copy whose archivedAt is absent (which must create an
// active todo, exactly like 1.1), and the targetSlug "import into existing board" path.
func TestBackupArchivalCopyAndImportIntoBoardInsertPaths(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	base := context.Background()
	owner, err := st.BootstrapUser(base, "backup-insert@example.com", "password123", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithUserID(base, owner.ID)
	now := time.Now().UTC().Truncate(time.Millisecond)

	// 1.2 with archivedAt absent on a copy import: absent means "no archive state",
	// so the new row must be active rather than inheriting anything.
	absent := archivalBackupTodo(1, "absent on copy")
	copyData := &ExportData{Version: "1.2", ExportedAt: now, Mode: "full", Scope: "full", Projects: []ProjectExport{{Slug: "absent-copy", Name: "Absent Copy", CreatedAt: now, UpdatedAt: now, Todos: []TodoExport{absent}}}}
	if _, err := st.ImportProjects(ctx, copyData, ModeFull, "copy"); err != nil {
		t.Fatalf("1.2 absent copy: %v", err)
	}
	var isNull int
	if err := st.db.QueryRowContext(ctx, `SELECT archived_at IS NULL FROM todos WHERE title = 'absent on copy'`).Scan(&isNull); err != nil {
		t.Fatal(err)
	}
	if isNull != 1 {
		t.Fatal("1.2 copy with absent archivedAt created an archived todo")
	}

	// Import into an existing board. This path is anonymous-mode only and requires a
	// temporary target board, so it doubles as the anonymous-mode archival import case.
	target, err := st.CreateAnonymousBoard(base)
	if err != nil {
		t.Fatal(err)
	}
	archivedAt := now.UnixMilli() - 5000
	incoming := archivalBackupTodo(1, "into board archived")
	incoming.ArchivedAtPresent = true
	incoming.ArchivedAt = &archivedAt
	active := archivalBackupTodo(2, "into board active")
	active.ArchivedAtPresent = true
	intoBoard := &ExportData{Version: "1.2", ExportedAt: now, Mode: "anonymous", Scope: "board", Projects: []ProjectExport{{Slug: "into-board", Name: "Into Board", CreatedAt: now, UpdatedAt: now, Todos: []TodoExport{incoming, active}}}}
	if _, err := st.ImportProjectsWithTarget(base, intoBoard, ModeAnonymous, "copy", target.Slug); err != nil {
		t.Fatalf("import into board: %v", err)
	}
	var gotArchived, gotActiveNull int64
	if err := st.db.QueryRowContext(ctx, `SELECT COALESCE(archived_at, -1) FROM todos WHERE project_id = ? AND title = 'into board archived'`, target.ID).Scan(&gotArchived); err != nil {
		t.Fatal(err)
	}
	if gotArchived != archivedAt {
		t.Fatalf("import into board archived_at=%d want %d", gotArchived, archivedAt)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT archived_at IS NULL FROM todos WHERE project_id = ? AND title = 'into board active'`, target.ID).Scan(&gotActiveNull); err != nil {
		t.Fatal(err)
	}
	if gotActiveNull != 1 {
		t.Fatal("explicit null archivedAt imported as archived")
	}
}

// TestBackupArchivalMergeCreatesNewRowsWithArchiveState covers the merge path's new-row
// branch: a merge whose payload contains a local ID the target project does not have must
// insert it, honouring explicit archive state rather than silently dropping it.
func TestBackupArchivalMergeCreatesNewRowsWithArchiveState(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	base := context.Background()
	owner, err := st.BootstrapUser(base, "backup-merge-new@example.com", "password123", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithUserID(base, owner.ID)
	project, err := st.CreateProject(ctx, "Merge new rows")
	if err != nil {
		t.Fatal(err)
	}
	existing, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "already here"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	archivedAt := now.UnixMilli() - 1000
	incoming := archivalBackupTodo(existing.LocalID+50, "merged new archived")
	incoming.ArchivedAtPresent = true
	incoming.ArchivedAt = &archivedAt
	data := &ExportData{Version: "1.2", ExportedAt: now, Mode: "full", Scope: "full", Projects: []ProjectExport{{Slug: project.Slug, Name: project.Name, CreatedAt: now, UpdatedAt: now, Todos: []TodoExport{incoming}}}}
	if _, err := st.ImportProjects(ctx, data, ModeFull, "merge"); err != nil {
		t.Fatalf("merge new row: %v", err)
	}

	var got int64
	if err := st.db.QueryRowContext(ctx, `SELECT COALESCE(archived_at, -1) FROM todos WHERE project_id = ? AND title = 'merged new archived'`, project.ID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != archivedAt {
		t.Fatalf("merge-created row archived_at=%d want %d", got, archivedAt)
	}
	// The pre-existing active row is untouched by the merge.
	after, err := st.GetTodoByLocalID(ctx, project.ID, existing.LocalID, ModeFull)
	if err != nil || after.ArchivedAt != nil {
		t.Fatalf("merge changed an unrelated row: %+v err=%v", after, err)
	}
}
