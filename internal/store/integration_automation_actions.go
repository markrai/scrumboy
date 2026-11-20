package store

import (
    "context"
    "crypto/rand"
    "database/sql"
    "encoding/hex"
    "errors"
    "time"
)

const maxIntegrationAutomationActions = 5000
const maxIntegrationAutomationActionAttempts = 8

type IntegrationAutomationAction struct {
    ID int64 `json:"id"`
    EventID int64 `json:"eventId"`
    RuleID int64 `json:"ruleId"`
    ResourceID int64 `json:"resourceId"`
    ItemNumber int64 `json:"itemNumber"`
    EventType string `json:"eventType"`
    ActionType string `json:"actionType"`
    Status string `json:"status"`
    Attempts int `json:"attempts"`
    ErrorClass *string `json:"errorClass,omitempty"`
    CreatedAt time.Time `json:"createdAt"`
    CompletedAt *time.Time `json:"completedAt,omitempty"`
}

type IntegrationAutomationActionClaim struct {
    ID int64
    LeaseToken string
}

type IntegrationAutomationActionCounts struct {
    Pending int64 `json:"pending"`
    Leased int64 `json:"leased"`
    Completed int64 `json:"completed"`
    Dead int64 `json:"dead"`
    Cancelled int64 `json:"cancelled"`
}

func (s *Store) ApproveIntegrationAutomationExecution(ctx context.Context, actorID,projectID,ruleID,expectedVersion int64)(IntegrationAutomationRule,error) {
    if actorID<=0 || projectID<=0 || ruleID<=0 || expectedVersion<=0{return IntegrationAutomationRule{},ErrValidation}
    tx,err:=s.db.BeginTx(ctx,nil)
    if err!=nil{return IntegrationAutomationRule{},err}
    defer tx.Rollback()
    if err:=s.checkIntegrationProjectTx(ctx,tx,actorID,projectID,true);err!=nil{return IntegrationAutomationRule{},err}
    rule,err:=automationRuleByIDTx(ctx,tx,projectID,ruleID)
    if err!=nil{return IntegrationAutomationRule{},err}
    if rule.UpdatedAt.UnixMilli()!=expectedVersion{return IntegrationAutomationRule{},ErrConflict}
    if !rule.Enabled || rule.ActionType!="notify"{return IntegrationAutomationRule{},ErrValidation}
    if err:=integrationAutomationResourceTx(ctx,tx,projectID,rule.ResourceID);err!=nil{return IntegrationAutomationRule{},err}
    if _,err:=tx.ExecContext(ctx,"DELETE FROM integration_automation_execution_approvals WHERE rule_id=?",ruleID);err!=nil{
        return IntegrationAutomationRule{},err
    }
    now:=time.Now().UTC().UnixMilli()
    if _,err:=tx.ExecContext(ctx,"INSERT INTO integration_automation_execution_approvals(rule_id,project_id,approved_by_user_id,rule_updated_at,approved_at) VALUES(?,?,?,?,?)",
        ruleID,projectID,actorID,expectedVersion,now);err!=nil{return IntegrationAutomationRule{},err}
    rule,err=automationRuleByIDTx(ctx,tx,projectID,ruleID)
    if err!=nil{return IntegrationAutomationRule{},err}
    if err:=tx.Commit();err!=nil{return IntegrationAutomationRule{},err}
    return rule,nil
}

func (s *Store) RevokeIntegrationAutomationExecution(ctx context.Context,actorID,projectID,ruleID int64)error {
    if actorID<=0 || projectID<=0 || ruleID<=0{return ErrValidation}
    tx,err:=s.db.BeginTx(ctx,nil)
    if err!=nil{return err}
    defer tx.Rollback()
    if err:=s.checkIntegrationProjectTx(ctx,tx,actorID,projectID,true);err!=nil{return err}
    if _,err:=automationRuleByIDTx(ctx,tx,projectID,ruleID);err!=nil{return err}
    if _,err:=tx.ExecContext(ctx,"DELETE FROM integration_automation_execution_approvals WHERE rule_id=?",ruleID);err!=nil{return err}
    return tx.Commit()
}

