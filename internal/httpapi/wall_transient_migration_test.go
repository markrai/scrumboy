package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	wallapp "scrumboy/internal/application/wall"
	"scrumboy/internal/store"
)

type wallTransientMigrationContextKey struct{}

type wallTransientMigrationPublishCall struct {
	ctx       context.Context
	projectID int64
	event     wallapp.TransientEvent
}

type wallTransientMigrationRecorder struct {
	trace []string

	role      store.ProjectRole
	roleErr   error
	roleCalls int
	roleCtx   context.Context
	rolePID   int64
	roleUID   int64

	publishCalls []wallTransientMigrationPublishCall
	publishErr   error
}

var (
	_ wallapp.RESTWriterRoleStore    = (*wallTransientMigrationRecorder)(nil)
	_ wallapp.WallTransientPublisher = (*wallTransientMigrationRecorder)(nil)
)

func (r *wallTransientMigrationRecorder) record(step string) {
	r.trace = append(r.trace, step)
}

func (r *wallTransientMigrationRecorder) GetProjectRole(
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

func (r *wallTransientMigrationRecorder) PublishWallTransient(
	ctx context.Context,
	projectID int64,
	event wallapp.TransientEvent,
) error {
	r.record("publish")
	r.publishCalls = append(r.publishCalls, wallTransientMigrationPublishCall{
		ctx: ctx, projectID: projectID, event: event,
	})
	return r.publishErr
}

type wallTransientMigrationBody struct {
	recorder *wallTransientMigrationRecorder
	reader   io.Reader
	recorded bool
}

func newWallTransientMigrationBody(
	recorder *wallTransientMigrationRecorder,
	body string,
) *wallTransientMigrationBody {
	return &wallTransientMigrationBody{
		recorder: recorder,
		reader:   bytes.NewBufferString(body),
	}
}

func (b *wallTransientMigrationBody) Read(p []byte) (int, error) {
	if !b.recorded {
		b.recorded = true
		b.recorder.record("body")
	}
	return b.reader.Read(p)
}

func installWallTransientMigrationService(
	server *Server,
	recorder *wallTransientMigrationRecorder,
) {
	server.wallTransientMutations = wallapp.NewRESTTransientService(
		wallapp.RESTTransientServiceDependencies{
			Roles:     recorder,
			Publisher: recorder,
		},
	)
}

func newWallTransientMigrationRequest(
	t *testing.T,
	fx *wallCharacterizationFixture,
	requestURL string,
	rawCtx context.Context,
	body io.Reader,
	authenticated bool,
) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, requestURL, body).WithContext(rawCtx)
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

func assertWallTransientMigrationTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trace=%v want=%v", got, want)
	}
}

func assertWallTransientMigrationNoEffects(
	t *testing.T,
	recorder *wallTransientMigrationRecorder,
	fx *wallCharacterizationFixture,
) {
	t.Helper()
	if len(recorder.publishCalls) != 0 {
		t.Fatalf("publisher calls=%+v want none", recorder.publishCalls)
	}
	if events := fx.collector.snapshot(); len(events) != 0 {
		t.Fatalf("unexpected direct handler events=%+v", events)
	}
}

func assertWallTransientMigrationNoDirectStoreCalls(
	t *testing.T,
	fx *wallCharacterizationFixture,
) {
	t.Helper()
	for _, operation := range []string{
		"GetProjectRole", "GetWall", "CreateNote", "PatchNote", "DeleteNote",
		"ReplaceWall", "CreateEdge", "DeleteEdge",
	} {
		if got := fx.spy.callCount(operation); got != 0 {
			t.Fatalf("direct fixture store %s calls=%d want=0", operation, got)
		}
	}
}

func assertWallTransientMigrationResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d want=%d body=%s", response.Code, status, response.Body.Bytes())
	}
	if response.Body.Len() != 0 {
		t.Fatalf("response body=%q want empty", response.Body.Bytes())
	}
}

