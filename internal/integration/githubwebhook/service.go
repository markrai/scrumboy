package githubwebhook

import (
    "context"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "errors"
    "io"
    "log"
    "mime"
    "net/http"
    "strconv"
    "strings"
    "time"

    "scrumboy/internal/integration"
    "scrumboy/internal/store"
)

const maxGitHubPayload = 256 << 10

type Inbox interface {
    AdmitGitHubInbound(context.Context, store.GitHubInboundEvent) (bool,error)
    GitHubInboundInstallationConnected(context.Context,string) (bool,error)
    ClaimGitHubInbound(context.Context,time.Time,int,time.Duration) ([]store.GitHubInboundEvent,error)
    SettleGitHubInbound(context.Context,int64,string,bool,string,time.Time) (bool,error)
    GitHubInboundSummary(context.Context) (store.GitHubInboundCounts,error)
    ListDeadGitHubInbound(context.Context,int64,int) ([]store.GitHubInboundDeadDelivery,error)
    RequeueGitHubInbound(context.Context,int64,int64) error
    MaintainGitHubInbound(context.Context,time.Time) error
    ReconcileGitHubInboundRepositories(context.Context,string,[]integration.Resource) error
    DisableGitHubInboundInstallation(context.Context,string) error
}

type Service struct {
    secret []byte
    inbox Inbox
    automation AutomationInbox
    executor AutomationExecutor
    provider integration.Provider
    logger *log.Logger
    done chan struct{}
}

func New(secret []byte, inbox Inbox, provider integration.Provider, logger *log.Logger) (*Service,error) {
    if len(secret)<32 || len(secret)>4096 || inbox==nil || provider==nil {
        return nil,errors.New("invalid GitHub webhook configuration")
    }
    if logger==nil {logger=log.New(io.Discard,"",0)}
    automation,_:=inbox.(AutomationInbox)
    executor,_:=inbox.(AutomationExecutor)
    return &Service{secret:append([]byte(nil),secret...),inbox:inbox,automation:automation,executor:executor,provider:provider,logger:logger,done:make(chan struct{})},nil
}

func (s *Service) Done() <-chan struct{} {return s.done}

func (s *Service) ServeHTTP(w http.ResponseWriter,r *http.Request) {
    w.Header().Set("Cache-Control","no-store")
    w.Header().Set("X-Content-Type-Options","nosniff")
    if r.Method!=http.MethodPost {
        w.Header().Set("Allow",http.MethodPost)
        http.Error(w,"method not allowed",http.StatusMethodNotAllowed)
        return
    }
    contentType,_,err:=mime.ParseMediaType(r.Header.Get("Content-Type"))
    if err!=nil || contentType!="application/json" {
        http.Error(w,"application/json required",http.StatusUnsupportedMediaType)
        return
    }
    signature:=r.Header.Get("X-Hub-Signature-256")
    if len(signature)!=71 || !strings.HasPrefix(signature,"sha256=") {
        http.Error(w,"invalid webhook signature",http.StatusUnauthorized)
        return
    }
    signatureBytes,err:=hex.DecodeString(signature[len("sha256="):])
    if err!=nil || len(signatureBytes)!=sha256.Size {
        http.Error(w,"invalid webhook signature",http.StatusUnauthorized)
        return
    }
    r.Body=http.MaxBytesReader(w,r.Body,maxGitHubPayload)
    payload,err:=io.ReadAll(r.Body)
    if err!=nil {
        var tooLarge *http.MaxBytesError
        if errors.As(err,&tooLarge) {
            http.Error(w,"webhook payload too large",http.StatusRequestEntityTooLarge)
        } else {
            http.Error(w,"invalid webhook payload",http.StatusBadRequest)
        }
        return
    }
    mac:=hmac.New(sha256.New,s.secret)
    _,_ = mac.Write(payload)
    if !hmac.Equal(signatureBytes,mac.Sum(nil)) {
        http.Error(w,"invalid webhook signature",http.StatusUnauthorized)
        return
    }
    var envelope struct {
        Action string `json:"action"`
        Installation struct {
            ID int64 `json:"id"`
        } `json:"installation"`
    }
    if err:=json.Unmarshal(payload,&envelope);err!=nil {
        http.Error(w,"invalid webhook payload",http.StatusBadRequest)
        return
    }
    eventType:=r.Header.Get("X-GitHub-Event")
    if eventType=="ping" {
        w.WriteHeader(http.StatusAccepted)
        return
    }
    if eventType=="issues" || eventType=="pull_request" {
        if s.automation==nil {
            w.WriteHeader(http.StatusAccepted)
            return
        }
        digest:=sha256.Sum256(payload)
        item,supported,err:=normalizeAutomationEvent(payload,eventType,
            r.Header.Get("X-GitHub-Delivery"),hex.EncodeToString(digest[:]))
        if err!=nil {
            http.Error(w,"invalid GitHub automation event",http.StatusBadRequest)
            return
        }
        if !supported {
            w.WriteHeader(http.StatusAccepted)
            return
        }
        _,err=s.automation.AdmitGitHubAutomation(r.Context(),item)
        switch {
        case err==nil,errors.Is(err,store.ErrNotFound):
            w.WriteHeader(http.StatusAccepted)
        case errors.Is(err,store.ErrGitHubAutomationFull):
            http.Error(w,"automation inbox full",http.StatusServiceUnavailable)
        case errors.Is(err,store.ErrConflict):
            http.Error(w,"conflicting GitHub delivery",http.StatusConflict)
        case errors.Is(err,store.ErrValidation):
            http.Error(w,"invalid automation delivery metadata",http.StatusBadRequest)
        default:
            s.logger.Printf("github automation: could not admit verified delivery")
            http.Error(w,"automation inbox temporarily unavailable",http.StatusServiceUnavailable)
        }
        return
    }
    switch eventType {
    case "installation","installation_repositories","repository":
    default:
        w.WriteHeader(http.StatusAccepted)
        return
    }
    if envelope.Installation.ID<=0 || len(envelope.Action)==0 || len(envelope.Action)>64 {
        http.Error(w,"missing installation or action",http.StatusBadRequest)
        return
    }
    digest:=sha256.Sum256(payload)
    event:=store.GitHubInboundEvent{
        InstallationID:strconv.FormatInt(envelope.Installation.ID,10),
        DeliveryID:r.Header.Get("X-GitHub-Delivery"),
        EventType:eventType,
        Action:envelope.Action,
        PayloadSHA256:hex.EncodeToString(digest[:]),
    }
    _,err=s.inbox.AdmitGitHubInbound(r.Context(),event)
    switch {
    case err==nil,errors.Is(err,store.ErrNotFound):
        w.WriteHeader(http.StatusAccepted)
    case errors.Is(err,store.ErrGitHubInboundFull):
        http.Error(w,"webhook inbox full",http.StatusServiceUnavailable)
    case errors.Is(err,store.ErrConflict):
        http.Error(w,"conflicting webhook delivery",http.StatusConflict)
    case errors.Is(err,store.ErrValidation):
        http.Error(w,"invalid delivery metadata",http.StatusBadRequest)
    default:
        s.logger.Printf("github inbound: could not admit delivery")
        http.Error(w,"webhook temporarily unavailable",http.StatusServiceUnavailable)
    }
}

