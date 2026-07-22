package mcp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"scrumboy/internal/publicorigin"
)

func TestJSONRPCRequestOnlyMethodsRequireID(t *testing.T) {
	toolCalls := 0
	adapter := &Adapter{
		mode:         "anonymous",
		publicOrigin: publicorigin.New("", false),
		tools: toolRegistry{
			"test.sideEffect": func(context.Context, any) (any, map[string]any, *adapterError) {
				toolCalls++
				return map[string]any{"ok": true}, nil, nil
			},
		},
	}

	cases := []struct {
		name string
		body string
	}{
		{name: "initialize", body: `{"jsonrpc":"2.0","method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`},
		{name: "ping", body: `{"jsonrpc":"2.0","method":"ping"}`},
		{name: "tools/list", body: `{"jsonrpc":"2.0","method":"tools/list"}`},
		{name: "tools/call", body: `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"test.sideEffect","arguments":{}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp/rpc", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("MCP-Protocol-Version", "2025-11-25")
			rec := httptest.NewRecorder()

			adapter.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
			}
		})
	}
	if toolCalls != 0 {
		t.Fatalf("tools/call without id executed %d side effects, want 0", toolCalls)
	}
}
