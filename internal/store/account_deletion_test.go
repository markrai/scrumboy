package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// deletionTarget returns the account under test: the deletion invariant holds for both account types.
func (f *serviceProjectFixture) deletionTarget(kind string) int64 {
	if kind == "service" {
		return f.svc.ID
	}
	return f.human.ID
}

func (f *serviceProjectFixture) addMember(t *testing.T, projectID, userID int64, role ProjectRole) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO project_members(project_id, user_id, role, created_at) VALUES (?, ?, ?, 1)`, projectID, userID, role); err != nil {
		t.Fatalf("add member %d as %s: %v", userID, role, err)
	}
}

func (f *serviceProjectFixture) dropMember(t *testing.T, projectID, userID int64) {
	t.Helper()
	if _, err := f.db.Exec(`DELETE FROM project_members WHERE project_id = ? AND user_id = ?`, projectID, userID); err != nil {
		t.Fatal(err)
	}
}

func (f *serviceProjectFixture) newProject(t *testing.T, name string) Project {
	t.Helper()
	p, err := f.st.CreateProject(f.ownerCtx, name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// projectLedBy returns a project whose only Maintainers are maintainers. The fixture owner stays
// recorded as projects.owner_user_id, so only the invariant under test can refuse the deletion.
func (f *serviceProjectFixture) projectLedBy(t *testing.T, name string, maintainers ...int64) Project {
	t.Helper()
	p := f.newProject(t, name)
	f.dropMember(t, p.ID, f.owner.ID)
	for _, id := range maintainers {
		f.addMember(t, p.ID, id, RoleMaintainer)
	}
	return p
}

func (f *serviceProjectFixture) maintainerCount(t *testing.T, projectID int64) int {
	t.Helper()
	return f.count(t, `SELECT COUNT(*) FROM project_members WHERE project_id = ? AND role IN ('maintainer', 'owner')`, projectID)
}

func (f *serviceProjectFixture) userExists(t *testing.T, id int64) bool {
	t.Helper()
	return f.count(t, `SELECT COUNT(*) FROM users WHERE id = ?`, id) == 1
}

func TestDeleteUser_SoleMaintainerInvariant(t *testing.T) {
	cases := map[string]struct {
		arrange func(t *testing.T, f *serviceProjectFixture, target int64) Project
		refused bool
		left    int
	}{
		"project already has no Maintainer": {
			arrange: func(t *testing.T, f *serviceProjectFixture, target int64) Project {
				p := f.projectLedBy(t, "Leaderless")
				f.addMember(t, p.ID, target, RoleViewer)
				return p
			},
			left: 0,
		},
		"target is a plain member": {
			arrange: func(t *testing.T, f *serviceProjectFixture, target int64) Project {
				p := f.projectLedBy(t, "Led by someone else", f.admin.ID)
				f.addMember(t, p.ID, target, RoleContributor)
				return p
			},
			left: 1,
		},
		"target is the only member and Maintainer": {
			arrange: func(t *testing.T, f *serviceProjectFixture, target int64) Project {
				return f.projectLedBy(t, "Sole", target)
			},
			refused: true,
			left:    1,
		},
		"other members hold no Maintainer role": {
			arrange: func(t *testing.T, f *serviceProjectFixture, target int64) Project {
				p := f.projectLedBy(t, "Sole with team", target)
				f.addMember(t, p.ID, f.admin.ID, RoleContributor)
				f.addMember(t, p.ID, f.owner.ID, RoleViewer)
				return p
			},
			refused: true,
			left:    1,
		},
		"another Maintainer remains": {
			arrange: func(t *testing.T, f *serviceProjectFixture, target int64) Project {
				return f.projectLedBy(t, "Shared", target, f.admin.ID)
			},
			left: 1,
		},
		"several Maintainers remain": {
			arrange: func(t *testing.T, f *serviceProjectFixture, target int64) Project {
				return f.projectLedBy(t, "Shared widely", target, f.admin.ID, f.owner.ID)
			},
			left: 2,
		},
		"the remaining Maintainer is a service account": {
			arrange: func(t *testing.T, f *serviceProjectFixture, target int64) Project {
				peer, err := f.st.CreateServiceAccount(context.Background(), f.owner.ID, "Peer")
				if err != nil {
					t.Fatal(err)
				}
				return f.projectLedBy(t, "Led by a peer bot", target, peer.ID)
			},
			left: 1,
		},
		"the remaining Maintainer holds the legacy owner role": {
			arrange: func(t *testing.T, f *serviceProjectFixture, target int64) Project {
				p := f.projectLedBy(t, "Legacy peer", target)
				f.addMember(t, p.ID, f.admin.ID, RoleOwner)
				return p
			},
			left: 1,
		},
	}
	for _, kind := range []string{"human", "service"} {
		for name, tc := range cases {
			t.Run(kind+"/"+name, func(t *testing.T) {
				f := newServiceProjectFixture(t)
				target := f.deletionTarget(kind)
				project := tc.arrange(t, f, target)

				err := f.st.DeleteUser(context.Background(), f.owner.ID, target)
				if tc.refused {
					if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "sole maintainer") {
						t.Fatalf("err = %v, want a sole-maintainer conflict", err)
					}
				} else if err != nil {
					t.Fatalf("delete: %v", err)
				}
				if got := f.userExists(t, target); got != tc.refused {
					t.Errorf("account exists = %v, want %v", got, tc.refused)
				}
				if got := f.maintainerCount(t, project.ID); got != tc.left {
					t.Errorf("%d Maintainers afterwards, want %d", got, tc.left)
				}
			})
		}
	}
}

func TestDeleteUser_SoleLegacyOwnerRoleIsProtected(t *testing.T) {
	f := newServiceProjectFixture(t)
	project := f.projectLedBy(t, "Legacy sole")
	f.addMember(t, project.ID, f.human.ID, RoleOwner)

	if err := f.st.DeleteUser(context.Background(), f.owner.ID, f.human.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestDeleteUser_ConsidersEveryMembership(t *testing.T) {
	f := newServiceProjectFixture(t)
	ctx := context.Background()
	id := f.svc.ID
	f.projectLedBy(t, "Shared one", id, f.admin.ID)
	f.projectLedBy(t, "Shared two", id, f.owner.ID)
	viewerOf := f.projectLedBy(t, "Viewer only", f.admin.ID)
	f.addMember(t, viewerOf.ID, id, RoleViewer)
	soleOne := f.projectLedBy(t, "Sole one", id)
	soleTwo := f.projectLedBy(t, "Sole two", id)

	err := f.st.DeleteUser(ctx, f.owner.ID, id)
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "2 project") {
		t.Fatalf("err = %v, want a conflict naming 2 projects", err)
	}
	for _, leak := range []string{soleOne.Name, soleOne.Slug, soleTwo.Name, soleTwo.Slug} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("the error discloses project %q to an Owner with no access to it: %v", leak, err)
		}
	}

	f.addMember(t, soleOne.ID, f.admin.ID, RoleMaintainer)
	if err := f.st.DeleteUser(ctx, f.owner.ID, id); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "1 project") {
		t.Fatalf("with one project left: err = %v, want a conflict naming 1 project", err)
	}
	if _, err := f.st.DeleteProject(WithUserID(ctx, id), soleTwo.ID, id); err != nil {
		t.Fatalf("the sole Maintainer deleting its project: %v", err)
	}
	if err := f.st.DeleteUser(ctx, f.owner.ID, id); err != nil {
		t.Fatalf("every project resolved: %v", err)
	}
}

// Only a durable, published project requires a Maintainer. A Maintainer row grants no authority on an
// expiring board, whose authority is its creator or its link, and a staged import row is not yet a
// project: neither may stop an account being deleted.
func TestDeleteUser_OnlyDurableProjectsRequireAMaintainer(t *testing.T) {
	for _, kind := range []string{"human", "service"} {
		t.Run(kind, func(t *testing.T) {
			f := newServiceProjectFixture(t)
			ctx := context.Background()
			target := f.deletionTarget(kind)
			f.addMember(t, f.temp.ID, target, RoleMaintainer)
			f.addMember(t, f.anon.ID, target, RoleMaintainer)
			staged := f.projectLedBy(t, "Staged import", target)
			if _, err := f.db.Exec(`UPDATE projects SET import_batch_id = 'batch' WHERE id = ?`, staged.ID); err != nil {
				t.Fatal(err)
			}

			if err := f.st.DeleteUser(ctx, f.owner.ID, target); err != nil {
				t.Fatalf("delete: %v", err)
			}
			if n := f.count(t, `SELECT COUNT(*) FROM projects WHERE id IN (?, ?, ?)`, f.temp.ID, f.anon.ID, staged.ID); n != 3 {
				t.Errorf("deleting an account removed a project: %d of 3 left", n)
			}
			if err := f.st.CheckCanManageProject(ctx, f.temp.ID, f.owner.ID); err != nil {
				t.Errorf("the temporary board's creator lost authority: %v", err)
			}
		})
	}
}

// DeleteUser removes no project itself, which is why every durable project it counts must survive.
// A cascade from users to projects would make that wrong, so it has to fail here first.
func TestDeleteUser_RemovesNoProjectByCascade(t *testing.T) {
	f := newServiceProjectFixture(t)
	rows, err := f.db.Query(`SELECT "table", "from", on_delete FROM pragma_foreign_key_list('projects')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, from, onDelete string
		if err := rows.Scan(&table, &from, &onDelete); err != nil {
			t.Fatal(err)
		}
		if table == "users" && strings.EqualFold(onDelete, "CASCADE") {
			t.Errorf("projects.%s cascades from users", from)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteUser_RefusalChangesNothing(t *testing.T) {
	for _, kind := range []string{"human", "service"} {
		t.Run(kind, func(t *testing.T) {
			f := newServiceProjectFixture(t)
			ctx := context.Background()
			target := f.deletionTarget(kind)

			project := f.projectLedBy(t, "Sole", target)
			shared := f.projectLedBy(t, "Shared", target, f.admin.ID)
			targetCtx := WithUserID(ctx, target)
			todo, err := f.st.CreateTodo(targetCtx, shared.ID, CreateTodoInput{Title: "Attributed"}, ModeFull)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.st.CreateWebhook(ctx, target, CreateWebhookInput{ProjectID: shared.ID, URL: "https://hooks.example.com/a", Events: []string{"todo.created"}}); err != nil {
				t.Fatal(err)
			}
			raw := APITokenPrefix + "survives-refusal"
			if _, err := f.db.Exec(`INSERT INTO api_tokens(user_id, token_hash, name, created_at, is_service) VALUES (?, ?, 'flagged', 1, 1)`, target, hashToken(raw)); err != nil {
				t.Fatal(err)
			}
			if kind == "human" {
				if _, _, err := f.st.CreateSession(ctx, target, 1<<40); err != nil {
					t.Fatal(err)
				}
			}

			snapshot := func() map[string]int {
				out := map[string]int{}
				for label, query := range map[string]string{
					"users":      `SELECT COUNT(*) FROM users`,
					"tokens":     `SELECT COUNT(*) FROM api_tokens WHERE revoked_at IS NULL`,
					"sessions":   `SELECT COUNT(*) FROM sessions`,
					"members":    `SELECT COUNT(*) FROM project_members`,
					"webhooks":   `SELECT COUNT(*) FROM webhooks`,
					"archive":    `SELECT COUNT(*) FROM archived_service_api_tokens`,
					"audit":      `SELECT COUNT(*) FROM audit_events`,
					"attributed": `SELECT COUNT(*) FROM todos WHERE created_by_user_id IS NOT NULL`,
				} {
					out[label] = f.count(t, query)
				}
				return out
			}
			before := snapshot()

			if err := f.st.DeleteUser(ctx, f.owner.ID, target); !errors.Is(err, ErrConflict) {
				t.Fatalf("err = %v, want ErrConflict", err)
			}
			after := snapshot()
			for label, n := range before {
				if after[label] != n {
					t.Errorf("%s went from %d to %d", label, n, after[label])
				}
			}
			if u, err := f.st.GetUserByAPIToken(ctx, raw); err != nil || u.ID != target {
				t.Fatalf("the token stopped working after a refusal: %+v, %v", u, err)
			}

			f.addMember(t, project.ID, f.admin.ID, RoleMaintainer)
			if err := f.st.DeleteUser(ctx, f.owner.ID, target); err != nil {
				t.Fatalf("delete once safe: %v", err)
			}
			if _, err := f.st.GetUserByAPIToken(ctx, raw); !errors.Is(err, ErrNotFound) {
				t.Errorf("token after the deletion committed: err = %v, want ErrNotFound", err)
			}
			if n := f.count(t, `SELECT COUNT(*) FROM archived_service_api_tokens WHERE origin_user_id = ?`, target); n != 1 {
				t.Errorf("%d archive rows, want 1", n)
			}
			if n := f.count(t, `SELECT COUNT(*) FROM todos WHERE id = ?`, todo.ID); n != 1 {
				t.Error("the todo should survive its author's deletion")
			}
		})
	}
}

// peerChanges each take away the other Maintainer of a project, committing through the fixture's own
// connection while the store call under test is part way through.
func peerChanges(f *serviceProjectFixture, target, peer int64, projectID int64) map[string]func() error {
	ctx := context.Background()
	return map[string]func() error{
		"removed": func() error { return f.st.RemoveProjectMember(ctx, target, projectID, peer) },
		"demoted": func() error { return f.st.UpdateProjectMemberRole(ctx, target, projectID, peer, RoleViewer) },
		"deleted": func() error { return f.st.DeleteUser(ctx, f.owner.ID, peer) },
	}
}

func TestDeleteUser_ReadsMaintainersOnItsOwnTransaction(t *testing.T) {
	for _, change := range []string{"removed", "demoted", "deleted"} {
		t.Run(change, func(t *testing.T) {
			f := newServiceProjectFixture(t)
			target, peer := f.human.ID, f.admin.ID
			project := f.projectLedBy(t, "Shared", target, peer)
			hooked, hook := f.hooked(t)
			apply := peerChanges(f, target, peer, project.ID)[change]
			hook.arm(hookAtStatement(hookAtBegin), func() {
				if err := apply(); err != nil {
					t.Errorf("concurrent change: %v", err)
				}
			})

			err := hooked.DeleteUser(context.Background(), f.owner.ID, target)
			hook.assertFired(t)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("err = %v, want the check to see the committed change and return ErrConflict", err)
			}
			if !f.userExists(t, target) || f.maintainerCount(t, project.ID) != 1 {
				t.Fatalf("project lost its last Maintainer: exists=%v, maintainers=%d", f.userExists(t, target), f.maintainerCount(t, project.ID))
			}
		})
	}
}

