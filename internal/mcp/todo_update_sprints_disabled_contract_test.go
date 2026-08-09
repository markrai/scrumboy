package mcp_test

import (
	"database/sql"
	"net/http"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func disabledTodoUpdateMCPError(t *testing.T, transport string, resp *http.Response, response map[string]any) map[string]any {
	t.Helper()
	if transport == "legacy" {
		if resp.StatusCode != http.StatusBadRequest || response["ok"] != false {
			t.Fatalf("legacy disabled response status=%d body=%+v", resp.StatusCode, response)
		}
		return response["error"].(map[string]any)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("JSON-RPC disabled response status=%d body=%+v", resp.StatusCode, response)
	}
	result := response["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("JSON-RPC disabled result=%+v", result)
	}
	return result["structuredContent"].(map[string]any)
}

func TestTodoUpdateMCPDisabledSprintFieldMatrix(t *testing.T) {
	cases := []struct {
		name           string
		patch          func(store.Todo, store.Sprint, store.Sprint) map[string]any
		wantSuccess    bool
		wantTitle      string
		wantSprint     bool
		wantAuditDelta int
	}{
		{
			name: "omitted sprint field preserves dormant association",
			patch: func(_ store.Todo, _, _ store.Sprint) map[string]any {
				return map[string]any{"title": "unrelated edit succeeded"}
			},
			wantSuccess: true, wantTitle: "unrelated edit succeeded", wantSprint: true, wantAuditDelta: 1,
		},
		{
			name: "explicit same sprint is accepted semantic no-op",
			patch: func(_ store.Todo, current, _ store.Sprint) map[string]any {
				return map[string]any{"sprintId": current.ID}
			},
			wantSuccess: true, wantTitle: "before", wantSprint: true, wantAuditDelta: 0,
		},
		{
			name: "explicit null clears dormant association",
			patch: func(_ store.Todo, _, _ store.Sprint) map[string]any {
				return map[string]any{"sprintId": nil}
			},
			wantSuccess: true, wantTitle: "before", wantSprint: false, wantAuditDelta: 1,
		},
		{
			name: "different sprint is rejected without effects",
			patch: func(_ store.Todo, _, different store.Sprint) map[string]any {
				return map[string]any{"title": "must not persist", "sprintId": different.ID}
			},
			wantSuccess: false, wantTitle: "before", wantSprint: true, wantAuditDelta: 0,
		},
	}

	for _, transport := range []string{"legacy", "jsonrpc"} {
		for _, tc := range cases {
			t.Run(transport+"/"+tc.name, func(t *testing.T) {
				ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
				defer cleanup()
				client := newCookieClient(t, ts)
				bootstrapUser(t, client, ts.URL)
				ownerID := firstUserID(t, sqlDB)
				project, ownerCtx := createTodoUpdateMCPProject(t, st, ownerID, "MCP disabled update "+transport+" "+tc.name)
				now := time.Now().UTC()
				current, err := st.CreateSprint(ownerCtx, project.ID, "Current", now, now.Add(7*24*time.Hour))
				if err != nil {
					t.Fatalf("CreateSprint current: %v", err)
				}
				different, err := st.CreateSprint(ownerCtx, project.ID, "Different", now.Add(8*24*time.Hour), now.Add(15*24*time.Hour))
				if err != nil {
					t.Fatalf("CreateSprint different: %v", err)
				}
				todo := createTodoUpdateMCPTodo(t, st, ownerCtx, project.ID, "before", nil, &current.ID)
				if err := st.UpdateProjectSprintsEnabled(ownerCtx, project.ID, ownerID, false); err != nil {
					t.Fatalf("disable sprints: %v", err)
				}
				if _, err := sqlDB.Exec(`UPDATE todos SET updated_at = 1 WHERE id = ?`, todo.ID); err != nil {
					t.Fatalf("set todo timestamp sentinel: %v", err)
				}
				var beforeProjectUpdatedAt int64
				if err := sqlDB.QueryRow(`SELECT updated_at FROM projects WHERE id = ?`, project.ID).Scan(&beforeProjectUpdatedAt); err != nil {
					t.Fatalf("read project timestamp: %v", err)
				}
				beforeAudits := todoUpdateMCPAuditCount(t, sqlDB, todo.ID)
				beforeAssignments := todoUpdateMCPAssignmentCount(t, sqlDB, todo.ID)
				stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
				defer stream.close()

				resp, out := callTodoUpdateMCP(t, client, ts.URL, transport, "todos_update", map[string]any{
					"projectSlug": project.Slug,
					"localId":     todo.LocalID,
					"patch":       tc.patch(todo, current, different),
				})
				if tc.wantSuccess {
					if resp.StatusCode != http.StatusOK {
						t.Fatalf("successful update status=%d response=%+v", resp.StatusCode, out)
					}
					returned := assertTodoUpdateMCPSuccess(t, transport, out, project.Slug, todo.LocalID)
					if returned["sprintId"] != nil {
						t.Fatalf("disabled response exposed effective sprint assignment: %+v", returned)
					}
					if returned["title"] != tc.wantTitle {
						t.Fatalf("returned title=%v want=%q", returned["title"], tc.wantTitle)
					}
				} else {
					publicError := disabledTodoUpdateMCPError(t, transport, resp, out)
					if publicError["code"] != "VALIDATION_ERROR" || publicError["message"] != store.ErrSprintsDisabled.Error() {
						t.Fatalf("disabled public error=%+v", publicError)
					}
					details, _ := publicError["details"].(map[string]any)
					if details["reason"] != "sprints_disabled" {
						t.Fatalf("disabled public error details=%+v", details)
					}
				}

				var persistedTitle string
				var persistedSprint sql.NullInt64
				var persistedUpdatedAt int64
				if err := sqlDB.QueryRow(`SELECT title, sprint_id, updated_at FROM todos WHERE id = ?`, todo.ID).Scan(&persistedTitle, &persistedSprint, &persistedUpdatedAt); err != nil {
					t.Fatalf("read persisted todo: %v", err)
				}
				if persistedTitle != tc.wantTitle || persistedSprint.Valid != tc.wantSprint || (persistedSprint.Valid && persistedSprint.Int64 != current.ID) {
					t.Fatalf("persisted todo title=%q sprint=%+v, want title=%q sprint current=%v", persistedTitle, persistedSprint, tc.wantTitle, tc.wantSprint)
				}
				if tc.wantSuccess && persistedUpdatedAt <= 1 {
					t.Fatalf("successful explicit/omitted update did not execute persistence: updated_at=%d", persistedUpdatedAt)
				}
				if !tc.wantSuccess && persistedUpdatedAt != 1 {
					t.Fatalf("rejected update changed todo timestamp: %d", persistedUpdatedAt)
				}
				if got := todoUpdateMCPAuditCount(t, sqlDB, todo.ID) - beforeAudits; got != tc.wantAuditDelta {
					t.Fatalf("todo_updated audit delta=%d want=%d", got, tc.wantAuditDelta)
				}
				if got := todoUpdateMCPAssignmentCount(t, sqlDB, todo.ID); got != beforeAssignments {
					t.Fatalf("assignment ledger rows=%d want unchanged %d", got, beforeAssignments)
				}
				var afterProjectUpdatedAt int64
				if err := sqlDB.QueryRow(`SELECT updated_at FROM projects WHERE id = ?`, project.ID).Scan(&afterProjectUpdatedAt); err != nil {
					t.Fatalf("read project timestamp after update: %v", err)
				}
				if !tc.wantSuccess && afterProjectUpdatedAt != beforeProjectUpdatedAt {
					t.Fatalf("rejected update changed project timestamp: before=%d after=%d", beforeProjectUpdatedAt, afterProjectUpdatedAt)
				}
				if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
					t.Fatalf("MCP sprint-only/non-assignment update published events: %+v", events)
				}
			})
		}
	}
}
