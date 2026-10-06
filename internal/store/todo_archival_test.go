package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestTodoArchivalPreservesLifecycleAndIsIdempotent(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, err := st.CreateProject(ctx, "archive")
	if err != nil {
		t.Fatal(err)
	}
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "done", ColumnKey: DefaultColumnDone}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	beforeDone, beforeUpdated := todo.DoneAt, todo.UpdatedAt
	r, err := st.ArchiveTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if r.TransitionedCount != 1 || r.UnchangedCount != 0 {
		t.Fatalf("result=%+v", r)
	}
	got, err := st.GetTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if got.ArchivedAt == nil || got.DoneAt == nil || got.DoneAt.UnixMilli() != beforeDone.UnixMilli() || got.UpdatedAt.UnixMilli() != beforeUpdated.UnixMilli() {
		t.Fatalf("lifecycle changed: before=%+v after=%+v", todo, got)
	}
	noOp, err := st.ArchiveTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if noOp.TransitionedCount != 0 || noOp.UnchangedCount != 1 || noOp.TransitionedAt != nil {
		t.Fatalf("noop=%+v", noOp)
	}
	if _, err := st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{Title: "x", Body: "x", Tags: []string{}}, ModeFull); !errors.Is(err, ErrConflict) || ErrorReason(err) != ReasonTodoArchived {
		t.Fatalf("update archived err=%v reason=%q", err, ErrorReason(err))
	}
	restored, err := st.RestoreTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if restored.TransitionedCount != 1 {
		t.Fatalf("restore=%+v", restored)
	}
	got, err = st.GetTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if got.ArchivedAt != nil {
		t.Fatal("restore did not clear archived state")
	}
}

func TestBoardExcludesArchivedAndArchiveListIncludes(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, _ := st.CreateProject(ctx, "board")
	todo, _ := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "a"}, ModeFull)
	_, _ = st.ArchiveTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull)
	pc, err := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, cols, err := st.GetBoard(ctx, &pc, []string{""}, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{}, SortOrderDefault)
	if err != nil {
		t.Fatal(err)
	}
	for _, items := range cols {
		if len(items) != 0 {
			t.Fatalf("archived story leaked to board: %+v", items)
		}
	}
	items, _, _, err := st.ListArchivedTodos(ctx, p.ID, 50, nil, nil, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ArchivedAt == nil {
		t.Fatalf("archive list=%+v", items)
	}
}

