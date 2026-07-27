package mcp_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"scrumboy/internal/mcp"
	"scrumboy/internal/store"
)

func TestMCPBoardGetAssigneeFilter(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Board Assignee MCP",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	slug := projectSlugByName(t, sqlDB, "Board Assignee MCP")
	projectID := projectIDBySlug(t, sqlDB, slug)
	ownerID := firstUserID(t, sqlDB)
	st := store.New(sqlDB, nil)
	ctx := store.WithUserID(context.Background(), ownerID)

	for _, in := range []store.CreateTodoInput{
		{Title: "Mine focus 1", Tags: []string{"focus"}, AssigneeUserID: &ownerID},
		{Title: "Mine focus 2", Tags: []string{"focus"}, AssigneeUserID: &ownerID},
		{Title: "Mine other", Tags: []string{"other"}, AssigneeUserID: &ownerID},
		{Title: "Unassigned focus", Tags: []string{"focus"}},
	} {
		if _, err := st.CreateTodo(ctx, projectID, in, store.ModeFull); err != nil {
			t.Fatalf("CreateTodo(%q): %v", in.Title, err)
		}
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board_get",
		"input": map[string]any{
			"projectSlug": slug,
			"assignee":    "me",
			"tag":         "focus",
			"search":      "mine",
			"limit":       1,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("board_get assignee=me expected 200, got %d body=%#v", resp2.StatusCode, out)
	}
	backlog := boardColumnByKey(t, out["data"].(map[string]any)["columns"].([]any), store.DefaultColumnBacklog)
	items := backlog["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected first filtered page to contain one item, got %#v", items)
	}
	item := items[0].(map[string]any)
	if int64(item["assigneeUserId"].(float64)) != ownerID {
		t.Fatalf("expected assigneeUserId=%d, got %#v", ownerID, item)
	}
	meta := out["meta"].(map[string]any)
	if meta["hasMoreByColumn"].(map[string]any)[store.DefaultColumnBacklog] != true {
		t.Fatalf("expected filtered backlog to have another page, got %#v", meta)
	}
	if got := int(meta["totalCountByColumn"].(map[string]any)[store.DefaultColumnBacklog].(float64)); got != 2 {
		t.Fatalf("filtered total count = %d, want 2", got)
	}
	cursor, ok := meta["nextCursorByColumn"].(map[string]any)[store.DefaultColumnBacklog].(string)
	if !ok || cursor == "" {
		t.Fatalf("expected filtered cursor, got %#v", meta["nextCursorByColumn"])
	}

	resp3, out2 := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board_get",
		"input": map[string]any{
			"projectSlug": slug,
			"assignee":    "me",
			"tag":         "focus",
			"search":      "mine",
			"limit":       1,
			"cursorByColumn": map[string]any{
				store.DefaultColumnBacklog: cursor,
			},
		},
	})
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("board_get filtered second page expected 200, got %d body=%#v", resp3.StatusCode, out2)
	}
	backlog = boardColumnByKey(t, out2["data"].(map[string]any)["columns"].([]any), store.DefaultColumnBacklog)
	if got := len(backlog["items"].([]any)); got != 1 {
		t.Fatalf("second filtered page has %d items, want 1", got)
	}
	meta = out2["meta"].(map[string]any)
	if meta["hasMoreByColumn"].(map[string]any)[store.DefaultColumnBacklog] != false {
		t.Fatalf("expected filtered second page to be final, got %#v", meta)
	}
	if got := int(meta["totalCountByColumn"].(map[string]any)[store.DefaultColumnBacklog].(float64)); got != 2 {
		t.Fatalf("second page filtered total count = %d, want 2", got)
	}

	resp4, unassignedOut := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board_get",
		"input": map[string]any{
			"projectSlug": slug,
			"assignee":    "unassigned",
		},
	})
	if resp4.StatusCode != http.StatusOK {
		t.Fatalf("board_get unassigned expected 200, got %d body=%#v", resp4.StatusCode, unassignedOut)
	}
	backlog = boardColumnByKey(t, unassignedOut["data"].(map[string]any)["columns"].([]any), store.DefaultColumnBacklog)
	items = backlog["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["title"] != "Unassigned focus" {
		t.Fatalf("unexpected unassigned items: %#v", items)
	}

	resp5, numericOut := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board_get",
		"input": map[string]any{
			"projectSlug": slug,
			"assignee":    strconv.FormatInt(ownerID, 10),
		},
	})
	if resp5.StatusCode != http.StatusOK {
		t.Fatalf("board_get numeric assignee expected 200, got %d body=%#v", resp5.StatusCode, numericOut)
	}
	backlog = boardColumnByKey(t, numericOut["data"].(map[string]any)["columns"].([]any), store.DefaultColumnBacklog)
	if got := len(backlog["items"].([]any)); got != 3 {
		t.Fatalf("numeric assignee returned %d items, want 3", got)
	}
}

func TestMCPBoardGetAssigneeValidation(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Board Assignee Validation",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Board Assignee Validation")

	for _, raw := range []string{"abc", "0", "-1", "Unassigned", "Me", "9223372036854775808"} {
		t.Run(raw, func(t *testing.T) {
			status, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
				"tool": "board_get",
				"input": map[string]any{
					"projectSlug": slug,
					"assignee":    raw,
				},
			})
			if status.StatusCode != http.StatusBadRequest {
				t.Fatalf("assignee=%q expected 400, got %d body=%#v", raw, status.StatusCode, out)
			}
			errObj := out["error"].(map[string]any)
			if errObj["code"] != mcp.CodeValidationError || errObj["message"] != "invalid assignee" {
				t.Fatalf("unexpected error for assignee=%q: %#v", raw, errObj)
			}
			details := errObj["details"].(map[string]any)
			if details["field"] != "assignee" {
				t.Fatalf("unexpected details for assignee=%q: %#v", raw, details)
			}
		})
	}

	t.Run("JSON number is rejected", func(t *testing.T) {
		status, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
			"tool": "board_get",
			"input": map[string]any{
				"projectSlug": slug,
				"assignee":    42,
			},
		})
		if status.StatusCode != http.StatusBadRequest {
			t.Fatalf("numeric assignee expected 400, got %d body=%#v", status.StatusCode, out)
		}
		errObj := out["error"].(map[string]any)
		if errObj["code"] != mcp.CodeValidationError || errObj["message"] != "invalid assignee" {
			t.Fatalf("unexpected numeric assignee error: %#v", errObj)
		}
		details := errObj["details"].(map[string]any)
		if details["field"] != "assignee" {
			t.Fatalf("unexpected numeric assignee details: %#v", details)
		}
	})
}
