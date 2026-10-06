package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	projectapp "scrumboy/internal/application/project"
	"scrumboy/internal/store"
)

type projectLifecycleRESTCreationRecorder struct {
	calls       int
	ctx         context.Context
	name        string
	workflow    []store.WorkflowColumn
	workflowNil bool
	result      store.Project
	err         error
}

func (r *projectLifecycleRESTCreationRecorder) CreateProjectWithWorkflow(
	ctx context.Context,
	name string,
	workflow []store.WorkflowColumn,
) (store.Project, error) {
	r.calls++
	r.ctx = ctx
	r.name = name
	r.workflowNil = workflow == nil
	if workflow == nil {
		r.workflow = nil
	} else {
		r.workflow = append([]store.WorkflowColumn{}, workflow...)
	}
	if len(workflow) > 0 {
		workflow[0].Key = "mutated-by-recorder"
	}
	return r.result, r.err
}

type projectLifecycleRESTAnonymousCreationRecorder struct {
	calls   int
	ctx     context.Context
	actorID int64
	result  store.Project
	err     error
}

func (r *projectLifecycleRESTAnonymousCreationRecorder) CreateAnonymousBoard(
	ctx context.Context,
) (store.Project, error) {
	r.calls++
	r.ctx = ctx
	r.actorID, _ = store.UserIDFromContext(ctx)
	return r.result, r.err
}

type projectLifecycleRESTUpdateRecorder struct {
	trace []string

	project store.Project
	getErr  error

	imageProjectID int64
	imageActorID   int64
	image          *string
	dominantColor  string
	imageErr       error

	nameProjectID int64
	nameActorID   int64
	name          string
	nameErr       error
}

func (r *projectLifecycleRESTUpdateRecorder) GetProject(
	_ context.Context,
	_ int64,
) (store.Project, error) {
	r.trace = append(r.trace, "get-project")
	return r.project, r.getErr
}

func (r *projectLifecycleRESTUpdateRecorder) UpdateProjectImage(
	_ context.Context,
	projectID int64,
	actorID int64,
	image *string,
	dominantColor string,
) error {
	r.trace = append(r.trace, "update-image")
	r.imageProjectID = projectID
	r.imageActorID = actorID
	if image != nil {
		cloned := *image
		r.image = &cloned
	}
	r.dominantColor = dominantColor
	return r.imageErr
}

func (r *projectLifecycleRESTUpdateRecorder) UpdateProjectName(
	_ context.Context,
	projectID int64,
	actorID int64,
	name string,
) error {
	r.trace = append(r.trace, "update-name")
	r.nameProjectID = projectID
	r.nameActorID = actorID
	r.name = name
	return r.nameErr
}

func newProjectLifecycleRESTUpdateService(
	server *Server,
	recorder *projectLifecycleRESTUpdateRecorder,
) *projectapp.RESTUpdateService {
	return projectapp.NewRESTUpdateService(projectapp.RESTUpdateServiceDependencies{
		Projects:  recorder,
		Images:    recorder,
		Names:     recorder,
		Publisher: projectUpdatePublisher{server: server},
	})
}

type projectLifecycleRESTDeletionRecorder struct {
	calls     int
	ctx       context.Context
	projectID int64
	actorID   int64
	result    store.DeletedProjectSnapshot
	err       error
}

func (r *projectLifecycleRESTDeletionRecorder) DeleteProject(
	ctx context.Context,
	projectID int64,
	actorID int64,
) (store.DeletedProjectSnapshot, error) {
	r.calls++
	r.ctx = ctx
	r.projectID = projectID
	r.actorID = actorID
	return r.result, r.err
}

type projectLifecycleRESTClaimRecorder struct {
	calls     int
	ctx       context.Context
	projectID int64
	actorID   int64
	err       error
}

func (r *projectLifecycleRESTClaimRecorder) ClaimTemporaryBoard(
	ctx context.Context,
	projectID int64,
	actorID int64,
) error {
	r.calls++
	r.ctx = ctx
	r.projectID = projectID
	r.actorID = actorID
	return r.err
}

