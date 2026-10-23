package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"scrumboy/internal/store"
)

func (f *assetHTTPFixture) roleJSON(t *testing.T, role, method, path string, body any) (int, string) {
	t.Helper()
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, f.ts.URL+path, r)
	req.Header.Set("X-Scrumboy", "1")
	req.Header.Set("Content-Type", "application/json")
	if token := f.tokens[role]; token != "" {
		req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: token})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// TestCustomFieldsRESTRolesAtomicityAndPublicProjection covers definition permissions per role,
// the contributor notes-only scope, all-or-nothing value patches and that values never reach
// public board responses or another project.
func TestCustomFieldsRESTRolesAtomicityAndPublicProjection(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	ctx := f.ownerCtx()
	p := f.project
	for _, def := range []struct {
		key string
		typ store.CustomFieldType
	}{{"ticket", store.CustomFieldURL}, {"points", store.CustomFieldNumber}, {"secret", store.CustomFieldText}} {
		if _, err := f.st.CreateCustomField(ctx, p.ID, def.key, strings.ToUpper(def.key), def.typ); err != nil {
			t.Fatal(err)
		}
	}
	contributorID := f.users["contributor"].ID
	todo, err := f.st.CreateTodo(ctx, p.ID, store.CreateTodoInput{
		Title: "Story", Body: "notes", ColumnKey: store.DefaultColumnBacklog, AssigneeUserID: &contributorID,
		CustomFields: store.CustomFieldValues{"ticket": "https://example.com/T-1", "points": float64(3), "secret": "cf-value-sentinel"},
	}, store.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.st.CreateProject(ctx, "Other Fields")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreateCustomField(ctx, other.ID, "elsewhere", "Elsewhere", store.CustomFieldText); err != nil {
		t.Fatal(err)
	}
	board := "/api/board/" + p.Slug
	todoPath := fmt.Sprintf("%s/todos/%d", board, todo.LocalID)
	stored := func() (store.Todo, int) {
		t.Helper()
		got, err := f.st.GetTodoByLocalID(ctx, p.ID, todo.LocalID, store.ModeFull)
		if err != nil {
			t.Fatal(err)
		}
		var defs int
		_ = f.sqlDB.QueryRow(`SELECT COUNT(*) FROM project_custom_fields WHERE project_id = ?`, p.ID).Scan(&defs)
		return got, defs
	}
	unchanged := func(what string) {
		t.Helper()
		got, defs := stored()
		if got.Title != "Story" || got.Body != "notes" || defs != 3 || len(got.CustomFields) != 3 || got.CustomFields["points"] != float64(3) || got.CustomFields["secret"] != "cf-value-sentinel" {
			t.Fatalf("%s changed the story: title=%q body=%q values=%v defs=%d", what, got.Title, got.Body, got.CustomFields, defs)
		}
	}

	t.Run("definitions", func(t *testing.T) {
		for role, want := range map[string]int{"maintainer": 200, "contributor": 200, "viewer": 200, "outsider": 404} {
			if status, raw := f.roleJSON(t, role, http.MethodGet, board+"/custom-fields", nil); status != want {
				t.Errorf("%s list: %d %s, want %d", role, status, raw, want)
			}
		}
		for _, role := range []string{"contributor", "viewer", "outsider"} {
			create, _ := f.roleJSON(t, role, http.MethodPost, board+"/custom-fields", map[string]any{"name": "Sneaky", "type": "text"})
			del, _ := f.roleJSON(t, role, http.MethodDelete, board+"/custom-fields/secret", nil)
			if create == http.StatusCreated || create == http.StatusOK || del == http.StatusNoContent {
				t.Errorf("%s create=%d delete=%d", role, create, del)
			}
		}
		unchanged("non-maintainer definition requests")
	})

	t.Run("contributor notes-only scope", func(t *testing.T) {
		patch := map[string]any{"title": "Story", "body": "contributor notes", "tags": []string{}, "assigneeUserId": contributorID,
			"customFields": map[string]any{"points": float64(99)}}
		if status, raw := f.roleJSON(t, "contributor", http.MethodPatch, todoPath, patch); status != http.StatusForbidden {
			t.Fatalf("contributor customFields patch: %d %s", status, raw)
		}
		unchanged("rejected contributor patch")
		for _, role := range []string{"viewer", "outsider"} {
			if status, _ := f.roleJSON(t, role, http.MethodPatch, todoPath, patch); status == http.StatusOK {
				t.Fatalf("%s patch succeeded", role)
			}
		}
		unchanged("viewer/outsider patch")
		delete(patch, "customFields")
		if status, raw := f.roleJSON(t, "contributor", http.MethodPatch, todoPath, patch); status != http.StatusOK {
			t.Fatalf("contributor notes patch: %d %s", status, raw)
		}
		got, _ := stored()
		if got.Body != "contributor notes" || got.CustomFields["points"] != float64(3) || len(got.CustomFields) != 3 {
			t.Fatalf("after contributor notes patch: body=%q values=%v", got.Body, got.CustomFields)
		}
		if _, err := f.st.UpdateTodoByLocalID(ctx, p.ID, todo.LocalID, store.UpdateTodoInput{
			Title: "Story", Body: "notes", Tags: []string{}, AssigneeUserID: &contributorID,
		}, store.ModeFull); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("patches are all or nothing", func(t *testing.T) {
		for name, fields := range map[string]map[string]any{
			"unknown key":            {"points": float64(5), "nope": "x"},
			"other project's key":    {"points": float64(5), "elsewhere": "x"},
			"invalid url":            {"points": float64(5), "ticket": "javascript:alert(1)"},
			"number out of range":    {"secret": nil, "points": float64(1 << 54)},
			"string for number":      {"secret": nil, "points": "5"},
			"empty text (not clear)": {"points": float64(5), "secret": "  "},
		} {
			status, raw := f.roleJSON(t, "maintainer", http.MethodPatch, todoPath, map[string]any{
				"title": "Renamed", "body": "changed", "tags": []string{}, "assigneeUserId": contributorID, "customFields": fields,
			})
			if status != http.StatusBadRequest {
				t.Fatalf("%s: %d %s, want 400", name, status, raw)
			}
			unchanged(name)
		}
		status, raw := f.roleJSON(t, "maintainer", http.MethodPost, board+"/todos", map[string]any{
			"title": "New", "columnKey": store.DefaultColumnBacklog, "customFields": map[string]any{"points": float64(1), "elsewhere": "x"},
		})
		if status != http.StatusBadRequest {
			t.Fatalf("create with other project's key: %d %s", status, raw)
		}
		var n int
		_ = f.sqlDB.QueryRow(`SELECT COUNT(*) FROM todos WHERE title = 'New'`).Scan(&n)
		if n != 0 {
			t.Fatal("rejected create left a todo")
		}
	})

	t.Run("public board never projects values", func(t *testing.T) {
		f.publish(t)
		public := "/api/public/board/" + p.Slug
		for _, path := range []string{public, fmt.Sprintf("%s/todos/%d", public, todo.LocalID), fmt.Sprintf("%s/todos/%d/links", public, todo.LocalID)} {
			status, raw := f.roleJSON(t, "", http.MethodGet, path, nil)
			if status != http.StatusOK {
				t.Fatalf("%s: %d %s", path, status, raw)
			}
			for _, leaked := range []string{"customFields", "cf-value-sentinel", "example.com/T-1"} {
				if strings.Contains(raw, leaked) {
					t.Fatalf("%s leaked %q: %s", path, leaked, raw)
				}
			}
		}
		if status, _ := f.roleJSON(t, "", http.MethodGet, board+"/custom-fields", nil); status == http.StatusOK {
			t.Fatal("anonymous visitor listed definitions of a public board")
		}
		if status, raw := f.roleJSON(t, "outsider", http.MethodGet, board+"/custom-fields", nil); status == http.StatusOK {
			t.Fatalf("outsider listed definitions of a public board: %s", raw)
		}
	})
}
