package mcp_test

import (
	"context"
	"net/http"
	"testing"

	"scrumboy/internal/store"
)

func createTwoTodosForLinking(t *testing.T, client *http.Client, tsURL, slug string) (fromLocalID, toLocalID float64) {
	t.Helper()

	resp1, out1 := doMCP(t, client, tsURL+"/mcp", map[string]any{
		"tool": "todos_create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "From todo",
		},
	})
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("todos_create (from) status=%d body=%#v", resp1.StatusCode, out1)
	}
	fromTodo := out1["data"].(map[string]any)["todo"].(map[string]any)

	resp2, out2 := doMCP(t, client, tsURL+"/mcp", map[string]any{
		"tool": "todos_create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "To todo",
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("todos_create (to) status=%d body=%#v", resp2.StatusCode, out2)
	}
	toTodo := out2["data"].(map[string]any)["todo"].(map[string]any)

	return fromTodo["localId"].(float64), toTodo["localId"].(float64)
}

func TestMCPTodosLinkAddAndListSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Link Add Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Link Add Project")

	fromID, toID := createTwoTodosForLinking(t, client, ts.URL, slug)

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linkAdd",
		"input": map[string]any{
			"projectSlug":   slug,
			"localId":       fromID,
			"targetLocalId": toID,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("todos_linkAdd status=%d body=%#v", resp2.StatusCode, out)
	}
	data := out["data"].(map[string]any)
	outbound := data["outbound"].([]any)
	if len(outbound) != 1 {
		t.Fatalf("expected 1 outbound link, got %#v", outbound)
	}
	link := outbound[0].(map[string]any)
	if link["localId"] != toID {
		t.Fatalf("expected outbound localId %v, got %#v", toID, link["localId"])
	}
	if link["linkType"] != "relates_to" {
		t.Fatalf("expected default linkType relates_to, got %#v", link["linkType"])
	}
	inbound := data["inbound"].([]any)
	if len(inbound) != 0 {
		t.Fatalf("expected 0 inbound links on the from-todo, got %#v", inbound)
	}

	resp3, out3 := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linksList",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     toID,
		},
	})
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("todos_linksList status=%d body=%#v", resp3.StatusCode, out3)
	}
	data3 := out3["data"].(map[string]any)
	inbound3 := data3["inbound"].([]any)
	if len(inbound3) != 1 {
		t.Fatalf("expected 1 inbound link on the to-todo, got %#v", inbound3)
	}
}

func TestMCPTodosLinkAddWithExplicitLinkType(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Link Type Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Link Type Project")

	fromID, toID := createTwoTodosForLinking(t, client, ts.URL, slug)

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linkAdd",
		"input": map[string]any{
			"projectSlug":   slug,
			"localId":       fromID,
			"targetLocalId": toID,
			"linkType":      "blocks",
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("todos_linkAdd status=%d body=%#v", resp2.StatusCode, out)
	}
	data := out["data"].(map[string]any)
	outbound := data["outbound"].([]any)
	link := outbound[0].(map[string]any)
	if link["linkType"] != "blocks" {
		t.Fatalf("expected linkType blocks, got %#v", link["linkType"])
	}
}

func TestMCPTodosLinkAddRejectsSelfLink(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Link Self Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Link Self Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos_create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Solo todo",
		},
	})

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linkAdd",
		"input": map[string]any{
			"projectSlug":   slug,
			"localId":       1,
			"targetLocalId": 1,
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%#v", resp2.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPTodosLinkAddNotFoundForMissingTarget(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Link Missing Target Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Link Missing Target Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos_create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Solo todo",
		},
	})

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linkAdd",
		"input": map[string]any{
			"projectSlug":   slug,
			"localId":       1,
			"targetLocalId": 999,
		},
	})
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%#v", resp2.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %#v", errObj["code"])
	}
}

func TestMCPTodosLinkRemoveSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Link Remove Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Link Remove Project")

	fromID, toID := createTwoTodosForLinking(t, client, ts.URL, slug)

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linkAdd",
		"input": map[string]any{
			"projectSlug":   slug,
			"localId":       fromID,
			"targetLocalId": toID,
		},
	})

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linkRemove",
		"input": map[string]any{
			"projectSlug":   slug,
			"localId":       fromID,
			"targetLocalId": toID,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("todos_linkRemove status=%d body=%#v", resp2.StatusCode, out)
	}
	data := out["data"].(map[string]any)
	outbound := data["outbound"].([]any)
	if len(outbound) != 0 {
		t.Fatalf("expected 0 outbound links after removal, got %#v", outbound)
	}
}

func TestMCPTodosLinkRemoveNotFoundForMissingLink(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Link Remove Missing Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Link Remove Missing Project")

	fromID, toID := createTwoTodosForLinking(t, client, ts.URL, slug)

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linkRemove",
		"input": map[string]any{
			"projectSlug":   slug,
			"localId":       fromID,
			"targetLocalId": toID,
		},
	})
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%#v", resp2.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %#v", errObj["code"])
	}
}

func TestMCPTodosLinksListRequiresAuth(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Link List Auth Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Link List Auth Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos_create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Solo todo",
		},
	})

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "todos_linksList",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     1,
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPTodosLinkAddCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "todos_linkAdd",
		"input": map[string]any{
			"projectSlug":   "demo",
			"localId":       1,
			"targetLocalId": 2,
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPTodosLinkAddRejectsInvalidLinkType(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Link Invalid Type Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Link Invalid Type Project")

	fromID, toID := createTwoTodosForLinking(t, client, ts.URL, slug)

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linkAdd",
		"input": map[string]any{
			"projectSlug":   slug,
			"localId":       fromID,
			"targetLocalId": toID,
			"linkType":      "nope",
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%#v", resp2.StatusCode, out)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

// A read-only project member (viewer) can list a todo's links...
func TestMCPTodosLinksListViewerCanRead(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Link Viewer Read Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Link Viewer Read Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	fromID, toID := createTwoTodosForLinking(t, ownerClient, ts.URL, slug)
	if r, o := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linkAdd",
		"input": map[string]any{
			"projectSlug":   slug,
			"localId":       fromID,
			"targetLocalId": toID,
		},
	}); r.StatusCode != http.StatusOK {
		t.Fatalf("owner todos_linkAdd status=%d body=%#v", r.StatusCode, o)
	}

	st := store.New(sqlDB, nil)
	viewer, err := st.CreateUser(context.Background(), "linkviewer@example.com", "password123", "Viewer")
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, viewer.ID, store.RoleViewer); err != nil {
		t.Fatalf("add viewer membership: %v", err)
	}
	viewerClient := newSessionClientForUser(t, ts, st, viewer.ID)

	resp2, out := doMCP(t, viewerClient, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linksList",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     fromID,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("viewer todos_linksList status=%d body=%#v", resp2.StatusCode, out)
	}
	data := out["data"].(map[string]any)
	outbound := data["outbound"].([]any)
	if len(outbound) != 1 {
		t.Fatalf("expected 1 outbound link for viewer, got %#v", outbound)
	}
}

// ...but cannot add a link: the store write path requires contributor+, and a
// viewer that lacks it gets ErrNotFound (existence is not leaked), surfaced as
// NOT_FOUND at the MCP boundary. The rejected add must not create a link.
func TestMCPTodosLinkAddViewerCannotAdd(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Link Viewer Add Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Link Viewer Add Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	fromID, toID := createTwoTodosForLinking(t, ownerClient, ts.URL, slug)

	st := store.New(sqlDB, nil)
	viewer, err := st.CreateUser(context.Background(), "linkvieweradd@example.com", "password123", "Viewer")
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, viewer.ID, store.RoleViewer); err != nil {
		t.Fatalf("add viewer membership: %v", err)
	}
	viewerClient := newSessionClientForUser(t, ts, st, viewer.ID)

	respAdd, outAdd := doMCP(t, viewerClient, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linkAdd",
		"input": map[string]any{
			"projectSlug":   slug,
			"localId":       fromID,
			"targetLocalId": toID,
		},
	})
	if respAdd.StatusCode != http.StatusNotFound {
		t.Fatalf("expected viewer todos_linkAdd 404, got %d body=%#v", respAdd.StatusCode, outAdd)
	}
	if code := outAdd["error"].(map[string]any)["code"]; code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND for viewer todos_linkAdd, got %#v", code)
	}

	// The rejected add must not have created a link.
	_, outList := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linksList",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     fromID,
		},
	})
	if outbound := outList["data"].(map[string]any)["outbound"].([]any); len(outbound) != 0 {
		t.Fatalf("expected no link created by rejected viewer add, got %#v", outbound)
	}
}

