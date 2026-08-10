package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

type sprintLifecycleRESTEventCollector struct {
	mu     sync.Mutex
	events []eventbus.Event
}

func (c *sprintLifecycleRESTEventCollector) OnEvent(_ context.Context, event eventbus.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
}

func (c *sprintLifecycleRESTEventCollector) snapshot() []eventbus.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]eventbus.Event(nil), c.events...)
}

type sprintLifecycleRESTStore struct {
	*store.Store

	mu     sync.Mutex
	active bool
	trace  []string

	accessErr      error
	accessOverride *store.ProjectContext
	roleErr        error
	roleOverride   *store.ProjectRole
	targetErr      error
	activateErr    error
	closeErr       error
	deleteErr      error

	accessSlug string
	accessMode store.Mode
	rolePID    int64
	roleUID    int64

	targetReads int
	targetIDs   []int64

	activateCalls int
	activatePID   int64
	activateID    int64
	closeCalls    int
	closePID      int64
	closeID       int64
	deleteCalls   int
	deletePID     int64
	deleteID      int64
}

func (s *sprintLifecycleRESTStore) activateTrace() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = true
	s.trace = nil
	s.targetReads = 0
	s.targetIDs = nil
	s.activateCalls = 0
	s.closeCalls = 0
	s.deleteCalls = 0
}

func (s *sprintLifecycleRESTStore) record(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return false
	}
	s.trace = append(s.trace, name)
	return true
}

func (s *sprintLifecycleRESTStore) snapshotTrace() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.trace...)
}

func (s *sprintLifecycleRESTStore) GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error) {
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

func (s *sprintLifecycleRESTStore) GetProjectRole(ctx context.Context, projectID, userID int64) (store.ProjectRole, error) {
	if !s.record("role") {
		return s.Store.GetProjectRole(ctx, projectID, userID)
	}
	s.mu.Lock()
	s.rolePID = projectID
	s.roleUID = userID
	err := s.roleErr
	override := s.roleOverride
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	if override != nil {
		return *override, nil
	}
	return s.Store.GetProjectRole(ctx, projectID, userID)
}

func (s *sprintLifecycleRESTStore) GetSprintByID(ctx context.Context, sprintID int64) (store.Sprint, error) {
	if !s.record("target") {
		return s.Store.GetSprintByID(ctx, sprintID)
	}
	s.mu.Lock()
	s.targetReads++
	s.targetIDs = append(s.targetIDs, sprintID)
	err := s.targetErr
	s.mu.Unlock()
	if err != nil {
		return store.Sprint{}, err
	}
	return s.Store.GetSprintByID(ctx, sprintID)
}

func (s *sprintLifecycleRESTStore) ActivateSprint(ctx context.Context, projectID, sprintID int64) error {
	if !s.record("activate") {
		return s.Store.ActivateSprint(ctx, projectID, sprintID)
	}
	s.mu.Lock()
	s.activateCalls++
	s.activatePID = projectID
	s.activateID = sprintID
	err := s.activateErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.Store.ActivateSprint(ctx, projectID, sprintID)
}

func (s *sprintLifecycleRESTStore) CloseSprint(ctx context.Context, projectID, sprintID int64) error {
	if !s.record("close") {
		return s.Store.CloseSprint(ctx, projectID, sprintID)
	}
	s.mu.Lock()
	s.closeCalls++
	s.closePID = projectID
	s.closeID = sprintID
	err := s.closeErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.Store.CloseSprint(ctx, projectID, sprintID)
}

func (s *sprintLifecycleRESTStore) DeleteSprint(ctx context.Context, projectID, sprintID int64) error {
	if !s.record("delete") {
		return s.Store.DeleteSprint(ctx, projectID, sprintID)
	}
	s.mu.Lock()
	s.deleteCalls++
	s.deletePID = projectID
	s.deleteID = sprintID
	err := s.deleteErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.Store.DeleteSprint(ctx, projectID, sprintID)
}

type sprintLifecycleRESTFixture struct {
	ts          *httptest.Server
	db          *sql.DB
	st          *store.Store
	wrapped     *sprintLifecycleRESTStore
	client      *http.Client
	ownerClient *http.Client
	ownerID     int64
	actorID     int64
	ownerCtx    context.Context
	actorCtx    context.Context
	project     store.Project
	projectPC   store.ProjectContext
	collector   *sprintLifecycleRESTEventCollector
}

func newSprintLifecycleRESTFixture(t *testing.T, name string) *sprintLifecycleRESTFixture {
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
	wrapped := &sprintLifecycleRESTStore{Store: st}
	srv := NewServer(wrapped, Options{MaxRequestBody: 1 << 20, ScrumboyMode: "full"})
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		_ = sqlDB.Close()
	})

	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Lifecycle Owner", name+"-owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	ownerCtx := store.WithUserID(context.Background(), ownerID)
	actor, err := st.CreateUser(context.Background(), name+"-actor@example.com", "password123", "Lifecycle Maintainer")
	if err != nil {
		t.Fatalf("CreateUser actor: %v", err)
	}

	offsetOne, err := st.CreateProject(ownerCtx, name+"-offset-one")
	if err != nil {
		t.Fatalf("CreateProject offset one: %v", err)
	}
	if _, err := st.CreateProject(ownerCtx, name+"-offset-two"); err != nil {
		t.Fatalf("CreateProject offset two: %v", err)
	}
	now := time.Now().UTC()
	for i := 0; i < 4; i++ {
		if _, err := st.CreateSprint(ownerCtx, offsetOne.ID, fmt.Sprintf("Offset %d", i+1), now.Add(-time.Hour), now.Add(time.Duration(48+i)*time.Hour)); err != nil {
			t.Fatalf("CreateSprint offset %d: %v", i+1, err)
		}
	}
	project, err := st.CreateProject(ownerCtx, name)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := st.AddProjectMember(ownerCtx, ownerID, project.ID, actor.ID, store.RoleMaintainer); err != nil {
		t.Fatalf("AddProjectMember actor: %v", err)
	}
	actorCtx := store.WithUserID(context.Background(), actor.ID)
	pc, err := st.GetProjectContextBySlug(actorCtx, project.Slug, store.ModeFull)
	if err != nil {
		t.Fatalf("GetProjectContextBySlug: %v", err)
	}
	client := sprintLifecycleRESTSessionClient(t, ts, st, actor.ID)
	collector := &sprintLifecycleRESTEventCollector{}
	srv.fanout = eventbus.NewFanout(collector)

	return &sprintLifecycleRESTFixture{
		ts: ts, db: sqlDB, st: st, wrapped: wrapped, client: client, ownerClient: ownerClient,
		ownerID: ownerID, actorID: actor.ID, ownerCtx: ownerCtx, actorCtx: actorCtx,
		project: project, projectPC: pc, collector: collector,
	}
}

