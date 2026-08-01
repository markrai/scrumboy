package mcp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func todoCreateMCPArgs(projectSlug, title string) map[string]any {
	return map[string]any{
		"projectSlug": projectSlug,
		"title":       title,
		"body":        "created through MCP",
		"tags":        []string{"create-contract"},
	}
}

func callTodoCreateMCP(t *testing.T, client *http.Client, baseURL, transport, tool string, args map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	return callTodoUpdateMCP(t, client, baseURL, transport, tool, args)
}

func assertTodoCreateMCPSuccess(t *testing.T, transport string, response map[string]any, projectSlug string, localID int64) map[string]any {
	t.Helper()
	todo := assertTodoUpdateMCPSuccess(t, transport, response, projectSlug, localID)
	if _, ok := todo["id"]; ok {
		t.Fatalf("MCP create exposed global todo id: %+v", todo)
	}
	return todo
}

func createTodoCreateMCPProject(t *testing.T, st *store.Store, ownerID int64, name string) (store.Project, context.Context) {
	t.Helper()
	ctx := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctx, name)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return project, ctx
}

func createTodoCreateMCPFixture(t *testing.T, st *store.Store, ctx context.Context, projectID int64, title, columnKey string) store.Todo {
	t.Helper()
	todo, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{Title: title, ColumnKey: columnKey}, store.ModeFull)
	if err != nil {
		t.Fatalf("create todo fixture: %v", err)
	}
	return todo
}

func todoCreateMCPAuditCount(t *testing.T, db *sql.DB, todoID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM audit_events
		WHERE action = 'todo_created' AND target_type = 'todo' AND target_id = ?
	`, todoID).Scan(&count); err != nil {
		t.Fatalf("count MCP todo_created audits: %v", err)
	}
	return count
}

func todoCreateMCPProjectAuditCount(t *testing.T, db *sql.DB, projectID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action = 'todo_created'`, projectID).Scan(&count); err != nil {
		t.Fatalf("count MCP project create audits: %v", err)
	}
	return count
}

