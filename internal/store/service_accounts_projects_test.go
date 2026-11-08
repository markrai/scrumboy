package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"scrumboy/internal/version"
)

// serviceProjectFixture adds what the project-boundary tests need to serviceFixture: a durable project
// and a temporary board owned by humans, and an anonymous board that nobody owns.
type serviceProjectFixture struct {
	*serviceFixture
	ownerCtx context.Context
	svcCtx   context.Context
	durable  Project
	temp     Project
	anon     Project
}

func newServiceProjectFixture(t *testing.T) *serviceProjectFixture {
	t.Helper()
	f := newServiceFixture(t)
	ownerCtx := WithUserID(context.Background(), f.owner.ID)
	durable, err := f.st.CreateProject(ownerCtx, "Durable")
	if err != nil {
		t.Fatal(err)
	}
	temp, err := f.st.CreateAnonymousBoard(ownerCtx)
	if err != nil {
		t.Fatal(err)
	}
	anon, err := f.st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return &serviceProjectFixture{
		serviceFixture: f,
		ownerCtx:       ownerCtx,
		svcCtx:         WithUserID(context.Background(), f.svc.ID),
		durable:        durable,
		temp:           temp,
		anon:           anon,
	}
}

// assertHoldsNoProjects fails if the service account owns, created, or belongs to anything.
func (f *serviceProjectFixture) assertHoldsNoProjects(t *testing.T) {
	t.Helper()
	if n := f.count(t, `SELECT COUNT(*) FROM projects WHERE owner_user_id = ? OR creator_user_id = ?`, f.svc.ID, f.svc.ID); n != 0 {
		t.Errorf("service account owns or created %d projects", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM project_members WHERE user_id = ?`, f.svc.ID); n != 0 {
		t.Errorf("service account holds %d memberships", n)
	}
}

// projectGrantCalls is every store operation that would make its actor a project's owner or creator.
func (f *serviceProjectFixture) projectGrantCalls(t *testing.T) map[string]func() error {
	t.Helper()
	export, err := f.st.ExportAllProjects(f.ownerCtx, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if len(export.Projects) == 0 {
		t.Fatal("expected the owner's export to include projects")
	}
	// New slugs get past the per-project authorization preflight, so these reach the write itself.
	fresh := *export
	fresh.Projects = append([]ProjectExport(nil), export.Projects...)
	for i := range fresh.Projects {
		fresh.Projects[i].Slug = fmt.Sprintf("fresh-import-%d", i)
	}
	archive := fresh
	archive.Version = version.ArchiveExportFormatVersion
	id := f.svc.ID
	return map[string]func() error{
		"CreateProject": func() error { _, err := f.st.CreateProject(f.svcCtx, "Mine"); return err },
		"CreateProjectWithWorkflow": func() error {
			_, err := f.st.CreateProjectWithWorkflow(f.svcCtx, "Mine", nil)
			return err
		},
		"CreateAnonymousBoard":   func() error { _, err := f.st.CreateAnonymousBoard(f.svcCtx); return err },
		"ImportProjects copy":    func() error { _, err := f.st.ImportProjects(f.svcCtx, &fresh, ModeFull, "copy"); return err },
		"ImportProjects replace": func() error { _, err := f.st.ImportProjects(f.svcCtx, &fresh, ModeFull, "replace"); return err },
		"ImportProjects merge":   func() error { _, err := f.st.ImportProjects(f.svcCtx, &fresh, ModeFull, "merge"); return err },
		"ImportProjectsWithAssets": func() error {
			_, err := f.st.ImportProjectsWithAssets(f.svcCtx, &archive, ModeFull, "copy", nil)
			return err
		},
		"AssignUnownedDurableProjectsToUser": func() error {
			return f.st.AssignUnownedDurableProjectsToUser(f.svcCtx, id)
		},
	}
}

func TestServiceAccount_CannotObtainProjectOwnership(t *testing.T) {
	f := newServiceProjectFixture(t)
	// Something for sign-in assignment to hand out.
	if _, err := f.db.Exec(`INSERT INTO projects(name, slug, estimation_mode, last_activity_at, created_at, updated_at) VALUES ('Ownerless', 'ownerless', ?, 1, 1, 1)`, EstimationModeModifiedFibonacci); err != nil {
		t.Fatal(err)
	}
	projectsBefore := f.count(t, `SELECT COUNT(*) FROM projects`)

	if err := f.st.EnsureMaintainerMembership(f.svcCtx, f.durable.ID, f.svc.ID); err != nil {
		t.Errorf("EnsureMaintainerMembership: %v", err)
	}
	f.assertHoldsNoProjects(t)

	for name, call := range f.projectGrantCalls(t) {
		if err := call(); !errors.Is(err, ErrServiceAccountRestricted) {
			t.Errorf("%s: err = %v, want ErrServiceAccountRestricted", name, err)
		}
		f.assertHoldsNoProjects(t)
		if got := f.count(t, `SELECT COUNT(*) FROM projects`); got != projectsBefore {
			t.Fatalf("%s changed the project count from %d to %d", name, projectsBefore, got)
		}
	}
	if n := f.count(t, `SELECT COUNT(*) FROM projects WHERE slug = 'ownerless' AND owner_user_id IS NULL`); n != 1 {
		t.Error("the ownerless project was assigned")
	}
}

// Replace imports delete before they insert, and a merge can reach a project the actor is in.
func TestServiceAccount_ImportsCannotDestroyOrMergeDelegatedProjects(t *testing.T) {
	f := newServiceProjectFixture(t)
	if err := f.st.AddProjectMember(f.ownerCtx, f.owner.ID, f.durable.ID, f.svc.ID, RoleMaintainer); err != nil {
		t.Fatal(err)
	}
	export, err := f.st.ExportAllProjects(f.svcCtx, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if len(export.Projects) != 1 {
		t.Fatalf("service export has %d projects, want the delegated one", len(export.Projects))
	}
	empty := *export
	empty.Projects = []ProjectExport{}
	projectsBefore := f.count(t, `SELECT COUNT(*) FROM projects`)

	for name, call := range map[string]func() error{
		"replace": func() error { _, err := f.st.ImportProjects(f.svcCtx, export, ModeFull, "replace"); return err },
		"replace with nothing": func() error {
			_, err := f.st.ImportProjects(f.svcCtx, &empty, ModeFull, "replace")
			return err
		},
		"merge": func() error { _, err := f.st.ImportProjects(f.svcCtx, export, ModeFull, "merge"); return err },
		"merge into a link board": func() error {
			_, err := f.st.ImportProjectsWithTarget(f.svcCtx, export, ModeAnonymous, "merge", f.anon.Slug)
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrServiceAccountRestricted) {
			t.Errorf("%s: err = %v, want ErrServiceAccountRestricted", name, err)
		}
		if n := f.count(t, `SELECT COUNT(*) FROM projects WHERE id = ?`, f.durable.ID); n != 1 {
			t.Fatalf("%s deleted the delegated project", name)
		}
		if got := f.count(t, `SELECT COUNT(*) FROM projects`); got != projectsBefore {
			t.Fatalf("%s changed the project count from %d to %d", name, projectsBefore, got)
		}
	}
	if n := f.count(t, `SELECT COUNT(*) FROM projects WHERE owner_user_id = ? OR creator_user_id = ?`, f.svc.ID, f.svc.ID); n != 0 {
		t.Errorf("service account owns or created %d projects", n)
	}
}

// Service accounts are always created with fresh identity references, and the
// schema refuses any other insert path. See docs/architecture/service-accounts.md section 6.
func TestServiceAccount_CreatedWithFreshIdentityReferences(t *testing.T) {
	f := newServiceProjectFixture(t)
	ctx := context.Background()

	departing, err := f.st.CreateUser(ctx, "departing@example.com", serviceTestPassword, "Departing")
	if err != nil {
		t.Fatal(err)
	}
	board, err := f.st.CreateAnonymousBoard(WithUserID(ctx, departing.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.DeleteUser(ctx, f.owner.ID, departing.ID); err != nil {
		t.Fatal(err)
	}

	successor, err := f.st.CreateServiceAccount(ctx, f.owner.ID, "Successor")
	if err != nil {
		t.Fatal(err)
	}
	if successor.ID == departing.ID {
		t.Fatalf("service account reused the id of a user whose board still names them (%d)", departing.ID)
	}
	succCtx := WithUserID(ctx, successor.ID)
	listed, err := f.st.ListProjects(succCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("service account sees %d projects", len(listed))
	}
	if err := f.st.ClaimTemporaryBoard(succCtx, board.ID, successor.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim: err = %v, want ErrNotFound", err)
	}

	// Todo assignments carry the same guarantee.
	assigned, err := f.st.CreateUser(ctx, "assigned@example.com", serviceTestPassword, "Assigned")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.AddProjectMember(f.ownerCtx, f.owner.ID, f.durable.ID, assigned.ID, RoleContributor); err != nil {
		t.Fatal(err)
	}
	todo, err := f.st.CreateTodo(f.ownerCtx, f.durable.ID, CreateTodoInput{Title: "Private card", AssigneeUserID: &assigned.ID}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.DeleteUser(ctx, f.owner.ID, assigned.ID); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM todos WHERE id = ? AND assignee_user_id = ?`, todo.ID, assigned.ID); n != 1 {
		t.Fatalf("expected the deleted user's assignment to remain, or this no longer exercises the check (%d)", n)
	}
	second, err := f.st.CreateServiceAccount(ctx, f.owner.ID, "Second")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == assigned.ID {
		t.Fatalf("service account reused the id of a user still assigned to a card (%d)", assigned.ID)
	}
	items, _, err := f.st.ListDashboardTodos(WithUserID(ctx, second.ID), second.ID, 50, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("dashboard exposed %d cards to a new service account", len(items))
	}

	// No other insert path can do it either: the schema refuses the row.
	_, err = f.db.Exec(`INSERT INTO users(id, email, name, created_at, system_role, is_service) VALUES (?, 'svc-reuse-card@service.invalid', 'R', 1, 'user', 1)`, assigned.ID)
	if !isServiceIdentityViolation(err) {
		t.Fatalf("raw insert reusing an assignee's id: err = %v, want a service identity violation", err)
	}
	_, err = f.db.Exec(`INSERT INTO users(id, email, name, created_at, system_role, is_service) VALUES (?, 'svc-reuse@service.invalid', 'R', 1, 'user', 1)`, departing.ID)
	if !isServiceIdentityViolation(err) {
		t.Fatalf("raw insert reusing the id: err = %v, want a service identity violation", err)
	}

	// The schema check applies only to service accounts.
	if _, err := f.db.Exec(`INSERT INTO users(id, email, name, created_at, system_role) VALUES (?, 'human-reuse@example.com', 'R', 1, 'user')`, departing.ID); err != nil {
		t.Fatalf("human insert: %v", err)
	}
}

// Expiring boards admit anyone with the link, but a service identity acts only where it was delegated.
func TestServiceAccount_LinkBoardsRequireExplicitMembership(t *testing.T) {
	f := newServiceProjectFixture(t)
	id := f.svc.ID

	for name, board := range map[string]Project{"human temporary board": f.temp, "anonymous board": f.anon} {
		t.Run(name, func(t *testing.T) {
			for call, run := range map[string]func() error{
				"GetProject":       func() error { _, err := f.st.GetProject(f.svcCtx, board.ID); return err },
				"GetProjectBySlug": func() error { _, err := f.st.GetProjectBySlug(f.svcCtx, board.Slug); return err },
				"GetProjectContextBySlug": func() error {
					_, err := f.st.GetProjectContextBySlug(f.svcCtx, board.Slug, ModeFull)
					return err
				},
				"GetProjectContextForRead": func() error {
					_, err := f.st.GetProjectContextForRead(f.svcCtx, board.ID, ModeFull)
					return err
				},
				"CreateTodo": func() error {
					_, err := f.st.CreateTodo(f.svcCtx, board.ID, CreateTodoInput{Title: "From a bot"}, ModeFull)
					return err
				},
				// These take the user explicitly, so a context naming no actor must not get around them.
				"CheckProjectRole": func() error {
					return f.st.CheckProjectRole(context.Background(), board.ID, id, RoleViewer)
				},
			} {
				if err := run(); !errors.Is(err, ErrNotFound) {
					t.Errorf("%s: err = %v, want ErrNotFound", call, err)
				}
			}
			if ok, err := f.st.userCanCreateTagsInProject(context.Background(), board.ID, id); err != nil || ok {
				t.Errorf("userCanCreateTagsInProject = %v, %v, want false", ok, err)
			}
			if n := f.count(t, `SELECT COUNT(*) FROM todos WHERE project_id = ?`, board.ID); n != 0 {
				t.Errorf("%d todos were written", n)
			}
		})
	}

	// Link collaboration is unchanged for everyone else: signed-out callers and other humans.
	humanCtx := WithUserID(context.Background(), f.human.ID)
	for name, ctx := range map[string]context.Context{"signed out": context.Background(), "another human": humanCtx} {
		for _, board := range []Project{f.temp, f.anon} {
			if _, err := f.st.GetProjectContextBySlug(ctx, board.Slug, ModeFull); err != nil {
				t.Errorf("%s reading %s: %v", name, board.Slug, err)
			}
			if _, err := f.st.CreateTodo(ctx, board.ID, CreateTodoInput{Title: "From a person"}, ModeFull); err != nil {
				t.Errorf("%s writing to %s: %v", name, board.Slug, err)
			}
		}
	}

	// An explicit membership on a temporary board does open it.
	if err := f.st.EnsureMaintainerMembership(f.ownerCtx, f.temp.ID, f.owner.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AddProjectMember(f.ownerCtx, f.owner.ID, f.temp.ID, id, RoleContributor); err != nil {
		t.Fatalf("explicit membership: %v", err)
	}
	if _, err := f.st.GetProjectContextBySlug(f.svcCtx, f.temp.Slug, ModeFull); err != nil {
		t.Errorf("after explicit membership: %v", err)
	}
	if _, err := f.st.GetProjectContextBySlug(f.svcCtx, f.anon.Slug, ModeFull); !errors.Is(err, ErrNotFound) {
		t.Errorf("membership leaked to another board: %v", err)
	}
}

func TestServiceAccount_ExplicitMembershipIsTheOnlyGrant(t *testing.T) {
	f := newServiceProjectFixture(t)
	id := f.svc.ID

	// The legacy owner role is project ownership by another name.
	if err := f.st.AddProjectMember(f.ownerCtx, f.owner.ID, f.durable.ID, id, RoleOwner); !errors.Is(err, ErrServiceAccountRestricted) {
		t.Fatalf("add as owner: err = %v, want ErrServiceAccountRestricted", err)
	}
	f.assertHoldsNoProjects(t)
	if _, err := f.st.CreateTodo(f.svcCtx, f.durable.ID, CreateTodoInput{Title: "Uninvited"}, ModeFull); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member write: err = %v, want ErrNotFound", err)
	}

	for _, role := range []ProjectRole{RoleViewer, RoleContributor, RoleMaintainer} {
		t.Run(string(role), func(t *testing.T) {
			if _, err := f.db.Exec(`DELETE FROM project_members WHERE user_id = ?`, id); err != nil {
				t.Fatal(err)
			}
			if err := f.st.AddProjectMember(f.ownerCtx, f.owner.ID, f.durable.ID, id, role); err != nil {
				t.Fatalf("add: %v", err)
			}
			pc, err := f.st.GetProjectContextBySlug(f.svcCtx, f.durable.Slug, ModeFull)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if pc.Role != role {
				t.Fatalf("role = %q, want %q", pc.Role, role)
			}
			_, err = f.st.CreateTodo(f.svcCtx, f.durable.ID, CreateTodoInput{Title: "Delegated"}, ModeFull)
			if allowed := CanCreateTodo(role); allowed != (err == nil) {
				t.Fatalf("CreateTodo with role %s: err = %v, want allowed = %v", role, err, allowed)
			}
		})
	}

	// Holding Maintainer does not turn into ownership or let it escalate past the role.
	if err := f.st.UpdateProjectMemberRole(f.svcCtx, id, f.durable.ID, id, RoleOwner); !errors.Is(err, ErrValidation) {
		t.Errorf("self-promotion to owner: err = %v, want ErrValidation", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM projects WHERE owner_user_id = ?`, id); n != 0 {
		t.Errorf("service account owns %d projects", n)
	}
	if _, err := f.st.CreateProject(f.svcCtx, "Still no"); !errors.Is(err, ErrServiceAccountRestricted) {
		t.Errorf("CreateProject as a Maintainer elsewhere: err = %v", err)
	}
	// Removal takes the access away again.
	if err := f.st.RemoveProjectMember(f.ownerCtx, f.owner.ID, f.durable.ID, id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.GetProjectContextBySlug(f.svcCtx, f.durable.Slug, ModeFull); !errors.Is(err, ErrNotFound) {
		t.Errorf("after removal: err = %v, want ErrNotFound", err)
	}
}

// Credential mutations carry their own guard. With CHECK enforcement off they must still leave the
// row untouched, and the account must stay unable to sign in.
func TestServiceAccount_CredentialMutationsHoldWithChecksDisabled(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	id := f.svc.ID
	secretEnc, err := f.st.EncryptTOTPSecret([]byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatal(err)
	}

	// Seed the proofs each mutation needs before any check can object to them.
	f.dropCredentialTriggers(t)
	session, _, err := f.st.CreateSession(ctx, id, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	const rawGrant = "first-password-grant"
	if _, err := f.db.Exec(`INSERT INTO first_password_grants(token_hash, user_id, session_token_hash, created_at, expires_at) VALUES (?, ?, ?, 1, ?)`,
		hashToken(rawGrant), id, hashToken(session), time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	setup, _, err := f.st.CreateTwoFactorEnrollment(ctx, id, secretEnc, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := bcrypt.GenerateFromPassword([]byte("x"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.db.Exec(`PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = f.db.Exec(`PRAGMA ignore_check_constraints = OFF`) })
	// Prove the CHECK really is off, so that it cannot be what rejects the calls below.
	if _, err := f.db.Exec(`UPDATE users SET name = 'probe', two_factor_enabled = 1 WHERE id = ?`, f.human.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE users SET two_factor_enabled = 1 WHERE id = ?`, id); err != nil {
		t.Fatalf("CHECK is still enforced: %v", err)
	}
	if _, err := f.db.Exec(`UPDATE users SET two_factor_enabled = 0 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		call func() error
		want error
	}{
		"SetUserTwoFactor":   {func() error { return f.st.SetUserTwoFactor(ctx, id, secretEnc) }, ErrNotFound},
		"UpdateUserPassword": {func() error { return f.st.UpdateUserPassword(ctx, id, serviceTestPassword) }, ErrNotFound},
		"SetFirstPassword": {func() error {
			return f.st.SetFirstPassword(ctx, id, rawGrant, session, serviceTestPassword)
		}, ErrNotFound},
		"ResetLocalPassword": {func() error {
			return f.st.ResetLocalPassword(ctx, id, string(expected), serviceTestPassword)
		}, ErrConflict},
		"RecoverOwnerPassword": {func() error { return f.st.RecoverOwnerPassword(ctx, f.svc.Email, serviceTestPassword) }, ErrValidation},
		"CompleteTwoFactorEnrollment": {func() error {
			return f.st.CompleteTwoFactorEnrollment(ctx, id, setup, recoveryCodes())
		}, ErrNotFound},
		"UpdateUserRole": {func() error { return f.st.UpdateUserRole(ctx, f.owner.ID, id, SystemRoleOwner) }, ErrServiceAccountRestricted},
	} {
		if err := tc.call(); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
		if n := f.count(t, `SELECT COUNT(*) FROM users WHERE id = ? AND is_service = 1 AND system_role = 'user' AND password_hash IS NULL AND two_factor_enabled = 0 AND two_factor_secret_enc IS NULL`, id); n != 1 {
			t.Fatalf("%s changed the service account's credentials", name)
		}
	}
	if u, err := f.st.GetUser(ctx, id); err != nil || u.IsTwoFactorActive() || u.HasLocalPassword {
		t.Errorf("GetUser = %+v, %v", u, err)
	}
	if _, err := f.st.GetUserTwoFactorSecret(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUserTwoFactorSecret: err = %v, want ErrNotFound", err)
	}
	if _, err := f.st.AuthenticateUser(ctx, f.svc.Email, serviceTestPassword); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("AuthenticateUser: err = %v, want ErrUnauthorized", err)
	}
	if _, err := f.st.GetUserBySessionToken(ctx, session); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUserBySessionToken: err = %v, want ErrNotFound", err)
	}
}

