package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/eventbus"
	mcpadapter "scrumboy/internal/mcp"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

type projectLifecycleHTTPStore struct {
	*store.Store

	mu sync.Mutex

	active  bool
	trace   []string
	mutated bool

	postReadErr error
	deleteErr   error
}

func (s *projectLifecycleHTTPStore) activate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = true
	s.trace = nil
	s.mutated = false
}

func (s *projectLifecycleHTTPStore) deactivate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = false
}

func (s *projectLifecycleHTTPStore) record(stage string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		s.trace = append(s.trace, stage)
	}
}

func (s *projectLifecycleHTTPStore) traceSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.trace...)
}

func (s *projectLifecycleHTTPStore) markMutated() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mutated = true
}

func (s *projectLifecycleHTTPStore) postReadFailure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active && s.mutated {
		return s.postReadErr
	}
	return nil
}

func (s *projectLifecycleHTTPStore) CreateProjectWithWorkflow(ctx context.Context, name string, workflow []store.WorkflowColumn) (store.Project, error) {
	s.record("create-durable")
	return s.Store.CreateProjectWithWorkflow(ctx, name, workflow)
}

func (s *projectLifecycleHTTPStore) CreateProject(ctx context.Context, name string) (store.Project, error) {
	s.record("create-mcp")
	return s.Store.CreateProject(ctx, name)
}

func (s *projectLifecycleHTTPStore) CreateAnonymousBoard(ctx context.Context) (store.Project, error) {
	s.record("create-anonymous")
	return s.Store.CreateAnonymousBoard(ctx)
}

func (s *projectLifecycleHTTPStore) GetProject(ctx context.Context, projectID int64) (store.Project, error) {
	s.record("get-project")
	if err := s.postReadFailure(); err != nil {
		return store.Project{}, err
	}
	return s.Store.GetProject(ctx, projectID)
}

func (s *projectLifecycleHTTPStore) UpdateProjectImage(ctx context.Context, projectID, userID int64, image *string, dominantColor string) error {
	s.record("update-image")
	err := s.Store.UpdateProjectImage(ctx, projectID, userID, image, dominantColor)
	if err == nil {
		s.markMutated()
	}
	return err
}

func (s *projectLifecycleHTTPStore) UpdateProjectName(ctx context.Context, projectID, userID int64, name string) error {
	s.record("update-name")
	err := s.Store.UpdateProjectName(ctx, projectID, userID, name)
	if err == nil {
		s.markMutated()
	}
	return err
}

func (s *projectLifecycleHTTPStore) DeleteProject(ctx context.Context, projectID, userID int64) (store.DeletedProjectSnapshot, error) {
	s.record("delete-project")
	s.mu.Lock()
	injected := s.deleteErr
	s.mu.Unlock()
	if injected != nil {
		return store.DeletedProjectSnapshot{}, injected
	}
	deleted, err := s.Store.DeleteProject(ctx, projectID, userID)
	if err == nil {
		s.markMutated()
	}
	return deleted, err
}

func (s *projectLifecycleHTTPStore) GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error) {
	s.record("access")
	return s.Store.GetProjectContextBySlug(ctx, slug, mode)
}

func (s *projectLifecycleHTTPStore) ClaimTemporaryBoard(ctx context.Context, projectID, userID int64) error {
	s.record("claim")
	err := s.Store.ClaimTemporaryBoard(ctx, projectID, userID)
	if err == nil {
		s.markMutated()
	}
	return err
}

type projectLifecycleEventSpy struct {
	mu      sync.Mutex
	store   *projectLifecycleHTTPStore
	events  []eventbus.Event
	reasons []string
}

func (s *projectLifecycleEventSpy) OnEvent(_ context.Context, event eventbus.Event) {
	s.mu.Lock()
	s.events = append(s.events, event)
	var payload refreshNeededPayload
	if event.Type == "board.refresh_needed" {
		_ = json.Unmarshal(event.Payload, &payload)
		s.reasons = append(s.reasons, payload.Reason)
	}
	s.mu.Unlock()
	if payload.Reason != "" {
		s.store.record("publish:" + payload.Reason)
	}
}

func (s *projectLifecycleEventSpy) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = nil
	s.reasons = nil
}

func (s *projectLifecycleEventSpy) snapshot() ([]eventbus.Event, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]eventbus.Event(nil), s.events...), append([]string(nil), s.reasons...)
}

type projectLifecycleHTTPFixture struct {
	server  *Server
	ts      *httptest.Server
	db      *sql.DB
	store   *store.Store
	wrapped *projectLifecycleHTTPStore
	spy     *projectLifecycleEventSpy
}

