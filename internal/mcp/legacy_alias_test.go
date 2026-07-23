package mcp_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// These tests cover the dispatch-only backward-compat shim added after the
// underscore rename: old dotted MCP tool names (e.g. "projects.list",
// "todos.create") must still work via direct invocation (tools/call and the
// legacy POST /mcp endpoint), but must never be advertised in discovery
// (tools/list / system_getCapabilities' implementedTools).

func TestJSONRPC_ToolsCall_LegacyDottedName_StillDispatches(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

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
		t.Fatalf("legacy dotted name tools/call should succeed, got error: %v", out["error"])
	}
	result, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %v", out["result"])
	}
	if result["isError"] == true {
		t.Fatalf("legacy dotted name tools/call returned isError, got %v", result)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected non-empty content from legacy dotted name tools/call, got %v", result["content"])
	}
}

func TestJSONRPC_ToolsCall_LegacyDottedName_RequiredFieldValidation(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	// todos.create requires "title" in addition to "projectSlug" -- the alias
	// must resolve to the canonical schema for required-field validation, not
	// skip validation entirely.
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
		t.Fatalf("missing required arguments via legacy alias should be a tool result error, got %v", out["error"])
	}
	result := out["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("expected isError=true, got %v", result["isError"])
	}
	content := result["content"].([]any)
	item := content[0].(map[string]any)
	if item["text"] != "missing required field: title" {
		t.Fatalf("expected missing required field error via legacy alias, got %v", item["text"])
	}
}

func TestJSONRPC_ToolsCall_LegacyDottedName_UnknownAliasStillErrors(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newStatelessClient(ts)
	_, out := doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "nonexistent.dotted.tool",
			"arguments": map[string]any{},
		},
	})

	result := out["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("expected isError=true for unknown dotted name, got %v", result)
	}
}

func TestHTTP_LegacyMCPEndpoint_DottedNameStillDispatches(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	resp, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{"tool": "projects.list", "input": map[string]any{}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy /mcp with dotted tool name: status=%d body=%v", resp.StatusCode, out)
	}
	if ok, _ := out["ok"].(bool); !ok {
		t.Fatalf("expected ok=true for legacy dotted tool name, got %v", out)
	}
}

func TestHTTP_LegacyMCPEndpoint_UnknownDottedNameStill404s(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	resp, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{"tool": "nonexistent.dotted.tool", "input": map[string]any{}})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("legacy /mcp with unknown dotted tool name: status=%d body=%v", resp.StatusCode, out)
	}
}

func TestJSONRPC_ToolsList_NeverContainsDottedNames(t *testing.T) {
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
	if len(tools) == 0 {
		t.Fatal("tools/list returned no tools")
	}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		name, _ := tool["name"].(string)
		if strings.Contains(name, ".") {
			t.Errorf("tools/list advertised a dotted (legacy) tool name: %q", name)
		}
	}
}

func TestHTTP_LegacyGetCapabilities_NeverContainsDottedNames(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	resp, err := http.Get(ts.URL + "/mcp")
	if err != nil {
		t.Fatalf("get /mcp: %v", err)
	}
	defer resp.Body.Close()

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	data := out["data"].(map[string]any)
	implemented, ok := data["implementedTools"].([]any)
	if !ok || len(implemented) == 0 {
		t.Fatalf("expected non-empty implementedTools, got %v", data["implementedTools"])
	}
	for _, raw := range implemented {
		name, _ := raw.(string)
		if strings.Contains(name, ".") {
			t.Errorf("system_getCapabilities advertised a dotted (legacy) tool name: %q", name)
		}
	}
}
