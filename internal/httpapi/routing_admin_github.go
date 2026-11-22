package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"context"
	"time"
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
	provider, appSlug, webhook := s.githubSnapshot()
	if len(rest)==2 && rest[0]=="manifest" && rest[1]=="start" {
		s.handleGitHubManifestStart(w,r)
		return
	}
	if len(rest)==1 && rest[0]=="webhooks" && r.Method==http.MethodGet {
		if webhook==nil || s.mode!="full" {
			writeJSON(w,http.StatusOK,map[string]any{"configured":false})
			return
		}
		counts,err:=webhook.Summary(s.requestContext(r))
		if err!=nil {writeInternal(w,err);return}
		dead,err:=webhook.Dead(s.requestContext(r),actorID)
		if err!=nil {writeStoreErr(w,err,false);return}
		automation,err:=webhook.AutomationSummary(s.requestContext(r))
        if err!=nil && !errors.Is(err,store.ErrNotFound){writeInternal(w,err);return}
        writeJSON(w,http.StatusOK,map[string]any{
            "configured":true,"counts":counts,"deadDeliveries":dead,
            "automation":automation,
        })
		return
	}
	if len(rest)==3 && rest[0]=="webhooks" && rest[2]=="retry" && r.Method==http.MethodPost {
		if webhook==nil || s.mode!="full" {
			writeError(w,http.StatusServiceUnavailable,"CAPABILITY_UNAVAILABLE","GitHub webhooks are not configured",nil)
			return
		}
		id,err:=strconv.ParseInt(rest[1],10,64)
		if err!=nil || id<=0 {
			writeValidationError(w,"invalid delivery id","invalid_delivery_id",map[string]any{"field":"deliveryId"})
			return
		}
		if err:=webhook.Retry(s.requestContext(r),actorID,id);err!=nil {
			writeStoreErr(w,err,false)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(rest)==0 && r.Method==http.MethodGet {
		installed,err:=s.store.ListIntegrationConnections(s.requestContext(r),actorID)
		if err!=nil {writeStoreErr(w,err,false);return}
		installURL:=""
		if provider!=nil && appSlug!="" {
			if len(appSlug)<=100 && !strings.ContainsAny(appSlug,"/\\?#%&@ ") {
				installURL="https://github.com/apps/"+appSlug+"/installations/new"
			}
		}
		writeJSON(w,http.StatusOK,map[string]any{
			"configured":provider!=nil && s.mode=="full",
			"manifestEnrollmentAvailable":s.githubManifest!=nil && s.githubManifest.Available() && provider==nil && s.mode=="full",
			"installUrl":installURL,
			"connections":githubConnectionsJSON(installed),
			"readiness":s.githubReadiness(true, githubActiveConnectionCount(installed)),
		})
		return
	}
	if provider==nil || s.mode!="full" {
		writeError(w,http.StatusServiceUnavailable,"CAPABILITY_UNAVAILABLE","GitHub App is not configured",nil)
		return
	}
	ctx:=s.requestContext(r)
	if len(rest)==1 && rest[0]=="installations" && r.Method==http.MethodGet {
		items,err:=provider.Installations(ctx)
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
		installations,err:=provider.Installations(ctx)
		if err!=nil{s.githubProviderError(w,err);return}
		var installation integration.Installation
		for _,item:=range installations {
			if item.ID==strconv.FormatInt(id,10) {installation=item;break}
		}
		if installation.ID=="" {
			writeError(w,http.StatusNotFound,"NOT_FOUND","GitHub installation is not available to this App",nil)
			return
		}
		repos,err:=provider.Resources(ctx,strconv.FormatInt(id,10))
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
		if len(rest)==3 && rest[2]=="impact" && r.Method==http.MethodGet {
			impact,err:=s.store.GetGitHubDisconnectImpact(ctx,actorID,id)
			if err!=nil{writeStoreErr(w,err,false);return}
			writeJSON(w,http.StatusOK,impact)
			return
		}
		if len(rest)==3 && rest[2]=="resources" && r.Method==http.MethodGet {
			resources,err:=s.store.ListIntegrationResources(ctx,actorID,id)
			if err!=nil{writeStoreErr(w,err,false);return}
			writeJSON(w,http.StatusOK,integrationResourcesJSON(resources))
			return
		}
		if len(rest)==2 && r.Method==http.MethodDelete {
			var confirmation struct {
				ConfirmConnectionID int64 `json:"confirmConnectionId"`
				Impact store.GitHubDisconnectImpact `json:"impact"`
			}
			if err:=readJSON(w,r,s.maxBody,&confirmation);err!=nil{return}
			if confirmation.ConfirmConnectionID!=id {
				writeValidationError(w,"confirmConnectionId does not match connection","invalid_confirmation",nil)
				return
			}
			if err:=s.store.DeleteGitHubInstallation(ctx,actorID,id,confirmation.Impact);err!=nil{writeStoreErr(w,err,false);return}
			installationID,_:=strconv.ParseInt(conn.ExternalAccountID,10,64)
			revokeCtx,cancel:=context.WithTimeout(ctx,3*time.Second)
			revokeErr:=provider.Revoke(revokeCtx,strconv.FormatInt(installationID,10))
			cancel()
			if revokeErr!=nil {
				s.logger.Printf("warning: could not revoke cached GitHub installation token after local disconnect")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeError(w,http.StatusNotFound,"NOT_FOUND","not found",nil)
}
