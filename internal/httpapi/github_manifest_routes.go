package httpapi

import (
 "errors"
 "fmt"
 "net/http"

 "scrumboy/internal/integration/githubmanifest"
 "scrumboy/internal/store"
)

func (s *Server) manifestOwner(r *http.Request)(int64,bool){
 ctx:=s.requestContext(r)
 id,ok:=store.UserIDFromContext(ctx)
 if !ok{return 0,false}
 user,err:=s.store.GetUser(ctx,id)
 if err!=nil || user.SystemRole!=store.SystemRoleOwner{return 0,false}
 return id,true
}

func (s *Server) handleGitHubManifestStart(w http.ResponseWriter,r *http.Request){
 if r.Method!=http.MethodPost{writeError(w,http.StatusMethodNotAllowed,"METHOD_NOT_ALLOWED","method not allowed",nil);return}
 if s.mode!="full"{writeError(w,http.StatusNotFound,"NOT_FOUND","not found",nil);return}
 id,ok:=s.manifestOwner(r)
 if !ok{writeError(w,http.StatusForbidden,"FORBIDDEN","instance owner required",nil);return}
 if s.githubProvider!=nil{writeError(w,http.StatusConflict,"INTEGRATION_CONFIGURED","An instance-owned GitHub App is already configured; manual configuration remains active",nil);return}
 if s.githubManifest==nil || !s.githubManifest.Available(){writeError(w,http.StatusServiceUnavailable,"INTEGRATION_UNAVAILABLE","Guided GitHub App setup is not enabled on this server",nil);return}
 reg,err:=s.githubManifest.Begin(id,s.publicBaseURL)
 if err!=nil{
  if errors.Is(err,githubmanifest.ErrInvalidOrigin){writeError(w,http.StatusUnprocessableEntity,"INVALID_PUBLIC_ORIGIN","Set SCRUMBOY_PUBLIC_BASE_URL to a pathless HTTPS origin for GitHub App setup",nil);return}
  writeError(w,http.StatusServiceUnavailable,"INTEGRATION_UNAVAILABLE","GitHub App setup is temporarily unavailable",nil)
  return
 }
 writeJSON(w,http.StatusOK,reg)
}

func (s *Server) handleGitHubManifestCallback(w http.ResponseWriter,r *http.Request){
 w.Header().Set("Cache-Control","no-store")
 w.Header().Set("Referrer-Policy","no-referrer")
 w.Header().Set("X-Content-Type-Options","nosniff")
 w.Header().Set("Content-Security-Policy","default-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
 if r.Method!=http.MethodGet{http.Error(w,"method not allowed",http.StatusMethodNotAllowed);return}
 if s.mode!="full" || s.githubManifest==nil || !s.githubManifest.Available(){http.NotFound(w,r);return}
 ownerID,ok:=s.manifestOwner(r)
 if !ok{http.Error(w,"Sign in as the instance owner to complete GitHub App setup",http.StatusForbidden);return}
 if s.githubProvider!=nil{http.Error(w,"GitHub App is already configured",http.StatusConflict);return}
 if len(r.URL.Query()["code"])!=1 || len(r.URL.Query()["state"])!=1{http.Error(w,"Invalid or expired enrollment session",http.StatusBadRequest);return}
 err:=s.githubManifest.Complete(s.requestContext(r),ownerID,r.URL.Query().Get("state"),r.URL.Query().Get("code"))
 if err!=nil{
  if errors.Is(err,githubmanifest.ErrInvalidState){http.Error(w,"Invalid or expired enrollment session",http.StatusBadRequest);return}
  http.Error(w,"GitHub App enrollment could not be completed. Check the server setup and retry from Scrumboy.",http.StatusBadGateway)
  return
 }
 w.Header().Set("Content-Type","text/html; charset=utf-8")
 w.WriteHeader(http.StatusOK)
 _,_=fmt.Fprint(w,"<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"referrer\" content=\"no-referrer\"><title>GitHub App enrollment</title></head><body><main><h1>GitHub App registration received</h1><p>The instance has securely received your App credentials. Return to Scrumboy to complete installation setup.</p></main></body></html>")
}
