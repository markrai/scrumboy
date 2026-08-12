package mcp_test

import (
	"context"
	"net/http"
	"testing"

	"scrumboy/internal/store"
)

func TestPriorityMutationMCPTransportAndRealtimeContracts(t *testing.T) {
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
				project, err := st.CreateProject(ctx, "priority MCP "+transport+" "+operation)
				if err != nil {
					t.Fatalf("create project: %v", err)
				}

				tool := "priorities_" + operation
				args := map[string]any{"projectSlug": project.Slug}
				var targetKey string
				switch operation {
				case "create":
					args["name"] = "Critical"
					targetKey = "critical"
				case "update":
					args["priorityKey"] = "low"
					args["name"] = "Chill"
					args["color"] = "#123456"
					targetKey = "low"
				case "delete":
					tier, err := st.AddPriorityTier(ctx, project.ID, "Disposable")
					if err != nil {
						t.Fatalf("add delete fixture: %v", err)
					}
					args["priorityKey"] = tier.Key
					targetKey = tier.Key
				}

				stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
				defer stream.close()
				resp, out := callTodoUpdateMCP(t, client, ts.URL, transport, tool, args)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("%s %s status=%d response=%+v", transport, tool, resp.StatusCode, out)
				}
				data := workflowMCPData(t, transport, out)

				tiers, err := st.GetProjectPriorities(ctx, project.ID)
				if err != nil {
					t.Fatalf("get priorities: %v", err)
				}
				switch operation {
				case "create":
					tier := data["priority"].(map[string]any)
					if tier["key"] != targetKey || tier["name"] != "Critical" {
						t.Fatalf("create projection=%+v", tier)
					}
					persisted, ok := priorityTierByKeyMCP(tiers, targetKey)
					if !ok || persisted.Name != "Critical" {
						t.Fatalf("create persistence=%+v", tiers)
					}
				case "update":
					tier := data["priority"].(map[string]any)
					if tier["key"] != targetKey || tier["name"] != "Chill" || tier["color"] != "#123456" {
						t.Fatalf("update projection=%+v", tier)
					}
					persisted, ok := priorityTierByKeyMCP(tiers, targetKey)
					if !ok || persisted.Name != "Chill" || persisted.Color != "#123456" {
						t.Fatalf("update persistence=%+v", tiers)
					}
				case "delete":
					deleted := data["deleted"].(map[string]any)
					if deleted["projectSlug"] != project.Slug || deleted["priorityKey"] != targetKey {
						t.Fatalf("delete projection=%+v", deleted)
					}
					if _, ok := priorityTierByKeyMCP(tiers, targetKey); ok {
						t.Fatalf("deleted tier %q still present: %+v", targetKey, tiers)
					}
				}
				if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
					t.Fatalf("MCP priority mutation emitted realtime events: %+v", events)
				}
			})
		}
	}
}

func TestPriorityListMCPTransportContract(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
			t.Cleanup(cleanup)
			ownerClient := newCookieClient(t, ts)
			bootstrapUser(t, ownerClient, ts.URL)
			ownerID := firstUserID(t, sqlDB)
			ctx := store.WithUserID(context.Background(), ownerID)
			project, err := st.CreateProject(ctx, "priority MCP list "+transport)
			if err != nil {
				t.Fatalf("create project: %v", err)
			}
			viewer, err := st.CreateUser(context.Background(), "priority-list-"+transport+"@example.com", "password123", "Viewer")
			if err != nil {
				t.Fatalf("create viewer: %v", err)
			}
			if err := st.AddProjectMember(ctx, ownerID, project.ID, viewer.ID, store.RoleViewer); err != nil {
				t.Fatalf("add viewer: %v", err)
			}
			viewerClient := loginTodoUpdateMCPUser(t, ts, viewer.Email, "password123")

			resp, out := callTodoUpdateMCP(t, viewerClient, ts.URL, transport, "priorities_list", map[string]any{"projectSlug": project.Slug})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
			}
			data := workflowMCPData(t, transport, out)
			items, ok := data["items"].([]any)
			if !ok || len(items) != 4 {
				t.Fatalf("items=%T %+v", data["items"], data["items"])
			}
			want := []string{"low", "medium", "high", "urgent"}
			for i, raw := range items {
				item := raw.(map[string]any)
				if item["key"] != want[i] || int(item["position"].(float64)) != i || item["name"] == "" || item["color"] == "" {
					t.Fatalf("item[%d]=%+v", i, item)
				}
			}

			outsider, err := st.CreateUser(context.Background(), "priority-outsider-"+transport+"@example.com", "password123", "Outsider")
			if err != nil {
				t.Fatalf("create outsider: %v", err)
			}
			outsiderClient := loginTodoUpdateMCPUser(t, ts, outsider.Email, "password123")
			resp, out = callTodoUpdateMCP(t, outsiderClient, ts.URL, transport, "priorities_list", map[string]any{"projectSlug": project.Slug})
			if transport == "legacy" && resp.StatusCode != http.StatusNotFound {
				t.Fatalf("outsider status=%d response=%+v", resp.StatusCode, out)
			}
			if transport == "jsonrpc" {
				if resp.StatusCode != http.StatusOK || out["result"].(map[string]any)["isError"] != true {
					t.Fatalf("outsider JSON-RPC status=%d response=%+v", resp.StatusCode, out)
				}
			}
		})
	}
}

func priorityTierByKeyMCP(tiers []store.PriorityTier, key string) (store.PriorityTier, bool) {
	for _, tier := range tiers {
		if tier.Key == key {
			return tier, true
		}
	}
	return store.PriorityTier{}, false
}

func TestPriorityMutationMCPSemanticValidationPrecedesAccess(t *testing.T) {
	ts, _, _, cleanup := newTodoUpdateMCPServer(t, "full")
	t.Cleanup(cleanup)
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	t.Run("invalid name wins before missing project", func(t *testing.T) {
		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "priorities_create", map[string]any{
			"projectSlug": "missing-priority-board",
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
		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "priorities_create", map[string]any{
			"projectSlug": "missing-priority-board",
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

func TestPriorityMutationMCPReasonContract(t *testing.T) {
	ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
	t.Cleanup(cleanup)
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	ctx := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctx, "priority MCP reasons")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	low := "low"
	if _, err := st.CreateTodo(ctx, project.ID, store.CreateTodoInput{Title: "in use", PriorityKey: &low}, store.ModeFull); err != nil {
		t.Fatalf("create todo: %v", err)
	}
	resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", "priorities_delete", map[string]any{
		"projectSlug": project.Slug,
		"priorityKey": "low",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
	}
	errBody := out["error"].(map[string]any)
	details := errBody["details"].(map[string]any)
	if errBody["code"] != "CONFLICT" || details["reason"] != "priority_tier_in_use" {
		t.Fatalf("error=%+v", errBody)
	}
}
