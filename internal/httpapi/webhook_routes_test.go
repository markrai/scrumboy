package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"scrumboy/internal/store"
)

func TestWebhookRESTCreateListDeleteCompatibilityAndAuthoritativeAuthorization(t *testing.T) {
	ts, database, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	ownerClient := newCookieClient(t)
	ownerWire := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "webhook-rest-owner@example.com", "password123")
	ownerID := int64(ownerWire["id"].(float64))
	st := store.New(database, nil)
	project, err := st.CreateProject(store.WithUserID(context.Background(), ownerID), "Webhook REST")
	if err != nil {
		t.Fatal(err)
	}

	const secret = "rest-only-signing-secret"
	var created map[string]any
	resp, _ := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/webhooks", map[string]any{
		"projectId": project.ID, "url": "https://example.com/rest", "events": []string{"todo.created"}, "secret": secret,
	}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("REST webhook create status=%d", resp.StatusCode)
	}
	if _, present := created["secret"]; present {
		t.Fatal("REST webhook create exposed the signing secret")
	}
	webhookID := int64(created["id"].(float64))
	var listed []map[string]any
	resp, _ = doJSON(t, ownerClient, http.MethodGet, ts.URL+"/api/webhooks", nil, &listed)
	if resp.StatusCode != http.StatusOK || len(listed) != 1 || int64(listed[0]["id"].(float64)) != webhookID {
		t.Fatalf("REST webhook list status=%d items=%d", resp.StatusCode, len(listed))
	}
	if _, present := listed[0]["secret"]; present {
		t.Fatal("REST webhook list exposed the signing secret")
	}

	viewer, err := st.CreateUser(context.Background(), "webhook-rest-viewer@example.com", "password123", "Viewer")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, project.ID, viewer.ID, store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	viewerClient := newCookieClient(t)
	loginUserClient(t, viewerClient, ts.URL, viewer.Email, "password123")
	resp, _ = doJSON(t, viewerClient, http.MethodPost, ts.URL+"/api/webhooks", map[string]any{
		"projectId": project.ID, "url": "https://example.com/denied", "events": []string{"*"},
	}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer REST webhook create status=%d want=403", resp.StatusCode)
	}
	resp, _ = doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/webhooks", map[string]any{
		"projectId": project.ID, "url": "https://example.com/forged", "events": []string{"*"}, "serviceAccountUserId": viewer.ID,
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("REST accepted an added ownership field: status=%d", resp.StatusCode)
	}

	resp, _ = doJSON(t, ownerClient, http.MethodDelete, fmt.Sprintf("%s/api/webhooks/%d", ts.URL, webhookID), nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("REST webhook delete status=%d", resp.StatusCode)
	}
	resp, _ = doJSON(t, ownerClient, http.MethodDelete, fmt.Sprintf("%s/api/webhooks/%d", ts.URL, webhookID), nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("repeated REST webhook delete status=%d want=404", resp.StatusCode)
	}
}

func TestWebhookRESTUsesTransactionalProjectLimit(t *testing.T) {
	ts, database, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	client := newCookieClient(t)
	ownerWire := bootstrapUserClient(t, client, ts.URL, "Owner", "webhook-limit-owner@example.com", "password123")
	ownerID := int64(ownerWire["id"].(float64))
	st := store.New(database, nil)
	project, err := st.CreateProject(store.WithUserID(context.Background(), ownerID), "Webhook REST Limit")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 20; index++ {
		resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/webhooks", map[string]any{
			"projectId": project.ID, "url": fmt.Sprintf("https://example.com/rest/%d", index), "events": []string{"*"},
		}, nil)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("REST webhook %d status=%d", index+1, resp.StatusCode)
		}
	}
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/webhooks", map[string]any{
		"projectId": project.ID, "url": "https://example.com/rest/21", "events": []string{"*"},
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("21st REST webhook status=%d want=400", resp.StatusCode)
	}
}