func TestProjectLifecycleRESTMigrationNewServerComposesServices(t *testing.T) {
	fx := newProjectLifecycleHTTPFixture(t, "full")
	if fx.server.projectCreations == nil ||
		fx.server.anonymousBoardCreations == nil ||
		fx.server.projectUpdates == nil ||
		fx.server.projectDeletions == nil ||
		fx.server.projectClaims == nil {
		t.Fatalf(
			"NewServer lifecycle services = create:%v anonymous:%v update:%v delete:%v claim:%v",
			fx.server.projectCreations,
			fx.server.anonymousBoardCreations,
			fx.server.projectUpdates,
			fx.server.projectDeletions,
			fx.server.projectClaims,
		)
	}
}

func TestProjectLifecycleRESTMigrationDurableCreationDelegatesWorkflowPresence(t *testing.T) {
	tests := []struct {
		name         string
		body         map[string]any
		wantNil      bool
		wantWorkflow []store.WorkflowColumn
	}{
		{
			name:    "omitted",
			body:    map[string]any{"name": "  Raw Project  "},
			wantNil: true,
		},
		{
			name:    "null",
			body:    map[string]any{"name": "  Raw Project  ", "workflow": nil},
			wantNil: true,
		},
		{
			name:         "supplied empty",
			body:         map[string]any{"name": "  Raw Project  ", "workflow": []any{}},
			wantWorkflow: []store.WorkflowColumn{},
		},
		{
			name: "custom",
			body: map[string]any{
				"name": "  Raw Project  ",
				"workflow": []map[string]any{
					{"key": " READY ", "name": " Ready ", "color": " raw ", "position": 9, "isDone": false},
					{"key": "DONE", "name": " Done ", "color": " #abcdef ", "position": -1, "isDone": true},
				},
			},
			wantWorkflow: []store.WorkflowColumn{
				{Key: " READY ", Name: " Ready ", Color: " raw ", Position: 9},
				{Key: "DONE", Name: " Done ", Color: " #abcdef ", Position: -1, IsDone: true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newProjectLifecycleHTTPFixture(t, "full")
			recorder := &projectLifecycleRESTCreationRecorder{result: store.Project{
				ID: 701, Name: "Projected Project", Slug: "projected-project",
			}}
			fx.server.projectCreations = projectapp.NewRESTDurableCreationService(recorder)
			fx.spy.reset()

			var response map[string]any
			resp, body := doJSON(
				t,
				fx.ts.Client(),
				http.MethodPost,
				fx.ts.URL+"/api/projects",
				tt.body,
				&response,
			)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("status=%d body=%s", resp.StatusCode, body)
			}
			if recorder.calls != 1 || recorder.name != "  Raw Project  " || recorder.workflowNil != tt.wantNil {
				t.Fatalf("creation recorder=%+v", recorder)
			}
			if !reflect.DeepEqual(recorder.workflow, tt.wantWorkflow) {
				t.Fatalf("workflow=%#v want=%#v", recorder.workflow, tt.wantWorkflow)
			}
			if response["id"] != float64(701) || response["slug"] != "projected-project" {
				t.Fatalf("response=%+v", response)
			}
			assertProjectLifecycleNoEvents(t, fx.spy)
		})
	}
}