func assertWallTransientMigrationPayload(
	t *testing.T,
	raw json.RawMessage,
	wantNoteID string,
	wantX float64,
	wantY float64,
	wantBy int64,
) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode transient payload %s: %v", raw, err)
	}
	assertExactJSONKeys(t, got, "noteId", "x", "y", "by")
	want := map[string]any{
		"noteId": wantNoteID,
		"x":      wantX,
		"y":      wantY,
		"by":     float64(wantBy),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("transient payload=%+v want=%+v", got, want)
	}
}

func TestWallTransientMigrationNewServerComposesApplicationService(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	if fx.server.wallTransientMutations == nil {
		t.Fatal("NewServer did not compose the REST Wall transient service")
	}
}

func TestWallTransientMigrationPublisherProjection(t *testing.T) {
	t.Run("exact payload and raw context", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		publisher := wallTransientPublisher{server: fx.server}
		marked := context.WithValue(context.Background(), wallTransientMigrationContextKey{}, "raw")
		deadline := time.Now().Add(time.Minute)
		rawCtx, cancel := context.WithDeadline(marked, deadline)
		fx.collector.reset()

		err := publisher.PublishWallTransient(rawCtx, fx.project.ID, wallapp.TransientEvent{
			NoteID: " raw-note ", X: -11.25, Y: 22.5, By: 71,
		})
		if err != nil {
			t.Fatalf("publish: %v", err)
		}
		events := fx.collector.snapshot()
		if len(events) != 1 {
			t.Fatalf("events=%+v want one", events)
		}
		if events[0].Type != "wall.transient" || events[0].ProjectID != fx.project.ID {
			t.Fatalf("event type/project=%q/%d", events[0].Type, events[0].ProjectID)
		}
		wantBytes := `{"by":71,"noteId":" raw-note ","x":-11.25,"y":22.5}`
		if string(events[0].Payload) != wantBytes {
			t.Fatalf("payload bytes=%s want=%s", events[0].Payload, wantBytes)
		}
		assertWallTransientMigrationPayload(t, events[0].Payload, " raw-note ", -11.25, 22.5, 71)

		contexts := fx.collector.contextSnapshot()
		if len(contexts) != 1 || contexts[0] != rawCtx {
			t.Fatalf("publisher contexts=%v want exact raw context", contexts)
		}
		if contexts[0].Value(wallTransientMigrationContextKey{}) != "raw" {
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

	t.Run("serialization failure emits nothing", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		publisher := wallTransientPublisher{server: fx.server}
		fx.collector.reset()

		err := publisher.PublishWallTransient(context.Background(), fx.project.ID, wallapp.TransientEvent{
			NoteID: "note", X: math.NaN(), Y: 2, By: 71,
		})
		var unsupported *json.UnsupportedValueError
		if !errors.As(err, &unsupported) {
			t.Fatalf("error=%T %v want *json.UnsupportedValueError", err, err)
		}
		if events := fx.collector.snapshot(); len(events) != 0 {
			t.Fatalf("serialization failure events=%+v want none", events)
		}
	})
}

func TestWallTransientMigrationHandlerDelegatesOnceWithRetainedContexts(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	recorder := &wallTransientMigrationRecorder{role: store.RoleContributor}
	installWallTransientMigrationService(fx.server, recorder)
	fx.collector.reset()
	fx.spy.resetCalls()
	marked := context.WithValue(context.Background(), wallTransientMigrationContextKey{}, "handler")
	deadline := time.Now().Add(time.Minute)
	rawCtx, cancel := context.WithDeadline(marked, deadline)
	body := newWallTransientMigrationBody(recorder, `{"noteId":" \t retained-note \n ","x":-7.25,"y":18.5}`)
	req := newWallTransientMigrationRequest(
		t, fx, wallMutationURL(fx, "/direct"), rawCtx, body, true,
	)
	response := httptest.NewRecorder()

	fx.server.handleWallTransient(response, req, fx.project.ID)
	assertWallTransientMigrationResponse(t, response, http.StatusNoContent)
	assertWallTransientMigrationTrace(t, recorder.trace, "role", "body", "publish")
	if recorder.roleCalls != 1 || recorder.rolePID != fx.project.ID || recorder.roleUID != fx.owner.ID {
		t.Fatalf("role calls/project/user=%d/%d/%d", recorder.roleCalls, recorder.rolePID, recorder.roleUID)
	}
	if len(recorder.publishCalls) != 1 {
		t.Fatalf("publisher calls=%+v want one", recorder.publishCalls)
	}
	call := recorder.publishCalls[0]
	if call.ctx != rawCtx || call.projectID != fx.project.ID {
		t.Fatalf("publish context/project=%v/%d want exact raw context/%d", call.ctx, call.projectID, fx.project.ID)
	}
	wantEvent := wallapp.TransientEvent{
		NoteID: " \t retained-note \n ", X: -7.25, Y: 18.5, By: fx.owner.ID,
	}
	if !reflect.DeepEqual(call.event, wantEvent) {
		t.Fatalf("published event=%+v want=%+v", call.event, wantEvent)
	}
	if recorder.roleCtx == rawCtx {
		t.Fatal("role read did not receive actor-enriched mutation context")
	}
	if recorder.roleCtx.Value(wallTransientMigrationContextKey{}) != "handler" {
		t.Fatal("mutation context lost raw request marker")
	}
	actorID, ok := store.UserIDFromContext(recorder.roleCtx)
	if !ok || actorID != fx.owner.ID {
		t.Fatalf("mutation actor=%d,%v want=%d,true", actorID, ok, fx.owner.ID)
	}
	if _, ok := store.UserIDFromContext(call.ctx); ok {
		t.Fatal("raw effect context unexpectedly contains actor")
	}
	for name, ctx := range map[string]context.Context{"role": recorder.roleCtx, "publish": call.ctx} {
		gotDeadline, ok := ctx.Deadline()
		if !ok || !gotDeadline.Equal(deadline) {
			t.Fatalf("%s deadline=(%v,%v) want=(%v,true)", name, gotDeadline, ok, deadline)
		}
	}
	assertWallTransientMigrationNoDirectStoreCalls(t, fx)
	if events := fx.collector.snapshot(); len(events) != 0 {
		t.Fatalf("handler retained direct event publication=%+v", events)
	}
	cancel()
	for name, ctx := range map[string]context.Context{"role": recorder.roleCtx, "publish": call.ctx} {
		if ctx.Err() != context.Canceled {
			t.Fatalf("%s cancellation=%v want=%v", name, ctx.Err(), context.Canceled)
		}
	}
}

func TestWallTransientMigrationPublishesOriginalWhitespaceBearingNoteID(t *testing.T) {
	const noteID = " \t note-123 \n "

	t.Run("application seam", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		recorder := &wallTransientMigrationRecorder{role: store.RoleContributor}
		installWallTransientMigrationService(fx.server, recorder)
		payload, err := json.Marshal(map[string]any{"noteId": noteID, "x": 3.25, "y": -4.5})
		if err != nil {
			t.Fatal(err)
		}
		req := newWallTransientMigrationRequest(
			t, fx, wallMutationURL(fx, "/direct"), context.Background(), bytes.NewReader(payload), true,
		)
		response := httptest.NewRecorder()

		fx.server.handleWallTransient(response, req, fx.project.ID)
		assertWallTransientMigrationResponse(t, response, http.StatusNoContent)
		if len(recorder.publishCalls) != 1 {
			t.Fatalf("publisher calls=%+v want one", recorder.publishCalls)
		}
		if got := recorder.publishCalls[0].event.NoteID; got != noteID {
			t.Fatalf("application event noteID=%q want exact original %q", got, noteID)
		}
	})

	t.Run("concrete wire", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		fx.collector.reset()
		payload, err := json.Marshal(map[string]any{"noteId": noteID, "x": 3.25, "y": -4.5})
		if err != nil {
			t.Fatal(err)
		}
		resp, body := doWallRawJSON(
			t, fx.client, http.MethodPost, wallMutationURL(fx, "/transient"), string(payload), true, nil,
		)
		assertWallStatus(t, resp, body, http.StatusNoContent)
		if len(body) != 0 {
			t.Fatalf("204 body=%q want empty", body)
		}
		events := fx.collector.snapshot()
		if len(events) != 1 {
			t.Fatalf("events=%+v want one", events)
		}
		if events[0].Type != "wall.transient" || events[0].ProjectID != fx.project.ID {
			t.Fatalf("event type/project=%q/%d", events[0].Type, events[0].ProjectID)
		}
		assertWallTransientMigrationPayload(t, events[0].Payload, noteID, 3.25, -4.5, fx.owner.ID)
	})
}

