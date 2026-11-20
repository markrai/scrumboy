package httpapi

import (
    "context"
    "net/http"
    "strconv"
    "testing"
    "time"

    "scrumboy/internal/store"
)

func TestAutomationMatchHistoryIsReadOnlyAndProjectScoped(t *testing.T){
    ts,db,cleanup:=newTestHTTPServer(t,"full")
    defer cleanup()
    ownerClient:=newCookieClient(t)
    owner:=bootstrapUserClient(t,ownerClient,ts.URL,"Owner","match-route-owner@example.com","password123")
    ownerID:=int64(owner["id"].(float64))
    st:=store.New(db,nil)
    ctx:=context.Background()
    ownerCtx:=store.WithUserID(ctx,ownerID)
    project,err:=st.CreateProject(ownerCtx,"Project With Matches")
    if err!=nil{t.Fatal(err)}
    other,err:=st.CreateProject(ownerCtx,"Unrelated Match Project")
    if err!=nil{t.Fatal(err)}
    conn,err:=st.CreateIntegrationConnection(ctx,ownerID,store.CreateIntegrationConnectionInput{
        Provider:"github",ExternalAccountID:"881",Name:"Engineering",
    })
    if err!=nil{t.Fatal(err)}
    repo,err:=st.RegisterIntegrationResource(ctx,ownerID,conn.ID,store.RegisterIntegrationResourceInput{
        Kind:"repository",ExternalID:"442",Name:"engineering/work",URL:"https://github.com/engineering/work",
    })
    if err!=nil{t.Fatal(err)}
    if err:=st.AttachIntegrationResource(ctx,ownerID,project.ID,repo.ID);err!=nil{t.Fatal(err)}
    rule,err:=st.CreateIntegrationAutomationRule(ctx,ownerID,project.ID,
        store.CreateIntegrationAutomationRuleInput{
            ResourceID:repo.ID,Name:"Merged PR match",EventType:"pull_request.merged",ActionType:"notify",
        })
    if err!=nil{t.Fatal(err)}
    enabled:=true
    if _,err:=st.UpdateIntegrationAutomationRule(ctx,ownerID,project.ID,rule.ID,
        store.UpdateIntegrationAutomationRuleInput{Enabled:&enabled});err!=nil{t.Fatal(err)}
    event:=store.GitHubAutomationEvent{
        DeliveryID:"b61e607d-2a44-4d9a-8e4f-109e055cfc2d",
        InstallationID:"881",RepositoryExternalID:"442",ItemNumber:357,
        EventType:"pull_request.merged",
        PayloadSHA256:"d1fa9facd8d2cfc79110b943e05c1b05aac3f8e84c49a4e7679d03d8ba9039c1",
    }
    if _,err:=st.AdmitGitHubAutomation(ctx,event);err!=nil{t.Fatal(err)}
    now:=time.Now().UTC().Add(time.Second)
    events,err:=st.ClaimGitHubAutomation(ctx,now,5,45*time.Second)
    if err!=nil || len(events)!=1{t.Fatalf("claim %+v %v",events,err)}
    if count,err:=st.MatchAndCompleteGitHubAutomation(ctx,events[0].ID,events[0].LeaseToken,now.Add(time.Second));err!=nil||count!=1{
        t.Fatalf("match %d %v",count,err)
    }
    viewer,err:=st.CreateUser(ctx,"match-route-viewer@example.com","password123","Viewer")
    if err!=nil{t.Fatal(err)}
    if err:=st.AddProjectMember(ownerCtx,ownerID,project.ID,viewer.ID,store.RoleViewer);err!=nil{t.Fatal(err)}
    viewerClient:=newCookieClient(t)
    loginUserClient(t,viewerClient,ts.URL,"match-route-viewer@example.com","password123")
    stranger,err:=st.CreateUser(ctx,"match-route-stranger@example.com","password123","Stranger")
    if err!=nil{t.Fatal(err)}
    _=stranger
    strangerClient:=newCookieClient(t)
    loginUserClient(t,strangerClient,ts.URL,"match-route-stranger@example.com","password123")
    guestClient:=newCookieClient(t)
    base:=ts.URL+"/api/board/"+project.Slug+"/integrations/automation-matches"
    var result struct{
        Matches []store.GitHubAutomationMatch `json:"matches"`
        ExecutionAvailable bool `json:"executionAvailable"`
    }
    resp,body:=doJSON(t,viewerClient,http.MethodGet,base+"?limit=1",nil,&result)
    if resp.StatusCode!=http.StatusOK||len(result.Matches)!=1||!result.ExecutionAvailable||
        result.Matches[0].RuleID!=rule.ID||result.Matches[0].WouldExecute{
        t.Fatalf("viewer match list %d %s %+v",resp.StatusCode,body,result)
    }
    resp,body=doJSON(t,strangerClient,http.MethodGet,base,nil,nil)
    if resp.StatusCode!=http.StatusNotFound{t.Fatalf("stranger accessed matches %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,guestClient,http.MethodGet,base,nil,nil)
    if resp.StatusCode!=http.StatusNotFound{t.Fatalf("anonymous accessed matches %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,viewerClient,http.MethodGet,base+"?limit=101",nil,nil)
    if resp.StatusCode!=http.StatusBadRequest{t.Fatalf("unbounded receipt listing %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,viewerClient,http.MethodDelete,base,nil,nil)
    if resp.StatusCode!=http.StatusMethodNotAllowed{t.Fatalf("receipt mutation exposed %d %s",resp.StatusCode,body)}
    otherBase:=ts.URL+"/api/board/"+other.Slug+"/integrations/automation-matches"
    var otherResult struct{Matches []store.GitHubAutomationMatch `json:"matches"`}
    resp,body=doJSON(t,ownerClient,http.MethodGet,otherBase,nil,&otherResult)
    if resp.StatusCode!=http.StatusOK||len(otherResult.Matches)!=0{
        t.Fatalf("other project leaked matches %d %s",resp.StatusCode,body)
    }
    resp,body=doJSON(t,viewerClient,http.MethodGet,base+"/"+strconv.FormatInt(rule.ID,10),nil,nil)
    if resp.StatusCode!=http.StatusNotFound{t.Fatalf("undeclared receipt details exposed %d %s",resp.StatusCode,body)}
}
