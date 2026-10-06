package mcp_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func newTodoArchiveMCPFixture(t *testing.T) (*httptest.Server, *sql.DB, *http.Client, *store.Store, context.Context, int64, store.Project, []store.Todo) {
	t.Helper()
	ts, sqlDB, st, cleanup := newTodoUpdateMCPServer(t, "full")
	t.Cleanup(cleanup)
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	ctx := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctx, "MCP archival")
	if err != nil {
		t.Fatal(err)
	}
	todos := make([]store.Todo, 3)
	for i := range todos {
		todos[i] = createTodoUpdateMCPTodo(t, st, ctx, project.ID, fmt.Sprintf("Archived candidate %d", i+1), nil, nil)
	}
	return ts, sqlDB, client, st, ctx, ownerID, project, todos
}

func callTodoArchiveMCP(t *testing.T, client *http.Client, baseURL, tool, slug string, ids []int64) (*http.Response, map[string]any) {
	t.Helper()
	return doMCP(t, client, baseURL+"/mcp", map[string]any{
		"tool":  tool,
		"input": map[string]any{"projectSlug": slug, "localIds": ids},
	})
}

func requireTodoArchiveMCPSuccess(t *testing.T, resp *http.Response, out map[string]any) map[string]any {
	t.Helper()
	if resp.StatusCode != http.StatusOK || out["ok"] != true {
		t.Fatalf("MCP archival status=%d response=%+v", resp.StatusCode, out)
	}
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("MCP archival data type=%T response=%+v", out["data"], out)
	}
	return data
}

func TestTodoArchivalMCPLifecycleValidationAtomicityAndAuthorization(t *testing.T) {
	ts, _, client, st, ownerCtx, ownerID, project, todos := newTodoArchiveMCPFixture(t)
	baseURL := ts.URL
	ids := []int64{todos[1].LocalID, todos[0].LocalID}

	resp, out := callTodoArchiveMCP(t, client, baseURL, "todos_archive", project.Slug, ids)
	data := requireTodoArchiveMCPSuccess(t, resp, out)
	if data["targetState"] != "archived" || data["transitionedCount"] != float64(2) {
		t.Fatalf("archive result=%+v", data)
	}
	transitioned := data["transitionedLocalIds"].([]any)
	if transitioned[0] != float64(ids[0]) || transitioned[1] != float64(ids[1]) {
		t.Fatalf("archive result order=%v want=%v", transitioned, ids)
	}

	resp, out = callTodoArchiveMCP(t, client, baseURL, "todos_archive", project.Slug, ids)
	data = requireTodoArchiveMCPSuccess(t, resp, out)
	if data["transitionedCount"] != float64(0) || data["unchangedCount"] != float64(2) {
		t.Fatalf("idempotent archive result=%+v", data)
	}

	resp, out = callTodoArchiveMCP(t, client, baseURL, "todos_restore", project.Slug, ids)
	data = requireTodoArchiveMCPSuccess(t, resp, out)
	if data["targetState"] != "restored" || data["transitionedCount"] != float64(2) {
		t.Fatalf("restore result=%+v", data)
	}
	resp, out = callTodoArchiveMCP(t, client, baseURL, "todos_restore", project.Slug, ids)
	data = requireTodoArchiveMCPSuccess(t, resp, out)
	if data["transitionedCount"] != float64(0) || data["unchangedCount"] != float64(2) {
		t.Fatalf("idempotent restore result=%+v", data)
	}

	invalid := []struct {
		name string
		ids  []int64
	}{
		{name: "empty", ids: []int64{}},
		{name: "zero", ids: []int64{0}},
		{name: "negative", ids: []int64{-1}},
		{name: "duplicate", ids: []int64{todos[0].LocalID, todos[0].LocalID}},
	}
	// Derived from the store's authoritative bound so the transport rejection point
	// cannot drift away from what the store would actually accept.
	many := make([]int64, store.MaxTodoArchiveBatch+1)
	for i := range many {
		many[i] = int64(i + 1)
	}
	invalid = append(invalid, struct {
		name string
		ids  []int64
	}{name: "over maximum", ids: many})
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			resp, out := callTodoArchiveMCP(t, client, baseURL, "todos_archive", project.Slug, tc.ids)
			if resp.StatusCode != http.StatusBadRequest || out["error"].(map[string]any)["code"] != "VALIDATION_ERROR" {
				t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
			}
		})
	}

	// A missing member makes the whole request fail without archiving its valid member.
	resp, out = callTodoArchiveMCP(t, client, baseURL, "todos_archive", project.Slug, []int64{todos[0].LocalID, todos[2].LocalID + 999})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("atomic missing status=%d response=%+v", resp.StatusCode, out)
	}
	got, err := st.GetTodoByLocalID(ownerCtx, project.ID, todos[0].LocalID, store.ModeFull)
	if err != nil || got.ArchivedAt != nil {
		t.Fatalf("failed batch partially archived todo: %+v err=%v", got, err)
	}

	contributor, err := st.CreateUser(context.Background(), "archive-mcp-contributor@example.com", "password123", "Contributor")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectMember(ownerCtx, ownerID, project.ID, contributor.ID, store.RoleContributor); err != nil {
		t.Fatal(err)
	}
	contributorClient := loginTodoUpdateMCPUser(t, ts, contributor.Email, "password123")
	resp, out = callTodoArchiveMCP(t, contributorClient, baseURL, "todos_archive", project.Slug, []int64{todos[0].LocalID})
	if resp.StatusCode != http.StatusForbidden || out["error"].(map[string]any)["code"] != "FORBIDDEN" {
		t.Fatalf("contributor status=%d response=%+v", resp.StatusCode, out)
	}

	maintainer, err := st.CreateUser(context.Background(), "archive-mcp-maintainer@example.com", "password123", "Maintainer")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectMember(ownerCtx, ownerID, project.ID, maintainer.ID, store.RoleMaintainer); err != nil {
		t.Fatal(err)
	}
	maintainerClient := loginTodoUpdateMCPUser(t, ts, maintainer.Email, "password123")
	resp, out = callTodoArchiveMCP(t, maintainerClient, baseURL, "todos_archive", project.Slug, []int64{todos[0].LocalID})
	requireTodoArchiveMCPSuccess(t, resp, out)
}

