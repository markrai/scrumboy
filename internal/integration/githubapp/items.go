package githubapp

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "net/url"
    "regexp"
    "strconv"
    "strings"
    "time"

    "scrumboy/internal/integration"
)

var githubRepositoryName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var _ integration.IssuePullRequestProvider = (*Client)(nil)
var _ integration.IssuePullRequestBrowser = (*Client)(nil)

func validItemRepository(id, name string) bool {
    n, err := strconv.ParseInt(id, 10, 64)
    return err == nil && n > 0 && len(name) <= 200 && githubRepositoryName.MatchString(name) && !strings.Contains(name, "..")
}

func (c *Client) scopedItemToken(ctx context.Context, installationID, repositoryID string) (string, error) {
    if !validItemRepository(repositoryID, "owner/repo") { return "", ErrIncomplete }
    id, err := strconv.ParseInt(installationID, 10, 64)
    if err != nil || id <= 0 { return "", ErrRevoked }
    repoID, _ := strconv.ParseInt(repositoryID, 10, 64)
    jwt, err := c.appJWT()
    if err != nil { return "", err }
    var response struct {
        Token string `json:"token"`
        ExpiresAt time.Time `json:"expires_at"`
    }
    err = c.request(ctx, http.MethodPost, "/app/installations/"+strconv.FormatInt(id, 10)+"/access_tokens",
        jwt, map[string]any{
            "permissions": map[string]string{"metadata":"read","issues":"read","pull_requests":"read"},
            "repository_ids": []int64{repoID},
        }, &response)
    if err != nil { return "", err }
    if len(response.Token) < 16 || !response.ExpiresAt.After(c.now().Add(time.Minute)) {
        return "", fmt.Errorf("%w: unusable installation token", ErrUnavailable)
    }
    return response.Token, nil
}

func validItemTitle(title string) bool {
    return len(title) > 0 && len(title) <= 500 && !strings.ContainsRune(title, 0)
}

func (c *Client) ResolveItem(ctx context.Context, installationID, repositoryID, repository, kind string, number int64) (integration.GitHubItem, error) {
    item := integration.GitHubItem{}
    if !validItemRepository(repositoryID, repository) || number <= 0 || number > 2147483647 ||
        (kind != "issue" && kind != "pr") { return item, ErrIncomplete }
    token, err := c.scopedItemToken(ctx, installationID, repositoryID)
    if err != nil { return item, err }
    endpoint := "issues"
    urlSegment := "issues"
    if kind == "pr" { endpoint, urlSegment = "pulls", "pull" }
    var data struct {
        Number int64 `json:"number"`
        Title string `json:"title"`
        State string `json:"state"`
        Draft bool `json:"draft"`
        MergedAt *time.Time `json:"merged_at"`
        HTMLURL string `json:"html_url"`
        PullRequest *json.RawMessage `json:"pull_request"`
    }
    err = c.request(ctx, http.MethodGet, "/repos/"+repository+"/"+endpoint+"/"+strconv.FormatInt(number,10), token,nil,&data)
    if err != nil { return item, err }
    canonical := "https://github.com/"+repository+"/"+urlSegment+"/"+strconv.FormatInt(number,10)
    if data.Number != number || !strings.EqualFold(data.HTMLURL, canonical) || !validItemTitle(data.Title) ||
        (data.State != "open" && data.State != "closed") || (kind=="issue" && data.PullRequest!=nil) {
        return item, ErrIncomplete
    }
    return integration.GitHubItem{Kind:kind,Number:number,Title:data.Title,State:data.State,Draft:data.Draft,Merged:data.MergedAt!=nil,URL:data.HTMLURL},nil
}

func (c *Client) SearchItems(ctx context.Context, installationID, repositoryID, repository, kind, query string) ([]integration.GitHubItem, error) {
    if len(strings.TrimSpace(query)) < 2 || len(query) > 100 { return nil, ErrIncomplete }
    return c.searchItems(ctx, installationID, repositoryID, repository, kind, "all", query)
}

func (c *Client) BrowseItems(ctx context.Context, installationID, repositoryID, repository, kind, state, query string) ([]integration.GitHubItem, error) {
    if state != "open" && state != "closed" && state != "all" { return nil, ErrIncomplete }
    if len(query) > 100 || (len(strings.TrimSpace(query)) > 0 && len(strings.TrimSpace(query)) < 2) { return nil, ErrIncomplete }
    return c.searchItems(ctx, installationID, repositoryID, repository, kind, state, query)
}

func (c *Client) searchItems(ctx context.Context, installationID, repositoryID, repository, kind, state, query string) ([]integration.GitHubItem, error) {
    if !validItemRepository(repositoryID, repository) || (kind != "issue" && kind != "pr") { return nil, ErrIncomplete }
    token, err := c.scopedItemToken(ctx, installationID, repositoryID)
    if err != nil { return nil, err }
    qualifier, segment := "is:issue", "issues"
    if kind == "pr" { qualifier, segment = "is:pr", "pull" }
    search := "repo:" + repository + " " + qualifier
    if state != "all" { search += " is:" + state }
    if strings.TrimSpace(query) != "" { search += " in:title " + strconv.Quote(strings.TrimSpace(query)) }
    var response struct {
        Items []struct {
            Number int64 `json:"number"`
            Title string `json:"title"`
            State string `json:"state"`
            HTMLURL string `json:"html_url"`
            PullRequest *json.RawMessage `json:"pull_request"`
        } `json:"items"`
    }
    endpoint := "/search/issues?per_page=20&sort=updated&order=desc&q=" + url.QueryEscape(search)
    if err := c.request(ctx, http.MethodGet, endpoint, token, nil, &response); err != nil { return nil, err }
    if len(response.Items) > 20 { return nil, ErrIncomplete }
    result := make([]integration.GitHubItem, 0, len(response.Items))
    for _, candidate := range response.Items {
        canonical := "https://github.com/" + repository + "/" + segment + "/" + strconv.FormatInt(candidate.Number, 10)
        if candidate.Number <= 0 || candidate.Number > 2147483647 ||
            !strings.EqualFold(candidate.HTMLURL, canonical) ||
            !validItemTitle(candidate.Title) || (candidate.State != "open" && candidate.State != "closed") ||
            (state != "all" && candidate.State != state) ||
            (kind == "issue" && candidate.PullRequest != nil) || (kind == "pr" && candidate.PullRequest == nil) {
            return nil, ErrIncomplete
        }
        result = append(result, integration.GitHubItem{Kind:kind, Number:candidate.Number, Title:candidate.Title, State:candidate.State, URL:candidate.HTMLURL})
    }
    return result, nil
}

func ParseIssuePullRequestURL(raw string) (string,string,int64,error) {
    parsed,err:=url.Parse(raw)
    if err!=nil || parsed.Scheme!="https" || !strings.EqualFold(parsed.Host,"github.com") ||
        parsed.User!=nil || parsed.RawQuery!="" || parsed.Fragment!="" {return "","",0,ErrIncomplete}
    parts:=strings.Split(strings.Trim(parsed.Path,"/"),"/")
    if len(parts)!=4{return "","",0,ErrIncomplete}
    repo:=parts[0]+"/"+parts[1]
    if !githubRepositoryName.MatchString(repo) || !validItemRepository("1",repo) {return "","",0,ErrIncomplete}
    kind:="issue"
    if parts[2]=="pull"{kind="pr"}else if parts[2]!="issues"{return "","",0,ErrIncomplete}
    number,err:=strconv.ParseInt(parts[3],10,64)
    if err!=nil || number<=0 || number>2147483647{return "","",0,ErrIncomplete}
    return repo,kind,number,nil
}
