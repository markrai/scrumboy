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
	"sync"
	"testing"
	"time"

	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

const wallCharacterizationPassword = "password123"

type wallCharacterizationStore struct {
	*store.Store

	mu       sync.Mutex
	roleErr  error
	failures map[string]error
	calls    map[string]int
}

func newWallCharacterizationStore(st *store.Store) *wallCharacterizationStore {
	return &wallCharacterizationStore{
		Store:    st,
		failures: make(map[string]error),
		calls:    make(map[string]int),
	}
}

func (s *wallCharacterizationStore) record(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[name]++
	return s.failures[name]
}

func (s *wallCharacterizationStore) setFailure(name string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		delete(s.failures, name)
		return
	}
	s.failures[name] = err
}

func (s *wallCharacterizationStore) callCount(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[name]
}

func (s *wallCharacterizationStore) resetCalls() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = make(map[string]int)
}

func (s *wallCharacterizationStore) setRoleError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roleErr = err
}

func (s *wallCharacterizationStore) GetProjectRole(ctx context.Context, projectID, userID int64) (store.ProjectRole, error) {
	s.mu.Lock()
	s.calls["GetProjectRole"]++
	err := s.roleErr
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	return s.Store.GetProjectRole(ctx, projectID, userID)
}

func (s *wallCharacterizationStore) GetWall(ctx context.Context, projectID int64) (store.Wall, error) {
	if err := s.record("GetWall"); err != nil {
		return store.Wall{}, err
	}
	return s.Store.GetWall(ctx, projectID)
}

func (s *wallCharacterizationStore) CreateNote(ctx context.Context, projectID int64, in store.CreateNoteInput) (store.WallNote, store.Wall, error) {
	if err := s.record("CreateNote"); err != nil {
		return store.WallNote{}, store.Wall{}, err
	}
	return s.Store.CreateNote(ctx, projectID, in)
}

func (s *wallCharacterizationStore) PatchNote(ctx context.Context, projectID int64, noteID string, in store.PatchNoteInput) (store.WallNote, store.Wall, error) {
	if err := s.record("PatchNote"); err != nil {
		return store.WallNote{}, store.Wall{}, err
	}
	return s.Store.PatchNote(ctx, projectID, noteID, in)
}

func (s *wallCharacterizationStore) DeleteNote(ctx context.Context, projectID int64, noteID string) (store.Wall, error) {
	if err := s.record("DeleteNote"); err != nil {
		return store.Wall{}, err
	}
	return s.Store.DeleteNote(ctx, projectID, noteID)
}

func (s *wallCharacterizationStore) ReplaceWall(ctx context.Context, projectID int64, notes []store.WallNote) (store.Wall, error) {
	if err := s.record("ReplaceWall"); err != nil {
		return store.Wall{}, err
	}
	return s.Store.ReplaceWall(ctx, projectID, notes)
}

func (s *wallCharacterizationStore) CreateEdge(ctx context.Context, projectID int64, from, to string) (store.WallEdge, store.Wall, error) {
	if err := s.record("CreateEdge"); err != nil {
		return store.WallEdge{}, store.Wall{}, err
	}
	return s.Store.CreateEdge(ctx, projectID, from, to)
}

func (s *wallCharacterizationStore) DeleteEdge(ctx context.Context, projectID int64, edgeID string) (store.Wall, error) {
	if err := s.record("DeleteEdge"); err != nil {
		return store.Wall{}, err
	}
	return s.Store.DeleteEdge(ctx, projectID, edgeID)
}

type wallCharacterizationCollector struct {
	mu       sync.Mutex
	events   []eventbus.Event
	contexts []context.Context
}

func (c *wallCharacterizationCollector) OnEvent(ctx context.Context, event eventbus.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
	c.contexts = append(c.contexts, ctx)
}

func (c *wallCharacterizationCollector) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = nil
	c.contexts = nil
}

