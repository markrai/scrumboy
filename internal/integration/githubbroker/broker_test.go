package githubbroker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"strconv"
	"testing"
	"time"

	"scrumboy/internal/integration"
)

type identityStub struct {
	instance string
	err error
}

func (s identityStub) Identify(*http.Request) (string, error) {
	return s.instance, s.err
}

type itemStub struct {
	resolveCalls int
	searchCalls int
	installation string
	repositoryID string
	repository string
	kind string
	query string
	item integration.GitHubItem
	items []integration.GitHubItem
	err error
}

func (p *itemStub) ResolveItem(_ context.Context, installation, id, repo, kind string, number int64) (integration.GitHubItem, error) {
	p.resolveCalls++
	p.installation, p.repositoryID, p.repository, p.kind = installation, id, repo, kind
	if p.err != nil {return integration.GitHubItem{},p.err}
	if p.item.URL != "" {return p.item,nil}
	segment := "issues"
	if kind == "pr" {segment = "pull"}
	return integration.GitHubItem{Kind: kind, Number: number, Title: "Verified work", State: "open",
		URL:"https://github.com/"+repo+"/"+segment+"/"+numberString(number)},nil
}

func numberString(n int64) string {
	return strconv.FormatInt(n,10)
}

func (p *itemStub) SearchItems(_ context.Context, installation,id,repo,kind,query string) ([]integration.GitHubItem,error) {
	p.searchCalls++
	p.installation,p.repositoryID,p.repository,p.kind,p.query=installation,id,repo,kind,query
	if p.err != nil {return nil,p.err}
	return p.items,nil
}

type fixedGrant struct {
	scope Grant
	err error
}

func (g fixedGrant) Lookup(_ context.Context, instance string, installationID, repositoryID int64) (Grant,error) {
	if g.err != nil{return Grant{},g.err}
	return g.scope,nil
}

func testBroker(t *testing.T,instance string, grant Grant,provider *itemStub) *Broker {
	t.Helper()
	b,err:=New(identityStub{instance:instance},fixedGrant{scope:grant},provider)
	if err!=nil{t.Fatal(err)}
	return b
}

func call(t *testing.T,handler http.Handler,method,path,body,contentType string) *httptest.ResponseRecorder {
	t.Helper()
	request:=httptest.NewRequest(method,path,strings.NewReader(body))
	if contentType!=""{request.Header.Set("Content-Type",contentType)}
	result:=httptest.NewRecorder()
	handler.ServeHTTP(result,request)
	return result
}

func activeGrant() Grant {
	return Grant{InstanceID:"instance-a",InstallationID:11,RepositoryID:22,
		Repository:"org/approved",ExpiresAt:time.Now().Add(time.Hour)}
}

const validResolve = `{"installationId":11,"repositoryId":22,"kind":"issue","number":357}`
const validSearch = `{"installationId":11,"repositoryId":22,"kind":"pr","query":"Security fix"}`

func TestBrokerScopesReadOnlyRequestsToAuthenticatedInstance(t *testing.T) {
	provider:=&itemStub{}
	broker:=testBroker(t,"instance-a",activeGrant(),provider)
	got:=call(t,broker,http.MethodPost,"/v1/github/items/resolve",validResolve,"application/json")
	if got.Code!=http.StatusOK{t.Fatalf("resolve %d %s",got.Code,got.Body.String())}
	var result struct{Item integration.GitHubItem `json:"item"`}
	if err:=json.Unmarshal(got.Body.Bytes(),&result);err!=nil{t.Fatal(err)}
	if result.Item.Title!="Verified work" || result.Item.Number!=357 ||
		provider.resolveCalls!=1 || provider.installation!="11" ||
		provider.repositoryID!="22" || provider.repository!="org/approved" {
		t.Fatalf("provider was not scoped: %+v %+v",provider,result)
	}
	if got.Header().Get("Cache-Control")!="no-store"{t.Fatal("sensitive response was cacheable")}
	provider.items=[]integration.GitHubItem{{Kind:"pr",Number:45,Title:"Fix auth",State:"closed",
		URL:"https://github.com/org/approved/pull/45",Merged:true}}
	search:=call(t,broker,http.MethodPost,"/v1/github/items/search",validSearch,"application/json")
	if search.Code!=http.StatusOK || provider.searchCalls!=1 || provider.query!="Security fix" ||
		!strings.Contains(search.Body.String(),`"merged":true`) {
		t.Fatalf("search failed %d %s",search.Code,search.Body.String())
	}
	if strings.Contains(search.Body.String(),"token")||strings.Contains(got.Body.String(),"token"){
		t.Fatal("broker leaked token")
	}
}

