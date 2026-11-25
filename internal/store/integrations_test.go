package store

import (
	"context"
	"errors"
	"testing"
)

func TestIntegrationFoundationRolesAndLifecycle(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "integrations-owner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "Integrations")
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := st.CreateProject(ownerCtx, "Separate Project")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := st.CreateUser(ctx, "integrations-viewer@example.com", "password123", "Viewer")
	if err != nil {
		t.Fatal(err)
	}
	maintainer, err := st.CreateUser(ctx, "integrations-maintainer@example.com", "password123", "Maintainer")
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := st.CreateUser(ctx, "integrations-stranger@example.com", "password123", "Stranger")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectMember(ownerCtx, owner.ID, project.ID, viewer.ID, RoleViewer); err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectMember(ownerCtx, owner.ID, project.ID, maintainer.ID, RoleMaintainer); err != nil {
		t.Fatal(err)
	}

	input := CreateIntegrationConnectionInput{Provider: "github", ExternalAccountID: "345678", Name: "Example organization"}
	if _, err := st.CreateIntegrationConnection(ctx, viewer.ID, input); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("non-admin connection create: %v", err)
	}
	conn, err := st.CreateIntegrationConnection(ctx, owner.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if conn.Provider != "github" || conn.Status != "active" || conn.ID <= 0 {
		t.Fatalf("bad connection: %+v", conn)
	}
	if _, err := st.CreateIntegrationConnection(ctx, owner.ID, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate connection: %v", err)
	}
	if err := st.UpdateIntegrationConnection(ctx, viewer.ID, conn.ID, "Renamed", "disabled"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("viewer updated connection: %v", err)
	}
	if err := st.UpdateIntegrationConnection(ctx, owner.ID, conn.ID, "Renamed", "disabled"); err != nil {
		t.Fatal(err)
	}
	connections, err := st.ListIntegrationConnections(ctx, owner.ID)
	if err != nil || len(connections) != 1 || connections[0].Status != "disabled" || connections[0].Name != "Renamed" {
		t.Fatalf("connection update/list: %+v: %v", connections, err)
	}
	if _, err := st.ListIntegrationConnections(ctx, stranger.ID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("connection inventory leaked: %v", err)
	}

	resourceInput := RegisterIntegrationResourceInput{Kind: "repository", ExternalID: "987654", Name: "example/project", URL: "https://github.com/example/project"}
	if _, err := st.RegisterIntegrationResource(ctx, viewer.ID, conn.ID, resourceInput); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("viewer registered resource: %v", err)
	}
	resource, err := st.RegisterIntegrationResource(ctx, owner.ID, conn.ID, resourceInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RegisterIntegrationResource(ctx, owner.ID, conn.ID, resourceInput); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate resource: %v", err)
	}
	if err := st.AttachIntegrationResource(ctx, viewer.ID, project.ID, resource.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer attached: %v", err)
	}
	if err := st.AttachIntegrationResource(ctx, stranger.ID, project.ID, resource.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member attached: %v", err)
	}
	if err := st.AttachIntegrationResource(ctx, maintainer.ID, otherProject.ID, resource.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project attach: %v", err)
	}
	if err := st.AttachIntegrationResource(ctx, maintainer.ID, project.ID, resource.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.AttachIntegrationResource(ctx, maintainer.ID, project.ID, resource.ID); err != nil {
		t.Fatalf("idempotent attach: %v", err)
	}
	linked, err := st.ListProjectIntegrationResources(ctx, viewer.ID, project.ID)
	if err != nil || len(linked) != 1 || linked[0].ID != resource.ID {
		t.Fatalf("viewer list: %+v: %v", linked, err)
	}
	if _, err := st.ListProjectIntegrationResources(ctx, stranger.ID, project.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member project list: %v", err)
	}
	if err := st.UpdateIntegrationResource(ctx, owner.ID, resource.ID, "example/renamed", "https://github.com/example/renamed"); err != nil {
		t.Fatal(err)
	}
	resources, err := st.ListIntegrationResources(ctx, owner.ID, conn.ID)
	if err != nil || len(resources) != 1 || resources[0].Name != "example/renamed" {
		t.Fatalf("resource update/list: %+v: %v", resources, err)
	}
	if err := st.DetachIntegrationResource(ctx, viewer.ID, project.ID, resource.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer detach: %v", err)
	}
	if err := st.DetachIntegrationResource(ctx, maintainer.ID, project.ID, resource.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DetachIntegrationResource(ctx, maintainer.ID, project.ID, resource.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("duplicate detach: %v", err)
	}
	if err := st.AttachIntegrationResource(ctx, owner.ID, project.ID, resource.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteIntegrationConnection(ctx, owner.ID, conn.ID); err != nil {
		t.Fatal(err)
	}
	resources, err = st.ListIntegrationResources(ctx, owner.ID, conn.ID)
	if err != nil || len(resources) != 0 {
		t.Fatalf("resource cascade: %+v: %v", resources, err)
	}
	linked, err = st.ListProjectIntegrationResources(ctx, viewer.ID, project.ID)
	if err != nil || len(linked) != 0 {
		t.Fatalf("association cascade: %+v: %v", linked, err)
	}
	if err := st.DeleteIntegrationResource(ctx, owner.ID, resource.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted resource still exists: %v", err)
	}
}

func TestIntegrationFoundationValidatesNamesAndURLs(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "integration-validation@example.com", "password123", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []CreateIntegrationConnectionInput{
		{Provider: "bad provider", ExternalAccountID: "id", Name: "name"},
		{Provider: "github", ExternalAccountID: "", Name: "name"},
		{Provider: "github", ExternalAccountID: "id", Name: ""},
	} {
		if _, err := st.CreateIntegrationConnection(ctx, owner.ID, in); !errors.Is(err, ErrValidation) {
			t.Fatalf("invalid connection %+v accepted: %v", in, err)
		}
	}
	conn, err := st.CreateIntegrationConnection(ctx, owner.ID, CreateIntegrationConnectionInput{Provider: "github", ExternalAccountID: "a", Name: "A"})
	if err != nil {
		t.Fatal(err)
	}
	for _, rawURL := range []string{"javascript:alert(1)", "file:///tmp/file", "https://user:secret@example.com/repo", "https://example.com/repo#fragment", "http://"} {
		_, err := st.RegisterIntegrationResource(ctx, owner.ID, conn.ID, RegisterIntegrationResourceInput{Kind: "repository", ExternalID: "1", Name: "repo", URL: rawURL})
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("invalid URL %q accepted: %v", rawURL, err)
		}
	}
	if _, err := st.RegisterIntegrationResource(ctx, owner.ID, 123456, RegisterIntegrationResourceInput{Kind: "repository", ExternalID: "1", Name: "repo", URL: "https://github.com/example/repo"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing connection: %v", err)
	}
}