// A viewer cannot remove an existing link. The owner creates the link first, so
// the viewer's NOT_FOUND proves the contributor+ write gate rather than a merely
// missing link (RemoveLink authorizes before checking existence), and the link
// must survive the rejected removal.
func TestMCPTodosLinkRemoveViewerCannotRemoveExistingLink(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Link Viewer Remove Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Link Viewer Remove Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	fromID, toID := createTwoTodosForLinking(t, ownerClient, ts.URL, slug)
	if r, o := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linkAdd",
		"input": map[string]any{
			"projectSlug":   slug,
			"localId":       fromID,
			"targetLocalId": toID,
		},
	}); r.StatusCode != http.StatusOK {
		t.Fatalf("owner todos_linkAdd status=%d body=%#v", r.StatusCode, o)
	}

	st := store.New(sqlDB, nil)
	viewer, err := st.CreateUser(context.Background(), "linkviewerrem@example.com", "password123", "Viewer")
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, viewer.ID, store.RoleViewer); err != nil {
		t.Fatalf("add viewer membership: %v", err)
	}
	viewerClient := newSessionClientForUser(t, ts, st, viewer.ID)

	respRemove, outRemove := doMCP(t, viewerClient, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linkRemove",
		"input": map[string]any{
			"projectSlug":   slug,
			"localId":       fromID,
			"targetLocalId": toID,
		},
	})
	if respRemove.StatusCode != http.StatusNotFound {
		t.Fatalf("expected viewer todos_linkRemove 404, got %d body=%#v", respRemove.StatusCode, outRemove)
	}
	if code := outRemove["error"].(map[string]any)["code"]; code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND for viewer todos_linkRemove, got %#v", code)
	}

	// The existing link must survive the rejected removal.
	_, outList := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linksList",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     fromID,
		},
	})
	if outbound := outList["data"].(map[string]any)["outbound"].([]any); len(outbound) != 1 {
		t.Fatalf("expected existing link to survive rejected viewer remove, got %#v", outbound)
	}
}

// An authenticated user who is not a member of the project cannot even resolve
// it: GetProjectContextBySlug returns ErrNotFound (existence not leaked), so
// list and mutate both surface NOT_FOUND.
func TestMCPTodosLinksNonMemberNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Link Non Member Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Link Non Member Project")

	fromID, toID := createTwoTodosForLinking(t, ownerClient, ts.URL, slug)

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "linknonmember@example.com", "password123", "Other")
	if err != nil {
		t.Fatalf("create non-member: %v", err)
	}
	otherClient := newSessionClientForUser(t, ts, st, other.ID)

	respList, outList := doMCP(t, otherClient, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linksList",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     fromID,
		},
	})
	if respList.StatusCode != http.StatusNotFound {
		t.Fatalf("expected non-member todos_linksList 404, got %d body=%#v", respList.StatusCode, outList)
	}
	if code := outList["error"].(map[string]any)["code"]; code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND for non-member todos_linksList, got %#v", code)
	}

	respAdd, outAdd := doMCP(t, otherClient, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linkAdd",
		"input": map[string]any{
			"projectSlug":   slug,
			"localId":       fromID,
			"targetLocalId": toID,
		},
	})
	if respAdd.StatusCode != http.StatusNotFound {
		t.Fatalf("expected non-member todos_linkAdd 404, got %d body=%#v", respAdd.StatusCode, outAdd)
	}
	if code := outAdd["error"].(map[string]any)["code"]; code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND for non-member todos_linkAdd, got %#v", code)
	}

	respRemove, outRemove := doMCP(t, otherClient, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linkRemove",
		"input": map[string]any{
			"projectSlug":   slug,
			"localId":       fromID,
			"targetLocalId": toID,
		},
	})
	if respRemove.StatusCode != http.StatusNotFound {
		t.Fatalf("expected non-member todos_linkRemove 404, got %d body=%#v", respRemove.StatusCode, outRemove)
	}
	if code := outRemove["error"].(map[string]any)["code"]; code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND for non-member todos_linkRemove, got %#v", code)
	}
}

