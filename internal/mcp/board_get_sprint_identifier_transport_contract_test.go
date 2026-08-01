package mcp_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type boardGetSprintTransportResult struct {
	data      map[string]any
	errorBody map[string]any
}

func TestMCPBoardGetStoredSprintIDRoundTripsAcrossTransportsAndAlias(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	st := store.New(sqlDB, nil)
	ownerID := firstUserID(t, sqlDB)
	ctx := store.WithUserID(context.Background(), ownerID)
	start := time.Unix(5_000, 0).UTC()

	offsetProject, err := st.CreateProject(ctx, "Sprint ID Collision Offset")
	if err != nil {
		t.Fatalf("create offset project: %v", err)
	}
	offsetSprint, err := st.CreateSprint(ctx, offsetProject.ID, "Offset sprint", start, start.Add(time.Hour))
	if err != nil {
		t.Fatalf("create offset sprint: %v", err)
	}

	targetProject, err := st.CreateProject(ctx, "Sprint ID Contract Target")
	if err != nil {
		t.Fatalf("create target project: %v", err)
	}
	targetSprint, err := st.CreateSprint(ctx, targetProject.ID, "Target sprint", start, start.Add(time.Hour))
	if err != nil {
		t.Fatalf("create target sprint: %v", err)
	}
	if targetSprint.ID == targetSprint.Number {
		t.Fatalf("fixture must distinguish stored ID and project-local number: %#v", targetSprint)
	}
	if offsetSprint.ID != targetSprint.Number {
		t.Fatalf(
			"fixture must make target local number %d collide with cross-project stored ID, got offset ID %d",
			targetSprint.Number,
			offsetSprint.ID,
		)
	}

	if _, err := st.CreateTodo(ctx, targetProject.ID, store.CreateTodoInput{
		Title:     "Stored ID match",
		ColumnKey: store.DefaultColumnBacklog,
		SprintID:  &targetSprint.ID,
	}, store.ModeFull); err != nil {
		t.Fatalf("create target sprint todo: %v", err)
	}

	listResp, listOut := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints_list",
		"input": map[string]any{
			"projectSlug": targetProject.Slug,
		},
	})
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("sprints_list status = %d, body = %#v", listResp.StatusCode, listOut)
	}
	items := listOut["data"].(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("sprints_list items = %#v, want one", items)
	}
	listedSprint := items[0].(map[string]any)
	listedSprintID := int64(listedSprint["sprintId"].(float64))
	listedNumber := int64(listedSprint["number"].(float64))
	if listedSprintID != targetSprint.ID || listedNumber != targetSprint.Number {
		t.Fatalf(
			"sprints_list identity = sprintId %d, number %d; want %d, %d",
			listedSprintID,
			listedNumber,
			targetSprint.ID,
			targetSprint.Number,
		)
	}

	transports := []struct {
		name     string
		toolName string
		call     func(*testing.T, string, int64) boardGetSprintTransportResult
	}{
		{
			name:     "legacy canonical",
			toolName: "board_get",
			call: func(t *testing.T, toolName string, sprintID int64) boardGetSprintTransportResult {
				return callLegacyBoardGetSprint(t, client, ts.URL, toolName, targetProject.Slug, sprintID)
			},
		},
		{
			name:     "legacy alias",
			toolName: "board.get",
			call: func(t *testing.T, toolName string, sprintID int64) boardGetSprintTransportResult {
				return callLegacyBoardGetSprint(t, client, ts.URL, toolName, targetProject.Slug, sprintID)
			},
		},
		{
			name:     "JSON-RPC canonical",
			toolName: "board_get",
			call: func(t *testing.T, toolName string, sprintID int64) boardGetSprintTransportResult {
				return callJSONRPCBoardGetSprint(t, client, ts.URL, toolName, targetProject.Slug, sprintID)
			},
		},
		{
			name:     "JSON-RPC alias",
			toolName: "board.get",
			call: func(t *testing.T, toolName string, sprintID int64) boardGetSprintTransportResult {
				return callJSONRPCBoardGetSprint(t, client, ts.URL, toolName, targetProject.Slug, sprintID)
			},
		},
	}

	for _, transport := range transports {
		t.Run(transport.name, func(t *testing.T) {
			roundTrip := transport.call(t, transport.toolName, listedSprintID)
			if roundTrip.errorBody != nil {
				t.Fatalf("stored sprintId round-trip error = %#v", roundTrip.errorBody)
			}
			backlog := boardColumnByKey(t, roundTrip.data["columns"].([]any), store.DefaultColumnBacklog)
			todos := backlog["items"].([]any)
			if len(todos) != 1 || todos[0].(map[string]any)["title"] != "Stored ID match" {
				t.Fatalf("stored sprintId round-trip todos = %#v", todos)
			}

			collision := transport.call(t, transport.toolName, listedNumber)
			if collision.data != nil {
				t.Fatalf("project-local number was silently accepted as stored sprintId: %#v", collision.data)
			}
			if collision.errorBody["code"] != "NOT_FOUND" ||
				collision.errorBody["message"] != "not found" {
				t.Fatalf("collision error = %#v, want masked NOT_FOUND", collision.errorBody)
			}
			details, ok := collision.errorBody["details"].(map[string]any)
			if !ok || len(details) != 0 {
				t.Fatalf("collision details = %#v, want empty masking details", collision.errorBody["details"])
			}
		})
	}
}

func callLegacyBoardGetSprint(
	t *testing.T,
	client *http.Client,
	baseURL string,
	toolName string,
	projectSlug string,
	sprintID int64,
) boardGetSprintTransportResult {
	t.Helper()

	resp, out := doMCP(t, client, baseURL+"/mcp", map[string]any{
		"tool": toolName,
		"input": map[string]any{
			"projectSlug": projectSlug,
			"sprintId":    sprintID,
		},
	})
	switch resp.StatusCode {
	case http.StatusOK:
		return boardGetSprintTransportResult{data: out["data"].(map[string]any)}
	case http.StatusNotFound:
		return boardGetSprintTransportResult{errorBody: out["error"].(map[string]any)}
	default:
		t.Fatalf("%s legacy status = %d, body = %#v", toolName, resp.StatusCode, out)
		return boardGetSprintTransportResult{}
	}
}

func callJSONRPCBoardGetSprint(
	t *testing.T,
	client *http.Client,
	baseURL string,
	toolName string,
	projectSlug string,
	sprintID int64,
) boardGetSprintTransportResult {
	t.Helper()

	resp, out := doJSONRPC(t, client, baseURL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": toolName,
			"arguments": map[string]any{
				"projectSlug": projectSlug,
				"sprintId":    sprintID,
			},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s JSON-RPC status = %d, body = %#v", toolName, resp.StatusCode, out)
	}
	if out["error"] != nil {
		t.Fatalf("%s JSON-RPC protocol error = %#v", toolName, out["error"])
	}
	result := out["result"].(map[string]any)
	if result["isError"] == true {
		return boardGetSprintTransportResult{
			errorBody: result["structuredContent"].(map[string]any),
		}
	}
	return boardGetSprintTransportResult{
		data: result["structuredContent"].(map[string]any),
	}
}