func sprintLifecycleRESTSessionClient(t *testing.T, ts *httptest.Server, st *store.Store, userID int64) *http.Client {
	t.Helper()
	token, expiresAt, err := st.CreateSession(context.Background(), userID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	base, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	jar.SetCookies(base, []*http.Cookie{{Name: "scrumboy_session", Value: token, Path: "/", Expires: expiresAt}})
	return &http.Client{Transport: ts.Client().Transport, Jar: jar}
}

func (fx *sprintLifecycleRESTFixture) actionURL(sprintID int64, action string) string {
	return fmt.Sprintf("%s/api/board/%s/sprints/%d/%s", fx.ts.URL, fx.project.Slug, sprintID, action)
}

func (fx *sprintLifecycleRESTFixture) deleteURL(sprintID int64) string {
	return fmt.Sprintf("%s/api/board/%s/sprints/%d", fx.ts.URL, fx.project.Slug, sprintID)
}

func createSprintLifecycleRESTSprint(t *testing.T, fx *sprintLifecycleRESTFixture, name, state string) store.Sprint {
	t.Helper()
	now := time.Now().UTC()
	sp, err := fx.st.CreateSprint(fx.ownerCtx, fx.project.ID, name, now.Add(-time.Hour), now.Add(72*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint %s: %v", state, err)
	}
	if state == store.SprintStateActive || state == store.SprintStateClosed {
		if err := fx.st.ActivateSprint(fx.ownerCtx, fx.project.ID, sp.ID); err != nil {
			t.Fatalf("ActivateSprint setup: %v", err)
		}
	}
	if state == store.SprintStateClosed {
		if err := fx.st.CloseSprint(fx.ownerCtx, fx.project.ID, sp.ID); err != nil {
			t.Fatalf("CloseSprint setup: %v", err)
		}
	}
	return sp
}

func assertSprintLifecycleRESTTrace(t *testing.T, wrapped *sprintLifecycleRESTStore, want ...string) {
	t.Helper()
	if got := wrapped.snapshotTrace(); !reflect.DeepEqual(got, want) {
		t.Fatalf("store trace=%v, want %v", got, want)
	}
}

func assertSprintLifecycleRESTError(t *testing.T, resp *http.Response, got apiErrorEnvelope, wantStatus int, wantCode, wantMessage string, wantDetails map[string]any) {
	t.Helper()
	if resp.StatusCode != wantStatus || got.Error.Code != wantCode || got.Error.Message != wantMessage {
		t.Fatalf("error status=%d envelope=%+v, want status=%d code=%q message=%q", resp.StatusCode, got, wantStatus, wantCode, wantMessage)
	}
	if !reflect.DeepEqual(got.Error.Details, wantDetails) {
		t.Fatalf("error details=%+v, want %+v", got.Error.Details, wantDetails)
	}
}

func assertSprintLifecycleRESTRefresh(t *testing.T, fx *sprintLifecycleRESTFixture, projectID int64, reason string) {
	t.Helper()
	events := fx.collector.snapshot()
	if len(events) != 1 {
		t.Fatalf("events=%+v, want exactly one refresh", events)
	}
	event := events[0]
	if event.Type != "board.refresh_needed" || event.ProjectID != projectID {
		t.Fatalf("event=%+v, want board.refresh_needed for project %d", event, projectID)
	}
	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("decode refresh payload: %v", err)
	}
	if payload["reason"] != reason || int64(payload["actorUserId"].(float64)) != fx.actorID {
		t.Fatalf("refresh payload=%+v, want reason=%q actor=%d", payload, reason, fx.actorID)
	}
}

func assertSprintLifecycleRESTSilence(t *testing.T, fx *sprintLifecycleRESTFixture) {
	t.Helper()
	if events := fx.collector.snapshot(); len(events) != 0 {
		t.Fatalf("unexpected lifecycle refresh events: %+v", events)
	}
}

func getSprintLifecycleRESTSprint(t *testing.T, fx *sprintLifecycleRESTFixture, sprintID int64) store.Sprint {
	t.Helper()
	sp, err := fx.st.GetSprintByID(fx.ownerCtx, sprintID)
	if err != nil {
		t.Fatalf("GetSprintByID(%d): %v", sprintID, err)
	}
	return sp
}

func TestSprintLifecycleRESTActivateContract(t *testing.T) {
	t.Run("planned future success", func(t *testing.T) {
		fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-activate")
		sp := createSprintLifecycleRESTSprint(t, fx, "Target", store.SprintStatePlanned)
		if sp.ID == sp.Number || sp.ID == fx.project.ID || fx.project.ID == fx.actorID {
			t.Fatalf("identity fixture is not distinct: actor=%d project=%d sprint=%d number=%d", fx.actorID, fx.project.ID, sp.ID, sp.Number)
		}
		fx.wrapped.activateTrace()

		resp, body := doJSON(t, fx.client, http.MethodPost, fx.actionURL(sp.ID, "activate"), map[string]any{}, nil)
		if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
			t.Fatalf("activate status=%d body=%q, want 204 empty", resp.StatusCode, body)
		}
		assertSprintLifecycleRESTTrace(t, fx.wrapped, "access", "role", "activate")
		if fx.wrapped.accessSlug != fx.project.Slug || fx.wrapped.accessMode != store.ModeFull || fx.wrapped.rolePID != fx.project.ID || fx.wrapped.roleUID != fx.actorID {
			t.Fatalf("access/role args=(slug=%q mode=%q project=%d actor=%d)", fx.wrapped.accessSlug, fx.wrapped.accessMode, fx.wrapped.rolePID, fx.wrapped.roleUID)
		}
		if fx.wrapped.targetReads != 0 || fx.wrapped.activateCalls != 1 || fx.wrapped.activatePID != fx.project.ID || fx.wrapped.activateID != sp.ID {
			t.Fatalf("activate observations=(targetReads=%d calls=%d project=%d sprint=%d)", fx.wrapped.targetReads, fx.wrapped.activateCalls, fx.wrapped.activatePID, fx.wrapped.activateID)
		}
		stored := getSprintLifecycleRESTSprint(t, fx, sp.ID)
		if stored.State != store.SprintStateActive || stored.StartedAt == nil || stored.ClosedAt != nil {
			t.Fatalf("activated sprint=%+v", stored)
		}
		assertSprintLifecycleRESTRefresh(t, fx, fx.project.ID, "sprint_activated")
	})

	t.Run("replacement closes prior active and publishes only activation", func(t *testing.T) {
		fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-activate-replacement")
		prior := createSprintLifecycleRESTSprint(t, fx, "Prior", store.SprintStateActive)
		target := createSprintLifecycleRESTSprint(t, fx, "Replacement", store.SprintStatePlanned)
		fx.wrapped.activateTrace()

		resp, body := doJSON(t, fx.client, http.MethodPost, fx.actionURL(target.ID, "activate"), map[string]any{}, nil)
		if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
			t.Fatalf("replacement status=%d body=%q", resp.StatusCode, body)
		}
		assertSprintLifecycleRESTTrace(t, fx.wrapped, "access", "role", "activate")
		if fx.wrapped.activateCalls != 1 || fx.wrapped.closeCalls != 0 {
			t.Fatalf("replacement activateCalls=%d closeCalls=%d", fx.wrapped.activateCalls, fx.wrapped.closeCalls)
		}
		priorAfter := getSprintLifecycleRESTSprint(t, fx, prior.ID)
		targetAfter := getSprintLifecycleRESTSprint(t, fx, target.ID)
		if priorAfter.State != store.SprintStateClosed || priorAfter.ClosedAt == nil || targetAfter.State != store.SprintStateActive || targetAfter.StartedAt == nil {
			t.Fatalf("replacement prior=%+v target=%+v", priorAfter, targetAfter)
		}
		active, err := fx.st.GetActiveSprintByProjectID(fx.ownerCtx, fx.project.ID)
		if err != nil || active == nil || active.ID != target.ID {
			t.Fatalf("active sprint=%+v err=%v, want target %d", active, err, target.ID)
		}
		assertSprintLifecycleRESTRefresh(t, fx, fx.project.ID, "sprint_activated")
	})

	t.Run("repeated activation remains idempotent success with another refresh", func(t *testing.T) {
		fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-activate-repeat")
		sp := createSprintLifecycleRESTSprint(t, fx, "Already active", store.SprintStateActive)
		before := getSprintLifecycleRESTSprint(t, fx, sp.ID)
		fx.wrapped.activateTrace()

		resp, body := doJSON(t, fx.client, http.MethodPost, fx.actionURL(sp.ID, "activate"), map[string]any{}, nil)
		if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
			t.Fatalf("repeated activate status=%d body=%q", resp.StatusCode, body)
		}
		assertSprintLifecycleRESTTrace(t, fx.wrapped, "access", "role", "activate")
		if fx.wrapped.targetReads != 0 || fx.wrapped.activateCalls != 1 {
			t.Fatalf("repeated activate targetReads=%d calls=%d", fx.wrapped.targetReads, fx.wrapped.activateCalls)
		}
		after := getSprintLifecycleRESTSprint(t, fx, sp.ID)
		if before.StartedAt == nil || after.StartedAt == nil || !after.StartedAt.Equal(*before.StartedAt) || !after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Fatalf("repeated activation changed timestamps before=%+v after=%+v", before, after)
		}
		assertSprintLifecycleRESTRefresh(t, fx, fx.project.ID, "sprint_activated")
	})

	for _, tc := range []struct {
		name        string
		prepare     func(*testing.T, *sprintLifecycleRESTFixture) int64
		wantStatus  int
		wantCode    string
		wantMessage string
		wantDetails map[string]any
	}{
		{
			name: "closed target",
			prepare: func(t *testing.T, fx *sprintLifecycleRESTFixture) int64 {
				return createSprintLifecycleRESTSprint(t, fx, "Closed", store.SprintStateClosed).ID
			},
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "validation: sprint must be PLANNED to activate", wantDetails: map[string]any{"reason": "sprint_activate_requires_planned"},
		},
		{
			name:       "missing target",
			prepare:    func(_ *testing.T, _ *sprintLifecycleRESTFixture) int64 { return 900001 },
			wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMessage: "not found",
		},
		{
			name: "foreign target exposes store validation",
			prepare: func(t *testing.T, fx *sprintLifecycleRESTFixture) int64 {
				other, err := fx.st.CreateProject(fx.ownerCtx, "rest-lifecycle-activate-foreign")
				if err != nil {
					t.Fatalf("CreateProject foreign: %v", err)
				}
				now := time.Now().UTC()
				sp, err := fx.st.CreateSprint(fx.ownerCtx, other.ID, "Foreign", now.Add(-time.Hour), now.Add(48*time.Hour))
				if err != nil {
					t.Fatalf("CreateSprint foreign: %v", err)
				}
				return sp.ID
			},
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "validation: sprint does not belong to project", wantDetails: map[string]any{"reason": "sprint_not_in_project"},
		},
		{
			name: "safely expired planned end",
			prepare: func(t *testing.T, fx *sprintLifecycleRESTFixture) int64 {
				now := time.Now().UTC()
				sp, err := fx.st.CreateSprint(fx.ownerCtx, fx.project.ID, "Past", now.Add(-48*time.Hour), now.Add(-24*time.Hour))
				if err != nil {
					t.Fatalf("CreateSprint past: %v", err)
				}
				return sp.ID
			},
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "validation: sprint end date is on or before now; cannot activate", wantDetails: map[string]any{"reason": "sprint_end_in_past"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-activate-reject-"+strings.ReplaceAll(tc.name, " ", "-"))
			sprintID := tc.prepare(t, fx)
			before, beforeErr := fx.st.GetSprintByID(fx.ownerCtx, sprintID)
			fx.wrapped.activateTrace()
			var got apiErrorEnvelope
			resp, _ := doJSON(t, fx.client, http.MethodPost, fx.actionURL(sprintID, "activate"), map[string]any{}, &got)
			assertSprintLifecycleRESTError(t, resp, got, tc.wantStatus, tc.wantCode, tc.wantMessage, tc.wantDetails)
			assertSprintLifecycleRESTTrace(t, fx.wrapped, "access", "role", "activate")
			if fx.wrapped.activateCalls != 1 || fx.wrapped.targetReads != 0 {
				t.Fatalf("rejection activateCalls=%d targetReads=%d", fx.wrapped.activateCalls, fx.wrapped.targetReads)
			}
			if beforeErr == nil {
				after := getSprintLifecycleRESTSprint(t, fx, sprintID)
				if !reflect.DeepEqual(after, before) {
					t.Fatalf("rejected activation changed sprint before=%+v after=%+v", before, after)
				}
			}
			assertSprintLifecycleRESTSilence(t, fx)
		})
	}
}

