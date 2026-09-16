package mcp_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func newTodoArchiveMCPFixture(t *testing.T) (*httptest.Server, *sql.DB, *http.Client, *store.Store, context.Context, int64, store.Project, []store.Todo) {
	t.Helper()
	ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
	t.Cleanup(cleanup)
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	ctx := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctx, "MCP archival")
	if err != nil {
		t.Fatal(err)
	}
	todos := make([]store.Todo, 3)
	for i := range todos {
		todos[i] = createTodoUpdateMCPTodo(t, st, ctx, project.ID, fmt.Sprintf("Archived candidate %d", i+1), nil, nil)
	}
	return ts, sqlDB, client, st, ctx, ownerID, project, todos
}

func callTodoArchiveMCP(t *testing.T, client *http.Client, baseURL, tool, slug string, ids []int64) (*http.Response, map[string]any) {
	t.Helper()
	return doMCP(t, client, baseURL+"/mcp", map[string]any{
		"tool":  tool,
		"input": map[string]any{"projectSlug": slug, "localIds": ids},
	})
}

func requireTodoArchiveMCPSuccess(t *testing.T, resp *http.Response, out map[string]any) map[string]any {
	t.Helper()
	if resp.StatusCode != http.StatusOK || out["ok"] != true {
		t.Fatalf("MCP archival status=%d response=%+v", resp.StatusCode, out)
	}
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("MCP archival data type=%T response=%+v", out["data"], out)
	}
	return data
}

func TestTodoArchivalMCPLifecycleValidationAtomicityAndAuthorization(t *testing.T) {
	ts, _, client, st, ownerCtx, ownerID, project, todos := newTodoArchiveMCPFixture(t)
	baseURL := ts.URL
	ids := []int64{todos[1].LocalID, todos[0].LocalID}

	resp, out := callTodoArchiveMCP(t, client, baseURL, "todos_archive", project.Slug, ids)
	data := requireTodoArchiveMCPSuccess(t, resp, out)
	if data["targetState"] != "archived" || data["transitionedCount"] != float64(2) {
		t.Fatalf("archive result=%+v", data)
	}
	transitioned := data["transitionedLocalIds"].([]any)
	if transitioned[0] != float64(ids[0]) || transitioned[1] != float64(ids[1]) {
		t.Fatalf("archive result order=%v want=%v", transitioned, ids)
	}

	resp, out = callTodoArchiveMCP(t, client, baseURL, "todos_archive", project.Slug, ids)
	data = requireTodoArchiveMCPSuccess(t, resp, out)
	if data["transitionedCount"] != float64(0) || data["unchangedCount"] != float64(2) {
		t.Fatalf("idempotent archive result=%+v", data)
	}

	resp, out = callTodoArchiveMCP(t, client, baseURL, "todos_restore", project.Slug, ids)
	data = requireTodoArchiveMCPSuccess(t, resp, out)
	if data["targetState"] != "restored" || data["transitionedCount"] != float64(2) {
		t.Fatalf("restore result=%+v", data)
	}
	resp, out = callTodoArchiveMCP(t, client, baseURL, "todos_restore", project.Slug, ids)
	data = requireTodoArchiveMCPSuccess(t, resp, out)
	if data["transitionedCount"] != float64(0) || data["unchangedCount"] != float64(2) {
		t.Fatalf("idempotent restore result=%+v", data)
	}

	invalid := []struct {
		name string
		ids  []int64
	}{
		{name: "empty", ids: []int64{}},
		{name: "zero", ids: []int64{0}},
		{name: "negative", ids: []int64{-1}},
		{name: "duplicate", ids: []int64{todos[0].LocalID, todos[0].LocalID}},
	}
	many := make([]int64, 501)
	for i := range many {
		many[i] = int64(i + 1)
	}
	invalid = append(invalid, struct {
		name string
		ids  []int64
	}{name: "over maximum", ids: many})
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			resp, out := callTodoArchiveMCP(t, client, baseURL, "todos_archive", project.Slug, tc.ids)
			if resp.StatusCode != http.StatusBadRequest || out["error"].(map[string]any)["code"] != "VALIDATION_ERROR" {
				t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
			}
		})
	}

	// A missing member makes the whole request fail without archiving its valid member.
	resp, out = callTodoArchiveMCP(t, client, baseURL, "todos_archive", project.Slug, []int64{todos[0].LocalID, todos[2].LocalID + 999})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("atomic missing status=%d response=%+v", resp.StatusCode, out)
	}
	got, err := st.GetTodoByLocalID(ownerCtx, project.ID, todos[0].LocalID, store.ModeFull)
	if err != nil || got.ArchivedAt != nil {
		t.Fatalf("failed batch partially archived todo: %+v err=%v", got, err)
	}

	contributor, err := st.CreateUser(context.Background(), "archive-mcp-contributor@example.com", "password123", "Contributor")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectMember(ownerCtx, ownerID, project.ID, contributor.ID, store.RoleContributor); err != nil {
		t.Fatal(err)
	}
	contributorClient := loginTodoUpdateMCPUser(t, ts, contributor.Email, "password123")
	resp, out = callTodoArchiveMCP(t, contributorClient, baseURL, "todos_archive", project.Slug, []int64{todos[0].LocalID})
	if resp.StatusCode != http.StatusForbidden || out["error"].(map[string]any)["code"] != "FORBIDDEN" {
		t.Fatalf("contributor status=%d response=%+v", resp.StatusCode, out)
	}

	maintainer, err := st.CreateUser(context.Background(), "archive-mcp-maintainer@example.com", "password123", "Maintainer")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectMember(ownerCtx, ownerID, project.ID, maintainer.ID, store.RoleMaintainer); err != nil {
		t.Fatal(err)
	}
	maintainerClient := loginTodoUpdateMCPUser(t, ts, maintainer.Email, "password123")
	resp, out = callTodoArchiveMCP(t, maintainerClient, baseURL, "todos_archive", project.Slug, []int64{todos[0].LocalID})
	requireTodoArchiveMCPSuccess(t, resp, out)
}

