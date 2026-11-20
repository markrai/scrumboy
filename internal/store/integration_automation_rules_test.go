package store

import (
    "context"
    "errors"
    "fmt"
    "testing"

    "scrumboy/internal/integration"
)

func TestIntegrationAutomationRuleAuthorizationAndPreviewAreNonExecuting(t *testing.T) {
    st,cleanup:=newTestStore(t)
    defer cleanup()
    ctx:=context.Background()
    owner,err:=st.BootstrapUser(ctx,"automation-owner@example.com","password123","Owner")
    if err!=nil{t.Fatal(err)}
    maintainer,err:=st.CreateUser(ctx,"automation-maintainer@example.com","password123","Maintainer")
    if err!=nil{t.Fatal(err)}
    viewer,err:=st.CreateUser(ctx,"automation-viewer@example.com","password123","Viewer")
    if err!=nil{t.Fatal(err)}
    outsider,err:=st.CreateUser(ctx,"automation-outsider@example.com","password123","Outsider")
    if err!=nil{t.Fatal(err)}
    ownerCtx:=WithUserID(ctx,owner.ID)
    project,err:=st.CreateProject(ownerCtx,"Automation Test")
    if err!=nil{t.Fatal(err)}
    other,err:=st.CreateProject(ownerCtx,"Other Automation")
    if err!=nil{t.Fatal(err)}
    if err:=st.AddProjectMember(ownerCtx,owner.ID,project.ID,maintainer.ID,RoleMaintainer);err!=nil{t.Fatal(err)}
    if err:=st.AddProjectMember(ownerCtx,owner.ID,project.ID,viewer.ID,RoleViewer);err!=nil{t.Fatal(err)}
    conn,err:=st.SyncGitHubInstallation(ctx,owner.ID,integration.Installation{
        ID:"222",Account:"engineering",Kind:"Organization",
    },[]integration.Resource{
        {ID:"123",Name:"engineering/repo",URL:"https://github.com/engineering/repo"},
        {ID:"124",Name:"engineering/other",URL:"https://github.com/engineering/other"},
    })
    if err!=nil{t.Fatal(err)}
    resources,err:=st.ListIntegrationResources(ctx,owner.ID,conn.ID)
    if err!=nil||len(resources)!=2{t.Fatalf("resources %+v %v",resources,err)}
    resourceID:=resources[0].ID
    otherResource:=resources[1].ID
    if err:=st.AttachIntegrationResource(ctx,owner.ID,project.ID,resourceID);err!=nil{t.Fatal(err)}
    if err:=st.AttachIntegrationResource(ctx,owner.ID,other.ID,otherResource);err!=nil{t.Fatal(err)}
    input:=CreateIntegrationAutomationRuleInput{
        ResourceID:resourceID,Name:"Notify when PR merges",
        EventType:"pull_request.merged",ActionType:"notify",
    }
    if _,err:=st.CreateIntegrationAutomationRule(ctx,viewer.ID,project.ID,input);!errors.Is(err,ErrForbidden){
        t.Fatalf("viewer created automation: %v",err)
    }
    if _,err:=st.CreateIntegrationAutomationRule(ctx,outsider.ID,project.ID,input);!errors.Is(err,ErrNotFound){
        t.Fatalf("nonmember created automation: %v",err)
    }
    if _,err:=st.CreateIntegrationAutomationRule(ctx,maintainer.ID,other.ID,input);!errors.Is(err,ErrNotFound){
        t.Fatalf("maintainer created in another project: %v",err)
    }
    input.ResourceID=otherResource
    if _,err:=st.CreateIntegrationAutomationRule(ctx,maintainer.ID,project.ID,input);!errors.Is(err,ErrNotFound){
        t.Fatalf("unattached repo granted access: %v",err)
    }
    input.ResourceID=resourceID
    rule,err:=st.CreateIntegrationAutomationRule(ctx,maintainer.ID,project.ID,input)
    if err!=nil{t.Fatal(err)}
    if _,err:=st.db.ExecContext(ctx,"UPDATE integration_automation_rules SET execution_approved_at=123 WHERE id=?",rule.ID);err==nil {
        t.Fatal("M5.A schema accepted execution activation without a future consent migration")
    }

    if rule.Enabled || rule.ActionType!="notify"||rule.CreatedByUserID==nil||*rule.CreatedByUserID!=maintainer.ID{
        t.Fatalf("new rule should be disabled and owned %+v",rule)
    }
    rules,err:=st.ListIntegrationAutomationRules(ctx,viewer.ID,project.ID)
    if err!=nil||len(rules)!=1||rules[0].ID!=rule.ID{t.Fatalf("viewer rule list %+v %v",rules,err)}
    if _,err:=st.ListIntegrationAutomationRules(ctx,outsider.ID,project.ID);!errors.Is(err,ErrNotFound){
        t.Fatalf("nonmember enumerated rules %v",err)
    }
    sample:=IntegrationAutomationPreviewInput{ResourceID:resourceID,EventType:"pull_request.merged"}
    preview,err:=st.PreviewIntegrationAutomationRule(ctx,viewer.ID,project.ID,rule.ID,sample)
    if err!=nil{t.Fatal(err)}
    if !preview.Matched||preview.Reason!="rule_disabled"||preview.WouldExecute||preview.ExecutionAvailable||!preview.SampleOnly{
        t.Fatalf("preview was not safe %+v",preview)
    }
    if _,err:=st.PreviewIntegrationAutomationRule(ctx,outsider.ID,project.ID,rule.ID,sample);!errors.Is(err,ErrNotFound){
        t.Fatalf("nonmember preview leaked rule %v",err)
    }
    if _,err:=st.PreviewIntegrationAutomationRule(ctx,owner.ID,other.ID,rule.ID,sample);!errors.Is(err,ErrNotFound){
        t.Fatalf("cross-project rule preview leaked %v",err)
    }
    enabled:=true
    if _,err:=st.UpdateIntegrationAutomationRule(ctx,viewer.ID,project.ID,rule.ID,UpdateIntegrationAutomationRuleInput{Enabled:&enabled});!errors.Is(err,ErrForbidden){
        t.Fatalf("viewer enabled automation %v",err)
    }
    rule,err=st.UpdateIntegrationAutomationRule(ctx,maintainer.ID,project.ID,rule.ID,
        UpdateIntegrationAutomationRuleInput{Enabled:&enabled})
    if err!=nil||!rule.Enabled{t.Fatalf("maintainer enable %+v %v",rule,err)}
    preview,err=st.PreviewIntegrationAutomationRule(ctx,viewer.ID,project.ID,rule.ID,sample)
    if err!=nil||!preview.Matched||preview.Reason!="execution_not_approved"||preview.WouldExecute||preview.ExecutionAvailable{
        t.Fatalf("enabled rule would execute unexpectedly %+v %v",preview,err)
    }
    mismatch,err:=st.PreviewIntegrationAutomationRule(ctx,owner.ID,project.ID,rule.ID,
        IntegrationAutomationPreviewInput{ResourceID:resourceID,EventType:"issue.closed"})
    if err!=nil||mismatch.Matched||mismatch.Reason!="sample_does_not_match"{
        t.Fatalf("mismatched event triggered %+v %v",mismatch,err)
    }
    if err:=st.DeleteIntegrationAutomationRule(ctx,viewer.ID,project.ID,rule.ID);!errors.Is(err,ErrForbidden){
        t.Fatalf("viewer deleted automation %v",err)
    }
    if err:=st.DeleteIntegrationAutomationRule(ctx,owner.ID,other.ID,rule.ID);!errors.Is(err,ErrNotFound){
        t.Fatalf("cross-project delete worked %v",err)
    }
    if err:=st.DeleteIntegrationAutomationRule(ctx,maintainer.ID,project.ID,rule.ID);err!=nil{t.Fatal(err)}
    if err:=st.DeleteIntegrationAutomationRule(ctx,maintainer.ID,project.ID,rule.ID);!errors.Is(err,ErrNotFound){
        t.Fatalf("duplicate delete succeeded %v",err)
    }
}

