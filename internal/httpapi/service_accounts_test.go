package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"scrumboy/internal/auth/tokens"
	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

// The store is the authoritative boundary (see internal/store/service_accounts_test.go). These tests
// pin what the store's rejections look like from outside: a service account must be indistinguishable
// from an account that does not exist, and no route may turn it into something interactive.

type serviceAccountHTTPFixture struct {
	ts      string
	db      *sql.DB
	st      *store.Store
	owner   *http.Client
	ownerID int64
	svc     store.User
}

func newServiceAccountHTTPFixture(t *testing.T) (*serviceAccountHTTPFixture, func() []string) {
	t.Helper()
	ts, fake, database, cleanup := newRequestPasswordResetTestServerWithDB(t, true, false)
	t.Cleanup(cleanup)
	owner := newCookieClient(t)
	ownerJSON := bootstrapUserClient(t, owner, ts.URL, "Owner", "owner@example.com", "password123")
	ownerID := int64(ownerJSON["id"].(float64))

	st := store.New(database, &store.StoreOptions{EncryptionKey: testEncryptionKey})
	svc, err := st.CreateServiceAccount(context.Background(), ownerID, "Bot")
	if err != nil {
		t.Fatalf("create service account: %v", err)
	}
	mail := func() []string {
		time.Sleep(150 * time.Millisecond) // Give the async worker a beat to (incorrectly) fire.
		var to []string
		for _, m := range fake.Messages() {
			to = append(to, m.To)
		}
		return to
	}
	return &serviceAccountHTTPFixture{ts: ts.URL, db: database, st: st, owner: owner, ownerID: ownerID, svc: svc}, mail
}

func TestServiceAccountHTTP_LoginLooksLikeUnknownAccount(t *testing.T) {
	f, _ := newServiceAccountHTTPFixture(t)
	client := newCookieClient(t)

	unknownResp, unknownBody := doJSON(t, client, http.MethodPost, f.ts+"/api/auth/login", map[string]any{
		"email": "svc-00000000-0000-4000-8000-000000000000@service.invalid", "password": "password123",
	}, nil)
	svcResp, svcBody := doJSON(t, client, http.MethodPost, f.ts+"/api/auth/login", map[string]any{
		"email": f.svc.Email, "password": "password123",
	}, nil)

	if svcResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login status = %d, want 401: %s", svcResp.StatusCode, svcBody)
	}
	if unknownResp.StatusCode != svcResp.StatusCode || string(unknownBody) != string(svcBody) {
		t.Fatalf("service account is distinguishable from an unknown account:\n%d %s\n%d %s",
			unknownResp.StatusCode, unknownBody, svcResp.StatusCode, svcBody)
	}
	if len(svcResp.Cookies()) != 0 {
		t.Fatalf("failed login set cookies: %v", svcResp.Cookies())
	}
}

