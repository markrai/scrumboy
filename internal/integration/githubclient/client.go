package githubclient

import (
    "bytes"
    "context"
    "crypto/tls"
    "crypto/x509"
    "encoding/json"
    "errors"
    "io"
    "net/http"
    "net/url"
    "strconv"
    "strings"
    "time"

    "scrumboy/internal/integration"
)

var ErrDenied = errors.New("Scrumboy Connect request denied")
var ErrUnavailable = errors.New("Scrumboy Connect unavailable")
var ErrInvalid = errors.New("invalid Scrumboy Connect request")

const maxResponseBytes int64 = 256 << 10

type Config struct {
    Endpoint string
    Certificate tls.Certificate
    ServerRoots *x509.CertPool
}

type Client struct {
    origin string
    http *http.Client
    transport *http.Transport
}

type Binding struct {
    InstanceID string `json:"InstanceID"`
    UserID int64 `json:"UserID"`
    GitHubUserID int64 `json:"GitHubUserID"`
    GitHubLogin string `json:"GitHubLogin"`
    InstallationID int64 `json:"InstallationID"`
    AccountID int64 `json:"AccountID"`
    AccountLogin string `json:"AccountLogin"`
    AccountType string `json:"AccountType"`
    RepositoryIDs []int64 `json:"RepositoryIDs"`
}

type request struct {
    UserID int64 `json:"userId"`
    InstallationID int64 `json:"installationId,omitempty"`
    RepositoryID int64 `json:"repositoryId,omitempty"`
    Kind string `json:"kind,omitempty"`
    Number int64 `json:"number,omitempty"`
    Query string `json:"query,omitempty"`
    State string `json:"state,omitempty"`
    Code string `json:"code,omitempty"`
}

func New(config Config) (*Client,error) {
    u,err:=url.Parse(config.Endpoint)
    if err!=nil || u.Scheme!="https" || u.Host=="" || u.User!=nil ||
        (u.Path!="" && u.Path!="/") || u.RawQuery!="" || u.Fragment!="" ||
        u.Opaque!="" || u.RawFragment!="" || u.ForceQuery ||
        config.ServerRoots==nil || len(config.Certificate.Certificate)==0 ||
        config.Certificate.PrivateKey==nil {
        return nil,ErrInvalid
    }
    transport:=&http.Transport{
        TLSClientConfig:&tls.Config{
            MinVersion:tls.VersionTLS13,
            Certificates:[]tls.Certificate{config.Certificate},
            RootCAs:config.ServerRoots,
        },
        MaxIdleConns:10,MaxConnsPerHost:10,
        IdleConnTimeout:20*time.Second,
        TLSHandshakeTimeout:5*time.Second,
        ResponseHeaderTimeout:10*time.Second,
        DisableCompression:true,ForceAttemptHTTP2:true,
    }
    client:=&http.Client{
        Transport:transport,Timeout:15*time.Second,
        CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse},
    }
    return &Client{origin:"https://"+u.Host,http:client,transport:transport},nil
}

func (c *Client) Close() {
    if c!=nil && c.transport!=nil{c.transport.CloseIdleConnections()}
}

func (c *Client) call(ctx context.Context,path string,body request,output any) error {
    if c==nil || c.http==nil{return ErrUnavailable}
    encoded,err:=json.Marshal(body)
    if err!=nil || len(encoded)>4096{return ErrInvalid}
    req,err:=http.NewRequestWithContext(ctx,http.MethodPost,c.origin+path,bytes.NewReader(encoded))
    if err!=nil{return ErrInvalid}
    req.Header.Set("Content-Type","application/json")
    req.Header.Set("Accept","application/json")
    resp,err:=c.http.Do(req)
    if err!=nil{return ErrUnavailable}
    defer resp.Body.Close()
    if resp.StatusCode==http.StatusUnauthorized || resp.StatusCode==http.StatusForbidden ||
        resp.StatusCode==http.StatusNotFound {
        return ErrDenied
    }
    if output==nil && resp.StatusCode==http.StatusNoContent{return nil}
    if resp.StatusCode!=http.StatusOK{return ErrUnavailable}
    data,err:=io.ReadAll(io.LimitReader(resp.Body,maxResponseBytes+1))
    if err!=nil || int64(len(data))>maxResponseBytes{return ErrUnavailable}
    if output==nil || len(data)==0{return ErrUnavailable}
    if err:=json.Unmarshal(data,output);err!=nil{return ErrUnavailable}
    return nil
}

func validIdentity(userID,installationID int64)bool {
    return userID>0 && installationID>0
}