func TestIntegrationAutomationRulesValidateInputsAndDetachCascades(t *testing.T){
    st,cleanup:=newTestStore(t)
    defer cleanup()
    ctx:=context.Background()
    owner,err:=st.BootstrapUser(ctx,"automation-validator@example.com","password123","Owner")
    if err!=nil{t.Fatal(err)}
    project,err:=st.CreateProject(WithUserID(ctx,owner.ID),"Automation Validation")
    if err!=nil{t.Fatal(err)}
    conn,err:=st.SyncGitHubInstallation(ctx,owner.ID,integration.Installation{
        ID:"333",Account:"engineering",Kind:"Organization",
    },[]integration.Resource{{ID:"123",Name:"engineering/repo",URL:"https://github.com/engineering/repo"}})
    if err!=nil{t.Fatal(err)}
    repos,err:=st.ListIntegrationResources(ctx,owner.ID,conn.ID)
    if err!=nil||len(repos)!=1{t.Fatal("expected repository",err)}
    resourceID:=repos[0].ID
    if err:=st.AttachIntegrationResource(ctx,owner.ID,project.ID,resourceID);err!=nil{t.Fatal(err)}
    good:=CreateIntegrationAutomationRuleInput{
        ResourceID:resourceID,Name:"Notify issue closed",EventType:"issue.closed",ActionType:"notify",
    }
    cases:=[]CreateIntegrationAutomationRuleInput{
        {ResourceID:resourceID,Name:"X",EventType:"issue.closed",ActionType:"write_card"},
        {ResourceID:resourceID,Name:"X",EventType:"custom.event",ActionType:"notify"},
        {ResourceID:resourceID,Name:" ",EventType:"issue.closed",ActionType:"notify"},
        {ResourceID:0,Name:"X",EventType:"issue.closed",ActionType:"notify"},
        {ResourceID:resourceID,Name:"X\nInjected",EventType:"issue.closed",ActionType:"notify"},
    }
    for _,in:=range cases{
        if _,err:=st.CreateIntegrationAutomationRule(ctx,owner.ID,project.ID,in);!errors.Is(err,ErrValidation){
            t.Fatalf("invalid rule %+v accepted: %v",in,err)
        }
    }
    rule,err:=st.CreateIntegrationAutomationRule(ctx,owner.ID,project.ID,good)
    if err!=nil{t.Fatal(err)}
    missingResource:=int64(987654)
    if _,err:=st.UpdateIntegrationAutomationRule(ctx,owner.ID,project.ID,rule.ID,
        UpdateIntegrationAutomationRuleInput{ResourceID:&missingResource});!errors.Is(err,ErrNotFound){
        t.Fatalf("invalid repo swap accepted %v",err)
    }
    invalidEvent:="issue.unknown"
    if _,err:=st.UpdateIntegrationAutomationRule(ctx,owner.ID,project.ID,rule.ID,
        UpdateIntegrationAutomationRuleInput{EventType:&invalidEvent});!errors.Is(err,ErrValidation){
        t.Fatalf("invalid event accepted %v",err)
    }
    if _,err:=st.UpdateIntegrationAutomationRule(ctx,owner.ID,project.ID,rule.ID,
        UpdateIntegrationAutomationRuleInput{});!errors.Is(err,ErrValidation){
        t.Fatalf("empty patch accepted %v",err)
    }
    for _,event:=range []string{"issue.opened","pull_request.opened","pull_request.closed","pull_request.merged"} {
        in:=good
        in.Name=fmt.Sprintf("Auto %s",event)
        in.EventType=event
        if _,err:=st.CreateIntegrationAutomationRule(ctx,owner.ID,project.ID,in);err!=nil{t.Fatal(err)}
    }
    before,err:=st.ListIntegrationAutomationRules(ctx,owner.ID,project.ID)
    if err!=nil||len(before)!=5{t.Fatalf("unexpected rule count %d %v",len(before),err)}
    if err:=st.DetachIntegrationResource(ctx,owner.ID,project.ID,resourceID);err!=nil{t.Fatal(err)}
    after,err:=st.ListIntegrationAutomationRules(ctx,owner.ID,project.ID)
    if err!=nil||len(after)!=0{t.Fatalf("detached repo retained rules %+v %v",after,err)}
}

