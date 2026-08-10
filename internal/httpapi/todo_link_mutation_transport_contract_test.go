package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"scrumboy/internal/store"
)

type todoLinkRESTFixture struct {
	ts      *httptest.Server
	db      *sql.DB
	st      *store.Store
	client  *http.Client
	ownerID int64
	ctx     context.Context
	project store.Project
	from    store.Todo
	to      store.Todo
}

func newTodoLinkRESTFixture(t *testing.T, name string) *todoLinkRESTFixture {
	t.Helper()
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	t.Cleanup(cleanup)
	client := newCookieClient(t)
	owner := bootstrapUserClient(t, client, ts.URL, "Todo Link Owner", name+"@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	ctx := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, nil)
	project, err := st.CreateProject(ctx, name)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	from := createTodoLinkRESTTodo(t, st, ctx, project.ID, "Link source", store.ModeFull)
	to := createTodoLinkRESTTodo(t, st, ctx, project.ID, "Link target", store.ModeFull)
	return &todoLinkRESTFixture{ts: ts, db: sqlDB, st: st, client: client, ownerID: ownerID, ctx: ctx, project: project, from: from, to: to}
}

func createTodoLinkRESTTodo(t *testing.T, st *store.Store, ctx context.Context, projectID int64, title string, mode store.Mode) store.Todo {
	t.Helper()
	todo, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{Title: title, ColumnKey: store.DefaultColumnBacklog}, mode)
	if err != nil {
		t.Fatalf("create todo %q: %v", title, err)
	}
	return todo
}

func todoLinkRowCount(t *testing.T, db *sql.DB, projectID, fromLocalID, toLocalID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM todo_links
		WHERE project_id = ? AND from_local_id = ? AND to_local_id = ?
	`, projectID, fromLocalID, toLocalID).Scan(&count); err != nil {
		t.Fatalf("count todo link rows: %v", err)
	}
	return count
}

func todoLinkStoredType(t *testing.T, db *sql.DB, projectID, fromLocalID, toLocalID int64) string {
	t.Helper()
	var linkType string
	if err := db.QueryRow(`
		SELECT link_type FROM todo_links
		WHERE project_id = ? AND from_local_id = ? AND to_local_id = ?
	`, projectID, fromLocalID, toLocalID).Scan(&linkType); err != nil {
		t.Fatalf("read todo link type: %v", err)
	}
	return linkType
}

func todoLinkAuditCount(t *testing.T, db *sql.DB, projectID int64, action string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM audit_events
		WHERE project_id = ? AND action = ? AND target_type = 'todo_link'
	`, projectID, action).Scan(&count); err != nil {
		t.Fatalf("count %s audit rows: %v", action, err)
	}
	return count
}

func assertTodoLinkAudit(t *testing.T, db *sql.DB, projectID, actorID int64, action string, from, to store.Todo, linkType string) {
	t.Helper()
	var (
		gotActor sql.NullInt64
		rawMeta  string
	)
	if err := db.QueryRow(`
		SELECT actor_user_id, metadata FROM audit_events
		WHERE project_id = ? AND action = ? AND target_type = 'todo_link'
		ORDER BY id DESC LIMIT 1
	`, projectID, action).Scan(&gotActor, &rawMeta); err != nil {
		t.Fatalf("read %s audit: %v", action, err)
	}
	if !gotActor.Valid || gotActor.Int64 != actorID {
		t.Fatalf("%s audit actor=%+v want=%d", action, gotActor, actorID)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(rawMeta), &meta); err != nil {
		t.Fatalf("decode %s audit metadata: %v", action, err)
	}
	want := map[string]any{
		"from_todo_id":  float64(from.ID),
		"to_todo_id":    float64(to.ID),
		"from_local_id": float64(from.LocalID),
		"to_local_id":   float64(to.LocalID),
		"link_type":     linkType,
	}
	for key, value := range want {
		if meta[key] != value {
			t.Fatalf("%s audit metadata[%q]=%v want=%v; metadata=%+v", action, key, meta[key], value, meta)
		}
	}
}

func assertTodoLinkRESTRefresh(t *testing.T, events []todoUpdateWireEvent, projectID int64) {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("todo-link event count=%d want=1; events=%+v", len(events), events)
	}
	event := events[0]
	if event.Type != "refresh_needed" || event.ProjectID != projectID || event.Reason != "todo_links_updated" {
		t.Fatalf("todo-link refresh mismatch: event=%+v want project=%d reason=todo_links_updated", event, projectID)
	}
}

