package mcp_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"scrumboy/internal/store"
)

type boardGetTransportFixture struct {
	serverURL string
	client    *http.Client
	db        *sql.DB
	slug      string
	creatorID int64
}

func newBoardGetTransportFixture(t *testing.T) boardGetTransportFixture {
	t.Helper()
	ts, sqlDB, cleanup := newTestServer(t, "full")
	t.Cleanup(cleanup)

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Phase 7 Transport Board",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Phase 7 Transport Board")
	projectID := projectIDBySlug(t, sqlDB, slug)
	st := store.New(sqlDB, nil)
	creatorID := firstUserID(t, sqlDB)
	ctx := store.WithUserID(t.Context(), creatorID)
	for i := 0; i < 3; i++ {
		if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
			Title:     "Phase 7 paged todo",
			ColumnKey: store.DefaultColumnBacklog,
		}, store.ModeFull); err != nil {
			t.Fatalf("create todo %d: %v", i, err)
		}
	}
	return boardGetTransportFixture{
		serverURL: ts.URL,
		client:    client,
		db:        sqlDB,
		slug:      slug,
		creatorID: creatorID,
	}
}

func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func TestMCPBoardGetTransportContract_LegacySuccessEnvelope(t *testing.T) {
	fixture := newBoardGetTransportFixture(t)

	resp, out := doMCP(t, fixture.client, fixture.serverURL+"/mcp", map[string]any{
		"tool": "board_get",
		"input": map[string]any{
			"projectSlug": fixture.slug,
			"limit":       2,
		},
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body=%#v", resp.StatusCode, out)
	}
	if got, want := sortedMapKeys(out), []string{"data", "meta", "ok"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("top-level keys = %v, want %v", got, want)
	}
	if out["ok"] != true {
		t.Fatalf("ok = %#v, want true", out["ok"])
	}
	data := out["data"].(map[string]any)
	if got, want := sortedMapKeys(data), []string{"columns", "project"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("data keys = %v, want %v", got, want)
	}
	project := data["project"].(map[string]any)
	if got, want := sortedMapKeys(project), []string{"name", "projectSlug", "role"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("project keys = %v, want %v", got, want)
	}
	columns := data["columns"].([]any)
	backlog := boardColumnByKey(t, columns, store.DefaultColumnBacklog)
	if got, want := sortedMapKeys(backlog), []string{"isDone", "items", "key", "name"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("column keys = %v, want %v", got, want)
	}
	items := backlog["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("backlog items = %#v, want first page of 2", items)
	}
	if got, want := sortedMapKeys(items[0].(map[string]any)), []string{
		"assigneeUserId",
		"body",
		"columnKey",
		"createdAt",
		"createdByUserId",
		"doneAt",
		"estimationPoints",
		"localId",
		"priorityKey",
		"projectSlug",
		"sprintId",
		"tags",
		"title",
		"updatedAt",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("todo keys = %v, want %v", got, want)
	}
	if got := items[0].(map[string]any)["createdByUserId"]; got != float64(fixture.creatorID) {
		t.Fatalf("createdByUserId = %#v, want %d", got, fixture.creatorID)
	}
	meta := out["meta"].(map[string]any)
	if got, want := sortedMapKeys(meta), []string{"hasMoreByColumn", "nextCursorByColumn", "totalCountByColumn"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("meta keys = %v, want %v", got, want)
	}
	if meta["hasMoreByColumn"].(map[string]any)[store.DefaultColumnBacklog] != true {
		t.Fatalf("backlog hasMore = %#v", meta["hasMoreByColumn"])
	}
	if _, ok := meta["nextCursorByColumn"].(map[string]any)[store.DefaultColumnBacklog].(string); !ok {
		t.Fatalf("backlog cursor = %#v", meta["nextCursorByColumn"])
	}
	if meta["totalCountByColumn"].(map[string]any)[store.DefaultColumnBacklog] != float64(3) {
		t.Fatalf("backlog total = %#v", meta["totalCountByColumn"])
	}
}

func TestMCPBoardGetTransportContract_NullCreatorIsExplicitNull(t *testing.T) {
	fixture := newBoardGetTransportFixture(t)
	if _, err := fixture.db.Exec(`UPDATE todos SET created_by_user_id = NULL WHERE project_id = (SELECT id FROM projects WHERE slug = ?) AND local_id = 1`, fixture.slug); err != nil {
		t.Fatalf("clear creator attribution: %v", err)
	}

	resp, out := doMCP(t, fixture.client, fixture.serverURL+"/mcp", map[string]any{
		"tool": "board_get",
		"input": map[string]any{
			"projectSlug": fixture.slug,
			"limit":       3,
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body=%#v", resp.StatusCode, out)
	}
	items := boardColumnByKey(t, out["data"].(map[string]any)["columns"].([]any), store.DefaultColumnBacklog)["items"].([]any)
	var first map[string]any
	for _, item := range items {
		candidate := item.(map[string]any)
		if candidate["localId"] == float64(1) {
			first = candidate
			break
		}
	}
	if first == nil {
		t.Fatalf("localId 1 missing from items: %#v", items)
	}
	value, exists := first["createdByUserId"]
	if !exists || value != nil {
		t.Fatalf("createdByUserId = %#v (exists=%v), want explicit null", value, exists)
	}
}

func TestMCPBoardGetTransportContract_LegacyErrorEnvelope(t *testing.T) {
	fixture := newBoardGetTransportFixture(t)

	resp, out := doMCP(t, fixture.client, fixture.serverURL+"/mcp", map[string]any{
		"tool": "board_get",
		"input": map[string]any{
			"projectSlug": fixture.slug,
			"sort":        "invalid",
		},
	})

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%#v", resp.StatusCode, out)
	}
	if got, want := sortedMapKeys(out), []string{"error", "ok"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("top-level keys = %v, want %v", got, want)
	}
	if out["ok"] != false {
		t.Fatalf("ok = %#v, want false", out["ok"])
	}
	errObj := out["error"].(map[string]any)
	if got, want := sortedMapKeys(errObj), []string{"code", "details", "message"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("error keys = %v, want %v", got, want)
	}
	if errObj["code"] != "VALIDATION_ERROR" ||
		errObj["message"] != "invalid sort" ||
		!reflect.DeepEqual(errObj["details"], map[string]any{"field": "sort"}) {
		t.Fatalf("error = %#v", errObj)
	}
}

func TestMCPBoardGetTransportContract_JSONRPCSuccessExposesPaginationMetadata(t *testing.T) {
	fixture := newBoardGetTransportFixture(t)

	resp, out := doJSONRPC(t, fixture.client, fixture.serverURL, map[string]any{
		"jsonrpc": "2.0",
		"id":      "phase-7-id",
		"method":  "tools/call",
		"params": map[string]any{
			"name": "board_get",
			"arguments": map[string]any{
				"projectSlug": fixture.slug,
				"limit":       2,
			},
		},
	})

	if resp.StatusCode != http.StatusOK || out["id"] != "phase-7-id" {
		t.Fatalf("status/id = %d/%#v, body=%#v", resp.StatusCode, out["id"], out)
	}
	result := out["result"].(map[string]any)
	if got, want := sortedMapKeys(result), []string{"content", "structuredContent"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("result keys = %v, want %v", got, want)
	}
	structured := result["structuredContent"].(map[string]any)
	if got, want := sortedMapKeys(structured), []string{
		"columns",
		"hasMoreByColumn",
		"nextCursorByColumn",
		"project",
		"totalCountByColumn",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("structuredContent keys = %v, want additive board shape %v", got, want)
	}
	if _, ok := structured["meta"]; ok {
		t.Fatalf("structuredContent must not wrap pagination in meta: %#v", structured)
	}
	hasMore := structured["hasMoreByColumn"].(map[string]any)
	next := structured["nextCursorByColumn"].(map[string]any)
	total := structured["totalCountByColumn"].(map[string]any)
	if hasMore[store.DefaultColumnBacklog] != true {
		t.Fatalf("backlog hasMore = %#v", hasMore)
	}
	if cursor, ok := next[store.DefaultColumnBacklog].(string); !ok || cursor == "" {
		t.Fatalf("backlog next cursor = %#v", next)
	}
	if total[store.DefaultColumnBacklog] != float64(3) {
		t.Fatalf("backlog total = %#v", total)
	}
	content := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content = %#v, want one item", content)
	}
	item := content[0].(map[string]any)
	if !reflect.DeepEqual(sortedMapKeys(item), []string{"text", "type"}) || item["type"] != "text" {
		t.Fatalf("content item = %#v", item)
	}
	var textData map[string]any
	if err := json.Unmarshal([]byte(item["text"].(string)), &textData); err != nil {
		t.Fatalf("decode text content: %v", err)
	}
	if !reflect.DeepEqual(textData, structured) {
		t.Fatalf("text JSON != structured content\ntext=%#v\nstructured=%#v", textData, structured)
	}
}

func TestMCPBoardGetTransportContract_CanonicalSlugIdentityMatrix(t *testing.T) {
	fixture := newBoardGetTransportFixture(t)
	suppliedSlugs := []string{
		fixture.slug,
		strings.ToUpper(fixture.slug),
		"Phase-7-Transport-Board",
		"  " + strings.ToUpper(fixture.slug) + "  ",
	}
	invocations := []struct {
		name     string
		toolName string
		jsonRPC  bool
	}{
		{name: "legacy canonical", toolName: "board_get"},
		{name: "legacy alias", toolName: "board.get"},
		{name: "json-rpc canonical", toolName: "board_get", jsonRPC: true},
		{name: "json-rpc alias", toolName: "board.get", jsonRPC: true},
	}

	for _, invocation := range invocations {
		for _, supplied := range suppliedSlugs {
			t.Run(invocation.name+"/"+supplied, func(t *testing.T) {
				var data map[string]any
				if invocation.jsonRPC {
					resp, out := doJSONRPC(t, fixture.client, fixture.serverURL, map[string]any{
						"jsonrpc": "2.0",
						"id":      1,
						"method":  "tools/call",
						"params": map[string]any{
							"name": invocation.toolName,
							"arguments": map[string]any{
								"projectSlug": supplied,
							},
						},
					})
					if resp.StatusCode != http.StatusOK || out["error"] != nil {
						t.Fatalf("JSON-RPC status/body = %d/%#v, want success", resp.StatusCode, out)
					}
					result := out["result"].(map[string]any)
					if result["isError"] == true {
						t.Fatalf("JSON-RPC result = %#v, want tool success", result)
					}
					data = result["structuredContent"].(map[string]any)
				} else {
					resp, out := doMCP(t, fixture.client, fixture.serverURL+"/mcp", map[string]any{
						"tool": invocation.toolName,
						"input": map[string]any{
							"projectSlug": supplied,
						},
					})
					if resp.StatusCode != http.StatusOK || out["ok"] != true {
						t.Fatalf("legacy status/body = %d/%#v, want success", resp.StatusCode, out)
					}
					data = out["data"].(map[string]any)
				}

				project := data["project"].(map[string]any)
				if project["projectSlug"] != fixture.slug {
					t.Fatalf("projectSlug = %#v, want canonical %q for supplied %q", project["projectSlug"], fixture.slug, supplied)
				}
				itemCount := 0
				for _, rawColumn := range data["columns"].([]any) {
					column := rawColumn.(map[string]any)
					for _, rawItem := range column["items"].([]any) {
						item := rawItem.(map[string]any)
						itemCount++
						if item["projectSlug"] != fixture.slug {
							t.Fatalf("todo projectSlug = %#v, want canonical %q for supplied %q", item["projectSlug"], fixture.slug, supplied)
						}
					}
				}
				if itemCount == 0 {
					t.Fatal("identity matrix fixture returned no todos")
				}
			})
		}
	}
}

func TestMCPBoardGetTransportContract_JSONRPCPaginationContinuesFromReturnedCursor(t *testing.T) {
	fixture := newBoardGetTransportFixture(t)

	_, first := doJSONRPC(t, fixture.client, fixture.serverURL, map[string]any{
		"jsonrpc": "2.0",
		"id":      "first-page",
		"method":  "tools/call",
		"params": map[string]any{
			"name": "board_get",
			"arguments": map[string]any{
				"projectSlug": fixture.slug,
				"limit":       2,
			},
		},
	})
	firstStructured := first["result"].(map[string]any)["structuredContent"].(map[string]any)
	firstNext := firstStructured["nextCursorByColumn"].(map[string]any)
	cursor, ok := firstNext[store.DefaultColumnBacklog].(string)
	if !ok || cursor == "" {
		t.Fatalf("first-page backlog cursor = %#v", firstNext)
	}

	resp, second := doJSONRPC(t, fixture.client, fixture.serverURL, map[string]any{
		"jsonrpc": "2.0",
		"id":      "second-page",
		"method":  "tools/call",
		"params": map[string]any{
			"name": "board_get",
			"arguments": map[string]any{
				"projectSlug": fixture.slug,
				"limit":       2,
				"cursorByColumn": map[string]any{
					store.DefaultColumnBacklog: cursor,
				},
			},
		},
	})
	if resp.StatusCode != http.StatusOK || second["id"] != "second-page" {
		t.Fatalf("second-page status/id = %d/%#v, body=%#v", resp.StatusCode, second["id"], second)
	}
	secondStructured := second["result"].(map[string]any)["structuredContent"].(map[string]any)
	backlog := boardColumnByKey(t, secondStructured["columns"].([]any), store.DefaultColumnBacklog)
	if items := backlog["items"].([]any); len(items) != 1 {
		t.Fatalf("second-page backlog items = %#v, want one", items)
	}
	if got := secondStructured["hasMoreByColumn"].(map[string]any)[store.DefaultColumnBacklog]; got != false {
		t.Fatalf("second-page backlog hasMore = %#v, want false", got)
	}
	if got := secondStructured["nextCursorByColumn"].(map[string]any)[store.DefaultColumnBacklog]; got != nil {
		t.Fatalf("second-page backlog next cursor = %#v, want null", got)
	}
	if got := secondStructured["totalCountByColumn"].(map[string]any)[store.DefaultColumnBacklog]; got != float64(3) {
		t.Fatalf("second-page backlog total = %#v, want 3", got)
	}
}

func TestMCPBoardGetTransportContract_JSONRPCToolErrorAddsSanitizedStructuredDetails(t *testing.T) {
	fixture := newBoardGetTransportFixture(t)

	resp, out := doJSONRPC(t, fixture.client, fixture.serverURL, map[string]any{
		"jsonrpc": "2.0",
		"id":      71,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "board_get",
			"arguments": map[string]any{
				"projectSlug": fixture.slug,
				"sort":        "invalid",
			},
		},
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body=%#v", resp.StatusCode, out)
	}
	result := out["result"].(map[string]any)
	if got, want := sortedMapKeys(result), []string{"content", "isError", "structuredContent"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("result keys = %v, want %v", got, want)
	}
	if result["isError"] != true {
		t.Fatalf("isError = %#v, want true", result["isError"])
	}
	content := result["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != "invalid sort" {
		t.Fatalf("content = %#v", content)
	}
	structured := result["structuredContent"].(map[string]any)
	if got, want := sortedMapKeys(structured), []string{"code", "details", "message"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("structuredContent keys = %v, want %v", got, want)
	}
	if structured["code"] != "VALIDATION_ERROR" ||
		structured["message"] != "invalid sort" ||
		!reflect.DeepEqual(structured["details"], map[string]any{"field": "sort"}) {
		t.Fatalf("structuredContent = %#v", structured)
	}
	for _, absent := range []string{"code", "details", "status"} {
		if _, ok := result[absent]; ok {
			t.Fatalf("JSON-RPC tool error unexpectedly flattens %q: %#v", absent, result)
		}
	}
	if _, ok := structured["status"]; ok {
		t.Fatalf("JSON-RPC structured tool error unexpectedly copies HTTP status: %#v", structured)
	}
}

func TestMCPBoardGetTransportContract_JSONRPCFullModeAuthBoundary(t *testing.T) {
	fixture := newBoardGetTransportFixture(t)
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "board_get",
			"arguments": map[string]any{"projectSlug": fixture.slug},
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, fixture.serverURL+"/mcp/rpc", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")

	stateless := &http.Client{Transport: fixture.client.Transport}
	resp, err := stateless.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized || len(body) != 0 {
		t.Fatalf("status/body = %d/%q, want 401 with empty body", resp.StatusCode, string(body))
	}
	if got := resp.Header.Get("WWW-Authenticate"); got == "" {
		t.Fatal("missing WWW-Authenticate challenge")
	}
}

func TestMCPBoardGetTransportContract_LegacyCapabilityAndAuthBoundaries(t *testing.T) {
	t.Run("full mode before bootstrap", func(t *testing.T) {
		ts, _, cleanup := newTestServer(t, "full")
		defer cleanup()

		resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
			"tool":  "board_get",
			"input": map[string]any{"projectSlug": "demo"},
		})

		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, body=%#v", resp.StatusCode, out)
		}
		errObj := out["error"].(map[string]any)
		if errObj["code"] != "CAPABILITY_UNAVAILABLE" ||
			errObj["message"] != "board_get is unavailable before bootstrap" ||
			!reflect.DeepEqual(errObj["details"], map[string]any{}) {
			t.Fatalf("error = %#v", errObj)
		}
	})

	t.Run("full mode signed out after bootstrap", func(t *testing.T) {
		fixture := newBoardGetTransportFixture(t)

		resp, out := doMCP(t, &http.Client{Transport: fixture.client.Transport}, fixture.serverURL+"/mcp", map[string]any{
			"tool":  "board_get",
			"input": map[string]any{"projectSlug": fixture.slug},
		})

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, body=%#v", resp.StatusCode, out)
		}
		errObj := out["error"].(map[string]any)
		if errObj["code"] != "AUTH_REQUIRED" ||
			errObj["message"] != "Sign-in required for this tool" ||
			!reflect.DeepEqual(errObj["details"], map[string]any{}) {
			t.Fatalf("error = %#v", errObj)
		}
	})

	t.Run("anonymous mode", func(t *testing.T) {
		ts, _, cleanup := newTestServer(t, "anonymous")
		defer cleanup()

		resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
			"tool":  "board_get",
			"input": map[string]any{"projectSlug": "demo"},
		})

		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, body=%#v", resp.StatusCode, out)
		}
		errObj := out["error"].(map[string]any)
		if errObj["code"] != "CAPABILITY_UNAVAILABLE" ||
			errObj["message"] != "board_get is unavailable in anonymous mode" ||
			!reflect.DeepEqual(errObj["details"], map[string]any{}) {
			t.Fatalf("error = %#v", errObj)
		}
	})
}

