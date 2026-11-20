package httpapi

import (
    "context"
    "net/http"
    "strconv"
    "testing"

    "scrumboy/internal/store"
)

func TestAutomationRuleRESTAuthorizationAndNonExecutingPreview(t *testing.T){
    t.Setenv("SCRUMBOY_GITHUB_AUTOMATION_EXECUTION_ENABLED","false")
    ts,db,cleanup:=newTestHTTPServer(t,"full")
    defer cleanup()
    ownerClient:=newCookieClient(t)
    owner:=bootstrapUserClient(t,ownerClient,ts.URL,"Owner","automation-http-owner@example.com","password123")
    ownerID:=int64(owner["id"].(float64))
    st:=store.New(db,nil)
    ctx:=context.Background()
    ownerCtx:=store.WithUserID(ctx,ownerID)
    project,err:=st.CreateProject(ownerCtx,"Automation HTTP")
    if err!=nil{t.Fatal(err)}
    otherProject,err:=st.CreateProject(ownerCtx,"Other Automation HTTP")
    if err!=nil{t.Fatal(err)}
    conn,err:=st.CreateIntegrationConnection(ctx,ownerID,store.CreateIntegrationConnectionInput{
        Provider:"github",ExternalAccountID:"automation-rest-installation",Name:"Engineering",
    })
    if err!=nil{t.Fatal(err)}
    repo,err:=st.RegisterIntegrationResource(ctx,ownerID,conn.ID,store.RegisterIntegrationResourceInput{
        Kind:"repository",ExternalID:"123",Name:"engineering/project",URL:"https://github.com/engineering/project",
    })
    if err!=nil{t.Fatal(err)}
    if err:=st.AttachIntegrationResource(ctx,ownerID,project.ID,repo.ID);err!=nil{t.Fatal(err)}
    viewer,err:=st.CreateUser(ctx,"automation-rest-viewer@example.com","password123","Viewer")
    if err!=nil{t.Fatal(err)}
    if err:=st.AddProjectMember(ownerCtx,ownerID,project.ID,viewer.ID,store.RoleViewer);err!=nil{t.Fatal(err)}
    viewerClient:=newCookieClient(t)
    loginUserClient(t,viewerClient,ts.URL,"automation-rest-viewer@example.com","password123")
    stranger,err:=st.CreateUser(ctx,"automation-rest-stranger@example.com","password123","Stranger")
    if err!=nil{t.Fatal(err)}
    _=stranger
    strangerClient:=newCookieClient(t)
    loginUserClient(t,strangerClient,ts.URL,"automation-rest-stranger@example.com","password123")
    guestClient:=newCookieClient(t)
    base:=ts.URL+"/api/board/"+project.Slug+"/integrations/automation-rules"
    input:=map[string]any{
        "name":"Notify PR merged","resourceId":repo.ID,"eventType":"pull_request.merged","actionType":"notify",
    }
    resp,body:=doJSON(t,guestClient,http.MethodGet,base,nil,nil)
    if resp.StatusCode!=http.StatusNotFound{t.Fatalf("guest listed private rules %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,strangerClient,http.MethodGet,base,nil,nil)
    if resp.StatusCode!=http.StatusNotFound{t.Fatalf("stranger listed rules %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,viewerClient,http.MethodPost,base,input,nil)
    if resp.StatusCode!=http.StatusForbidden{t.Fatalf("viewer created rule %d %s",resp.StatusCode,body)}
    var created store.IntegrationAutomationRule
    resp,body=doJSON(t,ownerClient,http.MethodPost,base,input,&created)
    if resp.StatusCode!=http.StatusCreated||created.ID<=0||created.Enabled||created.ActionType!="notify"{
        t.Fatalf("created rule %d %s %+v",resp.StatusCode,body,created)
    }
    url:=base+"/"+strconv.FormatInt(created.ID,10)
    var list struct {
        Rules []store.IntegrationAutomationRule `json:"rules"`
        ExecutionAvailable bool `json:"executionAvailable"`
    }
    resp,body=doJSON(t,viewerClient,http.MethodGet,base,nil,&list)
    if resp.StatusCode!=http.StatusOK||len(list.Rules)!=1||!list.ExecutionAvailable{
        t.Fatalf("viewer list %d %s %+v",resp.StatusCode,body,list)
    }
    sample:=map[string]any{"resourceId":repo.ID,"eventType":"pull_request.merged"}
    var preview store.IntegrationAutomationPreview
    resp,body=doJSON(t,viewerClient,http.MethodPost,url+"/preview",sample,&preview)
    if resp.StatusCode!=http.StatusOK||!preview.Matched||preview.Reason!="rule_disabled"||
        !preview.ExecutionAvailable||preview.WouldExecute||!preview.SampleOnly {
        t.Fatalf("preview %d %s %+v",resp.StatusCode,body,preview)
    }
    resp,body=doJSON(t,strangerClient,http.MethodPost,url+"/preview",sample,nil)
    if resp.StatusCode!=http.StatusNotFound{t.Fatalf("nonmember preview %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,viewerClient,http.MethodPatch,url,map[string]any{"enabled":true},nil)
    if resp.StatusCode!=http.StatusForbidden{t.Fatalf("viewer enabled rule %d %s",resp.StatusCode,body)}
    var enabled store.IntegrationAutomationRule
    resp,body=doJSON(t,ownerClient,http.MethodPatch,url,map[string]any{"enabled":true},&enabled)
    if resp.StatusCode!=http.StatusOK||!enabled.Enabled{t.Fatalf("enable %d %s %+v",resp.StatusCode,body,enabled)}
    resp,body=doJSON(t,viewerClient,http.MethodPost,url+"/preview",sample,&preview)
    if resp.StatusCode!=http.StatusOK||!preview.Matched||preview.Reason!="ready_for_new_events"||
        !preview.ExecutionAvailable||preview.WouldExecute{
        t.Fatalf("enabled preview triggered action %d %s %+v",resp.StatusCode,body,preview)
    }
    resp,body=doJSON(t,ownerClient,http.MethodPost,url+"/preview",map[string]any{
        "resourceId":repo.ID,"eventType":"issue.invalid",
    },nil)
    if resp.StatusCode!=http.StatusBadRequest{t.Fatalf("invalid preview %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,ownerClient,http.MethodGet,url+"/preview",nil,nil)
    if resp.StatusCode!=http.StatusMethodNotAllowed{t.Fatalf("preview GET %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,ownerClient,http.MethodPatch,
        base+"/9999999",map[string]any{"enabled":true},nil)
    if resp.StatusCode!=http.StatusNotFound{t.Fatalf("missing rule patch %d %s",resp.StatusCode,body)}
    otherURL:=ts.URL+"/api/board/"+otherProject.Slug+"/integrations/automation-rules/"+strconv.FormatInt(created.ID,10)
    resp,body=doJSON(t,ownerClient,http.MethodPatch,otherURL,map[string]any{"enabled":false},nil)
    if resp.StatusCode!=http.StatusNotFound{t.Fatalf("cross-project rule patch %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,viewerClient,http.MethodDelete,url,nil,nil)
    if resp.StatusCode!=http.StatusForbidden{t.Fatalf("viewer deleted rule %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,ownerClient,http.MethodDelete,url,nil,nil)
    if resp.StatusCode!=http.StatusNoContent{t.Fatalf("delete %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,ownerClient,http.MethodGet,base,nil,&list)
    if resp.StatusCode!=http.StatusOK||len(list.Rules)!=0{
        t.Fatalf("deleted rule still listed %d %s",resp.StatusCode,body)
    }
}

func TestAutomationRuleRESTCannotReferenceUnattachedRepository(t *testing.T){
    ts,db,cleanup:=newTestHTTPServer(t,"full")
    defer cleanup()
    client:=newCookieClient(t)
    owner:=bootstrapUserClient(t,client,ts.URL,"Owner","automation-unattached@example.com","password123")
    ownerID:=int64(owner["id"].(float64))
    st:=store.New(db,nil)
    ctx:=context.Background()
    project,err:=st.CreateProject(store.WithUserID(ctx,ownerID),"Unattached Automation")
    if err!=nil{t.Fatal(err)}
    conn,err:=st.CreateIntegrationConnection(ctx,ownerID,store.CreateIntegrationConnectionInput{
        Provider:"github",ExternalAccountID:"autom-unattached",Name:"Automation Account",
    })
    if err!=nil{t.Fatal(err)}
    resource,err:=st.RegisterIntegrationResource(ctx,ownerID,conn.ID,store.RegisterIntegrationResourceInput{
        Kind:"repository",ExternalID:"123",Name:"engineering/secret",URL:"https://github.com/engineering/secret",
    })
    if err!=nil{t.Fatal(err)}
    base:=ts.URL+"/api/board/"+project.Slug+"/integrations/automation-rules"
    resp,body:=doJSON(t,client,http.MethodPost,base,map[string]any{
        "resourceId":resource.ID,"name":"No access","eventType":"issue.closed","actionType":"notify",
    },nil)
    if resp.StatusCode!=http.StatusNotFound{t.Fatalf("unattached repo rule created %d %s",resp.StatusCode,body)}
}