func TestSetUserTwoFactor_HumanBehaviour(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	secretEnc, err := f.st.EncryptTOTPSecret([]byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetUserTwoFactor(ctx, f.human.ID, secretEnc); err != nil {
		t.Fatalf("human: %v", err)
	}
	if u, err := f.st.GetUser(ctx, f.human.ID); err != nil || !u.IsTwoFactorActive() {
		t.Fatalf("GetUser = %+v, %v", u, err)
	}
	if err := f.st.SetUserTwoFactor(ctx, 999999, secretEnc); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user: err = %v, want ErrNotFound", err)
	}
}

// A Maintainer demoted by a transaction still in flight cannot act on the stale role.
func TestProjectMembership_RejectsRequesterDemotedConcurrently(t *testing.T) {
	cases := map[string]func(ctx context.Context, f *serviceProjectFixture, requester int64) error{
		"AddProjectMember": func(ctx context.Context, f *serviceProjectFixture, requester int64) error {
			return f.st.AddProjectMember(ctx, requester, f.durable.ID, f.svc.ID, RoleMaintainer)
		},
		"UpdateProjectMemberRole": func(ctx context.Context, f *serviceProjectFixture, requester int64) error {
			return f.st.UpdateProjectMemberRole(ctx, requester, f.durable.ID, f.human.ID, RoleMaintainer)
		},
		"RemoveProjectMember": func(ctx context.Context, f *serviceProjectFixture, requester int64) error {
			return f.st.RemoveProjectMember(ctx, requester, f.durable.ID, f.human.ID)
		},
	}
	for name, mutate := range cases {
		for label, at := range staleReadInterleavings() {
			t.Run(name+"/"+label, func(t *testing.T) {
				f := newServiceProjectFixture(t)
				ctx := context.Background()
				if err := f.st.AddProjectMember(ctx, f.owner.ID, f.durable.ID, f.admin.ID, RoleMaintainer); err != nil {
					t.Fatal(err)
				}
				if err := f.st.AddProjectMember(ctx, f.owner.ID, f.durable.ID, f.human.ID, RoleViewer); err != nil {
					t.Fatal(err)
				}

				hooked, hook := f.hooked(t)
				base := *f.serviceFixture
				base.st = hooked
				racing := *f
				racing.serviceFixture = &base
				hook.arm(at, func() {
					if _, err := f.db.Exec(`UPDATE project_members SET role = 'viewer' WHERE project_id = ? AND user_id = ?`, f.durable.ID, f.admin.ID); err != nil {
						t.Errorf("demote: %v", err)
					}
				})

				err := mutate(ctx, &racing, f.admin.ID)
				hook.assertFired(t)
				if err == nil {
					t.Fatal("mutation succeeded on a stale project role")
				}
				if n := f.count(t, `SELECT COUNT(*) FROM project_members WHERE project_id = ? AND user_id = ?`, f.durable.ID, f.svc.ID); n != 0 {
					t.Error("service account was added on a stale project role")
				}
				if n := f.count(t, `SELECT COUNT(*) FROM project_members WHERE project_id = ? AND user_id = ? AND role = 'viewer'`, f.durable.ID, f.human.ID); n != 1 {
					t.Error("target membership changed on a stale project role")
				}
			})
		}
	}
}

