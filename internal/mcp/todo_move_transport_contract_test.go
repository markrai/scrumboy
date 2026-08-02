package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

type todoMoveSSEEvent struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

func subscribeTodoMoveEvents(
	t *testing.T,
	client *http.Client,
	eventsURL string,
) (context.CancelFunc, <-chan todoMoveSSEEvent, <-chan error) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsURL, nil)
	if err != nil {
		cancel()
		t.Fatalf("new board events request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("subscribe board events: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		t.Fatalf("subscribe board events: status=%d", resp.StatusCode)
	}
	t.Cleanup(func() {
		cancel()
		_ = resp.Body.Close()
	})

	events := make(chan todoMoveSSEEvent, 1)
	errorsCh := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if ctx.Err() == nil {
					errorsCh <- err
				}
				return
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var event todoMoveSSEEvent
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &event); err != nil {
				errorsCh <- err
				return
			}
			if event.Type == "ping" {
				continue
			}
			events <- event
			return
		}
	}()

	return cancel, events, errorsCh
}

func TestMCPTodosMoveDottedAliasOverJSONRPCPreservesRefreshSilence(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	var project map[string]any
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "MCP Move Alias Contract",
	}, &project)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: status=%d", resp.StatusCode)
	}
	slug := project["slug"].(string)

	resp, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos_create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Move through alias",
		},
	})
	if resp.StatusCode != http.StatusOK || out["ok"] != true {
		t.Fatalf("create todo: status=%d body=%#v", resp.StatusCode, out)
	}

	cancelEvents, events, eventErrors := subscribeTodoMoveEvents(
		t,
		client,
		ts.URL+"/api/board/"+slug+"/events",
	)
	defer cancelEvents()

	resp, out = doJSONRPC(t, client, ts.URL, map[string]any{
		"jsonrpc": "2.0",
		"id":      12,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "todos.move",
			"arguments": map[string]any{
				"projectSlug": slug,
				"localId":     1,
				"toColumnKey": "doing",
			},
		},
	})
	if resp.StatusCode != http.StatusOK || out["error"] != nil {
		t.Fatalf("JSON-RPC alias move: status=%d body=%#v", resp.StatusCode, out)
	}
	result := out["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("JSON-RPC alias move returned tool error: %#v", result)
	}
	structured := result["structuredContent"].(map[string]any)
	todo := structured["todo"].(map[string]any)
	if todo["projectSlug"] != slug || todo["localId"] != float64(1) || todo["columnKey"] != "doing" {
		t.Fatalf("JSON-RPC alias todo = %#v", todo)
	}

	select {
	case event := <-events:
		t.Fatalf("MCP move unexpectedly emitted board event: %+v", event)
	case err := <-eventErrors:
		t.Fatalf("read board events: %v", err)
	case <-time.After(500 * time.Millisecond):
	}

	var todoID int64
	if err := sqlDB.QueryRow(
		`SELECT id FROM todos WHERE project_id = (SELECT id FROM projects WHERE slug = ?) AND local_id = 1`,
		slug,
	).Scan(&todoID); err != nil {
		t.Fatalf("resolve moved todo ID: %v", err)
	}
	var auditCount int
	if err := sqlDB.QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE action = 'todo_moved' AND target_id = ?`,
		todoID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count todo_moved audit rows: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("todo_moved audit rows = %d, want 1", auditCount)
	}
}
