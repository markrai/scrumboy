package store

import (
    "context"
    "errors"
    "fmt"
    "testing"
    "time"

    "scrumboy/internal/integration"
)

func newAutomationFixture(t *testing.T)(*Store,func(),int64,int64,int64){
    t.Helper()
    st,cleanup:=newTestStore(t)
    ctx:=context.Background()
    owner,err:=st.BootstrapUser(ctx,"automation-matcher@example.com","password123","Owner")
    if err!=nil{cleanup();t.Fatal(err)}
    project,err:=st.CreateProject(WithUserID(ctx,owner.ID),"Matched Automation")
    if err!=nil{cleanup();t.Fatal(err)}
    installation:=integration.Installation{ID:"881",Account:"engineering",Kind:"Organization"}
    repo:=integration.Resource{ID:"442",Name:"engineering/work",URL:"https://github.com/engineering/work"}
    connection,err:=st.SyncGitHubInstallation(ctx,owner.ID,installation,[]integration.Resource{repo})
    if err!=nil{cleanup();t.Fatal(err)}
    resources,err:=st.ListIntegrationResources(ctx,owner.ID,connection.ID)
    if err!=nil || len(resources)!=1{cleanup();t.Fatalf("resources %+v %v",resources,err)}
    if err:=st.AttachIntegrationResource(ctx,owner.ID,project.ID,resources[0].ID);err!=nil{
        cleanup();t.Fatal(err)
    }
    return st,cleanup,owner.ID,project.ID,resources[0].ID
}

func automationEvent(n int) GitHubAutomationEvent{
    return GitHubAutomationEvent{
        DeliveryID:fmt.Sprintf("b61e607d-2a44-4d9a-8e4f-%012x",n),
        InstallationID:"881",RepositoryExternalID:"442",
        ItemNumber:357,EventType:"pull_request.merged",
        PayloadSHA256:fmt.Sprintf("%064x",n+1),
    }
}

func makeActiveAutomationRule(t *testing.T,st *Store,owner,project,resource int64)IntegrationAutomationRule{
    t.Helper()
    ctx:=context.Background()
    rule,err:=st.CreateIntegrationAutomationRule(ctx,owner,project,CreateIntegrationAutomationRuleInput{
        Name:"Match merged PR",ResourceID:resource,EventType:"pull_request.merged",ActionType:"notify",
    })
    if err!=nil{t.Fatal(err)}
    enabled:=true
    rule,err=st.UpdateIntegrationAutomationRule(ctx,owner,project,rule.ID,UpdateIntegrationAutomationRuleInput{Enabled:&enabled})
    if err!=nil||!rule.Enabled{t.Fatalf("cannot enable rule %+v %v",rule,err)}
    time.Sleep(4*time.Millisecond)
    return rule
}

func claimAutomationForTest(t *testing.T,st *Store,at time.Time)GitHubAutomationEvent{
    t.Helper()
    claims,err:=st.ClaimGitHubAutomation(context.Background(),at,5,45*time.Second)
    if err!=nil || len(claims)!=1{t.Fatalf("unexpected claimed events %+v %v",claims,err)}
    return claims[0]
}