func TestTodoArchivalMCPReadAndLinkContracts(t *testing.T) {
	ts, _, client, st, ctx, _, project, todos := newTodoArchiveMCPFixture(t)
	baseURL := ts.URL
	resp, out := doMCP(t, client, baseURL+"/mcp", map[string]any{"tool": "todos_get", "input": map[string]any{
		"projectSlug": project.Slug, "localId": todos[0].LocalID,
	}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("todos_get active status=%d response=%+v", resp.StatusCode, out)
	}
	active := out["data"].(map[string]any)["todo"].(map[string]any)
	if value, present := active["archivedAt"]; !present || value != nil {
		t.Fatalf("active MCP archivedAt present=%v value=%v", present, value)
	}
	if err := st.AddLink(ctx, project.ID, todos[1].LocalID, todos[0].LocalID, "blocks", store.ModeFull); err != nil {
		t.Fatal(err)
	}
	resp, out = callTodoArchiveMCP(t, client, baseURL, "todos_archive", project.Slug, []int64{todos[0].LocalID})
	requireTodoArchiveMCPSuccess(t, resp, out)

	resp, out = doMCP(t, client, baseURL+"/mcp", map[string]any{"tool": "todos_get", "input": map[string]any{
		"projectSlug": project.Slug, "localId": todos[0].LocalID,
	}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("todos_get archived status=%d response=%+v", resp.StatusCode, out)
	}
	archived := out["data"].(map[string]any)["todo"].(map[string]any)
	if archived["archivedAt"] == nil {
		t.Fatalf("todos_get archivedAt=%v", archived["archivedAt"])
	}

	resp, out = doMCP(t, client, baseURL+"/mcp", map[string]any{"tool": "todos_search", "input": map[string]any{
		"projectSlug": project.Slug, "query": "Archived candidate 1", "limit": 20, "excludeLocalIds": []int64{},
	}})
	if resp.StatusCode != http.StatusOK || len(out["data"].(map[string]any)["items"].([]any)) != 0 {
		t.Fatalf("todos_search leaked archived todo status=%d response=%+v", resp.StatusCode, out)
	}

	resp, out = doMCP(t, client, baseURL+"/mcp", map[string]any{"tool": "board_get", "input": map[string]any{"projectSlug": project.Slug}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("board_get status=%d response=%+v", resp.StatusCode, out)
	}
	for _, rawColumn := range out["data"].(map[string]any)["columns"].([]any) {
		for _, rawItem := range rawColumn.(map[string]any)["items"].([]any) {
			if rawItem.(map[string]any)["localId"] == float64(todos[0].LocalID) {
				t.Fatalf("board_get leaked archived todo: %+v", rawItem)
			}
		}
	}

	resp, out = doMCP(t, client, baseURL+"/mcp", map[string]any{"tool": "todos_linksList", "input": map[string]any{
		"projectSlug": project.Slug, "localId": todos[1].LocalID,
	}})
	links := todoLinkMCPData(t, "legacy", out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("links list status=%d response=%+v", resp.StatusCode, out)
	}
	items := links["outbound"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["archivedAt"] == nil {
		t.Fatalf("archived link target projection=%+v", items)
	}

	resp, out = doMCP(t, client, baseURL+"/mcp", map[string]any{"tool": "todos_linkRemove", "input": map[string]any{
		"projectSlug": project.Slug, "localId": todos[1].LocalID, "targetLocalId": todos[0].LocalID,
	}})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("archived link remove status=%d response=%+v", resp.StatusCode, out)
	}
}

func TestTodoArchivalMCPTemporaryProjectAndModeContracts(t *testing.T) {
	ts, sqlDB, client, st, ctx, _, _, _ := newTodoArchiveMCPFixture(t)
	baseURL := ts.URL
	p, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	todo := createTodoUpdateMCPTodo(t, st, ctx, p.ID, "Temporary archival", nil, nil)
	resp, out := callTodoArchiveMCP(t, client, baseURL, "todos_archive", p.Slug, []int64{todo.LocalID})
	requireTodoArchiveMCPSuccess(t, resp, out)
	if _, err := sqlDB.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Minute).UnixMilli(), p.ID); err != nil {
		t.Fatal(err)
	}
	resp, out = callTodoArchiveMCP(t, client, baseURL, "todos_restore", p.Slug, []int64{todo.LocalID})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expired temporary restore status=%d response=%+v", resp.StatusCode, out)
	}

	anonymousServer, _, _, anonymousCleanup := newTodoUpdateMCPServer(t, "anonymous")
	defer anonymousCleanup()
	resp, out = callTodoArchiveMCP(t, anonymousServer.Client(), anonymousServer.URL, "todos_archive", "any", []int64{1})
	if resp.StatusCode != http.StatusForbidden || out["error"].(map[string]any)["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("anonymous-mode archive status=%d response=%+v", resp.StatusCode, out)
	}
}

