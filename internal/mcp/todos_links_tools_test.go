package mcp_test

import (
	"net/http"
	"testing"
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