func (c *wallCharacterizationCollector) snapshot() []eventbus.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]eventbus.Event(nil), c.events...)
}

func (c *wallCharacterizationCollector) contextSnapshot() []context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]context.Context(nil), c.contexts...)
}

type wallCharacterizationFixture struct {
	baseURL   string
	server    *Server
	store     *store.Store
	spy       *wallCharacterizationStore
	collector *wallCharacterizationCollector
	owner     store.User
	project   store.Project
	client    *http.Client
}

func newWallCharacterizationFixture(t *testing.T, wallEnabled bool) *wallCharacterizationFixture {
	t.Helper()
	st := newTestStore(t)
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "wall-owner@example.com", wallCharacterizationPassword, "Wall Owner")
	if err != nil {
		t.Fatalf("bootstrap wall owner: %v", err)
	}
	ownerCtx := store.WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "Wall Characterization")
	if err != nil {
		t.Fatalf("create wall project: %v", err)
	}

	spy := newWallCharacterizationStore(st)
	srv := NewServer(spy, Options{MaxRequestBody: 1 << 20, ScrumboyMode: "full", WallEnabled: wallEnabled})
	collector := &wallCharacterizationCollector{}
	srv.fanout = eventbus.NewFanout(newSSEBridge(srv.hub, nil), collector)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Close(ctx)
	})

	return &wallCharacterizationFixture{
		baseURL: ts.URL, server: srv, store: st, spy: spy, collector: collector,
		owner: owner, project: project, client: wallCharacterizationClientForUser(t, st, ts.URL, owner.ID),
	}
}

func wallCharacterizationClientForUser(t *testing.T, st *store.Store, baseURL string, userID int64) *http.Client {
	t.Helper()
	token, expiresAt, err := st.CreateSession(context.Background(), userID, time.Hour)
	if err != nil {
		t.Fatalf("create wall session: %v", err)
	}
	client := newCookieClient(t)
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse wall server URL: %v", err)
	}
	client.Jar.SetCookies(parsed, []*http.Cookie{{
		Name: "scrumboy_session", Value: token, Path: "/", Expires: expiresAt,
	}})
	return client
}

func (fx *wallCharacterizationFixture) createUser(t *testing.T, role store.ProjectRole) (store.User, *http.Client) {
	t.Helper()
	email := fmt.Sprintf("wall-%s-%d@example.com", role, time.Now().UnixNano())
	user, err := fx.store.CreateUser(context.Background(), email, wallCharacterizationPassword, "Wall "+string(role))
	if err != nil {
		t.Fatalf("create %s user: %v", role, err)
	}
	if err := fx.store.AddProjectMember(context.Background(), fx.owner.ID, fx.project.ID, user.ID, role); err != nil {
		t.Fatalf("add %s project member: %v", role, err)
	}
	return user, wallCharacterizationClientForUser(t, fx.store, fx.baseURL, user.ID)
}

func wallMutationURL(fx *wallCharacterizationFixture, suffix string) string {
	return fx.baseURL + "/api/board/" + fx.project.Slug + "/wall" + suffix
}

func wallNoteInput(text string) map[string]any {
	return map[string]any{
		"x": 10.0, "y": 20.0, "width": 180.0, "height": 140.0,
		"color": "#ffd966", "text": text,
	}
}

func doWallRawJSON(t *testing.T, client *http.Client, method, requestURL, body string, withHeader bool, out any) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, requestURL, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new wall request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if withHeader {
		req.Header.Set("X-Scrumboy", "1")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do wall request: %v", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read wall response: %v", err)
	}
	if out != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			t.Fatalf("decode wall response %s: %v", payload, err)
		}
	}
	return resp, payload
}

func assertWallStatus(t *testing.T, resp *http.Response, body []byte, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("status=%d want=%d body=%s", resp.StatusCode, want, body)
	}
}

