package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/version"
)

func TestGuestRoleBoardAccess(t *testing.T) {
	f := newServiceProjectFixture(t)
	st := f.st
	id := f.human.ID
	ctx := WithUserID(context.Background(), id)
	oldTemporary, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureMaintainerMembership(f.ownerCtx, f.temp.ID, f.owner.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectMember(f.ownerCtx, f.owner.ID, f.temp.ID, id, RoleViewer); err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectMember(f.ownerCtx, f.owner.ID, f.durable.ID, id, RoleContributor); err != nil {
		t.Fatal(err)
	}
	err = st.UpdateUserRole(f.ownerCtx, f.owner.ID, id, SystemRoleGuest)
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), f.temp.Slug) || !strings.Contains(err.Error(), f.durable.Slug) {
		t.Fatalf("incompatible memberships: %v", err)
	}
	if err := st.RemoveProjectMember(f.ownerCtx, f.owner.ID, f.temp.ID, id); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateProjectMemberRole(f.ownerCtx, f.owner.ID, f.durable.ID, id, RoleViewer); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateUserRole(f.ownerCtx, f.admin.ID, id, SystemRoleGuest); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("admin changed role: %v", err)
	}
	if err := st.UpdateUserRole(f.ownerCtx, f.owner.ID, id, SystemRoleGuest); err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectMember(f.ownerCtx, f.owner.ID, f.temp.ID, id, RoleViewer); !errors.Is(err, ErrValidation) {
		t.Fatalf("add temporary membership: %v", err)
	}
	if err := st.UpdateProjectMemberRole(f.ownerCtx, f.owner.ID, f.durable.ID, id, RoleContributor); !errors.Is(err, ErrValidation) {
		t.Fatalf("promote guest membership: %v", err)
	}
	if _, err := st.GetProjectContextBySlug(ctx, f.durable.Slug, ModeFull); err != nil {
		t.Fatalf("read assigned board: %v", err)
	}
	if _, err := st.CreateTodo(ctx, f.durable.ID, CreateTodoInput{Title: "No edits"}, ModeFull); err == nil {
		t.Fatal("guest created a todo on an assigned board")
	}
	for _, project := range []Project{f.anon, f.temp, oldTemporary} {
		if _, err := st.GetProjectContextBySlug(ctx, project.Slug, ModeFull); !errors.Is(err, ErrNotFound) {
			t.Fatalf("read temporary board %q: %v", project.Slug, err)
		}
		if _, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "No edits"}, ModeFull); !errors.Is(err, ErrNotFound) {
			t.Fatalf("write temporary board %q: %v", project.Slug, err)
		}
	}
	if _, err := st.GetProjectContextBySlug(context.Background(), oldTemporary.Slug, ModeFull); err != nil {
		t.Fatalf("signed-out read of temporary link: %v", err)
	}
	if items, err := st.ListProjects(ctx); err != nil || len(items) != 1 || items[0].Project.Slug != f.durable.Slug {
		t.Fatalf("REST board list: %+v, %v", items, err)
	}
	if items, _, err := st.ListProjectSummaries(ctx, 10, nil); err != nil || len(items) != 1 || items[0].Slug != f.durable.Slug {
		t.Fatalf("MCP board list: %+v, %v", items, err)
	}
	if data, err := st.ExportAllProjects(ctx, ModeFull); err != nil || len(data.Projects) != 0 {
		t.Fatalf("backup export: %+v, %v", data, err)
	}

	data, err := st.ExportAllProjects(f.ownerCtx, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	trello := *data
	trello.Projects = data.Projects[:1]
	archive := *data
	archive.Version = version.ArchiveExportFormatVersion
	calls := map[string]func() error{
		"regular":        func() error { _, err := st.CreateProject(ctx, "Guest"); return err },
		"temporary":      func() error { _, err := st.CreateAnonymousBoard(ctx); return err },
		"claim":          func() error { return st.ClaimTemporaryBoard(ctx, oldTemporary.ID, id) },
		"Trello":         func() error { _, err := st.ImportTrelloProject(ctx, &trello, "", nil, ModeFull); return err },
		"backup copy":    func() error { _, err := st.ImportProjects(ctx, data, ModeFull, "copy"); return err },
		"backup merge":   func() error { _, err := st.ImportProjects(ctx, data, ModeFull, "merge"); return err },
		"backup replace": func() error { _, err := st.ImportProjects(ctx, data, ModeFull, "replace"); return err },
		"backup archive": func() error { _, err := st.ImportProjectsWithAssets(ctx, &archive, ModeFull, "copy", nil); return err },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, ErrForbidden) {
				t.Fatalf("expected forbidden, got %v", err)
			}
		})
	}
	if err := st.UpdateUserRole(f.ownerCtx, f.owner.ID, id, SystemRoleUser); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProject(ctx, "Regular user again"); err != nil {
		t.Fatalf("ordinary user with Viewer membership cannot create: %v", err)
	}
}

func TestGuestDashboardIgnoresArchivedAssignmentsOutsideMembership(t *testing.T) {
	f := newServiceProjectFixture(t)
	st := f.st
	id := f.human.ID
	if err := st.AddProjectMember(f.ownerCtx, f.owner.ID, f.durable.ID, id, RoleViewer); err != nil {
		t.Fatal(err)
	}
	todo, err := st.CreateTodo(f.ownerCtx, f.durable.ID, CreateTodoInput{Title: "Old assignment", ColumnKey: "done", AssigneeUserID: &id}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchiveTodoByLocalID(f.ownerCtx, f.durable.ID, todo.LocalID, ModeFull); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveProjectMember(f.ownerCtx, f.owner.ID, f.durable.ID, id); err != nil {
		t.Fatal(err)
	}
	if got := f.count(t, "SELECT COUNT(*) FROM todos WHERE id = ? AND assignee_user_id = ?", todo.ID, id); got != 1 {
		t.Fatal("fixture needs the archived assignment to outlive the membership")
	}

	now := time.Now().UTC().UnixMilli()
	if _, err := f.db.Exec("UPDATE todos SET done_at = ? WHERE id = ?", now, todo.ID); err != nil {
		t.Fatal(err)
	}

	if err := st.UpdateUserRole(f.ownerCtx, f.owner.ID, id, SystemRoleGuest); err != nil {
		t.Fatal(err)
	}
	ctx := WithUserID(context.Background(), id)
	summary, err := st.GetDashboardSummary(ctx, id, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	for _, week := range summary.WeeklyThroughput {
		if week.Stories != 0 {
			t.Fatalf("dashboard counted an assignment outside membership: %+v", week)
		}
	}
	if err := st.AddProjectMember(f.ownerCtx, f.owner.ID, f.durable.ID, id, RoleViewer); err != nil {
		t.Fatal(err)
	}
	todo, err = st.CreateTodo(f.ownerCtx, f.durable.ID, CreateTodoInput{Title: "Current assignment", ColumnKey: "backlog", AssigneeUserID: &id}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	for _, sort := range []string{"activity", "board"} {
		items, _, err := st.ListDashboardTodos(ctx, id, 10, nil, sort)
		if err != nil || len(items) != 1 || items[0].LocalID != todo.LocalID {
			t.Fatalf("%s dashboard: %+v, %v", sort, items, err)
		}
	}
}
