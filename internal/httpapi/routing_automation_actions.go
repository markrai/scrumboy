package httpapi

import (
    "net/http"
    "os"
    "strconv"

    "scrumboy/internal/store"
)

func (s *Server) handleProjectAutomationActions(w http.ResponseWriter,r *http.Request,rest []string,pc *store.ProjectContext){
    if s.mode!="full" || (len(rest)!=3 && len(rest)!=5){
        writeError(w,http.StatusNotFound,"NOT_FOUND","not found",nil)
        return
    }
    if r.Method!=http.MethodGet{
        writeError(w,http.StatusMethodNotAllowed,"METHOD_NOT_ALLOWED","method not allowed",nil)
        return
    }
    ctx:=s.requestContext(r)
    actor,ok:=store.UserIDFromContext(ctx)
    if !ok{writeStoreErr(w,store.ErrUnauthorized,true);return}
    if len(rest)==5 && rest[4]!="audit" {
        writeError(w,http.StatusNotFound,"NOT_FOUND","not found",nil)
        return
    }
    var actionID int64
    if len(rest)==5 {
        id,valid:=parseInt64(rest[3])
        if !valid || id<=0 {
            writeValidationError(w,"invalid action ID","invalid_automation_action_id",nil)
            return
        }
        actionID=id
    }
    limit:=30
    if values,exists:=r.URL.Query()["limit"];exists{
        if len(values)!=1{
            writeValidationError(w,"invalid limit","invalid_limit",nil)
            return
        }
        parsed,err:=strconv.Atoi(values[0])
        if err!=nil || parsed<1 || parsed>100{
            writeValidationError(w,"invalid limit","invalid_limit",nil)
            return
        }
        limit=parsed
    }
    if actionID>0 {
        audit,err:=s.store.ListIntegrationAutomationActionAudit(ctx,actor,pc.Project.ID,actionID,limit)
        if err!=nil{writeStoreErr(w,err,true);return}
        writeJSON(w,http.StatusOK,map[string]any{"audit":audit})
        return
    }
    actions,err:=s.store.ListIntegrationAutomationActions(ctx,actor,pc.Project.ID,limit)
    if err!=nil{writeStoreErr(w,err,true);return}
    writeJSON(w,http.StatusOK,map[string]any{
        "actions":actions,"executionAvailable":os.Getenv("SCRUMBOY_GITHUB_AUTOMATION_EXECUTION_ENABLED")=="true",
    })
}