func assertWallError(t *testing.T, body []byte, code, message string, details map[string]any) {
	t.Helper()
	var exact map[string]any
	if err := json.Unmarshal(body, &exact); err != nil {
		t.Fatalf("decode exact wall error %s: %v", body, err)
	}
	assertExactJSONKeys(t, exact, "error")
	errorObject, ok := exact["error"].(map[string]any)
	if !ok {
		t.Fatalf("error envelope=%+v want object", exact)
	}
	assertExactJSONKeys(t, errorObject, "code", "message", "details")

	var got apiErrorEnvelope
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode wall error %s: %v", body, err)
	}
	if got.Error.Code != code || got.Error.Message != message {
		t.Fatalf("error=%+v want code=%q message=%q", got.Error, code, message)
	}
	if !equalWallDetails(got.Error.Details, details) {
		t.Fatalf("error details=%+v want=%+v", got.Error.Details, details)
	}
}

func equalWallDetails(got, want map[string]any) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}

func TestWallMutationCharacterizationSelectedSurfaceSuccesses(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)

	var created map[string]any
	resp, body := doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/notes"), wallNoteInput("created"), &created)
	assertWallStatus(t, resp, body, http.StatusCreated)
	assertExactJSONKeys(t, created, "id", "x", "y", "width", "height", "color", "text", "version")
	noteID, _ := created["id"].(string)
	if noteID == "" || created["version"] != float64(1) {
		t.Fatalf("created note=%+v", created)
	}

	var patched map[string]any
	resp, body = doJSON(t, fx.client, http.MethodPatch, wallMutationURL(fx, "/notes/"+noteID), map[string]any{
		"ifVersion": 1, "text": "patched",
	}, &patched)
	assertWallStatus(t, resp, body, http.StatusOK)
	assertExactJSONKeys(t, patched, "id", "x", "y", "width", "height", "color", "text", "version")
	if patched["id"] != noteID || patched["text"] != "patched" || patched["version"] != float64(2) {
		t.Fatalf("patched note=%+v", patched)
	}

	var second map[string]any
	resp, body = doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/notes"), wallNoteInput("second"), &second)
	assertWallStatus(t, resp, body, http.StatusCreated)
	secondID, _ := second["id"].(string)

	var edge map[string]any
	resp, body = doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/edges"), map[string]any{
		"from": noteID, "to": secondID,
	}, &edge)
	assertWallStatus(t, resp, body, http.StatusCreated)
	assertExactJSONKeys(t, edge, "id", "from", "to")
	edgeID, _ := edge["id"].(string)
	if edgeID == "" || edge["from"] != noteID || edge["to"] != secondID {
		t.Fatalf("created edge=%+v", edge)
	}

	resp, body = doJSON(t, fx.client, http.MethodDelete, wallMutationURL(fx, "/edges/"+edgeID), nil, nil)
	assertWallStatus(t, resp, body, http.StatusNoContent)
	if len(body) != 0 {
		t.Fatalf("delete edge body=%q want empty", body)
	}

	resp, body = doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/transient"), map[string]any{
		"noteId": "unresolved-but-nonblank", "x": -12.5, "y": 44.25,
	}, nil)
	assertWallStatus(t, resp, body, http.StatusNoContent)
	if len(body) != 0 {
		t.Fatalf("transient body=%q want empty", body)
	}

	resp, body = doJSON(t, fx.client, http.MethodDelete, wallMutationURL(fx, "/notes/"+noteID), nil, nil)
	assertWallStatus(t, resp, body, http.StatusNoContent)
	if len(body) != 0 {
		t.Fatalf("delete note body=%q want empty", body)
	}

	var replaced map[string]any
	resp, body = doJSON(t, fx.client, http.MethodPut, wallMutationURL(fx, ""), map[string]any{
		"notes": []any{wallNoteInput("replacement")},
	}, &replaced)
	assertWallStatus(t, resp, body, http.StatusOK)
	assertExactJSONKeys(t, replaced, "notes", "edges", "version", "updatedAt")
	notes, ok := replaced["notes"].([]any)
	if !ok || len(notes) != 1 {
		t.Fatalf("replacement notes=%+v", replaced["notes"])
	}
	assertExactJSONKeys(t, notes[0].(map[string]any), "id", "x", "y", "width", "height", "color", "text", "version")
}

