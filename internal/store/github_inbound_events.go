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

    "scrumboy/internal/integration"
)

const githubInboundMaxOutstanding = 5000
const githubInboundMaxAttempts = 8
const githubInboundRetention = 30 * 24 * time.Hour

type GitHubInboundEvent struct {
    ID int64
    DeliveryID string
    InstallationID string
    EventType string
    Action string
    PayloadSHA256 string
    Attempts int
    LeaseToken string
}

type GitHubInboundCounts struct {
    Pending int64 `json:"pending"`
    Leased int64 `json:"leased"`
    Completed int64 `json:"completed"`
    Dead int64 `json:"dead"`
}

func validGitHubInboundEvent(event GitHubInboundEvent) bool {
    installationID, err := strconv.ParseInt(event.InstallationID, 10, 64)
    if err != nil || installationID <= 0 || len(event.DeliveryID) < 8 || len(event.DeliveryID) > 128 ||
       len(event.Action) == 0 || len(event.Action) > 64 || len(event.PayloadSHA256) != 64 {
        return false
    }
    for _, c := range event.DeliveryID {
        if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') || c == '-') {
            return false
        }
    }
    if _, err := hex.DecodeString(event.PayloadSHA256); err != nil {
        return false
    }
    switch event.EventType {
    case "installation", "installation_repositories", "repository":
        return true
    }
    return false
}

func (s *Store) AdmitGitHubInbound(ctx context.Context, event GitHubInboundEvent) (bool, error) {
    if !validGitHubInboundEvent(event) {
        return false, fmt.Errorf("%w: invalid GitHub webhook metadata", ErrValidation)
    }
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil { return false, err }
    defer tx.Rollback()

    var oldDigest string
    err = tx.QueryRowContext(ctx, "SELECT payload_sha256 FROM integration_github_inbound_events WHERE delivery_id=?", event.DeliveryID).Scan(&oldDigest)
    if err == nil {
        if !strings.EqualFold(oldDigest, event.PayloadSHA256) { return false, ErrConflict }
        return false, tx.Commit()
    }
    if !errors.Is(err, sql.ErrNoRows) { return false, err }

    var connectionID int64
    err = tx.QueryRowContext(ctx, "SELECT id FROM integration_connections WHERE provider='github' AND external_account_id=? AND status='active'", event.InstallationID).Scan(&connectionID)
    if errors.Is(err, sql.ErrNoRows) { return false, ErrNotFound }
    if err != nil { return false, err }

    var outstanding int
    err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM integration_github_inbound_events WHERE status IN ('pending','leased')").Scan(&outstanding)
    if err != nil { return false, err }
    if outstanding >= githubInboundMaxOutstanding { return false, ErrConflict }

    now := time.Now().UTC().UnixMilli()
    _, err = tx.ExecContext(ctx, `INSERT INTO integration_github_inbound_events
        (delivery_id,installation_id,event_type,event_action,payload_sha256,next_attempt_at,created_at,updated_at)
        VALUES(?,?,?,?,?,?,?,?)`, event.DeliveryID, event.InstallationID, event.EventType, event.Action, strings.ToLower(event.PayloadSHA256), now, now, now)
    if err != nil { return false, err }
    return true, tx.Commit()
}

func (s *Store) GitHubInboundInstallationConnected(ctx context.Context, installationID string) (bool, error) {
    var exists int
    err := s.db.QueryRowContext(ctx, `SELECT 1 FROM integration_connections
        WHERE provider='github' AND external_account_id=? AND status='active'`, installationID).Scan(&exists)
    if errors.Is(err, sql.ErrNoRows) { return false, nil }
    if err != nil { return false, err }
    return exists == 1, nil
}

func (s *Store) ClaimGitHubInbound(ctx context.Context, now time.Time, limit int, lease time.Duration) ([]GitHubInboundEvent,error) {
    if limit < 1 || limit > 20 || lease < time.Second || lease > time.Minute { return nil, ErrValidation }
    ts := now.UTC().UnixMilli()
    rows, err := s.db.QueryContext(ctx, `SELECT id,delivery_id,installation_id,event_type,event_action,payload_sha256,attempts
        FROM integration_github_inbound_events WHERE
        (status='pending' AND next_attempt_at<=?) OR (status='leased' AND lease_until<=?)
        ORDER BY id LIMIT ?`, ts, ts, limit)
    if err != nil { return nil, err }
    candidates := make([]GitHubInboundEvent,0,limit)
    for rows.Next() {
        var item GitHubInboundEvent
        if err:= rows.Scan(&item.ID,&item.DeliveryID,&item.InstallationID,&item.EventType,&item.Action,&item.PayloadSHA256,&item.Attempts);err!=nil{rows.Close();return nil,err}
        candidates = append(candidates,item)
    }
    err = rows.Err()
    rows.Close()
    if err!=nil{return nil,err}
    claims := make([]GitHubInboundEvent,0,len(candidates))
    for _,item := range candidates {
        secret := make([]byte,16)
        if _,err:=rand.Read(secret);err!=nil{return nil,err}
        item.LeaseToken=hex.EncodeToString(secret)
        result,err:=s.db.ExecContext(ctx,`UPDATE integration_github_inbound_events
            SET status='leased',attempts=attempts+1,lease_token=?,lease_until=?,updated_at=?
            WHERE id=? AND attempts<? AND
            ((status='pending' AND next_attempt_at<=?) OR (status='leased' AND lease_until<=?))`,
            item.LeaseToken,ts+lease.Milliseconds(),ts,item.ID,githubInboundMaxAttempts,ts,ts)
        if err!=nil{return nil,err}
        affected,err:=result.RowsAffected()
        if err!=nil{return nil,err}
        if affected==1 {item.Attempts++;claims=append(claims,item)}
    }
    return claims,nil
}