func TestWallTransientMigrationPreparationPrecedesBodyAndBlankValidation(t *testing.T) {
	tests := []struct {
		name          string
		role          store.ProjectRole
		roleErr       error
		authenticated bool
		body          string
		wantStatus    int
		wantCode      string
		wantMessage   string
		wantDetails   map[string]any
		wantTrace     []string
		wantRoleCalls int
	}{
		{
			name: "actor absent before malformed body", role: store.RoleContributor, body: "{",
			wantStatus: http.StatusUnauthorized, wantCode: "UNAUTHORIZED", wantMessage: "unauthorized",
		},
		{
			name: "role read error before malformed body", roleErr: errors.New("role failed"), authenticated: true, body: "{",
			wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN", wantMessage: "contributor or higher required",
			wantTrace: []string{"role"}, wantRoleCalls: 1,
		},
		{
			name: "viewer before malformed body", role: store.RoleViewer, authenticated: true, body: "{",
			wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN", wantMessage: "contributor or higher required",
			wantTrace: []string{"role"}, wantRoleCalls: 1,
		},
		{
			name: "authorized malformed body", role: store.RoleContributor, authenticated: true, body: "{",
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "invalid json",
			wantDetails: map[string]any{"reason": "invalid_json", "detail": "unexpected EOF"},
			wantTrace:   []string{"role", "body"}, wantRoleCalls: 1,
		},
		{
			name: "authorized unknown field", role: store.RoleContributor, authenticated: true,
			body:       `{"noteId":"note","x":1,"y":2,"extra":true}`,
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "invalid json",
			wantDetails: map[string]any{"reason": "invalid_json", "detail": `json: unknown field "extra"`},
			wantTrace:   []string{"role", "body"}, wantRoleCalls: 1,
		},
		{
			name: "authorized whitespace-only ID", role: store.RoleContributor, authenticated: true,
			body:       `{"noteId":" \t\r\n ","x":1,"y":2}`,
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "noteId required",
			wantDetails: map[string]any{"reason": "note_id_required", "field": "noteId"},
			wantTrace:   []string{"role", "body"}, wantRoleCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newWallCharacterizationFixture(t, true)
			recorder := &wallTransientMigrationRecorder{role: tt.role, roleErr: tt.roleErr}
			installWallTransientMigrationService(fx.server, recorder)
			fx.collector.reset()
			fx.spy.resetCalls()
			body := newWallTransientMigrationBody(recorder, tt.body)
			req := newWallTransientMigrationRequest(
				t, fx, wallMutationURL(fx, "/direct"), context.Background(), body, tt.authenticated,
			)
			response := httptest.NewRecorder()

			fx.server.handleWallTransient(response, req, fx.project.ID)
			if response.Code != tt.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tt.wantStatus, response.Body.Bytes())
			}
			assertWallError(t, response.Body.Bytes(), tt.wantCode, tt.wantMessage, tt.wantDetails)
			if recorder.roleCalls != tt.wantRoleCalls {
				t.Fatalf("role calls=%d want=%d", recorder.roleCalls, tt.wantRoleCalls)
			}
			assertWallTransientMigrationTrace(t, recorder.trace, tt.wantTrace...)
			assertWallTransientMigrationNoEffects(t, recorder, fx)
			assertWallTransientMigrationNoDirectStoreCalls(t, fx)
		})
	}
}

