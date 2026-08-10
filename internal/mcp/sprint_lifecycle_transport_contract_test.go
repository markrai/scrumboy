package mcp_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/httpapi"
	"scrumboy/internal/mcp"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

type sprintLifecycleMCPStore struct {
	*store.Store

	mu     sync.Mutex
	active bool
	trace  []string

	accessErr      error
	accessOverride *store.ProjectContext
	roleErr        error
	roleOverride   *store.ProjectRole
	targetErr      error
	projectionErr  error
	activateErr    error
	closeErr       error
	deleteErr      error

	accessSlug string
	accessMode store.Mode
	rolePID    int64
	roleUID    int64

	readCalls int
	readIDs   []int64

	activateCalls     int
	activatePID       int64
	activateID        int64
	activateCommitted bool
	closeCalls        int
	closePID          int64
	closeID           int64
	closeCommitted    bool
	deleteCalls       int
	deletePID         int64
	deleteID          int64
	deleteCommitted   bool
}

func (s *sprintLifecycleMCPStore) activateTrace() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = true
	s.trace = nil
	s.readCalls = 0
	s.readIDs = nil
	s.activateCalls = 0
	s.activateCommitted = false
	s.closeCalls = 0
	s.closeCommitted = false
	s.deleteCalls = 0
	s.deleteCommitted = false
}

func (s *sprintLifecycleMCPStore) record(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return false
	}
	s.trace = append(s.trace, name)
	return true
}

func (s *sprintLifecycleMCPStore) snapshotTrace() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.trace...)
}

func (s *sprintLifecycleMCPStore) GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error) {
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

func (s *sprintLifecycleMCPStore) GetProjectRole(ctx context.Context, projectID, userID int64) (store.ProjectRole, error) {
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

func (s *sprintLifecycleMCPStore) GetSprintByID(ctx context.Context, sprintID int64) (store.Sprint, error) {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return s.Store.GetSprintByID(ctx, sprintID)
	}
	s.readCalls++
	readNumber := s.readCalls
	s.readIDs = append(s.readIDs, sprintID)
	stage := "target"
	err := s.targetErr
	if readNumber > 1 {
		stage = "projection"
		err = s.projectionErr
	}
	s.trace = append(s.trace, stage)
	s.mu.Unlock()
	if err != nil {
		return store.Sprint{}, err
	}
	return s.Store.GetSprintByID(ctx, sprintID)
}

func (s *sprintLifecycleMCPStore) ActivateSprint(ctx context.Context, projectID, sprintID int64) error {
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
	if err := s.Store.ActivateSprint(ctx, projectID, sprintID); err != nil {
		return err
	}
	s.mu.Lock()
	s.activateCommitted = true
	s.mu.Unlock()
	return nil
}

func (s *sprintLifecycleMCPStore) CloseSprint(ctx context.Context, projectID, sprintID int64) error {
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
	if err := s.Store.CloseSprint(ctx, projectID, sprintID); err != nil {
		return err
	}
	s.mu.Lock()
	s.closeCommitted = true
	s.mu.Unlock()
	return nil
}

func (s *sprintLifecycleMCPStore) DeleteSprint(ctx context.Context, projectID, sprintID int64) error {
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
	if err := s.Store.DeleteSprint(ctx, projectID, sprintID); err != nil {
		return err
	}
	s.mu.Lock()
	s.deleteCommitted = true
	s.mu.Unlock()
	return nil
}

type sprintLifecycleMCPFixture struct {
	ts          *httptest.Server
	db          *sql.DB
	st          *store.Store
	wrapped     *sprintLifecycleMCPStore
	client      *http.Client
	ownerClient *http.Client
	ownerID     int64
	actorID     int64
	ownerCtx    context.Context
	actorCtx    context.Context
	project     store.Project
	projectPC   store.ProjectContext
}

func newSprintLifecycleMCPFixture(t *testing.T, name string) *sprintLifecycleMCPFixture {
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
	wrapped := &sprintLifecycleMCPStore{Store: st}
	srv := httpapi.NewServer(wrapped, httpapi.Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		MCPHandler:     mcp.New(wrapped, mcp.Options{Mode: "full"}),
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		_ = sqlDB.Close()
	})

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	ownerCtx := store.WithUserID(context.Background(), ownerID)
	actorEmail := name + "-actor@example.com"
	actor, err := st.CreateUser(context.Background(), actorEmail, "password123", "Lifecycle Maintainer")
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
	client := loginTodoUpdateMCPUser(t, ts, actorEmail, "password123")

	return &sprintLifecycleMCPFixture{
		ts: ts, db: sqlDB, st: st, wrapped: wrapped, client: client, ownerClient: ownerClient,
		ownerID: ownerID, actorID: actor.ID, ownerCtx: ownerCtx, actorCtx: actorCtx,
		project: project, projectPC: pc,
	}
}

