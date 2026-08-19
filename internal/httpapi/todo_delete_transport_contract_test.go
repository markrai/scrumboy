package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/eventbus"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

type todoDeleteRESTFixture struct {
	ts        *httptest.Server
	db        *sql.DB
	store     *store.Store
	collector *collectingConsumer
}

func newTodoDeleteRESTFixture(t *testing.T, mode string) *todoDeleteRESTFixture {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(sqlDB, nil)
	collector := &collectingConsumer{}
	srv := NewServer(st, Options{MaxRequestBody: 1 << 20, ScrumboyMode: mode})
	srv.fanout = eventbus.NewFanout(newSSEBridge(srv.hub, nil), collector)
	st.SetTodoAssignedPublisher(srv.PublishTodoAssigned)
	ts := httptest.NewServer(srv)
	fixture := &todoDeleteRESTFixture{ts: ts, db: sqlDB, store: st, collector: collector}
	t.Cleanup(func() {
		ts.Close()
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		srv.Close(closeCtx)
		_ = sqlDB.Close()
	})
	return fixture
}

func todoDeleteRESTClientForUser(t *testing.T, fixture *todoDeleteRESTFixture, userID int64) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Transport: fixture.ts.Client().Transport, Jar: jar}
	token, expiresAt, err := fixture.store.CreateSession(context.Background(), userID, 24*time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	baseURL, err := url.Parse(fixture.ts.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	jar.SetCookies(baseURL, []*http.Cookie{{
		Name:    "scrumboy_session",
		Value:   token,
		Path:    "/",
		Expires: expiresAt,
	}})
	return client
}

func newTodoDeleteRESTOwner(t *testing.T, fixture *todoDeleteRESTFixture, email string) (store.User, context.Context, *http.Client) {
	t.Helper()
	owner, err := fixture.store.BootstrapUser(context.Background(), email, "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx := store.WithUserID(context.Background(), owner.ID)
	return owner, ctx, todoDeleteRESTClientForUser(t, fixture, owner.ID)
}

func createTodoDeleteRESTTodo(t *testing.T, fixture *todoDeleteRESTFixture, ctx context.Context, projectID int64, title string) store.Todo {
	t.Helper()
	todo, err := fixture.store.CreateTodo(ctx, projectID, store.CreateTodoInput{
		Title:     title,
		ColumnKey: store.DefaultColumnBacklog,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo %q: %v", title, err)
	}
	return todo
}

func todoDeleteRESTAuditCount(t *testing.T, sqlDB *sql.DB, todoID int64) int {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'todo_deleted' AND target_type = 'todo' AND target_id = ?`, todoID).Scan(&count); err != nil {
		t.Fatalf("count todo_deleted audits: %v", err)
	}
	return count
}

func todoDeleteRESTEventsForProject(collector *collectingConsumer, projectID int64) []eventbus.Event {
	var events []eventbus.Event
	for _, event := range collector.events {
		if event.ProjectID == projectID {
			events = append(events, event)
		}
	}
	return events
}

func assertTodoDeleteRESTError(t *testing.T, body []byte, wantCode string) apiErrorEnvelope {
	t.Helper()
	var got apiErrorEnvelope
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode API error: %v body=%q", err, body)
	}
	if got.Error.Code != wantCode {
		t.Fatalf("error code=%q want %q body=%s", got.Error.Code, wantCode, body)
	}
	return got
}

func TestTodoDeleteRESTRealtimeContracts(t *testing.T) {
	t.Run("success publishes one internal and one SSE refresh", func(t *testing.T) {
		fixture := newTodoDeleteRESTFixture(t, "full")
		owner, ownerCtx, client := newTodoDeleteRESTOwner(t, fixture, "owner-success@example.com")
		project, err := fixture.store.CreateProject(ownerCtx, "REST delete success")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		todo := createTodoDeleteRESTTodo(t, fixture, ownerCtx, project.ID, "Delete me")
		stream := subscribeTodoUpdateEvents(t, client, fixture.ts.URL+"/api/board/"+project.Slug+"/events")

		resp, body := doJSON(t, client, http.MethodDelete, fmt.Sprintf("%s/api/board/%s/todos/%d", fixture.ts.URL, project.Slug, todo.LocalID), nil, nil)
		if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
			t.Fatalf("delete response status=%d body=%q want 204 with empty body", resp.StatusCode, body)
		}

		var todoRows int
		if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM todos WHERE id = ?`, todo.ID).Scan(&todoRows); err != nil {
			t.Fatalf("count todo: %v", err)
		}
		if todoRows != 0 || todoDeleteRESTAuditCount(t, fixture.db, todo.ID) != 1 {
			t.Fatalf("delete persistence todoRows=%d auditCount=%d want 0,1", todoRows, todoDeleteRESTAuditCount(t, fixture.db, todo.ID))
		}

		internalEvents := todoDeleteRESTEventsForProject(fixture.collector, project.ID)
		if len(internalEvents) != 1 || internalEvents[0].Type != "board.refresh_needed" {
			t.Fatalf("internal events=%+v want one board.refresh_needed", internalEvents)
		}
		var payload struct {
			Reason      string `json:"reason"`
			ActorUserID int64  `json:"actorUserId"`
		}
		if err := json.Unmarshal(internalEvents[0].Payload, &payload); err != nil {
			t.Fatalf("decode refresh payload: %v", err)
		}
		if payload.Reason != "todo_deleted" || payload.ActorUserID != owner.ID {
			t.Fatalf("refresh payload=%+v want reason todo_deleted actor %d", payload, owner.ID)
		}

		wireEvents := collectTodoUpdateEvents(t, stream)
		if len(wireEvents) != 1 || wireEvents[0].Type != "refresh_needed" || wireEvents[0].ProjectID != project.ID || wireEvents[0].Reason != "todo_deleted" {
			t.Fatalf("SSE events=%+v want one todo_deleted refresh for project %d", wireEvents, project.ID)
		}
	})

	t.Run("missing todo is silent", func(t *testing.T) {
		fixture := newTodoDeleteRESTFixture(t, "full")
		_, ownerCtx, client := newTodoDeleteRESTOwner(t, fixture, "owner-missing@example.com")
		project, err := fixture.store.CreateProject(ownerCtx, "REST missing todo")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		control := createTodoDeleteRESTTodo(t, fixture, ownerCtx, project.ID, "Keep me")
		stream := subscribeTodoUpdateEvents(t, client, fixture.ts.URL+"/api/board/"+project.Slug+"/events")

		resp, body := doJSON(t, client, http.MethodDelete, fmt.Sprintf("%s/api/board/%s/todos/%d", fixture.ts.URL, project.Slug, control.LocalID+1000), nil, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("missing delete status=%d body=%s want 404", resp.StatusCode, body)
		}
		assertTodoDeleteRESTError(t, body, "NOT_FOUND")
		var controlRows int
		if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM todos WHERE id = ?`, control.ID).Scan(&controlRows); err != nil {
			t.Fatalf("count control todo: %v", err)
		}
		if controlRows != 1 || todoDeleteRESTAuditCount(t, fixture.db, control.ID) != 0 {
			t.Fatalf("missing delete changed persistence todoRows=%d audits=%d", controlRows, todoDeleteRESTAuditCount(t, fixture.db, control.ID))
		}
		if events := todoDeleteRESTEventsForProject(fixture.collector, project.ID); len(events) != 0 {
			t.Fatalf("missing delete published internal events: %+v", events)
		}
		if events := collectTodoUpdateEvents(t, stream); len(events) != 0 {
			t.Fatalf("missing delete published SSE events: %+v", events)
		}
	})

	t.Run("persistence failure is silent", func(t *testing.T) {
		fixture := newTodoDeleteRESTFixture(t, "full")
		_, ownerCtx, client := newTodoDeleteRESTOwner(t, fixture, "owner-failure@example.com")
		project, err := fixture.store.CreateProject(ownerCtx, "REST persistence failure")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		todo := createTodoDeleteRESTTodo(t, fixture, ownerCtx, project.ID, "Retain me")
		if _, err := fixture.db.Exec(`
			CREATE TRIGGER phase21_rest_abort_todo_delete
			BEFORE DELETE ON todos
			BEGIN
				SELECT RAISE(ABORT, 'forced todo delete failure');
			END`); err != nil {
			t.Fatalf("create aborting trigger: %v", err)
		}
		defer func() {
			if _, err := fixture.db.Exec(`DROP TRIGGER IF EXISTS phase21_rest_abort_todo_delete`); err != nil {
				t.Errorf("drop aborting trigger: %v", err)
			}
		}()
		stream := subscribeTodoUpdateEvents(t, client, fixture.ts.URL+"/api/board/"+project.Slug+"/events")

		resp, body := doJSON(t, client, http.MethodDelete, fmt.Sprintf("%s/api/board/%s/todos/%d", fixture.ts.URL, project.Slug, todo.LocalID), nil, nil)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("failed delete status=%d body=%s want 500", resp.StatusCode, body)
		}
		assertTodoDeleteRESTError(t, body, "INTERNAL")
		var todoRows int
		if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM todos WHERE id = ?`, todo.ID).Scan(&todoRows); err != nil {
			t.Fatalf("count retained todo: %v", err)
		}
		if todoRows != 1 || todoDeleteRESTAuditCount(t, fixture.db, todo.ID) != 0 {
			t.Fatalf("failed delete persistence todoRows=%d auditCount=%d", todoRows, todoDeleteRESTAuditCount(t, fixture.db, todo.ID))
		}
		if events := todoDeleteRESTEventsForProject(fixture.collector, project.ID); len(events) != 0 {
			t.Fatalf("failed delete published internal events: %+v", events)
		}
		if events := collectTodoUpdateEvents(t, stream); len(events) != 0 {
			t.Fatalf("failed delete published SSE events: %+v", events)
		}
	})
}