func TestMCPBoardGetTransportContract_JSONRPCAnonymousModeIsToolError(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doJSONRPC(t, ts.Client(), ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "board_get",
			"arguments": map[string]any{"projectSlug": "demo"},
		},
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body=%#v", resp.StatusCode, out)
	}
	result := out["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("result = %#v, want tool error", result)
	}
	content := result["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != "board_get is unavailable in anonymous mode" {
		t.Fatalf("content = %#v", content)
	}
}

func TestMCPBoardGetTransportContract_CanonicalAndAliasEquivalent(t *testing.T) {
	fixture := newBoardGetTransportFixture(t)

	var legacyResults []map[string]any
	for _, name := range []string{"board_get", "board.get"} {
		resp, out := doMCP(t, fixture.client, fixture.serverURL+"/mcp", map[string]any{
			"tool":  name,
			"input": map[string]any{"projectSlug": fixture.slug, "limit": 1},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s legacy status=%d body=%#v", name, resp.StatusCode, out)
		}
		legacyResults = append(legacyResults, out)
	}
	if !reflect.DeepEqual(legacyResults[0], legacyResults[1]) {
		t.Fatalf("legacy canonical/alias differ\ncanonical=%#v\nalias=%#v", legacyResults[0], legacyResults[1])
	}

	var rpcResults []map[string]any
	for _, name := range []string{"board_get", "board.get"} {
		_, out := doJSONRPC(t, fixture.client, fixture.serverURL, map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      name,
				"arguments": map[string]any{"projectSlug": fixture.slug, "limit": 1},
			},
		})
		rpcResults = append(rpcResults, out["result"].(map[string]any))
	}
	if !reflect.DeepEqual(rpcResults[0], rpcResults[1]) {
		t.Fatalf("JSON-RPC canonical/alias differ\ncanonical=%#v\nalias=%#v", rpcResults[0], rpcResults[1])
	}
}