func createSprintLifecycleMCPSprint(t *testing.T, fx *sprintLifecycleMCPFixture, name, state string) store.Sprint {
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

func callSprintLifecycleMCP(t *testing.T, fx *sprintLifecycleMCPFixture, client *http.Client, transport, tool string, args map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	return callTodoUpdateMCP(t, client, fx.ts.URL, transport, tool, args)
}

func subscribeSprintLifecycleMCPEvents(t *testing.T, fx *sprintLifecycleMCPFixture, client *http.Client) *todoUpdateMCPEventStream {
	t.Helper()
	return subscribeTodoUpdateMCPEvents(t, client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")
}

func assertSprintLifecycleMCPSilence(t *testing.T, stream *todoUpdateMCPEventStream) {
	t.Helper()
	if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
		t.Fatalf("MCP lifecycle operation emitted realtime events: %+v", events)
	}
}

func assertSprintLifecycleMCPTrace(t *testing.T, wrapped *sprintLifecycleMCPStore, want ...string) {
	t.Helper()
	if got := wrapped.snapshotTrace(); !reflect.DeepEqual(got, want) {
		t.Fatalf("store trace=%v, want %v", got, want)
	}
}

func assertSprintLifecycleMCPError(t *testing.T, transport string, resp *http.Response, out map[string]any, wantStatus int, wantCode, wantMessage string, wantDetails map[string]any) map[string]any {
	t.Helper()
	publicErr := assertTodoLinkMCPError(t, transport, resp, out, wantStatus, wantCode, wantMessage)
	if transport == "legacy" {
		if got, want := sortedMapKeys(out), []string{"error", "ok"}; !reflect.DeepEqual(got, want) || out["ok"] != false {
			t.Fatalf("legacy error envelope=%+v keys=%v want=%v", out, got, want)
		}
		if got, want := sortedMapKeys(publicErr), []string{"code", "details", "message"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("legacy public error keys=%v want=%v error=%+v", got, want, publicErr)
		}
	} else {
		if got, want := sortedMapKeys(out), []string{"id", "jsonrpc", "result"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("JSON-RPC error envelope=%+v keys=%v want=%v", out, got, want)
		}
		result := out["result"].(map[string]any)
		if got, want := sortedMapKeys(result), []string{"content", "isError", "structuredContent"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("JSON-RPC error result=%+v keys=%v want=%v", result, got, want)
		}
		if got, want := sortedMapKeys(publicErr), []string{"code", "details", "message"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("JSON-RPC structured error keys=%v want=%v error=%+v", got, want, publicErr)
		}
	}
	if wantDetails == nil {
		wantDetails = map[string]any{}
	}
	if details, ok := publicErr["details"].(map[string]any); !ok || !reflect.DeepEqual(details, wantDetails) {
		t.Fatalf("public error details=%+v, want %+v", publicErr["details"], wantDetails)
	}
	return publicErr
}

func sprintLifecycleMCPData(t *testing.T, transport string, response map[string]any) map[string]any {
	t.Helper()
	return todoLinkMCPData(t, transport, response)
}

func assertSprintLifecycleMCPItem(t *testing.T, data map[string]any, projectSlug string, expected store.Sprint) {
	t.Helper()
	if got, want := sortedMapKeys(data), []string{"sprint"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("data keys=%v, want %v; data=%+v", got, want, data)
	}
	item, ok := data["sprint"].(map[string]any)
	if !ok {
		t.Fatalf("sprint projection type=%T data=%+v", data["sprint"], data)
	}
	if got, want := sortedMapKeys(item), []string{"closedAt", "name", "number", "plannedEndAt", "plannedStartAt", "projectSlug", "sprintId", "startedAt", "state", "todoCount"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sprint keys=%v, want %v; sprint=%+v", got, want, item)
	}
	if item["projectSlug"] != projectSlug || int64(item["sprintId"].(float64)) != expected.ID || int64(item["number"].(float64)) != expected.Number || item["name"] != expected.Name || int64(item["plannedStartAt"].(float64)) != expected.PlannedStartAt.UnixMilli() || int64(item["plannedEndAt"].(float64)) != expected.PlannedEndAt.UnixMilli() || item["state"] != expected.State || item["todoCount"] != nil {
		t.Fatalf("sprint projection=%+v expected=%+v", item, expected)
	}
	assertTime := func(field string, expectedTime *time.Time) {
		if expectedTime == nil {
			if item[field] != nil {
				t.Fatalf("sprint %s=%v, want null", field, item[field])
			}
			return
		}
		if raw, ok := item[field].(float64); !ok || int64(raw) != expectedTime.UnixMilli() {
			t.Fatalf("sprint %s=%v, want %d", field, item[field], expectedTime.UnixMilli())
		}
	}
	assertTime("startedAt", expected.StartedAt)
	assertTime("closedAt", expected.ClosedAt)
}

func getSprintLifecycleMCPSprint(t *testing.T, fx *sprintLifecycleMCPFixture, sprintID int64) store.Sprint {
	t.Helper()
	sp, err := fx.st.GetSprintByID(fx.ownerCtx, sprintID)
	if err != nil {
		t.Fatalf("GetSprintByID(%d): %v", sprintID, err)
	}
	return sp
}

func TestSprintLifecycleMCPActivateContract(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		transport := transport
		t.Run(transport+"/planned success", func(t *testing.T) {
			fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-activate-"+transport)
			sp := createSprintLifecycleMCPSprint(t, fx, "Target", store.SprintStatePlanned)
			if sp.ID == sp.Number || sp.ID == fx.project.ID || fx.project.ID == fx.actorID {
				t.Fatalf("identity fixture is not distinct: actor=%d project=%d sprint=%d number=%d", fx.actorID, fx.project.ID, sp.ID, sp.Number)
			}
			stream := subscribeSprintLifecycleMCPEvents(t, fx, fx.client)
			defer stream.close()
			fx.wrapped.activateTrace()

			resp, out := callSprintLifecycleMCP(t, fx, fx.client, transport, "sprints_activate", map[string]any{"projectSlug": fx.project.Slug, "sprintId": sp.ID})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("activate status=%d response=%+v", resp.StatusCode, out)
			}
			stored := getSprintLifecycleMCPSprint(t, fx, sp.ID)
			if stored.State != store.SprintStateActive || stored.StartedAt == nil || stored.ClosedAt != nil {
				t.Fatalf("activated sprint=%+v", stored)
			}
			assertSprintLifecycleMCPItem(t, sprintLifecycleMCPData(t, transport, out), fx.project.Slug, stored)
			assertSprintLifecycleMCPTrace(t, fx.wrapped, "access", "role", "target", "activate", "projection")
			if fx.wrapped.accessSlug != fx.project.Slug || fx.wrapped.accessMode != store.ModeFull || fx.wrapped.rolePID != fx.project.ID || fx.wrapped.roleUID != fx.actorID {
				t.Fatalf("access/role args=(slug=%q mode=%q project=%d actor=%d)", fx.wrapped.accessSlug, fx.wrapped.accessMode, fx.wrapped.rolePID, fx.wrapped.roleUID)
			}
			if fx.wrapped.readCalls != 2 || !reflect.DeepEqual(fx.wrapped.readIDs, []int64{sp.ID, sp.ID}) || fx.wrapped.activateCalls != 1 || fx.wrapped.activatePID != fx.project.ID || fx.wrapped.activateID != sp.ID || !fx.wrapped.activateCommitted {
				t.Fatalf("activate observations=(reads=%d ids=%v calls=%d project=%d sprint=%d committed=%v)", fx.wrapped.readCalls, fx.wrapped.readIDs, fx.wrapped.activateCalls, fx.wrapped.activatePID, fx.wrapped.activateID, fx.wrapped.activateCommitted)
			}
			if fx.wrapped.activateID == sp.Number {
				t.Fatalf("Sprint.Number %d was used as persistence identity", sp.Number)
			}
			assertSprintLifecycleMCPSilence(t, stream)
		})

		t.Run(transport+"/replacement", func(t *testing.T) {
			fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-activate-replacement-"+transport)
			prior := createSprintLifecycleMCPSprint(t, fx, "Prior", store.SprintStateActive)
			target := createSprintLifecycleMCPSprint(t, fx, "Replacement", store.SprintStatePlanned)
			stream := subscribeSprintLifecycleMCPEvents(t, fx, fx.client)
			defer stream.close()
			fx.wrapped.activateTrace()

			resp, out := callSprintLifecycleMCP(t, fx, fx.client, transport, "sprints_activate", map[string]any{"projectSlug": fx.project.Slug, "sprintId": target.ID})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("replacement status=%d response=%+v", resp.StatusCode, out)
			}
			priorAfter := getSprintLifecycleMCPSprint(t, fx, prior.ID)
			targetAfter := getSprintLifecycleMCPSprint(t, fx, target.ID)
			if priorAfter.State != store.SprintStateClosed || priorAfter.ClosedAt == nil || targetAfter.State != store.SprintStateActive || targetAfter.StartedAt == nil {
				t.Fatalf("replacement prior=%+v target=%+v", priorAfter, targetAfter)
			}
			assertSprintLifecycleMCPItem(t, sprintLifecycleMCPData(t, transport, out), fx.project.Slug, targetAfter)
			assertSprintLifecycleMCPTrace(t, fx.wrapped, "access", "role", "target", "activate", "projection")
			if fx.wrapped.activateCalls != 1 || fx.wrapped.closeCalls != 0 {
				t.Fatalf("replacement activateCalls=%d closeCalls=%d", fx.wrapped.activateCalls, fx.wrapped.closeCalls)
			}
			assertSprintLifecycleMCPSilence(t, stream)
		})
	}
}

