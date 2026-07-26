package mcp_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func setupProjectWithViewer(t *testing.T, ts *httptest.Server, sqlDB *sql.DB, projectName, viewerEmail string) (string, int64, *http.Client, *http.Client) {
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
	return slug, projectID, ownerClient, newSessionClientForUser(t, ts, st, viewer.ID)
}

func setupAdminUser(t *testing.T, ts *httptest.Server, sqlDB *sql.DB, email string) (*store.Store, int64, *http.Client) {
	t.Helper()
	ownerID := firstUserID(t, sqlDB)
	st := store.New(sqlDB, nil)
	admin, err := st.CreateUser(context.Background(), email, "password123", "Admin")
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}
	if err := st.UpdateUserRole(context.Background(), ownerID, admin.ID, store.SystemRoleAdmin); err != nil {
		t.Fatalf("promote admin: %v", err)
	}
	return st, admin.ID, newSessionClientForUser(t, ts, st, admin.ID)
}

func TestMCPProjectsCreateSuccess(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	resp, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "projects_create",
		"input": map[string]any{"name": "MCP Created Project"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	project := out["data"].(map[string]any)["project"].(map[string]any)
	if project["name"] != "MCP Created Project" {
		t.Fatalf("unexpected project name: %#v", project["name"])
	}
	if project["role"] != "maintainer" {
		t.Fatalf("expected maintainer role, got %#v", project["role"])
	}
	if project["projectSlug"] == nil || project["projectSlug"] == "" {
		t.Fatalf("expected projectSlug, got %#v", project["projectSlug"])
	}
}

func TestMCPProjectsUpdateTwoFieldsSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Update Two Fields",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Update Two Fields")
	projectID := projectIDBySlug(t, sqlDB, slug)

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "projects_update",
		"input": map[string]any{
			"projectSlug": slug,
			"patch": map[string]any{
				"name":               "Updated Name",
				"defaultSprintWeeks": 1,
			},
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%#v", resp2.StatusCode, out)
	}
	project := out["data"].(map[string]any)["project"].(map[string]any)
	if project["name"] != "Updated Name" {
		t.Fatalf("expected updated name, got %#v", project["name"])
	}
	if project["defaultSprintWeeks"].(float64) != 1 {
		t.Fatalf("expected defaultSprintWeeks 1, got %#v", project["defaultSprintWeeks"])
	}

	st := store.New(sqlDB, nil)
	updated, err := st.GetProject(context.Background(), projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if updated.Name != "Updated Name" || updated.DefaultSprintWeeks != 1 {
		t.Fatalf("db mismatch: name=%q weeks=%d", updated.Name, updated.DefaultSprintWeeks)
	}
}

func TestMCPProjectsUpdateAtomicityInvalidWeeks(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	slug, projectID, ownerClient, _ := setupProjectWithViewer(t, ts, sqlDB, "Atomic Update", "atomic-update@example.com")
	st := store.New(sqlDB, nil)
	before, err := st.GetProject(context.Background(), projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}

	resp, out := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool": "projects_update",
		"input": map[string]any{
			"projectSlug": slug,
			"patch": map[string]any{
				"name":               "Should Not Stick",
				"defaultSprintWeeks": 3,
			},
		},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%#v", resp.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}

	after, err := st.GetProject(context.Background(), projectID)
	if err != nil {
		t.Fatalf("GetProject after: %v", err)
	}
	if after.Name != before.Name || after.DefaultSprintWeeks != before.DefaultSprintWeeks {
		t.Fatalf("project changed after failed patch: before=%+v after=%+v", before, after)
	}
}