func TestDeleteUser_RefusedWhenMaintainersChangeAfterTheCheck(t *testing.T) {
	for _, change := range []string{"removed", "demoted", "deleted"} {
		t.Run(change, func(t *testing.T) {
			f := newServiceProjectFixture(t)
			target, peer := f.human.ID, f.admin.ID
			project := f.projectLedBy(t, "Shared", target, peer)
			raw := APITokenPrefix + "stale-delete"
			if _, err := f.db.Exec(`INSERT INTO api_tokens(user_id, token_hash, created_at, is_service) VALUES (?, ?, 1, 1)`, target, hashToken(raw)); err != nil {
				t.Fatal(err)
			}
			hooked, hook := f.hooked(t)
			apply := peerChanges(f, target, peer, project.ID)[change]
			hook.arm(hookAtFirstWrite, func() {
				if err := apply(); err != nil {
					t.Errorf("concurrent change: %v", err)
				}
			})

			err := hooked.DeleteUser(context.Background(), f.owner.ID, target)
			hook.assertFired(t)
			if err == nil {
				t.Fatal("deletion succeeded on a stale Maintainer count")
			}
			if !f.userExists(t, target) || f.maintainerCount(t, project.ID) != 1 {
				t.Fatalf("project lost its last Maintainer: exists=%v, maintainers=%d", f.userExists(t, target), f.maintainerCount(t, project.ID))
			}
			if n := f.count(t, `SELECT COUNT(*) FROM archived_service_api_tokens WHERE origin_user_id = ?`, target); n != 0 {
				t.Errorf("a refused deletion archived %d tokens", n)
			}
			if _, err := f.st.GetUserByAPIToken(context.Background(), raw); err != nil {
				t.Errorf("the token stopped working after a refusal: %v", err)
			}
		})
	}
}

