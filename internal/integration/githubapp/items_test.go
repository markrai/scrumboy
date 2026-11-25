package githubapp

import (
    "context"
    "encoding/json"
    "errors"
    "net/http"
    "net/http/httptest"
    "net/url"
    "strconv"
    "strings"
    "testing"
    "time"
)

func TestItemURLValidation(t *testing.T) {
    cases:=[]struct{raw,repo,kind string; number int64;valid bool}{
        {"https://github.com/org/repo/issues/17","org/repo","issue",17,true},
        {"https://github.com/org/repo/pull/19","org/repo","pr",19,true},
        {"http://github.com/org/repo/issues/17","","",0,false},
        {"https://github.com.evil.invalid/org/repo/issues/17","","",0,false},
        {"https://github.com/org/repo/issues/0","","",0,false},
        {"https://github.com/org/repo/commit/12","","",0,false},
        {"https://github.com/org/repo/issues/17?foo=1","","",0,false},
        {"https://github.com/org/repo/issues/17#x","","",0,false},
        {"https://github.com/org/repo/issues/17/extra","","",0,false},
        {"https://github.com/org/repo/issues/-1","","",0,false},
    }
    for _,tc:=range cases {
        repo,kind,number,err:=ParseIssuePullRequestURL(tc.raw)
        if (err==nil)!=tc.valid || (tc.valid && (repo!=tc.repo || kind!=tc.kind || number!=tc.number)) {
            t.Errorf("%q got %q %q %d %v",tc.raw,repo,kind,number,err)
        }
    }
}

func TestGitHubItemsRestrictedReadOnlyTokenAndSearch(t *testing.T) {
    var tokenCalls,issueCalls,pullCalls,searchCalls int
    server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
        switch {
        case r.URL.Path=="/app/installations/987/access_tokens" && r.Method==http.MethodPost:
            tokenCalls++
            var value struct {
                RepositoryIDs []int64 `json:"repository_ids"`
                Permissions map[string]string `json:"permissions"`
            }
            if err:=json.NewDecoder(r.Body).Decode(&value);err!=nil{t.Error(err)}
            if len(value.RepositoryIDs)!=1 || value.RepositoryIDs[0]!=123 ||
                value.Permissions["metadata"]!="read" || value.Permissions["issues"]!="read" ||
                value.Permissions["pull_requests"]!="read" {
                t.Errorf("token not restricted %+v",value)
            }
            json.NewEncoder(w).Encode(map[string]any{"token":"restricted-installation-token-very-long","expires_at":time.Now().Add(time.Hour).Format(time.RFC3339)})
        case r.URL.Path=="/repos/org/repo/issues/17":
            issueCalls++
            if r.Header.Get("Authorization")!="Bearer restricted-installation-token-very-long"{t.Error("unexpected bearer")}
            json.NewEncoder(w).Encode(map[string]any{"number":17,"title":"Patch bug","state":"open","html_url":"https://github.com/org/repo/issues/17"})
        case r.URL.Path=="/repos/org/repo/pulls/19":
            pullCalls++
            json.NewEncoder(w).Encode(map[string]any{"number":19,"title":"Merge feature","state":"closed","merged_at":"2026-11-19T00:00:00Z","draft":false,"html_url":"https://github.com/org/repo/pull/19"})
        case r.URL.Path=="/search/issues":
            searchCalls++
            q:=r.URL.Query().Get("q")
            if !strings.Contains(q,"repo:org/repo") || !strings.Contains(q,"is:issue") || !strings.Contains(q,"Patch") {t.Errorf("unscoped query %q",q)}
            json.NewEncoder(w).Encode(map[string]any{"items":[]any{map[string]any{"number":17,"title":"Patch bug","state":"open","html_url":"https://github.com/org/repo/issues/17"}}})
        default: http.NotFound(w,r)
        }
    }))
    defer server.Close()
    c:=testClient(t,server.URL)
    issue,err:=c.ResolveItem(context.Background(),"987","123","org/repo","issue",17)
    if err!=nil || issue.Kind!="issue" || issue.Number!=17 || issue.Title!="Patch bug"{t.Fatalf("issue %+v %v",issue,err)}
    pr,err:=c.ResolveItem(context.Background(),"987","123","org/repo","pr",19)
    if err!=nil || !pr.Merged || pr.State!="closed"{t.Fatalf("pr %+v %v",pr,err)}
    found,err:=c.SearchItems(context.Background(),"987","123","org/repo","issue","Patch")
    if err!=nil || len(found)!=1 || found[0].Number!=17{t.Fatalf("search %+v %v",found,err)}
    if tokenCalls!=3 || issueCalls!=1 || pullCalls!=1 || searchCalls!=1{t.Fatalf("unexpected calls %d %d %d %d",tokenCalls,issueCalls,pullCalls,searchCalls)}
    for _,tc:=range []struct{id,name string}{{"0","org/repo"},{"123","evil/../repo"},{"123","evil/repo/extra"}} {
        _,err:=c.ResolveItem(context.Background(),"987",tc.id,tc.name,"issue",17)
        if !errors.Is(err,ErrIncomplete){t.Errorf("unsafe repo %+v accepted: %v",tc,err)}
    }
    if !validItemRepository(strconv.FormatInt(123,10),"org/repo"){t.Fatal("valid repository rejected")}
    if _,err:=url.Parse("https://github.com/org/repo/issues/17");err!=nil{t.Fatal(err)}
}

