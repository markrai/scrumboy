package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func mustCreateDeletionTodo(t *testing.T, st *Store, ctx context.Context, projectID int64, title string) Todo {
	t.Helper()
	todo, err := st.CreateTodo(ctx, projectID, CreateTodoInput{Title: title, ColumnKey: "backlog"}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo %q: %v", title, err)
	}
	return todo
}

func TestDeleteTodoByLocalID_PersistenceContract(t *testing.T) {
	st, sqlDB, cleanup := newTestStoreWithSQL(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "Todo deletion contract")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	assignee, err := st.CreateUser(ctx, "assignee@example.com", "password", "Assignee")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.AddProjectMember(ownerCtx, owner.ID, project.ID, assignee.ID, RoleViewer); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}

	start := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Millisecond)
	sprint, err := st.CreateSprint(ownerCtx, project.ID, "Preserved Sprint", start, start.Add(14*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	priorityKey := "high"
	target, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{
		Title:       "Delete me",
		Body:        "persistence contract",
		Tags:        []string{"delete-contract"},
		ColumnKey:   "backlog",
		SprintID:    &sprint.ID,
		PriorityKey: &priorityKey,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo target: %v", err)
	}
	target, err = st.UpdateTodo(ownerCtx, target.ID, UpdateTodoInput{
		Title:              target.Title,
		Body:               target.Body,
		Tags:               []string{"delete-contract"},
		AssigneeUserID:     &assignee.ID,
		SprintID:           &sprint.ID,
		PriorityKey:        &priorityKey,
		PriorityKeyPresent: true,
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodo assignment: %v", err)
	}

	outbound := mustCreateDeletionTodo(t, st, ownerCtx, project.ID, "Outbound peer")
	inbound := mustCreateDeletionTodo(t, st, ownerCtx, project.ID, "Inbound peer")
	unrelatedFrom := mustCreateDeletionTodo(t, st, ownerCtx, project.ID, "Unrelated from")
	unrelatedTo := mustCreateDeletionTodo(t, st, ownerCtx, project.ID, "Unrelated to")
	if err := st.AddLink(ownerCtx, project.ID, target.LocalID, outbound.LocalID, "blocks", ModeFull); err != nil {
		t.Fatalf("AddLink outbound: %v", err)
	}
	if err := st.AddLink(ownerCtx, project.ID, inbound.LocalID, target.LocalID, "relates_to", ModeFull); err != nil {
		t.Fatalf("AddLink inbound: %v", err)
	}
	if err := st.AddLink(ownerCtx, project.ID, unrelatedFrom.LocalID, unrelatedTo.LocalID, "duplicates", ModeFull); err != nil {
		t.Fatalf("AddLink unrelated: %v", err)
	}

	var tagID int64
	if err := sqlDB.QueryRow(`
		SELECT tt.tag_id
		FROM todo_tags tt
		JOIN tags t ON t.id = tt.tag_id
		WHERE tt.todo_id = ? AND t.name = ?`, target.ID, "delete-contract").Scan(&tagID); err != nil {
		t.Fatalf("find target tag: %v", err)
	}
	var ledgerBefore int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM todo_assignee_events WHERE todo_id = ?`, target.ID).Scan(&ledgerBefore); err != nil {
		t.Fatalf("count assignee ledger before delete: %v", err)
	}
	if ledgerBefore == 0 {
		t.Fatal("assignment setup did not create assignee ledger history")
	}
	sprintBefore, err := st.GetSprintByID(ctx, sprint.ID)
	if err != nil {
		t.Fatalf("GetSprintByID before delete: %v", err)
	}
	prioritiesBefore, err := st.GetProjectPriorities(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProjectPriorities before delete: %v", err)
	}

	oldMs := time.Now().UTC().Add(-time.Hour).UnixMilli()
	if _, err := sqlDB.Exec(`UPDATE projects SET updated_at = ?, last_activity_at = ? WHERE id = ?`, oldMs, oldMs, project.ID); err != nil {
		t.Fatalf("seed project timestamps: %v", err)
	}

	if err := st.DeleteTodoByLocalID(ownerCtx, project.ID, target.LocalID, ModeFull); err != nil {
		t.Fatalf("DeleteTodoByLocalID: %v", err)
	}

	var targetRows int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM todos WHERE id = ?`, target.ID).Scan(&targetRows); err != nil {
		t.Fatalf("count target todo: %v", err)
	}
	if targetRows != 0 {
		t.Fatalf("target todo rows=%d want 0", targetRows)
	}

	var incidentLinks int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM todo_links WHERE project_id = ? AND (from_local_id = ? OR to_local_id = ?)`, project.ID, target.LocalID, target.LocalID).Scan(&incidentLinks); err != nil {
		t.Fatalf("count incident links: %v", err)
	}
	if incidentLinks != 0 {
		t.Fatalf("incident links=%d want 0", incidentLinks)
	}
	var unrelatedLinks int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM todo_links WHERE project_id = ? AND from_local_id = ? AND to_local_id = ?`, project.ID, unrelatedFrom.LocalID, unrelatedTo.LocalID).Scan(&unrelatedLinks); err != nil {
		t.Fatalf("count unrelated link: %v", err)
	}
	if unrelatedLinks != 1 {
		t.Fatalf("unrelated links=%d want 1", unrelatedLinks)
	}

	var tagJoins, tagRows int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM todo_tags WHERE todo_id = ?`, target.ID).Scan(&tagJoins); err != nil {
		t.Fatalf("count todo_tags: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM tags WHERE id = ?`, tagID).Scan(&tagRows); err != nil {
		t.Fatalf("count underlying tag: %v", err)
	}
	if tagJoins != 0 || tagRows != 1 {
		t.Fatalf("tag cleanup=(joins:%d tagRows:%d) want (0,1)", tagJoins, tagRows)
	}

	sprintAfter, err := st.GetSprintByID(ctx, sprint.ID)
	if err != nil {
		t.Fatalf("GetSprintByID after delete: %v", err)
	}
	if !reflect.DeepEqual(sprintAfter, sprintBefore) {
		t.Fatalf("sprint changed: before=%+v after=%+v", sprintBefore, sprintAfter)
	}
	prioritiesAfter, err := st.GetProjectPriorities(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProjectPriorities after delete: %v", err)
	}
	if !reflect.DeepEqual(prioritiesAfter, prioritiesBefore) {
		t.Fatalf("priority tiers changed: before=%+v after=%+v", prioritiesBefore, prioritiesAfter)
	}

	var ledgerAfter int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM todo_assignee_events WHERE todo_id = ?`, target.ID).Scan(&ledgerAfter); err != nil {
		t.Fatalf("count assignee ledger after delete: %v", err)
	}
	if ledgerAfter != ledgerBefore {
		t.Fatalf("assignee ledger rows=%d want retained %d", ledgerAfter, ledgerBefore)
	}

	var (
		auditCount     int
		auditProjectID int64
		auditActor     sql.NullInt64
		targetType     string
		targetID       sql.NullInt64
		metadataJSON   string
	)
	if err := sqlDB.QueryRow(`
		SELECT COUNT(*), MIN(project_id), MIN(actor_user_id), MIN(target_type), MIN(target_id), MIN(metadata)
		FROM audit_events
		WHERE action = 'todo_deleted' AND target_type = 'todo' AND target_id = ?`, target.ID).
		Scan(&auditCount, &auditProjectID, &auditActor, &targetType, &targetID, &metadataJSON); err != nil {
		t.Fatalf("read delete audit: %v", err)
	}
	if auditCount != 1 || auditProjectID != project.ID || !auditActor.Valid || auditActor.Int64 != owner.ID || targetType != "todo" || !targetID.Valid || targetID.Int64 != target.ID {
		t.Fatalf("delete audit mismatch count=%d project=%d actor=%+v type=%q target=%+v", auditCount, auditProjectID, auditActor, targetType, targetID)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatalf("decode delete audit metadata: %v", err)
	}
	if got := int64(metadata["local_id"].(float64)); got != target.LocalID {
		t.Fatalf("audit local_id=%d want %d", got, target.LocalID)
	}
	if got := metadata["column_key"]; got != target.ColumnKey {
		t.Fatalf("audit column_key=%v want %q", got, target.ColumnKey)
	}
	var linkRemoved int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'link_removed' AND project_id = ?`, project.ID).Scan(&linkRemoved); err != nil {
		t.Fatalf("count link_removed audits: %v", err)
	}
	if linkRemoved != 0 {
		t.Fatalf("implicit todo deletion produced %d link_removed audits", linkRemoved)
	}

	var updatedAt, lastActivityAt int64
	if err := sqlDB.QueryRow(`SELECT updated_at, last_activity_at FROM projects WHERE id = ?`, project.ID).Scan(&updatedAt, &lastActivityAt); err != nil {
		t.Fatalf("read project timestamps: %v", err)
	}
	if updatedAt <= oldMs {
		t.Fatalf("project updated_at=%d did not advance past %d", updatedAt, oldMs)
	}
	if lastActivityAt <= oldMs {
		t.Fatalf("project last_activity_at=%d did not advance past %d", lastActivityAt, oldMs)
	}
}