func TestSprintLifecycleRESTCloseContract(t *testing.T) {
	t.Run("same-project active success", func(t *testing.T) {
		fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-close")
		sp := createSprintLifecycleRESTSprint(t, fx, "Active", store.SprintStateActive)
		before := getSprintLifecycleRESTSprint(t, fx, sp.ID)
		fx.wrapped.activateTrace()

		resp, body := doJSON(t, fx.client, http.MethodPost, fx.actionURL(sp.ID, "close"), map[string]any{}, nil)
		if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
			t.Fatalf("close status=%d body=%q", resp.StatusCode, body)
		}
		assertSprintLifecycleRESTTrace(t, fx.wrapped, "access", "role", "target", "close")
		if fx.wrapped.targetReads != 1 || !reflect.DeepEqual(fx.wrapped.targetIDs, []int64{sp.ID}) || fx.wrapped.closeCalls != 1 || fx.wrapped.closePID != fx.project.ID || fx.wrapped.closeID != sp.ID {
			t.Fatalf("close observations=(targetReads=%d ids=%v calls=%d project=%d sprint=%d)", fx.wrapped.targetReads, fx.wrapped.targetIDs, fx.wrapped.closeCalls, fx.wrapped.closePID, fx.wrapped.closeID)
		}
		after := getSprintLifecycleRESTSprint(t, fx, sp.ID)
		if after.State != store.SprintStateClosed || after.ClosedAt == nil || before.StartedAt == nil || after.StartedAt == nil || !after.StartedAt.Equal(*before.StartedAt) {
			t.Fatalf("closed sprint before=%+v after=%+v", before, after)
		}
		assertSprintLifecycleRESTRefresh(t, fx, fx.project.ID, "sprint_closed")
	})

	for _, state := range []string{store.SprintStatePlanned, store.SprintStateClosed} {
		state := state
		t.Run(strings.ToLower(state), func(t *testing.T) {
			fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-close-"+strings.ToLower(state))
			sp := createSprintLifecycleRESTSprint(t, fx, state, state)
			before := getSprintLifecycleRESTSprint(t, fx, sp.ID)
			fx.wrapped.activateTrace()
			var got apiErrorEnvelope
			resp, _ := doJSON(t, fx.client, http.MethodPost, fx.actionURL(sp.ID, "close"), map[string]any{}, &got)
			assertSprintLifecycleRESTError(t, resp, got, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			assertSprintLifecycleRESTTrace(t, fx.wrapped, "access", "role", "target", "close")
			if fx.wrapped.targetReads != 1 || !reflect.DeepEqual(fx.wrapped.targetIDs, []int64{sp.ID}) || fx.wrapped.closeCalls != 1 || fx.wrapped.closePID != fx.project.ID || fx.wrapped.closeID != sp.ID || !reflect.DeepEqual(getSprintLifecycleRESTSprint(t, fx, sp.ID), before) {
				t.Fatalf("failed close observations=(reads=%d ids=%v calls=%d project=%d sprint=%d) or changed state", fx.wrapped.targetReads, fx.wrapped.targetIDs, fx.wrapped.closeCalls, fx.wrapped.closePID, fx.wrapped.closeID)
			}
			assertSprintLifecycleRESTSilence(t, fx)
		})
	}

	t.Run("missing", func(t *testing.T) {
		fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-close-missing")
		fx.wrapped.activateTrace()
		var got apiErrorEnvelope
		resp, _ := doJSON(t, fx.client, http.MethodPost, fx.actionURL(900002, "close"), map[string]any{}, &got)
		assertSprintLifecycleRESTError(t, resp, got, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		assertSprintLifecycleRESTTrace(t, fx.wrapped, "access", "role", "target")
		if fx.wrapped.targetReads != 1 || !reflect.DeepEqual(fx.wrapped.targetIDs, []int64{900002}) || fx.wrapped.closeCalls != 0 {
			t.Fatalf("missing close observations=(reads=%d ids=%v calls=%d)", fx.wrapped.targetReads, fx.wrapped.targetIDs, fx.wrapped.closeCalls)
		}
		assertSprintLifecycleRESTSilence(t, fx)
	})
}

func TestSprintLifecycleRESTCloseRejectsForeignProjectTarget(t *testing.T) {
	fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-close-foreign-route-a")
	projectB, err := fx.st.CreateProject(fx.ownerCtx, "rest-lifecycle-close-foreign-project-b")
	if err != nil {
		t.Fatalf("CreateProject B: %v", err)
	}
	now := time.Now().UTC()
	foreign, err := fx.st.CreateSprint(fx.ownerCtx, projectB.ID, "Foreign active", now.Add(-time.Hour), now.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint B: %v", err)
	}
	if err := fx.st.ActivateSprint(fx.ownerCtx, projectB.ID, foreign.ID); err != nil {
		t.Fatalf("ActivateSprint B: %v", err)
	}
	if role, err := fx.st.GetProjectRole(fx.actorCtx, projectB.ID, fx.actorID); err != nil || role.HasMinimumRole(store.RoleMaintainer) {
		t.Fatalf("actor unexpectedly authorized for project B: role=%q err=%v", role, err)
	}
	identities := []int64{fx.project.ID, projectB.ID, foreign.ID, foreign.Number, fx.actorID}
	seen := map[int64]bool{}
	for _, identity := range identities {
		if seen[identity] {
			t.Fatalf("exploit fixture identities not distinct: routeProject=%d foreignProject=%d sprintID=%d sprintNumber=%d actor=%d", fx.project.ID, projectB.ID, foreign.ID, foreign.Number, fx.actorID)
		}
		seen[identity] = true
	}
	fx.wrapped.activateTrace()

	var got apiErrorEnvelope
	resp, _ := doJSON(t, fx.client, http.MethodPost, fx.actionURL(foreign.ID, "close"), map[string]any{}, &got)
	assertSprintLifecycleRESTError(t, resp, got, http.StatusNotFound, "NOT_FOUND", "not found", nil)
	assertSprintLifecycleRESTTrace(t, fx.wrapped, "access", "role", "target")
	if fx.wrapped.rolePID != fx.project.ID || fx.wrapped.roleUID != fx.actorID || fx.wrapped.targetReads != 1 || !reflect.DeepEqual(fx.wrapped.targetIDs, []int64{foreign.ID}) || fx.wrapped.closeCalls != 0 {
		t.Fatalf("cross-project observations=(roleProject=%d actor=%d reads=%d ids=%v calls=%d)", fx.wrapped.rolePID, fx.wrapped.roleUID, fx.wrapped.targetReads, fx.wrapped.targetIDs, fx.wrapped.closeCalls)
	}
	if after := getSprintLifecycleRESTSprint(t, fx, foreign.ID); after.ProjectID != projectB.ID || after.State != store.SprintStateActive || after.ClosedAt != nil {
		t.Fatalf("foreign sprint changed after rejection=%+v", after)
	}
	assertSprintLifecycleRESTSilence(t, fx)
	for _, event := range fx.collector.snapshot() {
		if event.ProjectID == fx.project.ID || event.ProjectID == projectB.ID {
			t.Fatalf("unexpected refresh after foreign close rejection: %+v", event)
		}
	}
}

func TestSprintLifecycleRESTDeleteContract(t *testing.T) {
	for _, state := range []string{store.SprintStatePlanned, store.SprintStateActive, store.SprintStateClosed} {
		state := state
		t.Run(strings.ToLower(state), func(t *testing.T) {
			fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-delete-"+strings.ToLower(state))
			sp := createSprintLifecycleRESTSprint(t, fx, state, state)
			fx.wrapped.activateTrace()

			resp, body := doJSON(t, fx.client, http.MethodDelete, fx.deleteURL(sp.ID), map[string]any{}, nil)
			if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
				t.Fatalf("delete %s status=%d body=%q", state, resp.StatusCode, body)
			}
			assertSprintLifecycleRESTTrace(t, fx.wrapped, "access", "target", "role", "delete")
			if fx.wrapped.targetReads != 1 || !reflect.DeepEqual(fx.wrapped.targetIDs, []int64{sp.ID}) || fx.wrapped.deleteCalls != 1 || fx.wrapped.deletePID != fx.project.ID || fx.wrapped.deleteID != sp.ID {
				t.Fatalf("delete observations=(reads=%d ids=%v calls=%d project=%d sprint=%d)", fx.wrapped.targetReads, fx.wrapped.targetIDs, fx.wrapped.deleteCalls, fx.wrapped.deletePID, fx.wrapped.deleteID)
			}
			if _, err := fx.st.GetSprintByID(fx.ownerCtx, sp.ID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("GetSprintByID after delete error=%v, want not found", err)
			}
			if state == store.SprintStateActive {
				active, err := fx.st.GetActiveSprintByProjectID(fx.ownerCtx, fx.project.ID)
				if err != nil || active != nil {
					t.Fatalf("active sprint after active delete=%+v err=%v, want nil", active, err)
				}
			}
			assertSprintLifecycleRESTRefresh(t, fx, fx.project.ID, "sprint_deleted")
		})
	}
}

