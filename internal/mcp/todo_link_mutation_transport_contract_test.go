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

type todoLinkMCPFixture struct {
	ts      *httptest.Server
	db      *sql.DB
	st      *store.Store
	client  *http.Client
	ownerID int64
	ctx     context.Context
	project store.Project
	from    store.Todo
	to      store.Todo
}

func newTodoLinkMCPFixture(t *testing.T, name string) *todoLinkMCPFixture {
	t.Helper()
	ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
	t.Cleanup(cleanup)
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	ctx := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctx, name)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	from := createTodoUpdateMCPTodo(t, st, ctx, project.ID, "Link source", nil, nil)
	to := createTodoUpdateMCPTodo(t, st, ctx, project.ID, "Link target", nil, nil)
	return &todoLinkMCPFixture{ts: ts, db: sqlDB, st: st, client: client, ownerID: ownerID, ctx: ctx, project: project, from: from, to: to}
}

func todoLinkMCPRowCount(t *testing.T, db *sql.DB, projectID, fromLocalID, toLocalID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM todo_links
		WHERE project_id = ? AND from_local_id = ? AND to_local_id = ?
	`, projectID, fromLocalID, toLocalID).Scan(&count); err != nil {
		t.Fatalf("count todo link rows: %v", err)
	}
	return count
}

func todoLinkMCPStoredType(t *testing.T, db *sql.DB, projectID, fromLocalID, toLocalID int64) string {
	t.Helper()
	var linkType string
	if err := db.QueryRow(`
		SELECT link_type FROM todo_links
		WHERE project_id = ? AND from_local_id = ? AND to_local_id = ?
	`, projectID, fromLocalID, toLocalID).Scan(&linkType); err != nil {
		t.Fatalf("read todo link type: %v", err)
	}
	return linkType
}

func todoLinkMCPAuditCount(t *testing.T, db *sql.DB, projectID int64, action string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM audit_events
		WHERE project_id = ? AND action = ? AND target_type = 'todo_link'
	`, projectID, action).Scan(&count); err != nil {
		t.Fatalf("count %s audits: %v", action, err)
	}
	return count
}

func todoLinkMCPData(t *testing.T, transport string, response map[string]any) map[string]any {
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
			t.Fatalf("legacy data type=%T response=%+v", response["data"], response)
		}
		return data
	}

	if got, want := sortedMapKeys(response), []string{"id", "jsonrpc", "result"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON-RPC envelope=%+v keys=%v want=%v", response, got, want)
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("JSON-RPC result type=%T response=%+v", response["result"], response)
	}
	if _, exists := result["isError"]; exists {
		t.Fatalf("JSON-RPC success unexpectedly contains isError: %+v", result)
	}
	if got, want := sortedMapKeys(result), []string{"content", "structuredContent"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON-RPC result=%+v keys=%v want=%v", result, got, want)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("JSON-RPC structuredContent type=%T result=%+v", result["structuredContent"], result)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("JSON-RPC content=%+v want one item", result["content"])
	}
	item, ok := content[0].(map[string]any)
	if !ok || item["type"] != "text" {
		t.Fatalf("JSON-RPC content item=%+v", content[0])
	}
	textValue, ok := item["text"].(string)
	if !ok {
		t.Fatalf("JSON-RPC text type=%T item=%+v", item["text"], item)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(textValue), &decoded); err != nil {
		t.Fatalf("decode JSON-RPC text: %v", err)
	}
	if !reflect.DeepEqual(decoded, structured) {
		t.Fatalf("JSON-RPC text/structured divergence: text=%+v structured=%+v", decoded, structured)
	}
	return structured
}

func assertTodoLinkMCPItems(t *testing.T, data map[string]any, outbound, inbound []struct {
	localID  int64
	title    string
	linkType string
}) {
	t.Helper()
	assertList := func(name string, raw any, want []struct {
		localID  int64
		title    string
		linkType string
	}) {
		items, ok := raw.([]any)
		if !ok || len(items) != len(want) {
			t.Fatalf("%s=%+v want %d items", name, raw, len(want))
		}
		for i, expected := range want {
			item, ok := items[i].(map[string]any)
			if !ok {
				t.Fatalf("%s[%d] type=%T", name, i, items[i])
			}
			if got, wantKeys := sortedMapKeys(item), []string{"linkType", "localId", "title"}; !reflect.DeepEqual(got, wantKeys) {
				t.Fatalf("%s[%d] keys=%v want=%v item=%+v", name, i, got, wantKeys, item)
			}
			if item["localId"] != float64(expected.localID) || item["title"] != expected.title || item["linkType"] != expected.linkType {
				t.Fatalf("%s[%d]=%+v want localId=%d title=%q linkType=%q", name, i, item, expected.localID, expected.title, expected.linkType)
			}
		}
	}
	assertList("outbound", data["outbound"], outbound)
	assertList("inbound", data["inbound"], inbound)
}