func (s *Store) ClaimIntegrationAutomationActions(ctx context.Context,now time.Time,limit int,lease time.Duration)([]IntegrationAutomationActionClaim,error) {
    if limit<1 || limit>20 || lease<time.Second || lease>time.Minute{return nil,ErrValidation}
    timestamp:=now.UTC().UnixMilli()
    rows,err:=s.db.QueryContext(ctx,"SELECT id FROM integration_automation_actions WHERE attempts<? AND ((status='pending' AND next_attempt_at<=?) OR (status='leased' AND lease_until<=?)) ORDER BY id LIMIT ?",
        maxIntegrationAutomationActionAttempts,timestamp,timestamp,limit)
    if err!=nil{return nil,err}
    ids:=make([]int64,0,limit)
    for rows.Next(){
        var id int64
        if err:=rows.Scan(&id);err!=nil{rows.Close();return nil,err}
        ids=append(ids,id)
    }
    err=rows.Err()
    rows.Close()
    if err!=nil{return nil,err}
    claims:=make([]IntegrationAutomationActionClaim,0,len(ids))
    for _,id:=range ids {
        b:=make([]byte,16)
        if _,err:=rand.Read(b);err!=nil{return nil,err}
        token:=hex.EncodeToString(b)
        tx,err:=s.db.BeginTx(ctx,nil)
        if err!=nil{return nil,err}
        res,err:=tx.ExecContext(ctx,"UPDATE integration_automation_actions SET status='leased',attempts=attempts+1,lease_token=?,lease_until=?,updated_at=? WHERE id=? AND attempts<? AND ((status='pending' AND next_attempt_at<=?) OR (status='leased' AND lease_until<=?))",
            token,timestamp+lease.Milliseconds(),timestamp,id,maxIntegrationAutomationActionAttempts,timestamp,timestamp)
        if err!=nil{tx.Rollback();return nil,err}
        changed,err:=res.RowsAffected()
        if err!=nil{tx.Rollback();return nil,err}
        if changed==1{
            if _,err:=tx.ExecContext(ctx,"INSERT INTO integration_automation_action_audit(action_id,transition,created_at) VALUES(?,'claimed',?)",id,timestamp);err!=nil{
                tx.Rollback();return nil,err
            }
        }
        if err:=tx.Commit();err!=nil{return nil,err}
        if changed==1{claims=append(claims,IntegrationAutomationActionClaim{ID:id,LeaseToken:token})}
    }
    return claims,nil
}

