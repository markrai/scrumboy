package githubwebhook

import (
    "context"
    "errors"
    "io"
    "log"
    "testing"
    "time"

    "scrumboy/internal/store"
)

type automationExecutionFake struct {
    claims int
    settled int
    fail bool
}

func (f *automationExecutionFake) ClaimIntegrationAutomationActions(context.Context,time.Time,int,time.Duration)([]store.IntegrationAutomationActionClaim,error){
    f.claims++
    return []store.IntegrationAutomationActionClaim{{ID:1,LeaseToken:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},nil
}

func (f *automationExecutionFake) SettleIntegrationAutomationAction(context.Context,int64,string,time.Time)(string,error){
    f.settled++
    if f.fail{return "",errors.New("store unavailable")}
    return "completed",nil
}

func (f *automationExecutionFake) FailIntegrationAutomationAction(context.Context,int64,string,time.Time)(bool,error){
    return true,nil
}

func TestAutomationExecutorIsOperatorGated(t *testing.T){
    t.Setenv("SCRUMBOY_GITHUB_AUTOMATION_EXECUTION_ENABLED","false")
    fake:=&automationExecutionFake{}
    service:=&Service{executor:fake,logger:log.New(io.Discard,"",0)}
    if err:=service.ProcessAutomationExecutionBatch(context.Background());err!=nil{t.Fatal(err)}
    if fake.claims!=0||fake.settled!=0{t.Fatal("executed while operator gate disabled")}
    t.Setenv("SCRUMBOY_GITHUB_AUTOMATION_EXECUTION_ENABLED","true")
    if err:=service.ProcessAutomationExecutionBatch(context.Background());err!=nil{t.Fatal(err)}
    if fake.claims!=1||fake.settled!=1{t.Fatalf("enabled executor claims=%d settles=%d",fake.claims,fake.settled)}
}