func assertTodoLinkMCPError(t *testing.T, transport string, resp *http.Response, out map[string]any, wantStatus int, wantCode, wantMessage string) map[string]any {
	t.Helper()
	if transport == "legacy" {
		if resp.StatusCode != wantStatus {
			t.Fatalf("legacy error status=%d want=%d response=%+v", resp.StatusCode, wantStatus, out)
		}
		errBody, ok := out["error"].(map[string]any)
		if !ok || errBody["code"] != wantCode || errBody["message"] != wantMessage {
			t.Fatalf("legacy error=%+v want code=%q message=%q", out, wantCode, wantMessage)
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
		t.Fatalf("JSON-RPC structured error=%+v want code=%q message=%q", result["structuredContent"], wantCode, wantMessage)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("JSON-RPC content=%+v want one item", result["content"])
	}
	item, ok := content[0].(map[string]any)
	if !ok || item["type"] != "text" || item["text"] != wantMessage {
		t.Fatalf("JSON-RPC content item=%+v want text=%q", content[0], wantMessage)
	}
	return structured
}

func assertEmptyTodoLinkMCPDetails(t *testing.T, publicError map[string]any) {
	t.Helper()
	details, ok := publicError["details"].(map[string]any)
	if !ok || len(details) != 0 {
		t.Fatalf("public error details=%+v want empty object; error=%+v", publicError["details"], publicError)
	}
}

func TestTodoLinkMutationMCPTransportAndRealtimeContracts(t *testing.T) {
	for _, operation := range []string{"add", "remove"} {
		for _, transport := range []string{"legacy", "jsonrpc"} {
			t.Run(transport+"/"+operation, func(t *testing.T) {
				fx := newTodoLinkMCPFixture(t, "todo-link-mcp-"+transport+"-"+operation)
				tool := "todos_linkAdd"
				args := map[string]any{
					"projectSlug":   fx.project.Slug,
					"localId":       fx.from.LocalID,
					"targetLocalId": fx.to.LocalID,
				}
				wantType := "relates_to"
				action := "link_added"
				beforeAudit := todoLinkMCPAuditCount(t, fx.db, fx.project.ID, action)
				if operation == "remove" {
					tool = "todos_linkRemove"
					action = "link_removed"
					if err := fx.st.AddLink(fx.ctx, fx.project.ID, fx.from.LocalID, fx.to.LocalID, "blocks", store.ModeFull); err != nil {
						t.Fatalf("add remove fixture: %v", err)
					}
					wantType = "blocks"
					beforeAudit = todoLinkMCPAuditCount(t, fx.db, fx.project.ID, action)
				} else if transport == "jsonrpc" {
					args["linkType"] = "blocks"
					wantType = "blocks"
				}

				stream := subscribeTodoUpdateMCPEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")
				defer stream.close()
				resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, tool, args)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("%s %s status=%d response=%+v", transport, tool, resp.StatusCode, out)
				}
				data := todoLinkMCPData(t, transport, out)
				if operation == "add" {
					assertTodoLinkMCPItems(t, data, []struct {
						localID  int64
						title    string
						linkType string
					}{{fx.to.LocalID, fx.to.Title, wantType}}, nil)
					if got := todoLinkMCPRowCount(t, fx.db, fx.project.ID, fx.from.LocalID, fx.to.LocalID); got != 1 {
						t.Fatalf("add link rows=%d want=1", got)
					}
				} else {
					assertTodoLinkMCPItems(t, data, nil, nil)
					if got := todoLinkMCPRowCount(t, fx.db, fx.project.ID, fx.from.LocalID, fx.to.LocalID); got != 0 {
						t.Fatalf("remove link rows=%d want=0", got)
					}
				}
				if got := todoLinkMCPAuditCount(t, fx.db, fx.project.ID, action); got != beforeAudit+1 {
					t.Fatalf("%s audit count=%d want=%d", action, got, beforeAudit+1)
				}
				if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
					t.Fatalf("MCP todo-link mutation emitted realtime events: %+v", events)
				}
			})
		}
	}
}

