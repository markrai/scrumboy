package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sync"
	"testing"
)

func projectLifecycleCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count lifecycle rows: %v", err)
	}
	return count
}

func TestProjectLifecycleStoreDurableCreationContracts(t *testing.T) {
	t.Run("pre-bootstrap defaults and custom workflow normalization", func(t *testing.T) {
		st, sqlDB, cleanup := newTestStoreWithSQL(t)
		defer cleanup()

		ctx := context.Background()
		project, err := st.CreateProject(ctx, "  Lifecycle Project  ")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		if project.Name != "Lifecycle Project" || project.Slug != "lifecycle-project" {
			t.Fatalf("project normalization = %+v", project)
		}
		if project.OwnerUserID != nil || project.ExpiresAt != nil {
			t.Fatalf("pre-bootstrap durable identity = %+v", project)
		}
		if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM project_workflow_columns WHERE project_id = ?`, project.ID); got != 5 {
			t.Fatalf("default workflow count=%d want=5", got)
		}
		if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM project_priorities WHERE project_id = ?`, project.ID); got != 4 {
			t.Fatalf("default priority count=%d want=4", got)
		}
		if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action = 'project_created' AND actor_user_id IS NULL`, project.ID); got != 1 {
			t.Fatalf("anonymous creation audit count=%d want=1", got)
		}

		workflow := []WorkflowColumn{
			{Key: "  TODO  ", Name: "  Ready  ", Color: "  ", Position: 91, IsDone: false, System: true},
			{Key: " DONE ", Name: " Complete ", Color: " #abcdef ", Position: 7, IsDone: true, System: true},
		}
		custom, err := st.CreateProjectWithWorkflow(ctx, "Custom Lifecycle", workflow)
		if err != nil {
			t.Fatalf("CreateProjectWithWorkflow: %v", err)
		}
		wantMutated := []WorkflowColumn{
			{Key: "todo", Name: "Ready", Color: "#64748b", Position: 0, IsDone: false, System: false},
			{Key: "done", Name: "Complete", Color: "#abcdef", Position: 1, IsDone: true, System: false},
		}
		if !reflect.DeepEqual(workflow, wantMutated) {
			t.Fatalf("caller workflow after store normalization=%+v want=%+v", workflow, wantMutated)
		}
		persisted, err := st.GetProjectWorkflow(ctx, custom.ID)
		if err != nil {
			t.Fatalf("GetProjectWorkflow: %v", err)
		}
		if len(persisted) != 2 || persisted[0].Key != "todo" || persisted[0].Position != 0 || persisted[0].System || persisted[1].Key != "done" || persisted[1].Position != 1 || persisted[1].System {
			t.Fatalf("persisted custom workflow=%+v", persisted)
		}

		before := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM projects`)
		if _, err := st.CreateProjectWithWorkflow(ctx, "Empty Custom", []WorkflowColumn{}); !errors.Is(err, ErrValidation) {
			t.Fatalf("empty custom workflow error=%v want ErrValidation", err)
		}
		if after := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM projects`); after != before {
			t.Fatalf("empty custom workflow persisted project count %d->%d", before, after)
		}
	})

	t.Run("authenticated actor owns project and membership", func(t *testing.T) {
		st, sqlDB, cleanup := newTestStoreWithSQL(t)
		defer cleanup()

		ctx := context.Background()
		owner, err := st.BootstrapUser(ctx, "lifecycle-owner@example.com", "password123", "Owner")
		if err != nil {
			t.Fatalf("BootstrapUser: %v", err)
		}
		project, err := st.CreateProject(WithUserID(ctx, owner.ID), "Owned Lifecycle")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		if project.OwnerUserID == nil || *project.OwnerUserID != owner.ID {
			t.Fatalf("owner projection=%+v want=%d", project.OwnerUserID, owner.ID)
		}
		var role ProjectRole
		if err := sqlDB.QueryRow(`SELECT role FROM project_members WHERE project_id = ? AND user_id = ?`, project.ID, owner.ID).Scan(&role); err != nil {
			t.Fatalf("read owner membership: %v", err)
		}
		if role != RoleMaintainer {
			t.Fatalf("owner role=%q want=%q", role, RoleMaintainer)
		}
		if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action = 'project_created' AND actor_user_id = ?`, project.ID, owner.ID); got != 1 {
			t.Fatalf("actor creation audit count=%d want=1", got)
		}
	})
}