func (s *Store) SettleIntegrationAutomationAction(ctx context.Context,actionID int64,leaseToken string,now time.Time)(string,error) {
    if actionID<=0 || len(leaseToken)!=32{return "",ErrValidation}
    tx,err:=s.db.BeginTx(ctx,nil)
    if err!=nil{return "",err}
    defer tx.Rollback()
    timestamp:=now.UTC().UnixMilli()
    var leaseID int64
    err=tx.QueryRowContext(ctx,"SELECT id FROM integration_automation_actions WHERE id=? AND status='leased' AND lease_token=? AND lease_until>?",
        actionID,leaseToken,timestamp).Scan(&leaseID)
    if errors.Is(err,sql.ErrNoRows){return "",ErrConflict}
    if err!=nil{return "",err}
    var authorized int
    err=tx.QueryRowContext(ctx,`SELECT EXISTS (
        SELECT 1 FROM integration_automation_actions a
        JOIN integration_github_automation_events e ON e.id=a.event_id
        JOIN integration_automation_rules r ON r.id=a.rule_id
            AND r.project_id=a.project_id AND r.resource_id=a.resource_id
            AND r.updated_at=a.rule_updated_at
        JOIN integration_automation_execution_approvals ap ON ap.id=a.approval_id
            AND ap.rule_id=r.id AND ap.project_id=r.project_id
            AND ap.rule_updated_at=a.rule_updated_at
        JOIN project_members pm ON pm.project_id=ap.project_id
            AND pm.user_id=ap.approved_by_user_id AND pm.role='maintainer'
        JOIN projects p ON p.id=a.project_id AND p.expires_at IS NULL
        JOIN integration_project_resources pr ON pr.project_id=a.project_id
            AND pr.resource_id=a.resource_id
        JOIN integration_resources ir ON ir.id=a.resource_id
        JOIN integration_connections c ON c.id=ir.connection_id
        WHERE a.id=? AND r.enabled=1 AND r.action_type='notify'
          AND a.action_type='notify'
          AND ir.kind='repository' AND ir.availability='available'
          AND ir.external_id=e.repository_external_id
          AND c.provider='github' AND c.status='active'
          AND c.external_account_id=e.installation_id
          AND ap.approved_at<e.created_at
    )`,actionID).Scan(&authorized)
    if err!=nil{return "",err}
    status:="completed"
    transition:="completed"
    var errorClass any
    if authorized!=1{
        status="cancelled"
        transition="cancelled"
        errorClass="approval_or_access_revoked"
    }
    res,err:=tx.ExecContext(ctx,"UPDATE integration_automation_actions SET status=?,lease_token=NULL,lease_until=NULL,error_class=?,completed_at=?,updated_at=? WHERE id=? AND status='leased' AND lease_token=? AND lease_until>?",
        status,errorClass,timestamp,timestamp,actionID,leaseToken,timestamp)
    if err!=nil{return "",err}
    affected,err:=res.RowsAffected()
    if err!=nil{return "",err}
    if affected!=1{return "",ErrConflict}
    if _,err:=tx.ExecContext(ctx,"INSERT INTO integration_automation_action_audit(action_id,transition,error_class,created_at) VALUES(?,?,?,?)",
        actionID,transition,errorClass,timestamp);err!=nil{return "",err}
    if err:=tx.Commit();err!=nil{return "",err}
    return status,nil
}

func (s *Store) FailIntegrationAutomationAction(ctx context.Context,actionID int64,leaseToken string,now time.Time)(bool,error){
    if actionID<=0 || len(leaseToken)!=32{return false,ErrValidation}
    tx,err:=s.db.BeginTx(ctx,nil)
    if err!=nil{return false,err}
    defer tx.Rollback()
    var attempts int
    err=tx.QueryRowContext(ctx,"SELECT attempts FROM integration_automation_actions WHERE id=? AND status='leased' AND lease_token=?",
        actionID,leaseToken).Scan(&attempts)
    if errors.Is(err,sql.ErrNoRows){return false,nil}
    if err!=nil{return false,err}
    timestamp:=now.UTC().UnixMilli()
    status:="pending"
    transition:="retry"
    next:=timestamp+time.Minute.Milliseconds()*int64(1<<uint(attempts-1))
    var completed any
    if attempts>=maxIntegrationAutomationActionAttempts {
        status="dead"
        transition="dead"
        next=timestamp
        completed=timestamp
    }else if next>timestamp+(30*time.Minute).Milliseconds(){
        next=timestamp+(30*time.Minute).Milliseconds()
    }
    res,err:=tx.ExecContext(ctx,"UPDATE integration_automation_actions SET status=?,next_attempt_at=?,error_class='store_unavailable',lease_token=NULL,lease_until=NULL,completed_at=?,updated_at=? WHERE id=? AND status='leased' AND lease_token=?",
        status,next,completed,timestamp,actionID,leaseToken)
    if err!=nil{return false,err}
    affected,err:=res.RowsAffected()
    if err!=nil{return false,err}
    if affected==1{
        if _,err:=tx.ExecContext(ctx,"INSERT INTO integration_automation_action_audit(action_id,transition,error_class,created_at) VALUES(?,?,'store_unavailable',?)",
            actionID,transition,timestamp);err!=nil{return false,err}
    }
    if err:=tx.Commit();err!=nil{return false,err}
    return affected==1,nil
}

