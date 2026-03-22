package store

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAuditEvent_InsertAndQuery(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ctxOwner, "Audit Test")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	var count int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE project_id = ?`, project.ID).Scan(&count); err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	if count < 1 {
		t.Fatalf("expected at least 1 audit event (project_created), got %d", count)
	}

	todo, err := st.CreateTodo(ctxOwner, project.ID, CreateTodoInput{
		Title:     "Audit todo",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action = 'todo_created'`, project.ID).Scan(&count); err != nil {
		t.Fatalf("count todo_created: %v", err)
	}
	if count < 1 {
		t.Fatalf("expected todo_created audit event, got %d", count)
	}

	_, err = st.UpdateTodo(ctxOwner, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           "updated body",
		Tags:           todo.Tags,
		AssigneeUserID: todo.AssigneeUserID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodo: %v", err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action = 'todo_updated'`, project.ID).Scan(&count); err != nil {
		t.Fatalf("count todo_updated: %v", err)
	}
	if count < 1 {
		t.Fatalf("expected todo_updated audit event, got %d", count)
	}
}

func TestAuditEvent_AppendOnly(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ctxOwner, "Append Only Test")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	var id int64
	if err := st.db.QueryRowContext(ctx, `SELECT id FROM audit_events WHERE project_id = ? LIMIT 1`, project.ID).Scan(&id); err != nil {
		t.Fatalf("get audit event id: %v", err)
	}

	_, err = st.db.ExecContext(ctx, `UPDATE audit_events SET action = 'hacked' WHERE id = ?`, id)
	if err == nil {
		t.Fatal("expected UPDATE on audit_events to fail (append-only)")
	}

	_, err = st.db.ExecContext(ctx, `DELETE FROM audit_events WHERE id = ?`, id)
	if err == nil {
		t.Fatal("expected DELETE on audit_events to fail (append-only)")
	}
}

func TestAuditEvent_TodoCreated(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ctxOwner, "Todo Created Test")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	todo, err := st.CreateTodo(ctxOwner, project.ID, CreateTodoInput{
		Title:     "Full title for audit",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	var metadata string
	if err := st.db.QueryRowContext(ctx, `SELECT metadata FROM audit_events WHERE action = 'todo_created' AND target_id = ?`, todo.ID).Scan(&metadata); err != nil {
		t.Fatalf("get todo_created metadata: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(metadata), &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta["title"] != "Full title for audit" {
		t.Fatalf("expected full title in metadata, got %v", meta["title"])
	}
	if meta["local_id"] == nil {
		t.Fatalf("expected local_id in metadata")
	}
}

func TestAuditEvent_TodoUpdated_NoOpGuard(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ctxOwner, "NoOp Guard Test")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	todo, err := st.CreateTodo(ctxOwner, project.ID, CreateTodoInput{
		Title:     "Same",
		Body:      "same body",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	_, err = st.UpdateTodo(ctxOwner, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           todo.Body,
		Tags:           todo.Tags,
		AssigneeUserID: todo.AssigneeUserID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodo: %v", err)
	}

	var count int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action = 'todo_updated' AND target_id = ?`, todo.ID).Scan(&count); err != nil {
		t.Fatalf("count todo_updated: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 todo_updated for no-op PATCH, got %d", count)
	}
}

func TestAuditEvent_TodoMoved(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ctxOwner, "Todo Moved Test")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	todo, err := st.CreateTodo(ctxOwner, project.ID, CreateTodoInput{
		Title:     "Move me",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	_, err = st.MoveTodo(ctxOwner, todo.ID, DefaultColumnDoing, nil, nil, ModeFull)
	if err != nil {
		t.Fatalf("MoveTodo: %v", err)
	}

	var metadata string
	if err := st.db.QueryRowContext(ctx, `SELECT metadata FROM audit_events WHERE action = 'todo_moved' AND target_id = ?`, todo.ID).Scan(&metadata); err != nil {
		t.Fatalf("get todo_moved metadata: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(metadata), &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta["from_column"] != DefaultColumnBacklog {
		t.Fatalf("expected from_column %q, got %v", DefaultColumnBacklog, meta["from_column"])
	}
	if meta["to_column"] != DefaultColumnDoing {
		t.Fatalf("expected to_column %q, got %v", DefaultColumnDoing, meta["to_column"])
	}
}

func TestAuditEvent_MemberAdded(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ctxOwner, "Member Added Test")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	contributor, err := st.CreateUser(ctx, "contributor@example.com", "password", "Contributor")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, owner.ID, project.ID, contributor.ID, RoleContributor); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}

	var count int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action = 'member_added' AND target_id = ?`, contributor.ID).Scan(&count); err != nil {
		t.Fatalf("count member_added: %v", err)
	}
	if count < 1 {
		t.Fatalf("expected member_added audit event, got %d", count)
	}
}

func TestAuditEvent_AnonymousActor(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	project, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}

	todo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:     "Anonymous todo",
		ColumnKey: DefaultColumnBacklog,
	}, ModeAnonymous)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	var actorUserID interface{}
	if err := st.db.QueryRowContext(ctx, `SELECT actor_user_id FROM audit_events WHERE action = 'todo_created' AND target_id = ?`, todo.ID).Scan(&actorUserID); err != nil {
		t.Fatalf("get actor_user_id: %v", err)
	}
	if actorUserID != nil {
		t.Fatalf("expected actor_user_id NULL for anonymous action, got %v", actorUserID)
	}
}