func TestSprintLifecycleMCPCloseContract(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		transport := transport
		t.Run(transport, func(t *testing.T) {
			fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-close-"+transport)
			sp := createSprintLifecycleMCPSprint(t, fx, "Active", store.SprintStateActive)
			before := getSprintLifecycleMCPSprint(t, fx, sp.ID)
			stream := subscribeSprintLifecycleMCPEvents(t, fx, fx.client)
			defer stream.close()
			fx.wrapped.activateTrace()

			resp, out := callSprintLifecycleMCP(t, fx, fx.client, transport, "sprints_close", map[string]any{"projectSlug": fx.project.Slug, "sprintId": sp.ID})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("close status=%d response=%+v", resp.StatusCode, out)
			}
			stored := getSprintLifecycleMCPSprint(t, fx, sp.ID)
			if stored.State != store.SprintStateClosed || stored.ClosedAt == nil || before.StartedAt == nil || stored.StartedAt == nil || !stored.StartedAt.Equal(*before.StartedAt) {
				t.Fatalf("closed sprint before=%+v after=%+v", before, stored)
			}
			assertSprintLifecycleMCPItem(t, sprintLifecycleMCPData(t, transport, out), fx.project.Slug, stored)
			assertSprintLifecycleMCPTrace(t, fx.wrapped, "access", "role", "target", "close", "projection")
			if fx.wrapped.readCalls != 2 || !reflect.DeepEqual(fx.wrapped.readIDs, []int64{sp.ID, sp.ID}) || fx.wrapped.closeCalls != 1 || fx.wrapped.closePID != fx.project.ID || fx.wrapped.closeID != sp.ID || !fx.wrapped.closeCommitted {
				t.Fatalf("close observations=(reads=%d ids=%v calls=%d project=%d sprint=%d committed=%v)", fx.wrapped.readCalls, fx.wrapped.readIDs, fx.wrapped.closeCalls, fx.wrapped.closePID, fx.wrapped.closeID, fx.wrapped.closeCommitted)
			}
			if fx.wrapped.closeID == sp.Number {
				t.Fatalf("Sprint.Number %d was used as close persistence identity", sp.Number)
			}
			assertSprintLifecycleMCPSilence(t, stream)
		})
	}
}

func TestSprintLifecycleMCPDeleteContract(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		for _, state := range []string{store.SprintStatePlanned, store.SprintStateActive, store.SprintStateClosed} {
			transport, state := transport, state
			t.Run(transport+"/"+strings.ToLower(state), func(t *testing.T) {
				fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-delete-"+transport+"-"+strings.ToLower(state))
				sp := createSprintLifecycleMCPSprint(t, fx, state, state)
				stream := subscribeSprintLifecycleMCPEvents(t, fx, fx.client)
				defer stream.close()
				fx.wrapped.activateTrace()

				resp, out := callSprintLifecycleMCP(t, fx, fx.client, transport, "sprints_delete", map[string]any{"projectSlug": fx.project.Slug, "sprintId": sp.ID})
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("delete %s status=%d response=%+v", state, resp.StatusCode, out)
				}
				data := sprintLifecycleMCPData(t, transport, out)
				if got, want := sortedMapKeys(data), []string{"projectSlug", "sprintId", "status"}; !reflect.DeepEqual(got, want) || data["status"] != "deleted" || data["projectSlug"] != fx.project.Slug || int64(data["sprintId"].(float64)) != sp.ID {
					t.Fatalf("delete identity echo=%+v keys=%v want=%v", data, got, want)
				}
				assertSprintLifecycleMCPTrace(t, fx.wrapped, "access", "role", "target", "delete")
				if fx.wrapped.readCalls != 1 || !reflect.DeepEqual(fx.wrapped.readIDs, []int64{sp.ID}) || fx.wrapped.deleteCalls != 1 || fx.wrapped.deletePID != fx.project.ID || fx.wrapped.deleteID != sp.ID || !fx.wrapped.deleteCommitted {
					t.Fatalf("delete observations=(reads=%d ids=%v calls=%d project=%d sprint=%d committed=%v)", fx.wrapped.readCalls, fx.wrapped.readIDs, fx.wrapped.deleteCalls, fx.wrapped.deletePID, fx.wrapped.deleteID, fx.wrapped.deleteCommitted)
				}
				if _, err := fx.st.GetSprintByID(fx.ownerCtx, sp.ID); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("GetSprintByID after delete error=%v, want not found", err)
				}
				if state == store.SprintStateActive {
					active, err := fx.st.GetActiveSprintByProjectID(fx.ownerCtx, fx.project.ID)
					if err != nil || active != nil {
						t.Fatalf("active sprint after active delete=%+v err=%v", active, err)
					}
				}
				assertSprintLifecycleMCPSilence(t, stream)
			})
		}
	}
}

func TestSprintLifecycleMCPActivateRejectionContract(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		for _, tc := range []struct {
			name        string
			prepare     func(*testing.T, *sprintLifecycleMCPFixture) int64
			wantStatus  int
			wantCode    string
			wantMessage string
			wantDetails map[string]any
		}{
			{
				name: "already active",
				prepare: func(t *testing.T, fx *sprintLifecycleMCPFixture) int64 {
					return createSprintLifecycleMCPSprint(t, fx, "Already active", store.SprintStateActive).ID
				},
				wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "sprint must be PLANNED to activate", wantDetails: map[string]any{"field": "sprintId"},
			},
			{
				name: "closed",
				prepare: func(t *testing.T, fx *sprintLifecycleMCPFixture) int64 {
					return createSprintLifecycleMCPSprint(t, fx, "Closed", store.SprintStateClosed).ID
				},
				wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "sprint must be PLANNED to activate", wantDetails: map[string]any{"field": "sprintId"},
			},
			{
				name: "safely expired planned end",
				prepare: func(t *testing.T, fx *sprintLifecycleMCPFixture) int64 {
					now := time.Now().UTC()
					sp, err := fx.st.CreateSprint(fx.ownerCtx, fx.project.ID, "Past", now.Add(-48*time.Hour), now.Add(-24*time.Hour))
					if err != nil {
						t.Fatalf("CreateSprint past: %v", err)
					}
					return sp.ID
				},
				wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "sprint end date is on or before now; cannot activate", wantDetails: map[string]any{"field": "plannedEndAt"},
			},
			{
				name:       "missing",
				prepare:    func(_ *testing.T, _ *sprintLifecycleMCPFixture) int64 { return 910001 },
				wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMessage: "not found",
			},
			{
				name: "foreign",
				prepare: func(t *testing.T, fx *sprintLifecycleMCPFixture) int64 {
					other, err := fx.st.CreateProject(fx.ownerCtx, "mcp-lifecycle-activate-foreign-"+transport)
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
				wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMessage: "not found",
			},
		} {
			transport, tc := transport, tc
			t.Run(transport+"/"+tc.name, func(t *testing.T) {
				fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-activate-reject-"+transport+"-"+strings.ReplaceAll(tc.name, " ", "-"))
				sprintID := tc.prepare(t, fx)
				stream := subscribeSprintLifecycleMCPEvents(t, fx, fx.client)
				defer stream.close()
				fx.wrapped.activateTrace()

				resp, out := callSprintLifecycleMCP(t, fx, fx.client, transport, "sprints_activate", map[string]any{"projectSlug": fx.project.Slug, "sprintId": sprintID})
				assertSprintLifecycleMCPError(t, transport, resp, out, tc.wantStatus, tc.wantCode, tc.wantMessage, tc.wantDetails)
				assertSprintLifecycleMCPTrace(t, fx.wrapped, "access", "role", "target")
				if fx.wrapped.readCalls != 1 || !reflect.DeepEqual(fx.wrapped.readIDs, []int64{sprintID}) || fx.wrapped.activateCalls != 0 {
					t.Fatalf("activate rejection observations=(reads=%d ids=%v calls=%d)", fx.wrapped.readCalls, fx.wrapped.readIDs, fx.wrapped.activateCalls)
				}
				assertSprintLifecycleMCPSilence(t, stream)
			})
		}
	}
}