func TestTodoArchivalMCPReadAndLinkContracts(t *testing.T) {
	ts, _, client, st, ctx, _, project, todos := newTodoArchiveMCPFixture(t)
	baseURL := ts.URL
	resp, out := doMCP(t, client, baseURL+"/mcp", map[string]any{"tool": "todos_get", "input": map[string]any{
		"projectSlug": project.Slug, "localId": todos[0].LocalID,
	}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("todos_get active status=%d response=%+v", resp.StatusCode, out)
	}
	active := out["data"].(map[string]any)["todo"].(map[string]any)
	if value, present := active["archivedAt"]; !present || value != nil {
		t.Fatalf("active MCP archivedAt present=%v value=%v", present, value)
	}
	if err := st.AddLink(ctx, project.ID, todos[1].LocalID, todos[0].LocalID, "blocks", store.ModeFull); err != nil {
		t.Fatal(err)
	}
	resp, out = callTodoArchiveMCP(t, client, baseURL, "todos_archive", project.Slug, []int64{todos[0].LocalID})
	requireTodoArchiveMCPSuccess(t, resp, out)

	resp, out = doMCP(t, client, baseURL+"/mcp", map[string]any{"tool": "todos_get", "input": map[string]any{
		"projectSlug": project.Slug, "localId": todos[0].LocalID,
	}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("todos_get archived status=%d response=%+v", resp.StatusCode, out)
	}
	archived := out["data"].(map[string]any)["todo"].(map[string]any)
	if archived["archivedAt"] == nil {
		t.Fatalf("todos_get archivedAt=%v", archived["archivedAt"])
	}

	resp, out = doMCP(t, client, baseURL+"/mcp", map[string]any{"tool": "todos_search", "input": map[string]any{
		"projectSlug": project.Slug, "query": "Archived candidate 1", "limit": 20, "excludeLocalIds": []int64{},
	}})
	if resp.StatusCode != http.StatusOK || len(out["data"].(map[string]any)["items"].([]any)) != 0 {
		t.Fatalf("todos_search leaked archived todo status=%d response=%+v", resp.StatusCode, out)
	}

	resp, out = doMCP(t, client, baseURL+"/mcp", map[string]any{"tool": "board_get", "input": map[string]any{"projectSlug": project.Slug}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("board_get status=%d response=%+v", resp.StatusCode, out)
	}
	for _, rawColumn := range out["data"].(map[string]any)["columns"].([]any) {
		for _, rawItem := range rawColumn.(map[string]any)["items"].([]any) {
			if rawItem.(map[string]any)["localId"] == float64(todos[0].LocalID) {
				t.Fatalf("board_get leaked archived todo: %+v", rawItem)
			}
		}
	}

	resp, out = doMCP(t, client, baseURL+"/mcp", map[string]any{"tool": "todos_linksList", "input": map[string]any{
		"projectSlug": project.Slug, "localId": todos[1].LocalID,
	}})
	links := todoLinkMCPData(t, "legacy", out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("links list status=%d response=%+v", resp.StatusCode, out)
	}
	items := links["outbound"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["archivedAt"] == nil {
		t.Fatalf("archived link target projection=%+v", items)
	}

	resp, out = doMCP(t, client, baseURL+"/mcp", map[string]any{"tool": "todos_linkRemove", "input": map[string]any{
		"projectSlug": project.Slug, "localId": todos[1].LocalID, "targetLocalId": todos[0].LocalID,
	}})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("archived link remove status=%d response=%+v", resp.StatusCode, out)
	}
}

func TestTodoArchivalMCPTemporaryProjectAndModeContracts(t *testing.T) {
	ts, sqlDB, client, st, ctx, _, _, _ := newTodoArchiveMCPFixture(t)
	baseURL := ts.URL
	p, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	todo := createTodoUpdateMCPTodo(t, st, ctx, p.ID, "Temporary archival", nil, nil)
	resp, out := callTodoArchiveMCP(t, client, baseURL, "todos_archive", p.Slug, []int64{todo.LocalID})
	requireTodoArchiveMCPSuccess(t, resp, out)
	if _, err := sqlDB.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Minute).UnixMilli(), p.ID); err != nil {
		t.Fatal(err)
	}
	resp, out = callTodoArchiveMCP(t, client, baseURL, "todos_restore", p.Slug, []int64{todo.LocalID})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expired temporary restore status=%d response=%+v", resp.StatusCode, out)
	}

	anonymousServer, _, _, anonymousCleanup := newTodoUpdateMCPServer(t, "anonymous")
	defer anonymousCleanup()
	resp, out = callTodoArchiveMCP(t, anonymousServer.Client(), anonymousServer.URL, "todos_archive", "any", []int64{1})
	if resp.StatusCode != http.StatusForbidden || out["error"].(map[string]any)["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("anonymous-mode archive status=%d response=%+v", resp.StatusCode, out)
	}
}
