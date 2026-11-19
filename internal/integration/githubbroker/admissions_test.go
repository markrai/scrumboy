package githubbroker

import (
    "context"
    "errors"
    "net/http"
    "strings"
    "testing"
    "time"
)

func TestDurableAdmissionIsSharedByBrokerProcessesAndSurvivesRestart(t *testing.T){
    ctx:=context.Background()
    first,path:=newTestBindings(t)
    second,err:=OpenBindingsDB(path)
    if err!=nil{t.Fatal(err)}
    defer second.Close()
    clock:=time.Unix(1880000100,0).UTC()
    first.now=func()time.Time{return clock}
    second.now=func()time.Time{return clock}
    for i:=0;i<sharedInstanceBudget;i++{
        store:=first
        if i%2==0{store=second}
        allowed,err:=store.Admit(ctx,"instance-a","/v1/github/items/search")
        if err!=nil || !allowed{t.Fatalf("shared admission %d failed allowed=%v err=%v",i,allowed,err)}
    }
    allowed,err:=second.Admit(ctx,"instance-a","/v1/github/items/search")
    if err!=nil || allowed{t.Fatalf("cross-process limit bypass allowed=%v err=%v",allowed,err)}
    activity,err:=first.AdmissionActivity(ctx,"instance-a",10)
    if err!=nil || len(activity)!=2{t.Fatalf("unexpected activity %+v %v",activity,err)}
    for _,event:=range activity {
        if event.Operation!="search" || event.InstanceID!="instance-a" ||
            (event.Outcome=="admitted" && event.Hits!=sharedInstanceBudget) ||
            (event.Outcome=="limited" && event.Hits!=1) {
            t.Fatalf("inaccurate admission accounting %+v",event)
        }
    }
    if err:=first.Close();err!=nil{t.Fatal(err)}
    reopened,err:=OpenBindingsDB(path)
    if err!=nil{t.Fatal(err)}
    defer reopened.Close()
    reopened.now=func()time.Time{return clock}
    allowed,err=reopened.Admit(ctx,"instance-a","/v1/github/items/resolve")
    if err!=nil || allowed{t.Fatalf("restart reset shared budget allowed=%v err=%v",allowed,err)}
    clock=clock.Add(61*time.Second)
    allowed,err=reopened.Admit(ctx,"instance-a","/v1/github/items/resolve")
    if err!=nil || !allowed{t.Fatalf("new minute failed allowed=%v err=%v",allowed,err)}
    clock=clock.Add(-2*time.Minute)
    allowed,err=reopened.Admit(ctx,"instance-a","/v1/github/items/resolve")
    if err!=nil || allowed{t.Fatalf("clock rollback reset budget allowed=%v err=%v",allowed,err)}
}

func TestSharedGlobalBudgetCannotBeBypassedWithAnotherInstance(t *testing.T){
    ctx:=context.Background()
    store,_:=newTestBindings(t)
    allowed,err:=store.Admit(ctx,"instance-a","/v1/github/items/resolve")
    if err!=nil || !allowed{t.Fatal("seed budget failed",err)}
    _,err=store.db.ExecContext(ctx,`UPDATE github_broker_rate_counters SET hits=?
        WHERE scope='global'`,sharedGlobalBudget-1)
    if err!=nil{t.Fatal(err)}
    allowed,err=store.Admit(ctx,"instance-b","/v1/github/items/resolve")
    if err!=nil || !allowed{t.Fatalf("last global slot unavailable %v %v",allowed,err)}
    allowed,err=store.Admit(ctx,"instance-c","/v1/github/items/resolve")
    if err!=nil || allowed{t.Fatalf("global limit bypass with new instance: %v %v",allowed,err)}
    activity,err:=store.AdmissionActivity(ctx,"instance-c",10)
    if err!=nil||len(activity)!=1||activity[0].Outcome!="limited"{
        t.Fatalf("denied action not recorded %+v %v",activity,err)
    }
}

