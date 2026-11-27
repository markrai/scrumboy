package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"scrumboy/internal/store"
)

func creatorUpdateRESTURL(baseURL string, project store.Project, todo store.Todo) string {
	return fmt.Sprintf("%s/api/board/%s/todos/%d", baseURL, project.Slug, todo.LocalID)
}

func creatorUpdateRESTPayload(todo store.Todo, creator any) map[string]any {
	payload := todoUpdateRESTPayload(todo.Title, todo.Body, todo.Tags, todo.EstimationPoints, todo.AssigneeUserID)
	payload["createdByUserId"] = creator
	return payload
}

func creatorUpdateAuditCount(t *testing.T, db *sql.DB, todoID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'todo_creator_changed' AND target_id = ?`, todoID).Scan(&count); err != nil {
		t.Fatalf("count creator audits: %v", err)
	}
	return count
}

func TestTodoCreatorUpdateRESTContract(t *testing.T) {
	ts, db, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody:        1 << 20,
		ScrumboyMode:          "full",
		PublicProjectsEnabled: true,
	})
	defer cleanup()
	st := wireTodoUpdatePublisher(t, ts)
	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "rest-creator-owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	project, ctx := createTodoUpdateProject(t, st, ownerID, "REST creator contract")
	todo := createTodoUpdateTodo(t, st, ctx, project.ID, "original title", nil)
	target, err := st.CreateUser(context.Background(), "rest-creator-target@example.com", "password123", "Historical creator")
	if err != nil {
		t.Fatalf("create nonmember target: %v", err)
	}

	payload := creatorUpdateRESTPayload(todo, target.ID)
	var updated map[string]any
	resp, body := doJSON(t, ownerClient, http.MethodPatch, creatorUpdateRESTURL(ts.URL, project, todo), payload, &updated)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("creator update status=%d body=%s", resp.StatusCode, body)
	}
	if updated["createdByUserId"] != float64(target.ID) {
		t.Fatalf("response createdByUserId=%v want=%d", updated["createdByUserId"], target.ID)
	}
	persisted, err := st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, store.ModeFull)
	if err != nil {
		t.Fatalf("reload creator update: %v", err)
	}
	if persisted.CreatedByUserID == nil || *persisted.CreatedByUserID != target.ID {
		t.Fatalf("persisted creator=%v want=%d", persisted.CreatedByUserID, target.ID)
	}
	if role, err := st.GetProjectRole(ctx, project.ID, target.ID); err != nil || role != "" {
		t.Fatalf("nonmember target role=%q err=%v", role, err)
	}
	if got := creatorUpdateAuditCount(t, db, todo.ID); got != 1 {
		t.Fatalf("creator audit count=%d want=1", got)
	}

	resp, body = doJSON(t, ownerClient, http.MethodPatch, creatorUpdateRESTURL(ts.URL, project, todo), creatorUpdateRESTPayload(persisted, target.ID), &updated)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("same-value creator status=%d body=%s", resp.StatusCode, body)
	}
	if got := creatorUpdateAuditCount(t, db, todo.ID); got != 1 {
		t.Fatalf("same-value creator created an audit: %d", got)
	}

	omitted := todoUpdateRESTPayload("ordinary update", persisted.Body, persisted.Tags, persisted.EstimationPoints, persisted.AssigneeUserID)
	resp, body = doJSON(t, ownerClient, http.MethodPatch, creatorUpdateRESTURL(ts.URL, project, todo), omitted, &updated)
	if resp.StatusCode != http.StatusOK || updated["createdByUserId"] != float64(target.ID) {
		t.Fatalf("omitted creator status=%d body=%s response=%+v", resp.StatusCode, body, updated)
	}

	if _, err := st.UpdateProjectPublicViewing(ctx, project.ID, ownerID, true); err != nil {
		t.Fatalf("publish project: %v", err)
	}
	var public map[string]any
	resp, body = doJSON(t, ts.Client(), http.MethodGet, fmt.Sprintf("%s/api/public/board/%s/todos/%d", ts.URL, project.Slug, todo.LocalID), nil, &public)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("public todo status=%d body=%s", resp.StatusCode, body)
	}
	publicTodo := public["todo"].(map[string]any)
	if _, exists := publicTodo["createdByUserId"]; exists {
		t.Fatalf("public projection disclosed creator: %+v", publicTodo)
	}

	legacyPayload := todoUpdateRESTPayload("legacy ordinary update", persisted.Body, persisted.Tags, persisted.EstimationPoints, persisted.AssigneeUserID)
	legacyPayload["createdByUserId"] = ownerID
	resp, body = doJSON(t, ownerClient, http.MethodPatch, fmt.Sprintf("%s/api/todos/%d", ts.URL, todo.ID), legacyPayload, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy update status=%d body=%s", resp.StatusCode, body)
	}
	persisted, err = st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, store.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.CreatedByUserID == nil || *persisted.CreatedByUserID != target.ID {
		t.Fatalf("legacy route exposed creator mutation: %v", persisted.CreatedByUserID)
	}
}

func TestTodoCreatorUpdateRESTInvalidValuesAreAtomic(t *testing.T) {
	ts, db, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	st := wireTodoUpdatePublisher(t, ts)
	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "rest-creator-invalid-owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	project, ctx := createTodoUpdateProject(t, st, ownerID, "REST creator validation")
	todo := createTodoUpdateTodo(t, st, ctx, project.ID, "unchanged title", nil)

	cases := []struct {
		name  string
		value any
	}{
		{name: "null", value: nil},
		{name: "zero", value: 0},
		{name: "negative", value: -1},
		{name: "fraction", value: 1.5},
		{name: "string", value: "1"},
		{name: "boolean", value: true},
		{name: "object", value: map[string]any{"id": 1}},
		{name: "array", value: []any{1}},
		{name: "overflow", value: json.Number("9223372036854775808")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := creatorUpdateRESTPayload(todo, tc.value)
			payload["title"] = "must not persist"
			var envelope apiErrorEnvelope
			resp, body := doJSON(t, ownerClient, http.MethodPatch, creatorUpdateRESTURL(ts.URL, project, todo), payload, &envelope)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", resp.StatusCode, body)
			}
			assertAPIError(t, envelope, "VALIDATION_ERROR", "createdByUserId", "invalid_created_by_user_id")
			persisted, err := st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, store.ModeFull)
			if err != nil {
				t.Fatal(err)
			}
			if persisted.Title != todo.Title || persisted.CreatedByUserID == nil || *persisted.CreatedByUserID != ownerID {
				t.Fatalf("invalid combined patch partially persisted: %+v", persisted)
			}
		})
	}

	payload := creatorUpdateRESTPayload(todo, int64(999999))
	payload["title"] = "must not persist"
	resp, body := doJSON(t, ownerClient, http.MethodPatch, creatorUpdateRESTURL(ts.URL, project, todo), payload, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("nonexistent creator status=%d body=%s", resp.StatusCode, body)
	}
	persisted, err := st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, store.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Title != todo.Title || creatorUpdateAuditCount(t, db, todo.ID) != 0 {
		t.Fatalf("nonexistent creator partially persisted: %+v", persisted)
	}
}

func TestTodoCreatorUpdateRESTAuthorizationAndLifecycleGuards(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	st := wireTodoUpdatePublisher(t, ts)
	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "rest-creator-auth-owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	project, ctx := createTodoUpdateProject(t, st, ownerID, "REST creator authorization")
	contributorID, contributorClient := createUserAPI(t, ownerClient, ts.URL, "Contributor", "rest-creator-contributor@example.com", "password123")
	viewerID, viewerClient := createUserAPI(t, ownerClient, ts.URL, "Viewer", "rest-creator-viewer@example.com", "password123")
	if err := st.AddProjectMember(ctx, ownerID, project.ID, contributorID, store.RoleContributor); err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectMember(ctx, ownerID, project.ID, viewerID, store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	todo := createTodoUpdateTodo(t, st, ctx, project.ID, "authorization title", &contributorID)

	for _, tc := range []struct {
		name       string
		client     *http.Client
		creatorID  int64
		wantStatus int
	}{
		{name: "assigned contributor same value", client: contributorClient, creatorID: ownerID, wantStatus: http.StatusForbidden},
		{name: "assigned contributor different value", client: contributorClient, creatorID: contributorID, wantStatus: http.StatusForbidden},
		{name: "viewer", client: viewerClient, creatorID: viewerID, wantStatus: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := creatorUpdateRESTPayload(todo, tc.creatorID)
			payload["body"] = "must not persist"
			resp, body := doJSON(t, tc.client, http.MethodPatch, creatorUpdateRESTURL(ts.URL, project, todo), payload, nil)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", resp.StatusCode, tc.wantStatus, body)
			}
			persisted, err := st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, store.ModeFull)
			if err != nil {
				t.Fatal(err)
			}
			if persisted.Body != todo.Body || persisted.CreatedByUserID == nil || *persisted.CreatedByUserID != ownerID {
				t.Fatalf("unauthorized combined patch partially persisted: %+v", persisted)
			}
		})
	}

	temporary, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	temporaryTodo := createTodoUpdateTodo(t, st, ctx, temporary.ID, "temporary", nil)
	resp, body := doJSON(t, ownerClient, http.MethodPatch, creatorUpdateRESTURL(ts.URL, temporary, temporaryTodo), creatorUpdateRESTPayload(temporaryTodo, ownerID), nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("temporary creator update status=%d body=%s", resp.StatusCode, body)
	}

	archived := createTodoUpdateTodo(t, st, ctx, project.ID, "archived", nil)
	if _, err := st.ArchiveTodosByLocalID(ctx, project.ID, []int64{archived.LocalID}, store.ModeFull); err != nil {
		t.Fatal(err)
	}
	resp, body = doJSON(t, ownerClient, http.MethodPatch, creatorUpdateRESTURL(ts.URL, project, archived), creatorUpdateRESTPayload(archived, ownerID), nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("archived creator update status=%d body=%s", resp.StatusCode, body)
	}
}
