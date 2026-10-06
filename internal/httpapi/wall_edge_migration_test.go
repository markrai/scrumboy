package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	wallapp "scrumboy/internal/application/wall"
	"scrumboy/internal/store"
)

type wallEdgeMigrationContextKey struct{}

type wallEdgeMigrationCreateCall struct {
	ctx       context.Context
	projectID int64
	from      string
	to        string
}

type wallEdgeMigrationDeleteCall struct {
	ctx       context.Context
	projectID int64
	edgeID    string
}

type wallEdgeMigrationRefreshCall struct {
	ctx       context.Context
	projectID int64
	reason    wallapp.RefreshReason
}

type wallEdgeMigrationRecorder struct {
	trace []string

	role      store.ProjectRole
	roleErr   error
	roleCalls int
	roleCtx   context.Context
	rolePID   int64
	roleUID   int64

	createCalls  []wallEdgeMigrationCreateCall
	createResult store.WallEdge
	createWall   store.Wall
	createErr    error

	deleteCalls []wallEdgeMigrationDeleteCall
	deleteWall  store.Wall
	deleteErr   error

	refreshCalls []wallEdgeMigrationRefreshCall
}

var (
	_ wallapp.RESTWriterRoleStore  = (*wallEdgeMigrationRecorder)(nil)
	_ wallapp.EdgeMutationStore    = (*wallEdgeMigrationRecorder)(nil)
	_ wallapp.WallRefreshPublisher = (*wallEdgeMigrationRecorder)(nil)
)

func (r *wallEdgeMigrationRecorder) record(step string) {
	r.trace = append(r.trace, step)
}

func (r *wallEdgeMigrationRecorder) GetProjectRole(
	ctx context.Context,
	projectID int64,
	userID int64,
) (store.ProjectRole, error) {
	r.record("role")
	r.roleCalls++
	r.roleCtx = ctx
	r.rolePID = projectID
	r.roleUID = userID
	return r.role, r.roleErr
}

func (r *wallEdgeMigrationRecorder) CreateEdge(
	ctx context.Context,
	projectID int64,
	from string,
	to string,
) (store.WallEdge, store.Wall, error) {
	r.record("create")
	r.createCalls = append(r.createCalls, wallEdgeMigrationCreateCall{
		ctx: ctx, projectID: projectID, from: from, to: to,
	})
	return r.createResult, r.createWall, r.createErr
}

func (r *wallEdgeMigrationRecorder) DeleteEdge(
	ctx context.Context,
	projectID int64,
	edgeID string,
) (store.Wall, error) {
	r.record("delete")
	r.deleteCalls = append(r.deleteCalls, wallEdgeMigrationDeleteCall{
		ctx: ctx, projectID: projectID, edgeID: edgeID,
	})
	return r.deleteWall, r.deleteErr
}

func (r *wallEdgeMigrationRecorder) PublishWallRefresh(
	ctx context.Context,
	projectID int64,
	reason wallapp.RefreshReason,
) {
	r.record("refresh")
	r.refreshCalls = append(r.refreshCalls, wallEdgeMigrationRefreshCall{
		ctx: ctx, projectID: projectID, reason: reason,
	})
}

func (r *wallEdgeMigrationRecorder) mutationCalls() int {
	return len(r.createCalls) + len(r.deleteCalls)
}

type wallEdgeMigrationBody struct {
	recorder *wallEdgeMigrationRecorder
	reader   io.Reader
	recorded bool
}

func newWallEdgeMigrationBody(
	recorder *wallEdgeMigrationRecorder,
	body string,
) *wallEdgeMigrationBody {
	return &wallEdgeMigrationBody{
		recorder: recorder,
		reader:   bytes.NewBufferString(body),
	}
}

func (b *wallEdgeMigrationBody) Read(p []byte) (int, error) {
	if !b.recorded {
		b.recorded = true
		b.recorder.record("body")
	}
	return b.reader.Read(p)
}

