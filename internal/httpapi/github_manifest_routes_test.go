package httpapi

import (
 "context"
 "io"
 "net/http"
 "net/url"
 "strings"
 "testing"

 "scrumboy/internal/integration/githubmanifest"
 "scrumboy/internal/store"
)

type githubManifestTransport func(*http.Request)(*http.Response,error)
func (f githubManifestTransport)RoundTrip(r *http.Request)(*http.Response,error){return f(r)}

func TestGitHubManifestOwnerOnlyEnrollmentAndCallback(t *testing.T){
 calls:=0
 client:=&http.Client{Transport:githubManifestTransport(func(r *http.Request)(*http.Response,error){
  calls++
  return &http.Response{StatusCode:422,Body:io.NopCloser(strings.NewReader("private-key-not-for-user")),Header:http.Header{}},nil
 })}
 manifest:=githubmanifest.New(client,func(context.Context,githubmanifest.Credentials)error{return nil})
 ts,db,cleanup:=newTestHTTPServerWithOptions(t,Options{ScrumboyMode:"full",PublicBaseURL:"https://instance.example",GitHubManifest:manifest})
 defer cleanup()
 ownerClient:=newCookieClient(t)
 bootstrapUserClient(t,ownerClient,ts.URL,"Owner","github-manifest-owner@example.com","password123")
 viewer,err:=store.New(db,nil).CreateUser(context.Background(),"github-manifest-viewer@example.com","password123","Viewer")
 if err!=nil||viewer.ID<=0{t.Fatal(err)}
 viewerClient:=newCookieClient(t)
 loginUserClient(t,viewerClient,ts.URL,"github-manifest-viewer@example.com","password123")
 start:=ts.URL+"/api/admin/integrations/github/manifest/start"
 resp,body:=doJSON(t,viewerClient,http.MethodPost,start,nil,nil)
 if resp.StatusCode!=http.StatusForbidden{t.Fatalf("viewer started enrollment %d %s",resp.StatusCode,body)}
 var reg githubmanifest.Registration
 resp,body=doJSON(t,ownerClient,http.MethodPost,start,nil,&reg)
 if resp.StatusCode!=http.StatusOK || reg.Manifest=="" || !strings.HasPrefix(reg.Action,"https://github.com/settings/apps/new?state="){t.Fatalf("owner enrollment %d %s",resp.StatusCode,body)}
 u,err:=url.Parse(reg.Action)
 if err!=nil{t.Fatal(err)}
 callback:=ts.URL+"/api/integrations/github/manifest/callback?state="+u.Query().Get("state")+"&code=1234567890abcdef"
 response,err:=viewerClient.Get(callback)
 if err!=nil{t.Fatal(err)}
 response.Body.Close()
 if response.StatusCode!=http.StatusForbidden||calls!=0{t.Fatalf("viewer callback %d, conversion calls %d",response.StatusCode,calls)}
 response,err=ownerClient.Get(callback)
 if err!=nil{t.Fatal(err)}
 raw,_:=io.ReadAll(response.Body)
 response.Body.Close()
 if response.StatusCode!=http.StatusBadGateway||calls!=1 || strings.Contains(string(raw),"private-key-not-for-user"){t.Fatalf("failed callback exposed details: %d",response.StatusCode)}
 if response.Header.Get("Cache-Control")!="no-store"||response.Header.Get("Referrer-Policy")!="no-referrer"{t.Fatal("callback response cache or referrer policy unsafe")}
 response,err=ownerClient.Get(callback)
 if err!=nil{t.Fatal(err)}
 response.Body.Close()
 if response.StatusCode!=http.StatusBadRequest || calls!=1{t.Fatalf("replay accepted %d calls %d",response.StatusCode,calls)}
}

func TestGitHubManifestDisabledUntilSafeCredentialSink(t *testing.T){
 manifest:=githubmanifest.New(nil,nil)
 ts,_,cleanup:=newTestHTTPServerWithOptions(t,Options{ScrumboyMode:"full",GitHubManifest:manifest,PublicBaseURL:"https://instance.example"})
 defer cleanup()
 ownerClient:=newCookieClient(t)
 bootstrapUserClient(t,ownerClient,ts.URL,"Owner","github-manifest-disabled@example.com","password123")
 resp,body:=doJSON(t,ownerClient,http.MethodPost,ts.URL+"/api/admin/integrations/github/manifest/start",nil,nil)
 if resp.StatusCode!=http.StatusServiceUnavailable{t.Fatalf("unwired credential persistence accepted %d %s",resp.StatusCode,body)}
 resp2,err:=ownerClient.Get(ts.URL+"/api/integrations/github/manifest/callback?state=bogus&code=1234567890abcdef")
 if err!=nil{t.Fatal(err)}
 resp2.Body.Close()
 if resp2.StatusCode!=http.StatusNotFound{t.Fatalf("disabled callback %d",resp2.StatusCode)}
}
