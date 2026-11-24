package githubwebhook

import (
    "context"
    "errors"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
    "time"

    "scrumboy/internal/store"
)

type automationInboxFake struct {
    *webhookInboxFake
    admitted []store.GitHubAutomationEvent
    admitErr error
    claims []store.GitHubAutomationEvent
    matchCount int
    matchErr error
    retryCount int
}

func (f *automationInboxFake) AdmitGitHubAutomation(ctx context.Context,item store.GitHubAutomationEvent)(bool,error){
    if f.admitErr!=nil{return false,f.admitErr}
    f.admitted=append(f.admitted,item)
    return true,nil
}

func (f *automationInboxFake) ClaimGitHubAutomation(context.Context,time.Time,int,time.Duration)([]store.GitHubAutomationEvent,error){
    items:=f.claims
    f.claims=nil
    return items,nil
}

func (f *automationInboxFake) MatchAndCompleteGitHubAutomation(context.Context,int64,string,time.Time)(int,error){
    f.matchCount++
    if f.matchErr!=nil{return 0,f.matchErr}
    return 1,nil
}

func (f *automationInboxFake) FailGitHubAutomation(context.Context,int64,string,time.Time)(bool,error){
    f.retryCount++
    return true,nil
}

func (f *automationInboxFake) GitHubAutomationSummary(context.Context)(store.GitHubAutomationCounts,error){
    return store.GitHubAutomationCounts{Pending:int64(len(f.admitted))},nil
}

func (f *automationInboxFake) MaintainGitHubAutomation(context.Context,time.Time)error{return nil}

func TestVerifiedWebhookMapsIssueAndPullRequestEventsWithoutExecution(t *testing.T){
    cases:=[]struct{
        name,kind,body,event string
        supported bool
    }{
        {"issue opened","issues",`{"action":"opened","installation":{"id":881},"repository":{"id":442},"issue":{"number":30}}`,"issue.opened",true},
        {"issue closed","issues",`{"action":"closed","installation":{"id":881},"repository":{"id":442},"issue":{"number":30}}`,"issue.closed",true},
        {"PR opened","pull_request",`{"action":"opened","installation":{"id":881},"repository":{"id":442},"number":31,"pull_request":{"number":31,"merged":false}}`,"pull_request.opened",true},
        {"PR closed without merge","pull_request",`{"action":"closed","installation":{"id":881},"repository":{"id":442},"number":31,"pull_request":{"number":31,"merged":false}}`,"pull_request.closed",true},
        {"PR merged","pull_request",`{"action":"closed","installation":{"id":881},"repository":{"id":442},"number":31,"pull_request":{"number":31,"merged":true}}`,"pull_request.merged",true},
        {"ignored issue update","issues",`{"action":"labeled","installation":{"id":881},"repository":{"id":442},"issue":{"number":30}}`,"",false},
        {"ignored PR update","pull_request",`{"action":"synchronize","installation":{"id":881},"repository":{"id":442},"number":31,"pull_request":{"number":31}}`,"",false},
    }
    for _,tc:=range cases {
        t.Run(tc.name,func(t *testing.T){
            inbox:=&automationInboxFake{webhookInboxFake:&webhookInboxFake{}}
            svc,err:=New(sampleSecret,inbox,&webhookProviderFake{},nil)
            if err!=nil{t.Fatal(err)}
            request:=signedRequest([]byte(tc.body),tc.kind,"b61e607d-2a44-4d9a-8e4f-109e055cfc2d")
            response:=httptest.NewRecorder()
            svc.ServeHTTP(response,request)
            if response.Code!=http.StatusAccepted{t.Fatalf("webhook status %d %s",response.Code,response.Body.String())}
            want:=0
            if tc.supported{want=1}
            if len(inbox.admitted)!=want{t.Fatalf("unexpected inbox %+v",inbox.admitted)}
            if tc.supported{
                event:=inbox.admitted[0]
                number:=int64(30)
                if tc.kind=="pull_request"{number=31}
                if event.EventType!=tc.event || event.InstallationID!="881" ||
                    event.RepositoryExternalID!="442" || event.ItemNumber!=number ||
                    len(event.PayloadSHA256)!=64 || event.DeliveryID==""{
                    t.Fatalf("incorrect normalized event %+v",event)
                }
                if strings.Contains(event.PayloadSHA256,"secret"){t.Fatal("payload text stored in digest")}
            }
        })
    }
}