func installWallEdgeMigrationService(
	server *Server,
	recorder *wallEdgeMigrationRecorder,
) {
	server.wallEdgeMutations = wallapp.NewRESTEdgeService(
		wallapp.RESTEdgeServiceDependencies{
			Roles:     recorder,
			Mutations: recorder,
			Refresh:   recorder,
		},
	)
}

func newWallEdgeMigrationRequest(
	t *testing.T,
	fx *wallCharacterizationFixture,
	method string,
	requestURL string,
	rawCtx context.Context,
	body io.Reader,
	authenticated bool,
) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, requestURL, body).WithContext(rawCtx)
	req.Header.Set("Content-Type", "application/json")
	if !authenticated {
		return req
	}
	parsed, err := url.Parse(fx.baseURL)
	if err != nil {
		t.Fatalf("parse fixture URL: %v", err)
	}
	for _, cookie := range fx.client.Jar.Cookies(parsed) {
		req.AddCookie(cookie)
	}
	return req
}

func assertWallEdgeMigrationTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trace=%v want=%v", got, want)
	}
}

func assertWallEdgeMigrationResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
	wantEdge store.WallEdge,
) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d want=%d body=%s", response.Code, status, response.Body.Bytes())
	}
	if status == http.StatusNoContent {
		if response.Body.Len() != 0 {
			t.Fatalf("204 body=%q want empty", response.Body.Bytes())
		}
		return
	}
	var got map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode edge response %s: %v", response.Body.Bytes(), err)
	}
	assertExactJSONKeys(t, got, "id", "from", "to")
	want := map[string]any{"id": wantEdge.ID, "from": wantEdge.From, "to": wantEdge.To}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("edge response=%+v want=%+v", got, want)
	}
}

func assertWallEdgeMigrationNoEffects(
	t *testing.T,
	recorder *wallEdgeMigrationRecorder,
	fx *wallCharacterizationFixture,
) {
	t.Helper()
	if recorder.mutationCalls() != 0 || len(recorder.refreshCalls) != 0 {
		t.Fatalf("mutation/refresh calls=%d/%d", recorder.mutationCalls(), len(recorder.refreshCalls))
	}
	if events := fx.collector.snapshot(); len(events) != 0 {
		t.Fatalf("unexpected direct handler events=%+v", events)
	}
}

func assertWallEdgeMigrationNoDirectStoreCalls(
	t *testing.T,
	fx *wallCharacterizationFixture,
) {
	t.Helper()
	for _, operation := range []string{"GetProjectRole", "CreateEdge", "DeleteEdge", "GetWall"} {
		if got := fx.spy.callCount(operation); got != 0 {
			t.Fatalf("direct fixture store %s calls=%d want=0", operation, got)
		}
	}
}

func TestWallEdgeMigrationNewServerComposesApplicationService(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	if fx.server.wallEdgeMutations == nil {
		t.Fatal("NewServer did not compose the REST Wall edge service")
	}
}

