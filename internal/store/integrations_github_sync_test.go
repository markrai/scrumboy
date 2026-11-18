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
 if err!=nil || len(links)!=0{t.Fatalf("revoked repo left link %+v %v",links,err)}
 if err:=st.DeleteGitHubInstallation(ctx,viewer.ID,conn.ID);!errors.Is(err,ErrUnauthorized){t.Fatalf("viewer deleted %v",err)}
 if err:=st.DeleteGitHubInstallation(ctx,owner.ID,conn.ID);err!=nil{t.Fatal(err)}
}