func TestTodoDeleteRESTAccessRoleAndModeContracts(t *testing.T) {
	durableCases := []struct {
		name       string
		role       store.ProjectRole
		auth       bool
		missing    bool
		wantStatus int
		wantCode   string
	}{
		{name: "owner", auth: true, wantStatus: http.StatusNoContent},
		{name: "maintainer", role: store.RoleMaintainer, auth: true, wantStatus: http.StatusNoContent},
		{name: "contributor existing todo", role: store.RoleContributor, auth: true, wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN"},
		{name: "contributor missing todo", role: store.RoleContributor, auth: true, missing: true, wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND"},
		{name: "viewer", role: store.RoleViewer, auth: true, wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND"},
		{name: "non-member", auth: true, wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND"},
		{name: "no session", wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND"},
	}

	for index, tc := range durableCases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newTodoDeleteRESTFixture(t, "full")
			owner, ownerCtx, ownerClient := newTodoDeleteRESTOwner(t, fixture, fmt.Sprintf("owner-role-%d@example.com", index))
			project, err := fixture.store.CreateProject(ownerCtx, "REST durable access "+tc.name)
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			todo := createTodoDeleteRESTTodo(t, fixture, ownerCtx, project.ID, "Role target")
			client := newCookieClient(t)
			if tc.name == "owner" {
				client = ownerClient
			} else if tc.auth {
				user, err := fixture.store.CreateUser(context.Background(), fmt.Sprintf("actor-role-%d@example.com", index), "password123", "Actor")
				if err != nil {
					t.Fatalf("CreateUser: %v", err)
				}
				if tc.role != "" {
					if err := fixture.store.AddProjectMember(ownerCtx, owner.ID, project.ID, user.ID, tc.role); err != nil {
						t.Fatalf("AddProjectMember: %v", err)
					}
				}
				client = todoDeleteRESTClientForUser(t, fixture, user.ID)
			}
			localID := todo.LocalID
			if tc.missing {
				localID += 1000
			}
			resp, body := doJSON(t, client, http.MethodDelete, fmt.Sprintf("%s/api/board/%s/todos/%d", fixture.ts.URL, project.Slug, localID), nil, nil)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status=%d body=%s want %d", resp.StatusCode, body, tc.wantStatus)
			}
			if tc.wantCode != "" {
				assertTodoDeleteRESTError(t, body, tc.wantCode)
			}
			var todoRows int
			if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM todos WHERE id = ?`, todo.ID).Scan(&todoRows); err != nil {
				t.Fatalf("count todo: %v", err)
			}
			if tc.wantStatus == http.StatusNoContent {
				if todoRows != 0 || len(body) != 0 {
					t.Fatalf("successful delete todoRows=%d body=%q want 0 and empty", todoRows, body)
				}
			} else if todoRows != 1 || todoDeleteRESTAuditCount(t, fixture.db, todo.ID) != 0 {
				t.Fatalf("failed delete todoRows=%d audits=%d want 1,0", todoRows, todoDeleteRESTAuditCount(t, fixture.db, todo.ID))
			}
		})
	}

	t.Run("anonymous link-holder on unexpired temporary board", func(t *testing.T) {
		fixture := newTodoDeleteRESTFixture(t, "full")
		_, ownerCtx, _ := newTodoDeleteRESTOwner(t, fixture, "owner-temp@example.com")
		project, err := fixture.store.CreateAnonymousBoard(ownerCtx)
		if err != nil {
			t.Fatalf("CreateAnonymousBoard: %v", err)
		}
		todo := createTodoDeleteRESTTodo(t, fixture, ownerCtx, project.ID, "Temporary target")
		oldActivity := time.Now().UTC().Add(-time.Hour).UnixMilli()
		oldExpiry := time.Now().UTC().Add(24 * time.Hour).UnixMilli()
		if _, err := fixture.db.Exec(`UPDATE projects SET last_activity_at = ?, expires_at = ? WHERE id = ?`, oldActivity, oldExpiry, project.ID); err != nil {
			t.Fatalf("seed temporary timestamps: %v", err)
		}

		resp, body := doJSON(t, newCookieClient(t), http.MethodDelete, fmt.Sprintf("%s/api/board/%s/todos/%d", fixture.ts.URL, project.Slug, todo.LocalID), nil, nil)
		if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
			t.Fatalf("temporary delete status=%d body=%q want empty 204", resp.StatusCode, body)
		}
		var actor sql.NullInt64
		if err := fixture.db.QueryRow(`SELECT actor_user_id FROM audit_events WHERE action = 'todo_deleted' AND target_id = ?`, todo.ID).Scan(&actor); err != nil {
			t.Fatalf("read temporary delete actor: %v", err)
		}
		if actor.Valid {
			t.Fatalf("temporary anonymous delete actor=%d want NULL", actor.Int64)
		}
		var lastActivity, expiresAt int64
		if err := fixture.db.QueryRow(`SELECT last_activity_at, expires_at FROM projects WHERE id = ?`, project.ID).Scan(&lastActivity, &expiresAt); err != nil {
			t.Fatalf("read temporary activity: %v", err)
		}
		if lastActivity <= oldActivity || expiresAt <= oldExpiry {
			t.Fatalf("temporary activity not extended last=%d expiry=%d old=(%d,%d)", lastActivity, expiresAt, oldActivity, oldExpiry)
		}
		events := todoDeleteRESTEventsForProject(fixture.collector, project.ID)
		if len(events) != 1 {
			t.Fatalf("temporary delete events=%+v want one", events)
		}
		var payload map[string]any
		if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
			t.Fatalf("decode temporary refresh payload: %v", err)
		}
		if payload["reason"] != "todo_deleted" {
			t.Fatalf("temporary refresh payload=%+v", payload)
		}
		if _, present := payload["actorUserId"]; present {
			t.Fatalf("anonymous temporary refresh unexpectedly included actor: %+v", payload)
		}
	})

	t.Run("expired temporary board", func(t *testing.T) {
		fixture := newTodoDeleteRESTFixture(t, "full")
		_, ownerCtx, _ := newTodoDeleteRESTOwner(t, fixture, "owner-expired@example.com")
		project, err := fixture.store.CreateAnonymousBoard(ownerCtx)
		if err != nil {
			t.Fatalf("CreateAnonymousBoard: %v", err)
		}
		todo := createTodoDeleteRESTTodo(t, fixture, ownerCtx, project.ID, "Expired target")
		if _, err := fixture.db.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Minute).UnixMilli(), project.ID); err != nil {
			t.Fatalf("expire temporary board: %v", err)
		}
		resp, body := doJSON(t, newCookieClient(t), http.MethodDelete, fmt.Sprintf("%s/api/board/%s/todos/%d", fixture.ts.URL, project.Slug, todo.LocalID), nil, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expired delete status=%d body=%s want 404", resp.StatusCode, body)
		}
		assertTodoDeleteRESTError(t, body, "NOT_FOUND")
		if todoDeleteRESTAuditCount(t, fixture.db, todo.ID) != 0 || len(todoDeleteRESTEventsForProject(fixture.collector, project.ID)) != 0 {
			t.Fatal("expired temporary delete mutated or published")
		}
	})

	t.Run("missing project", func(t *testing.T) {
		fixture := newTodoDeleteRESTFixture(t, "full")
		owner, _, client := newTodoDeleteRESTOwner(t, fixture, "owner-project-missing@example.com")
		_ = owner
		resp, body := doJSON(t, client, http.MethodDelete, fixture.ts.URL+"/api/board/missing-project/todos/1", nil, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("missing project status=%d body=%s want 404", resp.StatusCode, body)
		}
		assertTodoDeleteRESTError(t, body, "NOT_FOUND")
		if len(fixture.collector.events) != 0 {
			t.Fatalf("missing project published events: %+v", fixture.collector.events)
		}
	})
}

func TestTodoDeleteRESTAccessPrecedesLocalIDValidation(t *testing.T) {
	fixture := newTodoDeleteRESTFixture(t, "full")
	owner, ownerCtx, ownerClient := newTodoDeleteRESTOwner(t, fixture, "owner-precedence@example.com")
	project, err := fixture.store.CreateProject(ownerCtx, "REST delete precedence")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	control := createTodoDeleteRESTTodo(t, fixture, ownerCtx, project.ID, "Control")
	outsider, err := fixture.store.CreateUser(context.Background(), "outsider-precedence@example.com", "password123", "Outsider")
	if err != nil {
		t.Fatalf("CreateUser outsider: %v", err)
	}
	outsiderClient := todoDeleteRESTClientForUser(t, fixture, outsider.ID)

	cases := []struct {
		name       string
		client     *http.Client
		slug       string
		localID    string
		wantStatus int
		wantCode   string
		wantReason string
		wantField  string
	}{
		{name: "accessible malformed local ID", client: ownerClient, slug: project.Slug, localID: "not-a-number", wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantReason: "invalid_todo_local_id", wantField: "localId"},
		{name: "accessible non-positive local ID", client: ownerClient, slug: project.Slug, localID: "0", wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantReason: "invalid_todo_local_id", wantField: "localId"},
		{name: "missing project hides malformed local ID", client: ownerClient, slug: "missing-project", localID: "not-a-number", wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND"},
		{name: "inaccessible project hides malformed local ID", client: outsiderClient, slug: project.Slug, localID: "not-a-number", wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := doJSON(t, tc.client, http.MethodDelete, fixture.ts.URL+"/api/board/"+tc.slug+"/todos/"+tc.localID, nil, nil)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status=%d body=%s want %d", resp.StatusCode, body, tc.wantStatus)
			}
			got := assertTodoDeleteRESTError(t, body, tc.wantCode)
			if tc.wantReason != "" {
				if got.Error.Message != "invalid todo localId" || got.Error.Details["reason"] != tc.wantReason || got.Error.Details["field"] != tc.wantField {
					t.Fatalf("validation error=%+v", got.Error)
				}
			}
		})
	}
	var controlRows int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM todos WHERE id = ?`, control.ID).Scan(&controlRows); err != nil {
		t.Fatalf("count control todo: %v", err)
	}
	if controlRows != 1 || todoDeleteRESTAuditCount(t, fixture.db, control.ID) != 0 || len(fixture.collector.events) != 0 {
		t.Fatalf("precedence probes mutated/published todoRows=%d audits=%d events=%+v", controlRows, todoDeleteRESTAuditCount(t, fixture.db, control.ID), fixture.collector.events)
	}
	_ = owner
}

func TestLegacyTodoDeleteMasksContributorUnauthorizedAsNotFound(t *testing.T) {
	fixture := newTodoDeleteRESTFixture(t, "full")
	owner, ownerCtx, ownerClient := newTodoDeleteRESTOwner(t, fixture, "owner-legacy@example.com")
	project, err := fixture.store.CreateProject(ownerCtx, "Legacy numeric delete")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	todo := createTodoDeleteRESTTodo(t, fixture, ownerCtx, project.ID, "Legacy target")
	contributor, err := fixture.store.CreateUser(context.Background(), "contributor-legacy@example.com", "password123", "Contributor")
	if err != nil {
		t.Fatalf("CreateUser contributor: %v", err)
	}
	if err := fixture.store.AddProjectMember(ownerCtx, owner.ID, project.ID, contributor.ID, store.RoleContributor); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}
	contributorClient := todoDeleteRESTClientForUser(t, fixture, contributor.ID)
	stream := subscribeTodoUpdateEvents(t, ownerClient, fixture.ts.URL+"/api/board/"+project.Slug+"/events")

	resp, body := doJSON(t, contributorClient, http.MethodDelete, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todo.ID), nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("legacy contributor delete status=%d body=%s want 404", resp.StatusCode, body)
	}
	assertTodoDeleteRESTError(t, body, "NOT_FOUND")
	var todoRows int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM todos WHERE id = ?`, todo.ID).Scan(&todoRows); err != nil {
		t.Fatalf("count retained todo: %v", err)
	}
	if todoRows != 1 || todoDeleteRESTAuditCount(t, fixture.db, todo.ID) != 0 {
		t.Fatalf("legacy failed delete todoRows=%d audits=%d want 1,0", todoRows, todoDeleteRESTAuditCount(t, fixture.db, todo.ID))
	}
	if events := todoDeleteRESTEventsForProject(fixture.collector, project.ID); len(events) != 0 {
		t.Fatalf("legacy failed delete published internal events: %+v", events)
	}
	if events := collectTodoUpdateEvents(t, stream); len(events) != 0 {
		t.Fatalf("legacy failed delete published SSE events: %+v", events)
	}
}