func assertTodoLinkRESTError(t *testing.T, resp *http.Response, body []byte, envelope apiErrorEnvelope, wantStatus int, wantCode, wantMessage, wantReason, wantField string) {
	t.Helper()
	if resp.StatusCode != wantStatus || envelope.Error.Code != wantCode || envelope.Error.Message != wantMessage {
		t.Fatalf("status=%d error=%+v want status=%d code=%q message=%q body=%s", resp.StatusCode, envelope.Error, wantStatus, wantCode, wantMessage, body)
	}
	if wantReason != "" {
		if envelope.Error.Details["reason"] != wantReason {
			t.Fatalf("error reason=%v want=%q details=%+v", envelope.Error.Details["reason"], wantReason, envelope.Error.Details)
		}
	}
	if wantField != "" {
		if envelope.Error.Details["field"] != wantField {
			t.Fatalf("error field=%v want=%q details=%+v", envelope.Error.Details["field"], wantField, envelope.Error.Details)
		}
	}
}

func TestTodoLinkMutationRESTPersistenceAndRefreshContracts(t *testing.T) {
	t.Run("add", func(t *testing.T) {
		fx := newTodoLinkRESTFixture(t, "todo-link-rest-add")
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

		resp, body := doJSON(t, fx.client, http.MethodPost, fx.ts.URL+"/api/board/"+fx.project.Slug+"/todos/"+itoa(fx.from.LocalID)+"/links", map[string]any{
			"targetLocalId": fx.to.LocalID,
			"linkType":      "blocks",
		}, nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("add status=%d body=%s", resp.StatusCode, body)
		}
		if got := todoLinkRowCount(t, fx.db, fx.project.ID, fx.from.LocalID, fx.to.LocalID); got != 1 {
			t.Fatalf("add link row count=%d want=1", got)
		}
		if got := todoLinkStoredType(t, fx.db, fx.project.ID, fx.from.LocalID, fx.to.LocalID); got != "blocks" {
			t.Fatalf("stored link type=%q want=blocks", got)
		}
		if got := todoLinkAuditCount(t, fx.db, fx.project.ID, "link_added"); got != 1 {
			t.Fatalf("link_added audit count=%d want=1", got)
		}
		assertTodoLinkAudit(t, fx.db, fx.project.ID, fx.ownerID, "link_added", fx.from, fx.to, "blocks")
		assertTodoLinkRESTRefresh(t, collectTodoUpdateEvents(t, stream), fx.project.ID)
	})

	t.Run("remove preserves reverse edge", func(t *testing.T) {
		fx := newTodoLinkRESTFixture(t, "todo-link-rest-remove")
		if err := fx.st.AddLink(fx.ctx, fx.project.ID, fx.from.LocalID, fx.to.LocalID, "parent", store.ModeFull); err != nil {
			t.Fatalf("add forward fixture: %v", err)
		}
		if err := fx.st.AddLink(fx.ctx, fx.project.ID, fx.to.LocalID, fx.from.LocalID, "relates_to", store.ModeFull); err != nil {
			t.Fatalf("add reverse fixture: %v", err)
		}
		before := todoLinkAuditCount(t, fx.db, fx.project.ID, "link_removed")
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

		resp, body := doJSON(t, fx.client, http.MethodDelete, fx.ts.URL+"/api/board/"+fx.project.Slug+"/todos/"+itoa(fx.from.LocalID)+"/links/"+itoa(fx.to.LocalID), nil, nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("remove status=%d body=%s", resp.StatusCode, body)
		}
		if got := todoLinkRowCount(t, fx.db, fx.project.ID, fx.from.LocalID, fx.to.LocalID); got != 0 {
			t.Fatalf("removed forward link row count=%d want=0", got)
		}
		if got := todoLinkRowCount(t, fx.db, fx.project.ID, fx.to.LocalID, fx.from.LocalID); got != 1 {
			t.Fatalf("reverse link row count=%d want=1", got)
		}
		if got := todoLinkAuditCount(t, fx.db, fx.project.ID, "link_removed"); got != before+1 {
			t.Fatalf("link_removed audit count=%d want=%d", got, before+1)
		}
		assertTodoLinkAudit(t, fx.db, fx.project.ID, fx.ownerID, "link_removed", fx.from, fx.to, "parent")
		assertTodoLinkRESTRefresh(t, collectTodoUpdateEvents(t, stream), fx.project.ID)
	})
}