func TestMCPProjectsUpdateViewerForbidden(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	slug, _, _, viewerClient := setupProjectWithViewer(t, ts, sqlDB, "Update Viewer Forbidden", "proj-update-viewer@example.com")

	resp, out := doMCP(t, viewerClient, ts.URL+"/mcp", map[string]any{
		"tool": "projects_update",
		"input": map[string]any{
			"projectSlug": slug,
			"patch":       map[string]any{"name": "Nope"},
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %#v", errObj["code"])
	}
}

func TestMCPProjectsUpdateReturnsCanonicalSlug(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Canonical Slug Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Canonical Slug Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "projects_update",
		"input": map[string]any{
			"projectSlug": " " + slug + " ",
			"patch":       map[string]any{"name": "Still Canonical"},
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%#v", resp2.StatusCode, out)
	}
	project := out["data"].(map[string]any)["project"].(map[string]any)
	if project["projectSlug"] != slug {
		t.Fatalf("expected canonical slug %q, got %#v", slug, project["projectSlug"])
	}
}

func TestMCPProjectsDeleteSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Delete Me",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Delete Me")
	projectID := projectIDBySlug(t, sqlDB, slug)

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "projects_delete",
		"input": map[string]any{"projectSlug": slug},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%#v", resp2.StatusCode, out)
	}
	data := out["data"].(map[string]any)
	if data["status"] != "deleted" || data["projectSlug"] != slug {
		t.Fatalf("unexpected delete payload: %#v", data)
	}

	st := store.New(sqlDB, nil)
	if _, err := st.GetProject(context.Background(), projectID); err == nil {
		t.Fatal("expected project to be deleted")
	}
}

