package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type publicationFixture struct {
	store   *Store
	db      *sql.DB
	cleanup func()
	ctx     context.Context
	owner   User
	project Project
}

func newPublicationFixture(t *testing.T) *publicationFixture {
	t.Helper()
	st, sqlDB, cleanup := newTestStoreWithSQL(t)
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "publication-owner@example.com", "password123", "Owner")
	if err != nil {
		cleanup()
		t.Fatalf("BootstrapUser: %v", err)
	}
	project, err := st.CreateProject(WithUserID(ctx, owner.ID), "Ignite")
	if err != nil {
		cleanup()
		t.Fatalf("CreateProject: %v", err)
	}
	return &publicationFixture{store: st, db: sqlDB, cleanup: cleanup, ctx: ctx, owner: owner, project: project}
}

func publicationAuditCount(t *testing.T, sqlDB *sql.DB, projectID int64) int {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(`
SELECT COUNT(*) FROM audit_events
WHERE project_id = ? AND action IN ('project_public_viewing_enabled', 'project_public_viewing_disabled')`, projectID).Scan(&count); err != nil {
		t.Fatalf("count publication audits: %v", err)
	}
	return count
}

func projectPublicViewingValue(t *testing.T, sqlDB *sql.DB, projectID int64) int {
	t.Helper()
	var enabled int
	if err := sqlDB.QueryRow(`SELECT public_view_enabled FROM projects WHERE id = ?`, projectID).Scan(&enabled); err != nil {
		t.Fatalf("read project publication value: %v", err)
	}
	return enabled
}

