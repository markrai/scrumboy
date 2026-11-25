package githubmanifest

import (
 "context"
 "crypto/rand"
 "crypto/rsa"
 "crypto/sha256"
 "crypto/x509"
 "encoding/hex"
 "encoding/json"
 "encoding/pem"
 "errors"
 "io"
 "net/http"
 "net/url"
 "regexp"
 "strings"
 "sync"
 "time"
)

var ErrUnavailable = errors.New("GitHub App enrollment is not available")
var ErrInvalidState = errors.New("GitHub App enrollment session is invalid or expired")
var ErrInvalidOrigin = errors.New("GitHub App enrollment requires a canonical HTTPS origin")
var ErrConversion = errors.New("GitHub App registration could not be completed")

const callbackPath = "/api/integrations/github/manifest/callback"
const webhookPath = "/api/integrations/github/webhook"
const maxPending = 32
const enrollmentLifetime = 10 * time.Minute

var validCode = regexp.MustCompile(`^[A-Za-z0-9_-]{16,256}$`)
var validSlug = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,99}$`)

type Credentials struct {
 AppID int64
 Slug string
 PrivateKey []byte
 WebhookSecret string
}

type CredentialSink func(context.Context, Credentials) error

type Registration struct {
 Action string `json:"action"`
 Manifest string `json:"manifest"`
 ExpiresAt string `json:"expiresAt"`
}

type enrollment struct {
 ownerID int64
 expires time.Time
}

type Service struct {
 mu sync.Mutex
 pending map[[32]byte]enrollment
 client *http.Client
 sink CredentialSink
 now func() time.Time
}

func New(client *http.Client, sink CredentialSink) *Service {
 if client == nil {client = &http.Client{Timeout:8*time.Second,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}}
 return &Service{pending:make(map[[32]byte]enrollment),client:client,sink:sink,now:time.Now}
}

func (s *Service) Available() bool {
 return s!=nil && s.sink!=nil
}

func ValidOrigin(origin string) bool {
 if len(origin)>2048 || strings.TrimSpace(origin)!=origin {return false}
 u,err:=url.Parse(origin)
 if err!=nil || u.Scheme!="https" || u.Host=="" || u.User!=nil || u.RawQuery!="" || u.Fragment!="" || u.RawPath!="" || (u.Path!="" && u.Path!="/") {return false}
 if u.Hostname()=="" || strings.HasSuffix(strings.ToLower(u.Hostname()),".localhost") || strings.EqualFold(u.Hostname(),"localhost") {return false}
 return true
}

func (s *Service) Begin(ownerID int64,origin string)(Registration,error){
 if !s.Available(){return Registration{},ErrUnavailable}
 if ownerID<=0 || !ValidOrigin(origin){return Registration{},ErrInvalidOrigin}
 token:=make([]byte,32)
 if _,err:=rand.Read(token);err!=nil{return Registration{},ErrUnavailable}
 state:=hex.EncodeToString(token)
 base:=strings.TrimSuffix(origin,"/")
 manifest:=map[string]any{
  "url":base,
  "redirect_url":base+callbackPath,
  "hook_attributes":map[string]any{"url":base+webhookPath,"active":true},
  "public":false,
  "default_permissions":map[string]string{"metadata":"read","issues":"read","pull_requests":"read"},
  "default_events":[]string{"installation","installation_repositories","repository","issues","pull_request"},
 }
 data,err:=json.Marshal(manifest)
 if err!=nil{return Registration{},ErrUnavailable}
 now:=s.now().UTC()
 s.mu.Lock()
 defer s.mu.Unlock()
 for key,item:=range s.pending {if !now.Before(item.expires)||item.ownerID==ownerID{delete(s.pending,key)}}
 if len(s.pending)>=maxPending{return Registration{},ErrUnavailable}
 s.pending[sha256.Sum256([]byte(state))]=enrollment{ownerID:ownerID,expires:now.Add(enrollmentLifetime)}
 return Registration{Action:"https://github.com/settings/apps/new?state="+url.QueryEscape(state),Manifest:string(data),ExpiresAt:now.Add(enrollmentLifetime).Format(time.RFC3339)},nil
}

func (s *Service) Complete(ctx context.Context,ownerID int64,state,code string)error{
 if !s.Available(){return ErrUnavailable}
 if ownerID<=0 || len(state)!=64 || !validCode.MatchString(code){return ErrInvalidState}
 decoded,err:=hex.DecodeString(state)
 if err!=nil || len(decoded)!=32{return ErrInvalidState}
 key:=sha256.Sum256([]byte(state))
 s.mu.Lock()
 item,ok:=s.pending[key]
 delete(s.pending,key)
 s.mu.Unlock()
 if !ok || item.ownerID!=ownerID || !s.now().Before(item.expires){return ErrInvalidState}
 req,err:=http.NewRequestWithContext(ctx,http.MethodPost,"https://api.github.com/app-manifests/"+code+"/conversions",nil)
 if err!=nil{return ErrConversion}
 req.Header.Set("Accept","application/vnd.github+json")
 req.Header.Set("X-GitHub-Api-Version","2022-11-28")
 req.Header.Set("User-Agent","Scrumboy")
 resp,err:=s.client.Do(req)
 if err!=nil{return ErrConversion}
 defer resp.Body.Close()
 if resp.StatusCode!=http.StatusCreated && resp.StatusCode!=http.StatusOK{return ErrConversion}
 data,err:=io.ReadAll(io.LimitReader(resp.Body,96*1024+1))
 if err!=nil || len(data)>96*1024{return ErrConversion}
 var result struct {
  ID int64 `json:"id"`
  Slug string `json:"slug"`
  PEM string `json:"pem"`
  WebhookSecret string `json:"webhook_secret"`
 }
 if err:=json.Unmarshal(data,&result);err!=nil{return ErrConversion}
 if result.ID<=0 || !validSlug.MatchString(result.Slug) || len(result.WebhookSecret)<32 || len(result.PEM)>64*1024{return ErrConversion}
 block,_:=pem.Decode([]byte(result.PEM))
 if block==nil || (block.Type!="RSA PRIVATE KEY" && block.Type!="PRIVATE KEY"){return ErrConversion}
 if block.Type=="RSA PRIVATE KEY" {
  if _,err:=x509.ParsePKCS1PrivateKey(block.Bytes);err!=nil{return ErrConversion}
 }else{
  key,err:=x509.ParsePKCS8PrivateKey(block.Bytes)
  if err!=nil{return ErrConversion}
  if _,ok:=key.(*rsa.PrivateKey);!ok{return ErrConversion}
 }
 if err:=s.sink(ctx,Credentials{AppID:result.ID,Slug:result.Slug,PrivateKey:[]byte(result.PEM),WebhookSecret:result.WebhookSecret});err!=nil{return ErrConversion}
 return nil
}
