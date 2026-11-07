package mcp_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"scrumboy/internal/store"
)

type serviceAccountRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn serviceAccountRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func serviceAdminMCPData(t *testing.T, transport string, resp *http.Response, out map[string]any) map[string]any {
	t.Helper()
	if transport == "legacy" {
		if resp.StatusCode != http.StatusOK || out["ok"] != true {
			t.Fatalf("legacy success status=%d response=%+v", resp.StatusCode, out)
		}
		return out["data"].(map[string]any)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("JSON-RPC status=%d response=%+v", resp.StatusCode, out)
	}
	result := out["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("JSON-RPC tool error=%+v", result)
	}
	return result["structuredContent"].(map[string]any)
}

func TestMCPServiceAccountLifecycleAcrossTransports(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			resp, out := callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_createServiceAccount", map[string]any{"name": "Deploy Bot"})
			data := serviceAdminMCPData(t, transport, resp, out)
			account := data["account"].(map[string]any)
			serviceUserID := int64(account["userId"].(float64))
			if account["stableUserId"] == "" || account["isService"] != true || account["name"] != "Deploy Bot" {
				t.Fatalf("created account = %+v", account)
			}

			resp, out = callTodoUpdateMCP(t, fx.adminClient, fx.ts.URL, transport, "admin_createServiceAccount", map[string]any{"name": "Admin Bot"})
			serviceAdminMCPData(t, transport, resp, out)
			resp, out = callTodoUpdateMCP(t, fx.adminClient, fx.ts.URL, transport, "admin_listServiceAccounts", map[string]any{"limit": 1})
			data = serviceAdminMCPData(t, transport, resp, out)
			if items := data["items"].([]any); len(items) != 1 || items[0].(map[string]any)["isService"] != true {
				t.Fatalf("service account list = %+v", data)
			}
			if transport == "jsonrpc" {
				if _, ok := data["hasMore"].(bool); !ok {
					t.Fatalf("JSON-RPC list metadata = %+v", data)
				}
			} else if meta := out["meta"].(map[string]any); meta["hasMore"] != true || meta["nextCursor"] == nil {
				t.Fatalf("legacy list metadata = %+v", meta)
			}
			resp, out = callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_listServiceAccounts", map[string]any{})
			data = serviceAdminMCPData(t, transport, resp, out)
			if len(data["items"].([]any)) != 2 {
				t.Fatalf("owner account list = %+v", data)
			}

			resp, out = callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_createServiceToken", map[string]any{"userId": serviceUserID, "name": "primary"})
			data = serviceAdminMCPData(t, transport, resp, out)
			created := data["token"].(map[string]any)
			tokenID := int64(created["tokenId"].(float64))
			plaintext := created["token"].(string)
			if !strings.HasPrefix(plaintext, store.APITokenPrefix) {
				t.Fatalf("created token = %+v", created)
			}
			serialized, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(serialized), plaintext) != 1 {
				t.Fatalf("plaintext occurrence count = %d response=%s", strings.Count(string(serialized), plaintext), serialized)
			}
			serviceClient := bearerClientForMCP(t, fx.ts, plaintext)
			resp, out = callTodoUpdateMCP(t, serviceClient, fx.ts.URL, transport, "me_get", map[string]any{})
			data = serviceAdminMCPData(t, transport, resp, out)
			if data["isService"] != true || data["canCreateProjects"] != false || int64(data["userId"].(float64)) != serviceUserID {
				t.Fatalf("service me projection = %+v", data)
			}
			resp, out = callTodoUpdateMCP(t, serviceClient, fx.ts.URL, transport, "projects_list", map[string]any{})
			data = serviceAdminMCPData(t, transport, resp, out)
			if len(data["items"].([]any)) != 0 {
				t.Fatalf("unassigned service projects = %+v", data)
			}
			resp, out = callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "members_listAvailable", map[string]any{"projectSlug": fx.project.Slug})
			data = serviceAdminMCPData(t, transport, resp, out)
			availableService := false
			for _, raw := range data["items"].([]any) {
				item := raw.(map[string]any)
				if int64(item["userId"].(float64)) == serviceUserID {
					availableService = item["isService"] == true
				}
			}
			if !availableService {
				t.Fatalf("available member service projection = %+v", data)
			}
			resp, out = callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "members_add", map[string]any{"projectSlug": fx.project.Slug, "userId": serviceUserID, "role": "viewer"})
			serviceAdminMCPData(t, transport, resp, out)
			resp, out = callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "members_list", map[string]any{"projectSlug": fx.project.Slug})
			data = serviceAdminMCPData(t, transport, resp, out)
			listedService := false
			for _, raw := range data["items"].([]any) {
				item := raw.(map[string]any)
				if int64(item["userId"].(float64)) == serviceUserID {
					listedService = item["isService"] == true && item["role"] == "viewer"
				}
			}
			if !listedService {
				t.Fatalf("project member service projection = %+v", data)
			}
			resp, out = callTodoUpdateMCP(t, serviceClient, fx.ts.URL, transport, "projects_list", map[string]any{})
			data = serviceAdminMCPData(t, transport, resp, out)
			if items := data["items"].([]any); len(items) != 1 || items[0].(map[string]any)["role"] != "viewer" {
				t.Fatalf("assigned service projects = %+v", data)
			}
			resp, out = callTodoUpdateMCP(t, serviceClient, fx.ts.URL, transport, "projects_create", map[string]any{"name": "Denied"})
			assertTodoLinkMCPError(t, transport, resp, out, http.StatusForbidden, "FORBIDDEN", "forbidden")

			resp, out = callTodoUpdateMCP(t, fx.adminClient, fx.ts.URL, transport, "admin_listServiceTokens", map[string]any{"userId": serviceUserID})
			data = serviceAdminMCPData(t, transport, resp, out)
			items := data["items"].([]any)
			if len(items) != 1 || int64(items[0].(map[string]any)["tokenId"].(float64)) != tokenID {
				t.Fatalf("token list = %+v", data)
			}
			sum := sha256.Sum256([]byte(plaintext))
			listBytes, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(listBytes), plaintext) || strings.Contains(string(listBytes), hex.EncodeToString(sum[:])) {
				t.Fatalf("token list leaked credential material: %s", listBytes)
			}
			resp, out = callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_listServiceTokens", map[string]any{"userId": serviceUserID})
			data = serviceAdminMCPData(t, transport, resp, out)
			if len(data["items"].([]any)) != 1 {
				t.Fatalf("owner token list = %+v", data)
			}

			resp, out = callTodoUpdateMCP(t, fx.adminClient, fx.ts.URL, transport, "admin_revokeServiceToken", map[string]any{"tokenId": tokenID})
			serviceAdminMCPData(t, transport, resp, out)
			resp, out = callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_revokeServiceToken", map[string]any{"tokenId": tokenID})
			serviceAdminMCPData(t, transport, resp, out)
			resp, _ = postMCPWithBearer(t, newStatelessClient(fx.ts), fx.ts.URL, plaintext, map[string]any{"tool": "me_get", "input": map[string]any{}})
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("revoked token status=%d, want 401", resp.StatusCode)
			}

			resp, out = callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_deleteServiceAccount", map[string]any{"userId": serviceUserID})
			data = serviceAdminMCPData(t, transport, resp, out)
			if data["status"] != "deleted" {
				t.Fatalf("delete result = %+v", data)
			}
		})
	}
}