func TestUpdateProjectPublicViewingTransitionsAndAudit(t *testing.T) {
	fx := newPublicationFixture(t)
	defer fx.cleanup()
	var originalUpdatedAt, originalLastActivityAt int64
	if err := fx.db.QueryRow(`SELECT updated_at, last_activity_at FROM projects WHERE id = ?`, fx.project.ID).Scan(&originalUpdatedAt, &originalLastActivityAt); err != nil {
		t.Fatalf("read original project timestamps: %v", err)
	}

	transitions := []struct {
		name        string
		enabled     bool
		wantChanged bool
		wantAudits  int
	}{
		{name: "private to public", enabled: true, wantChanged: true, wantAudits: 1},
		{name: "public to public", enabled: true, wantAudits: 1},
		{name: "public to private", enabled: false, wantChanged: true, wantAudits: 2},
		{name: "private to private", enabled: false, wantAudits: 2},
	}
	for _, tc := range transitions {
		t.Run(tc.name, func(t *testing.T) {
			state, err := fx.store.UpdateProjectPublicViewing(fx.ctx, fx.project.ID, fx.owner.ID, tc.enabled)
			if err != nil {
				t.Fatalf("UpdateProjectPublicViewing: %v", err)
			}
			if state.ProjectID != fx.project.ID || state.Slug != fx.project.Slug || state.Enabled != tc.enabled || state.Changed != tc.wantChanged {
				t.Fatalf("state = %+v", state)
			}
			if got := publicationAuditCount(t, fx.db, fx.project.ID); got != tc.wantAudits {
				t.Fatalf("publication audits = %d, want %d", got, tc.wantAudits)
			}
		})
	}

	rows, err := fx.db.Query(`
SELECT actor_user_id, action, target_type, target_id, metadata
FROM audit_events
WHERE project_id = ? AND action IN ('project_public_viewing_enabled', 'project_public_viewing_disabled')
ORDER BY id`, fx.project.ID)
	if err != nil {
		t.Fatalf("query publication audits: %v", err)
	}
	defer rows.Close()
	wantActions := []string{"project_public_viewing_enabled", "project_public_viewing_disabled"}
	for i := 0; rows.Next(); i++ {
		var actor, targetID int64
		var action, targetType, metadataRaw string
		if err := rows.Scan(&actor, &action, &targetType, &targetID, &metadataRaw); err != nil {
			t.Fatalf("scan publication audit: %v", err)
		}
		if i >= len(wantActions) || actor != fx.owner.ID || action != wantActions[i] || targetType != "project" || targetID != fx.project.ID {
			t.Fatalf("audit %d = actor:%d action:%q target:%q/%d", i, actor, action, targetType, targetID)
		}
		var metadata struct {
			FromEnabled bool   `json:"from_enabled"`
			ToEnabled   bool   `json:"to_enabled"`
			Slug        string `json:"slug"`
		}
		if err := json.Unmarshal([]byte(metadataRaw), &metadata); err != nil {
			t.Fatalf("decode publication audit metadata: %v", err)
		}
		if metadata.Slug != fx.project.Slug || metadata.FromEnabled == metadata.ToEnabled || metadata.ToEnabled != (i == 0) {
			t.Fatalf("audit %d metadata = %+v", i, metadata)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("publication audit rows: %v", err)
	}
	var updatedAt, lastActivityAt int64
	if err := fx.db.QueryRow(`SELECT updated_at, last_activity_at FROM projects WHERE id = ?`, fx.project.ID).Scan(&updatedAt, &lastActivityAt); err != nil {
		t.Fatalf("read final project timestamps: %v", err)
	}
	if updatedAt != originalUpdatedAt || lastActivityAt != originalLastActivityAt {
		t.Fatalf("publication changed project timestamps: updated %d->%d activity %d->%d", originalUpdatedAt, updatedAt, originalLastActivityAt, lastActivityAt)
	}
}

func TestReservedPublicProjectSlugSet(t *testing.T) {
	for _, slug := range []string{"_app", "agora", "anon", "api", "auth", "dashboard", "healthz", "mcp", "oauth", "p", "temp", "en", "fr", "zh", "pseudo"} {
		if !isReservedPublicProjectSlug(slug) {
			t.Fatalf("slug %q should be reserved", slug)
		}
	}
	for _, slug := range []string{"ignite", "roadmap", "public-roadmap"} {
		if isReservedPublicProjectSlug(slug) {
			t.Fatalf("slug %q should remain publishable", slug)
		}
	}
}

func TestUpdateProjectPublicViewingRequiresCurrentMaintainerMembership(t *testing.T) {
	fx := newPublicationFixture(t)
	defer fx.cleanup()

	contributor, err := fx.store.CreateUser(fx.ctx, "publication-contributor@example.com", "password123", "Contributor")
	if err != nil {
		t.Fatalf("CreateUser contributor: %v", err)
	}
	viewer, err := fx.store.CreateUser(fx.ctx, "publication-viewer@example.com", "password123", "Viewer")
	if err != nil {
		t.Fatalf("CreateUser viewer: %v", err)
	}
	nonmember, err := fx.store.CreateUser(fx.ctx, "publication-nonmember@example.com", "password123", "Nonmember")
	if err != nil {
		t.Fatalf("CreateUser nonmember: %v", err)
	}
	if err := fx.store.AddProjectMember(fx.ctx, fx.owner.ID, fx.project.ID, contributor.ID, RoleContributor); err != nil {
		t.Fatalf("AddProjectMember contributor: %v", err)
	}
	if err := fx.store.AddProjectMember(fx.ctx, fx.owner.ID, fx.project.ID, viewer.ID, RoleViewer); err != nil {
		t.Fatalf("AddProjectMember viewer: %v", err)
	}

	for _, tc := range []struct {
		name    string
		actorID int64
		wantErr error
	}{
		{name: "missing actor", actorID: 0, wantErr: ErrUnauthorized},
		{name: "contributor", actorID: contributor.ID, wantErr: ErrForbidden},
		{name: "viewer", actorID: viewer.ID, wantErr: ErrForbidden},
		{name: "authenticated nonmember", actorID: nonmember.ID, wantErr: ErrNotFound},
		{name: "nonexistent user", actorID: 999999, wantErr: ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := fx.store.UpdateProjectPublicViewing(fx.ctx, fx.project.ID, tc.actorID, true); !errors.Is(err, tc.wantErr) {
				t.Fatalf("UpdateProjectPublicViewing error = %v, want %v", err, tc.wantErr)
			}
			if got := projectPublicViewingValue(t, fx.db, fx.project.ID); got != 0 {
				t.Fatalf("public_view_enabled = %d, want 0", got)
			}
		})
	}
}

