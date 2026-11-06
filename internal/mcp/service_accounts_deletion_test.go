package mcp_test

import (
	"context"
	"net/http"
	"testing"

	"scrumboy/internal/store"
)

// Over the real transport: a refused deletion leaves the token working, and a deletion that succeeds
// ends it on the very next call.
func TestMCP_AccountDeletionConflictThenRevokesToken(t *testing.T) {
	f := newServiceBearerFixture(t)
	project, err := f.st.CreateProject(f.ownerCtx, "Led by the bot")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.AddProjectMember(f.ownerCtx, f.ownerID, project.ID, f.svc.ID, store.RoleMaintainer); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`DELETE FROM project_members WHERE project_id = ? AND user_id = ?`, project.ID, f.ownerID); err != nil {
		t.Fatal(err)
	}
	deleteAccount := map[string]any{"tool": "admin_deleteUser", "input": map[string]any{"userId": f.svc.ID}}

	resp, out := doMCP(t, f.owner, f.ts.URL+"/mcp", deleteAccount)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("sole Maintainer: status=%d %#v, want 409", resp.StatusCode, out)
	}
	if code := out["error"].(map[string]any)["code"]; code != "CONFLICT" {
		t.Fatalf("error code = %v, want CONFLICT", code)
	}
	if status, out := f.call(t, "projects_list", map[string]any{}); status != http.StatusOK {
		t.Fatalf("token after a refused deletion: status=%d %#v", status, out)
	}

	svcCtx := store.WithUserID(context.Background(), f.svc.ID)
	if err := f.st.AddProjectMember(svcCtx, f.svc.ID, project.ID, f.ownerID, store.RoleMaintainer); err != nil {
		t.Fatal(err)
	}
	resp, out = doMCP(t, f.owner, f.ts.URL+"/mcp", deleteAccount)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete once safe: status=%d %#v", resp.StatusCode, out)
	}
	if status, out := f.call(t, "projects_list", map[string]any{}); status != http.StatusUnauthorized {
		t.Fatalf("token after deletion: status=%d %#v, want 401", status, out)
	}
}

// Identity comes from the credential alone: fields a caller adds to a request change nothing.
func TestMCP_ForgedIdentityFieldsAreIgnored(t *testing.T) {
	f := newServiceBearerFixture(t)
	project, err := f.st.CreateProject(f.ownerCtx, "Private")
	if err != nil {
		t.Fatal(err)
	}
	for tool, input := range map[string]map[string]any{
		"projects_create": {"name": "Forged", "isService": false, "is_service": false, "userId": f.ownerID, "systemRole": "owner"},
		"todos_create":    {"projectSlug": project.Slug, "title": "Forged", "isService": false, "actorUserId": f.ownerID},
		"admin_listUsers": {"isService": false, "systemRole": "owner", "userId": f.ownerID},
	} {
		if status, out := f.call(t, tool, input); status == http.StatusOK {
			t.Errorf("%s accepted forged identity fields: %#v", tool, out)
		}
	}
	f.assertHoldsNoProjects(t)
}
