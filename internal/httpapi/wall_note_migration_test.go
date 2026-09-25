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

type wallNoteMigrationContextKey struct{}

type wallNoteMigrationCreateCall struct {
	ctx       context.Context
	projectID int64
	input     store.CreateNoteInput
}

type wallNoteMigrationPatchCall struct {
	ctx       context.Context
	projectID int64
	noteID    string
	input     store.PatchNoteInput
}

type wallNoteMigrationDeleteCall struct {
	ctx       context.Context
	projectID int64
	noteID    string
}

type wallNoteMigrationRefreshCall struct {
	ctx       context.Context
	projectID int64
	reason    wallapp.RefreshReason
}

type wallNoteMigrationRecorder struct {
	trace []string

	role      store.ProjectRole
	roleErr   error
	roleCalls int
	roleCtx   context.Context
	rolePID   int64
	roleUID   int64

	createCalls  []wallNoteMigrationCreateCall
	createResult store.WallNote
	createWall   store.Wall
	createErr    error

	patchCalls  []wallNoteMigrationPatchCall
	patchResult store.WallNote
	patchWall   store.Wall
	patchErr    error

	deleteCalls []wallNoteMigrationDeleteCall
	deleteWall  store.Wall
	deleteErr   error

	refreshCalls []wallNoteMigrationRefreshCall
}

var (
	_ wallapp.RESTWriterRoleStore  = (*wallNoteMigrationRecorder)(nil)
	_ wallapp.NoteMutationStore    = (*wallNoteMigrationRecorder)(nil)
	_ wallapp.WallRefreshPublisher = (*wallNoteMigrationRecorder)(nil)
)

func (r *wallNoteMigrationRecorder) record(step string) {
	r.trace = append(r.trace, step)
}

func (r *wallNoteMigrationRecorder) GetProjectRole(
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

func (r *wallNoteMigrationRecorder) CreateNote(
	ctx context.Context,
	projectID int64,
	input store.CreateNoteInput,
) (store.WallNote, store.Wall, error) {
	r.record("create")
	r.createCalls = append(r.createCalls, wallNoteMigrationCreateCall{
		ctx: ctx, projectID: projectID, input: input,
	})
	return r.createResult, r.createWall, r.createErr
}

func (r *wallNoteMigrationRecorder) PatchNote(
	ctx context.Context,
	projectID int64,
	noteID string,
	input store.PatchNoteInput,
) (store.WallNote, store.Wall, error) {
	r.record("patch")
	r.patchCalls = append(r.patchCalls, wallNoteMigrationPatchCall{
		ctx: ctx, projectID: projectID, noteID: noteID, input: cloneWallNoteMigrationPatchInput(input),
	})
	return r.patchResult, r.patchWall, r.patchErr
}

func (r *wallNoteMigrationRecorder) DeleteNote(
	ctx context.Context,
	projectID int64,
	noteID string,
) (store.Wall, error) {
	r.record("delete")
	r.deleteCalls = append(r.deleteCalls, wallNoteMigrationDeleteCall{
		ctx: ctx, projectID: projectID, noteID: noteID,
	})
	return r.deleteWall, r.deleteErr
}

func (r *wallNoteMigrationRecorder) PublishWallRefresh(
	ctx context.Context,
	projectID int64,
	reason wallapp.RefreshReason,
) {
	r.record("refresh")
	r.refreshCalls = append(r.refreshCalls, wallNoteMigrationRefreshCall{
		ctx: ctx, projectID: projectID, reason: reason,
	})
}

func (r *wallNoteMigrationRecorder) mutationCalls() int {
	return len(r.createCalls) + len(r.patchCalls) + len(r.deleteCalls)
}

func cloneWallNoteMigrationPatchInput(input store.PatchNoteInput) store.PatchNoteInput {
	return store.PatchNoteInput{
		IfVersion: input.IfVersion,
		X:         cloneWallNoteMigrationFloat(input.X),
		Y:         cloneWallNoteMigrationFloat(input.Y),
		Width:     cloneWallNoteMigrationFloat(input.Width),
		Height:    cloneWallNoteMigrationFloat(input.Height),
		Color:     cloneWallNoteMigrationString(input.Color),
		Text:      cloneWallNoteMigrationString(input.Text),
	}
}

func cloneWallNoteMigrationFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneWallNoteMigrationString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

type wallNoteMigrationBody struct {
	recorder *wallNoteMigrationRecorder
	reader   io.Reader
	recorded bool
}

func newWallNoteMigrationBody(recorder *wallNoteMigrationRecorder, body string) *wallNoteMigrationBody {
	return &wallNoteMigrationBody{recorder: recorder, reader: bytes.NewBufferString(body)}
}

func (b *wallNoteMigrationBody) Read(p []byte) (int, error) {
	if !b.recorded {
		b.recorded = true
		b.recorder.record("body")
	}
	return b.reader.Read(p)
}

func installWallNoteMigrationService(server *Server, recorder *wallNoteMigrationRecorder) {
	server.wallNoteMutations = wallapp.NewRESTNoteService(wallapp.RESTNoteServiceDependencies{
		Roles:     recorder,
		Mutations: recorder,
		Refresh:   recorder,
	})
}

func newWallNoteMigrationDirectRequest(
	t *testing.T,
	fx *wallCharacterizationFixture,
	method string,
	rawCtx context.Context,
	body io.Reader,
	authenticated bool,
) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, wallMutationURL(fx, "/direct"), body).WithContext(rawCtx)
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

func assertWallNoteMigrationTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trace=%v want=%v", got, want)
	}
}

func assertWallNoteMigrationResponse(t *testing.T, recorder *httptest.ResponseRecorder, status int, note store.WallNote) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, status, recorder.Body.Bytes())
	}
	if status == http.StatusNoContent {
		if recorder.Body.Len() != 0 {
			t.Fatalf("204 body=%q want empty", recorder.Body.Bytes())
		}
		return
	}
	var got map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode note response %s: %v", recorder.Body.Bytes(), err)
	}
	assertExactJSONKeys(t, got, "id", "x", "y", "width", "height", "color", "text", "version")
	want := map[string]any{
		"id": note.ID, "x": note.X, "y": note.Y, "width": note.Width,
		"height": note.Height, "color": note.Color, "text": note.Text,
		"version": float64(note.Version),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("note response=%+v want=%+v", got, want)
	}
}

func assertWallNoteMigrationNoEffects(t *testing.T, recorder *wallNoteMigrationRecorder, fx *wallCharacterizationFixture) {
	t.Helper()
	if recorder.mutationCalls() != 0 || len(recorder.refreshCalls) != 0 {
		t.Fatalf("mutation/refresh calls=%d/%d recorder=%+v", recorder.mutationCalls(), len(recorder.refreshCalls), recorder)
	}
	if events := fx.collector.snapshot(); len(events) != 0 {
		t.Fatalf("unexpected direct handler events=%+v", events)
	}
}

func TestWallNoteMigrationNewServerComposesApplicationService(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	if fx.server.wallNoteMutations == nil {
		t.Fatal("NewServer did not compose the REST Wall note service")
	}
}

