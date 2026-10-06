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

type wallReplacementMigrationContextKey struct{}

type wallReplacementMigrationReplaceCall struct {
	ctx       context.Context
	projectID int64
	notes     []store.WallNote
}

type wallReplacementMigrationRefreshCall struct {
	ctx       context.Context
	projectID int64
	reason    wallapp.RefreshReason
}

type wallReplacementMigrationRecorder struct {
	trace []string

	role      store.ProjectRole
	roleErr   error
	roleCalls int
	roleCtx   context.Context
	rolePID   int64
	roleUID   int64

	replaceCalls  []wallReplacementMigrationReplaceCall
	replaceResult store.Wall
	replaceErr    error

	refreshCalls []wallReplacementMigrationRefreshCall
}

var (
	_ wallapp.RESTWriterRoleStore  = (*wallReplacementMigrationRecorder)(nil)
	_ wallapp.WallReplacementStore = (*wallReplacementMigrationRecorder)(nil)
	_ wallapp.WallRefreshPublisher = (*wallReplacementMigrationRecorder)(nil)
)

func (r *wallReplacementMigrationRecorder) record(step string) {
	r.trace = append(r.trace, step)
}

func (r *wallReplacementMigrationRecorder) GetProjectRole(
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

func (r *wallReplacementMigrationRecorder) ReplaceWall(
	ctx context.Context,
	projectID int64,
	notes []store.WallNote,
) (store.Wall, error) {
	r.record("replace")
	r.replaceCalls = append(r.replaceCalls, wallReplacementMigrationReplaceCall{
		ctx:       ctx,
		projectID: projectID,
		notes:     cloneWallReplacementMigrationNotes(notes),
	})
	return r.replaceResult, r.replaceErr
}

func (r *wallReplacementMigrationRecorder) PublishWallRefresh(
	ctx context.Context,
	projectID int64,
	reason wallapp.RefreshReason,
) {
	r.record("refresh")
	r.refreshCalls = append(r.refreshCalls, wallReplacementMigrationRefreshCall{
		ctx: ctx, projectID: projectID, reason: reason,
	})
}

func cloneWallReplacementMigrationNotes(notes []store.WallNote) []store.WallNote {
	if notes == nil {
		return nil
	}
	return append(make([]store.WallNote, 0, len(notes)), notes...)
}

type wallReplacementMigrationBody struct {
	recorder *wallReplacementMigrationRecorder
	reader   io.Reader
	recorded bool
}

func newWallReplacementMigrationBody(
	recorder *wallReplacementMigrationRecorder,
	body string,
) *wallReplacementMigrationBody {
	return &wallReplacementMigrationBody{
		recorder: recorder,
		reader:   bytes.NewBufferString(body),
	}
}

func (b *wallReplacementMigrationBody) Read(p []byte) (int, error) {
	if !b.recorded {
		b.recorded = true
		b.recorder.record("body")
	}
	return b.reader.Read(p)
}

func installWallReplacementMigrationService(
	server *Server,
	recorder *wallReplacementMigrationRecorder,
) {
	server.wallReplacements = wallapp.NewRESTReplacementService(
		wallapp.RESTReplacementServiceDependencies{
			Roles:        recorder,
			Replacements: recorder,
			Refresh:      recorder,
		},
	)
}

func newWallReplacementMigrationDirectRequest(
	t *testing.T,
	fx *wallCharacterizationFixture,
	rawCtx context.Context,
	body io.Reader,
	authenticated bool,
) *http.Request {
	t.Helper()
	req := httptest.NewRequest(
		http.MethodPut,
		wallMutationURL(fx, "/direct"),
		body,
	).WithContext(rawCtx)
	req.Header.Set("Content-Type", "application/json")
	addWallReplacementMigrationSession(t, fx, req, authenticated)
	return req
}

func newWallReplacementMigrationRouteRequest(
	t *testing.T,
	fx *wallCharacterizationFixture,
	body io.Reader,
) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, wallMutationURL(fx, ""), body)
	req.Header.Set("Content-Type", "application/json")
	addWallReplacementMigrationSession(t, fx, req, true)
	return req
}

func addWallReplacementMigrationSession(
	t *testing.T,
	fx *wallCharacterizationFixture,
	req *http.Request,
	authenticated bool,
) {
	t.Helper()
	if !authenticated {
		return
	}
	parsed, err := url.Parse(fx.baseURL)
	if err != nil {
		t.Fatalf("parse fixture URL: %v", err)
	}
	for _, cookie := range fx.client.Jar.Cookies(parsed) {
		req.AddCookie(cookie)
	}
}

func assertWallReplacementMigrationTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trace=%v want=%v", got, want)
	}
}

func assertWallReplacementMigrationResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
	wantWall store.Wall,
) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusOK, response.Body.Bytes())
	}
	var got map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode replacement response %s: %v", response.Body.Bytes(), err)
	}
	assertExactJSONKeys(t, got, "notes", "edges", "version", "updatedAt")
	notes, ok := got["notes"].([]any)
	if !ok {
		t.Fatalf("response notes=%T want array", got["notes"])
	}
	for i, raw := range notes {
		note, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("response note[%d]=%T want object", i, raw)
		}
		assertExactJSONKeys(t, note, "id", "x", "y", "width", "height", "color", "text", "version")
	}
	edges, ok := got["edges"].([]any)
	if !ok {
		t.Fatalf("response edges=%T want array", got["edges"])
	}
	for i, raw := range edges {
		edge, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("response edge[%d]=%T want object", i, raw)
		}
		assertExactJSONKeys(t, edge, "id", "from", "to")
	}
	if want := wallReplacementMigrationJSONValue(wantWall); !reflect.DeepEqual(got, want) {
		t.Fatalf("replacement response=%+v want=%+v", got, want)
	}
}

func wallReplacementMigrationJSONValue(wall store.Wall) map[string]any {
	notes := make([]any, 0, len(wall.Notes))
	for _, note := range wall.Notes {
		notes = append(notes, map[string]any{
			"id": note.ID, "x": note.X, "y": note.Y,
			"width": note.Width, "height": note.Height,
			"color": note.Color, "text": note.Text,
			"version": float64(note.Version),
		})
	}
	edges := make([]any, 0, len(wall.Edges))
	for _, edge := range wall.Edges {
		edges = append(edges, map[string]any{
			"id": edge.ID, "from": edge.From, "to": edge.To,
		})
	}
	return map[string]any{
		"notes": notes, "edges": edges,
		"version": float64(wall.Version), "updatedAt": float64(wall.UpdatedAt),
	}
}

func assertWallReplacementMigrationNoEffects(
	t *testing.T,
	recorder *wallReplacementMigrationRecorder,
	fx *wallCharacterizationFixture,
) {
	t.Helper()
	if len(recorder.replaceCalls) != 0 || len(recorder.refreshCalls) != 0 {
		t.Fatalf("replace/refresh calls=%d/%d", len(recorder.replaceCalls), len(recorder.refreshCalls))
	}
	if events := fx.collector.snapshot(); len(events) != 0 {
		t.Fatalf("unexpected direct handler events=%+v", events)
	}
}

func TestWallReplacementMigrationNewServerComposesApplicationService(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	if fx.server.wallReplacements == nil {
		t.Fatal("NewServer did not compose the REST Wall replacement service")
	}
}

