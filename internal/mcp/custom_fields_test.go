package mcp_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"scrumboy/internal/store"
)

func customFieldMCPError(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	errObj, _ := out["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("missing error object: %+v", out)
	}
	return errObj
}

// TestCustomFieldsMCPRolesAndAtomicity mirrors the REST rules: only maintainers manage
// definitions, a contributor's notes-only scope rejects customFields, a patch with any invalid
// value changes nothing, and todos_get projects the stored values.
func TestCustomFieldsMCPRolesAndAtomicity(t *testing.T) {
	ts, db, st, cleanup := newTodoUpdateMCPServer(t, "full")
	t.Cleanup(cleanup)
	bootstrapUser(t, newCookieClient(t, ts), ts.URL)
	ownerID := firstUserID(t, db)
	project, ctx := createTodoUpdateMCPProject(t, st, ownerID, "cf mcp roles")
	if _, err := st.CreateCustomField(ctx, project.ID, "points", "Points", store.CustomFieldNumber); err != nil {
		t.Fatal(err)
	}
	clients := map[string]*http.Client{}
	ids := map[string]int64{}
	for name, role := range map[string]store.ProjectRole{"maintainer": store.RoleMaintainer, "contributor": store.RoleContributor, "viewer": store.RoleViewer} {
		u, err := st.CreateUser(context.Background(), "cf-mcp-"+name+"@example.com", "password123", name)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.AddProjectMember(ctx, ownerID, project.ID, u.ID, role); err != nil {
			t.Fatal(err)
		}
		clients[name] = loginTodoUpdateMCPUser(t, ts, u.Email, "password123")
		ids[name] = u.ID
	}
	contributorID := ids["contributor"]
	todo, err := st.CreateTodo(ctx, project.ID, store.CreateTodoInput{
		Title: "t", Body: "notes", ColumnKey: store.DefaultColumnBacklog, AssigneeUserID: &contributorID,
		CustomFields: store.CustomFieldValues{"points": float64(3)},
	}, store.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	unchanged := func(what string) {
		t.Helper()
		got, err := st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, store.ModeFull)
		if err != nil || got.Title != "t" || got.Body != "notes" || len(got.CustomFields) != 1 || got.CustomFields["points"] != float64(3) {
			t.Fatalf("%s changed the todo: %+v %v", what, got, err)
		}
	}
	code := func(out map[string]any) string {
		errObj, _ := out["error"].(map[string]any)
		c, _ := errObj["code"].(string)
		return c
	}

	for _, name := range []string{"contributor", "viewer"} {
		resp, out := callTodoUpdateMCP(t, clients[name], ts.URL, "legacy", "customFields_create", map[string]any{"projectSlug": project.Slug, "name": "Sneaky", "type": "text"})
		if resp.StatusCode != http.StatusForbidden || code(out) != "FORBIDDEN" {
			t.Fatalf("%s create: %d %+v", name, resp.StatusCode, out)
		}
		resp, out = callTodoUpdateMCP(t, clients[name], ts.URL, "legacy", "customFields_delete", map[string]any{"projectSlug": project.Slug, "key": "points"})
		if resp.StatusCode != http.StatusForbidden || code(out) != "FORBIDDEN" {
			t.Fatalf("%s delete: %d %+v", name, resp.StatusCode, out)
		}
		if resp, out := callTodoUpdateMCP(t, clients[name], ts.URL, "legacy", "customFields_list", map[string]any{"projectSlug": project.Slug}); resp.StatusCode != http.StatusOK {
			t.Fatalf("%s list: %d %+v", name, resp.StatusCode, out)
		}
	}

	resp, out := callTodoUpdateMCP(t, clients["contributor"], ts.URL, "legacy", "todos_update", map[string]any{
		"projectSlug": project.Slug, "localId": todo.LocalID,
		"patch": map[string]any{"body": "contributor notes", "customFields": map[string]any{"points": 99}},
	})
	if resp.StatusCode != http.StatusForbidden || code(out) != "FORBIDDEN" {
		t.Fatalf("contributor customFields update: %d %+v", resp.StatusCode, out)
	}
	unchanged("contributor customFields update")

	for name, fields := range map[string]map[string]any{
		"unknown key":       {"points": 5, "nope": "x"},
		"string for number": {"points": "5"},
		"out of range":      {"points": float64(1 << 54)},
	} {
		resp, out := callTodoUpdateMCP(t, clients["maintainer"], ts.URL, "legacy", "todos_update", map[string]any{
			"projectSlug": project.Slug, "localId": todo.LocalID,
			"patch": map[string]any{"title": "renamed", "body": "changed", "customFields": fields},
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: %d %+v", name, resp.StatusCode, out)
		}
		unchanged(name)
	}

	resp, out = callTodoUpdateMCP(t, clients["viewer"], ts.URL, "legacy", "todos_get", map[string]any{"projectSlug": project.Slug, "localId": todo.LocalID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("viewer get: %d %+v", resp.StatusCode, out)
	}
	data, _ := out["data"].(map[string]any)
	got, _ := data["todo"].(map[string]any)
	if got == nil {
		got = data
	}
	if fields, _ := got["customFields"].(map[string]any); fields["points"] != float64(3) {
		t.Fatalf("todos_get customFields = %v (data %v)", got["customFields"], data)
	}
}

func TestCustomFieldsMCP(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
			t.Cleanup(cleanup)
			client := newCookieClient(t, ts)
			bootstrapUser(t, client, ts.URL)
			ctx := store.WithUserID(context.Background(), firstUserID(t, sqlDB))
			project, err := st.CreateProject(ctx, "cf mcp "+transport)
			if err != nil {
				t.Fatalf("create project: %v", err)
			}
			todo, err := st.CreateTodo(ctx, project.ID, store.CreateTodoInput{Title: "t", ColumnKey: store.DefaultColumnBacklog}, store.ModeFull)
			if err != nil {
				t.Fatalf("create todo: %v", err)
			}

			// call returns the HTTP status for legacy, and 200/400 derived from
			// isError for JSON-RPC (which reports tool errors inside a 200).
			call := func(tool string, args map[string]any) (int, map[string]any) {
				t.Helper()
				resp, out := callTodoUpdateMCP(t, client, ts.URL, transport, tool, args)
				if transport == "jsonrpc" && resp.StatusCode == http.StatusOK {
					if result, _ := out["result"].(map[string]any); result["isError"] == true {
						return http.StatusBadRequest, out
					}
				}
				return resp.StatusCode, out
			}

			if status, out := call("customFields_create", map[string]any{"projectSlug": project.Slug, "name": "Merge Request", "type": "url"}); status != http.StatusOK {
				t.Fatalf("create status=%d %+v", status, out)
			} else {
				field, _ := workflowMCPData(t, transport, out)["field"].(map[string]any)
				if field["key"] != "merge_request" || field["name"] != "Merge Request" || field["type"] != "url" || field["position"] != float64(0) {
					t.Fatalf("create field=%v", field)
				}
			}
			if status, out := call("customFields_create", map[string]any{"projectSlug": project.Slug, "name": "Bad", "type": "boolean"}); status != http.StatusBadRequest {
				t.Fatalf("bad type status=%d %+v", status, out)
			}
			if status, out := call("customFields_list", map[string]any{"projectSlug": project.Slug}); status != http.StatusOK {
				t.Fatalf("list status=%d %+v", status, out)
			} else if items := workflowMCPData(t, transport, out)["items"].([]any); len(items) != 1 || items[0].(map[string]any)["key"] != "merge_request" {
				t.Fatalf("list items=%v", items)
			}

			status, out := call("todos_create", map[string]any{
				"projectSlug":  project.Slug,
				"title":        "created with fields",
				"customFields": map[string]any{"merge_request": "https://example.com/mr/2"},
			})
			if status != http.StatusOK {
				t.Fatalf("create todo status=%d %+v", status, out)
			}
			created := workflowMCPData(t, transport, out)["todo"].(map[string]any)
			if fields, _ := created["customFields"].(map[string]any); fields["merge_request"] != "https://example.com/mr/2" {
				t.Fatalf("created customFields=%v", created["customFields"])
			}
			if status, out := call("todos_create", map[string]any{"projectSlug": project.Slug, "title": "x", "customFields": map[string]any{"nope": "y"}}); status != http.StatusBadRequest {
				t.Fatalf("unknown field create status=%d %+v", status, out)
			}

			update := func(fields any) (int, map[string]any) {
				return call("todos_update", map[string]any{
					"projectSlug": project.Slug,
					"localId":     todo.LocalID,
					"patch":       map[string]any{"customFields": fields},
				})
			}
			status, out = update(map[string]any{"merge_request": "https://example.com/mr/9"})
			if status != http.StatusOK {
				t.Fatalf("update status=%d %+v", status, out)
			}
			data := assertTodoUpdateMCPSuccess(t, transport, out, project.Slug, todo.LocalID)
			fields, _ := data["customFields"].(map[string]any)
			if fields["merge_request"] != "https://example.com/mr/9" {
				t.Fatalf("customFields=%v", data["customFields"])
			}

			for name, bad := range map[string]any{
				"unknown key": map[string]any{"nope": "x"},
				"bad url":     map[string]any{"merge_request": "javascript:alert(1)"},
				"bool value":  map[string]any{"merge_request": true},
				"not object":  []any{"x"},
				"null":        nil,
			} {
				if status, out := update(bad); status != http.StatusBadRequest {
					t.Errorf("%s: status=%d %+v, want 400", name, status, out)
				}
			}

			if status, out := update(map[string]any{"merge_request": nil}); status != http.StatusOK {
				t.Fatalf("clear status=%d %+v", status, out)
			}
			read, err := st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, store.ModeFull)
			if err != nil || len(read.CustomFields) != 0 {
				t.Fatalf("after clear: %v %v", read.CustomFields, err)
			}

			if status, out := call("customFields_delete", map[string]any{"projectSlug": project.Slug, "key": "  Merge_Request  "}); status != http.StatusOK {
				t.Fatalf("delete status=%d %+v", status, out)
			} else {
				deleted, _ := workflowMCPData(t, transport, out)["deleted"].(map[string]any)
				if deleted["projectSlug"] != project.Slug || deleted["key"] != "Merge_Request" {
					t.Fatalf("deleted=%v", deleted)
				}
			}
		})
	}
}