func TestWallEdgeMigrationRefreshPublisherProjection(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	publisher := wallRefreshPublisher{server: fx.server}

	for _, tt := range []struct {
		name   string
		reason wallapp.RefreshReason
	}{
		{name: "created", reason: wallapp.RefreshEdgeCreated},
		{name: "deleted", reason: wallapp.RefreshEdgeDeleted},
	} {
		t.Run(tt.name, func(t *testing.T) {
			marked := context.WithValue(context.Background(), wallEdgeMigrationContextKey{}, tt.name)
			deadline := time.Now().Add(time.Minute)
			rawCtx, cancel := context.WithDeadline(marked, deadline)
			fx.collector.reset()

			publisher.PublishWallRefresh(rawCtx, fx.project.ID, tt.reason)
			assertSingleWallRefresh(t, fx, string(tt.reason))
			contexts := fx.collector.contextSnapshot()
			if len(contexts) != 1 || contexts[0] != rawCtx {
				t.Fatalf("publisher contexts=%v want exact raw context", contexts)
			}
			if contexts[0].Value(wallEdgeMigrationContextKey{}) != tt.name {
				t.Fatal("publisher context lost raw marker")
			}
			gotDeadline, ok := contexts[0].Deadline()
			if !ok || !gotDeadline.Equal(deadline) {
				t.Fatalf("publisher deadline=(%v,%v) want=(%v,true)", gotDeadline, ok, deadline)
			}
			if _, ok := store.UserIDFromContext(contexts[0]); ok {
				t.Fatal("publisher context unexpectedly contains actor enrichment")
			}
			cancel()
			if contexts[0].Err() != context.Canceled {
				t.Fatalf("publisher cancellation=%v want=%v", contexts[0].Err(), context.Canceled)
			}
		})
	}
}

