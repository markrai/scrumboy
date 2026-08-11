package mcp_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func TestMCPBoardGetSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Board Get Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	slug := projectSlugByName(t, sqlDB, "Board Get Project")
	projectID := projectIDBySlug(t, sqlDB, slug)
	ownerID := firstUserID(t, sqlDB)
	st := store.New(sqlDB, nil)
	ctx := store.WithUserID(context.Background(), ownerID)
	if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
		Title:     "Backlog todo",
		ColumnKey: store.DefaultColumnBacklog,
		Tags:      []string{"bug"},
	}, store.ModeFull); err != nil {
		t.Fatalf("create backlog todo: %v", err)
	}
	if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
		Title:     "Doing todo",
		ColumnKey: store.DefaultColumnDoing,
	}, store.ModeFull); err != nil {
		t.Fatalf("create doing todo: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board_get",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}

	data := out["data"].(map[string]any)
	project := data["project"].(map[string]any)
	if project["projectSlug"] != slug || project["name"] != "Board Get Project" || project["role"] != "maintainer" {
		t.Fatalf("unexpected project shape: %#v", project)
	}
	if _, ok := project["projectId"]; ok {
		t.Fatalf("board project should not expose projectId: %#v", project)
	}

	columns := data["columns"].([]any)
	backlog := boardColumnByKey(t, columns, store.DefaultColumnBacklog)
	items := backlog["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected one backlog item, got %#v", items)
	}
	item := items[0].(map[string]any)
	if item["projectSlug"] != slug || item["title"] != "Backlog todo" || item["columnKey"] != store.DefaultColumnBacklog {
		t.Fatalf("unexpected board todo item: %#v", item)
	}
	if _, ok := item["id"]; ok {
		t.Fatalf("board item should not expose global todo id: %#v", item)
	}

	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("expected meta object, got %#v", out["meta"])
	}
}

func TestMCPBoardGetFilters(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Board Filter Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	slug := projectSlugByName(t, sqlDB, "Board Filter Project")
	projectID := projectIDBySlug(t, sqlDB, slug)
	ownerID := firstUserID(t, sqlDB)
	st := store.New(sqlDB, nil)
	ctx := store.WithUserID(context.Background(), ownerID)
	if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
		Title:     "Fix login bug",
		ColumnKey: store.DefaultColumnBacklog,
		Tags:      []string{"bug"},
	}, store.ModeFull); err != nil {
		t.Fatalf("create matching todo: %v", err)
	}
	if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
		Title:     "Fix login copy",
		ColumnKey: store.DefaultColumnBacklog,
		Tags:      []string{"docs"},
	}, store.ModeFull); err != nil {
		t.Fatalf("create non-matching todo: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board_get",
		"input": map[string]any{
			"projectSlug": slug,
			"tag":         "bug",
			"search":      "login",
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	backlog := boardColumnByKey(t, out["data"].(map[string]any)["columns"].([]any), store.DefaultColumnBacklog)
	items := backlog["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["title"] != "Fix login bug" {
		t.Fatalf("unexpected filtered items: %#v", items)
	}
}

func TestMCPBoardGetColumnKeyFilter(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Board Column Filter Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	slug := projectSlugByName(t, sqlDB, "Board Column Filter Project")
	projectID := projectIDBySlug(t, sqlDB, slug)
	ownerID := firstUserID(t, sqlDB)
	st := store.New(sqlDB, nil)
	ctx := store.WithUserID(context.Background(), ownerID)
	if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
		Title:     "Backlog todo",
		ColumnKey: store.DefaultColumnBacklog,
	}, store.ModeFull); err != nil {
		t.Fatalf("create backlog todo: %v", err)
	}
	if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
		Title:     "Doing todo",
		ColumnKey: store.DefaultColumnDoing,
	}, store.ModeFull); err != nil {
		t.Fatalf("create doing todo: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board_get",
		"input": map[string]any{
			"projectSlug": slug,
			"columnKey":   store.DefaultColumnBacklog,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}

	columns := out["data"].(map[string]any)["columns"].([]any)
	if len(columns) != 1 {
		t.Fatalf("expected exactly one column in the response, got %#v", columns)
	}
	backlog := boardColumnByKey(t, columns, store.DefaultColumnBacklog)
	items := backlog["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["title"] != "Backlog todo" {
		t.Fatalf("unexpected backlog items: %#v", items)
	}

	meta := out["meta"].(map[string]any)
	totalCountByColumn := meta["totalCountByColumn"].(map[string]any)
	if len(totalCountByColumn) != 1 {
		t.Fatalf("expected totalCountByColumn to be scoped to the requested column, got %#v", totalCountByColumn)
	}
}

func TestMCPBoardGetSprintFilter(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Board Sprint Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	slug := projectSlugByName(t, sqlDB, "Board Sprint Project")
	projectID := projectIDBySlug(t, sqlDB, slug)
	ownerID := firstUserID(t, sqlDB)
	st := store.New(sqlDB, nil)
	ctx := store.WithUserID(context.Background(), ownerID)
	sp, err := st.CreateSprint(ctx, projectID, "Sprint 1", time.UnixMilli(1000), time.UnixMilli(2000))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}
	if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
		Title:     "In sprint",
		ColumnKey: store.DefaultColumnBacklog,
		SprintID:  &sp.ID,
	}, store.ModeFull); err != nil {
		t.Fatalf("create sprint todo: %v", err)
	}
	if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
		Title:     "Outside sprint",
		ColumnKey: store.DefaultColumnBacklog,
	}, store.ModeFull); err != nil {
		t.Fatalf("create unscheduled todo: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board_get",
		"input": map[string]any{
			"projectSlug": slug,
			"sprintId":    sp.ID,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	backlog := boardColumnByKey(t, out["data"].(map[string]any)["columns"].([]any), store.DefaultColumnBacklog)
	items := backlog["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["title"] != "In sprint" {
		t.Fatalf("unexpected sprint-filtered items: %#v", items)
	}
}
