package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
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

type legacyTodoMutationFixture struct {
	ts        *httptest.Server
	db        *sql.DB
	store     *store.Store
	server    *Server
	collector *collectingConsumer
}

func newLegacyTodoMutationFixture(t *testing.T, mode string) *legacyTodoMutationFixture {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "legacy-todo.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("apply migrations: %v", err)
	}

	st := store.New(sqlDB, nil)
	collector := &collectingConsumer{}
	srv := NewServer(st, Options{MaxRequestBody: 1 << 20, ScrumboyMode: mode})
	srv.fanout = eventbus.NewFanout(newSSEBridge(srv.hub), collector)
	st.SetTodoAssignedPublisher(srv.PublishTodoAssigned)
	ts := httptest.NewServer(srv)

	fixture := &legacyTodoMutationFixture{
		ts:        ts,
		db:        sqlDB,
		store:     st,
		server:    srv,
		collector: collector,
	}
	t.Cleanup(func() {
		ts.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		srv.Close(ctx)
		_ = sqlDB.Close()
	})
	return fixture
}

func legacyTodoClientForUser(t *testing.T, fixture *legacyTodoMutationFixture, userID int64) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	client := &http.Client{Transport: fixture.ts.Client().Transport, Jar: jar}
	token, expiresAt, err := fixture.store.CreateSession(context.Background(), userID, 24*time.Hour)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	baseURL, err := url.Parse(fixture.ts.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	jar.SetCookies(baseURL, []*http.Cookie{{
		Name:    "scrumboy_session",
		Value:   token,
		Path:    "/",
		Expires: expiresAt,
	}})
	return client
}

func legacyTodoAnonymousClient(t *testing.T, fixture *legacyTodoMutationFixture) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create anonymous cookie jar: %v", err)
	}
	return &http.Client{Transport: fixture.ts.Client().Transport, Jar: jar}
}

func legacyTodoBootstrapOwner(t *testing.T, fixture *legacyTodoMutationFixture) (store.User, context.Context, *http.Client) {
	t.Helper()
	owner, err := fixture.store.BootstrapUser(context.Background(), "legacy-owner@example.com", "password123", "Legacy Owner")
	if err != nil {
		t.Fatalf("bootstrap owner: %v", err)
	}
	ctx := store.WithUserID(context.Background(), owner.ID)
	return owner, ctx, legacyTodoClientForUser(t, fixture, owner.ID)
}

func legacyTodoCreateUser(t *testing.T, fixture *legacyTodoMutationFixture, email string) store.User {
	t.Helper()
	user, err := fixture.store.CreateUser(context.Background(), email, "password123", email)
	if err != nil {
		t.Fatalf("create user %q: %v", email, err)
	}
	return user
}

func legacyTodoAddMember(t *testing.T, fixture *legacyTodoMutationFixture, owner store.User, ownerCtx context.Context, projectID, userID int64, role store.ProjectRole) {
	t.Helper()
	if err := fixture.store.AddProjectMember(ownerCtx, owner.ID, projectID, userID, role); err != nil {
		t.Fatalf("add project member role=%s: %v", role, err)
	}
}

func legacyTodoCreateProject(t *testing.T, fixture *legacyTodoMutationFixture, ctx context.Context, name string) store.Project {
	t.Helper()
	project, err := fixture.store.CreateProject(ctx, name)
	if err != nil {
		t.Fatalf("create project %q: %v", name, err)
	}
	return project
}

func legacyTodoCreateTemporaryProject(t *testing.T, fixture *legacyTodoMutationFixture, ctx context.Context) store.Project {
	t.Helper()
	project, err := fixture.store.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("create temporary project: %v", err)
	}
	return project
}

func legacyTodoCreateTodo(t *testing.T, fixture *legacyTodoMutationFixture, ctx context.Context, projectID int64, mode store.Mode, input store.CreateTodoInput) store.Todo {
	t.Helper()
	if input.Title == "" {
		input.Title = "Legacy target"
	}
	if input.ColumnKey == "" {
		input.ColumnKey = store.DefaultColumnBacklog
	}
	todo, err := fixture.store.CreateTodo(ctx, projectID, input, mode)
	if err != nil {
		t.Fatalf("create todo %q: %v", input.Title, err)
	}
	return todo
}

func legacyTodoSeedGlobalIdentity(t *testing.T, fixture *legacyTodoMutationFixture, ctx context.Context, mode store.Mode) {
	t.Helper()
	seedProject := legacyTodoCreateTemporaryProject(t, fixture, ctx)
	legacyTodoCreateTodo(t, fixture, ctx, seedProject.ID, mode, store.CreateTodoInput{Title: "Global identity seed"})
}

