package githubwebhook

import (
    "context"
    "errors"
    "os"
    "time"

    "scrumboy/internal/store"
)

type AutomationExecutor interface {
    ClaimIntegrationAutomationActions(context.Context,time.Time,int,time.Duration)([]store.IntegrationAutomationActionClaim,error)
    SettleIntegrationAutomationAction(context.Context,int64,string,time.Time)(string,error)
    FailIntegrationAutomationAction(context.Context,int64,string,time.Time)(bool,error)
}

func (s *Service) ProcessAutomationExecutionBatch(ctx context.Context)error{
    if s.executor==nil || os.Getenv("SCRUMBOY_GITHUB_AUTOMATION_EXECUTION_ENABLED")!="true"{return nil}
    actions,err:=s.executor.ClaimIntegrationAutomationActions(ctx,time.Now().UTC(),5,45*time.Second)
    if err!=nil{return err}
    for _,action:=range actions {
        if err:=ctx.Err();err!=nil{return err}
        if os.Getenv("SCRUMBOY_GITHUB_AUTOMATION_EXECUTION_ENABLED")!="true"{return nil}
        attemptCtx,cancel:=context.WithTimeout(ctx,30*time.Second)
        _,err:=s.executor.SettleIntegrationAutomationAction(attemptCtx,action.ID,action.LeaseToken,time.Now().UTC())
        cancel()
        if err==nil || errors.Is(err,store.ErrConflict){continue}
        s.logger.Printf("github automation: notification execution failed; scheduling bounded retry")
        if _,retryErr:=s.executor.FailIntegrationAutomationAction(ctx,action.ID,action.LeaseToken,time.Now().UTC());retryErr!=nil{
            return retryErr
        }
    }
    return nil
}
