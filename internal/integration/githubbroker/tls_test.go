package githubbroker

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func makeTestCertAuthority(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {t.Fatal(err)}
	now:=time.Now()
	template:=&x509.Certificate{
		SerialNumber:big.NewInt(1),Subject:pkix.Name{CommonName:"broker test client CA"},
		NotBefore:now.Add(-time.Hour),NotAfter:now.Add(24*time.Hour),
		KeyUsage:x509.KeyUsageCertSign|x509.KeyUsageCRLSign,
		BasicConstraintsValid:true,IsCA:true,
	}
	raw,err:=x509.CreateCertificate(rand.Reader,template,template,&key.PublicKey,key)
	if err!=nil{t.Fatal(err)}
	cert,err:=x509.ParseCertificate(raw)
	if err!=nil{t.Fatal(err)}
	pool:=x509.NewCertPool()
	pool.AddCert(cert)
	return cert,key,pool
}

func makeTestClientCertificate(t *testing.T,ca *x509.Certificate,caKey *ecdsa.PrivateKey,serial int64) (tls.Certificate,[32]byte) {
	t.Helper()
	key,err:=ecdsa.GenerateKey(elliptic.P256(),rand.Reader)
	if err!=nil{t.Fatal(err)}
	now:=time.Now()
	template:=&x509.Certificate{
		SerialNumber:big.NewInt(serial),Subject:pkix.Name{CommonName:"test Scrumboy instance"},
		NotBefore:now.Add(-time.Hour),NotAfter:now.Add(time.Hour),
		KeyUsage:x509.KeyUsageDigitalSignature,
		ExtKeyUsage:[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	raw,err:=x509.CreateCertificate(rand.Reader,template,ca,&key.PublicKey,caKey)
	if err!=nil{t.Fatal(err)}
	return tls.Certificate{Certificate:[][]byte{raw,ca.Raw},PrivateKey:key},sha256.Sum256(raw)
}

func TestBrokerRealMutualTLSRefusesUnpinnedAndUnauthenticatedClients(t *testing.T) {
	ca,caKey,roots:=makeTestCertAuthority(t)
	goodCert,goodHash:=makeTestClientCertificate(t,ca,caKey,2)
	wrongCert,_:=makeTestClientCertificate(t,ca,caKey,3)
	instance:="vega-test"
	grant:=Grant{InstanceID:instance,InstallationID:11,RepositoryID:22,
		Repository:"org/approved",ExpiresAt:time.Now().Add(time.Hour)}
	snapshot:=&Snapshot{
		clients:map[[32]byte]string{goodHash:instance},
		grants:map[grantKey]Grant{{instance,11,22}:grant},
	}
	provider:=&itemStub{}
	broker,err:=New(snapshot,snapshot,provider)
	if err!=nil{t.Fatal(err)}
	server:=httptest.NewUnstartedServer(broker)
	server.TLS=&tls.Config{MinVersion:tls.VersionTLS13,ClientAuth:tls.RequireAndVerifyClientCert,ClientCAs:roots}
	server.StartTLS()
	defer server.Close()

	perform:=func(cert *tls.Certificate) (*http.Response,error) {
		transport:=server.Client().Transport.(*http.Transport).Clone()
		transport.TLSClientConfig=transport.TLSClientConfig.Clone()
		if cert!=nil{transport.TLSClientConfig.Certificates=[]tls.Certificate{*cert}}
		defer transport.CloseIdleConnections()
		client:=&http.Client{Transport:transport,Timeout:5*time.Second}
		request,err:=http.NewRequest(http.MethodPost,server.URL+"/v1/github/items/resolve",strings.NewReader(validResolve))
		if err!=nil{t.Fatal(err)}
		request.Header.Set("Content-Type","application/json")
		request.Header.Set("X-Forwarded-Client-Cert","forged-instance")
		return client.Do(request)
	}

	if response,err:=perform(nil);err==nil {
		response.Body.Close()
		t.Fatal("client without TLS certificate reached broker")
	}
	if response,err:=perform(&wrongCert);err!=nil{
		t.Fatalf("test certificate signed by trusted CA should reach handler: %v",err)
	}else{
		response.Body.Close()
		if response.StatusCode!=http.StatusUnauthorized{t.Fatalf("unpinned client accepted %d",response.StatusCode)}
	}
	response,err:=perform(&goodCert)
	if err!=nil{t.Fatal(err)}
	defer response.Body.Close()
	if response.StatusCode!=http.StatusOK || response.TLS==nil || response.TLS.Version!=tls.VersionTLS13 ||
		provider.resolveCalls!=1 {
		t.Fatalf("mTLS authorized request failed status=%d version=%v calls=%d",
			response.StatusCode,response.TLS,provider.resolveCalls)
	}
}
