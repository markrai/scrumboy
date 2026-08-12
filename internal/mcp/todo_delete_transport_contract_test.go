package mcp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"reflect"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func todoDeleteMCPSessionClient(t *testing.T, tsURL string, transport http.RoundTripper, st *store.Store, userID int64) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	token, expiresAt, err := st.CreateSession(context.Background(), userID, 24*time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	baseURL, err := url.Parse(tsURL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	jar.SetCookies(baseURL, []*http.Cookie{{Name: "scrumboy_session", Value: token, Path: "/", Expires: expiresAt}})
	return &http.Client{Transport: transport, Jar: jar}
}

func createTodoDeleteMCPUser(t *testing.T, st *store.Store, email, name string) store.User {
	t.Helper()
	user, err := st.CreateUser(context.Background(), email, "password123", name)
	if err != nil {
		t.Fatalf("CreateUser %s: %v", email, err)
	}
	return user
}

func bootstrapTodoDeleteMCPOwner(t *testing.T, st *store.Store, email string) (store.User, context.Context) {
	t.Helper()
	owner, err := st.BootstrapUser(context.Background(), email, "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	return owner, store.WithUserID(context.Background(), owner.ID)
}

func createTodoDeleteMCPTodo(t *testing.T, st *store.Store, ctx context.Context, projectID int64, title string) store.Todo {
	t.Helper()
	todo, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
		Title:     title,
		ColumnKey: store.DefaultColumnBacklog,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo %q: %v", title, err)
	}
	return todo
}

func todoDeleteMCPAuditCount(t *testing.T, sqlDB *sql.DB, projectID, todoID int64) int {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(`
		SELECT COUNT(*)
		FROM audit_events
		WHERE project_id = ? AND action = 'todo_deleted' AND target_type = 'todo' AND target_id = ?`, projectID, todoID).Scan(&count); err != nil {
		t.Fatalf("count todo_deleted audits: %v", err)
	}
	return count
}

func todoDeleteMCPProjectAuditCount(t *testing.T, sqlDB *sql.DB, projectID int64) int {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action = 'todo_deleted'`, projectID).Scan(&count); err != nil {
		t.Fatalf("count project todo_deleted audits: %v", err)
	}
	return count
}

func assertTodoDeleteMCPSuccess(t *testing.T, transport string, response map[string]any, slug string, localID int64) {
	t.Helper()
	var data map[string]any
	if transport == "legacy" {
		if got, want := sortedMapKeys(response), []string{"data", "meta", "ok"}; !reflect.DeepEqual(got, want) || response["ok"] != true {
			t.Fatalf("legacy success envelope=%+v keys=%v want=%v", response, got, want)
		}
		meta, ok := response["meta"].(map[string]any)
		if !ok || len(meta) != 0 {
			t.Fatalf("legacy meta=%+v want empty object", response["meta"])
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
		if _, present := data["meta"]; present {
			t.Fatalf("JSON-RPC structured content unexpectedly includes legacy meta: %+v", data)
		}
	}
	if got, want := sortedMapKeys(data), []string{"localId", "projectSlug", "status"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("delete data keys=%v want=%v data=%+v", got, want, data)
	}
	if data["status"] != "deleted" || data["projectSlug"] != slug || data["localId"] != float64(localID) {
		t.Fatalf("delete projection=%+v want status=deleted slug=%q localId=%d", data, slug, localID)
	}
	if _, present := data["id"]; present {
		t.Fatalf("delete projection exposed global todo id: %+v", data)
	}
}

func assertTodoDeleteMCPLegacyError(t *testing.T, resp *http.Response, response map[string]any, wantStatus int, wantCode, wantMessage string) map[string]any {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Fatalf("legacy error status=%d response=%+v want %d", resp.StatusCode, response, wantStatus)
	}
	return assertTodoCreateMCPLegacyError(t, response, wantCode, wantMessage)
}

func TestTodoDeleteMCPTransportAliasMatrix(t *testing.T) {
	ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
	defer cleanup()
	owner, ownerCtx := bootstrapTodoDeleteMCPOwner(t, st, "owner-matrix@example.com")
	client := todoDeleteMCPSessionClient(t, ts.URL, ts.Client().Transport, st, owner.ID)

	for index, tc := range []struct {
		transport string
		tool      string
	}{
		{transport: "legacy", tool: "todos_delete"},
		{transport: "legacy", tool: "todos.delete"},
		{transport: "jsonrpc", tool: "todos_delete"},
		{transport: "jsonrpc", tool: "todos.delete"},
	} {
		t.Run(tc.transport+"/"+tc.tool, func(t *testing.T) {
			project, err := st.CreateProject(ownerCtx, fmt.Sprintf("MCP delete matrix %d", index))
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			todo := createTodoDeleteMCPTodo(t, st, ownerCtx, project.ID, "Delete via "+tc.tool)
			resp, out := callTodoUpdateMCP(t, client, ts.URL, tc.transport, tc.tool, map[string]any{
				"projectSlug": project.Slug,
				"localId":     todo.LocalID,
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status=%d response=%+v want 200", resp.StatusCode, out)
			}
			assertTodoDeleteMCPSuccess(t, tc.transport, out, project.Slug, todo.LocalID)
			if _, err := st.GetTodoByLocalID(ownerCtx, project.ID, todo.LocalID, store.ModeFull); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("GetTodoByLocalID after delete err=%v want ErrNotFound", err)
			}
			if audits := todoDeleteMCPAuditCount(t, sqlDB, project.ID, todo.ID); audits != 1 {
				t.Fatalf("delete audits=%d want 1", audits)
			}
		})
	}
}

func TestTodoDeleteMCPRealtimeContracts(t *testing.T) {
	t.Run("success is realtime silent", func(t *testing.T) {
		ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
		defer cleanup()
		owner, ownerCtx := bootstrapTodoDeleteMCPOwner(t, st, "owner-realtime-success@example.com")
		client := todoDeleteMCPSessionClient(t, ts.URL, ts.Client().Transport, st, owner.ID)
		project, err := st.CreateProject(ownerCtx, "MCP realtime success")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		todo := createTodoDeleteMCPTodo(t, st, ownerCtx, project.ID, "Delete silently")
		stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
		defer stream.close()

		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "todos_delete", map[string]any{"projectSlug": project.Slug, "localId": todo.LocalID})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoDeleteMCPSuccess(t, "legacy", out, project.Slug, todo.LocalID)
		if _, err := st.GetTodoByLocalID(ownerCtx, project.ID, todo.LocalID, store.ModeFull); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("deleted todo lookup err=%v want ErrNotFound", err)
		}
		if todoDeleteMCPAuditCount(t, sqlDB, project.ID, todo.ID) != 1 {
			t.Fatal("successful MCP delete did not create exactly one audit")
		}
		if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
			t.Fatalf("successful MCP delete emitted realtime events: %+v", events)
		}
	})

	t.Run("missing todo is realtime silent", func(t *testing.T) {
		ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
		defer cleanup()
		owner, ownerCtx := bootstrapTodoDeleteMCPOwner(t, st, "owner-realtime-missing@example.com")
		client := todoDeleteMCPSessionClient(t, ts.URL, ts.Client().Transport, st, owner.ID)
		project, err := st.CreateProject(ownerCtx, "MCP realtime missing")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		control := createTodoDeleteMCPTodo(t, st, ownerCtx, project.ID, "Keep me")
		stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
		defer stream.close()

		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "todos_delete", map[string]any{"projectSlug": project.Slug, "localId": control.LocalID + 1000})
		assertTodoDeleteMCPLegacyError(t, resp, out, http.StatusNotFound, "NOT_FOUND", "not found")
		if _, err := st.GetTodoByLocalID(ownerCtx, project.ID, control.LocalID, store.ModeFull); err != nil {
			t.Fatalf("control todo changed: %v", err)
		}
		if todoDeleteMCPProjectAuditCount(t, sqlDB, project.ID) != 0 {
			t.Fatal("missing MCP delete created deletion audit")
		}
		if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
			t.Fatalf("missing MCP delete emitted realtime events: %+v", events)
		}
	})

	t.Run("persistence failure is realtime silent", func(t *testing.T) {
		ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
		defer cleanup()
		owner, ownerCtx := bootstrapTodoDeleteMCPOwner(t, st, "owner-realtime-failure@example.com")
		client := todoDeleteMCPSessionClient(t, ts.URL, ts.Client().Transport, st, owner.ID)
		project, err := st.CreateProject(ownerCtx, "MCP realtime failure")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		todo := createTodoDeleteMCPTodo(t, st, ownerCtx, project.ID, "Retain me")
		if _, err := sqlDB.Exec(`
			CREATE TRIGGER phase21_mcp_abort_todo_delete
			BEFORE DELETE ON todos
			BEGIN
				SELECT RAISE(ABORT, 'forced todo delete failure');
			END`); err != nil {
			t.Fatalf("create aborting trigger: %v", err)
		}
		defer func() {
			if _, err := sqlDB.Exec(`DROP TRIGGER IF EXISTS phase21_mcp_abort_todo_delete`); err != nil {
				t.Errorf("drop aborting trigger: %v", err)
			}
		}()
		stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
		defer stream.close()

		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "todos_delete", map[string]any{"projectSlug": project.Slug, "localId": todo.LocalID})
		publicError := assertTodoDeleteMCPLegacyError(t, resp, out, http.StatusInternalServerError, "INTERNAL", "internal error")
		if details, ok := publicError["details"].(map[string]any); !ok || len(details) != 0 {
			t.Fatalf("public INTERNAL details=%+v want empty", publicError["details"])
		}
		if _, err := st.GetTodoByLocalID(ownerCtx, project.ID, todo.LocalID, store.ModeFull); err != nil {
			t.Fatalf("failed delete did not retain todo: %v", err)
		}
		if todoDeleteMCPAuditCount(t, sqlDB, project.ID, todo.ID) != 0 {
			t.Fatal("failed MCP delete committed deletion audit")
		}
		if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
			t.Fatalf("failed MCP delete emitted realtime events: %+v", events)
		}
	})
}

func TestTodoDeleteMCPAccessRoleAndModeContracts(t *testing.T) {
	durableCases := []struct {
		name       string
		role       store.ProjectRole
		owner      bool
		missing    bool
		wantStatus int
		wantCode   string
		wantMsg    string
	}{
		{name: "owner", owner: true, wantStatus: http.StatusOK},
		{name: "maintainer", role: store.RoleMaintainer, wantStatus: http.StatusOK},
		{name: "contributor existing todo", role: store.RoleContributor, wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN", wantMsg: "forbidden"},
		{name: "contributor missing todo", role: store.RoleContributor, missing: true, wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMsg: "not found"},
		{name: "viewer", role: store.RoleViewer, wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMsg: "not found"},
		{name: "non-member", wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMsg: "not found"},
	}

	for index, tc := range durableCases {
		t.Run(tc.name, func(t *testing.T) {
			ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
			defer cleanup()
			owner, ownerCtx := bootstrapTodoDeleteMCPOwner(t, st, fmt.Sprintf("owner-role-%d@example.com", index))
			project, err := st.CreateProject(ownerCtx, "MCP durable access "+tc.name)
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			todo := createTodoDeleteMCPTodo(t, st, ownerCtx, project.ID, "Role target")
			actor := owner
			if !tc.owner {
				actor = createTodoDeleteMCPUser(t, st, fmt.Sprintf("actor-role-%d@example.com", index), "Actor")
				if tc.role != "" {
					if err := st.AddProjectMember(ownerCtx, owner.ID, project.ID, actor.ID, tc.role); err != nil {
						t.Fatalf("AddProjectMember: %v", err)
					}
				}
			}
			client := todoDeleteMCPSessionClient(t, ts.URL, ts.Client().Transport, st, actor.ID)
			ownerEventsClient := todoDeleteMCPSessionClient(t, ts.URL, ts.Client().Transport, st, owner.ID)
			stream := subscribeTodoUpdateMCPEvents(t, ownerEventsClient, ts.URL+"/api/board/"+project.Slug+"/events")
			defer stream.close()
			localID := todo.LocalID
			if tc.missing {
				localID += 1000
			}
			resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "todos_delete", map[string]any{"projectSlug": project.Slug, "localId": localID})
			if tc.wantStatus == http.StatusOK {
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status=%d response=%+v want 200", resp.StatusCode, out)
				}
				assertTodoDeleteMCPSuccess(t, "legacy", out, project.Slug, todo.LocalID)
				if todoDeleteMCPAuditCount(t, sqlDB, project.ID, todo.ID) != 1 {
					t.Fatal("successful role delete did not create one audit")
				}
			} else {
				assertTodoDeleteMCPLegacyError(t, resp, out, tc.wantStatus, tc.wantCode, tc.wantMsg)
				if _, err := st.GetTodoByLocalID(ownerCtx, project.ID, todo.LocalID, store.ModeFull); err != nil {
					t.Fatalf("failed role delete changed todo: %v", err)
				}
				if todoDeleteMCPAuditCount(t, sqlDB, project.ID, todo.ID) != 0 {
					t.Fatal("failed role delete created deletion audit")
				}
			}
			if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
				t.Fatalf("role delete emitted realtime events: %+v", events)
			}
		})
	}

	t.Run("missing project", func(t *testing.T) {
		ts, _, st, cleanup := newTodoUpdateMCPServer(t, "full")
		defer cleanup()
		owner, ownerCtx := bootstrapTodoDeleteMCPOwner(t, st, "owner-missing-project@example.com")
		client := todoDeleteMCPSessionClient(t, ts.URL, ts.Client().Transport, st, owner.ID)
		controlProject, err := st.CreateProject(ownerCtx, "MCP missing-project event control")
		if err != nil {
			t.Fatalf("CreateProject control: %v", err)
		}
		stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+controlProject.Slug+"/events")
		defer stream.close()
		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "todos_delete", map[string]any{"projectSlug": "missing-project", "localId": 1})
		assertTodoDeleteMCPLegacyError(t, resp, out, http.StatusNotFound, "NOT_FOUND", "not found")
		if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
			t.Fatalf("missing-project MCP delete emitted realtime events: %+v", events)
		}
	})

	t.Run("authenticated link-holder on unexpired temporary board", func(t *testing.T) {
		ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
		defer cleanup()
		owner, ownerCtx := bootstrapTodoDeleteMCPOwner(t, st, "owner-temp-link@example.com")
		project, err := st.CreateAnonymousBoard(ownerCtx)
		if err != nil {
			t.Fatalf("CreateAnonymousBoard: %v", err)
		}
		todo := createTodoDeleteMCPTodo(t, st, ownerCtx, project.ID, "Temporary target")
		linkHolder := createTodoDeleteMCPUser(t, st, "link-holder@example.com", "Link Holder")
		client := todoDeleteMCPSessionClient(t, ts.URL, ts.Client().Transport, st, linkHolder.ID)
		ownerClient := todoDeleteMCPSessionClient(t, ts.URL, ts.Client().Transport, st, owner.ID)
		stream := subscribeTodoUpdateMCPEvents(t, ownerClient, ts.URL+"/api/board/"+project.Slug+"/events")
		defer stream.close()

		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "todos_delete", map[string]any{"projectSlug": project.Slug, "localId": todo.LocalID})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoDeleteMCPSuccess(t, "legacy", out, project.Slug, todo.LocalID)
		var actor sql.NullInt64
		if err := sqlDB.QueryRow(`SELECT actor_user_id FROM audit_events WHERE action = 'todo_deleted' AND target_id = ?`, todo.ID).Scan(&actor); err != nil {
			t.Fatalf("read temporary delete actor: %v", err)
		}
		if !actor.Valid || actor.Int64 != linkHolder.ID {
			t.Fatalf("temporary MCP delete actor=%+v want %d", actor, linkHolder.ID)
		}
		if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
			t.Fatalf("temporary MCP delete emitted realtime events: %+v", events)
		}
	})

	t.Run("authenticated caller on expired temporary board", func(t *testing.T) {
		ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
		defer cleanup()
		owner, ownerCtx := bootstrapTodoDeleteMCPOwner(t, st, "owner-expired-temp@example.com")
		project, err := st.CreateAnonymousBoard(ownerCtx)
		if err != nil {
			t.Fatalf("CreateAnonymousBoard: %v", err)
		}
		todo := createTodoDeleteMCPTodo(t, st, ownerCtx, project.ID, "Expired target")
		client := todoDeleteMCPSessionClient(t, ts.URL, ts.Client().Transport, st, owner.ID)
		stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
		defer stream.close()
		if _, err := sqlDB.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Minute).UnixMilli(), project.ID); err != nil {
			t.Fatalf("expire temporary board: %v", err)
		}
		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "todos_delete", map[string]any{"projectSlug": project.Slug, "localId": todo.LocalID})
		assertTodoDeleteMCPLegacyError(t, resp, out, http.StatusNotFound, "NOT_FOUND", "not found")
		if todoDeleteMCPAuditCount(t, sqlDB, project.ID, todo.ID) != 0 {
			t.Fatal("expired temporary MCP delete created deletion audit")
		}
		if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
			t.Fatalf("expired temporary MCP delete emitted realtime events: %+v", events)
		}
	})
}

func TestTodoDeleteMCPGateAndValidationPrecedence(t *testing.T) {
	t.Run("anonymous mode capability gate precedes malformed input", func(t *testing.T) {
		ts, _, _, cleanup := newTodoUpdateMCPServer(t, "anonymous")
		defer cleanup()
		resp, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{"tool": "todos_delete", "input": "malformed"})
		publicError := assertTodoDeleteMCPLegacyError(t, resp, out, http.StatusForbidden, "CAPABILITY_UNAVAILABLE", "todos_delete is unavailable in anonymous mode")
		if details := publicError["details"].(map[string]any); len(details) != 0 {
			t.Fatalf("capability details=%+v want empty", details)
		}
	})

	t.Run("bootstrap gate precedes malformed input", func(t *testing.T) {
		ts, _, _, cleanup := newTodoUpdateMCPServer(t, "full")
		defer cleanup()
		resp, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{"tool": "todos_delete", "input": "malformed"})
		assertTodoDeleteMCPLegacyError(t, resp, out, http.StatusForbidden, "CAPABILITY_UNAVAILABLE", "todos_delete is unavailable before bootstrap")
	})

	t.Run("authentication gate precedes malformed input", func(t *testing.T) {
		ts, _, st, cleanup := newTodoUpdateMCPServer(t, "full")
		defer cleanup()
		bootstrapTodoDeleteMCPOwner(t, st, "owner-auth-gate@example.com")
		resp, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{"tool": "todos_delete", "input": "malformed"})
		assertTodoDeleteMCPLegacyError(t, resp, out, http.StatusUnauthorized, "AUTH_REQUIRED", "Sign-in required for this tool")
	})

	t.Run("authenticated validation precedes project lookup", func(t *testing.T) {
		ts, _, st, cleanup := newTodoUpdateMCPServer(t, "full")
		defer cleanup()
		owner, _ := bootstrapTodoDeleteMCPOwner(t, st, "owner-validation@example.com")
		client := todoDeleteMCPSessionClient(t, ts.URL, ts.Client().Transport, st, owner.ID)
		cases := []struct {
			name      string
			input     map[string]any
			wantMsg   string
			wantField string
		}{
			{name: "missing projectSlug", input: map[string]any{"localId": 1}, wantMsg: "missing projectSlug", wantField: "projectSlug"},
			{name: "non-positive localId", input: map[string]any{"projectSlug": "missing-project", "localId": 0}, wantMsg: "invalid localId", wantField: "localId"},
			{name: "invalid localId masks missing project", input: map[string]any{"projectSlug": "missing-project", "localId": -1}, wantMsg: "invalid localId", wantField: "localId"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				resp, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{"tool": "todos_delete", "input": tc.input})
				publicError := assertTodoDeleteMCPLegacyError(t, resp, out, http.StatusBadRequest, "VALIDATION_ERROR", tc.wantMsg)
				details := publicError["details"].(map[string]any)
				if !reflect.DeepEqual(details, map[string]any{"field": tc.wantField}) {
					t.Fatalf("validation details=%+v want field %q", details, tc.wantField)
				}
			})
		}
	})
}