func TestSprintLifecycleMCPCloseRejectionContract(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		for _, tc := range []struct {
			name    string
			prepare func(*testing.T, *sprintLifecycleMCPFixture) int64
		}{
			{
				name: "planned",
				prepare: func(t *testing.T, fx *sprintLifecycleMCPFixture) int64 {
					return createSprintLifecycleMCPSprint(t, fx, "Planned", store.SprintStatePlanned).ID
				},
			},
			{
				name: "already closed",
				prepare: func(t *testing.T, fx *sprintLifecycleMCPFixture) int64 {
					return createSprintLifecycleMCPSprint(t, fx, "Closed", store.SprintStateClosed).ID
				},
			},
		} {
			transport, tc := transport, tc
			t.Run(transport+"/"+tc.name, func(t *testing.T) {
				fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-close-reject-"+transport+"-"+strings.ReplaceAll(tc.name, " ", "-"))
				sprintID := tc.prepare(t, fx)
				stream := subscribeSprintLifecycleMCPEvents(t, fx, fx.client)
				defer stream.close()
				fx.wrapped.activateTrace()
				resp, out := callSprintLifecycleMCP(t, fx, fx.client, transport, "sprints_close", map[string]any{"projectSlug": fx.project.Slug, "sprintId": sprintID})
				assertSprintLifecycleMCPError(t, transport, resp, out, http.StatusBadRequest, "VALIDATION_ERROR", "sprint must be ACTIVE to close", map[string]any{"field": "sprintId"})
				assertSprintLifecycleMCPTrace(t, fx.wrapped, "access", "role", "target")
				if fx.wrapped.readCalls != 1 || fx.wrapped.closeCalls != 0 {
					t.Fatalf("close rejection reads=%d calls=%d", fx.wrapped.readCalls, fx.wrapped.closeCalls)
				}
				assertSprintLifecycleMCPSilence(t, stream)
			})
		}

		for _, targetKind := range []string{"missing", "foreign"} {
			transport, targetKind := transport, targetKind
			t.Run(transport+"/"+targetKind, func(t *testing.T) {
				fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-close-"+targetKind+"-"+transport)
				sprintID := int64(910002)
				if targetKind == "foreign" {
					other, err := fx.st.CreateProject(fx.ownerCtx, "mcp-lifecycle-close-foreign-project-"+transport)
					if err != nil {
						t.Fatalf("CreateProject foreign: %v", err)
					}
					now := time.Now().UTC()
					sp, err := fx.st.CreateSprint(fx.ownerCtx, other.ID, "Foreign active", now.Add(-time.Hour), now.Add(48*time.Hour))
					if err != nil {
						t.Fatalf("CreateSprint foreign: %v", err)
					}
					if err := fx.st.ActivateSprint(fx.ownerCtx, other.ID, sp.ID); err != nil {
						t.Fatalf("ActivateSprint foreign: %v", err)
					}
					sprintID = sp.ID
				}
				stream := subscribeSprintLifecycleMCPEvents(t, fx, fx.client)
				defer stream.close()
				fx.wrapped.activateTrace()
				resp, out := callSprintLifecycleMCP(t, fx, fx.client, transport, "sprints_close", map[string]any{"projectSlug": fx.project.Slug, "sprintId": sprintID})
				assertSprintLifecycleMCPError(t, transport, resp, out, http.StatusNotFound, "NOT_FOUND", "not found", nil)
				assertSprintLifecycleMCPTrace(t, fx.wrapped, "access", "role", "target")
				if fx.wrapped.closeCalls != 0 {
					t.Fatalf("%s close calls=%d", targetKind, fx.wrapped.closeCalls)
				}
				assertSprintLifecycleMCPSilence(t, stream)
			})
		}
	}
}

func TestSprintLifecycleMCPCommittedProjectionFailureContract(t *testing.T) {
	sentinel := errors.New("sprint lifecycle projection failed")
	for _, transport := range []string{"legacy", "jsonrpc"} {
		for _, operation := range []string{"activate", "close"} {
			transport, operation := transport, operation
			t.Run(transport+"/"+operation, func(t *testing.T) {
				fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-projection-failure-"+transport+"-"+operation)
				var prior store.Sprint
				var target store.Sprint
				if operation == "activate" {
					prior = createSprintLifecycleMCPSprint(t, fx, "Prior active", store.SprintStateActive)
					target = createSprintLifecycleMCPSprint(t, fx, "Activation target", store.SprintStatePlanned)
				} else {
					target = createSprintLifecycleMCPSprint(t, fx, "Close target", store.SprintStateActive)
				}
				stream := subscribeSprintLifecycleMCPEvents(t, fx, fx.client)
				defer stream.close()
				fx.wrapped.projectionErr = sentinel
				fx.wrapped.activateTrace()

				resp, out := callSprintLifecycleMCP(t, fx, fx.client, transport, "sprints_"+operation, map[string]any{"projectSlug": fx.project.Slug, "sprintId": target.ID})
				assertSprintLifecycleMCPError(t, transport, resp, out, http.StatusInternalServerError, "INTERNAL", "internal error", nil)
				assertSprintLifecycleMCPTrace(t, fx.wrapped, "access", "role", "target", operation, "projection")
				if fx.wrapped.readCalls != 2 || !reflect.DeepEqual(fx.wrapped.readIDs, []int64{target.ID, target.ID}) {
					t.Fatalf("projection failure reads=%d ids=%v, want target and projection once", fx.wrapped.readCalls, fx.wrapped.readIDs)
				}
				after := getSprintLifecycleMCPSprint(t, fx, target.ID)
				if operation == "activate" {
					if fx.wrapped.activateCalls != 1 || !fx.wrapped.activateCommitted || fx.wrapped.closeCalls != 0 || after.State != store.SprintStateActive {
						t.Fatalf("activate projection failure calls=%d committed=%v closeCalls=%d target=%+v", fx.wrapped.activateCalls, fx.wrapped.activateCommitted, fx.wrapped.closeCalls, after)
					}
					priorAfter := getSprintLifecycleMCPSprint(t, fx, prior.ID)
					if priorAfter.State != store.SprintStateClosed || priorAfter.ClosedAt == nil {
						t.Fatalf("prior active not durably closed after projection failure: %+v", priorAfter)
					}
				} else if fx.wrapped.closeCalls != 1 || !fx.wrapped.closeCommitted || fx.wrapped.activateCalls != 0 || after.State != store.SprintStateClosed {
					t.Fatalf("close projection failure calls=%d committed=%v activateCalls=%d target=%+v", fx.wrapped.closeCalls, fx.wrapped.closeCommitted, fx.wrapped.activateCalls, after)
				}
				assertSprintLifecycleMCPSilence(t, stream)
			})
		}
	}
}

