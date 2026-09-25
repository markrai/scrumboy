package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type archivePageJSON struct {
	Todos      []json.RawMessage `json:"todos"`
	NextCursor string            `json:"nextCursor"`
	HasMore    bool              `json:"hasMore"`
}

func todoArchiveURL(f *todoDeleteRESTFixture, slug string) string {
	return f.ts.URL + "/api/board/" + slug
}

func assertArchiveState(t *testing.T, f *todoDeleteRESTFixture, todoID int64, archived bool) {
	t.Helper()
	var count int
	predicate := "archived_at IS NULL"
	if archived {
		predicate = "archived_at IS NOT NULL"
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM todos WHERE id = ? AND `+predicate, todoID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("todo %d archived=%v count=%d", todoID, archived, count)
	}
}

func TestTodoArchivalRESTSingleBatchProjectionAndRefreshContracts(t *testing.T) {
	f := newTodoDeleteRESTFixture(t, "full")
	_, ctx, client := newTodoDeleteRESTOwner(t, f, "archive-rest-owner@example.com")
	p, err := f.store.CreateProject(ctx, "REST archival")
	if err != nil {
		t.Fatal(err)
	}
	a := createTodoDeleteRESTTodo(t, f, ctx, p.ID, "A")
	b := createTodoDeleteRESTTodo(t, f, ctx, p.ID, "B")
	base := todoArchiveURL(f, p.Slug)

	var active map[string]json.RawMessage
	resp, body := doJSON(t, client, http.MethodGet, fmt.Sprintf("%s/todos/%d", base, a.LocalID), nil, &active)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("active get status=%d body=%s", resp.StatusCode, body)
	}
	if _, present := active["archivedAt"]; present {
		t.Fatalf("active REST projection must omit archivedAt: %s", body)
	}

	f.collector.events = nil
	var single store.TodoArchiveBatchResult
	resp, body = doJSON(t, client, http.MethodPost, fmt.Sprintf("%s/todos/%d/archive", base, a.LocalID), nil, &single)
	if resp.StatusCode != http.StatusOK || single.TransitionedCount != 1 || single.TargetState != "archived" {
		t.Fatalf("single archive status=%d body=%s result=%+v", resp.StatusCode, body, single)
	}
	assertArchiveState(t, f, a.ID, true)
	if events := todoDeleteRESTEventsForProject(f.collector, p.ID); len(events) != 1 {
		t.Fatalf("single archive refreshes=%d want 1: %+v", len(events), events)
	}

	var archived map[string]json.RawMessage
	resp, body = doJSON(t, client, http.MethodGet, fmt.Sprintf("%s/todos/%d", base, a.LocalID), nil, &archived)
	if resp.StatusCode != http.StatusOK || len(archived["archivedAt"]) == 0 || string(archived["archivedAt"]) == "null" {
		t.Fatalf("archived projection status=%d body=%s", resp.StatusCode, body)
	}

	f.collector.events = nil
	var singleRestoreTransition store.TodoArchiveBatchResult
	resp, body = doJSON(t, client, http.MethodPost, fmt.Sprintf("%s/todos/%d/restore", base, a.LocalID), nil, &singleRestoreTransition)
	if resp.StatusCode != http.StatusOK || singleRestoreTransition.TransitionedCount != 1 || singleRestoreTransition.TargetState != "restored" {
		t.Fatalf("single restore status=%d body=%s result=%+v", resp.StatusCode, body, singleRestoreTransition)
	}
	if events := todoDeleteRESTEventsForProject(f.collector, p.ID); len(events) != 1 {
		t.Fatalf("single restore refreshes=%d want 1: %+v", len(events), events)
	}
	resp, body = doJSON(t, client, http.MethodPost, fmt.Sprintf("%s/todos/%d/archive", base, a.LocalID), nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rearchive status=%d body=%s", resp.StatusCode, body)
	}

	// Idempotent requests return the batch result but publish no refresh.
	f.collector.events = nil
	var noop store.TodoArchiveBatchResult
	resp, body = doJSON(t, client, http.MethodPost, fmt.Sprintf("%s/todos/%d/archive", base, a.LocalID), nil, &noop)
	if resp.StatusCode != http.StatusOK || noop.TransitionedCount != 0 || noop.UnchangedCount != 1 {
		t.Fatalf("archive no-op status=%d body=%s result=%+v", resp.StatusCode, body, noop)
	}
	if events := todoDeleteRESTEventsForProject(f.collector, p.ID); len(events) != 0 {
		t.Fatalf("archive no-op refreshes=%+v", events)
	}

	f.collector.events = nil
	var batch store.TodoArchiveBatchResult
	resp, body = doJSON(t, client, http.MethodPost, base+"/todos/archive", map[string]any{"localIds": []int64{a.LocalID, b.LocalID}}, &batch)
	if resp.StatusCode != http.StatusOK || batch.TransitionedCount != 1 || batch.UnchangedCount != 1 {
		t.Fatalf("batch archive status=%d body=%s result=%+v", resp.StatusCode, body, batch)
	}
	if events := todoDeleteRESTEventsForProject(f.collector, p.ID); len(events) != 1 {
		t.Fatalf("batch archive refreshes=%d want 1: %+v", len(events), events)
	}

	f.collector.events = nil
	var restored store.TodoArchiveBatchResult
	resp, body = doJSON(t, client, http.MethodPost, base+"/todos/restore", map[string]any{"localIds": []int64{b.LocalID, a.LocalID}}, &restored)
	if resp.StatusCode != http.StatusOK || restored.TransitionedCount != 2 || restored.TargetState != "restored" {
		t.Fatalf("batch restore status=%d body=%s result=%+v", resp.StatusCode, body, restored)
	}
	if len(restored.TransitionedLocalIDs) != 2 || restored.TransitionedLocalIDs[0] != b.LocalID || restored.TransitionedLocalIDs[1] != a.LocalID {
		t.Fatalf("restore result order=%v", restored.TransitionedLocalIDs)
	}
	if events := todoDeleteRESTEventsForProject(f.collector, p.ID); len(events) != 1 {
		t.Fatalf("batch restore refreshes=%d want 1: %+v", len(events), events)
	}
	assertArchiveState(t, f, a.ID, false)
	assertArchiveState(t, f, b.ID, false)

	var singleRestore store.TodoArchiveBatchResult
	resp, body = doJSON(t, client, http.MethodPost, fmt.Sprintf("%s/todos/%d/restore", base, a.LocalID), nil, &singleRestore)
	if resp.StatusCode != http.StatusOK || singleRestore.UnchangedCount != 1 {
		t.Fatalf("single restore no-op status=%d body=%s result=%+v", resp.StatusCode, body, singleRestore)
	}
}

func TestTodoArchivalRESTValidationAtomicityAndMutationGuardContracts(t *testing.T) {
	f := newTodoDeleteRESTFixture(t, "full")
	_, ctx, client := newTodoDeleteRESTOwner(t, f, "archive-rest-validation@example.com")
	p, err := f.store.CreateProject(ctx, "REST archival validation")
	if err != nil {
		t.Fatal(err)
	}
	todo := createTodoDeleteRESTTodo(t, f, ctx, p.ID, "Protected")
	base := todoArchiveURL(f, p.Slug)

	cases := []struct {
		name    string
		path    string
		payload any
		status  int
	}{
		{name: "malformed local id", path: base + "/todos/nope/archive", status: http.StatusBadRequest},
		{name: "empty", path: base + "/todos/archive", payload: map[string]any{"localIds": []int64{}}, status: http.StatusBadRequest},
		{name: "duplicate", path: base + "/todos/archive", payload: map[string]any{"localIds": []int64{todo.LocalID, todo.LocalID}}, status: http.StatusBadRequest},
		{name: "invalid", path: base + "/todos/archive", payload: map[string]any{"localIds": []int64{0}}, status: http.StatusBadRequest},
	}
	many := make([]int64, 501)
	for i := range many {
		many[i] = int64(i + 1)
	}
	cases = append(cases, struct {
		name    string
		path    string
		payload any
		status  int
	}{name: "over maximum", path: base + "/todos/archive", payload: map[string]any{"localIds": many}, status: http.StatusBadRequest})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.collector.events = nil
			resp, body := doJSON(t, client, http.MethodPost, tc.path, tc.payload, nil)
			if resp.StatusCode != tc.status {
				t.Fatalf("status=%d body=%s want %d", resp.StatusCode, body, tc.status)
			}
			assertArchiveState(t, f, todo.ID, false)
			if events := todoDeleteRESTEventsForProject(f.collector, p.ID); len(events) != 0 {
				t.Fatalf("failed request refreshes=%+v", events)
			}
		})
	}

	f.collector.events = nil
	resp, body := doJSON(t, client, http.MethodPost, base+"/todos/archive", map[string]any{"localIds": []int64{todo.LocalID, todo.LocalID + 999}}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing atomic batch status=%d body=%s", resp.StatusCode, body)
	}
	assertArchiveState(t, f, todo.ID, false)
	if len(todoDeleteRESTEventsForProject(f.collector, p.ID)) != 0 {
		t.Fatal("failed atomic batch published refresh")
	}

	resp, body = doJSON(t, client, http.MethodPost, fmt.Sprintf("%s/todos/%d/archive", base, todo.LocalID), nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("archive for guard: status=%d body=%s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/todos/%d", base, todo.LocalID), map[string]any{
		"title": "changed", "body": "", "tags": []string{}, "estimationPoints": nil, "assigneeUserId": nil,
	}, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("archived update status=%d body=%s want 409", resp.StatusCode, body)
	}
	resp, body = doJSON(t, client, http.MethodPost, fmt.Sprintf("%s/todos/%d/move", base, todo.LocalID), map[string]any{"toColumnKey": store.DefaultColumnDoing}, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("archived move status=%d body=%s want 409", resp.StatusCode, body)
	}
	resp, body = doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/todos/%d", f.ts.URL, todo.ID), map[string]any{
		"title": "legacy changed", "body": "", "tags": []string{}, "estimationPoints": nil, "assigneeUserId": nil,
	}, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("archived legacy update status=%d body=%s want 409", resp.StatusCode, body)
	}
	resp, body = doJSON(t, client, http.MethodPost, fmt.Sprintf("%s/api/todos/%d/move", f.ts.URL, todo.ID), map[string]any{"toColumnKey": store.DefaultColumnDoing}, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("archived legacy move status=%d body=%s want 409", resp.StatusCode, body)
	}
}

func TestTodoArchiveRESTPaginationAndReadPermissions(t *testing.T) {
	f := newTodoDeleteRESTFixture(t, "full")
	owner, ctx, client := newTodoDeleteRESTOwner(t, f, "archive-rest-page@example.com")
	p, err := f.store.CreateProject(ctx, "REST archive page")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 3)
	for i := range ids {
		ids[i] = createTodoDeleteRESTTodo(t, f, ctx, p.ID, fmt.Sprintf("Page %d", i)).LocalID
	}
	if _, err := f.store.ArchiveTodosByLocalID(ctx, p.ID, ids, store.ModeFull); err != nil {
		t.Fatal(err)
	}
	base := todoArchiveURL(f, p.Slug)
	var first archivePageJSON
	resp, body := doJSON(t, client, http.MethodGet, base+"/archive?limit=2", nil, &first)
	if resp.StatusCode != http.StatusOK || len(first.Todos) != 2 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page status=%d body=%s page=%+v", resp.StatusCode, body, first)
	}
	var second archivePageJSON
	resp, body = doJSON(t, client, http.MethodGet, base+"/archive?limit=2&afterCursor="+url.QueryEscape(first.NextCursor), nil, &second)
	if resp.StatusCode != http.StatusOK || len(second.Todos) != 1 || second.HasMore || second.NextCursor != "" {
		t.Fatalf("second page status=%d body=%s page=%+v", resp.StatusCode, body, second)
	}
	seen := map[int64]bool{}
	for _, raw := range append(first.Todos, second.Todos...) {
		var item struct {
			LocalID    int64      `json:"localId"`
			ArchivedAt *time.Time `json:"archivedAt"`
		}
		if err := json.Unmarshal(raw, &item); err != nil || item.ArchivedAt == nil || seen[item.LocalID] {
			t.Fatalf("invalid/duplicate archive item %s err=%v", raw, err)
		}
		seen[item.LocalID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("page traversal local IDs=%v", seen)
	}

	viewer, err := f.store.CreateUser(context.Background(), "archive-rest-viewer@example.com", "password123", "Viewer")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddProjectMember(ctx, owner.ID, p.ID, viewer.ID, store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	resp, body = doJSON(t, todoDeleteRESTClientForUser(t, f, viewer.ID), http.MethodGet, base+"/archive", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("viewer archive list status=%d body=%s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, newCookieClient(t), http.MethodGet, base+"/archive", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unauth archive list status=%d body=%s want 404", resp.StatusCode, body)
	}
}

func TestTodoArchivalRESTDurableAndTemporaryAuthorization(t *testing.T) {
	f := newTodoDeleteRESTFixture(t, "full")
	owner, ctx, _ := newTodoDeleteRESTOwner(t, f, "archive-rest-auth-owner@example.com")
	p, err := f.store.CreateProject(ctx, "REST archive auth")
	if err != nil {
		t.Fatal(err)
	}
	todo := createTodoDeleteRESTTodo(t, f, ctx, p.ID, "Target")
	base := todoArchiveURL(f, p.Slug)
	for _, tc := range []struct {
		name, email string
		role        store.ProjectRole
		status      int
	}{
		{name: "viewer", email: "archive-rest-auth-viewer@example.com", role: store.RoleViewer, status: http.StatusNotFound},
		{name: "contributor", email: "archive-rest-auth-contributor@example.com", role: store.RoleContributor, status: http.StatusForbidden},
		{name: "maintainer", email: "archive-rest-auth-maintainer@example.com", role: store.RoleMaintainer, status: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user, err := f.store.CreateUser(context.Background(), tc.email, "password123", tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.AddProjectMember(ctx, owner.ID, p.ID, user.ID, tc.role); err != nil {
				t.Fatal(err)
			}
			resp, body := doJSON(t, todoDeleteRESTClientForUser(t, f, user.ID), http.MethodPost, fmt.Sprintf("%s/todos/%d/archive", base, todo.LocalID), nil, nil)
			if resp.StatusCode != tc.status {
				t.Fatalf("status=%d body=%s want %d", resp.StatusCode, body, tc.status)
			}
			if tc.status == http.StatusOK {
				if _, err := f.store.RestoreTodoByLocalID(ctx, p.ID, todo.LocalID, store.ModeFull); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	resp, body := doJSON(t, newCookieClient(t), http.MethodPost, fmt.Sprintf("%s/todos/%d/archive", base, todo.LocalID), nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unauth durable archive status=%d body=%s", resp.StatusCode, body)
	}

	temporary, err := f.store.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tempTodo := createTodoDeleteRESTTodo(t, f, ctx, temporary.ID, "Temporary")
	tempURL := todoArchiveURL(f, temporary.Slug)
	resp, body = doJSON(t, newCookieClient(t), http.MethodPost, fmt.Sprintf("%s/todos/%d/archive", tempURL, tempTodo.LocalID), nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("temporary capability archive status=%d body=%s", resp.StatusCode, body)
	}
	if _, err := f.db.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Minute).UnixMilli(), temporary.ID); err != nil {
		t.Fatal(err)
	}
	resp, body = doJSON(t, newCookieClient(t), http.MethodPost, fmt.Sprintf("%s/todos/%d/restore", tempURL, tempTodo.LocalID), nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expired temporary restore status=%d body=%s", resp.StatusCode, body)
	}
}

// TestTodoArchiveRESTListInputValidationAndScope covers the archive list's remaining
// transport contract: malformed paging input is rejected rather than silently coerced, and
// the list is strictly the archive -- active stories never appear in it.
func TestTodoArchiveRESTListInputValidationAndScope(t *testing.T) {
	f := newTodoDeleteRESTFixture(t, "full")
	_, ctx, client := newTodoDeleteRESTOwner(t, f, "archive-rest-listinput@example.com")
	p, err := f.store.CreateProject(ctx, "REST archive list input")
	if err != nil {
		t.Fatal(err)
	}
	archived := createTodoDeleteRESTTodo(t, f, ctx, p.ID, "Archived story").LocalID
	active := createTodoDeleteRESTTodo(t, f, ctx, p.ID, "Active story").LocalID
	if _, err := f.store.ArchiveTodosByLocalID(ctx, p.ID, []int64{archived}, store.ModeFull); err != nil {
		t.Fatal(err)
	}
	base := todoArchiveURL(f, p.Slug) + "/archive"

	for _, query := range []string{
		"?limit=0",
		"?limit=-1",
		"?limit=abc",
		"?afterCursor=nonsense",
		"?afterCursor=1",
		"?afterCursor=1:2:3",
		"?afterCursor=-1:2",
		"?afterCursor=1:0",
		"?afterCursor=abc:2",
	} {
		t.Run("rejects "+query, func(t *testing.T) {
			resp, body := doJSON(t, client, http.MethodGet, base+query, nil, nil)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d want 400; body=%s", resp.StatusCode, body)
			}
		})
	}

	// An oversized limit is clamped rather than rejected, matching the store bound.
	var clamped archivePageJSON
	resp, body := doJSON(t, client, http.MethodGet, base+"?limit=100000", nil, &clamped)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("oversized limit status=%d body=%s", resp.StatusCode, body)
	}

	var page archivePageJSON
	resp, body = doJSON(t, client, http.MethodGet, base, nil, &page)
	if resp.StatusCode != http.StatusOK || len(page.Todos) != 1 {
		t.Fatalf("archive list status=%d body=%s page=%+v", resp.StatusCode, body, page)
	}
	var item struct {
		LocalID    int64      `json:"localId"`
		ArchivedAt *time.Time `json:"archivedAt"`
	}
	if err := json.Unmarshal(page.Todos[0], &item); err != nil {
		t.Fatal(err)
	}
	if item.LocalID != archived || item.ArchivedAt == nil {
		t.Fatalf("archive list returned the wrong story: %+v", item)
	}
	if item.LocalID == active {
		t.Fatal("active story leaked into the archive list")
	}
}
