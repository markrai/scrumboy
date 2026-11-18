package httpapi

import (
	"net/http"

	"scrumboy/internal/store"
)

type integrationResourceResponse struct {
	ID           int64  `json:"id"`
	ConnectionID int64  `json:"connectionId"`
	Kind         string `json:"kind"`
	ExternalID   string `json:"externalId"`
	Name         string `json:"name"`
	URL          string `json:"url"`
}

func integrationResourcesJSON(resources []store.IntegrationResource) []integrationResourceResponse {
	out := make([]integrationResourceResponse, 0, len(resources))
	for _, resource := range resources {
		out = append(out, integrationResourceResponse{
			ID: resource.ID, ConnectionID: resource.ConnectionID, Kind: resource.Kind,
			ExternalID: resource.ExternalID, Name: resource.Name, URL: resource.URL,
		})
	}
	return out
}

func (s *Server) handleBoardIntegrationRoutes(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	if len(rest) < 3 {
		return false
	}
	ctx := s.requestContext(r)
	actorID, ok := store.UserIDFromContext(ctx)
	if rest[1] == "integrations" && rest[2] == "resources" && (len(rest)==3 || len(rest)==4) {
		if !ok {
			writeStoreErr(w,store.ErrUnauthorized,true)
			return true
		}
		switch {
		case len(rest)==3 && r.Method==http.MethodGet:
			resources,err:=s.store.ListProjectIntegrationResources(ctx,actorID,pc.Project.ID)
			if err!=nil{writeStoreErr(w,err,true);return true}
			writeJSON(w,http.StatusOK,integrationResourcesJSON(resources))
		case len(rest)==3 && r.Method==http.MethodPost:
			var in struct {ResourceID int64 `json:"resourceId"`}
			if err:=readJSON(w,r,s.maxBody,&in);err!=nil{return true}
			if err:=s.store.AttachIntegrationResource(ctx,actorID,pc.Project.ID,in.ResourceID);err!=nil{writeStoreErr(w,err,true);return true}
			w.WriteHeader(http.StatusNoContent)
		case len(rest)==4 && r.Method==http.MethodDelete:
			id,valid:=parseInt64(rest[3])
			if !valid || id<=0 {
				writeValidationError(w,"invalid resourceId","invalid_resource_id",map[string]any{"field":"resourceId"})
				return true
			}
			if err:=s.store.DetachIntegrationResource(ctx,actorID,pc.Project.ID,id);err!=nil{writeStoreErr(w,err,true);return true}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w,http.StatusMethodNotAllowed,"METHOD_NOT_ALLOWED","method not allowed",nil)
		}
		return true
	}
	if rest[1] != "todos" || len(rest) < 4 || rest[3] != "integrations" {
		return false
	}
	if len(rest) != 4 && len(rest) != 5 {
		return false
	}
	if !ok {
		writeStoreErr(w, store.ErrUnauthorized, true)
		return true
	}
	localID, valid := parseInt64(rest[2])
	if !valid || localID <= 0 {
		writeValidationError(w, "invalid todo localId", "invalid_todo_local_id", map[string]any{"field": "localId"})
		return true
	}
	switch {
	case len(rest) == 4 && r.Method == http.MethodGet:
		resources, err := s.store.ListTodoIntegrationResources(ctx, actorID, pc.Project.ID, localID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusOK, integrationResourcesJSON(resources))
	case len(rest) == 4 && r.Method == http.MethodPost:
		var in struct {
			ResourceID int64 `json:"resourceId"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		if err := s.store.LinkTodoIntegrationResource(ctx, actorID, pc.Project.ID, localID, in.ResourceID); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		w.WriteHeader(http.StatusNoContent)
	case len(rest) == 5 && r.Method == http.MethodDelete:
		resourceID, valid := parseInt64(rest[4])
		if !valid || resourceID <= 0 {
			writeValidationError(w, "invalid resourceId", "invalid_resource_id", map[string]any{"field": "resourceId"})
			return true
		}
		if err := s.store.UnlinkTodoIntegrationResource(ctx, actorID, pc.Project.ID, localID, resourceID); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
	}
	return true
}
