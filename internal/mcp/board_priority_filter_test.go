package mcp_test

import (
	"context"
	"net/http"
	"testing"

	"scrumboy/internal/store"
)

func TestMCPBoardGetPriorityFilter(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Board Priority MCP",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	slug := projectSlugByName(t, sqlDB, "Board Priority MCP")
	projectID := projectIDBySlug(t, sqlDB, slug)
	ownerID := firstUserID(t, sqlDB)
	st := store.New(sqlDB, nil)
	ctx := store.WithUserID(context.Background(), ownerID)

	urgent := "urgent"
	high := "high"
	for _, in := range []store.CreateTodoInput{
		{Title: "Urgent card", PriorityKey: &urgent},
		{Title: "High card", PriorityKey: &high},
		{Title: "No priority card"},
	} {
		if _, err := st.CreateTodo(ctx, projectID, in, store.ModeFull); err != nil {
			t.Fatalf("CreateTodo(%q): %v", in.Title, err)
		}
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board_get",
		"input": map[string]any{
			"projectSlug": slug,
			"priority":    "urgent",
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("board_get priority=urgent expected 200, got %d body=%#v", resp2.StatusCode, out)
	}
	backlog := boardColumnByKey(t, out["data"].(map[string]any)["columns"].([]any), store.DefaultColumnBacklog)
	items := backlog["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["title"] != "Urgent card" {
		t.Fatalf("unexpected priority=urgent items: %#v", items)
	}

	resp3, noneOut := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board_get",
		"input": map[string]any{
			"projectSlug": slug,
			"priority":    "none",
		},
	})
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("board_get priority=none expected 200, got %d body=%#v", resp3.StatusCode, noneOut)
	}
	backlog = boardColumnByKey(t, noneOut["data"].(map[string]any)["columns"].([]any), store.DefaultColumnBacklog)
	items = backlog["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["title"] != "No priority card" {
		t.Fatalf("unexpected priority=none items: %#v", items)
	}

	resp4, unmatchedOut := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board_get",
		"input": map[string]any{
			"projectSlug": slug,
			"priority":    "not-a-real-key",
		},
	})
	if resp4.StatusCode != http.StatusOK {
		t.Fatalf("board_get unmatched priority expected 200, got %d body=%#v", resp4.StatusCode, unmatchedOut)
	}
	backlog = boardColumnByKey(t, unmatchedOut["data"].(map[string]any)["columns"].([]any), store.DefaultColumnBacklog)
	if got := len(backlog["items"].([]any)); got != 0 {
		t.Fatalf("unmatched priority key returned %d items, want 0", got)
	}

	resp5, allOut := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board_get",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp5.StatusCode != http.StatusOK {
		t.Fatalf("board_get no priority filter expected 200, got %d body=%#v", resp5.StatusCode, allOut)
	}
	backlog = boardColumnByKey(t, allOut["data"].(map[string]any)["columns"].([]any), store.DefaultColumnBacklog)
	if got := len(backlog["items"].([]any)); got != 3 {
		t.Fatalf("no filter returned %d items, want 3", got)
	}
}