func TestWallMutationCharacterizationAuthorizationAndGateOrdering(t *testing.T) {
	t.Run("global header gate wins before slug and body", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		resp, body := doWallRawJSON(t, fx.client, http.MethodPost, fx.baseURL+"/api/board/INVALID!/wall/notes", "{", false, nil)
		assertWallStatus(t, resp, body, http.StatusForbidden)
		assertWallError(t, body, "FORBIDDEN", "missing X-Scrumboy header", nil)
	})

	t.Run("PUT has no global header gate", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		resp, body := doWallRawJSON(t, fx.client, http.MethodPut, wallMutationURL(fx, ""), "{", false, nil)
		assertWallStatus(t, resp, body, http.StatusBadRequest)
		var got apiErrorEnvelope
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if got.Error.Code != "VALIDATION_ERROR" || got.Error.Message != "invalid json" || got.Error.Details["reason"] != "invalid_json" {
			t.Fatalf("PUT malformed error=%+v", got.Error)
		}
	})

	t.Run("router conceals unauthenticated durable project before body parsing", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		resp, body := doWallRawJSON(t, newCookieClient(t), http.MethodPost, wallMutationURL(fx, "/notes"), "{", true, nil)
		assertWallStatus(t, resp, body, http.StatusNotFound)
		assertWallError(t, body, "NOT_FOUND", "not found", nil)
	})

	t.Run("viewer writer denial wins before body parsing", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		_, viewer := fx.createUser(t, store.RoleViewer)
		resp, body := doWallRawJSON(t, viewer, http.MethodPost, wallMutationURL(fx, "/notes"), "{", true, nil)
		assertWallStatus(t, resp, body, http.StatusForbidden)
		assertWallError(t, body, "FORBIDDEN", "contributor or higher required", nil)
	})

	t.Run("contributor modern and deprecated writer roles are accepted", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		for _, role := range []store.ProjectRole{
			store.RoleContributor, store.RoleMaintainer, store.RoleEditor, store.RoleOwner,
		} {
			role := role
			t.Run(string(role), func(t *testing.T) {
				_, client := fx.createUser(t, role)
				resp, body := doJSON(t, client, http.MethodPost, wallMutationURL(fx, "/notes"), wallNoteInput(string(role)), nil)
				assertWallStatus(t, resp, body, http.StatusCreated)
			})
		}
	})

	t.Run("system role without project membership remains concealed", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		for _, systemRole := range []store.SystemRole{store.SystemRoleAdmin, store.SystemRoleOwner} {
			user, err := fx.store.CreateUser(context.Background(), fmt.Sprintf("wall-system-%s@example.com", systemRole), wallCharacterizationPassword, "System Role")
			if err != nil {
				t.Fatal(err)
			}
			if err := fx.store.UpdateUserRole(context.Background(), fx.owner.ID, user.ID, systemRole); err != nil {
				t.Fatal(err)
			}
			client := wallCharacterizationClientForUser(t, fx.store, fx.baseURL, user.ID)
			resp, body := doWallRawJSON(t, client, http.MethodPost, wallMutationURL(fx, "/notes"), "{", true, nil)
			assertWallStatus(t, resp, body, http.StatusNotFound)
			assertWallError(t, body, "NOT_FOUND", "not found", nil)
		}
	})

	t.Run("fresh role read failure collapses to forbidden before parsing", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		fx.spy.resetCalls()
		fx.spy.setRoleError(errors.New("injected role read failure"))
		resp, body := doWallRawJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/notes"), "{", true, nil)
		assertWallStatus(t, resp, body, http.StatusForbidden)
		assertWallError(t, body, "FORBIDDEN", "contributor or higher required", nil)
		if fx.spy.callCount("GetProjectRole") != 1 || fx.spy.callCount("CreateNote") != 0 {
			t.Fatalf("calls=%+v want one role read and no mutation", fx.spy.calls)
		}
	})

	t.Run("authorized malformed input reaches parser", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		resp, body := doWallRawJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/notes"), "{", true, nil)
		assertWallStatus(t, resp, body, http.StatusBadRequest)
		var got apiErrorEnvelope
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if got.Error.Code != "VALIDATION_ERROR" || got.Error.Message != "invalid json" || got.Error.Details["reason"] != "invalid_json" {
			t.Fatalf("malformed error=%+v", got.Error)
		}
	})

	t.Run("feature off wins before writer and body", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, false)
		resp, body := doWallRawJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/notes"), "{", true, nil)
		assertWallStatus(t, resp, body, http.StatusNotFound)
		assertWallError(t, body, "NOT_FOUND", "not found", nil)
	})

	t.Run("expiring board wins before writer and body", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		temporary, err := fx.store.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		requestURL := fx.baseURL + "/api/board/" + temporary.Slug + "/wall/notes"
		resp, body := doWallRawJSON(t, fx.client, http.MethodPost, requestURL, "{", true, nil)
		assertWallStatus(t, resp, body, http.StatusNotFound)
		assertWallError(t, body, "NOT_FOUND", "not found", nil)
	})

	t.Run("pre-bootstrap durable board reaches actorless writer gate", func(t *testing.T) {
		st := newTestStore(t)
		project, err := st.CreateProject(context.Background(), "Pre-bootstrap Wall")
		if err != nil {
			t.Fatal(err)
		}
		spy := newWallCharacterizationStore(st)
		srv := NewServer(spy, Options{MaxRequestBody: 1 << 20, ScrumboyMode: "full", WallEnabled: true})
		ts := httptest.NewServer(srv)
		t.Cleanup(ts.Close)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			srv.Close(ctx)
		})
		requestURL := ts.URL + "/api/board/" + project.Slug + "/wall/notes"
		resp, body := doWallRawJSON(t, newCookieClient(t), http.MethodPost, requestURL, "{", true, nil)
		assertWallStatus(t, resp, body, http.StatusUnauthorized)
		assertWallError(t, body, "UNAUTHORIZED", "unauthorized", nil)
	})
}