// The opposite order: a membership mutation is part way through when the account deletion commits.
func TestMemberMutations_RefusedWhenPeerDeletedAfterTheirCheck(t *testing.T) {
	cases := map[string]func(s *Store, requester, projectID, target int64) error{
		"RemoveProjectMember": func(s *Store, requester, projectID, target int64) error {
			return s.RemoveProjectMember(context.Background(), requester, projectID, target)
		},
		"UpdateProjectMemberRole": func(s *Store, requester, projectID, target int64) error {
			return s.UpdateProjectMemberRole(context.Background(), requester, projectID, target, RoleViewer)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newServiceProjectFixture(t)
			first, second := f.human.ID, f.admin.ID
			project := f.projectLedBy(t, "Shared", first, second)
			hooked, hook := f.hooked(t)
			hook.arm(hookAtFirstWrite, func() {
				if err := f.st.DeleteUser(context.Background(), f.owner.ID, first); err != nil {
					t.Errorf("concurrent deletion: %v", err)
				}
			})

			err := mutate(hooked, first, project.ID, second)
			hook.assertFired(t)
			if err == nil {
				t.Fatal("mutation succeeded on a stale Maintainer count")
			}
			if n := f.maintainerCount(t, project.ID); n != 1 {
				t.Fatalf("%d Maintainers, want the survivor kept", n)
			}
		})
	}
}