func TestServiceAccountHTTP_PlaceholderCannotRequestPasswordReset(t *testing.T) {
	f, mail := newServiceAccountHTTPFixture(t)
	client := newCookieClient(t)

	resp, body := doJSON(t, client, http.MethodPost, f.ts+"/api/auth/request-password-reset", map[string]any{"email": f.svc.Email}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	if to := mail(); len(to) != 0 {
		t.Fatalf("mail was sent for a placeholder address: %v", to)
	}
}

// Even a correctly signed reset token cannot give a service account a password.
func TestServiceAccountHTTP_SignedResetTokenCannotSetPassword(t *testing.T) {
	f, _ := newServiceAccountHTTPFixture(t)
	client := newCookieClient(t)

	for _, hash := range []string{"", "$2a$10$abcdefghijklmnopqrstuuabcdefghijklmnopqrstuvwxyz01234"} {
		token, _, err := tokens.GeneratePasswordResetToken(testEncryptionKey, f.svc.ID, hash)
		if err != nil {
			t.Fatal(err)
		}
		resp, body := doJSON(t, client, http.MethodPost, f.ts+"/api/auth/reset-password", map[string]any{
			"token": token, "new_password": "Replacement123!",
		}, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("reset status = %d, want 400: %s", resp.StatusCode, body)
		}
	}
	var hash sql.NullString
	if err := f.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, f.svc.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash.Valid {
		t.Fatal("reset gave the service account a password")
	}
}

func TestServiceAccountHTTP_AdminRoutesCannotPrivilegeOrResetIt(t *testing.T) {
	f, _ := newServiceAccountHTTPFixture(t)

	resp, body := doJSON(t, f.owner, http.MethodPatch, fmt.Sprintf("%s/api/admin/users/%d/role", f.ts, f.svc.ID), map[string]any{"role": "admin"}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("promote status = %d, want 403: %s", resp.StatusCode, body)
	}
	got, err := f.st.GetUser(context.Background(), f.svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SystemRole != store.SystemRoleUser {
		t.Fatalf("role = %q after rejected promotion", got.SystemRole)
	}

	resp, body = doJSON(t, f.owner, http.MethodPost, fmt.Sprintf("%s/api/admin/users/%d/password-reset", f.ts, f.svc.ID), nil, nil)
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		t.Fatalf("admin issued a reset link for a service account: %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, f.owner, http.MethodPost, f.ts+"/api/admin/users", map[string]any{
		"email": "impostor@service.invalid", "password": "password123", "name": "Impostor",
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("creating a human on the reserved domain: status = %d, want 400: %s", resp.StatusCode, body)
	}
}

// A session row for a service account (dropped trigger: a hand-edited or restored database) does not
// authenticate, while the same request with a human's session does.
func TestServiceAccountHTTP_StaleSessionCookieDoesNotAuthenticate(t *testing.T) {
	f, _ := newServiceAccountHTTPFixture(t)
	if _, err := f.db.Exec(`DROP TRIGGER trg_sessions_reject_service_insert`); err != nil {
		t.Fatal(err)
	}
	const raw = "stale-service-account-session"
	sum := sha256.Sum256([]byte(raw))
	now := time.Now().UnixMilli()
	if _, err := f.db.Exec(`INSERT INTO sessions(user_id, token_hash, created_at, expires_at, last_seen_at) VALUES (?, ?, ?, ?, ?)`,
		f.svc.ID, hex.EncodeToString(sum[:]), now, now+3600_000, now); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodGet, f.ts+"/api/me/tokens", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Scrumboy", "1")
	req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: raw})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stale service session: status = %d, want 401", resp.StatusCode)
	}

	resp, body := doJSON(t, f.owner, http.MethodGet, f.ts+"/api/me/tokens", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("human session: status = %d: %s", resp.StatusCode, body)
	}
}

