package store

import (
    "context"
    "errors"
    "testing"
    "time"
)

func TestIntegrationAutomationActionAuditRequiresProjectMembership(t *testing.T){
    st,cleanup,owner,project,resource:=newAutomationFixture(t)
    defer cleanup()
    ctx:=context.Background()
    viewer,err:=st.CreateUser(ctx,"automation-audit-viewer@example.com","password123","Viewer")
    if err!=nil{t.Fatal(err)}
    outsider,err:=st.CreateUser(ctx,"automation-audit-outsider@example.com","password123","Outsider")
    if err!=nil{t.Fatal(err)}
    if err:=st.AddProjectMember(WithUserID(ctx,owner),owner,project,viewer.ID,RoleViewer);err!=nil{t.Fatal(err)}
    _=makeActiveAutomationRule(t,st,owner,project,resource)
    time.Sleep(3*time.Millisecond)
    event:=automationEvent(5600)
    if ok,err:=st.AdmitGitHubAutomation(ctx,event);err!=nil||!ok{t.Fatalf("admit %v %v",ok,err)}
    matched:=claimAutomationForTest(t,st,time.Now().Add(time.Second))
    if _,err:=st.MatchAndCompleteGitHubAutomation(ctx,matched.ID,matched.LeaseToken,time.Now().Add(2*time.Second));err!=nil{t.Fatal(err)}
    claims,err:=st.ClaimIntegrationAutomationActions(ctx,time.Now().Add(3*time.Second),5,45*time.Second)
    if err!=nil||len(claims)!=1{t.Fatalf("action claim %+v %v",claims,err)}
    if status,err:=st.SettleIntegrationAutomationAction(ctx,claims[0].ID,claims[0].LeaseToken,time.Now().Add(4*time.Second));err!=nil||status!="completed"{
        t.Fatalf("settle %s %v",status,err)
    }
    audit,err:=st.ListIntegrationAutomationActionAudit(ctx,viewer.ID,project,claims[0].ID,30)
    if err!=nil||len(audit)!=3||audit[0].Transition!="completed"||audit[1].Transition!="claimed"||audit[2].Transition!="queued"{
        t.Fatalf("viewer audit %+v %v",audit,err)
    }
    if _,err:=st.ListIntegrationAutomationActionAudit(ctx,outsider.ID,project,claims[0].ID,30);!errors.Is(err,ErrNotFound){
        t.Fatalf("outsider audit visible: %v",err)
    }
    other,err:=st.CreateProject(WithUserID(ctx,owner),"Separate Automation Project")
    if err!=nil{t.Fatal(err)}
    if _,err:=st.ListIntegrationAutomationActionAudit(ctx,owner,other.ID,claims[0].ID,30);!errors.Is(err,ErrNotFound){
        t.Fatalf("cross-project audit visible: %v",err)
    }
    if _,err:=st.ListIntegrationAutomationActionAudit(ctx,owner,project,0,30);!errors.Is(err,ErrValidation){
        t.Fatalf("invalid action accepted: %v",err)
    }
    if _,err:=st.ListIntegrationAutomationActionAudit(ctx,owner,project,claims[0].ID,101);!errors.Is(err,ErrValidation){
        t.Fatalf("unbounded audit accepted: %v",err)
    }
}
