package httpapi

import (
    "net/http"
    "strconv"
    "strings"

    "scrumboy/internal/integration"
    "scrumboy/internal/integration/githubapp"
    "scrumboy/internal/store"
)

func (s *Server) githubItemProvider(w http.ResponseWriter) (integration.IssuePullRequestProvider,bool) {
    active, _, _ := s.githubSnapshot()
    if s.mode!="full" || active==nil {
        writeError(w,http.StatusServiceUnavailable,"CAPABILITY_UNAVAILABLE","GitHub App is not configured",nil)
        return nil,false
    }
    p,ok:=active.(integration.IssuePullRequestProvider)
    if !ok {
        writeError(w,http.StatusServiceUnavailable,"CAPABILITY_UNAVAILABLE","GitHub issue and PR search is unavailable",nil)
        return nil,false
    }
    return p,true
}

func (s *Server) handleProjectGitHubSearch(w http.ResponseWriter,r *http.Request,rest []string,pc *store.ProjectContext) {
    if len(rest)!=4 || r.Method!=http.MethodGet {
        writeError(w,http.StatusMethodNotAllowed,"METHOD_NOT_ALLOWED","method not allowed",nil)
        return
    }
    actor,ok:=store.UserIDFromContext(s.requestContext(r))
    if !ok {writeStoreErr(w,store.ErrUnauthorized,true);return}
    raw:=r.URL.Query()
    resourceID,err:=strconv.ParseInt(raw.Get("resourceId"),10,64)
    kind:=raw.Get("kind")
    query:=strings.TrimSpace(raw.Get("q"))
    if err!=nil || resourceID<=0 || (kind!="issue" && kind!="pr") || len(query)<2 || len(query)>100 {
        writeValidationError(w,"invalid GitHub search input","invalid_github_search",nil)
        return
    }
    repo,err:=s.store.GetGitHubProjectRepository(s.requestContext(r),actor,pc.Project.ID,resourceID,false)
    if err!=nil{writeStoreErr(w,err,true);return}
    provider,ok:=s.githubItemProvider(w)
    if !ok{return}
    found,err:=provider.SearchItems(s.requestContext(r),repo.InstallationID,repo.ExternalID,repo.Name,kind,query)
    if err!=nil{s.githubProviderError(w,err);return}
    for i:=range found {
        found[i].ResourceID=resourceID
        found[i].Repository=repo.Name
        found[i].Availability="available"
    }
    writeJSON(w,http.StatusOK,map[string]any{"items":found})
}

func (s *Server) handleProjectGitHubBrowse(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) {
    if len(rest) != 4 || r.Method != http.MethodGet {
        writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
        return
    }
    actor, ok := store.UserIDFromContext(s.requestContext(r))
    if !ok { writeStoreErr(w, store.ErrUnauthorized, true); return }
    raw := r.URL.Query()
    resourceID, err := strconv.ParseInt(raw.Get("resourceId"), 10, 64)
    kind, state, query := raw.Get("kind"), raw.Get("state"), strings.TrimSpace(raw.Get("q"))
    if err != nil || resourceID <= 0 || (kind != "issue" && kind != "pr") ||
        (state != "open" && state != "closed" && state != "all") ||
        len(query) > 100 || (len(query) > 0 && len(query) < 2) {
        writeValidationError(w, "invalid GitHub browse input", "invalid_github_browse", nil)
        return
    }
    repo, err := s.store.GetGitHubProjectRepository(s.requestContext(r), actor, pc.Project.ID, resourceID, false)
    if err != nil { writeStoreErr(w, err, true); return }
    active, _, _ := s.githubSnapshot()
    if s.mode != "full" || active == nil {
        writeError(w, http.StatusServiceUnavailable, "CAPABILITY_UNAVAILABLE", "GitHub App is not configured", nil)
        return
    }
    provider, ok := active.(integration.IssuePullRequestBrowser)
    if !ok {
        writeError(w, http.StatusServiceUnavailable, "CAPABILITY_UNAVAILABLE", "GitHub browsing is unavailable", nil)
        return
    }
    found, err := provider.BrowseItems(s.requestContext(r), repo.InstallationID, repo.ExternalID, repo.Name, kind, state, query)
    if err != nil { s.githubProviderError(w, err); return }
    for i := range found {
        found[i].ResourceID = resourceID
        found[i].Repository = repo.Name
        found[i].Availability = "available"
    }
    writeJSON(w, http.StatusOK, map[string]any{"items":found})
}