func TestCustomFieldsMCPDefinitionContracts(t *testing.T) {
	ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
	t.Cleanup(cleanup)
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ctx := store.WithUserID(context.Background(), firstUserID(t, sqlDB))
	project, err := st.CreateProject(ctx, "cf mcp contracts")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
	t.Cleanup(stream.close)

	call := func(tool string, args map[string]any) (int, map[string]any) {
		t.Helper()
		resp, out := callTodoUpdateMCP(t, client, ts.URL, "legacy", tool, args)
		return resp.StatusCode, out
	}

	t.Run("empty and invalid name type and key", func(t *testing.T) {
		status, out := call("customFields_create", map[string]any{"projectSlug": project.Slug, "name": "   ", "type": "text"})
		if status != http.StatusBadRequest {
			t.Fatalf("empty name status=%d %+v", status, out)
		}
		errObj := customFieldMCPError(t, out)
		details, _ := errObj["details"].(map[string]any)
		if errObj["code"] != "VALIDATION_ERROR" || errObj["message"] != "name required" || details["field"] != "name" {
			t.Fatalf("empty name error=%+v", out)
		}
		status, out = call("customFields_create", map[string]any{"projectSlug": project.Slug, "name": "X", "type": "boolean"})
		if status != http.StatusBadRequest {
			t.Fatalf("bad type status=%d %+v", status, out)
		}
		errObj = customFieldMCPError(t, out)
		details, _ = errObj["details"].(map[string]any)
		if errObj["message"] != "type must be text, url or number" || details["field"] != "type" {
			t.Fatalf("bad type error=%+v", out)
		}
		status, out = call("customFields_delete", map[string]any{"projectSlug": project.Slug, "key": "   "})
		if status != http.StatusBadRequest {
			t.Fatalf("empty key status=%d %+v", status, out)
		}
		errObj = customFieldMCPError(t, out)
		details, _ = errObj["details"].(map[string]any)
		if errObj["message"] != "missing key" || details["field"] != "key" {
			t.Fatalf("empty key error=%+v", out)
		}
		status, out = call("customFields_create", map[string]any{"name": "X", "type": "text"})
		if status != http.StatusBadRequest {
			t.Fatalf("missing slug status=%d %+v", status, out)
		}
		errObj = customFieldMCPError(t, out)
		details, _ = errObj["details"].(map[string]any)
		if errObj["message"] != "missing projectSlug" || details["field"] != "projectSlug" {
			t.Fatalf("missing slug error=%+v", out)
		}
	})

	t.Run("validation precedes lookup and authorization", func(t *testing.T) {
		status, out := call("customFields_create", map[string]any{"projectSlug": "no-such-board", "name": "", "type": "boolean"})
		if status != http.StatusBadRequest {
			t.Fatalf("unknown slug empty name status=%d %+v", status, out)
		}
		if customFieldMCPError(t, out)["message"] != "name required" {
			t.Fatalf("unknown slug empty name error=%+v", out)
		}
		status, out = call("customFields_delete", map[string]any{"projectSlug": "no-such-board", "key": "   "})
		if status != http.StatusBadRequest || customFieldMCPError(t, out)["message"] != "missing key" {
			t.Fatalf("unknown slug empty key=%d %+v", status, out)
		}
		status, out = call("customFields_create", map[string]any{"projectSlug": "no-such-board", "name": "X", "type": "text"})
		if status != http.StatusNotFound {
			t.Fatalf("unknown slug valid create status=%d %+v", status, out)
		}
	})

	t.Run("store constraints and conflict", func(t *testing.T) {
		status, out := call("customFields_create", map[string]any{"projectSlug": project.Slug, "name": "Ticket", "type": "text", "key": "ticket"})
		if status != http.StatusOK {
			t.Fatalf("create status=%d %+v", status, out)
		}
		status, out = call("customFields_create", map[string]any{"projectSlug": project.Slug, "name": "Dup", "type": "url", "key": "ticket"})
		if status != http.StatusConflict {
			t.Fatalf("duplicate status=%d %+v", status, out)
		}
		if customFieldMCPError(t, out)["code"] != "CONFLICT" || customFieldMCPError(t, out)["message"] != "conflict: custom field key already exists" {
			t.Fatalf("duplicate error=%+v", out)
		}
		status, out = call("customFields_create", map[string]any{"projectSlug": project.Slug, "name": "x", "type": "text", "key": "Bad Key!"})
		if status != http.StatusBadRequest {
			t.Fatalf("invalid key status=%d %+v", status, out)
		}
		if customFieldMCPError(t, out)["message"] != "validation: invalid custom field key" {
			t.Fatalf("invalid key error=%+v", out)
		}
		status, out = call("customFields_create", map[string]any{"projectSlug": project.Slug, "name": strings.Repeat("n", 101), "type": "text"})
		if status != http.StatusBadRequest {
			t.Fatalf("long name status=%d %+v", status, out)
		}
		if customFieldMCPError(t, out)["message"] != "validation: invalid custom field name" {
			t.Fatalf("long name error=%+v", out)
		}
		status, out = call("customFields_delete", map[string]any{"projectSlug": project.Slug, "key": "missing_field"})
		if status != http.StatusNotFound {
			t.Fatalf("missing field status=%d %+v", status, out)
		}
		status, out = call("customFields_delete", map[string]any{"projectSlug": project.Slug, "key": "ticket"})
		if status != http.StatusOK {
			t.Fatalf("delete status=%d %+v", status, out)
		}
	})

	events := collectTodoUpdateMCPEvents(t, stream)
	for _, event := range events {
		if event.Type == "board.refresh_needed" {
			t.Fatalf("definition mutations emitted refresh: %+v", events)
		}
	}
}