func TestWallNoteMigrationRefreshPublisherProjection(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	publisher := wallRefreshPublisher{server: fx.server}

	for _, tt := range []struct {
		name   string
		reason wallapp.RefreshReason
	}{
		{name: "created", reason: wallapp.RefreshNoteCreated},
		{name: "updated", reason: wallapp.RefreshNoteUpdated},
		{name: "deleted", reason: wallapp.RefreshNoteDeleted},
	} {
		t.Run(tt.name, func(t *testing.T) {
			marked := context.WithValue(context.Background(), wallNoteMigrationContextKey{}, tt.name)
			deadline := time.Now().Add(time.Minute)
			rawCtx, cancel := context.WithDeadline(marked, deadline)
			fx.collector.reset()

			publisher.PublishWallRefresh(rawCtx, fx.project.ID, tt.reason)
			assertSingleWallRefresh(t, fx, string(tt.reason))
			contexts := fx.collector.contextSnapshot()
			if len(contexts) != 1 || contexts[0] != rawCtx {
				t.Fatalf("publisher contexts=%v want exact raw context", contexts)
			}
			if contexts[0].Value(wallNoteMigrationContextKey{}) != tt.name {
				t.Fatal("publisher context lost raw marker")
			}
			gotDeadline, ok := contexts[0].Deadline()
			if !ok || !gotDeadline.Equal(deadline) {
				t.Fatalf("publisher context deadline = (%v, %v), want (%v, true)", gotDeadline, ok, deadline)
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

func TestWallNoteMigrationHandlersDelegateOnceWithRetainedContexts(t *testing.T) {
	wantCreateInput := store.CreateNoteInput{
		X: -100001.25, Y: 100001.5, Width: 0, Height: -1,
		Color: " NOT-A-COLOR ", Text: " \t raw create \n ",
	}
	wantCreateNote := store.WallNote{
		ID: "created-result", X: 1, Y: 2, Width: 3, Height: 4,
		Color: "#abcdef", Text: "created response", Version: 7,
	}
	wantPatchNote := store.WallNote{
		ID: "patched-result", X: 5, Y: 6, Width: 7, Height: 8,
		Color: "#fedcba", Text: "patched response", Version: 12,
	}

	tests := []struct {
		name       string
		method     string
		body       string
		status     int
		resultNote store.WallNote
		wantTrace  []string
		wantReason wallapp.RefreshReason
		invoke     func(*Server, http.ResponseWriter, *http.Request, int64)
		assertCall func(*testing.T, *wallNoteMigrationRecorder, context.Context)
	}{
		{
			name: "create", method: http.MethodPost,
			body: fmt.Sprintf(`{"x":%v,"y":%v,"width":%v,"height":%v,"color":%q,"text":%q}`,
				wantCreateInput.X, wantCreateInput.Y, wantCreateInput.Width, wantCreateInput.Height,
				wantCreateInput.Color, wantCreateInput.Text),
			status: http.StatusCreated, resultNote: wantCreateNote,
			wantTrace: []string{"role", "body", "create", "refresh"}, wantReason: wallapp.RefreshNoteCreated,
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallCreateNote(w, r, projectID)
			},
			assertCall: func(t *testing.T, recorder *wallNoteMigrationRecorder, mutationCtx context.Context) {
				t.Helper()
				if len(recorder.createCalls) != 1 || recorder.createCalls[0].ctx != mutationCtx || recorder.createCalls[0].input != wantCreateInput {
					t.Fatalf("create calls=%+v wantInput=%+v", recorder.createCalls, wantCreateInput)
				}
			},
		},
		{
			name: "patch", method: http.MethodPatch,
			body:   `{"ifVersion":-9,"x":0,"height":-3,"color":" \t ","text":""}`,
			status: http.StatusOK, resultNote: wantPatchNote,
			wantTrace: []string{"role", "body", "patch", "refresh"}, wantReason: wallapp.RefreshNoteUpdated,
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallPatchNote(w, r, projectID, " \t raw-note-id \n ")
			},
			assertCall: func(t *testing.T, recorder *wallNoteMigrationRecorder, mutationCtx context.Context) {
				t.Helper()
				if len(recorder.patchCalls) != 1 {
					t.Fatalf("patch calls=%+v", recorder.patchCalls)
				}
				call := recorder.patchCalls[0]
				if call.ctx != mutationCtx || call.noteID != "raw-note-id" || call.input.IfVersion != -9 ||
					call.input.X == nil || *call.input.X != 0 || call.input.Y != nil || call.input.Width != nil ||
					call.input.Height == nil || *call.input.Height != -3 || call.input.Color == nil || *call.input.Color != " \t " ||
					call.input.Text == nil || *call.input.Text != "" {
					t.Fatalf("patch call=%+v", call)
				}
			},
		},
		{
			name: "delete", method: http.MethodDelete, status: http.StatusNoContent,
			wantTrace: []string{"role", "delete", "refresh"}, wantReason: wallapp.RefreshNoteDeleted,
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallDeleteNote(w, r, projectID, " \t raw-delete-id \n ")
			},
			assertCall: func(t *testing.T, recorder *wallNoteMigrationRecorder, mutationCtx context.Context) {
				t.Helper()
				if len(recorder.deleteCalls) != 1 || recorder.deleteCalls[0].ctx != mutationCtx || recorder.deleteCalls[0].noteID != "raw-delete-id" {
					t.Fatalf("delete calls=%+v", recorder.deleteCalls)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newWallCharacterizationFixture(t, true)
			recorder := &wallNoteMigrationRecorder{
				role:         store.RoleContributor,
				createResult: tt.resultNote, createWall: store.Wall{Version: 91},
				patchResult: tt.resultNote, patchWall: store.Wall{Version: 92},
				deleteWall: store.Wall{Version: 93},
			}
			installWallNoteMigrationService(fx.server, recorder)
			fx.collector.reset()
			marked := context.WithValue(context.Background(), wallNoteMigrationContextKey{}, tt.name)
			deadline := time.Now().Add(time.Minute)
			rawCtx, cancel := context.WithDeadline(marked, deadline)
			body := io.Reader(nil)
			if tt.body != "" {
				body = newWallNoteMigrationBody(recorder, tt.body)
			}
			req := newWallNoteMigrationDirectRequest(t, fx, tt.method, rawCtx, body, true)
			response := httptest.NewRecorder()

			tt.invoke(fx.server, response, req, fx.project.ID)
			assertWallNoteMigrationResponse(t, response, tt.status, tt.resultNote)
			assertWallNoteMigrationTrace(t, recorder.trace, tt.wantTrace...)
			if recorder.roleCalls != 1 || recorder.rolePID != fx.project.ID || recorder.roleUID != fx.owner.ID {
				t.Fatalf("role calls/project/user=%d/%d/%d", recorder.roleCalls, recorder.rolePID, recorder.roleUID)
			}
			if recorder.mutationCalls() != 1 {
				t.Fatalf("mutation calls=%d", recorder.mutationCalls())
			}
			tt.assertCall(t, recorder, recorder.roleCtx)
			if len(recorder.refreshCalls) != 1 {
				t.Fatalf("refresh calls=%+v", recorder.refreshCalls)
			}
			refresh := recorder.refreshCalls[0]
			if refresh.ctx != rawCtx || refresh.projectID != fx.project.ID || refresh.reason != tt.wantReason {
				t.Fatalf("refresh=%+v want exact raw context/project/reason", refresh)
			}
			if recorder.roleCtx == rawCtx {
				t.Fatal("role call received raw rather than enriched mutation context")
			}
			if recorder.roleCtx.Value(wallNoteMigrationContextKey{}) != tt.name {
				t.Fatal("mutation context lost raw request marker")
			}
			mutationDeadline, ok := recorder.roleCtx.Deadline()
			if !ok || !mutationDeadline.Equal(deadline) {
				t.Fatalf("mutation context deadline = (%v, %v), want (%v, true)", mutationDeadline, ok, deadline)
			}
			actorID, ok := store.UserIDFromContext(recorder.roleCtx)
			if !ok || actorID != fx.owner.ID {
				t.Fatalf("mutation actor=%d,%v want=%d,true", actorID, ok, fx.owner.ID)
			}
			if _, ok := store.UserIDFromContext(rawCtx); ok {
				t.Fatal("raw effect context unexpectedly contains actor")
			}
			refreshDeadline, ok := refresh.ctx.Deadline()
			if !ok || !refreshDeadline.Equal(deadline) {
				t.Fatalf("refresh context deadline = (%v, %v), want (%v, true)", refreshDeadline, ok, deadline)
			}
			if events := fx.collector.snapshot(); len(events) != 0 {
				t.Fatalf("handler retained direct event publication=%+v", events)
			}
			cancel()
			if refresh.ctx.Err() != context.Canceled {
				t.Fatalf("refresh context cancellation=%v want=%v", refresh.ctx.Err(), context.Canceled)
			}
		})
	}
}

func TestWallNoteMigrationPreparationPrecedesInput(t *testing.T) {
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
			name: "actor absent before create body", role: store.RoleContributor, method: http.MethodPost, body: "{",
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallCreateNote(w, r, projectID)
			},
			wantStatus: http.StatusUnauthorized, wantCode: "UNAUTHORIZED", wantMessage: "unauthorized",
		},
		{
			name: "role read error before create body", roleErr: errors.New("role failed"), authenticated: true,
			method: http.MethodPost, body: "{",
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallCreateNote(w, r, projectID)
			},
			wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN", wantMessage: "contributor or higher required",
			wantTrace: []string{"role"}, wantRoleCalls: 1,
		},
		{
			name: "viewer before blank patch path and body", role: store.RoleViewer, authenticated: true,
			method: http.MethodPatch, body: "{",
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallPatchNote(w, r, projectID, " \t ")
			},
			wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN", wantMessage: "contributor or higher required",
			wantTrace: []string{"role"}, wantRoleCalls: 1,
		},
		{
			name: "viewer before blank delete path", role: store.RoleViewer, authenticated: true,
			method: http.MethodDelete,
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallDeleteNote(w, r, projectID, " \t ")
			},
			wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN", wantMessage: "contributor or higher required",
			wantTrace: []string{"role"}, wantRoleCalls: 1,
		},
		{
			name: "authorized malformed create body", role: store.RoleContributor, authenticated: true,
			method: http.MethodPost, body: "{",
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallCreateNote(w, r, projectID)
			},
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "invalid json",
			wantDetails: map[string]any{"reason": "invalid_json", "detail": "unexpected EOF"},
			wantTrace:   []string{"role", "body"}, wantRoleCalls: 1,
		},
		{
			name: "authorized malformed patch body", role: store.RoleContributor, authenticated: true,
			method: http.MethodPatch, body: "{",
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallPatchNote(w, r, projectID, "note")
			},
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "invalid json",
			wantDetails: map[string]any{"reason": "invalid_json", "detail": "unexpected EOF"},
			wantTrace:   []string{"role", "body"}, wantRoleCalls: 1,
		},
		{
			name: "authorized blank patch path before body", role: store.RoleContributor, authenticated: true,
			method: http.MethodPatch, body: "{}",
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallPatchNote(w, r, projectID, " \t ")
			},
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "noteId required",
			wantDetails: map[string]any{"reason": "note_id_required", "field": "noteId"},
			wantTrace:   []string{"role"}, wantRoleCalls: 1,
		},
		{
			name: "authorized blank delete path", role: store.RoleContributor, authenticated: true,
			method: http.MethodDelete,
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallDeleteNote(w, r, projectID, " \t ")
			},
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "noteId required",
			wantDetails: map[string]any{"reason": "note_id_required", "field": "noteId"},
			wantTrace:   []string{"role"}, wantRoleCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newWallCharacterizationFixture(t, true)
			recorder := &wallNoteMigrationRecorder{role: tt.role, roleErr: tt.roleErr}
			installWallNoteMigrationService(fx.server, recorder)
			fx.collector.reset()
			body := io.Reader(nil)
			if tt.body != "" {
				body = newWallNoteMigrationBody(recorder, tt.body)
			}
			req := newWallNoteMigrationDirectRequest(t, fx, tt.method, context.Background(), body, tt.authenticated)
			response := httptest.NewRecorder()

			tt.invoke(fx.server, response, req, fx.project.ID)
			if response.Code != tt.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tt.wantStatus, response.Body.Bytes())
			}
			assertWallError(t, response.Body.Bytes(), tt.wantCode, tt.wantMessage, tt.wantDetails)
			if recorder.roleCalls != tt.wantRoleCalls {
				t.Fatalf("role calls=%d want=%d", recorder.roleCalls, tt.wantRoleCalls)
			}
			assertWallNoteMigrationTrace(t, recorder.trace, tt.wantTrace...)
			assertWallNoteMigrationNoEffects(t, recorder, fx)
		})
	}
}

