package httpapi

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type todoUpdateWireEvent struct {
	ID          string `json:"id,omitempty"`
	Type        string `json:"type"`
	ProjectID   int64  `json:"projectId"`
	ProjectSlug string `json:"projectSlug,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Payload     struct {
		TodoID      int64  `json:"todoId"`
		Title       string `json:"title"`
		AssigneeID  int64  `json:"assigneeId"`
		ActorUserID int64  `json:"actorUserId"`
	} `json:"payload,omitempty"`
}

type todoUpdateEventStream struct {
	cancel context.CancelFunc
	body   interface{ Close() error }
	events <-chan todoUpdateWireEvent
	errs   <-chan error
}

func subscribeTodoUpdateEvents(t *testing.T, client *http.Client, eventsURL string) *todoUpdateEventStream {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsURL, nil)
	if err != nil {
		cancel()
		t.Fatalf("new SSE request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("connect SSE: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		t.Fatalf("connect SSE status=%d", resp.StatusCode)
	}

	events := make(chan todoUpdateWireEvent, 16)
	errs := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(resp.Body)
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				select {
				case <-ctx.Done():
				case errs <- readErr:
				}
				return
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var event todoUpdateWireEvent
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				select {
				case <-ctx.Done():
				case errs <- fmt.Errorf("decode SSE event %q: %w", payload, err):
				}
				return
			}
			if event.Type == "ping" {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case events <- event:
			}
		}
	}()

	stream := &todoUpdateEventStream{cancel: cancel, body: resp.Body, events: events, errs: errs}
	t.Cleanup(func() {
		stream.cancel()
		_ = stream.body.Close()
	})
	return stream
}

func collectTodoUpdateEvents(t *testing.T, stream *todoUpdateEventStream) []todoUpdateWireEvent {
	t.Helper()
	defer stream.cancel()
	defer stream.body.Close()

	const quietWindow = 175 * time.Millisecond
	const overallTimeout = 2 * time.Second
	quiet := time.NewTimer(quietWindow)
	deadline := time.NewTimer(overallTimeout)
	defer quiet.Stop()
	defer deadline.Stop()

	var got []todoUpdateWireEvent
	for {
		select {
		case event := <-stream.events:
			got = append(got, event)
			if !quiet.Stop() {
				select {
				case <-quiet.C:
				default:
				}
			}
			quiet.Reset(quietWindow)
		case err := <-stream.errs:
			t.Fatalf("SSE stream failed after events %+v: %v", got, err)
		case <-quiet.C:
			return got
		case <-deadline.C:
			t.Fatalf("SSE quiet window was not reached; events=%+v", got)
		}
	}
}

func wireTodoUpdatePublisher(t *testing.T, ts *httptest.Server) *store.Store {
	t.Helper()
	srv, ok := ts.Config.Handler.(*Server)
	if !ok {
		t.Fatalf("test handler type=%T, want *Server", ts.Config.Handler)
	}
	st, ok := srv.store.(*store.Store)
	if !ok {
		t.Fatalf("test store type=%T, want *store.Store", srv.store)
	}
	st.SetTodoAssignedPublisher(srv.PublishTodoAssigned)
	return st
}

func createTodoUpdateProject(t *testing.T, st *store.Store, ownerID int64, name string) (store.Project, context.Context) {
	t.Helper()
	ctx := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctx, name)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return project, ctx
}

func createTodoUpdateTodo(t *testing.T, st *store.Store, ctx context.Context, projectID int64, title string, assigneeID *int64) store.Todo {
	t.Helper()
	points := int64(3)
	todo, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
		Title:            title,
		Body:             "original body",
		Tags:             []string{"original"},
		ColumnKey:        "backlog",
		EstimationPoints: &points,
		AssigneeUserID:   assigneeID,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}
	return todo
}

func todoUpdateRESTPayload(title, body string, tags []string, points *int64, assigneeID *int64) map[string]any {
	return map[string]any{
		"title":            title,
		"body":             body,
		"tags":             tags,
		"estimationPoints": points,
		"assigneeUserId":   assigneeID,
	}
}

func countTodoUpdateAudits(t *testing.T, db *sql.DB, todoID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM audit_events
		WHERE action = 'todo_updated' AND target_type = 'todo' AND target_id = ?
	`, todoID).Scan(&count); err != nil {
		t.Fatalf("count todo_updated audits: %v", err)
	}
	return count
}

