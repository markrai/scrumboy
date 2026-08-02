package mcp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"scrumboy/internal/db"
	"scrumboy/internal/httpapi"
	"scrumboy/internal/mcp"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

func workflowMCPData(t *testing.T, transport string, response map[string]any) map[string]any {
	t.Helper()
	if transport == "legacy" {
		if got, want := sortedMapKeys(response), []string{"data", "meta", "ok"}; !reflect.DeepEqual(got, want) || response["ok"] != true {
			t.Fatalf("legacy success envelope=%+v keys=%v want=%v", response, got, want)
		}
		if meta := response["meta"].(map[string]any); len(meta) != 0 {
			t.Fatalf("legacy metadata=%+v want empty", meta)
		}
		return response["data"].(map[string]any)
	}

	if got, want := sortedMapKeys(response), []string{"id", "jsonrpc", "result"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON-RPC envelope=%+v keys=%v want=%v", response, got, want)
	}
	result := response["result"].(map[string]any)
	if got, want := sortedMapKeys(result), []string{"content", "structuredContent"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON-RPC result=%+v keys=%v want=%v", result, got, want)
	}
	data := result["structuredContent"].(map[string]any)
	content := result["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["type"] != "text" {
		t.Fatalf("JSON-RPC content=%+v", content)
	}
	var textData map[string]any
	if err := json.Unmarshal([]byte(content[0].(map[string]any)["text"].(string)), &textData); err != nil {
		t.Fatalf("decode JSON-RPC text content: %v", err)
	}
	if !reflect.DeepEqual(textData, data) {
		t.Fatalf("JSON-RPC text/structured divergence: text=%+v structured=%+v", textData, data)
	}
	return data
}

func TestWorkflowMutationMCPTransportAndRealtimeContracts(t *testing.T) {
	operations := []string{"create", "update", "delete"}
	transports := []string{"legacy", "jsonrpc"}
	for _, operation := range operations {
		for _, transport := range transports {
			t.Run(transport+"/"+operation, func(t *testing.T) {
				ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
				t.Cleanup(cleanup)
				client := newCookieClient(t, ts)
				bootstrapUser(t, client, ts.URL)
				ownerID := firstUserID(t, sqlDB)
				ctx := store.WithUserID(context.Background(), ownerID)
				project, err := st.CreateProject(ctx, "workflow MCP "+transport+" "+operation)
				if err != nil {
					t.Fatalf("create project: %v", err)
				}

				tool := "workflow_" + operation
				args := map[string]any{"projectSlug": project.Slug}
				var targetKey string
				switch operation {
				case "create":
					args["name"] = "Code Review"
					targetKey = "code_review"
				case "update":
					args["columnKey"] = store.DefaultColumnDoing
					args["name"] = "Building"
					args["color"] = "#123456"
					targetKey = store.DefaultColumnDoing
				case "delete":
					column, err := st.AddWorkflowColumn(ctx, project.ID, "Disposable")
					if err != nil {
						t.Fatalf("add delete fixture: %v", err)
					}
					args["columnKey"] = column.Key
					targetKey = column.Key
				}

				stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
				defer stream.close()
				resp, out := callTodoUpdateMCP(t, client, ts.URL, transport, tool, args)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("%s %s status=%d response=%+v", transport, tool, resp.StatusCode, out)
				}
				data := workflowMCPData(t, transport, out)

				columns, err := st.GetProjectWorkflow(ctx, project.ID)
				if err != nil {
					t.Fatalf("get workflow: %v", err)
				}
				switch operation {
				case "create":
					column := data["column"].(map[string]any)
					if column["key"] != targetKey || column["name"] != "Code Review" || column["isDone"] != false || column["system"] != false {
						t.Fatalf("create projection=%+v", column)
					}
					persisted, ok := workflowColumnByKeyMCP(columns, targetKey)
					if !ok || persisted.Name != "Code Review" {
						t.Fatalf("create persistence=%+v", columns)
					}
				case "update":
					column := data["column"].(map[string]any)
					if column["key"] != targetKey || column["name"] != "Building" || column["color"] != "#123456" {
						t.Fatalf("update projection=%+v", column)
					}
					persisted, ok := workflowColumnByKeyMCP(columns, targetKey)
					if !ok || persisted.Name != "Building" || persisted.Color != "#123456" {
						t.Fatalf("update persistence=%+v", columns)
					}
				case "delete":
					deleted := data["deleted"].(map[string]any)
					if deleted["projectSlug"] != project.Slug || deleted["columnKey"] != targetKey {
						t.Fatalf("delete projection=%+v", deleted)
					}
					if _, ok := workflowColumnByKeyMCP(columns, targetKey); ok {
						t.Fatalf("deleted column %q still present: %+v", targetKey, columns)
					}
				}
				if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
					t.Fatalf("MCP workflow mutation emitted realtime events: %+v", events)
				}
			})
		}
	}
}

func workflowColumnByKeyMCP(columns []store.WorkflowColumn, key string) (store.WorkflowColumn, bool) {
	for _, column := range columns {
		if column.Key == key {
			return column, true
		}
	}
	return store.WorkflowColumn{}, false
}