func TestMCPProjectsDeleteViewerForbidden(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	slug, _, _, viewerClient := setupProjectWithViewer(t, ts, sqlDB, "Delete Viewer Forbidden", "proj-delete-viewer@example.com")

	resp, out := doMCP(t, viewerClient, ts.URL+"/mcp", map[string]any{
		"tool":  "projects_delete",
		"input": map[string]any{"projectSlug": slug},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %#v", errObj["code"])
	}
}

func TestMCPProjectsDeleteRequiresAuth(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	slug, _, _, _ := setupProjectWithViewer(t, ts, sqlDB, "Delete Auth Required", "delete-auth@example.com")

	resp, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool":  "projects_delete",
		"input": map[string]any{"projectSlug": slug},
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPProjectsDeleteCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool":  "projects_delete",
		"input": map[string]any{"projectSlug": "demo"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPAdminListUsersRegularUserForbidden(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	st := store.New(sqlDB, nil)
	regular, err := st.CreateUser(context.Background(), "regular-list@example.com", "password123", "Regular")
	if err != nil {
		t.Fatalf("create regular user: %v", err)
	}
	regularClient := newSessionClientForUser(t, ts, st, regular.ID)

	resp, out := doMCP(t, regularClient, ts.URL+"/mcp", map[string]any{
		"tool":  "admin_listUsers",
		"input": map[string]any{},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%#v", resp.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %#v", errObj["code"])
	}
}

func TestMCPAdminListUsersAsAdmin(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	_, _, adminClient := setupAdminUser(t, ts, sqlDB, "admin-list@example.com")

	resp, out := doMCP(t, adminClient, ts.URL+"/mcp", map[string]any{
		"tool":  "admin_listUsers",
		"input": map[string]any{},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%#v", resp.StatusCode, out)
	}
	items := out["data"].(map[string]any)["items"].([]any)
	if len(items) < 2 {
		t.Fatalf("expected at least 2 users, got %d", len(items))
	}
}

func TestMCPAdminUpdateUserRoleOwnerSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	st := store.New(sqlDB, nil)
	target, err := st.CreateUser(context.Background(), "role-target@example.com", "password123", "Target")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	resp, out := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool": "admin_updateUserRole",
		"input": map[string]any{
			"userId": target.ID,
			"role":   "admin",
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%#v", resp.StatusCode, out)
	}
	user := out["data"].(map[string]any)["user"].(map[string]any)
	if user["systemRole"] != "admin" {
		t.Fatalf("expected admin role, got %#v", user["systemRole"])
	}
}

func TestMCPAdminUpdateUserRoleAdminForbidden(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	st := store.New(sqlDB, nil)
	target, err := st.CreateUser(context.Background(), "role-forbidden@example.com", "password123", "Target")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	_, _, adminClient := setupAdminUser(t, ts, sqlDB, "admin-role-forbidden@example.com")

	resp, out := doMCP(t, adminClient, ts.URL+"/mcp", map[string]any{
		"tool": "admin_updateUserRole",
		"input": map[string]any{
			"userId": target.ID,
			"role":   "admin",
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%#v", resp.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %#v", errObj["code"])
	}
}

func TestMCPAdminUpdateUserRolePreventsLastOwnerDemotion(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, sqlDB)

	resp, out := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool": "admin_updateUserRole",
		"input": map[string]any{
			"userId": ownerID,
			"role":   "user",
		},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%#v", resp.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPAdminDeleteUserOwnerSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	st := store.New(sqlDB, nil)
	target, err := st.CreateUser(context.Background(), "delete-target@example.com", "password123", "Delete Target")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	resp, out := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool": "admin_deleteUser",
		"input": map[string]any{
			"userId": target.ID,
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%#v", resp.StatusCode, out)
	}
	data := out["data"].(map[string]any)
	if data["status"] != "deleted" {
		t.Fatalf("expected deleted status, got %#v", data["status"])
	}
}

func TestMCPAdminDeleteUserAdminForbidden(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	st := store.New(sqlDB, nil)
	target, err := st.CreateUser(context.Background(), "delete-forbidden@example.com", "password123", "Delete Forbidden")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	_, _, adminClient := setupAdminUser(t, ts, sqlDB, "admin-delete-forbidden@example.com")

	resp, out := doMCP(t, adminClient, ts.URL+"/mcp", map[string]any{
		"tool": "admin_deleteUser",
		"input": map[string]any{
			"userId": target.ID,
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%#v", resp.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %#v", errObj["code"])
	}
}

func TestMCPAdminDeleteUserSelfRejected(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, sqlDB)

	resp, out := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool": "admin_deleteUser",
		"input": map[string]any{
			"userId": ownerID,
		},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%#v", resp.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPDashboardGetSummaryAndListTodos(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	st := store.New(sqlDB, nil)
	ctx := store.WithUserID(context.Background(), ownerID)

	p1, err := st.CreateProject(ctx, "Dashboard P1")
	if err != nil {
		t.Fatalf("CreateProject p1: %v", err)
	}
	p2, err := st.CreateProject(ctx, "Dashboard P2")
	if err != nil {
		t.Fatalf("CreateProject p2: %v", err)
	}
	for _, pid := range []int64{p1.ID, p2.ID} {
		for i := 0; i < 2; i++ {
			_, err := st.CreateTodo(ctx, pid, store.CreateTodoInput{
				Title:          "Assigned todo",
				ColumnKey:      store.DefaultColumnDoing,
				AssigneeUserID: &ownerID,
			}, store.ModeFull)
			if err != nil {
				t.Fatalf("CreateTodo: %v", err)
			}
		}
	}

	resp, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "dashboard_getSummary",
		"input": map[string]any{},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard_getSummary expected 200, got %d body=%#v", resp.StatusCode, out)
	}
	summary := out["data"].(map[string]any)["summary"].(map[string]any)
	if summary["assignedCount"].(float64) < 4 {
		t.Fatalf("expected assignedCount >= 4, got %#v", summary["assignedCount"])
	}

	resp2, out2 := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "dashboard_listTodos",
		"input": map[string]any{
			"limit": 1,
			"sort":  "board",
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("dashboard_listTodos page1 expected 200, got %d body=%#v", resp2.StatusCode, out2)
	}
	items := out2["data"].(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 item on page1, got %d", len(items))
	}
	meta := out2["meta"].(map[string]any)
	if meta["hasMore"] != true || meta["nextCursor"] == nil {
		t.Fatalf("expected hasMore and nextCursor, got meta=%#v", meta)
	}
	cursor := meta["nextCursor"].(string)

	resp3, out3 := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "dashboard_listTodos",
		"input": map[string]any{
			"limit":  10,
			"cursor": cursor,
			"sort":   "board",
		},
	})
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("dashboard_listTodos page2 expected 200, got %d body=%#v", resp3.StatusCode, out3)
	}
	items2 := out3["data"].(map[string]any)["items"].([]any)
	if len(items2) != 3 {
		t.Fatalf("expected 3 items on page2, got %d", len(items2))
	}
}

func TestMCPMetricsGetBurndownSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Metrics Burndown Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Metrics Burndown Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "metrics_getBurndown",
		"input": map[string]any{"projectSlug": slug},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%#v", resp2.StatusCode, out)
	}
	if _, ok := out["data"].(map[string]any)["points"]; !ok {
		t.Fatalf("expected points in response, got %#v", out["data"])
	}
}

