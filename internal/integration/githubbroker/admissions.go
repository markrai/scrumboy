package githubbroker

import (
    "context"
    "database/sql"
    "time"
)

const (
    sharedInstanceBudget = 120
    sharedGlobalBudget = 3000
    admissionWindowSeconds int64 = 60
    admissionRetentionWindows int64 = 1440
)

type DurableAdmission interface {
    Admit(context.Context,string,string) (bool,error)
}

type AdmissionSummary struct {
    WindowStart time.Time
    InstanceID string
    Operation string
    Outcome string
    Hits int64
}

func brokerOperation(path string)string {
    switch path {
    case "/v1/github/items/resolve":
        return "resolve"
    case "/v1/github/items/search":
        return "search"
    case "/v1/github/connections/begin":
        return "begin"
    case "/v1/github/connections/complete":
        return "complete"
    case "/v1/github/connections/revoke":
        return "revoke"
    default:
        return ""
    }
}

func (b *Broker) EnableDurableAdmission(admission DurableAdmission)error {
    if b==nil || b.userGrants==nil || admission==nil || b.durableAdmission!=nil{
        return ErrUnavailable
    }
    b.durableAdmission=admission
    return nil
}

func consumeWindow(ctx context.Context,tx *sql.Tx,scope string,window int64,limit int64)(bool,error){
    result,err:=tx.ExecContext(ctx,`INSERT INTO github_broker_rate_counters(scope,window_start,hits)
        VALUES(?,?,1) ON CONFLICT(scope) DO UPDATE SET
        window_start=excluded.window_start,
        hits=CASE WHEN window_start=excluded.window_start THEN hits+1 ELSE 1 END
        WHERE window_start<=excluded.window_start AND
            (window_start<excluded.window_start OR hits<?)`,scope,window,limit)
    if err!=nil{return false,err}
    changed,err:=result.RowsAffected()
    if err!=nil{return false,err}
    return changed==1,nil
}

func (s *BindingsDB) Admit(ctx context.Context,instance,operation string)(bool,error){
    if s==nil || s.db==nil || !validInstance(instance) || brokerOperation(operation)=="" ||
        ctx.Err()!=nil {return false,ErrDenied}
    window:=s.now().UTC().Unix()/admissionWindowSeconds
    tx,err:=s.db.BeginTx(ctx,nil)
    if err!=nil{return false,ErrUnavailable}
    defer tx.Rollback()
    globalOK,err:=consumeWindow(ctx,tx,"global",window,sharedGlobalBudget)
    if err!=nil{return false,ErrUnavailable}
    instanceOK:=false
    if globalOK{
        instanceOK,err=consumeWindow(ctx,tx,"instance:"+instance,window,sharedInstanceBudget)
        if err!=nil{return false,ErrUnavailable}
    }
    admitted:=globalOK&&instanceOK
    if !admitted{
        if err:=tx.Rollback();err!=nil{return false,ErrUnavailable}
        tx,err=s.db.BeginTx(ctx,nil)
        if err!=nil{return false,ErrUnavailable}
        defer tx.Rollback()
    }
    outcome:="admitted"
    if !admitted{outcome="limited"}
    _,err=tx.ExecContext(ctx,`INSERT INTO github_broker_admissions(
        window_start,instance_id,operation,outcome,hits) VALUES(?,?,?,?,1)
        ON CONFLICT(window_start,instance_id,operation,outcome)
        DO UPDATE SET hits=MIN(hits+1,10000)`,
        window,instance,brokerOperation(operation),outcome)
    if err!=nil{return false,ErrUnavailable}
    if _,err=tx.ExecContext(ctx,`DELETE FROM github_broker_admissions
        WHERE window_start<?`,window-admissionRetentionWindows);err!=nil{
        return false,ErrUnavailable
    }
    if err:=tx.Commit();err!=nil{return false,ErrUnavailable}
    return admitted,nil
}

func (s *BindingsDB) AdmissionActivity(ctx context.Context,instance string,limit int)([]AdmissionSummary,error){
    if s==nil || s.db==nil || !validInstance(instance) || limit<1 || limit>200 {
        return nil,ErrDenied
    }
    rows,err:=s.db.QueryContext(ctx,`SELECT window_start,instance_id,operation,outcome,hits
        FROM github_broker_admissions WHERE instance_id=?
        ORDER BY window_start DESC,operation,outcome LIMIT ?`,instance,limit)
    if err!=nil{return nil,ErrUnavailable}
    defer rows.Close()
    activity:=make([]AdmissionSummary,0)
    for rows.Next(){
        var summary AdmissionSummary
        var window int64
        if err:=rows.Scan(&window,&summary.InstanceID,&summary.Operation,&summary.Outcome,&summary.Hits);err!=nil{
            return nil,ErrUnavailable
        }
        summary.WindowStart=time.Unix(window*admissionWindowSeconds,0).UTC()
        activity=append(activity,summary)
    }
    if err:=rows.Err();err!=nil{return nil,ErrUnavailable}
    return activity,nil
}

var _ DurableAdmission = (*BindingsDB)(nil)