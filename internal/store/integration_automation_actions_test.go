package store

import (
    "context"
    "errors"
    "testing"
    "time"
)

func TestIntegrationAutomationEnableAuthorizesFutureEvents(t *testing.T){
    st,cleanup,owner,project,resource:=newAutomationFixture(t)
    defer cleanup()
    ctx:=context.Background()
    rule,err:=st.CreateIntegrationAutomationRule(ctx,owner,project,CreateIntegrationAutomationRuleInput{
        Name:"Notify about merged PRs",ResourceID:resource,EventType:"pull_request.merged",ActionType:"notify",
    })
    if err!=nil||rule.Enabled{t.Fatalf("new rule %+v %v",rule,err)}
    before:=automationEvent(5100)
    if ok,err:=st.AdmitGitHubAutomation(ctx,before);err!=nil||!ok{t.Fatalf("pre-enable admit %v %v",ok,err)}
    enabled:=true
    rule,err=st.UpdateIntegrationAutomationRule(ctx,owner,project,rule.ID,UpdateIntegrationAutomationRuleInput{Enabled:&enabled})
    if err!=nil||!rule.Enabled{t.Fatalf("enable %+v %v",rule,err)}
    claim:=claimAutomationForTest(t,st,time.Now().Add(time.Second))
    if count,err:=st.MatchAndCompleteGitHubAutomation(ctx,claim.ID,claim.LeaseToken,time.Now().Add(2*time.Second));err!=nil||count!=0{
        t.Fatalf("event before enable triggered: %d %v",count,err)
    }
    summary,err:=st.IntegrationAutomationActionSummary(ctx)
    if err!=nil||summary.Pending!=0{t.Fatalf("pre-enable action %+v %v",summary,err)}
    time.Sleep(4*time.Millisecond)
    next:=automationEvent(5101)
    if ok,err:=st.AdmitGitHubAutomation(ctx,next);err!=nil||!ok{t.Fatalf("post-enable admit %v %v",ok,err)}
    claim=claimAutomationForTest(t,st,time.Now().Add(time.Second))
    if count,err:=st.MatchAndCompleteGitHubAutomation(ctx,claim.ID,claim.LeaseToken,time.Now().Add(2*time.Second));err!=nil||count!=1{
        t.Fatalf("post-enable match %d %v",count,err)
    }
    summary,err=st.IntegrationAutomationActionSummary(ctx)
    if err!=nil||summary.Pending!=1||summary.Completed!=0{t.Fatalf("not queued exactly once %+v %v",summary,err)}
    claims,err:=st.ClaimIntegrationAutomationActions(ctx,time.Now().Add(3*time.Second),5,45*time.Second)
    if err!=nil||len(claims)!=1{t.Fatalf("claims %+v %v",claims,err)}
    if _,err:=st.SettleIntegrationAutomationAction(ctx,claims[0].ID,"invalid",time.Now().Add(2*time.Second));!errors.Is(err,ErrValidation){
        t.Fatalf("malformed action token accepted: %v",err)
    }
    at:=time.Now().Add(2*time.Second)
    status,err:=st.SettleIntegrationAutomationAction(ctx,claims[0].ID,claims[0].LeaseToken,at)
    if err!=nil||status!="completed"{t.Fatalf("completion %q %v",status,err)}
    if _,err:=st.SettleIntegrationAutomationAction(ctx,claims[0].ID,claims[0].LeaseToken,at);!errors.Is(err,ErrConflict){
        t.Fatalf("executed twice: %v",err)
    }
    if ok,err:=st.AdmitGitHubAutomation(ctx,next);err!=nil||ok{t.Fatalf("duplicate %v %v",ok,err)}
    actions,err:=st.ListIntegrationAutomationActions(ctx,owner,project,10)
    if err!=nil||len(actions)!=1||actions[0].Status!="completed"||actions[0].ItemNumber!=357{
        t.Fatalf("notification history %+v %v",actions,err)
    }
    var transitions int
    if err:=st.db.QueryRowContext(ctx,"SELECT COUNT(*) FROM integration_automation_action_audit WHERE action_id=?",claims[0].ID).Scan(&transitions);err!=nil||transitions!=3{
        t.Fatalf("expected enqueue/claim/complete audit: %d %v",transitions,err)
    }
    outsider,err:=st.CreateUser(ctx,"no-automation-access@example.com","password123","Outsider")
    if err!=nil{t.Fatal(err)}
    if _,err:=st.ListIntegrationAutomationActions(ctx,outsider.ID,project,10);!errors.Is(err,ErrNotFound){
        t.Fatalf("cross-tenant disclosure: %v",err)
    }
}