func TestWallMutationCharacterizationValidationTargetAndConflictOrdering(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	var created struct {
		ID      string `json:"id"`
		Version int64  `json:"version"`
	}
	resp, body := doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/notes"), wallNoteInput("target"), &created)
	assertWallStatus(t, resp, body, http.StatusCreated)

	t.Run("patch validates supplied color before missing target", func(t *testing.T) {
		resp, body := doJSON(t, fx.client, http.MethodPatch, wallMutationURL(fx, "/notes/missing"), map[string]any{
			"ifVersion": 1, "color": "not-a-color",
		}, nil)
		assertWallStatus(t, resp, body, http.StatusBadRequest)
		assertWallError(t, body, "VALIDATION_ERROR", "validation: invalid color", map[string]any{"reason": "invalid_color"})

		resp, body = doJSON(t, fx.client, http.MethodPatch, wallMutationURL(fx, "/notes/missing"), map[string]any{
			"ifVersion": 1, "color": "#abcdef",
		}, nil)
		assertWallStatus(t, resp, body, http.StatusNotFound)
		assertWallError(t, body, "NOT_FOUND", "not found", nil)
	})

	t.Run("nonzero stale note version conflicts after target lookup", func(t *testing.T) {
		resp, body := doJSON(t, fx.client, http.MethodPatch, wallMutationURL(fx, "/notes/"+created.ID), map[string]any{
			"ifVersion": created.Version, "text": "first patch",
		}, nil)
		assertWallStatus(t, resp, body, http.StatusOK)

		resp, body = doJSON(t, fx.client, http.MethodPatch, wallMutationURL(fx, "/notes/"+created.ID), map[string]any{
			"ifVersion": created.Version, "text": "stale patch",
		}, nil)
		assertWallStatus(t, resp, body, http.StatusConflict)
		assertWallError(t, body, "CONFLICT", "conflict: note version mismatch", nil)
	})

	t.Run("edge validation precedes endpoint lookup", func(t *testing.T) {
		resp, body := doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/edges"), map[string]any{
			"from": created.ID, "to": created.ID,
		}, nil)
		assertWallStatus(t, resp, body, http.StatusBadRequest)
		assertWallError(t, body, "VALIDATION_ERROR", "validation: self-edges not allowed", map[string]any{"reason": "self_edges_not_allowed"})

		resp, body = doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/edges"), map[string]any{
			"from": created.ID, "to": "missing",
		}, nil)
		assertWallStatus(t, resp, body, http.StatusNotFound)
		assertWallError(t, body, "NOT_FOUND", "not found", nil)
	})
}

