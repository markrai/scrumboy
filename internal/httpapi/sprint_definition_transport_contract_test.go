package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/eventbus"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

type sprintDefinitionRESTEventCollector struct {
	mu     sync.Mutex
	events []eventbus.Event
}

func (c *sprintDefinitionRESTEventCollector) OnEvent(_ context.Context, event eventbus.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
}

func (c *sprintDefinitionRESTEventCollector) snapshot() []eventbus.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]eventbus.Event(nil), c.events...)
}

type sprintDefinitionRESTStore struct {
	*store.Store

	mu sync.Mutex

	active bool
	trace  []string

	accessErr      error
	accessOverride *store.ProjectContext
	roleErr        error
	targetErr      error
	createErr      error
	updateErr      error

	accessSlug string
	accessMode store.Mode
	rolePID    int64
	roleUID    int64

	targetReads int
	targetID    int64

	createCalls int
	createPID   int64
	createName  string
	createStart time.Time
	createEnd   time.Time

	updateCalls int
	updateID    int64
	updateInput store.UpdateSprintInput
}

func (s *sprintDefinitionRESTStore) activate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = true
	s.trace = nil
	s.targetReads = 0
	s.createCalls = 0
	s.updateCalls = 0
}

func (s *sprintDefinitionRESTStore) record(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return false
	}
	s.trace = append(s.trace, name)
	return true
}

func (s *sprintDefinitionRESTStore) snapshotTrace() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.trace...)
}

func (s *sprintDefinitionRESTStore) GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error) {
	if !s.record("access") {
		return s.Store.GetProjectContextBySlug(ctx, slug, mode)
	}
	s.mu.Lock()
	s.accessSlug = slug
	s.accessMode = mode
	err := s.accessErr
	override := s.accessOverride
	s.mu.Unlock()
	if err != nil {
		return store.ProjectContext{}, err
	}
	if override != nil {
		return *override, nil
	}
	return s.Store.GetProjectContextBySlug(ctx, slug, mode)
}

func (s *sprintDefinitionRESTStore) GetProjectRole(ctx context.Context, projectID, userID int64) (store.ProjectRole, error) {
	if !s.record("role") {
		return s.Store.GetProjectRole(ctx, projectID, userID)
	}
	s.mu.Lock()
	s.rolePID = projectID
	s.roleUID = userID
	err := s.roleErr
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	return s.Store.GetProjectRole(ctx, projectID, userID)
}

func (s *sprintDefinitionRESTStore) GetSprintByID(ctx context.Context, sprintID int64) (store.Sprint, error) {
	if !s.record("target") {
		return s.Store.GetSprintByID(ctx, sprintID)
	}
	s.mu.Lock()
	s.targetReads++
	s.targetID = sprintID
	err := s.targetErr
	s.mu.Unlock()
	if err != nil {
		return store.Sprint{}, err
	}
	return s.Store.GetSprintByID(ctx, sprintID)
}

func (s *sprintDefinitionRESTStore) CreateSprint(ctx context.Context, projectID int64, name string, plannedStartAt, plannedEndAt time.Time) (store.Sprint, error) {
	if !s.record("create") {
		return s.Store.CreateSprint(ctx, projectID, name, plannedStartAt, plannedEndAt)
	}
	s.mu.Lock()
	s.createCalls++
	s.createPID = projectID
	s.createName = name
	s.createStart = plannedStartAt
	s.createEnd = plannedEndAt
	err := s.createErr
	s.mu.Unlock()
	if err != nil {
		return store.Sprint{}, err
	}
	return s.Store.CreateSprint(ctx, projectID, name, plannedStartAt, plannedEndAt)
}

func (s *sprintDefinitionRESTStore) UpdateSprint(ctx context.Context, sprintID int64, in store.UpdateSprintInput) error {
	if !s.record("update") {
		return s.Store.UpdateSprint(ctx, sprintID, in)
	}
	s.mu.Lock()
	s.updateCalls++
	s.updateID = sprintID
	s.updateInput = in
	err := s.updateErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.Store.UpdateSprint(ctx, sprintID, in)
}

type sprintDefinitionRESTFixture struct {
	ts        *httptest.Server
	db        *sql.DB
	st        *store.Store
	wrapped   *sprintDefinitionRESTStore
	client    *http.Client
	ownerID   int64
	ctx       context.Context
	project   store.Project
	projectPC store.ProjectContext
	collector *sprintDefinitionRESTEventCollector
}