func TestDeleteUser_ConcurrentDeletionsKeepAMaintainer(t *testing.T) {
	f := newServiceProjectFixture(t)
	ctx := context.Background()
	for round := 0; round < 20; round++ {
		peer, err := f.st.CreateServiceAccount(ctx, f.owner.ID, fmt.Sprintf("Peer %d", round))
		if err != nil {
			t.Fatal(err)
		}
		other, err := f.st.CreateServiceAccount(ctx, f.owner.ID, fmt.Sprintf("Other %d", round))
		if err != nil {
			t.Fatal(err)
		}
		project := f.projectLedBy(t, fmt.Sprintf("Race %d", round), peer.ID, other.ID)
		var wg sync.WaitGroup
		results := make([]error, 2)
		for i, id := range []int64{peer.ID, other.ID} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i] = f.st.DeleteUser(ctx, f.owner.ID, id)
			}()
		}
		wg.Wait()
		if n := f.maintainerCount(t, project.ID); n != 1 {
			t.Fatalf("round %d: %d Maintainers after %v", round, n, results)
		}
		if results[0] == nil && results[1] == nil {
			t.Fatalf("round %d: both deletions succeeded", round)
		}
	}
}

func TestAddProjectMember_LegacyOwnerRoleIsOnlyRefusedForServiceAccounts(t *testing.T) {
	f := newServiceProjectFixture(t)
	ctx := f.ownerCtx

	if err := f.st.AddProjectMember(ctx, f.owner.ID, f.durable.ID, f.svc.ID, RoleOwner); !errors.Is(err, ErrServiceAccountRestricted) {
		t.Fatalf("service account as owner: err = %v, want ErrServiceAccountRestricted", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM project_members WHERE user_id = ?`, f.svc.ID); n != 0 {
		t.Fatalf("a refused grant left %d membership rows", n)
	}
	if err := f.st.AddProjectMember(ctx, f.owner.ID, f.durable.ID, f.human.ID, RoleOwner); err != nil {
		t.Fatalf("same call for a human: %v", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM project_members WHERE user_id = ? AND role = 'owner'`, f.human.ID); n != 1 {
		t.Fatalf("human legacy owner row missing: %d", n)
	}
	if err := f.st.AddProjectMember(ctx, f.owner.ID, f.durable.ID, f.svc.ID, RoleMaintainer); err != nil {
		t.Fatalf("service account as Maintainer: %v", err)
	}
	other := f.projectLedBy(t, "Requester has no role here")
	if err := f.st.AddProjectMember(ctx, f.owner.ID, other.ID, f.svc.ID, RoleOwner); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a non-Maintainer learns the target's account type: err = %v, want ErrUnauthorized", err)
	}
}

