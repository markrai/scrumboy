package githubbroker

import (
    "context"
    "encoding/json"
    "errors"
    "io"
    "mime"
    "net/http"
    "strings"
    "time"
)

type Enrollment interface {
    Begin(string,int64) (string,error)
    Complete(context.Context,string,int64,string,string,int64) (BindingSummary,error)
}

func (b *Broker) EnableEnrollment(service Enrollment) error {
    if b==nil || b.userGrants==nil || service==nil || b.enrollment!=nil {
        return ErrUnavailable
    }
    b.enrollment=service
    return nil
}

type enrollmentRequest struct {
    UserID int64 `json:"userId"`
    InstallationID int64 `json:"installationId,omitempty"`
    State string `json:"state,omitempty"`
    Code string `json:"code,omitempty"`
}

func (b *Broker) handleEnrollment(w http.ResponseWriter,r *http.Request,instance string) {
    mediaType,_,err:=mime.ParseMediaType(r.Header.Get("Content-Type"))
    if err!=nil || mediaType!="application/json" {
        writeError(w,http.StatusUnsupportedMediaType)
        return
    }
    var in enrollmentRequest
    decoder:=json.NewDecoder(http.MaxBytesReader(w,r.Body,maxRequestBytes))
    decoder.DisallowUnknownFields()
    if decoder.Decode(&in)!=nil {
        writeError(w,http.StatusBadRequest)
        return
    }
    var extra any
    if !errors.Is(decoder.Decode(&extra),io.EOF) || in.UserID<=0 {
        writeError(w,http.StatusBadRequest)
        return
    }
    ctx,cancel:=context.WithTimeout(r.Context(),15*time.Second)
    defer cancel()
    if r.URL.Path=="/v1/github/connections/begin" {
        if in.InstallationID!=0 || in.State!="" || in.Code!="" {
            writeError(w,http.StatusBadRequest)
            return
        }
        authorizationURL,err:=b.enrollment.Begin(instance,in.UserID)
        if err!=nil || !strings.HasPrefix(authorizationURL,"https://github.com/login/oauth/authorize?") {
            writeError(w,http.StatusForbidden)
            return
        }
        writeJSON(w,map[string]string{"authorizationUrl":authorizationURL})
        return
    }
    if in.InstallationID<=0 || len(in.State)!=43 || len(in.Code)==0 ||
        len(in.Code)>1024 || strings.ContainsAny(in.Code,"\r\n\x00") {
        writeError(w,http.StatusBadRequest)
        return
    }
    binding,err:=b.enrollment.Complete(ctx,instance,in.UserID,in.State,in.Code,in.InstallationID)
    if err!=nil {
        writeError(w,http.StatusForbidden)
        return
    }
    writeJSON(w,map[string]any{"binding":binding})
}