func TestUpdateProjectPublicViewingDoesNotUseBootstrapOrSystemRoleBypass(t *testing.T) {
	t.Run("pre-bootstrap bypass is rejected", func(t *testing.T) {
		st, sqlDB, cleanup := newTestStoreWithSQL(t)
		defer cleanup()
		project, err := st.CreateProject(context.Background(), "Pre Bootstrap")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		if _, err := st.UpdateProjectPublicViewing(context.Background(), project.ID, 1, true); !errors.Is(err, ErrNotFound) {
			t.Fatalf("UpdateProjectPublicViewing error = %v, want ErrNotFound", err)
		}
		if got := projectPublicViewingValue(t, sqlDB, project.ID); got != 0 {
			t.Fatalf("public_view_enabled = %d, want 0", got)
		}
	})

	t.Run("system owner without membership is rejected", func(t *testing.T) {
		fx := newPublicationFixture(t)
		defer fx.cleanup()
		other, err := fx.store.CreateUser(fx.ctx, "publication-other-owner@example.com", "password123", "Other")
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		otherProject, err := fx.store.CreateProject(WithUserID(fx.ctx, other.ID), "Other Project")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		if fx.owner.SystemRole != SystemRoleOwner {
			t.Fatalf("bootstrap actor system role = %q, want owner", fx.owner.SystemRole)
		}
		if _, err := fx.store.UpdateProjectPublicViewing(fx.ctx, otherProject.ID, fx.owner.ID, true); !errors.Is(err, ErrNotFound) {
			t.Fatalf("UpdateProjectPublicViewing error = %v, want ErrNotFound", err)
		}
	})
}

func TestUpdateProjectPublicViewingRejectsIneligibleProjects(t *testing.T) {
	fx := newPublicationFixture(t)
	defer fx.cleanup()
	ownerCtx := WithUserID(fx.ctx, fx.owner.ID)
	temporary, err := fx.store.CreateAnonymousBoard(ownerCtx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard temporary: %v", err)
	}
	anonymous, err := fx.store.CreateAnonymousBoard(fx.ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard anonymous: %v", err)
	}
	staging, err := fx.store.CreateProject(ownerCtx, "Import Staging")
	if err != nil {
		t.Fatalf("CreateProject staging: %v", err)
	}
	if _, err := fx.db.Exec(`UPDATE projects SET import_batch_id = 'batch' WHERE id = ?`, staging.ID); err != nil {
		t.Fatalf("mark import staging: %v", err)
	}
	deleted, err := fx.store.CreateProject(ownerCtx, "Deleted Publication")
	if err != nil {
		t.Fatalf("CreateProject deleted: %v", err)
	}
	if _, err := fx.db.Exec(`DELETE FROM projects WHERE id = ?`, deleted.ID); err != nil {
		t.Fatalf("delete project: %v", err)
	}

	for _, projectID := range []int64{temporary.ID, anonymous.ID, staging.ID, deleted.ID, 999999} {
		for _, enabled := range []bool{false, true} {
			if _, err := fx.store.UpdateProjectPublicViewing(fx.ctx, projectID, fx.owner.ID, enabled); !errors.Is(err, ErrNotFound) {
				t.Fatalf("project %d enabled=%v error = %v, want ErrNotFound", projectID, enabled, err)
			}
		}
	}
}

func TestUpdateProjectPublicViewingReservedSlugFailsClosedOnEnable(t *testing.T) {
	fx := newPublicationFixture(t)
	defer fx.cleanup()
	project, err := fx.store.CreateProject(WithUserID(fx.ctx, fx.owner.ID), "Dashboard")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if project.Slug == "dashboard" || isReservedPublicProjectSlug(project.Slug) {
		t.Fatalf("new durable slug = %q, want nonreserved allocation", project.Slug)
	}
	if _, err := fx.db.Exec(`UPDATE projects SET slug = 'dashboard' WHERE id = ?`, project.ID); err != nil {
		t.Fatalf("seed legacy reserved slug: %v", err)
	}
	if _, err := fx.store.UpdateProjectPublicViewing(fx.ctx, project.ID, fx.owner.ID, true); !errors.Is(err, ErrValidation) {
		t.Fatalf("enable reserved slug error = %v, want ErrValidation", err)
	}
	if got := publicationAuditCount(t, fx.db, project.ID); got != 0 {
		t.Fatalf("publication audits = %d, want 0", got)
	}
	if _, err := fx.db.Exec(`UPDATE projects SET public_view_enabled = 1 WHERE id = ?`, project.ID); err != nil {
		t.Fatalf("seed legacy reserved state: %v", err)
	}
	state, err := fx.store.UpdateProjectPublicViewing(fx.ctx, project.ID, fx.owner.ID, false)
	if err != nil || !state.Changed || state.Enabled {
		t.Fatalf("disable reserved slug = %+v, %v", state, err)
	}
}