func TestMCPMetricsGetBurndownSprintProjectMismatch(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Metrics Project A",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project A status=%d", resp.StatusCode)
	}
	slugA := projectSlugByName(t, sqlDB, "Metrics Project A")

	resp = doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Metrics Project B",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project B status=%d", resp.StatusCode)
	}
	slugB := projectSlugByName(t, sqlDB, "Metrics Project B")

	sprintResp, sprintOut := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints_create",
		"input": map[string]any{
			"projectSlug":    slugB,
			"name":           "Sprint B",
			"plannedStartAt": time.Now().UTC().Format(time.RFC3339),
			"plannedEndAt":   time.Now().UTC().Add(14 * 24 * time.Hour).Format(time.RFC3339),
		},
	})
	if sprintResp.StatusCode != http.StatusOK {
		t.Fatalf("sprints_create expected 200, got %d body=%#v", sprintResp.StatusCode, sprintOut)
	}
	sprintID := sprintOut["data"].(map[string]any)["sprint"].(map[string]any)["sprintId"].(float64)

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "metrics_getBurndown",
		"input": map[string]any{
			"projectSlug": slugA,
			"sprintId":    int64(sprintID),
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%#v", resp2.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPMetricsGetBacklogSizeSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Metrics Backlog Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Metrics Backlog Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "metrics_getBacklogSize",
		"input": map[string]any{"projectSlug": slug},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%#v", resp2.StatusCode, out)
	}
	if _, ok := out["data"].(map[string]any)["points"]; !ok {
		t.Fatalf("expected points in response, got %#v", out["data"])
	}
}

func setupAuthenticatedTemporaryBoard(t *testing.T, ts *httptest.Server, sqlDB *sql.DB) (slug string, projectID int64, ownerClient *http.Client, ownerID int64) {
	t.Helper()
	ownerClient = newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID = firstUserID(t, sqlDB)
	st := store.New(sqlDB, nil)
	ctx := store.WithUserID(context.Background(), ownerID)
	tempBoard, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	tempBoard, err = st.GetProject(ctx, tempBoard.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if tempBoard.CreatorUserID == nil {
		t.Fatal("expected Temporary Board owner (creator_user_id set)")
	}
	return tempBoard.Slug, tempBoard.ID, ownerClient, ownerID
}

func TestMCPProjectsUpdateTemporaryBoardOwnerSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	slug, projectID, ownerClient, _ := setupAuthenticatedTemporaryBoard(t, ts, sqlDB)

	resp, out := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool": "projects_update",
		"input": map[string]any{
			"projectSlug": slug,
			"patch":       map[string]any{"name": "Temp Updated Name"},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%#v", resp.StatusCode, out)
	}
	project := out["data"].(map[string]any)["project"].(map[string]any)
	if project["name"] != "Temp Updated Name" {
		t.Fatalf("expected updated name, got %#v", project["name"])
	}

	st := store.New(sqlDB, nil)
	updated, err := st.GetProject(context.Background(), projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if updated.Name != "Temp Updated Name" {
		t.Fatalf("db name mismatch: %q", updated.Name)
	}
}

func TestMCPProjectsDeleteTemporaryBoardOwnerSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	slug, projectID, ownerClient, _ := setupAuthenticatedTemporaryBoard(t, ts, sqlDB)

	resp, out := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool":  "projects_delete",
		"input": map[string]any{"projectSlug": slug},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%#v", resp.StatusCode, out)
	}

	st := store.New(sqlDB, nil)
	if _, err := st.GetProject(context.Background(), projectID); err == nil {
		t.Fatal("expected temporary board to be deleted")
	}
}

