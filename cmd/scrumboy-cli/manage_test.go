package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const okData = `{"ok":true,"data":{"items":[],"points":[],"sprint":{"sprintId":3,"name":"S"},"token":{"token":"sb_x"}},"meta":{}}`

// TestManagementCommandsMapToTools checks that every management command sends
// the tool and input the server's catalog expects.
func TestManagementCommandsMapToTools(t *testing.T) {
	cases := []struct {
		args []string
		tool string
		want map[string]any // subset of the input that must match exactly
	}{
		{[]string{"sprint", "get", "3"}, "sprints_get", map[string]any{"projectSlug": "demo", "sprintId": 3.0}},
		{[]string{"sprint", "update", "3", "--name", "N", "--start", "2026-01-01"}, "sprints_update",
			map[string]any{"sprintId": 3.0, "patch": map[string]any{"name": "N", "plannedStartAt": 1767225600000.0}}},
		{[]string{"sprint", "delete", "3", "--yes"}, "sprints_delete", map[string]any{"sprintId": 3.0}},
		{[]string{"tag", "mine"}, "tags_listMine", map[string]any{}},
		{[]string{"tag", "color", "bug", "#FF0000"}, "tags_updateProjectColor", map[string]any{"projectSlug": "demo", "tagName": "bug", "color": "#ff0000"}},
		{[]string{"tag", "color", "--id", "5", "none"}, "tags_updateProjectColor", map[string]any{"tagId": 5.0, "color": nil}},
		{[]string{"tag", "color", "--mine", "--id", "5", "#00ff00"}, "tags_updateMineColor", map[string]any{"tagId": 5.0, "color": "#00ff00"}},
		{[]string{"tag", "delete", "--id", "5", "--yes"}, "tags_deleteProject", map[string]any{"projectSlug": "demo", "tagId": 5.0}},
		{[]string{"tag", "delete", "--mine", "--id", "5", "--yes"}, "tags_deleteMine", map[string]any{"tagId": 5.0}},
		{[]string{"member", "available"}, "members_listAvailable", map[string]any{"projectSlug": "demo"}},
		{[]string{"member", "add", "9", "--role", "viewer"}, "members_add", map[string]any{"userId": 9.0, "role": "viewer"}},
		{[]string{"member", "role", "9", "contributor"}, "members_updateRole", map[string]any{"userId": 9.0, "role": "contributor"}},
		{[]string{"member", "remove", "9", "--yes"}, "members_remove", map[string]any{"userId": 9.0}},
		{[]string{"column", "add", "Review"}, "workflow_create", map[string]any{"name": "Review"}},
		{[]string{"column", "update", "doing", "--name", "WIP", "--color", "#112233"}, "workflow_update", map[string]any{"columnKey": "doing", "name": "WIP", "color": "#112233"}},
		{[]string{"column", "delete", "review", "--yes"}, "workflow_delete", map[string]any{"columnKey": "review"}},
		{[]string{"priority", "add", "Critical"}, "priorities_create", map[string]any{"name": "Critical"}},
		{[]string{"priority", "update", "critical", "--name", "P0", "--color", "#ff0000"}, "priorities_update", map[string]any{"priorityKey": "critical", "name": "P0"}},
		{[]string{"priority", "delete", "critical", "--yes"}, "priorities_delete", map[string]any{"priorityKey": "critical"}},
		{[]string{"field", "list"}, "customFields_list", map[string]any{"projectSlug": "demo"}},
		{[]string{"field", "add", "MR URL", "--type", "url", "--key", "mr_url"}, "customFields_create", map[string]any{"name": "MR URL", "type": "url", "key": "mr_url"}},
		{[]string{"field", "delete", "mr_url", "--yes"}, "customFields_delete", map[string]any{"key": "mr_url"}},
		{[]string{"webhook", "list", "--limit", "5"}, "webhooks_list", map[string]any{"limit": 5.0}},
		{[]string{"webhook", "create", "https://x.test/h", "--event", "todo.created", "--event", "todo.moved", "--durable"}, "webhooks_create",
			map[string]any{"url": "https://x.test/h", "events": []any{"todo.created", "todo.moved"}, "deliveryMode": "durable"}},
		{[]string{"webhook", "delete", "4", "--yes"}, "webhooks_delete", map[string]any{"webhookId": 4.0}},
		{[]string{"dashboard", "summary", "--timezone", "UTC"}, "dashboard_getSummary", map[string]any{"timezone": "UTC"}},
		{[]string{"dashboard", "todos", "--sort", "board"}, "dashboard_listTodos", map[string]any{"sort": "board"}},
		{[]string{"metrics", "burndown", "--sprint", "3"}, "metrics_getBurndown", map[string]any{"projectSlug": "demo", "sprintId": 3.0}},
		{[]string{"metrics", "backlog"}, "metrics_getBacklogSize", map[string]any{"projectSlug": "demo"}},
		{[]string{"admin", "users", "list"}, "admin_listUsers", map[string]any{}},
		{[]string{"admin", "users", "role", "2", "admin"}, "admin_updateUserRole", map[string]any{"userId": 2.0, "role": "admin"}},
		{[]string{"admin", "users", "delete", "2", "--yes"}, "admin_deleteUser", map[string]any{"userId": 2.0}},
		{[]string{"admin", "service-account", "create", "ci-bot"}, "admin_createServiceAccount", map[string]any{"name": "ci-bot"}},
		{[]string{"admin", "service-account", "list"}, "admin_listServiceAccounts", map[string]any{}},
		{[]string{"admin", "service-account", "delete", "8", "--yes"}, "admin_deleteServiceAccount", map[string]any{"userId": 8.0}},
		{[]string{"admin", "service-token", "create", "8", "--name", "ci"}, "admin_createServiceToken", map[string]any{"userId": 8.0, "name": "ci"}},
		{[]string{"admin", "service-token", "list", "8"}, "admin_listServiceTokens", map[string]any{"userId": 8.0}},
		{[]string{"admin", "service-token", "revoke", "11", "--yes"}, "admin_revokeServiceToken", map[string]any{"tokenId": 11.0}},
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.args[len(tc.args)-1], func(t *testing.T) {
			srv, calls := fakeServer(t, map[string]string{tc.tool: okData})
			h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
			if code := h.run(tc.args...); code != 0 {
				t.Fatalf("exit %d: %s", code, h.err)
			}
			if len(*calls) != 1 || (*calls)[0].Tool != tc.tool {
				t.Fatalf("calls = %+v, want one %s call", *calls, tc.tool)
			}
			got := (*calls)[0].Input
			for k, want := range tc.want {
				if !reflect.DeepEqual(got[k], want) {
					b, _ := json.Marshal(got)
					t.Errorf("input[%q] = %#v, want %#v (input: %s)", k, got[k], want, b)
				}
			}
		})
	}
}

