package githubbroker

import (
    "context"
    "sort"
    "strconv"
    "strings"

    "scrumboy/internal/integration"
    "scrumboy/internal/integration/githubconnect"
)

type GitHubUserVerifier interface {
    Begin(string) (string,error)
    Complete(context.Context,string,string,string,int64) (githubconnect.Verification,error)
}

type GitHubRepositoryReader interface {
    Resources(context.Context,string) ([]integration.Resource,error)
}

type BindingCoordinator struct {
    verifier GitHubUserVerifier
    repositories GitHubRepositoryReader
    bindings *BindingsDB
}

type BindingSummary struct {
    InstanceID string
    UserID int64
    GitHubUserID int64
    GitHubLogin string
    InstallationID int64
    AccountID int64
    AccountLogin string
    AccountType string
    RepositoryIDs []int64
}

func NewBindingCoordinator(verifier GitHubUserVerifier,reader GitHubRepositoryReader,bindings *BindingsDB) (*BindingCoordinator,error) {
    if verifier==nil || reader==nil || bindings==nil || bindings.db==nil {return nil,ErrUnavailable}
    return &BindingCoordinator{verifier:verifier,repositories:reader,bindings:bindings},nil
}

func bindingSubject(instance string,userID int64) string {
    return instance+":"+strconv.FormatInt(userID,10)
}

func parsePositiveID(raw string)(int64,error) {
    result,err:=strconv.ParseInt(raw,10,64)
    if err!=nil || result<=0{return 0,ErrDenied}
    return result,nil
}

func (b *BindingCoordinator) Begin(instance string,userID int64) (string,error) {
    if b==nil || !validInstance(instance) || userID<=0{return "",ErrDenied}
    return b.verifier.Begin(bindingSubject(instance,userID))
}

func (b *BindingCoordinator) Complete(ctx context.Context,instance string,userID int64,state,code string,installationID int64) (BindingSummary,error) {
    result:=BindingSummary{}
    if b==nil || !validInstance(instance) || userID<=0 || installationID<=0 {return result,ErrDenied}
    subject:=bindingSubject(instance,userID)
    verified,err:=b.verifier.Complete(ctx,subject,state,code,installationID)
    if err!=nil{return result,ErrDenied}
    if verified.Subject!=subject || verified.InstallationID!=installationID ||
        verified.GitHubUserID<=0 || verified.AccountID<=0{return result,ErrDenied}
    resources,err:=b.repositories.Resources(ctx,strconv.FormatInt(installationID,10))
    if err!=nil{return result,ErrUnavailable}
    if len(resources)>maxBindingRepositories{return result,ErrUnavailable}
    catalog:=make(map[int64]integration.Resource,len(resources))
    for _,resource:=range resources{
        id,err:=parsePositiveID(resource.ID)
        if err!=nil || catalog[id].ID!="" || len(resource.Name)>200 ||
            !repositoryNamePattern.MatchString(resource.Name) ||
            strings.Contains(resource.Name,"..") ||
            !strings.EqualFold(resource.URL,"https://github.com/"+resource.Name) {
            return result,ErrUnavailable
        }
        catalog[id]=resource
    }
    selected:=make([]integration.Resource,0,len(verified.AuthorizedRepositoryIDs))
    seen:=make(map[int64]bool,len(verified.AuthorizedRepositoryIDs))
    for _,id:=range verified.AuthorizedRepositoryIDs{
        if id<=0 || seen[id]{return result,ErrDenied}
        resource,ok:=catalog[id]
        if !ok{return result,ErrDenied}
        seen[id]=true
        selected=append(selected,resource)
    }
    sort.Slice(selected,func(i,j int)bool{
        a,_:=parsePositiveID(selected[i].ID)
        b,_:=parsePositiveID(selected[j].ID)
        return a<b
    })
    record:=verifiedRecord{instance:instance,userID:userID,verification:verified,resources:selected}
    if err:=b.bindings.recordVerified(ctx,record);err!=nil{return result,err}
    result=BindingSummary{
        InstanceID:instance,UserID:userID,GitHubUserID:verified.GitHubUserID,
        GitHubLogin:verified.GitHubLogin,InstallationID:verified.InstallationID,
        AccountID:verified.AccountID,AccountLogin:verified.AccountLogin,
        AccountType:verified.AccountType,RepositoryIDs:make([]int64,0,len(selected)),
    }
    for _,r:=range selected{
        id,_:=parsePositiveID(r.ID)
        result.RepositoryIDs=append(result.RepositoryIDs,id)
    }
    return result,nil
}