func TestWallEdgeMigrationHandlersDelegateOnceWithRetainedContexts(t *testing.T) {
	wantCreateEdge := store.WallEdge{
		ID: "returned-edge", From: "returned-from", To: "returned-to",
	}
	tests := []struct {
		name       string
		method     string
		body       string
		status     int
		wantTrace  []string
		wantReason wallapp.RefreshReason
		invoke     func(*Server, http.ResponseWriter, *http.Request, int64)
		assertCall func(*testing.T, *wallEdgeMigrationRecorder) context.Context
	}{
		{
			name: "create", method: http.MethodPost,
			body:       `{"from":" \t z-note \n ","to":" \r a-note \t "}`,
			status:     http.StatusCreated,
			wantTrace:  []string{"role", "body", "create", "refresh"},
			wantReason: wallapp.RefreshEdgeCreated,
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallCreateEdge(w, r, projectID)
			},
			assertCall: func(t *testing.T, recorder *wallEdgeMigrationRecorder) context.Context {
				t.Helper()
				if len(recorder.createCalls) != 1 || len(recorder.deleteCalls) != 0 {
					t.Fatalf("create/delete calls=%+v/%+v", recorder.createCalls, recorder.deleteCalls)
				}
				call := recorder.createCalls[0]
				if call.ctx != recorder.roleCtx || call.projectID != recorder.rolePID ||
					call.from != "z-note" || call.to != "a-note" {
					t.Fatalf("create call=%+v", call)
				}
				return call.ctx
			},
		},
		{
			name: "delete", method: http.MethodDelete,
			status:     http.StatusNoContent,
			wantTrace:  []string{"role", "delete", "refresh"},
			wantReason: wallapp.RefreshEdgeDeleted,
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallDeleteEdge(w, r, projectID, " \t raw-edge-id \n ")
			},
			assertCall: func(t *testing.T, recorder *wallEdgeMigrationRecorder) context.Context {
				t.Helper()
				if len(recorder.createCalls) != 0 || len(recorder.deleteCalls) != 1 {
					t.Fatalf("create/delete calls=%+v/%+v", recorder.createCalls, recorder.deleteCalls)
				}
				call := recorder.deleteCalls[0]
				if call.ctx != recorder.roleCtx || call.projectID != recorder.rolePID || call.edgeID != "raw-edge-id" {
					t.Fatalf("delete call=%+v", call)
				}
				return call.ctx
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newWallCharacterizationFixture(t, true)
			recorder := &wallEdgeMigrationRecorder{
				role:         store.RoleContributor,
				createResult: wantCreateEdge,
				createWall: store.Wall{
					Notes:   []store.WallNote{{ID: "opaque-create-note", Version: 41}},
					Edges:   []store.WallEdge{{ID: "opaque-create-edge"}},
					Version: 91, UpdatedAt: 92,
				},
				deleteWall: store.Wall{
					Notes:   []store.WallNote{{ID: "opaque-delete-note", Version: 51}},
					Version: 93, UpdatedAt: 94,
				},
			}
			installWallEdgeMigrationService(fx.server, recorder)
			fx.collector.reset()
			fx.spy.resetCalls()
			marked := context.WithValue(context.Background(), wallEdgeMigrationContextKey{}, tt.name)
			deadline := time.Now().Add(time.Minute)
			rawCtx, cancel := context.WithDeadline(marked, deadline)
			body := io.Reader(nil)
			if tt.body != "" {
				body = newWallEdgeMigrationBody(recorder, tt.body)
			}
			req := newWallEdgeMigrationRequest(
				t, fx, tt.method, wallMutationURL(fx, "/direct"), rawCtx, body, true,
			)
			response := httptest.NewRecorder()

			tt.invoke(fx.server, response, req, fx.project.ID)
			assertWallEdgeMigrationResponse(t, response, tt.status, wantCreateEdge)
			assertWallEdgeMigrationTrace(t, recorder.trace, tt.wantTrace...)
			if recorder.roleCalls != 1 || recorder.rolePID != fx.project.ID || recorder.roleUID != fx.owner.ID {
				t.Fatalf("role calls/project/user=%d/%d/%d", recorder.roleCalls, recorder.rolePID, recorder.roleUID)
			}
			if recorder.mutationCalls() != 1 {
				t.Fatalf("mutation calls=%d want=1", recorder.mutationCalls())
			}
			storeCtx := tt.assertCall(t, recorder)
			if len(recorder.refreshCalls) != 1 {
				t.Fatalf("refresh calls=%+v", recorder.refreshCalls)
			}
			refresh := recorder.refreshCalls[0]
			if refresh.ctx != rawCtx || refresh.projectID != fx.project.ID || refresh.reason != tt.wantReason {
				t.Fatalf("refresh=%+v want exact raw context/project/reason", refresh)
			}
			if recorder.roleCtx == rawCtx || storeCtx != recorder.roleCtx {
				t.Fatal("role/store did not share one actor-enriched mutation context")
			}
			if recorder.roleCtx.Value(wallEdgeMigrationContextKey{}) != tt.name {
				t.Fatal("mutation context lost raw request marker")
			}
			actorID, ok := store.UserIDFromContext(recorder.roleCtx)
			if !ok || actorID != fx.owner.ID {
				t.Fatalf("mutation actor=%d,%v want=%d,true", actorID, ok, fx.owner.ID)
			}
			if _, ok := store.UserIDFromContext(rawCtx); ok {
				t.Fatal("raw effect context unexpectedly contains actor")
			}
			for name, ctx := range map[string]context.Context{
				"role": recorder.roleCtx, "store": storeCtx, "refresh": refresh.ctx,
			} {
				gotDeadline, ok := ctx.Deadline()
				if !ok || !gotDeadline.Equal(deadline) {
					t.Fatalf("%s deadline=(%v,%v) want=(%v,true)", name, gotDeadline, ok, deadline)
				}
			}
			assertWallEdgeMigrationNoDirectStoreCalls(t, fx)
			if events := fx.collector.snapshot(); len(events) != 0 {
				t.Fatalf("handler retained direct event publication=%+v", events)
			}
			cancel()
			for name, ctx := range map[string]context.Context{
				"role": recorder.roleCtx, "store": storeCtx, "refresh": refresh.ctx,
			} {
				if ctx.Err() != context.Canceled {
					t.Fatalf("%s cancellation=%v want=%v", name, ctx.Err(), context.Canceled)
				}
			}
		})
	}
}

