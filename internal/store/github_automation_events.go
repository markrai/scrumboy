package store

import (
    "context"
    "crypto/rand"
    "database/sql"
    "encoding/hex"
    "errors"
    "fmt"
    "strconv"
    "strings"
    "time"
)

var ErrGitHubAutomationFull = errors.New("github automation inbox is full")

const githubAutomationMaxOutstanding = 5000
const githubAutomationMaxAttempts = 8
const githubAutomationMaxMatchesPerEvent = 1000
const githubAutomationRetention = 30 * 24 * time.Hour

type GitHubAutomationEvent struct {
    ID int64
    DeliveryID string
    InstallationID string
    RepositoryExternalID string
    ItemNumber int64
    EventType string
    PayloadSHA256 string
    Attempts int
    LeaseToken string
}

type GitHubAutomationCounts struct {
    Pending int64 `json:"pending"`
    Leased int64 `json:"leased"`
    Completed int64 `json:"completed"`
    Dead int64 `json:"dead"`
    Matches int64 `json:"matches"`
    Actions IntegrationAutomationActionCounts `json:"actions"`
}

type GitHubAutomationMatch struct {
    ID int64 `json:"id"`
    EventID int64 `json:"eventId"`
    RuleID int64 `json:"ruleId"`
    ResourceID int64 `json:"resourceId"`
    ItemNumber int64 `json:"itemNumber"`
    EventType string `json:"eventType"`
    RuleUpdatedAt int64 `json:"ruleUpdatedAt"`
    ActionType string `json:"actionType"`
    MatchedAt time.Time `json:"matchedAt"`
    ExecutionAvailable bool `json:"executionAvailable"`
    WouldExecute bool `json:"wouldExecute"`
}

func validGitHubAutomationEvent(event GitHubAutomationEvent) bool {
    installation,err:=strconv.ParseInt(event.InstallationID,10,64)
    if err!=nil || installation<=0 || strconv.FormatInt(installation,10)!=event.InstallationID{return false}
    repository,err:=strconv.ParseInt(event.RepositoryExternalID,10,64)
    if err!=nil || repository<=0 || strconv.FormatInt(repository,10)!=event.RepositoryExternalID{return false}
    if event.ItemNumber<=0 || event.ItemNumber>2147483647 || !validIntegrationAutomationEvent(event.EventType) ||
        len(event.DeliveryID)<8 || len(event.DeliveryID)>128 || len(event.PayloadSHA256)!=64{return false}
    for _,b:=range event.DeliveryID {
        if !((b>='0' && b<='9') || (b>='a' && b<='f') || (b>='A' && b<='F') || b=='-'){
            return false
        }
    }
    digest,err:=hex.DecodeString(event.PayloadSHA256)
    return err==nil && len(digest)==32
}

