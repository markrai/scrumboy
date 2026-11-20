package githubbroker

import (
    "context"
    "net/http"
    "testing"
)

func TestUserBoundBrokerRequiresExactStoredOwnership(t *testing.T) {
    store,_:=newTestBindings(t)
    provider:=&itemStub{}
    broker,err:=NewUserBound(identityStub{instance:"instance-a"},store,provider)
    if err!=nil{t.Fatal(err)}
    contentType:="application/json"
    initial:=`{"userId":42,"installationId":11,"repositoryId":22,"kind":"issue","number":357}`
    got:=call(t,broker,http.MethodPost,"/v1/github/items/resolve",initial,contentType)
    if got.Code!=http.StatusForbidden || provider.resolveCalls!=0 {
        t.Fatalf("unbound request reached provider %d",got.Code)
    }
    binding:=verifiedFixture("instance-a",42,11)
    if err:=store.recordVerified(context.Background(),binding);err!=nil{t.Fatal(err)}
    wrong:=[]string{
        `{"userId":43,"installationId":11,"repositoryId":22,"kind":"issue","number":357}`,
        `{"userId":42,"installationId":12,"repositoryId":22,"kind":"issue","number":357}`,
        `{"userId":42,"installationId":11,"repositoryId":99,"kind":"issue","number":357}`,
    }
    for _,payload:=range wrong{
        result:=call(t,broker,http.MethodPost,"/v1/github/items/resolve",payload,contentType)
        if result.Code!=http.StatusForbidden || provider.resolveCalls!=0{
            t.Fatalf("cross-user/repo request escaped authorization %d",result.Code)
        }
    }
    for _,payload:=range []string{
        validResolve,
        `{"userId":0,"installationId":11,"repositoryId":22,"kind":"issue","number":357}`,
        `{"userId":-1,"installationId":11,"repositoryId":22,"kind":"issue","number":357}`,
    }{
        result:=call(t,broker,http.MethodPost,"/v1/github/items/resolve",payload,contentType)
        if result.Code!=http.StatusBadRequest{t.Fatalf("missing or invalid user accepted %d",result.Code)}
    }
    success:=call(t,broker,http.MethodPost,"/v1/github/items/resolve",initial,contentType)
    if success.Code!=http.StatusOK || provider.resolveCalls!=1 || provider.repository!="engineering/one"{
        t.Fatalf("authorized lookup %d provider=%+v",success.Code,provider)
    }
    if err:=store.RevokeUser(context.Background(),"instance-a",42);err!=nil{t.Fatal(err)}
    revoked:=call(t,broker,http.MethodPost,"/v1/github/items/resolve",initial,contentType)
    if revoked.Code!=http.StatusForbidden || provider.resolveCalls!=1{
        t.Fatalf("revoked user still reached provider %d",revoked.Code)
    }
}

func TestUserBoundBrokerRejectsAnotherRegisteredInstance(t *testing.T) {
    store,_:=newTestBindings(t)
    if err:=store.recordVerified(context.Background(),verifiedFixture("instance-a",42,11));err!=nil{t.Fatal(err)}
    provider:=&itemStub{}
    broker,err:=NewUserBound(identityStub{instance:"instance-b"},store,provider)
    if err!=nil{t.Fatal(err)}
    got:=call(t,broker,http.MethodPost,"/v1/github/items/resolve",
        `{"userId":42,"installationId":11,"repositoryId":22,"kind":"issue","number":357}`,
        "application/json")
    if got.Code!=http.StatusForbidden || provider.resolveCalls!=0{
        t.Fatalf("separate instance stole binding %d",got.Code)
    }
    if _,err:=NewUserBound(nil,store,provider);err==nil{t.Fatal("missing instance auth accepted")}
    if _,err:=NewUserBound(identityStub{instance:"instance-a"},nil,provider);err==nil{
        t.Fatal("missing ownership DB accepted")
    }
}