func (s *Service) Summary(ctx context.Context) (store.GitHubInboundCounts,error) {
    return s.inbox.GitHubInboundSummary(ctx)
}

func (s *Service) Dead(ctx context.Context,actorID int64) ([]store.GitHubInboundDeadDelivery,error) {
    return s.inbox.ListDeadGitHubInbound(ctx,actorID,20)
}

func (s *Service) Retry(ctx context.Context,actorID,eventID int64) error {
    return s.inbox.RequeueGitHubInbound(ctx,actorID,eventID)
}

func (s *Service) ProcessBatch(ctx context.Context) error {
    events,err:=s.inbox.ClaimGitHubInbound(ctx,time.Now().UTC(),5,45*time.Second)
    if err!=nil{return err}
    for _,event:=range events {
        if ctx.Err()!=nil{return ctx.Err()}
        attemptCtx,cancel:=context.WithTimeout(ctx,30*time.Second)
        err:=s.processEvent(attemptCtx,event)
        cancel()
        class:=""
        if err!=nil {
            class="provider_or_store_unavailable"
            if errors.Is(err,store.ErrNotFound){class="installation_disconnected"}
            if errors.Is(err,store.ErrValidation){class="invalid_provider_data"}
        }
        _,settleErr:=s.inbox.SettleGitHubInbound(ctx,event.ID,event.LeaseToken,err==nil,class,time.Now().UTC())
        if settleErr!=nil {return settleErr}
    }
    return nil
}

func (s *Service) processEvent(ctx context.Context,event store.GitHubInboundEvent) error {
    connected,err:=s.inbox.GitHubInboundInstallationConnected(ctx,event.InstallationID)
    if err!=nil{return err}
    if !connected{return nil}
    if event.EventType=="installation" {
        switch event.Action {
        case "deleted","suspend":
            err:=s.inbox.DisableGitHubInboundInstallation(ctx,event.InstallationID)
            if errors.Is(err,store.ErrNotFound){return nil}
            return err
        case "unsuspend","created","new_permissions_accepted":
            return nil
        }
    }
    if event.EventType!="repository" && event.EventType!="installation_repositories" {
        return nil
    }
    installations,err:=s.provider.Installations(ctx)
    if err!=nil{return err}
    owned:=false
    for _,installation:=range installations {
        if installation.ID==event.InstallationID {owned=true;break}
    }
    if !owned {return errors.New("github installation is no longer discoverable")}
    resources,err:=s.provider.Resources(ctx,event.InstallationID)
    if err!=nil{return err}
    err=s.inbox.ReconcileGitHubInboundRepositories(ctx,event.InstallationID,resources)
    if errors.Is(err,store.ErrNotFound){return nil}
    return err
}

func (s *Service) Run(ctx context.Context) {
    defer close(s.done)
    ticker:=time.NewTicker(3*time.Second)
    defer ticker.Stop()
    maintenance:=time.NewTicker(time.Hour)
    defer maintenance.Stop()
    for {
        select {
        case <-ctx.Done():return
        default:
        }
        if err:=s.ProcessBatch(ctx);err!=nil && ctx.Err()==nil {
            s.logger.Printf("github inbound: processing batch failed")
        }
        if s.automation!=nil {
            if err:=s.ProcessAutomationBatch(ctx);err!=nil && ctx.Err()==nil {
                s.logger.Printf("github automation: matching batch failed")
            }
        }
        if err:=s.ProcessAutomationExecutionBatch(ctx);err!=nil && ctx.Err()==nil {
            s.logger.Printf("github automation: execution batch failed")
        }
        select {
        case <-ctx.Done():return
        case <-ticker.C:
        case <-maintenance.C:
            if err:=s.inbox.MaintainGitHubInbound(ctx,time.Now().UTC());err!=nil && ctx.Err()==nil {
                s.logger.Printf("github inbound: maintenance failed")
            }
            if s.automation!=nil {
                if err:=s.automation.MaintainGitHubAutomation(ctx,time.Now().UTC());err!=nil && ctx.Err()==nil {
                    s.logger.Printf("github automation: maintenance failed")
                }
            }
        }
    }
}
