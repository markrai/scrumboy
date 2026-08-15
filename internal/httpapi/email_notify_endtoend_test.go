package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
	defer srv.Close(context.Background())
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
	member, err := st.CreateUser(context.Background(), "member-e2e@example.com", "password123", "Member")
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	if err := st.AddProjectMember(store.WithUserID(context.Background(), ownerID), ownerID, projectID, member.ID, store.RoleViewer); err != nil {
		t.Fatalf("add second project member: %v", err)
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
	pref.CardActivity = true
	prefJSON, _ := json.Marshal(pref)
	prefResp, prefBody := doJSON(t, assigneeClient, http.MethodPut, ts.URL+"/api/user/preferences", map[string]any{
		"key": "emailNotifications", "value": string(prefJSON),
	}, nil)
	if prefResp.StatusCode != http.StatusNoContent {
		t.Fatalf("set emailNotifications pref: status=%d body=%s", prefResp.StatusCode, string(prefBody))
	}
	if err := st.SetUserPreference(context.Background(), member.ID, "emailNotifications", string(prefJSON)); err != nil {
		t.Fatalf("set second member preference: %v", err)
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

	msgs := waitForMessages(t, fake, 2)
	first := map[string]string{}
	for _, message := range msgs {
		first[message.To] = message.Subject
	}
	if !strings.Contains(first["assignee-e2e@example.com"], "Assigned to you") {
		t.Fatalf("expected assignment delivery to assignee, got %+v", msgs)
	}
	if !strings.Contains(first["member-e2e@example.com"], "card created") {
		t.Fatalf("expected card activity delivery to other member, got %+v", msgs)
	}
	localID := int64(todo["localId"].(float64))
	updateResp, updateBody := doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+slug+"/todos/"+strconv.FormatInt(localID, 10), map[string]any{
		"title":          "Ship the corrected feature",
		"assigneeUserId": member.ID,
	}, nil)
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("update assigned todo: status=%d body=%s", updateResp.StatusCode, string(updateBody))
	}

	msgs = waitForMessages(t, fake, 4)
	assignmentByRecipient := map[string]int{}
	activityByRecipient := map[string]int{}
	for _, message := range msgs {
		if strings.Contains(message.Subject, "Assigned to you") {
			assignmentByRecipient[message.To]++
		}
		if strings.Contains(message.Subject, "card created") || strings.Contains(message.Subject, "card updated") {
			activityByRecipient[message.To]++
		}
		if message.To == "owner-e2e@example.com" {
			t.Fatalf("actor received a self-notification: %+v", msgs)
		}
	}
	if assignmentByRecipient["assignee-e2e@example.com"] != 1 || assignmentByRecipient["member-e2e@example.com"] != 1 {
		t.Fatalf("expected one assignment delivery per assignment recipient, got %+v", msgs)
	}
	if activityByRecipient["member-e2e@example.com"] != 1 || activityByRecipient["assignee-e2e@example.com"] != 1 {
		t.Fatalf("expected card activity only for the other eligible member on each combined event, got %+v", msgs)
	}

	// The original owner is the card's historical creator. Let the other
	// member mutate it as a maintainer and opt the owner into every overlapping
	// creator/activity category. The creator policy must still deliver once.
	ownerCtx := store.WithUserID(context.Background(), ownerID)
	if err := st.UpdateProjectMemberRole(ownerCtx, ownerID, projectID, member.ID, store.RoleMaintainer); err != nil {
		t.Fatalf("promote member actor: %v", err)
	}
	ownerPref := store.DefaultEmailNotifyPref()
	ownerPref.Enabled = true
	ownerPref.CreatedByMe = true
	ownerPref.CardActivity = true
	ownerPrefJSON, _ := json.Marshal(ownerPref)
	if err := st.SetUserPreference(context.Background(), ownerID, "emailNotifications", string(ownerPrefJSON)); err != nil {
		t.Fatalf("enable creator preference: %v", err)
	}
	memberClient := newCookieClient(t)
	loginResp, loginBody = doJSON(t, memberClient, http.MethodPost, ts.URL+"/api/auth/login", map[string]any{
		"email": "member-e2e@example.com", "password": "password123",
	}, nil)
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("member login: status=%d body=%s", loginResp.StatusCode, string(loginBody))
	}
	creatorUpdate := map[string]any{
		"title": "Ship the corrected feature", "body": "creator-visible material change", "tags": []string{},
		"estimationPoints": nil, "assigneeUserId": member.ID,
	}
	updateResp, updateBody = doJSON(t, memberClient, http.MethodPatch,
		ts.URL+"/api/board/"+slug+"/todos/"+strconv.FormatInt(localID, 10), creatorUpdate, nil)
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("creator-notify update: status=%d body=%s", updateResp.StatusCode, string(updateBody))
	}
	msgs = waitForMessages(t, fake, 5)
	ownerMessages := 0
	for _, message := range msgs {
		if message.To == "owner-e2e@example.com" {
			ownerMessages++
			if !strings.Contains(message.Subject, "card you opened") {
				t.Fatalf("owner received wrong precedence category: %+v", message)
			}
		}
	}
	if ownerMessages != 1 {
		t.Fatalf("creator received %d emails for one mutation, messages=%+v", ownerMessages, msgs)
	}

	// Repeating the identical replacement payload is a successful semantic
	// no-op. It may retain the existing refresh contract, but must not add any
	// creator-category or card-activity fallback mail for the creator.
	updateResp, updateBody = doJSON(t, memberClient, http.MethodPatch,
		ts.URL+"/api/board/"+slug+"/todos/"+strconv.FormatInt(localID, 10), creatorUpdate, nil)
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("creator semantic no-op: status=%d body=%s", updateResp.StatusCode, string(updateBody))
	}
	time.Sleep(300 * time.Millisecond)
	if got := len(fake.Messages()); got != 5 {
		t.Fatalf("semantic no-op produced email: count=%d messages=%+v", got, fake.Messages())
	}

	creatorAssignment := map[string]any{
		"title": "Ship the corrected feature", "body": "assignment overlap", "tags": []string{},
		"estimationPoints": nil, "assigneeUserId": ownerID,
	}
	updateResp, updateBody = doJSON(t, memberClient, http.MethodPatch,
		ts.URL+"/api/board/"+slug+"/todos/"+strconv.FormatInt(localID, 10), creatorAssignment, nil)
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("creator assignment overlap: status=%d body=%s", updateResp.StatusCode, string(updateBody))
	}
	msgs = waitForMessages(t, fake, 6)
	ownerAssignments := 0
	ownerMessages = 0
	for _, message := range msgs {
		if message.To != "owner-e2e@example.com" {
			continue
		}
		ownerMessages++
		if strings.Contains(message.Subject, "Assigned to you") {
			ownerAssignments++
		}
	}
	if ownerMessages != 2 || ownerAssignments != 1 {
		t.Fatalf("assignment/creator/activity overlap did not collapse to one assignment email: %+v", msgs)
	}

	// Prepare the same card for the contributor-only mutation branch without a
	// creator request (the owner/creator is the actor), then prove an assigned
	// contributor's real body change still reaches creator email policy.
	assignContributor := map[string]any{
		"title": "Ship the corrected feature", "body": "prepare contributor", "tags": []string{},
		"estimationPoints": nil, "assigneeUserId": member.ID,
	}
	updateResp, updateBody = doJSON(t, client, http.MethodPatch,
		ts.URL+"/api/board/"+slug+"/todos/"+strconv.FormatInt(localID, 10), assignContributor, nil)
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("assign contributor setup: status=%d body=%s", updateResp.StatusCode, string(updateBody))
	}
	waitForMessages(t, fake, 7)
	if err := st.UpdateProjectMemberRole(ownerCtx, ownerID, projectID, member.ID, store.RoleContributor); err != nil {
		t.Fatalf("downgrade assigned member to contributor: %v", err)
	}
	contributorBodyOnly := map[string]any{
		"title": "must be ignored", "body": "assigned contributor body", "tags": []string{"must-be-ignored"},
		"estimationPoints": int64(99), "assigneeUserId": ownerID,
	}
	updateResp, updateBody = doJSON(t, memberClient, http.MethodPatch,
		ts.URL+"/api/board/"+slug+"/todos/"+strconv.FormatInt(localID, 10), contributorBodyOnly, nil)
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("assigned contributor body-only update: status=%d body=%s", updateResp.StatusCode, string(updateBody))
	}
	msgs = waitForMessages(t, fake, 8)
	ownerCreatorMessages := 0
	for _, message := range msgs {
		if message.To == "owner-e2e@example.com" && strings.Contains(message.Subject, "card you opened") {
			ownerCreatorMessages++
		}
	}
	if ownerCreatorMessages != 2 {
		t.Fatalf("assigned contributor body-only mutation did not add exactly one creator email: %+v", msgs)
	}
}
