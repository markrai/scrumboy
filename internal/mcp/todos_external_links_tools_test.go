package mcp_test

import (
	"context"
	"net/http"
	"testing"

	"scrumboy/internal/store"
)

func TestMCPExternalReferencesLifecycle(t *testing.T) {
	ts, db, cleanup := newTestServer(t, "full")
	defer cleanup()
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	st := store.New(db, nil)
	ctx := context.Background()
	owner, err := st.GetUserByEmail(ctx, "owner@example.com")
	if err != nil { t.Fatal(err) }
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "External MCP"}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated { t.Fatalf("project create: %d",resp.StatusCode) }
	slug := projectSlugByName(t, db, "External MCP")
	create, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{"tool":"todos_create","input":map[string]any{"projectSlug":slug,"title":"Card"}})
	if create.StatusCode != http.StatusOK { t.Fatalf("todo create: %d %#v",create.StatusCode,out) }
	localID := out["data"].(map[string]any)["todo"].(map[string]any)["localId"].(float64)
	project, err := st.GetProjectBySlug(ctx, slug)
	if err != nil { t.Fatal(err) }
	connection, err := st.CreateIntegrationConnection(ctx, owner.ID, store.CreateIntegrationConnectionInput{Provider:"github",ExternalAccountID:"mcp-test",Name:"GitHub"})
	if err != nil { t.Fatal(err) }
	resource, err := st.RegisterIntegrationResource(ctx, owner.ID, connection.ID, store.RegisterIntegrationResourceInput{Kind:"repository",ExternalID:"100",Name:"org/repo",URL:"https://github.com/org/repo"})
	if err != nil { t.Fatal(err) }
	if err := st.AttachIntegrationResource(ctx, owner.ID, project.ID, resource.ID); err != nil { t.Fatal(err) }
	call := func(tool string, input map[string]any) map[string]any {
		t.Helper()
		response, result := doMCP(t, client, ts.URL+"/mcp", map[string]any{"tool":tool,"input":input})
		if response.StatusCode != http.StatusOK { t.Fatalf("%s: status=%d body=%#v",tool,response.StatusCode,result) }
		return result["data"].(map[string]any)
	}
	projectTarget := map[string]any{"projectSlug":slug}
	resources := call("integrations_resourcesList", projectTarget)["items"].([]any)
	if len(resources) != 1 { t.Fatalf("resources: %#v",resources) }
	target := map[string]any{"projectSlug":slug, "localId":localID, "resourceId":resource.ID}
	added := call("todos_externalLinkAdd",target)["items"].([]any)
	if len(added) != 1 { t.Fatalf("added: %#v",added) }
	listed := call("todos_externalLinksList",map[string]any{"projectSlug":slug,"localId":localID})["items"].([]any)
	if len(listed) != 1 { t.Fatalf("listed: %#v",listed) }
	removed := call("todos_externalLinkRemove",target)["items"].([]any)
	if len(removed) != 0 { t.Fatalf("removed: %#v",removed) }
}