func TestManagementDestructiveNeedsYes(t *testing.T) {
	srv, calls := fakeServer(t, map[string]string{"admin_deleteUser": okData, "members_remove": okData, "workflow_delete": okData})
	h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	for _, args := range [][]string{
		{"admin", "users", "delete", "2"},
		{"member", "remove", "9"},
		{"column", "delete", "review"},
	} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("destructive commands must not reach the server without --yes: %+v", *calls)
	}
}

func TestManagementUsageErrors(t *testing.T) {
	srv, calls := fakeServer(t, nil)
	h := newHarness(t, srv.URL, nil) // no default project
	for _, args := range [][]string{
		{"member", "list"},                                             // needs a project
		{"member", "add", "9"},                                         // --role missing
		{"column", "update", "doing", "--name", "x"},                   // --color missing
		{"column", "update", "doing", "--name", "x", "--color", "red"}, // bad color
		{"webhook", "create", "https://x.test"},                        // no events
		{"sprint", "update", "3"},                                      // nothing to update
		{"tag", "color", "bug"},                                        // missing color
		{"admin", "bogus"},
	} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%v: exit %d, want 2 (stderr %q)", args, code, h.err)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("invalid invocations must not reach the server")
	}
}

func TestBareNounStillLists(t *testing.T) {
	srv, calls := fakeServer(t, map[string]string{"members_list": okData})
	h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	if code := h.run("member", "-p", "other"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if (*calls)[0].Tool != "members_list" || (*calls)[0].Input["projectSlug"] != "other" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestConfirmPromptNamesTheProject(t *testing.T) {
	srv, calls := fakeServer(t, map[string]string{"sprints_delete": okData})
	h := newHarness(t, srv.URL, map[string]string{envProject: "ambient-proj"})
	h.app.isTTY = true
	h.app.in = strings.NewReader("n\n")
	if code := h.run("sprint", "delete", "3"); code != 1 {
		t.Errorf("declined prompt: exit %d, want 1", code)
	}
	if !strings.Contains(h.err.String(), "ambient-proj") {
		t.Errorf("the prompt must name the project it will act on: %q", h.err)
	}
	if len(*calls) != 0 {
		t.Error("a declined prompt must not reach the server")
	}
	h.app.in = strings.NewReader("y\n")
	if code := h.run("sprint", "delete", "3"); code != 0 || len(*calls) != 1 {
		t.Errorf("accepted prompt: exit %d, calls %d", code, len(*calls))
	}
}

func TestTagColorMineNeedsID(t *testing.T) {
	srv, calls := fakeServer(t, nil)
	h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	if code := h.run("tag", "color", "--mine", "bug", "#ff0000"); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if code := h.run("tag", "color", "--id", "0", "#ff0000"); code != 2 {
		t.Errorf("--id 0: exit %d, want 2", code)
	}
	if len(*calls) != 0 {
		t.Error("invalid invocations must not reach the server")
	}
}

func TestListShowsNextCursorHint(t *testing.T) {
	srv, _ := fakeServer(t, map[string]string{
		"webhooks_list": `{"ok":true,"data":{"items":[]},"meta":{"nextCursor":"CUR9","hasMore":true}}`,
	})
	h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	if code := h.run("webhook", "list"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if !strings.Contains(h.err.String(), "--cursor CUR9") {
		t.Errorf("truncated lists must say how to continue: %q", h.err)
	}
}

func TestWebhookSecretFromStdin(t *testing.T) {
	srv, calls := fakeServer(t, map[string]string{"webhooks_create": okData})
	h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	h.app.in = strings.NewReader("s3cret\n")
	if code := h.run("webhook", "create", "https://x.test/h", "--event", "*", "--secret-file", "-"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if (*calls)[0].Input["secret"] != "s3cret" {
		t.Errorf("secret = %v", (*calls)[0].Input["secret"])
	}
}
