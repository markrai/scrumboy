package githubbroker

import (
    "context"
    "crypto/sha256"
    "crypto/tls"
    "crypto/x509"
    "errors"
    "net/http"
    "net/http/httptest"
    "testing"
)

func certificateRequest(certificateDER []byte) *http.Request {
    cert:=&x509.Certificate{Raw:certificateDER}
    request:=httptest.NewRequest(http.MethodPost,"https://broker.example/v1/github/items/resolve",nil)
    request.TLS=&tls.ConnectionState{
        PeerCertificates:[]*x509.Certificate{cert},
        VerifiedChains:[][]*x509.Certificate{{cert}},
    }
    return request
}

func TestDynamicInstanceIdentityPersistsAndRevokesImmediately(t *testing.T){
    ctx:=context.Background()
    db,path:=newTestBindings(t)
    ca,caKey,_:=makeTestCertAuthority(t)
    certA,_:=makeTestClientCertificate(t,ca,caKey,21)
    certB,_:=makeTestClientCertificate(t,ca,caKey,22)
    certC,_:=makeTestClientCertificate(t,ca,caKey,23)
    if _,err:=db.Identify(certificateRequest(certA.Certificate[0]));!errors.Is(err,ErrDenied){
        t.Fatal("unregistered certificate accepted")
    }
    if err:=db.RegisterInstanceCertificate(ctx,"instance-a",certA.Certificate[0]);err!=nil{t.Fatal(err)}
    if name,err:=db.Identify(certificateRequest(certA.Certificate[0]));err!=nil||name!="instance-a"{
        t.Fatalf("registered certificate rejected %q %v",name,err)
    }
    if err:=db.RegisterInstanceCertificate(ctx,"instance-b",certA.Certificate[0]);!errors.Is(err,ErrDenied){
        t.Fatal("same certificate registered for another instance")
    }
    if err:=db.RegisterInstanceCertificate(ctx,"instance-a",certB.Certificate[0]);err!=nil{t.Fatal(err)}
    if err:=db.RegisterInstanceCertificate(ctx,"instance-a",certC.Certificate[0]);!errors.Is(err,ErrDenied){
        t.Fatal("unbounded certificate rotation allowed")
    }
    if err:=db.RevokeInstanceCertificate(ctx,"instance-a",certA.Certificate[0]);err!=nil{t.Fatal(err)}
    if _,err:=db.Identify(certificateRequest(certA.Certificate[0]));!errors.Is(err,ErrDenied){
        t.Fatal("revoked certificate remained active")
    }
    if err:=db.RegisterInstanceCertificate(ctx,"instance-a",certA.Certificate[0]);!errors.Is(err,ErrDenied){
        t.Fatal("revoked certificate was silently reactivated")
    }
    if err:=db.RegisterInstanceCertificate(ctx,"instance-a",certC.Certificate[0]);err!=nil{t.Fatal(err)}
    if err:=db.Close();err!=nil{t.Fatal(err)}
    reopened,err:=OpenBindingsDB(path)
    if err!=nil{t.Fatal(err)}
    defer reopened.Close()
    if name,err:=reopened.Identify(certificateRequest(certB.Certificate[0]));err!=nil||name!="instance-a"{
        t.Fatalf("rotation did not survive restart %q %v",name,err)
    }
    if _,err:=reopened.Identify(certificateRequest(certA.Certificate[0]));!errors.Is(err,ErrDenied){
        t.Fatal("revocation did not persist")
    }
}

func TestDisconnectInstanceRevokesCertificatesAndExistingUserGrantsAtomically(t *testing.T){
    ctx:=context.Background()
    db,_:=newTestBindings(t)
    ca,caKey,_:=makeTestCertAuthority(t)
    certA,_:=makeTestClientCertificate(t,ca,caKey,31)
    certB,_:=makeTestClientCertificate(t,ca,caKey,32)
    for _,r:=range []struct{instance string;cert tls.Certificate;userID int64}{
        {"instance-a",certA,42},{"instance-b",certB,42},
    }{
        if err:=db.RegisterInstanceCertificate(ctx,r.instance,r.cert.Certificate[0]);err!=nil{t.Fatal(err)}
        if err:=db.recordVerified(ctx,verifiedFixture(r.instance,r.userID,11));err!=nil{t.Fatal(err)}
    }
    if err:=db.DisconnectInstance(ctx,"instance-a");err!=nil{t.Fatal(err)}
    if _,err:=db.Identify(certificateRequest(certA.Certificate[0]));!errors.Is(err,ErrDenied){
        t.Fatal("disconnected instance's certificate was active")
    }
    if _,err:=db.LookupUser(ctx,"instance-a",42,11,22);!errors.Is(err,ErrDenied){
        t.Fatal("disconnected instance retained repo grant")
    }
    if name,err:=db.Identify(certificateRequest(certB.Certificate[0]));err!=nil||name!="instance-b"{
        t.Fatalf("other instance was disconnected %q %v",name,err)
    }
    if _,err:=db.LookupUser(ctx,"instance-b",42,11,22);err!=nil{
        t.Fatal("other instance's repo grant was revoked",err)
    }
}

func TestDynamicIdentityRequiresVerifiedTLSChain(t *testing.T){
    ctx:=context.Background()
    db,_:=newTestBindings(t)
    ca,caKey,_:=makeTestCertAuthority(t)
    cert,_:=makeTestClientCertificate(t,ca,caKey,44)
    if err:=db.RegisterInstanceCertificate(ctx,"instance-a",cert.Certificate[0]);err!=nil{t.Fatal(err)}
    req:=certificateRequest(cert.Certificate[0])
    req.TLS.VerifiedChains=nil
    if _,err:=db.Identify(req);!errors.Is(err,ErrDenied){t.Fatal("unverified identity accepted")}
    req.TLS.VerifiedChains=[][]*x509.Certificate{{req.TLS.PeerCertificates[0]}}
    if _,err:=db.Identify(req);err!=nil{t.Fatal(err)}
    req.TLS.PeerCertificates[0].Raw=make([]byte,32)
    if _,err:=db.Identify(req);!errors.Is(err,ErrDenied){t.Fatal("unknown fingerprint accepted")}
    digest:=sha256.Sum256(cert.Certificate[0])
    if digest==[32]byte{}{t.Fatal("unexpected zero digest")}
}

func TestDynamicRegistryRefusesInvalidCertificates(t *testing.T){
    db,_:=newTestBindings(t)
    ctx:=context.Background()
    for _,tc:=range []struct{instance string;der []byte}{
        {"",[]byte("fake")},{"instance/forged",[]byte("fake")},{"instance-a",[]byte("fake")},
        {"instance-a",make([]byte,17<<10)},
    }{
        if err:=db.RegisterInstanceCertificate(ctx,tc.instance,tc.der);!errors.Is(err,ErrDenied){
            t.Fatalf("invalid certificate registered: %v",err)
        }
    }
}