func newSprintDefinitionRESTFixture(t *testing.T, name string) *sprintDefinitionRESTFixture {
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
	wrapped := &sprintDefinitionRESTStore{Store: st}
	srv := NewServer(wrapped, Options{MaxRequestBody: 1 << 20, ScrumboyMode: "full"})
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		_ = sqlDB.Close()
	})

	client := newCookieClient(t)
	owner := bootstrapUserClient(t, client, ts.URL, "Sprint Owner", name+"@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	ctx := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctx, name)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	pc, err := st.GetProjectContextBySlug(ctx, project.Slug, store.ModeFull)
	if err != nil {
		t.Fatalf("GetProjectContextBySlug: %v", err)
	}

	collector := &sprintDefinitionRESTEventCollector{}
	srv.fanout = eventbus.NewFanout(collector)

	return &sprintDefinitionRESTFixture{
		ts: ts, db: sqlDB, st: st, wrapped: wrapped, client: client,
		ownerID: ownerID, ctx: ctx, project: project, projectPC: pc, collector: collector,
	}
}

func (fx *sprintDefinitionRESTFixture) createURL() string {
	return fx.ts.URL + "/api/board/" + fx.project.Slug + "/sprints"
}

func (fx *sprintDefinitionRESTFixture) updateURL(sprintID int64) string {
	return fmt.Sprintf("%s/api/board/%s/sprints/%d", fx.ts.URL, fx.project.Slug, sprintID)
}

func createSprintDefinitionRESTSprint(t *testing.T, fx *sprintDefinitionRESTFixture, name string) store.Sprint {
	t.Helper()
	start := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	sp, err := fx.st.CreateSprint(fx.ctx, fx.project.ID, name, start, start.Add(7*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint fixture: %v", err)
	}
	return sp
}

func assertSprintDefinitionRESTTrace(t *testing.T, wrapped *sprintDefinitionRESTStore, want ...string) {
	t.Helper()
	if got := wrapped.snapshotTrace(); !reflect.DeepEqual(got, want) {
		t.Fatalf("store trace=%v, want %v", got, want)
	}
}

func assertSprintDefinitionRESTError(t *testing.T, resp *http.Response, got apiErrorEnvelope, wantStatus int, wantCode, wantMessage string, wantDetail *string) {
	t.Helper()
	if resp.StatusCode != wantStatus || got.Error.Code != wantCode || got.Error.Message != wantMessage {
		t.Fatalf("error status=%d envelope=%+v, want status=%d code=%q message=%q", resp.StatusCode, got, wantStatus, wantCode, wantMessage)
	}
	if wantDetail == nil {
		if got.Error.Details != nil {
			t.Fatalf("error details=%+v, want null", got.Error.Details)
		}
		return
	}
	if got.Error.Details == nil || got.Error.Details["detail"] != *wantDetail {
		t.Fatalf("error details=%+v, want detail=%q", got.Error.Details, *wantDetail)
	}
}

func assertSprintDefinitionRESTRefresh(t *testing.T, fx *sprintDefinitionRESTFixture, reason string) {
	t.Helper()
	events := fx.collector.snapshot()
	if len(events) != 1 {
		t.Fatalf("events=%+v, want one board refresh", events)
	}
	event := events[0]
	if event.Type != "board.refresh_needed" || event.ProjectID != fx.project.ID {
		t.Fatalf("event=%+v, want board.refresh_needed for project %d", event, fx.project.ID)
	}
	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("decode refresh payload: %v", err)
	}
	if payload["reason"] != reason || int64(payload["actorUserId"].(float64)) != fx.ownerID {
		t.Fatalf("refresh payload=%+v, want reason=%q actor=%d", payload, reason, fx.ownerID)
	}
}

func assertSprintDefinitionRESTSilence(t *testing.T, fx *sprintDefinitionRESTFixture) {
	t.Helper()
	if events := fx.collector.snapshot(); len(events) != 0 {
		t.Fatalf("unexpected sprint refresh events: %+v", events)
	}
}

