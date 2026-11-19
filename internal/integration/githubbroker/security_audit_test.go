package githubbroker

import (
    "context"
    "errors"
    "strings"
    "testing"
    "time"
)

func TestSecurityAuditRecordsVerifiedOwnershipAndCertificateLifecycle(t *testing.T){
    ctx:=context.Background()
    store,path:=newTestBindings(t)
    ca,caKey,_:=makeTestCertAuthority(t)
    certificate,_:=makeTestClientCertificate(t,ca,caKey,75)
    if err:=store.RegisterInstanceCertificate(ctx,"instance-a",certificate.Certificate[0]);err!=nil{t.Fatal(err)}
    if err:=store.recordVerified(ctx,verifiedFixture("instance-a",42,11));err!=nil{t.Fatal(err)}
    if err:=store.RevokeRepository(ctx,"instance-a",42,11,22);err!=nil{t.Fatal(err)}
    if err:=store.RevokeInstallation(ctx,"instance-a",42,11);err!=nil{t.Fatal(err)}
    if err:=store.RevokeUser(ctx,"instance-a",42);err!=nil{t.Fatal(err)}
    if err:=store.RevokeInstanceCertificate(ctx,"instance-a",certificate.Certificate[0]);err!=nil{t.Fatal(err)}
    if err:=store.DisconnectInstance(ctx,"instance-a");err!=nil{t.Fatal(err)}
    events,err:=store.SecurityActivity(ctx,"instance-a",20)
    if err!=nil{t.Fatal(err)}
    expected:=[]string{
        "disconnect_instance","revoke_certificate","revoke_user",
        "revoke_installation","revoke_repository","bind_verified","register_certificate",
    }
    if len(events)!=len(expected){t.Fatalf("audit count mismatch %+v",events)}
    for i,event:=range events{
        if event.Operation!=expected[i] || event.InstanceID!="instance-a" ||
            event.OccurredAt.IsZero() || strings.Contains(event.Operation,"token"){
            t.Fatalf("invalid security audit record %+v",event)
        }
        if event.Operation=="bind_verified" && (event.UserID!=42 ||
            event.InstallationID!=11 || event.RepositoryID!=0){
            t.Fatalf("binding context not audited %+v",event)
        }
        if event.Operation=="revoke_repository" && event.RepositoryID!=22{
            t.Fatalf("repository revoke not audited %+v",event)
        }
    }
    if err:=store.Close();err!=nil{t.Fatal(err)}
    reopened,err:=OpenBindingsDB(path)
    if err!=nil{t.Fatal(err)}
    defer reopened.Close()
    persisted,err:=reopened.SecurityActivity(ctx,"instance-a",20)
    if err!=nil || len(persisted)!=len(events){t.Fatalf("audit not durable %+v %v",persisted,err)}
    other,err:=reopened.SecurityActivity(ctx,"instance-b",20)
    if err!=nil||len(other)!=0{t.Fatalf("audit crossed instance boundary %+v %v",other,err)}
    if _,err:=reopened.SecurityActivity(ctx,"instance-a",201);!errors.Is(err,ErrDenied){
        t.Fatal("unbounded security audit query accepted")
    }
}

func TestSecurityAuditFailureRollsBackRevocation(t *testing.T){
    ctx:=context.Background()
    store,_:=newTestBindings(t)
    if err:=store.recordVerified(ctx,verifiedFixture("instance-a",42,11));err!=nil{t.Fatal(err)}
    if _,err:=store.db.ExecContext(ctx,`DROP TABLE github_broker_security_events`);err!=nil{t.Fatal(err)}
    if err:=store.RevokeUser(ctx,"instance-a",42);!errors.Is(err,ErrUnavailable){
        t.Fatalf("revocation without audit succeeded %v",err)
    }
    if _,err:=store.LookupUser(ctx,"instance-a",42,11,22);err!=nil{
        t.Fatalf("audit failure partially revoked binding %v",err)
    }
    if err:=store.recordVerified(ctx,verifiedFixture("instance-b",43,11));!errors.Is(err,ErrUnavailable){
        t.Fatalf("verified binding without audit persisted: %v",err)
    }
    if _,err:=store.LookupUser(ctx,"instance-b",43,11,22);!errors.Is(err,ErrDenied){
        t.Fatal("unlogged user binding persisted")
    }
}

func TestSecurityAuditRetentionAndFailureIsolation(t *testing.T){
    ctx:=context.Background()
    store,_:=newTestBindings(t)
    clock:=time.Unix(1880000100,0).UTC()
    store.now=func()time.Time{return clock}
    if err:=store.recordVerified(ctx,verifiedFixture("instance-a",42,11));err!=nil{t.Fatal(err)}
    clock=clock.Add(31*24*time.Hour)
    if err:=store.RevokeUser(ctx,"instance-a",42);err!=nil{t.Fatal(err)}
    recent,err:=store.SecurityActivity(ctx,"instance-a",10)
    if err!=nil || len(recent)!=1 || recent[0].Operation!="revoke_user"{
        t.Fatalf("expired audit records not pruned %+v %v",recent,err)
    }
    if err:=store.RevokeUser(ctx,"instance-b",42);err!=nil{t.Fatal(err)}
    foreign,err:=store.SecurityActivity(ctx,"instance-b",10)
    if err!=nil || len(foreign)!=1 || foreign[0].InstanceID!="instance-b"{
        t.Fatalf("cross-instance audit retrieval %+v %v",foreign,err)
    }
}