func (s *Store) AdmitGitHubAutomation(ctx context.Context,event GitHubAutomationEvent)(bool,error){
    if !validGitHubAutomationEvent(event){return false,fmt.Errorf("%w: invalid GitHub automation event",ErrValidation)}
    tx,err:=s.db.BeginTx(ctx,nil)
    if err!=nil{return false,err}
    defer tx.Rollback()
    var digest string
    err=tx.QueryRowContext(ctx,`SELECT payload_sha256 FROM integration_github_automation_events WHERE delivery_id=?`,
        event.DeliveryID).Scan(&digest)
    if err==nil {
        if !strings.EqualFold(digest,event.PayloadSHA256){return false,ErrConflict}
        return false,tx.Commit()
    }
    if !errors.Is(err,sql.ErrNoRows){return false,err}
    err=tx.QueryRowContext(ctx,`SELECT payload_sha256 FROM integration_github_inbound_events WHERE delivery_id=?`,
        event.DeliveryID).Scan(&digest)
    if err==nil{return false,ErrConflict}
    if !errors.Is(err,sql.ErrNoRows){return false,err}
    var resourceID int64
    err=tx.QueryRowContext(ctx,`SELECT r.id FROM integration_resources r
        JOIN integration_connections c ON c.id=r.connection_id
        WHERE c.provider='github' AND c.status='active'
          AND c.external_account_id=? AND r.kind='repository'
          AND r.external_id=? AND r.availability='available'`,
        event.InstallationID,event.RepositoryExternalID).Scan(&resourceID)
    if errors.Is(err,sql.ErrNoRows){return false,ErrNotFound}
    if err!=nil{return false,err}
    var outstanding int
    err=tx.QueryRowContext(ctx,`SELECT COUNT(*) FROM integration_github_automation_events
        WHERE status IN ('pending','leased')`).Scan(&outstanding)
    if err!=nil{return false,err}
    if outstanding>=githubAutomationMaxOutstanding{return false,ErrGitHubAutomationFull}
    now:=time.Now().UTC().UnixMilli()
    _,err=tx.ExecContext(ctx,`INSERT INTO integration_github_automation_events(
        delivery_id,installation_id,repository_external_id,item_number,event_type,payload_sha256,
        next_attempt_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
        event.DeliveryID,event.InstallationID,event.RepositoryExternalID,event.ItemNumber,
        event.EventType,strings.ToLower(event.PayloadSHA256),now,now,now)
    if err!=nil{return false,err}
    return true,tx.Commit()
}

func (s *Store) ClaimGitHubAutomation(ctx context.Context,now time.Time,limit int,lease time.Duration)([]GitHubAutomationEvent,error){
    if limit<1 || limit>20 || lease<time.Second || lease>time.Minute{return nil,ErrValidation}
    timestamp:=now.UTC().UnixMilli()
    _,err:=s.db.ExecContext(ctx,`UPDATE integration_github_automation_events
        SET status='dead',error_class='lease_retries_exhausted',
            lease_token=NULL,lease_until=NULL,completed_at=?,updated_at=?
        WHERE status='leased' AND lease_until<=? AND attempts>=?`,
        timestamp,timestamp,timestamp,githubAutomationMaxAttempts)
    if err!=nil{return nil,err}
    rows,err:=s.db.QueryContext(ctx,`SELECT id,delivery_id,installation_id,repository_external_id,
        item_number,event_type,payload_sha256,attempts
        FROM integration_github_automation_events WHERE
        (status='pending' AND next_attempt_at<=?) OR
        (status='leased' AND lease_until<=?)
        ORDER BY id LIMIT ?`,timestamp,timestamp,limit)
    if err!=nil{return nil,err}
    candidates:=make([]GitHubAutomationEvent,0,limit)
    for rows.Next(){
        var event GitHubAutomationEvent
        err=rows.Scan(&event.ID,&event.DeliveryID,&event.InstallationID,
            &event.RepositoryExternalID,&event.ItemNumber,&event.EventType,
            &event.PayloadSHA256,&event.Attempts)
        if err!=nil{rows.Close();return nil,err}
        candidates=append(candidates,event)
    }
    err=rows.Err()
    rows.Close()
    if err!=nil{return nil,err}
    result:=make([]GitHubAutomationEvent,0,len(candidates))
    for _,event:=range candidates {
        secret:=make([]byte,16)
        if _,err:=rand.Read(secret);err!=nil{return nil,err}
        event.LeaseToken=hex.EncodeToString(secret)
        res,err:=s.db.ExecContext(ctx,`UPDATE integration_github_automation_events
            SET status='leased',attempts=attempts+1,lease_token=?,lease_until=?,updated_at=?
            WHERE id=? AND attempts<? AND
            ((status='pending' AND next_attempt_at<=?) OR (status='leased' AND lease_until<=?))`,
            event.LeaseToken,timestamp+lease.Milliseconds(),timestamp,event.ID,
            githubAutomationMaxAttempts,timestamp,timestamp)
        if err!=nil{return nil,err}
        affected,err:=res.RowsAffected()
        if err!=nil{return nil,err}
        if affected==1{event.Attempts++;result=append(result,event)}
    }
    return result,nil
}

func (s *Store) MatchAndCompleteGitHubAutomation(ctx context.Context,eventID int64,leaseToken string,now time.Time)(int,error){
    if eventID<=0 || len(leaseToken)!=32{return 0,ErrValidation}
    tx,err:=s.db.BeginTx(ctx,nil)
    if err!=nil{return 0,err}
    defer tx.Rollback()
    timestamp:=now.UTC().UnixMilli()
    var event GitHubAutomationEvent
    var eventCreatedAt int64
    err=tx.QueryRowContext(ctx,`SELECT installation_id,repository_external_id,item_number,event_type,created_at
        FROM integration_github_automation_events
        WHERE id=? AND status='leased' AND lease_token=? AND lease_until>?`,
        eventID,leaseToken,timestamp).Scan(&event.InstallationID,
        &event.RepositoryExternalID,&event.ItemNumber,&event.EventType,&eventCreatedAt)
    if errors.Is(err,sql.ErrNoRows){return 0,ErrConflict}
    if err!=nil{return 0,err}
    rows,err:=tx.QueryContext(ctx,`SELECT a.id,a.project_id,a.resource_id,a.updated_at,a.action_type,ap.id
        FROM integration_automation_rules a
        JOIN integration_project_resources pr
          ON pr.project_id=a.project_id AND pr.resource_id=a.resource_id
        JOIN integration_resources r ON r.id=a.resource_id
        JOIN integration_connections c ON c.id=r.connection_id
        LEFT JOIN integration_automation_execution_approvals ap ON ap.rule_id=a.id AND ap.project_id=a.project_id AND ap.rule_updated_at=a.updated_at AND ap.approved_at<? AND a.updated_at<?
        WHERE a.enabled=1 AND a.event_type=? AND a.action_type='notify'
          AND c.provider='github' AND c.status='active'
          AND c.external_account_id=? AND r.kind='repository'
          AND r.external_id=? AND r.availability='available'
        ORDER BY a.id LIMIT ?`,eventCreatedAt,eventCreatedAt,event.EventType,event.InstallationID,
        event.RepositoryExternalID,githubAutomationMaxMatchesPerEvent+1)
    if err!=nil{return 0,err}
    type match struct{id,projectID,resourceID,updatedAt int64;action string;approvalID sql.NullInt64}
    matches:=make([]match,0)
    for rows.Next(){
        var item match
        if err:=rows.Scan(&item.id,&item.projectID,&item.resourceID,&item.updatedAt,&item.action,&item.approvalID);err!=nil{
            rows.Close()
            return 0,err
        }
        matches=append(matches,item)
    }
    err=rows.Err()
    rows.Close()
    if err!=nil{return 0,err}
    if len(matches)>githubAutomationMaxMatchesPerEvent{return 0,ErrGitHubAutomationFull}
    approvedCount:=0
    for _,item:=range matches{if item.approvalID.Valid{approvedCount++}}
    if approvedCount>0{
        var pending int
        if err:=tx.QueryRowContext(ctx,"SELECT COUNT(*) FROM integration_automation_actions WHERE status IN ('pending','leased')").Scan(&pending);err!=nil{return 0,err}
        if pending+approvedCount>maxIntegrationAutomationActions{return 0,ErrGitHubAutomationFull}
    }
    for _,item:=range matches{
        _,err=tx.ExecContext(ctx,`INSERT INTO integration_github_automation_matches(
            event_id,project_id,rule_id,resource_id,rule_updated_at,action_type,matched_at)
            VALUES(?,?,?,?,?,?,?) ON CONFLICT(event_id,rule_id) DO NOTHING`,
            eventID,item.projectID,item.id,item.resourceID,item.updatedAt,item.action,timestamp)
        if err!=nil{return 0,err}
        if item.approvalID.Valid{
            insert,err:=tx.ExecContext(ctx,`INSERT INTO integration_automation_actions(
                event_id,project_id,rule_id,resource_id,rule_updated_at,approval_id,action_type,
                next_attempt_at,created_at,updated_at)
                VALUES(?,?,?,?,?,?,'notify',?,?,?)
                ON CONFLICT(event_id,rule_id) DO NOTHING`,
                eventID,item.projectID,item.id,item.resourceID,item.updatedAt,item.approvalID.Int64,
                timestamp,timestamp,timestamp)
            if err!=nil{return 0,err}
            count,err:=insert.RowsAffected()
            if err!=nil{return 0,err}
            if count==1{
                id,err:=insert.LastInsertId()
                if err!=nil{return 0,err}
                if _,err:=tx.ExecContext(ctx,"INSERT INTO integration_automation_action_audit(action_id,transition,created_at) VALUES(?,'queued',?)",id,timestamp);err!=nil{return 0,err}
            }
        }
    }
    result,err:=tx.ExecContext(ctx,`UPDATE integration_github_automation_events
        SET status='completed',lease_token=NULL,lease_until=NULL,completed_at=?,updated_at=?,
            error_class=NULL WHERE id=? AND status='leased' AND lease_token=?`,
        timestamp,timestamp,eventID,leaseToken)
    if err!=nil{return 0,err}
    affected,err:=result.RowsAffected()
    if err!=nil{return 0,err}
    if affected!=1{return 0,ErrConflict}
    if err:=tx.Commit();err!=nil{return 0,err}
    return len(matches),nil
}

func (s *Store) FailGitHubAutomation(ctx context.Context,eventID int64,leaseToken string,now time.Time)(bool,error){
    if eventID<=0 || len(leaseToken)!=32{return false,ErrValidation}
    tx,err:=s.db.BeginTx(ctx,nil)
    if err!=nil{return false,err}
    defer tx.Rollback()
    var attempts int
    err=tx.QueryRowContext(ctx,`SELECT attempts FROM integration_github_automation_events
        WHERE id=? AND status='leased' AND lease_token=?`,eventID,leaseToken).Scan(&attempts)
    if errors.Is(err,sql.ErrNoRows){return false,nil}
    if err!=nil{return false,err}
    timestamp:=now.UTC().UnixMilli()
    status:="pending"
    next:=timestamp+time.Minute.Milliseconds()*int64(1<<uint(attempts-1))
    var completed any
    if attempts>=githubAutomationMaxAttempts{
        status="dead"
        next=timestamp
        completed=timestamp
    }else if next>timestamp+(30*time.Minute).Milliseconds(){
        next=timestamp+(30*time.Minute).Milliseconds()
    }
    result,err:=tx.ExecContext(ctx,`UPDATE integration_github_automation_events
        SET status=?,next_attempt_at=?,error_class='match_store_unavailable',
            lease_token=NULL,lease_until=NULL,completed_at=?,updated_at=?
        WHERE id=? AND status='leased' AND lease_token=?`,
        status,next,completed,timestamp,eventID,leaseToken)
    if err!=nil{return false,err}
    affected,err:=result.RowsAffected()
    if err!=nil{return false,err}
    if err:=tx.Commit();err!=nil{return false,err}
    return affected==1,nil
}

func (s *Store) GitHubAutomationSummary(ctx context.Context)(GitHubAutomationCounts,error){
    var summary GitHubAutomationCounts
    rows,err:=s.db.QueryContext(ctx,`SELECT status,COUNT(*) FROM integration_github_automation_events GROUP BY status`)
    if err!=nil{return summary,err}
    for rows.Next(){
        var status string
        var count int64
        if err:=rows.Scan(&status,&count);err!=nil{rows.Close();return summary,err}
        switch status{
        case "pending":summary.Pending=count
        case "leased":summary.Leased=count
        case "completed":summary.Completed=count
        case "dead":summary.Dead=count
        }
    }
    err=rows.Err()
    rows.Close()
    if err!=nil{return summary,err}
    err=s.db.QueryRowContext(ctx,`SELECT COUNT(*) FROM integration_github_automation_matches`).Scan(&summary.Matches)
    if err!=nil{return summary,err}
    summary.Actions,err=s.IntegrationAutomationActionSummary(ctx)
    return summary,err
}

func (s *Store) MaintainGitHubAutomation(ctx context.Context,now time.Time)error{
    if err:=s.MaintainIntegrationAutomationActions(ctx,now);err!=nil{return err}
    timestamp:=now.UTC().UnixMilli()
    _,err:=s.db.ExecContext(ctx,`UPDATE integration_github_automation_events
        SET status='dead',error_class='lease_retries_exhausted',
            lease_token=NULL,lease_until=NULL,completed_at=?,updated_at=?
        WHERE status='leased' AND lease_until<=? AND attempts>=?`,
        timestamp,timestamp,timestamp,githubAutomationMaxAttempts)
    if err!=nil{return err}
    _,err=s.db.ExecContext(ctx,`DELETE FROM integration_github_automation_events
        WHERE id IN (SELECT id FROM integration_github_automation_events
          WHERE status IN ('completed','dead') AND updated_at<?
          ORDER BY updated_at LIMIT 200)`,
        now.UTC().Add(-githubAutomationRetention).UnixMilli())
    return err
}

func (s *Store) ListGitHubAutomationMatches(ctx context.Context,actorID,projectID int64,limit int)([]GitHubAutomationMatch,error){
    if limit<1 || limit>100{return nil,ErrValidation}
    tx,err:=s.db.BeginTx(ctx,&sql.TxOptions{ReadOnly:true})
    if err!=nil{return nil,err}
    defer tx.Rollback()
    if err:=s.checkIntegrationProjectTx(ctx,tx,actorID,projectID,false);err!=nil{return nil,err}
    rows,err:=tx.QueryContext(ctx,`SELECT m.id,m.event_id,m.rule_id,m.resource_id,
        e.item_number,e.event_type,m.rule_updated_at,m.action_type,m.matched_at
        FROM integration_github_automation_matches m
        JOIN integration_github_automation_events e ON e.id=m.event_id
        WHERE m.project_id=? ORDER BY m.id DESC LIMIT ?`,projectID,limit)
    if err!=nil{return nil,err}
    result:=make([]GitHubAutomationMatch,0)
    for rows.Next(){
        var item GitHubAutomationMatch
        var timestamp int64
        if err:=rows.Scan(&item.ID,&item.EventID,&item.RuleID,&item.ResourceID,
            &item.ItemNumber,&item.EventType,&item.RuleUpdatedAt,&item.ActionType,&timestamp);err!=nil{
            rows.Close()
            return nil,err
        }
        item.MatchedAt=time.UnixMilli(timestamp).UTC()
        result=append(result,item)
    }
    err=rows.Err()
    rows.Close()
    if err!=nil{return nil,err}
    if err:=tx.Commit();err!=nil{return nil,err}
    return result,nil
}