func TestSprintLifecycleMCPDeleteDetachesTodos(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		transport := transport
		t.Run(transport, func(t *testing.T) {
			fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-delete-detach-"+transport)
			target := createSprintLifecycleMCPSprint(t, fx, "Target", store.SprintStateClosed)
			controlSprint := createSprintLifecycleMCPSprint(t, fx, "Control", store.SprintStatePlanned)
			points := int64(5)
			assigned, err := fx.st.CreateTodo(fx.ownerCtx, fx.project.ID, store.CreateTodoInput{
				Title: "Assigned", Body: "assigned body", Tags: []string{"phase20"}, ColumnKey: store.DefaultColumnDoing,
				EstimationPoints: &points, AssigneeUserID: &fx.actorID, SprintID: &target.ID,
			}, store.ModeFull)
			if err != nil {
				t.Fatalf("CreateTodo assigned: %v", err)
			}
			control, err := fx.st.CreateTodo(fx.ownerCtx, fx.project.ID, store.CreateTodoInput{
				Title: "Control", Body: "control body", ColumnKey: store.DefaultColumnBacklog, SprintID: &controlSprint.ID,
			}, store.ModeFull)
			if err != nil {
				t.Fatalf("CreateTodo control: %v", err)
			}
			stream := subscribeSprintLifecycleMCPEvents(t, fx, fx.client)
			defer stream.close()
			fx.wrapped.activateTrace()

			resp, out := callSprintLifecycleMCP(t, fx, fx.client, transport, "sprints_delete", map[string]any{"projectSlug": fx.project.Slug, "sprintId": target.ID})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("delete detach status=%d response=%+v", resp.StatusCode, out)
			}
			data := sprintLifecycleMCPData(t, transport, out)
			if data["status"] != "deleted" || data["projectSlug"] != fx.project.Slug || int64(data["sprintId"].(float64)) != target.ID {
				t.Fatalf("delete detach echo=%+v", data)
			}
			assertSprintLifecycleMCPTrace(t, fx.wrapped, "access", "role", "target", "delete")
			if fx.wrapped.readCalls != 1 || fx.wrapped.deleteCalls != 1 {
				t.Fatalf("delete detach reads=%d calls=%d", fx.wrapped.readCalls, fx.wrapped.deleteCalls)
			}
			afterAssigned, err := fx.st.GetTodoByLocalID(fx.ownerCtx, fx.project.ID, assigned.LocalID, store.ModeFull)
			if err != nil {
				t.Fatalf("GetTodoByLocalID assigned: %v", err)
			}
			wantAssigned := assigned
			wantAssigned.SprintID = nil
			wantAssigned.AssignmentChanged = false
			if !reflect.DeepEqual(afterAssigned, wantAssigned) {
				t.Fatalf("assigned todo changed beyond detachment before=%+v after=%+v", assigned, afterAssigned)
			}
			afterControl, err := fx.st.GetTodoByLocalID(fx.ownerCtx, fx.project.ID, control.LocalID, store.ModeFull)
			if err != nil {
				t.Fatalf("GetTodoByLocalID control: %v", err)
			}
			wantControl := control
			wantControl.AssignmentChanged = false
			if !reflect.DeepEqual(afterControl, wantControl) {
				t.Fatalf("control todo changed before=%+v after=%+v", control, afterControl)
			}
			assertSprintLifecycleMCPSilence(t, stream)
		})
	}
}

func prepareSprintLifecycleMCPFailureTarget(t *testing.T, fx *sprintLifecycleMCPFixture, operation string) (string, map[string]any, store.Sprint) {
	t.Helper()
	state := store.SprintStatePlanned
	if operation == "close" {
		state = store.SprintStateActive
	}
	target := createSprintLifecycleMCPSprint(t, fx, "Failure target", state)
	return "sprints_" + operation, map[string]any{"projectSlug": fx.project.Slug, "sprintId": target.ID}, getSprintLifecycleMCPSprint(t, fx, target.ID)
}

func injectSprintLifecycleMCPFailure(wrapped *sprintLifecycleMCPStore, operation, stage string, err error) {
	switch stage {
	case "access":
		wrapped.accessErr = err
	case "role":
		wrapped.roleErr = err
	case "target":
		wrapped.targetErr = err
	case "mutation":
		switch operation {
		case "activate":
			wrapped.activateErr = err
		case "close":
			wrapped.closeErr = err
		case "delete":
			wrapped.deleteErr = err
		}
	case "projection":
		wrapped.projectionErr = err
	}
}

func sprintLifecycleMCPFailureTrace(operation, stage string) []string {
	trace := []string{"access"}
	if stage == "access" {
		return trace
	}
	trace = append(trace, "role")
	if stage == "role" {
		return trace
	}
	trace = append(trace, "target")
	if stage == "target" {
		return trace
	}
	trace = append(trace, operation)
	if stage == "mutation" {
		return trace
	}
	return append(trace, "projection")
}

func assertSprintLifecycleMCPFailurePersistence(t *testing.T, fx *sprintLifecycleMCPFixture, operation, stage string, before store.Sprint) {
	t.Helper()
	after := getSprintLifecycleMCPSprint(t, fx, before.ID)
	if stage != "projection" {
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("pre-commit failure changed sprint before=%+v after=%+v", before, after)
		}
		return
	}
	if operation == "activate" {
		if after.State != store.SprintStateActive || after.StartedAt == nil || !fx.wrapped.activateCommitted || fx.wrapped.activateCalls != 1 || fx.wrapped.closeCalls != 0 {
			t.Fatalf("activation projection failure state=%+v calls=%d committed=%v closeCalls=%d", after, fx.wrapped.activateCalls, fx.wrapped.activateCommitted, fx.wrapped.closeCalls)
		}
		return
	}
	if after.State != store.SprintStateClosed || after.ClosedAt == nil || !fx.wrapped.closeCommitted || fx.wrapped.closeCalls != 1 || fx.wrapped.activateCalls != 0 {
		t.Fatalf("close projection failure state=%+v calls=%d committed=%v activateCalls=%d", after, fx.wrapped.closeCalls, fx.wrapped.closeCommitted, fx.wrapped.activateCalls)
	}
}

