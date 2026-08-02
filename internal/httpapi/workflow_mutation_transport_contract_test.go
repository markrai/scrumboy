package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"scrumboy/internal/store"
)

type workflowRESTFixture struct {
	ts      *httptest.Server
	st      *store.Store
	client  *http.Client
	ownerID int64
	ctx     context.Context
	project store.Project
}

func newWorkflowRESTFixture(t *testing.T, name string) *workflowRESTFixture {
	t.Helper()
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	t.Cleanup(cleanup)
	client := newCookieClient(t)
	owner := bootstrapUserClient(t, client, ts.URL, "Workflow Owner", name+"@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	ctx := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, nil)
	project, err := st.CreateProject(ctx, name)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return &workflowRESTFixture{ts: ts, st: st, client: client, ownerID: ownerID, ctx: ctx, project: project}
}

func workflowColumnByKey(t *testing.T, columns []store.WorkflowColumn, key string) (store.WorkflowColumn, bool) {
	t.Helper()
	for _, column := range columns {
		if column.Key == key {
			return column, true
		}
	}
	return store.WorkflowColumn{}, false
}

func assertWorkflowRESTRefresh(t *testing.T, events []todoUpdateWireEvent, projectID int64, reason string) {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("workflow event count=%d want=1; events=%+v", len(events), events)
	}
	event := events[0]
	if event.Type != "refresh_needed" || event.ProjectID != projectID || event.Reason != reason {
		t.Fatalf("workflow refresh mismatch: want project=%d reason=%q; event=%+v", projectID, reason, event)
	}
}

func TestWorkflowMutationRESTSuccessRefreshContracts(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		fx := newWorkflowRESTFixture(t, "workflow-rest-create")
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

		var created workflowColumnJSON
		resp, body := doJSON(t, fx.client, http.MethodPost, fx.ts.URL+"/api/board/"+fx.project.Slug+"/workflow", map[string]any{
			"name": "  Code Review  ",
		}, &created)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create status=%d body=%s", resp.StatusCode, body)
		}
		if created.Key != "code_review" || created.Name != "Code Review" || created.IsDone || created.Position != 4 {
			t.Fatalf("created column=%+v", created)
		}
		columns, err := fx.st.GetProjectWorkflow(fx.ctx, fx.project.ID)
		if err != nil {
			t.Fatalf("get workflow: %v", err)
		}
		persisted, ok := workflowColumnByKey(t, columns, created.Key)
		if !ok || persisted.Name != created.Name {
			t.Fatalf("created column not persisted as expected: response=%+v columns=%+v", created, columns)
		}
		assertWorkflowRESTRefresh(t, collectTodoUpdateEvents(t, stream), fx.project.ID, "workflow_column_added")
	})

	t.Run("update", func(t *testing.T) {
		fx := newWorkflowRESTFixture(t, "workflow-rest-update")
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

		resp, body := doJSON(t, fx.client, http.MethodPatch, fx.ts.URL+"/api/board/"+fx.project.Slug+"/workflow/"+store.DefaultColumnDoing, map[string]any{
			"name":  "  Building  ",
			"color": "  #123456  ",
		}, nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("update status=%d body=%s", resp.StatusCode, body)
		}
		columns, err := fx.st.GetProjectWorkflow(fx.ctx, fx.project.ID)
		if err != nil {
			t.Fatalf("get workflow: %v", err)
		}
		persisted, ok := workflowColumnByKey(t, columns, store.DefaultColumnDoing)
		if !ok || persisted.Name != "Building" || persisted.Color != "#123456" {
			t.Fatalf("updated column=%+v found=%v", persisted, ok)
		}
		assertWorkflowRESTRefresh(t, collectTodoUpdateEvents(t, stream), fx.project.ID, "workflow_column_updated")
	})

	t.Run("delete", func(t *testing.T) {
		fx := newWorkflowRESTFixture(t, "workflow-rest-delete")
		column, err := fx.st.AddWorkflowColumn(fx.ctx, fx.project.ID, "Disposable")
		if err != nil {
			t.Fatalf("add delete fixture: %v", err)
		}
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

		resp, body := doJSON(t, fx.client, http.MethodDelete, fx.ts.URL+"/api/board/"+fx.project.Slug+"/workflow/"+column.Key, nil, nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("delete status=%d body=%s", resp.StatusCode, body)
		}
		columns, err := fx.st.GetProjectWorkflow(fx.ctx, fx.project.ID)
		if err != nil {
			t.Fatalf("get workflow: %v", err)
		}
		if _, ok := workflowColumnByKey(t, columns, column.Key); ok {
			t.Fatalf("deleted column %q still present: %+v", column.Key, columns)
		}
		assertWorkflowRESTRefresh(t, collectTodoUpdateEvents(t, stream), fx.project.ID, "workflow_column_deleted")
	})
}

