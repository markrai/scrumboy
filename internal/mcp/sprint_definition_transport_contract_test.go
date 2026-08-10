package mcp_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
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

type sprintDefinitionMCPStore struct {
	*store.Store

	mu sync.Mutex

	active bool
	trace  []string

	accessErr     error
	roleErr       error
	roleOverride  *store.ProjectRole
	targetErr     error
	projectionErr error
	createErr     error
	updateErr     error

	accessSlug string
	accessMode store.Mode
	rolePID    int64
	roleUID    int64

	readCalls int
	readIDs   []int64

	createCalls int
	createPID   int64
	createName  string
	createStart time.Time
	createEnd   time.Time

	updateCalls int
	updateID    int64
	updateInput store.UpdateSprintInput
}

func (s *sprintDefinitionMCPStore) activate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = true
	s.trace = nil
	s.readCalls = 0
	s.readIDs = nil
	s.createCalls = 0
	s.updateCalls = 0
}

func (s *sprintDefinitionMCPStore) record(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return false
	}
	s.trace = append(s.trace, name)
	return true
}

func (s *sprintDefinitionMCPStore) snapshotTrace() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.trace...)
}

func (s *sprintDefinitionMCPStore) GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error) {
	if !s.record("access") {
		return s.Store.GetProjectContextBySlug(ctx, slug, mode)
	}
	s.mu.Lock()
	s.accessSlug = slug
	s.accessMode = mode
	err := s.accessErr
	s.mu.Unlock()
	if err != nil {
		return store.ProjectContext{}, err
	}
	return s.Store.GetProjectContextBySlug(ctx, slug, mode)
}

