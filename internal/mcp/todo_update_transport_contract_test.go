package mcp_test

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/httpapi"
	"scrumboy/internal/mcp"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

type todoUpdateMCPWireEvent struct {
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

type todoUpdateMCPEventStream struct {
	cancel context.CancelFunc
	body   interface{ Close() error }
	events <-chan todoUpdateMCPWireEvent
	errs   <-chan error
}

func newTodoUpdateMCPServer(t *testing.T, mode string) (*httptest.Server, *sql.DB, *store.Store, func()) {
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
	srv := httpapi.NewServer(st, httpapi.Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   mode,
		MCPHandler:     mcp.New(st, mcp.Options{Mode: mode}),
	})
	st.SetTodoAssignedPublisher(srv.PublishTodoAssigned)
	ts := httptest.NewServer(srv)
	return ts, sqlDB, st, func() {
		ts.Close()
		_ = sqlDB.Close()
	}
}

func subscribeTodoUpdateMCPEvents(t *testing.T, client *http.Client, eventsURL string) *todoUpdateMCPEventStream {
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

	events := make(chan todoUpdateMCPWireEvent, 16)
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
			var event todoUpdateMCPWireEvent
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
	return &todoUpdateMCPEventStream{cancel: cancel, body: resp.Body, events: events, errs: errs}
}

func (s *todoUpdateMCPEventStream) close() {
	s.cancel()
	_ = s.body.Close()
}

func collectTodoUpdateMCPEvents(t *testing.T, stream *todoUpdateMCPEventStream) []todoUpdateMCPWireEvent {
	t.Helper()
	quiet := time.NewTimer(175 * time.Millisecond)
	deadline := time.NewTimer(2 * time.Second)
	defer quiet.Stop()
	defer deadline.Stop()
	var got []todoUpdateMCPWireEvent
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
			quiet.Reset(175 * time.Millisecond)
		case err := <-stream.errs:
			t.Fatalf("SSE stream failed after events %+v: %v", got, err)
		case <-quiet.C:
			return got
		case <-deadline.C:
			t.Fatalf("SSE quiet window was not reached; events=%+v", got)
		}
	}
}

func loginTodoUpdateMCPUser(t *testing.T, ts *httptest.Server, email, password string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Transport: ts.Client().Transport, Jar: jar}
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", map[string]any{
		"email": email, "password": password,
	}, &map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s status=%d", email, resp.StatusCode)
	}
	return client
}

func createTodoUpdateMCPProject(t *testing.T, st *store.Store, ownerID int64, name string) (store.Project, context.Context) {
	t.Helper()
	ctx := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctx, name)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return project, ctx
}

func createTodoUpdateMCPTodo(t *testing.T, st *store.Store, ctx context.Context, projectID int64, title string, assigneeID, sprintID *int64) store.Todo {
	t.Helper()
	points := int64(3)
	todo, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
		Title:            title,
		Body:             "original body",
		Tags:             []string{"original"},
		ColumnKey:        "backlog",
		EstimationPoints: &points,
		AssigneeUserID:   assigneeID,
		SprintID:         sprintID,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}
	return todo
}

func callTodoUpdateMCP(t *testing.T, client *http.Client, baseURL, transport, tool string, args map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	if transport == "legacy" {
		return doMCP(t, client, baseURL+"/mcp", map[string]any{"tool": tool, "input": args})
	}
	return doJSONRPC(t, client, baseURL, map[string]any{
		"jsonrpc": "2.0",
		"id":      14,
		"method":  "tools/call",
		"params":  map[string]any{"name": tool, "arguments": args},
	})
}