func TestMCPBoardGetTransportContract_CanonicalAndAliasValidationEquivalent(t *testing.T) {
	fixture := newBoardGetTransportFixture(t)

	var (
		legacyStatuses []int
		legacyResults  []map[string]any
	)
	for _, name := range []string{"board_get", "board.get"} {
		resp, out := doMCP(t, fixture.client, fixture.serverURL+"/mcp", map[string]any{
			"tool":  name,
			"input": map[string]any{"projectSlug": fixture.slug, "sort": "invalid"},
		})
		legacyStatuses = append(legacyStatuses, resp.StatusCode)
		legacyResults = append(legacyResults, out)
	}
	if legacyStatuses[0] != http.StatusBadRequest ||
		legacyStatuses[1] != legacyStatuses[0] ||
		!reflect.DeepEqual(legacyResults[0], legacyResults[1]) {
		t.Fatalf("legacy validation differs: statuses=%v results=%#v", legacyStatuses, legacyResults)
	}

	var rpcResults []map[string]any
	for _, name := range []string{"board_get", "board.get"} {
		_, out := doJSONRPC(t, fixture.client, fixture.serverURL, map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      name,
				"arguments": map[string]any{"projectSlug": fixture.slug, "sort": "invalid"},
			},
		})
		rpcResults = append(rpcResults, out["result"].(map[string]any))
	}
	if !reflect.DeepEqual(rpcResults[0], rpcResults[1]) {
		t.Fatalf("JSON-RPC validation differs\ncanonical=%#v\nalias=%#v", rpcResults[0], rpcResults[1])
	}
}

