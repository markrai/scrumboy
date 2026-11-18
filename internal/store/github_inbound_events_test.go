package store

import (
    "context"
    "errors"
    "testing"
    "time"

    "scrumboy/internal/integration"
)

func TestGitHubInboundAdmissionDedupeAndLeases(t *testing.T) {
    st,cleanup:=newTestStore(t)
    defer cleanup()
    ctx:=context.Background()
    owner,err:=st.BootstrapUser(ctx,"gh-inbound-owner@example.com","password123","Owner")
    if err!=nil{t.Fatal(err)}
    event:=GitHubInboundEvent{
        DeliveryID:"b61e607d-2a44-4d9a-8e4f-109e055cfc2d",
        InstallationID:"42",EventType:"repository",Action:"renamed",
        PayloadSHA256:"d1fa9facd8d2cfc79110b943e05c1b05aac3f8e84c49a4e7679d03d8ba9039c1",
    }
    if _,err:=st.AdmitGitHubInbound(ctx,event);!errors.Is(err,ErrNotFound){t.Fatalf("unconnected install: %v",err)}
    installation:=integration.Installation{ID:"42",Account:"example",Kind:"Organization"}
    repo:=integration.Resource{ID:"234",Name:"example/repo",URL:"https://github.com/example/repo"}
    connection,err:=st.SyncGitHubInstallation(ctx,owner.ID,installation,[]integration.Resource{repo})
    if err!=nil{t.Fatal(err)}
    first,err:=st.AdmitGitHubInbound(ctx,event)
    if err!=nil||!first{t.Fatalf("first admit: %v %v",first,err)}
    duplicate,err:=st.AdmitGitHubInbound(ctx,event)
    if err!=nil||duplicate{t.Fatalf("duplicate admit: %v %v",duplicate,err)}
    conflict:=event
    conflict.PayloadSHA256="f1fa9facd8d2cfc79110b943e05c1b05aac3f8e84c49a4e7679d03d8ba9039c1"
    if _,err:=st.AdmitGitHubInbound(ctx,conflict);!errors.Is(err,ErrConflict){t.Fatalf("changed payload accepted: %v",err)}
    now:=time.Now().UTC().Add(2*time.Second)
    claims,err:=st.ClaimGitHubInbound(ctx,now,5,10*time.Second)
    if err!=nil||len(claims)!=1||claims[0].Attempts!=1{t.Fatalf("first claim %+v %v",claims,err)}
    second,err:=st.ClaimGitHubInbound(ctx,now.Add(time.Second),5,10*time.Second)
    if err!=nil||len(second)!=0{t.Fatalf("double claim %+v %v",second,err)}
    ok,err:=st.SettleGitHubInbound(ctx,claims[0].ID,"invalid",true,"",now)
    if !errors.Is(err,ErrValidation)||ok{t.Fatalf("invalid lease token accepted: %v %v",ok,err)}
    ok,err=st.SettleGitHubInbound(ctx,claims[0].ID,claims[0].LeaseToken,false,"provider_unavailable",now)
    if err!=nil||!ok{t.Fatalf("retry: %v %v",ok,err)}
    counts,err:=st.GitHubInboundSummary(ctx)
    if err!=nil||counts.Pending!=1||counts.Leased!=0{t.Fatalf("retry summary %+v %v",counts,err)}
    claims,err=st.ClaimGitHubInbound(ctx,now.Add(2*time.Minute),5,10*time.Second)
    if err!=nil||len(claims)!=1||claims[0].Attempts!=2{t.Fatalf("retry claim %+v %v",claims,err)}
    ok,err=st.SettleGitHubInbound(ctx,claims[0].ID,claims[0].LeaseToken,true,"",now.Add(2*time.Minute))
    if err!=nil||!ok{t.Fatalf("complete %v %v",ok,err)}
    counts,err=st.GitHubInboundSummary(ctx)
    if err!=nil||counts.Completed!=1||counts.Pending!=0{t.Fatalf("completed summary %+v %v",counts,err)}
    if err:=st.DeleteGitHubInstallation(ctx,owner.ID,connection.ID);err!=nil{t.Fatal(err)}
    if _,err:=st.AdmitGitHubInbound(ctx,GitHubInboundEvent{
        DeliveryID:"e61e607d-2a44-4d9a-8e4f-109e055cfc2d",
        InstallationID:"42",EventType:"repository",Action:"edited",
        PayloadSHA256:event.PayloadSHA256,
    });!errors.Is(err,ErrNotFound){t.Fatalf("disconnected installation accepted: %v",err)}
}

