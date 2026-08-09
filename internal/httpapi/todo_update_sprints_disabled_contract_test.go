package httpapi

import (
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func TestTodoUpdateRESTDisabledSprintFieldMatrix(t *testing.T) {
	cases := []struct {
		name             string
		configurePayload func(map[string]any, store.Todo, store.Sprint, store.Sprint)
		wantStatus       int
		wantTitle        string
		wantSprint       bool
		wantAuditDelta   int
		wantRefresh      bool
	}{
		{
			name: "omitted sprint field preserves dormant association",
			configurePayload: func(payload map[string]any, _ store.Todo, _, _ store.Sprint) {
				payload["title"] = "unrelated edit succeeded"
			},
			wantStatus: http.StatusOK, wantTitle: "unrelated edit succeeded", wantSprint: true, wantAuditDelta: 1, wantRefresh: true,
		},
		{
			name: "explicit same sprint is accepted semantic no-op",
			configurePayload: func(payload map[string]any, _ store.Todo, current, _ store.Sprint) {
				payload["sprintId"] = current.ID
			},
			wantStatus: http.StatusOK, wantTitle: "before", wantSprint: true, wantAuditDelta: 0, wantRefresh: true,
		},
		{
			name: "explicit null clears dormant association",
			configurePayload: func(payload map[string]any, _ store.Todo, _, _ store.Sprint) {
				payload["sprintId"] = nil
			},
			wantStatus: http.StatusOK, wantTitle: "before", wantSprint: false, wantAuditDelta: 1, wantRefresh: true,
		},
		{
			name: "different sprint is rejected without effects",
			configurePayload: func(payload map[string]any, _ store.Todo, _, different store.Sprint) {
				payload["title"] = "must not persist"
				payload["sprintId"] = different.ID
			},
			wantStatus: http.StatusBadRequest, wantTitle: "before", wantSprint: true, wantAuditDelta: 0, wantRefresh: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
			defer cleanup()
			st := wireTodoUpdatePublisher(t, ts)
			client := newCookieClient(t)
			owner := bootstrapUserClient(t, client, ts.URL, "Owner", "rest-disabled-update-"+fmt.Sprint(time.Now().UnixNano())+"@example.com", "password123")
			ownerID := int64(owner["id"].(float64))
			project, ownerCtx := createTodoUpdateProject(t, st, ownerID, "REST disabled update "+tc.name)
			now := time.Now().UTC()
			current, err := st.CreateSprint(ownerCtx, project.ID, "Current", now, now.Add(7*24*time.Hour))
			if err != nil {
				t.Fatalf("CreateSprint current: %v", err)
			}
			different, err := st.CreateSprint(ownerCtx, project.ID, "Different", now.Add(8*24*time.Hour), now.Add(15*24*time.Hour))
			if err != nil {
				t.Fatalf("CreateSprint different: %v", err)
			}
			todo, err := st.CreateTodo(ownerCtx, project.ID, store.CreateTodoInput{
				Title: "before", Body: "body", Tags: []string{"tag"}, ColumnKey: store.DefaultColumnBacklog, SprintID: &current.ID,
			}, store.ModeFull)
			if err != nil {
				t.Fatalf("CreateTodo: %v", err)
			}
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
			beforeAudits := countTodoUpdateAudits(t, sqlDB, todo.ID)
			beforeAssignments := countTodoAssignmentRows(t, sqlDB, todo.ID)
			stream := subscribeTodoUpdateEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")

			payload := todoUpdateRESTPayload(todo.Title, todo.Body, todo.Tags, todo.EstimationPoints, todo.AssigneeUserID)
			tc.configurePayload(payload, todo, current, different)
			var response map[string]any
			resp, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/board/%s/todos/%d", ts.URL, project.Slug, todo.LocalID), payload, &response)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("PATCH status=%d want=%d body=%s", resp.StatusCode, tc.wantStatus, body)
			}

			if tc.wantStatus == http.StatusOK {
				if _, exposed := response["sprintId"]; exposed {
					t.Fatalf("disabled response exposed effective sprint assignment: %+v", response)
				}
				if response["title"] != tc.wantTitle {
					t.Fatalf("response title=%v want=%q", response["title"], tc.wantTitle)
				}
			} else {
				errorBody, ok := response["error"].(map[string]any)
				if !ok || errorBody["code"] != "VALIDATION_ERROR" || errorBody["message"] != store.ErrSprintsDisabled.Error() {
					t.Fatalf("disabled error response=%+v", response)
				}
				details, _ := errorBody["details"].(map[string]any)
				if details["reason"] != "sprints_disabled" {
					t.Fatalf("disabled error details=%+v", details)
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
			if tc.wantStatus == http.StatusOK && persistedUpdatedAt <= 1 {
				t.Fatalf("successful explicit/omitted update did not execute persistence: updated_at=%d", persistedUpdatedAt)
			}
			if tc.wantStatus != http.StatusOK && persistedUpdatedAt != 1 {
				t.Fatalf("rejected update changed todo timestamp: %d", persistedUpdatedAt)
			}
			if got := countTodoUpdateAudits(t, sqlDB, todo.ID) - beforeAudits; got != tc.wantAuditDelta {
				t.Fatalf("todo_updated audit delta=%d want=%d", got, tc.wantAuditDelta)
			}
			if got := countTodoAssignmentRows(t, sqlDB, todo.ID); got != beforeAssignments {
				t.Fatalf("assignment ledger rows=%d want unchanged %d", got, beforeAssignments)
			}
			var afterProjectUpdatedAt int64
			if err := sqlDB.QueryRow(`SELECT updated_at FROM projects WHERE id = ?`, project.ID).Scan(&afterProjectUpdatedAt); err != nil {
				t.Fatalf("read project timestamp after update: %v", err)
			}
			if tc.wantStatus != http.StatusOK && afterProjectUpdatedAt != beforeProjectUpdatedAt {
				t.Fatalf("rejected update changed project timestamp: before=%d after=%d", beforeProjectUpdatedAt, afterProjectUpdatedAt)
			}

			events := collectTodoUpdateEvents(t, stream)
			if tc.wantRefresh {
				assertTodoUpdateRefreshes(t, events, project.ID, "todo_updated", 0)
			} else if len(events) != 0 {
				t.Fatalf("rejected update published events: %+v", events)
			}
		})
	}
}