func newProjectLifecycleHTTPFixture(t *testing.T, mode string) *projectLifecycleHTTPFixture {
	t.Helper()

	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(sqlDB, nil)
	wrapped := &projectLifecycleHTTPStore{Store: st}
	server := NewServer(wrapped, Options{MaxRequestBody: 1 << 20, ScrumboyMode: mode})
	spy := &projectLifecycleEventSpy{store: wrapped}
	server.fanout = eventbus.NewFanout(newSSEBridge(server.hub, server.creatorNotificationAuthorizer), spy)
	ts := httptest.NewServer(server)
	t.Cleanup(func() {
		ts.Close()
		_ = sqlDB.Close()
	})
	return &projectLifecycleHTTPFixture{server: server, ts: ts, db: sqlDB, store: st, wrapped: wrapped, spy: spy}
}

func newProjectLifecycleHTTPFixtureWithMCP(t *testing.T) *projectLifecycleHTTPFixture {
	t.Helper()

	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(sqlDB, nil)
	wrapped := &projectLifecycleHTTPStore{Store: st}
	adapter := mcpadapter.New(wrapped, mcpadapter.Options{Mode: "full"})
	server := NewServer(wrapped, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		MCPHandler:     adapter,
	})
	spy := &projectLifecycleEventSpy{store: wrapped}
	server.fanout = eventbus.NewFanout(newSSEBridge(server.hub, server.creatorNotificationAuthorizer), spy)
	ts := httptest.NewServer(server)
	t.Cleanup(func() {
		ts.Close()
		_ = sqlDB.Close()
	})
	return &projectLifecycleHTTPFixture{server: server, ts: ts, db: sqlDB, store: st, wrapped: wrapped, spy: spy}
}

func doProjectLifecycleRaw(t *testing.T, client *http.Client, method, url, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Scrumboy", "1")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp, raw
}

func noRedirectProjectLifecycleClient(base *http.Client) *http.Client {
	return &http.Client{
		Transport: base.Transport,
		Jar:       base.Jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func assertProjectLifecycleTrace(t *testing.T, wrapped *projectLifecycleHTTPStore, want ...string) {
	t.Helper()
	if got := wrapped.traceSnapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("lifecycle trace=%v want=%v", got, want)
	}
}

func assertProjectLifecycleNoEvents(t *testing.T, spy *projectLifecycleEventSpy) {
	t.Helper()
	events, reasons := spy.snapshot()
	if len(events) != 0 || len(reasons) != 0 {
		t.Fatalf("unexpected lifecycle events=%+v reasons=%v", events, reasons)
	}
}

func projectLifecycleUserID(t *testing.T, sqlDB *sql.DB, email string) int64 {
	t.Helper()
	var userID int64
	if err := sqlDB.QueryRow(`SELECT id FROM users WHERE email = ?`, email).Scan(&userID); err != nil {
		t.Fatalf("read user id: %v", err)
	}
	return userID
}

func assertProjectLifecycleRESTError(t *testing.T, body []byte, wantCode, wantMessage, wantReason string) {
	t.Helper()
	var envelope apiErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode REST lifecycle error: %v body=%s", err, body)
	}
	if envelope.Error.Code != wantCode || envelope.Error.Message != wantMessage {
		t.Fatalf("REST lifecycle error=%+v want code=%q message=%q", envelope.Error, wantCode, wantMessage)
	}
	gotReason := ""
	if envelope.Error.Details != nil {
		gotReason, _ = envelope.Error.Details["reason"].(string)
	}
	if gotReason != wantReason {
		t.Fatalf("REST lifecycle error reason=%q want=%q envelope=%+v", gotReason, wantReason, envelope.Error)
	}
}

