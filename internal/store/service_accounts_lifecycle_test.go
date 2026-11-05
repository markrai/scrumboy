package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

func (f *serviceFixture) seedToken(t *testing.T, raw string, isService bool, revoked bool) {
	t.Helper()
	var revokedAt any
	if revoked {
		revokedAt = int64(5)
	}
	if _, err := f.db.Exec(`INSERT INTO api_tokens(user_id, token_hash, name, created_at, revoked_at, is_service) VALUES (?, ?, ?, 1, ?, ?)`,
		f.svc.ID, hashToken(raw), raw, revokedAt, isService); err != nil {
		t.Fatal(err)
	}
}

// Deleting a service account ends every credential and all project authority in one transaction,
// while history stays and the service-flagged token metadata is archived.
func TestServiceAccountDeletion_EndsAllAuthority(t *testing.T) {
	f := newServiceProjectFixture(t)
	ctx := context.Background()
	id := f.svc.ID

	active := []string{APITokenPrefix + "flagged-one", APITokenPrefix + "flagged-two", APITokenPrefix + "unflagged"}
	f.seedToken(t, active[0], true, false)
	f.seedToken(t, active[1], true, false)
	f.seedToken(t, active[2], false, false)
	f.seedToken(t, APITokenPrefix+"flagged-revoked", true, true)
	for _, raw := range active {
		if _, err := f.st.GetUserByAPIToken(ctx, raw); err != nil {
			t.Fatalf("token works before deletion: %v", err)
		}
	}

	if err := f.st.AddProjectMember(f.ownerCtx, f.owner.ID, f.durable.ID, id, RoleMaintainer); err != nil {
		t.Fatal(err)
	}
	todo, err := f.st.CreateTodo(f.svcCtx, f.durable.ID, CreateTodoInput{Title: "Written by the bot"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreateWebhook(ctx, id, CreateWebhookInput{ProjectID: f.durable.ID, URL: "https://hooks.example.com/bot", Events: []string{"todo.created"}}); err != nil {
		t.Fatal(err)
	}

	if err := f.st.DeleteUser(ctx, f.owner.ID, id); err != nil {
		t.Fatalf("delete: %v", err)
	}

	for _, raw := range active {
		if _, err := f.st.GetUserByAPIToken(ctx, raw); !errors.Is(err, ErrNotFound) {
			t.Errorf("token after deletion: err = %v, want ErrNotFound", err)
		}
	}
	for table, query := range map[string]string{
		"api_tokens":      `SELECT COUNT(*) FROM api_tokens WHERE user_id = ?`,
		"project_members": `SELECT COUNT(*) FROM project_members WHERE user_id = ?`,
		"webhooks":        `SELECT COUNT(*) FROM webhooks WHERE user_id = ?`,
		"sessions":        `SELECT COUNT(*) FROM sessions WHERE user_id = ?`,
		"users":           `SELECT COUNT(*) FROM users WHERE id = ?`,
	} {
		if n := f.count(t, query, id); n != 0 {
			t.Errorf("%s still has %d rows", table, n)
		}
	}
	if n := f.count(t, `SELECT COUNT(*) FROM todos WHERE id = ? AND created_by_user_id IS NULL`, todo.ID); n != 1 {
		t.Error("the todo should survive with its attribution cleared, as for any deleted user")
	}
	if n := f.count(t, `SELECT COUNT(*) FROM audit_events WHERE actor_user_id = ?`, id); n == 0 {
		t.Error("deletion lost the account's audit history")
	}

	// Only service-flagged tokens reach the archive, with active and revoked told apart.
	if n := f.count(t, `SELECT COUNT(*) FROM archived_service_api_tokens WHERE origin_user_id = ?`, id); n != 3 {
		t.Fatalf("archived %d tokens, want the 3 service-flagged ones", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM archived_service_api_tokens WHERE origin_user_id = ? AND revoked_on_archive = 1`, id); n != 2 {
		t.Errorf("%d tokens revoked by the archive, want 2", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM archived_service_api_tokens WHERE name = ?`, APITokenPrefix+"unflagged"); n != 0 {
		t.Errorf("an unflagged token was archived")
	}
	if n := f.count(t, `SELECT COUNT(*) FROM archived_service_api_tokens WHERE origin_user_id = ? AND origin_user_email = ? AND origin_user_name = ? AND archived_by_user_id = ?`, id, f.svc.Email, f.svc.Name, f.owner.ID); n != 3 {
		t.Errorf("provenance was not snapshotted on %d of 3 archive rows", 3-n)
	}
	var hasHash int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('archived_service_api_tokens') WHERE name LIKE '%hash%'`).Scan(&hasHash); err != nil || hasHash != 0 {
		t.Errorf("the archive has a hash column (%d, %v)", hasHash, err)
	}
}

func TestServiceAccountDeletion_RequiresOwnerAndCommitsAtomically(t *testing.T) {
	f := newServiceProjectFixture(t)
	ctx := context.Background()
	raw := APITokenPrefix + "survives-refused-delete"
	f.seedToken(t, raw, true, false)

	for name, requester := range map[string]int64{"admin": f.admin.ID, "plain user": f.human.ID, "itself": f.svc.ID} {
		if err := f.st.DeleteUser(ctx, requester, f.svc.ID); err == nil {
			t.Errorf("%s deleted a service account", name)
		}
	}
	if _, err := f.st.GetUserByAPIToken(ctx, raw); err != nil {
		t.Fatalf("a refused deletion affected the token: %v", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM archived_service_api_tokens`); n != 0 {
		t.Fatalf("a refused deletion wrote %d archive rows", n)
	}
}

func TestServiceToken_RevocationIsImmediateAndScoped(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	first, second := APITokenPrefix+"first", APITokenPrefix+"second"
	f.seedToken(t, first, true, false)
	f.seedToken(t, second, true, false)
	var firstID int64
	if err := f.db.QueryRow(`SELECT id FROM api_tokens WHERE token_hash = ?`, hashToken(first)).Scan(&firstID); err != nil {
		t.Fatal(err)
	}

	if err := f.st.RevokeUserAPIToken(ctx, f.human.ID, firstID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoking another user's token: err = %v, want ErrNotFound", err)
	}
	if _, err := f.st.GetUserByAPIToken(ctx, first); err != nil {
		t.Fatalf("token after a refused revocation: %v", err)
	}
	if err := f.st.RevokeUserAPIToken(ctx, f.svc.ID, firstID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := f.st.GetUserByAPIToken(ctx, first); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoked token: err = %v, want ErrNotFound", err)
	}
	if err := f.st.RevokeUserAPIToken(ctx, f.svc.ID, firstID); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoking twice: err = %v, want ErrNotFound", err)
	}
	if u, err := f.st.GetUserByAPIToken(ctx, second); err != nil || u.ID != f.svc.ID {
		t.Errorf("sibling token = %+v, %v", u, err)
	}
}

// Lookups racing a deletion either see the account before the commit or nothing after it, and an
// authorization already resolved by a request gains nothing once the row is gone.
func TestServiceAccountDeletion_ConcurrentAuthorization(t *testing.T) {
	f := newServiceProjectFixture(t)
	ctx := context.Background()
	raw := APITokenPrefix + "raced"
	f.seedToken(t, raw, true, false)
	if err := f.st.AddProjectMember(f.ownerCtx, f.owner.ID, f.durable.ID, f.svc.ID, RoleMaintainer); err != nil {
		t.Fatal(err)
	}
	authenticated, err := f.st.GetUserByAPIToken(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	inFlight := WithServiceIdentity(WithUserID(ctx, authenticated.ID), authenticated.ID, authenticated.IsService)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				u, err := f.st.GetUserByAPIToken(ctx, raw)
				if err == nil && u.ID != f.svc.ID {
					t.Errorf("token resolved to user %d", u.ID)
					return
				}
				if err != nil && !errors.Is(err, ErrNotFound) {
					t.Errorf("lookup: %v", err)
					return
				}
			}
		}()
	}
	if err := f.st.DeleteUser(ctx, f.owner.ID, f.svc.ID); err != nil {
		t.Errorf("delete: %v", err)
	}
	close(stop)
	wg.Wait()

	if _, err := f.st.GetUserByAPIToken(ctx, raw); !errors.Is(err, ErrNotFound) {
		t.Errorf("after the commit: err = %v, want ErrNotFound", err)
	}
	if _, err := f.st.GetProjectContextBySlug(inFlight, f.durable.Slug, ModeFull); !errors.Is(err, ErrNotFound) {
		t.Errorf("in-flight read: err = %v, want ErrNotFound", err)
	}
	if _, err := f.st.CreateTodo(inFlight, f.durable.ID, CreateTodoInput{Title: "After deletion"}, ModeFull); !errors.Is(err, ErrNotFound) {
		t.Errorf("in-flight write: err = %v, want ErrNotFound", err)
	}
}

