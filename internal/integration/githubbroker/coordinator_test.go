package githubbroker

import (
    "context"
    "errors"
    "strconv"
    "testing"

    "scrumboy/internal/integration"
    "scrumboy/internal/integration/githubconnect"
)

type verificationStub struct {
    verification githubconnect.Verification
    err error
    begins int
    completes int
    cancellations int
}

func (f *verificationStub) Begin(subject string) (string,error) {
    f.begins++
    return "https://github.com/login/oauth/authorize?state="+subject,nil
}

func (f *verificationStub) Cancel(subject string) {
    f.cancellations++
}

func (f *verificationStub) Complete(ctx context.Context,subject,state,code string,installationID int64) (githubconnect.Verification,error) {
    f.completes++
    if f.err!=nil{return githubconnect.Verification{},f.err}
    return f.verification,nil
}

type resourceStub struct {
    items []integration.Resource
    err error
    calls int
    lastInstallation string
}

func (r *resourceStub) Resources(ctx context.Context,installationID string) ([]integration.Resource,error) {
    r.calls++
    r.lastInstallation=installationID
    if r.err!=nil{return nil,r.err}
    return r.items,nil
}

func TestCoordinatorOnlyPersistsVerifiedIntersection(t *testing.T) {
    store,_:=newTestBindings(t)
    valid:=verifiedFixture("instance-a",42,11)
    vf:=&verificationStub{verification:valid.verification}
    reader:=&resourceStub{items:append([]integration.Resource{
        {ID:"99",Name:"engineering/secret",URL:"https://github.com/engineering/secret"},
    },valid.resources...)}
    coordinator,err:=NewBindingCoordinator(vf,reader,store)
    if err!=nil{t.Fatal(err)}
    if _,err:=coordinator.Begin("",42);!errors.Is(err,ErrDenied){t.Fatal("untrusted instance began OAuth")}
    uri,err:=coordinator.Begin("instance-a",42)
    if err!=nil || vf.begins!=1 || uri==""{t.Fatalf("begin %s %v",uri,err)}
    summary,err:=coordinator.Complete(context.Background(),"instance-a",42,"state","code",11)
    if err!=nil{t.Fatal(err)}
    if summary.GitHubUserID!=777 || summary.InstallationID!=11 || len(summary.RepositoryIDs)!=2 ||
        reader.lastInstallation!="11" || reader.calls!=1 {
        t.Fatalf("bad summary %+v calls=%d",summary,reader.calls)
    }
    if _,err:=store.LookupUser(context.Background(),"instance-a",42,11,99);!errors.Is(err,ErrDenied){
        t.Fatal("App-global but non-user-authorized repo was granted")
    }
    for _,id:=range []int64{22,23}{
        if _,err:=store.LookupUser(context.Background(),"instance-a",42,11,id);err!=nil{
            t.Fatalf("user-authorized repo %d denied: %v",id,err)
        }
    }
}

func TestCoordinatorRefusesForgedOAuthAndIncompleteMetadataWithoutWriting(t *testing.T) {
    for _,tc:=range []struct{name string;change func(*verificationStub,*resourceStub)}{
        {"wrong-subject",func(v *verificationStub,r *resourceStub){v.verification.Subject="instance-b:42"}},
        {"wrong-installation",func(v *verificationStub,r *resourceStub){v.verification.InstallationID=12}},
        {"wrong-repository",func(v *verificationStub,r *resourceStub){r.items=r.items[:1]}},
        {"duplicate-provider-repo",func(v *verificationStub,r *resourceStub){r.items=append(r.items,r.items[0])}},
        {"provider-unavailable",func(v *verificationStub,r *resourceStub){r.err=errors.New("service unavailable")}},
        {"oauth-unverified",func(v *verificationStub,r *resourceStub){v.err=githubconnect.ErrNotAuthorized}},
        {"wrong-url",func(v *verificationStub,r *resourceStub){r.items[0].URL="https://evil.invalid/repo"}},
        {"wrong-id",func(v *verificationStub,r *resourceStub){r.items[0].ID="not-a-number"}},
    }{
        t.Run(tc.name,func(t *testing.T){
            store,_:=newTestBindings(t)
            r:=verifiedFixture("instance-a",42,11)
            v:=&verificationStub{verification:r.verification}
            reader:=&resourceStub{items:r.resources}
            tc.change(v,reader)
            b,err:=NewBindingCoordinator(v,reader,store)
            if err!=nil{t.Fatal(err)}
            if _,err=b.Complete(context.Background(),"instance-a",42,"state","code",11);err==nil{
                t.Fatal("invalid verified binding accepted")
            }
            if _,err=store.LookupUser(context.Background(),"instance-a",42,11,22);!errors.Is(err,ErrDenied){
                t.Fatal("invalid binding persisted")
            }
        })
    }
}

