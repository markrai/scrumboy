package httpapi

import (
	"net/http"
	"strconv"
	"testing"

	"scrumboy/internal/store"
)

func TestCustomFieldsREST(t *testing.T) {
	fx := newPriorityRESTFixture(t, "custom-fields")
	base := fx.ts.URL + "/api/board/" + fx.project.Slug

	resp, body := doJSON(t, fx.client, http.MethodPost, base+"/custom-fields", map[string]any{"name": "Merge Request", "type": "url"}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d %s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, fx.client, http.MethodPost, base+"/custom-fields", map[string]any{"name": "X", "type": "boolean"}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad type status=%d %s", resp.StatusCode, body)
	}

	var list struct {
		Items []struct {
			Key  string `json:"key"`
			Type string `json:"type"`
		} `json:"items"`
	}
	resp, body = doJSON(t, fx.client, http.MethodGet, base+"/custom-fields", nil, &list)
	if resp.StatusCode != http.StatusOK || len(list.Items) != 1 || list.Items[0].Key != "merge_request" || list.Items[0].Type != "url" {
		t.Fatalf("list status=%d %s", resp.StatusCode, body)
	}

	todo, err := fx.st.CreateTodo(fx.ctx, fx.project.ID, store.CreateTodoInput{Title: "t", ColumnKey: store.DefaultColumnBacklog}, store.ModeFull)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}
	patch := func(fields any) (*http.Response, []byte, map[string]any) {
		var out map[string]any
		resp, raw := doJSON(t, fx.client, http.MethodPatch, base+"/todos/"+strconv.FormatInt(todo.LocalID, 10), map[string]any{
			"title": "t", "body": "", "tags": []string{}, "assigneeUserId": nil, "customFields": fields,
		}, &out)
		return resp, raw, out
	}
	resp, body, out := patch(map[string]any{"merge_request": "https://example.com/mr/1"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status=%d %s", resp.StatusCode, body)
	}
	if got, _ := out["customFields"].(map[string]any); got["merge_request"] != "https://example.com/mr/1" {
		t.Fatalf("customFields=%v", out["customFields"])
	}
	var createdTodo map[string]any
	resp, body = doJSON(t, fx.client, http.MethodPost, base+"/todos", map[string]any{
		"title": "new", "columnKey": store.DefaultColumnBacklog, "customFields": map[string]any{"merge_request": "https://example.com/mr/3"},
	}, &createdTodo)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create todo status=%d %s", resp.StatusCode, body)
	}
	if got, _ := createdTodo["customFields"].(map[string]any); got["merge_request"] != "https://example.com/mr/3" {
		t.Fatalf("created customFields=%v", createdTodo["customFields"])
	}
	resp, body = doJSON(t, fx.client, http.MethodPost, base+"/todos", map[string]any{"title": "x", "customFields": map[string]any{"nope": "y"}}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field create status=%d %s", resp.StatusCode, body)
	}
	for name, bad := range map[string]any{
		"unknown key": map[string]any{"nope": "x"},
		"bad url":     map[string]any{"merge_request": "ftp://x"},
		"bool":        map[string]any{"merge_request": true},
		"array":       []any{},
	} {
		if resp, body, _ := patch(bad); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status=%d %s, want 400", name, resp.StatusCode, body)
		}
	}

	resp, body = doJSON(t, fx.client, http.MethodDelete, base+"/custom-fields/merge_request", nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status=%d %s", resp.StatusCode, body)
	}
	resp, _ = doJSON(t, fx.client, http.MethodDelete, base+"/custom-fields/merge_request", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete status=%d, want 404", resp.StatusCode)
	}
}
