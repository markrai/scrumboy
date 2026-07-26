package mcp_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"scrumboy/internal/store"
)

func TestMCPWorkflowListDefaultColumns(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Workflow List Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Workflow List Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "workflow_list",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	items := out["data"].(map[string]any)["items"].([]any)
	if len(items) != 5 {
		t.Fatalf("expected 5 default columns, got %#v", items)
	}
	first := items[0].(map[string]any)
	if first["key"] != "backlog" || first["system"] != true {
		t.Fatalf("unexpected first column: %#v", first)
	}
	last := items[len(items)-1].(map[string]any)
	if last["isDone"] != true {
		t.Fatalf("expected last column to be done, got %#v", last)
	}
}

func TestMCPWorkflowCreateSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Workflow Create Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Workflow Create Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "workflow_create",
		"input": map[string]any{
			"projectSlug": slug,
			"name":        "Code Review",
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%#v", resp2.StatusCode, out)
	}
	col := out["data"].(map[string]any)["column"].(map[string]any)
	if col["name"] != "Code Review" || col["key"] != "code_review" || col["isDone"] != false || col["system"] != false {
		t.Fatalf("unexpected column: %#v", col)
	}

	resp3, listOut := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "workflow_list",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("workflow_list status=%d", resp3.StatusCode)
	}
	items := listOut["data"].(map[string]any)["items"].([]any)
	if len(items) != 6 {
		t.Fatalf("expected 6 columns after create, got %#v", items)
	}
	// New column must be inserted before the done column, not appended at the end.
	last := items[len(items)-1].(map[string]any)
	if last["isDone"] != true {
		t.Fatalf("expected done column to remain last, got %#v", last)
	}
}

func TestMCPWorkflowCreatePermissionFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Workflow Permission Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	projSlug := projectSlugByName(t, sqlDB, "Workflow Permission Project")
	projectID := projectIDBySlug(t, sqlDB, projSlug)

	st := store.New(sqlDB, nil)
	viewer, err := st.CreateUser(context.Background(), "workflow-viewer@example.com", "password123", "Viewer")
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, viewer.ID, store.RoleViewer); err != nil {
		t.Fatalf("add viewer membership: %v", err)
	}
	viewerClient := newSessionClientForUser(t, ts, st, viewer.ID)

	resp2, out := doMCP(t, viewerClient, ts.URL+"/mcp", map[string]any{
		"tool": "workflow_create",
		"input": map[string]any{
			"projectSlug": projSlug,
			"name":        "Should Fail",
		},
	})
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %#v", errObj["code"])
	}
}

func TestMCPWorkflowUpdateSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Workflow Update Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Workflow Update Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "workflow_update",
		"input": map[string]any{
			"projectSlug": slug,
			"columnKey":   "doing",
			"name":        "Building",
			"color":       "#123456",
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%#v", resp2.StatusCode, out)
	}
	col := out["data"].(map[string]any)["column"].(map[string]any)
	if col["name"] != "Building" || col["color"] != "#123456" || col["key"] != "doing" {
		t.Fatalf("unexpected column: %#v", col)
	}
}

func TestMCPWorkflowUpdateInvalidColor(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Workflow Update Invalid Color Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Workflow Update Invalid Color Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "workflow_update",
		"input": map[string]any{
			"projectSlug": slug,
			"columnKey":   "doing",
			"name":        "Building",
			"color":       "not-a-color",
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%#v", resp2.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPWorkflowDeleteSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Workflow Delete Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Workflow Delete Project")

	resp2, createOut := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "workflow_create",
		"input": map[string]any{
			"projectSlug": slug,
			"name":        "Temp Lane",
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("workflow_create status=%d", resp2.StatusCode)
	}
	key := createOut["data"].(map[string]any)["column"].(map[string]any)["key"].(string)

	resp3, delOut := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "workflow_delete",
		"input": map[string]any{
			"projectSlug": slug,
			"columnKey":   key,
		},
	})
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%#v", resp3.StatusCode, delOut)
	}
	deleted := delOut["data"].(map[string]any)["deleted"].(map[string]any)
	if deleted["columnKey"] != key {
		t.Fatalf("unexpected deleted response: %#v", deleted)
	}

	resp4, listOut := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "workflow_list",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp4.StatusCode != http.StatusOK {
		t.Fatalf("workflow_list status=%d", resp4.StatusCode)
	}
	items := listOut["data"].(map[string]any)["items"].([]any)
	for _, it := range items {
		if it.(map[string]any)["key"] == key {
			t.Fatalf("expected column %q to be deleted, still present: %#v", key, items)
		}
	}
}

