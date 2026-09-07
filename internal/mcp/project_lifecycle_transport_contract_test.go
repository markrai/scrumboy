package mcp_test

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
	"sync"
	"testing"

	"scrumboy/internal/db"
	"scrumboy/internal/httpapi"
	"scrumboy/internal/mcp"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

type projectLifecycleMCPStore struct {
	*store.Store

	mu sync.Mutex

	active  bool
	trace   []string
	mutated bool

	postReadErr error
	updateCalls int
	deleteCalls int
}

func (s *projectLifecycleMCPStore) activate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = true
	s.trace = nil
	s.mutated = false
	s.updateCalls = 0
	s.deleteCalls = 0
}

func (s *projectLifecycleMCPStore) deactivate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = false
}

func (s *projectLifecycleMCPStore) record(stage string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return false
	}
	s.trace = append(s.trace, stage)
	return true
}

func (s *projectLifecycleMCPStore) traceSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.trace...)
}

func (s *projectLifecycleMCPStore) callCounts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateCalls, s.deleteCalls
}

func (s *projectLifecycleMCPStore) CreateProject(ctx context.Context, name string) (store.Project, error) {
	s.record("create-project")
	return s.Store.CreateProject(ctx, name)
}

func (s *projectLifecycleMCPStore) GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error) {
	s.record("access")
	return s.Store.GetProjectContextBySlug(ctx, slug, mode)
}

func (s *projectLifecycleMCPStore) CheckCanManageProject(ctx context.Context, projectID, userID int64) error {
	s.record("manage")
	return s.Store.CheckCanManageProject(ctx, projectID, userID)
}

func (s *projectLifecycleMCPStore) UpdateProjectPatch(ctx context.Context, projectID, userID int64, patch store.UpdateProjectPatch) error {
	active := s.record("update-project")
	if active {
		s.mu.Lock()
		s.updateCalls++
		s.mu.Unlock()
	}
	err := s.Store.UpdateProjectPatch(ctx, projectID, userID, patch)
	if err == nil && active {
		s.mu.Lock()
		s.mutated = true
		s.mu.Unlock()
	}
	return err
}

func (s *projectLifecycleMCPStore) GetProject(ctx context.Context, projectID int64) (store.Project, error) {
	active := s.record("post-read")
	if active {
		s.mu.Lock()
		mutated := s.mutated
		injected := s.postReadErr
		s.mu.Unlock()
		if mutated && injected != nil {
			return store.Project{}, injected
		}
	}
	return s.Store.GetProject(ctx, projectID)
}

func (s *projectLifecycleMCPStore) DeleteProject(ctx context.Context, projectID, userID int64) (store.DeletedProjectSnapshot, error) {
	active := s.record("delete-project")
	if active {
		s.mu.Lock()
		s.deleteCalls++
		s.mu.Unlock()
	}
	deleted, err := s.Store.DeleteProject(ctx, projectID, userID)
	if err == nil && active {
		s.mu.Lock()
		s.mutated = true
		s.mu.Unlock()
	}
	return deleted, err
}

type projectLifecycleMCPFixture struct {
	ts      *httptest.Server
	db      *sql.DB
	store   *store.Store
	wrapped *projectLifecycleMCPStore
	client  *http.Client
	ownerID int64
	ctx     context.Context
}

func newProjectLifecycleMCPFixture(t *testing.T) *projectLifecycleMCPFixture {
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
	wrapped := &projectLifecycleMCPStore{Store: st}
	adapter := mcp.New(wrapped, mcp.Options{Mode: "full"})
	server := httpapi.NewServer(wrapped, httpapi.Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		MCPHandler:     adapter,
	})
	ts := httptest.NewServer(server)
	t.Cleanup(func() {
		ts.Close()
		_ = sqlDB.Close()
	})
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	ctx := store.WithUserID(context.Background(), ownerID)
	return &projectLifecycleMCPFixture{ts: ts, db: sqlDB, store: st, wrapped: wrapped, client: client, ownerID: ownerID, ctx: ctx}
}

