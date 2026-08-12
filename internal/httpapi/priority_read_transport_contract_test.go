package httpapi

import (
	"context"
	"net/http"
	"testing"

	"scrumboy/internal/store"
)

func TestPriorityListRESTContract(t *testing.T) {
	fx := newPriorityRESTFixture(t, "priority-rest-list")

	viewer, err := fx.st.CreateUser(context.Background(), "priority-list-viewer@example.com", "password123", "Viewer")
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	if err := fx.st.AddProjectMember(fx.ctx, fx.ownerID, fx.project.ID, viewer.ID, store.RoleViewer); err != nil {
		t.Fatalf("add viewer: %v", err)
	}
	viewerClient := newCookieClient(t)
	loginUserClient(t, viewerClient, fx.ts.URL, viewer.Email, "password123")

	var body struct {
		Items []priorityTierJSON `json:"items"`
	}
	resp, raw := doJSON(t, viewerClient, http.MethodGet, fx.ts.URL+"/api/board/"+fx.project.Slug+"/priorities", nil, &body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("viewer list status=%d body=%s", resp.StatusCode, raw)
	}
	wantKeys := []string{"low", "medium", "high", "urgent"}
	if len(body.Items) != len(wantKeys) {
		t.Fatalf("items=%+v", body.Items)
	}
	for i, item := range body.Items {
		if item.Key != wantKeys[i] || item.Position != i || item.Name == "" || item.Color == "" {
			t.Fatalf("item[%d]=%+v", i, item)
		}
	}

	outsider, err := fx.st.CreateUser(context.Background(), "priority-list-outsider@example.com", "password123", "Outsider")
	if err != nil {
		t.Fatalf("create outsider: %v", err)
	}
	outsiderClient := newCookieClient(t)
	loginUserClient(t, outsiderClient, fx.ts.URL, outsider.Email, "password123")
	resp, raw = doJSON(t, outsiderClient, http.MethodGet, fx.ts.URL+"/api/board/"+fx.project.Slug+"/priorities", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("outsider status=%d body=%s", resp.StatusCode, raw)
	}

	resp, raw = doJSON(t, fx.client, http.MethodGet, fx.ts.URL+"/api/board/missing-priority-board/priorities", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing status=%d body=%s", resp.StatusCode, raw)
	}
}

func TestPriorityListRESTContractTemporaryBoard(t *testing.T) {
	fx := newPriorityRESTFixture(t, "priority-rest-list-temporary-host")
	temporary, err := fx.st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("create temporary board: %v", err)
	}
	client := newCookieClient(t)
	var body struct {
		Items []priorityTierJSON `json:"items"`
	}
	resp, raw := doJSON(t, client, http.MethodGet, fx.ts.URL+"/api/board/"+temporary.Slug+"/priorities", nil, &body)
	if resp.StatusCode != http.StatusOK || len(body.Items) != 4 {
		t.Fatalf("temporary list status=%d body=%s items=%+v", resp.StatusCode, raw, body.Items)
	}
}
