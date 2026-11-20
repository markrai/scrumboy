package githubwebhook

import (
    "context"
    "encoding/json"
    "errors"
    "strconv"
    "time"

    "scrumboy/internal/store"
)

type AutomationInbox interface {
    AdmitGitHubAutomation(context.Context,store.GitHubAutomationEvent)(bool,error)
    ClaimGitHubAutomation(context.Context,time.Time,int,time.Duration)([]store.GitHubAutomationEvent,error)
    MatchAndCompleteGitHubAutomation(context.Context,int64,string,time.Time)(int,error)
    FailGitHubAutomation(context.Context,int64,string,time.Time)(bool,error)
    GitHubAutomationSummary(context.Context)(store.GitHubAutomationCounts,error)
    MaintainGitHubAutomation(context.Context,time.Time)error
}

func normalizeAutomationEvent(payload []byte,kind,deliveryID,digest string)(store.GitHubAutomationEvent,bool,error){
    out:=store.GitHubAutomationEvent{DeliveryID:deliveryID,PayloadSHA256:digest}
    if kind!="issues" && kind!="pull_request"{return out,false,nil}
    var body struct {
        Action string `json:"action"`
        Installation struct{ID int64 `json:"id"`} `json:"installation"`
        Repository struct{ID int64 `json:"id"`} `json:"repository"`
        Number int64 `json:"number"`
        Issue *struct{Number int64 `json:"number"`} `json:"issue"`
        PullRequest *struct{
            Number int64 `json:"number"`
            Merged *bool `json:"merged"`
        } `json:"pull_request"`
    }
    if err:=json.Unmarshal(payload,&body);err!=nil{return out,false,err}
    if len(body.Action)==0 || len(body.Action)>64{return out,false,store.ErrValidation}
    event:=""
    number:=int64(0)
    switch kind {
    case "issues":
        if body.Action!="opened" && body.Action!="closed"{return out,false,nil}
        if body.Issue==nil || body.PullRequest!=nil{return out,false,store.ErrValidation}
        event="issue."+body.Action
        number=body.Issue.Number
    case "pull_request":
        if body.Action!="opened" && body.Action!="closed"{return out,false,nil}
        if body.PullRequest==nil || body.Issue!=nil || body.PullRequest.Number!=body.Number{
            return out,false,store.ErrValidation
        }
        number=body.PullRequest.Number
        if body.Action=="opened" {
            event="pull_request.opened"
        }else{
            if body.PullRequest.Merged==nil{return out,false,store.ErrValidation}
            if *body.PullRequest.Merged {
                event="pull_request.merged"
            }else{
                event="pull_request.closed"
            }
        }
    }
    if body.Installation.ID<=0 || body.Repository.ID<=0 ||
        number<=0 || number>2147483647{return out,false,store.ErrValidation}
    out.InstallationID=strconv.FormatInt(body.Installation.ID,10)
    out.RepositoryExternalID=strconv.FormatInt(body.Repository.ID,10)
    out.ItemNumber=number
    out.EventType=event
    return out,true,nil
}

func (s *Service) AutomationSummary(ctx context.Context)(store.GitHubAutomationCounts,error){
    if s.automation==nil{return store.GitHubAutomationCounts{},store.ErrNotFound}
    return s.automation.GitHubAutomationSummary(ctx)
}

func (s *Service) ProcessAutomationBatch(ctx context.Context)error{
    if s.automation==nil{return nil}
    events,err:=s.automation.ClaimGitHubAutomation(ctx,time.Now().UTC(),5,45*time.Second)
    if err!=nil{return err}
    for _,event:=range events {
        if err:=ctx.Err();err!=nil{return err}
        attemptCtx,cancel:=context.WithTimeout(ctx,30*time.Second)
        _,err:=s.automation.MatchAndCompleteGitHubAutomation(attemptCtx,event.ID,event.LeaseToken,time.Now().UTC())
        cancel()
        if err==nil || errors.Is(err,store.ErrConflict){continue}
        s.logger.Printf("github automation: matching failed; scheduling bounded retry")
        _,settleErr:=s.automation.FailGitHubAutomation(ctx,event.ID,event.LeaseToken,time.Now().UTC())
        if settleErr!=nil{return settleErr}
    }
    return nil
}
