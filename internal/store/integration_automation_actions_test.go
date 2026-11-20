package store

import (
    "context"
    "errors"
    "testing"
    "time"
)

func TestIntegrationAutomationExecutionRequiresFreshApproval(t *testing.T){
    st,cleanup,owner,project,resource:=newAutomationFixture(t)
    defer cleanup()
    ctx:=context.Background()
    rule:=makeActiveAutomationRule(t,st,owner,project,resource)
    if rule.ExecutionApprovedAt!=nil{t.Fatal("enabled legacy rule was pre-approved")}

    initial:=automationEvent(5100)
    if ok,err:=st.AdmitGitHubAutomation(ctx,initial);err!=nil||!ok{t.Fatalf("initial event %v %v",ok,err)}
    claim:=claimAutomationForTest(t,st,time.Now().Add(time.Second))
    if _,err:=st.MatchAndCompleteGitHubAutomation(ctx,claim.ID,claim.LeaseToken,time.Now().Add(2*time.Second));err!=nil{t.Fatal(err)}
    before,err:=st.IntegrationAutomationActionSummary(ctx)
    if err!=nil||before.Pending!=0{t.Fatalf("pre-consent action %+v %v",before,err)}

    if _,err:=st.ApproveIntegrationAutomationExecution(ctx,owner,project,rule.ID,rule.UpdatedAt.UnixMilli()+1);!errors.Is(err,ErrConflict){
        t.Fatalf("stale version accepted: %v",err)
    }
    approved,err:=st.ApproveIntegrationAutomationExecution(ctx,owner,project,rule.ID,rule.UpdatedAt.UnixMilli())
    if err!=nil||approved.ExecutionApprovedAt==nil{t.Fatalf("approval %+v %v",approved,err)}
    time.Sleep(3*time.Millisecond)
    next:=automationEvent(5101)
    if ok,err:=st.AdmitGitHubAutomation(ctx,next);err!=nil||!ok{t.Fatalf("event after consent %v %v",ok,err)}
    claim=claimAutomationForTest(t,st,time.Now().Add(time.Second))
    if _,err:=st.MatchAndCompleteGitHubAutomation(ctx,claim.ID,claim.LeaseToken,time.Now().Add(2*time.Second));err!=nil{t.Fatal(err)}
    summary,err:=st.IntegrationAutomationActionSummary(ctx)
    if err!=nil||summary.Pending!=1||summary.Completed!=0{t.Fatalf("not queued exactly once %+v %v",summary,err)}
    claims,err:=st.ClaimIntegrationAutomationActions(ctx,time.Now().Add(time.Second),5,45*time.Second)
    if err!=nil||len(claims)!=1{t.Fatalf("claims %+v %v",claims,err)}
    if _,err:=st.SettleIntegrationAutomationAction(ctx,claims[0].ID,"invalid",time.Now().Add(2*time.Second));!errors.Is(err,ErrValidation){
        t.Fatalf("malformed action token accepted: %v",err)
    }
    when:=time.Now().Add(2*time.Second)
    status,err:=st.SettleIntegrationAutomationAction(ctx,claims[0].ID,claims[0].LeaseToken,when)
    if err!=nil||status!="completed"{t.Fatalf("completion %q %v",status,err)}
    if _,err:=st.SettleIntegrationAutomationAction(ctx,claims[0].ID,claims[0].LeaseToken,when);!errors.Is(err,ErrConflict){
        t.Fatalf("action executed twice: %v",err)
    }
    if ok,err:=st.AdmitGitHubAutomation(ctx,next);err!=nil||ok{t.Fatalf("duplicate event %v %v",ok,err)}
    actions,err:=st.ListIntegrationAutomationActions(ctx,owner,project,10)
    if err!=nil||len(actions)!=1||actions[0].Status!="completed"||actions[0].ItemNumber!=357{
        t.Fatalf("project notification history %+v %v",actions,err)
    }
    var transitions int
    if err:=st.db.QueryRowContext(ctx,"SELECT COUNT(*) FROM integration_automation_action_audit WHERE action_id=?",claims[0].ID).Scan(&transitions);err!=nil||transitions!=3{
        t.Fatalf("expected enqueue/claim/complete audit: %d %v",transitions,err)
    }
    outsider,err:=st.CreateUser(ctx,"no-automation-access@example.com","password123","Outsider")
    if err!=nil{t.Fatal(err)}
    if _,err:=st.ListIntegrationAutomationActions(ctx,outsider.ID,project,10);!errors.Is(err,ErrNotFound){
        t.Fatalf("cross-tenant action disclosure: %v",err)
    }
}