// TestTodoArchivalMCPRemainsRealtimeSilent pins Scrumboy's established surface contract:
// REST mutations publish a board refresh, MCP mutations do not. Archive/restore follow the
// same rule as MCP create/update/move/delete, so a real transition driven over MCP must emit
// no realtime event at all. docs/mcp.md states this directly ("MCP/Agora do not gain
// card-activity fallback because they still emit no board refresh"). This asymmetry is
// deliberate: do not "fix" it by giving the MCP archive service a refresh publisher.
func TestTodoArchivalMCPRemainsRealtimeSilent(t *testing.T) {
	ts, _, client, st, ownerCtx, _, project, todos := newTodoArchiveMCPFixture(t)

	stream := subscribeTodoUpdateMCPEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
	defer stream.close()

	ids := []int64{todos[0].LocalID, todos[1].LocalID}
	resp, out := callTodoArchiveMCP(t, client, ts.URL, "todos_archive", project.Slug, ids)
	data := requireTodoArchiveMCPSuccess(t, resp, out)
	if count, _ := data["transitionedCount"].(float64); count != 2 {
		t.Fatalf("expected a real transition to test silence against, data=%+v", data)
	}

	resp, out = callTodoArchiveMCP(t, client, ts.URL, "todos_restore", project.Slug, ids)
	data = requireTodoArchiveMCPSuccess(t, resp, out)
	if count, _ := data["transitionedCount"].(float64); count != 2 {
		t.Fatalf("expected a real restore transition, data=%+v", data)
	}

	assertNoTodoUpdateMCPEvents(t, collectTodoUpdateMCPEvents(t, stream))

	// Silence is realtime-only: the store still records the transitions.
	for _, id := range ids {
		got, err := st.GetTodoByLocalID(ownerCtx, project.ID, id, store.ModeFull)
		if err != nil || got.ArchivedAt != nil {
			t.Fatalf("restore did not persist for local id %d: %+v err=%v", id, got, err)
		}
	}
}