func TestUpdateProjectPublicViewingAuditFailureRollsBackState(t *testing.T) {
	fx := newPublicationFixture(t)
	defer fx.cleanup()
	if _, err := fx.db.Exec(`
CREATE TRIGGER reject_publication_audit
BEFORE INSERT ON audit_events
WHEN NEW.action = 'project_public_viewing_enabled'
BEGIN SELECT RAISE(ABORT, 'injected publication audit failure'); END`); err != nil {
		t.Fatalf("create audit failure trigger: %v", err)
	}
	if _, err := fx.store.UpdateProjectPublicViewing(fx.ctx, fx.project.ID, fx.owner.ID, true); err == nil {
		t.Fatal("expected publication mutation to fail")
	}
	if got := projectPublicViewingValue(t, fx.db, fx.project.ID); got != 0 {
		t.Fatalf("public_view_enabled after audit failure = %d, want 0", got)
	}
	if got := publicationAuditCount(t, fx.db, fx.project.ID); got != 0 {
		t.Fatalf("publication audits after failure = %d, want 0", got)
	}
}

func TestUpdateProjectPublicViewingOnlyChangesTargetProject(t *testing.T) {
	fx := newPublicationFixture(t)
	defer fx.cleanup()
	other, err := fx.store.CreateProject(WithUserID(fx.ctx, fx.owner.ID), "Scrumboy")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	if _, err := fx.store.UpdateProjectPublicViewing(fx.ctx, fx.project.ID, fx.owner.ID, true); err != nil {
		t.Fatalf("UpdateProjectPublicViewing: %v", err)
	}
	if got := projectPublicViewingValue(t, fx.db, fx.project.ID); got != 1 {
		t.Fatalf("target public_view_enabled = %d, want 1", got)
	}
	if got := projectPublicViewingValue(t, fx.db, other.ID); got != 0 {
		t.Fatalf("other public_view_enabled = %d, want 0", got)
	}
}

