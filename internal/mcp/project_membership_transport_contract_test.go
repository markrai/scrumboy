package mcp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"scrumboy/internal/db"
	"scrumboy/internal/httpapi"
	"scrumboy/internal/mcp"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

func membershipMCPData(t *testing.T, transport string, response map[string]any) map[string]any {
	t.Helper()
	if transport == "legacy" {
		if got, want := sortedMapKeys(response), []string{"data", "meta", "ok"}; !reflect.DeepEqual(got, want) || response["ok"] != true {
			t.Fatalf("legacy success envelope=%+v keys=%v want=%v", response, got, want)
		}
		meta, ok := response["meta"].(map[string]any)
		if !ok || len(meta) != 0 {
			t.Fatalf("legacy metadata=%+v want empty object", response["meta"])
		}
		data, ok := response["data"].(map[string]any)
		if !ok {
			t.Fatalf("legacy data=%+v want object", response["data"])
		}
		return data
	}

	if got, want := sortedMapKeys(response), []string{"id", "jsonrpc", "result"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON-RPC envelope=%+v keys=%v want=%v", response, got, want)
	}
	if response["jsonrpc"] != "2.0" || response["id"] != float64(14) {
		t.Fatalf("JSON-RPC identity=%+v", response)
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("JSON-RPC result=%+v want object", response["result"])
	}
	if got, want := sortedMapKeys(result), []string{"content", "structuredContent"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON-RPC result=%+v keys=%v want=%v", result, got, want)
	}
	data, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("JSON-RPC structuredContent=%+v want object", result["structuredContent"])
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("JSON-RPC content=%+v", result["content"])
	}
	textBlock, ok := content[0].(map[string]any)
	if !ok || textBlock["type"] != "text" {
		t.Fatalf("JSON-RPC content block=%+v", content[0])
	}
	text, ok := textBlock["text"].(string)
	if !ok {
		t.Fatalf("JSON-RPC text=%+v", textBlock["text"])
	}
	var textData map[string]any
	if err := json.Unmarshal([]byte(text), &textData); err != nil {
		t.Fatalf("decode JSON-RPC text content: %v", err)
	}
	if !reflect.DeepEqual(textData, data) {
		t.Fatalf("JSON-RPC text/structured divergence: text=%+v structured=%+v", textData, data)
	}
	return data
}

func membershipMCPAuditCount(t *testing.T, sqlDB *sql.DB, projectID, targetUserID int64, action string) int {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(`
		SELECT COUNT(*)
		FROM audit_events
		WHERE project_id = ? AND target_type = 'member' AND target_id = ? AND action = ?
	`, projectID, targetUserID, action).Scan(&count); err != nil {
		t.Fatalf("count %s audits: %v", action, err)
	}
	return count
}