// TestTodoArchivalMCPUpdateEmptyPatchIsANoOpNotAConflict characterizes existing MCP
// semantics against an archived story. An empty patch is a lookup/no-op that performs no
// store update, audit entry, activity stamp or event, so it succeeds even though the story
// is read-only; only an actual mutation is rejected with the archived conflict. This is the
// established `{}` contract and is deliberately NOT changed by archival — altering it is a
// separate decision.
func TestTodoArchivalMCPUpdateEmptyPatchIsANoOpNotAConflict(t *testing.T) {
	ts, _, client, st, ownerCtx, _, project, todos := newTodoArchiveMCPFixture(t)
	target := todos[0]

	resp, out := callTodoArchiveMCP(t, client, ts.URL, "todos_archive", project.Slug, []int64{target.LocalID})
	requireTodoArchiveMCPSuccess(t, resp, out)
	before, err := st.GetTodoByLocalID(ownerCtx, project.ID, target.LocalID, store.ModeFull)
	if err != nil || before.ArchivedAt == nil {
		t.Fatalf("archive did not persist: %+v err=%v", before, err)
	}

	t.Run("empty patch succeeds and mutates nothing", func(t *testing.T) {
		resp, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
			"tool": "todos_update",
			"input": map[string]any{
				"projectSlug": project.Slug,
				"localId":     target.LocalID,
				"patch":       map[string]any{},
			},
		})
		if resp.StatusCode != http.StatusOK || out["ok"] != true {
			t.Fatalf("empty patch on archived todo status=%d response=%+v", resp.StatusCode, out)
		}
		after, err := st.GetTodoByLocalID(ownerCtx, project.ID, target.LocalID, store.ModeFull)
		if err != nil {
			t.Fatal(err)
		}
		if after.Title != before.Title || after.Body != before.Body ||
			after.UpdatedAt.UnixMilli() != before.UpdatedAt.UnixMilli() ||
			after.ArchivedAt == nil || after.ArchivedAt.UnixMilli() != before.ArchivedAt.UnixMilli() {
			t.Fatalf("empty patch mutated the archived todo: before=%+v after=%+v", before, after)
		}
	})

	t.Run("non-empty patch is rejected as archived", func(t *testing.T) {
		resp, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
			"tool": "todos_update",
			"input": map[string]any{
				"projectSlug": project.Slug,
				"localId":     target.LocalID,
				"patch":       map[string]any{"title": "rewritten while archived"},
			},
		})
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("non-empty patch on archived todo status=%d want 409; response=%+v", resp.StatusCode, out)
		}
		after, err := st.GetTodoByLocalID(ownerCtx, project.ID, target.LocalID, store.ModeFull)
		if err != nil || after.Title != before.Title {
			t.Fatalf("rejected patch still mutated the todo: %+v err=%v", after, err)
		}
	})
}