func TestCustomFieldsMCPGates(t *testing.T) {
	t.Run("anonymous mode", func(t *testing.T) {
		ts, _, _, cleanup := newTodoUpdateMCPServer(t, "anonymous")
		t.Cleanup(cleanup)
		for _, tool := range []string{"customFields_create", "customFields_delete"} {
			resp, out := callTodoUpdateMCP(t, newStatelessClient(ts), ts.URL, "legacy", tool, map[string]any{"projectSlug": "x", "name": "X", "type": "text", "key": "x"})
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("%s status=%d %+v", tool, resp.StatusCode, out)
			}
			errObj := customFieldMCPError(t, out)
			if errObj["code"] != "CAPABILITY_UNAVAILABLE" || errObj["message"] != tool+" is unavailable in anonymous mode" {
				t.Fatalf("%s error=%+v", tool, out)
			}
		}
	})
	t.Run("before bootstrap", func(t *testing.T) {
		ts, _, _, cleanup := newTodoUpdateMCPServer(t, "full")
		t.Cleanup(cleanup)
		for _, tool := range []string{"customFields_create", "customFields_delete"} {
			resp, out := callTodoUpdateMCP(t, newStatelessClient(ts), ts.URL, "legacy", tool, map[string]any{"projectSlug": "x", "name": "X", "type": "text", "key": "x"})
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("%s status=%d %+v", tool, resp.StatusCode, out)
			}
			errObj := customFieldMCPError(t, out)
			if errObj["code"] != "CAPABILITY_UNAVAILABLE" || errObj["message"] != tool+" is unavailable before bootstrap" {
				t.Fatalf("%s error=%+v", tool, out)
			}
		}
	})
	t.Run("unauthenticated after bootstrap", func(t *testing.T) {
		ts, _, st, cleanup := newTodoUpdateMCPServer(t, "full")
		t.Cleanup(cleanup)
		if _, err := st.BootstrapUser(context.Background(), "cf-gate@example.com", "password123", "Owner"); err != nil {
			t.Fatal(err)
		}
		resp, out := callTodoUpdateMCP(t, newStatelessClient(ts), ts.URL, "legacy", "customFields_create", map[string]any{"projectSlug": "x", "name": "X", "type": "text"})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status=%d %+v", resp.StatusCode, out)
		}
		errObj := customFieldMCPError(t, out)
		if errObj["code"] != "AUTH_REQUIRED" || errObj["message"] != "Sign-in required for this tool" {
			t.Fatalf("error=%+v", out)
		}
	})
}