func TestProjectLifecycleRESTMigrationAnonymousCreationAndTempBoundary(t *testing.T) {
	fx := newProjectLifecycleHTTPFixture(t, "full")
	client := newCookieClient(t)
	bootstrapUserClient(t, client, fx.ts.URL, "Creator", "migration-anon@example.com", "password123")
	creatorID := projectLifecycleUserID(t, fx.db, "migration-anon@example.com")
	client = noRedirectProjectLifecycleClient(client)
	recorder := &projectLifecycleRESTAnonymousCreationRecorder{result: store.Project{ID: 711, Slug: "from-application"}}
	fx.server.anonymousBoardCreations = projectapp.NewAnonymousBoardCreationService(recorder)

	resp, body := doProjectLifecycleRaw(t, client, http.MethodGet, fx.ts.URL+"/anon", "")
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/from-application" {
		t.Fatalf("GET /anon status=%d location=%q body=%s", resp.StatusCode, resp.Header.Get("Location"), body)
	}
	if recorder.calls != 1 || recorder.actorID != creatorID {
		t.Fatalf("anonymous creation calls/actor=%d/%d want 1/%d", recorder.calls, recorder.actorID, creatorID)
	}
	assertProjectLifecycleNoEvents(t, fx.spy)

	recorder.calls = 0
	recorder.err = errors.New("late initialization failure")
	fx.spy.reset()
	resp, body = doProjectLifecycleRaw(t, client, http.MethodGet, fx.ts.URL+"/anon", "")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("late error status=%d body=%s", resp.StatusCode, body)
	}
	assertProjectLifecycleRESTError(t, body, "INTERNAL", "failed to create board", "")
	if recorder.calls != 1 {
		t.Fatalf("late error creation calls=%d want 1", recorder.calls)
	}
	assertProjectLifecycleNoEvents(t, fx.spy)

	recorder.calls = 0
	recorder.err = nil
	resp, body = doProjectLifecycleRaw(t, client, http.MethodGet, fx.ts.URL+"/temp?source=legacy", "")
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/anon" {
		t.Fatalf("GET /temp status=%d location=%q body=%s", resp.StatusCode, resp.Header.Get("Location"), body)
	}
	if recorder.calls != 0 {
		t.Fatalf("GET /temp called anonymous creation %d times", recorder.calls)
	}

	resp, body = doProjectLifecycleRaw(t, client, http.MethodPost, fx.ts.URL+"/anon", "{}")
	if resp.StatusCode != http.StatusMethodNotAllowed || recorder.calls != 0 {
		t.Fatalf("POST /anon status=%d calls=%d body=%s", resp.StatusCode, recorder.calls, body)
	}
}

func TestProjectLifecycleRESTMigrationUpdateDelegatesAtCharacterizedPreparePoint(t *testing.T) {
	t.Run("full mode malformed body performs no lifecycle read", func(t *testing.T) {
		fx := newProjectLifecycleHTTPFixture(t, "full")
		recorder := &projectLifecycleRESTUpdateRecorder{project: store.Project{ID: 721}}
		fx.server.projectUpdates = newProjectLifecycleRESTUpdateService(fx.server, recorder)

		resp, body := doProjectLifecycleRaw(t, fx.ts.Client(), http.MethodPatch, fx.ts.URL+"/api/projects/721", "{bad")
		if resp.StatusCode != http.StatusBadRequest || apiErrorCode(t, body) != "VALIDATION_ERROR" {
			t.Fatalf("status=%d body=%s", resp.StatusCode, body)
		}
		if len(recorder.trace) != 0 {
			t.Fatalf("full malformed trace=%v want none", recorder.trace)
		}
	})

	t.Run("anonymous mode prepares target before malformed body", func(t *testing.T) {
		fx := newProjectLifecycleHTTPFixture(t, "anonymous")
		expires := time.Now().UTC().Add(time.Hour)
		recorder := &projectLifecycleRESTUpdateRecorder{project: store.Project{ID: 722, ExpiresAt: &expires}}
		fx.server.projectUpdates = newProjectLifecycleRESTUpdateService(fx.server, recorder)

		resp, body := doProjectLifecycleRaw(t, fx.ts.Client(), http.MethodPatch, fx.ts.URL+"/api/projects/722", "{bad")
		if resp.StatusCode != http.StatusBadRequest || apiErrorCode(t, body) != "VALIDATION_ERROR" {
			t.Fatalf("status=%d body=%s", resp.StatusCode, body)
		}
		if !reflect.DeepEqual(recorder.trace, []string{"get-project"}) {
			t.Fatalf("anonymous malformed trace=%v", recorder.trace)
		}
	})

	t.Run("success delegates sequence and uses real publisher", func(t *testing.T) {
		fx := newProjectLifecycleHTTPFixture(t, "full")
		client := newCookieClient(t)
		bootstrapUserClient(t, client, fx.ts.URL, "Owner", "migration-update@example.com", "password123")
		ownerID := projectLifecycleUserID(t, fx.db, "migration-update@example.com")
		recorder := &projectLifecycleRESTUpdateRecorder{project: store.Project{ID: 723, Name: "Projected", Slug: "projected"}}
		fx.server.projectUpdates = newProjectLifecycleRESTUpdateService(fx.server, recorder)
		fx.spy.reset()

		rawImage := "not-a-data-url"
		rawName := "  Raw Update  "
		var response map[string]any
		resp, body := doJSON(
			t,
			client,
			http.MethodPatch,
			fx.ts.URL+"/api/projects/723",
			map[string]any{"image": rawImage, "name": rawName},
			&response,
		)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d body=%s", resp.StatusCode, body)
		}
		if !reflect.DeepEqual(recorder.trace, []string{"update-image", "update-name", "get-project"}) {
			t.Fatalf("update trace=%v", recorder.trace)
		}
		if recorder.imageProjectID != 723 || recorder.nameProjectID != 723 ||
			recorder.imageActorID != ownerID || recorder.nameActorID != ownerID ||
			recorder.image == nil || *recorder.image != rawImage || recorder.name != rawName ||
			recorder.dominantColor != "#888888" {
			t.Fatalf("update recorder=%+v", recorder)
		}
		if response["id"] != float64(723) || response["name"] != "Projected" {
			t.Fatalf("response=%+v", response)
		}
		assertProjectLifecycleRefresh(t, fx, 723, "project_updated", ownerID)

		recorder.trace = nil
		fx.spy.reset()
		resp, body = doJSON(t, client, http.MethodPatch, fx.ts.URL+"/api/projects/723", map[string]any{}, nil)
		if resp.StatusCode != http.StatusOK || !reflect.DeepEqual(recorder.trace, []string{"get-project"}) {
			t.Fatalf("empty patch status=%d trace=%v body=%s", resp.StatusCode, recorder.trace, body)
		}
		assertProjectLifecycleNoEvents(t, fx.spy)
	})

	t.Run("application actor sentinel keeps old public mapping", func(t *testing.T) {
		fx := newProjectLifecycleHTTPFixture(t, "full")
		authenticated := newCookieClient(t)
		bootstrapUserClient(t, authenticated, fx.ts.URL, "Owner", "migration-update-auth@example.com", "password123")
		recorder := &projectLifecycleRESTUpdateRecorder{project: store.Project{ID: 724}}
		fx.server.projectUpdates = newProjectLifecycleRESTUpdateService(fx.server, recorder)

		resp, body := doJSON(
			t,
			fx.ts.Client(),
			http.MethodPatch,
			fx.ts.URL+"/api/projects/724",
			map[string]any{"image": "image"},
			nil,
		)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleRESTError(t, body, "UNAUTHORIZED", "unauthorized", "")
		if strings.Contains(string(body), projectapp.ErrActorRequired.Error()) || len(recorder.trace) != 0 {
			t.Fatalf("actor sentinel leaked or reached capability: trace=%v body=%s", recorder.trace, body)
		}
		assertProjectLifecycleNoEvents(t, fx.spy)
	})
}