func TestWallTransientMigrationGlobalHeaderGateRemainsOutsideService(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	recorder := &wallTransientMigrationRecorder{role: store.RoleContributor}
	installWallTransientMigrationService(fx.server, recorder)
	fx.collector.reset()
	fx.spy.resetCalls()
	body := newWallTransientMigrationBody(recorder, "{")
	req := newWallTransientMigrationRequest(
		t, fx, wallMutationURL(fx, "/transient"), context.Background(), body, true,
	)
	response := httptest.NewRecorder()

	fx.server.ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusForbidden, response.Body.Bytes())
	}
	assertWallError(t, response.Body.Bytes(), "FORBIDDEN", "missing X-Scrumboy header", nil)
	assertWallTransientMigrationTrace(t, recorder.trace)
	assertWallTransientMigrationNoEffects(t, recorder, fx)
	assertWallTransientMigrationNoDirectStoreCalls(t, fx)
}

func TestWallTransientMigrationFeatureAndDurabilityGatesRemainOutsideService(t *testing.T) {
	t.Run("feature disabled", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, false)
		recorder := &wallTransientMigrationRecorder{role: store.RoleContributor}
		installWallTransientMigrationService(fx.server, recorder)
		fx.collector.reset()
		fx.spy.resetCalls()
		body := newWallTransientMigrationBody(recorder, "{")
		req := newWallTransientMigrationRequest(
			t, fx, wallMutationURL(fx, "/transient"), context.Background(), body, true,
		)
		req.Header.Set("X-Scrumboy", "1")
		response := httptest.NewRecorder()

		fx.server.ServeHTTP(response, req)
		if response.Code != http.StatusNotFound {
			t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusNotFound, response.Body.Bytes())
		}
		assertWallError(t, response.Body.Bytes(), "NOT_FOUND", "not found", nil)
		assertWallTransientMigrationTrace(t, recorder.trace)
		assertWallTransientMigrationNoEffects(t, recorder, fx)
		assertWallTransientMigrationNoDirectStoreCalls(t, fx)
	})

	t.Run("temporary board", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		temporary, err := fx.store.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		recorder := &wallTransientMigrationRecorder{role: store.RoleContributor}
		installWallTransientMigrationService(fx.server, recorder)
		fx.collector.reset()
		fx.spy.resetCalls()
		body := newWallTransientMigrationBody(recorder, "{")
		requestURL := fx.baseURL + "/api/board/" + temporary.Slug + "/wall/transient"
		req := newWallTransientMigrationRequest(
			t, fx, requestURL, context.Background(), body, true,
		)
		req.Header.Set("X-Scrumboy", "1")
		response := httptest.NewRecorder()

		fx.server.ServeHTTP(response, req)
		if response.Code != http.StatusNotFound {
			t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusNotFound, response.Body.Bytes())
		}
		assertWallError(t, response.Body.Bytes(), "NOT_FOUND", "not found", nil)
		assertWallTransientMigrationTrace(t, recorder.trace)
		assertWallTransientMigrationNoEffects(t, recorder, fx)
		assertWallTransientMigrationNoDirectStoreCalls(t, fx)
	})
}

