package githubbroker

import (
    "context"
    "errors"
    "path/filepath"
    "testing"
    "time"

    "scrumboy/internal/integration"
    "scrumboy/internal/integration/githubconnect"
)

func newTestBindings(t *testing.T) (*BindingsDB,string) {
    t.Helper()
    path:=filepath.Join(t.TempDir(),"ownership.db")
    store,err:=OpenBindingsDB(path)
    if err!=nil{t.Fatal(err)}
    t.Cleanup(func(){store.Close()})
    return store,path
}

func verifiedFixture(instance string,userID,installationID int64) verifiedRecord {
    return verifiedRecord{
        instance:instance,userID:userID,
        verification:githubconnect.Verification{
            Subject:bindingSubject(instance,userID),
            GitHubUserID:777,GitHubLogin:"alice",
            InstallationID:installationID,AccountID:456,
            AccountLogin:"engineering",AccountType:"Organization",
            AuthorizedRepositoryIDs:[]int64{22,23},
        },
        resources:[]integration.Resource{
            {ID:"22",Name:"engineering/one",URL:"https://github.com/engineering/one"},
            {ID:"23",Name:"engineering/two",URL:"https://github.com/engineering/two"},
        },
    }
}

func TestVerifiedBindingsPersistAndIsolateInstanceUserAndRepository(t *testing.T) {
    store,path:=newTestBindings(t)
    ctx:=context.Background()
    rec:=verifiedFixture("instance-a",42,11)
    if err:=store.recordVerified(ctx,rec);err!=nil{t.Fatal(err)}
    g,err:=store.LookupUser(ctx,"instance-a",42,11,22)
    if err!=nil || g.Repository!="engineering/one"{t.Fatalf("valid binding %+v %v",g,err)}
    for _,tc:=range []struct{instance string;user,installation,repository int64}{
        {"instance-b",42,11,22},
        {"instance-a",43,11,22},
        {"instance-a",42,12,22},
        {"instance-a",42,11,24},
    }{
        if _,err:=store.LookupUser(ctx,tc.instance,tc.user,tc.installation,tc.repository);!errors.Is(err,ErrDenied){
            t.Fatalf("cross-account binding was accessible: %+v %v",tc,err)
        }
    }
    if err:=store.Close();err!=nil{t.Fatal(err)}
    reopened,err:=OpenBindingsDB(path)
    if err!=nil{t.Fatal(err)}
    defer reopened.Close()
    if got,err:=reopened.LookupUser(ctx,"instance-a",42,11,23);err!=nil || got.Repository!="engineering/two"{
        t.Fatalf("binding not persisted %+v %v",got,err)
    }
    rec.verification.AuthorizedRepositoryIDs=[]int64{23}
    rec.resources=rec.resources[1:]
    if err:=reopened.recordVerified(ctx,rec);err!=nil{t.Fatal(err)}
    if _,err:=reopened.LookupUser(ctx,"instance-a",42,11,22);!errors.Is(err,ErrDenied){
        t.Fatalf("removed repo retained grant %v",err)
    }
    if _,err:=reopened.LookupUser(ctx,"instance-a",42,11,23);err!=nil{t.Fatal(err)}
}

