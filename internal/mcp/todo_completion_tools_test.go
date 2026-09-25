package mcp_test

import (
	"net/http"
	"testing"
)

func TestMCPTodosCountCompletedUsesCurrentProjectAuthoritativeCount(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Completion Query Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Completion Query Project")

	resp = doJSON(t, client, http.MethodPost, ts.URL+"/mcp", map[string]any{
		"tool": "todos_create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Completed now",
			"columnKey":   "done",
		},
	}, &map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create completed todo status=%d", resp.StatusCode)
	}

	countResp, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos_countCompleted",
		"input": map[string]any{
			"projectSlug": slug,
			"period":      "this-week",
			"timezone":    "UTC",
		},
	})
	if countResp.StatusCode != http.StatusOK {
		t.Fatalf("count status=%d output=%#v", countResp.StatusCode, out)
	}
	data := out["data"].(map[string]any)
	if data["count"] != float64(1) || data["period"] != "this-week" || data["timezone"] != "UTC" {
		t.Fatalf("unexpected count response: %#v", data)
	}
	if data["startAt"] == "" || data["endAt"] == "" {
		t.Fatalf("missing authoritative boundaries: %#v", data)
	}
}

func TestMCPTodosCountCompletedRejectsModelLikeAuthorityFields(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	resp, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos_countCompleted",
		"input": map[string]any{
			"projectSlug": "alpha",
			"period":      "this-week",
			"timezone":    "UTC",
			"count":       12,
		},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d output=%#v", resp.StatusCode, out)
	}
}