func TestTodoLinkMutationRESTDuplicateAddContract(t *testing.T) {
	fx := newTodoLinkRESTFixture(t, "todo-link-rest-duplicate")
	resp, body := doJSON(t, fx.client, http.MethodPost, fx.ts.URL+"/api/board/"+fx.project.Slug+"/todos/"+itoa(fx.from.LocalID)+"/links", map[string]any{
		"targetLocalId": fx.to.LocalID,
		"linkType":      "blocks",
	}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("first add status=%d body=%s", resp.StatusCode, body)
	}
	beforeAudits := todoLinkAuditCount(t, fx.db, fx.project.ID, "link_added")
	stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

	resp, body = doJSON(t, fx.client, http.MethodPost, fx.ts.URL+"/api/board/"+fx.project.Slug+"/todos/"+itoa(fx.from.LocalID)+"/links", map[string]any{
		"targetLocalId": fx.to.LocalID,
		"linkType":      "parent",
	}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("duplicate add status=%d body=%s", resp.StatusCode, body)
	}
	if got := todoLinkRowCount(t, fx.db, fx.project.ID, fx.from.LocalID, fx.to.LocalID); got != 1 {
		t.Fatalf("duplicate row count=%d want=1", got)
	}
	if got := todoLinkStoredType(t, fx.db, fx.project.ID, fx.from.LocalID, fx.to.LocalID); got != "blocks" {
		t.Fatalf("duplicate changed link type=%q want=blocks", got)
	}
	if got := todoLinkAuditCount(t, fx.db, fx.project.ID, "link_added"); got != beforeAudits {
		t.Fatalf("duplicate audit count=%d want=%d", got, beforeAudits)
	}
	assertTodoLinkRESTRefresh(t, collectTodoUpdateEvents(t, stream), fx.project.ID)
}

func TestTodoLinkMutationRESTFailureSilence(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		path        func(*todoLinkRESTFixture) string
		body        any
		wantStatus  int
		wantCode    string
		wantMessage string
		wantReason  string
	}{
		{
			name: "store validation", method: http.MethodPost,
			path:       func(fx *todoLinkRESTFixture) string { return "/todos/" + itoa(fx.from.LocalID) + "/links" },
			body:       map[string]any{"targetLocalId": 2, "linkType": "invalid"},
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "invalid link", wantReason: "invalid_link",
		},
		{
			name: "missing directed edge", method: http.MethodDelete,
			path: func(fx *todoLinkRESTFixture) string {
				return "/todos/" + itoa(fx.from.LocalID) + "/links/" + itoa(fx.to.LocalID)
			},
			wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMessage: "not found",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fx := newTodoLinkRESTFixture(t, "todo-link-rest-failure-"+tc.name)
			stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")
			var envelope apiErrorEnvelope
			resp, body := doJSON(t, fx.client, tc.method, fx.ts.URL+"/api/board/"+fx.project.Slug+tc.path(fx), tc.body, &envelope)
			assertTodoLinkRESTError(t, resp, body, envelope, tc.wantStatus, tc.wantCode, tc.wantMessage, tc.wantReason, "")
			if got := todoLinkRowCount(t, fx.db, fx.project.ID, fx.from.LocalID, fx.to.LocalID); got != 0 {
				t.Fatalf("failed mutation link rows=%d want=0", got)
			}
			if got := todoLinkAuditCount(t, fx.db, fx.project.ID, "link_added") + todoLinkAuditCount(t, fx.db, fx.project.ID, "link_removed"); got != 0 {
				t.Fatalf("failed mutation link audit rows=%d want=0", got)
			}
			if events := collectTodoUpdateEvents(t, stream); len(events) != 0 {
				t.Fatalf("failed mutation emitted events: %+v", events)
			}
		})
	}
}

