package githubclient

import (
    "context"
    "crypto/ecdsa"
    "crypto/elliptic"
    "crypto/rand"
    "crypto/tls"
    "crypto/x509"
    "math/big"
    "encoding/json"
    "errors"
    "io"
    "net/http"
    "net/http/httptest"
    "net/url"
    "strings"
    "sync/atomic"
    "testing"
    "time"
)

func testCertificate(t *testing.T) (tls.Certificate,*x509.CertPool){
    t.Helper()
    key,err:=ecdsa.GenerateKey(elliptic.P256(),rand.Reader)
    if err!=nil{t.Fatal(err)}
    now:=time.Now()
    template:=&x509.Certificate{
        SerialNumber:big.NewInt(10),NotBefore:now.Add(-time.Hour),NotAfter:now.Add(time.Hour),
        KeyUsage:x509.KeyUsageDigitalSignature|x509.KeyUsageCertSign,
        ExtKeyUsage:[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
        BasicConstraintsValid:true,IsCA:true,
    }
    der,err:=x509.CreateCertificate(rand.Reader,template,template,&key.PublicKey,key)
    if err!=nil{t.Fatal(err)}
    root,err:=x509.ParseCertificate(der)
    if err!=nil{t.Fatal(err)}
    roots:=x509.NewCertPool()
    roots.AddCert(root)
    return tls.Certificate{Certificate:[][]byte{der},PrivateKey:key},roots
}

func testRemote(t *testing.T,handler http.HandlerFunc)(*Client,func()){
    t.Helper()
    certificate,clientRoots:=testCertificate(t)
    server:=httptest.NewUnstartedServer(handler)
    server.TLS=&tls.Config{MinVersion:tls.VersionTLS13,
        ClientAuth:tls.RequireAndVerifyClientCert,ClientCAs:clientRoots}
    server.StartTLS()
    roots:=x509.NewCertPool()
    roots.AddCert(server.Certificate())
    client,err:=New(Config{Endpoint:server.URL,Certificate:certificate,ServerRoots:roots})
    if err!=nil{t.Fatal(err)}
    return client,func(){client.Close();server.Close()}
}

func TestSelfHostedClientUsesMutualTLSAndOnlyFixedReadOnlyBrokerPaths(t *testing.T){
    var requests atomic.Int64
    c,closeRemote:=testRemote(t,func(w http.ResponseWriter,r *http.Request){
        requests.Add(1)
        if r.TLS==nil||r.TLS.Version!=tls.VersionTLS13||len(r.TLS.PeerCertificates)!=1{
            t.Error("request was not mutually authenticated TLS 1.3")
        }
        if r.Method!=http.MethodPost||r.Header.Get("Content-Type")!="application/json"{
            t.Error("unexpected broker request")
        }
        var payload map[string]any
        if err:=json.NewDecoder(r.Body).Decode(&payload);err!=nil{t.Error(err)}
        if payload["userId"]!=float64(42){t.Error("incorrect local user scope")}
        switch r.URL.Path{
        case "/v1/github/items/resolve":
            if payload["repositoryId"]!=float64(22)||payload["installationId"]!=float64(11)||
                payload["kind"]!="issue"||payload["number"]!=float64(357){
                t.Error("lookup escaped requested scope")
            }
            io.WriteString(w,`{"item":{"kind":"issue","number":357,"title":"Secure ref","state":"open","url":"https://github.com/org/repo/issues/357"}}`)
        case "/v1/github/items/search":
            if payload["query"]!="security"||payload["kind"]!="pr"{
                t.Error("search escaped requested scope")
            }
            io.WriteString(w,`{"items":[{"kind":"pr","number":5,"title":"Fix","state":"closed","url":"https://github.com/org/repo/pull/5","merged":true}]}`)
        case "/v1/github/connections/begin":
            io.WriteString(w,`{"authorizationUrl":"https://github.com/login/oauth/authorize?client_id=Iv1.test&state=`+
                strings.Repeat("a",43)+`&code_challenge=`+strings.Repeat("b",43)+`&code_challenge_method=S256"}`)
        case "/v1/github/connections/complete":
            if payload["code"]!="one-time-code"{t.Error("OAuth code changed")}
            io.WriteString(w,`{"binding":{"InstanceID":"instance-a","UserID":42,"GitHubUserID":777,"InstallationID":11,"AccountID":456,"RepositoryIDs":[22]}}`)
        case "/v1/github/connections/revoke":
            w.WriteHeader(http.StatusNoContent)
        default:
            http.NotFound(w,r)
        }
    })
    defer closeRemote()
    ctx:=context.Background()
    item,err:=c.Resolve(ctx,42,11,22,"issue",357)
    if err!=nil||item.Title!="Secure ref"{t.Fatalf("resolve failed %+v %v",item,err)}
    items,err:=c.Search(ctx,42,11,22,"pr","security")
    if err!=nil||len(items)!=1||!items[0].Merged{t.Fatalf("search failed %+v %v",items,err)}
    auth,err:=c.Begin(ctx,42)
    if err!=nil||!strings.HasPrefix(auth,"https://github.com/login/oauth/authorize?"){
        t.Fatalf("invalid OAuth URL: %v",err)
    }
    binding,err:=c.Complete(ctx,42,11,strings.Repeat("a",43),"one-time-code")
    if err!=nil||binding.InstanceID!="instance-a"||len(binding.RepositoryIDs)!=1{
        t.Fatalf("binding failed %+v %v",binding,err)
    }
    if err:=c.Revoke(ctx,42,11,22);err!=nil{t.Fatal(err)}
    if requests.Load()!=5{t.Fatalf("unexpected request count %d",requests.Load())}
}

func TestSelfHostedClientRejectsUntrustedEndpointsAndInput(t *testing.T){
    cert,roots:=testCertificate(t)
    for _,endpoint:=range []string{
        "http://example.test","https://example.test/evil","https://user:pass@example.test",
        "https://example.test?token=abc","https://example.test#frag","javascript:alert(1)","",
    }{
        if c,err:=New(Config{Endpoint:endpoint,Certificate:cert,ServerRoots:roots});err==nil{
            c.Close()
            t.Fatalf("unsafe broker endpoint accepted: %s",endpoint)
        }
    }
    c,err:=New(Config{Endpoint:"https://example.test",Certificate:cert,ServerRoots:roots})
    if err!=nil{t.Fatal(err)}
    defer c.Close()
    ctx:=context.Background()
    if _,err=c.Resolve(ctx,0,11,22,"issue",1);!errors.Is(err,ErrInvalid){t.Fatal("missing user accepted")}
    if _,err=c.Resolve(ctx,42,11,22,"delete",1);!errors.Is(err,ErrInvalid){t.Fatal("write method accepted")}
    if _,err=c.Search(ctx,42,11,22,"issue","x");!errors.Is(err,ErrInvalid){t.Fatal("unbounded query accepted")}
    if _,err=c.Complete(ctx,42,11,"short","secret");!errors.Is(err,ErrInvalid){t.Fatal("weak state accepted")}
    if err:=c.Revoke(ctx,42,0,22);!errors.Is(err,ErrInvalid){t.Fatal("unscoped repository revoke accepted")}
}

func TestSelfHostedClientRefusesRedirectAndRedactsProviderErrors(t *testing.T){
    var followed atomic.Bool
    c,cleanup:=testRemote(t,func(w http.ResponseWriter,r *http.Request){
        if r.URL.Path=="/stolen"{followed.Store(true)}
        w.Header().Set("Location","/stolen")
        w.WriteHeader(http.StatusFound)
        io.WriteString(w,"private-token-do-not-log")
    })
    defer cleanup()
    _,err:=c.Resolve(context.Background(),42,11,22,"issue",357)
    if !errors.Is(err,ErrUnavailable)||strings.Contains(err.Error(),"private-token")||followed.Load(){
        t.Fatalf("unsafe redirect or token surfaced: %v",err)
    }
}

func TestSelfHostedClientRejectsOversizedAndWrongRepositoryMetadata(t *testing.T){
    cases:=[]struct{name string;response string}{
        {"wrong-host",`{"item":{"kind":"issue","number":357,"title":"X","state":"open","url":"https://evil.example/org/repo/issues/357"}}`},
        {"wrong-kind",`{"item":{"kind":"pr","number":357,"title":"X","state":"open","url":"https://github.com/org/repo/pull/357"}}`},
        {"oversized",strings.Repeat("A",int(maxResponseBytes)+1)},
    }
    for _,test:=range cases{
        t.Run(test.name,func(t *testing.T){
            c,cleanup:=testRemote(t,func(w http.ResponseWriter,r *http.Request){io.WriteString(w,test.response)})
            defer cleanup()
            _,err:=c.Resolve(context.Background(),42,11,22,"issue",357)
            if !errors.Is(err,ErrUnavailable){t.Fatalf("untrusted metadata accepted: %v",err)}
        })
    }
}

func TestSelfHostedClientRefusesBrokerCertificateMismatch(t *testing.T){
    cert,roots:=testCertificate(t)
    server:=httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
        io.WriteString(w,`{"item":{"kind":"issue","number":357,"title":"X","state":"open","url":"https://github.com/org/repo/issues/357"}}`)
    }))
    defer server.Close()
    client,err:=New(Config{Endpoint:server.URL,Certificate:cert,ServerRoots:roots})
    if err!=nil{t.Fatal(err)}
    defer client.Close()
    _,err=client.Resolve(context.Background(),42,11,22,"issue",357)
    if !errors.Is(err,ErrUnavailable){t.Fatalf("untrusted broker server certificate accepted: %v",err)}
}

func TestBrokerOriginCannotBeRedirectedThroughUserControlledURL(t *testing.T){
    parsed,err:=url.Parse("https://github.com/login/oauth/authorize")
    if err!=nil||parsed.Host!="github.com"{t.Fatal("unexpected canonical GitHub URL")}
}