func TestProjectMembership_ConcurrentRemovalsKeepAMaintainer(t *testing.T) {
	f := newServiceProjectFixture(t)
	ctx := context.Background()
	for round := 0; round < 40; round++ {
		if _, err := f.db.Exec(`INSERT OR REPLACE INTO project_members(project_id, user_id, role, created_at) VALUES (?, ?, 'maintainer', 1), (?, ?, 'maintainer', 1)`, f.durable.ID, f.owner.ID, f.durable.ID, f.admin.ID); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = f.st.RemoveProjectMember(ctx, f.owner.ID, f.durable.ID, f.admin.ID)
		}()
		go func() {
			defer wg.Done()
			_ = f.st.RemoveProjectMember(ctx, f.admin.ID, f.durable.ID, f.owner.ID)
		}()
		wg.Wait()
		if n := f.count(t, `SELECT COUNT(*) FROM project_members WHERE project_id = ? AND role = 'maintainer'`, f.durable.ID); n != 1 {
			t.Fatalf("round %d: %d maintainers after mutual removal, want exactly 1", round, n)
		}
	}
}

// A burst of project-granting calls overlapping human project work grants the service account nothing.
func TestServiceAccount_ConcurrentProjectGrants(t *testing.T) {
	f := newServiceProjectFixture(t)
	const rounds = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	var successes, humanProjects []string
	record := func(list *[]string, entry string) {
		mu.Lock()
		defer mu.Unlock()
		*list = append(*list, entry)
	}
	for i := 0; i < rounds; i++ {
		wg.Add(3)
		go func(i int) {
			defer wg.Done()
			if _, err := f.st.CreateProject(f.svcCtx, fmt.Sprintf("Bot %d", i)); err == nil {
				record(&successes, "CreateProject")
			}
		}(i)
		go func() {
			defer wg.Done()
			if _, err := f.st.CreateAnonymousBoard(f.svcCtx); err == nil {
				record(&successes, "CreateAnonymousBoard")
			}
		}()
		go func(i int) {
			defer wg.Done()
			p, err := f.st.CreateProject(f.ownerCtx, fmt.Sprintf("Human %d", i))
			if err != nil {
				t.Errorf("human CreateProject: %v", err)
				return
			}
			record(&humanProjects, p.Slug)
		}(i)
	}
	wg.Wait()
	if len(successes) != 0 {
		t.Errorf("succeeded for the service account: %v", successes)
	}
	f.assertHoldsNoProjects(t)
	if len(humanProjects) != rounds {
		t.Errorf("human created %d projects, want %d", len(humanProjects), rounds)
	}
}