func assertTodoUpdateMCPSuccess(t *testing.T, transport string, response map[string]any, projectSlug string, localID int64) map[string]any {
	t.Helper()
	var data map[string]any
	if transport == "legacy" {
		if got, want := sortedMapKeys(response), []string{"data", "meta", "ok"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("legacy envelope keys=%v want=%v response=%+v", got, want, response)
		}
		if response["ok"] != true {
			t.Fatalf("legacy response not successful: %+v", response)
		}
		if meta := response["meta"].(map[string]any); len(meta) != 0 {
			t.Fatalf("legacy metadata=%+v want empty", meta)
		}
		data = response["data"].(map[string]any)
	} else {
		if got, want := sortedMapKeys(response), []string{"id", "jsonrpc", "result"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("JSON-RPC envelope keys=%v want=%v response=%+v", got, want, response)
		}
		if response["jsonrpc"] != "2.0" || response["id"] != float64(14) {
			t.Fatalf("JSON-RPC identity mismatch: %+v", response)
		}
		result := response["result"].(map[string]any)
		if got, want := sortedMapKeys(result), []string{"content", "structuredContent"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("JSON-RPC result keys=%v want=%v result=%+v", got, want, result)
		}
		data = result["structuredContent"].(map[string]any)
		content := result["content"].([]any)
		if len(content) != 1 || content[0].(map[string]any)["type"] != "text" {
			t.Fatalf("JSON-RPC content=%+v", content)
		}
		var textData map[string]any
		if err := json.Unmarshal([]byte(content[0].(map[string]any)["text"].(string)), &textData); err != nil {
			t.Fatalf("decode JSON-RPC text content: %v", err)
		}
		if !reflect.DeepEqual(textData, data) {
			t.Fatalf("JSON-RPC text/structured divergence: text=%+v structured=%+v", textData, data)
		}
	}
	if got, want := sortedMapKeys(data), []string{"todo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("update data keys=%v want=%v data=%+v", got, want, data)
	}
	todo := data["todo"].(map[string]any)
	if todo["projectSlug"] != projectSlug || int64(todo["localId"].(float64)) != localID {
		t.Fatalf("returned todo identity=%+v want slug=%q localId=%d", todo, projectSlug, localID)
	}
	return todo
}

func todoUpdateMCPAuditCount(t *testing.T, db *sql.DB, todoID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'todo_updated' AND target_type = 'todo' AND target_id = ?`, todoID).Scan(&count); err != nil {
		t.Fatalf("count todo_updated audits: %v", err)
	}
	return count
}

func todoUpdateMCPAssignmentCount(t *testing.T, db *sql.DB, todoID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM todo_assignee_events WHERE todo_id = ?`, todoID).Scan(&count); err != nil {
		t.Fatalf("count assignment rows: %v", err)
	}
	return count
}

func assertNoTodoUpdateMCPEvents(t *testing.T, events []todoUpdateMCPWireEvent) {
	t.Helper()
	if len(events) != 0 {
		t.Fatalf("unexpected realtime events: %+v", events)
	}
}

func assertTodoUpdateMCPAssignmentEvents(t *testing.T, events []todoUpdateMCPWireEvent, project store.Project, todo store.Todo, assigneeID, actorID int64) {
	t.Helper()
	var refreshes, assigned []todoUpdateMCPWireEvent
	for _, event := range events {
		switch event.Type {
		case "refresh_needed":
			refreshes = append(refreshes, event)
		case "todo.assigned":
			assigned = append(assigned, event)
		default:
			t.Fatalf("unexpected event type; events=%+v", events)
		}
	}
	if len(refreshes) != 1 || refreshes[0].Reason != "todo_assigned" || refreshes[0].ProjectID != project.ID {
		t.Fatalf("assignment refresh mismatch: %+v", events)
	}
	if len(assigned) != 1 || assigned[0].ProjectSlug != project.Slug || assigned[0].Payload.TodoID != todo.ID || assigned[0].Payload.AssigneeID != assigneeID || assigned[0].Payload.ActorUserID != actorID {
		t.Fatalf("structured assignment mismatch: %+v", events)
	}
}

