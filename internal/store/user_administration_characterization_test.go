package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type userAdministrationStoreFixture struct {
	store *Store
	owner User
	admin User
	user  User
}

func newUserAdministrationStoreFixture(t *testing.T) *userAdministrationStoreFixture {
	t.Helper()
	st, cleanup := newTestStore(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "owner-user-admin-store@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("bootstrap owner: %v", err)
	}
	admin, err := st.CreateUser(ctx, "admin-user-admin-store@example.com", "password123", "Admin")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := st.UpdateUserRole(ctx, owner.ID, admin.ID, SystemRoleAdmin); err != nil {
		t.Fatalf("promote admin: %v", err)
	}
	user, err := st.CreateUser(ctx, "user-user-admin-store@example.com", "password123", "User")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return &userAdministrationStoreFixture{store: st, owner: owner, admin: admin, user: user}
}

func assertUserAdministrationStoreError(t *testing.T, err, sentinel error, exact string) {
	t.Helper()
	if !errors.Is(err, sentinel) {
		t.Fatalf("error=%v want errors.Is(%v)", err, sentinel)
	}
	if exact != "" && err.Error() != exact {
		t.Fatalf("error=%q want=%q", err, exact)
	}
}

func TestUserAdministrationStoreAuthorizationValidationAndTargetPrecedence(t *testing.T) {
	t.Run("role validation precedes authority and target", func(t *testing.T) {
		fx := newUserAdministrationStoreFixture(t)
		err := fx.store.UpdateUserRole(context.Background(), fx.admin.ID, 999999, SystemRole("ADMIN"))
		assertUserAdministrationStoreError(t, err, ErrValidation, "validation: invalid system role")
	})

	t.Run("role authority precedes target existence", func(t *testing.T) {
		fx := newUserAdministrationStoreFixture(t)
		err := fx.store.UpdateUserRole(context.Background(), fx.admin.ID, 999999, SystemRoleAdmin)
		assertUserAdministrationStoreError(t, err, ErrUnauthorized, ErrUnauthorized.Error())
	})

	t.Run("authorized role mutation reaches target existence", func(t *testing.T) {
		fx := newUserAdministrationStoreFixture(t)
		err := fx.store.UpdateUserRole(context.Background(), fx.owner.ID, 999999, SystemRoleAdmin)
		assertUserAdministrationStoreError(t, err, ErrNotFound, ErrNotFound.Error())
	})

	t.Run("delete self precedes authority and target lookup", func(t *testing.T) {
		fx := newUserAdministrationStoreFixture(t)
		err := fx.store.DeleteUser(context.Background(), fx.admin.ID, fx.admin.ID)
		assertUserAdministrationStoreError(t, err, ErrValidation, "validation: cannot delete yourself")
	})

	t.Run("delete authority precedes target existence", func(t *testing.T) {
		fx := newUserAdministrationStoreFixture(t)
		err := fx.store.DeleteUser(context.Background(), fx.admin.ID, 999999)
		assertUserAdministrationStoreError(t, err, ErrUnauthorized, ErrUnauthorized.Error())
	})

	t.Run("authorized delete reaches target existence", func(t *testing.T) {
		fx := newUserAdministrationStoreFixture(t)
		err := fx.store.DeleteUser(context.Background(), fx.owner.ID, 999999)
		assertUserAdministrationStoreError(t, err, ErrNotFound, ErrNotFound.Error())
	})
}

