package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func TestBoardGetContract_DurableReadOmitsActivityRefresh(t *testing.T) {
	h := newBoardGetContractHarness(t)

	_, _, err := h.call(map[string]any{"projectSlug": h.Project.Slug})
	if err != nil {
		t.Fatalf("board_get: %v", err)
	}
	if len(h.Recording.callsFor("activity")) != 0 {
		t.Fatalf("durable read activity calls = %#v", h.Recording.callsFor("activity"))
	}
}

func TestBoardGetContract_ExpiringReadRefreshesActivityLast(t *testing.T) {
	h := newBoardGetContractHarness(t)
	project, err := h.Store.CreateAnonymousBoard(store.WithUserID(context.Background(), h.Owner.ID))
	if err != nil {
		t.Fatalf("create temporary board: %v", err)
	}

	_, _, readErr := h.call(map[string]any{"projectSlug": project.Slug})
	if readErr != nil {
		t.Fatalf("board_get: %v", readErr)
	}

	activity := h.Recording.callsFor("activity")
	if len(activity) != 1 {
		t.Fatalf("activity calls = %#v, want one", activity)
	}
	if activity[0].ProjectID != project.ID || activity[0].Context != h.Context {
		t.Fatalf("activity call = %#v, want project=%d exact request context", activity[0], project.ID)
	}
	if got := h.Recording.Calls[len(h.Recording.Calls)-1].Operation; got != "activity" {
		t.Fatalf("last operation = %q, want activity (all=%v)", got, h.Recording.operationNames())
	}
	workflow := h.Recording.callsFor("workflow")
	if len(workflow) != 1 {
		t.Fatalf("workflow calls = %#v", workflow)
	}
	if got, want := len(h.Recording.callsFor("list")), 5; got != want {
		t.Fatalf("list calls = %d, want %d for default temporary workflow", got, want)
	}
	if got, want := len(h.Recording.callsFor("count")), 5; got != want {
		t.Fatalf("count calls = %d, want %d for default temporary workflow", got, want)
	}
}

func TestBoardGetContract_PriorFailurePreventsActivityRefresh(t *testing.T) {
	h := newBoardGetContractHarness(t)
	project, err := h.Store.CreateAnonymousBoard(store.WithUserID(context.Background(), h.Owner.ID))
	if err != nil {
		t.Fatalf("create temporary board: %v", err)
	}
	h.Recording.Errors["count:"+store.DefaultColumnBacklog] = injectedBoardGetError("lane count")

	_, _, readErr := h.call(map[string]any{"projectSlug": project.Slug})

	requireBoardGetError(t, readErr, http.StatusInternalServerError, CodeInternal, "internal error", map[string]any{
		"detail": "phase 7 injected lane count failure",
	})
	if len(h.Recording.callsFor("activity")) != 0 {
		t.Fatalf("activity occurred after prior failure: %v", h.Recording.operationNames())
	}
}

func TestBoardGetContract_ActivityFailureIsBestEffortAndReturnsProjection(t *testing.T) {
	h := newBoardGetContractHarness(t)
	project, err := h.Store.CreateAnonymousBoard(store.WithUserID(context.Background(), h.Owner.ID))
	if err != nil {
		t.Fatalf("create temporary board: %v", err)
	}
	h.Recording.Errors["activity"] = injectedBoardGetError("activity")

	data, meta, readErr := h.call(map[string]any{"projectSlug": project.Slug})

	if readErr != nil {
		t.Fatalf("board_get returned ancillary activity error: %v", readErr)
	}
	dataObject, ok := data.(map[string]any)
	if !ok || dataObject["project"] == nil || dataObject["columns"] == nil {
		t.Fatalf("activity failure result = %#v, want completed projection", data)
	}
	if meta == nil || meta["nextCursorByColumn"] == nil || meta["hasMoreByColumn"] == nil || meta["totalCountByColumn"] == nil {
		t.Fatalf("activity failure metadata = %#v, want completed pagination maps", meta)
	}
	if got := h.Recording.Calls[len(h.Recording.Calls)-1].Operation; got != "activity" {
		t.Fatalf("last operation = %q, want activity (all=%v)", got, h.Recording.operationNames())
	}
	logged := h.Logs.String()
	if !strings.Contains(logged, "mcp: board activity refresh failed") ||
		!strings.Contains(logged, "project_id="+strconv.FormatInt(project.ID, 10)) ||
		!strings.Contains(logged, "phase 7 injected activity failure") {
		t.Fatalf("activity failure log = %q, want operation, project identity, and internal cause", logged)
	}
}

func TestBoardGetContract_ActivityFailureIsBestEffortAcrossTransports(t *testing.T) {
	tests := []struct {
		name string
		body func(string) map[string]any
	}{
		{
			name: "legacy",
			body: func(slug string) map[string]any {
				return map[string]any{
					"tool":  "board_get",
					"input": map[string]any{"projectSlug": slug},
				}
			},
		},
		{
			name: "json-rpc",
			body: func(slug string) map[string]any {
				return map[string]any{
					"jsonrpc": "2.0",
					"id":      1,
					"method":  "tools/call",
					"params": map[string]any{
						"name":      "board_get",
						"arguments": map[string]any{"projectSlug": slug},
					},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newBoardGetContractHarness(t)
			project, err := h.Store.CreateAnonymousBoard(store.WithUserID(context.Background(), h.Owner.ID))
			if err != nil {
				t.Fatalf("create temporary board: %v", err)
			}
			h.Recording.Errors["activity"] = injectedBoardGetError("activity")
			session, _, err := h.Store.CreateSession(context.Background(), h.Owner.ID, time.Hour)
			if err != nil {
				t.Fatalf("create session: %v", err)
			}
			server := httptest.NewServer(h.Adapter)
			defer server.Close()

			var requestBody bytes.Buffer
			if err := json.NewEncoder(&requestBody).Encode(tt.body(project.Slug)); err != nil {
				t.Fatalf("encode request: %v", err)
			}
			path := "/mcp"
			if tt.name == "json-rpc" {
				path = "/mcp/rpc"
			}
			req, err := http.NewRequest(http.MethodPost, server.URL+path, &requestBody)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			if tt.name == "json-rpc" {
				req.Header.Set("MCP-Protocol-Version", "2025-11-25")
			}
			req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: session})

			resp, err := server.Client().Do(req)
			if err != nil {
				t.Fatalf("call %s: %v", tt.name, err)
			}
			defer resp.Body.Close()
			var out map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatalf("decode %s response: %v", tt.name, err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s status = %d body=%#v, want 200", tt.name, resp.StatusCode, out)
			}

			var structured map[string]any
			if tt.name == "legacy" {
				if out["ok"] != true || out["error"] != nil {
					t.Fatalf("legacy response = %#v, want success", out)
				}
				structured, _ = out["data"].(map[string]any)
			} else {
				if out["error"] != nil {
					t.Fatalf("JSON-RPC response = %#v, want success", out)
				}
				result, _ := out["result"].(map[string]any)
				if result["isError"] == true {
					t.Fatalf("JSON-RPC result = %#v, want tool success", result)
				}
				structured, _ = result["structuredContent"].(map[string]any)
			}
			if structured == nil || structured["project"] == nil || structured["columns"] == nil {
				t.Fatalf("%s board projection = %#v, want project and columns", tt.name, structured)
			}
			if !strings.Contains(h.Logs.String(), "phase 7 injected activity failure") {
				t.Fatalf("%s activity failure was not logged: %q", tt.name, h.Logs.String())
			}
		})
	}
}