func TestTodoUpdateMCPTransportAliasMatrix(t *testing.T) {
	for _, tc := range []struct {
		tool      string
		transport string
	}{
		{tool: "todos_update", transport: "legacy"},
		{tool: "todos.update", transport: "legacy"},
		{tool: "todos_update", transport: "jsonrpc"},
		{tool: "todos.update", transport: "jsonrpc"},
	} {
		t.Run(tc.transport+"/"+tc.tool, func(t *testing.T) {
			ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
			defer cleanup()
			client := newCookieClient(t, ts)
			bootstrapUser(t, client, ts.URL)
			ownerID := firstUserID(t, db)
			project, ctx := createTodoUpdateMCPProject(t, st, ownerID, "MCP alias matrix")
			todo := createTodoUpdateMCPTodo(t, st, ctx, project.ID, "before", nil, nil)

			resp, out := callTodoUpdateMCP(t, client, ts.URL, tc.transport, tc.tool, map[string]any{
				"projectSlug": project.Slug,
				"localId":     todo.LocalID,
				"patch":       map[string]any{"title": "after " + tc.tool},
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
			}
			got := assertTodoUpdateMCPSuccess(t, tc.transport, out, project.Slug, todo.LocalID)
			if got["title"] != "after "+tc.tool {
				t.Fatalf("returned title=%v", got["title"])
			}
		})
	}

	t.Run("JSON-RPC dotted alias error envelope", func(t *testing.T) {
		ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
		defer cleanup()
		client := newCookieClient(t, ts)
		bootstrapUser(t, client, ts.URL)
		project, _ := createTodoUpdateMCPProject(t, st, firstUserID(t, db), "MCP alias error")
		resp, out := callTodoUpdateMCP(t, client, ts.URL, "jsonrpc", "todos.update", map[string]any{
			"projectSlug": project.Slug, "localId": 999, "patch": map[string]any{"title": "never"},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("JSON-RPC tool error HTTP status=%d", resp.StatusCode)
		}
		result := out["result"].(map[string]any)
		if got, want := sortedMapKeys(result), []string{"content", "isError", "structuredContent"}; !reflect.DeepEqual(got, want) || result["isError"] != true {
			t.Fatalf("JSON-RPC tool error result=%+v", result)
		}
		errorBody := result["structuredContent"].(map[string]any)
		if errorBody["code"] != "NOT_FOUND" || errorBody["message"] != "not found" {
			t.Fatalf("JSON-RPC tool error=%+v", errorBody)
		}
	})
}

func TestTodoUpdateMCPPriorityPresenceContract(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
			defer cleanup()
			client := newCookieClient(t, ts)
			bootstrapUser(t, client, ts.URL)
			project, ctx := createTodoUpdateMCPProject(t, st, firstUserID(t, db), "MCP priority presence "+transport)
			high := "high"
			todo, err := st.CreateTodo(ctx, project.ID, store.CreateTodoInput{Title: "priority presence", PriorityKey: &high}, store.ModeFull)
			if err != nil {
				t.Fatalf("create todo: %v", err)
			}

			assertUpdate := func(name string, patch map[string]any, want *string) {
				t.Helper()
				resp, out := callTodoUpdateMCP(t, client, ts.URL, transport, "todos_update", map[string]any{
					"projectSlug": project.Slug,
					"localId":     todo.LocalID,
					"patch":       patch,
				})
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("%s status=%d response=%+v", name, resp.StatusCode, out)
				}
				got, err := st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, store.ModeFull)
				if err != nil {
					t.Fatalf("%s reload: %v", name, err)
				}
				if (got.PriorityKey == nil) != (want == nil) || got.PriorityKey != nil && *got.PriorityKey != *want {
					t.Fatalf("%s priority=%v want=%v", name, got.PriorityKey, want)
				}
			}

			assertUpdate("omitted", map[string]any{"body": "omitted preserves"}, mcpStringPtr("high"))
			assertUpdate("clear", map[string]any{"priorityKey": nil}, nil)
			assertUpdate("assign", map[string]any{"priorityKey": "urgent"}, mcpStringPtr("urgent"))
		})
	}
}

func mcpStringPtr(value string) *string { return &value }

