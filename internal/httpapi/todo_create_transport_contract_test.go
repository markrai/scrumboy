package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func todoCreateRESTPayload(title string) map[string]any {
	return map[string]any{
		"title": title,
		"body":  "created through REST",
		"tags":  []string{"create-contract"},
	}
}

func createTodoThroughREST(t *testing.T, client *http.Client, baseURL, slug string, payload map[string]any) todoJSON {
	t.Helper()
	var created todoJSON
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/api/board/"+slug+"/todos", payload, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST create status=%d body=%s", resp.StatusCode, body)
	}
	return created
}

func countTodoCreateAudits(t *testing.T, db *sql.DB, todoID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM audit_events
		WHERE action = 'todo_created' AND target_type = 'todo' AND target_id = ?
	`, todoID).Scan(&count); err != nil {
		t.Fatalf("count todo_created audits: %v", err)
	}
	return count
}

func countProjectTodoCreateAudits(t *testing.T, db *sql.DB, projectID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action = 'todo_created'`, projectID).Scan(&count); err != nil {
		t.Fatalf("count project todo_created audits: %v", err)
	}
	return count
}

func countTodoCreateAssignmentRows(t *testing.T, db *sql.DB, todoID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM todo_assignee_events WHERE todo_id = ?`, todoID).Scan(&count); err != nil {
		t.Fatalf("count create assignment rows: %v", err)
	}
	return count
}

func countProjectTodos(t *testing.T, db *sql.DB, projectID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM todos WHERE project_id = ?`, projectID).Scan(&count); err != nil {
		t.Fatalf("count project todos: %v", err)
	}
	return count
}

func assertTodoCreateRESTEvents(t *testing.T, events []todoUpdateWireEvent, projectID int64, refreshReason string, wantAssigned int) {
	t.Helper()
	var refreshes, assigned []todoUpdateWireEvent
	for _, event := range events {
		switch event.Type {
		case "refresh_needed":
			refreshes = append(refreshes, event)
		case "todo.assigned":
			assigned = append(assigned, event)
		default:
			t.Fatalf("unexpected create event type; events=%+v", events)
		}
	}
	if len(refreshes) != 1 || refreshes[0].ProjectID != projectID || refreshes[0].Reason != refreshReason {
		t.Fatalf("create refresh mismatch: want one project=%d reason=%q, events=%+v", projectID, refreshReason, events)
	}
	if len(assigned) != wantAssigned {
		t.Fatalf("create structured assignment count=%d want=%d; events=%+v", len(assigned), wantAssigned, events)
	}
	for _, event := range refreshes {
		if refreshReason == "todo_assigned" && event.Reason == "todo_created" {
			t.Fatalf("assigned create also emitted todo_created; events=%+v", events)
		}
	}
}

func createTodoCreateProject(t *testing.T, st *store.Store, ownerID int64, name string) (store.Project, context.Context) {
	t.Helper()
	ctx := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctx, name)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return project, ctx
}

func createTodoCreateFixture(t *testing.T, st *store.Store, ctx context.Context, projectID int64, title, columnKey string) store.Todo {
	t.Helper()
	todo, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{Title: title, ColumnKey: columnKey}, store.ModeFull)
	if err != nil {
		t.Fatalf("create todo fixture: %v", err)
	}
	return todo
}