func TestBindingRevocationIsImmediateAndIsolated(t *testing.T) {
    ctx:=context.Background()
    store,_:=newTestBindings(t)
    for _,tc:=range []struct{instance string;user int64}{
        {"instance-a",42},{"instance-a",43},{"instance-b",42},
    }{
        rec:=verifiedFixture(tc.instance,tc.user,11)
        if err:=store.recordVerified(ctx,rec);err!=nil{t.Fatal(err)}
    }
    if err:=store.RevokeRepository(ctx,"instance-a",42,11,22);err!=nil{t.Fatal(err)}
    if _,err:=store.LookupUser(ctx,"instance-a",42,11,22);!errors.Is(err,ErrDenied){t.Fatal("repo revoke failed")}
    if _,err:=store.LookupUser(ctx,"instance-a",42,11,23);err!=nil{t.Fatal(err)}
    if err:=store.RevokeInstallation(ctx,"instance-a",42,11);err!=nil{t.Fatal(err)}
    if _,err:=store.LookupUser(ctx,"instance-a",42,11,23);!errors.Is(err,ErrDenied){t.Fatal("installation revoke failed")}
    if _,err:=store.LookupUser(ctx,"instance-a",43,11,22);err!=nil{t.Fatal("revoked another user",err)}
    if err:=store.RevokeUser(ctx,"instance-a",43);err!=nil{t.Fatal(err)}
    if _,err:=store.LookupUser(ctx,"instance-a",43,11,22);!errors.Is(err,ErrDenied){t.Fatal("user revoke failed")}
    if _,err:=store.LookupUser(ctx,"instance-b",42,11,22);err!=nil{t.Fatal("revoked another instance",err)}
    if err:=store.RevokeInstance(ctx,"instance-b");err!=nil{t.Fatal(err)}
    if _,err:=store.LookupUser(ctx,"instance-b",42,11,22);!errors.Is(err,ErrDenied){t.Fatal("instance revoke failed")}
    rec:=verifiedFixture("instance-a",42,11)
    if err:=store.recordVerified(ctx,rec);err!=nil{t.Fatal("verified reauthorization should restore",err)}
    if _,err:=store.LookupUser(ctx,"instance-a",42,11,22);err!=nil{t.Fatal(err)}
}

func TestInvalidVerificationCannotOverwriteExistingBinding(t *testing.T) {
    ctx:=context.Background()
    store,_:=newTestBindings(t)
    valid:=verifiedFixture("instance-a",42,11)
    if err:=store.recordVerified(ctx,valid);err!=nil{t.Fatal(err)}
    cases:=[]struct{name string;mutate func(*verifiedRecord)}{
        {"unbound-subject",func(r *verifiedRecord){r.verification.Subject="instance-b:42"}},
        {"wrong-user",func(r *verifiedRecord){r.verification.GitHubUserID=778}},
        {"unavailable-id",func(r *verifiedRecord){r.verification.AuthorizedRepositoryIDs=[]int64{22,25}}},
        {"duplicate-repo",func(r *verifiedRecord){r.verification.AuthorizedRepositoryIDs=[]int64{22,22}}},
        {"injected-url",func(r *verifiedRecord){r.resources[0].URL="https://evil.example/engineering/one"}},
        {"invalid-account",func(r *verifiedRecord){r.verification.AccountType="SomethingElse"}},
        {"other-repository-name",func(r *verifiedRecord){r.resources[0].Name="engineering/../other"}},
    }
    for _,tc:=range cases{
        t.Run(tc.name,func(t *testing.T){
            rec:=verifiedFixture("instance-a",42,11)
            tc.mutate(&rec)
            if err:=store.recordVerified(ctx,rec);err==nil{t.Fatal("unverified metadata accepted")}
            if _,err:=store.LookupUser(ctx,"instance-a",42,11,22);err!=nil{t.Fatal("valid binding disappeared",err)}
        })
    }
    store.now=func()time.Time{return time.Now().Add(25*time.Hour)}
    if _,err:=store.LookupUser(ctx,"instance-a",42,11,22);!errors.Is(err,ErrDenied){
        t.Fatalf("expired binding still valid: %v",err)
    }
}

func TestBindingStoreRefusesNonDurableOrUnsafePath(t *testing.T) {
    for _,path:=range []string{"",":memory:","file:temporary","relative.db","/tmp/invalid?mode=memory"}{
        if s,err:=OpenBindingsDB(path);err==nil {
            s.Close()
            t.Fatalf("unsafe DB path accepted: %s",path)
        }
    }
    store,_:=newTestBindings(t)
    if err:=store.recordVerified(context.Background(),verifiedFixture("instance-a",42,11));err!=nil{t.Fatal(err)}
    if _,err:=store.LookupUser(context.Background(),"instance-a",42,11,999);!errors.Is(err,ErrDenied){
        t.Fatal("arbitrary repository granted")
    }
    if _,err:=store.LookupUser(context.Background(),"instance-a",42,11,22);err!=nil{t.Fatal(err)}
}