func TestConcurrentIdempotentPublicationRequestsSerialize(t *testing.T) {
	fx := newPublicationFixture(t)
	defer fx.cleanup()

	run := func(enabled bool) {
		t.Helper()
		start := make(chan struct{})
		results := make(chan struct {
			state ProjectPublicationState
			err   error
		}, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		for i := 0; i < 2; i++ {
			go func() {
				defer wg.Done()
				<-start
				state, err := fx.store.UpdateProjectPublicViewing(fx.ctx, fx.project.ID, fx.owner.ID, enabled)
				results <- struct {
					state ProjectPublicationState
					err   error
				}{state: state, err: err}
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		changed := 0
		for result := range results {
			if result.err != nil {
				t.Fatalf("concurrent update: %v", result.err)
			}
			if result.state.Enabled != enabled {
				t.Fatalf("concurrent state = %+v, want enabled=%v", result.state, enabled)
			}
			if result.state.Changed {
				changed++
			}
		}
		if changed != 1 {
			t.Fatalf("changed results = %d, want 1", changed)
		}
	}

	run(true)
	if got := publicationAuditCount(t, fx.db, fx.project.ID); got != 1 {
		t.Fatalf("enable audits = %d, want 1", got)
	}
	run(false)
	if got := publicationAuditCount(t, fx.db, fx.project.ID); got != 2 {
		t.Fatalf("total audits = %d, want 2", got)
	}
}

func TestPublicationRechecksAuthorizationAfterConcurrentMembershipChange(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, *sql.Tx, int64, int64)
	}{
		{
			name: "demotion",
			mutate: func(t *testing.T, tx *sql.Tx, projectID, actorID int64) {
				t.Helper()
				if _, err := tx.Exec(`UPDATE project_members SET role = 'contributor' WHERE project_id = ? AND user_id = ?`, projectID, actorID); err != nil {
					t.Fatalf("demote membership: %v", err)
				}
			},
		},
		{
			name: "removal",
			mutate: func(t *testing.T, tx *sql.Tx, projectID, actorID int64) {
				t.Helper()
				if _, err := tx.Exec(`DELETE FROM project_members WHERE project_id = ? AND user_id = ?`, projectID, actorID); err != nil {
					t.Fatalf("remove membership: %v", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newPublicationFixture(t)
			defer fx.cleanup()
			tx, err := fx.db.BeginTx(fx.ctx, nil)
			if err != nil {
				t.Fatalf("BeginTx: %v", err)
			}
			tc.mutate(t, tx, fx.project.ID, fx.owner.ID)

			ctx, cancel := context.WithTimeout(fx.ctx, 5*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := fx.store.UpdateProjectPublicViewing(ctx, fx.project.ID, fx.owner.ID, true)
				result <- err
			}()
			if err := tx.Commit(); err != nil {
				t.Fatalf("commit membership change: %v", err)
			}
			err = <-result
			if tc.name == "demotion" && !errors.Is(err, ErrForbidden) {
				t.Fatalf("publication error = %v, want ErrForbidden", err)
			}
			if tc.name == "removal" && !errors.Is(err, ErrNotFound) {
				t.Fatalf("publication error = %v, want ErrNotFound", err)
			}
			if got := projectPublicViewingValue(t, fx.db, fx.project.ID); got != 0 {
				t.Fatalf("public_view_enabled = %d, want 0", got)
			}
			if got := publicationAuditCount(t, fx.db, fx.project.ID); got != 0 {
				t.Fatalf("publication audits = %d, want 0", got)
			}
		})
	}
}

func TestPublicationSerializesWithProjectDeletionAndSettingsMutation(t *testing.T) {
	t.Run("deletion wins before authoritative read", func(t *testing.T) {
		fx := newPublicationFixture(t)
		defer fx.cleanup()
		tx, err := fx.db.BeginTx(fx.ctx, nil)
		if err != nil {
			t.Fatalf("BeginTx: %v", err)
		}
		if _, err := tx.Exec(`DELETE FROM projects WHERE id = ?`, fx.project.ID); err != nil {
			t.Fatalf("delete project: %v", err)
		}
		ctx, cancel := context.WithTimeout(fx.ctx, 5*time.Second)
		defer cancel()
		result := make(chan error, 1)
		go func() {
			_, err := fx.store.UpdateProjectPublicViewing(ctx, fx.project.ID, fx.owner.ID, true)
			result <- err
		}()
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit deletion: %v", err)
		}
		if err := <-result; !errors.Is(err, ErrNotFound) {
			t.Fatalf("publication error = %v, want ErrNotFound", err)
		}
		if got := publicationAuditCount(t, fx.db, fx.project.ID); got != 0 {
			t.Fatalf("publication audits = %d, want 0", got)
		}
	})

	t.Run("settings write and publication both persist", func(t *testing.T) {
		fx := newPublicationFixture(t)
		defer fx.cleanup()
		tx, err := fx.db.BeginTx(fx.ctx, nil)
		if err != nil {
			t.Fatalf("BeginTx: %v", err)
		}
		if _, err := tx.Exec(`UPDATE projects SET name = 'Ignite Renamed' WHERE id = ?`, fx.project.ID); err != nil {
			t.Fatalf("update project name: %v", err)
		}
		ctx, cancel := context.WithTimeout(fx.ctx, 5*time.Second)
		defer cancel()
		result := make(chan error, 1)
		go func() {
			_, err := fx.store.UpdateProjectPublicViewing(ctx, fx.project.ID, fx.owner.ID, true)
			result <- err
		}()
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit settings mutation: %v", err)
		}
		if err := <-result; err != nil {
			t.Fatalf("publication error: %v", err)
		}
		var name string
		var enabled int
		if err := fx.db.QueryRow(`SELECT name, public_view_enabled FROM projects WHERE id = ?`, fx.project.ID).Scan(&name, &enabled); err != nil {
			t.Fatalf("read project: %v", err)
		}
		if name != "Ignite Renamed" || enabled != 1 {
			t.Fatalf("project state = name:%q public:%d", name, enabled)
		}
	})
}
