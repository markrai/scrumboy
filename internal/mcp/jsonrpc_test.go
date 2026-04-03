package mcp_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// doJSONRPC posts a JSON-RPC request to /mcp/rpc and returns the decoded response map.
func doJSONRPC(t *testing.T, client *http.Client, baseURL string, body any) (*http.Response, map[string]any) {
	t.Helper()

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode jsonrpc body: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp/rpc", &buf)
	if err != nil {
		t.Fatalf("new jsonrpc request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do jsonrpc request: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read jsonrpc response: %v", err)
	}

	if len(raw) == 0 {
		return resp, nil
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode jsonrpc response: %v (body=%q)", err, string(raw))
	}
	return resp, out
}

func TestJSONRPC_InitializeHandshake(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	resp, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "test-client",
				"version": "0.1",
			},
		},
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if out["jsonrpc"] != "2.0" {
		t.Fatalf("expected jsonrpc 2.0, got %v", out["jsonrpc"])
	}
	if out["error"] != nil {
		t.Fatalf("unexpected error: %v", out["error"])
	}

	result, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %v", out["result"])
	}
	if result["protocolVersion"] != "2024-11-05" {
		t.Fatalf("expected protocolVersion 2024-11-05, got %v", result["protocolVersion"])
	}
	serverInfo, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("expected serverInfo object, got %v", result["serverInfo"])
	}
	if serverInfo["name"] != "scrumboy" {
		t.Fatalf("expected serverInfo.name scrumboy, got %v", serverInfo["name"])
	}
	caps, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("expected capabilities object, got %v", result["capabilities"])
	}
	if caps["tools"] == nil {
		t.Fatalf("expected capabilities.tools, got nil")
	}
}

func TestJSONRPC_InitializeMinimalParams(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})

	if out["error"] != nil {
		t.Fatalf("initialize with no params should succeed, got error: %v", out["error"])
	}
	result := out["result"].(map[string]any)
	if result["protocolVersion"] != "2024-11-05" {
		t.Fatalf("unexpected protocolVersion: %v", result["protocolVersion"])
	}
}

func TestJSONRPC_InitializedNotification(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	resp, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 for notification, got %d", resp.StatusCode)
	}
	if out != nil {
		t.Fatalf("expected no body for notification, got %v", out)
	}
}

func TestJSONRPC_InitializedAltName(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	resp, _ := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"method":  "initialized",
	})

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 for 'initialized' notification, got %d", resp.StatusCode)
	}
}

func TestJSONRPC_InitializedWithIDRejected(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "notifications/initialized",
	})

	errObj, ok := out["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error for initialized-with-id, got %v", out)
	}
	if errObj["code"].(float64) != -32600 {
		t.Fatalf("expected InvalidRequest code, got %v", errObj["code"])
	}
}

func TestJSONRPC_UnknownMethodReturnsError(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "nonexistent/method",
	})

	errObj, ok := out["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error for unknown method, got %v", out)
	}
	if errObj["code"].(float64) != -32601 {
		t.Fatalf("expected MethodNotFound code, got %v", errObj["code"])
	}
}

func TestJSONRPC_InvalidJSONReturnsParseError(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp/rpc", bytes.NewBufferString("{bad json"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	errObj := out["error"].(map[string]any)
	if errObj["code"].(float64) != -32700 {
		t.Fatalf("expected ParseError code, got %v", errObj["code"])
	}
}

func TestJSONRPC_MissingVersionReturnsInvalidRequest(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"id":     1,
		"method": "initialize",
	})

	errObj := out["error"].(map[string]any)
	if errObj["code"].(float64) != -32600 {
		t.Fatalf("expected InvalidRequest code, got %v", errObj["code"])
	}
}

func TestJSONRPC_GetMethodRejected(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	resp, err := http.Get(ts.URL + "/mcp/rpc")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	_ = client // use ts.Client() transport via http.Get on ts.URL

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	errObj := out["error"].(map[string]any)
	if errObj["code"].(float64) != -32600 {
		t.Fatalf("expected InvalidRequest code for GET, got %v", errObj["code"])
	}
}