func TestCoordinatorDoesNotAcceptInvalidOrZeroIdentifiers(t *testing.T) {
    store,_:=newTestBindings(t)
    v:=&verificationStub{}
    reader:=&resourceStub{}
    b,err:=NewBindingCoordinator(v,reader,store)
    if err!=nil{t.Fatal(err)}
    for _,tc:=range []struct{instance string;user,install int64}{
        {"",42,11},{"instance-a",0,11},{"instance-a",42,0},{"instance/forged",42,11},
    }{
        if _,err:=b.Complete(context.Background(),tc.instance,tc.user,"state","code",tc.install);
            !errors.Is(err,ErrDenied){t.Fatalf("unsafe begin %+v %v",tc,err)}
    }
    if v.completes!=0 || reader.calls!=0{t.Fatal("invalid request reached OAuth or GitHub")}
    if bindingSubject("instance-a",42)!="instance-a:"+strconv.FormatInt(42,10){t.Fatal("subject collision")}
}

func TestCoordinatorRevocationCancelsPendingOAuthAndPreservesOtherUsers(t *testing.T) {
    store,_:=newTestBindings(t)
    ctx:=context.Background()
    for _,user:=range []int64{42,43}{
        if err:=store.recordVerified(ctx,verifiedFixture("instance-a",user,11));err!=nil{t.Fatal(err)}
    }
    fixture:=verifiedFixture("instance-a",42,11)
    vf:=&verificationStub{verification:fixture.verification}
    reader:=&resourceStub{items:fixture.resources}
    coordinator,err:=NewBindingCoordinator(vf,reader,store)
    if err!=nil{t.Fatal(err)}
    if err:=coordinator.RevokeRepository(ctx,"instance-a",42,11,22);err!=nil{t.Fatal(err)}
    if vf.cancellations!=1{t.Fatal("OAuth state not invalidated on repo revoke")}
    if _,err:=store.LookupUser(ctx,"instance-a",42,11,22);!errors.Is(err,ErrDenied){t.Fatal("repo not revoked")}
    if _,err:=store.LookupUser(ctx,"instance-a",42,11,23);err!=nil{t.Fatal("other repo lost access",err)}
    if err:=coordinator.RevokeInstallation(ctx,"instance-a",42,11);err!=nil{t.Fatal(err)}
    if vf.cancellations!=2{t.Fatal("OAuth state not invalidated on installation revoke")}
    if _,err:=store.LookupUser(ctx,"instance-a",42,11,23);!errors.Is(err,ErrDenied){t.Fatal("installation not revoked")}
    if _,err:=store.LookupUser(ctx,"instance-a",43,11,22);err!=nil{t.Fatal("other user lost access",err)}
    if err:=coordinator.RevokeUser(ctx,"instance-a",43);err!=nil{t.Fatal(err)}
    if vf.cancellations!=3{t.Fatal("OAuth state not invalidated on user revoke")}
    if _,err:=store.LookupUser(ctx,"instance-a",43,11,22);!errors.Is(err,ErrDenied){t.Fatal("user not revoked")}
}