func TestVerifiedAutomationWebhookRejectsForgedOrIncompleteEvents(t *testing.T){
    invalid:=[]struct{kind,body string}{
        {"issues",`{"action":"opened","installation":{"id":881},"repository":{"id":0},"issue":{"number":30}}`},
        {"issues",`{"action":"opened","installation":{"id":881},"repository":{"id":442}}`},
        {"issues",`{"action":"opened","installation":{"id":881},"repository":{"id":442},"issue":{"number":2147483648}}`},
        {"pull_request",`{"action":"closed","installation":{"id":881},"repository":{"id":442},"number":31,"pull_request":{"number":31}}`},
        {"pull_request",`{"action":"closed","installation":{"id":881},"repository":{"id":442},"number":31,"pull_request":{"number":32,"merged":false}}`},
        {"pull_request",`{"action":"opened","installation":{"id":881},"repository":{"id":442},"number":31,"pull_request":null}`},
        {"issues",`{"action":"opened","installation":{"id":881},"repository":{"id":442},"issue":{"number":30},"pull_request":{"number":30}}`},
    }
    inbox:=&automationInboxFake{webhookInboxFake:&webhookInboxFake{}}
    svc,err:=New(sampleSecret,inbox,&webhookProviderFake{},nil)
    if err!=nil{t.Fatal(err)}
    for _,item:=range invalid {
        got:=httptest.NewRecorder()
        svc.ServeHTTP(got,signedRequest([]byte(item.body),item.kind,"b61e607d-2a44-4d9a-8e4f-109e055cfc2d"))
        if got.Code!=http.StatusBadRequest{
            t.Fatalf("incomplete GitHub event accepted: %s %s %d",item.kind,item.body,got.Code)
        }
    }
    body:=[]byte(`{"action":"opened","installation":{"id":881},"repository":{"id":442},"issue":{"number":30}}`)
    badSignature:=signedRequest(body,"issues","b61e607d-2a44-4d9a-8e4f-109e055cfc2d")
    badSignature.Body=httptest.NewRequest(http.MethodPost,"/",strings.NewReader("{}")).Body
    got:=httptest.NewRecorder()
    svc.ServeHTTP(got,badSignature)
    if got.Code!=http.StatusUnauthorized{t.Fatalf("invalid raw HMAC accepted: %d",got.Code)}
    if len(inbox.admitted)!=0{t.Fatalf("unverified/incomplete event reached inbox %+v",inbox.admitted)}
}

func TestAutomationWebhookAdmissionErrorsAndRetryWorker(t *testing.T){
    inbox:=&automationInboxFake{webhookInboxFake:&webhookInboxFake{}}
    svc,err:=New(sampleSecret,inbox,&webhookProviderFake{err:errors.New("provider disabled")},nil)
    if err!=nil{t.Fatal(err)}
    body:=[]byte(`{"action":"opened","installation":{"id":881},"repository":{"id":442},"issue":{"number":30}}`)
    delivery:="b61e607d-2a44-4d9a-8e4f-109e055cfc2d"
    for _,tc:=range []struct{err error;want int}{
        {store.ErrNotFound,http.StatusAccepted},
        {store.ErrConflict,http.StatusConflict},
        {store.ErrGitHubAutomationFull,http.StatusServiceUnavailable},
        {store.ErrValidation,http.StatusBadRequest},
        {errors.New("database offline"),http.StatusServiceUnavailable},
    }{
        inbox.admitErr=tc.err
        got:=httptest.NewRecorder()
        svc.ServeHTTP(got,signedRequest(body,"issues",delivery))
        if got.Code!=tc.want{t.Fatalf("admission %v returned %d",tc.err,got.Code)}
    }
    inbox.admitErr=nil
    got:=httptest.NewRecorder()
    svc.ServeHTTP(got,signedRequest(body,"issues",delivery))
    if got.Code!=http.StatusAccepted||len(inbox.admitted)!=1{t.Fatalf("verified event not admitted %d",got.Code)}
    inbox.claims=[]store.GitHubAutomationEvent{{ID:12,LeaseToken:strings.Repeat("a",32)}}
    if err:=svc.ProcessAutomationBatch(context.Background());err!=nil{t.Fatal(err)}
    if inbox.matchCount!=1||inbox.retryCount!=0{t.Fatal("successful match did not complete")}
    inbox.claims=[]store.GitHubAutomationEvent{{ID:13,LeaseToken:strings.Repeat("b",32)}}
    inbox.matchErr=errors.New("temporary sqlite lock")
    if err:=svc.ProcessAutomationBatch(context.Background());err!=nil{t.Fatal(err)}
    if inbox.matchCount!=2||inbox.retryCount!=1{t.Fatal("failed match not queued for bounded retry")}
    inbox.claims=[]store.GitHubAutomationEvent{{ID:14,LeaseToken:strings.Repeat("c",32)}}
    inbox.matchErr=store.ErrConflict
    if err:=svc.ProcessAutomationBatch(context.Background());err!=nil{t.Fatal(err)}
    if inbox.retryCount!=1{t.Fatal("stolen lease was retried by stale worker")}
    counts,err:=svc.AutomationSummary(context.Background())
    if err!=nil||counts.Pending!=1{t.Fatalf("inbox summary %+v %v",counts,err)}
}