func TestAutomationInboxDeduplicatesAndRecoversLeases(t *testing.T){
    st,cleanup,_,_,_:=newAutomationFixture(t)
    defer cleanup()
    ctx:=context.Background()
    event:=automationEvent(1)
    if ok,err:=st.AdmitGitHubAutomation(ctx,event);err!=nil||!ok{t.Fatalf("first admit %v %v",ok,err)}
    if ok,err:=st.AdmitGitHubAutomation(ctx,event);err!=nil||ok{t.Fatalf("duplicate %v %v",ok,err)}
    changed:=event
    changed.PayloadSHA256=fmt.Sprintf("%064x",999)
    if _,err:=st.AdmitGitHubAutomation(ctx,changed);!errors.Is(err,ErrConflict){
        t.Fatalf("conflicting delivery not rejected: %v",err)
    }
    changed=event
    changed.RepositoryExternalID="443"
    changed.DeliveryID=automationEvent(2).DeliveryID
    if _,err:=st.AdmitGitHubAutomation(ctx,changed);!errors.Is(err,ErrNotFound){
        t.Fatalf("uninstalled repository admitted: %v",err)
    }
    changed=event
    changed.InstallationID="882"
    changed.DeliveryID=automationEvent(3).DeliveryID
    if _,err:=st.AdmitGitHubAutomation(ctx,changed);!errors.Is(err,ErrNotFound){
        t.Fatalf("unowned installation admitted: %v",err)
    }
    changed=event
    changed.EventType="delete"
    changed.DeliveryID=automationEvent(4).DeliveryID
    if _,err:=st.AdmitGitHubAutomation(ctx,changed);!errors.Is(err,ErrValidation){
        t.Fatalf("unrecognized event admitted: %v",err)
    }
    now:=time.Now().UTC().Add(time.Second)
    claimed:=claimAutomationForTest(t,st,now)
    if claimed.Attempts!=1 || len(claimed.LeaseToken)!=32{t.Fatalf("invalid lease %+v",claimed)}
    concurrent,err:=st.ClaimGitHubAutomation(ctx,now.Add(time.Second),5,45*time.Second)
    if err!=nil || len(concurrent)!=0{t.Fatalf("double claim %+v %v",concurrent,err)}
    if _,err:=st.MatchAndCompleteGitHubAutomation(ctx,claimed.ID,"invalid",now);!errors.Is(err,ErrValidation){
        t.Fatalf("malformed lease accepted: %v",err)
    }
    if _,err:=st.MatchAndCompleteGitHubAutomation(ctx,claimed.ID,
        "00000000000000000000000000000000",now);!errors.Is(err,ErrConflict){
        t.Fatalf("stolen lease accepted: %v",err)
    }
    ok,err:=st.FailGitHubAutomation(ctx,claimed.ID,claimed.LeaseToken,now)
    if err!=nil||!ok{t.Fatalf("failed retry %v %v",ok,err)}
    summary,err:=st.GitHubAutomationSummary(ctx)
    if err!=nil||summary.Pending!=1||summary.Leased!=0||summary.Matches!=0{
        t.Fatalf("incorrect retry state %+v %v",summary,err)
    }
    retryTime:=now.Add(2*time.Minute)
    claimed=claimAutomationForTest(t,st,retryTime)
    if claimed.Attempts!=2{t.Fatalf("retry attempts=%d",claimed.Attempts)}
    matched,err:=st.MatchAndCompleteGitHubAutomation(ctx,claimed.ID,claimed.LeaseToken,retryTime.Add(time.Second))
    if err!=nil||matched!=0{t.Fatalf("unexpected match %d %v",matched,err)}
    if _,err:=st.MatchAndCompleteGitHubAutomation(ctx,claimed.ID,claimed.LeaseToken,retryTime.Add(time.Second));
        !errors.Is(err,ErrConflict){t.Fatalf("completed lease reused %v",err)}
    summary,err=st.GitHubAutomationSummary(ctx)
    if err!=nil||summary.Completed!=1||summary.Pending!=0||summary.Matches!=0{
        t.Fatalf("unexpected completed state %+v %v",summary,err)
    }
    if err:=st.MaintainGitHubAutomation(ctx,now.Add(31*24*time.Hour));err!=nil{t.Fatal(err)}
    summary,err=st.GitHubAutomationSummary(ctx)
    if err!=nil||summary.Completed!=0{t.Fatalf("old events not pruned %+v %v",summary,err)}
}