func TestTodoLinkMutationRESTPrecedence(t *testing.T) {
	fx := newTodoLinkRESTFixture(t, "todo-link-rest-precedence")
	stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")
	tests := []struct {
		name        string
		method      string
		slug        string
		path        string
		body        any
		wantStatus  int
		wantCode    string
		wantMessage string
		wantReason  string
		wantField   string
	}{
		{name: "missing slug wins before invalid source and body", method: http.MethodPost, slug: "missing-link-board", path: "/todos/not-a-number/links", body: "not-an-object", wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMessage: "not found"},
		{name: "missing source wins before malformed body", method: http.MethodPost, slug: fx.project.Slug, path: "/todos/999/links", body: "not-an-object", wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMessage: "not found"},
		{name: "valid source reaches body decoding", method: http.MethodPost, slug: fx.project.Slug, path: "/todos/" + itoa(fx.from.LocalID) + "/links", body: "not-an-object", wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "invalid json", wantReason: "invalid_json"},
		{name: "missing source wins before self target", method: http.MethodPost, slug: fx.project.Slug, path: "/todos/999/links", body: map[string]any{"targetLocalId": 999}, wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMessage: "not found"},
		{name: "delete missing source wins before target parsing", method: http.MethodDelete, slug: fx.project.Slug, path: "/todos/999/links/not-a-number", wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMessage: "not found"},
		{name: "delete valid source reaches target parsing", method: http.MethodDelete, slug: fx.project.Slug, path: "/todos/" + itoa(fx.from.LocalID) + "/links/not-a-number", wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "invalid targetLocalId", wantReason: "invalid_target_local_id", wantField: "targetLocalId"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var envelope apiErrorEnvelope
			resp, body := doJSON(t, fx.client, tc.method, fx.ts.URL+"/api/board/"+tc.slug+tc.path, tc.body, &envelope)
			assertTodoLinkRESTError(t, resp, body, envelope, tc.wantStatus, tc.wantCode, tc.wantMessage, tc.wantReason, tc.wantField)
		})
	}
	if got := todoLinkAuditCount(t, fx.db, fx.project.ID, "link_added") + todoLinkAuditCount(t, fx.db, fx.project.ID, "link_removed"); got != 0 {
		t.Fatalf("precedence failures created link audits=%d", got)
	}
	if events := collectTodoUpdateEvents(t, stream); len(events) != 0 {
		t.Fatalf("precedence failures emitted events: %+v", events)
	}
}

func TestTodoLinkMutationRESTUnsupportedMethodContract(t *testing.T) {
	fx := newTodoLinkRESTFixture(t, "todo-link-rest-unsupported-method")
	stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")
	beforeAdded := todoLinkAuditCount(t, fx.db, fx.project.ID, "link_added")
	beforeRemoved := todoLinkAuditCount(t, fx.db, fx.project.ID, "link_removed")

	var envelope apiErrorEnvelope
	resp, body := doJSON(
		t,
		fx.client,
		http.MethodPut,
		fx.ts.URL+"/api/board/"+fx.project.Slug+"/todos/"+itoa(fx.from.LocalID)+"/links",
		nil,
		&envelope,
	)
	assertTodoLinkRESTError(t, resp, body, envelope, http.StatusNotFound, "NOT_FOUND", "not found", "", "")
	if len(envelope.Error.Details) != 0 {
		t.Fatalf("unsupported-method details=%+v want empty", envelope.Error.Details)
	}
	if got := todoLinkRowCount(t, fx.db, fx.project.ID, fx.from.LocalID, fx.to.LocalID); got != 0 {
		t.Fatalf("unsupported method link rows=%d want=0", got)
	}
	if got := todoLinkAuditCount(t, fx.db, fx.project.ID, "link_added"); got != beforeAdded {
		t.Fatalf("link_added audit count=%d want=%d", got, beforeAdded)
	}
	if got := todoLinkAuditCount(t, fx.db, fx.project.ID, "link_removed"); got != beforeRemoved {
		t.Fatalf("link_removed audit count=%d want=%d", got, beforeRemoved)
	}
	if events := collectTodoUpdateEvents(t, stream); len(events) != 0 {
		t.Fatalf("unsupported method emitted events: %+v", events)
	}
}