func TestGitHubItemRejectsWrongResourceAndWrongKind(t *testing.T) {
    server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
        switch {
        case strings.HasSuffix(r.URL.Path,"access_tokens"):
            json.NewEncoder(w).Encode(map[string]any{"token":"restricted-installation-token-very-long","expires_at":time.Now().Add(time.Hour).Format(time.RFC3339)})
        case r.URL.Path=="/repos/org/repo/issues/17":
            json.NewEncoder(w).Encode(map[string]any{"number":17,"title":"A PR, not issue","state":"open","html_url":"https://github.com/org/repo/issues/17","pull_request":map[string]any{"url":"https://api.github.com"}})
        default:http.NotFound(w,r)
        }
    }))
    defer server.Close()
    c:=testClient(t,server.URL)
    _,err:=c.ResolveItem(context.Background(),"987","123","org/repo","issue",17)
    if !errors.Is(err,ErrIncomplete){t.Fatalf("PR masqueraded as issue %v",err)}
}

func TestBrowseGitHubItemsRestrictsRepositoryAndState(t *testing.T) {
    var gotQueries []string
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        switch {
        case strings.HasSuffix(r.URL.Path, "/access_tokens"):
            json.NewEncoder(w).Encode(map[string]any{"token":"restricted-installation-token-very-long","expires_at":time.Now().Add(time.Hour).Format(time.RFC3339)})
        case r.URL.Path == "/search/issues":
            q := r.URL.Query().Get("q")
            gotQueries = append(gotQueries, q)
            kind := "issue"
            segment := "issues"
            pointer := any(nil)
            if strings.Contains(q, "is:pr") { kind, segment, pointer = "pr", "pull", map[string]any{"url":"https://api.github.com"} }
            state := "open"
            if strings.Contains(q, "is:closed") { state = "closed" }
            _ = kind
            json.NewEncoder(w).Encode(map[string]any{"items":[]any{map[string]any{
                "number":12,"title":"Tracked item","state":state,"html_url":"https://github.com/org/repo/"+segment+"/12","pull_request":pointer,
            }}})
        default:
            http.NotFound(w,r)
        }
    }))
    defer server.Close()
    c := testClient(t, server.URL)
    cases := []struct{kind,state,query string}{
        {"pr","open",""},{"issue","closed",""},{"issue","all","security"},
    }
    for _,tc := range cases {
        items,err:=c.BrowseItems(context.Background(),"987","123","org/repo",tc.kind,tc.state,tc.query)
        if err!=nil || len(items)!=1 || items[0].Kind!=tc.kind {t.Fatalf("browse %+v: %+v %v",tc,items,err)}
    }
    if len(gotQueries)!=3 || !strings.Contains(gotQueries[0],"repo:org/repo is:pr is:open") ||
       !strings.Contains(gotQueries[1],"repo:org/repo is:issue is:closed") ||
       !strings.Contains(gotQueries[2],"in:title") { t.Fatalf("browse queries %+v",gotQueries) }
    for _,tc:=range []struct{repo,kind,state,query string}{
        {"org/else","issue","open",""},{"org/repo","issue","unknown",""},
        {"org/repo","issue","open","x"},{"org/repo","issue","open",strings.Repeat("z",101)},
        {"org/repo","bad","open",""},
    } {
        if _,err:=c.BrowseItems(context.Background(),"987","123",tc.repo,tc.kind,tc.state,tc.query);err==nil {
            t.Errorf("unexpected successful browse for %+v",tc)
        }
    }
}
