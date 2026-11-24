package githubwebhook

import (
    "bytes"
    "context"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "errors"
    "io"
    "net/http"
    "net/http/httptest"
    "testing"

    "scrumboy/internal/integration"
    "scrumboy/internal/store"
)

var sampleSecret=[]byte("github-webhook-test-secret-key-at-least-32-bytes-long")

type webhookInboxFake struct {
    Inbox
    events []store.GitHubInboundEvent
    admissionError error
    connected bool
    reconciled []integration.Resource
    disabled string
}

func (f *webhookInboxFake) AdmitGitHubInbound(ctx context.Context,event store.GitHubInboundEvent) (bool,error) {
    if f.admissionError!=nil{return false,f.admissionError}
    f.events=append(f.events,event)
    return true,nil
}
func (f *webhookInboxFake) GitHubInboundInstallationConnected(context.Context,string) (bool,error) {
    return f.connected,nil
}
func (f *webhookInboxFake) ReconcileGitHubInboundRepositories(ctx context.Context,id string,resources []integration.Resource) error {
    f.reconciled=append([]integration.Resource(nil),resources...)
    return nil
}
func (f *webhookInboxFake) DisableGitHubInboundInstallation(ctx context.Context,id string) error {
    f.disabled=id
    return nil
}

type webhookProviderFake struct {
    installs []integration.Installation
    resources []integration.Resource
    err error
}
func (p *webhookProviderFake) Installations(context.Context) ([]integration.Installation,error) {
    if p.err!=nil{return nil,p.err}
    return p.installs,nil
}
func (p *webhookProviderFake) Resources(context.Context,string) ([]integration.Resource,error) {
    if p.err!=nil{return nil,p.err}
    return p.resources,nil
}
func (p *webhookProviderFake) Invalidate(string) {}
func (p *webhookProviderFake) Revoke(context.Context,string) error {return nil}

func signedRequest(body []byte,event,delivery string) *http.Request {
    req:=httptest.NewRequest(http.MethodPost,"/api/integrations/github/webhook",bytes.NewReader(body))
    req.Header.Set("Content-Type","application/json")
    req.Header.Set("X-GitHub-Event",event)
    req.Header.Set("X-GitHub-Delivery",delivery)
    mac:=hmac.New(sha256.New,sampleSecret)
    _,_=mac.Write(body)
    req.Header.Set("X-Hub-Signature-256","sha256="+hex.EncodeToString(mac.Sum(nil)))
    return req
}

func TestGitHubWebhookHMACBeforeParsingAndStrictBodyLimit(t *testing.T) {
    inbox:=&webhookInboxFake{}
    provider:=&webhookProviderFake{}
    svc,err:=New(sampleSecret,inbox,provider,nil)
    if err!=nil{t.Fatal(err)}
    body:=[]byte(`{"action":"renamed","installation":{"id":123},"repository":{"name":"demo"}}`)
    delivery:="b61e607d-2a44-4d9a-8e4f-109e055cfc2d"
    good:=signedRequest(body,"repository",delivery)
    response:=httptest.NewRecorder()
    svc.ServeHTTP(response,good)
    if response.Code!=http.StatusAccepted || len(inbox.events)!=1{t.Fatalf("signed delivery %d, %+v",response.Code,inbox.events)}
    if inbox.events[0].InstallationID!="123" || inbox.events[0].EventType!="repository"{t.Fatalf("unexpected metadata %+v",inbox.events[0])}

    tampered:=signedRequest([]byte(`{"action":"renamed","installation":{"id":123}}`),"repository",delivery)
    tampered.Body=io.NopCloser(bytes.NewReader([]byte(`{"action":"deleted","installation":{"id":123}}`)))
    response=httptest.NewRecorder()
    svc.ServeHTTP(response,tampered)
    if response.Code!=http.StatusUnauthorized || len(inbox.events)!=1 {t.Fatalf("tampered body accepted: %d",response.Code)}

    invalid:=signedRequest([]byte("{not json"),"repository",delivery)
    response=httptest.NewRecorder()
    svc.ServeHTTP(response,invalid)
    if response.Code!=http.StatusBadRequest || len(inbox.events)!=1{t.Fatalf("malformed JSON accepted: %d",response.Code)}

    huge:=signedRequest(bytes.Repeat([]byte("a"),maxGitHubPayload+1),"repository",delivery)
    response=httptest.NewRecorder()
    svc.ServeHTTP(response,huge)
    if response.Code!=http.StatusRequestEntityTooLarge || len(inbox.events)!=1{t.Fatalf("oversized delivery accepted: %d",response.Code)}

    missing:=signedRequest(body,"repository",delivery)
    missing.Header.Del("X-Hub-Signature-256")
    response=httptest.NewRecorder()
    svc.ServeHTTP(response,missing)
    if response.Code!=http.StatusUnauthorized{t.Fatalf("missing signature %d",response.Code)}

    ping:=signedRequest([]byte("{}"),"ping",delivery)
    response=httptest.NewRecorder()
    svc.ServeHTTP(response,ping)
    if response.Code!=http.StatusAccepted || len(inbox.events)!=1{t.Fatalf("ping should not enqueue: %d",response.Code)}
}