func TestProjectLifecycleStoreDurableCreationRollsBackAllState(t *testing.T) {
	st, sqlDB, cleanup := newTestStoreWithSQL(t)
	defer cleanup()

	ctx := context.Background()
	if _, err := sqlDB.ExecContext(ctx, `CREATE TRIGGER lifecycle_reject_durable_priority BEFORE INSERT ON project_priorities BEGIN SELECT RAISE(ABORT, 'reject durable priority'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	tables := []string{"projects", "project_members", "project_workflow_columns", "project_priorities", "audit_events"}
	before := make(map[string]int, len(tables))
	for _, table := range tables {
		before[table] = projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM `+table)
	}
	if _, err := st.CreateProject(ctx, "Rollback Lifecycle"); err == nil {
		t.Fatal("CreateProject should fail when priority seed is rejected")
	}
	for _, table := range tables {
		if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM `+table); got != before[table] {
			t.Fatalf("%s count=%d want=%d after rolled-back durable creation", table, got, before[table])
		}
	}
}

func TestProjectLifecycleStoreAnonymousCreationCommitBoundaries(t *testing.T) {
	t.Run("authenticated creator is persisted but omitted from returned project", func(t *testing.T) {
		st, sqlDB, cleanup := newTestStoreWithSQL(t)
		defer cleanup()

		ctx := context.Background()
		creator, err := st.BootstrapUser(ctx, "temporary-creator@example.com", "password123", "Creator")
		if err != nil {
			t.Fatalf("BootstrapUser: %v", err)
		}
		returned, err := st.CreateAnonymousBoard(WithUserID(ctx, creator.ID))
		if err != nil {
			t.Fatalf("CreateAnonymousBoard: %v", err)
		}
		if returned.CreatorUserID != nil {
			t.Fatalf("returned CreatorUserID=%+v want nil compatibility projection", returned.CreatorUserID)
		}
		var persistedCreator sql.NullInt64
		if err := sqlDB.QueryRow(`SELECT creator_user_id FROM projects WHERE id = ?`, returned.ID).Scan(&persistedCreator); err != nil {
			t.Fatalf("read persisted creator: %v", err)
		}
		if !persistedCreator.Valid || persistedCreator.Int64 != creator.ID {
			t.Fatalf("persisted creator=%+v want=%d", persistedCreator, creator.ID)
		}
		if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM project_members WHERE project_id = ?`, returned.ID); got != 0 {
			t.Fatalf("temporary creator membership count=%d want=0", got)
		}
		if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM tags WHERE project_id = ?`, returned.ID); got != 0 {
			t.Fatalf("creator Temporary Board default tags=%d want=0", got)
		}
		if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action = 'project_created' AND actor_user_id = ?`, returned.ID, creator.ID); got != 1 {
			t.Fatalf("temporary creation audit count=%d want=1", got)
		}
	})

	t.Run("default tag failure is ignored after primary commit", func(t *testing.T) {
		st, sqlDB, cleanup := newTestStoreWithSQL(t)
		defer cleanup()

		if _, err := sqlDB.Exec(`CREATE TRIGGER lifecycle_reject_default_tags BEFORE INSERT ON tags BEGIN SELECT RAISE(ABORT, 'reject default tags'); END`); err != nil {
			t.Fatalf("create tag trigger: %v", err)
		}
		project, err := st.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatalf("CreateAnonymousBoard should ignore tag failure: %v", err)
		}
		if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM tags WHERE project_id = ?`, project.ID); got != 0 {
			t.Fatalf("tags after ignored failure=%d want=0", got)
		}
		if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM project_workflow_columns WHERE project_id = ?`, project.ID); got != 5 {
			t.Fatalf("workflow after ignored tag failure=%d want=5", got)
		}
	})

	t.Run("workflow failure returns after commit and leaves partial initialization", func(t *testing.T) {
		st, sqlDB, cleanup := newTestStoreWithSQL(t)
		defer cleanup()

		if _, err := sqlDB.Exec(`CREATE TRIGGER lifecycle_reject_doing_workflow BEFORE INSERT ON project_workflow_columns WHEN NEW.key = 'doing' BEGIN SELECT RAISE(ABORT, 'reject doing workflow'); END`); err != nil {
			t.Fatalf("create workflow trigger: %v", err)
		}
		if _, err := st.CreateAnonymousBoard(context.Background()); err == nil {
			t.Fatal("CreateAnonymousBoard should report late workflow failure")
		}
		var projectID int64
		if err := sqlDB.QueryRow(`SELECT id FROM projects WHERE name = 'Anonymous Board' ORDER BY id DESC LIMIT 1`).Scan(&projectID); err != nil {
			t.Fatalf("committed anonymous project missing after workflow failure: %v", err)
		}
		if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM project_priorities WHERE project_id = ?`, projectID); got != 4 {
			t.Fatalf("committed priorities=%d want=4", got)
		}
		if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action = 'project_created'`, projectID); got != 1 {
			t.Fatalf("committed creation audits=%d want=1", got)
		}
		if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM tags WHERE project_id = ?`, projectID); got == 0 {
			t.Fatal("default tags should have committed before workflow failure")
		}
		var keys []string
		rows, err := sqlDB.Query(`SELECT key FROM project_workflow_columns WHERE project_id = ? ORDER BY position`, projectID)
		if err != nil {
			t.Fatalf("query partial workflow: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				t.Fatalf("scan workflow key: %v", err)
			}
			keys = append(keys, key)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate workflow keys: %v", err)
		}
		if !reflect.DeepEqual(keys, []string{DefaultColumnBacklog, DefaultColumnNotStarted}) {
			t.Fatalf("partial workflow keys=%v want=[%s %s]", keys, DefaultColumnBacklog, DefaultColumnNotStarted)
		}
	})
}

func TestProjectLifecycleStorePatchRollbackAndSameValueAudits(t *testing.T) {
	st, sqlDB, cleanup := newTestStoreWithSQL(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "patch-owner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "Patch Original")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	name := project.Name
	weeks := project.DefaultSprintWeeks
	if err := st.UpdateProjectPatch(ownerCtx, project.ID, owner.ID, UpdateProjectPatch{Name: &name, DefaultSprintWeeks: &weeks}); err != nil {
		t.Fatalf("same-value UpdateProjectPatch: %v", err)
	}
	if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action IN ('project_renamed','project_default_sprint_weeks_updated')`, project.ID); got != 2 {
		t.Fatalf("same-value patch audit count=%d want=2", got)
	}

	if _, err := sqlDB.Exec(`CREATE TRIGGER lifecycle_reject_patch_weeks_audit BEFORE INSERT ON audit_events WHEN NEW.action = 'project_default_sprint_weeks_updated' BEGIN SELECT RAISE(ABORT, 'reject weeks audit'); END`); err != nil {
		t.Fatalf("create patch audit trigger: %v", err)
	}
	changedName := "Patch Should Roll Back"
	changedWeeks := 1
	if err := st.UpdateProjectPatch(ownerCtx, project.ID, owner.ID, UpdateProjectPatch{Name: &changedName, DefaultSprintWeeks: &changedWeeks}); err == nil {
		t.Fatal("UpdateProjectPatch should fail at second audit")
	}
	got, err := st.GetProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.Name != project.Name || got.DefaultSprintWeeks != project.DefaultSprintWeeks {
		t.Fatalf("patch partially committed: got name=%q weeks=%d", got.Name, got.DefaultSprintWeeks)
	}
	if audits := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action IN ('project_renamed','project_default_sprint_weeks_updated')`, project.ID); audits != 2 {
		t.Fatalf("failed patch audit count=%d want existing 2", audits)
	}
}

func TestProjectLifecycleStoreDeletionRollbackAndSnapshot(t *testing.T) {
	st, sqlDB, cleanup := newTestStoreWithSQL(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "delete-owner-lifecycle@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	member, err := st.CreateUser(ctx, "delete-member-lifecycle@example.com", "password123", "Member")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "Deletion Lifecycle")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := st.AddProjectMember(ownerCtx, owner.ID, project.ID, member.ID, RoleViewer); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}
	if _, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "Cascade Me", ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	if _, err := sqlDB.Exec(`CREATE TRIGGER lifecycle_reject_project_delete BEFORE DELETE ON projects BEGIN SELECT RAISE(ABORT, 'reject project delete'); END`); err != nil {
		t.Fatalf("create delete trigger: %v", err)
	}
	if _, err := st.DeleteProject(ownerCtx, project.ID, owner.ID); err == nil {
		t.Fatal("DeleteProject should fail when project delete is rejected")
	}
	if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM projects WHERE id = ?`, project.ID); got != 1 {
		t.Fatalf("project count after rollback=%d want=1", got)
	}
	if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM todos WHERE project_id = ?`, project.ID); got != 1 {
		t.Fatalf("todo count after rollback=%d want=1", got)
	}
	if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM project_members WHERE project_id = ?`, project.ID); got != 2 {
		t.Fatalf("member count after rollback=%d want=2", got)
	}
	if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action = 'project_deleted'`, project.ID); got != 0 {
		t.Fatalf("rolled-back deletion audits=%d want=0", got)
	}

	if _, err := sqlDB.Exec(`DROP TRIGGER lifecycle_reject_project_delete`); err != nil {
		t.Fatalf("drop delete trigger: %v", err)
	}
	snapshot, err := st.DeleteProject(ownerCtx, project.ID, owner.ID)
	if err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if snapshot.ProjectID != project.ID || snapshot.Name != project.Name || !reflect.DeepEqual(snapshot.MemberUserIDs, []int64{owner.ID, member.ID}) {
		t.Fatalf("deletion snapshot=%+v", snapshot)
	}
	if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM projects WHERE id = ?`, project.ID); got != 0 {
		t.Fatalf("project count after commit=%d want=0", got)
	}
	if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM todos WHERE project_id = ?`, project.ID); got != 0 {
		t.Fatalf("todo count after cascade=%d want=0", got)
	}
	if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action = 'project_deleted'`, project.ID); got != 1 {
		t.Fatalf("surviving deletion audits=%d want=1", got)
	}
}

