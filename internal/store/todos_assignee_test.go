package store

import (
	"context"
	"errors"
	"testing"
)

func ptrInt64(v int64) *int64 { return &v }

func setupAssigneeTestProject(t *testing.T) (*Store, func(), context.Context, Project, User, User, User) {
	t.Helper()

	st, cleanup := newTestStore(t)
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		cleanup()
		t.Fatalf("BootstrapUser owner: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)

	project, err := st.CreateProject(ctxOwner, "Assignee Test Project")
	if err != nil {
		cleanup()
		t.Fatalf("CreateProject: %v", err)
	}

	maintainer, err := st.CreateUser(ctx, "maintainer@example.com", "password", "Maintainer")
	if err != nil {
		cleanup()
		t.Fatalf("CreateUser maintainer: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, owner.ID, project.ID, maintainer.ID, RoleMaintainer); err != nil {
		cleanup()
		t.Fatalf("AddProjectMember maintainer: %v", err)
	}

	contributor, err := st.CreateUser(ctx, "contributor@example.com", "password", "Contributor")
	if err != nil {
		cleanup()
		t.Fatalf("CreateUser contributor: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, owner.ID, project.ID, contributor.ID, RoleContributor); err != nil {
		cleanup()
		t.Fatalf("AddProjectMember contributor: %v", err)
	}

	viewer, err := st.CreateUser(ctx, "viewer@example.com", "password", "Viewer")
	if err != nil {
		cleanup()
		t.Fatalf("CreateUser viewer: %v", err)
	}
	ctxMaintainer := WithUserID(ctx, maintainer.ID)
	if err := st.AddProjectMember(ctxMaintainer, maintainer.ID, project.ID, viewer.ID, RoleViewer); err != nil {
		cleanup()
		t.Fatalf("AddProjectMember viewer: %v", err)
	}

	return st, cleanup, ctx, project, maintainer, contributor, viewer
}

func TestTodoAssignee_ContributorCanEditBodyWhenAssigned(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, _ := setupAssigneeTestProject(t)
	defer cleanup()

	ctxMaintainer := WithUserID(ctx, maintainer.ID)
	ctxContributor := WithUserID(ctx, contributor.ID)

	todo, err := st.CreateTodo(ctxMaintainer, project.ID, CreateTodoInput{
		Title:     "Edit my notes",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	_, err = st.UpdateTodo(ctxMaintainer, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           todo.Body,
		Tags:           todo.Tags,
		AssigneeUserID: ptrInt64(contributor.ID),
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodo assign contributor: %v", err)
	}

	updated, err := st.UpdateTodo(ctxContributor, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           "My notes",
		Tags:           todo.Tags,
		AssigneeUserID: ptrInt64(contributor.ID),
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodo edit body: %v", err)
	}
	if updated.Body != "My notes" {
		t.Fatalf("expected body 'My notes', got %q", updated.Body)
	}
}

func TestTodoAssignee_ContributorCannotAssignOthers(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, viewer := setupAssigneeTestProject(t)
	defer cleanup()

	ctxMaintainer := WithUserID(ctx, maintainer.ID)
	ctxContributor := WithUserID(ctx, contributor.ID)

	todo, err := st.CreateTodo(ctxMaintainer, project.ID, CreateTodoInput{
		Title:     "No assign others",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	_, err = st.UpdateTodo(ctxMaintainer, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           todo.Body,
		Tags:           todo.Tags,
		AssigneeUserID: ptrInt64(contributor.ID),
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodo assign contributor: %v", err)
	}

	updated, err := st.UpdateTodo(ctxContributor, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           "ignored",
		Tags:           todo.Tags,
		AssigneeUserID: ptrInt64(viewer.ID),
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodo: %v", err)
	}
	if updated.AssigneeUserID == nil || *updated.AssigneeUserID != contributor.ID {
		t.Fatalf("contributor must not change assignee: expected %d, got %#v", contributor.ID, updated.AssigneeUserID)
	}
}

func TestTodoAssignee_ContributorCannotSelfAssign(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, _ := setupAssigneeTestProject(t)
	defer cleanup()

	ctxMaintainer := WithUserID(ctx, maintainer.ID)
	ctxContributor := WithUserID(ctx, contributor.ID)

	// CreateTodo: contributor cannot create with self-assign (contributor cannot create at all; ErrUnauthorized)
	_, err := st.CreateTodo(ctxContributor, project.ID, CreateTodoInput{
		Title:          "Self assign on create",
		ColumnKey:      DefaultColumnBacklog,
		AssigneeUserID: ptrInt64(contributor.ID),
	}, ModeFull)
	if err == nil {
		t.Fatal("expected CreateTodo with contributor self-assign to fail")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}

	// UpdateTodo: contributor cannot assign self (todo unassigned, contributor tries to assign self)
	todo, err := st.CreateTodo(ctxMaintainer, project.ID, CreateTodoInput{
		Title:     "Will try self-assign",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	_, err = st.UpdateTodo(ctxContributor, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           todo.Body,
		Tags:           todo.Tags,
		AssigneeUserID: ptrInt64(contributor.ID),
	}, ModeFull)
	if err == nil {
		t.Fatal("expected UpdateTodo with contributor self-assign to fail")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestTodoAssignee_MaintainerCanAssignOthersAndViewerAssignable(t *testing.T) {
	st, cleanup, ctx, project, maintainer, _, viewer := setupAssigneeTestProject(t)
	defer cleanup()

	ctxMaintainer := WithUserID(ctx, maintainer.ID)
	todo, err := st.CreateTodo(ctxMaintainer, project.ID, CreateTodoInput{
		Title:  "Assign viewer",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	updated, err := st.UpdateTodo(ctxMaintainer, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           todo.Body,
		Tags:           todo.Tags,
		AssigneeUserID: ptrInt64(viewer.ID),
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodo assign viewer: %v", err)
	}
	if updated.AssigneeUserID == nil || *updated.AssigneeUserID != viewer.ID {
		t.Fatalf("expected assignee %d, got %#v", viewer.ID, updated.AssigneeUserID)
	}
}

func TestTodoAssignee_AnonymousModeRejectsAssignment(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	project, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	todo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:     "Anonymous",
		ColumnKey: DefaultColumnBacklog,
	}, ModeAnonymous)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	assignID := int64(999)
	_, err = st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           todo.Body,
		Tags:           todo.Tags,
		AssigneeUserID: &assignID,
	}, ModeAnonymous)
	if err == nil {
		t.Fatal("expected anonymous assignment to fail")
	}
}

func TestTodoAssignee_AuditAndMemberRemovalUnassign(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, _ := setupAssigneeTestProject(t)
	defer cleanup()

	ctxMaintainer := WithUserID(ctx, maintainer.ID)
	todo, err := st.CreateTodo(ctxMaintainer, project.ID, CreateTodoInput{
		Title:  "Audit me",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	// Initial assignment should create one audit event.
	_, err = st.UpdateTodo(ctxMaintainer, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           todo.Body,
		Tags:           todo.Tags,
		AssigneeUserID: ptrInt64(contributor.ID),
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodo assign contributor: %v", err)
	}

	var count int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM todo_assignee_events WHERE todo_id = ?`, todo.ID).Scan(&count); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 audit row after first assignment, got %d", count)
	}

	// Unchanged assignee must not create a new audit row.
	_, err = st.UpdateTodo(ctxMaintainer, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           todo.Body + " updated",
		Tags:           todo.Tags,
		AssigneeUserID: ptrInt64(contributor.ID),
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodo unchanged assignee: %v", err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM todo_assignee_events WHERE todo_id = ?`, todo.ID).Scan(&count); err != nil {
		t.Fatalf("count audit rows after unchanged assignee: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 audit row when assignee unchanged, got %d", count)
	}

	// Removing member must unassign and write member_removed audit row.
	if err := st.RemoveProjectMember(ctxMaintainer, maintainer.ID, project.ID, contributor.ID); err != nil {
		t.Fatalf("RemoveProjectMember: %v", err)
	}

	got, err := st.GetTodoByLocalID(ctxMaintainer, project.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatalf("GetTodoByLocalID: %v", err)
	}
	if got.AssigneeUserID != nil {
		t.Fatalf("expected assignee to be nil after member removal, got %#v", got.AssigneeUserID)
	}

	var memberRemovedCount int
	if err := st.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM todo_assignee_events
		WHERE todo_id = ? AND reason = 'member_removed'
	`, todo.ID).Scan(&memberRemovedCount); err != nil {
		t.Fatalf("count member_removed events: %v", err)
	}
	if memberRemovedCount != 1 {
		t.Fatalf("expected 1 member_removed event, got %d", memberRemovedCount)
	}
}

func TestTodoAssignee_AssignNonMemberRejected(t *testing.T) {
	st, cleanup, ctx, project, maintainer, _, _ := setupAssigneeTestProject(t)
	defer cleanup()

	outsider, err := st.CreateUser(ctx, "outsider@example.com", "password", "Outsider")
	if err != nil {
		t.Fatalf("CreateUser outsider: %v", err)
	}

	ctxMaintainer := WithUserID(ctx, maintainer.ID)
	todo, err := st.CreateTodo(ctxMaintainer, project.ID, CreateTodoInput{
		Title:  "Assign outsider",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	_, err = st.UpdateTodo(ctxMaintainer, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           todo.Body,
		Tags:           todo.Tags,
		AssigneeUserID: ptrInt64(outsider.ID),
	}, ModeFull)
	if err == nil {
		t.Fatal("expected assign non-member to fail")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestTodoAssignee_AssignNonexistentUserRejected(t *testing.T) {
	st, cleanup, ctx, project, maintainer, _, _ := setupAssigneeTestProject(t)
	defer cleanup()

	ctxMaintainer := WithUserID(ctx, maintainer.ID)
	todo, err := st.CreateTodo(ctxMaintainer, project.ID, CreateTodoInput{
		Title:  "Assign ghost",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	ghostID := int64(999999999)
	_, err = st.UpdateTodo(ctxMaintainer, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           todo.Body,
		Tags:           todo.Tags,
		AssigneeUserID: &ghostID,
	}, ModeFull)
	if err == nil {
		t.Fatal("expected assign nonexistent user to fail")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestTodoAssignee_ContributorCannotUnassign(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, _ := setupAssigneeTestProject(t)
	defer cleanup()

	ctxMaintainer := WithUserID(ctx, maintainer.ID)
	ctxContributor := WithUserID(ctx, contributor.ID)

	todo, err := st.CreateTodo(ctxMaintainer, project.ID, CreateTodoInput{
		Title:     "Cannot unassign",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	_, err = st.UpdateTodo(ctxMaintainer, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           todo.Body,
		Tags:           todo.Tags,
		AssigneeUserID: ptrInt64(contributor.ID),
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodo assign contributor: %v", err)
	}

	updated, err := st.UpdateTodo(ctxContributor, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           todo.Body,
		Tags:           todo.Tags,
		AssigneeUserID: nil,
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodo: %v", err)
	}
	if updated.AssigneeUserID == nil || *updated.AssigneeUserID != contributor.ID {
		t.Fatalf("contributor must not change assignee: expected %d, got %#v", contributor.ID, updated.AssigneeUserID)
	}
}

func TestTodoAssignee_ViewerCannotAssignSelf(t *testing.T) {
	st, cleanup, ctx, project, maintainer, _, viewer := setupAssigneeTestProject(t)
	defer cleanup()

	ctxMaintainer := WithUserID(ctx, maintainer.ID)
	ctxViewer := WithUserID(ctx, viewer.ID)

	todo, err := st.CreateTodo(ctxMaintainer, project.ID, CreateTodoInput{
		Title:  "Viewer claim",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	_, err = st.UpdateTodo(ctxViewer, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           todo.Body,
		Tags:           todo.Tags,
		AssigneeUserID: ptrInt64(viewer.ID),
	}, ModeFull)
	if err == nil {
		t.Fatal("expected viewer self-assign to fail")
	}
	// Viewers lack write access; getProjectForWriteTx returns ErrNotFound, or UpdateTodo returns ErrUnauthorized
	if !errors.Is(err, ErrUnauthorized) && !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrUnauthorized or ErrNotFound, got %v", err)
	}
}

func TestTodoAssignee_UnauthenticatedAssignmentChangeRejected(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, _ := setupAssigneeTestProject(t)
	defer cleanup()

	ctxMaintainer := WithUserID(ctx, maintainer.ID)
	todo, err := st.CreateTodo(ctxMaintainer, project.ID, CreateTodoInput{
		Title:  "No actor",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	_, err = st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           todo.Body,
		Tags:           todo.Tags,
		AssigneeUserID: ptrInt64(contributor.ID),
	}, ModeFull)
	if err == nil {
		t.Fatal("expected unauthenticated assignment change to fail")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestTodoAssignee_ContributorCannotCreate(t *testing.T) {
	st, cleanup, ctx, project, _, contributor, _ := setupAssigneeTestProject(t)
	defer cleanup()

	ctxContributor := WithUserID(ctx, contributor.ID)
	_, err := st.CreateTodo(ctxContributor, project.ID, CreateTodoInput{
		Title:     "Contributor create",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err == nil {
		t.Fatal("expected contributor create to fail")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestTodoAssignee_ContributorCannotDelete(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, _ := setupAssigneeTestProject(t)
	defer cleanup()

	ctxMaintainer := WithUserID(ctx, maintainer.ID)
	ctxContributor := WithUserID(ctx, contributor.ID)

	todo, err := st.CreateTodo(ctxMaintainer, project.ID, CreateTodoInput{
		Title:     "Delete me",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	err = st.DeleteTodo(ctxContributor, todo.ID, ModeFull)
	if err == nil {
		t.Fatal("expected contributor delete to fail")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestTodoAssignee_ContributorCannotMove(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, _ := setupAssigneeTestProject(t)
	defer cleanup()

	ctxMaintainer := WithUserID(ctx, maintainer.ID)
	ctxContributor := WithUserID(ctx, contributor.ID)

	todo, err := st.CreateTodo(ctxMaintainer, project.ID, CreateTodoInput{
		Title:     "Move me",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	_, err = st.MoveTodo(ctxContributor, todo.ID, DefaultColumnDoing, nil, nil, ModeFull)
	if err == nil {
		t.Fatal("expected contributor move to fail")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestTodoAssignee_ContributorCannotEditUnassignedTodo(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, _ := setupAssigneeTestProject(t)
	defer cleanup()

	ctxMaintainer := WithUserID(ctx, maintainer.ID)
	ctxContributor := WithUserID(ctx, contributor.ID)

	todo, err := st.CreateTodo(ctxMaintainer, project.ID, CreateTodoInput{
		Title:     "Unassigned",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	_, err = st.UpdateTodo(ctxContributor, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           "trying to edit",
		Tags:           todo.Tags,
		AssigneeUserID: nil,
	}, ModeFull)
	if err == nil {
		t.Fatal("expected contributor edit of unassigned todo to fail")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestTodoAssignee_ContributorCannotModifyTitle(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, _ := setupAssigneeTestProject(t)
	defer cleanup()

	ctxMaintainer := WithUserID(ctx, maintainer.ID)
	ctxContributor := WithUserID(ctx, contributor.ID)

	todo, err := st.CreateTodo(ctxMaintainer, project.ID, CreateTodoInput{
		Title:     "Original",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	_, err = st.UpdateTodo(ctxMaintainer, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           todo.Body,
		Tags:           todo.Tags,
		AssigneeUserID: ptrInt64(contributor.ID),
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodo assign: %v", err)
	}

	updated, err := st.UpdateTodo(ctxContributor, todo.ID, UpdateTodoInput{
		Title:          "Hacked",
		Body:           "notes",
		Tags:           todo.Tags,
		AssigneeUserID: ptrInt64(contributor.ID),
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodo: %v", err)
	}
	if updated.Title != "Original" {
		t.Fatalf("contributor must not change title: expected 'Original', got %q", updated.Title)
	}
	if updated.Body != "notes" {
		t.Fatalf("expected body 'notes', got %q", updated.Body)
	}
}

func TestTodoAssignee_MaintainerFullAccess(t *testing.T) {
	st, cleanup, ctx, project, maintainer, _, _ := setupAssigneeTestProject(t)
	defer cleanup()

	ctxMaintainer := WithUserID(ctx, maintainer.ID)

	todo, err := st.CreateTodo(ctxMaintainer, project.ID, CreateTodoInput{
		Title:     "Full access",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	updated, err := st.UpdateTodo(ctxMaintainer, todo.ID, UpdateTodoInput{
		Title:          "Updated",
		Body:           "body",
		Tags:           []string{"tag"},
		AssigneeUserID: ptrInt64(maintainer.ID),
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodo: %v", err)
	}
	if updated.Title != "Updated" || updated.Body != "body" || len(updated.Tags) != 1 {
		t.Fatalf("maintainer should have full edit: got %+v", updated)
	}

	_, err = st.MoveTodo(ctxMaintainer, todo.ID, DefaultColumnDoing, nil, nil, ModeFull)
	if err != nil {
		t.Fatalf("MoveTodo: %v", err)
	}

	err = st.DeleteTodo(ctxMaintainer, todo.ID, ModeFull)
	if err != nil {
		t.Fatalf("DeleteTodo: %v", err)
	}
}