func (s *sprintDefinitionMCPStore) GetProjectRole(ctx context.Context, projectID, userID int64) (store.ProjectRole, error) {
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

func (s *sprintDefinitionMCPStore) GetSprintByID(ctx context.Context, sprintID int64) (store.Sprint, error) {
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

func (s *sprintDefinitionMCPStore) CreateSprint(ctx context.Context, projectID int64, name string, plannedStartAt, plannedEndAt time.Time) (store.Sprint, error) {
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

func (s *sprintDefinitionMCPStore) UpdateSprint(ctx context.Context, sprintID int64, in store.UpdateSprintInput) error {
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

type sprintDefinitionMCPFixture struct {
	ts      *httptest.Server
	db      *sql.DB
	st      *store.Store
	wrapped *sprintDefinitionMCPStore
	client  *http.Client
	ownerID int64
	ctx     context.Context
	project store.Project
}

func newSprintDefinitionMCPFixture(t *testing.T, name string) *sprintDefinitionMCPFixture {
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
	wrapped := &sprintDefinitionMCPStore{Store: st}
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

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	ctx := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctx, name)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	return &sprintDefinitionMCPFixture{
		ts: ts, db: sqlDB, st: st, wrapped: wrapped, client: client,
		ownerID: ownerID, ctx: ctx, project: project,
	}
}

func createSprintDefinitionMCPSprint(t *testing.T, fx *sprintDefinitionMCPFixture, name string) store.Sprint {
	t.Helper()
	start := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	sp, err := fx.st.CreateSprint(fx.ctx, fx.project.ID, name, start, start.Add(7*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint fixture: %v", err)
	}
	return sp
}

func subscribeSprintDefinitionMCPEvents(t *testing.T, fx *sprintDefinitionMCPFixture) *todoUpdateMCPEventStream {
	t.Helper()
	return subscribeTodoUpdateMCPEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")
}

func assertSprintDefinitionMCPTrace(t *testing.T, wrapped *sprintDefinitionMCPStore, want ...string) {
	t.Helper()
	if got := wrapped.snapshotTrace(); !reflect.DeepEqual(got, want) {
		t.Fatalf("store trace=%v, want %v", got, want)
	}
}

func assertSprintDefinitionMCPSilence(t *testing.T, stream *todoUpdateMCPEventStream) {
	t.Helper()
	if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
		t.Fatalf("MCP sprint mutation emitted realtime events: %+v", events)
	}
}

func sprintDefinitionMCPData(t *testing.T, transport string, response map[string]any) map[string]any {
	t.Helper()
	return todoLinkMCPData(t, transport, response)
}

func assertSprintDefinitionMCPItem(t *testing.T, data map[string]any, projectSlug string, sprintID, number int64, name string, start, end time.Time) map[string]any {
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
	if item["projectSlug"] != projectSlug || int64(item["sprintId"].(float64)) != sprintID || int64(item["number"].(float64)) != number || item["name"] != name || int64(item["plannedStartAt"].(float64)) != start.UnixMilli() || int64(item["plannedEndAt"].(float64)) != end.UnixMilli() || item["state"] != store.SprintStatePlanned || item["startedAt"] != nil || item["closedAt"] != nil || item["todoCount"] != nil {
		t.Fatalf("sprint projection=%+v", item)
	}
	return item
}

func callSprintDefinitionMCP(t *testing.T, fx *sprintDefinitionMCPFixture, transport, tool string, args map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	return callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, tool, args)
}

func callSprintDefinitionJSONRPCWithoutRetry(t *testing.T, client *http.Client, baseURL string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode JSON-RPC: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp/rpc", &buf)
	if err != nil {
		t.Fatalf("new JSON-RPC request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do JSON-RPC request: %v", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read JSON-RPC response: %v", err)
	}
	if len(bodyBytes) == 0 {
		return resp, nil
	}
	var out map[string]any
	if err := json.Unmarshal(bodyBytes, &out); err != nil {
		t.Fatalf("decode JSON-RPC response: %v body=%s", err, bodyBytes)
	}
	return resp, out
}

func TestSprintDefinitionMCPTransportAndDefinitionContracts(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport+"/create", func(t *testing.T) {
			fx := newSprintDefinitionMCPFixture(t, "mcp-sprint-definition-create-"+transport)
			stream := subscribeSprintDefinitionMCPEvents(t, fx)
			defer stream.close()
			start := time.Date(2026, time.August, 11, 13, 14, 15, 456000000, time.FixedZone("plus-two", 2*60*60))
			end := start.Add(9 * 24 * time.Hour)
			fx.wrapped.activate()

			resp, out := callSprintDefinitionMCP(t, fx, transport, "sprints_create", map[string]any{
				"projectSlug":    fx.project.Slug,
				"name":           "  MCP definition  ",
				"plannedStartAt": start.Format(time.RFC3339Nano),
				"plannedEndAt":   end.Format(time.RFC3339Nano),
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("create status=%d response=%+v", resp.StatusCode, out)
			}
			var stored store.Sprint
			if err := fx.db.QueryRow(`SELECT id FROM sprints WHERE project_id = ? AND name = ?`, fx.project.ID, "MCP definition").Scan(&stored.ID); err != nil {
				t.Fatalf("query created sprint ID: %v", err)
			}
			stored, err := fx.st.GetSprintByID(fx.ctx, stored.ID)
			if err != nil {
				t.Fatalf("GetSprintByID: %v", err)
			}
			assertSprintDefinitionMCPItem(t, sprintDefinitionMCPData(t, transport, out), fx.project.Slug, stored.ID, stored.Number, "MCP definition", stored.PlannedStartAt, stored.PlannedEndAt)
			assertSprintDefinitionMCPTrace(t, fx.wrapped, "access", "role", "create")
			if fx.wrapped.accessSlug != fx.project.Slug || fx.wrapped.accessMode != store.ModeFull || fx.wrapped.rolePID != fx.project.ID || fx.wrapped.roleUID != fx.ownerID {
				t.Fatalf("access/role args=(slug=%q mode=%q project=%d user=%d)", fx.wrapped.accessSlug, fx.wrapped.accessMode, fx.wrapped.rolePID, fx.wrapped.roleUID)
			}
			if fx.wrapped.createCalls != 1 || fx.wrapped.createPID != fx.project.ID || fx.wrapped.createName != "  MCP definition  " || !fx.wrapped.createStart.Equal(start) || !fx.wrapped.createEnd.Equal(end) {
				t.Fatalf("create call=(count=%d project=%d name=%q start=%s end=%s)", fx.wrapped.createCalls, fx.wrapped.createPID, fx.wrapped.createName, fx.wrapped.createStart, fx.wrapped.createEnd)
			}
			assertSprintDefinitionMCPSilence(t, stream)
		})

		t.Run(transport+"/update", func(t *testing.T) {
			fx := newSprintDefinitionMCPFixture(t, "mcp-sprint-definition-update-"+transport)
			offsetProject, err := fx.st.CreateProject(fx.ctx, "mcp-sprint-definition-offset-"+transport)
			if err != nil {
				t.Fatalf("CreateProject offset: %v", err)
			}
			offsetStart := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
			if _, err := fx.st.CreateSprint(fx.ctx, offsetProject.ID, "Offset", offsetStart, offsetStart.Add(24*time.Hour)); err != nil {
				t.Fatalf("CreateSprint offset: %v", err)
			}
			sp := createSprintDefinitionMCPSprint(t, fx, "Before MCP update")
			if sp.ID == sp.Number {
				t.Fatalf("identity fixture requires stored ID and local number to differ, sprint=%+v", sp)
			}
			stream := subscribeSprintDefinitionMCPEvents(t, fx)
			defer stream.close()
			newStart := sp.PlannedStartAt.Add(24 * time.Hour)
			newEnd := sp.PlannedEndAt.Add(48 * time.Hour)
			fx.wrapped.activate()

			resp, out := callSprintDefinitionMCP(t, fx, transport, "sprints_update", map[string]any{
				"projectSlug": fx.project.Slug,
				"sprintId":    sp.ID,
				"patch":       map[string]any{"name": "After MCP update", "plannedStartAt": newStart.UnixMilli(), "plannedEndAt": newEnd.UnixMilli()},
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("update status=%d response=%+v", resp.StatusCode, out)
			}
			assertSprintDefinitionMCPItem(t, sprintDefinitionMCPData(t, transport, out), fx.project.Slug, sp.ID, sp.Number, "After MCP update", newStart, newEnd)
			assertSprintDefinitionMCPTrace(t, fx.wrapped, "access", "role", "target", "update", "projection")
			if fx.wrapped.readCalls != 2 || !reflect.DeepEqual(fx.wrapped.readIDs, []int64{sp.ID, sp.ID}) || fx.wrapped.updateCalls != 1 || fx.wrapped.updateID != sp.ID {
				t.Fatalf("read/update=(reads=%d ids=%v calls=%d id=%d), want verified stored ID %d", fx.wrapped.readCalls, fx.wrapped.readIDs, fx.wrapped.updateCalls, fx.wrapped.updateID, sp.ID)
			}
			in := fx.wrapped.updateInput
			if in.Name == nil || *in.Name != "After MCP update" || in.PlannedStartAt == nil || !in.PlannedStartAt.Equal(newStart) || in.PlannedEndAt == nil || !in.PlannedEndAt.Equal(newEnd) {
				t.Fatalf("update input=%+v", in)
			}
			assertSprintDefinitionMCPSilence(t, stream)
		})
	}
}

func TestSprintDefinitionMCPEmptyPatchContract(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newSprintDefinitionMCPFixture(t, "mcp-sprint-definition-empty-"+transport)
			sp := createSprintDefinitionMCPSprint(t, fx, "Empty MCP patch")
			stream := subscribeSprintDefinitionMCPEvents(t, fx)
			defer stream.close()
			fx.wrapped.activate()

			resp, out := callSprintDefinitionMCP(t, fx, transport, "sprints_update", map[string]any{
				"projectSlug": fx.project.Slug, "sprintId": sp.ID, "patch": map[string]any{},
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("empty patch status=%d response=%+v", resp.StatusCode, out)
			}
			assertSprintDefinitionMCPItem(t, sprintDefinitionMCPData(t, transport, out), fx.project.Slug, sp.ID, sp.Number, sp.Name, sp.PlannedStartAt, sp.PlannedEndAt)
			assertSprintDefinitionMCPTrace(t, fx.wrapped, "access", "role", "target")
			if fx.wrapped.readCalls != 1 || fx.wrapped.updateCalls != 0 {
				t.Fatalf("empty patch reads=%d updates=%d, want one preparation read and no mutation/post-read", fx.wrapped.readCalls, fx.wrapped.updateCalls)
			}
			assertSprintDefinitionMCPSilence(t, stream)
		})
	}
}

func TestSprintDefinitionMCPCancellationContract(t *testing.T) {
	type testCase struct {
		operation string
		stage     string
		wantTrace []string
	}
	cases := []testCase{
		{operation: "create", stage: "access", wantTrace: []string{"access"}},
		{operation: "create", stage: "role", wantTrace: []string{"access", "role"}},
		{operation: "create", stage: "mutation", wantTrace: []string{"access", "role", "create"}},
		{operation: "update", stage: "access", wantTrace: []string{"access"}},
		{operation: "update", stage: "role", wantTrace: []string{"access", "role"}},
		{operation: "update", stage: "target", wantTrace: []string{"access", "role", "target"}},
		{operation: "update", stage: "mutation", wantTrace: []string{"access", "role", "target", "update"}},
		{operation: "update", stage: "projection", wantTrace: []string{"access", "role", "target", "update", "projection"}},
	}

	for _, transport := range []string{"legacy", "jsonrpc"} {
		for _, tc := range cases {
			t.Run(transport+"/"+tc.operation+"/"+tc.stage, func(t *testing.T) {
				fx := newSprintDefinitionMCPFixture(t, "mcp-sprint-definition-cancel-"+transport+"-"+tc.operation+"-"+tc.stage)
				var sp store.Sprint
				if tc.operation == "update" {
					sp = createSprintDefinitionMCPSprint(t, fx, "Before canceled MCP update")
				}
				stream := subscribeSprintDefinitionMCPEvents(t, fx)
				defer stream.close()
				switch tc.stage {
				case "access":
					fx.wrapped.accessErr = context.Canceled
				case "role":
					fx.wrapped.roleErr = context.Canceled
				case "target":
					fx.wrapped.targetErr = context.Canceled
				case "mutation":
					if tc.operation == "create" {
						fx.wrapped.createErr = context.Canceled
					} else {
						fx.wrapped.updateErr = context.Canceled
					}
				case "projection":
					fx.wrapped.projectionErr = context.Canceled
				}
				fx.wrapped.activate()

				tool := "sprints_create"
				args := map[string]any{
					"projectSlug": fx.project.Slug, "name": "Canceled MCP create",
					"plannedStartAt": "2026-08-10T12:00:00Z", "plannedEndAt": "2026-08-17T12:00:00Z",
				}
				if tc.operation == "update" {
					tool = "sprints_update"
					args = map[string]any{"projectSlug": fx.project.Slug, "sprintId": sp.ID, "patch": map[string]any{"name": "Canceled MCP update"}}
				}
				resp, out := callSprintDefinitionMCP(t, fx, transport, tool, args)
				publicErr := assertTodoLinkMCPError(t, transport, resp, out, http.StatusInternalServerError, "INTERNAL", "internal error")
				assertEmptyTodoLinkMCPDetails(t, publicErr)
				assertSprintDefinitionMCPTrace(t, fx.wrapped, tc.wantTrace...)
				assertSprintDefinitionMCPSilence(t, stream)

				if tc.operation == "create" {
					var count int
					if err := fx.db.QueryRow(`SELECT COUNT(*) FROM sprints WHERE project_id = ?`, fx.project.ID).Scan(&count); err != nil {
						t.Fatalf("count sprints: %v", err)
					}
					if count != 0 {
						t.Fatalf("canceled create rows=%d, want zero", count)
					}
					return
				}
				stored, err := fx.st.GetSprintByID(fx.ctx, sp.ID)
				if err != nil {
					t.Fatalf("GetSprintByID: %v", err)
				}
				if tc.stage == "projection" {
					if stored.Name != "Canceled MCP update" || fx.wrapped.updateCalls != 1 {
						t.Fatalf("projection cancellation stored=%+v updateCalls=%d, want committed update", stored, fx.wrapped.updateCalls)
					}
				} else if stored.Name != sp.Name {
					t.Fatalf("pre-commit cancellation stored=%+v, want unchanged name %q", stored, sp.Name)
				}
			})
		}
	}
}

func TestSprintDefinitionMCPCommittedCreateFailureContract(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newSprintDefinitionMCPFixture(t, "mcp-sprint-definition-committed-create-"+transport)
			const corruptValue = "mcp-create-return-read-failed"
			if _, err := fx.db.Exec(`
				CREATE TRIGGER sprint_definition_mcp_corrupt_created_row
				AFTER INSERT ON sprints
				BEGIN
					UPDATE sprints SET planned_start_at = '` + corruptValue + `' WHERE id = NEW.id;
				END
			`); err != nil {
				t.Fatalf("create post-insert fault trigger: %v", err)
			}
			stream := subscribeSprintDefinitionMCPEvents(t, fx)
			defer stream.close()
			fx.wrapped.activate()

			resp, out := callSprintDefinitionMCP(t, fx, transport, "sprints_create", map[string]any{
				"projectSlug":    fx.project.Slug,
				"name":           "Committed MCP failure",
				"plannedStartAt": "2026-08-10T12:00:00Z",
				"plannedEndAt":   "2026-08-17T12:00:00Z",
			})
			publicErr := assertTodoLinkMCPError(t, transport, resp, out, http.StatusInternalServerError, "INTERNAL", "internal error")
			assertEmptyTodoLinkMCPDetails(t, publicErr)
			assertSprintDefinitionMCPTrace(t, fx.wrapped, "access", "role", "create")
			if fx.wrapped.createCalls != 1 {
				t.Fatalf("create calls=%d, want one with no retry", fx.wrapped.createCalls)
			}
			var (
				count int
				raw   string
			)
			if err := fx.db.QueryRow(`SELECT COUNT(*), MIN(CAST(planned_start_at AS TEXT)) FROM sprints WHERE project_id = ? AND name = ?`, fx.project.ID, "Committed MCP failure").Scan(&count, &raw); err != nil {
				t.Fatalf("query committed sprint: %v", err)
			}
			if count != 1 || raw != corruptValue {
				t.Fatalf("committed sprint=(count=%d raw=%q), want one unreadable row", count, raw)
			}
			assertSprintDefinitionMCPSilence(t, stream)
		})
	}
}