func TestProjectMembershipMCPTransportAliasAndRealtimeMatrix(t *testing.T) {
	operations := []struct {
		name      string
		canonical string
		alias     string
	}{
		{name: "add", canonical: "members_add", alias: "members.add"},
		{name: "update", canonical: "members_updateRole", alias: "members.updateRole"},
		{name: "remove", canonical: "members_remove", alias: "members.remove"},
	}
	transports := []string{"legacy", "jsonrpc"}

	for _, operation := range operations {
		for _, tool := range []string{operation.canonical, operation.alias} {
			for _, transport := range transports {
				t.Run(operation.name+"/"+tool+"/"+transport, func(t *testing.T) {
					ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
					t.Cleanup(cleanup)
					client := newCookieClient(t, ts)
					bootstrapUser(t, client, ts.URL)
					ownerID := firstUserID(t, sqlDB)
					ctx := store.WithUserID(context.Background(), ownerID)
					project, err := st.CreateProject(ctx, "membership MCP matrix")
					if err != nil {
						t.Fatalf("create project: %v", err)
					}
					target, err := st.CreateUser(context.Background(), "membership-matrix-target@example.com", "password123", "Matrix Target")
					if err != nil {
						t.Fatalf("create target: %v", err)
					}

					args := map[string]any{"projectSlug": project.Slug, "userId": target.ID}
					wantRole := store.RoleContributor
					wantAudit := "member_added"
					switch operation.name {
					case "add":
						args["role"] = "contributor"
					case "update":
						if err := st.AddProjectMember(ctx, ownerID, project.ID, target.ID, store.RoleContributor); err != nil {
							t.Fatalf("seed update member: %v", err)
						}
						args["role"] = "viewer"
						wantRole = store.RoleViewer
						wantAudit = "member_role_changed"
					case "remove":
						if err := st.AddProjectMember(ctx, ownerID, project.ID, target.ID, store.RoleContributor); err != nil {
							t.Fatalf("seed remove member: %v", err)
						}
						wantRole = ""
						wantAudit = "member_removed"
					}

					stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
					defer stream.close()
					resp, out := callTodoUpdateMCP(t, client, ts.URL, transport, tool, args)
					if resp.StatusCode != http.StatusOK {
						t.Fatalf("%s %s status=%d response=%+v", transport, tool, resp.StatusCode, out)
					}
					data := membershipMCPData(t, transport, out)

					role, err := st.GetProjectRole(ctx, project.ID, target.ID)
					if err != nil || role != wantRole {
						t.Fatalf("persisted role=%q want=%q err=%v", role, wantRole, err)
					}
					if got := membershipMCPAuditCount(t, sqlDB, project.ID, target.ID, wantAudit); got != 1 {
						t.Fatalf("%s audit count=%d want=1", wantAudit, got)
					}

					if operation.name == "remove" {
						if got, want := sortedMapKeys(data), []string{"removed"}; !reflect.DeepEqual(got, want) {
							t.Fatalf("remove data keys=%v want=%v data=%+v", got, want, data)
						}
						removed := data["removed"].(map[string]any)
						if removed["projectSlug"] != project.Slug || int64(removed["userId"].(float64)) != target.ID {
							t.Fatalf("remove projection=%+v", removed)
						}
					} else {
						if got, want := sortedMapKeys(data), []string{"member"}; !reflect.DeepEqual(got, want) {
							t.Fatalf("member data keys=%v want=%v data=%+v", got, want, data)
						}
						member := data["member"].(map[string]any)
						if member["projectSlug"] != project.Slug || int64(member["userId"].(float64)) != target.ID || member["role"] != string(wantRole) {
							t.Fatalf("member projection=%+v", member)
						}
					}
					if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
						t.Fatalf("MCP membership mutation emitted realtime events: %+v", events)
					}
				})
			}
		}
	}
}

func assertMembershipMCPError(t *testing.T, transport string, resp *http.Response, out map[string]any, wantStatus int, wantCode, wantMessage string) map[string]any {
	t.Helper()
	if transport == "legacy" {
		if resp.StatusCode != wantStatus {
			t.Fatalf("legacy error status=%d want=%d response=%+v", resp.StatusCode, wantStatus, out)
		}
		errBody, ok := out["error"].(map[string]any)
		if !ok || errBody["code"] != wantCode || errBody["message"] != wantMessage {
			t.Fatalf("legacy error=%+v", out)
		}
		return errBody
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("JSON-RPC status=%d response=%+v", resp.StatusCode, out)
	}
	result, ok := out["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Fatalf("JSON-RPC result=%+v want tool error", out["result"])
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok || structured["code"] != wantCode || structured["message"] != wantMessage {
		t.Fatalf("JSON-RPC structured error=%+v", result["structuredContent"])
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) != 1 || content[0].(map[string]any)["text"] != wantMessage {
		t.Fatalf("JSON-RPC content=%+v", result["content"])
	}
	return structured
}

func TestProjectMembershipMCPSemanticValidationPrecedesAccess(t *testing.T) {
	ts, _, _, cleanup := newTodoUpdateMCPServer(t, "full")
	t.Cleanup(cleanup)
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	tests := []struct {
		name      string
		transport string
		tool      string
		role      string
		wantHTTP  int
		wantCode  string
		wantMsg   string
	}{
		{name: "legacy invalid role", transport: "legacy", tool: "members_add", role: "owner", wantHTTP: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMsg: "unsupported role"},
		{name: "JSON-RPC alias invalid role", transport: "jsonrpc", tool: "members.add", role: "owner", wantHTTP: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMsg: "unsupported role"},
		{name: "valid semantics reach access", transport: "legacy", tool: "members_add", role: "viewer", wantHTTP: http.StatusNotFound, wantCode: "NOT_FOUND", wantMsg: "not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, out := callTodoUpdateMCP(t, client, ts.URL, tt.transport, tt.tool, map[string]any{
				"projectSlug": "missing-membership-project",
				"userId":      9876,
				"role":        tt.role,
			})
			assertMembershipMCPError(t, tt.transport, resp, out, tt.wantHTTP, tt.wantCode, tt.wantMsg)
		})
	}
}