func TestJSONRPC_LegacyEndpointStillWorks(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	resp, err := http.Get(ts.URL + "/mcp")
	if err != nil {
		t.Fatalf("get /mcp: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy /mcp expected 200, got %d", resp.StatusCode)
	}

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true {
		t.Fatalf("expected legacy ok=true, got %v", out["ok"])
	}
	data := out["data"].(map[string]any)
	if data["implementedTools"] == nil {
		t.Fatalf("expected implementedTools in legacy response")
	}
}

func TestJSONRPC_MissingMethodReturnsInvalidRequest(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
	})

	errObj := out["error"].(map[string]any)
	if errObj["code"].(float64) != -32600 {
		t.Fatalf("expected InvalidRequest code for missing method, got %v", errObj["code"])
	}
}

func TestJSONRPC_ToolsList_MatchesImplementedTools(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})

	if out["error"] != nil {
		t.Fatalf("unexpected error: %v", out["error"])
	}
	result, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %v", out["result"])
	}
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array, got %v", result["tools"])
	}

	resp, err := http.Get(ts.URL + "/mcp")
	if err != nil {
		t.Fatalf("get legacy /mcp: %v", err)
	}
	defer resp.Body.Close()

	var legacy map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&legacy); err != nil {
		t.Fatalf("decode legacy response: %v", err)
	}
	legacyData := legacy["data"].(map[string]any)
	implemented := legacyData["implementedTools"].([]any)

	if len(tools) != len(implemented) {
		t.Fatalf("expected %d tools, got %d", len(implemented), len(tools))
	}

	byName := make(map[string]map[string]any, len(tools))
	for _, raw := range tools {
		tool := raw.(map[string]any)
		name, _ := tool["name"].(string)
		if name == "" {
			t.Fatalf("tool entry missing name: %#v", tool)
		}
		byName[name] = tool
	}

	for _, rawName := range implemented {
		name := rawName.(string)
		tool, ok := byName[name]
		if !ok {
			t.Fatalf("missing tool %q in tools/list", name)
		}
		if tool["description"] == nil || tool["description"] == "" {
			t.Fatalf("tool %q missing description", name)
		}
		if tool["inputSchema"] == nil {
			t.Fatalf("tool %q missing inputSchema", name)
		}
		schema := tool["inputSchema"].(map[string]any)
		if schema["type"] != "object" {
			t.Fatalf("tool %q inputSchema.type expected object, got %v", name, schema["type"])
		}
		if schema["additionalProperties"] != false {
			t.Fatalf("tool %q inputSchema.additionalProperties expected false, got %v", name, schema["additionalProperties"])
		}
	}
}

func TestJSONRPC_ToolsList_TodosCreateSchema(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})

	result := out["result"].(map[string]any)
	tools := result["tools"].([]any)

	var todosCreate map[string]any
	for _, t2 := range tools {
		tool := t2.(map[string]any)
		if tool["name"] == "todos.create" {
			todosCreate = tool
			break
		}
	}
	if todosCreate == nil {
		t.Fatal("todos.create not found in tools/list")
	}

	schema := todosCreate["inputSchema"].(map[string]any)
	if schema["additionalProperties"] != false {
		t.Fatalf("todos.create root additionalProperties expected false, got %v", schema["additionalProperties"])
	}
	props := schema["properties"].(map[string]any)

	requiredFields := []string{"projectSlug", "title"}
	required, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("expected required array, got %v", schema["required"])
	}
	if len(required) != len(requiredFields) {
		t.Fatalf("expected %d required fields, got %d", len(requiredFields), len(required))
	}
	for i, field := range requiredFields {
		if required[i] != field {
			t.Fatalf("required[%d] expected %q, got %q", i, field, required[i])
		}
	}

	expectedProps := []string{"projectSlug", "title", "body", "tags", "columnKey", "estimationPoints", "sprintId", "assigneeUserId", "position"}
	for _, prop := range expectedProps {
		if props[prop] == nil {
			t.Fatalf("todos.create schema missing property %q", prop)
		}
	}

	tags := props["tags"].(map[string]any)
	if tags["type"] != "array" {
		t.Fatalf("tags expected array type, got %v", tags["type"])
	}

	position := props["position"].(map[string]any)
	if position["additionalProperties"] != false {
		t.Fatalf("position additionalProperties expected false, got %v", position["additionalProperties"])
	}
	posProps := position["properties"].(map[string]any)
	if posProps["afterLocalId"] == nil || posProps["beforeLocalId"] == nil {
		t.Fatal("position missing afterLocalId or beforeLocalId")
	}
}

