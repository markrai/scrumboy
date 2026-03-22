package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"scrumboy/internal/version"
)

func TestImportMergeUpdate_AnonymousMode_NoHang(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	now := time.Now().UTC()
	data := &ExportData{
		Version:    version.ExportFormatVersion,
		ExportedAt: now,
		Mode:       "anonymous",
		Scope:      "project",
		Projects: []ProjectExport{
			{
				Slug:      "anon-import-source",
				Name:      "Anon Import Source",
				ExpiresAt: &now,
				CreatedAt: now,
				UpdatedAt: now,
				Todos: []TodoExport{
					{
						LocalID:   1,
						Title:     "anon todo",
						Body:      "",
						Status:    "BACKLOG",
						Rank:      1000,
						CreatedAt: now,
						UpdatedAt: now,
					},
				},
			},
		},
	}

	done := make(chan error, 1)
	go func() {
		_, err := st.ImportProjects(context.Background(), data, ModeAnonymous, "merge")
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ImportProjects merge anonymous: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ImportProjects merge anonymous timed out")
	}
}

func TestImportMergeUpdate_PreservesExistingAssignee(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "owner-import@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)

	project, err := st.CreateProject(ctxOwner, "Merge Preserve Assignee")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	contributor, err := st.CreateUser(ctx, "contrib-import@example.com", "password", "Contributor")
	if err != nil {
		t.Fatalf("CreateUser contributor: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, owner.ID, project.ID, contributor.ID, RoleContributor); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}

	todo, err := st.CreateTodo(ctxOwner, project.ID, CreateTodoInput{
		Title:  "before merge",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	_, err = st.UpdateTodo(ctxOwner, todo.ID, UpdateTodoInput{
		Title:          todo.Title,
		Body:           todo.Body,
		Tags:           todo.Tags,
		AssigneeUserID: &contributor.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodo assign contributor: %v", err)
	}

	now := time.Now().UTC()
	data := &ExportData{
		Version:    version.ExportFormatVersion,
		ExportedAt: now,
		Mode:       "full",
		Scope:      "full",
		Projects: []ProjectExport{
			{
				Slug:      project.Slug,
				Name:      project.Name,
				CreatedAt: now,
				UpdatedAt: now,
				Todos: []TodoExport{
					{
						LocalID:        todo.LocalID,
						Title:          "after merge",
						Body:           "updated",
						Status:         "BACKLOG",
						Rank:           todo.Rank,
						AssigneeUserId: nil,
						CreatedAt:      now,
						UpdatedAt:      now,
					},
				},
			},
		},
	}

	if _, err := st.ImportProjects(ctxOwner, data, ModeFull, "merge"); err != nil {
		t.Fatalf("ImportProjects merge: %v", err)
	}

	got, err := st.GetTodoByLocalID(ctxOwner, project.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatalf("GetTodoByLocalID: %v", err)
	}
	if got.AssigneeUserID == nil || *got.AssigneeUserID != contributor.ID {
		t.Fatalf("expected assignee to remain contributor %d, got %#v", contributor.ID, got.AssigneeUserID)
	}
}

func TestImportCopy_PreservesAssigneeWhenMember(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "owner-copy@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)

	now := time.Now().UTC()
	data := &ExportData{
		Version:    version.ExportFormatVersion,
		ExportedAt: now,
		Mode:       "full",
		Scope:      "full",
		Projects: []ProjectExport{
			{
				Slug:      "copy-assignee-source",
				Name:      "Copy Assignee Source",
				CreatedAt: now,
				UpdatedAt: now,
				Todos: []TodoExport{
					{
						LocalID:        1,
						Title:          "copy-todo-assignee",
						Body:           "",
						Status:         "BACKLOG",
						Rank:           1000,
						AssigneeUserId: &owner.ID,
						CreatedAt:      now,
						UpdatedAt:      now,
					},
				},
			},
		},
	}

	if _, err := st.ImportProjects(ctxOwner, data, ModeFull, "copy"); err != nil {
		t.Fatalf("ImportProjects copy: %v", err)
	}

	var assignee sql.NullInt64
	if err := st.db.QueryRowContext(ctx, `
		SELECT t.assignee_user_id
		FROM todos t
		WHERE t.title = ?
		ORDER BY t.id DESC
		LIMIT 1
	`, "copy-todo-assignee").Scan(&assignee); err != nil {
		t.Fatalf("query imported todo assignee: %v", err)
	}
	if !assignee.Valid || assignee.Int64 != owner.ID {
		val := int64(0)
		if assignee.Valid {
			val = assignee.Int64
		}
		t.Fatalf("expected imported todo assignee_user_id %d (owner is project member), got valid=%v value=%d", owner.ID, assignee.Valid, val)
	}
}