func TestWallMutationCharacterizationMethodAndWriterGateMatrix(t *testing.T) {
	t.Run("PATCH and DELETE require X-Scrumboy before route input", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		for _, tt := range []struct {
			name   string
			method string
			url    string
			body   string
		}{
			{name: "patch", method: http.MethodPatch, url: fx.baseURL + "/api/board/INVALID!/wall/notes/missing", body: "{"},
			{name: "delete", method: http.MethodDelete, url: fx.baseURL + "/api/board/INVALID!/wall/notes/missing"},
		} {
			t.Run(tt.name, func(t *testing.T) {
				resp, body := doWallRawJSON(t, fx.client, tt.method, tt.url, tt.body, false, nil)
				assertWallStatus(t, resp, body, http.StatusForbidden)
				assertWallError(t, body, "FORBIDDEN", "missing X-Scrumboy header", nil)
			})
		}
	})

	t.Run("viewer denial precedes mutation-specific input", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		_, viewer := fx.createUser(t, store.RoleViewer)
		for _, tt := range []struct {
			name      string
			operation string
			method    string
			url       string
		}{
			{name: "patch note", operation: "PatchNote", method: http.MethodPatch, url: wallMutationURL(fx, "/notes/missing")},
			{name: "replace wall", operation: "ReplaceWall", method: http.MethodPut, url: wallMutationURL(fx, "")},
			{name: "create edge", operation: "CreateEdge", method: http.MethodPost, url: wallMutationURL(fx, "/edges")},
			{name: "transient", operation: "wall transient publication", method: http.MethodPost, url: wallMutationURL(fx, "/transient")},
		} {
			t.Run(tt.name, func(t *testing.T) {
				fx.spy.resetCalls()
				fx.collector.reset()
				resp, body := doWallRawJSON(t, viewer, tt.method, tt.url, "{", true, nil)
				assertWallStatus(t, resp, body, http.StatusForbidden)
				assertWallError(t, body, "FORBIDDEN", "contributor or higher required", nil)
				if got := fx.spy.callCount("GetProjectRole"); got != 1 {
					t.Fatalf("GetProjectRole calls=%d want=1", got)
				}
				if tt.operation != "wall transient publication" && fx.spy.callCount(tt.operation) != 0 {
					t.Fatalf("%s reached before authorization", tt.operation)
				}
				if events := fx.collector.snapshot(); len(events) != 0 {
					t.Fatalf("authorization failure published events=%+v", events)
				}
			})
		}
	})
}