func TestAddProjectMember_ConcurrentOwnerGrantsToServiceAccountAllFail(t *testing.T) {
	f := newServiceProjectFixture(t)
	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = f.st.AddProjectMember(f.ownerCtx, f.owner.ID, f.durable.ID, f.svc.ID, RoleOwner)
		}()
	}
	wg.Wait()
	for i, err := range results {
		if !errors.Is(err, ErrServiceAccountRestricted) {
			t.Errorf("attempt %d: err = %v, want ErrServiceAccountRestricted", i, err)
		}
	}
	if n := f.count(t, `SELECT COUNT(*) FROM project_members WHERE user_id = ?`, f.svc.ID); n != 0 {
		t.Errorf("%d membership rows", n)
	}
}

// The system Owner's authority, and the last-Owner rule, still come first and are unchanged.
func TestDeleteUser_OwnerProtectionsPrecedeTheMaintainerCheck(t *testing.T) {
	f := newServiceProjectFixture(t)
	ctx := context.Background()
	f.projectLedBy(t, "Sole", f.human.ID)

	if err := f.st.DeleteUser(ctx, f.admin.ID, f.human.ID); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("an Admin: err = %v, want ErrUnauthorized and no hint about the project", err)
	}
	if err := f.st.DeleteUser(ctx, f.human.ID, f.owner.ID); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("a plain user: err = %v, want ErrUnauthorized", err)
	}
	if err := f.st.DeleteUser(ctx, f.owner.ID, f.owner.ID); !errors.Is(err, ErrValidation) {
		t.Errorf("self: err = %v, want ErrValidation", err)
	}
	if err := f.st.DeleteUser(ctx, f.owner.ID, 987654); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing account: err = %v, want ErrNotFound", err)
	}
}