func TestIntegrationAutomationRuleCountBound(t *testing.T){
    st,cleanup:=newTestStore(t)
    defer cleanup()
    ctx:=context.Background()
    owner,err:=st.BootstrapUser(ctx,"automation-limit@example.com","password123","Owner")
    if err!=nil{t.Fatal(err)}
    project,err:=st.CreateProject(WithUserID(ctx,owner.ID),"Automation Limits")
    if err!=nil{t.Fatal(err)}
    conn,err:=st.SyncGitHubInstallation(ctx,owner.ID,integration.Installation{
        ID:"444",Account:"engineering",Kind:"Organization",
    },[]integration.Resource{{ID:"123",Name:"engineering/repo",URL:"https://github.com/engineering/repo"}})
    if err!=nil{t.Fatal(err)}
    repos,err:=st.ListIntegrationResources(ctx,owner.ID,conn.ID)
    if err!=nil||len(repos)!=1{t.Fatal("missing repo",err)}
    if err:=st.AttachIntegrationResource(ctx,owner.ID,project.ID,repos[0].ID);err!=nil{t.Fatal(err)}
    for n:=0;n<maxIntegrationAutomationRules;n++{
        _,err:=st.CreateIntegrationAutomationRule(ctx,owner.ID,project.ID,CreateIntegrationAutomationRuleInput{
            ResourceID:repos[0].ID,Name:fmt.Sprintf("Rule %d",n),EventType:"issue.closed",ActionType:"notify",
        })
        if err!=nil{t.Fatalf("rule %d failed %v",n,err)}
    }
    if _,err:=st.CreateIntegrationAutomationRule(ctx,owner.ID,project.ID,CreateIntegrationAutomationRuleInput{
        ResourceID:repos[0].ID,Name:"Too many",EventType:"issue.closed",ActionType:"notify",
    });!errors.Is(err,ErrValidation){t.Fatalf("accepted unbounded rules: %v",err)}
}