func TestDeleteTodoByLocalID_RollsBackOnDeleteFailure(t *testing.T) {
	st, sqlDB, cleanup := newTestStoreWithSQL(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "Todo delete rollback")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	target := mustCreateDeletionTodo(t, st, ownerCtx, project.ID, "Delete target")
	outbound := mustCreateDeletionTodo(t, st, ownerCtx, project.ID, "Outbound")
	inbound := mustCreateDeletionTodo(t, st, ownerCtx, project.ID, "Inbound")
	if err := st.AddLink(ownerCtx, project.ID, target.LocalID, outbound.LocalID, "blocks", ModeFull); err != nil {
		t.Fatalf("AddLink outbound: %v", err)
	}
	if err := st.AddLink(ownerCtx, project.ID, inbound.LocalID, target.LocalID, "relates_to", ModeFull); err != nil {
		t.Fatalf("AddLink inbound: %v", err)
	}

	oldMs := time.Now().UTC().Add(-time.Hour).UnixMilli()
	if _, err := sqlDB.Exec(`UPDATE projects SET updated_at = ?, last_activity_at = ? WHERE id = ?`, oldMs, oldMs, project.ID); err != nil {
		t.Fatalf("seed project timestamps: %v", err)
	}
	if _, err := sqlDB.Exec(`
		CREATE TRIGGER phase21_abort_todo_delete
		BEFORE DELETE ON todos
		BEGIN
			SELECT RAISE(ABORT, 'forced todo delete failure');
		END`); err != nil {
		t.Fatalf("create aborting trigger: %v", err)
	}
	defer func() {
		if _, err := sqlDB.Exec(`DROP TRIGGER IF EXISTS phase21_abort_todo_delete`); err != nil {
			t.Errorf("drop aborting trigger: %v", err)
		}
	}()

	err = st.DeleteTodoByLocalID(ownerCtx, project.ID, target.LocalID, ModeFull)
	if err == nil {
		t.Fatal("DeleteTodoByLocalID unexpectedly succeeded")
	}

	var todoRows, linkRows, audits int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM todos WHERE id = ?`, target.ID).Scan(&todoRows); err != nil {
		t.Fatalf("count todo: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM todo_links WHERE project_id = ? AND (from_local_id = ? OR to_local_id = ?)`, project.ID, target.LocalID, target.LocalID).Scan(&linkRows); err != nil {
		t.Fatalf("count links: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'todo_deleted' AND target_id = ?`, target.ID).Scan(&audits); err != nil {
		t.Fatalf("count delete audits: %v", err)
	}
	if todoRows != 1 || linkRows != 2 || audits != 0 {
		t.Fatalf("rollback state todo=%d links=%d audits=%d want 1,2,0", todoRows, linkRows, audits)
	}
	var updatedAt, lastActivityAt int64
	if err := sqlDB.QueryRow(`SELECT updated_at, last_activity_at FROM projects WHERE id = ?`, project.ID).Scan(&updatedAt, &lastActivityAt); err != nil {
		t.Fatalf("read project timestamps: %v", err)
	}
	if updatedAt != oldMs || lastActivityAt != oldMs {
		t.Fatalf("failed delete changed project timestamps updated_at=%d last_activity_at=%d want %d", updatedAt, lastActivityAt, oldMs)
	}
}