// TestTodoArchivalMCPGuardsAndRestoreAtomicity closes the guard/atomicity gaps the initial
// contract suite left open: restore must be as all-or-nothing as archive, the two
// pre-auth capability guards must both fire, a missing projectSlug must be rejected before
// any store work, and a plain viewer must be refused like any other non-maintainer.
func TestTodoArchivalMCPGuardsAndRestoreAtomicity(t *testing.T) {
	ts, _, client, st, ownerCtx, ownerID, project, todos := newTodoArchiveMCPFixture(t)
	baseURL := ts.URL

	t.Run("restore is atomic across a missing member", func(t *testing.T) {
		ids := []int64{todos[0].LocalID, todos[1].LocalID}
		resp, out := callTodoArchiveMCP(t, client, baseURL, "todos_archive", project.Slug, ids)
		requireTodoArchiveMCPSuccess(t, resp, out)

		resp, out = callTodoArchiveMCP(t, client, baseURL, "todos_restore", project.Slug, []int64{todos[0].LocalID, todos[2].LocalID + 999})
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("atomic restore status=%d response=%+v", resp.StatusCode, out)
		}
		for _, id := range ids {
			got, err := st.GetTodoByLocalID(ownerCtx, project.ID, id, store.ModeFull)
			if err != nil || got.ArchivedAt == nil {
				t.Fatalf("failed restore batch partially applied to %d: %+v err=%v", id, got, err)
			}
		}
		resp, out = callTodoArchiveMCP(t, client, baseURL, "todos_restore", project.Slug, ids)
		requireTodoArchiveMCPSuccess(t, resp, out)
	})

	t.Run("missing projectSlug is rejected", func(t *testing.T) {
		for _, tool := range []string{"todos_archive", "todos_restore"} {
			resp, out := doMCP(t, client, baseURL+"/mcp", map[string]any{
				"tool":  tool,
				"input": map[string]any{"localIds": []int64{todos[0].LocalID}},
			})
			if resp.StatusCode != http.StatusBadRequest || out["error"].(map[string]any)["code"] != "VALIDATION_ERROR" {
				t.Fatalf("%s missing slug status=%d response=%+v", tool, resp.StatusCode, out)
			}
		}
	})

	t.Run("viewer is refused", func(t *testing.T) {
		viewer, err := st.CreateUser(context.Background(), "archive-mcp-viewer@example.com", "password123", "Viewer")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.AddProjectMember(ownerCtx, ownerID, project.ID, viewer.ID, store.RoleViewer); err != nil {
			t.Fatal(err)
		}
		viewerClient := loginTodoUpdateMCPUser(t, ts, viewer.Email, "password123")
		for _, tool := range []string{"todos_archive", "todos_restore"} {
			resp, out := callTodoArchiveMCP(t, viewerClient, baseURL, tool, project.Slug, []int64{todos[0].LocalID})
			if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
				t.Fatalf("%s viewer status=%d response=%+v", tool, resp.StatusCode, out)
			}
		}
		got, err := st.GetTodoByLocalID(ownerCtx, project.ID, todos[0].LocalID, store.ModeFull)
		if err != nil || got.ArchivedAt != nil {
			t.Fatalf("viewer request changed archive state: %+v err=%v", got, err)
		}
	})

	t.Run("unauthenticated is rejected before any store work", func(t *testing.T) {
		anonymousClient := newCookieClient(t, ts)
		for _, tool := range []string{"todos_archive", "todos_restore"} {
			resp, out := callTodoArchiveMCP(t, anonymousClient, baseURL, tool, project.Slug, []int64{todos[0].LocalID})
			if resp.StatusCode != http.StatusUnauthorized || out["error"].(map[string]any)["code"] != "AUTH_REQUIRED" {
				t.Fatalf("%s unauthenticated status=%d response=%+v", tool, resp.StatusCode, out)
			}
		}
	})

	t.Run("bootstrap mode reports capability unavailable", func(t *testing.T) {
		// A fresh full-mode server with no bootstrapped user yet: archival must be
		// refused as a capability, ahead of the authentication guard.
		bootstrapServer, _, _, cleanup := newTodoUpdateMCPServer(t, "full")
		defer cleanup()
		for _, tool := range []string{"todos_archive", "todos_restore"} {
			resp, out := callTodoArchiveMCP(t, bootstrapServer.Client(), bootstrapServer.URL, tool, "any", []int64{1})
			if resp.StatusCode != http.StatusForbidden || out["error"].(map[string]any)["code"] != "CAPABILITY_UNAVAILABLE" {
				t.Fatalf("%s bootstrap status=%d response=%+v", tool, resp.StatusCode, out)
			}
		}
	})
}

// TestTodoArchivalMCPRejectsLinkAddAndMoveOnArchivedStories complements the existing
// linkRemove coverage. Archived stories are read-only, so every story-directed mutation
// reachable over MCP must be refused until the story is restored.
func TestTodoArchivalMCPRejectsLinkAddAndMoveOnArchivedStories(t *testing.T) {
	ts, _, client, st, ownerCtx, _, project, todos := newTodoArchiveMCPFixture(t)
	baseURL := ts.URL

	resp, out := callTodoArchiveMCP(t, client, baseURL, "todos_archive", project.Slug, []int64{todos[0].LocalID})
	requireTodoArchiveMCPSuccess(t, resp, out)

	// Archived as the link source, and archived as the link target.
	for name, input := range map[string]map[string]any{
		"archived source": {"projectSlug": project.Slug, "localId": todos[0].LocalID, "targetLocalId": todos[1].LocalID, "linkType": "blocks"},
		"archived target": {"projectSlug": project.Slug, "localId": todos[1].LocalID, "targetLocalId": todos[0].LocalID, "linkType": "blocks"},
	} {
		t.Run("linkAdd/"+name, func(t *testing.T) {
			resp, out := doMCP(t, client, baseURL+"/mcp", map[string]any{"tool": "todos_linkAdd", "input": input})
			if resp.StatusCode != http.StatusConflict {
				t.Fatalf("status=%d want 409; response=%+v", resp.StatusCode, out)
			}
		})
	}

	t.Run("move is rejected", func(t *testing.T) {
		resp, out := doMCP(t, client, baseURL+"/mcp", map[string]any{"tool": "todos_move", "input": map[string]any{
			"projectSlug": project.Slug,
			"localId":     todos[0].LocalID,
			"toColumnKey": store.DefaultColumnDoing,
		}})
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("move status=%d want 409; response=%+v", resp.StatusCode, out)
		}
	})

	got, err := st.GetTodoByLocalID(ownerCtx, project.ID, todos[0].LocalID, store.ModeFull)
	if err != nil || got.ArchivedAt == nil || got.ColumnKey != todos[0].ColumnKey {
		t.Fatalf("rejected mutations still changed the archived story: %+v err=%v", got, err)
	}
}