func assertProjectLifecycleMCPTrace(t *testing.T, wrapped *projectLifecycleMCPStore, want ...string) {
	t.Helper()
	if got := wrapped.traceSnapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("project lifecycle MCP trace=%v want=%v", got, want)
	}
}

func projectLifecycleMCPData(t *testing.T, transport string, out map[string]any) map[string]any {
	t.Helper()
	if transport == "legacy" {
		if out["ok"] != true {
			t.Fatalf("legacy response not successful: %+v", out)
		}
		meta, ok := out["meta"].(map[string]any)
		if !ok || len(meta) != 0 {
			t.Fatalf("legacy metadata=%+v want empty", out["meta"])
		}
		data, ok := out["data"].(map[string]any)
		if !ok {
			t.Fatalf("legacy data=%T %+v", out["data"], out)
		}
		return data
	}
	result, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("JSON-RPC result=%T %+v", out["result"], out)
	}
	if result["isError"] == true {
		t.Fatalf("JSON-RPC result unexpectedly failed: %+v", result)
	}
	data, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("JSON-RPC structuredContent=%T %+v", result["structuredContent"], result)
	}
	return data
}

func assertProjectLifecycleMCPSilence(t *testing.T, stream *todoUpdateMCPEventStream) {
	t.Helper()
	if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
		t.Fatalf("MCP project lifecycle emitted realtime events: %+v", events)
	}
}

func callProjectLifecycleJSONRPCNoRetry(t *testing.T, client *http.Client, baseURL string, body any) (*http.Response, []byte) {
	t.Helper()
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(body); err != nil {
		t.Fatalf("encode JSON-RPC body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp/rpc", &encoded)
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
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read JSON-RPC response: %v", err)
	}
	return resp, raw
}