func TestDeleteTodo_ActivityFailureDoesNotReverseCommittedDelete(t *testing.T) {
	st, sqlDB, cleanup := newTestStoreWithSQL(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "Todo activity failure")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	target := mustCreateDeletionTodo(t, st, ownerCtx, project.ID, "Delete target")

	oldMs := time.Now().UTC().Add(-time.Hour).UnixMilli()
	if _, err := sqlDB.Exec(`UPDATE projects SET updated_at = ?, last_activity_at = ? WHERE id = ?`, oldMs, oldMs, project.ID); err != nil {
		t.Fatalf("seed project timestamps: %v", err)
	}
	if _, err := sqlDB.Exec(`
		CREATE TRIGGER phase21_abort_board_activity
		BEFORE UPDATE OF last_activity_at ON projects
		BEGIN
			SELECT RAISE(ABORT, 'forced board activity failure');
		END`); err != nil {
		t.Fatalf("create activity trigger: %v", err)
	}
	defer func() {
		if _, err := sqlDB.Exec(`DROP TRIGGER IF EXISTS phase21_abort_board_activity`); err != nil {
			t.Errorf("drop activity trigger: %v", err)
		}
	}()

	if err := st.DeleteTodo(ownerCtx, target.ID, ModeFull); err != nil {
		t.Fatalf("DeleteTodo returned activity failure: %v", err)
	}

	var todoRows, audits int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM todos WHERE id = ?`, target.ID).Scan(&todoRows); err != nil {
		t.Fatalf("count todo: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'todo_deleted' AND target_id = ?`, target.ID).Scan(&audits); err != nil {
		t.Fatalf("count delete audits: %v", err)
	}
	if todoRows != 0 || audits != 1 {
		t.Fatalf("committed delete state todo=%d audits=%d want 0,1", todoRows, audits)
	}
	var updatedAt, lastActivityAt int64
	if err := sqlDB.QueryRow(`SELECT updated_at, last_activity_at FROM projects WHERE id = ?`, project.ID).Scan(&updatedAt, &lastActivityAt); err != nil {
		t.Fatalf("read project timestamps: %v", err)
	}
	if updatedAt <= oldMs {
		t.Fatalf("transactional project touch did not commit: updated_at=%d old=%d", updatedAt, oldMs)
	}
	if lastActivityAt != oldMs {
		t.Fatalf("failed best-effort activity update changed last_activity_at=%d want %d", lastActivityAt, oldMs)
	}

	if _, err := st.GetTodoByLocalID(ownerCtx, project.ID, target.LocalID, ModeFull); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTodoByLocalID after committed delete err=%v want ErrNotFound", err)
	}
}