func TestTodoLinkMutationMCPPrecedence(t *testing.T) {
	fx := newTodoLinkMCPFixture(t, "todo-link-mcp-precedence")
	tests := []struct {
		name        string
		transport   string
		args        map[string]any
		wantStatus  int
		wantCode    string
		wantMessage string
		wantField   string
	}{
		{name: "basic validation before access", transport: "legacy", args: map[string]any{"localId": fx.from.LocalID, "targetLocalId": fx.to.LocalID}, wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "missing projectSlug", wantField: "projectSlug"},
		{name: "project access before store semantics", transport: "legacy", args: map[string]any{"projectSlug": "missing-link-board", "localId": fx.from.LocalID, "targetLocalId": fx.from.LocalID, "linkType": "invalid"}, wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMessage: "not found"},
		{name: "source lookup before store semantics", transport: "legacy", args: map[string]any{"projectSlug": fx.project.Slug, "localId": 999, "targetLocalId": 999, "linkType": "invalid"}, wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMessage: "not found"},
		{name: "valid source reaches store validation", transport: "jsonrpc", args: map[string]any{"projectSlug": fx.project.Slug, "localId": fx.from.LocalID, "targetLocalId": fx.to.LocalID, "linkType": "invalid"}, wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "validation: invalid link_type"},
		{name: "target absent in resolved project", transport: "legacy", args: map[string]any{"projectSlug": fx.project.Slug, "localId": fx.from.LocalID, "targetLocalId": 999}, wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMessage: "not found"},
	}
	stream := subscribeTodoUpdateMCPEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")
	defer stream.close()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, tc.transport, "todos_linkAdd", tc.args)
			publicError := assertTodoLinkMCPError(t, tc.transport, resp, out, tc.wantStatus, tc.wantCode, tc.wantMessage)
			if tc.wantField == "" {
				assertEmptyTodoLinkMCPDetails(t, publicError)
			} else {
				details, ok := publicError["details"].(map[string]any)
				if !ok || len(details) != 1 || details["field"] != tc.wantField {
					t.Fatalf("public details=%+v want field=%q", publicError["details"], tc.wantField)
				}
			}
		})
	}
	if got := todoLinkMCPAuditCount(t, fx.db, fx.project.ID, "link_added"); got != 0 {
		t.Fatalf("precedence failures created link_added audits=%d", got)
	}
	if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
		t.Fatalf("precedence failures emitted realtime events: %+v", events)
	}
}

func TestTodoLinkMutationMCPDuplicateAddContract(t *testing.T) {
	fx := newTodoLinkMCPFixture(t, "todo-link-mcp-duplicate")
	if err := fx.st.AddLink(fx.ctx, fx.project.ID, fx.from.LocalID, fx.to.LocalID, "blocks", store.ModeFull); err != nil {
		t.Fatalf("add initial link: %v", err)
	}
	beforeAudit := todoLinkMCPAuditCount(t, fx.db, fx.project.ID, "link_added")
	stream := subscribeTodoUpdateMCPEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")
	defer stream.close()
	resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, "legacy", "todos_linkAdd", map[string]any{
		"projectSlug":   fx.project.Slug,
		"localId":       fx.from.LocalID,
		"targetLocalId": fx.to.LocalID,
		"linkType":      "parent",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("duplicate add status=%d response=%+v", resp.StatusCode, out)
	}
	data := todoLinkMCPData(t, "legacy", out)
	assertTodoLinkMCPItems(t, data, []struct {
		localID  int64
		title    string
		linkType string
	}{{fx.to.LocalID, fx.to.Title, "blocks"}}, nil)
	if got := todoLinkMCPRowCount(t, fx.db, fx.project.ID, fx.from.LocalID, fx.to.LocalID); got != 1 {
		t.Fatalf("duplicate link rows=%d want=1", got)
	}
	if got := todoLinkMCPStoredType(t, fx.db, fx.project.ID, fx.from.LocalID, fx.to.LocalID); got != "blocks" {
		t.Fatalf("duplicate changed stored type=%q want=blocks", got)
	}
	if got := todoLinkMCPAuditCount(t, fx.db, fx.project.ID, "link_added"); got != beforeAudit {
		t.Fatalf("duplicate audit count=%d want=%d", got, beforeAudit)
	}
	if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
		t.Fatalf("duplicate MCP add emitted realtime events: %+v", events)
	}
}