func TestWallMutationCharacterizationSharedFeatureAndDurabilityGateMatrix(t *testing.T) {
	routes := []struct {
		name   string
		method string
		suffix string
		body   string
	}{
		{name: "create note", method: http.MethodPost, suffix: "/notes", body: "{"},
		{name: "patch note", method: http.MethodPatch, suffix: "/notes/missing", body: "{"},
		{name: "delete note", method: http.MethodDelete, suffix: "/notes/missing"},
		{name: "replace wall", method: http.MethodPut, suffix: "", body: "{"},
		{name: "create edge", method: http.MethodPost, suffix: "/edges", body: "{"},
		{name: "delete edge", method: http.MethodDelete, suffix: "/edges/missing"},
		{name: "transient", method: http.MethodPost, suffix: "/transient", body: "{"},
	}

	t.Run("feature off precedes every writer and input", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, false)
		for _, tt := range routes {
			t.Run(tt.name, func(t *testing.T) {
				fx.spy.resetCalls()
				resp, body := doWallRawJSON(t, fx.client, tt.method, wallMutationURL(fx, tt.suffix), tt.body, true, nil)
				assertWallStatus(t, resp, body, http.StatusNotFound)
				assertWallError(t, body, "NOT_FOUND", "not found", nil)
				if got := fx.spy.callCount("GetProjectRole"); got != 0 {
					t.Fatalf("GetProjectRole calls=%d want=0", got)
				}
			})
		}
	})

	t.Run("expiring board precedes every writer and input", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		temporary, err := fx.store.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		base := fx.baseURL + "/api/board/" + temporary.Slug + "/wall"
		for _, tt := range routes {
			t.Run(tt.name, func(t *testing.T) {
				fx.spy.resetCalls()
				resp, body := doWallRawJSON(t, fx.client, tt.method, base+tt.suffix, tt.body, true, nil)
				assertWallStatus(t, resp, body, http.StatusNotFound)
				assertWallError(t, body, "NOT_FOUND", "not found", nil)
				if got := fx.spy.callCount("GetProjectRole"); got != 0 {
					t.Fatalf("GetProjectRole calls=%d want=0", got)
				}
			})
		}
	})
}

func TestWallMutationCharacterizationDeletePathAndTargetPrecedence(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	_, viewer := fx.createUser(t, store.RoleViewer)

	for _, tt := range []struct {
		name      string
		operation string
		suffix    string
		field     string
		reason    string
		message   string
	}{
		{name: "note", operation: "DeleteNote", suffix: "/notes/", field: "noteId", reason: "note_id_required", message: "noteId required"},
		{name: "edge", operation: "DeleteEdge", suffix: "/edges/", field: "edgeId", reason: "edge_id_required", message: "edgeId required"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			blankURL := wallMutationURL(fx, tt.suffix+url.PathEscape("   "))
			fx.spy.resetCalls()
			resp, body := doWallRawJSON(t, viewer, http.MethodDelete, blankURL, "", true, nil)
			assertWallStatus(t, resp, body, http.StatusForbidden)
			assertWallError(t, body, "FORBIDDEN", "contributor or higher required", nil)
			if fx.spy.callCount(tt.operation) != 0 {
				t.Fatalf("%s reached before authorization", tt.operation)
			}

			fx.spy.resetCalls()
			fx.collector.reset()
			resp, body = doWallRawJSON(t, fx.client, http.MethodDelete, blankURL, "", true, nil)
			assertWallStatus(t, resp, body, http.StatusBadRequest)
			assertWallError(t, body, "VALIDATION_ERROR", tt.message, map[string]any{"field": tt.field, "reason": tt.reason})
			if fx.spy.callCount(tt.operation) != 0 {
				t.Fatalf("%s reached for blank path", tt.operation)
			}

			fx.spy.resetCalls()
			resp, body = doWallRawJSON(t, fx.client, http.MethodDelete, wallMutationURL(fx, tt.suffix+"missing"), "", true, nil)
			assertWallStatus(t, resp, body, http.StatusNotFound)
			assertWallError(t, body, "NOT_FOUND", "not found", nil)
			if fx.spy.callCount(tt.operation) != 1 {
				t.Fatalf("%s calls=%d want=1", tt.operation, fx.spy.callCount(tt.operation))
			}
			if events := fx.collector.snapshot(); len(events) != 0 {
				t.Fatalf("delete failure published events=%+v", events)
			}
		})
	}
}