func TestWallReplacementMigrationRefreshPublisherProjection(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	marked := context.WithValue(context.Background(), wallReplacementMigrationContextKey{}, "raw")
	deadline := time.Now().Add(time.Minute)
	rawCtx, cancel := context.WithDeadline(marked, deadline)
	fx.collector.reset()

	wallRefreshPublisher{server: fx.server}.PublishWallRefresh(
		rawCtx,
		fx.project.ID,
		wallapp.RefreshReplaced,
	)
	assertSingleWallRefresh(t, fx, string(wallapp.RefreshReplaced))
	contexts := fx.collector.contextSnapshot()
	if len(contexts) != 1 || contexts[0] != rawCtx {
		t.Fatalf("publisher contexts=%v want exact raw context", contexts)
	}
	if contexts[0].Value(wallReplacementMigrationContextKey{}) != "raw" {
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
}

func TestWallReplacementMigrationHandlerDelegatesOnceWithRetainedContexts(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	wantWall := store.Wall{
		Notes: []store.WallNote{
			{ID: "generated-a", X: 1, Y: 2, Width: 3, Height: 4, Color: "#abcdef", Text: "returned a", Version: 1},
			{ID: "generated-b", X: 5, Y: 6, Width: 7, Height: 8, Color: "#fedcba", Text: "returned b", Version: 9},
		},
		Edges: []store.WallEdge{
			{ID: "retained-edge", From: "old-note-a", To: "old-note-b"},
		},
		Version: 47, UpdatedAt: 1700000000123,
	}
	recorder := &wallReplacementMigrationRecorder{
		role: store.RoleContributor, replaceResult: wantWall,
	}
	installWallReplacementMigrationService(fx.server, recorder)
	fx.collector.reset()
	fx.spy.resetCalls()
	marked := context.WithValue(context.Background(), wallReplacementMigrationContextKey{}, "handler")
	deadline := time.Now().Add(time.Minute)
	rawCtx, cancel := context.WithDeadline(marked, deadline)
	body := newWallReplacementMigrationBody(recorder, `{"notes":[{"x":-100001.25,"y":100001.5,"width":0,"height":-1,"color":" NOT-A-COLOR ","text":" \t raw first \n "},{"x":7.75,"y":-8.5,"width":9.25,"height":10.5,"color":"","text":"second"}]}`)
	req := newWallReplacementMigrationDirectRequest(t, fx, rawCtx, body, true)
	response := httptest.NewRecorder()

	fx.server.handleWallPut(response, req, fx.project.ID)
	assertWallReplacementMigrationResponse(t, response, wantWall)
	assertWallReplacementMigrationTrace(t, recorder.trace, "role", "body", "replace", "refresh")
	if recorder.roleCalls != 1 || recorder.rolePID != fx.project.ID || recorder.roleUID != fx.owner.ID {
		t.Fatalf("role calls/project/user=%d/%d/%d", recorder.roleCalls, recorder.rolePID, recorder.roleUID)
	}
	if len(recorder.replaceCalls) != 1 {
		t.Fatalf("replace calls=%+v want one", recorder.replaceCalls)
	}
	replace := recorder.replaceCalls[0]
	wantNotes := []store.WallNote{
		{X: -100001.25, Y: 100001.5, Width: 0, Height: -1, Color: " NOT-A-COLOR ", Text: " \t raw first \n "},
		{X: 7.75, Y: -8.5, Width: 9.25, Height: 10.5, Color: "", Text: "second"},
	}
	if replace.ctx != recorder.roleCtx || replace.projectID != fx.project.ID || !reflect.DeepEqual(replace.notes, wantNotes) {
		t.Fatalf("replace=%+v want context=%p project=%d notes=%+v", replace, recorder.roleCtx, fx.project.ID, wantNotes)
	}
	if len(recorder.refreshCalls) != 1 {
		t.Fatalf("refresh calls=%+v want one", recorder.refreshCalls)
	}
	refresh := recorder.refreshCalls[0]
	if refresh.ctx != rawCtx || refresh.projectID != fx.project.ID || refresh.reason != wallapp.RefreshReplaced {
		t.Fatalf("refresh=%+v want exact raw context/project/reason", refresh)
	}
	if recorder.roleCtx == rawCtx {
		t.Fatal("role call received raw rather than actor-enriched mutation context")
	}
	if recorder.roleCtx.Value(wallReplacementMigrationContextKey{}) != "handler" {
		t.Fatal("mutation context lost raw marker")
	}
	mutationDeadline, ok := recorder.roleCtx.Deadline()
	if !ok || !mutationDeadline.Equal(deadline) {
		t.Fatalf("mutation deadline=(%v,%v) want=(%v,true)", mutationDeadline, ok, deadline)
	}
	actorID, ok := store.UserIDFromContext(recorder.roleCtx)
	if !ok || actorID != fx.owner.ID {
		t.Fatalf("mutation actor=(%d,%v) want=(%d,true)", actorID, ok, fx.owner.ID)
	}
	if _, ok := store.UserIDFromContext(rawCtx); ok {
		t.Fatal("raw effect context unexpectedly contains actor")
	}
	if events := fx.collector.snapshot(); len(events) != 0 {
		t.Fatalf("handler retained direct event publication=%+v", events)
	}
	if got := fx.spy.callCount("ReplaceWall"); got != 0 {
		t.Fatalf("direct HTTP ReplaceWall calls=%d want=0", got)
	}
	if got := fx.spy.callCount("GetWall"); got != 0 {
		t.Fatalf("HTTP/application GetWall calls=%d want=0", got)
	}
	cancel()
	if recorder.roleCtx.Err() != context.Canceled || refresh.ctx.Err() != context.Canceled {
		t.Fatalf("retained cancellation mutation/refresh=%v/%v", recorder.roleCtx.Err(), refresh.ctx.Err())
	}
}

func TestWallReplacementMigrationEmptyInputsRemainNonNil(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
	}{
		{name: "missing notes", body: `{}`},
		{name: "null notes", body: `{"notes":null}`},
		{name: "empty notes", body: `{"notes":[]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fx := newWallCharacterizationFixture(t, true)
			wantWall := store.Wall{
				Notes: []store.WallNote{}, Edges: []store.WallEdge{},
				Version: 8, UpdatedAt: 9,
			}
			recorder := &wallReplacementMigrationRecorder{
				role: store.RoleContributor, replaceResult: wantWall,
			}
			installWallReplacementMigrationService(fx.server, recorder)
			fx.collector.reset()
			body := newWallReplacementMigrationBody(recorder, tt.body)
			req := newWallReplacementMigrationDirectRequest(t, fx, context.Background(), body, true)
			response := httptest.NewRecorder()

			fx.server.handleWallPut(response, req, fx.project.ID)
			assertWallReplacementMigrationResponse(t, response, wantWall)
			assertWallReplacementMigrationTrace(t, recorder.trace, "role", "body", "replace", "refresh")
			if len(recorder.replaceCalls) != 1 {
				t.Fatalf("replace calls=%+v want one", recorder.replaceCalls)
			}
			if recorder.replaceCalls[0].notes == nil || len(recorder.replaceCalls[0].notes) != 0 {
				t.Fatalf("store notes=%#v want non-nil empty slice", recorder.replaceCalls[0].notes)
			}
			if len(recorder.refreshCalls) != 1 || recorder.refreshCalls[0].reason != wallapp.RefreshReplaced {
				t.Fatalf("refresh calls=%+v want one replaced refresh", recorder.refreshCalls)
			}
			if events := fx.collector.snapshot(); len(events) != 0 {
				t.Fatalf("handler retained direct event publication=%+v", events)
			}
		})
	}
}

func TestWallReplacementMigrationPreparationPrecedesBody(t *testing.T) {
	tests := []struct {
		name          string
		authenticated bool
		role          store.ProjectRole
		roleErr       error
		wantStatus    int
		wantCode      string
		wantMessage   string
		wantDetails   map[string]any
		wantTrace     []string
		wantRoleCalls int
	}{
		{
			name: "actor absent", role: store.RoleContributor,
			wantStatus: http.StatusUnauthorized, wantCode: "UNAUTHORIZED", wantMessage: "unauthorized",
		},
		{
			name: "role read error", authenticated: true, roleErr: errors.New("role failed"),
			wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN", wantMessage: "contributor or higher required",
			wantTrace: []string{"role"}, wantRoleCalls: 1,
		},
		{
			name: "viewer", authenticated: true, role: store.RoleViewer,
			wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN", wantMessage: "contributor or higher required",
			wantTrace: []string{"role"}, wantRoleCalls: 1,
		},
		{
			name: "authorized malformed body", authenticated: true, role: store.RoleContributor,
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "invalid json",
			wantDetails: map[string]any{"reason": "invalid_json", "detail": "unexpected EOF"},
			wantTrace:   []string{"role", "body"}, wantRoleCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newWallCharacterizationFixture(t, true)
			recorder := &wallReplacementMigrationRecorder{role: tt.role, roleErr: tt.roleErr}
			installWallReplacementMigrationService(fx.server, recorder)
			fx.collector.reset()
			body := newWallReplacementMigrationBody(recorder, "{")
			req := newWallReplacementMigrationDirectRequest(
				t, fx, context.Background(), body, tt.authenticated,
			)
			response := httptest.NewRecorder()

			fx.server.handleWallPut(response, req, fx.project.ID)
			if response.Code != tt.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tt.wantStatus, response.Body.Bytes())
			}
			assertWallError(t, response.Body.Bytes(), tt.wantCode, tt.wantMessage, tt.wantDetails)
			if recorder.roleCalls != tt.wantRoleCalls {
				t.Fatalf("role calls=%d want=%d", recorder.roleCalls, tt.wantRoleCalls)
			}
			assertWallReplacementMigrationTrace(t, recorder.trace, tt.wantTrace...)
			assertWallReplacementMigrationNoEffects(t, recorder, fx)
		})
	}
}

func TestWallReplacementMigrationPUTStillBypassesGlobalHeaderGate(t *testing.T) {
	t.Run("headerless malformed PUT reaches body decoder", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		recorder := &wallReplacementMigrationRecorder{role: store.RoleContributor}
		installWallReplacementMigrationService(fx.server, recorder)
		fx.collector.reset()
		body := newWallReplacementMigrationBody(recorder, "{")
		req := newWallReplacementMigrationRouteRequest(t, fx, body)
		response := httptest.NewRecorder()

		fx.server.ServeHTTP(response, req)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusBadRequest, response.Body.Bytes())
		}
		assertWallError(t, response.Body.Bytes(), "VALIDATION_ERROR", "invalid json", map[string]any{
			"reason": "invalid_json", "detail": "unexpected EOF",
		})
		if recorder.roleCalls != 1 {
			t.Fatalf("role calls=%d want=1", recorder.roleCalls)
		}
		assertWallReplacementMigrationTrace(t, recorder.trace, "role", "body")
		assertWallReplacementMigrationNoEffects(t, recorder, fx)
	})

	t.Run("headerless valid PUT reaches replacement", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		wantWall := store.Wall{
			Notes: []store.WallNote{}, Edges: []store.WallEdge{},
			Version: 12, UpdatedAt: 13,
		}
		recorder := &wallReplacementMigrationRecorder{
			role: store.RoleContributor, replaceResult: wantWall,
		}
		installWallReplacementMigrationService(fx.server, recorder)
		fx.collector.reset()
		body := newWallReplacementMigrationBody(recorder, `{"notes":[]}`)
		req := newWallReplacementMigrationRouteRequest(t, fx, body)
		response := httptest.NewRecorder()

		fx.server.ServeHTTP(response, req)
		assertWallReplacementMigrationResponse(t, response, wantWall)
		assertWallReplacementMigrationTrace(t, recorder.trace, "role", "body", "replace", "refresh")
		if recorder.roleCalls != 1 || len(recorder.replaceCalls) != 1 || len(recorder.refreshCalls) != 1 {
			t.Fatalf("role/replace/refresh=%d/%d/%d", recorder.roleCalls, len(recorder.replaceCalls), len(recorder.refreshCalls))
		}
		if recorder.replaceCalls[0].notes == nil || len(recorder.replaceCalls[0].notes) != 0 {
			t.Fatalf("store notes=%#v want non-nil empty", recorder.replaceCalls[0].notes)
		}
		if events := fx.collector.snapshot(); len(events) != 0 {
			t.Fatalf("handler retained direct event publication=%+v", events)
		}
	})
}

func TestWallReplacementMigrationExecutionErrorsUseExistingProjectionAndPublishNothing(t *testing.T) {
	tests := []struct {
		name        string
		replaceErr  error
		wantStatus  int
		wantCode    string
		wantMessage string
		wantDetails map[string]any
	}{
		{
			name:       "note limit validation",
			replaceErr: fmt.Errorf("%w: wall note limit reached", store.ErrValidation),
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR",
			wantMessage: "validation: wall note limit reached",
			wantDetails: map[string]any{"reason": "wall_note_limit_reached"},
		},
		{
			name:       "note validation",
			replaceErr: fmt.Errorf("%w: invalid color", store.ErrValidation),
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR",
			wantMessage: "validation: invalid color",
			wantDetails: map[string]any{"reason": "invalid_color"},
		},
		{
			name:       "internal failure",
			replaceErr: errors.New("injected replacement failure"),
			wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL",
			wantMessage: "internal error",
			wantDetails: map[string]any{"detail": "injected replacement failure"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newWallCharacterizationFixture(t, true)
			recorder := &wallReplacementMigrationRecorder{
				role: store.RoleContributor,
				replaceResult: store.Wall{
					Notes:   []store.WallNote{{ID: "must-not-project", Text: "ignored", Version: 99}},
					Version: 99,
				},
				replaceErr: tt.replaceErr,
			}
			installWallReplacementMigrationService(fx.server, recorder)
			fx.collector.reset()
			fx.spy.resetCalls()
			body := newWallReplacementMigrationBody(recorder, `{"notes":[]}`)
			req := newWallReplacementMigrationDirectRequest(t, fx, context.Background(), body, true)
			response := httptest.NewRecorder()

			fx.server.handleWallPut(response, req, fx.project.ID)
			if response.Code != tt.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tt.wantStatus, response.Body.Bytes())
			}
			assertWallError(t, response.Body.Bytes(), tt.wantCode, tt.wantMessage, tt.wantDetails)
			if recorder.roleCalls != 1 || len(recorder.replaceCalls) != 1 || len(recorder.refreshCalls) != 0 {
				t.Fatalf("role/replace/refresh=%d/%d/%d", recorder.roleCalls, len(recorder.replaceCalls), len(recorder.refreshCalls))
			}
			assertWallReplacementMigrationTrace(t, recorder.trace, "role", "body", "replace")
			if events := fx.collector.snapshot(); len(events) != 0 {
				t.Fatalf("failed replacement published direct events=%+v", events)
			}
			if got := fx.spy.callCount("ReplaceWall"); got != 0 {
				t.Fatalf("direct HTTP ReplaceWall calls=%d want=0", got)
			}
			if got := fx.spy.callCount("GetWall"); got != 0 {
				t.Fatalf("HTTP/application GetWall calls=%d want=0", got)
			}
		})
	}
}