func TestGitHubInboundLeaseExpiryAndRetryExhaustion(t *testing.T) {
    st,cleanup:=newTestStore(t)
    defer cleanup()
    ctx:=context.Background()
    owner,err:=st.BootstrapUser(ctx,"gh-inbound-retry@example.com","password123","Owner")
    if err!=nil{t.Fatal(err)}
    inst:=integration.Installation{ID:"44",Account:"example",Kind:"User"}
    if _,err:=st.SyncGitHubInstallation(ctx,owner.ID,inst,nil);err!=nil{t.Fatal(err)}
    event:=GitHubInboundEvent{
        DeliveryID:"a61e607d-2a44-4d9a-8e4f-109e055cfc2d",
        InstallationID:"44",EventType:"installation_repositories",Action:"added",
        PayloadSHA256:"f1fa9facd8d2cfc79110b943e05c1b05aac3f8e84c49a4e7679d03d8ba9039c1",
    }
    if _,err:=st.AdmitGitHubInbound(ctx,event);err!=nil{t.Fatal(err)}
    now:=time.Now().UTC().Add(time.Minute)
    claims,err:=st.ClaimGitHubInbound(ctx,now,1,5*time.Second)
    if err!=nil||len(claims)!=1{t.Fatalf("initial %+v %v",claims,err)}
    recovered,err:=st.ClaimGitHubInbound(ctx,now.Add(6*time.Second),1,5*time.Second)
    if err!=nil||len(recovered)!=1||recovered[0].LeaseToken==claims[0].LeaseToken{t.Fatalf("lease reclaim %+v %v",recovered,err)}
    ok,err:=st.SettleGitHubInbound(ctx,claims[0].ID,claims[0].LeaseToken,true,"",now.Add(6*time.Second))
    if err!=nil||ok{t.Fatalf("stale lease acknowledged %v %v",ok,err)}
    timestamp:=now.Add(6*time.Second)
    for attempt:=2;attempt<=githubInboundMaxAttempts;attempt++ {
        eventClaim:=recovered[0]
        if eventClaim.Attempts!=attempt {t.Fatalf("attempt %d, got %d",attempt,eventClaim.Attempts)}
        ok,err=st.SettleGitHubInbound(ctx,eventClaim.ID,eventClaim.LeaseToken,false,"provider_unavailable",timestamp)
        if err!=nil||!ok{t.Fatalf("settle %d: %v %v",attempt,ok,err)}
        if attempt==githubInboundMaxAttempts{break}
        timestamp=timestamp.Add(time.Hour)
        recovered,err=st.ClaimGitHubInbound(ctx,timestamp,1,5*time.Second)
        if err!=nil||len(recovered)!=1{t.Fatalf("retry %d %+v %v",attempt,recovered,err)}
    }
    summary,err:=st.GitHubInboundSummary(ctx)
    if err!=nil||summary.Dead!=1||summary.Pending!=0{t.Fatalf("terminal summary %+v %v",summary,err)}
    later,err:=st.ClaimGitHubInbound(ctx,timestamp.Add(time.Hour),1,5*time.Second)
    if err!=nil||len(later)!=0{t.Fatalf("claimed dead delivery %+v %v",later,err)}
}