func TestSprintLifecycleMCPDependencyFailureContract(t *testing.T) {
	sentinel := errors.New("sprint lifecycle MCP injected failure")
	for _, transport := range []string{"legacy", "jsonrpc"} {
		for _, operation := range []string{"activate", "close", "delete"} {
			for _, stage := range []string{"access", "role", "target", "mutation"} {
				transport, operation, stage := transport, operation, stage
				t.Run(transport+"/"+operation+"/"+stage, func(t *testing.T) {
					fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-failure-"+transport+"-"+operation+"-"+stage)
					tool, args, before := prepareSprintLifecycleMCPFailureTarget(t, fx, operation)
					stream := subscribeSprintLifecycleMCPEvents(t, fx, fx.client)
					defer stream.close()
					injectSprintLifecycleMCPFailure(fx.wrapped, operation, stage, sentinel)
					fx.wrapped.activateTrace()

					resp, out := callSprintLifecycleMCP(t, fx, fx.client, transport, tool, args)
					assertSprintLifecycleMCPError(t, transport, resp, out, http.StatusInternalServerError, "INTERNAL", "internal error", nil)
					assertSprintLifecycleMCPTrace(t, fx.wrapped, sprintLifecycleMCPFailureTrace(operation, stage)...)
					assertSprintLifecycleMCPFailurePersistence(t, fx, operation, stage, before)
					if fx.wrapped.activateCommitted || fx.wrapped.closeCommitted || fx.wrapped.deleteCommitted {
						t.Fatalf("pre-commit failure recorded committed mutation: activate=%v close=%v delete=%v", fx.wrapped.activateCommitted, fx.wrapped.closeCommitted, fx.wrapped.deleteCommitted)
					}
					assertSprintLifecycleMCPSilence(t, stream)
				})
			}
		}
	}
}

func TestSprintLifecycleMCPCancellationContract(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		for _, operation := range []string{"activate", "close", "delete"} {
			stages := []string{"access", "role", "target", "mutation"}
			if operation != "delete" {
				stages = append(stages, "projection")
			}
			for _, stage := range stages {
				transport, operation, stage := transport, operation, stage
				t.Run(transport+"/"+operation+"/"+stage, func(t *testing.T) {
					fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-cancel-"+transport+"-"+operation+"-"+stage)
					tool, args, before := prepareSprintLifecycleMCPFailureTarget(t, fx, operation)
					stream := subscribeSprintLifecycleMCPEvents(t, fx, fx.client)
					defer stream.close()
					injectSprintLifecycleMCPFailure(fx.wrapped, operation, stage, context.Canceled)
					fx.wrapped.activateTrace()

					resp, out := callSprintLifecycleMCP(t, fx, fx.client, transport, tool, args)
					assertSprintLifecycleMCPError(t, transport, resp, out, http.StatusInternalServerError, "INTERNAL", "internal error", nil)
					assertSprintLifecycleMCPTrace(t, fx.wrapped, sprintLifecycleMCPFailureTrace(operation, stage)...)
					assertSprintLifecycleMCPFailurePersistence(t, fx, operation, stage, before)
					if stage == "projection" {
						if fx.wrapped.readCalls != 2 {
							t.Fatalf("projection cancellation reads=%d, want two", fx.wrapped.readCalls)
						}
					} else if fx.wrapped.activateCommitted || fx.wrapped.closeCommitted || fx.wrapped.deleteCommitted {
						t.Fatalf("pre-commit cancellation recorded committed mutation")
					}
					assertSprintLifecycleMCPSilence(t, stream)
				})
			}
		}
	}
}

func TestSprintLifecycleMCPStoreErrorMappingContract(t *testing.T) {
	validationErr := fmt.Errorf("%w: lifecycle mutation rejected", store.ErrValidation)
	for _, transport := range []string{"legacy", "jsonrpc"} {
		for _, operation := range []string{"activate", "close", "delete"} {
			for _, tc := range []struct {
				name        string
				stage       string
				err         error
				wantStatus  int
				wantCode    string
				wantMessage string
			}{
				{name: "access not found", stage: "access", err: store.ErrNotFound, wantStatus: 404, wantCode: "NOT_FOUND", wantMessage: "not found"},
				{name: "access unauthorized", stage: "access", err: store.ErrUnauthorized, wantStatus: 401, wantCode: "AUTH_REQUIRED", wantMessage: "Sign-in required for this tool"},
				{name: "role forbidden", stage: "role", err: store.ErrForbidden, wantStatus: 403, wantCode: "FORBIDDEN", wantMessage: "forbidden"},
				{name: "role not found", stage: "role", err: store.ErrNotFound, wantStatus: 404, wantCode: "NOT_FOUND", wantMessage: "not found"},
				{name: "mutation validation", stage: "mutation", err: validationErr, wantStatus: 400, wantCode: "VALIDATION_ERROR", wantMessage: validationErr.Error()},
				{name: "mutation not found", stage: "mutation", err: store.ErrNotFound, wantStatus: 404, wantCode: "NOT_FOUND", wantMessage: "not found"},
			} {
				transport, operation, tc := transport, operation, tc
				t.Run(transport+"/"+operation+"/"+tc.name, func(t *testing.T) {
					fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-mapping-"+transport+"-"+operation+"-"+strings.ReplaceAll(tc.name, " ", "-"))
					tool, args, before := prepareSprintLifecycleMCPFailureTarget(t, fx, operation)
					stream := subscribeSprintLifecycleMCPEvents(t, fx, fx.client)
					defer stream.close()
					injectSprintLifecycleMCPFailure(fx.wrapped, operation, tc.stage, tc.err)
					fx.wrapped.activateTrace()

					resp, out := callSprintLifecycleMCP(t, fx, fx.client, transport, tool, args)
					assertSprintLifecycleMCPError(t, transport, resp, out, tc.wantStatus, tc.wantCode, tc.wantMessage, nil)
					assertSprintLifecycleMCPTrace(t, fx.wrapped, sprintLifecycleMCPFailureTrace(operation, tc.stage)...)
					assertSprintLifecycleMCPFailurePersistence(t, fx, operation, tc.stage, before)
					assertSprintLifecycleMCPSilence(t, stream)
				})
			}
		}
	}
}