func (s *Server) handleTodoGitHubItems(w http.ResponseWriter,r *http.Request,rest []string,pc *store.ProjectContext) {
    ctx:=s.requestContext(r)
    actor,ok:=store.UserIDFromContext(ctx)
    if !ok{writeStoreErr(w,store.ErrUnauthorized,true);return}
    localID,valid:=parseInt64(rest[2])
    if !valid || localID<=0 {
        writeValidationError(w,"invalid todo local ID","invalid_todo_local_id",nil)
        return
    }
    switch {
    case len(rest)==4 && r.Method==http.MethodGet:
        items,err:=s.store.ListTodoGitHubItems(ctx,actor,pc.Project.ID,localID)
        if err!=nil{writeStoreErr(w,err,true);return}
        writeJSON(w,http.StatusOK,items)
    case len(rest)==4 && r.Method==http.MethodPost:
        var in struct {URL string `json:"url"`}
        if err:=readJSON(w,r,s.maxBody,&in);err!=nil{return}
        if len(in.URL)>2048 {writeValidationError(w,"invalid GitHub URL","invalid_github_url",nil);return}
        repository,kind,number,err:=githubapp.ParseIssuePullRequestURL(in.URL)
        if err!=nil{writeValidationError(w,"enter a canonical GitHub issue or PR URL","invalid_github_url",nil);return}
        resources,err:=s.store.ListProjectIntegrationResources(ctx,actor,pc.Project.ID)
        if err!=nil{writeStoreErr(w,err,true);return}
        var resourceID int64
        for _,res:=range resources {
            if res.Kind=="repository" && res.Availability=="available" && strings.EqualFold(res.Name,repository) {
                resourceID=res.ID
                break
            }
        }
        if resourceID==0{writeError(w,http.StatusNotFound,"NOT_FOUND","repository is not linked to this project",nil);return}
        repo,err:=s.store.GetGitHubProjectRepository(ctx,actor,pc.Project.ID,resourceID,true)
        if err!=nil{writeStoreErr(w,err,true);return}
        provider,ok:=s.githubItemProvider(w)
        if !ok{return}
        item,err:=provider.ResolveItem(ctx,repo.InstallationID,repo.ExternalID,repo.Name,kind,number)
        if err!=nil{s.githubProviderError(w,err);return}
        item.ResourceID=resourceID
        if err:=s.store.LinkTodoGitHubItem(ctx,actor,pc.Project.ID,localID,item);err!=nil{writeStoreErr(w,err,true);return}
        w.WriteHeader(http.StatusNoContent)
    case (len(rest)==7 && r.Method==http.MethodDelete) || (len(rest)==8 && rest[7]=="refresh" && r.Method==http.MethodPost):
        resourceID,valid:=parseInt64(rest[4])
        number,validNumber:=parseInt64(rest[6])
        kind:=rest[5]
        if !valid || !validNumber || resourceID<=0 || number<=0 || (kind!="issue" && kind!="pr"){
            writeValidationError(w,"invalid GitHub reference","invalid_github_reference",nil)
            return
        }
        if r.Method==http.MethodDelete {
            if err:=s.store.UnlinkTodoGitHubItem(ctx,actor,pc.Project.ID,localID,resourceID,kind,number);err!=nil{
                writeStoreErr(w,err,true);return
            }
            w.WriteHeader(http.StatusNoContent)
            return
        }
        linked,err:=s.store.ListTodoGitHubItems(ctx,actor,pc.Project.ID,localID)
        if err!=nil{writeStoreErr(w,err,true);return}
        found:=false
        for _,item:=range linked {
            if item.ResourceID==resourceID && item.Number==number && item.Kind==kind {found=true;break}
        }
        if !found{writeStoreErr(w,store.ErrNotFound,true);return}
        repo,err:=s.store.GetGitHubProjectRepository(ctx,actor,pc.Project.ID,resourceID,true)
        if err!=nil{writeStoreErr(w,err,true);return}
        provider,ok:=s.githubItemProvider(w)
        if !ok{return}
        item,err:=provider.ResolveItem(ctx,repo.InstallationID,repo.ExternalID,repo.Name,kind,number)
        if err!=nil{s.githubProviderError(w,err);return}
        item.ResourceID=resourceID
        if err:=s.store.RefreshTodoGitHubItem(ctx,actor,pc.Project.ID,localID,item);err!=nil{
            writeStoreErr(w,err,true);return
        }
        w.WriteHeader(http.StatusNoContent)
    default:
        writeError(w,http.StatusMethodNotAllowed,"METHOD_NOT_ALLOWED","method not allowed",nil)
    }
}
