package mcp_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

func TestMCPAdminUpdateUserOwnerSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	st := store.New(sqlDB, nil)
	target, err := st.CreateUser(context.Background(), "mars@example.com", "password123", "Mars Yeen")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	resp, out := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool":  "admin_updateUser",
		"input": map[string]any{"userId": target.ID, "name": "Lynix Wolf", "email": "Lynix@Example.com"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%#v", resp.StatusCode, out)
	}
	user := out["data"].(map[string]any)["user"].(map[string]any)
	if user["name"] != "Lynix Wolf" || user["email"] != "lynix@example.com" {
		t.Fatalf("unexpected user %#v", user)
	}
}

func TestMCPAdminUpdateUserErrors(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	st := store.New(sqlDB, nil)
	target, err := st.CreateUser(context.Background(), "a@example.com", "password123", "A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(context.Background(), "b@example.com", "password123", "B"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		client *http.Client
		input  map[string]any
		status int
		code   string
	}{
		{"owner ok", ownerClient, map[string]any{"userId": target.ID, "name": "Renamed"}, http.StatusOK, ""},
		{"nothing to change", ownerClient, map[string]any{"userId": target.ID}, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"bad user id", ownerClient, map[string]any{"userId": 0, "name": "X"}, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"bad email", ownerClient, map[string]any{"userId": target.ID, "email": "nope"}, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"duplicate email", ownerClient, map[string]any{"userId": target.ID, "email": "b@example.com"}, http.StatusConflict, "CONFLICT"},
		{"missing user", ownerClient, map[string]any{"userId": 999999, "name": "X"}, http.StatusNotFound, "NOT_FOUND"},
	}
	for _, tc := range cases {
		resp, out := doMCP(t, tc.client, ts.URL+"/mcp", map[string]any{"tool": "admin_updateUser", "input": tc.input})
		if resp.StatusCode != tc.status {
			t.Errorf("%s: status=%d body=%#v, want %d", tc.name, resp.StatusCode, out, tc.status)
			continue
		}
		if errObj, _ := out["error"].(map[string]any); tc.code != "" && errObj["code"] != tc.code {
			t.Errorf("%s: code=%v, want %s", tc.name, errObj["code"], tc.code)
		}
	}
}

