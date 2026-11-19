package githubconnect

import (
    "context"
    "crypto/sha256"
    "encoding/base64"
    "encoding/json"
    "errors"
    "net/http"
    "net/http/httptest"
    "net/url"
    "strings"
    "sync/atomic"
    "testing"
    "time"
)

func testService(t *testing.T, handler http.HandlerFunc) *Service {
    t.Helper()
    server:=httptest.NewServer(handler)
    t.Cleanup(server.Close)
    service,err:=newService(Config{
        AppID:123,ClientID:"Iv1.scrumboy-test",ClientSecret:"secret-value-for-github-oauth",
        RedirectURI:"https://example.test/integrations/github/callback",
    },server.Client(),server.URL,server.URL)
    if err!=nil{t.Fatal(err)}
    return service
}

func flowState(t *testing.T, uri string) string {
    t.Helper()
    parsed,err:=url.Parse(uri)
    if err!=nil{t.Fatal(err)}
    if parsed.Path!="/login/oauth/authorize" || parsed.Query().Get("code_challenge_method")!="S256" ||
        parsed.Query().Get("client_id")!="Iv1.scrumboy-test"{t.Fatalf("invalid authorization URL %s",parsed.Path)}
    state:=parsed.Query().Get("state")
    if len(state)!=43{t.Fatal("weak OAuth state")}
    return state
}

func TestVerifiedInstallationRequiresUserTokenAndOneTimePKCE(t *testing.T) {
    var exchanges, profiles, installs atomic.Int64
    var challenge string
    service:=testService(t,func(w http.ResponseWriter,r *http.Request){
        switch r.URL.Path{
        case "/login/oauth/access_token":
            exchanges.Add(1)
            if r.Method!=http.MethodPost || r.Header.Get("Content-Type")!="application/x-www-form-urlencoded"{
                t.Error("invalid exchange request")
            }
            if err:=r.ParseForm();err!=nil{t.Error(err)}
            if r.Form.Get("client_secret")!="secret-value-for-github-oauth" || r.Form.Get("code")!="valid-code"{
                t.Error("incorrect exchange")
            }
            verifier:=r.Form.Get("code_verifier")
            digest:=sha256.Sum256([]byte(verifier))
            if base64.RawURLEncoding.EncodeToString(digest[:])!=challenge{t.Error("invalid PKCE")}
            json.NewEncoder(w).Encode(map[string]any{"access_token":"github-user-token-long-value","token_type":"bearer"})
        case "/user":
            profiles.Add(1)
            if r.Header.Get("Authorization")!="Bearer github-user-token-long-value"{t.Error("missing user token")}
            json.NewEncoder(w).Encode(map[string]any{"id":444,"login":"other-user"})
        case "/user/installations":
            installs.Add(1)
            json.NewEncoder(w).Encode(map[string]any{"total_count":2,"installations":[]any{
                map[string]any{"id":88,"app_id":999,"target_type":"User","account":map[string]any{"id":555,"login":"unrelated"}},
                map[string]any{"id":77,"app_id":123,"target_type":"Organization","account":map[string]any{"id":666,"login":"sample-org"}},
            }})
        default:http.NotFound(w,r)
        }
    })
    uri,err:=service.Begin("instance:4:user:9")
    if err!=nil{t.Fatal(err)}
    parsed,_:=url.Parse(uri)
    challenge=parsed.Query().Get("code_challenge")
    state:=flowState(t,uri)
    if _,err=service.Complete(context.Background(),"instance:5:user:9",state,"valid-code",77);!errors.Is(err,ErrInvalidFlow){
        t.Fatalf("wrong subject accepted: %v",err)
    }
    result,err:=service.Complete(context.Background(),"instance:4:user:9",state,"valid-code",77)
    if err!=nil{t.Fatal(err)}
    if result.Subject!="instance:4:user:9" || result.GitHubUserID!=444 ||
        result.InstallationID!=77 || result.AccountID!=666 || result.AccountLogin!="sample-org" ||
        result.AccountType!="Organization" {t.Fatalf("wrong verified identity %+v",result)}
    if exchanges.Load()!=1 || profiles.Load()!=1 || installs.Load()!=1 {t.Fatal("unexpected GitHub requests")}
    if _,err=service.Complete(context.Background(),"instance:4:user:9",state,"valid-code",77);!errors.Is(err,ErrInvalidFlow){
        t.Fatalf("replay accepted: %v",err)
    }
    if exchanges.Load()!=1{t.Fatal("replay exchanged code")}
}