func TestTodoCreateRESTRealtimeContracts(t *testing.T) {
	t.Run("unassigned create emits exactly one todo_created refresh", func(t *testing.T) {
		ts, db, cleanup := newTestHTTPServer(t, "full")
		defer cleanup()
		st := wireTodoUpdatePublisher(t, ts)
		client := newCookieClient(t)
		owner := bootstrapUserClient(t, client, ts.URL, "Owner", "rest-create-owner@example.com", "password123")
		ownerID := int64(owner["id"].(float64))
		project, _ := createTodoCreateProject(t, st, ownerID, "REST create refresh")
		stream := subscribeTodoUpdateEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")

		created := createTodoThroughREST(t, client, ts.URL, project.Slug, todoCreateRESTPayload("unassigned create"))
		events := collectTodoUpdateEvents(t, stream)
		assertTodoCreateRESTEvents(t, events, project.ID, "todo_created", 0)
		if got := countTodoCreateAudits(t, db, created.ID); got != 1 {
			t.Fatalf("todo_created audit count=%d want=1", got)
		}
		if got := countTodoCreateAssignmentRows(t, db, created.ID); got != 0 {
			t.Fatalf("unassigned create ledger rows=%d want=0", got)
		}
	})

	t.Run("assigned create emits one assigned refresh and one structured event", func(t *testing.T) {
		ts, db, cleanup := newTestHTTPServer(t, "full")
		defer cleanup()
		st := wireTodoUpdatePublisher(t, ts)
		client := newCookieClient(t)
		owner := bootstrapUserClient(t, client, ts.URL, "Owner", "rest-create-assigned-owner@example.com", "password123")
		ownerID := int64(owner["id"].(float64))
		project, ownerCtx := createTodoCreateProject(t, st, ownerID, "REST assigned create")
		assigneeID, _ := createUserAPI(t, client, ts.URL, "Assignee", "rest-create-assignee@example.com", "password123")
		if err := st.AddProjectMember(ownerCtx, ownerID, project.ID, assigneeID, store.RoleViewer); err != nil {
			t.Fatalf("add assignee: %v", err)
		}
		stream := subscribeTodoUpdateEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")

		payload := todoCreateRESTPayload("assigned create")
		payload["assigneeUserId"] = assigneeID
		created := createTodoThroughREST(t, client, ts.URL, project.Slug, payload)
		events := collectTodoUpdateEvents(t, stream)
		assertTodoCreateRESTEvents(t, events, project.ID, "todo_assigned", 1)

		var assigned todoUpdateWireEvent
		for _, event := range events {
			if event.Type == "todo.assigned" {
				assigned = event
			}
		}
		if assigned.ProjectSlug != project.Slug || assigned.Payload.TodoID != created.ID || assigned.Payload.AssigneeID != assigneeID || assigned.Payload.ActorUserID != ownerID {
			t.Fatalf("assigned create payload mismatch: %+v", assigned)
		}
		if got := countTodoCreateAudits(t, db, created.ID); got != 1 {
			t.Fatalf("assigned create audit count=%d want=1", got)
		}
		// Create currently publishes the assignment event but does not insert an
		// initial todo_assignee_events ledger row. Characterize that distinction.
		if got := countTodoCreateAssignmentRows(t, db, created.ID); got != 0 {
			t.Fatalf("assigned create ledger rows=%d want=0", got)
		}
	})

	t.Run("store validation failure is realtime silent", func(t *testing.T) {
		ts, db, cleanup := newTestHTTPServer(t, "full")
		defer cleanup()
		st := wireTodoUpdatePublisher(t, ts)
		client := newCookieClient(t)
		owner := bootstrapUserClient(t, client, ts.URL, "Owner", "rest-create-failure-owner@example.com", "password123")
		ownerID := int64(owner["id"].(float64))
		project, _ := createTodoCreateProject(t, st, ownerID, "REST create failure")
		stream := subscribeTodoUpdateEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")

		var envelope apiErrorEnvelope
		resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/todos", todoCreateRESTPayload(""), &envelope)
		if resp.StatusCode != http.StatusBadRequest || envelope.Error.Code != "VALIDATION_ERROR" {
			t.Fatalf("invalid create status=%d envelope=%+v body=%s", resp.StatusCode, envelope, body)
		}
		if events := collectTodoUpdateEvents(t, stream); len(events) != 0 {
			t.Fatalf("failed create emitted events: %+v", events)
		}
		if got := countProjectTodos(t, db, project.ID); got != 0 {
			t.Fatalf("failed create persisted todos=%d", got)
		}
		if got := countProjectTodoCreateAudits(t, db, project.ID); got != 0 {
			t.Fatalf("failed create audit count=%d", got)
		}
	})
}