func TestSprintLifecycleRESTDeleteDetachesTodos(t *testing.T) {
	fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-delete-detach")
	target := createSprintLifecycleRESTSprint(t, fx, "Target", store.SprintStateActive)
	controlSprint := createSprintLifecycleRESTSprint(t, fx, "Control", store.SprintStatePlanned)
	points := int64(8)
	assignedOne, err := fx.st.CreateTodo(fx.ownerCtx, fx.project.ID, store.CreateTodoInput{
		Title: "Assigned one", Body: "body one", Tags: []string{"contract", "phase20"}, ColumnKey: store.DefaultColumnDoing,
		EstimationPoints: &points, AssigneeUserID: &fx.actorID, SprintID: &target.ID,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo assigned one: %v", err)
	}
	assignedTwo, err := fx.st.CreateTodo(fx.ownerCtx, fx.project.ID, store.CreateTodoInput{
		Title: "Assigned two", Body: "body two", ColumnKey: store.DefaultColumnBacklog, SprintID: &target.ID,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo assigned two: %v", err)
	}
	controlAssigned, err := fx.st.CreateTodo(fx.ownerCtx, fx.project.ID, store.CreateTodoInput{
		Title: "Control scheduled", Body: "control", ColumnKey: store.DefaultColumnBacklog, SprintID: &controlSprint.ID,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo control scheduled: %v", err)
	}
	controlBacklog, err := fx.st.CreateTodo(fx.ownerCtx, fx.project.ID, store.CreateTodoInput{
		Title: "Control backlog", Body: "backlog", ColumnKey: store.DefaultColumnBacklog,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo control backlog: %v", err)
	}
	fx.wrapped.activateTrace()

	resp, body := doJSON(t, fx.client, http.MethodDelete, fx.deleteURL(target.ID), map[string]any{}, nil)
	if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
		t.Fatalf("delete with todos status=%d body=%q", resp.StatusCode, body)
	}
	assertSprintLifecycleRESTTrace(t, fx.wrapped, "access", "target", "role", "delete")
	if fx.wrapped.targetReads != 1 || fx.wrapped.deleteCalls != 1 {
		t.Fatalf("delete with todos reads=%d calls=%d", fx.wrapped.targetReads, fx.wrapped.deleteCalls)
	}
	for _, before := range []store.Todo{assignedOne, assignedTwo} {
		after, err := fx.st.GetTodoByLocalID(fx.ownerCtx, fx.project.ID, before.LocalID, store.ModeFull)
		if err != nil {
			t.Fatalf("GetTodoByLocalID(%d): %v", before.LocalID, err)
		}
		want := before
		want.SprintID = nil
		want.AssignmentChanged = false
		if !reflect.DeepEqual(after, want) {
			t.Fatalf("todo changed beyond sprint detachment before=%+v after=%+v", before, after)
		}
	}
	for _, before := range []store.Todo{controlAssigned, controlBacklog} {
		after, err := fx.st.GetTodoByLocalID(fx.ownerCtx, fx.project.ID, before.LocalID, store.ModeFull)
		if err != nil {
			t.Fatalf("GetTodoByLocalID control %d: %v", before.LocalID, err)
		}
		want := before
		want.AssignmentChanged = false
		if !reflect.DeepEqual(after, want) {
			t.Fatalf("control todo changed before=%+v after=%+v", before, after)
		}
	}
	assertSprintLifecycleRESTRefresh(t, fx, fx.project.ID, "sprint_deleted")
}

func TestSprintLifecycleRESTAuthorityAndInputContract(t *testing.T) {
	operationSetup := func(t *testing.T, fx *sprintLifecycleRESTFixture, operation string) (string, string) {
		t.Helper()
		switch operation {
		case "activate":
			sp := createSprintLifecycleRESTSprint(t, fx, "Authority activate", store.SprintStatePlanned)
			return http.MethodPost, fx.actionURL(sp.ID, operation)
		case "close":
			sp := createSprintLifecycleRESTSprint(t, fx, "Authority close", store.SprintStateActive)
			return http.MethodPost, fx.actionURL(sp.ID, operation)
		case "delete":
			sp := createSprintLifecycleRESTSprint(t, fx, "Authority delete", store.SprintStatePlanned)
			return http.MethodDelete, fx.deleteURL(sp.ID)
		default:
			t.Fatalf("unknown operation %q", operation)
			return "", ""
		}
	}

	for _, operation := range []string{"activate", "close", "delete"} {
		operation := operation
		t.Run(operation+"/invalid sprint id", func(t *testing.T) {
			fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-invalid-id-"+operation)
			method := http.MethodPost
			path := fx.ts.URL + "/api/board/" + fx.project.Slug + "/sprints/not-a-number/" + operation
			if operation == "delete" {
				method = http.MethodDelete
				path = fx.ts.URL + "/api/board/" + fx.project.Slug + "/sprints/not-a-number"
			}
			fx.wrapped.activateTrace()
			var got apiErrorEnvelope
			resp, _ := doJSON(t, fx.client, method, path, map[string]any{}, &got)
			assertSprintLifecycleRESTError(t, resp, got, http.StatusBadRequest, "VALIDATION_ERROR", "invalid sprintId", map[string]any{"field": "sprintId", "reason": "invalid_sprint_id"})
			assertSprintLifecycleRESTTrace(t, fx.wrapped, "access")
			assertSprintLifecycleRESTSilence(t, fx)
		})

		t.Run(operation+"/signed out access hides project", func(t *testing.T) {
			fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-signed-out-"+operation)
			method, path := operationSetup(t, fx, operation)
			fx.wrapped.activateTrace()
			var got apiErrorEnvelope
			resp, _ := doJSON(t, fx.ts.Client(), method, path, map[string]any{}, &got)
			assertSprintLifecycleRESTError(t, resp, got, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			assertSprintLifecycleRESTTrace(t, fx.wrapped, "access")
			assertSprintLifecycleRESTSilence(t, fx)
		})

		t.Run(operation+"/actor absence after forced access", func(t *testing.T) {
			fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-actor-absent-"+operation)
			method, path := operationSetup(t, fx, operation)
			pc := fx.projectPC
			fx.wrapped.accessOverride = &pc
			fx.wrapped.activateTrace()
			var got apiErrorEnvelope
			resp, _ := doJSON(t, fx.ts.Client(), method, path, map[string]any{}, &got)
			assertSprintLifecycleRESTError(t, resp, got, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			wantTrace := []string{"access"}
			if operation == "delete" {
				wantTrace = append(wantTrace, "target")
			}
			assertSprintLifecycleRESTTrace(t, fx.wrapped, wantTrace...)
			assertSprintLifecycleRESTSilence(t, fx)
		})

		t.Run(operation+"/fresh viewer role overrides access context", func(t *testing.T) {
			fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-fresh-role-"+operation)
			method, path := operationSetup(t, fx, operation)
			pc := fx.projectPC
			pc.Role = store.RoleOwner
			viewer := store.RoleViewer
			fx.wrapped.accessOverride = &pc
			fx.wrapped.roleOverride = &viewer
			fx.wrapped.activateTrace()
			var got apiErrorEnvelope
			resp, _ := doJSON(t, fx.client, method, path, map[string]any{}, &got)
			assertSprintLifecycleRESTError(t, resp, got, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
			wantTrace := []string{"access"}
			if operation == "delete" {
				wantTrace = append(wantTrace, "target")
			}
			wantTrace = append(wantTrace, "role")
			assertSprintLifecycleRESTTrace(t, fx.wrapped, wantTrace...)
			if fx.wrapped.roleUID != fx.actorID || fx.wrapped.rolePID != fx.project.ID || fx.wrapped.activateCalls+fx.wrapped.closeCalls+fx.wrapped.deleteCalls != 0 {
				t.Fatalf("authority observations=(project=%d actor=%d mutations=%d)", fx.wrapped.rolePID, fx.wrapped.roleUID, fx.wrapped.activateCalls+fx.wrapped.closeCalls+fx.wrapped.deleteCalls)
			}
			assertSprintLifecycleRESTSilence(t, fx)
		})
	}
}

func TestSprintLifecycleRESTDeleteFailurePrecedenceContract(t *testing.T) {
	t.Run("missing target stops before role", func(t *testing.T) {
		fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-delete-missing")
		fx.wrapped.activateTrace()
		var got apiErrorEnvelope
		resp, _ := doJSON(t, fx.client, http.MethodDelete, fx.deleteURL(900003), map[string]any{}, &got)
		assertSprintLifecycleRESTError(t, resp, got, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		assertSprintLifecycleRESTTrace(t, fx.wrapped, "access", "target")
		if fx.wrapped.deleteCalls != 0 {
			t.Fatalf("missing target delete calls=%d", fx.wrapped.deleteCalls)
		}
		assertSprintLifecycleRESTSilence(t, fx)
	})

	t.Run("foreign target is hidden before role", func(t *testing.T) {
		fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-delete-foreign")
		other, err := fx.st.CreateProject(fx.ownerCtx, "rest-lifecycle-delete-foreign-project")
		if err != nil {
			t.Fatalf("CreateProject foreign: %v", err)
		}
		now := time.Now().UTC()
		sp, err := fx.st.CreateSprint(fx.ownerCtx, other.ID, "Foreign", now.Add(-time.Hour), now.Add(48*time.Hour))
		if err != nil {
			t.Fatalf("CreateSprint foreign: %v", err)
		}
		fx.wrapped.activateTrace()
		var got apiErrorEnvelope
		resp, _ := doJSON(t, fx.client, http.MethodDelete, fx.deleteURL(sp.ID), map[string]any{}, &got)
		assertSprintLifecycleRESTError(t, resp, got, http.StatusNotFound, "NOT_FOUND", "sprint not found", nil)
		assertSprintLifecycleRESTTrace(t, fx.wrapped, "access", "target")
		if fx.wrapped.deleteCalls != 0 {
			t.Fatalf("foreign target delete calls=%d", fx.wrapped.deleteCalls)
		}
		if stored := getSprintLifecycleRESTSprint(t, fx, sp.ID); stored.ProjectID != other.ID {
			t.Fatalf("foreign target changed=%+v", stored)
		}
		assertSprintLifecycleRESTSilence(t, fx)
	})
}

func TestSprintLifecycleRESTDependencyFailureContract(t *testing.T) {
	type testCase struct {
		operation   string
		stage       string
		wantTrace   []string
		wantStatus  int
		wantCode    string
		wantMessage string
		wantDetails map[string]any
	}
	sentinel := errors.New("sprint lifecycle REST injected failure")
	cases := []testCase{
		{operation: "activate", stage: "access", wantTrace: []string{"access"}, wantStatus: 500, wantCode: "INTERNAL", wantMessage: "internal error", wantDetails: map[string]any{"detail": sentinel.Error()}},
		{operation: "activate", stage: "role", wantTrace: []string{"access", "role"}, wantStatus: 403, wantCode: "FORBIDDEN", wantMessage: "maintainer or higher required"},
		{operation: "activate", stage: "mutation", wantTrace: []string{"access", "role", "activate"}, wantStatus: 500, wantCode: "INTERNAL", wantMessage: "internal error", wantDetails: map[string]any{"detail": sentinel.Error()}},
		{operation: "close", stage: "access", wantTrace: []string{"access"}, wantStatus: 500, wantCode: "INTERNAL", wantMessage: "internal error", wantDetails: map[string]any{"detail": sentinel.Error()}},
		{operation: "close", stage: "role", wantTrace: []string{"access", "role"}, wantStatus: 403, wantCode: "FORBIDDEN", wantMessage: "maintainer or higher required"},
		{operation: "close", stage: "target", wantTrace: []string{"access", "role", "target"}, wantStatus: 500, wantCode: "INTERNAL", wantMessage: "internal error", wantDetails: map[string]any{"detail": sentinel.Error()}},
		{operation: "close", stage: "mutation", wantTrace: []string{"access", "role", "target", "close"}, wantStatus: 500, wantCode: "INTERNAL", wantMessage: "internal error", wantDetails: map[string]any{"detail": sentinel.Error()}},
		{operation: "delete", stage: "access", wantTrace: []string{"access"}, wantStatus: 500, wantCode: "INTERNAL", wantMessage: "internal error", wantDetails: map[string]any{"detail": sentinel.Error()}},
		{operation: "delete", stage: "target", wantTrace: []string{"access", "target"}, wantStatus: 500, wantCode: "INTERNAL", wantMessage: "internal error", wantDetails: map[string]any{"detail": sentinel.Error()}},
		{operation: "delete", stage: "role", wantTrace: []string{"access", "target", "role"}, wantStatus: 403, wantCode: "FORBIDDEN", wantMessage: "maintainer or higher required"},
		{operation: "delete", stage: "mutation", wantTrace: []string{"access", "target", "role", "delete"}, wantStatus: 500, wantCode: "INTERNAL", wantMessage: "internal error", wantDetails: map[string]any{"detail": sentinel.Error()}},
	}
	for _, tc := range cases {
		t.Run(tc.operation+"/"+tc.stage, func(t *testing.T) {
			fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-failure-"+tc.operation+"-"+tc.stage)
			var method, path string
			var before store.Sprint
			switch tc.operation {
			case "activate":
				sp := createSprintLifecycleRESTSprint(t, fx, "Failure activate", store.SprintStatePlanned)
				before = getSprintLifecycleRESTSprint(t, fx, sp.ID)
				method, path = http.MethodPost, fx.actionURL(sp.ID, "activate")
			case "close":
				sp := createSprintLifecycleRESTSprint(t, fx, "Failure close", store.SprintStateActive)
				before = getSprintLifecycleRESTSprint(t, fx, sp.ID)
				method, path = http.MethodPost, fx.actionURL(sp.ID, "close")
			case "delete":
				sp := createSprintLifecycleRESTSprint(t, fx, "Failure delete", store.SprintStatePlanned)
				before = getSprintLifecycleRESTSprint(t, fx, sp.ID)
				method, path = http.MethodDelete, fx.deleteURL(sp.ID)
			}
			switch tc.stage {
			case "access":
				fx.wrapped.accessErr = sentinel
			case "role":
				fx.wrapped.roleErr = sentinel
			case "target":
				fx.wrapped.targetErr = sentinel
			case "mutation":
				switch tc.operation {
				case "activate":
					fx.wrapped.activateErr = sentinel
				case "close":
					fx.wrapped.closeErr = sentinel
				case "delete":
					fx.wrapped.deleteErr = sentinel
				}
			}
			fx.wrapped.activateTrace()
			var got apiErrorEnvelope
			resp, _ := doJSON(t, fx.client, method, path, map[string]any{}, &got)
			assertSprintLifecycleRESTError(t, resp, got, tc.wantStatus, tc.wantCode, tc.wantMessage, tc.wantDetails)
			assertSprintLifecycleRESTTrace(t, fx.wrapped, tc.wantTrace...)
			if after := getSprintLifecycleRESTSprint(t, fx, before.ID); !reflect.DeepEqual(after, before) {
				t.Fatalf("dependency failure changed sprint before=%+v after=%+v", before, after)
			}
			assertSprintLifecycleRESTSilence(t, fx)
		})
	}
}

func TestSprintLifecycleRESTCancellationContract(t *testing.T) {
	type testCase struct {
		operation string
		stage     string
		wantTrace []string
	}
	cases := []testCase{
		{operation: "activate", stage: "access", wantTrace: []string{"access"}},
		{operation: "activate", stage: "role", wantTrace: []string{"access", "role"}},
		{operation: "activate", stage: "mutation", wantTrace: []string{"access", "role", "activate"}},
		{operation: "close", stage: "access", wantTrace: []string{"access"}},
		{operation: "close", stage: "role", wantTrace: []string{"access", "role"}},
		{operation: "close", stage: "target", wantTrace: []string{"access", "role", "target"}},
		{operation: "close", stage: "mutation", wantTrace: []string{"access", "role", "target", "close"}},
		{operation: "delete", stage: "access", wantTrace: []string{"access"}},
		{operation: "delete", stage: "target", wantTrace: []string{"access", "target"}},
		{operation: "delete", stage: "role", wantTrace: []string{"access", "target", "role"}},
		{operation: "delete", stage: "mutation", wantTrace: []string{"access", "target", "role", "delete"}},
	}
	for _, tc := range cases {
		t.Run(tc.operation+"/"+tc.stage, func(t *testing.T) {
			fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-cancel-"+tc.operation+"-"+tc.stage)
			var method, path string
			var before store.Sprint
			switch tc.operation {
			case "activate":
				sp := createSprintLifecycleRESTSprint(t, fx, "Canceled activate", store.SprintStatePlanned)
				before = getSprintLifecycleRESTSprint(t, fx, sp.ID)
				method, path = http.MethodPost, fx.actionURL(sp.ID, "activate")
			case "close":
				sp := createSprintLifecycleRESTSprint(t, fx, "Canceled close", store.SprintStateActive)
				before = getSprintLifecycleRESTSprint(t, fx, sp.ID)
				method, path = http.MethodPost, fx.actionURL(sp.ID, "close")
			case "delete":
				sp := createSprintLifecycleRESTSprint(t, fx, "Canceled delete", store.SprintStatePlanned)
				before = getSprintLifecycleRESTSprint(t, fx, sp.ID)
				method, path = http.MethodDelete, fx.deleteURL(sp.ID)
			}
			switch tc.stage {
			case "access":
				fx.wrapped.accessErr = context.Canceled
			case "role":
				fx.wrapped.roleErr = context.Canceled
			case "target":
				fx.wrapped.targetErr = context.Canceled
			case "mutation":
				switch tc.operation {
				case "activate":
					fx.wrapped.activateErr = context.Canceled
				case "close":
					fx.wrapped.closeErr = context.Canceled
				case "delete":
					fx.wrapped.deleteErr = context.Canceled
				}
			}
			fx.wrapped.activateTrace()
			var got apiErrorEnvelope
			resp, _ := doJSON(t, fx.client, method, path, map[string]any{}, &got)
			if tc.stage == "role" {
				assertSprintLifecycleRESTError(t, resp, got, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
			} else {
				assertSprintLifecycleRESTError(t, resp, got, http.StatusInternalServerError, "INTERNAL", "internal error", map[string]any{"detail": context.Canceled.Error()})
			}
			assertSprintLifecycleRESTTrace(t, fx.wrapped, tc.wantTrace...)
			if after := getSprintLifecycleRESTSprint(t, fx, before.ID); !reflect.DeepEqual(after, before) {
				t.Fatalf("cancellation changed sprint before=%+v after=%+v", before, after)
			}
			assertSprintLifecycleRESTSilence(t, fx)
		})
	}
}

func TestSprintLifecycleRESTMutationErrorMappingContract(t *testing.T) {
	validationErr := fmt.Errorf("%w: lifecycle mutation rejected", store.ErrValidation)
	for _, operation := range []string{"activate", "close", "delete"} {
		for _, tc := range []struct {
			name        string
			err         error
			wantStatus  int
			wantCode    string
			wantMessage string
		}{
			{name: "validation", err: validationErr, wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: validationErr.Error()},
			{name: "not found", err: store.ErrNotFound, wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMessage: "not found"},
		} {
			operation, tc := operation, tc
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-mutation-mapping-"+operation+"-"+strings.ReplaceAll(tc.name, " ", "-"))
				state := store.SprintStatePlanned
				if operation == "close" {
					state = store.SprintStateActive
				}
				target := createSprintLifecycleRESTSprint(t, fx, "Mutation mapping", state)
				before := getSprintLifecycleRESTSprint(t, fx, target.ID)
				method := http.MethodPost
				path := fx.actionURL(target.ID, operation)
				switch operation {
				case "activate":
					fx.wrapped.activateErr = tc.err
				case "close":
					fx.wrapped.closeErr = tc.err
				case "delete":
					method = http.MethodDelete
					path = fx.deleteURL(target.ID)
					fx.wrapped.deleteErr = tc.err
				}
				fx.wrapped.activateTrace()
				var got apiErrorEnvelope
				resp, _ := doJSON(t, fx.client, method, path, map[string]any{}, &got)
				assertSprintLifecycleRESTError(t, resp, got, tc.wantStatus, tc.wantCode, tc.wantMessage, nil)
				wantTrace := []string{"access", "role", operation}
				if operation == "close" {
					wantTrace = []string{"access", "role", "target", "close"}
				} else if operation == "delete" {
					wantTrace = []string{"access", "target", "role", "delete"}
				}
				assertSprintLifecycleRESTTrace(t, fx.wrapped, wantTrace...)
				if after := getSprintLifecycleRESTSprint(t, fx, target.ID); !reflect.DeepEqual(after, before) {
					t.Fatalf("mapped mutation failure changed sprint before=%+v after=%+v", before, after)
				}
				assertSprintLifecycleRESTSilence(t, fx)
			})
		}
	}
}

func TestSprintLifecycleRESTRejectsDisabledProjectWithoutSideEffects(t *testing.T) {
	for _, operation := range []string{"activate", "close", "delete"} {
		t.Run(operation, func(t *testing.T) {
			fx := newSprintLifecycleRESTFixture(t, "rest-lifecycle-disabled-"+operation)
			state := store.SprintStatePlanned
			if operation == "close" {
				state = store.SprintStateActive
			}
			sp := createSprintLifecycleRESTSprint(t, fx, "Dormant "+operation, state)
			before := getSprintLifecycleRESTSprint(t, fx, sp.ID)
			if err := fx.st.UpdateProjectSprintsEnabled(fx.ownerCtx, fx.project.ID, fx.ownerID, false); err != nil {
				t.Fatalf("disable sprints: %v", err)
			}
			fx.wrapped.activateTrace()

			method := http.MethodPost
			path := fx.actionURL(sp.ID, operation)
			if operation == "delete" {
				method = http.MethodDelete
				path = fx.deleteURL(sp.ID)
			}
			var got apiErrorEnvelope
			resp, _ := doJSON(t, fx.client, method, path, map[string]any{}, &got)
			assertSprintLifecycleRESTError(
				t, resp, got, http.StatusBadRequest, "VALIDATION_ERROR",
				store.ErrSprintsDisabled.Error(), map[string]any{"reason": "sprints_disabled"},
			)
			if after := getSprintLifecycleRESTSprint(t, fx, sp.ID); !reflect.DeepEqual(after, before) {
				t.Fatalf("disabled %s changed sprint: before=%+v after=%+v", operation, before, after)
			}
			assertSprintLifecycleRESTSilence(t, fx)
		})
	}
}
