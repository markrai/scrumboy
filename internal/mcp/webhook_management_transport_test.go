package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"scrumboy/internal/store"
)

func TestMCPWebhookManagementAcrossTransports(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			serviceAccount, err := fx.store.CreateServiceAccount(context.Background(), fx.owner.ID, "Webhook Bot")
			if err != nil {
				t.Fatal(err)
			}
			if err := fx.store.AddProjectMember(context.Background(), fx.owner.ID, fx.project.ID, serviceAccount.ID, store.RoleViewer); err != nil {
				t.Fatal(err)
			}
			const signingSecret = "transport-only-signing-secret"
			resp, out := callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "webhooks_create", map[string]any{
				"projectSlug": fx.project.Slug, "url": "https://example.com/delegated", "events": []string{"todo.created"},
				"secret": signingSecret, "serviceAccountUserId": serviceAccount.ID,
			})
			data := serviceAdminMCPData(t, transport, resp, out)
			webhook := data["webhook"].(map[string]any)
			webhookID := int64(webhook["webhookId"].(float64))
			if int64(webhook["ownerUserId"].(float64)) != serviceAccount.ID || webhook["ownerIsService"] != true || int64(webhook["registeredByUserId"].(float64)) != fx.owner.ID {
				t.Fatal("delegated ownership projection is incorrect")
			}
			serialized, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(serialized), signingSecret) || strings.Contains(string(serialized), `"secret"`) {
				t.Fatal("webhook creation response exposed signing material")
			}

			resp, out = callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "webhooks_create", map[string]any{
				"projectSlug": fx.project.Slug, "url": "https://example.com/self", "events": []string{"*"},
			})
			serviceAdminMCPData(t, transport, resp, out)

			resp, out = callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "webhooks_list", map[string]any{
				"projectSlug": fx.project.Slug, "limit": 1,
			})
			data = serviceAdminMCPData(t, transport, resp, out)
			if len(data["items"].([]any)) != 1 {
				t.Fatal("paginated webhook list returned the wrong item count")
			}
			if transport == "jsonrpc" {
				if data["hasMore"] != true || data["nextCursor"] == nil {
					t.Fatal("JSON-RPC webhook pagination metadata is missing")
				}
			} else {
				meta := out["meta"].(map[string]any)
				if meta["hasMore"] != true || meta["nextCursor"] == nil {
					t.Fatal("legacy webhook pagination metadata is missing")
				}
			}
			serialized, err = json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(serialized), signingSecret) || strings.Contains(string(serialized), `"secret"`) {
				t.Fatal("webhook listing exposed signing material")
			}

			resp, out = callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "webhooks_delete", map[string]any{
				"projectSlug": fx.project.Slug, "webhookId": webhookID,
			})
			data = serviceAdminMCPData(t, transport, resp, out)
			if data["status"] != "deleted" {
				t.Fatal("webhook deletion did not return deleted status")
			}
			resp, out = callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "webhooks_delete", map[string]any{
				"projectSlug": fx.project.Slug, "webhookId": webhookID,
			})
			assertTodoLinkMCPError(t, transport, resp, out, http.StatusNotFound, "NOT_FOUND", "not found")
		})
	}
}

