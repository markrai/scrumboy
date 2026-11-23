package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"scrumboy/internal/store"
)

func TestGuestRoleREST(t *testing.T) {
	f := newAdminUserRESTFixture(t)
	ctx := store.WithUserID(context.Background(), f.owner.ID)
	assigned, err := f.store.CreateProject(ctx, "Assigned")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddProjectMember(ctx, f.owner.ID, assigned.ID, f.user.ID, store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	other, err := f.store.CreateProject(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}

	temporary, err := f.store.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatal(err)
	}

	rolePath := fmt.Sprintf("/api/admin/users/%d/role", f.user.ID)
	resp, body := doJSON(t, f.ownerClient, http.MethodPatch, f.ts.URL+rolePath, map[string]string{"role": "guest"}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("assign guest: %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, f.userClient, http.MethodGet, f.ts.URL+"/api/me", nil, nil)
	var me currentUserJSON
	if err := json.Unmarshal(body, &me); err != nil || resp.StatusCode != http.StatusOK || me.SystemRole != "guest" || me.Capabilities.CreateBoards {
		t.Fatalf("guest profile: %d %s, %v", resp.StatusCode, body, err)
	}

	for _, tc := range []struct {
		method string
		path   string
		data   any
		want   int
	}{
		{http.MethodPost, "/api/projects", map[string]string{"name": "Denied"}, http.StatusForbidden},
		{http.MethodGet, "/api/board/" + assigned.Slug, nil, http.StatusOK},
		{http.MethodGet, "/api/board/" + other.Slug, nil, http.StatusNotFound},
		{http.MethodGet, "/api/board/" + temporary.Slug, nil, http.StatusOK},
		{http.MethodPost, "/api/board/" + temporary.Slug + "/todos", map[string]string{"title": "Shared card"}, http.StatusCreated},
		{http.MethodGet, "/anon", nil, http.StatusForbidden},
	} {
		resp, body := doJSON(t, f.userClient, tc.method, f.ts.URL+tc.path, tc.data, nil)
		if resp.StatusCode != tc.want {
			t.Errorf("%s %s: %d, want %d: %s", tc.method, tc.path, resp.StatusCode, tc.want, body)
		}
	}
}