func countTodoAssignmentRows(t *testing.T, db *sql.DB, todoID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM todo_assignee_events WHERE todo_id = ?`, todoID).Scan(&count); err != nil {
		t.Fatalf("count todo assignment rows: %v", err)
	}
	return count
}

func assertTodoUpdateRefreshes(t *testing.T, events []todoUpdateWireEvent, projectID int64, reason string, wantAssigned int) {
	t.Helper()
	var refreshes, assigned []todoUpdateWireEvent
	for _, event := range events {
		switch event.Type {
		case "refresh_needed":
			refreshes = append(refreshes, event)
		case "todo.assigned":
			assigned = append(assigned, event)
		default:
			t.Fatalf("unexpected SSE event type; events=%+v", events)
		}
	}
	if len(refreshes) != 1 || refreshes[0].ProjectID != projectID || refreshes[0].Reason != reason {
		t.Fatalf("refresh contract mismatch: want one project=%d reason=%q, events=%+v", projectID, reason, events)
	}
	if len(assigned) != wantAssigned {
		t.Fatalf("assigned event count=%d want=%d; events=%+v", len(assigned), wantAssigned, events)
	}
	for _, event := range refreshes {
		if reason == "todo_assigned" && event.Reason == "todo_updated" {
			t.Fatalf("assignment emitted todo_updated; events=%+v", events)
		}
	}
}

func TestTodoUpdateRESTRealtimeContracts(t *testing.T) {
	t.Run("non-assignment update emits exactly one todo_updated refresh", func(t *testing.T) {
		ts, db, cleanup := newTestHTTPServer(t, "full")
		defer cleanup()
		st := wireTodoUpdatePublisher(t, ts)
		client := newCookieClient(t)
		owner := bootstrapUserClient(t, client, ts.URL, "Owner", "rest-update-owner@example.com", "password123")
		ownerID := int64(owner["id"].(float64))
		project, ctx := createTodoUpdateProject(t, st, ownerID, "REST update refresh")
		todo := createTodoUpdateTodo(t, st, ctx, project.ID, "before", nil)
		stream := subscribeTodoUpdateEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")

		points := int64(3)
		resp, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/board/%s/todos/%d", ts.URL, project.Slug, todo.LocalID),
			todoUpdateRESTPayload("after", todo.Body, todo.Tags, &points, nil), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PATCH status=%d body=%s", resp.StatusCode, body)
		}
		events := collectTodoUpdateEvents(t, stream)
		assertTodoUpdateRefreshes(t, events, project.ID, "todo_updated", 0)
		if got := countTodoUpdateAudits(t, db, todo.ID); got != 1 {
			t.Fatalf("todo_updated audit count=%d want=1", got)
		}
		if got := countTodoAssignmentRows(t, db, todo.ID); got != 0 {
			t.Fatalf("assignment row count=%d want=0", got)
		}
	})

	t.Run("assignment set emits one assigned refresh and one structured event", func(t *testing.T) {
		ts, db, cleanup := newTestHTTPServer(t, "full")
		defer cleanup()
		st := wireTodoUpdatePublisher(t, ts)
		client := newCookieClient(t)
		owner := bootstrapUserClient(t, client, ts.URL, "Owner", "rest-assign-owner@example.com", "password123")
		ownerID := int64(owner["id"].(float64))
		project, ctx := createTodoUpdateProject(t, st, ownerID, "REST assignment set")
		todo := createTodoUpdateTodo(t, st, ctx, project.ID, "assign me", nil)
		assigneeID, _ := createUserAPI(t, client, ts.URL, "Assignee", "rest-assignee@example.com", "password123")
		if err := st.AddProjectMember(ctx, ownerID, project.ID, assigneeID, store.RoleViewer); err != nil {
			t.Fatalf("add assignee member: %v", err)
		}
		stream := subscribeTodoUpdateEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")

		points := int64(3)
		resp, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/board/%s/todos/%d", ts.URL, project.Slug, todo.LocalID),
			todoUpdateRESTPayload(todo.Title, todo.Body, todo.Tags, &points, &assigneeID), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PATCH status=%d body=%s", resp.StatusCode, body)
		}
		events := collectTodoUpdateEvents(t, stream)
		assertTodoUpdateRefreshes(t, events, project.ID, "todo_assigned", 1)
		var assigned todoUpdateWireEvent
		for _, event := range events {
			if event.Type == "todo.assigned" {
				assigned = event
			}
		}
		if assigned.ProjectSlug != project.Slug || assigned.Payload.TodoID != todo.ID || assigned.Payload.AssigneeID != assigneeID || assigned.Payload.ActorUserID != ownerID {
			t.Fatalf("structured assignment mismatch: %+v", assigned)
		}
		if got := countTodoAssignmentRows(t, db, todo.ID); got != 1 {
			t.Fatalf("assignment row count=%d want=1", got)
		}
		if got := countTodoUpdateAudits(t, db, todo.ID); got != 0 {
			t.Fatalf("assignment-only todo_updated audit count=%d want=0", got)
		}
	})

	t.Run("assignment clear emits only one assigned refresh", func(t *testing.T) {
		ts, db, cleanup := newTestHTTPServer(t, "full")
		defer cleanup()
		st := wireTodoUpdatePublisher(t, ts)
		client := newCookieClient(t)
		owner := bootstrapUserClient(t, client, ts.URL, "Owner", "rest-clear-owner@example.com", "password123")
		ownerID := int64(owner["id"].(float64))
		project, ctx := createTodoUpdateProject(t, st, ownerID, "REST assignment clear")
		assigneeID, _ := createUserAPI(t, client, ts.URL, "Assignee", "rest-clear-assignee@example.com", "password123")
		if err := st.AddProjectMember(ctx, ownerID, project.ID, assigneeID, store.RoleViewer); err != nil {
			t.Fatalf("add assignee member: %v", err)
		}
		todo := createTodoUpdateTodo(t, st, ctx, project.ID, "clear me", &assigneeID)
		beforeRows := countTodoAssignmentRows(t, db, todo.ID)
		stream := subscribeTodoUpdateEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")

		points := int64(3)
		resp, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/board/%s/todos/%d", ts.URL, project.Slug, todo.LocalID),
			todoUpdateRESTPayload(todo.Title, todo.Body, todo.Tags, &points, nil), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PATCH status=%d body=%s", resp.StatusCode, body)
		}
		events := collectTodoUpdateEvents(t, stream)
		assertTodoUpdateRefreshes(t, events, project.ID, "todo_assigned", 0)
		if got := countTodoAssignmentRows(t, db, todo.ID); got != beforeRows+1 {
			t.Fatalf("assignment row count=%d want=%d", got, beforeRows+1)
		}
	})

	t.Run("route validation failure emits no event", func(t *testing.T) {
		ts, _, cleanup := newTestHTTPServer(t, "full")
		defer cleanup()
		st := wireTodoUpdatePublisher(t, ts)
		client := newCookieClient(t)
		owner := bootstrapUserClient(t, client, ts.URL, "Owner", "rest-failure-owner@example.com", "password123")
		ownerID := int64(owner["id"].(float64))
		project, ctx := createTodoUpdateProject(t, st, ownerID, "REST validation failure")
		todo := createTodoUpdateTodo(t, st, ctx, project.ID, "unchanged", nil)
		stream := subscribeTodoUpdateEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")

		var envelope apiErrorEnvelope
		resp, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/board/%s/todos/%d", ts.URL, project.Slug, todo.LocalID), map[string]any{
			"title": "missing required replacement key",
		}, &envelope)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("PATCH status=%d body=%s", resp.StatusCode, body)
		}
		assertAPIError(t, envelope, "VALIDATION_ERROR", "assigneeUserId", "missing_assignee_user_id")
		if events := collectTodoUpdateEvents(t, stream); len(events) != 0 {
			t.Fatalf("validation failure emitted events: %+v", events)
		}
		// This protects route sequencing; it deliberately does not simulate a transactional store failure.
	})
}

func TestTodoUpdateRESTSemanticNoOpContract(t *testing.T) {
	ts, db, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	st := wireTodoUpdatePublisher(t, ts)
	client := newCookieClient(t)
	owner := bootstrapUserClient(t, client, ts.URL, "Owner", "rest-noop-owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	project, ctx := createTodoUpdateProject(t, st, ownerID, "REST semantic no-op")
	todo := createTodoUpdateTodo(t, st, ctx, project.ID, "same title", nil)
	if _, err := db.Exec(`UPDATE todos SET updated_at = 1 WHERE id = ?`, todo.ID); err != nil {
		t.Fatalf("set sentinel updated_at: %v", err)
	}
	stream := subscribeTodoUpdateEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")

	points := int64(3)
	resp, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/board/%s/todos/%d", ts.URL, project.Slug, todo.LocalID),
		todoUpdateRESTPayload(todo.Title, todo.Body, todo.Tags, &points, nil), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH status=%d body=%s", resp.StatusCode, body)
	}
	var updatedAt int64
	if err := db.QueryRow(`SELECT updated_at FROM todos WHERE id = ?`, todo.ID).Scan(&updatedAt); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}
	if updatedAt <= 1 {
		t.Fatalf("updated_at=%d: store update path did not execute", updatedAt)
	}
	if got := countTodoUpdateAudits(t, db, todo.ID); got != 0 {
		t.Fatalf("semantic no-op audit count=%d want=0", got)
	}
	if got := countTodoAssignmentRows(t, db, todo.ID); got != 0 {
		t.Fatalf("semantic no-op assignment row count=%d want=0", got)
	}
	assertTodoUpdateRefreshes(t, collectTodoUpdateEvents(t, stream), project.ID, "todo_updated", 0)
}

func TestTodoUpdateRESTAccessPrecedesValidation(t *testing.T) {
	ts, db, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	st := wireTodoUpdatePublisher(t, ts)
	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "rest-order-owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	project, ctx := createTodoUpdateProject(t, st, ownerID, "REST access ordering")
	todo := createTodoUpdateTodo(t, st, ctx, project.ID, "untouched", nil)
	outsider := newCookieClient(t)

	cases := []struct {
		name string
		path string
		body map[string]any
	}{
		{name: "invalid local id", path: "not-a-number", body: map[string]any{}},
		{name: "incomplete body", path: fmt.Sprint(todo.LocalID), body: map[string]any{"title": "missing assignee"}},
		{name: "unsupported field", path: fmt.Sprint(todo.LocalID), body: map[string]any{"columnKey": "done"}},
	}
	for _, tc := range cases {
		t.Run("inaccessible/"+tc.name, func(t *testing.T) {
			var envelope apiErrorEnvelope
			resp, body := doJSON(t, outsider, http.MethodPatch, fmt.Sprintf("%s/api/board/%s/todos/%s", ts.URL, project.Slug, tc.path), tc.body, &envelope)
			if resp.StatusCode != http.StatusNotFound || envelope.Error.Code != "NOT_FOUND" {
				t.Fatalf("status=%d envelope=%+v body=%s", resp.StatusCode, envelope, body)
			}
		})
	}

	temporary, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("create Temporary Board: %v", err)
	}
	temporaryTodo := createTodoUpdateTodo(t, st, ctx, temporary.ID, "expired", nil)
	if _, err := db.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Hour).UnixMilli(), temporary.ID); err != nil {
		t.Fatalf("expire Temporary Board: %v", err)
	}
	for _, tc := range []struct {
		name string
		path string
		body map[string]any
	}{
		{name: "invalid local id", path: "not-a-number", body: map[string]any{}},
		{name: "incomplete body", path: fmt.Sprint(temporaryTodo.LocalID), body: map[string]any{"title": "missing assignee"}},
		{name: "unsupported field", path: fmt.Sprint(temporaryTodo.LocalID), body: map[string]any{"columnKey": "done"}},
	} {
		t.Run("expired/"+tc.name, func(t *testing.T) {
			var envelope apiErrorEnvelope
			resp, body := doJSON(t, ownerClient, http.MethodPatch, fmt.Sprintf("%s/api/board/%s/todos/%s", ts.URL, temporary.Slug, tc.path), tc.body, &envelope)
			if resp.StatusCode != http.StatusNotFound || envelope.Error.Code != "NOT_FOUND" {
				t.Fatalf("status=%d envelope=%+v body=%s", resp.StatusCode, envelope, body)
			}
		})
	}
	if got := countTodoUpdateAudits(t, db, todo.ID); got != 0 {
		t.Fatalf("ordering failures mutated todo; audit count=%d", got)
	}
}

func TestTodoUpdateRESTRoleAndModeContracts(t *testing.T) {
	t.Run("durable roles", func(t *testing.T) {
		ts, db, cleanup := newTestHTTPServer(t, "full")
		defer cleanup()
		st := wireTodoUpdatePublisher(t, ts)
		ownerClient := newCookieClient(t)
		owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "rest-role-owner@example.com", "password123")
		ownerID := int64(owner["id"].(float64))
		project, ctx := createTodoUpdateProject(t, st, ownerID, "REST roles")

		maintainerID, maintainer := createUserAPI(t, ownerClient, ts.URL, "Maintainer", "rest-maintainer@example.com", "password123")
		contributorID, contributor := createUserAPI(t, ownerClient, ts.URL, "Contributor", "rest-contributor@example.com", "password123")
		viewerID, viewer := createUserAPI(t, ownerClient, ts.URL, "Viewer", "rest-viewer@example.com", "password123")
		for id, role := range map[int64]store.ProjectRole{maintainerID: store.RoleMaintainer, contributorID: store.RoleContributor, viewerID: store.RoleViewer} {
			if err := st.AddProjectMember(ctx, ownerID, project.ID, id, role); err != nil {
				t.Fatalf("add member %d: %v", id, err)
			}
		}

		maintainerTodo := createTodoUpdateTodo(t, st, ctx, project.ID, "maintainer before", nil)
		now := time.Now().UTC()
		sprint, err := st.CreateSprint(ctx, project.ID, "Maintainer sprint", now, now.Add(7*24*time.Hour))
		if err != nil {
			t.Fatalf("create maintainer sprint: %v", err)
		}
		points := int64(5)
		maintainerPatch := todoUpdateRESTPayload("maintainer after", "changed", []string{"changed"}, &points, nil)
		maintainerPatch["sprintId"] = sprint.ID
		resp, body := doJSON(t, maintainer, http.MethodPatch, fmt.Sprintf("%s/api/board/%s/todos/%d", ts.URL, project.Slug, maintainerTodo.LocalID),
			maintainerPatch, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("maintainer PATCH status=%d body=%s", resp.StatusCode, body)
		}
		var persistedSprintID sql.NullInt64
		if err := db.QueryRow(`SELECT sprint_id FROM todos WHERE id = ?`, maintainerTodo.ID).Scan(&persistedSprintID); err != nil {
			t.Fatalf("read maintainer sprint: %v", err)
		}
		if !persistedSprintID.Valid || persistedSprintID.Int64 != sprint.ID {
			t.Fatalf("maintainer sprint=%+v want=%d", persistedSprintID, sprint.ID)
		}

		assignedTodo := createTodoUpdateTodo(t, st, ctx, project.ID, "restricted title", &contributorID)
		resp, body = doJSON(t, contributor, http.MethodPatch, fmt.Sprintf("%s/api/board/%s/todos/%d", ts.URL, project.Slug, assignedTodo.LocalID),
			todoUpdateRESTPayload("attempted title", "contributor body", []string{"attempted"}, &points, nil), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("assigned contributor PATCH status=%d body=%s", resp.StatusCode, body)
		}
		var title, todoBody string
		if err := db.QueryRow(`SELECT title, body FROM todos WHERE id = ?`, assignedTodo.ID).Scan(&title, &todoBody); err != nil {
			t.Fatalf("read assigned contributor todo: %v", err)
		}
		if title != assignedTodo.Title || todoBody != "contributor body" {
			t.Fatalf("assigned contributor result title=%q body=%q", title, todoBody)
		}

		for name, tc := range map[string]struct {
			client     *http.Client
			todo       store.Todo
			wantStatus int
		}{
			"unassigned contributor": {client: contributor, todo: createTodoUpdateTodo(t, st, ctx, project.ID, "unassigned", nil), wantStatus: http.StatusForbidden},
			"viewer":                 {client: viewer, todo: createTodoUpdateTodo(t, st, ctx, project.ID, "viewer", nil), wantStatus: http.StatusNotFound},
		} {
			t.Run(name, func(t *testing.T) {
				resp, body := doJSON(t, tc.client, http.MethodPatch, fmt.Sprintf("%s/api/board/%s/todos/%d", ts.URL, project.Slug, tc.todo.LocalID),
					todoUpdateRESTPayload(tc.todo.Title, "blocked", tc.todo.Tags, tc.todo.EstimationPoints, nil), nil)
				if resp.StatusCode != tc.wantStatus {
					t.Fatalf("PATCH status=%d want=%d body=%s", resp.StatusCode, tc.wantStatus, body)
				}
			})
		}
	})

	t.Run("Temporary Board link holder", func(t *testing.T) {
		ts, _, cleanup := newTestHTTPServer(t, "full")
		defer cleanup()
		st := wireTodoUpdatePublisher(t, ts)
		ownerClient := newCookieClient(t)
		owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "rest-temp-owner@example.com", "password123")
		ownerID := int64(owner["id"].(float64))
		_, outsider := createUserAPI(t, ownerClient, ts.URL, "Outsider", "rest-temp-outsider@example.com", "password123")
		ctx := store.WithUserID(context.Background(), ownerID)
		project, err := st.CreateAnonymousBoard(ctx)
		if err != nil {
			t.Fatalf("create Temporary Board: %v", err)
		}
		todo := createTodoUpdateTodo(t, st, ctx, project.ID, "temporary before", nil)
		resp, body := doJSON(t, outsider, http.MethodPatch, fmt.Sprintf("%s/api/board/%s/todos/%d", ts.URL, project.Slug, todo.LocalID),
			todoUpdateRESTPayload("temporary after", todo.Body, todo.Tags, todo.EstimationPoints, nil), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Temporary Board PATCH status=%d body=%s", resp.StatusCode, body)
		}
	})

	t.Run("Anonymous Board", func(t *testing.T) {
		ts, _, cleanup := newTestHTTPServer(t, "anonymous")
		defer cleanup()
		st := wireTodoUpdatePublisher(t, ts)
		project, err := st.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatalf("create Anonymous Board: %v", err)
		}
		todo := createTodoUpdateTodo(t, st, context.Background(), project.ID, "anonymous before", nil)
		client := ts.Client()
		resp, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/board/%s/todos/%d", ts.URL, project.Slug, todo.LocalID),
			todoUpdateRESTPayload("anonymous after", todo.Body, todo.Tags, todo.EstimationPoints, nil), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Anonymous Board PATCH status=%d body=%s", resp.StatusCode, body)
		}
		fakeAssignee := int64(999)
		var envelope apiErrorEnvelope
		resp, body = doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/board/%s/todos/%d", ts.URL, project.Slug, todo.LocalID),
			todoUpdateRESTPayload("anonymous after", todo.Body, todo.Tags, todo.EstimationPoints, &fakeAssignee), &envelope)
		if resp.StatusCode != http.StatusBadRequest || envelope.Error.Code != "VALIDATION_ERROR" {
			t.Fatalf("anonymous assignment status=%d envelope=%+v body=%s", resp.StatusCode, envelope, body)
		}
	})
}