func TestTodoCreateRESTPositionIdentityAndOrdering(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	st := wireTodoUpdatePublisher(t, ts)
	client := newCookieClient(t)
	owner := bootstrapUserClient(t, client, ts.URL, "Owner", "rest-create-position-owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))

	t.Run("route position uses internal todo IDs rather than project-local IDs", func(t *testing.T) {
		foreign, foreignCtx := createTodoCreateProject(t, st, ownerID, "REST position foreign")
		foreignTodo := createTodoCreateFixture(t, st, foreignCtx, foreign.ID, "foreign local one", store.DefaultColumnBacklog)
		project, ctx := createTodoCreateProject(t, st, ownerID, "REST position identity")
		anchor := createTodoCreateFixture(t, st, ctx, project.ID, "target local one", store.DefaultColumnBacklog)
		if anchor.LocalID != foreignTodo.LocalID || anchor.ID == anchor.LocalID {
			t.Fatalf("fixture must distinguish identity: target=%+v foreign=%+v", anchor, foreignTodo)
		}

		payload := todoCreateRESTPayload("local ID must not resolve")
		payload["position"] = map[string]any{"afterId": anchor.LocalID}
		var envelope apiErrorEnvelope
		resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/todos", payload, &envelope)
		if resp.StatusCode != http.StatusNotFound || envelope.Error.Code != "NOT_FOUND" {
			t.Fatalf("local-ID anchor status=%d envelope=%+v body=%s", resp.StatusCode, envelope, body)
		}

		payload = todoCreateRESTPayload("internal ID resolves")
		payload["position"] = map[string]any{"afterId": anchor.ID}
		created := createTodoThroughREST(t, client, ts.URL, project.Slug, payload)
		if created.Rank <= anchor.Rank {
			t.Fatalf("created rank=%d want after anchor rank=%d", created.Rank, anchor.Rank)
		}

		payload = todoCreateRESTPayload("foreign internal ID")
		payload["position"] = map[string]any{"afterId": foreignTodo.ID}
		resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/todos", payload, &envelope)
		if resp.StatusCode != http.StatusNotFound || envelope.Error.Code != "NOT_FOUND" {
			t.Fatalf("foreign anchor status=%d envelope=%+v body=%s", resp.StatusCode, envelope, body)
		}
	})

	t.Run("ordered and reversed two-anchor contracts", func(t *testing.T) {
		project, ctx := createTodoCreateProject(t, st, ownerID, "REST position ordering")
		after := createTodoCreateFixture(t, st, ctx, project.ID, "after", store.DefaultColumnBacklog)
		before := createTodoCreateFixture(t, st, ctx, project.ID, "before", store.DefaultColumnBacklog)

		payload := todoCreateRESTPayload("between")
		payload["position"] = map[string]any{"afterId": after.ID, "beforeId": before.ID}
		created := createTodoThroughREST(t, client, ts.URL, project.Slug, payload)
		if created.Rank <= after.Rank || created.Rank >= before.Rank {
			t.Fatalf("between rank=%d want %d < rank < %d", created.Rank, after.Rank, before.Rank)
		}

		payload = todoCreateRESTPayload("reversed")
		payload["position"] = map[string]any{"afterId": before.ID, "beforeId": after.ID}
		var envelope apiErrorEnvelope
		resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/todos", payload, &envelope)
		if resp.StatusCode != http.StatusConflict || envelope.Error.Code != "CONFLICT" {
			t.Fatalf("reversed anchors status=%d envelope=%+v body=%s", resp.StatusCode, envelope, body)
		}
	})

	t.Run("wrong-column internal anchor is not found", func(t *testing.T) {
		project, ctx := createTodoCreateProject(t, st, ownerID, "REST position column")
		anchor := createTodoCreateFixture(t, st, ctx, project.ID, "doing anchor", store.DefaultColumnDoing)
		payload := todoCreateRESTPayload("backlog target")
		payload["position"] = map[string]any{"afterId": anchor.ID}
		var envelope apiErrorEnvelope
		resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/todos", payload, &envelope)
		if resp.StatusCode != http.StatusNotFound || envelope.Error.Code != "NOT_FOUND" {
			t.Fatalf("wrong-column anchor status=%d envelope=%+v body=%s", resp.StatusCode, envelope, body)
		}
	})
}

func TestTodoCreateRESTAccessLaneRoleAndModeContracts(t *testing.T) {
	t.Run("access precedes malformed JSON", func(t *testing.T) {
		ts, db, cleanup := newTestHTTPServer(t, "full")
		defer cleanup()
		st := wireTodoUpdatePublisher(t, ts)
		ownerClient := newCookieClient(t)
		owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "rest-create-order-owner@example.com", "password123")
		ownerID := int64(owner["id"].(float64))
		project, ownerCtx := createTodoCreateProject(t, st, ownerID, "REST create access ordering")

		assertNotFoundBeforeJSON := func(t *testing.T, client *http.Client, slug string) {
			t.Helper()
			req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/board/"+slug+"/todos", strings.NewReader("{"))
			if err != nil {
				t.Fatalf("new malformed request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Scrumboy", "1")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("malformed create request: %v", err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read malformed response: %v", err)
			}
			var envelope apiErrorEnvelope
			if err := json.Unmarshal(body, &envelope); err != nil {
				t.Fatalf("decode error body %q: %v", body, err)
			}
			if resp.StatusCode != http.StatusNotFound || envelope.Error.Code != "NOT_FOUND" {
				t.Fatalf("status=%d envelope=%+v body=%s", resp.StatusCode, envelope, body)
			}
		}

		assertNotFoundBeforeJSON(t, newCookieClient(t), project.Slug)
		temporary, err := st.CreateAnonymousBoard(ownerCtx)
		if err != nil {
			t.Fatalf("create Temporary Board: %v", err)
		}
		if _, err := db.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Hour).UnixMilli(), temporary.ID); err != nil {
			t.Fatalf("expire Temporary Board: %v", err)
		}
		assertNotFoundBeforeJSON(t, ownerClient, temporary.Slug)
		if got := countProjectTodos(t, db, project.ID); got != 0 {
			t.Fatalf("access-ordering failures persisted todos=%d", got)
		}
	})

	t.Run("lane normalization and projection", func(t *testing.T) {
		ts, _, cleanup := newTestHTTPServer(t, "full")
		defer cleanup()
		st := wireTodoUpdatePublisher(t, ts)
		client := newCookieClient(t)
		owner := bootstrapUserClient(t, client, ts.URL, "Owner", "rest-create-lane-owner@example.com", "password123")
		ownerID := int64(owner["id"].(float64))
		project, ctx := createTodoCreateProject(t, st, ownerID, "REST create lanes")
		custom, err := st.AddWorkflowColumn(ctx, project.ID, "Review")
		if err != nil {
			t.Fatalf("add custom lane: %v", err)
		}

		created := createTodoThroughREST(t, client, ts.URL, project.Slug, map[string]any{"title": "default lane"})
		if created.ColumnKey != store.DefaultColumnBacklog || created.Status != "BACKLOG" || created.ProjectID != project.ID || created.LocalID != 1 {
			t.Fatalf("default projection=%+v", created)
		}

		payload := todoCreateRESTPayload("column wins")
		payload["columnKey"] = custom.Key
		payload["status"] = "DONE"
		created = createTodoThroughREST(t, client, ts.URL, project.Slug, payload)
		if created.ColumnKey != custom.Key || created.DoneAt != nil {
			t.Fatalf("column/status precedence projection=%+v", created)
		}

		payload = todoCreateRESTPayload("status fallback")
		payload["status"] = "IN_PROGRESS"
		created = createTodoThroughREST(t, client, ts.URL, project.Slug, payload)
		if created.ColumnKey != store.DefaultColumnDoing {
			t.Fatalf("status fallback projection=%+v", created)
		}

		payload = todoCreateRESTPayload("done projection")
		payload["columnKey"] = "DONE"
		created = createTodoThroughREST(t, client, ts.URL, project.Slug, payload)
		if created.ColumnKey != store.DefaultColumnDone || created.DoneAt == nil {
			t.Fatalf("done projection=%+v", created)
		}
	})

	t.Run("durable roles", func(t *testing.T) {
		ts, _, cleanup := newTestHTTPServer(t, "full")
		defer cleanup()
		st := wireTodoUpdatePublisher(t, ts)
		ownerClient := newCookieClient(t)
		owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "rest-create-role-owner@example.com", "password123")
		ownerID := int64(owner["id"].(float64))
		project, ctx := createTodoCreateProject(t, st, ownerID, "REST create roles")
		maintainerID, maintainer := createUserAPI(t, ownerClient, ts.URL, "Maintainer", "rest-create-maintainer@example.com", "password123")
		contributorID, contributor := createUserAPI(t, ownerClient, ts.URL, "Contributor", "rest-create-contributor@example.com", "password123")
		viewerID, viewer := createUserAPI(t, ownerClient, ts.URL, "Viewer", "rest-create-viewer@example.com", "password123")
		for id, role := range map[int64]store.ProjectRole{maintainerID: store.RoleMaintainer, contributorID: store.RoleContributor, viewerID: store.RoleViewer} {
			if err := st.AddProjectMember(ctx, ownerID, project.ID, id, role); err != nil {
				t.Fatalf("add member %d: %v", id, err)
			}
		}

		created := createTodoThroughREST(t, maintainer, ts.URL, project.Slug, todoCreateRESTPayload("maintainer create"))
		if created.Title != "maintainer create" {
			t.Fatalf("maintainer result=%+v", created)
		}
		for name, blocked := range map[string]struct {
			client     *http.Client
			wantStatus int
			wantCode   string
		}{
			"contributor": {client: contributor, wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN"},
			"viewer":      {client: viewer, wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND"},
		} {
			t.Run(name, func(t *testing.T) {
				var envelope apiErrorEnvelope
				resp, body := doJSON(t, blocked.client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/todos", todoCreateRESTPayload(name+" blocked"), &envelope)
				if resp.StatusCode != blocked.wantStatus || envelope.Error.Code != blocked.wantCode {
					t.Fatalf("status=%d envelope=%+v body=%s", resp.StatusCode, envelope, body)
				}
			})
		}
	})

	t.Run("Temporary Board link holder refreshes persisted activity", func(t *testing.T) {
		ts, db, cleanup := newTestHTTPServer(t, "full")
		defer cleanup()
		st := wireTodoUpdatePublisher(t, ts)
		ownerClient := newCookieClient(t)
		owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "rest-create-temp-owner@example.com", "password123")
		ownerID := int64(owner["id"].(float64))
		_, outsider := createUserAPI(t, ownerClient, ts.URL, "Outsider", "rest-create-temp-outsider@example.com", "password123")
		ctx := store.WithUserID(context.Background(), ownerID)
		project, err := st.CreateAnonymousBoard(ctx)
		if err != nil {
			t.Fatalf("create Temporary Board: %v", err)
		}
		activityBefore := time.Now().UTC().Add(-6 * time.Minute).UnixMilli()
		expiresBefore := time.Now().UTC().Add(24 * time.Hour).UnixMilli()
		if _, err := db.Exec(`UPDATE projects SET last_activity_at = ?, expires_at = ? WHERE id = ?`, activityBefore, expiresBefore, project.ID); err != nil {
			t.Fatalf("set activity baseline: %v", err)
		}

		created := createTodoThroughREST(t, outsider, ts.URL, project.Slug, todoCreateRESTPayload("Temporary link-holder create"))
		if created.ProjectID != project.ID {
			t.Fatalf("Temporary Board result=%+v", created)
		}
		var activityAfter, expiresAfter int64
		if err := db.QueryRow(`SELECT last_activity_at, expires_at FROM projects WHERE id = ?`, project.ID).Scan(&activityAfter, &expiresAfter); err != nil {
			t.Fatalf("read activity after create: %v", err)
		}
		if activityAfter <= activityBefore || expiresAfter <= expiresBefore {
			t.Fatalf("create did not refresh activity: activity %d->%d expires %d->%d", activityBefore, activityAfter, expiresBefore, expiresAfter)
		}
	})

	t.Run("Anonymous Board permits unassigned create and rejects assignment", func(t *testing.T) {
		ts, _, cleanup := newTestHTTPServer(t, "anonymous")
		defer cleanup()
		st := wireTodoUpdatePublisher(t, ts)
		project, err := st.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatalf("create Anonymous Board: %v", err)
		}
		created := createTodoThroughREST(t, ts.Client(), ts.URL, project.Slug, todoCreateRESTPayload("anonymous create"))
		if created.AssigneeUserId != nil {
			t.Fatalf("anonymous create assignee=%v", *created.AssigneeUserId)
		}

		payload := todoCreateRESTPayload("anonymous assigned")
		payload["assigneeUserId"] = int64(1)
		var envelope apiErrorEnvelope
		resp, body := doJSON(t, ts.Client(), http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/todos", payload, &envelope)
		if resp.StatusCode != http.StatusBadRequest || envelope.Error.Code != "VALIDATION_ERROR" {
			t.Fatalf("anonymous assignment status=%d envelope=%+v body=%s", resp.StatusCode, envelope, body)
		}
	})
}

func TestTodoCreateRESTResponseCarriesExistingFields(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	st := wireTodoUpdatePublisher(t, ts)
	client := newCookieClient(t)
	owner := bootstrapUserClient(t, client, ts.URL, "Owner", "rest-create-projection-owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	project, ctx := createTodoCreateProject(t, st, ownerID, "REST create projection")
	now := time.Now().UTC()
	sprint, err := st.CreateSprint(ctx, project.ID, "Create sprint", now, now.Add(7*24*time.Hour))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}
	points := int64(5)
	payload := map[string]any{
		"title":            "  projected title  ",
		"body":             "projected body",
		"tags":             []string{"Alpha", "Beta"},
		"columnKey":        "testing",
		"estimationPoints": points,
		"sprintId":         sprint.ID,
	}
	created := createTodoThroughREST(t, client, ts.URL, project.Slug, payload)
	if created.Title != "projected title" || created.Body != "projected body" || created.ColumnKey != store.DefaultColumnTesting {
		t.Fatalf("basic create projection=%+v", created)
	}
	if created.EstimationPoints == nil || *created.EstimationPoints != points || created.SprintId == nil || *created.SprintId != sprint.ID {
		t.Fatalf("nullable create projection=%+v", created)
	}
	if fmt.Sprint(created.Tags) != "[alpha beta]" {
		t.Fatalf("tag projection=%v want normalized tags", created.Tags)
	}
}

func TestTodoCreateRESTRejectsSprintAssignmentWhenDisabled(t *testing.T) {
	ts, db, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	st := wireTodoUpdatePublisher(t, ts)
	client := newCookieClient(t)
	owner := bootstrapUserClient(t, client, ts.URL, "Owner", "rest-create-disabled@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	project, ownerCtx := createTodoCreateProject(t, st, ownerID, "REST disabled sprint create")
	now := time.Now().UTC()
	sp, err := st.CreateSprint(ownerCtx, project.ID, "Dormant", now, now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	if err := st.UpdateProjectSprintsEnabled(ownerCtx, project.ID, ownerID, false); err != nil {
		t.Fatalf("disable sprints: %v", err)
	}
	stream := subscribeTodoUpdateEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")

	payload := todoCreateRESTPayload("blocked assignment")
	payload["sprintId"] = sp.ID
	var envelope apiErrorEnvelope
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/todos", payload, &envelope)
	if resp.StatusCode != http.StatusBadRequest || envelope.Error.Code != "VALIDATION_ERROR" ||
		envelope.Error.Message != store.ErrSprintsDisabled.Error() || envelope.Error.Details["reason"] != "sprints_disabled" {
		t.Fatalf("disabled assigned create status=%d envelope=%+v body=%s", resp.StatusCode, envelope, body)
	}
	if events := collectTodoUpdateEvents(t, stream); len(events) != 0 {
		t.Fatalf("disabled assigned create emitted events: %+v", events)
	}
	if got := countProjectTodos(t, db, project.ID); got != 0 {
		t.Fatalf("disabled assigned create persisted %d todos", got)
	}
	if got := countProjectTodoCreateAudits(t, db, project.ID); got != 0 {
		t.Fatalf("disabled assigned create persisted %d audits", got)
	}

	delete(payload, "sprintId")
	created := createTodoThroughREST(t, client, ts.URL, project.Slug, payload)
	if created.SprintId != nil {
		t.Fatalf("unscheduled create returned sprint: %+v", created)
	}
}