type todoLinkPostReadStore struct {
	*store.Store
	outboundErr   error
	inboundErr    error
	outboundCalls int
	inboundCalls  int
	trace         []string
}

func (s *todoLinkPostReadStore) ListLinksForTodo(ctx context.Context, projectID, localID int64, mode store.Mode) ([]store.TodoLinkTarget, error) {
	s.outboundCalls++
	s.trace = append(s.trace, "outbound")
	if s.outboundErr != nil {
		return nil, s.outboundErr
	}
	return s.Store.ListLinksForTodo(ctx, projectID, localID, mode)
}

func (s *todoLinkPostReadStore) ListBacklinksForTodo(ctx context.Context, projectID, localID int64, mode store.Mode) ([]store.TodoLinkTarget, error) {
	s.inboundCalls++
	s.trace = append(s.trace, "inbound")
	if s.inboundErr != nil {
		return nil, s.inboundErr
	}
	return s.Store.ListBacklinksForTodo(ctx, projectID, localID, mode)
}

func newTodoLinkPostReadServer(t *testing.T, outboundErr, inboundErr error) (*httptest.Server, *sql.DB, *todoLinkPostReadStore) {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "app.db"), db.Options{BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}
	wrapper := &todoLinkPostReadStore{Store: store.New(sqlDB, nil), outboundErr: outboundErr, inboundErr: inboundErr}
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

func TestTodoLinkMutationMCPPostWriteReadFailures(t *testing.T) {
	tests := []struct {
		name        string
		operation   string
		transport   string
		outboundErr error
		inboundErr  error
		wantStatus  int
		wantCode    string
		wantMessage string
		wantTrace   []string
		forbidden   []string
	}{
		{
			name: "legacy add outbound generic failure", operation: "add", transport: "legacy",
			outboundErr: errors.New("forced todo-link outbound read failure"),
			wantStatus:  http.StatusInternalServerError, wantCode: "INTERNAL", wantMessage: "internal error",
			wantTrace: []string{"outbound"}, forbidden: []string{"forced todo-link outbound read failure"},
		},
		{
			name: "JSON-RPC remove inbound wrapped not found", operation: "remove", transport: "jsonrpc",
			inboundErr: fmt.Errorf("forced todo-link inbound read failure: %w", store.ErrNotFound),
			wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMessage: "not found",
			wantTrace: []string{"outbound", "inbound"}, forbidden: []string{"forced todo-link inbound read failure"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts, sqlDB, wrapped := newTodoLinkPostReadServer(t, tc.outboundErr, tc.inboundErr)
			client := newCookieClient(t, ts)
			bootstrapUser(t, client, ts.URL)
			ownerID := firstUserID(t, sqlDB)
			ctx := store.WithUserID(context.Background(), ownerID)
			project, err := wrapped.Store.CreateProject(ctx, "todo-link post-read "+tc.operation)
			if err != nil {
				t.Fatalf("create project: %v", err)
			}
			from := createTodoUpdateMCPTodo(t, wrapped.Store, ctx, project.ID, "Post-read source", nil, nil)
			to := createTodoUpdateMCPTodo(t, wrapped.Store, ctx, project.ID, "Post-read target", nil, nil)
			tool := "todos_linkAdd"
			action := "link_added"
			if tc.operation == "remove" {
				tool = "todos_linkRemove"
				action = "link_removed"
				if err := wrapped.Store.AddLink(ctx, project.ID, from.LocalID, to.LocalID, "blocks", store.ModeFull); err != nil {
					t.Fatalf("add remove fixture: %v", err)
				}
			}
			beforeAudit := todoLinkMCPAuditCount(t, sqlDB, project.ID, action)
			stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
			defer stream.close()
			resp, out := callTodoUpdateMCP(t, client, ts.URL, tc.transport, tool, map[string]any{
				"projectSlug":   project.Slug,
				"localId":       from.LocalID,
				"targetLocalId": to.LocalID,
			})
			publicError := assertTodoLinkMCPError(t, tc.transport, resp, out, tc.wantStatus, tc.wantCode, tc.wantMessage)
			assertEmptyTodoLinkMCPDetails(t, publicError)
			if !reflect.DeepEqual(wrapped.trace, tc.wantTrace) {
				t.Fatalf("read trace=%v want=%v", wrapped.trace, tc.wantTrace)
			}
			if wrapped.outboundCalls != 1 {
				t.Fatalf("outbound calls=%d want=1", wrapped.outboundCalls)
			}
			wantInbound := 0
			if tc.inboundErr != nil {
				wantInbound = 1
			}
			if wrapped.inboundCalls != wantInbound {
				t.Fatalf("inbound calls=%d want=%d", wrapped.inboundCalls, wantInbound)
			}
			if tc.operation == "add" {
				if got := todoLinkMCPRowCount(t, sqlDB, project.ID, from.LocalID, to.LocalID); got != 1 {
					t.Fatalf("add did not commit before outbound failure; rows=%d", got)
				}
			} else if got := todoLinkMCPRowCount(t, sqlDB, project.ID, from.LocalID, to.LocalID); got != 0 {
				t.Fatalf("remove did not commit before inbound failure; rows=%d", got)
			}
			if got := todoLinkMCPAuditCount(t, sqlDB, project.ID, action); got != beforeAudit+1 {
				t.Fatalf("%s audit count=%d want=%d after read failure", action, got, beforeAudit+1)
			}
			encoded, err := json.Marshal(out)
			if err != nil {
				t.Fatalf("marshal public error: %v", err)
			}
			for _, forbidden := range tc.forbidden {
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("public response leaked %q: %s", forbidden, encoded)
				}
			}
			if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
				t.Fatalf("post-write read failure emitted realtime events: %+v", events)
			}
		})
	}
}