func TestProjectMembershipMCPNoOpAndSelfRemovalContracts(t *testing.T) {
	t.Run("semantic no-op persists no audit and remains realtime silent", func(t *testing.T) {
		ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
		t.Cleanup(cleanup)
		client := newCookieClient(t, ts)
		bootstrapUser(t, client, ts.URL)
		ownerID := firstUserID(t, sqlDB)
		ctx := store.WithUserID(context.Background(), ownerID)
		project, err := st.CreateProject(ctx, "membership MCP no-op")
		if err != nil {
			t.Fatalf("create project: %v", err)
		}
		target, err := st.CreateUser(context.Background(), "membership-noop-target@example.com", "password123", "No-op Target")
		if err != nil {
			t.Fatalf("create target: %v", err)
		}
		if err := st.AddProjectMember(ctx, ownerID, project.ID, target.ID, store.RoleViewer); err != nil {
			t.Fatalf("seed member: %v", err)
		}
		stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
		defer stream.close()
		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "members_updateRole", map[string]any{
			"projectSlug": project.Slug, "userId": target.ID, "role": " viewer ",
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("no-op status=%d response=%+v", resp.StatusCode, out)
		}
		member := membershipMCPData(t, "legacy", out)["member"].(map[string]any)
		if member["role"] != "viewer" {
			t.Fatalf("no-op projection=%+v", member)
		}
		if got := membershipMCPAuditCount(t, sqlDB, project.ID, target.ID, "member_role_changed"); got != 0 {
			t.Fatalf("no-op audit count=%d want=0", got)
		}
		if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
			t.Fatalf("no-op emitted realtime events: %+v", events)
		}
	})

	t.Run("self-removal succeeds without a post-read", func(t *testing.T) {
		ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
		t.Cleanup(cleanup)
		client := newCookieClient(t, ts)
		bootstrapUser(t, client, ts.URL)
		ownerID := firstUserID(t, sqlDB)
		ctx := store.WithUserID(context.Background(), ownerID)
		project, err := st.CreateProject(ctx, "membership MCP self-removal")
		if err != nil {
			t.Fatalf("create project: %v", err)
		}
		second, err := st.CreateUser(context.Background(), "membership-mcp-second@example.com", "password123", "Second Maintainer")
		if err != nil {
			t.Fatalf("create second maintainer: %v", err)
		}
		if err := st.AddProjectMember(ctx, ownerID, project.ID, second.ID, store.RoleMaintainer); err != nil {
			t.Fatalf("seed second maintainer: %v", err)
		}
		stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
		defer stream.close()
		resp, out := callTodoUpdateMCP(t, client, ts.URL, "jsonrpc", "members.remove", map[string]any{
			"projectSlug": project.Slug, "userId": ownerID,
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("self-removal status=%d response=%+v", resp.StatusCode, out)
		}
		removed := membershipMCPData(t, "jsonrpc", out)["removed"].(map[string]any)
		if removed["projectSlug"] != project.Slug || int64(removed["userId"].(float64)) != ownerID {
			t.Fatalf("self-removal projection=%+v", removed)
		}
		if role, err := st.GetProjectRole(ctx, project.ID, ownerID); err != nil || role != "" {
			t.Fatalf("self-removal persistence role=%q err=%v", role, err)
		}
		if got := membershipMCPAuditCount(t, sqlDB, project.ID, ownerID, "member_removed"); got != 1 {
			t.Fatalf("self-removal audit count=%d want=1", got)
		}
		if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
			t.Fatalf("self-removal emitted realtime events: %+v", events)
		}
	})
}