func TestMCPProjectsUpdateTemporaryBoardOtherUserForbidden(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	slug, _, _, _ := setupAuthenticatedTemporaryBoard(t, ts, sqlDB)
	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "temp-update-other@example.com", "password123", "Other")
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	otherClient := newSessionClientForUser(t, ts, st, other.ID)

	resp, out := doMCP(t, otherClient, ts.URL+"/mcp", map[string]any{
		"tool": "projects_update",
		"input": map[string]any{
			"projectSlug": slug,
			"patch":       map[string]any{"name": "Should Not Stick"},
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%#v", resp.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %#v", errObj["code"])
	}
}

func TestMCPProjectsDeleteTemporaryBoardOtherUserForbidden(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	slug, _, _, _ := setupAuthenticatedTemporaryBoard(t, ts, sqlDB)
	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "temp-delete-other@example.com", "password123", "Other")
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	otherClient := newSessionClientForUser(t, ts, st, other.ID)

	resp, out := doMCP(t, otherClient, ts.URL+"/mcp", map[string]any{
		"tool":  "projects_delete",
		"input": map[string]any{"projectSlug": slug},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%#v", resp.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %#v", errObj["code"])
	}
}

func TestMCPProjectsUpdateDurableNonMemberNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Non Member Update",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Non Member Update")

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "durable-nonmember@example.com", "password123", "Other")
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	otherClient := newSessionClientForUser(t, ts, st, other.ID)

	resp2, out := doMCP(t, otherClient, ts.URL+"/mcp", map[string]any{
		"tool": "projects_update",
		"input": map[string]any{
			"projectSlug": slug,
			"patch":       map[string]any{"name": "Nope"},
		},
	})
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%#v", resp2.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %#v", errObj["code"])
	}
}

func TestMCPProjectsDeleteDurableNonMemberNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Non Member Delete",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Non Member Delete")

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "durable-delete-nonmember@example.com", "password123", "Other")
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	otherClient := newSessionClientForUser(t, ts, st, other.ID)

	resp2, out := doMCP(t, otherClient, ts.URL+"/mcp", map[string]any{
		"tool":  "projects_delete",
		"input": map[string]any{"projectSlug": slug},
	})
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%#v", resp2.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %#v", errObj["code"])
	}
}

func TestMCPProjectsUpdateAnonymousBoardNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	st := store.New(sqlDB, nil)
	anonBoard, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	if anonBoard.CreatorUserID != nil {
		t.Fatal("expected Anonymous Board without Temporary Board owner (creator_user_id NULL)")
	}

	resp, out := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool": "projects_update",
		"input": map[string]any{
			"projectSlug": anonBoard.Slug,
			"patch":       map[string]any{"name": "Should Not Stick"},
		},
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%#v", resp.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %#v", errObj["code"])
	}
}

func TestMCPProjectsDeleteAnonymousBoardNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	st := store.New(sqlDB, nil)
	anonBoard, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}

	resp, out := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool":  "projects_delete",
		"input": map[string]any{"projectSlug": anonBoard.Slug},
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%#v", resp.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %#v", errObj["code"])
	}
}