func TestProjectLifecycleMCPTransportsCanonicalCreateAndAliasAbsence(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport+" canonical create", func(t *testing.T) {
			fx := newProjectLifecycleMCPFixture(t)
			fx.wrapped.activate()
			resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, "projects_create", map[string]any{"name": "  MCP Lifecycle Create  "})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("create HTTP status=%d response=%+v", resp.StatusCode, out)
			}
			assertProjectLifecycleMCPTrace(t, fx.wrapped, "create-project")
			data := projectLifecycleMCPData(t, transport, out)
			project, ok := data["project"].(map[string]any)
			if !ok {
				t.Fatalf("create project result=%T %+v", data["project"], data)
			}
			if project["name"] != "MCP Lifecycle Create" || project["projectSlug"] != "mcp-lifecycle-create" || project["role"] != "maintainer" {
				t.Fatalf("create projection=%+v", project)
			}
			if _, exists := project["image"]; exists {
				t.Fatalf("create projection must be image-free: %+v", project)
			}
			projectID := int64(project["projectId"].(float64))
			if got := projectLifecycleMCPCount(t, fx.db, `SELECT COUNT(*) FROM project_members WHERE project_id = ? AND user_id = ? AND role = 'maintainer'`, projectID, fx.ownerID); got != 1 {
				t.Fatalf("creator membership count=%d want=1", got)
			}
			if got := projectLifecycleMCPCount(t, fx.db, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action = 'project_created' AND actor_user_id = ?`, projectID, fx.ownerID); got != 1 {
				t.Fatalf("creator audit count=%d want=1", got)
			}
		})

		for _, alias := range []string{"projects.create", "projects.update", "projects.delete"} {
			t.Run(transport+" rejects "+alias, func(t *testing.T) {
				fx := newProjectLifecycleMCPFixture(t)
				fx.wrapped.activate()
				resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, alias, map[string]any{})
				if transport == "legacy" {
					if resp.StatusCode != http.StatusNotFound {
						t.Fatalf("legacy alias status=%d response=%+v", resp.StatusCode, out)
					}
					errObj := out["error"].(map[string]any)
					if errObj["code"] != "NOT_FOUND" || errObj["message"] != "tool not found" {
						t.Fatalf("legacy alias error=%+v", errObj)
					}
				} else {
					if resp.StatusCode != http.StatusOK {
						t.Fatalf("JSON-RPC alias HTTP status=%d response=%+v", resp.StatusCode, out)
					}
					result := out["result"].(map[string]any)
					if result["isError"] != true {
						t.Fatalf("JSON-RPC alias result=%+v", result)
					}
					structured := result["structuredContent"].(map[string]any)
					if structured["code"] != "NOT_FOUND" || structured["message"] != "tool not found" {
						t.Fatalf("JSON-RPC alias error=%+v", structured)
					}
				}
				assertProjectLifecycleMCPTrace(t, fx.wrapped)
			})
		}
	}
}

func TestProjectLifecycleMCPRegistrySchemasAndCapabilityGates(t *testing.T) {
	t.Run("canonical schemas advertised and mutation aliases absent", func(t *testing.T) {
		ts, _, cleanup := newTestServer(t, "full")
		defer cleanup()
		_, out := doJSONRPC(t, newStatelessClient(ts), ts.URL, map[string]any{
			"jsonrpc": "2.0",
			"id":      24,
			"method":  "tools/list",
		})
		result := out["result"].(map[string]any)
		tools := result["tools"].([]any)
		byName := make(map[string]map[string]any, len(tools))
		for _, raw := range tools {
			tool := raw.(map[string]any)
			byName[tool["name"].(string)] = tool
		}
		for alias := range map[string]struct{}{"projects.create": {}, "projects.update": {}, "projects.delete": {}} {
			if _, ok := byName[alias]; ok {
				t.Fatalf("dotted mutation alias unexpectedly advertised: %q", alias)
			}
		}
		wantRequired := map[string][]any{
			"projects_create": {"name"},
			"projects_update": {"projectSlug", "patch"},
			"projects_delete": {"projectSlug"},
		}
		for name, required := range wantRequired {
			tool, ok := byName[name]
			if !ok {
				t.Fatalf("canonical tool %q missing from tools/list", name)
			}
			schema := tool["inputSchema"].(map[string]any)
			if schema["type"] != "object" || schema["additionalProperties"] != false || !reflect.DeepEqual(schema["required"], required) {
				t.Fatalf("%s schema=%+v want required=%v strict object", name, schema, required)
			}
		}
		updateSchema := byName["projects_update"]["inputSchema"].(map[string]any)
		patch := updateSchema["properties"].(map[string]any)["patch"].(map[string]any)
		if patch["type"] != "object" || patch["additionalProperties"] != false {
			t.Fatalf("projects_update patch schema=%+v", patch)
		}
		patchProperties := patch["properties"].(map[string]any)
		if got := []string{patchProperties["name"].(map[string]any)["type"].(string), patchProperties["defaultSprintWeeks"].(map[string]any)["type"].(string)}; !reflect.DeepEqual(got, []string{"string", "integer"}) {
			t.Fatalf("projects_update patch property types=%v", got)
		}
	})

	for _, modeCase := range []struct {
		name    string
		mode    string
		message func(string) string
	}{
		{name: "anonymous mode", mode: "anonymous", message: func(tool string) string { return tool + " is unavailable in anonymous mode" }},
		{name: "full mode before bootstrap", mode: "full", message: func(tool string) string { return tool + " is unavailable before bootstrap" }},
	} {
		for _, transport := range []string{"legacy", "jsonrpc"} {
			if modeCase.mode == "full" && transport == "jsonrpc" {
				continue
			}
			for _, tool := range []string{"projects_create", "projects_update", "projects_delete"} {
				t.Run(modeCase.name+" "+transport+" "+tool, func(t *testing.T) {
					ts, _, cleanup := newTestServer(t, modeCase.mode)
					defer cleanup()
					args := map[string]any{}
					if transport == "jsonrpc" {
						switch tool {
						case "projects_create":
							args = map[string]any{"name": "schema-valid"}
						case "projects_update":
							args = map[string]any{"projectSlug": "schema-valid", "patch": map[string]any{"name": "schema-valid"}}
						case "projects_delete":
							args = map[string]any{"projectSlug": "schema-valid"}
						}
					}
					resp, out := callTodoUpdateMCP(t, newStatelessClient(ts), ts.URL, transport, tool, args)
					assertTodoLinkMCPError(t, transport, resp, out, http.StatusForbidden, "CAPABILITY_UNAVAILABLE", modeCase.message(tool))
				})
			}
		}
	}

	for _, modeCase := range []struct {
		name string
		mode string
	}{{name: "anonymous mode", mode: "anonymous"}} {
		for _, toolCase := range []struct {
			tool    string
			field   string
			message string
		}{
			{tool: "projects_create", field: "name", message: "missing required field: name"},
			{tool: "projects_update", field: "projectSlug", message: "missing required field: projectSlug"},
			{tool: "projects_delete", field: "projectSlug", message: "missing required field: projectSlug"},
		} {
			t.Run(modeCase.name+" JSON-RPC schema precedes "+toolCase.tool+" capability", func(t *testing.T) {
				ts, _, cleanup := newTestServer(t, modeCase.mode)
				defer cleanup()
				resp, out := callTodoUpdateMCP(t, newStatelessClient(ts), ts.URL, "jsonrpc", toolCase.tool, map[string]any{})
				publicError := assertTodoLinkMCPError(t, "jsonrpc", resp, out, http.StatusBadRequest, "VALIDATION_ERROR", toolCase.message)
				details := publicError["details"].(map[string]any)
				if details["field"] != toolCase.field {
					t.Fatalf("JSON-RPC missing-field details=%+v want field=%q", details, toolCase.field)
				}
			})
		}
	}

	t.Run("full mode JSON-RPC transport auth precedes bootstrap capability and schema", func(t *testing.T) {
		ts, _, cleanup := newTestServer(t, "full")
		defer cleanup()
		resp, raw := callProjectLifecycleJSONRPCNoRetry(t, newStatelessClient(ts), ts.URL, map[string]any{
			"jsonrpc": "2.0",
			"id":      24,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      "projects_create",
				"arguments": map[string]any{},
			},
		})
		if resp.StatusCode != http.StatusUnauthorized || len(raw) != 0 {
			t.Fatalf("pre-bootstrap JSON-RPC status=%d body=%q", resp.StatusCode, raw)
		}
		if got := resp.Header.Get("WWW-Authenticate"); got == "" {
			t.Fatal("pre-bootstrap JSON-RPC 401 missing WWW-Authenticate challenge")
		}
	})
}

func TestProjectLifecycleMCPUpdateOrderingPostReadFailureAndSilence(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport+" success", func(t *testing.T) {
			fx := newProjectLifecycleMCPFixture(t)
			project, err := fx.store.CreateProject(fx.ctx, "MCP Update Sequence "+transport)
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			stream := subscribeTodoUpdateMCPEvents(t, fx.client, fx.ts.URL+"/api/board/"+project.Slug+"/events")
			defer stream.close()

			fx.wrapped.activate()
			resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, "projects_update", map[string]any{
				"projectSlug": " " + project.Slug + " ",
				"patch": map[string]any{
					"name":               "MCP Updated " + transport,
					"defaultSprintWeeks": 1,
				},
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("update HTTP status=%d response=%+v", resp.StatusCode, out)
			}
			assertProjectLifecycleMCPTrace(t, fx.wrapped, "access", "manage", "update-project", "post-read")
			updateCalls, _ := fx.wrapped.callCounts()
			if updateCalls != 1 {
				t.Fatalf("update calls=%d want=1", updateCalls)
			}
			data := projectLifecycleMCPData(t, transport, out)
			returned := data["project"].(map[string]any)
			if returned["projectSlug"] != project.Slug || returned["name"] != "MCP Updated "+transport || returned["defaultSprintWeeks"] != float64(1) || returned["role"] != "maintainer" {
				t.Fatalf("updated projection=%+v", returned)
			}
			if _, exists := returned["image"]; exists {
				t.Fatalf("updated projection must be image-free: %+v", returned)
			}
			assertProjectLifecycleMCPSilence(t, stream)
		})

		t.Run(transport+" validation precedes missing project", func(t *testing.T) {
			fx := newProjectLifecycleMCPFixture(t)
			fx.wrapped.activate()
			resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, "projects_update", map[string]any{
				"projectSlug": "missing-project",
				"patch":       map[string]any{"defaultSprintWeeks": 3},
			})
			assertTodoLinkMCPError(t, transport, resp, out, http.StatusBadRequest, "VALIDATION_ERROR", "defaultSprintWeeks must be 1 or 2")
			assertProjectLifecycleMCPTrace(t, fx.wrapped)
		})

		t.Run(transport+" committed mutation then post-read error", func(t *testing.T) {
			fx := newProjectLifecycleMCPFixture(t)
			project, err := fx.store.CreateProject(fx.ctx, "MCP Post Read "+transport)
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			stream := subscribeTodoUpdateMCPEvents(t, fx.client, fx.ts.URL+"/api/board/"+project.Slug+"/events")
			defer stream.close()
			fx.wrapped.postReadErr = errors.New("MCP lifecycle post-read failed")
			fx.wrapped.activate()
			resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, "projects_update", map[string]any{
				"projectSlug": project.Slug,
				"patch":       map[string]any{"name": "Committed Despite Projection Failure"},
			})
			assertTodoLinkMCPError(t, transport, resp, out, http.StatusInternalServerError, "INTERNAL", "internal error")
			assertProjectLifecycleMCPTrace(t, fx.wrapped, "access", "manage", "update-project", "post-read")
			updateCalls, _ := fx.wrapped.callCounts()
			if updateCalls != 1 {
				t.Fatalf("post-read update calls=%d want=1", updateCalls)
			}
			fx.wrapped.deactivate()
			persisted, err := fx.store.GetProject(context.Background(), project.ID)
			if err != nil {
				t.Fatalf("GetProject: %v", err)
			}
			if persisted.Name != "Committed Despite Projection Failure" {
				t.Fatalf("persisted name=%q", persisted.Name)
			}
			if got := projectLifecycleMCPCount(t, fx.db, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action = 'project_renamed'`, project.ID); got != 1 {
				t.Fatalf("committed rename audits=%d want=1", got)
			}
			assertProjectLifecycleMCPSilence(t, stream)
		})
	}
}

