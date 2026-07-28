package mcp_test

import (
	"context"
	"net/http"
	"testing"

	"scrumboy/internal/mcp"
	"scrumboy/internal/store"
)

func titlesOfBoardItems(items []any) []string {
	out := make([]string, len(items))
	for i, raw := range items {
		out[i] = raw.(map[string]any)["title"].(string)
	}
	return out
}

func TestMCPBoardGetSortOrder(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Board Sort MCP",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	slug := projectSlugByName(t, sqlDB, "Board Sort MCP")
	projectID := projectIDBySlug(t, sqlDB, slug)
	ownerID := firstUserID(t, sqlDB)
	st := store.New(sqlDB, nil)
	ctx := store.WithUserID(context.Background(), ownerID)

	for _, title := range []string{"First", "Second", "Third"} {
		if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{Title: title}, store.ModeFull); err != nil {
			t.Fatalf("CreateTodo(%q): %v", title, err)
		}
	}

	t.Run("default order is manual rank order", func(t *testing.T) {
		respN, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
			"tool":  "board_get",
			"input": map[string]any{"projectSlug": slug},
		})
		if respN.StatusCode != http.StatusOK {
			t.Fatalf("board_get expected 200, got %d body=%#v", respN.StatusCode, out)
		}
		backlog := boardColumnByKey(t, out["data"].(map[string]any)["columns"].([]any), store.DefaultColumnBacklog)
		got := titlesOfBoardItems(backlog["items"].([]any))
		want := []string{"First", "Second", "Third"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	})

	t.Run("sort=newest", func(t *testing.T) {
		respN, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
			"tool":  "board_get",
			"input": map[string]any{"projectSlug": slug, "sort": "newest"},
		})
		if respN.StatusCode != http.StatusOK {
			t.Fatalf("board_get sort=newest expected 200, got %d body=%#v", respN.StatusCode, out)
		}
		backlog := boardColumnByKey(t, out["data"].(map[string]any)["columns"].([]any), store.DefaultColumnBacklog)
		got := titlesOfBoardItems(backlog["items"].([]any))
		want := []string{"Third", "Second", "First"}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	})

	t.Run("sort=oldest", func(t *testing.T) {
		respN, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
			"tool":  "board_get",
			"input": map[string]any{"projectSlug": slug, "sort": "oldest"},
		})
		if respN.StatusCode != http.StatusOK {
			t.Fatalf("board_get sort=oldest expected 200, got %d body=%#v", respN.StatusCode, out)
		}
		backlog := boardColumnByKey(t, out["data"].(map[string]any)["columns"].([]any), store.DefaultColumnBacklog)
		got := titlesOfBoardItems(backlog["items"].([]any))
		want := []string{"First", "Second", "Third"}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	})

	t.Run("invalid sort value", func(t *testing.T) {
		status, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
			"tool":  "board_get",
			"input": map[string]any{"projectSlug": slug, "sort": "manual"},
		})
		if status.StatusCode != http.StatusBadRequest {
			t.Fatalf("sort=manual expected 400, got %d body=%#v", status.StatusCode, out)
		}
		errObj := out["error"].(map[string]any)
		if errObj["code"] != mcp.CodeValidationError || errObj["message"] != "invalid sort" {
			t.Fatalf("unexpected error for sort=manual: %#v", errObj)
		}
		details := errObj["details"].(map[string]any)
		if details["field"] != "sort" {
			t.Fatalf("unexpected details for sort=manual: %#v", details)
		}
	})
}
