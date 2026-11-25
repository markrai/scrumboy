package httpapi

import (
    "context"
    "net/http"
    "net/url"
    "strconv"
    "testing"

    "scrumboy/internal/integration"
    "scrumboy/internal/store"
)

type githubItemTestProvider struct {
    *githubFakeProvider
    resolves int
    searches int
    browses int
    browseKind string
    browseState string
    browseQuery string
}

func (p *githubItemTestProvider) ResolveItem(ctx context.Context,installationID,repositoryID,repository,kind string,number int64) (integration.GitHubItem,error) {
    p.resolves++
    if installationID!="987" || repositoryID!="123" || repository!="org/repo" || kind!="issue" || number!=357 {
        return integration.GitHubItem{},store.ErrNotFound
    }
    return integration.GitHubItem{Kind:kind,Number:number,Title:"Fix card controls",State:"open",URL:"https://github.com/org/repo/issues/357"},nil
}

func (p *githubItemTestProvider) SearchItems(ctx context.Context,installationID,repositoryID,repository,kind,query string) ([]integration.GitHubItem,error) {
    p.searches++
    if installationID!="987" || repositoryID!="123" || repository!="org/repo" || kind!="issue" || query!="controls" {
        return nil,store.ErrNotFound
    }
    return []integration.GitHubItem{{Kind:"issue",Number:357,Title:"Fix card controls",State:"open",URL:"https://github.com/org/repo/issues/357"}},nil
}

func (p *githubItemTestProvider) BrowseItems(ctx context.Context, installationID, repositoryID, repository, kind, state, query string) ([]integration.GitHubItem, error) {
    p.browses++
    p.browseKind, p.browseState, p.browseQuery = kind, state, query
    if installationID != "987" || repositoryID != "123" || repository != "org/repo" {
        return nil, store.ErrNotFound
    }
    return []integration.GitHubItem{{Kind:"issue",Number:357,Title:"Fix card controls",State:"open",URL:"https://github.com/org/repo/issues/357"}},nil
}