func TestMCPWorkflowDeleteDoneColumnRejected(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Workflow Delete Done Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Workflow Delete Done Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "workflow_delete",
		"input": map[string]any{
			"projectSlug": slug,
			"columnKey":   "done",
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 deleting the done column, got %d, body=%#v", resp2.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

// setupWorkflowProjectWithViewer bootstraps an owner, creates a project, and adds a
// separate viewer member. It returns the project slug and a session client for the viewer.
func setupWorkflowProjectWithViewer(t *testing.T, ts *httptest.Server, sqlDB *sql.DB, projectName, viewerEmail string) (string, *http.Client) {
	t.Helper()
	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": projectName,
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, projectName)
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	viewer, err := st.CreateUser(context.Background(), viewerEmail, "password123", "Viewer")
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, viewer.ID, store.RoleViewer); err != nil {
		t.Fatalf("add viewer membership: %v", err)
	}
	return slug, newSessionClientForUser(t, ts, st, viewer.ID)
}

func TestMCPWorkflowUpdateViewerForbidden(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	slug, viewerClient := setupWorkflowProjectWithViewer(t, ts, sqlDB, "Workflow Update Viewer Project", "workflow-update-viewer@example.com")

	resp, out := doMCP(t, viewerClient, ts.URL+"/mcp", map[string]any{
		"tool": "workflow_update",
		"input": map[string]any{
			"projectSlug": slug,
			"columnKey":   "doing",
			"name":        "Should Fail",
			"color":       "#123456",
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d, body=%#v", resp.StatusCode, out)
	}
	if code := out["error"].(map[string]any)["code"]; code != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %#v", code)
	}
}

func TestMCPWorkflowDeleteViewerForbidden(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	slug, viewerClient := setupWorkflowProjectWithViewer(t, ts, sqlDB, "Workflow Delete Viewer Project", "workflow-delete-viewer@example.com")

	resp, out := doMCP(t, viewerClient, ts.URL+"/mcp", map[string]any{
		"tool": "workflow_delete",
		"input": map[string]any{
			"projectSlug": slug,
			"columnKey":   "doing",
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d, body=%#v", resp.StatusCode, out)
	}
	if code := out["error"].(map[string]any)["code"]; code != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %#v", code)
	}
}

func TestMCPWorkflowNonMemberNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Workflow Non Member Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Workflow Non Member Project")

	st := store.New(sqlDB, nil)
	stranger, err := st.CreateUser(context.Background(), "workflow-stranger@example.com", "password123", "Stranger")
	if err != nil {
		t.Fatalf("create non-member: %v", err)
	}
	strangerClient := newSessionClientForUser(t, ts, st, stranger.ID)

	respList, outList := doMCP(t, strangerClient, ts.URL+"/mcp", map[string]any{
		"tool": "workflow_list",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if respList.StatusCode != http.StatusNotFound {
		t.Fatalf("expected non-member workflow_list 404, got %d body=%#v", respList.StatusCode, outList)
	}
	if code := outList["error"].(map[string]any)["code"]; code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND for non-member workflow_list, got %#v", code)
	}

	respCreate, outCreate := doMCP(t, strangerClient, ts.URL+"/mcp", map[string]any{
		"tool": "workflow_create",
		"input": map[string]any{
			"projectSlug": slug,
			"name":        "Should Not Appear",
		},
	})
	if respCreate.StatusCode != http.StatusNotFound {
		t.Fatalf("expected non-member workflow_create 404, got %d body=%#v", respCreate.StatusCode, outCreate)
	}
	if code := outCreate["error"].(map[string]any)["code"]; code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND for non-member workflow_create, got %#v", code)
	}
}

func TestMCPWorkflowDeleteNonEmptyColumnRejected(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Workflow Delete Non Empty Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Workflow Delete Non Empty Project")

	respCreate, createOut := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "workflow_create",
		"input": map[string]any{
			"projectSlug": slug,
			"name":        "Occupied Lane",
		},
	})
	if respCreate.StatusCode != http.StatusOK {
		t.Fatalf("workflow_create status=%d", respCreate.StatusCode)
	}
	key := createOut["data"].(map[string]any)["column"].(map[string]any)["key"].(string)

	respTodo, todoOut := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos_create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Blocks deletion",
			"columnKey":   key,
		},
	})
	if respTodo.StatusCode != http.StatusOK {
		t.Fatalf("todos_create status=%d, body=%#v", respTodo.StatusCode, todoOut)
	}

	respDel, delOut := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "workflow_delete",
		"input": map[string]any{
			"projectSlug": slug,
			"columnKey":   key,
		},
	})
	if respDel.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 deleting a non-empty column, got %d, body=%#v", respDel.StatusCode, delOut)
	}
	if code := delOut["error"].(map[string]any)["code"]; code != "CONFLICT" {
		t.Fatalf("expected CONFLICT, got %#v", code)
	}
}