func TestSprintDefinitionMCPAuthorityAndBoundaryContracts(t *testing.T) {
	t.Run("fresh role lookup is authoritative over access context", func(t *testing.T) {
		for _, transport := range []string{"legacy", "jsonrpc"} {
			t.Run(transport, func(t *testing.T) {
				fx := newSprintDefinitionMCPFixture(t, "mcp-sprint-definition-fresh-role-"+transport)
				viewer := store.RoleViewer
				fx.wrapped.roleOverride = &viewer
				stream := subscribeSprintDefinitionMCPEvents(t, fx)
				defer stream.close()
				fx.wrapped.activate()
				resp, out := callSprintDefinitionMCP(t, fx, transport, "sprints_create", map[string]any{
					"projectSlug": fx.project.Slug, "name": "Denied by fresh role",
					"plannedStartAt": "2026-08-10T12:00:00Z", "plannedEndAt": "2026-08-17T12:00:00Z",
				})
				publicErr := assertTodoLinkMCPError(t, transport, resp, out, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required")
				assertEmptyTodoLinkMCPDetails(t, publicErr)
				assertSprintDefinitionMCPTrace(t, fx.wrapped, "access", "role")
				if fx.wrapped.roleUID != fx.ownerID || fx.wrapped.createCalls != 0 {
					t.Fatalf("role user=%d createCalls=%d, want authenticated actor at role lookup and no mutation", fx.wrapped.roleUID, fx.wrapped.createCalls)
				}
				assertSprintDefinitionMCPSilence(t, stream)
			})
		}
	})

	t.Run("cross-project target is hidden before patch parsing", func(t *testing.T) {
		for _, transport := range []string{"legacy", "jsonrpc"} {
			t.Run(transport, func(t *testing.T) {
				fx := newSprintDefinitionMCPFixture(t, "mcp-sprint-definition-cross-project-"+transport)
				other, err := fx.st.CreateProject(fx.ctx, "mcp-sprint-definition-other-"+transport)
				if err != nil {
					t.Fatalf("CreateProject other: %v", err)
				}
				start := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
				foreign, err := fx.st.CreateSprint(fx.ctx, other.ID, "Foreign", start, start.Add(24*time.Hour))
				if err != nil {
					t.Fatalf("CreateSprint other: %v", err)
				}
				stream := subscribeSprintDefinitionMCPEvents(t, fx)
				defer stream.close()
				fx.wrapped.activate()
				resp, out := callSprintDefinitionMCP(t, fx, transport, "sprints_update", map[string]any{
					"projectSlug": fx.project.Slug, "sprintId": foreign.ID, "patch": map[string]any{"state": "ACTIVE"},
				})
				publicErr := assertTodoLinkMCPError(t, transport, resp, out, http.StatusNotFound, "NOT_FOUND", "not found")
				assertEmptyTodoLinkMCPDetails(t, publicErr)
				assertSprintDefinitionMCPTrace(t, fx.wrapped, "access", "role", "target")
				if fx.wrapped.updateCalls != 0 {
					t.Fatalf("cross-project update calls=%d", fx.wrapped.updateCalls)
				}
				assertSprintDefinitionMCPSilence(t, stream)
			})
		}
	})

	t.Run("state is not a public definition patch field", func(t *testing.T) {
		for _, transport := range []string{"legacy", "jsonrpc"} {
			t.Run(transport, func(t *testing.T) {
				fx := newSprintDefinitionMCPFixture(t, "mcp-sprint-definition-state-field-"+transport)
				sp := createSprintDefinitionMCPSprint(t, fx, "No MCP state patch")
				stream := subscribeSprintDefinitionMCPEvents(t, fx)
				defer stream.close()
				fx.wrapped.activate()
				resp, out := callSprintDefinitionMCP(t, fx, transport, "sprints_update", map[string]any{
					"projectSlug": fx.project.Slug, "sprintId": sp.ID, "patch": map[string]any{"state": "ACTIVE"},
				})
				publicErr := assertTodoLinkMCPError(t, transport, resp, out, http.StatusBadRequest, "VALIDATION_ERROR", "unsupported patch field")
				details, ok := publicErr["details"].(map[string]any)
				if !ok || details["field"] != "state" {
					t.Fatalf("state patch details=%+v", publicErr)
				}
				assertSprintDefinitionMCPTrace(t, fx.wrapped, "access", "role", "target")
				assertSprintDefinitionMCPSilence(t, stream)
			})
		}
	})

	t.Run("create requires RFC3339 strings before access", func(t *testing.T) {
		for _, transport := range []string{"legacy", "jsonrpc"} {
			t.Run(transport, func(t *testing.T) {
				fx := newSprintDefinitionMCPFixture(t, "mcp-sprint-definition-time-format-"+transport)
				stream := subscribeSprintDefinitionMCPEvents(t, fx)
				defer stream.close()
				fx.wrapped.activate()
				resp, out := callSprintDefinitionMCP(t, fx, transport, "sprints_create", map[string]any{
					"projectSlug": fx.project.Slug, "name": "Numeric MCP dates", "plannedStartAt": int64(1000), "plannedEndAt": int64(2000),
				})
				assertTodoLinkMCPError(t, transport, resp, out, http.StatusBadRequest, "VALIDATION_ERROR", "invalid input")
				assertSprintDefinitionMCPTrace(t, fx.wrapped)
				assertSprintDefinitionMCPSilence(t, stream)
			})
		}
	})
}

func TestSprintDefinitionMCPAuthenticationAndCapabilityContracts(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		for _, operation := range []string{"create", "update"} {
			t.Run("signed-out/"+transport+"/"+operation, func(t *testing.T) {
				fx := newSprintDefinitionMCPFixture(t, "mcp-sprint-definition-signed-out-"+transport+"-"+operation)
				tool := "sprints_create"
				args := map[string]any{}
				if operation == "update" {
					tool = "sprints_update"
				}
				client := newStatelessClient(fx.ts)
				var resp *http.Response
				var out map[string]any
				if transport == "legacy" {
					resp, out = doMCP(t, client, fx.ts.URL+"/mcp", map[string]any{"tool": tool, "input": args})
				} else {
					resp, out = callSprintDefinitionJSONRPCWithoutRetry(t, client, fx.ts.URL, map[string]any{
						"jsonrpc": "2.0", "id": 71, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args},
					})
				}
				if resp.StatusCode != http.StatusUnauthorized {
					t.Fatalf("signed-out status=%d response=%+v", resp.StatusCode, out)
				}
				if transport == "jsonrpc" {
					if out != nil {
						t.Fatalf("signed-out JSON-RPC response=%+v, want bare 401 before tool serialization", out)
					}
					assertSprintDefinitionMCPTrace(t, fx.wrapped)
					return
				}
				errBody, ok := out["error"].(map[string]any)
				if !ok || errBody["code"] != "AUTH_REQUIRED" || errBody["message"] != "Sign-in required for this tool" {
					t.Fatalf("signed-out response=%+v", out)
				}
				assertSprintDefinitionMCPTrace(t, fx.wrapped)
			})
		}
	}

	for _, transport := range []string{"legacy", "jsonrpc"} {
		for _, operation := range []string{"create", "update"} {
			t.Run("anonymous/"+transport+"/"+operation, func(t *testing.T) {
				ts, _, cleanup := newTestServer(t, "anonymous")
				defer cleanup()
				tool := "sprints_create"
				wantMessage := "sprints_create is unavailable in anonymous mode"
				args := map[string]any{
					"projectSlug": "anonymous-project", "name": "Anonymous sprint",
					"plannedStartAt": "2026-08-10T12:00:00Z", "plannedEndAt": "2026-08-17T12:00:00Z",
				}
				if operation == "update" {
					tool = "sprints_update"
					wantMessage = "sprints_update is unavailable in anonymous mode"
					args = map[string]any{"projectSlug": "anonymous-project", "sprintId": int64(1), "patch": map[string]any{}}
				}
				client := ts.Client()
				var resp *http.Response
				var out map[string]any
				if transport == "legacy" {
					resp, out = doMCP(t, client, ts.URL+"/mcp", map[string]any{"tool": tool, "input": args})
				} else {
					resp, out = doJSONRPC(t, client, ts.URL, map[string]any{
						"jsonrpc": "2.0", "id": 72, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args},
					})
				}
				publicErr := assertTodoLinkMCPError(t, transport, resp, out, http.StatusForbidden, "CAPABILITY_UNAVAILABLE", wantMessage)
				assertEmptyTodoLinkMCPDetails(t, publicErr)
			})
		}
	}
}

