package githubbroker

import (
    "context"
    "database/sql"
    "time"
)

const securityAuditRetention = 30*24*time.Hour

type SecurityEvent struct {
    OccurredAt time.Time
    InstanceID string
    Operation string
    UserID int64
    InstallationID int64
    RepositoryID int64
}

func securityOperation(event string)bool{
    switch event{
    case "bind_verified","revoke_user","revoke_installation","revoke_repository",
        "revoke_instance","register_certificate","revoke_certificate","disconnect_instance":
        return true
    default:
        return false
    }
}

func (s *BindingsDB) auditChange(ctx context.Context,tx *sql.Tx,operation,instance string,userID,installationID,repositoryID int64)error{
    if !securityOperation(operation) || !validInstance(instance) || userID<0 ||
        installationID<0 || repositoryID<0{return ErrDenied}
    now:=s.now().UTC().Unix()
    if _,err:=tx.ExecContext(ctx,`INSERT INTO github_broker_security_events(
        occurred_at,instance_id,operation,user_id,installation_id,repository_id)
        VALUES(?,?,?,?,?,?)`,now,instance,operation,userID,installationID,repositoryID);err!=nil{
        return ErrUnavailable
    }
    if _,err:=tx.ExecContext(ctx,`DELETE FROM github_broker_security_events
        WHERE occurred_at<?`,now-int64(securityAuditRetention/time.Second));err!=nil{
        return ErrUnavailable
    }
    return nil
}

func (s *BindingsDB) SecurityActivity(ctx context.Context,instance string,limit int)([]SecurityEvent,error){
    if s==nil || s.db==nil || !validInstance(instance) || limit<1 || limit>200{
        return nil,ErrDenied
    }
    rows,err:=s.db.QueryContext(ctx,`SELECT occurred_at,instance_id,operation,user_id,installation_id,repository_id
        FROM github_broker_security_events WHERE instance_id=?
        ORDER BY id DESC LIMIT ?`,instance,limit)
    if err!=nil{return nil,ErrUnavailable}
    defer rows.Close()
    result:=make([]SecurityEvent,0)
    for rows.Next(){
        var event SecurityEvent
        var timestamp int64
        if err:=rows.Scan(&timestamp,&event.InstanceID,&event.Operation,&event.UserID,
            &event.InstallationID,&event.RepositoryID);err!=nil{return nil,ErrUnavailable}
        if !securityOperation(event.Operation){return nil,ErrUnavailable}
        event.OccurredAt=time.Unix(timestamp,0).UTC()
        result=append(result,event)
    }
    if err:=rows.Err();err!=nil{return nil,ErrUnavailable}
    return result,nil
}