// A contributor (the minimum intended write role) can both add and remove links.
// This pins the lower bound of the write gate so a future regression that tightened
// it to maintainer/owner-only would be caught by the suite.
func TestMCPTodosLinkContributorCanMutate(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Link Contributor Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Link Contributor Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	fromID, toID := createTwoTodosForLinking(t, ownerClient, ts.URL, slug)

	st := store.New(sqlDB, nil)
	contributor, err := st.CreateUser(context.Background(), "linkcontrib@example.com", "password123", "Contributor")
	if err != nil {
		t.Fatalf("create contributor: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, contributor.ID, store.RoleContributor); err != nil {
		t.Fatalf("add contributor membership: %v", err)
	}
	contribClient := newSessionClientForUser(t, ts, st, contributor.ID)

	respAdd, outAdd := doMCP(t, contribClient, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linkAdd",
		"input": map[string]any{
			"projectSlug":   slug,
			"localId":       fromID,
			"targetLocalId": toID,
		},
	})
	if respAdd.StatusCode != http.StatusOK {
		t.Fatalf("expected contributor todos_linkAdd 200, got %d body=%#v", respAdd.StatusCode, outAdd)
	}
	if outbound := outAdd["data"].(map[string]any)["outbound"].([]any); len(outbound) != 1 {
		t.Fatalf("expected 1 outbound link after contributor add, got %#v", outbound)
	}

	respRemove, outRemove := doMCP(t, contribClient, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linkRemove",
		"input": map[string]any{
			"projectSlug":   slug,
			"localId":       fromID,
			"targetLocalId": toID,
		},
	})
	if respRemove.StatusCode != http.StatusOK {
		t.Fatalf("expected contributor todos_linkRemove 200, got %d body=%#v", respRemove.StatusCode, outRemove)
	}
	if outbound := outRemove["data"].(map[string]any)["outbound"].([]any); len(outbound) != 0 {
		t.Fatalf("expected 0 outbound links after contributor remove, got %#v", outbound)
	}
}

// The target todo lookup is scoped by project_id: a target localId that exists
// only in another project must not resolve. Project A has a single source todo
// (localId 1) and no localId 2; project B has a localId 2. Linking A's source to
// targetLocalId 2 must fail with NOT_FOUND rather than resolving B's todo.
func TestMCPTodosLinkAddCrossProjectTargetIsolation(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	respA := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Cross Project A",
	}, &map[string]any{})
	if respA.StatusCode != http.StatusCreated {
		t.Fatalf("create project A status=%d", respA.StatusCode)
	}
	slugA := projectSlugByName(t, sqlDB, "Cross Project A")

	respB := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Cross Project B",
	}, &map[string]any{})
	if respB.StatusCode != http.StatusCreated {
		t.Fatalf("create project B status=%d", respB.StatusCode)
	}
	slugB := projectSlugByName(t, sqlDB, "Cross Project B")

	// Project A: a single source todo (localId 1); no localId 2 exists here.
	rA, oA := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos_create",
		"input": map[string]any{
			"projectSlug": slugA,
			"title":       "A source",
		},
	})
	if rA.StatusCode != http.StatusOK {
		t.Fatalf("create A source todo status=%d body=%#v", rA.StatusCode, oA)
	}
	sourceLocalID := oA["data"].(map[string]any)["todo"].(map[string]any)["localId"].(float64)

	// Project B: two todos so a localId 2 exists in B (but not in A).
	_, targetLocalID := createTwoTodosForLinking(t, client, ts.URL, slugB)

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos_linkAdd",
		"input": map[string]any{
			"projectSlug":   slugA,
			"localId":       sourceLocalID,
			"targetLocalId": targetLocalID,
		},
	})
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected cross-project todos_linkAdd 404, got %d body=%#v", resp2.StatusCode, out)
	}
	if code := out["error"].(map[string]any)["code"]; code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND for cross-project target, got %#v", code)
	}
}