func TestMCPBoardGetTransportContract_PrecedenceEquivalentForDeniedTarget(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Board Precedence Transport Target",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status = %d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Board Precedence Transport Target")

	st := store.New(sqlDB, nil)
	outsider, err := st.CreateUser(t.Context(), "precedence-outsider@example.com", "password123", "Precedence Outsider")
	if err != nil {
		t.Fatalf("create outsider: %v", err)
	}
	outsiderClient := newSessionClientForUser(t, ts, st, outsider.ID)

	tests := []struct {
		name         string
		input        map[string]any
		legacyStatus int
		wantError    map[string]any
	}{
		{
			name: "target-independent sort validation precedes access",
			input: map[string]any{
				"projectSlug": slug,
				"sort":        "invalid",
			},
			legacyStatus: http.StatusBadRequest,
			wantError: map[string]any{
				"code":    "VALIDATION_ERROR",
				"message": "invalid sort",
				"details": map[string]any{"field": "sort"},
			},
		},
		{
			name: "target-dependent sprint validation follows access",
			input: map[string]any{
				"projectSlug": slug,
				"sprintId":    -1,
			},
			legacyStatus: http.StatusNotFound,
			wantError: map[string]any{
				"code":    "NOT_FOUND",
				"message": "not found",
				"details": map[string]any{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, toolName := range []string{"board_get", "board.get"} {
				t.Run("legacy "+toolName, func(t *testing.T) {
					resp, out := doMCP(t, outsiderClient, ts.URL+"/mcp", map[string]any{
						"tool":  toolName,
						"input": tt.input,
					})
					if resp.StatusCode != tt.legacyStatus {
						t.Fatalf("status = %d, want %d; body = %#v", resp.StatusCode, tt.legacyStatus, out)
					}
					if got := out["error"].(map[string]any); !reflect.DeepEqual(got, tt.wantError) {
						t.Fatalf("legacy error = %#v, want %#v", got, tt.wantError)
					}
				})

				t.Run("JSON-RPC "+toolName, func(t *testing.T) {
					resp, out := doJSONRPC(t, outsiderClient, ts.URL, map[string]any{
						"jsonrpc": "2.0",
						"id":      1,
						"method":  "tools/call",
						"params": map[string]any{
							"name":      toolName,
							"arguments": tt.input,
						},
					})
					if resp.StatusCode != http.StatusOK || out["error"] != nil {
						t.Fatalf("JSON-RPC status/error = %d/%#v; body = %#v", resp.StatusCode, out["error"], out)
					}
					result := out["result"].(map[string]any)
					if result["isError"] != true {
						t.Fatalf("JSON-RPC result = %#v, want tool error", result)
					}
					if got := result["structuredContent"].(map[string]any); !reflect.DeepEqual(got, tt.wantError) {
						t.Fatalf("JSON-RPC error = %#v, want %#v", got, tt.wantError)
					}
				})
			}
		})
	}
}