func (s *Store) MaintainIntegrationAutomationActions(ctx context.Context,now time.Time)error{
    tx,err:=s.db.BeginTx(ctx,nil)
    if err!=nil{return err}
    defer tx.Rollback()
    timestamp:=now.UTC().UnixMilli()
    if _,err:=tx.ExecContext(ctx,"INSERT INTO integration_automation_action_audit(action_id,transition,error_class,created_at) SELECT id,'dead','lease_retries_exhausted',? FROM integration_automation_actions WHERE status='leased' AND lease_until<=? AND attempts>=?",
        timestamp,timestamp,maxIntegrationAutomationActionAttempts);err!=nil{return err}
    if _,err:=tx.ExecContext(ctx,"UPDATE integration_automation_actions SET status='dead',error_class='lease_retries_exhausted',lease_token=NULL,lease_until=NULL,completed_at=?,updated_at=? WHERE status='leased' AND lease_until<=? AND attempts>=?",
        timestamp,timestamp,timestamp,maxIntegrationAutomationActionAttempts);err!=nil{return err}
    return tx.Commit()
}

func (s *Store) IntegrationAutomationActionSummary(ctx context.Context)(IntegrationAutomationActionCounts,error) {
    var out IntegrationAutomationActionCounts
    rows,err:=s.db.QueryContext(ctx,"SELECT status,COUNT(*) FROM integration_automation_actions GROUP BY status")
    if err!=nil{return out,err}
    for rows.Next(){
        var status string
        var count int64
        if err:=rows.Scan(&status,&count);err!=nil{rows.Close();return out,err}
        switch status {
        case "pending":out.Pending=count
        case "leased":out.Leased=count
        case "completed":out.Completed=count
        case "dead":out.Dead=count
        case "cancelled":out.Cancelled=count
        }
    }
    err=rows.Err()
    rows.Close()
    return out,err
}

func (s *Store) ListIntegrationAutomationActions(ctx context.Context,actorID,projectID int64,limit int)([]IntegrationAutomationAction,error){
    if limit<1 || limit>100{return nil,ErrValidation}
    tx,err:=s.db.BeginTx(ctx,&sql.TxOptions{ReadOnly:true})
    if err!=nil{return nil,err}
    defer tx.Rollback()
    if err:=s.checkIntegrationProjectTx(ctx,tx,actorID,projectID,false);err!=nil{return nil,err}
    rows,err:=tx.QueryContext(ctx,`SELECT a.id,a.event_id,a.rule_id,a.resource_id,e.item_number,e.event_type,
        a.action_type,a.status,a.attempts,a.error_class,a.created_at,a.completed_at
        FROM integration_automation_actions a
        JOIN integration_github_automation_events e ON e.id=a.event_id
        WHERE a.project_id=? ORDER BY a.id DESC LIMIT ?`,projectID,limit)
    if err!=nil{return nil,err}
    actions:=make([]IntegrationAutomationAction,0)
    for rows.Next(){
        var item IntegrationAutomationAction
        var errorClass sql.NullString
        var created int64
        var completed sql.NullInt64
        if err:=rows.Scan(&item.ID,&item.EventID,&item.RuleID,&item.ResourceID,
            &item.ItemNumber,&item.EventType,&item.ActionType,&item.Status,&item.Attempts,
            &errorClass,&created,&completed);err!=nil{rows.Close();return nil,err}
        if errorClass.Valid{v:=errorClass.String;item.ErrorClass=&v}
        item.CreatedAt=time.UnixMilli(created).UTC()
        if completed.Valid{v:=time.UnixMilli(completed.Int64).UTC();item.CompletedAt=&v}
        actions=append(actions,item)
    }
    err=rows.Err()
    rows.Close()
    if err!=nil{return nil,err}
    if err:=tx.Commit();err!=nil{return nil,err}
    return actions,nil
}