func TestProjectLifecycleRESTMigrationDeletionDelegatesAndPublishesSnapshot(t *testing.T) {
	fx := newProjectLifecycleHTTPFixture(t, "full")
	client := newCookieClient(t)
	bootstrapUserClient(t, client, fx.ts.URL, "Owner", "migration-delete@example.com", "password123")
	ownerID := projectLifecycleUserID(t, fx.db, "migration-delete@example.com")
	recorder := &projectLifecycleRESTDeletionRecorder{result: store.DeletedProjectSnapshot{
		ProjectID: 731, Name: "Private Deleted Name", MemberUserIDs: []int64{41, 42},
	}}
	fx.server.projectDeletions = projectapp.NewRESTDeletionService(projectapp.RESTDeletionServiceDependencies{
		Projects:  recorder,
		Publisher: projectDeletionPublisher{server: fx.server},
	})
	fx.spy.reset()

	resp, body := doJSON(t, client, http.MethodDelete, fx.ts.URL+"/api/projects/731", nil, nil)
	if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if recorder.calls != 1 || recorder.projectID != 731 || recorder.actorID != ownerID {
		t.Fatalf("deletion recorder=%+v", recorder)
	}
	events, reasons := fx.spy.snapshot()
	if len(events) != 1 || !reflect.DeepEqual(reasons, []string{"project_deleted"}) || events[0].ProjectID != 731 {
		t.Fatalf("events=%+v reasons=%v", events, reasons)
	}
	if strings.Contains(string(events[0].Payload), "Private Deleted Name") ||
		strings.Contains(string(events[0].Payload), "41") {
		t.Fatalf("public deletion payload leaked snapshot: %s", events[0].Payload)
	}

	recorder.calls = 0
	recorder.err = store.ErrConflict
	fx.spy.reset()
	resp, body = doJSON(t, client, http.MethodDelete, fx.ts.URL+"/api/projects/731", nil, nil)
	if resp.StatusCode != http.StatusConflict || recorder.calls != 1 {
		t.Fatalf("failed delete status=%d calls=%d body=%s", resp.StatusCode, recorder.calls, body)
	}
	assertProjectLifecycleNoEvents(t, fx.spy)

	recorder.calls = 0
	recorder.err = nil
	resp, body = doJSON(t, fx.ts.Client(), http.MethodDelete, fx.ts.URL+"/api/projects/731", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized || recorder.calls != 0 {
		t.Fatalf("missing actor status=%d calls=%d body=%s", resp.StatusCode, recorder.calls, body)
	}
}

func TestProjectLifecycleRESTMigrationClaimDelegatesAfterRouteAccess(t *testing.T) {
	fx := newProjectLifecycleHTTPFixture(t, "full")
	client := newCookieClient(t)
	bootstrapUserClient(t, client, fx.ts.URL, "Creator", "migration-claim@example.com", "password123")
	creatorID := projectLifecycleUserID(t, fx.db, "migration-claim@example.com")
	board, err := fx.store.CreateAnonymousBoard(store.WithUserID(context.Background(), creatorID))
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	recorder := &projectLifecycleRESTClaimRecorder{}
	fx.server.projectClaims = projectapp.NewRESTClaimService(projectapp.RESTClaimServiceDependencies{
		Claims:    recorder,
		Publisher: projectClaimPublisher{server: fx.server},
	})
	fx.wrapped.activate()
	fx.spy.reset()

	resp, body := doProjectLifecycleRaw(
		t,
		client,
		http.MethodPost,
		fx.ts.URL+"/api/board/"+board.Slug+"/claim",
		"{ignored",
	)
	if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if recorder.calls != 1 || recorder.projectID != board.ID || recorder.actorID != creatorID {
		t.Fatalf("claim recorder=%+v", recorder)
	}
	assertProjectLifecycleTrace(t, fx.wrapped, "access", "publish:board_claimed")
	assertProjectLifecycleRefresh(t, fx, board.ID, "board_claimed", creatorID)

	recorder.calls = 0
	recorder.err = store.ErrNotFound
	fx.wrapped.activate()
	fx.spy.reset()
	resp, body = doProjectLifecycleRaw(t, client, http.MethodPost, fx.ts.URL+"/api/board/"+board.Slug+"/claim", "")
	if resp.StatusCode != http.StatusNotFound || recorder.calls != 1 {
		t.Fatalf("failed claim status=%d calls=%d body=%s", resp.StatusCode, recorder.calls, body)
	}
	assertProjectLifecycleTrace(t, fx.wrapped, "access")
	assertProjectLifecycleNoEvents(t, fx.spy)

	creatorless, err := fx.store.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard creatorless: %v", err)
	}
	recorder.calls = 0
	recorder.err = nil
	resp, body = doProjectLifecycleRaw(
		t,
		fx.ts.Client(),
		http.MethodPost,
		fx.ts.URL+"/api/board/"+creatorless.Slug+"/claim",
		"",
	)
	if resp.StatusCode != http.StatusUnauthorized || recorder.calls != 0 {
		t.Fatalf("missing actor claim status=%d calls=%d body=%s", resp.StatusCode, recorder.calls, body)
	}
}

func assertProjectLifecycleRefresh(
	t *testing.T,
	fx *projectLifecycleHTTPFixture,
	projectID int64,
	reason string,
	actorID int64,
) {
	t.Helper()
	events, reasons := fx.spy.snapshot()
	if len(events) != 1 || !reflect.DeepEqual(reasons, []string{reason}) || events[0].ProjectID != projectID {
		t.Fatalf("events=%+v reasons=%v want project=%d reason=%q", events, reasons, projectID, reason)
	}
	var payload refreshNeededPayload
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("decode refresh payload: %v", err)
	}
	if payload.Reason != reason || payload.ActorUserID != actorID ||
		payload.LocalID != 0 || payload.Title != "" || payload.Name != "" {
		t.Fatalf("refresh payload=%+v", payload)
	}
}