func TestTodoUpdateMCPRealtimeContracts(t *testing.T) {
	ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
	defer cleanup()
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, db)
	project, ctx := createTodoUpdateMCPProject(t, st, ownerID, "MCP realtime")
	assignee, err := st.CreateUser(context.Background(), "mcp-realtime-assignee@example.com", "password123", "Assignee")
	if err != nil {
		t.Fatalf("create assignee: %v", err)
	}
	if err := st.AddProjectMember(ctx, ownerID, project.ID, assignee.ID, store.RoleViewer); err != nil {
		t.Fatalf("add assignee: %v", err)
	}
	stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
	defer stream.close()

	t.Run("non-assignment update remains realtime silent", func(t *testing.T) {
		todo := createTodoUpdateMCPTodo(t, st, ctx, project.ID, "non-assignment before", nil, nil)
		now := time.Now().UTC()
		sprint, err := st.CreateSprint(ctx, project.ID, "Realtime sprint", now, now.Add(7*24*time.Hour))
		if err != nil {
			t.Fatalf("create sprint: %v", err)
		}
		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "todos_update", map[string]any{
			"projectSlug": project.Slug,
			"localId":     todo.LocalID,
			"patch": map[string]any{
				"title":            "non-assignment after",
				"body":             "updated body",
				"tags":             []string{"updated"},
				"estimationPoints": 5,
				"sprintId":         sprint.ID,
			},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		updated := assertTodoUpdateMCPSuccess(t, "legacy", out, project.Slug, todo.LocalID)
		if int64(updated["sprintId"].(float64)) != sprint.ID {
			t.Fatalf("returned sprintId=%v want=%d", updated["sprintId"], sprint.ID)
		}
		if got := todoUpdateMCPAuditCount(t, db, todo.ID); got != 1 {
			t.Fatalf("non-assignment todo_updated audit count=%d want=1", got)
		}
		if got := todoUpdateMCPAssignmentCount(t, db, todo.ID); got != 0 {
			t.Fatalf("non-assignment assignment row count=%d want=0", got)
		}
		assertNoTodoUpdateMCPEvents(t, collectTodoUpdateMCPEvents(t, stream))
	})

	t.Run("assignment update emits one refresh and one structured event", func(t *testing.T) {
		todo := createTodoUpdateMCPTodo(t, st, ctx, project.ID, "assignment before", nil, nil)
		beforeRows := todoUpdateMCPAssignmentCount(t, db, todo.ID)
		resp, out := callTodoUpdateMCP(t, client, ts.URL, "jsonrpc", "todos_update", map[string]any{
			"projectSlug": project.Slug, "localId": todo.LocalID, "patch": map[string]any{"assigneeUserId": assignee.ID},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoUpdateMCPSuccess(t, "jsonrpc", out, project.Slug, todo.LocalID)
		assertTodoUpdateMCPAssignmentEvents(t, collectTodoUpdateMCPEvents(t, stream), project, todo, assignee.ID, ownerID)
		if got := todoUpdateMCPAssignmentCount(t, db, todo.ID); got != beforeRows+1 {
			t.Fatalf("assignment rows=%d want=%d", got, beforeRows+1)
		}
		if got := todoUpdateMCPAuditCount(t, db, todo.ID); got != 0 {
			t.Fatalf("assignment-only todo_updated audit count=%d want=0", got)
		}
	})

	t.Run("failed update emits no event", func(t *testing.T) {
		todo := createTodoUpdateMCPTodo(t, st, ctx, project.ID, "failed unchanged", nil, nil)
		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "todos_update", map[string]any{
			"projectSlug": project.Slug, "localId": todo.LocalID, "patch": map[string]any{"columnKey": "done"},
		})
		if resp.StatusCode != http.StatusBadRequest || out["ok"] != false {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		if todoUpdateMCPAuditCount(t, db, todo.ID) != 0 || todoUpdateMCPAssignmentCount(t, db, todo.ID) != 0 {
			t.Fatalf("failed update created domain rows: audits=%d assignments=%d", todoUpdateMCPAuditCount(t, db, todo.ID), todoUpdateMCPAssignmentCount(t, db, todo.ID))
		}
		assertNoTodoUpdateMCPEvents(t, collectTodoUpdateMCPEvents(t, stream))
	})
}

func TestTodoUpdateMCPEmptyAndSemanticNoOpEffects(t *testing.T) {
	ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
	defer cleanup()
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, db)
	project, ctx := createTodoUpdateMCPProject(t, st, ownerID, "MCP no-op effects")
	stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
	defer stream.close()

	t.Run("empty patch reads and returns existing todo without persistence", func(t *testing.T) {
		todo := createTodoUpdateMCPTodo(t, st, ctx, project.ID, "empty patch", nil, nil)
		if _, err := db.Exec(`UPDATE todos SET updated_at = 1 WHERE id = ?`, todo.ID); err != nil {
			t.Fatalf("set updated_at sentinel: %v", err)
		}
		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "todos_update", map[string]any{
			"projectSlug": project.Slug, "localId": todo.LocalID, "patch": map[string]any{},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		returned := assertTodoUpdateMCPSuccess(t, "legacy", out, project.Slug, todo.LocalID)
		if returned["title"] != todo.Title {
			t.Fatalf("returned existing title=%v want=%q", returned["title"], todo.Title)
		}
		var updatedAt int64
		if err := db.QueryRow(`SELECT updated_at FROM todos WHERE id = ?`, todo.ID).Scan(&updatedAt); err != nil {
			t.Fatalf("read updated_at: %v", err)
		}
		if updatedAt != 1 || todoUpdateMCPAuditCount(t, db, todo.ID) != 0 || todoUpdateMCPAssignmentCount(t, db, todo.ID) != 0 {
			t.Fatalf("empty patch effects: updatedAt=%d audits=%d assignmentRows=%d", updatedAt, todoUpdateMCPAuditCount(t, db, todo.ID), todoUpdateMCPAssignmentCount(t, db, todo.ID))
		}
		assertNoTodoUpdateMCPEvents(t, collectTodoUpdateMCPEvents(t, stream))
	})

	t.Run("explicit existing value persists but has no domain effects", func(t *testing.T) {
		todo := createTodoUpdateMCPTodo(t, st, ctx, project.ID, "semantic no-op", nil, nil)
		if _, err := db.Exec(`UPDATE todos SET updated_at = 1 WHERE id = ?`, todo.ID); err != nil {
			t.Fatalf("set updated_at sentinel: %v", err)
		}
		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "todos_update", map[string]any{
			"projectSlug": project.Slug, "localId": todo.LocalID, "patch": map[string]any{"title": todo.Title},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoUpdateMCPSuccess(t, "legacy", out, project.Slug, todo.LocalID)
		var updatedAt int64
		if err := db.QueryRow(`SELECT updated_at FROM todos WHERE id = ?`, todo.ID).Scan(&updatedAt); err != nil {
			t.Fatalf("read updated_at: %v", err)
		}
		if updatedAt <= 1 {
			t.Fatalf("updated_at=%d: persistence did not run", updatedAt)
		}
		if todoUpdateMCPAuditCount(t, db, todo.ID) != 0 || todoUpdateMCPAssignmentCount(t, db, todo.ID) != 0 {
			t.Fatalf("semantic no-op created domain rows: audits=%d assignments=%d", todoUpdateMCPAuditCount(t, db, todo.ID), todoUpdateMCPAssignmentCount(t, db, todo.ID))
		}
		assertNoTodoUpdateMCPEvents(t, collectTodoUpdateMCPEvents(t, stream))
	})
}

func TestTodoUpdateMCPPrecedenceContracts(t *testing.T) {
	ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
	defer cleanup()
	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, db)
	project, ctx := createTodoUpdateMCPProject(t, st, ownerID, "MCP precedence")
	todo := createTodoUpdateMCPTodo(t, st, ctx, project.ID, "precedence untouched", nil, nil)
	if _, err := db.Exec(`UPDATE todos SET updated_at = 1 WHERE id = ?`, todo.ID); err != nil {
		t.Fatalf("set updated_at sentinel: %v", err)
	}

	assertLegacyError := func(t *testing.T, client *http.Client, args map[string]any, wantStatus int, wantCode, wantMessage string) {
		t.Helper()
		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "todos_update", args)
		if resp.StatusCode != wantStatus || out["ok"] != false {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		errorBody := out["error"].(map[string]any)
		if errorBody["code"] != wantCode || errorBody["message"] != wantMessage {
			t.Fatalf("error=%+v want code=%q message=%q", errorBody, wantCode, wantMessage)
		}
	}

	t.Run("authentication precedes update execution", func(t *testing.T) {
		assertLegacyError(t, newStatelessClient(ts), map[string]any{
			"projectSlug": project.Slug, "localId": todo.LocalID, "patch": map[string]any{"title": "never"},
		}, http.StatusUnauthorized, "AUTH_REQUIRED", "Sign-in required for this tool")
	})
	t.Run("basic envelope validation precedes project access", func(t *testing.T) {
		assertLegacyError(t, ownerClient, map[string]any{
			"projectSlug": "missing-project", "localId": todo.LocalID,
		}, http.StatusBadRequest, "VALIDATION_ERROR", "missing patch")
	})
	t.Run("missing project precedes patch semantics", func(t *testing.T) {
		assertLegacyError(t, ownerClient, map[string]any{
			"projectSlug": "missing-project", "localId": todo.LocalID, "patch": map[string]any{"columnKey": "done"},
		}, http.StatusNotFound, "NOT_FOUND", "not found")
	})
	t.Run("expired project precedes patch semantics", func(t *testing.T) {
		temporary, err := st.CreateAnonymousBoard(ctx)
		if err != nil {
			t.Fatalf("create Temporary Board: %v", err)
		}
		if _, err := db.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Hour).UnixMilli(), temporary.ID); err != nil {
			t.Fatalf("expire Temporary Board: %v", err)
		}
		assertLegacyError(t, ownerClient, map[string]any{
			"projectSlug": temporary.Slug, "localId": 1, "patch": map[string]any{"title": nil},
		}, http.StatusNotFound, "NOT_FOUND", "not found")
	})
	t.Run("missing todo precedes patch semantics", func(t *testing.T) {
		assertLegacyError(t, ownerClient, map[string]any{
			"projectSlug": project.Slug, "localId": 999, "patch": map[string]any{"title": nil},
		}, http.StatusNotFound, "NOT_FOUND", "not found")
	})

	var title string
	var updatedAt int64
	if err := db.QueryRow(`SELECT title, updated_at FROM todos WHERE id = ?`, todo.ID).Scan(&title, &updatedAt); err != nil {
		t.Fatalf("read protected todo: %v", err)
	}
	if title != todo.Title || updatedAt != 1 || todoUpdateMCPAuditCount(t, db, todo.ID) != 0 {
		t.Fatalf("precedence failures mutated todo: title=%q updatedAt=%d audits=%d", title, updatedAt, todoUpdateMCPAuditCount(t, db, todo.ID))
	}

	t.Run("Anonymous Mode capability precedes input", func(t *testing.T) {
		anonymousTS, _, _, anonymousCleanup := newTodoUpdateMCPServer(t, "anonymous")
		defer anonymousCleanup()
		resp, out := callTodoUpdateMCP(t, newStatelessClient(anonymousTS), anonymousTS.URL, "legacy", "todos_update", map[string]any{})
		if resp.StatusCode != http.StatusForbidden || out["ok"] != false {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		errorBody := out["error"].(map[string]any)
		if errorBody["code"] != "CAPABILITY_UNAVAILABLE" || errorBody["message"] != "todos_update is unavailable in anonymous mode" {
			t.Fatalf("anonymous capability error=%+v", errorBody)
		}
	})
}

func TestTodoUpdateMCPRoleAndModeContracts(t *testing.T) {
	t.Run("durable roles", func(t *testing.T) {
		ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
		defer cleanup()
		ownerClient := newCookieClient(t, ts)
		bootstrapUser(t, ownerClient, ts.URL)
		ownerID := firstUserID(t, db)
		project, ctx := createTodoUpdateMCPProject(t, st, ownerID, "MCP roles")

		maintainer, err := st.CreateUser(context.Background(), "mcp-maintainer@example.com", "password123", "Maintainer")
		if err != nil {
			t.Fatalf("create maintainer: %v", err)
		}
		contributor, err := st.CreateUser(context.Background(), "mcp-contributor@example.com", "password123", "Contributor")
		if err != nil {
			t.Fatalf("create contributor: %v", err)
		}
		viewer, err := st.CreateUser(context.Background(), "mcp-viewer@example.com", "password123", "Viewer")
		if err != nil {
			t.Fatalf("create viewer: %v", err)
		}
		for id, role := range map[int64]store.ProjectRole{maintainer.ID: store.RoleMaintainer, contributor.ID: store.RoleContributor, viewer.ID: store.RoleViewer} {
			if err := st.AddProjectMember(ctx, ownerID, project.ID, id, role); err != nil {
				t.Fatalf("add member %d: %v", id, err)
			}
		}
		maintainerClient := loginTodoUpdateMCPUser(t, ts, maintainer.Email, "password123")
		contributorClient := loginTodoUpdateMCPUser(t, ts, contributor.Email, "password123")
		viewerClient := loginTodoUpdateMCPUser(t, ts, viewer.Email, "password123")
		if pc, err := st.GetProjectContextBySlug(store.WithUserID(context.Background(), maintainer.ID), project.Slug, store.ModeFull); err != nil || pc.Role != store.RoleMaintainer {
			t.Fatalf("maintainer fixture access: context=%+v err=%v", pc, err)
		}
		var authStatus map[string]any
		if resp := doJSON(t, maintainerClient, http.MethodGet, ts.URL+"/api/auth/status", nil, &authStatus); resp.StatusCode != http.StatusOK {
			t.Fatalf("maintainer auth status=%d body=%+v", resp.StatusCode, authStatus)
		}
		authUser := authStatus["user"].(map[string]any)
		if int64(authUser["id"].(float64)) != maintainer.ID {
			t.Fatalf("maintainer client user=%+v want id=%d", authUser, maintainer.ID)
		}

		maintainerTodo := createTodoUpdateMCPTodo(t, st, ctx, project.ID, "maintainer before", nil, nil)
		resp, out := callTodoUpdateMCP(t, maintainerClient, ts.URL, "legacy", "todos_update", map[string]any{
			"projectSlug": project.Slug, "localId": maintainerTodo.LocalID,
			"patch": map[string]any{"title": "maintainer after", "body": "changed", "tags": []string{"changed"}, "estimationPoints": 5},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("maintainer status=%d response=%+v", resp.StatusCode, out)
		}

		assignedTodo := createTodoUpdateMCPTodo(t, st, ctx, project.ID, "restricted title", &contributor.ID, nil)
		resp, out = callTodoUpdateMCP(t, contributorClient, ts.URL, "legacy", "todos_update", map[string]any{
			"projectSlug": project.Slug, "localId": assignedTodo.LocalID,
			"patch": map[string]any{"title": "attempted title", "body": "contributor body", "tags": []string{"attempted"}},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("assigned contributor status=%d response=%+v", resp.StatusCode, out)
		}
		var title, body string
		if err := db.QueryRow(`SELECT title, body FROM todos WHERE id = ?`, assignedTodo.ID).Scan(&title, &body); err != nil {
			t.Fatalf("read contributor todo: %v", err)
		}
		if title != assignedTodo.Title || body != "contributor body" {
			t.Fatalf("contributor result title=%q body=%q", title, body)
		}

		for name, tc := range map[string]struct {
			client     *http.Client
			todo       store.Todo
			wantStatus int
			wantCode   string
		}{
			"unassigned contributor": {client: contributorClient, todo: createTodoUpdateMCPTodo(t, st, ctx, project.ID, "unassigned", nil, nil), wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN"},
			"viewer":                 {client: viewerClient, todo: createTodoUpdateMCPTodo(t, st, ctx, project.ID, "viewer", nil, nil), wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND"},
		} {
			t.Run(name, func(t *testing.T) {
				resp, out := callTodoUpdateMCP(t, tc.client, ts.URL, "legacy", "todos_update", map[string]any{
					"projectSlug": project.Slug, "localId": tc.todo.LocalID, "patch": map[string]any{"body": "blocked"},
				})
				if resp.StatusCode != tc.wantStatus || out["error"].(map[string]any)["code"] != tc.wantCode {
					t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
				}
			})
		}
	})

	t.Run("active Temporary Board nonmember", func(t *testing.T) {
		ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
		defer cleanup()
		ownerClient := newCookieClient(t, ts)
		bootstrapUser(t, ownerClient, ts.URL)
		ownerID := firstUserID(t, db)
		outsider, err := st.CreateUser(context.Background(), "mcp-temp-outsider@example.com", "password123", "Outsider")
		if err != nil {
			t.Fatalf("create outsider: %v", err)
		}
		outsiderClient := loginTodoUpdateMCPUser(t, ts, outsider.Email, "password123")
		ctx := store.WithUserID(context.Background(), ownerID)
		project, err := st.CreateAnonymousBoard(ctx)
		if err != nil {
			t.Fatalf("create Temporary Board: %v", err)
		}
		todo := createTodoUpdateMCPTodo(t, st, ctx, project.ID, "temporary before", nil, nil)
		resp, out := callTodoUpdateMCP(t, outsiderClient, ts.URL, "legacy", "todos_update", map[string]any{
			"projectSlug": project.Slug, "localId": todo.LocalID, "patch": map[string]any{"title": "temporary after"},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Temporary Board status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoUpdateMCPSuccess(t, "legacy", out, project.Slug, todo.LocalID)
	})
}

func TestTodoUpdateMCPTemporaryBoardSprintOmissionPreservesSprint(t *testing.T) {
	ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
	defer cleanup()
	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, db)
	outsider, err := st.CreateUser(context.Background(), "mcp-sprint-edge-outsider@example.com", "password123", "Outsider")
	if err != nil {
		t.Fatalf("create outsider: %v", err)
	}
	outsiderClient := loginTodoUpdateMCPUser(t, ts, outsider.Email, "password123")
	ctx := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("create Temporary Board: %v", err)
	}
	now := time.Now().UTC()
	sprint, err := st.CreateSprint(ctx, project.ID, "Scheduled", now, now.Add(7*24*time.Hour))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}
	todo := createTodoUpdateMCPTodo(t, st, ctx, project.ID, "scheduled before", nil, &sprint.ID)
	if _, err := db.Exec(`UPDATE todos SET updated_at = 1 WHERE id = ?`, todo.ID); err != nil {
		t.Fatalf("set updated_at sentinel: %v", err)
	}
	stream := subscribeTodoUpdateMCPEvents(t, outsiderClient, ts.URL+"/api/board/"+project.Slug+"/events")
	defer stream.close()
	staleActivity := time.Now().UTC().Add(-6 * time.Minute).UnixMilli()
	nearExpiry := time.Now().UTC().Add(24 * time.Hour).UnixMilli()
	if _, err := db.Exec(`UPDATE projects SET last_activity_at = ?, expires_at = ? WHERE id = ?`, staleActivity, nearExpiry, project.ID); err != nil {
		t.Fatalf("set deterministic activity baseline: %v", err)
	}
	var activityBefore, expiresBefore int64
	if err := db.QueryRow(`SELECT last_activity_at, expires_at FROM projects WHERE id = ?`, project.ID).Scan(&activityBefore, &expiresBefore); err != nil {
		t.Fatalf("read Temporary Board activity: %v", err)
	}
	auditsBefore := todoUpdateMCPAuditCount(t, db, todo.ID)
	assignmentsBefore := todoUpdateMCPAssignmentCount(t, db, todo.ID)

	resp, out := callTodoUpdateMCP(t, outsiderClient, ts.URL, "legacy", "todos_update", map[string]any{
		"projectSlug": project.Slug,
		"localId":     todo.LocalID,
		"patch":       map[string]any{"title": "scheduled after"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy /mcp status=%d response=%+v", resp.StatusCode, out)
	}
	returned := assertTodoUpdateMCPSuccess(t, "legacy", out, project.Slug, todo.LocalID)
	if returned["title"] != "scheduled after" || int64(returned["sprintId"].(float64)) != sprint.ID {
		t.Fatalf("sprint omission response=%+v", returned)
	}
	var title string
	var sprintID sql.NullInt64
	var updatedAt, activityAfter, expiresAfter int64
	if err := db.QueryRow(`SELECT title, sprint_id, updated_at FROM todos WHERE id = ?`, todo.ID).Scan(&title, &sprintID, &updatedAt); err != nil {
		t.Fatalf("read scheduled todo: %v", err)
	}
	if err := db.QueryRow(`SELECT last_activity_at, expires_at FROM projects WHERE id = ?`, project.ID).Scan(&activityAfter, &expiresAfter); err != nil {
		t.Fatalf("read Temporary Board activity after update: %v", err)
	}
	if title != "scheduled after" || !sprintID.Valid || sprintID.Int64 != sprint.ID || updatedAt <= 1 {
		t.Fatalf("sparse update result: title=%q sprint=%+v updatedAt=%d", title, sprintID, updatedAt)
	}
	if todoUpdateMCPAuditCount(t, db, todo.ID) != auditsBefore+1 || todoUpdateMCPAssignmentCount(t, db, todo.ID) != assignmentsBefore {
		t.Fatalf("sparse update domain rows: audits %d->%d assignments %d->%d", auditsBefore, todoUpdateMCPAuditCount(t, db, todo.ID), assignmentsBefore, todoUpdateMCPAssignmentCount(t, db, todo.ID))
	}
	if activityAfter <= activityBefore || expiresAfter <= expiresBefore {
		t.Fatalf("successful update did not refresh activity: last_activity %d->%d expires %d->%d", activityBefore, activityAfter, expiresBefore, expiresAfter)
	}
	assertNoTodoUpdateMCPEvents(t, collectTodoUpdateMCPEvents(t, stream))
}