func TestWallEdgeMigrationPreparationPrecedesInput(t *testing.T) {
	tests := []struct {
		name          string
		role          store.ProjectRole
		roleErr       error
		authenticated bool
		method        string
		body          string
		invoke        func(*Server, http.ResponseWriter, *http.Request, int64)
		wantStatus    int
		wantCode      string
		wantMessage   string
		wantDetails   map[string]any
		wantTrace     []string
		wantRoleCalls int
	}{
		{
			name: "actor absent before create body", role: store.RoleContributor,
			method: http.MethodPost, body: "{",
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallCreateEdge(w, r, projectID)
			},
			wantStatus: http.StatusUnauthorized, wantCode: "UNAUTHORIZED", wantMessage: "unauthorized",
		},
		{
			name: "role read error before create body", roleErr: errors.New("role failed"), authenticated: true,
			method: http.MethodPost, body: "{",
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallCreateEdge(w, r, projectID)
			},
			wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN", wantMessage: "contributor or higher required",
			wantTrace: []string{"role"}, wantRoleCalls: 1,
		},
		{
			name: "viewer before malformed create body", role: store.RoleViewer, authenticated: true,
			method: http.MethodPost, body: "{",
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallCreateEdge(w, r, projectID)
			},
			wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN", wantMessage: "contributor or higher required",
			wantTrace: []string{"role"}, wantRoleCalls: 1,
		},
		{
			name: "authorized malformed create body", role: store.RoleContributor, authenticated: true,
			method: http.MethodPost, body: "{",
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallCreateEdge(w, r, projectID)
			},
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "invalid json",
			wantDetails: map[string]any{"reason": "invalid_json", "detail": "unexpected EOF"},
			wantTrace:   []string{"role", "body"}, wantRoleCalls: 1,
		},
		{
			name: "authorized whitespace endpoints", role: store.RoleContributor, authenticated: true,
			method: http.MethodPost, body: `{"from":" \t ","to":" \n "}`,
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallCreateEdge(w, r, projectID)
			},
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "from and to required",
			wantDetails: map[string]any{"reason": "wall_edge_endpoints_required"},
			wantTrace:   []string{"role", "body"}, wantRoleCalls: 1,
		},
		{
			name: "viewer before blank delete path", role: store.RoleViewer, authenticated: true,
			method: http.MethodDelete,
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallDeleteEdge(w, r, projectID, " \t ")
			},
			wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN", wantMessage: "contributor or higher required",
			wantTrace: []string{"role"}, wantRoleCalls: 1,
		},
		{
			name: "authorized blank delete path", role: store.RoleContributor, authenticated: true,
			method: http.MethodDelete,
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallDeleteEdge(w, r, projectID, " \t ")
			},
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "edgeId required",
			wantDetails: map[string]any{"reason": "edge_id_required", "field": "edgeId"},
			wantTrace:   []string{"role"}, wantRoleCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newWallCharacterizationFixture(t, true)
			recorder := &wallEdgeMigrationRecorder{role: tt.role, roleErr: tt.roleErr}
			installWallEdgeMigrationService(fx.server, recorder)
			fx.collector.reset()
			fx.spy.resetCalls()
			body := io.Reader(nil)
			if tt.body != "" {
				body = newWallEdgeMigrationBody(recorder, tt.body)
			}
			req := newWallEdgeMigrationRequest(
				t, fx, tt.method, wallMutationURL(fx, "/direct"), context.Background(), body, tt.authenticated,
			)
			response := httptest.NewRecorder()

			tt.invoke(fx.server, response, req, fx.project.ID)
			if response.Code != tt.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tt.wantStatus, response.Body.Bytes())
			}
			assertWallError(t, response.Body.Bytes(), tt.wantCode, tt.wantMessage, tt.wantDetails)
			if recorder.roleCalls != tt.wantRoleCalls {
				t.Fatalf("role calls=%d want=%d", recorder.roleCalls, tt.wantRoleCalls)
			}
			assertWallEdgeMigrationTrace(t, recorder.trace, tt.wantTrace...)
			assertWallEdgeMigrationNoEffects(t, recorder, fx)
			assertWallEdgeMigrationNoDirectStoreCalls(t, fx)
		})
	}
}

