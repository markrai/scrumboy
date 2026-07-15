package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"scrumboy/internal/db"
	"scrumboy/internal/mailer/mailertest"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

// TestEmailNotify_EndToEnd_TodoAssignedOverRealSMTP drives the full stack
// through a real HTTP server against a real (fake) SMTP listener: HTTP
// mutation -> event bus -> emailNotifier -> mailQueue/mailWorker -> a real
// net/smtp send. This is the same harness pattern used by
// TestRequestPasswordReset_SMTPDebugLogsSendAttempt for #128.
//
// Built directly on store.New/migrate.Apply/NewServer (rather than the
// shared newTestHTTPServerWithOptions helper) because todo.assigned only
// fires when the store's assignment publisher is wired to the server, which
// is normally done in cmd/scrumboy/main.go — the shared helper doesn't do
// this, and both need to share the same *store.Store instance.
func TestEmailNotify_EndToEnd_TodoAssignedOverRealSMTP(t *testing.T) {
	fake, err := mailertest.Start(mailertest.Options{})
	if err != nil {
		t.Fatalf("start fake smtp server: %v", err)
	}
	defer fake.Close()
	host, port := fake.HostPort()

	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer sqlDB.Close()
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	st := store.New(sqlDB, nil)
	srv := NewServer(st, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		SMTPTLSMode:    "none",
		SMTPHost:       host,
		SMTPPort:       port,
		SMTPFrom:       "no-reply@example.com",
		PublicBaseURL:  "https://scrumboy.example.com",
	})
	st.SetTodoAssignedPublisher(srv.PublishTodoAssigned)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	client := newCookieClient(t)
	owner := bootstrapUserClient(t, client, ts.URL, "Owner", "owner-e2e@example.com", "password123")
	ownerID := int64(owner["id"].(float64))

	var proj map[string]any
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "E2E Project"}, &proj)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create project: status=%d body=%s", resp.StatusCode, string(body))
	}
	projectID := int64(proj["id"].(float64))
	slug := proj["slug"].(string)

	assignee, err := st.CreateUser(context.Background(), "assignee-e2e@example.com", "password123", "Assignee")
	if err != nil {
		t.Fatalf("create assignee: %v", err)
	}
	if err := st.AddProjectMember(store.WithUserID(context.Background(), ownerID), ownerID, projectID, assignee.ID, store.RoleViewer); err != nil {
		t.Fatalf("add project member: %v", err)
	}

	// Assignee opts in via the real preferences endpoint, as a second signed-in session.
	assigneeClient := newCookieClient(t)
	loginResp, loginBody := doJSON(t, assigneeClient, http.MethodPost, ts.URL+"/api/auth/login", map[string]any{
		"email": "assignee-e2e@example.com", "password": "password123",
	}, nil)
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("assignee login: status=%d body=%s", loginResp.StatusCode, string(loginBody))
	}
	pref := store.DefaultEmailNotifyPref()
	pref.Enabled = true
	prefJSON, _ := json.Marshal(pref)
	prefResp, prefBody := doJSON(t, assigneeClient, http.MethodPut, ts.URL+"/api/user/preferences", map[string]any{
		"key": "emailNotifications", "value": string(prefJSON),
	}, nil)
	if prefResp.StatusCode != http.StatusNoContent {
		t.Fatalf("set emailNotifications pref: status=%d body=%s", prefResp.StatusCode, string(prefBody))
	}

	// Owner creates a card assigned directly to the assignee.
	var todo map[string]any
	todoResp, todoBody := doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+slug+"/todos", map[string]any{
		"title":          "Ship the feature",
		"assigneeUserId": assignee.ID,
	}, &todo)
	if todoResp.StatusCode != http.StatusCreated && todoResp.StatusCode != http.StatusOK {
		t.Fatalf("create assigned todo: status=%d body=%s", todoResp.StatusCode, string(todoBody))
	}

	msgs := waitForMessages(t, fake, 1)
	if msgs[0].To != "assignee-e2e@example.com" {
		t.Fatalf("expected email to assignee, got %+v", msgs[0])
	}
	if msgs[0].Subject == "" {
		t.Fatalf("expected non-empty subject, got %+v", msgs[0])
	}
}