func TestGitHubInboundMetadataReconciliationIsNonDestructive(t *testing.T) {
    st,cleanup:=newTestStore(t)
    defer cleanup()
    ctx:=context.Background()
    owner,err:=st.BootstrapUser(ctx,"gh-inbound-reconcile@example.com","password123","Owner")
    if err!=nil{t.Fatal(err)}
    userCtx:=WithUserID(ctx,owner.ID)
    project,err:=st.CreateProject(userCtx,"Inbound repository reconciliation")
    if err!=nil{t.Fatal(err)}
    card,err:=st.CreateTodo(userCtx,project.ID,CreateTodoInput{Title:"Keep content intact"},ModeFull)
    if err!=nil{t.Fatal(err)}
    inst:=integration.Installation{ID:"101",Account:"example",Kind:"User"}
    repo:=integration.Resource{ID:"500",Name:"example/old",URL:"https://github.com/example/old"}
    conn,err:=st.SyncGitHubInstallation(ctx,owner.ID,inst,[]integration.Resource{repo})
    if err!=nil{t.Fatal(err)}
    resources,err:=st.ListIntegrationResources(ctx,owner.ID,conn.ID)
    if err!=nil||len(resources)!=1{t.Fatalf("initial resources %+v %v",resources,err)}
    resourceID:=resources[0].ID
    if err:=st.AttachIntegrationResource(ctx,owner.ID,project.ID,resourceID);err!=nil{t.Fatal(err)}
    if err:=st.LinkTodoIntegrationResource(ctx,owner.ID,project.ID,card.LocalID,resourceID);err!=nil{t.Fatal(err)}
    renamed:=integration.Resource{ID:"500",Name:"example/new",URL:"https://github.com/example/new"}
    if err:=st.ReconcileGitHubInboundRepositories(ctx,"101",[]integration.Resource{renamed});err!=nil{t.Fatal(err)}
    resources,err=st.ListIntegrationResources(ctx,owner.ID,conn.ID)
    if err!=nil||len(resources)!=1||resources[0].ID!=resourceID||resources[0].Name!=renamed.Name{t.Fatalf("renamed %+v %v",resources,err)}
    if err:=st.ReconcileGitHubInboundRepositories(ctx,"101",nil);err!=nil{t.Fatal(err)}
    links,err:=st.ListTodoIntegrationResources(ctx,owner.ID,project.ID,card.LocalID)
    if err!=nil||len(links)!=1||links[0].ID!=resourceID{t.Fatalf("automatic reconciliation deleted card link %+v %v",links,err)}
    if err:=st.DisableGitHubInboundInstallation(ctx,"101");err!=nil{t.Fatal(err)}
    connected,err:=st.GitHubInboundInstallationConnected(ctx,"101")
    if err!=nil||connected{t.Fatalf("disabled installation still connected %v %v",connected,err)}
    links,err=st.ListTodoIntegrationResources(ctx,owner.ID,project.ID,card.LocalID)
    if err!=nil||len(links)!=1{t.Fatalf("disabled installation removed references %+v %v",links,err)}
}

func TestGitHubInboundDeadDeliveryRequeueIsAdminGated(t *testing.T) {
    st,cleanup:=newTestStore(t)
    defer cleanup()
    ctx:=context.Background()
    owner,err:=st.BootstrapUser(ctx,"gh-inbound-operator@example.com","password123","Owner")
    if err!=nil{t.Fatal(err)}
    nonAdmin,err:=st.CreateUser(ctx,"gh-inbound-viewer@example.com","password123","Viewer")
    if err!=nil{t.Fatal(err)}
    inst:=integration.Installation{ID:"500",Account:"example",Kind:"User"}
    if _,err:=st.SyncGitHubInstallation(ctx,owner.ID,inst,nil);err!=nil{t.Fatal(err)}
    event:=GitHubInboundEvent{
        DeliveryID:"d61e607d-2a44-4d9a-8e4f-109e055cfc2d",
        InstallationID:"500",EventType:"repository",Action:"edited",
        PayloadSHA256:"f1fa9facd8d2cfc79110b943e05c1b05aac3f8e84c49a4e7679d03d8ba9039c1",
    }
    if _,err:=st.AdmitGitHubInbound(ctx,event);err!=nil{t.Fatal(err)}
    now:=time.Now().UTC().Add(time.Minute)
    for attempt:=1;attempt<=githubInboundMaxAttempts;attempt++{
        events,err:=st.ClaimGitHubInbound(ctx,now,1,10*time.Second)
        if err!=nil||len(events)!=1{t.Fatalf("claim attempt %d: %+v %v",attempt,events,err)}
        ok,err:=st.SettleGitHubInbound(ctx,events[0].ID,events[0].LeaseToken,false,"provider_unavailable",now)
        if err!=nil||!ok{t.Fatalf("settle attempt %d: %v %v",attempt,ok,err)}
        now=now.Add(time.Hour)
    }
    if _,err:=st.ListDeadGitHubInbound(ctx,nonAdmin.ID,20);!errors.Is(err,ErrUnauthorized){t.Fatalf("viewer listed dead deliveries: %v",err)}
    dead,err:=st.ListDeadGitHubInbound(ctx,owner.ID,20)
    if err!=nil||len(dead)!=1||dead[0].Attempts!=8{t.Fatalf("dead deliveries %+v %v",dead,err)}
    if err:=st.RequeueGitHubInbound(ctx,nonAdmin.ID,dead[0].ID);!errors.Is(err,ErrUnauthorized){t.Fatalf("viewer requeued dead event: %v",err)}
    if err:=st.RequeueGitHubInbound(ctx,owner.ID,dead[0].ID);err!=nil{t.Fatal(err)}
    if err:=st.RequeueGitHubInbound(ctx,owner.ID,dead[0].ID);!errors.Is(err,ErrNotFound){t.Fatalf("twice requeued event: %v",err)}
    summary,err:=st.GitHubInboundSummary(ctx)
    if err!=nil||summary.Pending!=1||summary.Dead!=0{t.Fatalf("requeue state %+v %v",summary,err)}
}
