package githubbroker

import (
    "context"
    "errors"
    "testing"
    "time"

    "scrumboy/internal/integration/githubconnect"
)

type delayedVerifier struct {
    result githubconnect.Verification
    started chan struct{}
    unblock chan struct{}
}

func (v *delayedVerifier) Begin(subject string)(string,error){
    return "https://github.com/login/oauth/authorize?state=placeholder",nil
}

func (v *delayedVerifier) Complete(ctx context.Context,subject,state,code string,installationID int64)(githubconnect.Verification,error){
    close(v.started)
    select {
    case <-v.unblock:
        return v.result,nil
    case <-ctx.Done():
        return githubconnect.Verification{},ctx.Err()
    }
}

func (v *delayedVerifier) Cancel(subject string) {}

func TestDisconnectAfterInflightOAuthCompletionCannotRestoreRevokedGrant(t *testing.T) {
    ctx,cancel:=context.WithTimeout(context.Background(),8*time.Second)
    defer cancel()
    store,_:=newTestBindings(t)
    record:=verifiedFixture("instance-a",42,11)
    verifier:=&delayedVerifier{
        result:record.verification,started:make(chan struct{}),unblock:make(chan struct{}),
    }
    reader:=&resourceStub{items:record.resources}
    coordinator,err:=NewBindingCoordinator(verifier,reader,store)
    if err!=nil{t.Fatal(err)}
    doneComplete:=make(chan error,1)
    go func() {
        _,err:=coordinator.Complete(ctx,"instance-a",42,"state","code",11)
        doneComplete<-err
    }()
    select {
    case <-verifier.started:
    case <-ctx.Done():
        t.Fatal("GitHub verification did not start")
    }
    doneRevoke:=make(chan error,1)
    go func(){doneRevoke<-coordinator.RevokeUser(ctx,"instance-a",42)}()
    select {
    case err:=<-doneRevoke:
        t.Fatalf("revocation overtook in-flight GitHub verification: %v",err)
    case <-time.After(15*time.Millisecond):
    }
    close(verifier.unblock)
    select {
    case err:=<-doneComplete:
        if err!=nil{t.Fatalf("verified completion failed: %v",err)}
    case <-ctx.Done():
        t.Fatal("GitHub verification blocked indefinitely")
    }
    select {
    case err:=<-doneRevoke:
        if err!=nil{t.Fatalf("disconnect failed: %v",err)}
    case <-ctx.Done():
        t.Fatal("disconnect blocked indefinitely")
    }
    if _,err:=store.LookupUser(ctx,"instance-a",42,11,22);!errors.Is(err,ErrDenied){
        t.Fatalf("revoked grant unexpectedly active: %v",err)
    }
}