func sprintDefinitionRESTSessionClient(t *testing.T, fx *sprintDefinitionRESTFixture, userID int64) *http.Client {
	t.Helper()
	token, expiresAt, err := fx.st.CreateSession(context.Background(), userID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	base, err := url.Parse(fx.ts.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	jar.SetCookies(base, []*http.Cookie{{Name: "scrumboy_session", Value: token, Path: "/", Expires: expiresAt}})
	return &http.Client{Transport: fx.ts.Client().Transport, Jar: jar}
}

func TestSprintDefinitionRESTCreateAndUpdateContracts(t *testing.T) {
	t.Run("create uses Unix milliseconds and publishes once after one store call", func(t *testing.T) {
		fx := newSprintDefinitionRESTFixture(t, "rest-sprint-definition-create")
		start := time.Date(2026, time.August, 11, 13, 14, 15, 321000000, time.UTC)
		end := start.Add(9 * 24 * time.Hour)
		fx.wrapped.activate()

		var got sprintJSON
		resp, body := doJSON(t, fx.client, http.MethodPost, fx.createURL(), map[string]any{
			"name": "  REST definition  ", "plannedStartAt": start.UnixMilli(), "plannedEndAt": end.UnixMilli(),
		}, &got)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create status=%d body=%s", resp.StatusCode, body)
		}
		if got.ProjectID != fx.project.ID || got.Name != "REST definition" || got.PlannedStartAt != start.UnixMilli() || got.PlannedEndAt != end.UnixMilli() || got.State != store.SprintStatePlanned {
			t.Fatalf("created sprint=%+v", got)
		}
		assertSprintDefinitionRESTTrace(t, fx.wrapped, "access", "role", "create")
		if fx.wrapped.accessSlug != fx.project.Slug || fx.wrapped.accessMode != store.ModeFull {
			t.Fatalf("access=(slug=%q mode=%q), want (%q,%q)", fx.wrapped.accessSlug, fx.wrapped.accessMode, fx.project.Slug, store.ModeFull)
		}
		if fx.wrapped.rolePID != fx.project.ID || fx.wrapped.roleUID != fx.ownerID {
			t.Fatalf("role args=(%d,%d), want (%d,%d)", fx.wrapped.rolePID, fx.wrapped.roleUID, fx.project.ID, fx.ownerID)
		}
		if fx.wrapped.createCalls != 1 || fx.wrapped.createPID != fx.project.ID || fx.wrapped.createName != "  REST definition  " || !fx.wrapped.createStart.Equal(start) || !fx.wrapped.createEnd.Equal(end) {
			t.Fatalf("create call=(count=%d project=%d name=%q start=%s end=%s)", fx.wrapped.createCalls, fx.wrapped.createPID, fx.wrapped.createName, fx.wrapped.createStart, fx.wrapped.createEnd)
		}
		assertSprintDefinitionRESTRefresh(t, fx, "sprint_created")
	})

	t.Run("non-empty update binds stored ID and performs no route post-read", func(t *testing.T) {
		fx := newSprintDefinitionRESTFixture(t, "rest-sprint-definition-update")
		offsetProject, err := fx.st.CreateProject(fx.ctx, "rest-sprint-definition-update-offset")
		if err != nil {
			t.Fatalf("CreateProject offset: %v", err)
		}
		offsetStart := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
		if _, err := fx.st.CreateSprint(fx.ctx, offsetProject.ID, "Offset sprint", offsetStart, offsetStart.Add(24*time.Hour)); err != nil {
			t.Fatalf("CreateSprint offset: %v", err)
		}
		sp := createSprintDefinitionRESTSprint(t, fx, "Before update")
		if sp.ID == sp.Number {
			t.Fatalf("identity fixture requires stored ID and project-local number to differ, sprint=%+v", sp)
		}
		newName := "After update"
		newStart := sp.PlannedStartAt.Add(24 * time.Hour)
		newEnd := sp.PlannedEndAt.Add(48 * time.Hour)
		fx.wrapped.activate()

		resp, body := doJSON(t, fx.client, http.MethodPatch, fx.updateURL(sp.ID), map[string]any{
			"name": newName, "plannedStartAt": newStart.UnixMilli(), "plannedEndAt": newEnd.UnixMilli(),
		}, nil)
		if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
			t.Fatalf("update status=%d body=%q", resp.StatusCode, body)
		}
		assertSprintDefinitionRESTTrace(t, fx.wrapped, "access", "target", "role", "update")
		if fx.wrapped.targetReads != 1 || fx.wrapped.targetID != sp.ID || fx.wrapped.updateCalls != 1 || fx.wrapped.updateID != sp.ID {
			t.Fatalf("target/update=(reads=%d targetID=%d calls=%d updateID=%d), want stored ID %d exactly once", fx.wrapped.targetReads, fx.wrapped.targetID, fx.wrapped.updateCalls, fx.wrapped.updateID, sp.ID)
		}
		in := fx.wrapped.updateInput
		if in.Name == nil || *in.Name != newName || in.PlannedStartAt == nil || !in.PlannedStartAt.Equal(newStart) || in.PlannedEndAt == nil || !in.PlannedEndAt.Equal(newEnd) {
			t.Fatalf("update input=%+v", in)
		}
		stored, err := fx.st.GetSprintByID(fx.ctx, sp.ID)
		if err != nil {
			t.Fatalf("GetSprintByID: %v", err)
		}
		if stored.Name != newName || !stored.PlannedStartAt.Equal(newStart) || !stored.PlannedEndAt.Equal(newEnd) {
			t.Fatalf("stored sprint=%+v", stored)
		}
		assertSprintDefinitionRESTRefresh(t, fx, "sprint_updated")
		assertRefreshNeededName(t, fx.collector.snapshot()[0].Payload, newName)
	})

	t.Run("empty update still calls store and publishes without post-read", func(t *testing.T) {
		fx := newSprintDefinitionRESTFixture(t, "rest-sprint-definition-empty")
		sp := createSprintDefinitionRESTSprint(t, fx, "Empty update")
		fx.wrapped.activate()

		resp, body := doJSON(t, fx.client, http.MethodPatch, fx.updateURL(sp.ID), map[string]any{}, nil)
		if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
			t.Fatalf("empty update status=%d body=%q", resp.StatusCode, body)
		}
		assertSprintDefinitionRESTTrace(t, fx.wrapped, "access", "target", "role", "update")
		if fx.wrapped.updateCalls != 1 || fx.wrapped.targetReads != 1 {
			t.Fatalf("empty update calls=%d targetReads=%d, want one each", fx.wrapped.updateCalls, fx.wrapped.targetReads)
		}
		if in := fx.wrapped.updateInput; in.Name != nil || in.PlannedStartAt != nil || in.PlannedEndAt != nil {
			t.Fatalf("empty update input=%+v", in)
		}
		assertSprintDefinitionRESTRefresh(t, fx, "sprint_updated")
		assertRefreshNeededName(t, fx.collector.snapshot()[0].Payload, sp.Name)
	})
}