func TestProjectLifecycleMCPDeleteOrderingProjectionAndSilence(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newProjectLifecycleMCPFixture(t)
			project, err := fx.store.CreateProject(fx.ctx, "MCP Delete Sequence "+transport)
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			stream := subscribeTodoUpdateMCPEvents(t, fx.client, fx.ts.URL+"/api/board/"+project.Slug+"/events")
			defer stream.close()
			fx.wrapped.activate()
			resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, "projects_delete", map[string]any{"projectSlug": " " + project.Slug + " "})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("delete HTTP status=%d response=%+v", resp.StatusCode, out)
			}
			assertProjectLifecycleMCPTrace(t, fx.wrapped, "access", "manage", "delete-project")
			_, deleteCalls := fx.wrapped.callCounts()
			if deleteCalls != 1 {
				t.Fatalf("delete calls=%d want=1", deleteCalls)
			}
			data := projectLifecycleMCPData(t, transport, out)
			if data["status"] != "deleted" || data["projectSlug"] != project.Slug || data["projectId"] != float64(project.ID) {
				t.Fatalf("delete projection=%+v", data)
			}
			assertProjectLifecycleMCPSilence(t, stream)

			fx.wrapped.activate()
			resp, out = callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, "projects_delete", map[string]any{"projectSlug": project.Slug})
			assertTodoLinkMCPError(t, transport, resp, out, http.StatusNotFound, "NOT_FOUND", "not found")
			assertProjectLifecycleMCPTrace(t, fx.wrapped, "access")
			_, deleteCalls = fx.wrapped.callCounts()
			if deleteCalls != 0 {
				t.Fatalf("repeat delete calls=%d want=0", deleteCalls)
			}
		})
	}
}

func projectLifecycleMCPCount(t *testing.T, sqlDB *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count lifecycle rows: %v", err)
	}
	return count
}