func TestMCPAdminUpdateUserAdminForbidden(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	st := store.New(sqlDB, nil)
	target, err := st.CreateUser(context.Background(), "a@example.com", "password123", "A")
	if err != nil {
		t.Fatal(err)
	}
	_, _, adminClient := setupAdminUser(t, ts, sqlDB, "admin-update-user@example.com")

	resp, out := doMCP(t, adminClient, ts.URL+"/mcp", map[string]any{
		"tool": "admin_updateUser", "input": map[string]any{"userId": target.ID, "name": "X"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("admin: status=%d body=%#v, want 403", resp.StatusCode, out)
	}
	if got, _ := st.GetUser(context.Background(), target.ID); got.Name != "A" {
		t.Fatalf("admin changed the name to %q", got.Name)
	}
}

// TestMCPAdminUpdateUserTransportContract pins admin_updateUser to the same gate order, error
// projection and post-write behavior as admin_updateUserRole, on both MCP transports.
func TestMCPAdminUpdateUserTransportContract(t *testing.T) {
	// Legacy only: doJSONRPC answers a 401 by bootstrapping and signing in, so these gates are
	// pinned on the legacy transport, as in TestMCPAdminUserMutationCapabilityAuthenticationAndInputPrecedence.
	t.Run("legacy/pre-bootstrap", func(t *testing.T) {
		fx := newAdminMCPFixture(t, "full", false)
		resp, out := callTodoUpdateMCP(t, newStatelessClient(fx.ts), fx.ts.URL, "legacy", "admin_updateUser", map[string]any{"userId": 1, "name": "X"})
		assertTodoLinkMCPError(t, "legacy", resp, out, 403, "CAPABILITY_UNAVAILABLE", "admin_updateUser is unavailable before bootstrap")
	})
	t.Run("legacy/unauthenticated", func(t *testing.T) {
		fx := newAdminMCPFixture(t, "full", true)
		resp, out := callTodoUpdateMCP(t, newStatelessClient(fx.ts), fx.ts.URL, "legacy", "admin_updateUser", map[string]any{"userId": fx.user.ID, "name": "X"})
		assertTodoLinkMCPError(t, "legacy", resp, out, 401, "AUTH_REQUIRED", "Sign-in required for this tool")
		if got, _ := fx.store.GetUser(context.Background(), fx.user.ID); got.Name != "User" {
			t.Fatalf("unauthenticated call changed the name to %q", got.Name)
		}
	})

	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport+"/anonymous", func(t *testing.T) {
			fx := newAdminMCPFixture(t, "anonymous", false)
			resp, out := callTodoUpdateMCP(t, newStatelessClient(fx.ts), fx.ts.URL, transport, "admin_updateUser", map[string]any{"userId": 1, "name": "X"})
			assertTodoLinkMCPError(t, transport, resp, out, 403, "CAPABILITY_UNAVAILABLE", "admin_updateUser is unavailable in anonymous mode")
		})
		t.Run(transport+"/non-owners", func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			const missingID = 999999
			for name, client := range map[string]*http.Client{"admin": fx.adminClient, "user": fx.userClient} {
				// The owner check precedes the target read, so a non-owner learns nothing about
				// whether a user id or email exists.
				for _, input := range []map[string]any{
					{"userId": fx.owner.ID, "name": "Hijacked", "email": "hijack@example.com"},
					{"userId": missingID, "name": "X"},
					{"userId": fx.user.ID, "email": fx.admin.Email},
				} {
					resp, out := callTodoUpdateMCP(t, client, fx.ts.URL, transport, "admin_updateUser", input)
					assertTodoLinkMCPError(t, transport, resp, out, 403, "FORBIDDEN", "forbidden")
				}
				if got, _ := fx.store.GetUser(context.Background(), fx.owner.ID); got.Name != "Owner" || got.Email != "owner@example.com" {
					t.Fatalf("%s changed the owner: %+v", name, got)
				}
			}
		})
		t.Run(transport+"/owner-projection", func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			resp, out := callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_updateUser", map[string]any{"userId": fx.user.ID, "name": "Renamed", "email": "Renamed@Example.com"})
			data := adminMCPData(t, transport, resp, out)
			user := data["user"].(map[string]any)
			if got, want := sortedMapKeys(user), []string{"createdAt", "email", "isBootstrap", "isService", "name", "systemRole", "userId"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("user projection keys=%v want=%v", got, want)
			}
			if user["name"] != "Renamed" || user["email"] != "renamed@example.com" || user["systemRole"] != "user" {
				t.Fatalf("user projection=%+v", user)
			}
		})
		t.Run(transport+"/committed-mutation-survives-post-read-failure", func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			fx.spy.getUserErrByID[fx.user.ID] = errors.New("forced admin_updateUser post-read failure")
			resp, out := callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_updateUser", map[string]any{"userId": fx.user.ID, "name": "Committed"})
			publicErr := assertTodoLinkMCPError(t, transport, resp, out, 500, "INTERNAL", "internal error")
			assertEmptyTodoLinkMCPDetails(t, publicErr)
			delete(fx.spy.getUserErrByID, fx.user.ID)
			if got, err := fx.store.GetUser(context.Background(), fx.user.ID); err != nil || got.Name != "Committed" {
				t.Fatalf("committed mutation missing: user=%+v err=%v", got, err)
			}
			// Re-sending the same request is safe and now returns the projection.
			resp, out = callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_updateUser", map[string]any{"userId": fx.user.ID, "name": "Committed"})
			if user := adminMCPData(t, transport, resp, out)["user"].(map[string]any); user["name"] != "Committed" {
				t.Fatalf("retry projection=%+v", user)
			}
		})
	}
}
