package mcp_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scrumboy/internal/store"
)

type serviceBearerFixture struct {
	ts       *httptest.Server
	db       *sql.DB
	st       *store.Store
	owner    *http.Client
	ownerCtx context.Context
	ownerID  int64
	svc      store.User
	bearer   string
	bare     *http.Client
}

func newServiceBearerFixture(t *testing.T) *serviceBearerFixture {
	t.Helper()
	ts, sqlDB, cleanup := newTestServer(t, "full")
	t.Cleanup(cleanup)
	owner := newCookieClient(t, ts)
	bootstrapUser(t, owner, ts.URL)
	var ownerID int64
	if err := sqlDB.QueryRow(`SELECT id FROM users WHERE system_role = 'owner'`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	st := store.New(sqlDB, nil)
	svc, err := st.CreateServiceAccount(context.Background(), ownerID, "Bot")
	if err != nil {
		t.Fatal(err)
	}
	const raw = store.APITokenPrefix + "service-account-bearer"
	sum := sha256.Sum256([]byte(raw))
	if _, err := sqlDB.Exec(`INSERT INTO api_tokens(user_id, token_hash, created_at) VALUES (?, ?, 1)`, svc.ID, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	return &serviceBearerFixture{
		ts: ts, db: sqlDB, st: st, owner: owner,
		ownerCtx: store.WithUserID(context.Background(), ownerID), ownerID: ownerID,
		svc: svc, bearer: raw, bare: newStatelessClient(ts),
	}
}

func (f *serviceBearerFixture) call(t *testing.T, tool string, input map[string]any) (int, map[string]any) {
	t.Helper()
	resp, out := postMCPWithBearer(t, f.bare, f.ts.URL, f.bearer, map[string]any{"tool": tool, "input": input})
	return resp.StatusCode, out
}

func (f *serviceBearerFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func (f *serviceBearerFixture) assertHoldsNoProjects(t *testing.T) {
	t.Helper()
	if n := f.count(t, `SELECT COUNT(*) FROM projects WHERE owner_user_id = ? OR creator_user_id = ?`, f.svc.ID, f.svc.ID); n != 0 {
		t.Errorf("service account owns or created %d projects", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM project_members WHERE user_id = ?`, f.svc.ID); n != 0 {
		t.Errorf("service account holds %d memberships", n)
	}
}

func TestMCP_ServiceBearerCannotCreateProjects(t *testing.T) {
	f := newServiceBearerFixture(t)
	before := f.count(t, `SELECT COUNT(*) FROM projects`)

	status, out := f.call(t, "projects_create", map[string]any{"name": "Bot's own project"})
	if status != http.StatusForbidden {
		t.Fatalf("projects_create: status=%d %#v, want 403", status, out)
	}
	if got := f.count(t, `SELECT COUNT(*) FROM projects`); got != before {
		t.Fatalf("project count went from %d to %d", before, got)
	}
	f.assertHoldsNoProjects(t)

	// The same call on a human's session and a human's token still works.
	resp, out := doMCP(t, f.owner, f.ts.URL+"/mcp", map[string]any{"tool": "projects_create", "input": map[string]any{"name": "Human cookie"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("human cookie projects_create: status=%d %#v", resp.StatusCode, out)
	}
	_, plain, _, err := f.st.CreateUserAPIToken(context.Background(), f.ownerID, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	resp, out = postMCPWithBearer(t, f.bare, f.ts.URL, plain, map[string]any{"tool": "projects_create", "input": map[string]any{"name": "Human token"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("human service-flagged token projects_create: status=%d %#v", resp.StatusCode, out)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM projects WHERE owner_user_id = ?`, f.ownerID); n != 2 {
		t.Fatalf("human owns %d projects, want 2", n)
	}
}

// Every project-scoped tool refuses a service bearer on a project it was not delegated, including
// boards it could otherwise reach just by holding the link.
func TestMCP_ServiceBearerNeedsMembershipForEveryProjectTool(t *testing.T) {
	f := newServiceBearerFixture(t)
	durable, err := f.st.CreateProject(f.ownerCtx, "Durable")
	if err != nil {
		t.Fatal(err)
	}
	temp, err := f.st.CreateAnonymousBoard(f.ownerCtx)
	if err != nil {
		t.Fatal(err)
	}
	anon, err := f.st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seedCtx := map[string]context.Context{durable.Slug: f.ownerCtx, temp.Slug: f.ownerCtx, anon.Slug: context.Background()}
	seedProject := map[string]int64{durable.Slug: durable.ID, temp.Slug: temp.ID, anon.Slug: anon.ID}
	for slug, ctx := range seedCtx {
		if _, err := f.st.CreateTodo(ctx, seedProject[slug], store.CreateTodoInput{Title: "Seed", ColumnKey: store.DefaultColumnBacklog}, store.ModeFull); err != nil {
			t.Fatalf("seed %s: %v", slug, err)
		}
	}
	todosBefore := f.count(t, `SELECT COUNT(*) FROM todos`)

	tools := map[string]map[string]any{
		"board_get":               {},
		"todos_search":            {},
		"todos_countCompleted":    {"period": "this-week"},
		"sprints_list":            {},
		"sprints_getActive":       {},
		"tags_listProject":        {},
		"members_list":            {},
		"members_listAvailable":   {},
		"workflow_list":           {},
		"priorities_list":         {},
		"customFields_list":       {},
		"metrics_getBurndown":     {},
		"metrics_getBacklogSize":  {},
		"todos_get":               {"localId": 1},
		"todos_linksList":         {"localId": 1},
		"todos_create":            {"title": "Written by a bot"},
		"todos_update":            {"localId": 1, "patch": map[string]any{"title": "Changed"}},
		"todos_delete":            {"localId": 1},
		"todos_archive":           {"localIds": []int64{1}},
		"todos_restore":           {"localIds": []int64{1}},
		"todos_move":              {"localId": 1, "toColumnKey": store.DefaultColumnDoing},
		"sprints_create":          {"name": "Sprint", "plannedStartAt": "2030-01-01T00:00:00Z", "plannedEndAt": "2030-01-15T00:00:00Z"},
		"workflow_create":         {"name": "Column"},
		"priorities_create":       {"name": "Urgent"},
		"customFields_create":     {"name": "Field", "type": "text", "key": "field"},
		"tags_updateProjectColor": {"tagId": 1, "color": "#ffffff"},
		"tags_deleteProject":      {"tagId": 1},
		"members_add":             {"userId": f.svc.ID, "role": "maintainer"},
		"projects_update":         {"patch": map[string]any{"name": "Renamed"}},
		"projects_delete":         {},
	}
	for name, slug := range map[string]string{"durable": durable.Slug, "temporary": temp.Slug, "anonymous": anon.Slug} {
		t.Run(name, func(t *testing.T) {
			for tool, input := range tools {
				merged := map[string]any{"projectSlug": slug}
				for k, v := range input {
					merged[k] = v
				}
				status, out := f.call(t, tool, merged)
				if status != http.StatusNotFound && status != http.StatusForbidden {
					t.Errorf("%s: status=%d %#v, want an authorization refusal rather than a validation error", tool, status, out)
				}
			}
		})
	}
	if got := f.count(t, `SELECT COUNT(*) FROM todos`); got != todosBefore {
		t.Errorf("todo count went from %d to %d", todosBefore, got)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM projects WHERE id IN (?, ?, ?)`, durable.ID, temp.ID, anon.ID); n != 3 {
		t.Errorf("%d of 3 projects remain", n)
	}
	f.assertHoldsNoProjects(t)
	if status, out := f.call(t, "projects_list", map[string]any{}); status != http.StatusOK {
		t.Fatalf("projects_list: status=%d %#v", status, out)
	} else if data, _ := out["data"].(map[string]any); len(asSlice(data["items"])) != 0 {
		t.Errorf("projects_list exposed projects: %#v", out)
	}

	// The owner, who holds no membership row on the temporary board, still gets link access.
	resp, out := doMCP(t, f.owner, f.ts.URL+"/mcp", map[string]any{"tool": "board_get", "input": map[string]any{"projectSlug": anon.Slug}})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("human on an anonymous board: status=%d %#v", resp.StatusCode, out)
	}
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

// An explicit membership opens exactly that project at exactly that role, and removal closes it.
func TestMCP_ServiceBearerExplicitMembership(t *testing.T) {
	f := newServiceBearerFixture(t)
	delegated, err := f.st.CreateProject(f.ownerCtx, "Delegated")
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.st.CreateProject(f.ownerCtx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	add := func(role string) {
		t.Helper()
		resp, out := doMCP(t, f.owner, f.ts.URL+"/mcp", map[string]any{"tool": "members_add", "input": map[string]any{"projectSlug": delegated.Slug, "userId": f.svc.ID, "role": role}})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("members_add %s: status=%d %#v", role, resp.StatusCode, out)
		}
	}

	add("viewer")
	if status, out := f.call(t, "board_get", map[string]any{"projectSlug": delegated.Slug}); status != http.StatusOK {
		t.Errorf("viewer read: status=%d %#v", status, out)
	}
	if status, out := f.call(t, "todos_create", map[string]any{"projectSlug": delegated.Slug, "title": "As viewer"}); status == http.StatusOK {
		t.Errorf("viewer wrote a todo: %#v", out)
	}
	if status, out := f.call(t, "board_get", map[string]any{"projectSlug": other.Slug}); status != http.StatusNotFound {
		t.Errorf("unrelated project: status=%d %#v, want 404", status, out)
	}
	status, out := f.call(t, "projects_list", map[string]any{})
	if data, _ := out["data"].(map[string]any); status != http.StatusOK || len(asSlice(data["items"])) != 1 {
		t.Errorf("projects_list: status=%d %#v, want only the delegated project", status, out)
	}

	if _, err := f.db.Exec(`UPDATE project_members SET role = 'maintainer' WHERE user_id = ?`, f.svc.ID); err != nil {
		t.Fatal(err)
	}
	if status, out := f.call(t, "todos_create", map[string]any{"projectSlug": delegated.Slug, "title": "As maintainer", "columnKey": store.DefaultColumnBacklog}); status != http.StatusOK {
		t.Errorf("maintainer write: status=%d %#v", status, out)
	}
	// Being a Maintainer somewhere confers no ownership, and no new projects.
	if status, out := f.call(t, "projects_create", map[string]any{"name": "Escalated"}); status != http.StatusForbidden {
		t.Errorf("projects_create as a Maintainer: status=%d %#v, want 403", status, out)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM projects WHERE owner_user_id = ?`, f.svc.ID); n != 0 {
		t.Errorf("service account owns %d projects", n)
	}

	if err := f.st.RemoveProjectMember(f.ownerCtx, f.ownerID, delegated.ID, f.svc.ID); err != nil {
		t.Fatal(err)
	}
	if status, out := f.call(t, "board_get", map[string]any{"projectSlug": delegated.Slug}); status != http.StatusNotFound {
		t.Errorf("after removal: status=%d %#v, want 404", status, out)
	}
}

// REST does not accept bearer tokens, so a service token cannot reach the routes that only have MCP
// counterparts in later slices: project creation, claiming, imports, or minting credentials.
func TestMCP_ServiceBearerGainsNothingOverREST(t *testing.T) {
	f := newServiceBearerFixture(t)
	temp, err := f.st.CreateAnonymousBoard(f.ownerCtx)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/projects", `{"name":"From REST"}`},
		{http.MethodPost, "/api/board/" + temp.Slug + "/claim", ``},
		{http.MethodPost, "/api/backup/import", `{}`},
		{http.MethodPost, "/api/me/tokens", `{"name":"Escalation"}`},
		{http.MethodGet, "/api/me/tokens", ``},
		{http.MethodGet, "/api/admin/users", ``},
		{http.MethodPost, "/api/auth/2fa/setup", `{}`},
		{http.MethodPost, "/api/webhooks", `{}`},
	} {
		req, err := http.NewRequest(route.method, f.ts.URL+route.path, strings.NewReader(route.body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Scrumboy", "1")
		req.Header.Set("Authorization", "Bearer "+f.bearer)
		resp, err := f.bare.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		for _, c := range resp.Cookies() {
			if c.Name == "scrumboy_session" && c.Value != "" {
				t.Errorf("%s %s issued a session cookie", route.method, route.path)
			}
		}
	}
	f.assertHoldsNoProjects(t)
	if n := f.count(t, `SELECT COUNT(*) FROM api_tokens WHERE user_id = ?`, f.svc.ID); n != 1 {
		t.Errorf("service account has %d tokens, want only the seeded one", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM sessions WHERE user_id = ?`, f.svc.ID); n != 0 {
		t.Errorf("service account has %d sessions", n)
	}
	var owner sql.NullInt64
	if err := f.db.QueryRow(`SELECT owner_user_id FROM projects WHERE id = ?`, temp.ID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner.Valid {
		t.Errorf("the temporary board was claimed by user %d", owner.Int64)
	}
}

// Explicit delegation carries the whole of the role it names, because a project has one Maintainer
// role. That is project administration, and it stops at the project: nothing here reaches the instance.
func TestMCP_ServiceMaintainerAdministersOnlyItsDelegatedProject(t *testing.T) {
	f := newServiceBearerFixture(t)
	delegated, err := f.st.CreateProject(f.ownerCtx, "Delegated")
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.st.CreateProject(f.ownerCtx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.AddProjectMember(f.ownerCtx, f.ownerID, delegated.ID, f.svc.ID, store.RoleMaintainer); err != nil {
		t.Fatal(err)
	}
	human, err := f.st.CreateUser(context.Background(), "colleague@example.com", "CorrectHorse123!", "Colleague")
	if err != nil {
		t.Fatal(err)
	}

	if status, out := f.call(t, "members_add", map[string]any{"projectSlug": delegated.Slug, "userId": human.ID, "role": "viewer"}); status != http.StatusOK {
		t.Errorf("members_add on the delegated project: status=%d %#v", status, out)
	}
	if status, out := f.call(t, "members_add", map[string]any{"projectSlug": other.Slug, "userId": human.ID, "role": "viewer"}); status != http.StatusNotFound {
		t.Errorf("members_add on another project: status=%d %#v, want 404", status, out)
	}
	for tool, input := range map[string]map[string]any{
		"admin_listUsers":      {},
		"admin_updateUserRole": {"userId": human.ID, "role": "admin"},
		"admin_deleteUser":     {"userId": human.ID},
	} {
		if status, out := f.call(t, tool, input); status != http.StatusForbidden {
			t.Errorf("%s as a Maintainer: status=%d %#v, want 403", tool, status, out)
		}
	}
	if status, out := f.call(t, "projects_delete", map[string]any{"projectSlug": other.Slug}); status != http.StatusNotFound {
		t.Errorf("projects_delete on another project: status=%d %#v, want 404", status, out)
	}
	if status, out := f.call(t, "projects_delete", map[string]any{"projectSlug": delegated.Slug}); status != http.StatusOK {
		t.Errorf("projects_delete on the delegated project: status=%d %#v", status, out)
	}
}