func TestWallEdgeMigrationGlobalHeaderGateRemainsOutsideService(t *testing.T) {
	for _, tt := range []struct {
		name   string
		method string
		suffix string
		body   string
	}{
		{name: "create", method: http.MethodPost, suffix: "/edges", body: "{"},
		{name: "delete", method: http.MethodDelete, suffix: "/edges/missing", body: "{"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fx := newWallCharacterizationFixture(t, true)
			recorder := &wallEdgeMigrationRecorder{role: store.RoleContributor}
			installWallEdgeMigrationService(fx.server, recorder)
			fx.collector.reset()
			fx.spy.resetCalls()
			body := newWallEdgeMigrationBody(recorder, tt.body)
			req := newWallEdgeMigrationRequest(
				t, fx, tt.method, wallMutationURL(fx, tt.suffix), context.Background(), body, true,
			)
			response := httptest.NewRecorder()

			fx.server.ServeHTTP(response, req)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusForbidden, response.Body.Bytes())
			}
			assertWallError(t, response.Body.Bytes(), "FORBIDDEN", "missing X-Scrumboy header", nil)
			if recorder.roleCalls != 0 {
				t.Fatalf("role calls=%d want=0", recorder.roleCalls)
			}
			assertWallEdgeMigrationTrace(t, recorder.trace)
			assertWallEdgeMigrationNoEffects(t, recorder, fx)
			assertWallEdgeMigrationNoDirectStoreCalls(t, fx)
		})
	}
}

func TestWallEdgeMigrationDuplicateDirectionsRemain201AndPublish(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	var firstNote, secondNote map[string]any
	resp, body := doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/notes"), wallNoteInput("first"), &firstNote)
	assertWallStatus(t, resp, body, http.StatusCreated)
	resp, body = doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/notes"), wallNoteInput("second"), &secondNote)
	assertWallStatus(t, resp, body, http.StatusCreated)
	firstID, _ := firstNote["id"].(string)
	secondID, _ := secondNote["id"].(string)
	if firstID == "" || secondID == "" {
		t.Fatalf("setup note IDs=%q/%q", firstID, secondID)
	}
	fx.collector.reset()

	var first map[string]any
	resp, body = doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/edges"), map[string]any{
		"from": firstID, "to": secondID,
	}, &first)
	assertWallStatus(t, resp, body, http.StatusCreated)
	assertExactJSONKeys(t, first, "id", "from", "to")
	firstEdgeID, idOK := first["id"].(string)
	firstEdgeFrom, fromOK := first["from"].(string)
	firstEdgeTo, toOK := first["to"].(string)
	if !idOK || !fromOK || !toOK || firstEdgeID == "" {
		t.Fatalf("first edge response has invalid values: %+v", first)
	}
	firstEdge := store.WallEdge{
		ID: firstEdgeID, From: firstEdgeFrom, To: firstEdgeTo,
	}
	assertSingleWallRefresh(t, fx, string(wallapp.RefreshEdgeCreated))
	fx.collector.reset()

	before, err := fx.store.GetWall(context.Background(), fx.project.ID)
	if err != nil {
		t.Fatalf("GetWall before duplicates: %v", err)
	}
	if len(before.Edges) != 1 || before.Edges[0] != firstEdge {
		t.Fatalf("first persisted edge=%+v want=%+v", before.Edges, firstEdge)
	}

	var same map[string]any
	resp, body = doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/edges"), map[string]any{
		"from": firstID, "to": secondID,
	}, &same)
	assertWallStatus(t, resp, body, http.StatusCreated)
	assertExactJSONKeys(t, same, "id", "from", "to")
	if !reflect.DeepEqual(same, first) {
		t.Fatalf("same-direction duplicate=%+v want=%+v", same, first)
	}
	assertSingleWallRefresh(t, fx, string(wallapp.RefreshEdgeCreated))
	afterSame, err := fx.store.GetWall(context.Background(), fx.project.ID)
	if err != nil {
		t.Fatalf("GetWall after same duplicate: %v", err)
	}
	if afterSame.Version != before.Version || afterSame.UpdatedAt != before.UpdatedAt ||
		len(afterSame.Edges) != 1 || afterSame.Edges[0] != firstEdge {
		t.Fatalf("same-direction duplicate changed Wall: before=%+v after=%+v", before, afterSame)
	}
	fx.collector.reset()

	var reverse map[string]any
	resp, body = doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/edges"), map[string]any{
		"from": secondID, "to": firstID,
	}, &reverse)
	assertWallStatus(t, resp, body, http.StatusCreated)
	assertExactJSONKeys(t, reverse, "id", "from", "to")
	if !reflect.DeepEqual(reverse, first) {
		t.Fatalf("reverse duplicate=%+v want=%+v", reverse, first)
	}
	assertSingleWallRefresh(t, fx, string(wallapp.RefreshEdgeCreated))
	afterReverse, err := fx.store.GetWall(context.Background(), fx.project.ID)
	if err != nil {
		t.Fatalf("GetWall after reverse duplicate: %v", err)
	}
	if afterReverse.Version != before.Version || afterReverse.UpdatedAt != before.UpdatedAt ||
		len(afterReverse.Edges) != 1 || afterReverse.Edges[0] != firstEdge {
		t.Fatalf("reverse duplicate changed Wall: before=%+v after=%+v", before, afterReverse)
	}
}