func TestSprintDefinitionMCPAliasesRemainDispatchOnly(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newSprintDefinitionMCPFixture(t, "mcp-sprint-definition-alias-"+transport)
			stream := subscribeSprintDefinitionMCPEvents(t, fx)
			defer stream.close()
			fx.wrapped.activate()
			resp, out := callSprintDefinitionMCP(t, fx, transport, "sprints.create", map[string]any{
				"projectSlug": fx.project.Slug, "name": "Alias sprint",
				"plannedStartAt": "2026-08-10T12:00:00Z", "plannedEndAt": "2026-08-17T12:00:00Z",
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("create alias status=%d response=%+v", resp.StatusCode, out)
			}
			data := sprintDefinitionMCPData(t, transport, out)
			item := data["sprint"].(map[string]any)
			sprintID := int64(item["sprintId"].(float64))
			assertSprintDefinitionMCPTrace(t, fx.wrapped, "access", "role", "create")

			fx.wrapped.activate()
			resp, out = callSprintDefinitionMCP(t, fx, transport, "sprints.update", map[string]any{
				"projectSlug": fx.project.Slug, "sprintId": sprintID, "patch": map[string]any{},
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("update alias status=%d response=%+v", resp.StatusCode, out)
			}
			assertSprintDefinitionMCPTrace(t, fx.wrapped, "access", "role", "target")
			assertSprintDefinitionMCPSilence(t, stream)
		})
	}

	fx := newSprintDefinitionMCPFixture(t, "mcp-sprint-definition-advertisement")
	resp, out := doJSONRPC(t, fx.client, fx.ts.URL, map[string]any{"jsonrpc": "2.0", "id": 73, "method": "tools/list", "params": map[string]any{}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tools/list status=%d response=%+v", resp.StatusCode, out)
	}
	result := out["result"].(map[string]any)
	tools := result["tools"].([]any)
	seen := map[string]bool{}
	for _, raw := range tools {
		seen[raw.(map[string]any)["name"].(string)] = true
	}
	if !seen["sprints_create"] || !seen["sprints_update"] || seen["sprints.create"] || seen["sprints.update"] {
		t.Fatalf("tool advertisement create/update aliases=%+v", seen)
	}
}

