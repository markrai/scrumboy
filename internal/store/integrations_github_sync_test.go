package store

import (
 "context"
 "errors"
 "testing"

 "scrumboy/internal/integration"
)

func TestGitHubInstallSyncResourceChurnAndIsolation(t *testing.T) {
 st,cleanup:=newTestStore(t)
 defer cleanup()
 ctx:=context.Background()
 owner,err:=st.BootstrapUser(ctx,"github-sync-owner@example.com","password123","Owner")
 if err!=nil{t.Fatal(err)}
 viewer,err:=st.CreateUser(ctx,"github-sync-viewer@example.com","password123","Viewer")
 if err!=nil{t.Fatal(err)}
 project,err:=st.CreateProject(WithUserID(ctx,owner.ID),"GitHub Sync")
 if err!=nil{t.Fatal(err)}
 todo,err:=st.CreateTodo(WithUserID(ctx,owner.ID),project.ID,CreateTodoInput{Title:"Card"},ModeFull)
 if err!=nil{t.Fatal(err)}
 inst:=integration.Installation{ID:"123",Account:"test-org",Kind:"Organization"}
 repo:=integration.Resource{ID:"100",Name:"test-org/repo",URL:"https://github.com/test-org/repo"}
 if _,err:=st.SyncGitHubInstallation(ctx,viewer.ID,inst,[]integration.Resource{repo});!errors.Is(err,ErrUnauthorized){t.Fatalf("viewer sync %v",err)}
 conn,err:=st.SyncGitHubInstallation(ctx,owner.ID,inst,[]integration.Resource{repo})
 if err!=nil{t.Fatal(err)}
 resources,err:=st.ListIntegrationResources(ctx,owner.ID,conn.ID)
 if err!=nil || len(resources)!=1{t.Fatalf("resources %+v %v",resources,err)}
 resourceID:=resources[0].ID
 if err:=st.AttachIntegrationResource(ctx,owner.ID,project.ID,resourceID);err!=nil{t.Fatal(err)}
 if err:=st.LinkTodoIntegrationResource(ctx,owner.ID,project.ID,todo.LocalID,resourceID);err!=nil{t.Fatal(err)}
 repo.Name="test-org/renamed"
 repo.URL="https://github.com/test-org/renamed"
 same,err:=st.SyncGitHubInstallation(ctx,owner.ID,inst,[]integration.Resource{repo})
 if err!=nil || same.ID!=conn.ID{t.Fatalf("resync %+v %v",same,err)}
 resources,err=st.ListIntegrationResources(ctx,owner.ID,conn.ID)
 if err!=nil || len(resources)!=1 || resources[0].ID!=resourceID || resources[0].Name!=repo.Name {t.Fatalf("rename %+v %v",resources,err)}
 if _,err:=st.SyncGitHubInstallation(ctx,owner.ID,inst,[]integration.Resource{repo,repo});!errors.Is(err,ErrValidation){t.Fatalf("duplicate accepted %v",err)}
 malicious:=integration.Resource{ID:"99",Name:"evil/repo",URL:"https://evil.example/repo"}
 if _,err:=st.SyncGitHubInstallation(ctx,owner.ID,inst,[]integration.Resource{malicious});!errors.Is(err,ErrValidation){t.Fatalf("URL accepted %v",err)}
 links,err:=st.ListTodoIntegrationResources(ctx,owner.ID,project.ID,todo.LocalID)
 if err!=nil || len(links)!=1{t.Fatalf("failed sync removed link %+v %v",links,err)}
 if _,err:=st.SyncGitHubInstallation(ctx,owner.ID,inst,nil);err!=nil{t.Fatal(err)}
 links,err=st.ListTodoIntegrationResources(ctx,owner.ID,project.ID,todo.LocalID)
 if err!=nil || len(links)!=1 || links[0].ID!=resourceID || links[0].Availability!="unavailable"{t.Fatalf("revoked repo lost link %+v %v",links,err)}
 if err:=st.AttachIntegrationResource(ctx,owner.ID,project.ID,resourceID);!errors.Is(err,ErrNotFound){t.Fatalf("unavailable repo was attached: %v",err)}
 if err:=st.LinkTodoIntegrationResource(ctx,owner.ID,project.ID,todo.LocalID,resourceID);!errors.Is(err,ErrNotFound){t.Fatalf("unavailable repo was linked: %v",err)}
 restored,err:=st.SyncGitHubInstallation(ctx,owner.ID,inst,[]integration.Resource{repo})
 if err!=nil || restored.ID!=conn.ID{t.Fatalf("restored sync %+v %v",restored,err)}
 links,err=st.ListTodoIntegrationResources(ctx,owner.ID,project.ID,todo.LocalID)
 if err!=nil || len(links)!=1 || links[0].ID!=resourceID || links[0].Availability!="available" {t.Fatalf("restored repo lost links %+v %v",links,err)}
 impact,err:=st.GetGitHubDisconnectImpact(ctx,owner.ID,conn.ID)
 if err!=nil || impact.Repositories!=1 || impact.ProjectAssociations!=1 || impact.CardReferences!=1{t.Fatalf("disconnect impact %+v %v",impact,err)}
 if err:=st.DeleteGitHubInstallation(ctx,viewer.ID,conn.ID,impact);!errors.Is(err,ErrUnauthorized){t.Fatalf("viewer deleted %v",err)}
 if err:=st.DeleteGitHubInstallation(ctx,owner.ID,conn.ID,GitHubDisconnectImpact{});!errors.Is(err,ErrConflict){t.Fatalf("stale impact accepted %v",err)}
 if err:=st.DeleteGitHubInstallation(ctx,owner.ID,conn.ID,impact);err!=nil{t.Fatal(err)}
}
