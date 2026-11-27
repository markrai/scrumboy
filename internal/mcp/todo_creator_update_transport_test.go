package mcp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"scrumboy/internal/store"
)

func mcpCreatorAuditCount(t *testing.T, db *sql.DB, todoID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'todo_creator_changed' AND target_id = ?`, todoID).Scan(&count); err != nil {
		t.Fatalf("count creator audits: %v", err)
	}
	return count
}

func TestTodoCreatorUpdateMCPContract(t *testing.T) {
	ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
	defer cleanup()
	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, db)
	project, ctx := createTodoUpdateMCPProject(t, st, ownerID, "MCP creator contract")
	todo := createTodoUpdateMCPTodo(t, st, ctx, project.ID, "original title", nil, nil)
	target, err := st.CreateUser(context.Background(), "mcp-creator-target@example.com", "password123", "Historical creator")
	if err != nil {
		t.Fatalf("create nonmember target: %v", err)
	}

	resp, out := callTodoUpdateMCP(t, ownerClient, ts.URL, "legacy", "todos_update", map[string]any{
		"projectSlug": project.Slug,
		"localId":     todo.LocalID,
		"patch":       map[string]any{"createdByUserId": target.ID},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("creator-only update status=%d response=%+v", resp.StatusCode, out)
	}
	updated := assertTodoUpdateMCPSuccess(t, "legacy", out, project.Slug, todo.LocalID)
	if updated["createdByUserId"] != float64(target.ID) || updated["title"] != todo.Title || updated["body"] != todo.Body {
		t.Fatalf("creator-only sparse response=%+v", updated)
	}
	persisted, err := st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, store.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.CreatedByUserID == nil || *persisted.CreatedByUserID != target.ID || persisted.Title != todo.Title || persisted.Body != todo.Body {
		t.Fatalf("creator-only sparse persistence=%+v", persisted)
	}
	if role, err := st.GetProjectRole(ctx, project.ID, target.ID); err != nil || role != "" {
		t.Fatalf("nonmember target role=%q err=%v", role, err)
	}
	if got := mcpCreatorAuditCount(t, db, todo.ID); got != 1 {
		t.Fatalf("creator audit count=%d want=1", got)
	}

	resp, out = callTodoUpdateMCP(t, ownerClient, ts.URL, "jsonrpc", "todos_update", map[string]any{
		"projectSlug": project.Slug,
		"localId":     todo.LocalID,
		"patch":       map[string]any{"body": "unrelated sparse update"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("creator omission status=%d response=%+v", resp.StatusCode, out)
	}
	updated = assertTodoUpdateMCPSuccess(t, "jsonrpc", out, project.Slug, todo.LocalID)
	if updated["createdByUserId"] != float64(target.ID) || updated["title"] != todo.Title || updated["body"] != "unrelated sparse update" {
		t.Fatalf("creator omission response=%+v", updated)
	}

	resp, out = callTodoUpdateMCP(t, ownerClient, ts.URL, "legacy", "todos_update", map[string]any{
		"projectSlug": project.Slug,
		"localId":     todo.LocalID,
		"patch":       map[string]any{"createdByUserId": target.ID},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorized same-value status=%d response=%+v", resp.StatusCode, out)
	}
	if got := mcpCreatorAuditCount(t, db, todo.ID); got != 1 {
		t.Fatalf("same-value creator created an audit: %d", got)
	}
}

func TestTodoCreatorUpdateMCPInvalidValuesAreAtomic(t *testing.T) {
	ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
	defer cleanup()
	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, db)
	project, ctx := createTodoUpdateMCPProject(t, st, ownerID, "MCP creator validation")
	todo := createTodoUpdateMCPTodo(t, st, ctx, project.ID, "unchanged title", nil, nil)

	cases := []struct {
		name  string
		value any
	}{
		{name: "null", value: nil},
		{name: "zero", value: 0},
		{name: "negative", value: -1},
		{name: "fraction", value: 1.5},
		{name: "string", value: "1"},
		{name: "boolean", value: true},
		{name: "object", value: map[string]any{"id": 1}},
		{name: "array", value: []any{1}},
		{name: "overflow", value: json.Number("9223372036854775808")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, out := callTodoUpdateMCP(t, ownerClient, ts.URL, "legacy", "todos_update", map[string]any{
				"projectSlug": project.Slug,
				"localId":     todo.LocalID,
				"patch": map[string]any{
					"title":           "must not persist",
					"createdByUserId": tc.value,
				},
			})
			if resp.StatusCode != http.StatusBadRequest || out["ok"] != false {
				t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
			}
			errorBody := out["error"].(map[string]any)
			if errorBody["code"] != "VALIDATION_ERROR" || errorBody["details"].(map[string]any)["field"] != "createdByUserId" {
				t.Fatalf("error=%+v", errorBody)
			}
			persisted, err := st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, store.ModeFull)
			if err != nil {
				t.Fatal(err)
			}
			if persisted.Title != todo.Title || persisted.CreatedByUserID == nil || *persisted.CreatedByUserID != ownerID {
				t.Fatalf("invalid combined patch partially persisted: %+v", persisted)
			}
		})
	}

	resp, out := callTodoUpdateMCP(t, ownerClient, ts.URL, "legacy", "todos_update", map[string]any{
		"projectSlug": project.Slug,
		"localId":     todo.LocalID,
		"patch": map[string]any{
			"title":           "must not persist",
			"createdByUserId": int64(999999),
		},
	})
	if resp.StatusCode != http.StatusBadRequest || out["ok"] != false {
		t.Fatalf("nonexistent creator status=%d response=%+v", resp.StatusCode, out)
	}
	persisted, err := st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, store.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Title != todo.Title || mcpCreatorAuditCount(t, db, todo.ID) != 0 {
		t.Fatalf("nonexistent creator partially persisted: %+v", persisted)
	}
}

func TestTodoCreatorUpdateMCPRequiresMaintainerForSameAndDifferentValues(t *testing.T) {
	ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
	defer cleanup()
	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, db)
	project, ctx := createTodoUpdateMCPProject(t, st, ownerID, "MCP creator authorization")
	contributor, err := st.CreateUser(context.Background(), "mcp-creator-contributor@example.com", "password123", "Contributor")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectMember(ctx, ownerID, project.ID, contributor.ID, store.RoleContributor); err != nil {
		t.Fatal(err)
	}
	contributorClient := loginTodoUpdateMCPUser(t, ts, contributor.Email, "password123")
	todo := createTodoUpdateMCPTodo(t, st, ctx, project.ID, "authorization title", &contributor.ID, nil)

	for _, tc := range []struct {
		name      string
		creatorID int64
	}{
		{name: "same value", creatorID: ownerID},
		{name: "different value", creatorID: contributor.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, out := callTodoUpdateMCP(t, contributorClient, ts.URL, "legacy", "todos_update", map[string]any{
				"projectSlug": project.Slug,
				"localId":     todo.LocalID,
				"patch": map[string]any{
					"body":            "must not persist",
					"createdByUserId": tc.creatorID,
				},
			})
			if resp.StatusCode != http.StatusForbidden || out["error"].(map[string]any)["code"] != "FORBIDDEN" {
				t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
			}
			persisted, err := st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, store.ModeFull)
			if err != nil {
				t.Fatal(err)
			}
			if persisted.Body != todo.Body || persisted.CreatedByUserID == nil || *persisted.CreatedByUserID != ownerID {
				t.Fatalf("unauthorized combined patch partially persisted: %+v", persisted)
			}
		})
	}
}