func legacyTodoResetEvents(fixture *legacyTodoMutationFixture) {
	fixture.collector.events = nil
}

func legacyTodoEventsForProject(fixture *legacyTodoMutationFixture, projectID int64) []eventbus.Event {
	var events []eventbus.Event
	for _, event := range fixture.collector.events {
		if event.ProjectID == projectID {
			events = append(events, event)
		}
	}
	return events
}

type legacyTodoRefreshPayload struct {
	Reason      string `json:"reason"`
	ActorUserID int64  `json:"actorUserId"`
}

func legacyTodoAssertOneRefresh(t *testing.T, fixture *legacyTodoMutationFixture, projectID int64, reason string, actorID int64) {
	t.Helper()
	events := legacyTodoEventsForProject(fixture, projectID)
	if len(events) != 1 || events[0].Type != "board.refresh_needed" {
		t.Fatalf("events=%+v, want exactly one board.refresh_needed", events)
	}
	var payload legacyTodoRefreshPayload
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("decode refresh payload: %v", err)
	}
	if payload.Reason != reason || payload.ActorUserID != actorID {
		t.Fatalf("refresh payload=%+v, want reason=%q actor=%d", payload, reason, actorID)
	}
}

func legacyTodoAssertNoEvents(t *testing.T, fixture *legacyTodoMutationFixture, projectID int64) {
	t.Helper()
	if events := legacyTodoEventsForProject(fixture, projectID); len(events) != 0 {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func legacyTodoCountEvents(t *testing.T, fixture *legacyTodoMutationFixture, projectID int64, eventType string) int {
	t.Helper()
	count := 0
	for _, event := range legacyTodoEventsForProject(fixture, projectID) {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

func legacyTodoDoRawJSON(t *testing.T, client *http.Client, method, requestURL, raw string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, requestURL, bytes.NewBufferString(raw))
	if err != nil {
		t.Fatalf("create raw request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Scrumboy", "1")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("perform raw request: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read raw response: %v", err)
	}
	return resp, body
}

func legacyTodoDecodeError(t *testing.T, body []byte) apiErrorEnvelope {
	t.Helper()
	var got apiErrorEnvelope
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode API error: %v, body=%q", err, body)
	}
	return got
}

func legacyTodoAssertError(t *testing.T, response *http.Response, body []byte, status int, code, message, reason, field string) {
	t.Helper()
	if response.StatusCode != status {
		t.Fatalf("status=%d body=%s, want %d", response.StatusCode, body, status)
	}
	got := legacyTodoDecodeError(t, body)
	if got.Error.Code != code || (message != "" && got.Error.Message != message) {
		t.Fatalf("error=%+v, want code=%q message=%q", got.Error, code, message)
	}
	if reason != "" {
		gotReason, _ := got.Error.Details["reason"].(string)
		if gotReason != reason {
			t.Fatalf("error reason=%q details=%+v, want %q", gotReason, got.Error.Details, reason)
		}
	}
	if field != "" {
		gotField, _ := got.Error.Details["field"].(string)
		if gotField != field {
			t.Fatalf("error field=%q details=%+v, want %q", gotField, got.Error.Details, field)
		}
	}
}

func legacyTodoFullPatch(todo store.Todo) map[string]any {
	return map[string]any{
		"title":            todo.Title,
		"body":             todo.Body,
		"tags":             todo.Tags,
		"estimationPoints": todo.EstimationPoints,
		"assigneeUserId":   todo.AssigneeUserID,
	}
}

type legacyTodoAuditRow struct {
	ProjectID  int64
	ActorID    sql.NullInt64
	TargetType string
	TargetID   sql.NullInt64
	Metadata   map[string]any
}

func legacyTodoAudits(t *testing.T, fixture *legacyTodoMutationFixture, action string, todoID int64) []legacyTodoAuditRow {
	t.Helper()
	rows, err := fixture.db.Query(`
		SELECT project_id, actor_user_id, target_type, target_id, metadata
		FROM audit_events
		WHERE action = ? AND target_type = 'todo' AND target_id = ?
		ORDER BY id`, action, todoID)
	if err != nil {
		t.Fatalf("query %s audits: %v", action, err)
	}
	defer rows.Close()
	var out []legacyTodoAuditRow
	for rows.Next() {
		var row legacyTodoAuditRow
		var metadata sql.NullString
		if err := rows.Scan(&row.ProjectID, &row.ActorID, &row.TargetType, &row.TargetID, &metadata); err != nil {
			t.Fatalf("scan %s audit: %v", action, err)
		}
		if metadata.Valid && metadata.String != "" {
			if err := json.Unmarshal([]byte(metadata.String), &row.Metadata); err != nil {
				t.Fatalf("decode %s metadata %q: %v", action, metadata.String, err)
			}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s audits: %v", action, err)
	}
	return out
}

func legacyTodoAssignmentCount(t *testing.T, fixture *legacyTodoMutationFixture, todoID int64) int {
	t.Helper()
	var count int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM todo_assignee_events WHERE todo_id = ?`, todoID).Scan(&count); err != nil {
		t.Fatalf("count assignee ledger: %v", err)
	}
	return count
}

func legacyTodoRead(t *testing.T, fixture *legacyTodoMutationFixture, todoID int64) store.Todo {
	t.Helper()
	var todo store.Todo
	var estimation, assignee, sprint sql.NullInt64
	var priority sql.NullString
	var createdAt, updatedAt int64
	err := fixture.db.QueryRow(`
		SELECT id, project_id, local_id, title, body, column_key, rank,
		       estimation_points, assignee_user_id, sprint_id, priority_key,
		       created_at, updated_at
		FROM todos WHERE id = ?`, todoID).Scan(
		&todo.ID, &todo.ProjectID, &todo.LocalID, &todo.Title, &todo.Body, &todo.ColumnKey, &todo.Rank,
		&estimation, &assignee, &sprint, &priority, &createdAt, &updatedAt,
	)
	if err != nil {
		t.Fatalf("read todo %d: %v", todoID, err)
	}
	if estimation.Valid {
		value := estimation.Int64
		todo.EstimationPoints = &value
	}
	if assignee.Valid {
		value := assignee.Int64
		todo.AssigneeUserID = &value
	}
	if sprint.Valid {
		value := sprint.Int64
		todo.SprintID = &value
	}
	if priority.Valid {
		value := priority.String
		todo.PriorityKey = &value
	}
	todo.CreatedAt = time.UnixMilli(createdAt).UTC()
	todo.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	tagRows, err := fixture.db.Query(`
		SELECT tags.name
		FROM todo_tags JOIN tags ON tags.id = todo_tags.tag_id
		WHERE todo_tags.todo_id = ? ORDER BY tags.name`, todoID)
	if err != nil {
		t.Fatalf("query todo tags: %v", err)
	}
	defer tagRows.Close()
	for tagRows.Next() {
		var tag string
		if err := tagRows.Scan(&tag); err != nil {
			t.Fatalf("scan todo tag: %v", err)
		}
		todo.Tags = append(todo.Tags, tag)
	}
	return todo
}

func legacyTodoRowCount(t *testing.T, fixture *legacyTodoMutationFixture, todoID int64) int {
	t.Helper()
	var count int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM todos WHERE id = ?`, todoID).Scan(&count); err != nil {
		t.Fatalf("count todo row: %v", err)
	}
	return count
}

func legacyTodoAssertProjectionIdentity(t *testing.T, got todoJSON, want store.Todo) {
	t.Helper()
	if got.ID != want.ID || got.ProjectID != want.ProjectID || got.LocalID != want.LocalID {
		t.Fatalf("projection identity=%+v, want global=%d project=%d local=%d", got, want.ID, want.ProjectID, want.LocalID)
	}
}

func legacyTodoAssertAnonymousAuditActor(t *testing.T, fixture *legacyTodoMutationFixture, action string, todoID int64) {
	t.Helper()
	audits := legacyTodoAudits(t, fixture, action, todoID)
	if len(audits) != 1 || audits[0].ActorID.Valid {
		t.Fatalf("%s audits=%+v, want one with NULL actor", action, audits)
	}
}

func TestLegacyTodoMutationGlobalIDValidationContract(t *testing.T) {
	cases := []struct {
		name   string
		method string
		pathID string
		suffix string
		body   any
	}{
		{name: "malformed_patch", method: http.MethodPatch, pathID: "not-a-number", body: map[string]any{"assigneeUserId": nil}},
		{name: "zero_move", method: http.MethodPost, pathID: "0", suffix: "/move", body: map[string]any{"toColumnKey": store.DefaultColumnDoing}},
		{name: "negative_delete", method: http.MethodDelete, pathID: "-7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newLegacyTodoMutationFixture(t, "full")
			_, ownerCtx, client := legacyTodoBootstrapOwner(t, fixture)
			project := legacyTodoCreateProject(t, fixture, ownerCtx, "Global ID validation")
			control := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Control"})
			legacyTodoResetEvents(fixture)

			response, body := doJSON(t, client, tc.method, fmt.Sprintf("%s/api/todos/%s%s", fixture.ts.URL, tc.pathID, tc.suffix), tc.body, nil)
			legacyTodoAssertError(t, response, body, http.StatusBadRequest, "VALIDATION_ERROR", "invalid todo id", "invalid_todo_id", "todoId")
			if legacyTodoRowCount(t, fixture, control.ID) != 1 || len(legacyTodoAudits(t, fixture, "todo_updated", control.ID))+len(legacyTodoAudits(t, fixture, "todo_moved", control.ID))+len(legacyTodoAudits(t, fixture, "todo_deleted", control.ID)) != 0 {
				t.Fatal("invalid global ID changed the control Todo or wrote a mutation audit")
			}
			legacyTodoAssertNoEvents(t, fixture, project.ID)
		})
	}
}

func TestLegacyTodoMutationsAnonymousModeContract(t *testing.T) {
	fixture := newLegacyTodoMutationFixture(t, "anonymous")
	client := legacyTodoAnonymousClient(t, fixture)
	legacyTodoSeedGlobalIdentity(t, fixture, context.Background(), store.ModeAnonymous)
	project := legacyTodoCreateTemporaryProject(t, fixture, context.Background())
	patchTodo := legacyTodoCreateTodo(t, fixture, context.Background(), project.ID, store.ModeAnonymous, store.CreateTodoInput{Title: "Anonymous patch", Body: "before"})
	moveTodo := legacyTodoCreateTodo(t, fixture, context.Background(), project.ID, store.ModeAnonymous, store.CreateTodoInput{Title: "Anonymous move"})
	deleteTodo := legacyTodoCreateTodo(t, fixture, context.Background(), project.ID, store.ModeAnonymous, store.CreateTodoInput{Title: "Anonymous delete"})
	if patchTodo.ID == patchTodo.LocalID || moveTodo.ID == moveTodo.LocalID || deleteTodo.ID == deleteTodo.LocalID {
		t.Fatalf("fixture identity did not diverge: patch=%+v move=%+v delete=%+v", patchTodo, moveTodo, deleteTodo)
	}

	legacyTodoResetEvents(fixture)
	patchBody := legacyTodoFullPatch(patchTodo)
	patchBody["body"] = "anonymous body"
	response, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, patchTodo.ID), patchBody, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("anonymous PATCH status=%d body=%s", response.StatusCode, body)
	}
	if got := legacyTodoRead(t, fixture, patchTodo.ID); got.Body != "anonymous body" || got.AssigneeUserID != nil {
		t.Fatalf("anonymous PATCH persisted=%+v", got)
	}
	legacyTodoAssertAnonymousAuditActor(t, fixture, "todo_updated", patchTodo.ID)
	legacyTodoAssertOneRefresh(t, fixture, project.ID, "todo_updated", 0)

	legacyTodoResetEvents(fixture)
	response, body = doJSON(t, client, http.MethodPost, fmt.Sprintf("%s/api/todos/%d/move", fixture.ts.URL, moveTodo.ID), map[string]any{"toColumnKey": store.DefaultColumnDoing}, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("anonymous MOVE status=%d body=%s", response.StatusCode, body)
	}
	if got := legacyTodoRead(t, fixture, moveTodo.ID); got.ColumnKey != store.DefaultColumnDoing {
		t.Fatalf("anonymous MOVE column=%q", got.ColumnKey)
	}
	legacyTodoAssertAnonymousAuditActor(t, fixture, "todo_moved", moveTodo.ID)
	legacyTodoAssertOneRefresh(t, fixture, project.ID, "todo_moved", 0)

	legacyTodoResetEvents(fixture)
	response, body = doJSON(t, client, http.MethodDelete, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, deleteTodo.ID), nil, nil)
	if response.StatusCode != http.StatusNoContent || len(body) != 0 {
		t.Fatalf("anonymous DELETE status=%d body=%q, want empty 204", response.StatusCode, body)
	}
	if legacyTodoRowCount(t, fixture, deleteTodo.ID) != 0 {
		t.Fatal("anonymous DELETE retained Todo")
	}
	legacyTodoAssertAnonymousAuditActor(t, fixture, "todo_deleted", deleteTodo.ID)
	legacyTodoAssertOneRefresh(t, fixture, project.ID, "todo_deleted", 0)
}