func TestTodoArchivalBatchValidationAndAtomicity(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, err := st.CreateProject(ctx, "archive batch")
	if err != nil {
		t.Fatal(err)
	}
	a := mustCreateTodo(t, st, p.ID, "a", DefaultColumnBacklog)
	b := mustCreateTodo(t, st, p.ID, "b", DefaultColumnBacklog)

	for name, ids := range map[string][]int64{
		"empty":     {},
		"zero":      {0},
		"negative":  {-1},
		"duplicate": {a.LocalID, a.LocalID},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := st.ArchiveTodosByLocalID(ctx, p.ID, ids, ModeFull); !errors.Is(err, ErrValidation) {
				t.Fatalf("ArchiveTodosByLocalID(%v) err=%v want validation", ids, err)
			}
		})
	}
	tooMany := make([]int64, MaxTodoArchiveBatch+1)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	if _, err := st.ArchiveTodosByLocalID(ctx, p.ID, tooMany, ModeFull); !errors.Is(err, ErrValidation) {
		t.Fatalf("501 IDs err=%v want validation", err)
	}

	if _, err := st.ArchiveTodosByLocalID(ctx, p.ID, []int64{a.LocalID, 999999}, ModeFull); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing member err=%v want not found", err)
	}
	for _, todo := range []Todo{a, b} {
		got, err := st.GetTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull)
		if err != nil || got.ArchivedAt != nil {
			t.Fatalf("atomic failure changed todo %d: got=%+v err=%v", todo.LocalID, got, err)
		}
	}

	first, err := st.ArchiveTodosByLocalID(ctx, p.ID, []int64{b.LocalID, a.LocalID}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.TransitionedLocalIDs, []int64{b.LocalID, a.LocalID}) || first.TransitionedCount != 2 {
		t.Fatalf("archive input-order result=%+v", first)
	}
	mixedArchive, err := st.ArchiveTodosByLocalID(ctx, p.ID, []int64{a.LocalID, b.LocalID}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if mixedArchive.TransitionedCount != 0 || !reflect.DeepEqual(mixedArchive.UnchangedLocalIDs, []int64{a.LocalID, b.LocalID}) {
		t.Fatalf("idempotent archive=%+v", mixedArchive)
	}
	if _, err := st.RestoreTodoByLocalID(ctx, p.ID, a.LocalID, ModeFull); err != nil {
		t.Fatal(err)
	}
	mixedRestore, err := st.RestoreTodosByLocalID(ctx, p.ID, []int64{a.LocalID, b.LocalID}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if mixedRestore.TransitionedCount != 1 || !reflect.DeepEqual(mixedRestore.TransitionedLocalIDs, []int64{b.LocalID}) || !reflect.DeepEqual(mixedRestore.UnchangedLocalIDs, []int64{a.LocalID}) {
		t.Fatalf("mixed restore=%+v", mixedRestore)
	}
}

func TestTodoArchivalBatchAcceptsFiveHundredAtomically(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, err := st.CreateProject(ctx, "archive 500")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixMilli()
	ids := make([]int64, MaxTodoArchiveBatch)
	for i := range ids {
		ids[i] = int64(i + 1)
		if _, err := tx.ExecContext(ctx, `
INSERT INTO todos(project_id, local_id, title, body, column_key, rank, created_at, updated_at)
VALUES (?, ?, ?, '', ?, ?, ?, ?)`, p.ID, ids[i], fmt.Sprintf("todo %d", ids[i]), DefaultColumnBacklog, ids[i]*1000, now, now); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	result, err := st.ArchiveTodosByLocalID(ctx, p.ID, ids, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if result.TransitionedCount != MaxTodoArchiveBatch || !reflect.DeepEqual(result.TransitionedLocalIDs, ids) {
		t.Fatalf("500 result count=%d", result.TransitionedCount)
	}
	var archived, audits int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM todos WHERE project_id = ? AND archived_at IS NOT NULL`, p.ID).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action = 'todo_archived'`, p.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if archived != MaxTodoArchiveBatch || audits != MaxTodoArchiveBatch {
		t.Fatalf("archived=%d audits=%d want %d", archived, audits, MaxTodoArchiveBatch)
	}
}

func TestTodoArchivalPermissionMatrixAndTemporaryBoards(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, viewer := setupAssigneeTestProject(t)
	defer cleanup()
	maintainerCtx := WithUserID(ctx, maintainer.ID)
	todo, err := st.CreateTodo(maintainerCtx, project.ID, CreateTodoInput{Title: "permission target"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}

	for name, actorCtx := range map[string]context.Context{
		"unauthenticated": ctx,
		"viewer":          WithUserID(ctx, viewer.ID),
		"contributor":     WithUserID(ctx, contributor.ID),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := st.ArchiveTodoByLocalID(actorCtx, project.ID, todo.LocalID, ModeFull); !errors.Is(err, ErrUnauthorized) && !errors.Is(err, ErrNotFound) {
				t.Fatalf("archive err=%v want established write denial", err)
			}
			got, getErr := st.GetTodoByLocalID(maintainerCtx, project.ID, todo.LocalID, ModeFull)
			if getErr != nil || got.ArchivedAt != nil {
				t.Fatalf("unauthorized archive changed todo: %+v err=%v", got, getErr)
			}
		})
	}
	if _, err := st.ArchiveTodoByLocalID(maintainerCtx, project.ID, todo.LocalID, ModeFull); err != nil {
		t.Fatalf("maintainer archive: %v", err)
	}

	temporary, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	temporaryTodo, err := st.CreateTodo(ctx, temporary.ID, CreateTodoInput{Title: "temporary"}, ModeAnonymous)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchiveTodoByLocalID(ctx, temporary.ID, temporaryTodo.LocalID, ModeAnonymous); err != nil {
		t.Fatalf("temporary capability archive: %v", err)
	}
	if _, err := st.RestoreTodoByLocalID(ctx, temporary.ID, temporaryTodo.LocalID, ModeAnonymous); err != nil {
		t.Fatalf("temporary capability restore: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Hour).UnixMilli(), temporary.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchiveTodoByLocalID(ctx, temporary.ID, temporaryTodo.LocalID, ModeAnonymous); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired temporary archive err=%v want not found", err)
	}
}

func TestTodoArchivalAuditMetadataAndRollback(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	base := context.Background()
	owner, err := st.BootstrapUser(base, "archive-audit@example.com", "password123", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithUserID(base, owner.ID)
	p, err := st.CreateProject(ctx, "archive audit")
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "a", ColumnKey: DefaultColumnDoing}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "b", ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET updated_at = 1, last_activity_at = 1 WHERE id = ?`, p.ID); err != nil {
		t.Fatal(err)
	}
	result, err := st.ArchiveTodosByLocalID(ctx, p.ID, []int64{a.LocalID}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if result.TransitionedAt == nil {
		t.Fatal("missing transition timestamp")
	}
	var actor sql.NullInt64
	var targetID int64
	var metadata string
	if err := st.db.QueryRowContext(ctx, `
SELECT actor_user_id, target_id, metadata FROM audit_events
WHERE project_id = ? AND action = 'todo_archived' ORDER BY id DESC LIMIT 1`, p.ID).Scan(&actor, &targetID, &metadata); err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(metadata), &meta); err != nil {
		t.Fatal(err)
	}
	if !actor.Valid || actor.Int64 != owner.ID || targetID != a.ID || meta["local_id"] != float64(a.LocalID) || meta["column_key"] != DefaultColumnDoing || meta["before_archived_at"] != nil || meta["after_archived_at"] != float64(result.TransitionedAt.UnixMilli()) {
		t.Fatalf("archive audit actor=%+v target=%d metadata=%+v", actor, targetID, meta)
	}
	var projectUpdated, activity int64
	if err := st.db.QueryRowContext(ctx, `SELECT updated_at, last_activity_at FROM projects WHERE id = ?`, p.ID).Scan(&projectUpdated, &activity); err != nil {
		t.Fatal(err)
	}
	if projectUpdated <= 1 || activity <= 1 {
		t.Fatalf("project timestamps not touched: updated=%d activity=%d", projectUpdated, activity)
	}
	var assignmentEvents int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM todo_assignee_events WHERE todo_id = ?`, a.ID).Scan(&assignmentEvents); err != nil {
		t.Fatal(err)
	}
	if assignmentEvents != 0 {
		t.Fatalf("archive created %d assignment events", assignmentEvents)
	}
	if _, err := st.ArchiveTodoByLocalID(ctx, p.ID, a.LocalID, ModeFull); err != nil {
		t.Fatal(err)
	}
	var archiveAudits int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action = 'todo_archived'`, p.ID).Scan(&archiveAudits); err != nil {
		t.Fatal(err)
	}
	if archiveAudits != 1 {
		t.Fatalf("idempotent archive audit count=%d want 1", archiveAudits)
	}
	if _, err := st.RestoreTodoByLocalID(ctx, p.ID, a.LocalID, ModeFull); err != nil {
		t.Fatal(err)
	}
	var restoreMetadata string
	if err := st.db.QueryRowContext(ctx, `SELECT metadata FROM audit_events WHERE project_id = ? AND action = 'todo_restored'`, p.ID).Scan(&restoreMetadata); err != nil {
		t.Fatal(err)
	}
	meta = nil
	if err := json.Unmarshal([]byte(restoreMetadata), &meta); err != nil {
		t.Fatal(err)
	}
	if meta["before_archived_at"] != float64(result.TransitionedAt.UnixMilli()) || meta["after_archived_at"] != nil {
		t.Fatalf("restore metadata=%+v", meta)
	}

	if _, err := st.db.ExecContext(ctx, `
CREATE TRIGGER reject_archive_audit BEFORE INSERT ON audit_events
WHEN NEW.action = 'todo_archived'
BEGIN SELECT RAISE(ABORT, 'reject archive audit'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchiveTodoByLocalID(ctx, p.ID, b.LocalID, ModeFull); err == nil {
		t.Fatal("archive should fail when its audit insert fails")
	}
	got, err := st.GetTodoByLocalID(ctx, p.ID, b.LocalID, ModeFull)
	if err != nil || got.ArchivedAt != nil {
		t.Fatalf("audit failure did not roll back archive: got=%+v err=%v", got, err)
	}
}

func TestListArchivedTodosStableCursorWithEqualTimestamps(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.CreateUser(ctx, "archive-page@example.com", "password123", "Archive Page")
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithUserID(ctx, user.ID)
	p, err := st.CreateProject(ctx, "archive pagination")
	if err != nil {
		t.Fatal(err)
	}
	created := make([]Todo, 5)
	ids := make([]int64, len(created))
	for i := range created {
		created[i], err = st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: fmt.Sprintf("todo %d", i), Tags: []string{fmt.Sprintf("tag-%d", i)}}, ModeFull)
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = created[i].LocalID
	}
	if _, err := st.ArchiveTodosByLocalID(ctx, p.ID, ids, ModeFull); err != nil {
		t.Fatal(err)
	}
	var distinct int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT archived_at) FROM todos WHERE project_id = ?`, p.ID).Scan(&distinct); err != nil {
		t.Fatal(err)
	}
	if distinct != 1 {
		t.Fatalf("archive batch timestamps=%d want 1", distinct)
	}

	var gotIDs []int64
	var afterAt, afterID *int64
	for page := 0; ; page++ {
		items, cursor, more, err := st.ListArchivedTodos(ctx, p.ID, 2, afterAt, afterID, ModeFull)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ArchivedAt == nil || len(item.Tags) != 1 {
				t.Fatalf("incomplete archive projection: %+v", item)
			}
			gotIDs = append(gotIDs, item.ID)
		}
		if !more {
			if cursor != "" {
				t.Fatalf("terminal cursor=%q want empty", cursor)
			}
			break
		}
		a, id, err := ParseArchiveCursor(cursor)
		if err != nil {
			t.Fatal(err)
		}
		afterAt, afterID = &a, &id
		if page > 10 {
			t.Fatal("archive pagination did not terminate")
		}
	}
	wantIDs := []int64{created[4].ID, created[3].ID, created[2].ID, created[1].ID, created[0].ID}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("archive traversal IDs=%v want=%v", gotIDs, wantIDs)
	}
}

func TestArchivedTodoRejectsOrdinaryMutationAndAllowsHardDelete(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, err := st.CreateProject(ctx, "archive guards")
	if err != nil {
		t.Fatal(err)
	}
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "archived source"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	neighbor, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "archived neighbor"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	active, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "active"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchiveTodosByLocalID(ctx, p.ID, []int64{todo.LocalID, neighbor.LocalID}, ModeFull); err != nil {
		t.Fatal(err)
	}
	points, assignee, sprint := int64(3), int64(999), int64(999)
	priority := "high"
	_, err = st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{
		Title:              "changed title",
		Body:               "changed body",
		Tags:               []string{"changed-tag"},
		EstimationPoints:   &points,
		AssigneeUserID:     &assignee,
		SprintID:           &sprint,
		PriorityKey:        &priority,
		PriorityKeyPresent: true,
	}, ModeFull)
	if !errors.Is(err, ErrConflict) || ErrorReason(err) != ReasonTodoArchived {
		t.Fatalf("archived update err=%v reason=%q", err, ErrorReason(err))
	}
	if _, err := st.MoveTodo(ctx, todo.ID, DefaultColumnDoing, nil, nil, ModeFull); !errors.Is(err, ErrConflict) || ErrorReason(err) != ReasonTodoArchived {
		t.Fatalf("archived move err=%v reason=%q", err, ErrorReason(err))
	}
	if _, err := st.MoveTodoByLocalID(ctx, p.ID, todo.LocalID, DefaultColumnDoing, nil, nil, ModeFull); !errors.Is(err, ErrConflict) || ErrorReason(err) != ReasonTodoArchived {
		t.Fatalf("archived local-ID move err=%v reason=%q", err, ErrorReason(err))
	}
	if _, err := st.MoveTodoByLocalID(ctx, p.ID, active.LocalID, DefaultColumnBacklog, nil, &neighbor.LocalID, ModeFull); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archived ordering neighbor err=%v want not found", err)
	}
	if err := st.DeleteTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull); err != nil {
		t.Fatalf("hard delete archived todo: %v", err)
	}
	if _, err := st.GetTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted archived todo lookup err=%v", err)
	}
}

// TestArchivedTodoGuardRunsAfterWriteAuthorization pins the error precedence shared by
// UpdateTodo and MoveTodo: the write boundary is evaluated first, so a caller who could not
// have written the todo anyway is denied without learning whether the story is archived.
// The archived conflict is reserved for callers who did clear that boundary.
func TestArchivedTodoGuardRunsAfterWriteAuthorization(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, viewer := setupAssigneeTestProject(t)
	defer cleanup()
	maintainerCtx := WithUserID(ctx, maintainer.ID)

	unassigned, err := st.CreateTodo(maintainerCtx, project.ID, CreateTodoInput{Title: "unassigned target"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := st.CreateTodo(maintainerCtx, project.ID, CreateTodoInput{Title: "assigned target"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	assignee := contributor.ID
	if _, err := st.UpdateTodo(maintainerCtx, assigned.ID, UpdateTodoInput{Title: assigned.Title, Body: "", Tags: []string{}, AssigneeUserID: &assignee}, ModeFull); err != nil {
		t.Fatal(err)
	}
	for _, todo := range []Todo{unassigned, assigned} {
		if _, err := st.ArchiveTodoByLocalID(maintainerCtx, project.ID, todo.LocalID, ModeFull); err != nil {
			t.Fatal(err)
		}
	}

	// Denied before the guard: archival state must never appear in the error.
	for _, tc := range []struct {
		name   string
		actor  context.Context
		todoID int64
	}{
		{"viewer", WithUserID(ctx, viewer.ID), unassigned.ID},
		{"unauthenticated", ctx, unassigned.ID},
		// A contributor has no edit scope on a todo assigned to nobody.
		{"unassigned contributor", WithUserID(ctx, contributor.ID), unassigned.ID},
	} {
		t.Run(tc.name+"/update", func(t *testing.T) {
			_, err := st.UpdateTodo(tc.actor, tc.todoID, UpdateTodoInput{Title: "rewritten", Body: "rewritten", Tags: []string{}}, ModeFull)
			if ErrorReason(err) == ReasonTodoArchived {
				t.Fatalf("archival state leaked to a caller without write access: %v", err)
			}
			if !errors.Is(err, ErrUnauthorized) && !errors.Is(err, ErrNotFound) {
				t.Fatalf("update err=%v want established write denial", err)
			}
		})
		t.Run(tc.name+"/move", func(t *testing.T) {
			_, err := st.MoveTodo(tc.actor, tc.todoID, DefaultColumnDoing, nil, nil, ModeFull)
			if ErrorReason(err) == ReasonTodoArchived {
				t.Fatalf("archival state leaked to a caller without write access: %v", err)
			}
			if !errors.Is(err, ErrUnauthorized) && !errors.Is(err, ErrNotFound) {
				t.Fatalf("move err=%v want established write denial", err)
			}
		})
	}

	// Cleared the boundary: these callers could otherwise have written, so they get the
	// archived conflict. A contributor assigned to the todo holds body-only edit scope.
	if _, err := st.UpdateTodo(WithUserID(ctx, contributor.ID), assigned.ID, UpdateTodoInput{Title: "rewritten", Body: "rewritten", Tags: []string{}}, ModeFull); !errors.Is(err, ErrConflict) || ErrorReason(err) != ReasonTodoArchived {
		t.Fatalf("assigned contributor update err=%v reason=%q want archived conflict", err, ErrorReason(err))
	}
	if _, err := st.UpdateTodo(maintainerCtx, unassigned.ID, UpdateTodoInput{Title: "rewritten", Body: "rewritten", Tags: []string{}}, ModeFull); !errors.Is(err, ErrConflict) || ErrorReason(err) != ReasonTodoArchived {
		t.Fatalf("maintainer update err=%v reason=%q want archived conflict", err, ErrorReason(err))
	}
	if _, err := st.MoveTodo(maintainerCtx, unassigned.ID, DefaultColumnDoing, nil, nil, ModeFull); !errors.Is(err, ErrConflict) || ErrorReason(err) != ReasonTodoArchived {
		t.Fatalf("maintainer move err=%v reason=%q want archived conflict", err, ErrorReason(err))
	}

	for _, todo := range []Todo{unassigned, assigned} {
		got, err := st.GetTodoByLocalID(maintainerCtx, project.ID, todo.LocalID, ModeFull)
		if err != nil || got.Title != todo.Title || got.ArchivedAt == nil {
			t.Fatalf("archived todo mutated by a rejected write: %+v err=%v", got, err)
		}
	}
}

// TestRestoreAfterRebalancePreservesStoredRank characterizes how archival interacts with
// lane rebalancing, which is otherwise unspecified. A1 preserves `rank` across archive and
// restore, and rebalanceColumn deliberately renumbers only active rows. The consequence is
// that a rebalance while a story is archived can leave that story's preserved rank pointing
// somewhere other than its original slot -- including colliding with an active row. That is
// accepted behaviour, not a bug: restore returns the story with the rank it was archived
// with, and ordering stays deterministic because the lane sorts by (rank, id).
func TestRestoreAfterRebalancePreservesStoredRank(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, err := st.CreateProject(ctx, "rebalance")
	if err != nil {
		t.Fatal(err)
	}
	created := make([]Todo, 4)
	for i := range created {
		todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: fmt.Sprintf("lane %d", i+1), ColumnKey: DefaultColumnBacklog}, ModeFull)
		if err != nil {
			t.Fatal(err)
		}
		created[i] = todo
	}
	target := created[2]

	if _, err := st.ArchiveTodoByLocalID(ctx, p.ID, target.LocalID, ModeFull); err != nil {
		t.Fatal(err)
	}
	archived, err := st.GetTodoByLocalID(ctx, p.ID, target.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	rankAtArchive := archived.Rank

	tx, err := st.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rebalanceColumn(ctx, tx, p.ID, DefaultColumnBacklog); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// The rebalance renumbered the three active rows to 1000/2000/3000 and skipped
	// the archived one entirely, so its stored rank is untouched.
	afterRebalance, err := st.GetTodoByLocalID(ctx, p.ID, target.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if afterRebalance.Rank != rankAtArchive {
		t.Fatalf("rebalance renumbered an archived story: rank %d -> %d", rankAtArchive, afterRebalance.Rank)
	}
	activeRanks := map[int64]int64{}
	for _, c := range created {
		if c.LocalID == target.LocalID {
			continue
		}
		got, err := st.GetTodoByLocalID(ctx, p.ID, c.LocalID, ModeFull)
		if err != nil {
			t.Fatal(err)
		}
		activeRanks[c.LocalID] = got.Rank
	}
	for localID, rank := range activeRanks {
		if rank%rankStep != 0 || rank == 0 {
			t.Fatalf("active todo %d was not rebalanced onto the rank grid: %d", localID, rank)
		}
	}

	if _, err := st.RestoreTodoByLocalID(ctx, p.ID, target.LocalID, ModeFull); err != nil {
		t.Fatal(err)
	}
	restored, err := st.GetTodoByLocalID(ctx, p.ID, target.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ArchivedAt != nil || restored.Rank != rankAtArchive {
		t.Fatalf("restore rewrote rank: want %d got %+v", rankAtArchive, restored)
	}
	if restored.ColumnKey != target.ColumnKey {
		t.Fatalf("restore changed the lane: %q -> %q", target.ColumnKey, restored.ColumnKey)
	}

	// Ordering remains total and deterministic even if the restored rank ties an
	// active row, because the lane sorts by (rank, id).
	pc, err := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, cols, err := st.GetBoard(ctx, &pc, []string{""}, "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{}, SortOrderDefault)
	if err != nil {
		t.Fatal(err)
	}
	lane := cols[DefaultColumnBacklog]
	if len(lane) != len(created) {
		t.Fatalf("restored story missing from lane: %d of %d", len(lane), len(created))
	}
	for i := 1; i < len(lane); i++ {
		if !lessByRankID(lane[i-1].Rank, lane[i-1].ID, lane[i].Rank, lane[i].ID) {
			t.Fatalf("lane ordering is not total after restore: %+v", lane)
		}
	}
}