func TestWallNoteMigrationExecutionErrorsUseExistingProjectionAndPublishNothing(t *testing.T) {
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
			name: "create validation", method: http.MethodPost, body: `{}`,
			mutationErr: fmt.Errorf("%w: invalid color", store.ErrValidation),
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallCreateNote(w, r, projectID)
			},
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "validation: invalid color",
			wantDetails: map[string]any{"reason": "invalid_color"}, wantTrace: []string{"role", "body", "create"},
		},
		{
			name: "patch conflict", method: http.MethodPatch, body: `{"ifVersion":7}`,
			mutationErr: fmt.Errorf("%w: note version mismatch", store.ErrConflict),
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallPatchNote(w, r, projectID, "note")
			},
			wantStatus: http.StatusConflict, wantCode: "CONFLICT", wantMessage: "conflict: note version mismatch",
			wantTrace: []string{"role", "body", "patch"},
		},
		{
			name: "delete not found", method: http.MethodDelete, mutationErr: store.ErrNotFound,
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallDeleteNote(w, r, projectID, "missing")
			},
			wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMessage: "not found",
			wantTrace: []string{"role", "delete"},
		},
		{
			name: "create internal", method: http.MethodPost, body: `{}`, mutationErr: errors.New("injected write failure"),
			invoke: func(server *Server, w http.ResponseWriter, r *http.Request, projectID int64) {
				server.handleWallCreateNote(w, r, projectID)
			},
			wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL", wantMessage: "internal error",
			wantDetails: map[string]any{"detail": "injected write failure"}, wantTrace: []string{"role", "body", "create"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newWallCharacterizationFixture(t, true)
			recorder := &wallNoteMigrationRecorder{role: store.RoleContributor}
			switch tt.wantTrace[len(tt.wantTrace)-1] {
			case "create":
				recorder.createErr = tt.mutationErr
			case "patch":
				recorder.patchErr = tt.mutationErr
			case "delete":
				recorder.deleteErr = tt.mutationErr
			}
			installWallNoteMigrationService(fx.server, recorder)
			fx.collector.reset()
			body := io.Reader(nil)
			if tt.body != "" {
				body = newWallNoteMigrationBody(recorder, tt.body)
			}
			req := newWallNoteMigrationDirectRequest(t, fx, tt.method, context.Background(), body, true)
			response := httptest.NewRecorder()

			tt.invoke(fx.server, response, req, fx.project.ID)
			if response.Code != tt.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tt.wantStatus, response.Body.Bytes())
			}
			assertWallError(t, response.Body.Bytes(), tt.wantCode, tt.wantMessage, tt.wantDetails)
			if recorder.roleCalls != 1 || recorder.mutationCalls() != 1 || len(recorder.refreshCalls) != 0 {
				t.Fatalf("role/mutation/refresh=%d/%d/%d", recorder.roleCalls, recorder.mutationCalls(), len(recorder.refreshCalls))
			}
			assertWallNoteMigrationTrace(t, recorder.trace, tt.wantTrace...)
			if events := fx.collector.snapshot(); len(events) != 0 {
				t.Fatalf("failed mutation published direct events=%+v", events)
			}
		})
	}
}