func TestGitHubAutomationMatchesOnlyEnabledRulesWithinVerifiedInstallations(t *testing.T){
    st,cleanup,owner,project,resource:=newAutomationFixture(t)
    defer cleanup()
    ctx:=context.Background()
    active:=makeActiveAutomationRule(t,st,owner,project,resource)
    disabled,err:=st.CreateIntegrationAutomationRule(ctx,owner,project,CreateIntegrationAutomationRuleInput{
        ResourceID:resource,Name:"Disabled",EventType:"pull_request.merged",ActionType:"notify",
    })
    if err!=nil{t.Fatal(err)}
    unrelated,err:=st.CreateProject(WithUserID(ctx,owner),"Unrelated Rules")
    if err!=nil{t.Fatal(err)}
    if err:=st.AttachIntegrationResource(ctx,owner,unrelated.ID,resource);err!=nil{t.Fatal(err)}
    unrelatedRule,err:=st.CreateIntegrationAutomationRule(ctx,owner,unrelated.ID,
        CreateIntegrationAutomationRuleInput{ResourceID:resource,Name:"Other Project",EventType:"issue.closed",ActionType:"notify"})
    if err!=nil{t.Fatal(err)}
    enabled:=true
    if _,err:=st.UpdateIntegrationAutomationRule(ctx,owner,unrelated.ID,unrelatedRule.ID,
        UpdateIntegrationAutomationRuleInput{Enabled:&enabled});err!=nil{t.Fatal(err)}
    viewer,err:=st.CreateUser(ctx,"automation-matcher-viewer@example.com","password123","Viewer")
    if err!=nil{t.Fatal(err)}
    outsider,err:=st.CreateUser(ctx,"automation-matcher-outsider@example.com","password123","Outsider")
    if err!=nil{t.Fatal(err)}
    if err:=st.AddProjectMember(WithUserID(ctx,owner),owner,project,viewer.ID,RoleViewer);err!=nil{t.Fatal(err)}
    event:=automationEvent(21)
    if _,err:=st.AdmitGitHubAutomation(ctx,event);err!=nil{t.Fatal(err)}
    now:=time.Now().UTC().Add(time.Second)
    claimed:=claimAutomationForTest(t,st,now)
    count,err:=st.MatchAndCompleteGitHubAutomation(ctx,claimed.ID,claimed.LeaseToken,now.Add(time.Second))
    if err!=nil||count!=1{t.Fatalf("expected only one match count=%d err=%v",count,err)}
    matches,err:=st.ListGitHubAutomationMatches(ctx,viewer.ID,project,30)
    if err!=nil || len(matches)!=1 || matches[0].RuleID!=active.ID ||
        matches[0].ResourceID!=resource || matches[0].EventType!="pull_request.merged" ||
        matches[0].ItemNumber!=357 || matches[0].ActionType!="notify" ||
        matches[0].WouldExecute || matches[0].ExecutionAvailable {
        t.Fatalf("unsafe/missing match receipt %+v %v",matches,err)
    }
    if matches[0].RuleID==disabled.ID{t.Fatal("disabled rule matched")}
    foreign,err:=st.ListGitHubAutomationMatches(ctx,owner,unrelated.ID,30)
    if err!=nil||len(foreign)!=0{t.Fatalf("cross-project match leaked %+v %v",foreign,err)}
    if _,err:=st.ListGitHubAutomationMatches(ctx,outsider.ID,project,30);!errors.Is(err,ErrNotFound){
        t.Fatalf("outsider read receipt metadata %v",err)
    }
    if _,err:=st.ListGitHubAutomationMatches(ctx,viewer.ID,project,101);!errors.Is(err,ErrValidation){
        t.Fatalf("unbounded receipt history %v",err)
    }
    if err:=st.DeleteIntegrationAutomationRule(ctx,owner,project,active.ID);err!=nil{t.Fatal(err)}
    after,err:=st.ListGitHubAutomationMatches(ctx,viewer.ID,project,30)
    if err!=nil||len(after)!=0{t.Fatalf("deleted rule leaked receipts %+v %v",after,err)}
}