func TestMCPServiceAccountAuthorizationAndHumanCredentialProtection(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			serviceAccount, err := fx.store.CreateServiceAccount(context.Background(), fx.owner.ID, "Protected Bot")
			if err != nil {
				t.Fatal(err)
			}
			tokenID, servicePlaintext, _, err := fx.store.CreateServiceAccountToken(context.Background(), fx.owner.ID, serviceAccount.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			humanTokenID, humanPlaintext, _, err := fx.store.CreateUserAPIToken(context.Background(), fx.user.ID, nil, true)
			if err != nil {
				t.Fatal(err)
			}

			for _, tc := range []struct {
				name   string
				client *http.Client
				tool   string
				input  map[string]any
			}{
				{"admin mint", fx.adminClient, "admin_createServiceToken", map[string]any{"userId": serviceAccount.ID}},
				{"admin delete", fx.adminClient, "admin_deleteServiceAccount", map[string]any{"userId": serviceAccount.ID}},
				{"human create", fx.userClient, "admin_createServiceAccount", map[string]any{"name": "forged"}},
				{"human list", fx.userClient, "admin_listServiceAccounts", map[string]any{}},
				{"human delete", fx.userClient, "admin_deleteServiceAccount", map[string]any{"userId": serviceAccount.ID}},
				{"human mint", fx.userClient, "admin_createServiceToken", map[string]any{"userId": serviceAccount.ID}},
				{"human list tokens", fx.userClient, "admin_listServiceTokens", map[string]any{"userId": serviceAccount.ID}},
				{"human revoke", fx.userClient, "admin_revokeServiceToken", map[string]any{"tokenId": tokenID}},
			} {
				resp, out := callTodoUpdateMCP(t, tc.client, fx.ts.URL, transport, tc.tool, tc.input)
				assertTodoLinkMCPError(t, transport, resp, out, http.StatusForbidden, "FORBIDDEN", "forbidden")
			}
			resp, out := callTodoUpdateMCP(t, fx.userClient, fx.ts.URL, transport, "admin_createServiceAccount", map[string]any{
				"name": "forged", "isService": true, "userId": fx.owner.ID, "systemRole": "owner",
			})
			assertTodoLinkMCPError(t, transport, resp, out, http.StatusBadRequest, "VALIDATION_ERROR", "invalid input")

			serviceClient := newStatelessClient(fx.ts)
			for tool, input := range map[string]map[string]any{
				"admin_createServiceAccount": {"name": "forged"},
				"admin_listServiceAccounts":  {},
				"admin_deleteServiceAccount": {"userId": serviceAccount.ID},
				"admin_createServiceToken":   {"userId": serviceAccount.ID},
				"admin_listServiceTokens":    {"userId": serviceAccount.ID},
				"admin_revokeServiceToken":   {"tokenId": tokenID},
			} {
				if transport == "legacy" {
					resp, out := postMCPWithBearer(t, serviceClient, fx.ts.URL, servicePlaintext, map[string]any{"tool": tool, "input": input})
					assertTodoLinkMCPError(t, transport, resp, out, http.StatusForbidden, "FORBIDDEN", "forbidden")
				} else {
					requestClient := bearerClientForMCP(t, fx.ts, servicePlaintext)
					resp, out := callTodoUpdateMCP(t, requestClient, fx.ts.URL, transport, tool, input)
					assertTodoLinkMCPError(t, transport, resp, out, http.StatusForbidden, "FORBIDDEN", "forbidden")
				}
			}

			for _, tc := range []struct {
				tool  string
				input map[string]any
			}{
				{"admin_deleteServiceAccount", map[string]any{"userId": fx.user.ID}},
				{"admin_listServiceTokens", map[string]any{"userId": fx.user.ID}},
				{"admin_revokeServiceToken", map[string]any{"tokenId": humanTokenID}},
			} {
				resp, out := callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, tc.tool, tc.input)
				assertTodoLinkMCPError(t, transport, resp, out, http.StatusNotFound, "NOT_FOUND", "not found")
			}
			if _, err := fx.store.GetUserByAPIToken(context.Background(), humanPlaintext); err != nil {
				t.Fatalf("human automation token changed: %v", err)
			}
		})
	}
}