func TestBrokerRefusesCrossInstanceAndForgedClaimsBeforeProviderUse(t *testing.T) {
	cases:=[]struct{name string;instance string;scope Grant;body string;want int}{
		{"another-instance","instance-b",activeGrant(),validResolve,http.StatusForbidden},
		{"wrong-grant-installation","instance-a",func()Grant{g:=activeGrant();g.InstallationID=12;return g}(),validResolve,http.StatusForbidden},
		{"wrong-grant-repository","instance-a",func()Grant{g:=activeGrant();g.RepositoryID=23;return g}(),validResolve,http.StatusForbidden},
		{"expired","instance-a",func()Grant{g:=activeGrant();g.ExpiresAt=time.Now().Add(-time.Minute);return g}(),validResolve,http.StatusForbidden},
		{"unauthorized-repo","instance-a",activeGrant(),`{"installationId":11,"repositoryId":99,"kind":"issue","number":357}`,http.StatusForbidden},
		{"caller-supplied-repo-name","instance-a",activeGrant(),`{"installationId":11,"repositoryId":22,"repository":"other/private","kind":"issue","number":357}`,http.StatusBadRequest},
		{"unknown-provider-operation","instance-a",activeGrant(),`{"installationId":11,"repositoryId":22,"kind":"delete","number":357}`,http.StatusBadRequest},
		{"invalid-issue-number","instance-a",activeGrant(),`{"installationId":11,"repositoryId":22,"kind":"issue","number":0}`,http.StatusBadRequest},
		{"duplicate-json-objects","instance-a",activeGrant(),validResolve+validResolve,http.StatusBadRequest},
		{"malformed-trailing-json","instance-a",activeGrant(),validResolve+"{",http.StatusBadRequest},
		{"oversized","instance-a",activeGrant(),validResolve+strings.Repeat(" ",5000),http.StatusBadRequest},
	}
	for _,tc:=range cases {
		t.Run(tc.name,func(t *testing.T){
			provider:=&itemStub{}
			broker:=testBroker(t,tc.instance,tc.scope,provider)
			got:=call(t,broker,http.MethodPost,"/v1/github/items/resolve",tc.body,"application/json")
			if got.Code!=tc.want || provider.resolveCalls!=0 || provider.searchCalls!=0 {
				t.Fatalf("unsafe broker request %d calls=%d/%d",got.Code,provider.resolveCalls,provider.searchCalls)
			}
		})
	}
}

func TestBrokerRequiresAuthenticatedTransportAndValidMedia(t *testing.T) {
	provider:=&itemStub{}
	broker,err:=New(identityStub{err:ErrDenied},fixedGrant{scope:activeGrant()},provider)
	if err!=nil{t.Fatal(err)}
	if got:=call(t,broker,http.MethodPost,"/v1/github/items/resolve",validResolve,"application/json");
		got.Code!=http.StatusUnauthorized || provider.resolveCalls!=0{t.Fatalf("unauthenticated got %d",got.Code)}
	broker=testBroker(t,"instance-a",activeGrant(),provider)
	for _,tc:=range []struct{method,path,ctype string;want int}{
		{http.MethodGet,"/v1/github/items/resolve","application/json",http.StatusMethodNotAllowed},
		{http.MethodPost,"/v1/github/installations","application/json",http.StatusNotFound},
		{http.MethodPost,"/v1/github/items/resolve","text/plain",http.StatusUnsupportedMediaType},
	}{
		got:=call(t,broker,tc.method,tc.path,validResolve,tc.ctype)
		if got.Code!=tc.want{t.Fatalf("%s %s got %d",tc.method,tc.path,got.Code)}
	}
	if provider.resolveCalls!=0{t.Fatal("unauthorized path reached GitHub provider")}
}