func TestJSONRPC_ToolsList_TodosUpdateSchema(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})

	result := out["result"].(map[string]any)
	tools := result["tools"].([]any)

	var todosUpdate map[string]any
	for _, t2 := range tools {
		tool := t2.(map[string]any)
		if tool["name"] == "todos.update" {
			todosUpdate = tool
			break
		}
	}
	if todosUpdate == nil {
		t.Fatal("todos.update not found in tools/list")
	}

	schema := todosUpdate["inputSchema"].(map[string]any)
	if schema["additionalProperties"] != false {
		t.Fatalf("todos.update root additionalProperties expected false, got %v", schema["additionalProperties"])
	}
	props := schema["properties"].(map[string]any)

	required := schema["required"].([]any)
	requiredFields := []string{"projectSlug", "localId", "patch"}
	if len(required) != len(requiredFields) {
		t.Fatalf("expected %d required fields, got %d", len(requiredFields), len(required))
	}
	for i, field := range requiredFields {
		if required[i] != field {
			t.Fatalf("required[%d] expected %q, got %q", i, field, required[i])
		}
	}

	patch := props["patch"].(map[string]any)
	if patch["additionalProperties"] != false {
		t.Fatalf("patch additionalProperties expected false, got %v", patch["additionalProperties"])
	}
	patchProps := patch["properties"].(map[string]any)
	expectedPatchFields := []string{"title", "body", "tags", "estimationPoints", "assigneeUserId", "sprintId"}
	for _, field := range expectedPatchFields {
		if patchProps[field] == nil {
			t.Fatalf("todos.update patch missing field %q", field)
		}
	}
}

func TestJSONRPC_ToolsList_ProjectsListSchema(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})

	result := out["result"].(map[string]any)
	tools := result["tools"].([]any)

	var projectsList map[string]any
	for _, t2 := range tools {
		tool := t2.(map[string]any)
		if tool["name"] == "projects.list" {
			projectsList = tool
			break
		}
	}
	if projectsList == nil {
		t.Fatal("projects.list not found in tools/list")
	}

	schema := projectsList["inputSchema"].(map[string]any)
	if schema["type"] != "object" {
		t.Fatalf("projects.list inputSchema.type expected object, got %v", schema["type"])
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("projects.list additionalProperties expected false, got %v", schema["additionalProperties"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("projects.list expected properties object, got %v", schema["properties"])
	}
	if len(props) != 0 {
		t.Fatalf("projects.list expected empty properties, got %v", props)
	}
}

func TestJSONRPC_ToolsList_WithoutInitialize(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})

	if out["error"] != nil {
		t.Fatalf("tools/list without prior initialize should succeed, got error: %v", out["error"])
	}
	result := out["result"].(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("tools/list returned empty tools array")
	}
}

// ---------- tools/call tests ----------