func TestTodoLinkMutationRESTBoardModeContracts(t *testing.T) {
	t.Run("Durable contributor can mutate", func(t *testing.T) {
		fx := newTodoLinkRESTFixture(t, "todo-link-rest-contributor")
		contributor, err := fx.st.CreateUser(context.Background(), "todo-link-rest-contributor-user@example.com", "password123", "Contributor")
		if err != nil {
			t.Fatalf("create contributor: %v", err)
		}
		if err := fx.st.AddProjectMember(fx.ctx, fx.ownerID, fx.project.ID, contributor.ID, store.RoleContributor); err != nil {
			t.Fatalf("add contributor: %v", err)
		}
		client := newCookieClient(t)
		loginUserClient(t, client, fx.ts.URL, contributor.Email, "password123")
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")
		resp, body := doJSON(t, client, http.MethodPost, fx.ts.URL+"/api/board/"+fx.project.Slug+"/todos/"+itoa(fx.from.LocalID)+"/links", map[string]any{"targetLocalId": fx.to.LocalID}, nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("contributor add status=%d body=%s", resp.StatusCode, body)
		}
		assertTodoLinkRESTRefresh(t, collectTodoUpdateEvents(t, stream), fx.project.ID)
	})

	t.Run("Temporary Board authenticated link holder can mutate", func(t *testing.T) {
		ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
		t.Cleanup(cleanup)
		creatorClient := newCookieClient(t)
		creator := bootstrapUserClient(t, creatorClient, ts.URL, "Temporary Creator", "todo-link-temp-creator@example.com", "password123")
		creatorID := int64(creator["id"].(float64))
		st := store.New(sqlDB, nil)
		board, err := st.CreateAnonymousBoard(store.WithUserID(context.Background(), creatorID))
		if err != nil {
			t.Fatalf("create Temporary Board: %v", err)
		}
		ctx := store.WithUserID(context.Background(), creatorID)
		from := createTodoLinkRESTTodo(t, st, ctx, board.ID, "Temporary source", store.ModeFull)
		to := createTodoLinkRESTTodo(t, st, ctx, board.ID, "Temporary target", store.ModeFull)
		linkHolder, err := st.CreateUser(context.Background(), "todo-link-temp-holder@example.com", "password123", "Link Holder")
		if err != nil {
			t.Fatalf("create link holder: %v", err)
		}
		holderClient := newCookieClient(t)
		loginUserClient(t, holderClient, ts.URL, linkHolder.Email, "password123")
		stream := subscribeTodoUpdateEvents(t, holderClient, ts.URL+"/api/board/"+board.Slug+"/events")
		resp, body := doJSON(t, holderClient, http.MethodPost, ts.URL+"/api/board/"+board.Slug+"/todos/"+itoa(from.LocalID)+"/links", map[string]any{"targetLocalId": to.LocalID}, nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("Temporary Board add status=%d body=%s", resp.StatusCode, body)
		}
		if got := todoLinkRowCount(t, sqlDB, board.ID, from.LocalID, to.LocalID); got != 1 {
			t.Fatalf("Temporary Board link rows=%d want=1", got)
		}
		assertTodoLinkRESTRefresh(t, collectTodoUpdateEvents(t, stream), board.ID)
	})

	t.Run("Anonymous Board permits anonymous REST mutation", func(t *testing.T) {
		ts, sqlDB, cleanup := newTestHTTPServer(t, "anonymous")
		t.Cleanup(cleanup)
		st := store.New(sqlDB, nil)
		board, err := st.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatalf("create Anonymous Board: %v", err)
		}
		from := createTodoLinkRESTTodo(t, st, context.Background(), board.ID, "Anonymous source", store.ModeAnonymous)
		to := createTodoLinkRESTTodo(t, st, context.Background(), board.ID, "Anonymous target", store.ModeAnonymous)
		client := ts.Client()
		stream := subscribeTodoUpdateEvents(t, client, ts.URL+"/api/board/"+board.Slug+"/events")
		resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+board.Slug+"/todos/"+itoa(from.LocalID)+"/links", map[string]any{"targetLocalId": to.LocalID}, nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("Anonymous Board add status=%d body=%s", resp.StatusCode, body)
		}
		if got := todoLinkRowCount(t, sqlDB, board.ID, from.LocalID, to.LocalID); got != 1 {
			t.Fatalf("Anonymous Board link rows=%d want=1", got)
		}
		assertTodoLinkRESTRefresh(t, collectTodoUpdateEvents(t, stream), board.ID)
	})
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}