func TestProjectMembershipMCPBoardModeContracts(t *testing.T) {
	t.Run("Temporary Board creator lacks a durable maintainer role", func(t *testing.T) {
		ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
		t.Cleanup(cleanup)
		client := newCookieClient(t, ts)
		bootstrapUser(t, client, ts.URL)
		ownerID := firstUserID(t, sqlDB)
		board, err := st.CreateAnonymousBoard(store.WithUserID(context.Background(), ownerID))
		if err != nil {
			t.Fatalf("create Temporary Board: %v", err)
		}
		target, err := st.CreateUser(context.Background(), "membership-mcp-temp-target@example.com", "password123", "Temp Target")
		if err != nil {
			t.Fatalf("create target: %v", err)
		}
		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "members_add", map[string]any{
			"projectSlug": board.Slug, "userId": target.ID, "role": "viewer",
		})
		assertMembershipMCPError(t, "legacy", resp, out, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required")
	})

	t.Run("Anonymous Mode reports capability unavailable", func(t *testing.T) {
		ts, _, st, cleanup := newTodoUpdateMCPServer(t, "anonymous")
		t.Cleanup(cleanup)
		board, err := st.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatalf("create Anonymous Board: %v", err)
		}
		resp, out := callTodoUpdateMCP(t, ts.Client(), ts.URL, "legacy", "members_add", map[string]any{
			"projectSlug": board.Slug, "userId": 1, "role": "viewer",
		})
		assertMembershipMCPError(t, "legacy", resp, out, http.StatusForbidden, "CAPABILITY_UNAVAILABLE", "members_add is unavailable in anonymous mode")
	})
}

type membershipPostReadStore struct {
	*store.Store
	err       error
	members   []store.ProjectMember
	readCalls int
}

func (s *membershipPostReadStore) ListProjectMembers(context.Context, int64, int64) ([]store.ProjectMember, error) {
	s.readCalls++
	if s.err != nil {
		return nil, s.err
	}
	return append([]store.ProjectMember(nil), s.members...), nil
}

func newMembershipPostReadMCPServer(t *testing.T, readErr error, members []store.ProjectMember) (*httptest.Server, *sql.DB, *membershipPostReadStore) {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "app.db"), db.Options{
		BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}
	wrapper := &membershipPostReadStore{Store: store.New(sqlDB, nil), err: readErr, members: members}
	srv := httpapi.NewServer(wrapper, httpapi.Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		MCPHandler:     mcp.New(wrapper, mcp.Options{Mode: "full"}),
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		_ = sqlDB.Close()
	})
	return ts, sqlDB, wrapper
}