func TestSprintLifecycleMCPRejectsDisabledProjectAcrossTransports(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		for _, operation := range []string{"activate", "close", "delete"} {
			t.Run(transport+"/"+operation, func(t *testing.T) {
				fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-disabled-"+transport+"-"+operation)
				state := store.SprintStatePlanned
				if operation == "close" {
					state = store.SprintStateActive
				}
				sp := createSprintLifecycleMCPSprint(t, fx, "Dormant "+operation, state)
				before := getSprintLifecycleMCPSprint(t, fx, sp.ID)
				if err := fx.st.UpdateProjectSprintsEnabled(fx.ownerCtx, fx.project.ID, fx.ownerID, false); err != nil {
					t.Fatalf("disable sprints: %v", err)
				}
				stream := subscribeSprintLifecycleMCPEvents(t, fx, fx.client)
				defer stream.close()
				fx.wrapped.activateTrace()

				resp, out := callSprintLifecycleMCP(
					t, fx, fx.client, transport, "sprints_"+operation,
					map[string]any{"projectSlug": fx.project.Slug, "sprintId": sp.ID},
				)
				assertSprintLifecycleMCPError(
					t, transport, resp, out, http.StatusBadRequest, "VALIDATION_ERROR",
					store.ErrSprintsDisabled.Error(), map[string]any{"reason": "sprints_disabled"},
				)
				if after := getSprintLifecycleMCPSprint(t, fx, sp.ID); !reflect.DeepEqual(after, before) {
					t.Fatalf("disabled %s changed sprint: before=%+v after=%+v", operation, before, after)
				}
				assertSprintLifecycleMCPSilence(t, stream)
			})
		}
	}
}

func TestSprintLifecycleMCPDeleteFailureContract(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		for _, targetKind := range []string{"missing", "foreign"} {
			transport, targetKind := transport, targetKind
			t.Run(transport+"/"+targetKind, func(t *testing.T) {
				fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-delete-"+targetKind+"-"+transport)
				sprintID := int64(920001)
				var foreignProjectID int64
				if targetKind == "foreign" {
					other, err := fx.st.CreateProject(fx.ownerCtx, "mcp-lifecycle-delete-foreign-project-"+transport)
					if err != nil {
						t.Fatalf("CreateProject foreign: %v", err)
					}
					foreignProjectID = other.ID
					now := time.Now().UTC()
					sp, err := fx.st.CreateSprint(fx.ownerCtx, other.ID, "Foreign", now.Add(-time.Hour), now.Add(48*time.Hour))
					if err != nil {
						t.Fatalf("CreateSprint foreign: %v", err)
					}
					sprintID = sp.ID
				}
				stream := subscribeSprintLifecycleMCPEvents(t, fx, fx.client)
				defer stream.close()
				fx.wrapped.activateTrace()

				resp, out := callSprintLifecycleMCP(t, fx, fx.client, transport, "sprints_delete", map[string]any{"projectSlug": fx.project.Slug, "sprintId": sprintID})
				assertSprintLifecycleMCPError(t, transport, resp, out, http.StatusNotFound, "NOT_FOUND", "not found", nil)
				assertSprintLifecycleMCPTrace(t, fx.wrapped, "access", "role", "target")
				if fx.wrapped.readCalls != 1 || fx.wrapped.deleteCalls != 0 || fx.wrapped.deleteCommitted {
					t.Fatalf("delete failure observations=(reads=%d calls=%d committed=%v)", fx.wrapped.readCalls, fx.wrapped.deleteCalls, fx.wrapped.deleteCommitted)
				}
				if targetKind == "foreign" {
					sp := getSprintLifecycleMCPSprint(t, fx, sprintID)
					if sp.ProjectID != foreignProjectID {
						t.Fatalf("foreign sprint changed=%+v", sp)
					}
				}
				assertSprintLifecycleMCPSilence(t, stream)
			})
		}
	}
}

func TestSprintLifecycleMCPFreshRoleThresholdContract(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		for _, operation := range []string{"activate", "close", "delete"} {
			for _, role := range []store.ProjectRole{store.RoleViewer, store.RoleContributor} {
				transport, operation, role := transport, operation, role
				t.Run(transport+"/"+operation+"/"+string(role), func(t *testing.T) {
					fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-role-"+transport+"-"+operation+"-"+strings.ToLower(string(role)))
					tool, args, before := prepareSprintLifecycleMCPFailureTarget(t, fx, operation)
					pc := fx.projectPC
					pc.Role = store.RoleOwner
					fx.wrapped.accessOverride = &pc
					fx.wrapped.roleOverride = &role
					stream := subscribeSprintLifecycleMCPEvents(t, fx, fx.client)
					defer stream.close()
					fx.wrapped.activateTrace()

					resp, out := callSprintLifecycleMCP(t, fx, fx.client, transport, tool, args)
					assertSprintLifecycleMCPError(t, transport, resp, out, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
					assertSprintLifecycleMCPTrace(t, fx.wrapped, "access", "role")
					if fx.wrapped.rolePID != fx.project.ID || fx.wrapped.roleUID != fx.actorID || fx.wrapped.readCalls != 0 || fx.wrapped.activateCalls+fx.wrapped.closeCalls+fx.wrapped.deleteCalls != 0 {
						t.Fatalf("role observations=(project=%d actor=%d reads=%d mutations=%d)", fx.wrapped.rolePID, fx.wrapped.roleUID, fx.wrapped.readCalls, fx.wrapped.activateCalls+fx.wrapped.closeCalls+fx.wrapped.deleteCalls)
					}
					assertSprintLifecycleMCPFailurePersistence(t, fx, operation, "role", before)
					assertSprintLifecycleMCPSilence(t, stream)
				})
			}
		}
	}
}

func TestSprintLifecycleMCPAuthenticationAndCapabilityContract(t *testing.T) {
	toolNames := []string{"sprints_activate", "sprints_close", "sprints_delete"}
	for _, transport := range []string{"legacy", "jsonrpc"} {
		for _, tool := range toolNames {
			transport, tool := transport, tool
			t.Run("signed-out/"+transport+"/"+tool, func(t *testing.T) {
				fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-signed-out-"+transport+"-"+tool)
				stream := subscribeSprintLifecycleMCPEvents(t, fx, fx.client)
				defer stream.close()
				fx.wrapped.activateTrace()
				args := map[string]any{"projectSlug": fx.project.Slug, "sprintId": int64(1)}
				client := newStatelessClient(fx.ts)
				var resp *http.Response
				var out map[string]any
				if transport == "legacy" {
					resp, out = doMCP(t, client, fx.ts.URL+"/mcp", map[string]any{"tool": tool, "input": args})
					assertSprintLifecycleMCPError(t, transport, resp, out, http.StatusUnauthorized, "AUTH_REQUIRED", "Sign-in required for this tool", nil)
				} else {
					resp, out = callSprintDefinitionJSONRPCWithoutRetry(t, client, fx.ts.URL, map[string]any{
						"jsonrpc": "2.0", "id": 801, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args},
					})
					if resp.StatusCode != http.StatusUnauthorized || out != nil {
						t.Fatalf("signed-out JSON-RPC status=%d response=%+v, want bare 401", resp.StatusCode, out)
					}
				}
				assertSprintLifecycleMCPTrace(t, fx.wrapped)
				assertSprintLifecycleMCPSilence(t, stream)
			})

			t.Run("anonymous/"+transport+"/"+tool, func(t *testing.T) {
				ts, _, cleanup := newTestServer(t, "anonymous")
				defer cleanup()
				args := map[string]any{"projectSlug": "demo", "sprintId": int64(1)}
				var resp *http.Response
				var out map[string]any
				if transport == "legacy" {
					resp, out = doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{"tool": tool, "input": args})
				} else {
					resp, out = callSprintDefinitionJSONRPCWithoutRetry(t, ts.Client(), ts.URL, map[string]any{
						"jsonrpc": "2.0", "id": 802, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args},
					})
				}
				assertSprintLifecycleMCPError(t, transport, resp, out, http.StatusForbidden, "CAPABILITY_UNAVAILABLE", tool+" is unavailable in anonymous mode", nil)
			})

			t.Run("bootstrap/"+transport+"/"+tool, func(t *testing.T) {
				ts, _, cleanup := newTestServer(t, "full")
				defer cleanup()
				args := map[string]any{"projectSlug": "demo", "sprintId": int64(1)}
				var resp *http.Response
				var out map[string]any
				if transport == "legacy" {
					resp, out = doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{"tool": tool, "input": args})
				} else {
					resp, out = callSprintDefinitionJSONRPCWithoutRetry(t, ts.Client(), ts.URL, map[string]any{
						"jsonrpc": "2.0", "id": 803, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args},
					})
				}
				if transport == "jsonrpc" {
					if resp.StatusCode != http.StatusUnauthorized || out != nil {
						t.Fatalf("pre-bootstrap JSON-RPC status=%d response=%+v, want bare 401 before tool dispatch", resp.StatusCode, out)
					}
					return
				}
				assertSprintLifecycleMCPError(t, transport, resp, out, http.StatusForbidden, "CAPABILITY_UNAVAILABLE", tool+" is unavailable before bootstrap", nil)
			})
		}
	}
}