func TestUserAdministrationStoreOwnerInvariantMatrix(t *testing.T) {
	t.Run("last owner self downgrade is rejected", func(t *testing.T) {
		fx := newUserAdministrationStoreFixture(t)
		err := fx.store.UpdateUserRole(context.Background(), fx.owner.ID, fx.owner.ID, SystemRoleAdmin)
		assertUserAdministrationStoreError(t, err, ErrValidation, "validation: cannot demote the last owner")
	})

	t.Run("last owner self delete reports self restriction first", func(t *testing.T) {
		fx := newUserAdministrationStoreFixture(t)
		err := fx.store.DeleteUser(context.Background(), fx.owner.ID, fx.owner.ID)
		assertUserAdministrationStoreError(t, err, ErrValidation, "validation: cannot delete yourself")
	})

	t.Run("self downgrade is allowed when another owner remains", func(t *testing.T) {
		fx := newUserAdministrationStoreFixture(t)
		owner2, err := fx.store.CreateUser(context.Background(), "second-owner-self-downgrade@example.com", "password123", "Owner Two")
		if err != nil {
			t.Fatalf("create second owner: %v", err)
		}
		if err := fx.store.UpdateUserRole(context.Background(), fx.owner.ID, owner2.ID, SystemRoleOwner); err != nil {
			t.Fatalf("promote second owner: %v", err)
		}
		if err := fx.store.UpdateUserRole(context.Background(), fx.owner.ID, fx.owner.ID, SystemRoleUser); err != nil {
			t.Fatalf("self downgrade with two owners: %v", err)
		}
		got, err := fx.store.GetUser(context.Background(), fx.owner.ID)
		if err != nil || got.SystemRole != SystemRoleUser {
			t.Fatalf("downgraded user=%+v err=%v", got, err)
		}
	})

	t.Run("owner may mutate and delete another owner when two exist", func(t *testing.T) {
		fx := newUserAdministrationStoreFixture(t)
		owner2, err := fx.store.CreateUser(context.Background(), "second-owner-delete@example.com", "password123", "Owner Two")
		if err != nil {
			t.Fatalf("create second owner: %v", err)
		}
		if err := fx.store.UpdateUserRole(context.Background(), fx.owner.ID, owner2.ID, SystemRoleOwner); err != nil {
			t.Fatalf("promote second owner: %v", err)
		}
		if err := fx.store.UpdateUserRole(context.Background(), fx.owner.ID, owner2.ID, SystemRoleOwner); err != nil {
			t.Fatalf("same-role owner update: %v", err)
		}
		if err := fx.store.DeleteUser(context.Background(), fx.owner.ID, owner2.ID); err != nil {
			t.Fatalf("delete second owner: %v", err)
		}
		if _, err := fx.store.GetUser(context.Background(), owner2.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted owner lookup error=%v want not found", err)
		}
	})

	t.Run("owner may promote demote and delete non-owner", func(t *testing.T) {
		fx := newUserAdministrationStoreFixture(t)
		if err := fx.store.UpdateUserRole(context.Background(), fx.owner.ID, fx.user.ID, SystemRoleAdmin); err != nil {
			t.Fatalf("promote user: %v", err)
		}
		if err := fx.store.UpdateUserRole(context.Background(), fx.owner.ID, fx.user.ID, SystemRoleUser); err != nil {
			t.Fatalf("demote user: %v", err)
		}
		if err := fx.store.DeleteUser(context.Background(), fx.owner.ID, fx.user.ID); err != nil {
			t.Fatalf("delete user: %v", err)
		}
	})
}