func addedToProjectEvent(t *testing.T, projectID, actorID, affectedID int64) eventbus.Event {
	t.Helper()
	payload, err := json.Marshal(eventbus.MembershipPayload{ProjectID: projectID, AffectedUserID: affectedID, Action: "added", ActorUserID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	return eventbus.Event{Type: "project.membership", ProjectID: projectID, Payload: payload}
}

// Notifications skip a service account by identity, through the real store, on every send path that
// picks a recipient, while a human member on the same project still gets mail.
func TestEmailNotifier_NeverSelectsServiceAccounts(t *testing.T) {
	f, _ := newServiceAccountHTTPFixture(t)
	ctx := store.WithUserID(context.Background(), f.ownerID)
	project, err := f.st.CreateProject(ctx, "Notified")
	if err != nil {
		t.Fatal(err)
	}
	member, err := f.st.CreateUser(context.Background(), "member@example.com", "password123", "Member")
	if err != nil {
		t.Fatal(err)
	}
	for id, role := range map[int64]store.ProjectRole{f.svc.ID: store.RoleMaintainer, member.ID: store.RoleContributor} {
		if err := f.st.AddProjectMember(ctx, f.ownerID, project.ID, id, role); err != nil {
			t.Fatal(err)
		}
	}
	pref, err := json.Marshal(enabledEmailNotifyPref())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{f.svc.ID, member.ID} {
		if err := f.st.SetUserPreference(context.Background(), id, "emailNotifications", string(pref)); err != nil {
			t.Fatal(err)
		}
	}
	todo, err := f.st.CreateTodo(ctx, project.ID, store.CreateTodoInput{Title: "Assigned", AssigneeUserID: &f.svc.ID}, store.ModeFull)
	if err != nil {
		t.Fatal(err)
	}

	assigned, err := json.Marshal(eventbus.TodoAssignedPayload{
		ProjectID: project.ID, TodoID: todo.ID, LocalID: todo.LocalID, Title: todo.Title, Reason: "todo_assigned",
		ActivityReason: "todo_created", ActorUserID: f.ownerID, ToAssigneeUID: &f.svc.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	dueSoon, err := json.Marshal(eventbus.TodoDueSoonPayload{
		ProjectID: project.ID, TodoID: todo.ID, LocalID: todo.LocalID, Title: todo.Title, ColumnKey: todo.ColumnKey,
		AssigneeUserID: &f.svc.ID, AssigneeStableUserID: f.svc.PublicID, DueAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}

	q := newMailQueue(discardLogger())
	n := newEmailNotifier(f.st, q, "https://scrumboy.example.com", true, discardLogger())
	n.handle(context.Background(), eventbus.Event{Type: "todo.assigned", ProjectID: project.ID, Payload: assigned})
	n.handleMembership(context.Background(), addedToProjectEvent(t, project.ID, f.ownerID, f.svc.ID))
	n.handleDueSoon(context.Background(), eventbus.Event{Type: eventbus.TodoDueSoonEventType, ProjectID: project.ID, Payload: dueSoon})

	got := q.Drain()
	if len(got) != 1 || got[0].To != member.Email {
		t.Fatalf("deliveries = %+v, want only the human member's activity mail", got)
	}

	// The same membership event for a human does send, so the case above failed for the right reason.
	n.handleMembership(context.Background(), addedToProjectEvent(t, project.ID, f.ownerID, member.ID))
	if got := q.Drain(); len(got) != 1 || got[0].To != member.Email {
		t.Fatalf("human added-to-project deliveries = %+v", got)
	}
}

// An existing human on the reserved domain is not a service account and keeps their mail.
func TestMailWorker_DeliversToLegacyHumanOnReservedDomain(t *testing.T) {
	sender := &fakeMailSender{}
	q := newMailQueue(discardLogger())
	w := newMailWorker(q, sender, discardLogger())
	w.deliver(mailDelivery{To: "legacy@service.invalid", Subject: "s", Body: "b", LogRef: "legacy"})
	if len(sender.sent) != 1 || sender.sent[0].To != "legacy@service.invalid" {
		t.Fatalf("sent = %+v, want the legacy human's mail", sender.sent)
	}
}

// A browser session is only ever a human's, and the request context says so without another query.
func TestRequestContext_RecordsHumanIdentityForSessions(t *testing.T) {
	f, _ := newServiceAccountHTTPFixture(t)
	token, _, err := f.st.CreateSession(context.Background(), f.ownerID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(f.st, Options{MaxRequestBody: 1 << 20, ScrumboyMode: "full"})
	req, err := http.NewRequest(http.MethodGet, f.ts+"/api/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: token})
	ctx := srv.requestContext(req)
	if isService, ok := store.ServiceIdentityFromContext(ctx, f.ownerID); !ok || isService {
		t.Fatalf("recorded identity = %v, %v, want a known human", isService, ok)
	}
}

// The REST admin route reports the conflict as such, for either account type, and changes nothing.
func TestAdminDeleteUser_SoleMaintainerIsAConflict(t *testing.T) {
	f, _ := newServiceAccountHTTPFixture(t)
	ownerCtx := store.WithUserID(context.Background(), f.ownerID)
	human, err := f.st.CreateUser(context.Background(), "maintainer@example.com", "password123", "Maintainer")
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]int64{"service": f.svc.ID, "human": human.ID} {
		t.Run(name, func(t *testing.T) {
			project, err := f.st.CreateProject(ownerCtx, "Led by "+name)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.st.AddProjectMember(ownerCtx, f.ownerID, project.ID, id, store.RoleMaintainer); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.Exec(`DELETE FROM project_members WHERE project_id = ? AND user_id = ?`, project.ID, f.ownerID); err != nil {
				t.Fatal(err)
			}

			url := f.ts + fmt.Sprintf("/api/admin/users/%d", id)
			resp, body := doJSON(t, f.owner, http.MethodDelete, url, nil, nil)
			if resp.StatusCode != http.StatusConflict {
				t.Fatalf("status = %d: %s", resp.StatusCode, body)
			}
			var row int
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM users WHERE id = ?`, id).Scan(&row); err != nil {
				t.Fatal(err)
			}
			if row != 1 {
				t.Fatal("the account was deleted")
			}

			if err := f.st.AddProjectMember(store.WithUserID(context.Background(), id), id, project.ID, f.ownerID, store.RoleMaintainer); err != nil {
				t.Fatal(err)
			}
			if resp, body := doJSON(t, f.owner, http.MethodDelete, url, nil, nil); resp.StatusCode != http.StatusNoContent {
				t.Fatalf("delete once safe: status = %d: %s", resp.StatusCode, body)
			}
		})
	}
}