func TestBrokerRejectsProviderErrorsAndInvalidResultURLs(t *testing.T) {
	provider:=&itemStub{err:errors.New("token-secret-should-not-leak")}
	broker:=testBroker(t,"instance-a",activeGrant(),provider)
	got:=call(t,broker,http.MethodPost,"/v1/github/items/resolve",validResolve,"application/json")
	if got.Code!=http.StatusBadGateway || strings.Contains(got.Body.String(),"token-secret") {
		t.Fatalf("provider error leaked %d %s",got.Code,got.Body.String())
	}
	provider.err=nil
	provider.item=integration.GitHubItem{Kind:"issue",Number:357,Title:"Private",State:"open",
		URL:"https://evil.example.net/org/approved/issues/357"}
	got=call(t,broker,http.MethodPost,"/v1/github/items/resolve",validResolve,"application/json")
	if got.Code!=http.StatusBadGateway{t.Fatalf("malicious provider URL got %d",got.Code)}
	provider.items=[]integration.GitHubItem{{Kind:"pr",Number:45,Title:"Unexpected",State:"open",
		URL:"https://github.com/another/private/pull/45"}}
	got=call(t,broker,http.MethodPost,"/v1/github/items/search",validSearch,"application/json")
	if got.Code!=http.StatusBadGateway{t.Fatalf("cross-repo result got %d",got.Code)}
}

func TestBrokerPerInstanceBudget(t *testing.T) {
	provider:=&itemStub{}
	broker:=testBroker(t,"instance-a",activeGrant(),provider)
	broker.rate.limit=2
	for i:=0;i<2;i++{
		if got:=call(t,broker,http.MethodPost,"/v1/github/items/resolve",validResolve,"application/json");got.Code!=http.StatusOK{
			t.Fatalf("first requests failed %d",got.Code)
		}
	}
	if got:=call(t,broker,http.MethodPost,"/v1/github/items/resolve",validResolve,"application/json");got.Code!=http.StatusTooManyRequests{
		t.Fatalf("rate limiter failed %d",got.Code)
	}
	if provider.resolveCalls!=2{t.Fatalf("provider received %d calls",provider.resolveCalls)}
}

func TestBrokerRequiresAllDependencies(t *testing.T) {
	provider:=&itemStub{}
	for _,test:=range []struct{auth Authenticator;grants Grants;provider Provider}{
		{nil,fixedGrant{scope:activeGrant()},provider},
		{identityStub{instance:"instance-a"},nil,provider},
		{identityStub{instance:"instance-a"},fixedGrant{scope:activeGrant()},nil},
	}{
		if _,err:=New(test.auth,test.grants,test.provider);!errors.Is(err,ErrUnavailable){
			t.Fatalf("broker accepted missing authorization dependency: %v",err)
		}
	}
}

func TestLegacyBrokerRejectsInjectedUserIdentity(t *testing.T) {
    provider:=&itemStub{}
    broker:=testBroker(t,"instance-a",activeGrant(),provider)
    result:=call(t,broker,http.MethodPost,"/v1/github/items/resolve",
        `{"userId":42,"installationId":11,"repositoryId":22,"kind":"issue","number":357}`,
        "application/json")
    if result.Code!=http.StatusBadRequest || provider.resolveCalls!=0{
        t.Fatalf("legacy mode accepted user identity override %d",result.Code)
    }
}