func TestAdmissionRejectsUnscopedOperationsAndRecordsNoPrivateData(t *testing.T) {
    ctx:=context.Background()
    store,_:=newTestBindings(t)
    for _,tc:=range []struct{instance,path string}{
        {"instance/forged","/v1/github/items/resolve"},
        {"instance-a","/admin/repositories"},
        {"instance-a","/v1/github/items/resolve?token=secret"},
    }{
        if allowed,err:=store.Admit(ctx,tc.instance,tc.path);allowed || !errors.Is(err,ErrDenied){
            t.Fatalf("unsafe operation was admitted: %v %v",allowed,err)
        }
    }
    if _,err:=store.AdmissionActivity(ctx,"",10);!errors.Is(err,ErrDenied){t.Fatal("unscoped audit query")}
    if _,err:=store.AdmissionActivity(ctx,"instance-a",201);!errors.Is(err,ErrDenied){t.Fatal("unbounded audit query")}
    for _,operation:=range []string{
        "/v1/github/connections/begin","/v1/github/connections/complete",
        "/v1/github/connections/revoke",
    }{
        if allowed,err:=store.Admit(ctx,"instance-a",operation);err!=nil||!allowed{
            t.Fatalf("authorized bounded operation failed %v %v",allowed,err)
        }
    }
    events,err:=store.AdmissionActivity(ctx,"instance-a",20)
    if err!=nil || len(events)!=3{t.Fatalf("expected 3 aggregate events %+v %v",events,err)}
    for _,event:=range events {
        if strings.Contains(event.Operation,"token") || strings.Contains(event.Operation,"code") ||
            event.Hits!=1 || event.WindowStart.IsZero() {
            t.Fatalf("unexpected audit metadata %+v",event)
        }
    }
}

type durableStub struct{
    ok bool
    err error
    calls int
}

func (s *durableStub) Admit(_ context.Context,instance,operation string)(bool,error){
    s.calls++
    return s.ok,s.err
}

func TestBrokerDurableAdmissionFailsClosedBeforeGitHubAccess(t *testing.T) {
    provider:=&itemStub{}
    testGrant:=noopUserGrants{}
    broker,err:=NewUserBound(identityStub{instance:"instance-a"},testGrant,provider)
    if err!=nil{t.Fatal(err)}
    gate:=&durableStub{ok:false}
    if err:=broker.EnableDurableAdmission(gate);err!=nil{t.Fatal(err)}
    body:=`{"userId":42,"installationId":11,"repositoryId":22,"kind":"issue","number":357}`
    response:=call(t,broker,http.MethodPost,"/v1/github/items/resolve",body,"application/json")
    if response.Code!=http.StatusTooManyRequests || provider.resolveCalls!=0 || gate.calls!=1{
        t.Fatalf("shared rate limit bypass %d calls=%d",response.Code,provider.resolveCalls)
    }
    gate.err=ErrUnavailable
    response=call(t,broker,http.MethodPost,"/v1/github/items/resolve",body,"application/json")
    if response.Code!=http.StatusServiceUnavailable || provider.resolveCalls!=0{
        t.Fatalf("shared rate store failure bypass %d",response.Code)
    }
    gate.err=nil
    gate.ok=true
    response=call(t,broker,http.MethodPost,"/v1/github/items/resolve",body,"application/json")
    if response.Code!=http.StatusForbidden || provider.resolveCalls!=0{
        t.Fatalf("durable admission granted ownership incorrectly %d",response.Code)
    }
    if err:=broker.EnableDurableAdmission(gate);!errors.Is(err,ErrUnavailable){t.Fatal("overwrote admission guard")}
    legacy:=testBroker(t,"instance-a",activeGrant(),provider)
    if err:=legacy.EnableDurableAdmission(gate);!errors.Is(err,ErrUnavailable){t.Fatal("enabled shared admission in operator mode")}
}