// A token never follows a reused id: the row that held it went with the account.
func TestServiceAccountDeletion_ReusedIDDoesNotReviveCredentials(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	raw := APITokenPrefix + "held-by-an-integration"
	f.seedToken(t, raw, true, false)
	if err := f.st.DeleteUser(ctx, f.owner.ID, f.svc.ID); err != nil {
		t.Fatal(err)
	}
	successor, err := f.st.CreateUser(ctx, "successor@example.com", serviceTestPassword, "Successor")
	if err != nil {
		t.Fatal(err)
	}
	if successor.ID != f.svc.ID {
		t.Fatalf("expected SQLite to reuse id %d, got %d", f.svc.ID, successor.ID)
	}
	if _, err := f.st.GetUserByAPIToken(ctx, raw); !errors.Is(err, ErrNotFound) {
		t.Fatalf("token after id reuse: err = %v, want ErrNotFound", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM api_tokens WHERE user_id = ?`, successor.ID); n != 0 {
		t.Fatalf("successor inherited %d tokens", n)
	}
}

// The account's own credential cannot provision anything: not more tokens, and no other accounts.
func TestServiceAccount_CannotProvisionCredentials(t *testing.T) {
	f := newServiceFixture(t)
	ctx := WithServiceIdentity(WithUserID(context.Background(), f.svc.ID), f.svc.ID, true)
	if _, _, _, err := f.st.CreateUserAPIToken(ctx, f.svc.ID, nil, true); !errors.Is(err, ErrServiceAccountRestricted) {
		t.Errorf("own token: err = %v, want ErrServiceAccountRestricted", err)
	}
	if _, err := f.st.CreateServiceAccount(ctx, f.svc.ID, "Spawned"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("new account: err = %v, want ErrUnauthorized", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM api_tokens WHERE user_id = ?`, f.svc.ID); n != 0 {
		t.Errorf("%d tokens were minted", n)
	}
}

func TestEmailNotifyPref_ServiceAccountsNeverReceiveMail(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	raw, err := json.Marshal(EmailNotifyPref{V: EmailNotifyPrefVersion, Enabled: true, Assigned: true, AddedToProject: true, CardActivity: true, SprintActivity: true, ProjectActivity: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{f.svc.ID, f.human.ID} {
		if err := f.st.SetUserPreference(ctx, id, "emailNotifications", string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	svcPref, err := f.st.GetEmailNotifyPref(ctx, f.svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if svcPref.Enabled || svcPref.Assigned || svcPref.AddedToProject || svcPref.CardActivity || svcPref.SprintActivity || svcPref.ProjectActivity {
		t.Errorf("service account preference = %+v, want everything off", svcPref)
	}
	humanPref, err := f.st.GetEmailNotifyPref(ctx, f.human.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !humanPref.Enabled || !humanPref.CardActivity {
		t.Errorf("human preference = %+v, want it honoured", humanPref)
	}
}

// The recorded identity spares a query but is bound to the user it describes, so it cannot answer for
// anyone else, and a caller that recorded nothing gets the users row.
func TestServiceIdentityContext(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()

	if _, ok := ServiceIdentityFromContext(ctx, f.svc.ID); ok {
		t.Error("an empty context claimed to know an identity")
	}
	humanCtx := WithServiceIdentity(ctx, f.human.ID, false)
	if _, ok := ServiceIdentityFromContext(humanCtx, f.svc.ID); ok {
		t.Error("a recorded identity answered for a different user")
	}
	for name, tc := range map[string]struct {
		ctx  context.Context
		id   int64
		want bool
	}{
		"nothing recorded, service": {ctx, f.svc.ID, true},
		"nothing recorded, human":   {ctx, f.human.ID, false},
		"other user recorded":       {humanCtx, f.svc.ID, true},
		"recorded human":            {humanCtx, f.human.ID, false},
		"recorded service":          {WithServiceIdentity(ctx, f.svc.ID, true), f.svc.ID, true},
	} {
		got, err := isServiceUser(tc.ctx, f.db, tc.id)
		if err != nil || got != tc.want {
			t.Errorf("%s: isServiceUser = %v, %v, want %v", name, got, err, tc.want)
		}
	}
}