func TestGitHubWebhookAdmissionFailuresAndDisabledMode(t *testing.T) {
    if _,err:=New([]byte("short"),&webhookInboxFake{},&webhookProviderFake{},nil);err==nil{t.Fatal("short webhook secret accepted")}
    inbox:=&webhookInboxFake{admissionError:store.ErrNotFound}
    svc,err:=New(sampleSecret,inbox,&webhookProviderFake{},nil)
    if err!=nil{t.Fatal(err)}
    req:=signedRequest([]byte(`{"action":"added","installation":{"id":999}}`),"installation_repositories","b61e607d-2a44-4d9a-8e4f-109e055cfc2d")
    response:=httptest.NewRecorder()
    svc.ServeHTTP(response,req)
    if response.Code!=http.StatusAccepted || len(inbox.events)!=0{t.Fatalf("unknown installation: %d",response.Code)}
    inbox.admissionError=store.ErrGitHubInboundFull
    response=httptest.NewRecorder()
    svc.ServeHTTP(response,signedRequest([]byte(`{"action":"added","installation":{"id":999}}`),"installation_repositories","b61e607d-2a44-4d9a-8e4f-109e055cfc2d"))
    if response.Code!=http.StatusServiceUnavailable{t.Fatalf("full inbox: %d",response.Code)}
    inbox.admissionError=store.ErrConflict
    response=httptest.NewRecorder()
    svc.ServeHTTP(response,signedRequest([]byte(`{"action":"added","installation":{"id":999}}`),"installation_repositories","b61e607d-2a44-4d9a-8e4f-109e055cfc2d"))
    if response.Code!=http.StatusConflict{t.Fatalf("duplicate collision: %d",response.Code)}
    response=httptest.NewRecorder()
    svc.ServeHTTP(response,httptest.NewRequest(http.MethodGet,"/api/integrations/github/webhook",nil))
    if response.Code!=http.StatusMethodNotAllowed{t.Fatalf("GET status: %d",response.Code)}
}

func TestGitHubWebhookProcessingChecksOwnershipAndPreservesReferences(t *testing.T) {
    inbox:=&webhookInboxFake{connected:true}
    provider:=&webhookProviderFake{
        installs:[]integration.Installation{{ID:"123",Account:"owner",Kind:"User"}},
        resources:[]integration.Resource{{ID:"40",Name:"owner/repo",URL:"https://github.com/owner/repo"}},
    }
    svc,err:=New(sampleSecret,inbox,provider,nil)
    if err!=nil{t.Fatal(err)}
    event:=store.GitHubInboundEvent{InstallationID:"123",EventType:"repository",Action:"renamed"}
    if err:=svc.processEvent(context.Background(),event);err!=nil{t.Fatal(err)}
    if len(inbox.reconciled)!=1||inbox.reconciled[0].ID!="40"{t.Fatalf("no reconciliation %+v",inbox.reconciled)}
    provider.installs=[]integration.Installation{{ID:"999",Account:"other"}}
    if err:=svc.processEvent(context.Background(),event);err==nil{t.Fatal("foreign installation accepted")}
    provider.err=errors.New("provider outage")
    if err:=svc.processEvent(context.Background(),event);err==nil{t.Fatal("provider outage accepted")}
    provider.err=nil
    event.EventType="installation"
    event.Action="suspend"
    if err:=svc.processEvent(context.Background(),event);err!=nil{t.Fatal(err)}
    if inbox.disabled!="123"{t.Fatalf("suspend did not disable installation: %q",inbox.disabled)}
    inbox.connected=false
    inbox.disabled=""
    event.Action="deleted"
    if err:=svc.processEvent(context.Background(),event);err!=nil || inbox.disabled!="" {t.Fatalf("disconnected installation processed: %v",err)}
}