func TestSprintDefinitionRESTAuthorityAndInputContracts(t *testing.T) {
	t.Run("signed out full-mode access hides project before actor extraction", func(t *testing.T) {
		fx := newSprintDefinitionRESTFixture(t, "rest-sprint-definition-signed-out")
		fx.wrapped.activate()
		var got apiErrorEnvelope
		resp, _ := doJSON(t, fx.ts.Client(), http.MethodPost, fx.createURL(), map[string]any{
			"name": "Denied", "plannedStartAt": int64(1), "plannedEndAt": int64(2),
		}, &got)
		assertSprintDefinitionRESTError(t, resp, got, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		assertSprintDefinitionRESTTrace(t, fx.wrapped, "access")
		assertSprintDefinitionRESTSilence(t, fx)
	})

	t.Run("actor absence after a successful access result returns unauthorized", func(t *testing.T) {
		fx := newSprintDefinitionRESTFixture(t, "rest-sprint-definition-actor")
		fx.wrapped.accessOverride = &fx.projectPC
		fx.wrapped.activate()
		var got apiErrorEnvelope
		resp, _ := doJSON(t, fx.ts.Client(), http.MethodPost, fx.createURL(), map[string]any{
			"name": "Denied", "plannedStartAt": int64(1), "plannedEndAt": int64(2),
		}, &got)
		assertSprintDefinitionRESTError(t, resp, got, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		assertSprintDefinitionRESTTrace(t, fx.wrapped, "access")
		assertSprintDefinitionRESTSilence(t, fx)
	})

	t.Run("viewer is denied by the fresh role lookup", func(t *testing.T) {
		fx := newSprintDefinitionRESTFixture(t, "rest-sprint-definition-viewer")
		viewer, err := fx.st.CreateUser(context.Background(), "rest-sprint-viewer@example.com", "password123", "Viewer")
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		if err := fx.st.AddProjectMember(fx.ctx, fx.ownerID, fx.project.ID, viewer.ID, store.RoleViewer); err != nil {
			t.Fatalf("AddProjectMember: %v", err)
		}
		client := sprintDefinitionRESTSessionClient(t, fx, viewer.ID)
		fx.wrapped.activate()
		var got apiErrorEnvelope
		resp, _ := doJSON(t, client, http.MethodPost, fx.createURL(), map[string]any{
			"name": "Denied", "plannedStartAt": int64(1), "plannedEndAt": int64(2),
		}, &got)
		assertSprintDefinitionRESTError(t, resp, got, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
		assertSprintDefinitionRESTTrace(t, fx.wrapped, "access", "role")
		if fx.wrapped.roleUID != viewer.ID {
			t.Fatalf("role user=%d, want viewer %d", fx.wrapped.roleUID, viewer.ID)
		}
		assertSprintDefinitionRESTSilence(t, fx)
	})

	t.Run("cross-project target is rejected before role and body", func(t *testing.T) {
		fx := newSprintDefinitionRESTFixture(t, "rest-sprint-definition-cross-project")
		other, err := fx.st.CreateProject(fx.ctx, "rest-sprint-definition-other-project")
		if err != nil {
			t.Fatalf("CreateProject other: %v", err)
		}
		start := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
		foreign, err := fx.st.CreateSprint(fx.ctx, other.ID, "Foreign", start, start.Add(24*time.Hour))
		if err != nil {
			t.Fatalf("CreateSprint other: %v", err)
		}
		fx.wrapped.activate()
		var got apiErrorEnvelope
		resp, _ := doJSON(t, fx.client, http.MethodPatch, fx.updateURL(foreign.ID), map[string]any{"name": "Should not decode"}, &got)
		assertSprintDefinitionRESTError(t, resp, got, http.StatusNotFound, "NOT_FOUND", "sprint not found", nil)
		assertSprintDefinitionRESTTrace(t, fx.wrapped, "access", "target")
		assertSprintDefinitionRESTSilence(t, fx)
	})

	t.Run("state is not a public definition patch field", func(t *testing.T) {
		fx := newSprintDefinitionRESTFixture(t, "rest-sprint-definition-state-field")
		sp := createSprintDefinitionRESTSprint(t, fx, "No state patch")
		fx.wrapped.activate()
		var got apiErrorEnvelope
		resp, _ := doJSON(t, fx.client, http.MethodPatch, fx.updateURL(sp.ID), map[string]any{"state": "ACTIVE"}, &got)
		if resp.StatusCode != http.StatusBadRequest || got.Error.Code != "VALIDATION_ERROR" || got.Error.Message != "invalid json" || got.Error.Details["reason"] != "invalid_json" {
			t.Fatalf("state patch response status=%d envelope=%+v", resp.StatusCode, got)
		}
		assertSprintDefinitionRESTTrace(t, fx.wrapped, "access", "target", "role")
		if fx.wrapped.updateCalls != 0 {
			t.Fatalf("state patch update calls=%d, want zero", fx.wrapped.updateCalls)
		}
		assertSprintDefinitionRESTSilence(t, fx)
	})

	t.Run("create validation ownership preserves route and store distinctions", func(t *testing.T) {
		for _, tc := range []struct {
			name        string
			body        map[string]any
			wantMessage string
			wantReason  string
			wantTrace   []string
		}{
			{name: "missing name", body: map[string]any{"plannedStartAt": int64(1), "plannedEndAt": int64(2)}, wantMessage: "name required", wantReason: "name_required", wantTrace: []string{"access", "role"}},
			{name: "whitespace name reaches store", body: map[string]any{"name": "   ", "plannedStartAt": int64(1), "plannedEndAt": int64(2)}, wantMessage: "validation: invalid sprint name", wantReason: "invalid_sprint_name", wantTrace: []string{"access", "role", "create"}},
			{name: "reversed dates reach store", body: map[string]any{"name": "Reverse", "plannedStartAt": int64(2), "plannedEndAt": int64(1)}, wantMessage: "validation: end_at must be >= start_at", wantReason: "sprint_end_before_start", wantTrace: []string{"access", "role", "create"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				fx := newSprintDefinitionRESTFixture(t, "rest-sprint-definition-validation-"+strings.ReplaceAll(tc.name, " ", "-"))
				fx.wrapped.activate()
				var got apiErrorEnvelope
				resp, _ := doJSON(t, fx.client, http.MethodPost, fx.createURL(), tc.body, &got)
				if resp.StatusCode != http.StatusBadRequest || got.Error.Code != "VALIDATION_ERROR" || got.Error.Message != tc.wantMessage || got.Error.Details["reason"] != tc.wantReason {
					t.Fatalf("validation status=%d envelope=%+v", resp.StatusCode, got)
				}
				assertSprintDefinitionRESTTrace(t, fx.wrapped, tc.wantTrace...)
				assertSprintDefinitionRESTSilence(t, fx)
			})
		}
	})
}

func TestSprintDefinitionRESTCancellationContract(t *testing.T) {
	canceledText := context.Canceled.Error()

	for _, operation := range []string{"create", "update"} {
		for _, stage := range []string{"access", "role", "mutation"} {
			t.Run(operation+"/"+stage, func(t *testing.T) {
				fx := newSprintDefinitionRESTFixture(t, "rest-sprint-definition-cancel-"+operation+"-"+stage)
				var sp store.Sprint
				if operation == "update" {
					sp = createSprintDefinitionRESTSprint(t, fx, "Before canceled update")
				}
				switch stage {
				case "access":
					fx.wrapped.accessErr = context.Canceled
				case "role":
					fx.wrapped.roleErr = context.Canceled
				case "mutation":
					if operation == "create" {
						fx.wrapped.createErr = context.Canceled
					} else {
						fx.wrapped.updateErr = context.Canceled
					}
				}
				fx.wrapped.activate()

				var got apiErrorEnvelope
				var resp *http.Response
				if operation == "create" {
					resp, _ = doJSON(t, fx.client, http.MethodPost, fx.createURL(), map[string]any{"name": "Canceled", "plannedStartAt": int64(1), "plannedEndAt": int64(2)}, &got)
				} else {
					resp, _ = doJSON(t, fx.client, http.MethodPatch, fx.updateURL(sp.ID), map[string]any{"name": "Canceled"}, &got)
				}

				if stage == "role" {
					assertSprintDefinitionRESTError(t, resp, got, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
				} else {
					assertSprintDefinitionRESTError(t, resp, got, http.StatusInternalServerError, "INTERNAL", "internal error", &canceledText)
				}

				wantTrace := []string{"access"}
				if operation == "update" && stage != "access" {
					wantTrace = append(wantTrace, "target")
				}
				if stage == "role" || stage == "mutation" {
					wantTrace = append(wantTrace, "role")
				}
				if stage == "mutation" {
					wantTrace = append(wantTrace, operation)
				}
				assertSprintDefinitionRESTTrace(t, fx.wrapped, wantTrace...)
				assertSprintDefinitionRESTSilence(t, fx)
				if operation == "create" {
					var count int
					if err := fx.db.QueryRow(`SELECT COUNT(*) FROM sprints WHERE project_id = ?`, fx.project.ID).Scan(&count); err != nil {
						t.Fatalf("count sprints: %v", err)
					}
					if count != 0 {
						t.Fatalf("canceled create rows=%d, want zero", count)
					}
				} else if stored, err := fx.st.GetSprintByID(fx.ctx, sp.ID); err != nil || stored.Name != sp.Name {
					t.Fatalf("canceled update stored=%+v err=%v, want unchanged", stored, err)
				}
			})
		}
	}

	t.Run("update target", func(t *testing.T) {
		fx := newSprintDefinitionRESTFixture(t, "rest-sprint-definition-cancel-target")
		sp := createSprintDefinitionRESTSprint(t, fx, "Target canceled")
		fx.wrapped.targetErr = context.Canceled
		fx.wrapped.activate()
		var got apiErrorEnvelope
		resp, _ := doJSON(t, fx.client, http.MethodPatch, fx.updateURL(sp.ID), map[string]any{"name": "Never"}, &got)
		assertSprintDefinitionRESTError(t, resp, got, http.StatusInternalServerError, "INTERNAL", "internal error", &canceledText)
		assertSprintDefinitionRESTTrace(t, fx.wrapped, "access", "target")
		assertSprintDefinitionRESTSilence(t, fx)
	})
}

func TestSprintDefinitionRESTCommittedCreateFailureContract(t *testing.T) {
	fx := newSprintDefinitionRESTFixture(t, "rest-sprint-definition-committed-failure")
	const corruptValue = "rest-create-return-read-failed"
	if _, err := fx.db.Exec(`
		CREATE TRIGGER sprint_definition_rest_corrupt_created_row
		AFTER INSERT ON sprints
		BEGIN
			UPDATE sprints SET planned_start_at = '` + corruptValue + `' WHERE id = NEW.id;
		END
	`); err != nil {
		t.Fatalf("create post-insert fault trigger: %v", err)
	}
	fx.wrapped.activate()

	var got apiErrorEnvelope
	resp, _ := doJSON(t, fx.client, http.MethodPost, fx.createURL(), map[string]any{
		"name": "Committed REST failure", "plannedStartAt": int64(1000), "plannedEndAt": int64(2000),
	}, &got)
	if resp.StatusCode != http.StatusInternalServerError || got.Error.Code != "INTERNAL" || got.Error.Message != "internal error" {
		t.Fatalf("committed create response status=%d envelope=%+v", resp.StatusCode, got)
	}
	detail, _ := got.Error.Details["detail"].(string)
	if !strings.Contains(detail, "get sprint") || !strings.Contains(detail, corruptValue) {
		t.Fatalf("committed create public detail=%q, want current return-read diagnostic", detail)
	}
	assertSprintDefinitionRESTTrace(t, fx.wrapped, "access", "role", "create")
	if fx.wrapped.createCalls != 1 {
		t.Fatalf("create calls=%d, want one with no retry", fx.wrapped.createCalls)
	}
	var count int
	if err := fx.db.QueryRow(`SELECT COUNT(*) FROM sprints WHERE project_id = ? AND name = ?`, fx.project.ID, "Committed REST failure").Scan(&count); err != nil {
		t.Fatalf("count committed sprint: %v", err)
	}
	if count != 1 {
		t.Fatalf("committed sprint rows=%d, want one", count)
	}
	assertSprintDefinitionRESTSilence(t, fx)
}

func TestSprintDefinitionRESTRoleCancellationMatchesInsufficientRole(t *testing.T) {
	for _, roleResult := range []string{"canceled", "viewer"} {
		t.Run(roleResult, func(t *testing.T) {
			fx := newSprintDefinitionRESTFixture(t, "rest-sprint-definition-role-"+roleResult)
			if roleResult == "canceled" {
				fx.wrapped.roleErr = context.Canceled
			} else {
				viewer, err := fx.st.CreateUser(context.Background(), "rest-role-collapse-viewer@example.com", "password123", "Viewer")
				if err != nil {
					t.Fatalf("CreateUser: %v", err)
				}
				if err := fx.st.AddProjectMember(fx.ctx, fx.ownerID, fx.project.ID, viewer.ID, store.RoleViewer); err != nil {
					t.Fatalf("AddProjectMember: %v", err)
				}
				fx.client = sprintDefinitionRESTSessionClient(t, fx, viewer.ID)
			}
			fx.wrapped.activate()
			var got apiErrorEnvelope
			resp, _ := doJSON(t, fx.client, http.MethodPost, fx.createURL(), map[string]any{"name": "Denied", "plannedStartAt": int64(1), "plannedEndAt": int64(2)}, &got)
			assertSprintDefinitionRESTError(t, resp, got, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
			assertSprintDefinitionRESTTrace(t, fx.wrapped, "access", "role")
			assertSprintDefinitionRESTSilence(t, fx)
		})
	}
}

func TestSprintDefinitionRESTDefinitionFieldsRespectExistingSprintState(t *testing.T) {
	start := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name        string
		state       string
		body        map[string]any
		wantStatus  int
		wantName    string
		wantMessage string
		wantReason  string
	}{
		{name: "planned name", state: store.SprintStatePlanned, body: map[string]any{"name": "Planned renamed"}, wantStatus: http.StatusNoContent, wantName: "Planned renamed"},
		{name: "active end", state: store.SprintStateActive, body: map[string]any{"plannedEndAt": start.Add(9 * 24 * time.Hour).UnixMilli()}, wantStatus: http.StatusNoContent, wantName: "State fixture"},
		{name: "active name rejected", state: store.SprintStateActive, body: map[string]any{"name": "No active rename"}, wantStatus: http.StatusBadRequest, wantName: "State fixture", wantMessage: "validation: only endAt can be updated for ACTIVE sprint", wantReason: "active_sprint_only_end_at"},
		{name: "closed name", state: store.SprintStateClosed, body: map[string]any{"name": "Closed renamed"}, wantStatus: http.StatusNoContent, wantName: "Closed renamed"},
		{name: "closed end rejected", state: store.SprintStateClosed, body: map[string]any{"plannedEndAt": start.Add(10 * 24 * time.Hour).UnixMilli()}, wantStatus: http.StatusBadRequest, wantName: "State fixture", wantMessage: "validation: dates cannot be updated for CLOSED sprint", wantReason: "closed_sprint_dates_locked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newSprintDefinitionRESTFixture(t, "rest-sprint-definition-state-"+strings.ReplaceAll(tc.name, " ", "-"))
			sp, err := fx.st.CreateSprint(fx.ctx, fx.project.ID, "State fixture", start, start.Add(7*24*time.Hour))
			if err != nil {
				t.Fatalf("CreateSprint: %v", err)
			}
			if tc.state != store.SprintStatePlanned {
				var started any
				var closed any
				if tc.state == store.SprintStateActive {
					started = start.UnixMilli()
				} else {
					started = start.UnixMilli()
					closed = start.Add(24 * time.Hour).UnixMilli()
				}
				if _, err := fx.db.Exec(`UPDATE sprints SET state = ?, started_at = ?, closed_at = ? WHERE id = ?`, tc.state, started, closed, sp.ID); err != nil {
					t.Fatalf("set state fixture: %v", err)
				}
			}
			fx.wrapped.activate()
			var got apiErrorEnvelope
			resp, _ := doJSON(t, fx.client, http.MethodPatch, fx.updateURL(sp.ID), tc.body, &got)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("state update status=%d envelope=%+v, want %d", resp.StatusCode, got, tc.wantStatus)
			}
			stored, err := fx.st.GetSprintByID(fx.ctx, sp.ID)
			if err != nil {
				t.Fatalf("GetSprintByID: %v", err)
			}
			if stored.Name != tc.wantName || stored.State != tc.state {
				t.Fatalf("stored sprint=%+v, want name=%q state=%q", stored, tc.wantName, tc.state)
			}
			if tc.wantStatus == http.StatusNoContent {
				assertSprintDefinitionRESTRefresh(t, fx, "sprint_updated")
			} else {
				if got.Error.Code != "VALIDATION_ERROR" || got.Error.Message != tc.wantMessage || got.Error.Details["reason"] != tc.wantReason {
					t.Fatalf("state validation envelope=%+v, want message=%q reason=%q", got, tc.wantMessage, tc.wantReason)
				}
				assertSprintDefinitionRESTSilence(t, fx)
			}
			assertSprintDefinitionRESTTrace(t, fx.wrapped, "access", "target", "role", "update")
		})
	}
}

func TestSprintDefinitionRESTRejectsDisabledProjectWithoutSideEffects(t *testing.T) {
	fx := newSprintDefinitionRESTFixture(t, "rest-sprint-definition-disabled")
	sp := createSprintDefinitionRESTSprint(t, fx, "Dormant")
	if err := fx.st.UpdateProjectSprintsEnabled(fx.ctx, fx.project.ID, fx.ownerID, false); err != nil {
		t.Fatalf("disable sprints: %v", err)
	}

	for _, tc := range []struct {
		name   string
		method string
		url    string
		body   map[string]any
	}{
		{
			name: "create", method: http.MethodPost, url: fx.createURL(),
			body: map[string]any{"name": "Blocked", "plannedStartAt": time.Now().UnixMilli(), "plannedEndAt": time.Now().Add(time.Hour).UnixMilli()},
		},
		{
			name: "update", method: http.MethodPatch, url: fx.updateURL(sp.ID),
			body: map[string]any{"name": "Blocked rename"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx.wrapped.activate()
			beforeEvents := len(fx.collector.snapshot())
			var got apiErrorEnvelope
			resp, _ := doJSON(t, fx.client, tc.method, tc.url, tc.body, &got)
			if resp.StatusCode != http.StatusBadRequest || got.Error.Code != "VALIDATION_ERROR" ||
				got.Error.Message != store.ErrSprintsDisabled.Error() || got.Error.Details["reason"] != "sprints_disabled" {
				t.Fatalf("disabled %s status=%d envelope=%+v", tc.name, resp.StatusCode, got)
			}
			if len(fx.collector.snapshot()) != beforeEvents {
				t.Fatalf("disabled %s published events", tc.name)
			}
		})
	}

	stored, err := fx.st.GetSprintByID(fx.ctx, sp.ID)
	if err != nil {
		t.Fatalf("GetSprintByID: %v", err)
	}
	if stored.Name != "Dormant" {
		t.Fatalf("disabled update persisted: %+v", stored)
	}
}