func TestMCPWebhookAuthorizationValidationAndNoSystemRoleBypass(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			serviceAccount, err := fx.store.CreateServiceAccount(context.Background(), fx.owner.ID, "Webhook Bot")
			if err != nil {
				t.Fatal(err)
			}
			if err := fx.store.AddProjectMember(context.Background(), fx.owner.ID, fx.project.ID, serviceAccount.ID, store.RoleViewer); err != nil {
				t.Fatal(err)
			}

			for _, client := range []*http.Client{fx.adminClient, fx.userClient} {
				resp, out := callTodoUpdateMCP(t, client, fx.ts.URL, transport, "webhooks_create", map[string]any{
					"projectSlug": fx.project.Slug, "url": "https://example.com/denied", "events": []string{"*"},
				})
				assertTodoLinkMCPError(t, transport, resp, out, http.StatusNotFound, "NOT_FOUND", "not found")
			}

			if err := fx.store.AddProjectMember(context.Background(), fx.owner.ID, fx.project.ID, fx.user.ID, store.RoleViewer); err != nil {
				t.Fatal(err)
			}
			resp, out := callTodoUpdateMCP(t, fx.userClient, fx.ts.URL, transport, "webhooks_create", map[string]any{
				"projectSlug": fx.project.Slug, "url": "https://example.com/denied", "events": []string{"*"},
			})
			assertTodoLinkMCPError(t, transport, resp, out, http.StatusForbidden, "FORBIDDEN", "forbidden")

			resp, out = callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "webhooks_create", map[string]any{
				"projectSlug": fx.project.Slug, "url": "https://example.com/human", "events": []string{"*"}, "serviceAccountUserId": fx.user.ID,
			})
			assertTodoLinkMCPError(t, transport, resp, out, http.StatusForbidden, "FORBIDDEN", "forbidden")

			resp, out = callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "webhooks_create", map[string]any{
				"projectSlug": fx.project.Slug, "url": "https://example.com/forged", "events": []string{"*"},
				"ownerUserId": serviceAccount.ID, "registeredByUserId": fx.owner.ID, "isService": true,
			})
			assertTodoLinkMCPError(t, transport, resp, out, http.StatusBadRequest, "VALIDATION_ERROR", "invalid input")
		})
	}
}

func TestMCPWebhookCreateUsesSharedProjectLimit(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			for index := 0; index < 20; index++ {
				if _, err := fx.store.CreateWebhook(store.WithUserID(context.Background(), fx.owner.ID), fx.owner.ID, store.CreateWebhookInput{
					ProjectID: fx.project.ID, URL: "https://example.com/existing/" + strconv.Itoa(index), Events: []string{"*"},
				}); err != nil {
					t.Fatal(err)
				}
			}
			resp, out := callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "webhooks_create", map[string]any{
				"projectSlug": fx.project.Slug, "url": "https://example.com/limit", "events": []string{"*"},
			})
			assertTodoLinkMCPError(t, transport, resp, out, http.StatusBadRequest, "VALIDATION_ERROR", "validation: maximum webhooks per project reached")
		})
	}
}

func TestMCPWebhookOwnerCanManageOwnSubscriptionAfterRemoval(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			serviceAccount, err := fx.store.CreateServiceAccount(context.Background(), fx.owner.ID, "Removed Webhook Bot")
			if err != nil {
				t.Fatal(err)
			}
			_, token, _, err := fx.store.CreateServiceAccountToken(context.Background(), fx.owner.ID, serviceAccount.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := fx.store.AddProjectMember(context.Background(), fx.owner.ID, fx.project.ID, serviceAccount.ID, store.RoleViewer); err != nil {
				t.Fatal(err)
			}
			hook, err := fx.store.CreateWebhook(store.WithUserID(context.Background(), fx.owner.ID), fx.owner.ID, store.CreateWebhookInput{
				ProjectID: fx.project.ID, URL: "https://example.com/removed-owner", Events: []string{"*"}, ServiceOwnerUserID: &serviceAccount.ID,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := fx.store.RemoveProjectMember(context.Background(), fx.owner.ID, fx.project.ID, serviceAccount.ID); err != nil {
				t.Fatal(err)
			}

			serviceClient := bearerClientForMCP(t, fx.ts, token)
			resp, out := callTodoUpdateMCP(t, serviceClient, fx.ts.URL, transport, "webhooks_list", map[string]any{"projectSlug": fx.project.Slug})
			data := serviceAdminMCPData(t, transport, resp, out)
			items := data["items"].([]any)
			if len(items) != 1 || int64(items[0].(map[string]any)["webhookId"].(float64)) != hook.ID {
				t.Fatal("removed subscription owner could not inspect its own webhook")
			}

			resp, out = callTodoUpdateMCP(t, serviceClient, fx.ts.URL, transport, "webhooks_delete", map[string]any{
				"projectSlug": fx.project.Slug, "webhookId": hook.ID,
			})
			data = serviceAdminMCPData(t, transport, resp, out)
			if data["status"] != "deleted" {
				t.Fatal("removed subscription owner could not delete its own webhook")
			}
		})
	}
}