// The limit of token revocation, pinned so the documentation cannot claim more. Revocation stops the
// next authentication. It removes no membership, so a request that had already authenticated keeps
// its project authority until it ends. Deleting the account does remove the membership, so later
// checks in that same request fail.
func TestTokenRevocation_EndsAuthenticationNotAnInFlightRequest(t *testing.T) {
	f := newServiceProjectFixture(t)
	ctx := context.Background()
	raw := APITokenPrefix + "in-flight"
	f.seedToken(t, raw, true, false)
	f.addMember(t, f.durable.ID, f.svc.ID, RoleMaintainer)
	u, err := f.st.GetUserByAPIToken(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	inFlight := WithServiceIdentity(WithUserID(ctx, u.ID), u.ID, u.IsService)

	if _, err := f.db.Exec(`UPDATE api_tokens SET revoked_at = 1 WHERE token_hash = ?`, hashToken(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.GetUserByAPIToken(ctx, raw); !errors.Is(err, ErrNotFound) {
		t.Fatalf("next authentication: err = %v, want ErrNotFound", err)
	}
	if _, err := f.st.CreateTodo(inFlight, f.durable.ID, CreateTodoInput{Title: "Same request"}, ModeFull); err != nil {
		t.Fatalf("an already authenticated request after revocation: %v", err)
	}

	if err := f.st.DeleteUser(ctx, f.owner.ID, f.svc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreateTodo(inFlight, f.durable.ID, CreateTodoInput{Title: "After deletion"}, ModeFull); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the same request after account deletion: err = %v, want ErrNotFound", err)
	}
}

// An in-flight request holding a Maintainer's authority cannot delete the project once that authority
// has gone, whichever way it went.
func TestDeleteProject_RechecksAuthorityInItsTransaction(t *testing.T) {
	for name, revoke := range map[string]func(f *serviceProjectFixture, projectID int64) error{
		"account deleted": func(f *serviceProjectFixture, _ int64) error {
			return f.st.DeleteUser(context.Background(), f.owner.ID, f.svc.ID)
		},
		"membership removed": func(f *serviceProjectFixture, projectID int64) error {
			return f.st.RemoveProjectMember(f.ownerCtx, f.owner.ID, projectID, f.svc.ID)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newServiceProjectFixture(t)
			f.addMember(t, f.durable.ID, f.svc.ID, RoleMaintainer)
			hooked, hook := f.hooked(t)
			hook.arm(hookAtStatement(hookAtBegin), func() {
				if err := revoke(f, f.durable.ID); err != nil {
					t.Errorf("revoke: %v", err)
				}
			})

			_, err := hooked.DeleteProject(f.svcCtx, f.durable.ID, f.svc.ID)
			hook.assertFired(t)
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
			if n := f.count(t, `SELECT COUNT(*) FROM projects WHERE id = ?`, f.durable.ID); n != 1 {
				t.Fatal("the project was deleted on stale authority")
			}
		})
	}
}