func TestJSONRPC_ToolsCall_HappyPath(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	var created map[string]any
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "RPC Project",
	}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      42,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "projects.list",
			"arguments": map[string]any{},
		},
	})

	if out["jsonrpc"] != "2.0" {
		t.Fatalf("expected jsonrpc 2.0, got %v", out["jsonrpc"])
	}
	if out["id"].(float64) != 42 {
		t.Fatalf("expected id 42, got %v", out["id"])
	}
	if out["error"] != nil {
		t.Fatalf("unexpected error: %v", out["error"])
	}

	result, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %v", out["result"])
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected non-empty content array, got %v", result["content"])
	}
	item := content[0].(map[string]any)
	if item["type"] != "text" {
		t.Fatalf("expected content type text, got %v", item["type"])
	}
	if item["json"] != nil {
		t.Fatalf("did not expect custom json content block field, got %v", item["json"])
	}
	if item["text"] == nil || item["text"] == "" {
		t.Fatalf("expected non-empty text content, got %v", item["text"])
	}

	jsonData, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("expected structuredContent object, got %v", result["structuredContent"])
	}
	items, ok := jsonData["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected 1 project item, got %v", jsonData["items"])
	}
	proj := items[0].(map[string]any)
	if proj["name"] != "RPC Project" {
		t.Fatalf("expected project name RPC Project, got %v", proj["name"])
	}
}

func TestJSONRPC_ToolsCall_TodosCreate(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	var created map[string]any
	doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Project",
	}, &created)
	slug := created["slug"].(string)

	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "todos.create",
			"arguments": map[string]any{
				"projectSlug": slug,
				"title":       "MCP Todo",
			},
		},
	})

	if out["error"] != nil {
		t.Fatalf("unexpected error: %v", out["error"])
	}

	result := out["result"].(map[string]any)
	content := result["content"].([]any)
	item := content[0].(map[string]any)
	if item["type"] != "text" {
		t.Fatalf("expected content type text, got %v", item["type"])
	}
	if item["text"] == nil || item["text"] == "" {
		t.Fatalf("expected non-empty text content, got %v", item["text"])
	}
	jsonData := result["structuredContent"].(map[string]any)
	todo := jsonData["todo"].(map[string]any)
	if todo["title"] != "MCP Todo" {
		t.Fatalf("expected title MCP Todo, got %v", todo["title"])
	}
	if todo["projectSlug"] != slug {
		t.Fatalf("expected projectSlug %s, got %v", slug, todo["projectSlug"])
	}
}

func TestJSONRPC_ToolsCall_UnknownTool(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "nonexistent.tool",
			"arguments": map[string]any{},
		},
	})

	if out["error"] != nil {
		t.Fatalf("unknown tool should be a tool result error, got %v", out["error"])
	}
	result := out["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("expected isError=true, got %v", result["isError"])
	}
	content := result["content"].([]any)
	item := content[0].(map[string]any)
	if item["type"] != "text" {
		t.Fatalf("expected text error content, got %v", item["type"])
	}
	if item["text"] != "tool not found" {
		t.Fatalf("expected tool not found message, got %v", item["text"])
	}
}

func TestJSONRPC_ToolsCall_MissingName(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"arguments": map[string]any{},
		},
	})

	errObj, ok := out["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error for missing name, got %v", out)
	}
	if errObj["code"].(float64) != -32602 {
		t.Fatalf("expected InvalidParams code, got %v", errObj["code"])
	}
}

func TestJSONRPC_ToolsCall_MissingParams(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
	})

	errObj, ok := out["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error for missing params, got %v", out)
	}
	if errObj["code"].(float64) != -32602 {
		t.Fatalf("expected InvalidParams code, got %v", errObj["code"])
	}
}

func TestJSONRPC_ToolsCall_MissingRequiredArguments(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "todos.create",
			"arguments": map[string]any{"projectSlug": "x"},
		},
	})

	if out["error"] != nil {
		t.Fatalf("missing required arguments should be a tool result error, got %v", out["error"])
	}
	result := out["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("expected isError=true, got %v", result["isError"])
	}
	content := result["content"].([]any)
	item := content[0].(map[string]any)
	if item["text"] != "missing required field: title" {
		t.Fatalf("expected missing required field error, got %v", item["text"])
	}
}

