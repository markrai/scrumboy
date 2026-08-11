package httpapi

import (
	"fmt"
	"net/http"
	"testing"

	"scrumboy/internal/store"
)

func TestTodoPriorityRESTPatchPresenceContract(t *testing.T) {
	for _, route := range []struct {
		name string
		url  func(*priorityRESTFixture, store.Todo) string
	}{
		{name: "slug", url: func(fx *priorityRESTFixture, todo store.Todo) string {
			return fmt.Sprintf("%s/api/board/%s/todos/%d", fx.ts.URL, fx.project.Slug, todo.LocalID)
		}},
		{name: "legacy_numeric", url: func(fx *priorityRESTFixture, todo store.Todo) string {
			return fmt.Sprintf("%s/api/todos/%d", fx.ts.URL, todo.ID)
		}},
	} {
		t.Run(route.name, func(t *testing.T) {
			fx := newPriorityRESTFixture(t, "priority-rest-tristate-"+route.name)
			high := "high"
			todo, err := fx.st.CreateTodo(fx.ctx, fx.project.ID, store.CreateTodoInput{Title: "tri-state", PriorityKey: &high}, store.ModeFull)
			if err != nil {
				t.Fatalf("create todo: %v", err)
			}
			base := map[string]any{
				"title": todo.Title, "body": todo.Body, "tags": []string{},
				"estimationPoints": nil, "assigneeUserId": nil,
			}

			assertPatch := func(name string, mutate func(map[string]any), want *string) {
				t.Helper()
				payload := make(map[string]any, len(base)+1)
				for key, value := range base {
					payload[key] = value
				}
				mutate(payload)
				resp, raw := doJSON(t, fx.client, http.MethodPatch, route.url(fx, todo), payload, nil)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("%s status=%d body=%s", name, resp.StatusCode, raw)
				}
				got, err := fx.st.GetTodoByLocalID(fx.ctx, fx.project.ID, todo.LocalID, store.ModeFull)
				if err != nil {
					t.Fatalf("%s reload: %v", name, err)
				}
				if (got.PriorityKey == nil) != (want == nil) || got.PriorityKey != nil && *got.PriorityKey != *want {
					t.Fatalf("%s priority=%v want=%v", name, got.PriorityKey, want)
				}
			}

			assertPatch("omitted", func(map[string]any) {}, stringPtr("high"))
			assertPatch("clear", func(payload map[string]any) { payload["priorityKey"] = nil }, nil)
			assertPatch("assign", func(payload map[string]any) { payload["priorityKey"] = "urgent" }, stringPtr("urgent"))
		})
	}
}

func stringPtr(value string) *string { return &value }