func TestWallEdgeMigrationDeleteThenSecondDelete(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	var firstNote, secondNote map[string]any
	resp, body := doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/notes"), wallNoteInput("first"), &firstNote)
	assertWallStatus(t, resp, body, http.StatusCreated)
	resp, body = doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/notes"), wallNoteInput("second"), &secondNote)
	assertWallStatus(t, resp, body, http.StatusCreated)
	var edge map[string]any
	resp, body = doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/edges"), map[string]any{
		"from": firstNote["id"], "to": secondNote["id"],
	}, &edge)
	assertWallStatus(t, resp, body, http.StatusCreated)
	edgeID, _ := edge["id"].(string)
	if edgeID == "" {
		t.Fatalf("setup edge=%+v", edge)
	}
	fx.collector.reset()

	resp, body = doJSON(t, fx.client, http.MethodDelete, wallMutationURL(fx, "/edges/"+edgeID), nil, nil)
	assertWallStatus(t, resp, body, http.StatusNoContent)
	if len(body) != 0 {
		t.Fatalf("first delete body=%q want empty", body)
	}
	assertSingleWallRefresh(t, fx, string(wallapp.RefreshEdgeDeleted))
	fx.collector.reset()

	resp, body = doJSON(t, fx.client, http.MethodDelete, wallMutationURL(fx, "/edges/"+edgeID), nil, nil)
	assertWallStatus(t, resp, body, http.StatusNotFound)
	if len(body) == 0 {
		t.Fatal("second delete body is empty; want characterized error envelope")
	}
	assertWallError(t, body, "NOT_FOUND", "not found", nil)
	if events := fx.collector.snapshot(); len(events) != 0 {
		t.Fatalf("second delete published events=%+v", events)
	}
}

