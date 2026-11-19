package githubbroker

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeSnapshot(t *testing.T, value any) string {
	t.Helper()
	data,err:=json.Marshal(value)
	if err!=nil{t.Fatal(err)}
	path:=filepath.Join(t.TempDir(),"broker-grants.json")
	if err:=os.WriteFile(path,data,0600);err!=nil{t.Fatal(err)}
	return path
}

func testSnapshotData() map[string]any {
	digest:=sha256.Sum256([]byte("certificate-a"))
	return map[string]any{
		"clients":[]any{map[string]any{"instanceId":"instance-a","certificateSha256":hex.EncodeToString(digest[:])}},
		"grants":[]any{map[string]any{"instanceId":"instance-a","installationId":11,
			"repositoryId":22,"repository":"org/approved","expiresAt":time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}},
	}
}

func TestSnapshotAuthenticatesVerifiedAndPinnedClientCertificates(t *testing.T) {
	snapshot,err:=LoadSnapshot(writeSnapshot(t,testSnapshotData()))
	if err!=nil{t.Fatal(err)}
	request:=httptest.NewRequest("POST","https://example.test/v1/github/items/resolve",strings.NewReader("{}"))
	request.Header.Set("X-Forwarded-Client-Cert","certificate-a")
	if _,err:=snapshot.Identify(request);!errors.Is(err,ErrDenied){
		t.Fatalf("forwarded certificate header accepted: %v",err)
	}
	cert:=&x509.Certificate{Raw:[]byte("certificate-a")}
	request.TLS=&tls.ConnectionState{PeerCertificates:[]*x509.Certificate{cert}}
	if _,err:=snapshot.Identify(request);!errors.Is(err,ErrDenied){
		t.Fatalf("unverified certificate accepted: %v",err)
	}
	request.TLS.VerifiedChains=[][]*x509.Certificate{{cert}}
	instance,err:=snapshot.Identify(request)
	if err!=nil || instance!="instance-a"{t.Fatalf("valid pinned client denied %q %v",instance,err)}
	request.TLS.PeerCertificates=[]*x509.Certificate{{Raw:[]byte("certificate-b")}}
	if _,err:=snapshot.Identify(request);!errors.Is(err,ErrDenied){
		t.Fatalf("unregistered certificate accepted: %v",err)
	}
}

func TestSnapshotLimitsGrantsToOneInstanceAndOneRepository(t *testing.T) {
	snapshot,err:=LoadSnapshot(writeSnapshot(t,testSnapshotData()))
	if err!=nil{t.Fatal(err)}
	ctx:=context.Background()
	g,err:=snapshot.Lookup(ctx,"instance-a",11,22)
	if err!=nil || g.Repository!="org/approved"{t.Fatalf("valid grant %+v %v",g,err)}
	for _,test:=range []struct{instance string;installation,repository int64}{
		{"instance-b",11,22},
		{"instance-a",12,22},
		{"instance-a",11,23},
	}{
		if _,err:=snapshot.Lookup(ctx,test.instance,test.installation,test.repository);!errors.Is(err,ErrDenied){
			t.Fatalf("cross-boundary grant accepted %+v %v",test,err)
		}
	}
	cancelled,cancel:=context.WithCancel(ctx)
	cancel()
	if _,err:=snapshot.Lookup(cancelled,"instance-a",11,22);!errors.Is(err,ErrDenied){
		t.Fatal("cancelled request received grant")
	}
}

func TestSnapshotInvalidInputFailsClosed(t *testing.T) {
	for _,test:=range []struct{name string;mutate func(map[string]any)}{
		{"wrong-repository",func(data map[string]any){data["grants"].([]any)[0].(map[string]any)["repository"]="org/../private"}},
		{"expired",func(data map[string]any){data["grants"].([]any)[0].(map[string]any)["expiresAt"]=time.Now().Add(-time.Second).Format(time.RFC3339)}},
		{"unbounded-expiry",func(data map[string]any){data["grants"].([]any)[0].(map[string]any)["expiresAt"]=time.Now().Add(90*24*time.Hour).Format(time.RFC3339)}},
		{"other-instance",func(data map[string]any){data["grants"].([]any)[0].(map[string]any)["instanceId"]="instance-b"}},
		{"duplicate-client",func(data map[string]any){clients:=data["clients"].([]any);data["clients"]=append(clients,clients[0])}},
		{"duplicate-grant",func(data map[string]any){grants:=data["grants"].([]any);data["grants"]=append(grants,grants[0])}},
		{"malformed-fingerprint",func(data map[string]any){data["clients"].([]any)[0].(map[string]any)["certificateSha256"]="not-a-fingerprint"}},
		{"no-grants",func(data map[string]any){data["grants"]=[]any{}}},
		{"no-clients",func(data map[string]any){data["clients"]=[]any{}}},
	}{
		t.Run(test.name,func(t *testing.T){
			data:=testSnapshotData()
			test.mutate(data)
			if _,err:=LoadSnapshot(writeSnapshot(t,data));!errors.Is(err,ErrUnavailable){
				t.Fatalf("unsafe grant file accepted: %v",err)
			}
		})
	}
}

func TestSnapshotRejectsUnrecognizedAndOversizedConfiguration(t *testing.T) {
	file:=writeSnapshot(t,testSnapshotData())
	content,err:=os.ReadFile(file)
	if err!=nil{t.Fatal(err)}
	content=[]byte(strings.Replace(string(content),`"clients":`,`"unknown":true,"clients":`,1))
	if err:=os.WriteFile(file,content,0600);err!=nil{t.Fatal(err)}
	if _,err:=LoadSnapshot(file);!errors.Is(err,ErrUnavailable){t.Fatal("unknown field accepted")}
	if err:=os.WriteFile(file,[]byte(strings.Repeat("x",1<<20+1)),0600);err!=nil{t.Fatal(err)}
	if _,err:=LoadSnapshot(file);!errors.Is(err,ErrUnavailable){t.Fatal("oversized grant file accepted")}
}

func TestSnapshotMissingConfigFailsClosed(t *testing.T) {
	if _,err:=LoadSnapshot("");!errors.Is(err,ErrUnavailable){t.Fatal("blank grants config accepted")}
	if _,err:=LoadSnapshot(filepath.Join(t.TempDir(),"absent.json"));!errors.Is(err,ErrUnavailable){
		t.Fatal("missing grants config accepted")
	}
}