func TestOtherAppAndSuspendedInstallationsCannotBeClaimed(t *testing.T) {
    for _,tc:=range []struct{name string;actualID,appID,requestedID int64;suspended bool}{
        {"wrong-installation",77,123,88,false},
        {"wrong-app",77,999,77,false},
        {"suspended",77,123,77,true},
    }{
        t.Run(tc.name,func(t *testing.T){
            service:=testService(t,func(w http.ResponseWriter,r *http.Request){
                switch r.URL.Path{
                case "/login/oauth/access_token":
                    json.NewEncoder(w).Encode(map[string]any{"access_token":"github-user-token-long-value","token_type":"bearer"})
                case "/user":
                    json.NewEncoder(w).Encode(map[string]any{"id":444,"login":"test-user"})
                case "/user/installations":
                    var suspended any
                    if tc.suspended{suspended="2026-11-19T00:00:00Z"}
                    json.NewEncoder(w).Encode(map[string]any{"total_count":1,"installations":[]any{
                        map[string]any{"id":tc.actualID,"app_id":tc.appID,"target_type":"User",
                            "suspended_at":suspended,"account":map[string]any{"id":444,"login":"test-user"}},
                    }})
                default:http.NotFound(w,r)
                }
            })
            uri,err:=service.Begin("tenant:1:user:3")
            if err!=nil{t.Fatal(err)}
            _,err=service.Complete(context.Background(),"tenant:1:user:3",flowState(t,uri),"valid-code",tc.requestedID)
            if !errors.Is(err,ErrNotAuthorized){t.Fatalf("unauthorized installation accepted: %v",err)}
        })
    }
}

func TestExpiredStateAndRedirectFailClosed(t *testing.T) {
    var requests atomic.Int64
    service:=testService(t,func(w http.ResponseWriter,r *http.Request){
        requests.Add(1)
        w.Header().Set("Location","/steal")
        w.WriteHeader(http.StatusFound)
        w.Write([]byte("access_token=secret-token-that-must-not-leak"))
    })
    uri,err:=service.Begin("user:1")
    if err!=nil{t.Fatal(err)}
    service.now=func()time.Time{return time.Now().Add(11*time.Minute)}
    _,err=service.Complete(context.Background(),"user:1",flowState(t,uri),"code",77)
    if !errors.Is(err,ErrInvalidFlow) || requests.Load()!=0{t.Fatalf("expired state accepted: %v",err)}
    service.now=time.Now
    uri,err=service.Begin("user:1")
    if err!=nil{t.Fatal(err)}
    _,err=service.Complete(context.Background(),"user:1",flowState(t,uri),"code",77)
    if err==nil || strings.Contains(err.Error(),"secret-token-that-must-not-leak") || requests.Load()!=1{
        t.Fatalf("redirect with OAuth credentials accepted: %v",err)
    }
}

func TestPartialInstallationsAndCapacityFailClosed(t *testing.T) {
    service:=testService(t,func(w http.ResponseWriter,r *http.Request){
        switch r.URL.Path{
        case "/login/oauth/access_token":
            json.NewEncoder(w).Encode(map[string]any{"access_token":"github-user-token-long-value","token_type":"bearer"})
        case "/user":
            json.NewEncoder(w).Encode(map[string]any{"id":444,"login":"test-user"})
        case "/user/installations":
            json.NewEncoder(w).Encode(map[string]any{"total_count":101,"installations":[]any{
                map[string]any{"id":77,"app_id":123,"target_type":"User","account":map[string]any{"id":444,"login":"test-user"}},
            }})
        default:http.NotFound(w,r)
        }
    })
    uri,err:=service.Begin("user:1")
    if err!=nil{t.Fatal(err)}
    _,err=service.Complete(context.Background(),"user:1",flowState(t,uri),"valid-code",77)
    if !errors.Is(err,ErrUnavailable){t.Fatalf("partial installation listing accepted: %v",err)}
    if _,err=service.Begin("");!errors.Is(err,ErrInvalidFlow){t.Fatalf("empty subject accepted: %v",err)}
    for i:=0;i<maxPendingFlows;i++{
        if _,err=service.Begin("user:1");err!=nil{t.Fatalf("flow capacity wrong: %v",err)}
    }
    if _,err=service.Begin("user:1");!errors.Is(err,ErrCapacity){t.Fatalf("unbounded pending flow count: %v",err)}
}