func TestProjectMembershipMCPPostReadContracts(t *testing.T) {
	tests := []struct {
		name       string
		operation  string
		transport  string
		readErr    error
		members    []store.ProjectMember
		wantStatus int
		wantCode   string
		wantMsg    string
		forbidden  []string
	}{
		{name: "add generic read failure", operation: "add", transport: "legacy", readErr: errors.New("forced membership post-read failure"), wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL", wantMsg: "internal error", forbidden: []string{"forced membership post-read failure"}},
		{name: "add wrapped not found", operation: "add", transport: "legacy", readErr: fmt.Errorf("wrapped membership read: %w", store.ErrNotFound), wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMsg: "not found"},
		{name: "add target missing", operation: "add", transport: "legacy", members: []store.ProjectMember{}, wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL", wantMsg: "internal error", forbidden: []string{"member not found after add"}},
		{name: "add target missing JSON-RPC", operation: "add", transport: "jsonrpc", members: []store.ProjectMember{}, wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL", wantMsg: "internal error", forbidden: []string{"member not found after add"}},
		{name: "update generic read failure JSON-RPC", operation: "update", transport: "jsonrpc", readErr: errors.New("forced JSON-RPC membership post-read failure"), wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL", wantMsg: "internal error", forbidden: []string{"forced JSON-RPC membership post-read failure"}},
		{name: "update target missing", operation: "update", transport: "legacy", members: []store.ProjectMember{}, wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, sqlDB, wrapped := newMembershipPostReadMCPServer(t, tt.readErr, tt.members)
			client := newCookieClient(t, ts)
			bootstrapUser(t, client, ts.URL)
			ownerID := firstUserID(t, sqlDB)
			ctx := store.WithUserID(context.Background(), ownerID)
			project, err := wrapped.Store.CreateProject(ctx, "membership MCP post-read")
			if err != nil {
				t.Fatalf("create project: %v", err)
			}
			target, err := wrapped.Store.CreateUser(context.Background(), "membership-postread-target@example.com", "password123", "Post-read Target")
			if err != nil {
				t.Fatalf("create target: %v", err)
			}
			tool := "members_add"
			args := map[string]any{"projectSlug": project.Slug, "userId": target.ID, "role": "viewer"}
			wantAction := "member_added"
			if tt.operation == "update" {
				if err := wrapped.Store.AddProjectMember(ctx, ownerID, project.ID, target.ID, store.RoleContributor); err != nil {
					t.Fatalf("seed member: %v", err)
				}
				tool = "members_updateRole"
				wantAction = "member_role_changed"
			}

			stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
			defer stream.close()
			resp, out := callTodoUpdateMCP(t, client, ts.URL, tt.transport, tool, args)
			publicError := assertMembershipMCPError(t, tt.transport, resp, out, tt.wantStatus, tt.wantCode, tt.wantMsg)
			if tt.wantCode == "INTERNAL" {
				details, ok := publicError["details"].(map[string]any)
				if !ok || len(details) != 0 {
					t.Fatalf("INTERNAL public details=%+v want empty object", publicError["details"])
				}
				encoded, err := json.Marshal(out)
				if err != nil {
					t.Fatalf("marshal public response: %v", err)
				}
				for _, forbidden := range tt.forbidden {
					if strings.Contains(string(encoded), forbidden) {
						t.Fatalf("public response leaked %q: %s", forbidden, encoded)
					}
				}
			}
			if wrapped.readCalls != 1 {
				t.Fatalf("post-read calls=%d want=1", wrapped.readCalls)
			}
			if role, err := wrapped.Store.GetProjectRole(ctx, project.ID, target.ID); err != nil || role != store.RoleViewer {
				t.Fatalf("mutation did not commit before post-read outcome: role=%q err=%v", role, err)
			}
			if got := membershipMCPAuditCount(t, sqlDB, project.ID, target.ID, wantAction); got != 1 {
				t.Fatalf("%s audit count=%d want=1", wantAction, got)
			}
			if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
				t.Fatalf("post-read outcome emitted realtime events: %+v", events)
			}
		})
	}
}

func TestProjectMembershipMCPRemoveSkipsPostRead(t *testing.T) {
	ts, sqlDB, wrapped := newMembershipPostReadMCPServer(t, errors.New("remove must not read members"), nil)
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	ctx := store.WithUserID(context.Background(), ownerID)
	project, err := wrapped.Store.CreateProject(ctx, "membership MCP remove no read")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	target, err := wrapped.Store.CreateUser(context.Background(), "membership-remove-no-read@example.com", "password123", "Remove Target")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	if err := wrapped.Store.AddProjectMember(ctx, ownerID, project.ID, target.ID, store.RoleViewer); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
	defer stream.close()

	resp, out := callTodoUpdateMCP(t, client, ts.URL, "jsonrpc", "members.remove", map[string]any{
		"projectSlug": project.Slug,
		"userId":      target.ID,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove status=%d response=%+v", resp.StatusCode, out)
	}
	removed := membershipMCPData(t, "jsonrpc", out)["removed"].(map[string]any)
	if removed["projectSlug"] != project.Slug || int64(removed["userId"].(float64)) != target.ID {
		t.Fatalf("remove projection=%+v", removed)
	}
	if wrapped.readCalls != 0 {
		t.Fatalf("remove post-read calls=%d want=0", wrapped.readCalls)
	}
	if role, err := wrapped.Store.GetProjectRole(ctx, project.ID, target.ID); err != nil || role != "" {
		t.Fatalf("remove persistence role=%q err=%v", role, err)
	}
	if got := membershipMCPAuditCount(t, sqlDB, project.ID, target.ID, "member_removed"); got != 1 {
		t.Fatalf("remove audit count=%d want=1", got)
	}
	if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
		t.Fatalf("remove emitted realtime events: %+v", events)
	}
}
