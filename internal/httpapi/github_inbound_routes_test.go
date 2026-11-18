package httpapi

import (
    "bytes"
    "context"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "net/http"
    "net/http/httptest"
    "path/filepath"
    "testing"
    "time"

    "scrumboy/internal/abusepolicy"
    "scrumboy/internal/db"
    "scrumboy/internal/integration/githubwebhook"
    "scrumboy/internal/migrate"
    "scrumboy/internal/store"
)

type routeGitHubInbox struct {
    githubwebhook.Inbox
    accepted []store.GitHubInboundEvent
}

func (f *routeGitHubInbox) AdmitGitHubInbound(ctx context.Context,event store.GitHubInboundEvent) (bool,error) {
    f.accepted=append(f.accepted,event)
    return true,nil
}
func (f *routeGitHubInbox) ClaimGitHubInbound(context.Context,time.Time,int,time.Duration) ([]store.GitHubInboundEvent,error) {
    return nil,nil
}
func (f *routeGitHubInbox) MaintainGitHubInbound(context.Context,time.Time) error {return nil}
func (f *routeGitHubInbox) GitHubInboundSummary(context.Context) (store.GitHubInboundCounts,error) {
    return store.GitHubInboundCounts{Pending:int64(len(f.accepted))},nil
}

func TestGitHubInboundRouteRequiresOptInAndValidHMAC(t *testing.T) {
    testServer,_,cleanup:=newTestHTTPServer(t,"full")
    defer cleanup()
    resp,err:=http.Post(testServer.URL+"/api/integrations/github/webhook","application/json",bytes.NewBufferString("{}"))
    if err!=nil{t.Fatal(err)}
    resp.Body.Close()
    if resp.StatusCode!=http.StatusNotFound{t.Fatalf("disabled route returned %d",resp.StatusCode)}

    rawDB,err:=db.Open(filepath.Join(t.TempDir(),"webhook.db"),db.Options{BusyTimeout:5000,JournalMode:"WAL",Synchronous:"FULL"})
    if err!=nil{t.Fatal(err)}
    defer rawDB.Close()
    if err:=migrate.Apply(context.Background(),rawDB);err!=nil{t.Fatal(err)}
    st:=store.New(rawDB,nil)
    inbox:=&routeGitHubInbox{}
    secret:=[]byte("github-test-webhook-signature-key-very-long")
    provider:=&githubFakeProvider{}
    service,err:=githubwebhook.New(secret,inbox,provider,nil)
    if err!=nil{t.Fatal(err)}
    policy:=abusepolicy.DisabledThrottling()
    srv:=NewServer(st,Options{ScrumboyMode:"full",GitHubProvider:provider,GitHubWebhook:service,AbusePolicy:&policy})
    ts:=httptest.NewServer(srv)
    defer func(){
        ts.Close()
        ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
        defer cancel()
        srv.Close(ctx)
    }()

    body:=[]byte(`{"action":"renamed","installation":{"id":321}}`)
    req,err:=http.NewRequest(http.MethodPost,ts.URL+"/api/integrations/github/webhook",bytes.NewReader(body))
    if err!=nil{t.Fatal(err)}
    req.Header.Set("Content-Type","application/json")
    req.Header.Set("X-GitHub-Event","repository")
    req.Header.Set("X-GitHub-Delivery","b61e607d-2a44-4d9a-8e4f-109e055cfc2d")
    mac:=hmac.New(sha256.New,secret)
    _,_=mac.Write(body)
    req.Header.Set("X-Hub-Signature-256","sha256="+hex.EncodeToString(mac.Sum(nil)))
    response,err:=http.DefaultClient.Do(req)
    if err!=nil{t.Fatal(err)}
    response.Body.Close()
    if response.StatusCode!=http.StatusAccepted || len(inbox.accepted)!=1{t.Fatalf("signed request without X-Scrumboy: %d %+v",response.StatusCode,inbox.accepted)}
    request,err:=http.NewRequest(http.MethodPost,ts.URL+"/api/integrations/github/webhook",bytes.NewReader(body))
    if err!=nil{t.Fatal(err)}
    request.Header.Set("Content-Type","application/json")
    response,err=http.DefaultClient.Do(request)
    if err!=nil{t.Fatal(err)}
    response.Body.Close()
    if response.StatusCode!=http.StatusUnauthorized{t.Fatalf("invalid signature: %d",response.StatusCode)}
    adminResponse,bodyBytes:=doJSON(t,newCookieClient(t),http.MethodGet,ts.URL+"/api/admin/integrations/github/webhooks",nil,nil)
    if adminResponse.StatusCode!=http.StatusUnauthorized{t.Fatalf("unauthenticated admin status %d %s",adminResponse.StatusCode,bodyBytes)}
    ownerClient:=newCookieClient(t)
    bootstrapUserClient(t,ownerClient,ts.URL,"Owner","github-webhook-admin@example.com","password123")
    var status struct {
        Configured bool `json:"configured"`
        Counts store.GitHubInboundCounts `json:"counts"`
    }
    adminResponse,bodyBytes=doJSON(t,ownerClient,http.MethodGet,ts.URL+"/api/admin/integrations/github/webhooks",nil,&status)
    if adminResponse.StatusCode!=http.StatusOK || !status.Configured || status.Counts.Pending!=1{
        t.Fatalf("admin status: %d %s %+v",adminResponse.StatusCode,bodyBytes,status)
    }
}