func TestJSONRPC_ToolsCall_WithoutInitialize(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "No-Init Project",
	}, &map[string]any{})

	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "projects.list",
			"arguments": map[string]any{},
		},
	})

	if out["error"] != nil {
		t.Fatalf("tools/call without prior initialize should work, got error: %v", out["error"])
	}
	result := out["result"].(map[string]any)
	content := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("expected non-empty content from tools/call without initialize")
	}
}

func TestJSONRPC_ToolsCall_WithoutID(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "projects.list",
			"arguments": map[string]any{},
		},
	})

	errObj, ok := out["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error for tools/call without id, got %v", out)
	}
	if errObj["code"].(float64) != -32600 {
		t.Fatalf("expected InvalidRequest code, got %v", errObj["code"])
	}
}

func TestJSONRPC_ToolsCall_ErrorMapping_AuthRequired(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	bootstrapUser(t, newCookieClient(t, ts), ts.URL)

	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "projects.list",
			"arguments": map[string]any{},
		},
	})

	if out["error"] != nil {
		t.Fatalf("expected tool result error, got %v", out["error"])
	}
	result := out["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("expected isError=true, got %v", result["isError"])
	}
	content := result["content"].([]any)
	item := content[0].(map[string]any)
	if item["text"] != "Sign-in required for this tool" {
		t.Fatalf("expected auth error message, got %v", item["text"])
	}
	if out["ok"] != nil {
		t.Fatal("JSON-RPC response must not contain legacy ok field")
	}
}

func TestJSONRPC_ToolsCall_ErrorMapping_CapabilityUnavailable(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	client := newStatelessClient(ts)
	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "projects.list",
			"arguments": map[string]any{},
		},
	})

	if out["error"] != nil {
		t.Fatalf("expected tool result error, got %v", out["error"])
	}
	result := out["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("expected isError=true, got %v", result["isError"])
	}
	content := result["content"].([]any)
	item := content[0].(map[string]any)
	if item["text"] != "projects.list is unavailable in anonymous mode" {
		t.Fatalf("expected capability error message, got %v", item["text"])
	}
	if out["ok"] != nil {
		t.Fatal("JSON-RPC response must not contain legacy ok field")
	}
}

func TestJSONRPC_ToolsCall_NoLegacyLeakage(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Leak Test",
	}, &map[string]any{})

	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "projects.list",
			"arguments": map[string]any{},
		},
	})

	if out["ok"] != nil {
		t.Fatal("JSON-RPC response must not contain legacy ok field")
	}
	if out["data"] != nil {
		t.Fatal("JSON-RPC response must not contain legacy data field")
	}
	if out["meta"] != nil {
		t.Fatal("JSON-RPC response must not contain legacy meta field")
	}
}

func TestJSONRPC_ToolsCall_DefaultsEmptyArguments(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Defaults Project",
	}, &map[string]any{})

	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "projects.list",
		},
	})

	if out["error"] != nil {
		t.Fatalf("tools/call with omitted arguments should succeed, got error: %v", out["error"])
	}
	result := out["result"].(map[string]any)
	content := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("expected non-empty content when arguments omitted")
	}
}

func TestJSONRPC_ResponsePreservesID(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)

	for _, id := range []any{42, "req-abc", float64(0)} {
		_, out := doJSONRPC(t, client, ts.URL, map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  "initialize",
		})
		gotID := out["id"]
		// JSON numbers unmarshal as float64.
		switch expected := id.(type) {
		case int:
			if gotID.(float64) != float64(expected) {
				t.Fatalf("id mismatch: sent %v got %v", id, gotID)
			}
		case string:
			if gotID.(string) != expected {
				t.Fatalf("id mismatch: sent %v got %v", id, gotID)
			}
		case float64:
			if gotID.(float64) != expected {
				t.Fatalf("id mismatch: sent %v got %v", id, gotID)
			}
		}
	}
}
