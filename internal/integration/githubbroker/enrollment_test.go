package githubbroker

import (
    "context"
    "encoding/json"
    "errors"
    "net/http"
    "strings"
    "testing"

    "scrumboy/internal/integration/githubconnect"
)

func TestBoundBrokerEnrollmentIsAuthenticatedAndAtomicallyPersisted(t *testing.T) {
    store,_:=newTestBindings(t)
    provider:=&itemStub{}
    b,err:=NewUserBound(identityStub{instance:"instance-a"},store,provider)
    if err!=nil{t.Fatal(err)}
    valid:=verifiedFixture("instance-a",42,11)
    verifier:=&verificationStub{verification:valid.verification}
    repos:=&resourceStub{items:valid.resources}
    coordinator,err:=NewBindingCoordinator(verifier,repos,store)
    if err!=nil{t.Fatal(err)}
    if err:=b.EnableEnrollment(coordinator);err!=nil{t.Fatal(err)}
    begin:=call(t,b,http.MethodPost,"/v1/github/connections/begin",
        `{"userId":42}`,"application/json")
    if begin.Code!=http.StatusOK || verifier.begins!=1{
        t.Fatalf("enrollment begin failed: %d %s",begin.Code,begin.Body.String())
    }
    var beginResult struct{AuthorizationURL string `json:"authorizationUrl"`}
    if err:=json.Unmarshal(begin.Body.Bytes(),&beginResult);err!=nil{t.Fatal(err)}
    if !strings.HasPrefix(beginResult.AuthorizationURL,"https://github.com/login/oauth/authorize?"){
        t.Fatal("unsafe enrollment redirect")
    }
    validState:=strings.Repeat("a",43)
    complete:=call(t,b,http.MethodPost,"/v1/github/connections/complete",
        `{"userId":42,"installationId":11,"state":"`+validState+`","code":"one-time-code"}`,"application/json")
    if complete.Code!=http.StatusOK || verifier.completes!=1 || repos.calls!=1 {
        t.Fatalf("enrollment complete failed %d %s",complete.Code,complete.Body.String())
    }
    if _,err:=store.LookupUser(context.Background(),"instance-a",42,11,22);err!=nil{t.Fatal(err)}
    lookup:=call(t,b,http.MethodPost,"/v1/github/items/resolve",
        `{"userId":42,"installationId":11,"repositoryId":22,"kind":"issue","number":357}`,"application/json")
    if lookup.Code!=http.StatusOK || provider.resolveCalls!=1{
        t.Fatalf("enrolled user could not read permitted GitHub item: %d",lookup.Code)
    }
    if strings.Contains(complete.Body.String(),"one-time-code") ||
        strings.Contains(complete.Body.String(),"github-user-token") {
        t.Fatal("OAuth credentials leaked in binding response")
    }
    if err:=b.EnableEnrollment(coordinator);!errors.Is(err,ErrUnavailable){
        t.Fatal("double enrollment registration accepted")
    }
}

func TestEnrollmentDeniesInvalidSubjectAndMalformedOAuth(t *testing.T) {
    store,_:=newTestBindings(t)
    provider:=&itemStub{}
    b,err:=NewUserBound(identityStub{instance:"instance-a"},store,provider)
    if err!=nil{t.Fatal(err)}
    valid:=verifiedFixture("instance-a",42,11)
    verifier:=&verificationStub{verification:valid.verification,err:githubconnect.ErrNotAuthorized}
    reader:=&resourceStub{items:valid.resources}
    coordinator,err:=NewBindingCoordinator(verifier,reader,store)
    if err!=nil{t.Fatal(err)}
    if err:=b.EnableEnrollment(coordinator);err!=nil{t.Fatal(err)}
    validState:=strings.Repeat("a",43)
    cases:=[]string{
        `{"userId":0}`,
        `{"userId":42,"installationId":11}`,
        `{"userId":42,"state":"state"}`,
        `{"userId":42,"unexpected":true}`,
        `{"userId":42} {"userId":43}`,
    }
    for _,payload:=range cases {
        got:=call(t,b,http.MethodPost,"/v1/github/connections/begin",payload,"application/json")
        if got.Code!=http.StatusBadRequest{
            t.Fatalf("invalid begin accepted %d %s",got.Code,got.Body.String())
        }
    }
    for _,payload:=range []string{
        `{"userId":42,"installationId":11,"state":"short","code":"abc"}`,
        `{"userId":42,"installationId":11,"state":"`+validState+`","code":""}`,
        `{"userId":42,"installationId":0,"state":"`+validState+`","code":"abc"}`,
    }{
        got:=call(t,b,http.MethodPost,"/v1/github/connections/complete",payload,"application/json")
        if got.Code!=http.StatusBadRequest{
            t.Fatalf("invalid complete accepted %d %s",got.Code,got.Body.String())
        }
    }
    got:=call(t,b,http.MethodPost,"/v1/github/connections/complete",
        `{"userId":42,"installationId":11,"state":"`+validState+`","code":"abc"}`,"application/json")
    if got.Code!=http.StatusForbidden || reader.calls!=0 || strings.Contains(got.Body.String(),"token"){
        t.Fatalf("unverified OAuth reached repository grants %d %s",got.Code,got.Body.String())
    }
    if _,err:=store.LookupUser(context.Background(),"instance-a",42,11,22);!errors.Is(err,ErrDenied){
        t.Fatal("failed OAuth persisted binding")
    }
}

func TestLegacyBrokerCannotExposeEnrollmentEndpoints(t *testing.T) {
    provider:=&itemStub{}
    legacy:=testBroker(t,"instance-a",activeGrant(),provider)
    result:=call(t,legacy,http.MethodPost,"/v1/github/connections/begin",`{"userId":42}`,"application/json")
    if result.Code!=http.StatusNotFound{t.Fatalf("legacy broker enrollment exposed %d",result.Code)}
    if err:=legacy.EnableEnrollment(&verificationStub{});!errors.Is(err,ErrUnavailable){
        t.Fatalf("legacy broker enabled enrollment: %v",err)
    }
    unauthorized,err:=NewUserBound(identityStub{err:ErrDenied},newNoopUserGrants(),provider)
    if err!=nil{t.Fatal(err)}
    got:=call(t,unauthorized,http.MethodPost,"/v1/github/connections/begin",`{"userId":42}`,"application/json")
    if got.Code!=http.StatusUnauthorized{t.Fatalf("unauthorized enrollment reached API %d",got.Code)}
}

type noopUserGrants struct{}

func newNoopUserGrants() UserGrants {return noopUserGrants{}}

func (noopUserGrants) LookupUser(context.Context,string,int64,int64,int64)(Grant,error) {
    return Grant{},ErrDenied
}
