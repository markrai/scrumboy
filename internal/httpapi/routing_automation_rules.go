package httpapi

import (
    "net/http"

    "scrumboy/internal/store"
)

func (s *Server) handleProjectAutomationRules(w http.ResponseWriter,r *http.Request,rest []string,pc *store.ProjectContext){
    if s.mode!="full"{
        writeError(w,http.StatusNotFound,"NOT_FOUND","not found",nil)
        return
    }
    ctx:=s.requestContext(r)
    actor,ok:=store.UserIDFromContext(ctx)
    if !ok{
        writeStoreErr(w,store.ErrUnauthorized,true)
        return
    }
    if len(rest)<3 || len(rest)>5{
        writeError(w,http.StatusNotFound,"NOT_FOUND","not found",nil)
        return
    }
    if len(rest)==3{
        switch r.Method{
        case http.MethodGet:
            rules,err:=s.store.ListIntegrationAutomationRules(ctx,actor,pc.Project.ID)
            if err!=nil{writeStoreErr(w,err,true);return}
            writeJSON(w,http.StatusOK,map[string]any{
                "rules":rules,"executionAvailable":true,
            })
        case http.MethodPost:
            var in store.CreateIntegrationAutomationRuleInput
            if err:=readJSON(w,r,s.maxBody,&in);err!=nil{return}
            rule,err:=s.store.CreateIntegrationAutomationRule(ctx,actor,pc.Project.ID,in)
            if err!=nil{writeStoreErr(w,err,true);return}
            writeJSON(w,http.StatusCreated,rule)
        default:
            writeError(w,http.StatusMethodNotAllowed,"METHOD_NOT_ALLOWED","method not allowed",nil)
        }
        return
    }
    ruleID,valid:=parseInt64(rest[3])
    if !valid || ruleID<=0 {
        writeValidationError(w,"invalid automation rule ID","invalid_automation_rule_id",nil)
        return
    }
    if len(rest)==5{
        switch rest[4]{
        case "preview":
            if r.Method!=http.MethodPost{
                writeError(w,http.StatusMethodNotAllowed,"METHOD_NOT_ALLOWED","method not allowed",nil)
                return
            }
            var in store.IntegrationAutomationPreviewInput
            if err:=readJSON(w,r,s.maxBody,&in);err!=nil{return}
            preview,err:=s.store.PreviewIntegrationAutomationRule(ctx,actor,pc.Project.ID,ruleID,in)
            if err!=nil{writeStoreErr(w,err,true);return}
            preview.ExecutionAvailable=true
            writeJSON(w,http.StatusOK,preview)
        default:
            writeError(w,http.StatusNotFound,"NOT_FOUND","not found",nil)
        }
        return
    }
    switch r.Method{
    case http.MethodPatch:
        var in store.UpdateIntegrationAutomationRuleInput
        if err:=readJSON(w,r,s.maxBody,&in);err!=nil{return}
        rule,err:=s.store.UpdateIntegrationAutomationRule(ctx,actor,pc.Project.ID,ruleID,in)
        if err!=nil{writeStoreErr(w,err,true);return}
        writeJSON(w,http.StatusOK,rule)
    case http.MethodDelete:
        if err:=s.store.DeleteIntegrationAutomationRule(ctx,actor,pc.Project.ID,ruleID);err!=nil{
            writeStoreErr(w,err,true)
            return
        }
        w.WriteHeader(http.StatusNoContent)
    default:
        writeError(w,http.StatusMethodNotAllowed,"METHOD_NOT_ALLOWED","method not allowed",nil)
    }
}