func TestWallTransientMigrationPublisherErrorUsesExistingInternalProjection(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	sentinel := errors.New("transient projection failed")
	recorder := &wallTransientMigrationRecorder{
		role: store.RoleContributor, publishErr: sentinel,
	}
	installWallTransientMigrationService(fx.server, recorder)
	fx.collector.reset()
	fx.spy.resetCalls()
	body := newWallTransientMigrationBody(recorder, `{"noteId":"note","x":1.25,"y":-2.5}`)
	req := newWallTransientMigrationRequest(
		t, fx, wallMutationURL(fx, "/direct"), context.Background(), body, true,
	)
	response := httptest.NewRecorder()

	fx.server.handleWallTransient(response, req, fx.project.ID)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusInternalServerError, response.Body.Bytes())
	}
	assertWallError(
		t,
		response.Body.Bytes(),
		"INTERNAL",
		"internal error",
		map[string]any{"detail": sentinel.Error()},
	)
	assertWallTransientMigrationTrace(t, recorder.trace, "role", "body", "publish")
	if len(recorder.publishCalls) != 1 {
		t.Fatalf("publisher calls=%+v want exactly one", recorder.publishCalls)
	}
	if response.Body.Len() == 0 {
		t.Fatal("publisher failure unexpectedly wrote an empty 204-style response")
	}
	if events := fx.collector.snapshot(); len(events) != 0 {
		t.Fatalf("publisher failure direct events=%+v want none", events)
	}
	assertWallTransientMigrationNoDirectStoreCalls(t, fx)
}