func TestWallEdgeMigrationExecutionErrorsUseExistingProjectionAndPublishNothing(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		body        string
		mutationErr error
		invoke      func(*Server, http.ResponseWriter, *http.Request, int64)
		wantStatus  int
		wantCode    string
		wantMessage string
		wantDetails map[string]any
		wantTrace   []string
	}{
		{
			name: "create self edge validation", method: http.MethodPost,
			body:        `{ "from": "note-a", "to": "note-a" }`,
			mutationErr: fmt.Errorf("%w: self-edges not allowed", store.ErrValidation),
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallCreateEdge(w, r, projectID)
			},
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR",
			wantMessage: "validation: self-edges not allowed",
			wantDetails: map[string]any{"reason": "self_edges_not_allowed"},
			wantTrace:   []string{"role", "body", "create"},
		},
		{
			name: "create missing endpoint", method: http.MethodPost,
			body: `{ "from": "note-a", "to": "missing" }`, mutationErr: store.ErrNotFound,
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallCreateEdge(w, r, projectID)
			},
			wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMessage: "not found",
			wantTrace: []string{"role", "body", "create"},
		},
		{
			name: "create edge limit validation", method: http.MethodPost,
			body:        `{ "from": "note-a", "to": "note-b" }`,
			mutationErr: fmt.Errorf("%w: wall edge limit reached", store.ErrValidation),
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallCreateEdge(w, r, projectID)
			},
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR",
			wantMessage: "validation: wall edge limit reached",
			wantDetails: map[string]any{"reason": "wall_edge_limit_reached"},
			wantTrace:   []string{"role", "body", "create"},
		},
		{
			name: "create internal", method: http.MethodPost,
			body: `{ "from": "note-a", "to": "note-b" }`, mutationErr: errors.New("injected create failure"),
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallCreateEdge(w, r, projectID)
			},
			wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL", wantMessage: "internal error",
			wantDetails: map[string]any{"detail": "injected create failure"},
			wantTrace:   []string{"role", "body", "create"},
		},
		{
			name: "delete missing edge", method: http.MethodDelete, mutationErr: store.ErrNotFound,
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallDeleteEdge(w, r, projectID, "missing-edge")
			},
			wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMessage: "not found",
			wantTrace: []string{"role", "delete"},
		},
		{
			name: "delete internal", method: http.MethodDelete, mutationErr: errors.New("injected delete failure"),
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallDeleteEdge(w, r, projectID, "edge-id")
			},
			wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL", wantMessage: "internal error",
			wantDetails: map[string]any{"detail": "injected delete failure"},
			wantTrace:   []string{"role", "delete"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newWallCharacterizationFixture(t, true)
			recorder := &wallEdgeMigrationRecorder{
				role:         store.RoleContributor,
				createResult: store.WallEdge{ID: "must-not-return", From: "x", To: "y"},
				createWall:   store.Wall{Version: 71, UpdatedAt: 72},
				deleteWall:   store.Wall{Version: 73, UpdatedAt: 74},
			}
			if tt.wantTrace[len(tt.wantTrace)-1] == "create" {
				recorder.createErr = tt.mutationErr
			} else {
				recorder.deleteErr = tt.mutationErr
			}
			installWallEdgeMigrationService(fx.server, recorder)
			fx.collector.reset()
			fx.spy.resetCalls()
			body := io.Reader(nil)
			if tt.body != "" {
				body = newWallEdgeMigrationBody(recorder, tt.body)
			}
			req := newWallEdgeMigrationRequest(
				t, fx, tt.method, wallMutationURL(fx, "/direct"), context.Background(), body, true,
			)
			response := httptest.NewRecorder()

			tt.invoke(fx.server, response, req, fx.project.ID)
			if response.Code != tt.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tt.wantStatus, response.Body.Bytes())
			}
			assertWallError(t, response.Body.Bytes(), tt.wantCode, tt.wantMessage, tt.wantDetails)
			if recorder.roleCalls != 1 || recorder.mutationCalls() != 1 || len(recorder.refreshCalls) != 0 {
				t.Fatalf("role/mutation/refresh=%d/%d/%d", recorder.roleCalls, recorder.mutationCalls(), len(recorder.refreshCalls))
			}
			assertWallEdgeMigrationTrace(t, recorder.trace, tt.wantTrace...)
			assertWallEdgeMigrationNoDirectStoreCalls(t, fx)
			if events := fx.collector.snapshot(); len(events) != 0 {
				t.Fatalf("failed mutation published direct events=%+v", events)
			}
		})
	}
}