// Everything a human does with projects still works through the changed code.
func TestHumanProjectBehaviourUnaffected(t *testing.T) {
	f := newServiceProjectFixture(t)
	ctx := context.Background()
	humanCtx := WithUserID(ctx, f.human.ID)

	project, err := f.st.CreateProject(humanCtx, "Human project")
	if err != nil {
		t.Fatal(err)
	}
	if project.OwnerUserID == nil || *project.OwnerUserID != f.human.ID {
		t.Fatalf("owner = %v", project.OwnerUserID)
	}
	if role, err := f.st.GetProjectRole(ctx, project.ID, f.human.ID); err != nil || role != RoleMaintainer {
		t.Fatalf("creator role = %q, %v", role, err)
	}

	board, err := f.st.CreateAnonymousBoard(humanCtx)
	if err != nil {
		t.Fatal(err)
	}
	if listed, err := f.st.ListProjects(humanCtx); err != nil || len(listed) != 2 {
		t.Fatalf("ListProjects = %d, %v, want the project and the board", len(listed), err)
	}
	if err := f.st.ClaimTemporaryBoard(humanCtx, board.ID, f.human.ID); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if role, err := f.st.GetProjectRole(ctx, board.ID, f.human.ID); err != nil || role != RoleMaintainer {
		t.Fatalf("claimer role = %q, %v", role, err)
	}

	export, err := f.st.ExportAllProjects(humanCtx, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.ImportProjects(humanCtx, export, ModeFull, "copy"); err != nil {
		t.Fatalf("import copy: %v", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM projects WHERE owner_user_id = ?`, f.human.ID); n != 4 {
		t.Fatalf("human owns %d projects after a copy import, want 4", n)
	}

	if err := f.st.AddProjectMember(humanCtx, f.human.ID, project.ID, f.admin.ID, RoleMaintainer); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := f.st.AddProjectMember(humanCtx, f.human.ID, project.ID, f.admin.ID, RoleViewer); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate member: err = %v, want ErrConflict", err)
	}
	if err := f.st.AddProjectMember(humanCtx, f.human.ID, 999999, f.admin.ID, RoleViewer); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing project: err = %v, want ErrNotFound", err)
	}
	if err := f.st.AddProjectMember(humanCtx, f.human.ID, project.ID, 999999, RoleViewer); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing target: err = %v, want ErrNotFound", err)
	}
	if err := f.st.AddProjectMember(WithUserID(ctx, f.owner.ID), f.owner.ID, project.ID, f.owner.ID, RoleViewer); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("non-member requester: err = %v, want ErrUnauthorized", err)
	}
	if err := f.st.UpdateProjectMemberRole(humanCtx, f.human.ID, project.ID, f.admin.ID, RoleContributor); err != nil {
		t.Fatalf("update role: %v", err)
	}
	if err := f.st.UpdateProjectMemberRole(humanCtx, f.human.ID, project.ID, f.human.ID, RoleViewer); !errors.Is(err, ErrConflict) {
		t.Fatalf("self-demotion: err = %v, want ErrConflict", err)
	}
	if err := f.st.RemoveProjectMember(humanCtx, f.human.ID, project.ID, f.admin.ID); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	if err := f.st.RemoveProjectMember(humanCtx, f.human.ID, project.ID, f.human.ID); !errors.Is(err, ErrValidation) {
		t.Fatalf("removing the last maintainer: err = %v, want ErrValidation", err)
	}
	if err := f.st.EnsureMaintainerMembership(ctx, project.ID, f.admin.ID); err != nil {
		t.Fatalf("EnsureMaintainerMembership: %v", err)
	}
}
