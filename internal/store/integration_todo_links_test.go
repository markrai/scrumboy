package store

import (
	"context"
	"errors"
	"testing"
)

func TestIntegrationTodoLinksAuthorizationAndCleanup(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "card-link-owner@example.com", "password123", "Owner")
	if err != nil { t.Fatal(err) }
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "Card links")
	if err != nil { t.Fatal(err) }
	otherProject, err := st.CreateProject(ownerCtx, "Other card links")
	if err != nil { t.Fatal(err) }
	todo, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "Linked card"}, ModeFull)
	if err != nil { t.Fatal(err) }
	contributor, err := st.CreateUser(ctx, "card-link-contributor@example.com", "password123", "Contributor")
	if err != nil { t.Fatal(err) }
	viewer, err := st.CreateUser(ctx, "card-link-viewer@example.com", "password123", "Viewer")
	if err != nil { t.Fatal(err) }
	stranger, err := st.CreateUser(ctx, "card-link-stranger@example.com", "password123", "Stranger")
	if err != nil { t.Fatal(err) }
	for _, member := range []struct{id int64; role ProjectRole}{{contributor.ID, RoleContributor}, {viewer.ID, RoleViewer}} {
		if err := st.AddProjectMember(ownerCtx, owner.ID, project.ID, member.id, member.role); err != nil { t.Fatal(err) }
	}
	connection, err := st.CreateIntegrationConnection(ctx, owner.ID, CreateIntegrationConnectionInput{Provider:"github",ExternalAccountID:"app-1",Name:"GitHub"})
	if err != nil { t.Fatal(err) }
	resource, err := st.RegisterIntegrationResource(ctx, owner.ID, connection.ID, RegisterIntegrationResourceInput{Kind:"repository",ExternalID:"123",Name:"test/repo",URL:"https://github.com/test/repo"})
	if err != nil { t.Fatal(err) }
	if err := st.LinkTodoIntegrationResource(ctx, contributor.ID, project.ID, todo.LocalID, resource.ID); !errors.Is(err, ErrNotFound) { t.Fatalf("unassociated resource: %v",err) }
	if err := st.AttachIntegrationResource(ctx, owner.ID, otherProject.ID, resource.ID); err != nil { t.Fatal(err) }
	if err := st.LinkTodoIntegrationResource(ctx, contributor.ID, project.ID, todo.LocalID, resource.ID); !errors.Is(err, ErrNotFound) { t.Fatalf("cross-project resource: %v",err) }
	if err := st.AttachIntegrationResource(ctx, owner.ID, project.ID, resource.ID); err != nil { t.Fatal(err) }
	if err := st.LinkTodoIntegrationResource(ctx, viewer.ID, project.ID, todo.LocalID, resource.ID); !errors.Is(err, ErrForbidden) { t.Fatalf("viewer linked: %v",err) }
	if err := st.LinkTodoIntegrationResource(ctx, stranger.ID, project.ID, todo.LocalID, resource.ID); !errors.Is(err, ErrNotFound) { t.Fatalf("stranger linked: %v",err) }
	if err := st.LinkTodoIntegrationResource(ctx, contributor.ID, project.ID, todo.LocalID, resource.ID); err != nil { t.Fatal(err) }
	if err := st.LinkTodoIntegrationResource(ctx, contributor.ID, project.ID, todo.LocalID, resource.ID); err != nil { t.Fatalf("idempotent link: %v", err) }
	links, err := st.ListTodoIntegrationResources(ctx, viewer.ID, project.ID, todo.LocalID)
	if err != nil || len(links) != 1 || links[0].ID != resource.ID { t.Fatalf("viewer list: %+v %v",links,err) }
	if _, err := st.ListTodoIntegrationResources(ctx, stranger.ID, project.ID, todo.LocalID); !errors.Is(err, ErrNotFound) { t.Fatalf("stranger read: %v",err) }
	if _, err := st.ListTodoIntegrationResources(ctx, owner.ID, project.ID, 99999); !errors.Is(err, ErrNotFound) { t.Fatalf("missing card: %v",err) }
	if err := st.UnlinkTodoIntegrationResource(ctx, viewer.ID, project.ID, todo.LocalID, resource.ID); !errors.Is(err, ErrForbidden) { t.Fatalf("viewer unlinked: %v",err) }
	if err := st.DetachIntegrationResource(ctx, owner.ID, project.ID, resource.ID); err != nil { t.Fatal(err) }
	links, err = st.ListTodoIntegrationResources(ctx, owner.ID, project.ID, todo.LocalID)
	if err != nil || len(links) != 0 { t.Fatalf("detached resource left link: %+v %v",links,err) }
	if err := st.AttachIntegrationResource(ctx, owner.ID, project.ID, resource.ID); err != nil { t.Fatal(err) }
	if err := st.LinkTodoIntegrationResource(ctx, owner.ID, project.ID, todo.LocalID, resource.ID); err != nil { t.Fatal(err) }
	if err := st.UnlinkTodoIntegrationResource(ctx, contributor.ID, project.ID, todo.LocalID, resource.ID); err != nil { t.Fatal(err) }
	if err := st.UnlinkTodoIntegrationResource(ctx, contributor.ID, project.ID, todo.LocalID, resource.ID); !errors.Is(err, ErrNotFound) { t.Fatalf("repeat unlink: %v",err) }
	if err := st.DeleteIntegrationConnection(ctx, owner.ID, connection.ID); err != nil { t.Fatal(err) }
	links, err = st.ListTodoIntegrationResources(ctx, owner.ID, project.ID, todo.LocalID)
	if err != nil || len(links) != 0 { t.Fatalf("connection cascade: %+v %v",links,err) }
}

func TestIntegrationCardDeletionCascadesExternalLinks(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "card-delete-external@example.com", "password123", "Owner")
	if err != nil { t.Fatal(err) }
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "Card cascade")
	if err != nil { t.Fatal(err) }
	todo, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title:"Delete card"}, ModeFull)
	if err != nil { t.Fatal(err) }
	connection, err := st.CreateIntegrationConnection(ctx, owner.ID, CreateIntegrationConnectionInput{Provider:"github",ExternalAccountID:"delete-test",Name:"GitHub"})
	if err != nil { t.Fatal(err) }
	resource, err := st.RegisterIntegrationResource(ctx, owner.ID, connection.ID, RegisterIntegrationResourceInput{Kind:"repository",ExternalID:"123",Name:"org/repo",URL:"https://github.com/org/repo"})
	if err != nil { t.Fatal(err) }
	if err := st.AttachIntegrationResource(ctx, owner.ID, project.ID, resource.ID); err != nil { t.Fatal(err) }
	if err := st.LinkTodoIntegrationResource(ctx, owner.ID, project.ID, todo.LocalID, resource.ID); err != nil { t.Fatal(err) }
	if err := st.DeleteTodoByLocalID(ownerCtx, project.ID, todo.LocalID, ModeFull); err != nil { t.Fatal(err) }
	var count int
	if err := st.db.QueryRowContext(ctx,"SELECT COUNT(*) FROM integration_todo_resources WHERE todo_id = ?",todo.ID).Scan(&count); err != nil { t.Fatal(err) }
	if count != 0 { t.Fatalf("deleted card left %d external links",count) }
}