func TestMCPBoardGetTransportContract_ToolsListAdvertisesCanonicalAndSort(t *testing.T) {
	fixture := newBoardGetTransportFixture(t)

	_, out := doJSONRPC(t, fixture.client, fixture.serverURL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})
	tools := out["result"].(map[string]any)["tools"].([]any)
	var board map[string]any
	for _, raw := range tools {
		candidate := raw.(map[string]any)
		if candidate["name"] == "board.get" {
			t.Fatalf("legacy alias advertised: %#v", candidate)
		}
		if candidate["name"] == "board_get" {
			board = candidate
		}
	}
	if board == nil {
		t.Fatal("canonical board_get missing from tools/list")
	}
	properties := board["inputSchema"].(map[string]any)["properties"].(map[string]any)
	sortProperty, ok := properties["sort"].(map[string]any)
	if !ok {
		t.Fatalf("board_get sort schema has unexpected shape: %#v", properties["sort"])
	}
	if sortProperty["type"] != "string" {
		t.Fatalf("board_get sort type = %#v, want string", sortProperty["type"])
	}
	if got, want := sortProperty["enum"], []any{"newest", "oldest"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("board_get sort enum = %#v, want %#v", got, want)
	}
	description, _ := sortProperty["description"].(string)
	if !strings.Contains(description, "omit for manual drag-rank order") {
		t.Fatalf("board_get sort description = %q", description)
	}
}

func TestMCPBoardGetTransportContract_RuntimeSortMatchesAdvertisedValues(t *testing.T) {
	fixture := newBoardGetTransportFixture(t)

	for _, sortOrder := range []string{"newest", "oldest"} {
		resp, out := doJSONRPC(t, fixture.client, fixture.serverURL, map[string]any{
			"jsonrpc": "2.0",
			"id":      sortOrder,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "board_get",
				"arguments": map[string]any{
					"projectSlug": fixture.slug,
					"sort":        sortOrder,
				},
			},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("sort=%s status=%d body=%#v", sortOrder, resp.StatusCode, out)
		}
		result := out["result"].(map[string]any)
		if result["isError"] == true || result["structuredContent"] == nil {
			t.Fatalf("sort=%s result=%#v", sortOrder, result)
		}
	}
}