func TestIntegrationAutomationRuleDisableSurvivesGitHubRevocation(t *testing.T){
    st,cleanup:=newTestStore(t)
    defer cleanup()
    ctx:=context.Background()
    owner,err:=st.BootstrapUser(ctx,"automation-outage-owner@example.com","password123","Owner")
    if err!=nil{t.Fatal(err)}
    project,err:=st.CreateProject(WithUserID(ctx,owner.ID),"Outage Automation")
    if err!=nil{t.Fatal(err)}
    install:=integration.Installation{ID:"555",Account:"engineering",Kind:"Organization"}
    repo:=integration.Resource{ID:"123",Name:"engineering/repo",URL:"https://github.com/engineering/repo"}
    conn,err:=st.SyncGitHubInstallation(ctx,owner.ID,install,[]integration.Resource{repo})
    if err!=nil{t.Fatal(err)}
    resources,err:=st.ListIntegrationResources(ctx,owner.ID,conn.ID)
    if err!=nil||len(resources)!=1{t.Fatal("missing repo",err)}
    resourceID:=resources[0].ID
    if err:=st.AttachIntegrationResource(ctx,owner.ID,project.ID,resourceID);err!=nil{t.Fatal(err)}
    rule,err:=st.CreateIntegrationAutomationRule(ctx,owner.ID,project.ID,
        CreateIntegrationAutomationRuleInput{ResourceID:resourceID,Name:"Notify",EventType:"issue.closed",ActionType:"notify"})
    if err!=nil{t.Fatal(err)}
    enabled:=true
    if _,err:=st.UpdateIntegrationAutomationRule(ctx,owner.ID,project.ID,rule.ID,
        UpdateIntegrationAutomationRuleInput{Enabled:&enabled});err!=nil{t.Fatal(err)}
    if _,err:=st.SyncGitHubInstallation(ctx,owner.ID,install,nil);err!=nil{t.Fatal(err)}
    preview,err:=st.PreviewIntegrationAutomationRule(ctx,owner.ID,project.ID,rule.ID,
        IntegrationAutomationPreviewInput{ResourceID:resourceID,EventType:"issue.closed"})
    if err!=nil||preview.Matched||preview.Reason!="resource_unavailable"||preview.WouldExecute{
        t.Fatalf("revoked repo matched active rule %+v %v",preview,err)
    }
    if _,err:=st.UpdateIntegrationAutomationRule(ctx,owner.ID,project.ID,rule.ID,
        UpdateIntegrationAutomationRuleInput{Enabled:&enabled});!errors.Is(err,ErrNotFound){
        t.Fatalf("re-enabled unavailable repository rule: %v",err)
    }
    enabled=false
    updated,err:=st.UpdateIntegrationAutomationRule(ctx,owner.ID,project.ID,rule.ID,
        UpdateIntegrationAutomationRuleInput{Enabled:&enabled})
    if err!=nil||updated.Enabled{t.Fatalf("cannot disable inaccessible repo rule %+v %v",updated,err)}
    if _,err:=st.SyncGitHubInstallation(ctx,owner.ID,install,[]integration.Resource{repo});err!=nil{t.Fatal(err)}
    updated,err=st.UpdateIntegrationAutomationRule(ctx,owner.ID,project.ID,rule.ID,
        UpdateIntegrationAutomationRuleInput{Enabled:&enabled})
    if err!=nil||updated.Enabled{t.Fatalf("disabled rule was silently re-enabled %+v %v",updated,err)}
}
