package httpapi

import (
    "context"
    "net/http"
    "strconv"
    "testing"

    "scrumboy/internal/store"
)

func TestAutomationExecutionConsentAndProjectHistoryREST(t *testing.T){
    t.Setenv("SCRUMBOY_GITHUB_AUTOMATION_EXECUTION_ENABLED","false")
    ts,db,cleanup:=newTestHTTPServer(t,"full")
    defer cleanup()
    ownerClient:=newCookieClient(t)
    owner:=bootstrapUserClient(t,ownerClient,ts.URL,"Owner","automation-exec-owner@example.com","password123")
    ownerID:=int64(owner["id"].(float64))
    st:=store.New(db,nil)
    ctx:=context.Background()
    project,err:=st.CreateProject(store.WithUserID(ctx,ownerID),"Automation Execute")
    if err!=nil{t.Fatal(err)}
    connection,err:=st.CreateIntegrationConnection(ctx,ownerID,store.CreateIntegrationConnectionInput{
        Provider:"github",ExternalAccountID:"881",Name:"Engineering",
    })
    if err!=nil{t.Fatal(err)}
    repo,err:=st.RegisterIntegrationResource(ctx,ownerID,connection.ID,store.RegisterIntegrationResourceInput{
        Kind:"repository",ExternalID:"442",Name:"engineering/repo",URL:"https://github.com/engineering/repo",
    })
    if err!=nil{t.Fatal(err)}
    if err:=st.AttachIntegrationResource(ctx,ownerID,project.ID,repo.ID);err!=nil{t.Fatal(err)}
    viewer,err:=st.CreateUser(ctx,"automation-exec-viewer@example.com","password123","Viewer")
    if err!=nil{t.Fatal(err)}
    if err:=st.AddProjectMember(store.WithUserID(ctx,ownerID),ownerID,project.ID,viewer.ID,store.RoleViewer);err!=nil{t.Fatal(err)}
    viewerClient:=newCookieClient(t)
    loginUserClient(t,viewerClient,ts.URL,"automation-exec-viewer@example.com","password123")
    outsider,err:=st.CreateUser(ctx,"automation-exec-outsider@example.com","password123","Outsider")
    if err!=nil{t.Fatal(err)}
    _=outsider
    outsiderClient:=newCookieClient(t)
    loginUserClient(t,outsiderClient,ts.URL,"automation-exec-outsider@example.com","password123")

    base:=ts.URL+"/api/board/"+project.Slug+"/integrations/automation-rules"
    var rule store.IntegrationAutomationRule
    resp,body:=doJSON(t,ownerClient,http.MethodPost,base,map[string]any{
        "name":"Project notification","resourceId":repo.ID,"eventType":"pull_request.merged","actionType":"notify",
    },&rule)
    if resp.StatusCode!=http.StatusCreated{t.Fatalf("create %d %s",resp.StatusCode,body)}
    var enabled store.IntegrationAutomationRule
    ruleURL:=base+"/"+strconv.FormatInt(rule.ID,10)
    resp,body=doJSON(t,ownerClient,http.MethodPatch,ruleURL,map[string]any{"enabled":true},&enabled)
    if resp.StatusCode!=http.StatusOK{t.Fatalf("enable %d %s",resp.StatusCode,body)}
    consentURL:=ruleURL+"/execution-approval"
    consent:=map[string]any{"confirm":"APPROVE_NOTIFY_EXECUTION","ruleUpdatedAtMs":enabled.UpdatedAt.UnixMilli()}
    resp,body=doJSON(t,ownerClient,http.MethodPost,consentURL,consent,nil)
    if resp.StatusCode!=http.StatusConflict{t.Fatalf("operator-off consent %d %s",resp.StatusCode,body)}
    t.Setenv("SCRUMBOY_GITHUB_AUTOMATION_EXECUTION_ENABLED","true")
    resp,body=doJSON(t,viewerClient,http.MethodPost,consentURL,consent,nil)
    if resp.StatusCode!=http.StatusForbidden{t.Fatalf("viewer consent %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,outsiderClient,http.MethodPost,consentURL,consent,nil)
    if resp.StatusCode!=http.StatusNotFound{t.Fatalf("outsider consent %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,ownerClient,http.MethodPost,consentURL,map[string]any{
        "confirm":"approve","ruleUpdatedAtMs":enabled.UpdatedAt.UnixMilli(),
    },nil)
    if resp.StatusCode!=http.StatusBadRequest{t.Fatalf("unconfirmed consent %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,ownerClient,http.MethodPost,consentURL,map[string]any{
        "confirm":"APPROVE_NOTIFY_EXECUTION","ruleUpdatedAtMs":enabled.UpdatedAt.UnixMilli()+1,
    },nil)
    if resp.StatusCode!=http.StatusConflict{t.Fatalf("stale consent %d %s",resp.StatusCode,body)}
    var approved store.IntegrationAutomationRule
    resp,body=doJSON(t,ownerClient,http.MethodPost,consentURL,consent,&approved)
    if resp.StatusCode!=http.StatusOK||approved.ExecutionApprovedAt==nil{t.Fatalf("approval %d %s %+v",resp.StatusCode,body,approved)}
    actionsURL:=ts.URL+"/api/board/"+project.Slug+"/integrations/automation-actions"
    resp,body=doJSON(t,viewerClient,http.MethodGet,actionsURL,nil,nil)
    if resp.StatusCode!=http.StatusOK{t.Fatalf("viewer history %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,outsiderClient,http.MethodGet,actionsURL,nil,nil)
    if resp.StatusCode!=http.StatusNotFound{t.Fatalf("outsider history %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,ownerClient,http.MethodGet,actionsURL+"?limit=101",nil,nil)
    if resp.StatusCode!=http.StatusBadRequest{t.Fatalf("unbounded history %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,ownerClient,http.MethodDelete,consentURL,nil,nil)
    if resp.StatusCode!=http.StatusNoContent{t.Fatalf("revoke %d %s",resp.StatusCode,body)}
    t.Setenv("SCRUMBOY_GITHUB_AUTOMATION_EXECUTION_ENABLED","false")
    resp,body=doJSON(t,ownerClient,http.MethodDelete,consentURL,nil,nil)
    if resp.StatusCode!=http.StatusNoContent{t.Fatalf("disabled-operator revoke %d %s",resp.StatusCode,body)}
    listed,err:=st.ListIntegrationAutomationRules(ctx,ownerID,project.ID)
    if err!=nil||len(listed)!=1||listed[0].ExecutionApprovedAt!=nil{t.Fatalf("revoked %+v %v",listed,err)}
}
