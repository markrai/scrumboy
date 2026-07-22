package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"scrumboy/internal/version"
)

func expireProject(t *testing.T, st *Store, projectID int64, past time.Duration) {
	t.Helper()
	pastMs := time.Now().UTC().Add(-past).UnixMilli()
	if _, err := st.db.ExecContext(context.Background(), `UPDATE projects SET expires_at = ? WHERE id = ?`, pastMs, projectID); err != nil {
		t.Fatalf("expire project: %v", err)
	}
}

func TestCreateAnonymousBoard_InitialExpiresAt90Days(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	p, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	if p.ExpiresAt == nil {
		t.Fatal("expected expires_at on anonymous board")
	}

	want := time.Now().UTC().AddDate(0, 0, TemporaryBoardLifetimeDays)
	slack := 2 * time.Minute
	if p.ExpiresAt.Before(want.Add(-slack)) || p.ExpiresAt.After(want.Add(slack)) {
		t.Fatalf("expires_at %v, want about %v (±%v)", p.ExpiresAt, want, slack)
	}
}

func TestDeleteProject_AnonymousTempBoard_Blocked(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	p, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}

	_, err = st.DeleteProject(ctx, p.ID, 1)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteProject_ReturnsCommittedNotificationSnapshot(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "owner-delete@example.com", "password123", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.CreateUser(ctx, "member-delete@example.com", "password123", "Member")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "Delete Snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectMember(ownerCtx, owner.ID, project.ID, member.ID, RoleViewer); err != nil {
		t.Fatal(err)
	}

	snapshot, err := st.DeleteProject(ownerCtx, project.ID, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ProjectID != project.ID || snapshot.Name != "Delete Snapshot" {
		t.Fatalf("unexpected deletion snapshot: %+v", snapshot)
	}
	if len(snapshot.MemberUserIDs) != 2 || snapshot.MemberUserIDs[0] != owner.ID || snapshot.MemberUserIDs[1] != member.ID {
		t.Fatalf("unexpected snapshot recipients: %+v", snapshot.MemberUserIDs)
	}
	if _, err := st.GetProject(ctx, project.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected project to be deleted before snapshot returned, got %v", err)
	}
}

func TestExpiredTemporaryProject_BoardReadDenied(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	p, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	expireProject(t, st, p.ID, 24*time.Hour)

	_, err = st.GetProjectContextBySlug(ctx, p.Slug, ModeAnonymous)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for expired board read, got %v", err)
	}
}

func TestExpiredTemporaryProject_TodoCreateDenied(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	p, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	expireProject(t, st, p.ID, 24*time.Hour)

	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:     "late",
		ColumnKey: DefaultColumnBacklog,
	}, ModeAnonymous)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for todo create on expired board, got %v", err)
	}
}

func TestDeleteExpiredProjects_AuthenticatedTempBoard(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u, err := st.BootstrapUser(ctx, "owner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	if _, err := st.CreateAnonymousBoard(WithUserID(ctx, u.ID)); err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	var projectID int64
	if err := st.db.QueryRowContext(ctx, `SELECT id FROM projects WHERE creator_user_id = ? AND expires_at IS NOT NULL ORDER BY id DESC LIMIT 1`, u.ID).Scan(&projectID); err != nil {
		t.Fatalf("find authenticated temp board: %v", err)
	}
	p, err := st.GetProject(ctx, projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p.CreatorUserID == nil {
		t.Fatal("expected authenticated temporary board with creator_user_id")
	}
	expireProject(t, st, p.ID, 15*24*time.Hour)

	deleted, err := st.DeleteExpiredProjects(ctx)
	if err != nil {
		t.Fatalf("DeleteExpiredProjects: %v", err)
	}
	if deleted < 1 {
		t.Fatalf("expected at least 1 deleted project, got %d", deleted)
	}

	var count int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?`, p.ID).Scan(&count); err != nil {
		t.Fatalf("count project: %v", err)
	}
	if count != 0 {
		t.Fatal("expected authenticated expired temp board to be removed")
	}
}

func TestDeleteExpiredProjects_AuditEventsMayRemain(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	p, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	expireProject(t, st, p.ID, 91*24*time.Hour)

	nowMs := time.Now().UTC().UnixMilli()
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO audit_events (project_id, actor_user_id, action, target_type, target_id, metadata, created_at)
VALUES (?, NULL, 'project_created', 'project', ?, '{}', ?)`, p.ID, p.ID, nowMs); err != nil {
		t.Fatalf("insert audit event: %v", err)
	}

	if _, err := st.DeleteExpiredProjects(ctx); err != nil {
		t.Fatalf("DeleteExpiredProjects: %v", err)
	}

	var auditCount int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE project_id = ?`, p.ID).Scan(&auditCount); err != nil {
		t.Fatalf("count audit_events: %v", err)
	}
	if auditCount < 1 {
		t.Fatalf("expected append-only audit row(s) to remain after project delete, got count %d", auditCount)
	}
	var projectCount int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?`, p.ID).Scan(&projectCount); err != nil {
		t.Fatalf("count project: %v", err)
	}
	if projectCount != 0 {
		t.Fatal("expected project row removed by DeleteExpiredProjects")
	}
}

func TestDeleteExpiredProjects_CascadesTodos(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	p, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:     "gone",
		ColumnKey: DefaultColumnBacklog,
	}, ModeAnonymous)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	expireProject(t, st, p.ID, 15*24*time.Hour)

	if _, err := st.DeleteExpiredProjects(ctx); err != nil {
		t.Fatalf("DeleteExpiredProjects: %v", err)
	}

	var todoCount int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM todos WHERE id = ?`, todo.ID).Scan(&todoCount); err != nil {
		t.Fatalf("count todo: %v", err)
	}
	if todoCount != 0 {
		t.Fatal("expected todo cascade delete when expired project is removed")
	}
}

func TestImportReplace_ForbiddenInAnonymousMode(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	data := &ExportData{
		Version:  version.ExportFormatVersion,
		Scope:    "single",
		Projects: []ProjectExport{{Slug: "x", Name: "X"}},
	}

	_, err := st.ImportProjects(ctx, data, ModeAnonymous, "replace")
	if err == nil {
		t.Fatal("expected error for replace import in anonymous mode")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}