func TestSprintDefinitionMCPDefinitionFieldsRespectExistingSprintState(t *testing.T) {
	start := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name        string
		state       string
		patch       map[string]any
		wantSuccess bool
		wantName    string
		wantMessage string
	}{
		{name: "planned name", state: store.SprintStatePlanned, patch: map[string]any{"name": "Planned renamed"}, wantSuccess: true, wantName: "Planned renamed"},
		{name: "active end", state: store.SprintStateActive, patch: map[string]any{"plannedEndAt": start.Add(9 * 24 * time.Hour).UnixMilli()}, wantSuccess: true, wantName: "State fixture"},
		{name: "active name rejected", state: store.SprintStateActive, patch: map[string]any{"name": "No active rename"}, wantName: "State fixture", wantMessage: "validation: only endAt can be updated for ACTIVE sprint"},
		{name: "closed name", state: store.SprintStateClosed, patch: map[string]any{"name": "Closed renamed"}, wantSuccess: true, wantName: "Closed renamed"},
		{name: "closed end rejected", state: store.SprintStateClosed, patch: map[string]any{"plannedEndAt": start.Add(10 * 24 * time.Hour).UnixMilli()}, wantName: "State fixture", wantMessage: "validation: dates cannot be updated for CLOSED sprint"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newSprintDefinitionMCPFixture(t, "mcp-sprint-definition-state-"+strings.ReplaceAll(tc.name, " ", "-"))
			sp, err := fx.st.CreateSprint(fx.ctx, fx.project.ID, "State fixture", start, start.Add(7*24*time.Hour))
			if err != nil {
				t.Fatalf("CreateSprint: %v", err)
			}
			if tc.state != store.SprintStatePlanned {
				var closed any
				if tc.state == store.SprintStateClosed {
					closed = start.Add(24 * time.Hour).UnixMilli()
				}
				if _, err := fx.db.Exec(`UPDATE sprints SET state = ?, started_at = ?, closed_at = ? WHERE id = ?`, tc.state, start.UnixMilli(), closed, sp.ID); err != nil {
					t.Fatalf("set state fixture: %v", err)
				}
			}
			stream := subscribeSprintDefinitionMCPEvents(t, fx)
			defer stream.close()
			fx.wrapped.activate()
			resp, out := callSprintDefinitionMCP(t, fx, "legacy", "sprints_update", map[string]any{
				"projectSlug": fx.project.Slug, "sprintId": sp.ID, "patch": tc.patch,
			})
			if tc.wantSuccess {
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("state update status=%d response=%+v", resp.StatusCode, out)
				}
				assertSprintDefinitionMCPTrace(t, fx.wrapped, "access", "role", "target", "update", "projection")
			} else {
				assertTodoLinkMCPError(t, "legacy", resp, out, http.StatusBadRequest, "VALIDATION_ERROR", tc.wantMessage)
				assertSprintDefinitionMCPTrace(t, fx.wrapped, "access", "role", "target", "update")
			}
			stored, err := fx.st.GetSprintByID(fx.ctx, sp.ID)
			if err != nil {
				t.Fatalf("GetSprintByID: %v", err)
			}
			if stored.Name != tc.wantName || stored.State != tc.state {
				t.Fatalf("stored sprint=%+v, want name=%q state=%q", stored, tc.wantName, tc.state)
			}
			assertSprintDefinitionMCPSilence(t, stream)
		})
	}
}

