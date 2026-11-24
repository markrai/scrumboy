package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"scrumboy/internal/store"
)

func TestCardExternalReferencesRESTAuthorization(t *testing.T) {
	ts, db, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "external-rest-owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	st := store.New(db, nil)
	ctx := context.Background()
	ownerCtx := store.WithUserID(ctx, ownerID)
	project, err := st.CreateProject(ownerCtx, "External REST")
	if err != nil { t.Fatal(err) }
	todo, err := st.CreateTodo(ownerCtx, project.ID, store.CreateTodoInput{Title:"External link card"}, store.ModeFull)
	if err != nil { t.Fatal(err) }
	connection, err := st.CreateIntegrationConnection(ctx, ownerID, store.CreateIntegrationConnectionInput{Provider:"github", ExternalAccountID:"rest-test", Name:"GitHub"})
	if err != nil { t.Fatal(err) }
	resource, err := st.RegisterIntegrationResource(ctx, ownerID, connection.ID, store.RegisterIntegrationResourceInput{Kind:"repository",ExternalID:"123",Name:"example/scrumboy",URL:"https://github.com/example/scrumboy"})
	if err != nil { t.Fatal(err) }
	if err := st.AttachIntegrationResource(ctx, ownerID, project.ID, resource.ID); err != nil { t.Fatal(err) }
	other, err := st.RegisterIntegrationResource(ctx, ownerID, connection.ID, store.RegisterIntegrationResourceInput{Kind:"repository",ExternalID:"456",Name:"example/private",URL:"https://github.com/example/private"})
	if err != nil { t.Fatal(err) }

	base := ts.URL + "/api/board/" + project.Slug
	card := base + "/todos/" + strconv.FormatInt(todo.LocalID,10) + "/integrations"
	var available []integrationResourceResponse
	resp, body := doJSON(t, ownerClient, http.MethodGet, base+"/integrations/resources", nil, &available)
	if resp.StatusCode != http.StatusOK || len(available) != 1 || available[0].ID != resource.ID {
		t.Fatalf("available resources: status=%d body=%s value=%+v", resp.StatusCode, body, available)
	}
	resp, body = doJSON(t, ownerClient, http.MethodPost, card, map[string]any{"resourceId":other.ID}, nil)
	if resp.StatusCode != http.StatusNotFound { t.Fatalf("unassociated resource: status=%d body=%s",resp.StatusCode,body) }
	resp, body = doJSON(t, ownerClient, http.MethodPost, card, map[string]any{"resourceId":resource.ID}, nil)
	if resp.StatusCode != http.StatusNoContent { t.Fatalf("link: status=%d body=%s",resp.StatusCode,body) }
	var linked []integrationResourceResponse
	resp, body = doJSON(t, ownerClient, http.MethodGet, card, nil, &linked)
	if resp.StatusCode != http.StatusOK || len(linked) != 1 || linked[0].URL != resource.URL {
		t.Fatalf("linked: status=%d body=%s value=%+v",resp.StatusCode,body,linked)
	}
	viewer, err := st.CreateUser(ctx,"external-rest-viewer@example.com","password123","Viewer")
	if err != nil { t.Fatal(err) }
	if err := st.AddProjectMember(ownerCtx, ownerID, project.ID, viewer.ID, store.RoleViewer); err != nil { t.Fatal(err) }
	viewerClient := newCookieClient(t)
	loginUserClient(t, viewerClient, ts.URL, "external-rest-viewer@example.com","password123")
	resp, body = doJSON(t, viewerClient, http.MethodGet, card, nil, &linked)
	if resp.StatusCode != http.StatusOK { t.Fatalf("viewer read: status=%d body=%s",resp.StatusCode,body) }
	resp, body = doJSON(t, viewerClient, http.MethodDelete, card+"/"+strconv.FormatInt(resource.ID,10), nil, nil)
	if resp.StatusCode != http.StatusForbidden { t.Fatalf("viewer delete: status=%d body=%s",resp.StatusCode,body) }
	stranger, err := st.CreateUser(ctx,"external-rest-stranger@example.com","password123","Stranger")
	if err != nil { t.Fatal(err) }
	_ = stranger
	strangerClient := newCookieClient(t)
	loginUserClient(t, strangerClient, ts.URL,"external-rest-stranger@example.com","password123")
	resp, body = doJSON(t, strangerClient, http.MethodGet, card, nil, nil)
	if resp.StatusCode != http.StatusNotFound { t.Fatalf("stranger read: status=%d body=%s",resp.StatusCode,body) }
	resp, body = doJSON(t, ownerClient, http.MethodDelete, card+"/1", nil, nil)
	if resp.StatusCode != http.StatusNoContent { t.Fatalf("unlink: status=%d body=%s",resp.StatusCode,body) }
	resp, body = doJSON(t, ownerClient, http.MethodGet, card, nil, &linked)
	if resp.StatusCode != http.StatusOK || len(linked) != 0 { t.Fatalf("empty links: status=%d body=%s %+v",resp.StatusCode,body,linked) }
	
}
