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
		"method":  "tools/call",
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