func TestProjectLifecycleRESTDurableCreationOrderingAndEffects(t *testing.T) {
	t.Run("anonymous mode hides route before malformed body", func(t *testing.T) {
		fx := newProjectLifecycleHTTPFixture(t, "anonymous")
		fx.wrapped.activate()
		resp, body := doProjectLifecycleRaw(t, fx.ts.Client(), http.MethodPost, fx.ts.URL+"/api/projects", "{bad")
		if resp.StatusCode != http.StatusNotFound || apiErrorCode(t, body) != "NOT_FOUND" {
			t.Fatalf("status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped)
		assertProjectLifecycleNoEvents(t, fx.spy)
	})

	t.Run("pre-bootstrap create and post-bootstrap parse-auth precedence", func(t *testing.T) {
		fx := newProjectLifecycleHTTPFixture(t, "full")
		fx.wrapped.activate()
		var created map[string]any
		resp, body := doJSON(t, fx.ts.Client(), http.MethodPost, fx.ts.URL+"/api/projects", map[string]any{"name": "  Pre Bootstrap  "}, &created)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("pre-bootstrap status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped, "create-durable")
		if created["name"] != "Pre Bootstrap" || created["slug"] != "pre-bootstrap" {
			t.Fatalf("created project=%+v", created)
		}
		assertProjectLifecycleNoEvents(t, fx.spy)

		ownerClient := newCookieClient(t)
		bootstrapUserClient(t, ownerClient, fx.ts.URL, "Owner", "rest-create-owner@example.com", "password123")
		fx.wrapped.activate()
		fx.spy.reset()
		resp, body = doProjectLifecycleRaw(t, fx.ts.Client(), http.MethodPost, fx.ts.URL+"/api/projects", "{bad")
		if resp.StatusCode != http.StatusBadRequest || apiErrorCode(t, body) != "VALIDATION_ERROR" {
			t.Fatalf("signed-out malformed status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped)

		fx.wrapped.activate()
		resp, body = doJSON(t, fx.ts.Client(), http.MethodPost, fx.ts.URL+"/api/projects", map[string]any{"name": "Needs Actor"}, nil)
		if resp.StatusCode != http.StatusUnauthorized || apiErrorCode(t, body) != "UNAUTHORIZED" {
			t.Fatalf("signed-out valid status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped, "create-durable")
		assertProjectLifecycleNoEvents(t, fx.spy)

		workflow := []map[string]any{
			{"key": " READY ", "name": " Ready ", "color": "", "position": 99, "isDone": false},
			{"key": "DONE", "name": " Done ", "color": " #abcdef ", "position": 42, "isDone": true},
		}
		fx.wrapped.activate()
		resp, body = doJSON(t, ownerClient, http.MethodPost, fx.ts.URL+"/api/projects", map[string]any{"name": " Custom REST ", "workflow": workflow}, &created)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("signed-in custom status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped, "create-durable")
		projectID := int64(created["id"].(float64))
		var keys string
		if err := fx.db.QueryRow(`SELECT group_concat(key, ',') FROM (SELECT key FROM project_workflow_columns WHERE project_id = ? ORDER BY position)`, projectID).Scan(&keys); err != nil {
			t.Fatalf("read custom workflow keys: %v", err)
		}
		if keys != "ready,done" {
			t.Fatalf("custom workflow keys=%q want=ready,done", keys)
		}
		assertProjectLifecycleNoEvents(t, fx.spy)

		fx.wrapped.activate()
		resp, body = doJSON(t, ownerClient, http.MethodPost, fx.ts.URL+"/api/projects", map[string]any{"name": "Null Workflow", "workflow": nil}, &created)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("null workflow status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped, "create-durable")
		nullWorkflowID := int64(created["id"].(float64))
		if got := projectLifecycleHTTPCount(t, fx.db, `SELECT COUNT(*) FROM project_workflow_columns WHERE project_id = ?`, nullWorkflowID); got != 5 {
			t.Fatalf("null workflow default count=%d want=5", got)
		}

		fx.wrapped.activate()
		resp, body = doJSON(t, ownerClient, http.MethodPost, fx.ts.URL+"/api/projects", map[string]any{"name": "Empty Workflow", "workflow": []any{}}, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("empty workflow status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleRESTError(t, body, "VALIDATION_ERROR", "validation: project workflow must have at least 2 columns", "project_workflow_min_columns")
		assertProjectLifecycleTrace(t, fx.wrapped, "create-durable")

		fx.wrapped.activate()
		resp, body = doJSON(t, ownerClient, http.MethodPost, fx.ts.URL+"/api/projects", map[string]any{"name": strings.Repeat("n", 200)}, nil)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("200-character name status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped, "create-durable")
		fx.wrapped.activate()
		resp, body = doJSON(t, ownerClient, http.MethodPost, fx.ts.URL+"/api/projects", map[string]any{"name": strings.Repeat("n", 201)}, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("201-character name status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleRESTError(t, body, "VALIDATION_ERROR", "validation: invalid project name", "invalid_project_name")
		assertProjectLifecycleTrace(t, fx.wrapped, "create-durable")
		assertProjectLifecycleNoEvents(t, fx.spy)
	})
}

func TestProjectLifecycleRESTAnonAndTempCompatibilityAndCommitBoundary(t *testing.T) {
	t.Run("temp redirect itself performs no lifecycle operation", func(t *testing.T) {
		fx := newProjectLifecycleHTTPFixture(t, "full")
		client := noRedirectProjectLifecycleClient(fx.ts.Client())
		fx.wrapped.activate()
		resp, body := doProjectLifecycleRaw(t, client, http.MethodGet, fx.ts.URL+"/temp?source=legacy", "")
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/anon" {
			t.Fatalf("GET /temp status=%d location=%q body=%s", resp.StatusCode, resp.Header.Get("Location"), body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped)
		if got := projectLifecycleHTTPCount(t, fx.db, `SELECT COUNT(*) FROM projects`); got != 0 {
			t.Fatalf("GET /temp project count=%d want=0", got)
		}
		assertProjectLifecycleNoEvents(t, fx.spy)

		fx.wrapped.activate()
		resp, body = doProjectLifecycleRaw(t, client, http.MethodPost, fx.ts.URL+"/temp", "{}")
		if resp.StatusCode != http.StatusMethodNotAllowed || apiErrorCode(t, body) != "METHOD_NOT_ALLOWED" {
			t.Fatalf("POST /temp status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped)
	})

	t.Run("anon honors full-mode signed-in actor and emits nothing", func(t *testing.T) {
		fx := newProjectLifecycleHTTPFixture(t, "full")
		client := newCookieClient(t)
		bootstrapUserClient(t, client, fx.ts.URL, "Creator", "anon-signed-in@example.com", "password123")
		creatorID := projectLifecycleUserID(t, fx.db, "anon-signed-in@example.com")
		client = noRedirectProjectLifecycleClient(client)
		fx.wrapped.activate()
		resp, body := doProjectLifecycleRaw(t, client, http.MethodGet, fx.ts.URL+"/anon", "")
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("GET /anon status=%d body=%s", resp.StatusCode, body)
		}
		location := resp.Header.Get("Location")
		if len(location) < 2 || location[0] != '/' {
			t.Fatalf("GET /anon location=%q", location)
		}
		assertProjectLifecycleTrace(t, fx.wrapped, "create-anonymous")
		var persistedCreator, owner, expires sql.NullInt64
		var projectID int64
		if err := fx.db.QueryRow(`SELECT id, creator_user_id, owner_user_id, expires_at FROM projects WHERE slug = ?`, location[1:]).Scan(&projectID, &persistedCreator, &owner, &expires); err != nil {
			t.Fatalf("read temporary project: %v", err)
		}
		if !persistedCreator.Valid || persistedCreator.Int64 != creatorID || owner.Valid || !expires.Valid {
			t.Fatalf("temporary identity creator=%+v owner=%+v expires=%+v", persistedCreator, owner, expires)
		}
		if got := projectLifecycleHTTPCount(t, fx.db, `SELECT COUNT(*) FROM project_members WHERE project_id = ?`, projectID); got != 0 {
			t.Fatalf("temporary membership count=%d want=0", got)
		}
		if got := projectLifecycleHTTPCount(t, fx.db, `SELECT COUNT(*) FROM tags WHERE project_id = ?`, projectID); got != 0 {
			t.Fatalf("temporary default tags=%d want=0", got)
		}
		assertProjectLifecycleNoEvents(t, fx.spy)
	})

	t.Run("late workflow failure returns 500 after project commit", func(t *testing.T) {
		fx := newProjectLifecycleHTTPFixture(t, "full")
		if _, err := fx.db.Exec(`CREATE TRIGGER lifecycle_http_reject_doing_workflow BEFORE INSERT ON project_workflow_columns WHEN NEW.key = 'doing' BEGIN SELECT RAISE(ABORT, 'reject doing workflow'); END`); err != nil {
			t.Fatalf("create trigger: %v", err)
		}
		client := noRedirectProjectLifecycleClient(fx.ts.Client())
		fx.wrapped.activate()
		resp, body := doProjectLifecycleRaw(t, client, http.MethodGet, fx.ts.URL+"/anon", "")
		if resp.StatusCode != http.StatusInternalServerError || apiErrorCode(t, body) != "INTERNAL" {
			t.Fatalf("late workflow status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleRESTError(t, body, "INTERNAL", "failed to create board", "")
		assertProjectLifecycleTrace(t, fx.wrapped, "create-anonymous")
		var projectID int64
		if err := fx.db.QueryRow(`SELECT id FROM projects WHERE name = 'Anonymous Board' ORDER BY id DESC LIMIT 1`).Scan(&projectID); err != nil {
			t.Fatalf("committed project missing: %v", err)
		}
		if got := projectLifecycleHTTPCount(t, fx.db, `SELECT COUNT(*) FROM project_workflow_columns WHERE project_id = ?`, projectID); got != 2 {
			t.Fatalf("partial workflow count=%d want=2", got)
		}
		if got := projectLifecycleHTTPCount(t, fx.db, `SELECT COUNT(*) FROM project_priorities WHERE project_id = ?`, projectID); got != 4 {
			t.Fatalf("committed priorities=%d want=4", got)
		}
		assertProjectLifecycleNoEvents(t, fx.spy)
	})
}

func projectLifecycleHTTPCount(t *testing.T, sqlDB *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count lifecycle rows: %v", err)
	}
	return count
}

func TestProjectLifecycleRESTUpdatePartialSuccessPostReadAndPublication(t *testing.T) {
	t.Run("image commits before later invalid name and failure is silent", func(t *testing.T) {
		fx := newProjectLifecycleHTTPFixture(t, "full")
		client := newCookieClient(t)
		bootstrapUserClient(t, client, fx.ts.URL, "Owner", "update-partial@example.com", "password123")
		ownerID := projectLifecycleUserID(t, fx.db, "update-partial@example.com")
		project, err := fx.store.CreateProject(store.WithUserID(context.Background(), ownerID), "Partial Update")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}

		image := "data:image/png;base64,aaaa"
		fx.wrapped.activate()
		resp, body := doJSON(t, client, http.MethodPatch, fx.ts.URL+"/api/projects/"+strconv.FormatInt(project.ID, 10), map[string]any{"image": image, "name": "   "}, nil)
		if resp.StatusCode != http.StatusBadRequest || apiErrorCode(t, body) != "VALIDATION_ERROR" {
			t.Fatalf("partial update status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleRESTError(t, body, "VALIDATION_ERROR", "validation: invalid project name", "invalid_project_name")
		assertProjectLifecycleTrace(t, fx.wrapped, "update-image", "update-name")
		fx.wrapped.deactivate()
		persisted, err := fx.store.GetProject(context.Background(), project.ID)
		if err != nil {
			t.Fatalf("GetProject: %v", err)
		}
		if persisted.Image == nil || *persisted.Image != image || persisted.Name != project.Name {
			t.Fatalf("partial state=%+v want image committed/name unchanged", persisted)
		}
		if got := projectLifecycleHTTPCount(t, fx.db, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action = 'project_image_updated'`, project.ID); got != 1 {
			t.Fatalf("image audit count=%d want=1", got)
		}
		assertProjectLifecycleNoEvents(t, fx.spy)
	})

	t.Run("both writes commit before post-read error and are not retried", func(t *testing.T) {
		fx := newProjectLifecycleHTTPFixture(t, "full")
		client := newCookieClient(t)
		bootstrapUserClient(t, client, fx.ts.URL, "Owner", "update-postread@example.com", "password123")
		ownerID := projectLifecycleUserID(t, fx.db, "update-postread@example.com")
		project, err := fx.store.CreateProject(store.WithUserID(context.Background(), ownerID), "Post Read Original")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}

		fx.wrapped.postReadErr = errors.New("project lifecycle post-read failed")
		fx.wrapped.activate()
		image := "data:image/png;base64,bbbb"
		resp, body := doJSON(t, client, http.MethodPatch, fx.ts.URL+"/api/projects/"+strconv.FormatInt(project.ID, 10), map[string]any{"image": image, "name": "Post Read Committed"}, nil)
		if resp.StatusCode != http.StatusInternalServerError || apiErrorCode(t, body) != "INTERNAL" {
			t.Fatalf("post-read status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleRESTError(t, body, "INTERNAL", "internal error", "")
		assertProjectLifecycleTrace(t, fx.wrapped, "update-image", "update-name", "get-project")
		fx.wrapped.deactivate()
		persisted, err := fx.store.GetProject(context.Background(), project.ID)
		if err != nil {
			t.Fatalf("GetProject: %v", err)
		}
		if persisted.Name != "Post Read Committed" || persisted.Image == nil || *persisted.Image != image {
			t.Fatalf("post-read committed state=%+v", persisted)
		}
		if got := projectLifecycleHTTPCount(t, fx.db, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action IN ('project_image_updated','project_renamed')`, project.ID); got != 2 {
			t.Fatalf("committed update audit count=%d want=2", got)
		}
		assertProjectLifecycleNoEvents(t, fx.spy)
	})

	t.Run("successful update publishes once after post-read while empty patch does not", func(t *testing.T) {
		fx := newProjectLifecycleHTTPFixture(t, "full")
		client := newCookieClient(t)
		bootstrapUserClient(t, client, fx.ts.URL, "Owner", "update-publish@example.com", "password123")
		ownerID := projectLifecycleUserID(t, fx.db, "update-publish@example.com")
		project, err := fx.store.CreateProject(store.WithUserID(context.Background(), ownerID), "Publish Original")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}

		fx.wrapped.activate()
		resp, body := doJSON(t, client, http.MethodPatch, fx.ts.URL+"/api/projects/"+strconv.FormatInt(project.ID, 10), map[string]any{"name": "Publish Changed"}, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("successful update status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped, "update-name", "get-project", "publish:project_updated")
		events, reasons := fx.spy.snapshot()
		if len(events) != 1 || !reflect.DeepEqual(reasons, []string{"project_updated"}) || events[0].ProjectID != project.ID {
			t.Fatalf("update events=%+v reasons=%v", events, reasons)
		}
		var payload refreshNeededPayload
		if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
			t.Fatalf("decode refresh payload: %v", err)
		}
		if payload.ActorUserID != ownerID || payload.LocalID != 0 || payload.Title != "" || payload.Name != "" {
			t.Fatalf("update refresh payload=%+v", payload)
		}

		fx.spy.reset()
		fx.wrapped.activate()
		resp, body = doJSON(t, client, http.MethodPatch, fx.ts.URL+"/api/projects/"+strconv.FormatInt(project.ID, 10), map[string]any{}, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("empty patch status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped, "get-project")
		assertProjectLifecycleNoEvents(t, fx.spy)

		fx.spy.reset()
		fx.wrapped.activate()
		resp, body = doJSON(t, client, http.MethodPatch, fx.ts.URL+"/api/projects/"+strconv.FormatInt(project.ID, 10), map[string]any{"name": "Publish Changed"}, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("same-value patch status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped, "update-name", "get-project", "publish:project_updated")
		if got := projectLifecycleHTTPCount(t, fx.db, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action = 'project_renamed'`, project.ID); got != 2 {
			t.Fatalf("changed plus same-value rename audits=%d want=2", got)
		}
		events, reasons = fx.spy.snapshot()
		if len(events) != 1 || !reflect.DeepEqual(reasons, []string{"project_updated"}) {
			t.Fatalf("same-value update events=%+v reasons=%v", events, reasons)
		}
	})
}

func TestProjectLifecycleRESTUpdateModeSpecificParseOrdering(t *testing.T) {
	t.Run("anonymous mode resolves target before malformed body", func(t *testing.T) {
		fx := newProjectLifecycleHTTPFixture(t, "anonymous")
		board, err := fx.store.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatalf("CreateAnonymousBoard: %v", err)
		}
		fx.wrapped.activate()
		resp, body := doProjectLifecycleRaw(t, fx.ts.Client(), http.MethodPatch, fx.ts.URL+"/api/projects/"+strconv.FormatInt(board.ID, 10), "{bad")
		if resp.StatusCode != http.StatusBadRequest || apiErrorCode(t, body) != "VALIDATION_ERROR" {
			t.Fatalf("anonymous malformed status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped, "get-project")
	})

	t.Run("full mode parses malformed body before missing target", func(t *testing.T) {
		fx := newProjectLifecycleHTTPFixture(t, "full")
		fx.wrapped.activate()
		resp, body := doProjectLifecycleRaw(t, fx.ts.Client(), http.MethodPatch, fx.ts.URL+"/api/projects/999999", "{bad")
		if resp.StatusCode != http.StatusBadRequest || apiErrorCode(t, body) != "VALIDATION_ERROR" {
			t.Fatalf("full malformed status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped)
	})
}

func TestProjectLifecycleRESTDeletionSequencingAndEffects(t *testing.T) {
	t.Run("committed snapshot drives exactly one publication", func(t *testing.T) {
		fx := newProjectLifecycleHTTPFixture(t, "full")
		client := newCookieClient(t)
		bootstrapUserClient(t, client, fx.ts.URL, "Owner", "delete-sequence@example.com", "password123")
		ownerID := projectLifecycleUserID(t, fx.db, "delete-sequence@example.com")
		project, err := fx.store.CreateProject(store.WithUserID(context.Background(), ownerID), "Delete Sequence")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}

		fx.wrapped.activate()
		resp, body := doJSON(t, client, http.MethodDelete, fx.ts.URL+"/api/projects/"+strconv.FormatInt(project.ID, 10), nil, nil)
		if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
			t.Fatalf("delete status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped, "delete-project", "publish:project_deleted")
		events, reasons := fx.spy.snapshot()
		if len(events) != 1 || !reflect.DeepEqual(reasons, []string{"project_deleted"}) || events[0].ProjectID != project.ID {
			t.Fatalf("delete events=%+v reasons=%v", events, reasons)
		}

		fx.spy.reset()
		fx.wrapped.activate()
		resp, body = doJSON(t, client, http.MethodDelete, fx.ts.URL+"/api/projects/"+strconv.FormatInt(project.ID, 10), nil, nil)
		if resp.StatusCode != http.StatusNotFound || apiErrorCode(t, body) != "NOT_FOUND" {
			t.Fatalf("repeat delete status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped, "delete-project")
		assertProjectLifecycleNoEvents(t, fx.spy)
	})

	t.Run("mutation failure has no publication and leaves project", func(t *testing.T) {
		fx := newProjectLifecycleHTTPFixture(t, "full")
		client := newCookieClient(t)
		bootstrapUserClient(t, client, fx.ts.URL, "Owner", "delete-failure@example.com", "password123")
		ownerID := projectLifecycleUserID(t, fx.db, "delete-failure@example.com")
		project, err := fx.store.CreateProject(store.WithUserID(context.Background(), ownerID), "Delete Failure")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		fx.wrapped.deleteErr = store.ErrConflict
		fx.wrapped.activate()
		resp, body := doJSON(t, client, http.MethodDelete, fx.ts.URL+"/api/projects/"+strconv.FormatInt(project.ID, 10), nil, nil)
		if resp.StatusCode != http.StatusConflict || apiErrorCode(t, body) != "CONFLICT" {
			t.Fatalf("failed delete status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleRESTError(t, body, "CONFLICT", "conflict", "")
		assertProjectLifecycleTrace(t, fx.wrapped, "delete-project")
		assertProjectLifecycleNoEvents(t, fx.spy)
		if got := projectLifecycleHTTPCount(t, fx.db, `SELECT COUNT(*) FROM projects WHERE id = ?`, project.ID); got != 1 {
			t.Fatalf("project count after failed delete=%d want=1", got)
		}
	})
}

func TestProjectLifecycleRESTClaimConditionalAuthorityAndEffects(t *testing.T) {
	fx := newProjectLifecycleHTTPFixture(t, "full")
	creatorClient := newCookieClient(t)
	bootstrapUserClient(t, creatorClient, fx.ts.URL, "Creator", "claim-creator-http@example.com", "password123")
	creatorID := projectLifecycleUserID(t, fx.db, "claim-creator-http@example.com")
	creatorCtx := store.WithUserID(context.Background(), creatorID)

	t.Run("anonymous caller cannot claim creatorless board", func(t *testing.T) {
		board, err := fx.store.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatalf("CreateAnonymousBoard: %v", err)
		}
		fx.spy.reset()
		fx.wrapped.activate()
		resp, body := doProjectLifecycleRaw(t, fx.ts.Client(), http.MethodPost, fx.ts.URL+"/api/board/"+board.Slug+"/claim", "{ignored")
		if resp.StatusCode != http.StatusUnauthorized || apiErrorCode(t, body) != "UNAUTHORIZED" {
			t.Fatalf("anonymous claim status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped, "access")
		assertProjectLifecycleNoEvents(t, fx.spy)
	})

	t.Run("recorded creator claims with ignored body and one refresh", func(t *testing.T) {
		board, err := fx.store.CreateAnonymousBoard(creatorCtx)
		if err != nil {
			t.Fatalf("CreateAnonymousBoard: %v", err)
		}
		fx.spy.reset()
		fx.wrapped.activate()
		resp, body := doProjectLifecycleRaw(t, creatorClient, http.MethodPost, fx.ts.URL+"/api/board/"+board.Slug+"/claim", "{ignored")
		if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
			t.Fatalf("creator claim status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped, "access", "claim", "publish:board_claimed")
		events, reasons := fx.spy.snapshot()
		if len(events) != 1 || !reflect.DeepEqual(reasons, []string{"board_claimed"}) || events[0].ProjectID != board.ID {
			t.Fatalf("claim events=%+v reasons=%v", events, reasons)
		}

		fx.spy.reset()
		fx.wrapped.activate()
		resp, body = doProjectLifecycleRaw(t, creatorClient, http.MethodPost, fx.ts.URL+"/api/board/"+board.Slug+"/claim", "")
		if resp.StatusCode != http.StatusNotFound || apiErrorCode(t, body) != "NOT_FOUND" {
			t.Fatalf("repeat claim status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped, "access", "claim")
		assertProjectLifecycleNoEvents(t, fx.spy)
	})

	t.Run("non-creator and expired boards fail without refresh", func(t *testing.T) {
		other, err := fx.store.CreateUser(context.Background(), "claim-other-http@example.com", "password123", "Other")
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		otherClient := newCookieClient(t)
		loginUserClient(t, otherClient, fx.ts.URL, other.Email, "password123")
		board, err := fx.store.CreateAnonymousBoard(creatorCtx)
		if err != nil {
			t.Fatalf("CreateAnonymousBoard: %v", err)
		}
		fx.spy.reset()
		fx.wrapped.activate()
		resp, body := doProjectLifecycleRaw(t, otherClient, http.MethodPost, fx.ts.URL+"/api/board/"+board.Slug+"/claim", "")
		if resp.StatusCode != http.StatusNotFound || apiErrorCode(t, body) != "NOT_FOUND" {
			t.Fatalf("non-creator claim status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped, "access", "claim")
		assertProjectLifecycleNoEvents(t, fx.spy)

		expired, err := fx.store.CreateAnonymousBoard(creatorCtx)
		if err != nil {
			t.Fatalf("CreateAnonymousBoard expired fixture: %v", err)
		}
		if _, err := fx.db.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Hour).UnixMilli(), expired.ID); err != nil {
			t.Fatalf("expire board: %v", err)
		}
		fx.spy.reset()
		fx.wrapped.activate()
		resp, body = doProjectLifecycleRaw(t, creatorClient, http.MethodPost, fx.ts.URL+"/api/board/"+expired.Slug+"/claim", "")
		if resp.StatusCode != http.StatusNotFound || apiErrorCode(t, body) != "NOT_FOUND" {
			t.Fatalf("expired claim status=%d body=%s", resp.StatusCode, body)
		}
		assertProjectLifecycleTrace(t, fx.wrapped, "access")
		assertProjectLifecycleNoEvents(t, fx.spy)
	})
}

func TestProjectLifecycleMCPCreateObservableSilenceBothTransports(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newProjectLifecycleHTTPFixtureWithMCP(t)
			client := newCookieClient(t)
			bootstrapUserClient(t, client, fx.ts.URL, "Owner", "mcp-create-silence-"+transport+"@example.com", "password123")

			var nextProjectID int64
			if err := fx.db.QueryRow(`SELECT COALESCE(MAX(id), 0) + 1 FROM projects`).Scan(&nextProjectID); err != nil {
				t.Fatalf("predict next project id: %v", err)
			}
			hubEvents, unsubscribe := fx.server.hub.Subscribe(nextProjectID)
			defer unsubscribe()
			fx.wrapped.activate()

			var resp *http.Response
			var body []byte
			if transport == "legacy" {
				resp, body = doJSON(t, client, http.MethodPost, fx.ts.URL+"/mcp", map[string]any{
					"tool":  "projects_create",
					"input": map[string]any{"name": "MCP Create Silence " + transport},
				}, nil)
			} else {
				raw, err := json.Marshal(map[string]any{
					"jsonrpc": "2.0",
					"id":      24,
					"method":  "tools/call",
					"params": map[string]any{
						"name":      "projects_create",
						"arguments": map[string]any{"name": "MCP Create Silence " + transport},
					},
				})
				if err != nil {
					t.Fatalf("marshal JSON-RPC request: %v", err)
				}
				req, err := http.NewRequest(http.MethodPost, fx.ts.URL+"/mcp/rpc", bytes.NewReader(raw))
				if err != nil {
					t.Fatalf("new JSON-RPC request: %v", err)
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("MCP-Protocol-Version", "2025-11-25")
				resp, err = client.Do(req)
				if err != nil {
					t.Fatalf("do JSON-RPC request: %v", err)
				}
				defer resp.Body.Close()
				body, err = io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("read JSON-RPC response: %v", err)
				}
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("MCP create status=%d body=%s", resp.StatusCode, body)
			}
			assertProjectLifecycleTrace(t, fx.wrapped, "create-mcp")
			assertProjectLifecycleNoEvents(t, fx.spy)
			select {
			case event := <-hubEvents:
				t.Fatalf("MCP create emitted hub event: %s", event)
			default:
			}
			if got := projectLifecycleHTTPCount(t, fx.db, `SELECT COUNT(*) FROM projects WHERE id = ?`, nextProjectID); got != 1 {
				t.Fatalf("created project count=%d want=1", got)
			}
		})
	}
}
