package mcp_test

import (
	"context"
	"net/http"
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