func TestIntegrationAutomationDisableCancelsClaimedWorkAndReenableRequiresFreshEvent(t *testing.T){
    st,cleanup,owner,project,resource:=newAutomationFixture(t)
    defer cleanup()
    ctx:=context.Background()
    rule:=makeActiveAutomationRule(t,st,owner,project,resource)
    event:=automationEvent(5200)
    if ok,err:=st.AdmitGitHubAutomation(ctx,event);err!=nil||!ok{t.Fatalf("admit %v %v",ok,err)}
    claim:=claimAutomationForTest(t,st,time.Now().Add(time.Second))
    if _,err:=st.MatchAndCompleteGitHubAutomation(ctx,claim.ID,claim.LeaseToken,time.Now().Add(2*time.Second));err!=nil{t.Fatal(err)}
    actions,err:=st.ClaimIntegrationAutomationActions(ctx,time.Now().Add(3*time.Second),5,45*time.Second)
    if err!=nil||len(actions)!=1{t.Fatalf("claim %+v %v",actions,err)}
    disabled:=false
    changed,err:=st.UpdateIntegrationAutomationRule(ctx,owner,project,rule.ID,UpdateIntegrationAutomationRuleInput{Enabled:&disabled})
    if err!=nil||changed.Enabled{t.Fatalf("disable %+v %v",changed,err)}
    status,err:=st.SettleIntegrationAutomationAction(ctx,actions[0].ID,actions[0].LeaseToken,time.Now().Add(2*time.Second))
    if err!=nil||status!="cancelled"{t.Fatalf("disabled action executed: %s %v",status,err)}
    if _,err:=st.SettleIntegrationAutomationAction(ctx,actions[0].ID,actions[0].LeaseToken,time.Now().Add(2*time.Second));!errors.Is(err,ErrConflict){
        t.Fatalf("stale lease completed: %v",err)
    }
    enabled:=true
    changed,err=st.UpdateIntegrationAutomationRule(ctx,owner,project,rule.ID,UpdateIntegrationAutomationRuleInput{Enabled:&enabled})
    if err!=nil||!changed.Enabled{t.Fatalf("reenable %+v %v",changed,err)}
    past,err:=st.ListIntegrationAutomationActions(ctx,owner,project,10)
    if err!=nil||len(past)!=1||past[0].Status!="cancelled"{t.Fatalf("old action revived %+v %v",past,err)}
    time.Sleep(4*time.Millisecond)
    newEvent:=automationEvent(5201)
    if ok,err:=st.AdmitGitHubAutomation(ctx,newEvent);err!=nil||!ok{t.Fatalf("new event %v %v",ok,err)}
    match:=claimAutomationForTest(t,st,time.Now().Add(time.Second))
    if n,err:=st.MatchAndCompleteGitHubAutomation(ctx,match.ID,match.LeaseToken,time.Now().Add(2*time.Second));err!=nil||n!=1{t.Fatalf("reenable match %d %v",n,err)}
    summary,err:=st.IntegrationAutomationActionSummary(ctx)
    if err!=nil||summary.Pending!=1{t.Fatalf("reenabled rule did not queue %+v %v",summary,err)}
}

func TestIntegrationAutomationEditCancelsPriorAction(t *testing.T){
    st,cleanup,owner,project,resource:=newAutomationFixture(t)
    defer cleanup()
    ctx:=context.Background()
    rule:=makeActiveAutomationRule(t,st,owner,project,resource)
    if ok,err:=st.AdmitGitHubAutomation(ctx,automationEvent(5400));err!=nil||!ok{t.Fatalf("admit %v %v",ok,err)}
    claim:=claimAutomationForTest(t,st,time.Now().Add(time.Second))
    if _,err:=st.MatchAndCompleteGitHubAutomation(ctx,claim.ID,claim.LeaseToken,time.Now().Add(2*time.Second));err!=nil{t.Fatal(err)}
    actions,err:=st.ClaimIntegrationAutomationActions(ctx,time.Now().Add(3*time.Second),5,45*time.Second)
    if err!=nil||len(actions)!=1{t.Fatalf("claim %+v %v",actions,err)}
    name:="Renamed rule"
    changed,err:=st.UpdateIntegrationAutomationRule(ctx,owner,project,rule.ID,UpdateIntegrationAutomationRuleInput{Name:&name})
    if err!=nil||!changed.Enabled{t.Fatalf("edit %+v %v",changed,err)}
    status,err:=st.SettleIntegrationAutomationAction(ctx,actions[0].ID,actions[0].LeaseToken,time.Now().Add(2*time.Second))
    if err!=nil||status!="cancelled"{t.Fatalf("old rule version executed: %s %v",status,err)}
    time.Sleep(4*time.Millisecond)
    if ok,err:=st.AdmitGitHubAutomation(ctx,automationEvent(5401));err!=nil||!ok{t.Fatalf("new version event %v %v",ok,err)}
    match:=claimAutomationForTest(t,st,time.Now().Add(time.Second))
    if n,err:=st.MatchAndCompleteGitHubAutomation(ctx,match.ID,match.LeaseToken,time.Now().Add(2*time.Second));err!=nil||n!=1{t.Fatalf("edited rule match %d %v",n,err)}
}

func TestIntegrationAutomationRepositoryRevocationCancelsClaimedWork(t *testing.T){
    st,cleanup,owner,project,resource:=newAutomationFixture(t)
    defer cleanup()
    ctx:=context.Background()
    _=makeActiveAutomationRule(t,st,owner,project,resource)
    if ok,err:=st.AdmitGitHubAutomation(ctx,automationEvent(5500));err!=nil||!ok{t.Fatalf("admit %v %v",ok,err)}
    match:=claimAutomationForTest(t,st,time.Now().Add(time.Second))
    if _,err:=st.MatchAndCompleteGitHubAutomation(ctx,match.ID,match.LeaseToken,time.Now().Add(2*time.Second));err!=nil{t.Fatal(err)}
    actions,err:=st.ClaimIntegrationAutomationActions(ctx,time.Now().Add(3*time.Second),5,45*time.Second)
    if err!=nil||len(actions)!=1{t.Fatalf("claim %+v %v",actions,err)}
    if _,err:=st.db.ExecContext(ctx,"UPDATE integration_resources SET availability='unavailable' WHERE id=?",resource);err!=nil{t.Fatal(err)}
    status,err:=st.SettleIntegrationAutomationAction(ctx,actions[0].ID,actions[0].LeaseToken,time.Now().Add(2*time.Second))
    if err!=nil||status!="cancelled"{t.Fatalf("revoked repository executed: %s %v",status,err)}
}

func TestIntegrationAutomationActionRetriesAreBounded(t *testing.T){
    st,cleanup,owner,project,resource:=newAutomationFixture(t)
    defer cleanup()
    ctx:=context.Background()
    _=makeActiveAutomationRule(t,st,owner,project,resource)
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