func TestWorkflowMutationMCPSemanticValidationPrecedesAccess(t *testing.T) {
	ts, _, _, cleanup := newTodoUpdateMCPServer(t, "full")
	t.Cleanup(cleanup)
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	t.Run("invalid name wins before missing project", func(t *testing.T) {
		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "workflow_create", map[string]any{
			"projectSlug": "missing-workflow-board",
			"name":        "   ",
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		errBody := out["error"].(map[string]any)
		if errBody["code"] != "VALIDATION_ERROR" || errBody["message"] != "name required" {
			t.Fatalf("validation error=%+v", errBody)
		}
	})

	t.Run("valid semantics reach project access", func(t *testing.T) {
		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "workflow_create", map[string]any{
			"projectSlug": "missing-workflow-board",
			"name":        "Valid Name",
		})
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
		}
		if code := out["error"].(map[string]any)["code"]; code != "NOT_FOUND" {
			t.Fatalf("access error code=%v want NOT_FOUND", code)
		}
	})
}

func TestWorkflowMutationMCPBoardModeContracts(t *testing.T) {
	t.Run("Temporary Board creator is forbidden because access context has no durable role", func(t *testing.T) {
		ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
		t.Cleanup(cleanup)
		client := newCookieClient(t, ts)
		bootstrapUser(t, client, ts.URL)
		ownerID := firstUserID(t, sqlDB)
		board, err := st.CreateAnonymousBoard(store.WithUserID(context.Background(), ownerID))
		if err != nil {
			t.Fatalf("create Temporary Board: %v", err)
		}

		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "workflow_create", map[string]any{
			"projectSlug": board.Slug,
			"name":        "Not Allowed",
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("Temporary Board status=%d response=%+v", resp.StatusCode, out)
		}
		if code := out["error"].(map[string]any)["code"]; code != "FORBIDDEN" {
			t.Fatalf("Temporary Board error=%+v", out)
		}
	})

	t.Run("Anonymous Mode reports capability unavailable", func(t *testing.T) {
		ts, _, st, cleanup := newTodoUpdateMCPServer(t, "anonymous")
		t.Cleanup(cleanup)
		board, err := st.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatalf("create Anonymous Board: %v", err)
		}

		resp, out := callTodoUpdateMCP(t, ts.Client(), ts.URL, "legacy", "workflow_create", map[string]any{
			"projectSlug": board.Slug,
			"name":        "Not Allowed",
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("Anonymous Mode status=%d response=%+v", resp.StatusCode, out)
		}
		errBody := out["error"].(map[string]any)
		if errBody["code"] != "CAPABILITY_UNAVAILABLE" || errBody["message"] != "workflow_create is unavailable in anonymous mode" {
			t.Fatalf("Anonymous Mode error=%+v", errBody)
		}
	})
}

type workflowPostReadFailureStore struct {
	*store.Store
	err       error
	readCalls int
}

func (s *workflowPostReadFailureStore) GetProjectWorkflow(context.Context, int64) ([]store.WorkflowColumn, error) {
	s.readCalls++
	return nil, s.err
}

func newWorkflowPostReadFailureServer(t *testing.T, readErr error) (*httptest.Server, *sql.DB, *workflowPostReadFailureStore, func()) {
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
	wrapped := &workflowPostReadFailureStore{Store: store.New(sqlDB, nil), err: readErr}
	srv := httpapi.NewServer(wrapped, httpapi.Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		MCPHandler:     mcp.New(wrapped, mcp.Options{Mode: "full"}),
	})
	ts := httptest.NewServer(srv)
	return ts, sqlDB, wrapped, func() {
		ts.Close()
		_ = sqlDB.Close()
	}
}

func TestWorkflowMutationMCPUpdatePostReadFailureOccursAfterCommit(t *testing.T) {
	readErr := errors.New("forced workflow post-read failure")
	ts, sqlDB, wrapped, cleanup := newWorkflowPostReadFailureServer(t, readErr)
	t.Cleanup(cleanup)
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	ctx := store.WithUserID(context.Background(), ownerID)
	project, err := wrapped.Store.CreateProject(ctx, "workflow MCP post-read failure")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
	defer stream.close()

	resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "workflow_update", map[string]any{
		"projectSlug": project.Slug,
		"columnKey":   store.DefaultColumnDoing,
		"name":        "Committed Before Read Failure",
		"color":       "#654321",
	})
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
	}
	errBody := out["error"].(map[string]any)
	if errBody["code"] != "INTERNAL" || errBody["message"] != "internal error" {
		t.Fatalf("post-read error envelope=%+v", errBody)
	}
	if wrapped.readCalls != 1 {
		t.Fatalf("post-read calls=%d want=1", wrapped.readCalls)
	}
	columns, err := wrapped.Store.GetProjectWorkflow(ctx, project.ID)
	if err != nil {
		t.Fatalf("read committed workflow: %v", err)
	}
	updated, ok := workflowColumnByKeyMCP(columns, store.DefaultColumnDoing)
	if !ok || updated.Name != "Committed Before Read Failure" || updated.Color != "#654321" {
		t.Fatalf("update did not commit before post-read failure: %+v", columns)
	}
	if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
		t.Fatalf("post-read failure emitted realtime events: %+v", events)
	}
}
