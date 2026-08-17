package store

import (
	"context"
	"database/sql"
	"testing"
)

func assertTodoCreator(t *testing.T, todo Todo, want *int64) {
	t.Helper()
	if want == nil {
		if todo.CreatedByUserID != nil {
			t.Fatalf("CreatedByUserID = %v, want nil", *todo.CreatedByUserID)
		}
		return
	}
	if todo.CreatedByUserID == nil || *todo.CreatedByUserID != *want {
		t.Fatalf("CreatedByUserID = %v, want %d", todo.CreatedByUserID, *want)
	}
}

func TestTodoCreatorAttributionCreationSemantics(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "creator-attribution@example.com", "password123", "Creator")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)

	t.Run("authenticated durable creation records actor", func(t *testing.T) {
		project, err := st.CreateProject(ownerCtx, "Creator attribution durable")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		created, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "durable"}, ModeFull)
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
		assertTodoCreator(t, created, &owner.ID)

		read, err := st.GetTodoByLocalID(ownerCtx, project.ID, created.LocalID, ModeFull)
		if err != nil {
			t.Fatalf("GetTodoByLocalID: %v", err)
		}
		assertTodoCreator(t, read, &owner.ID)
	})

	t.Run("authenticated temporary creation records actor", func(t *testing.T) {
		project, err := st.CreateAnonymousBoard(ownerCtx)
		if err != nil {
			t.Fatalf("CreateAnonymousBoard: %v", err)
		}
		created, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "authenticated temporary"}, ModeFull)
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
		assertTodoCreator(t, created, &owner.ID)
	})

	t.Run("unauthenticated temporary link holder has no creator", func(t *testing.T) {
		project, err := st.CreateAnonymousBoard(ownerCtx)
		if err != nil {
			t.Fatalf("CreateAnonymousBoard: %v", err)
		}
		created, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "temporary link holder"}, ModeFull)
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
		assertTodoCreator(t, created, nil)
	})

	t.Run("anonymous board creation has no creator", func(t *testing.T) {
		project, err := st.CreateAnonymousBoard(ctx)
		if err != nil {
			t.Fatalf("CreateAnonymousBoard: %v", err)
		}
		created, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "anonymous"}, ModeAnonymous)
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
		assertTodoCreator(t, created, nil)
	})
}

func TestTodoCreatorAttributionIsHistoricalNotMembership(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "creator-history-owner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	creator, err := st.CreateUser(ctx, "creator-history-member@example.com", "password123", "Former member")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "Creator history")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := st.AddProjectMember(ownerCtx, owner.ID, project.ID, creator.ID, RoleMaintainer); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}
	created, err := st.CreateTodo(WithUserID(ctx, creator.ID), project.ID, CreateTodoInput{Title: "historical"}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if err := st.RemoveProjectMember(ownerCtx, owner.ID, project.ID, creator.ID); err != nil {
		t.Fatalf("RemoveProjectMember: %v", err)
	}

	read, err := st.GetTodoByLocalID(ownerCtx, project.ID, created.LocalID, ModeFull)
	if err != nil {
		t.Fatalf("GetTodoByLocalID: %v", err)
	}
	assertTodoCreator(t, read, &creator.ID)
	if role, err := st.GetProjectRole(ctx, project.ID, creator.ID); err != nil || role != "" {
		t.Fatalf("former creator role = %q, err=%v; attribution must not imply membership", role, err)
	}
}

func TestTodoCreatorAttributionNullsWhenUserIsDeleted(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "creator-delete-owner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	creator, err := st.CreateUser(ctx, "creator-delete-target@example.com", "password123", "Creator")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "Creator deletion")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := st.AddProjectMember(ownerCtx, owner.ID, project.ID, creator.ID, RoleMaintainer); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}
	created, err := st.CreateTodo(WithUserID(ctx, creator.ID), project.ID, CreateTodoInput{Title: "survives creator"}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	if err := st.DeleteUser(ownerCtx, owner.ID, creator.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	read, err := st.GetTodoByLocalID(ownerCtx, project.ID, created.LocalID, ModeFull)
	if err != nil {
		t.Fatalf("GetTodoByLocalID: %v", err)
	}
	assertTodoCreator(t, read, nil)

	var stored sql.NullInt64
	if err := st.db.QueryRowContext(ctx, `SELECT created_by_user_id FROM todos WHERE id = ?`, created.ID).Scan(&stored); err != nil {
		t.Fatalf("read created_by_user_id: %v", err)
	}
	if stored.Valid {
		t.Fatalf("created_by_user_id remained %d after user deletion", stored.Int64)
	}
}

func TestTodoCreatorAttributionAllowsHistoricalNull(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "creator-null@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "Creator null")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	created, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "pre-migration stand-in"}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE todos SET created_by_user_id = NULL WHERE id = ?`, created.ID); err != nil {
		t.Fatalf("clear created_by_user_id: %v", err)
	}
	read, err := st.GetTodoByLocalID(ownerCtx, project.ID, created.LocalID, ModeFull)
	if err != nil {
		t.Fatalf("GetTodoByLocalID: %v", err)
	}
	assertTodoCreator(t, read, nil)
}