func TestUserAdministrationStoreDeleteCascadesAndPreservesTodoHistory(t *testing.T) {
	fx := newUserAdministrationStoreFixture(t)
	ctx := context.Background()
	project, err := fx.store.CreateProject(WithUserID(ctx, fx.owner.ID), "User Delete Cascade")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := fx.store.AddProjectMember(ctx, fx.owner.ID, project.ID, fx.user.ID, RoleMaintainer); err != nil {
		t.Fatalf("add target membership: %v", err)
	}
	if _, _, err := fx.store.CreateSession(ctx, fx.user.ID, time.Hour); err != nil {
		t.Fatalf("create target session: %v", err)
	}
	name := "delete-cascade-token"
	if _, _, _, err := fx.store.CreateUserAPIToken(ctx, fx.user.ID, &name); err != nil {
		t.Fatalf("create target API token: %v", err)
	}
	if err := fx.store.SetUserPreference(ctx, fx.user.ID, "cardsPerLane", "50"); err != nil {
		t.Fatalf("set target preference: %v", err)
	}
	todo, err := fx.store.CreateTodo(WithUserID(ctx, fx.user.ID), project.ID, CreateTodoInput{
		Title: "Historical creator survives user deletion", ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("create attributed todo: %v", err)
	}

	if err := fx.store.DeleteUser(ctx, fx.owner.ID, fx.user.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	for _, table := range []string{"users", "sessions", "api_tokens", "user_preferences", "project_members"} {
		query := "SELECT COUNT(*) FROM " + table + " WHERE user_id = ?"
		if table == "users" {
			query = "SELECT COUNT(*) FROM users WHERE id = ?"
		}
		var count int
		if err := fx.store.db.QueryRow(query, fx.user.ID).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s rows for deleted user=%d want 0", table, count)
		}
	}
	var creatorID *int64
	if err := fx.store.db.QueryRow(`SELECT created_by_user_id FROM todos WHERE id = ?`, todo.ID).Scan(&creatorID); err != nil {
		t.Fatalf("read retained todo: %v", err)
	}
	if creatorID != nil {
		t.Fatalf("retained todo creator=%v want NULL", *creatorID)
	}
}

func TestUserAdministrationStoreDeleteOwnedProjectFailureIsAtomic(t *testing.T) {
	fx := newUserAdministrationStoreFixture(t)
	ctx := context.Background()
	owner2, err := fx.store.CreateUser(ctx, "project-owner-delete@example.com", "password123", "Project Owner")
	if err != nil {
		t.Fatalf("create second owner: %v", err)
	}
	if err := fx.store.UpdateUserRole(ctx, fx.owner.ID, owner2.ID, SystemRoleOwner); err != nil {
		t.Fatalf("promote second owner: %v", err)
	}
	project, err := fx.store.CreateProject(WithUserID(ctx, owner2.ID), "Owned Project Blocks User Delete")
	if err != nil {
		t.Fatalf("create owned project: %v", err)
	}
	if _, _, err := fx.store.CreateSession(ctx, owner2.ID, time.Hour); err != nil {
		t.Fatalf("create session: %v", err)
	}

	err = fx.store.DeleteUser(ctx, fx.owner.ID, owner2.ID)
	if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Fatalf("delete owned-project user error=%v want foreign-key failure", err)
	}
	if _, err := fx.store.GetUser(ctx, owner2.ID); err != nil {
		t.Fatalf("user missing after rolled-back delete: %v", err)
	}
	if _, err := fx.store.GetProject(ctx, project.ID); err != nil {
		t.Fatalf("project missing after rolled-back delete: %v", err)
	}
	var sessions int
	if err := fx.store.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, owner2.ID).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions == 0 {
		t.Fatal("session cascade was not rolled back with failed user delete")
	}
}

func TestUserAdministrationStoreCreateUserSeedFailureRollsBackUser(t *testing.T) {
	fx := newUserAdministrationStoreFixture(t)
	ctx := context.Background()
	project, err := fx.store.CreateProject(WithUserID(ctx, fx.owner.ID), "Default Board Seed Rollback")
	if err != nil {
		t.Fatalf("create default board: %v", err)
	}
	if err := fx.store.SetDefaultBoardOrgSetting(ctx, fx.owner.ID, project.ID, RoleViewer); err != nil {
		t.Fatalf("set default board: %v", err)
	}
	if _, err := fx.store.db.Exec(`
CREATE TRIGGER fail_characterized_default_membership
BEFORE INSERT ON project_members
BEGIN
  SELECT RAISE(FAIL, 'forced default membership seed failure');
END`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	_, err = fx.store.CreateUser(ctx, "rolled-back-create@example.com", "password123", "Rolled Back")
	if err == nil || !strings.Contains(err.Error(), "forced default membership seed failure") {
		t.Fatalf("CreateUser error=%v want forced seed failure", err)
	}
	var users, prefs int
	if err := fx.store.db.QueryRow(`SELECT COUNT(*) FROM users WHERE email = ?`, "rolled-back-create@example.com").Scan(&users); err != nil {
		t.Fatalf("count rolled-back users: %v", err)
	}
	if err := fx.store.db.QueryRow(`
SELECT COUNT(*) FROM user_preferences up
JOIN users u ON u.id = up.user_id
WHERE u.email = ?`, "rolled-back-create@example.com").Scan(&prefs); err != nil {
		t.Fatalf("count rolled-back preferences: %v", err)
	}
	if users != 0 || prefs != 0 {
		t.Fatalf("failed CreateUser left rows users=%d preferences=%d", users, prefs)
	}
}