func validKind(kind string)bool {
    return kind=="issue" || kind=="pr"
}

func validItem(item integration.GitHubItem,kind string)bool {
    if item.Kind!=kind || item.Number<=0 || item.Number>2147483647 ||
        len(item.Title)==0 || len(item.Title)>500 ||
        (item.State!="open" && item.State!="closed"){return false}
    u,err:=url.Parse(item.URL)
    if err!=nil || u.Scheme!="https" || u.Host!="github.com" || u.User!=nil ||
        u.RawQuery!="" || u.Fragment!=""{return false}
    segments:=strings.Split(strings.TrimPrefix(u.Path,"/"),"/")
    if len(segments)!=4 || len(segments[0])==0 || len(segments[1])==0 ||
        segments[3]!=strconv.FormatInt(item.Number,10){return false}
    if kind=="pr" {return segments[2]=="pull"}
    return segments[2]=="issues"
}

func (c *Client) Resolve(ctx context.Context,userID,installationID,repositoryID int64,kind string,number int64) (integration.GitHubItem,error) {
    if !validIdentity(userID,installationID)||repositoryID<=0||!validKind(kind)||number<=0||number>2147483647{
        return integration.GitHubItem{},ErrInvalid
    }
    var out struct{Item integration.GitHubItem `json:"item"`}
    err:=c.call(ctx,"/v1/github/items/resolve",request{UserID:userID,InstallationID:installationID,RepositoryID:repositoryID,Kind:kind,Number:number},&out)
    if err!=nil{return integration.GitHubItem{},err}
    if !validItem(out.Item,kind)||out.Item.Number!=number{return integration.GitHubItem{},ErrUnavailable}
    return out.Item,nil
}

func (c *Client) Search(ctx context.Context,userID,installationID,repositoryID int64,kind,query string)([]integration.GitHubItem,error) {
    if !validIdentity(userID,installationID)||repositoryID<=0||!validKind(kind)||
        len(query)<2||len(query)>100||len(strings.TrimSpace(query))<2{return nil,ErrInvalid}
    var out struct{Items []integration.GitHubItem `json:"items"`}
    err:=c.call(ctx,"/v1/github/items/search",request{UserID:userID,InstallationID:installationID,RepositoryID:repositoryID,Kind:kind,Query:query},&out)
    if err!=nil{return nil,err}
    if len(out.Items)>20{return nil,ErrUnavailable}
    for _,item:=range out.Items {
        if !validItem(item,kind){return nil,ErrUnavailable}
    }
    if out.Items==nil{out.Items=[]integration.GitHubItem{}}
    return out.Items,nil
}

func (c *Client) Begin(ctx context.Context,userID int64)(string,error){
    if userID<=0{return "",ErrInvalid}
    var out struct{AuthorizationURL string `json:"authorizationUrl"`}
    if err:=c.call(ctx,"/v1/github/connections/begin",request{UserID:userID},&out);err!=nil{return "",err}
    u,err:=url.Parse(out.AuthorizationURL)
    if err!=nil||u.Scheme!="https"||u.Host!="github.com"||u.Path!="/login/oauth/authorize"||
        u.User!=nil||u.Fragment!=""||len(u.Query().Get("state"))!=43||
        u.Query().Get("code_challenge_method")!="S256"||len(u.Query().Get("code_challenge"))!=43 {
        return "",ErrUnavailable
    }
    return out.AuthorizationURL,nil
}

func (c *Client) Complete(ctx context.Context,userID,installationID int64,state,code string)(Binding,error){
    if !validIdentity(userID,installationID)||len(state)!=43||len(code)==0||len(code)>1024||
        strings.ContainsAny(code,"\r\n\x00"){return Binding{},ErrInvalid}
    var out struct{Binding Binding `json:"binding"`}
    err:=c.call(ctx,"/v1/github/connections/complete",request{UserID:userID,InstallationID:installationID,State:state,Code:code},&out)
    if err!=nil{return Binding{},err}
    if out.Binding.UserID!=userID||out.Binding.InstallationID!=installationID||
        out.Binding.GitHubUserID<=0||out.Binding.AccountID<=0||len(out.Binding.InstanceID)==0 {
        return Binding{},ErrUnavailable
    }
    return out.Binding,nil
}

func (c *Client) Revoke(ctx context.Context,userID,installationID,repositoryID int64)error {
    if userID<=0||installationID<0||repositoryID<0||(repositoryID>0&&installationID==0){
        return ErrInvalid
    }
    return c.call(ctx,"/v1/github/connections/revoke",request{UserID:userID,InstallationID:installationID,RepositoryID:repositoryID},nil)
}
