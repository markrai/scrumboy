package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	key,err:=rsa.GenerateKey(rand.Reader,2048)
	if err!=nil{t.Fatal(err)}
	pemBytes:=pem.EncodeToMemory(&pem.Block{Type:"RSA PRIVATE KEY",Bytes:x509.MarshalPKCS1PrivateKey(key)})
	client,err:=newClient(123,pemBytes,nil,serverURL)
	if err!=nil{t.Fatal(err)}
	return client
}

func TestGitHubAppInstallationDiscoveryAndRefresh(t *testing.T) {
	var tokenRequests int
	var repoRequests int
	now:=time.Now().UTC().Truncate(time.Second)
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"),"Bearer ") {t.Errorf("missing bearer header")}
		switch {
		case strings.HasPrefix(r.URL.Path,"/app/installations") && r.Method==http.MethodGet:
			json.NewEncoder(w).Encode([]any{map[string]any{"id":44,"account":map[string]any{"login":"example"},"target_type":"Organization"}})
		case r.URL.Path=="/app/installations/44/access_tokens" && r.Method==http.MethodPost:
			tokenRequests++
			if r.Header.Get("Authorization")=="Bearer gh-test-token-long-enough" {t.Error("installation token used for app mint")}
			var input struct {Permissions map[string]string `json:"permissions"`}
			if err:=json.NewDecoder(r.Body).Decode(&input);err!=nil{t.Error(err)}
			if input.Permissions["metadata"]!="read"{t.Errorf("unexpected permission: %+v",input.Permissions)}
			json.NewEncoder(w).Encode(map[string]any{"token":"gh-test-token-long-enough","expires_at":now.Add(60*time.Minute).Format(time.RFC3339)})
		case r.URL.Path=="/installation/repositories" && r.Method==http.MethodGet:
			repoRequests++
			if r.Header.Get("Authorization")!="Bearer gh-test-token-long-enough"{t.Error("missing installation token")}
			json.NewEncoder(w).Encode(map[string]any{"total_count":1,"repositories":[]any{map[string]any{"id":567,"full_name":"example/repo","html_url":"https://github.com/example/repo"}}})
		default:
			http.NotFound(w,r)
		}
	}))
	defer server.Close()
	c:=testClient(t,server.URL)
	c.now=func() time.Time{return now}
	installations,err:=c.Installations(context.Background())
	if err!=nil || len(installations)!=1 || installations[0].ID!=44 {t.Fatalf("installations: %+v %v",installations,err)}
	for i:=0;i<2;i++ {
		resources,err:=c.Resources(context.Background(),44)
		if err!=nil || len(resources)!=1 || resources[0].ID!=567 {t.Fatalf("resources: %+v %v",resources,err)}
	}
	if tokenRequests!=1 || repoRequests!=2 {t.Fatalf("unexpected caching: tokens %d repositories %d",tokenRequests,repoRequests)}
	now=now.Add(56*time.Minute)
	_,err=c.Resources(context.Background(),44)
	if err!=nil {t.Fatal(err)}
	if tokenRequests!=2 {t.Fatalf("expired token not refreshed: %d",tokenRequests)}
	c.Invalidate(44)
	_,err=c.Resources(context.Background(),44)
	if err!=nil || tokenRequests!=3 {t.Fatalf("token not invalidated: %d %v",tokenRequests,err)}
}

func TestGitHubAppIncompleteDiscoveryNeverReturnsPartialResources(t *testing.T) {
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path,"access_tokens"):
			json.NewEncoder(w).Encode(map[string]any{"token":"gh-test-token-long-enough","expires_at":time.Now().Add(time.Hour).Format(time.RFC3339)})
		case r.URL.Path=="/installation/repositories":
			json.NewEncoder(w).Encode(map[string]any{"total_count":2,"repositories":[]any{map[string]any{"id":12,"full_name":"org/repo","html_url":"https://github.com/org/repo"}}})
		default:
			http.NotFound(w,r)
		}
	}))
	defer server.Close()
	c:=testClient(t,server.URL)
	resources,err:=c.Resources(context.Background(),44)
	if len(resources)!=0 || !errors.Is(err,ErrIncomplete) {t.Fatalf("incomplete discovery: %+v %v",resources,err)}
}

func TestGitHubAppRevokedTokenInvalidatesCacheAndDoesNotExposeSecrets(t *testing.T) {
	var minted int
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path,"access_tokens"):
			minted++
			json.NewEncoder(w).Encode(map[string]any{"token":"gh-test-token-long-enough","expires_at":time.Now().Add(time.Hour).Format(time.RFC3339)})
		case r.URL.Path=="/installation/repositories":
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("gh-test-token-long-enough"))
		default: http.NotFound(w,r)
		}
	}))
	defer server.Close()
	c:=testClient(t,server.URL)
	for i:=0;i<2;i++{
		_,err:=c.Resources(context.Background(),44)
		if !errors.Is(err,ErrRevoked) || strings.Contains(err.Error(),"gh-test-token-long-enough") {t.Fatalf("revocation: %v",err)}
	}
	if minted!=2 {t.Fatalf("revoked token reused: %d",minted)}
}