func TestWallMutationCharacterizationEmptyReplacementAndLimitOrdering(t *testing.T) {
	t.Run("empty replacement is a successful durable write without PUT header", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		fx.collector.reset()
		var replaced map[string]any
		resp, body := doWallRawJSON(t, fx.client, http.MethodPut, wallMutationURL(fx, ""), `{"notes":[]}`, false, &replaced)
		assertWallStatus(t, resp, body, http.StatusOK)
		assertExactJSONKeys(t, replaced, "notes", "edges", "version", "updatedAt")
		notes, notesOK := replaced["notes"].([]any)
		edges, edgesOK := replaced["edges"].([]any)
		if !notesOK || len(notes) != 0 || !edgesOK || len(edges) != 0 || replaced["version"] != float64(1) {
			t.Fatalf("empty replacement=%+v", replaced)
		}
		if updatedAt, ok := replaced["updatedAt"].(float64); !ok || updatedAt <= 0 {
			t.Fatalf("empty replacement updatedAt=%+v", replaced["updatedAt"])
		}
		assertSingleWallRefresh(t, fx, "wall_replaced")
	})

	t.Run("replacement limit wins before per-note validation and persistence", func(t *testing.T) {
		fx := newWallCharacterizationFixture(t, true)
		before, err := fx.store.GetWall(context.Background(), fx.project.ID)
		if err != nil {
			t.Fatal(err)
		}
		notes := make([]map[string]any, 501)
		for i := range notes {
			notes[i] = wallNoteInput(fmt.Sprintf("limit-%d", i))
		}
		notes[0]["color"] = "invalid-but-limit-wins"
		fx.spy.resetCalls()
		fx.collector.reset()
		resp, body := doJSON(t, fx.client, http.MethodPut, wallMutationURL(fx, ""), map[string]any{"notes": notes}, nil)
		assertWallStatus(t, resp, body, http.StatusBadRequest)
		assertWallError(t, body, "VALIDATION_ERROR", "validation: wall note limit reached", map[string]any{"reason": "wall_note_limit_reached"})
		if fx.spy.callCount("ReplaceWall") != 1 || fx.spy.callCount("GetWall") != 0 {
			t.Fatalf("calls=%+v want one ReplaceWall and no adapter Wall read", fx.spy.calls)
		}
		if events := fx.collector.snapshot(); len(events) != 0 {
			t.Fatalf("replacement limit published events=%+v", events)
		}
		after, err := fx.store.GetWall(context.Background(), fx.project.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(after.Notes) != len(before.Notes) || len(after.Edges) != len(before.Edges) || after.Version != before.Version || after.UpdatedAt != before.UpdatedAt {
			t.Fatalf("replacement limit persisted state: before=%+v after=%+v", before, after)
		}
	})
}

func TestWallMutationCharacterizationWrongMethodAndRequestShape(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	for _, tt := range []struct {
		name   string
		method string
		suffix string
	}{
		{name: "POST collection root", method: http.MethodPost, suffix: ""},
		{name: "GET mutation collection", method: http.MethodGet, suffix: "/notes"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fx.spy.resetCalls()
			resp, body := doWallRawJSON(t, fx.client, tt.method, wallMutationURL(fx, tt.suffix), "{}", true, nil)
			assertWallStatus(t, resp, body, http.StatusNotFound)
			assertWallError(t, body, "NOT_FOUND", "not found", nil)
			if got := fx.spy.callCount("GetProjectRole"); got != 0 {
				t.Fatalf("wrong route performed writer check calls=%d", got)
			}
		})
	}
}