func (s *Store) SettleGitHubInbound(ctx context.Context, id int64, leaseToken string, success bool, class string, now time.Time) (bool,error) {
    if id<=0 || len(leaseToken)!=32 || len(class)>64 {return false,ErrValidation}
    ts:=now.UTC().UnixMilli()
    status:="completed"
    var next int64=ts
    var completed any=ts
    if !success {
        var attempts int
        err:=s.db.QueryRowContext(ctx,"SELECT attempts FROM integration_github_inbound_events WHERE id=? AND status='leased' AND lease_token=?",id,leaseToken).Scan(&attempts)
        if errors.Is(err,sql.ErrNoRows){return false,nil}
        if err!=nil{return false,err}
        if attempts>=githubInboundMaxAttempts {
            status="dead"
        } else {
            status="pending"
            delay:=time.Minute*time.Duration(1<<uint(attempts-1))
            if delay>30*time.Minute {delay=30*time.Minute}
            next=ts+delay.Milliseconds()
            completed=nil
        }
    }
    result,err:=s.db.ExecContext(ctx,`UPDATE integration_github_inbound_events
        SET status=?,next_attempt_at=?,lease_token=NULL,lease_until=NULL,
            error_class=?,completed_at=?,updated_at=?
        WHERE id=? AND status='leased' AND lease_token=?`,status,next,class,completed,ts,id,leaseToken)
    if err!=nil{return false,err}
    n,err:=result.RowsAffected()
    return n==1,err
}

func (s *Store) GitHubInboundSummary(ctx context.Context) (GitHubInboundCounts,error) {
    var out GitHubInboundCounts
    rows,err:=s.db.QueryContext(ctx,"SELECT status,COUNT(*) FROM integration_github_inbound_events GROUP BY status")
    if err!=nil{return out,err}
    defer rows.Close()
    for rows.Next() {
        var state string
        var count int64
        if err:=rows.Scan(&state,&count);err!=nil{return out,err}
        switch state {
        case "pending":out.Pending=count
        case "leased":out.Leased=count
        case "completed":out.Completed=count
        case "dead":out.Dead=count
        }
    }
    return out,rows.Err()
}

func (s *Store) MaintainGitHubInbound(ctx context.Context, now time.Time) error {
    older := now.UTC().Add(-githubInboundRetention).UnixMilli()
    _,err:=s.db.ExecContext(ctx,`DELETE FROM integration_github_inbound_events
        WHERE id IN (SELECT id FROM integration_github_inbound_events
        WHERE status IN ('completed','dead') AND updated_at<?
        ORDER BY updated_at LIMIT 200)`,older)
    return err
}

func (s *Store) ReconcileGitHubInboundRepositories(ctx context.Context, installationID string, repositories []integration.Resource) error {
    if len(repositories)>2000{return ErrValidation}
    seen:=make(map[string]struct{},len(repositories))
    for _,item:=range repositories {
        if !validateGitHubResource(item){return ErrValidation}
        if _,ok:=seen[item.ID];ok{return ErrValidation}
        seen[item.ID]=struct{}{}
    }
    tx,err:=s.db.BeginTx(ctx,nil)
    if err!=nil{return err}
    defer tx.Rollback()
    var connID int64
    err=tx.QueryRowContext(ctx,`SELECT id FROM integration_connections
        WHERE provider='github' AND external_account_id=? AND status='active'`,installationID).Scan(&connID)
    if errors.Is(err,sql.ErrNoRows){return ErrNotFound}
    if err!=nil{return err}
    now:=time.Now().UTC().UnixMilli()
    for _,resource:=range repositories {
        _,err=tx.ExecContext(ctx,`INSERT INTO integration_resources(connection_id,kind,external_id,name,url,created_at,updated_at)
            VALUES(?,'repository',?,?,?,?,?)
            ON CONFLICT(connection_id,kind,external_id) DO UPDATE SET name=excluded.name,url=excluded.url,updated_at=excluded.updated_at`,
            connID,resource.ID,resource.Name,resource.URL,now,now)
        if err!=nil{return err}
    }
    return tx.Commit()
}

func (s *Store) DisableGitHubInboundInstallation(ctx context.Context, installationID string) error {
    result,err:=s.db.ExecContext(ctx,`UPDATE integration_connections SET status='disabled',updated_at=?
        WHERE provider='github' AND external_account_id=? AND status='active'`,time.Now().UTC().UnixMilli(),installationID)
    if err!=nil{return err}
    n,err:=result.RowsAffected()
    if err!=nil{return err}
    if n==0{return ErrNotFound}
    return nil
}