func TestProjectLifecycleStoreClaimConditionalAuthorityAndRollback(t *testing.T) {
	t.Run("membership failure rolls back conversion", func(t *testing.T) {
		st, sqlDB, cleanup := newTestStoreWithSQL(t)
		defer cleanup()

		ctx := context.Background()
		creator, err := st.BootstrapUser(ctx, "claim-rollback@example.com", "password123", "Creator")
		if err != nil {
			t.Fatalf("BootstrapUser: %v", err)
		}
		board, err := st.CreateAnonymousBoard(WithUserID(ctx, creator.ID))
		if err != nil {
			t.Fatalf("CreateAnonymousBoard: %v", err)
		}
		if _, err := sqlDB.Exec(`CREATE TRIGGER lifecycle_reject_claim_membership BEFORE INSERT ON project_members BEGIN SELECT RAISE(ABORT, 'reject claim membership'); END`); err != nil {
			t.Fatalf("create membership trigger: %v", err)
		}
		if err := st.ClaimTemporaryBoard(ctx, board.ID, creator.ID); err == nil {
			t.Fatal("ClaimTemporaryBoard should fail when membership insert fails")
		}
		var owner, expires sql.NullInt64
		if err := sqlDB.QueryRow(`SELECT owner_user_id, expires_at FROM projects WHERE id = ?`, board.ID).Scan(&owner, &expires); err != nil {
			t.Fatalf("read board after rollback: %v", err)
		}
		if owner.Valid || !expires.Valid {
			t.Fatalf("claim conversion partially committed owner=%+v expires=%+v", owner, expires)
		}
		if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM project_members WHERE project_id = ?`, board.ID); got != 0 {
			t.Fatalf("membership count after rollback=%d want=0", got)
		}
	})

	t.Run("concurrent attempts have exactly one conditional winner", func(t *testing.T) {
		st, sqlDB, cleanup := newTestStoreWithSQL(t)
		defer cleanup()

		ctx := context.Background()
		creator, err := st.BootstrapUser(ctx, "claim-race@example.com", "password123", "Creator")
		if err != nil {
			t.Fatalf("BootstrapUser: %v", err)
		}
		board, err := st.CreateAnonymousBoard(WithUserID(ctx, creator.ID))
		if err != nil {
			t.Fatalf("CreateAnonymousBoard: %v", err)
		}
		auditsBefore := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM audit_events WHERE project_id = ?`, board.ID)
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		for i := 0; i < 2; i++ {
			go func() {
				defer wg.Done()
				<-start
				results <- st.ClaimTemporaryBoard(ctx, board.ID, creator.ID)
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		var success, notFound int
		for err := range results {
			switch {
			case err == nil:
				success++
			case errors.Is(err, ErrNotFound):
				notFound++
			default:
				t.Fatalf("unexpected concurrent claim error: %v", err)
			}
		}
		if success != 1 || notFound != 1 {
			t.Fatalf("concurrent results success=%d notFound=%d want=1/1", success, notFound)
		}
		var owner, expires sql.NullInt64
		if err := sqlDB.QueryRow(`SELECT owner_user_id, expires_at FROM projects WHERE id = ?`, board.ID).Scan(&owner, &expires); err != nil {
			t.Fatalf("read claimed board: %v", err)
		}
		if !owner.Valid || owner.Int64 != creator.ID || expires.Valid {
			t.Fatalf("claimed project state owner=%+v expires=%+v", owner, expires)
		}
		if got := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM project_members WHERE project_id = ? AND user_id = ? AND role = 'maintainer'`, board.ID, creator.ID); got != 1 {
			t.Fatalf("maintainer membership count=%d want=1", got)
		}
		if auditsAfter := projectLifecycleCount(t, sqlDB, `SELECT COUNT(*) FROM audit_events WHERE project_id = ?`, board.ID); auditsAfter != auditsBefore {
			t.Fatalf("claim added audit rows %d->%d; current contract has no distinct claim audit", auditsBefore, auditsAfter)
		}
	})
}