func bearerClientForMCP(t *testing.T, ts interface{ Client() *http.Client }, token string) *http.Client {
	t.Helper()
	base := ts.Client()
	return &http.Client{Transport: serviceAccountRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		clone.Header = req.Header.Clone()
		clone.Header.Set("Authorization", "Bearer "+token)
		return base.Transport.RoundTrip(clone)
	})}
}

func TestMCPServiceTokenRevocationBypassesDeletionConflictAcrossTransports(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			serviceAccount, err := fx.store.CreateServiceAccount(context.Background(), fx.owner.ID, "Sole Maintainer")
			if err != nil {
				t.Fatal(err)
			}
			tokenID, plaintext, _, err := fx.store.CreateServiceAccountToken(context.Background(), fx.owner.ID, serviceAccount.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := fx.store.AddProjectMember(store.WithUserID(context.Background(), fx.owner.ID), fx.owner.ID, fx.project.ID, serviceAccount.ID, store.RoleMaintainer); err != nil {
				t.Fatal(err)
			}
			if _, err := fx.db.Exec(`DELETE FROM project_members WHERE project_id = ? AND user_id <> ?`, fx.project.ID, serviceAccount.ID); err != nil {
				t.Fatal(err)
			}
			resp, out := callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_deleteServiceAccount", map[string]any{"userId": serviceAccount.ID})
			publicErr := assertTodoLinkMCPError(t, transport, resp, out, http.StatusConflict, "CONFLICT", "conflict: account is the sole maintainer of 1 project(s); another maintainer must be added or the project deleted first")
			encoded, _ := json.Marshal(publicErr)
			if strings.Contains(string(encoded), fx.project.Name) || strings.Contains(string(encoded), fx.project.Slug) {
				t.Fatalf("conflict disclosed project: %s", encoded)
			}
			resp, out = callTodoUpdateMCP(t, fx.adminClient, fx.ts.URL, transport, "admin_revokeServiceToken", map[string]any{"tokenId": tokenID})
			serviceAdminMCPData(t, transport, resp, out)
			if _, err := fx.store.GetUserByAPIToken(context.Background(), plaintext); err == nil {
				t.Fatal("revoked compromised token still authenticates")
			}
		})
	}
}
