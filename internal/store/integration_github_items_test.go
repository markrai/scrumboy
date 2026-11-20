package store

import (
    "context"
    "errors"
    "testing"

    "scrumboy/internal/integration"
)

func TestGitHubItemProjectBoundariesAndRetention(t *testing.T) {
    st,cleanup:=newTestStore(t)
    defer cleanup()
    ctx:=context.Background()
    owner,err:=st.BootstrapUser(ctx,"items-owner@example.com","password123","Owner")
    if err!=nil{t.Fatal(err)}
    viewer,err:=st.CreateUser(ctx,"items-viewer@example.com","password123","Viewer")
    if err!=nil{t.Fatal(err)}
    outsider,err:=st.CreateUser(ctx,"items-outsider@example.com","password123","Other")
    if err!=nil{t.Fatal(err)}
    project,err:=st.CreateProject(WithUserID(ctx,owner.ID),"GitHub Item Project")
    if err!=nil{t.Fatal(err)}
    card,err:=st.CreateTodo(WithUserID(ctx,owner.ID),project.ID,CreateTodoInput{Title:"Unchanged"},ModeFull)
    if err!=nil{t.Fatal(err)}
    if err:=st.AddProjectMember(WithUserID(ctx,owner.ID),owner.ID,project.ID,viewer.ID,RoleViewer);err!=nil{t.Fatal(err)}
    inst:=integration.Installation{ID:"987",Account:"org",Kind:"Organization"}
    repo:=integration.Resource{ID:"123",Name:"org/repo",URL:"https://github.com/org/repo"}
    conn,err:=st.SyncGitHubInstallation(ctx,owner.ID,inst,[]integration.Resource{repo})
    if err!=nil{t.Fatal(err)}
    resources,err:=st.ListIntegrationResources(ctx,owner.ID,conn.ID)
    if err!=nil||len(resources)!=1{t.Fatalf("resources %+v %v",resources,err)}
    resourceID:=resources[0].ID
    if err:=st.AttachIntegrationResource(ctx,owner.ID,project.ID,resourceID);err!=nil{t.Fatal(err)}
    if _,err:=st.GetGitHubProjectRepository(ctx,outsider.ID,project.ID,resourceID,false);err==nil{t.Fatal("nonmember gained repo access")}
    if _,err:=st.GetGitHubProjectRepository(ctx,viewer.ID,project.ID,resourceID,true);!errors.Is(err,ErrForbidden){t.Fatalf("viewer write access: %v",err)}
    item:=integration.GitHubItem{ResourceID:resourceID,Kind:"pr",Number:19,Title:"Review",State:"open",URL:"https://github.com/org/repo/pull/19"}
    if err:=st.LinkTodoGitHubItem(ctx,viewer.ID,project.ID,card.LocalID,item);!errors.Is(err,ErrForbidden){t.Fatalf("viewer linked: %v",err)}
    if err:=st.LinkTodoGitHubItem(ctx,owner.ID,project.ID,card.LocalID,item);err!=nil{t.Fatal(err)}
    if err:=st.LinkTodoGitHubItem(ctx,owner.ID,project.ID,card.LocalID,item);err!=nil{t.Fatal(err)}
    links,err:=st.ListTodoGitHubItems(ctx,viewer.ID,project.ID,card.LocalID)
    if err!=nil || len(links)!=1 || links[0].Kind!="pr" || links[0].Repository!="org/repo" {t.Fatalf("links %+v %v",links,err)}
    if _,err:=st.ListTodoGitHubItems(ctx,outsider.ID,project.ID,card.LocalID);err==nil{t.Fatal("nonmember read private links")}
    if _,err:=st.SyncGitHubInstallation(ctx,owner.ID,inst,nil);err!=nil{t.Fatal(err)}
    links,err=st.ListTodoGitHubItems(ctx,owner.ID,project.ID,card.LocalID)
    if err!=nil || len(links)!=1 || links[0].Availability!="unavailable"{t.Fatalf("access loss deleted item %+v %v",links,err)}
    if _,err:=st.GetGitHubProjectRepository(ctx,owner.ID,project.ID,resourceID,true);!errors.Is(err,ErrNotFound){t.Fatalf("unavailable repo accessible %v",err)}
    if _,err:=st.SyncGitHubInstallation(ctx,owner.ID,inst,[]integration.Resource{repo});err!=nil{t.Fatal(err)}
    item.State="closed"
    item.Merged=true
    if err:=st.RefreshTodoGitHubItem(ctx,owner.ID,project.ID,card.LocalID,item);err!=nil{t.Fatal(err)}
    links,err=st.ListTodoGitHubItems(ctx,owner.ID,project.ID,card.LocalID)
    if err!=nil || len(links)!=1 || !links[0].Merged || links[0].State!="closed"{t.Fatalf("refresh %+v %v",links,err)}
    if err:=st.UnlinkTodoGitHubItem(ctx,viewer.ID,project.ID,card.LocalID,resourceID,"pr",19);!errors.Is(err,ErrForbidden){t.Fatalf("viewer removed: %v",err)}
    if err:=st.DetachIntegrationResource(ctx,owner.ID,project.ID,resourceID);err!=nil{t.Fatal(err)}
    links,err=st.ListTodoGitHubItems(ctx,owner.ID,project.ID,card.LocalID)
    if err!=nil || len(links)!=0{t.Fatalf("detach left items %+v %v",links,err)}
}