func todoCreateMCPAssignmentCount(t *testing.T, db *sql.DB, todoID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM todo_assignee_events WHERE todo_id = ?`, todoID).Scan(&count); err != nil {
		t.Fatalf("count MCP create assignment rows: %v", err)
	}
	return count
}

func assertTodoCreateMCPLegacyError(t *testing.T, response map[string]any, wantCode, wantMessage string) map[string]any {
	t.Helper()
	if got, want := sortedMapKeys(response), []string{"error", "ok"}; !reflect.DeepEqual(got, want) || response["ok"] != false {
		t.Fatalf("legacy error envelope=%+v keys=%v want=%v", response, got, want)
	}
	errorBody := response["error"].(map[string]any)
	if errorBody["code"] != wantCode || errorBody["message"] != wantMessage {
		t.Fatalf("legacy error=%+v want code=%q message=%q", errorBody, wantCode, wantMessage)
	}
	return errorBody
}

func assertTodoCreateMCPJSONRPCError(t *testing.T, response map[string]any, wantCode, wantMessage string) map[string]any {
	t.Helper()
	if got, want := sortedMapKeys(response), []string{"id", "jsonrpc", "result"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON-RPC error envelope keys=%v want=%v response=%+v", got, want, response)
	}
	result := response["result"].(map[string]any)
	if got, want := sortedMapKeys(result), []string{"content", "isError", "structuredContent"}; !reflect.DeepEqual(got, want) || result["isError"] != true {
		t.Fatalf("JSON-RPC tool error result=%+v", result)
	}
	structured := result["structuredContent"].(map[string]any)
	if structured["code"] != wantCode || structured["message"] != wantMessage {
		t.Fatalf("JSON-RPC structured error=%+v want code=%q message=%q", structured, wantCode, wantMessage)
	}
	content := result["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != wantMessage {
		t.Fatalf("JSON-RPC text error=%+v want=%q", content, wantMessage)
	}
	return structured
}

func TestTodoCreateMCPTransportAliasMatrix(t *testing.T) {
	ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
	defer cleanup()
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, db)

	for index, tc := range []struct {
		tool      string
		transport string
	}{
		{tool: "todos_create", transport: "legacy"},
		{tool: "todos.create", transport: "legacy"},
		{tool: "todos_create", transport: "jsonrpc"},
		{tool: "todos.create", transport: "jsonrpc"},
	} {
		t.Run(tc.transport+"/"+tc.tool, func(t *testing.T) {
			project, ctx := createTodoCreateMCPProject(t, st, ownerID, "MCP create matrix "+tc.transport+" "+tc.tool)
			title := "created by " + tc.tool
			resp, out := callTodoCreateMCP(t, client, ts.URL, tc.transport, tc.tool, todoCreateMCPArgs(project.Slug, title))
			if tc.transport == "legacy" && resp.StatusCode != http.StatusOK {
				t.Fatalf("legacy status=%d response=%+v", resp.StatusCode, out)
			}
			if tc.transport == "jsonrpc" && resp.StatusCode != http.StatusOK {
				t.Fatalf("JSON-RPC status=%d response=%+v", resp.StatusCode, out)
			}
			returned := assertTodoCreateMCPSuccess(t, tc.transport, out, project.Slug, 1)
			if returned["title"] != title || returned["columnKey"] != store.DefaultColumnBacklog {
				t.Fatalf("matrix result=%+v", returned)
			}
			persisted, err := st.GetTodoByLocalID(ctx, project.ID, 1, store.ModeFull)
			if err != nil {
				t.Fatalf("read created todo: %v", err)
			}
			if persisted.Title != title || todoCreateMCPAuditCount(t, db, persisted.ID) != 1 {
				t.Fatalf("matrix persistence=%+v audits=%d index=%d", persisted, todoCreateMCPAuditCount(t, db, persisted.ID), index)
			}
		})
	}

	t.Run("JSON-RPC dotted alias preserves tool-error envelope", func(t *testing.T) {
		resp, out := callTodoCreateMCP(t, client, ts.URL, "jsonrpc", "todos.create", todoCreateMCPArgs("missing-project", "never created"))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("JSON-RPC tool error HTTP status=%d", resp.StatusCode)
		}
		assertTodoCreateMCPJSONRPCError(t, out, "NOT_FOUND", "not found")
	})
}

func TestTodoCreateMCPRealtimeContracts(t *testing.T) {
	ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
	defer cleanup()
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, db)
	project, ownerCtx := createTodoCreateMCPProject(t, st, ownerID, "MCP create realtime")
	stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
	defer stream.close()

	t.Run("unassigned create is realtime silent", func(t *testing.T) {
		resp, out := callTodoCreateMCP(t, client, ts.URL, "legacy", "todos_create", todoCreateMCPArgs(project.Slug, "MCP unassigned create"))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoCreateMCPSuccess(t, "legacy", out, project.Slug, 1)
		persisted, err := st.GetTodoByLocalID(ownerCtx, project.ID, 1, store.ModeFull)
		if err != nil {
			t.Fatalf("read unassigned create: %v", err)
		}
		if todoCreateMCPAuditCount(t, db, persisted.ID) != 1 || todoCreateMCPAssignmentCount(t, db, persisted.ID) != 0 {
			t.Fatalf("unassigned effects audits=%d ledger=%d", todoCreateMCPAuditCount(t, db, persisted.ID), todoCreateMCPAssignmentCount(t, db, persisted.ID))
		}
		assertNoTodoUpdateMCPEvents(t, collectTodoUpdateMCPEvents(t, stream))
	})

	t.Run("assigned create uses the production store callback exactly once", func(t *testing.T) {
		assignee, err := st.CreateUser(context.Background(), "mcp-create-assignee@example.com", "password123", "Assignee")
		if err != nil {
			t.Fatalf("create assignee: %v", err)
		}
		if err := st.AddProjectMember(ownerCtx, ownerID, project.ID, assignee.ID, store.RoleViewer); err != nil {
			t.Fatalf("add assignee: %v", err)
		}
		args := todoCreateMCPArgs(project.Slug, "MCP assigned create")
		args["assigneeUserId"] = assignee.ID
		resp, out := callTodoCreateMCP(t, client, ts.URL, "legacy", "todos_create", args)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoCreateMCPSuccess(t, "legacy", out, project.Slug, 2)
		persisted, err := st.GetTodoByLocalID(ownerCtx, project.ID, 2, store.ModeFull)
		if err != nil {
			t.Fatalf("read assigned create: %v", err)
		}
		events := collectTodoUpdateMCPEvents(t, stream)
		assertTodoUpdateMCPAssignmentEvents(t, events, project, persisted, assignee.ID, ownerID)
		for _, event := range events {
			if event.Type == "refresh_needed" && event.Reason == "todo_created" {
				t.Fatalf("assigned MCP create emitted todo_created: %+v", events)
			}
		}
		if todoCreateMCPAuditCount(t, db, persisted.ID) != 1 {
			t.Fatalf("assigned create audit count=%d", todoCreateMCPAuditCount(t, db, persisted.ID))
		}
		// The current create path publishes an assignment event but does not
		// create an initial todo_assignee_events ledger row.
		if todoCreateMCPAssignmentCount(t, db, persisted.ID) != 0 {
			t.Fatalf("assigned create ledger count=%d want=0", todoCreateMCPAssignmentCount(t, db, persisted.ID))
		}
	})

	t.Run("store validation failure is realtime silent", func(t *testing.T) {
		auditsBefore := todoCreateMCPProjectAuditCount(t, db, project.ID)
		resp, out := callTodoCreateMCP(t, client, ts.URL, "legacy", "todos_create", todoCreateMCPArgs(project.Slug, ""))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoCreateMCPLegacyError(t, out, "VALIDATION_ERROR", "validation: invalid title")
		assertNoTodoUpdateMCPEvents(t, collectTodoUpdateMCPEvents(t, stream))
		if after := todoCreateMCPProjectAuditCount(t, db, project.ID); after != auditsBefore {
			t.Fatalf("failed create audits %d->%d", auditsBefore, after)
		}
	})
}

func TestTodoCreateMCPPositionContracts(t *testing.T) {
	ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
	defer cleanup()
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, db)

	t.Run("after local ID resolves in target project", func(t *testing.T) {
		project, ctx := createTodoCreateMCPProject(t, st, ownerID, "MCP create after")
		_ = createTodoCreateMCPFixture(t, st, ctx, project.ID, "first", store.DefaultColumnBacklog)
		anchor := createTodoCreateMCPFixture(t, st, ctx, project.ID, "last", store.DefaultColumnBacklog)
		args := todoCreateMCPArgs(project.Slug, "after last")
		args["position"] = map[string]any{"afterLocalId": anchor.LocalID}
		resp, out := callTodoCreateMCP(t, client, ts.URL, "legacy", "todos_create", args)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoCreateMCPSuccess(t, "legacy", out, project.Slug, 3)
		persisted, err := st.GetTodoByLocalID(ctx, project.ID, 3, store.ModeFull)
		if err != nil {
			t.Fatalf("read after-position create: %v", err)
		}
		if persisted.Rank <= anchor.Rank {
			t.Fatalf("after rank=%d anchor=%d", persisted.Rank, anchor.Rank)
		}
	})

	t.Run("before local ID resolves in target project", func(t *testing.T) {
		project, ctx := createTodoCreateMCPProject(t, st, ownerID, "MCP create before")
		anchor := createTodoCreateMCPFixture(t, st, ctx, project.ID, "first", store.DefaultColumnBacklog)
		args := todoCreateMCPArgs(project.Slug, "before first")
		args["position"] = map[string]any{"beforeLocalId": anchor.LocalID}
		resp, out := callTodoCreateMCP(t, client, ts.URL, "legacy", "todos_create", args)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoCreateMCPSuccess(t, "legacy", out, project.Slug, 2)
		persisted, err := st.GetTodoByLocalID(ctx, project.ID, 2, store.ModeFull)
		if err != nil {
			t.Fatalf("read before-position create: %v", err)
		}
		if persisted.Rank >= anchor.Rank {
			t.Fatalf("before rank=%d anchor=%d", persisted.Rank, anchor.Rank)
		}
	})

	t.Run("two local anchors preserve order and reversed anchors conflict", func(t *testing.T) {
		project, ctx := createTodoCreateMCPProject(t, st, ownerID, "MCP create two anchors")
		after := createTodoCreateMCPFixture(t, st, ctx, project.ID, "after", store.DefaultColumnBacklog)
		before := createTodoCreateMCPFixture(t, st, ctx, project.ID, "before", store.DefaultColumnBacklog)
		args := todoCreateMCPArgs(project.Slug, "between")
		args["position"] = map[string]any{"afterLocalId": after.LocalID, "beforeLocalId": before.LocalID}
		resp, out := callTodoCreateMCP(t, client, ts.URL, "jsonrpc", "todos_create", args)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoCreateMCPSuccess(t, "jsonrpc", out, project.Slug, 3)
		persisted, err := st.GetTodoByLocalID(ctx, project.ID, 3, store.ModeFull)
		if err != nil {
			t.Fatalf("read two-anchor create: %v", err)
		}
		if persisted.Rank <= after.Rank || persisted.Rank >= before.Rank {
			t.Fatalf("between rank=%d want %d < rank < %d", persisted.Rank, after.Rank, before.Rank)
		}

		args = todoCreateMCPArgs(project.Slug, "reversed")
		args["position"] = map[string]any{"afterLocalId": before.LocalID, "beforeLocalId": after.LocalID}
		resp, out = callTodoCreateMCP(t, client, ts.URL, "jsonrpc", "todos.create", args)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("JSON-RPC error HTTP status=%d", resp.StatusCode)
		}
		structured := assertTodoCreateMCPJSONRPCError(t, out, "CONFLICT", "conflict: afterId must come before beforeId")
		if _, ok := structured["status"]; ok {
			t.Fatalf("JSON-RPC error exposed HTTP status: %+v", structured)
		}
	})

	t.Run("local anchor resolution is scoped to the target project", func(t *testing.T) {
		foreign, foreignCtx := createTodoCreateMCPProject(t, st, ownerID, "MCP create foreign anchor")
		_ = createTodoCreateMCPFixture(t, st, foreignCtx, foreign.ID, "foreign one", store.DefaultColumnBacklog)
		foreignTwo := createTodoCreateMCPFixture(t, st, foreignCtx, foreign.ID, "foreign two", store.DefaultColumnBacklog)
		project, ctx := createTodoCreateMCPProject(t, st, ownerID, "MCP create target anchor")
		_ = createTodoCreateMCPFixture(t, st, ctx, project.ID, "target one", store.DefaultColumnBacklog)

		args := todoCreateMCPArgs(project.Slug, "must not use foreign local ID")
		args["position"] = map[string]any{"afterLocalId": foreignTwo.LocalID}
		resp, out := callTodoCreateMCP(t, client, ts.URL, "legacy", "todos_create", args)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		errorBody := assertTodoCreateMCPLegacyError(t, out, "VALIDATION_ERROR", "invalid local todo reference")
		details := errorBody["details"].(map[string]any)
		if details["field"] != "afterLocalId" || int64(details["localId"].(float64)) != foreignTwo.LocalID {
			t.Fatalf("target-scoped error details=%+v", details)
		}
	})

	t.Run("wrong column and after-before lookup precedence", func(t *testing.T) {
		project, ctx := createTodoCreateMCPProject(t, st, ownerID, "MCP create invalid anchors")
		doing := createTodoCreateMCPFixture(t, st, ctx, project.ID, "doing", store.DefaultColumnDoing)
		args := todoCreateMCPArgs(project.Slug, "wrong column")
		args["position"] = map[string]any{"afterLocalId": doing.LocalID}
		resp, out := callTodoCreateMCP(t, client, ts.URL, "legacy", "todos_create", args)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		errorBody := assertTodoCreateMCPLegacyError(t, out, "VALIDATION_ERROR", "position reference must be in target column")
		if errorBody["details"].(map[string]any)["field"] != "afterLocalId" {
			t.Fatalf("wrong-column details=%+v", errorBody)
		}

		args = todoCreateMCPArgs(project.Slug, "nonpositive anchor")
		args["position"] = map[string]any{"afterLocalId": int64(0)}
		resp, out = callTodoCreateMCP(t, client, ts.URL, "legacy", "todos_create", args)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		errorBody = assertTodoCreateMCPLegacyError(t, out, "VALIDATION_ERROR", "invalid local todo reference")
		details := errorBody["details"].(map[string]any)
		if details["field"] != "afterLocalId" {
			t.Fatalf("nonpositive-anchor details=%+v", details)
		}
		if _, ok := details["localId"]; ok {
			t.Fatalf("nonpositive-anchor unexpectedly includes localId: %+v", details)
		}

		args = todoCreateMCPArgs(project.Slug, "after fails first")
		args["position"] = map[string]any{"afterLocalId": int64(999), "beforeLocalId": int64(0)}
		resp, out = callTodoCreateMCP(t, client, ts.URL, "legacy", "todos_create", args)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		errorBody = assertTodoCreateMCPLegacyError(t, out, "VALIDATION_ERROR", "invalid local todo reference")
		details = errorBody["details"].(map[string]any)
		if details["field"] != "afterLocalId" || int64(details["localId"].(float64)) != 999 {
			t.Fatalf("lookup precedence details=%+v", details)
		}
	})
}

func TestTodoCreateMCPPrecedenceRoleAndModeContracts(t *testing.T) {
	t.Run("authentication envelope access and anchor precedence", func(t *testing.T) {
		ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
		defer cleanup()
		client := newCookieClient(t, ts)
		bootstrapUser(t, client, ts.URL)
		ownerID := firstUserID(t, db)
		project, _ := createTodoCreateMCPProject(t, st, ownerID, "MCP create precedence")

		resp, out := callTodoCreateMCP(t, newStatelessClient(ts), ts.URL, "legacy", "todos_create", todoCreateMCPArgs(project.Slug, "never"))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("auth status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoCreateMCPLegacyError(t, out, "AUTH_REQUIRED", "Sign-in required for this tool")

		resp, out = callTodoCreateMCP(t, client, ts.URL, "legacy", "todos_create", map[string]any{})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("envelope status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoCreateMCPLegacyError(t, out, "VALIDATION_ERROR", "missing projectSlug")

		// JSON-RPC applies the catalog's required title before dispatch. The
		// legacy /mcp endpoint has no equivalent schema gate and reaches slug
		// access with an empty decoded title.
		resp, out = callTodoCreateMCP(t, client, ts.URL, "jsonrpc", "todos_create", map[string]any{"projectSlug": "missing-project"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("JSON-RPC validation HTTP status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoCreateMCPJSONRPCError(t, out, "VALIDATION_ERROR", "missing required field: title")

		resp, out = callTodoCreateMCP(t, client, ts.URL, "legacy", "todos_create", map[string]any{"projectSlug": "missing-project"})
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("legacy missing-title/access status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoCreateMCPLegacyError(t, out, "NOT_FOUND", "not found")

		args := todoCreateMCPArgs("missing-project", "access first")
		args["columnKey"] = "DONE"
		args["position"] = map[string]any{"afterLocalId": int64(0)}
		resp, out = callTodoCreateMCP(t, client, ts.URL, "legacy", "todos_create", args)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("access status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoCreateMCPLegacyError(t, out, "NOT_FOUND", "not found")

		temporary, err := st.CreateAnonymousBoard(store.WithUserID(context.Background(), ownerID))
		if err != nil {
			t.Fatalf("create Temporary Board: %v", err)
		}
		if _, err := db.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Hour).UnixMilli(), temporary.ID); err != nil {
			t.Fatalf("expire Temporary Board: %v", err)
		}
		args = todoCreateMCPArgs(temporary.Slug, "expired access first")
		args["position"] = map[string]any{"afterLocalId": int64(0)}
		resp, out = callTodoCreateMCP(t, client, ts.URL, "legacy", "todos_create", args)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expired access status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoCreateMCPLegacyError(t, out, "NOT_FOUND", "not found")

		args = todoCreateMCPArgs(project.Slug, "")
		args["position"] = map[string]any{"afterLocalId": int64(999)}
		resp, out = callTodoCreateMCP(t, client, ts.URL, "legacy", "todos_create", args)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("anchor/store status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoCreateMCPLegacyError(t, out, "VALIDATION_ERROR", "invalid local todo reference")

		resp, out = callTodoCreateMCP(t, client, ts.URL, "legacy", "todos_create", todoCreateMCPArgs(project.Slug, ""))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("store validation status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoCreateMCPLegacyError(t, out, "VALIDATION_ERROR", "validation: invalid title")
		if got := todoCreateMCPProjectAuditCount(t, db, project.ID); got != 0 {
			t.Fatalf("precedence failures created audits=%d", got)
		}
	})

	t.Run("durable roles", func(t *testing.T) {
		ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
		defer cleanup()
		ownerClient := newCookieClient(t, ts)
		bootstrapUser(t, ownerClient, ts.URL)
		ownerID := firstUserID(t, db)
		project, ownerCtx := createTodoCreateMCPProject(t, st, ownerID, "MCP create roles")
		maintainer, err := st.CreateUser(context.Background(), "mcp-create-maintainer@example.com", "password123", "Maintainer")
		if err != nil {
			t.Fatalf("create maintainer: %v", err)
		}
		contributor, err := st.CreateUser(context.Background(), "mcp-create-contributor@example.com", "password123", "Contributor")
		if err != nil {
			t.Fatalf("create contributor: %v", err)
		}
		viewer, err := st.CreateUser(context.Background(), "mcp-create-viewer@example.com", "password123", "Viewer")
		if err != nil {
			t.Fatalf("create viewer: %v", err)
		}
		for id, role := range map[int64]store.ProjectRole{maintainer.ID: store.RoleMaintainer, contributor.ID: store.RoleContributor, viewer.ID: store.RoleViewer} {
			if err := st.AddProjectMember(ownerCtx, ownerID, project.ID, id, role); err != nil {
				t.Fatalf("add member %d: %v", id, err)
			}
		}
		maintainerClient := loginTodoUpdateMCPUser(t, ts, maintainer.Email, "password123")
		contributorClient := loginTodoUpdateMCPUser(t, ts, contributor.Email, "password123")
		viewerClient := loginTodoUpdateMCPUser(t, ts, viewer.Email, "password123")

		resp, out := callTodoCreateMCP(t, maintainerClient, ts.URL, "legacy", "todos_create", todoCreateMCPArgs(project.Slug, "maintainer create"))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("maintainer status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoCreateMCPSuccess(t, "legacy", out, project.Slug, 1)

		for name, blocked := range map[string]struct {
			client     *http.Client
			wantStatus int
			wantCode   string
		}{
			"contributor": {client: contributorClient, wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN"},
			"viewer":      {client: viewerClient, wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND"},
		} {
			t.Run(name, func(t *testing.T) {
				resp, out := callTodoCreateMCP(t, blocked.client, ts.URL, "legacy", "todos_create", todoCreateMCPArgs(project.Slug, name+" blocked"))
				if resp.StatusCode != blocked.wantStatus {
					t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
				}
				message := "forbidden"
				if blocked.wantCode == "NOT_FOUND" {
					message = "not found"
				}
				assertTodoCreateMCPLegacyError(t, out, blocked.wantCode, message)
			})
		}
	})

	t.Run("active Temporary Board authenticated link holder", func(t *testing.T) {
		ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
		defer cleanup()
		ownerClient := newCookieClient(t, ts)
		bootstrapUser(t, ownerClient, ts.URL)
		ownerID := firstUserID(t, db)
		outsider, err := st.CreateUser(context.Background(), "mcp-create-temp-outsider@example.com", "password123", "Outsider")
		if err != nil {
			t.Fatalf("create outsider: %v", err)
		}
		outsiderClient := loginTodoUpdateMCPUser(t, ts, outsider.Email, "password123")
		project, err := st.CreateAnonymousBoard(store.WithUserID(context.Background(), ownerID))
		if err != nil {
			t.Fatalf("create Temporary Board: %v", err)
		}
		resp, out := callTodoCreateMCP(t, outsiderClient, ts.URL, "legacy", "todos_create", todoCreateMCPArgs(project.Slug, "Temporary link-holder create"))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Temporary Board status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoCreateMCPSuccess(t, "legacy", out, project.Slug, 1)
	})

	t.Run("Anonymous Mode capability precedes input", func(t *testing.T) {
		ts, _, _, cleanup := newTodoUpdateMCPServer(t, "anonymous")
		defer cleanup()
		resp, out := callTodoCreateMCP(t, newStatelessClient(ts), ts.URL, "legacy", "todos_create", map[string]any{})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		assertTodoCreateMCPLegacyError(t, out, "CAPABILITY_UNAVAILABLE", "todos_create is unavailable in anonymous mode")
	})
}

func TestTodoCreateMCPLaneAndFieldProjectionContracts(t *testing.T) {
	ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
	defer cleanup()
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, db)
	project, ctx := createTodoCreateMCPProject(t, st, ownerID, "MCP create lane projection")
	custom, err := st.AddWorkflowColumn(ctx, project.ID, "Review")
	if err != nil {
		t.Fatalf("add custom lane: %v", err)
	}
	now := time.Now().UTC()
	sprint, err := st.CreateSprint(ctx, project.ID, "MCP create sprint", now, now.Add(7*24*time.Hour))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}

	resp, out := callTodoCreateMCP(t, client, ts.URL, "legacy", "todos_create", map[string]any{
		"projectSlug": project.Slug,
		"title":       "default lane",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("default status=%d response=%+v", resp.StatusCode, out)
	}
	created := assertTodoCreateMCPSuccess(t, "legacy", out, project.Slug, 1)
	if created["columnKey"] != store.DefaultColumnBacklog {
		t.Fatalf("default lane result=%+v", created)
	}

	points := int64(8)
	args := map[string]any{
		"projectSlug":      project.Slug,
		"title":            "  projected title  ",
		"body":             "projected body",
		"tags":             []string{"Alpha", "Beta"},
		"columnKey":        custom.Key,
		"estimationPoints": points,
		"sprintId":         sprint.ID,
	}
	resp, out = callTodoCreateMCP(t, client, ts.URL, "jsonrpc", "todos_create", args)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("custom status=%d response=%+v", resp.StatusCode, out)
	}
	created = assertTodoCreateMCPSuccess(t, "jsonrpc", out, project.Slug, 2)
	if created["title"] != "projected title" || created["body"] != "projected body" || created["columnKey"] != custom.Key {
		t.Fatalf("field projection=%+v", created)
	}
	if int64(created["estimationPoints"].(float64)) != points || int64(created["sprintId"].(float64)) != sprint.ID {
		t.Fatalf("nullable field projection=%+v", created)
	}
	tags := created["tags"].([]any)
	encodedTags, _ := json.Marshal(tags)
	if string(encodedTags) != `["alpha","beta"]` {
		t.Fatalf("tag projection=%s", encodedTags)
	}

	args = todoCreateMCPArgs(project.Slug, "done projection")
	args["columnKey"] = "DONE"
	resp, out = callTodoCreateMCP(t, client, ts.URL, "legacy", "todos_create", args)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("done status=%d response=%+v", resp.StatusCode, out)
	}
	created = assertTodoCreateMCPSuccess(t, "legacy", out, project.Slug, 3)
	if created["columnKey"] != store.DefaultColumnDone || created["doneAt"] == nil {
		t.Fatalf("done projection=%+v", created)
	}

	args = todoCreateMCPArgs("missing-project", "unknown status")
	args["status"] = "DONE"
	resp, out = callTodoCreateMCP(t, client, ts.URL, "legacy", "todos_create", args)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown status field=%d response=%+v", resp.StatusCode, out)
	}
	errorBody := assertTodoCreateMCPLegacyError(t, out, "VALIDATION_ERROR", "invalid input")
	if !strings.Contains(errorBody["details"].(map[string]any)["detail"].(string), "unknown field") {
		t.Fatalf("unknown status details=%+v", errorBody)
	}
}