func TestTodoLinkMutationMCPBoardModeContracts(t *testing.T) {
	t.Run("Temporary Board authenticated link holder can mutate", func(t *testing.T) {
		ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
		t.Cleanup(cleanup)
		creatorClient := newCookieClient(t, ts)
		bootstrapUser(t, creatorClient, ts.URL)
		creatorID := firstUserID(t, sqlDB)
		creatorCtx := store.WithUserID(context.Background(), creatorID)
		board, err := st.CreateAnonymousBoard(creatorCtx)
		if err != nil {
			t.Fatalf("create Temporary Board: %v", err)
		}
		from := createTodoUpdateMCPTodo(t, st, creatorCtx, board.ID, "Temporary source", nil, nil)
		to := createTodoUpdateMCPTodo(t, st, creatorCtx, board.ID, "Temporary target", nil, nil)
		linkHolder, err := st.CreateUser(context.Background(), "todo-link-mcp-temp-holder@example.com", "password123", "Temporary Link Holder")
		if err != nil {
			t.Fatalf("create link holder: %v", err)
		}
		holderClient := loginTodoUpdateMCPUser(t, ts, linkHolder.Email, "password123")
		stream := subscribeTodoUpdateMCPEvents(t, holderClient, ts.URL+"/api/board/"+board.Slug+"/events")
		defer stream.close()
		resp, out := callTodoUpdateMCP(t, holderClient, ts.URL, "legacy", "todos_linkAdd", map[string]any{
			"projectSlug": board.Slug, "localId": from.LocalID, "targetLocalId": to.LocalID,
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Temporary Board add status=%d response=%+v", resp.StatusCode, out)
		}
		todoLinkMCPData(t, "legacy", out)
		if got := todoLinkMCPRowCount(t, sqlDB, board.ID, from.LocalID, to.LocalID); got != 1 {
			t.Fatalf("Temporary Board link rows=%d want=1", got)
		}
		if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
			t.Fatalf("Temporary Board MCP link emitted realtime events: %+v", events)
		}
	})

	t.Run("Anonymous Mode reports capability unavailable", func(t *testing.T) {
		ts, _, st, cleanup := newTodoUpdateMCPServer(t, "anonymous")
		t.Cleanup(cleanup)
		board, err := st.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatalf("create Anonymous Board: %v", err)
		}
		for _, tool := range []string{"todos_linkAdd", "todos_linkRemove"} {
			t.Run(tool, func(t *testing.T) {
				resp, out := callTodoUpdateMCP(t, ts.Client(), ts.URL, "legacy", tool, map[string]any{
					"projectSlug": board.Slug, "localId": 1, "targetLocalId": 2,
				})
				publicError := assertTodoLinkMCPError(t, "legacy", resp, out, http.StatusForbidden, "CAPABILITY_UNAVAILABLE", tool+" is unavailable in anonymous mode")
				assertEmptyTodoLinkMCPDetails(t, publicError)
			})
		}
	})
}