func TestWorkflowMutationRESTFailureSilence(t *testing.T) {
	t.Run("validation", func(t *testing.T) {
		fx := newWorkflowRESTFixture(t, "workflow-rest-validation-silence")
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

		resp, body := doJSON(t, fx.client, http.MethodPost, fx.ts.URL+"/api/board/"+fx.project.Slug+"/workflow", map[string]any{"name": "   "}, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("validation status=%d body=%s", resp.StatusCode, body)
		}
		if events := collectTodoUpdateEvents(t, stream); len(events) != 0 {
			t.Fatalf("validation emitted workflow events: %+v", events)
		}
	})

	t.Run("authorization", func(t *testing.T) {
		fx := newWorkflowRESTFixture(t, "workflow-rest-auth-silence")
		contributor, err := fx.st.CreateUser(context.Background(), "workflow-rest-contributor@example.com", "password123", "Contributor")
		if err != nil {
			t.Fatalf("create contributor: %v", err)
		}
		if err := fx.st.AddProjectMember(fx.ctx, fx.ownerID, fx.project.ID, contributor.ID, store.RoleContributor); err != nil {
			t.Fatalf("add contributor: %v", err)
		}
		contributorClient := newCookieClient(t)
		loginUserClient(t, contributorClient, fx.ts.URL, contributor.Email, "password123")
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

		resp, body := doJSON(t, contributorClient, http.MethodPatch, fx.ts.URL+"/api/board/"+fx.project.Slug+"/workflow/"+store.DefaultColumnDoing, map[string]any{
			"name":  "Forbidden",
			"color": "#123456",
		}, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("authorization status=%d body=%s", resp.StatusCode, body)
		}
		if events := collectTodoUpdateEvents(t, stream); len(events) != 0 {
			t.Fatalf("authorization failure emitted workflow events: %+v", events)
		}
	})

	t.Run("store rejection", func(t *testing.T) {
		fx := newWorkflowRESTFixture(t, "workflow-rest-store-silence")
		column, err := fx.st.AddWorkflowColumn(fx.ctx, fx.project.ID, "Occupied")
		if err != nil {
			t.Fatalf("add occupied lane: %v", err)
		}
		if _, err := fx.st.CreateTodo(fx.ctx, fx.project.ID, store.CreateTodoInput{Title: "Blocks deletion", ColumnKey: column.Key}, store.ModeFull); err != nil {
			t.Fatalf("create todo in occupied lane: %v", err)
		}
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

		resp, body := doJSON(t, fx.client, http.MethodDelete, fx.ts.URL+"/api/board/"+fx.project.Slug+"/workflow/"+column.Key, nil, nil)
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("store rejection status=%d body=%s", resp.StatusCode, body)
		}
		if events := collectTodoUpdateEvents(t, stream); len(events) != 0 {
			t.Fatalf("store rejection emitted workflow events: %+v", events)
		}
	})
}

func TestWorkflowMutationRESTAccessRoleAndBodyPrecedence(t *testing.T) {
	fx := newWorkflowRESTFixture(t, "workflow-rest-precedence")
	contributor, err := fx.st.CreateUser(context.Background(), "workflow-rest-precedence-contributor@example.com", "password123", "Contributor")
	if err != nil {
		t.Fatalf("create contributor: %v", err)
	}
	if err := fx.st.AddProjectMember(fx.ctx, fx.ownerID, fx.project.ID, contributor.ID, store.RoleContributor); err != nil {
		t.Fatalf("add contributor: %v", err)
	}
	contributorClient := newCookieClient(t)
	loginUserClient(t, contributorClient, fx.ts.URL, contributor.Email, "password123")

	tests := []struct {
		name     string
		client   *http.Client
		slug     string
		wantHTTP int
		wantCode string
	}{
		{name: "inaccessible project wins before body", client: fx.client, slug: "missing-workflow-board", wantHTTP: http.StatusNotFound, wantCode: "NOT_FOUND"},
		{name: "maintainer role wins before body", client: contributorClient, slug: fx.project.Slug, wantHTTP: http.StatusForbidden, wantCode: "FORBIDDEN"},
		{name: "authorized actor reaches body validation", client: fx.client, slug: fx.project.Slug, wantHTTP: http.StatusBadRequest, wantCode: "VALIDATION_ERROR"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var envelope apiErrorEnvelope
			resp, body := doJSON(t, tc.client, http.MethodPost, fx.ts.URL+"/api/board/"+tc.slug+"/workflow", "not-an-object", &envelope)
			if resp.StatusCode != tc.wantHTTP || envelope.Error.Code != tc.wantCode {
				t.Fatalf("status=%d code=%q want status=%d code=%q body=%s", resp.StatusCode, envelope.Error.Code, tc.wantHTTP, tc.wantCode, body)
			}
		})
	}
}

func TestWorkflowMutationRESTBoardModeContracts(t *testing.T) {
	t.Run("Temporary Board creator is forbidden because workflow requires durable membership", func(t *testing.T) {
		ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
		t.Cleanup(cleanup)
		client := newCookieClient(t)
		owner := bootstrapUserClient(t, client, ts.URL, "Temporary Creator", "workflow-temp-creator@example.com", "password123")
		ownerID := int64(owner["id"].(float64))
		st := store.New(sqlDB, nil)
		board, err := st.CreateAnonymousBoard(store.WithUserID(context.Background(), ownerID))
		if err != nil {
			t.Fatalf("create Temporary Board: %v", err)
		}

		resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+board.Slug+"/workflow", map[string]any{"name": "Not Allowed"}, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("Temporary Board workflow status=%d want=403 body=%s", resp.StatusCode, body)
		}
	})

	t.Run("Anonymous Board signed-out caller is unauthorized", func(t *testing.T) {
		ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
		t.Cleanup(cleanup)
		st := store.New(sqlDB, nil)
		board, err := st.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatalf("create Anonymous Board: %v", err)
		}

		resp, body := doJSON(t, ts.Client(), http.MethodPost, ts.URL+"/api/board/"+board.Slug+"/workflow", map[string]any{"name": "Not Allowed"}, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Anonymous Board workflow status=%d want=401 body=%s", resp.StatusCode, body)
		}
	})
}