func TestIntegrationAutomationExecutionRevocationCancelsClaimedWork(t *testing.T){
    st,cleanup,owner,project,resource:=newAutomationFixture(t)
    defer cleanup()
    ctx:=context.Background()
    rule:=makeActiveAutomationRule(t,st,owner,project,resource)
    if _,err:=st.ApproveIntegrationAutomationExecution(ctx,owner,project,rule.ID,rule.UpdatedAt.UnixMilli());err!=nil{t.Fatal(err)}
    time.Sleep(3*time.Millisecond)
    event:=automationEvent(5200)
    if ok,err:=st.AdmitGitHubAutomation(ctx,event);err!=nil||!ok{t.Fatalf("admit %v %v",ok,err)}
    claim:=claimAutomationForTest(t,st,time.Now().Add(time.Second))
    if _,err:=st.MatchAndCompleteGitHubAutomation(ctx,claim.ID,claim.LeaseToken,time.Now().Add(2*time.Second));err!=nil{t.Fatal(err)}
    actions,err:=st.ClaimIntegrationAutomationActions(ctx,time.Now().Add(time.Second),5,45*time.Second)
    if err!=nil||len(actions)!=1{t.Fatalf("claim %+v %v",actions,err)}
    if err:=st.RevokeIntegrationAutomationExecution(ctx,owner,project,rule.ID);err!=nil{t.Fatal(err)}
    status,err:=st.SettleIntegrationAutomationAction(ctx,actions[0].ID,actions[0].LeaseToken,time.Now().Add(2*time.Second))
    if err!=nil||status!="cancelled"{t.Fatalf("revoked action executed: %s %v",status,err)}
    if _,err:=st.SettleIntegrationAutomationAction(ctx,actions[0].ID,actions[0].LeaseToken,time.Now().Add(2*time.Second));!errors.Is(err,ErrConflict){
        t.Fatalf("stale lease completed: %v",err)
    }
    approved,err:=st.ApproveIntegrationAutomationExecution(ctx,owner,project,rule.ID,rule.UpdatedAt.UnixMilli())
    if err!=nil||approved.ExecutionApprovedAt==nil{t.Fatalf("reapproval %+v %v",approved,err)}
    past,err:=st.ListIntegrationAutomationActions(ctx,owner,project,10)
    if err!=nil||len(past)!=1||past[0].Status!="cancelled"{t.Fatalf("reapproved old action %+v %v",past,err)}
    enabled:=false
    changed,err:=st.UpdateIntegrationAutomationRule(ctx,owner,project,rule.ID,UpdateIntegrationAutomationRuleInput{Enabled:&enabled})
    if err!=nil||changed.ExecutionApprovedAt!=nil{t.Fatalf("edit did not revoke consent %+v %v",changed,err)}
    if _,err:=st.ApproveIntegrationAutomationExecution(ctx,owner,project,rule.ID,changed.UpdatedAt.UnixMilli());!errors.Is(err,ErrValidation){
        t.Fatalf("disabled rule approved: %v",err)
    }
}

func TestIntegrationAutomationExecutionCancelsWhenApproverLosesMaintainerRole(t *testing.T){
    st,cleanup,owner,project,resource:=newAutomationFixture(t)
    defer cleanup()
    ctx:=context.Background()
    rule:=makeActiveAutomationRule(t,st,owner,project,resource)
    if _,err:=st.ApproveIntegrationAutomationExecution(ctx,owner,project,rule.ID,rule.UpdatedAt.UnixMilli());err!=nil{t.Fatal(err)}
    time.Sleep(3*time.Millisecond)
    event:=automationEvent(5400)
    if ok,err:=st.AdmitGitHubAutomation(ctx,event);err!=nil||!ok{t.Fatalf("admit %v %v",ok,err)}
    claim:=claimAutomationForTest(t,st,time.Now().Add(time.Second))
    if _,err:=st.MatchAndCompleteGitHubAutomation(ctx,claim.ID,claim.LeaseToken,time.Now().Add(2*time.Second));err!=nil{t.Fatal(err)}
    actions,err:=st.ClaimIntegrationAutomationActions(ctx,time.Now().Add(time.Second),5,45*time.Second)
    if err!=nil||len(actions)!=1{t.Fatalf("claim %+v %v",actions,err)}
    if _,err:=st.db.ExecContext(ctx,"UPDATE project_members SET role='viewer' WHERE project_id=? AND user_id=?",project,owner);err!=nil{t.Fatal(err)}
    status,err:=st.SettleIntegrationAutomationAction(ctx,actions[0].ID,actions[0].LeaseToken,time.Now().Add(2*time.Second))
    if err!=nil||status!="cancelled"{t.Fatalf("removed approver executed action %s %v",status,err)}
}

func TestIntegrationAutomationActionRetriesAreBounded(t *testing.T){
    st,cleanup,owner,project,resource:=newAutomationFixture(t)
    defer cleanup()
    ctx:=context.Background()
    rule:=makeActiveAutomationRule(t,st,owner,project,resource)
    if _,err:=st.ApproveIntegrationAutomationExecution(ctx,owner,project,rule.ID,rule.UpdatedAt.UnixMilli());err!=nil{t.Fatal(err)}
    time.Sleep(3*time.Millisecond)
    event:=automationEvent(5300)
    if ok,err:=st.AdmitGitHubAutomation(ctx,event);err!=nil||!ok{t.Fatalf("admit %v %v",ok,err)}
    match:=claimAutomationForTest(t,st,time.Now().Add(time.Second))
    if _,err:=st.MatchAndCompleteGitHubAutomation(ctx,match.ID,match.LeaseToken,time.Now().Add(2*time.Second));err!=nil{t.Fatal(err)}
    at:=time.Now().Add(time.Minute)
    var id int64
    for i:=1;i<=maxIntegrationAutomationActionAttempts;i++{
        claims,err:=st.ClaimIntegrationAutomationActions(ctx,at,1,15*time.Second)
        if err!=nil||len(claims)!=1{t.Fatalf("attempt %d %+v %v",i,claims,err)}
        id=claims[0].ID
        if ok,err:=st.FailIntegrationAutomationAction(ctx,id,claims[0].LeaseToken,at.Add(time.Second));err!=nil||!ok{
            t.Fatalf("failed attempt %d %v %v",i,ok,err)
        }
        at=at.Add(31*time.Minute)
    }
    summary,err:=st.IntegrationAutomationActionSummary(ctx)
    if err!=nil||summary.Dead!=1||summary.Completed!=0{t.Fatalf("retry cap %+v %v",summary,err)}
    claims,err:=st.ClaimIntegrationAutomationActions(ctx,at,1,15*time.Second)
    if err!=nil||len(claims)!=0{t.Fatalf("dead action reclaimed %+v %v",claims,err)}
}
