package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

func callToolOverBothTransports(
	t *testing.T,
	client *http.Client,
	baseURL string,
	toolName string,
	input map[string]any,
	metadataKeys ...string,
) (map[string]any, map[string]any) {
	t.Helper()

	legacyResp, legacy := doMCP(t, client, baseURL+"/mcp", map[string]any{
		"tool":  toolName,
		"input": input,
	})
	if legacyResp.StatusCode != http.StatusOK || legacy["ok"] != true {
		t.Fatalf("legacy %s failed: status=%d body=%#v", toolName, legacyResp.StatusCode, legacy)
	}
	legacyData, ok := legacy["data"].(map[string]any)
	if !ok {
		t.Fatalf("legacy %s data type = %T, want object", toolName, legacy["data"])
	}
	legacyMeta, ok := legacy["meta"].(map[string]any)
	if !ok {
		t.Fatalf("legacy %s meta type = %T, want object", toolName, legacy["meta"])
	}
	if len(legacyMeta) != len(metadataKeys) {
		t.Fatalf("legacy %s meta = %#v, want exactly keys %v", toolName, legacyMeta, metadataKeys)
	}

	expectedStructured := make(map[string]any, len(legacyData)+len(metadataKeys))
	for key, value := range legacyData {
		expectedStructured[key] = value
	}
	for _, key := range metadataKeys {
		value, exists := legacyMeta[key]
		if !exists {
			t.Fatalf("legacy %s meta missing %q: %#v", toolName, key, legacyMeta)
		}
		if _, collision := expectedStructured[key]; collision {
			t.Fatalf("legacy %s data unexpectedly owns metadata key %q", toolName, key)
		}
		expectedStructured[key] = value
	}

	rpcResp, rpc := doJSONRPC(t, client, baseURL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": input,
		},
	})
	if rpcResp.StatusCode != http.StatusOK || rpc["error"] != nil {
		t.Fatalf("JSON-RPC %s failed: status=%d body=%#v", toolName, rpcResp.StatusCode, rpc)
	}
	result, ok := rpc["result"].(map[string]any)
	if !ok {
		t.Fatalf("JSON-RPC %s result type = %T, want object", toolName, rpc["result"])
	}
	if len(result) != 2 {
		t.Fatalf("JSON-RPC %s result = %#v, want only content and structuredContent", toolName, result)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("JSON-RPC %s structuredContent type = %T, want object", toolName, result["structuredContent"])
	}
	if !reflect.DeepEqual(structured, expectedStructured) {
		t.Fatalf("JSON-RPC %s structuredContent = %#v, want legacy data plus approved meta %#v", toolName, structured, expectedStructured)
	}

	content, ok := result["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("JSON-RPC %s content = %#v, want one text block", toolName, result["content"])
	}
	textBlock, ok := content[0].(map[string]any)
	if !ok || textBlock["type"] != "text" {
		t.Fatalf("JSON-RPC %s content block = %#v, want text block", toolName, content[0])
	}
	var textPayload map[string]any
	text, ok := textBlock["text"].(string)
	if !ok {
		t.Fatalf("JSON-RPC %s text type = %T, want string", toolName, textBlock["text"])
	}
	if err := json.Unmarshal([]byte(text), &textPayload); err != nil {
		t.Fatalf("decode JSON-RPC %s text payload: %v", toolName, err)
	}
	if !reflect.DeepEqual(textPayload, structured) {
		t.Fatalf("JSON-RPC %s text payload = %#v, want structuredContent %#v", toolName, textPayload, structured)
	}

	return legacyData, legacyMeta
}

func TestJSONRPCSystemCapabilitiesExposesLegacyAdapterVersion(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	data, meta := callToolOverBothTransports(
		t,
		ts.Client(),
		ts.URL,
		"system_getCapabilities",
		map[string]any{},
		"adapterVersion",
	)

	if data["serverMode"] != "anonymous" {
		t.Fatalf("serverMode = %#v, want anonymous", data["serverMode"])
	}
	if meta["adapterVersion"] != float64(1) {
		t.Fatalf("adapterVersion = %#v, want 1", meta["adapterVersion"])
	}
}

func TestJSONRPCSprintsListExposesLegacyUnscheduledCount(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	var project map[string]any
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "JSON-RPC Sprint Metadata",
	}, &project)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := project["slug"].(string)
	projectID := projectIDBySlug(t, sqlDB, slug)
	ctx := store.WithUserID(context.Background(), firstUserID(t, sqlDB))
	if _, err := store.New(sqlDB, nil).CreateTodo(
		ctx,
		projectID,
		store.CreateTodoInput{Title: "Unscheduled", ColumnKey: store.DefaultColumnBacklog},
		store.ModeFull,
	); err != nil {
		t.Fatalf("create unscheduled todo: %v", err)
	}

	data, meta := callToolOverBothTransports(
		t,
		client,
		ts.URL,
		"sprints_list",
		map[string]any{"projectSlug": slug},
		"unscheduledCount",
	)

	if items, ok := data["items"].([]any); !ok || len(items) != 0 {
		t.Fatalf("sprint items = %#v, want empty array", data["items"])
	}
	if meta["unscheduledCount"] != float64(1) {
		t.Fatalf("unscheduledCount = %#v, want 1", meta["unscheduledCount"])
	}
}

func TestJSONRPCDashboardListTodosExposesLegacyPagination(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	ctx := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, nil)
	project, err := st.CreateProject(ctx, "JSON-RPC Dashboard Metadata")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := st.CreateTodo(ctx, project.ID, store.CreateTodoInput{
			Title:          "Assigned todo",
			ColumnKey:      store.DefaultColumnDoing,
			AssigneeUserID: &ownerID,
		}, store.ModeFull); err != nil {
			t.Fatalf("create assigned todo %d: %v", i, err)
		}
	}

	firstData, firstMeta := callToolOverBothTransports(
		t,
		client,
		ts.URL,
		"dashboard_listTodos",
		map[string]any{"limit": 1, "sort": "board"},
		"nextCursor",
		"hasMore",
	)
	if items := firstData["items"].([]any); len(items) != 1 {
		t.Fatalf("first page items = %d, want 1", len(items))
	}
	if firstMeta["hasMore"] != true || firstMeta["nextCursor"] == nil {
		t.Fatalf("first page meta = %#v, want non-final page", firstMeta)
	}

	secondData, secondMeta := callToolOverBothTransports(
		t,
		client,
		ts.URL,
		"dashboard_listTodos",
		map[string]any{
			"limit":  1,
			"sort":   "board",
			"cursor": firstMeta["nextCursor"],
		},
		"nextCursor",
		"hasMore",
	)
	if items := secondData["items"].([]any); len(items) != 1 {
		t.Fatalf("middle page items = %d, want 1", len(items))
	}
	if secondMeta["hasMore"] != true || secondMeta["nextCursor"] == nil {
		t.Fatalf("middle page meta = %#v, want non-final page", secondMeta)
	}

	finalData, finalMeta := callToolOverBothTransports(
		t,
		client,
		ts.URL,
		"dashboard_listTodos",
		map[string]any{
			"limit":  10,
			"sort":   "board",
			"cursor": secondMeta["nextCursor"],
		},
		"nextCursor",
		"hasMore",
	)
	if items := finalData["items"].([]any); len(items) != 1 {
		t.Fatalf("final page items = %d, want 1", len(items))
	}
	if finalMeta["hasMore"] != false || finalMeta["nextCursor"] != nil {
		t.Fatalf("final page meta = %#v, want completed pagination", finalMeta)
	}
}