func TestAutomationMatchingRejectsRevocationAfterAdmission(t *testing.T){
    st,cleanup,owner,project,resource:=newAutomationFixture(t)
    defer cleanup()
    ctx:=context.Background()
    _=makeActiveAutomationRule(t,st,owner,project,resource)
    if _,err:=st.AdmitGitHubAutomation(ctx,automationEvent(31));err!=nil{t.Fatal(err)}
    install:=integration.Installation{ID:"881",Account:"engineering",Kind:"Organization"}
    if _,err:=st.SyncGitHubInstallation(ctx,owner,install,nil);err!=nil{t.Fatal(err)}
    at:=time.Now().UTC().Add(time.Second)
    claimed:=claimAutomationForTest(t,st,at)
    count,err:=st.MatchAndCompleteGitHubAutomation(ctx,claimed.ID,claimed.LeaseToken,at.Add(time.Second))
    if err!=nil||count!=0{t.Fatalf("revoked repository produced match %d %v",count,err)}
    if _,err:=st.SyncGitHubInstallation(ctx,owner,install,[]integration.Resource{
        {ID:"442",Name:"engineering/work",URL:"https://github.com/engineering/work"},
    });err!=nil{t.Fatal(err)}
    if _,err:=st.AdmitGitHubAutomation(ctx,automationEvent(32));err!=nil{t.Fatal(err)}
    if err:=st.DisableGitHubInboundInstallation(ctx,"881");err!=nil{t.Fatal(err)}
    at=at.Add(3*time.Second)
    claimed=claimAutomationForTest(t,st,at)
    count,err=st.MatchAndCompleteGitHubAutomation(ctx,claimed.ID,claimed.LeaseToken,at.Add(time.Second))
    if err!=nil||count!=0{t.Fatalf("disabled installation produced match %d %v",count,err)}
}

func TestAutomationMatchingFailedReceiptRollsBackEntireDelivery(t *testing.T){
    st,cleanup,owner,project,resource:=newAutomationFixture(t)
    defer cleanup()
    _=makeActiveAutomationRule(t,st,owner,project,resource)
    ctx:=context.Background()
    if _,err:=st.AdmitGitHubAutomation(ctx,automationEvent(41));err!=nil{t.Fatal(err)}
    at:=time.Now().UTC().Add(time.Second)
    claimed:=claimAutomationForTest(t,st,at)
    if _,err:=st.db.ExecContext(ctx,`CREATE TRIGGER reject_match_receipt
        BEFORE INSERT ON integration_github_automation_matches
        BEGIN SELECT RAISE(ABORT,'receipt storage unavailable'); END`);err!=nil{t.Fatal(err)}
    if _,err:=st.MatchAndCompleteGitHubAutomation(ctx,claimed.ID,claimed.LeaseToken,at.Add(time.Second));err==nil{
        t.Fatal("matching commit survived receipt insertion failure")
    }
    summary,err:=st.GitHubAutomationSummary(ctx)
    if err!=nil||summary.Leased!=1||summary.Completed!=0||summary.Matches!=0{
        t.Fatalf("partial match recorded %+v %v",summary,err)
    }
    if _,err:=st.db.ExecContext(ctx,"DROP TRIGGER reject_match_receipt");err!=nil{t.Fatal(err)}
    if ok,err:=st.FailGitHubAutomation(ctx,claimed.ID,claimed.LeaseToken,at.Add(time.Second));err!=nil||!ok{
        t.Fatalf("retry unavailable %v %v",ok,err)
    }
    retry:=at.Add(2*time.Minute)
    claimed=claimAutomationForTest(t,st,retry)
    if count,err:=st.MatchAndCompleteGitHubAutomation(ctx,claimed.ID,claimed.LeaseToken,retry.Add(time.Second));
        err!=nil||count!=1{t.Fatalf("recovered match %d %v",count,err)}
    receipts,err:=st.ListGitHubAutomationMatches(ctx,owner,project,20)
    if err!=nil||len(receipts)!=1{t.Fatalf("exactly once receipts %+v %v",receipts,err)}
}
