package githubmanifest

import (
 "context"
 "crypto/rand"
 "crypto/rsa"
 "crypto/x509"
 "encoding/json"
 "encoding/pem"
 "errors"
 "io"
 "net/http"
 "strings"
 "testing"
 "time"
)

type roundTripFunc func(*http.Request)(*http.Response,error)
func (f roundTripFunc) RoundTrip(r *http.Request)(*http.Response,error){return f(r)}

func TestEnrollmentManifestAndSingleUseCode(t *testing.T){
 key,err:=rsa.GenerateKey(rand.Reader,1024)
 if err!=nil{t.Fatal(err)}
 pemKey:=string(pem.EncodeToMemory(&pem.Block{Type:"RSA PRIVATE KEY",Bytes:x509.MarshalPKCS1PrivateKey(key)}))
 payload,_:=json.Marshal(map[string]any{"id":1234,"slug":"scrumboy-instance","pem":pemKey,"webhook_secret":strings.Repeat("a",40)})
 calls:=0
 client:=&http.Client{Transport:roundTripFunc(func(r *http.Request)(*http.Response,error){
  calls++
  if r.Method!=http.MethodPost || r.URL.String()!="https://api.github.com/app-manifests/1234567890abcdef/conversions"{t.Fatalf("unexpected conversion request %s %s",r.Method,r.URL)}
  return &http.Response{StatusCode:http.StatusCreated,Body:io.NopCloser(strings.NewReader(string(payload))),Header:http.Header{}},nil
 })}
 stored:=0
 svc:=New(client,func(ctx context.Context,c Credentials)error{
  stored++
  if c.AppID!=1234 || c.Slug!="scrumboy-instance" || string(c.PrivateKey)!=pemKey || c.WebhookSecret!=strings.Repeat("a",40){t.Fatal("wrong credentials")}
  return nil
 })
 reg,err:=svc.Begin(10,"https://scrumboy.example")
 if err!=nil{t.Fatal(err)}
 if !strings.HasPrefix(reg.Action,"https://github.com/settings/apps/new?state="){t.Fatalf("unexpected form action %q",reg.Action)}
 var manifest map[string]any
 if err:=json.Unmarshal([]byte(reg.Manifest),&manifest);err!=nil{t.Fatal(err)}
 if manifest["redirect_url"]!="https://scrumboy.example"+callbackPath || manifest["public"]!=false {t.Fatal("wrong redirect or visibility")}
 hook:=manifest["hook_attributes"].(map[string]any)
 if hook["url"]!="https://scrumboy.example"+webhookPath || hook["active"]!=true {t.Fatal("wrong webhook")}
 permissions:=manifest["default_permissions"].(map[string]any)
 if len(permissions)!=3 || permissions["metadata"]!="read" || permissions["issues"]!="read" || permissions["pull_requests"]!="read"{t.Fatalf("unsafe permissions %#v",permissions)}
 state:=strings.TrimPrefix(reg.Action,"https://github.com/settings/apps/new?state=")
 if err:=svc.Complete(context.Background(),11,state,"1234567890abcdef");!errors.Is(err,ErrInvalidState){t.Fatalf("cross-owner callback accepted: %v",err)}
 if calls!=0||stored!=0{t.Fatal("cross-owner callback reached conversion")}
 reg,err=svc.Begin(10,"https://scrumboy.example")
 if err!=nil{t.Fatal(err)}
 state=strings.TrimPrefix(reg.Action,"https://github.com/settings/apps/new?state=")
 if err:=svc.Complete(context.Background(),10,state,"1234567890abcdef");err!=nil{t.Fatal(err)}
 if calls!=1||stored!=1{t.Fatalf("conversion=%d stored=%d",calls,stored)}
 if err:=svc.Complete(context.Background(),10,state,"1234567890abcdef");!errors.Is(err,ErrInvalidState){t.Fatalf("replay accepted %v",err)}
 if calls!=1{t.Fatal("replay made second GitHub request")}
}

func TestEnrollmentFailClosed(t *testing.T){
 svc:=New(nil,nil)
 if _,err:=svc.Begin(5,"https://example.com");!errors.Is(err,ErrUnavailable){t.Fatalf("unwired sink allowed registration %v",err)}
 svc=New(nil,func(context.Context,Credentials)error{return nil})
 for _,origin:=range []string{"","http://example.com","https://user@example.com","https://example.com/path","https://example.com?x=1","https://localhost","https://example.com/#fragment"}{
  if _,err:=svc.Begin(5,origin);!errors.Is(err,ErrInvalidOrigin){t.Fatalf("invalid origin %q accepted: %v",origin,err)}
 }
 if err:=svc.Complete(context.Background(),1,"short","1234567890abcdef");!errors.Is(err,ErrInvalidState){t.Fatalf("invalid state accepted: %v",err)}
 now:=time.Now()
 svc.now=func()time.Time{return now}
 reg,err:=svc.Begin(5,"https://example.com")
 if err!=nil{t.Fatal(err)}
 state:=strings.TrimPrefix(reg.Action,"https://github.com/settings/apps/new?state=")
 svc.now=func()time.Time{return now.Add(11*time.Minute)}
 if err:=svc.Complete(context.Background(),5,state,"1234567890abcdef");!errors.Is(err,ErrInvalidState){t.Fatalf("expired state accepted: %v",err)}
}

func TestEnrollmentConversionDoesNotLeakSecrets(t *testing.T){
 svc:=New(&http.Client{Transport:roundTripFunc(func(r *http.Request)(*http.Response,error){
  return &http.Response{StatusCode:422,Body:io.NopCloser(strings.NewReader("PRIVATE KEY: example")),Header:http.Header{}},nil
 })},func(context.Context,Credentials)error{return nil})
 reg,err:=svc.Begin(1,"https://example.com")
 if err!=nil{t.Fatal(err)}
 state:=strings.TrimPrefix(reg.Action,"https://github.com/settings/apps/new?state=")
 err=svc.Complete(context.Background(),1,state,"1234567890abcdef")
 if !errors.Is(err,ErrConversion)||strings.Contains(err.Error(),"PRIVATE KEY"){t.Fatalf("unredacted error %v",err)}
}
