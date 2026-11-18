package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"scrumboy/internal/integration"
	"scrumboy/internal/integration/githubapp"
	"scrumboy/internal/store"
)

type githubConnectionJSON struct {
	ID int64 `json:"id"`
	InstallationID string `json:"installationId"`
	Account string `json:"account"`
	Status string `json:"status"`
}

func githubConnectionsJSON(connections []store.IntegrationConnection) []githubConnectionJSON {
	out:=make([]githubConnectionJSON,0)
	for _,conn:=range connections {
		if conn.Provider=="github"{
			out=append(out,githubConnectionJSON{ID:conn.ID,InstallationID:conn.ExternalAccountID,Account:conn.Name,Status:conn.Status})
		}
	}
	return out
}

func (s *Server) githubProviderError(w http.ResponseWriter,err error) {
	switch {
	case errors.Is(err,githubapp.ErrRevoked):
		writeError(w,http.StatusConflict,"INTEGRATION_REVOKED","GitHub App installation is not accessible; check its permissions or reinstall",nil)
	case errors.Is(err,githubapp.ErrIncomplete):
		writeError(w,http.StatusBadGateway,"INTEGRATION_INCOMPLETE","GitHub repository discovery was incomplete; no changes applied",nil)
	default:
		writeError(w,http.StatusBadGateway,"INTEGRATION_UNAVAILABLE","GitHub App API unavailable",nil)
	}
}

func (s *Server) handleAdminIntegrations(w http.ResponseWriter,r *http.Request,actorID int64,rest []string) {
	if len(rest)==0 || rest[0]!="github" {
		writeError(w,http.StatusNotFound,"NOT_FOUND","not found",nil)
		return
	}
	rest=rest[1:]
	if len(rest)==0 && r.Method==http.MethodGet {
		installed,err:=s.store.ListIntegrationConnections(s.requestContext(r),actorID)
		if err!=nil {writeStoreErr(w,err,false);return}
		installURL:=""
		if s.githubProvider!=nil && s.githubAppSlug!="" {
			slug:=s.githubAppSlug
			if len(slug)<=100 && !strings.ContainsAny(slug,"/\\?#%&@ ") {
				installURL="https://github.com/apps/"+slug+"/installations/new"
			}
		}
		writeJSON(w,http.StatusOK,map[string]any{
			"configured":s.githubProvider!=nil && s.mode=="full",
			"installUrl":installURL,
			"connections":githubConnectionsJSON(installed),
		})
		return
	}
	if s.githubProvider==nil || s.mode!="full" {
		writeError(w,http.StatusServiceUnavailable,"CAPABILITY_UNAVAILABLE","GitHub App is not configured",nil)
		return
	}
	ctx:=s.requestContext(r)
	if len(rest)==1 && rest[0]=="installations" && r.Method==http.MethodGet {
		items,err:=s.githubProvider.Installations(ctx)
		if err!=nil{s.githubProviderError(w,err);return}
		writeJSON(w,http.StatusOK,items)
		return
	}
	if len(rest)==3 && rest[0]=="installations" && rest[2]=="sync" && r.Method==http.MethodPost {
		id,err:=strconv.ParseInt(rest[1],10,64)
		if err!=nil || id<=0 {
			writeValidationError(w,"invalid installationId","invalid_installation_id",map[string]any{"field":"installationId"})
			return
		}
		installations,err:=s.githubProvider.Installations(ctx)
		if err!=nil{s.githubProviderError(w,err);return}
		var installation integration.Installation
		for _,item:=range installations {
			if item.ID==strconv.FormatInt(id,10) {installation=item;break}
		}
		if installation.ID=="" {
			writeError(w,http.StatusNotFound,"NOT_FOUND","GitHub installation is not available to this App",nil)
			return
		}
		repos,err:=s.githubProvider.Resources(ctx,strconv.FormatInt(id,10))
		if err!=nil{s.githubProviderError(w,err);return}
		conn,err:=s.store.SyncGitHubInstallation(ctx,actorID,installation,repos)
		if err!=nil{writeStoreErr(w,err,false);return}
		writeJSON(w,http.StatusOK,map[string]any{"connectionId":conn.ID,"repositoryCount":len(repos),"account":conn.Name})
		return
	}
	if len(rest)>=2 && rest[0]=="connections" {
		id,err:=strconv.ParseInt(rest[1],10,64)
		if err!=nil || id<=0 {
			writeValidationError(w,"invalid connectionId","invalid_connection_id",map[string]any{"field":"connectionId"})
			return
		}
		conns,err:=s.store.ListIntegrationConnections(ctx,actorID)
		if err!=nil{writeStoreErr(w,err,false);return}
		var conn *store.IntegrationConnection
		for i:=range conns {
			if conns[i].ID==id && conns[i].Provider=="github" {
				conn=&conns[i];break
			}
		}
		if conn==nil {writeError(w,http.StatusNotFound,"NOT_FOUND","not found",nil);return}
		if len(rest)==3 && rest[2]=="resources" && r.Method==http.MethodGet {
			resources,err:=s.store.ListIntegrationResources(ctx,actorID,id)
			if err!=nil{writeStoreErr(w,err,false);return}
			writeJSON(w,http.StatusOK,integrationResourcesJSON(resources))
			return
		}
		if len(rest)==2 && r.Method==http.MethodDelete {
			if err:=s.store.DeleteGitHubInstallation(ctx,actorID,id);err!=nil{writeStoreErr(w,err,false);return}
			installationID,_:=strconv.ParseInt(conn.ExternalAccountID,10,64)
			s.githubProvider.Invalidate(strconv.FormatInt(installationID,10))
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeError(w,http.StatusNotFound,"NOT_FOUND","not found",nil)
}