func TestGitHubItemsRESTAuthorizationAndProjectScope(t *testing.T) {
    provider:=&githubItemTestProvider{githubFakeProvider:&githubFakeProvider{
        installs:[]integration.Installation{{ID:"987",Account:"org",Kind:"Organization"}},
        resources:[]integration.Resource{{ID:"123",Name:"org/repo",URL:"https://github.com/org/repo"}},
    }}
    ts,db,cleanup:=newTestHTTPServerWithOptions(t,Options{ScrumboyMode:"full",GitHubProvider:provider})
    defer cleanup()
    ownerClient:=newCookieClient(t)
    owner:=bootstrapUserClient(t,ownerClient,ts.URL,"Owner","github-items-owner@example.com","password123")
    ownerID:=int64(owner["id"].(float64))
    st:=store.New(db,nil)
    ctx:=context.Background()
    project,err:=st.CreateProject(store.WithUserID(ctx,ownerID),"GitHub Item Boundaries")
    if err!=nil{t.Fatal(err)}
    other,err:=st.CreateProject(store.WithUserID(ctx,ownerID),"Unassociated Project")
    if err!=nil{t.Fatal(err)}
    card,err:=st.CreateTodo(store.WithUserID(ctx,ownerID),project.ID,store.CreateTodoInput{Title:"Original card"},store.ModeFull)
    if err!=nil{t.Fatal(err)}
    otherCard,err:=st.CreateTodo(store.WithUserID(ctx,ownerID),other.ID,store.CreateTodoInput{Title:"Other"},store.ModeFull)
    if err!=nil{t.Fatal(err)}
    viewer,err:=st.CreateUser(ctx,"github-item-viewer@example.com","password123","Viewer")
    if err!=nil{t.Fatal(err)}
    if err:=st.AddProjectMember(store.WithUserID(ctx,ownerID),ownerID,project.ID,viewer.ID,store.RoleViewer);err!=nil{t.Fatal(err)}
    viewerClient:=newCookieClient(t)
    loginUserClient(t,viewerClient,ts.URL,"github-item-viewer@example.com","password123")
    outsider,err:=st.CreateUser(ctx,"github-item-outsider@example.com","password123","Outsider")
    if err!=nil{t.Fatal(err)}
    if outsider.ID==viewer.ID{t.Fatal("duplicate user")}
    outsiderClient:=newCookieClient(t)
    loginUserClient(t,outsiderClient,ts.URL,"github-item-outsider@example.com","password123")

    admin:=ts.URL+"/api/admin/integrations/github/installations/987/sync"
    var synced struct { ConnectionID int64 `json:"connectionId"` }
    resp,body:=doJSON(t,ownerClient,http.MethodPost,admin,nil,&synced)
    if resp.StatusCode!=http.StatusOK || synced.ConnectionID<=0{t.Fatalf("sync %d %s",resp.StatusCode,body)}
    resources,err:=st.ListIntegrationResources(ctx,ownerID,synced.ConnectionID)
    if err!=nil || len(resources)!=1{t.Fatalf("resources %+v %v",resources,err)}
    resourceID:=resources[0].ID
    attached:=ts.URL+"/api/board/"+project.Slug+"/integrations/resources"
    resp,body=doJSON(t,ownerClient,http.MethodPost,attached,map[string]any{"resourceId":resourceID},nil)
    if resp.StatusCode!=http.StatusNoContent{t.Fatalf("attach %d %s",resp.StatusCode,body)}
    cardAPI:=ts.URL+"/api/board/"+project.Slug+"/todos/"+strconv.FormatInt(card.LocalID,10)+"/github-items"
    itemURL:="https://github.com/org/repo/issues/357"

    resp,body=doJSON(t,viewerClient,http.MethodPost,cardAPI,map[string]any{"url":itemURL},nil)
    if resp.StatusCode!=http.StatusForbidden || provider.resolves!=0{t.Fatalf("viewer provider access %d %s %d",resp.StatusCode,body,provider.resolves)}
    outsiderAPI:=ts.URL+"/api/board/"+other.Slug+"/todos/"+strconv.FormatInt(otherCard.LocalID,10)+"/github-items"
    resp,body=doJSON(t,ownerClient,http.MethodPost,outsiderAPI,map[string]any{"url":itemURL},nil)
    if resp.StatusCode!=http.StatusNotFound || provider.resolves!=0{t.Fatalf("unassociated repository access %d %s %d",resp.StatusCode,body,provider.resolves)}
    resp,body=doJSON(t,ownerClient,http.MethodPost,cardAPI,map[string]any{"url":"https://github.com/other/private/issues/357"},nil)
    if resp.StatusCode!=http.StatusNotFound || provider.resolves!=0{t.Fatalf("arbitrary repository access %d %s %d",resp.StatusCode,body,provider.resolves)}
    resp,body=doJSON(t,ownerClient,http.MethodPost,cardAPI,map[string]any{"url":"https://evil.example.org/org/repo/issues/357"},nil)
    if resp.StatusCode!=http.StatusBadRequest || provider.resolves!=0{t.Fatalf("unsafe URL %d %s %d",resp.StatusCode,body,provider.resolves)}
    resp,body=doJSON(t,ownerClient,http.MethodPost,cardAPI,map[string]any{"url":itemURL},nil)
    if resp.StatusCode!=http.StatusNoContent || provider.resolves!=1{t.Fatalf("link %d %s %d",resp.StatusCode,body,provider.resolves)}

    var found []integration.GitHubItem
    resp,body=doJSON(t,viewerClient,http.MethodGet,cardAPI,nil,&found)
    if resp.StatusCode!=http.StatusOK || len(found)!=1 || found[0].ResourceID!=resourceID {t.Fatalf("viewer list %d %s %+v",resp.StatusCode,body,found)}
    resp,body=doJSON(t,outsiderClient,http.MethodGet,cardAPI,nil,nil)
    if resp.StatusCode==http.StatusOK{t.Fatalf("outsider read references: %s",body)}
    search:=ts.URL+"/api/board/"+project.Slug+"/integrations/github/search?resourceId="+strconv.FormatInt(resourceID,10)+"&kind=issue&q="+url.QueryEscape("controls")
    var searchResult struct { Items []integration.GitHubItem `json:"items"` }
    resp,body=doJSON(t,viewerClient,http.MethodGet,search,nil,&searchResult)
    if resp.StatusCode!=http.StatusOK || provider.searches!=1 || len(searchResult.Items)!=1{t.Fatalf("search %d %s %+v",resp.StatusCode,body,searchResult)}
    resp,body=doJSON(t,outsiderClient,http.MethodGet,search,nil,nil)
    if resp.StatusCode==http.StatusOK || provider.searches!=1{t.Fatalf("outsider search %d %s",resp.StatusCode,body)}
    badSearch:=ts.URL+"/api/board/"+other.Slug+"/integrations/github/search?resourceId="+strconv.FormatInt(resourceID,10)+"&kind=issue&q=controls"
    resp,body=doJSON(t,ownerClient,http.MethodGet,badSearch,nil,nil)
    if resp.StatusCode!=http.StatusNotFound || provider.searches!=1{t.Fatalf("cross-project search %d %s",resp.StatusCode,body)}

    browseBase := ts.URL+"/api/board/"+project.Slug+"/integrations/github/browse?resourceId="+strconv.FormatInt(resourceID,10)+"&kind=issue&state=open"
    var browseResult struct { Items []integration.GitHubItem `json:"items"` }
    resp,body=doJSON(t,viewerClient,http.MethodGet,browseBase,nil,&browseResult)
    if resp.StatusCode!=http.StatusOK || provider.browses!=1 || len(browseResult.Items)!=1 || browseResult.Items[0].ResourceID!=resourceID || provider.browseState!="open" || provider.browseQuery!="" {t.Fatalf("browse %d %s %+v",resp.StatusCode,body,browseResult)}
    for _,bad:=range []string{
        ts.URL+"/api/board/"+project.Slug+"/integrations/github/browse?resourceId="+strconv.FormatInt(resourceID,10)+"&kind=issue&state=invalid",
        browseBase+"&q=x",
    } {
        resp,body=doJSON(t,viewerClient,http.MethodGet,bad,nil,nil)
        if resp.StatusCode!=http.StatusBadRequest || provider.browses!=1 {t.Fatalf("invalid browse reached provider %d %s",resp.StatusCode,body)}
    }
    resp,body=doJSON(t,outsiderClient,http.MethodGet,browseBase,nil,nil)
    if resp.StatusCode==http.StatusOK || provider.browses!=1 {t.Fatalf("outsider browse %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,ownerClient,http.MethodGet,ts.URL+"/api/board/"+other.Slug+"/integrations/github/browse?resourceId="+strconv.FormatInt(resourceID,10)+"&kind=issue&state=open",nil,nil)
    if resp.StatusCode!=http.StatusNotFound || provider.browses!=1 {t.Fatalf("cross-project browse %d %s",resp.StatusCode,body)}
    itemPath:=cardAPI+"/"+strconv.FormatInt(resourceID,10)+"/issue/357"
    resp,body=doJSON(t,viewerClient,http.MethodDelete,itemPath,nil,nil)
    if resp.StatusCode!=http.StatusForbidden{t.Fatalf("viewer unlink %d %s",resp.StatusCode,body)}
    resp,body=doJSON(t,ownerClient,http.MethodPost,itemPath+"/refresh",nil,nil)
    if resp.StatusCode!=http.StatusNoContent || provider.resolves!=2{t.Fatalf("refresh %d %s %d",resp.StatusCode,body,provider.resolves)}
    resp,body=doJSON(t,ownerClient,http.MethodDelete,itemPath,nil,nil)
    if resp.StatusCode!=http.StatusNoContent{t.Fatalf("unlink %d %s",resp.StatusCode,body)}
}