func TestSprintDefinitionMCPRejectsDisabledProjectAcrossTransports(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newSprintDefinitionMCPFixture(t, "mcp-sprint-definition-disabled-"+transport)
			sp := createSprintDefinitionMCPSprint(t, fx, "Dormant")
			if err := fx.st.UpdateProjectSprintsEnabled(fx.ctx, fx.project.ID, fx.ownerID, false); err != nil {
				t.Fatalf("disable sprints: %v", err)
			}

			for _, tc := range []struct {
				name string
				tool string
				args map[string]any
			}{
				{
					name: "create", tool: "sprints_create",
					args: map[string]any{
						"projectSlug": fx.project.Slug, "name": "Blocked",
						"plannedStartAt": "2026-08-10T12:00:00Z", "plannedEndAt": "2026-08-17T12:00:00Z",
					},
				},
				{
					name: "update", tool: "sprints_update",
					args: map[string]any{"projectSlug": fx.project.Slug, "sprintId": sp.ID, "patch": map[string]any{"name": "Blocked rename"}},
				},
			} {
				t.Run(tc.name, func(t *testing.T) {
					fx.wrapped.activate()
					resp, out := callSprintDefinitionMCP(t, fx, transport, tc.tool, tc.args)
					publicErr := assertTodoLinkMCPError(
						t, transport, resp, out, http.StatusBadRequest, "VALIDATION_ERROR", store.ErrSprintsDisabled.Error(),
					)
					details, ok := publicErr["details"].(map[string]any)
					if !ok || details["reason"] != "sprints_disabled" {
						t.Fatalf("disabled details=%+v", publicErr)
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
		})
	}
}

func TestSprintDefinitionMCPCreateReturnFailureDoesNotLeakCause(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newSprintDefinitionMCPFixture(t, "mcp-sprint-definition-private-cause-"+transport)
			privateCause := fmt.Errorf("private sprint diagnostic %s", transport)
			fx.wrapped.createErr = privateCause
			stream := subscribeSprintDefinitionMCPEvents(t, fx)
			defer stream.close()
			fx.wrapped.activate()
			resp, out := callSprintDefinitionMCP(t, fx, transport, "sprints_create", map[string]any{
				"projectSlug": fx.project.Slug, "name": "Private failure",
				"plannedStartAt": "2026-08-10T12:00:00Z", "plannedEndAt": "2026-08-17T12:00:00Z",
			})
			publicErr := assertTodoLinkMCPError(t, transport, resp, out, http.StatusInternalServerError, "INTERNAL", "internal error")
			assertEmptyTodoLinkMCPDetails(t, publicErr)
			encoded, err := json.Marshal(out)
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
			if bytes.Contains(encoded, []byte(privateCause.Error())) {
				t.Fatalf("public response leaked private cause: %s", encoded)
			}
			assertSprintDefinitionMCPTrace(t, fx.wrapped, "access", "role", "create")
			assertSprintDefinitionMCPSilence(t, stream)
		})
	}
}
