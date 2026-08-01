package mcp_test

import (
	"context"
	"net/http"
	"testing"

	"scrumboy/internal/store"
)

func TestMCPBoardGetPerColumnPagination(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Board Page Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	slug := projectSlugByName(t, sqlDB, "Board Page Project")
	projectID := projectIDBySlug(t, sqlDB, slug)
	ownerID := firstUserID(t, sqlDB)
	st := store.New(sqlDB, nil)
	ctx := store.WithUserID(context.Background(), ownerID)
	for i := 0; i < 3; i++ {
		if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
			Title:     "Paged todo",
			ColumnKey: store.DefaultColumnBacklog,
		}, store.ModeFull); err != nil {
			t.Fatalf("create todo %d: %v", i, err)
		}
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board_get",
		"input": map[string]any{
			"projectSlug": slug,
			"limit":       2,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	meta := out["meta"].(map[string]any)
	hasMoreByColumn := meta["hasMoreByColumn"].(map[string]any)
	nextCursorByColumn := meta["nextCursorByColumn"].(map[string]any)
	totalCountByColumn := meta["totalCountByColumn"].(map[string]any)
	if hasMoreByColumn[store.DefaultColumnBacklog] != true {
		t.Fatalf("expected backlog hasMore=true, got %#v", hasMoreByColumn)
	}
	cursor, ok := nextCursorByColumn[store.DefaultColumnBacklog].(string)
	if !ok || cursor == "" {
		t.Fatalf("expected opaque backlog cursor, got %#v", nextCursorByColumn[store.DefaultColumnBacklog])
	}
	if int(totalCountByColumn[store.DefaultColumnBacklog].(float64)) != 3 {
		t.Fatalf("expected totalCount 3, got %#v", totalCountByColumn[store.DefaultColumnBacklog])
	}

	resp3, out2 := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board_get",
		"input": map[string]any{
			"projectSlug": slug,
			"limit":       2,
			"cursorByColumn": map[string]any{
				store.DefaultColumnBacklog: cursor,
			},
		},
	})
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on follow-up page, got %d", resp3.StatusCode)
	}
	backlog := boardColumnByKey(t, out2["data"].(map[string]any)["columns"].([]any), store.DefaultColumnBacklog)
	if len(backlog["items"].([]any)) != 1 {
		t.Fatalf("expected one remaining backlog item, got %#v", backlog["items"])
	}
}