func TestSprintLifecycleMCPInputContract(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		for _, operation := range []string{"activate", "close", "delete"} {
			for _, tc := range []struct {
				name        string
				args        func(*sprintLifecycleMCPFixture) map[string]any
				wantMessage string
				wantDetails map[string]any
				wantTrace   []string
			}{
				{
					name: "missing project slug",
					args: func(_ *sprintLifecycleMCPFixture) map[string]any {
						return map[string]any{"sprintId": int64(1)}
					},
					wantMessage: "missing projectSlug", wantDetails: map[string]any{"field": "projectSlug"},
				},
				{
					name: "invalid sprint id",
					args: func(fx *sprintLifecycleMCPFixture) map[string]any {
						return map[string]any{"projectSlug": fx.project.Slug, "sprintId": int64(0)}
					},
					wantMessage: "invalid sprintId", wantDetails: map[string]any{"field": "sprintId"},
				},
			} {
				transport, operation, tc := transport, operation, tc
				t.Run(transport+"/"+operation+"/"+tc.name, func(t *testing.T) {
					fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-input-"+transport+"-"+operation+"-"+strings.ReplaceAll(tc.name, " ", "-"))
					stream := subscribeSprintLifecycleMCPEvents(t, fx, fx.client)
					defer stream.close()
					fx.wrapped.activateTrace()
					resp, out := callSprintLifecycleMCP(t, fx, fx.client, transport, "sprints_"+operation, tc.args(fx))
					wantMessage := tc.wantMessage
					if transport == "jsonrpc" && tc.name == "missing project slug" {
						wantMessage = "missing required field: projectSlug"
					}
					assertSprintLifecycleMCPError(t, transport, resp, out, http.StatusBadRequest, "VALIDATION_ERROR", wantMessage, tc.wantDetails)
					assertSprintLifecycleMCPTrace(t, fx.wrapped, tc.wantTrace...)
					assertSprintLifecycleMCPSilence(t, stream)
				})
			}
		}
	}
}

func TestSprintLifecycleMCPAliasesRemainDispatchOnly(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		transport := transport
		t.Run(transport, func(t *testing.T) {
			fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-alias-"+transport)
			target := createSprintLifecycleMCPSprint(t, fx, "Alias target", store.SprintStatePlanned)
			ownerEquivalent := store.RoleOwner
			fx.wrapped.roleOverride = &ownerEquivalent
			stream := subscribeSprintLifecycleMCPEvents(t, fx, fx.client)
			defer stream.close()

			fx.wrapped.activateTrace()
			resp, out := callSprintLifecycleMCP(t, fx, fx.client, transport, "sprints.activate", map[string]any{"projectSlug": fx.project.Slug, "sprintId": target.ID})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("activate alias status=%d response=%+v", resp.StatusCode, out)
			}
			active := getSprintLifecycleMCPSprint(t, fx, target.ID)
			assertSprintLifecycleMCPItem(t, sprintLifecycleMCPData(t, transport, out), fx.project.Slug, active)
			assertSprintLifecycleMCPTrace(t, fx.wrapped, "access", "role", "target", "activate", "projection")

			fx.wrapped.activateTrace()
			resp, out = callSprintLifecycleMCP(t, fx, fx.client, transport, "sprints.close", map[string]any{"projectSlug": fx.project.Slug, "sprintId": target.ID})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("close alias status=%d response=%+v", resp.StatusCode, out)
			}
			closed := getSprintLifecycleMCPSprint(t, fx, target.ID)
			assertSprintLifecycleMCPItem(t, sprintLifecycleMCPData(t, transport, out), fx.project.Slug, closed)
			assertSprintLifecycleMCPTrace(t, fx.wrapped, "access", "role", "target", "close", "projection")

			fx.wrapped.activateTrace()
			resp, out = callSprintLifecycleMCP(t, fx, fx.client, transport, "sprints.delete", map[string]any{"projectSlug": fx.project.Slug, "sprintId": target.ID})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("delete alias status=%d response=%+v", resp.StatusCode, out)
			}
			data := sprintLifecycleMCPData(t, transport, out)
			if data["status"] != "deleted" || data["projectSlug"] != fx.project.Slug || int64(data["sprintId"].(float64)) != target.ID {
				t.Fatalf("delete alias echo=%+v", data)
			}
			assertSprintLifecycleMCPTrace(t, fx.wrapped, "access", "role", "target", "delete")
			if fx.wrapped.roleUID != fx.actorID {
				t.Fatalf("alias fresh role actor=%d, want %d", fx.wrapped.roleUID, fx.actorID)
			}
			assertSprintLifecycleMCPSilence(t, stream)
		})
	}

	fx := newSprintLifecycleMCPFixture(t, "mcp-lifecycle-advertisement")
	resp, out := doJSONRPC(t, fx.client, fx.ts.URL, map[string]any{"jsonrpc": "2.0", "id": 804, "method": "tools/list", "params": map[string]any{}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tools/list status=%d response=%+v", resp.StatusCode, out)
	}
	result := out["result"].(map[string]any)
	tools := result["tools"].([]any)
	seen := map[string]bool{}
	for _, raw := range tools {
		seen[raw.(map[string]any)["name"].(string)] = true
	}
	for _, canonical := range []string{"sprints_activate", "sprints_close", "sprints_delete"} {
		if !seen[canonical] {
			t.Fatalf("canonical tool %q is not advertised", canonical)
		}
	}
	for _, alias := range []string{"sprints.activate", "sprints.close", "sprints.delete"} {
		if seen[alias] {
			t.Fatalf("compatibility alias %q is unexpectedly advertised", alias)
		}
	}
}
