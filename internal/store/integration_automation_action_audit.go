package store

import (
    "context"
    "database/sql"
    "errors"
    "time"
)

type IntegrationAutomationActionAudit struct {
    Transition string `json:"transition"`
    ErrorClass *string `json:"errorClass,omitempty"`
    CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) ListIntegrationAutomationActionAudit(ctx context.Context,actorID,projectID,actionID int64,limit int)([]IntegrationAutomationActionAudit,error){
    if actionID<=0 || limit<1 || limit>100{return nil,ErrValidation}
    tx,err:=s.db.BeginTx(ctx,&sql.TxOptions{ReadOnly:true})
    if err!=nil{return nil,err}
    defer tx.Rollback()
    if err:=s.checkIntegrationProjectTx(ctx,tx,actorID,projectID,false);err!=nil{return nil,err}
    var id int64
    err=tx.QueryRowContext(ctx,"SELECT id FROM integration_automation_actions WHERE id=? AND project_id=?",actionID,projectID).Scan(&id)
    if errors.Is(err,sql.ErrNoRows){return nil,ErrNotFound}
    if err!=nil{return nil,err}
    rows,err:=tx.QueryContext(ctx,`SELECT transition,error_class,created_at
        FROM integration_automation_action_audit WHERE action_id=? ORDER BY id DESC LIMIT ?`,actionID,limit)
    if err!=nil{return nil,err}
    audit:=make([]IntegrationAutomationActionAudit,0)
    for rows.Next(){
        var item IntegrationAutomationActionAudit
        var reason sql.NullString
        var timestamp int64
        if err:=rows.Scan(&item.Transition,&reason,&timestamp);err!=nil{rows.Close();return nil,err}
        if reason.Valid{v:=reason.String;item.ErrorClass=&v}
        item.CreatedAt=time.UnixMilli(timestamp).UTC()
        audit=append(audit,item)
    }
    err=rows.Err()
    rows.Close()
    if err!=nil{return nil,err}
    if err:=tx.Commit();err!=nil{return nil,err}
    return audit,nil
}
