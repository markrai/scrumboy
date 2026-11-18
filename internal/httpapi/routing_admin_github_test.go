package httpapi

import (
 "context"
 "errors"
 "net/http"
 "strconv"
 "testing"

 "scrumboy/internal/integration"
 "scrumboy/internal/integration/githubapp"
 "scrumboy/internal/store"
)

type githubFakeProvider struct {
 installs []integration.Installation
 resources []integration.Resource
 unavailable bool
 invalidated int64
}

func (f *githubFakeProvider) Installations(ctx context.Context)([]integration.Installation,error){
 return f.installs,nil
}
func (f *githubFakeProvider) Resources(ctx context.Context,id int64)([]integration.Resource,error){
 if f.unavailable {return nil,githubapp.ErrRevoked}
 return f.resources,nil
}
func (f *githubFakeProvider) Invalidate(id string){
	n,_:=strconv.ParseInt(id,10,64)
	f.invalidated=n
}
func (f *githubFakeProvider) Revoke(ctx context.Context,id string) error {
	f.Invalidate(id)
	return nil
}

func TestRESTGitHubAppInstallationAdminLifecycle(t *testing.T){
 provider:=&githubFakeProvider{
  installs:[]integration.Installation{{ID:"987",Account:"org",Kind:"Organization"}},
  resources:[]integration.Resource{{ID:"123",Name:"org/repo",URL:"https://github.com/org/repo"}},
 }
 ts,db,cleanup:=newTestHTTPServerWithOptions(t,Options{ScrumboyMode:"full",GitHubProvider:provider,GitHubAppSlug:"scrumboy-example"})
 defer cleanup()
 ownerClient:=newCookieClient(t)
 owner:=bootstrapUserClient(t,ownerClient,ts.URL,"Owner","github-admin@example.com","password123")
 ownerID:=int64(owner["id"].(float64))
 st:=store.New(db,nil)
 ctx:=context.Background()
 project,err:=st.CreateProject(store.WithUserID(ctx,ownerID),"GitHub REST")
 if err!=nil{t.Fatal(err)}
 todo,err:=st.CreateTodo(store.WithUserID(ctx,ownerID),project.ID,store.CreateTodoInput{Title:"Linked card"},store.ModeFull)
 if err!=nil{t.Fatal(err)}
 viewer,err:=st.CreateUser(ctx,"github-viewer@example.com","password123","Viewer")
 if err!=nil{t.Fatal(err)}
 if err:=st.AddProjectMember(store.WithUserID(ctx,ownerID),ownerID,project.ID,viewer.ID,store.RoleViewer);err!=nil{t.Fatal(err)}
 viewerClient:=newCookieClient(t)
 loginUserClient(t,viewerClient,ts.URL,"github-viewer@example.com","password123")

 base:=ts.URL+"/api/admin/integrations/github"
 resp,body:=doJSON(t,viewerClient,http.MethodGet,base,nil,nil)
 if resp.StatusCode!=http.StatusForbidden{t.Fatalf("viewer admin access: %d %s",resp.StatusCode,body)}
 var status struct{Configured bool `json:"configured"`;InstallURL string `json:"installUrl"`;Connections []githubConnectionJSON `json:"connections"`}
 resp,body=doJSON(t,ownerClient,http.MethodGet,base,nil,&status)
 if resp.StatusCode!=http.StatusOK || !status.Configured || status.InstallURL!="https://github.com/apps/scrumboy-example/installations/new" {
  t.Fatalf("status: %d %s %+v",resp.StatusCode,body,status)
 }
 var installs []integration.Installation
 resp,body=doJSON(t,ownerClient,http.MethodGet,base+"/installations",nil,&installs)
 if resp.StatusCode!=http.StatusOK || len(installs)!=1 || installs[0].ID!="987" {t.Fatalf("installations: %d %s",resp.StatusCode,body)}
 var synced struct{ConnectionID int64 `json:"connectionId"`;RepositoryCount int `json:"repositoryCount"`}
 resp,body=doJSON(t,ownerClient,http.MethodPost,base+"/installations/654/sync",nil,nil)
 if resp.StatusCode!=http.StatusNotFound{t.Fatalf("unknown installation: %d %s",resp.StatusCode,body)}
 resp,body=doJSON(t,ownerClient,http.MethodPost,base+"/installations/987/sync",nil,&synced)
 if resp.StatusCode!=http.StatusOK || synced.ConnectionID<=0 || synced.RepositoryCount!=1 {
  t.Fatalf("sync: %d %s %+v",resp.StatusCode,body,synced)
 }
 var resources []integrationResourceResponse
 connPath:=base+"/connections/"+strconv.FormatInt(synced.ConnectionID,10)
 resp,body=doJSON(t,ownerClient,http.MethodGet,connPath+"/resources",nil,&resources)
 if resp.StatusCode!=http.StatusOK || len(resources)!=1 {t.Fatalf("resources: %d %s %+v",resp.StatusCode,body,resources)}
 resourceID:=resources[0].ID
 projectURL:=ts.URL+"/api/board/"+project.Slug+"/integrations/resources"
 resp,body=doJSON(t,viewerClient,http.MethodPost,projectURL,map[string]any{"resourceId":resourceID},nil)
 if resp.StatusCode!=http.StatusForbidden{t.Fatalf("viewer associated resource: %d %s",resp.StatusCode,body)}
 resp,body=doJSON(t,ownerClient,http.MethodPost,projectURL,map[string]any{"resourceId":resourceID},nil)
 if resp.StatusCode!=http.StatusNoContent{t.Fatalf("attach resource: %d %s",resp.StatusCode,body)}
 cardURL:=ts.URL+"/api/board/"+project.Slug+"/todos/"+strconv.FormatInt(todo.LocalID,10)+"/integrations"
 resp,body=doJSON(t,ownerClient,http.MethodPost,cardURL,map[string]any{"resourceId":resourceID},nil)
 if resp.StatusCode!=http.StatusNoContent{t.Fatalf("link resource: %d %s",resp.StatusCode,body)}
 provider.unavailable=true
 resp,body=doJSON(t,ownerClient,http.MethodPost,base+"/installations/987/sync",nil,nil)
 if resp.StatusCode!=http.StatusConflict || !errors.Is(githubapp.ErrRevoked,githubapp.ErrRevoked) {t.Fatalf("revoked sync: %d %s",resp.StatusCode,body)}
 var links []integrationResourceResponse
 resp,body=doJSON(t,ownerClient,http.MethodGet,cardURL,nil,&links)
 if resp.StatusCode!=http.StatusOK || len(links)!=1 {t.Fatalf("failed sync removed card link: %d %s",resp.StatusCode,body)}
 resp,body=doJSON(t,ownerClient,http.MethodDelete,connPath,nil,nil)
 if resp.StatusCode!=http.StatusNoContent || provider.invalidated!=987 {t.Fatalf("disconnect: %d %s invalidated %d",resp.StatusCode,body,provider.invalidated)}
 resp,body=doJSON(t,ownerClient,http.MethodGet,cardURL,nil,&links)
 if resp.StatusCode!=http.StatusOK || len(links)!=0 {t.Fatalf("disconnected resources left card links: %d %s",resp.StatusCode,body)}
}
