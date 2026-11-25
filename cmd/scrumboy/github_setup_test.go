package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/config"
	"scrumboy/internal/integration"
	"scrumboy/internal/integration/githubcredentials"
	"scrumboy/internal/integration/githubmanifest"
	"scrumboy/internal/integration/githubwebhook"
	"scrumboy/internal/store"
)

type githubSetupInbox struct{}

func (githubSetupInbox) AdmitGitHubInbound(context.Context, store.GitHubInboundEvent) (bool,error) { return true,nil }
func (githubSetupInbox) GitHubInboundInstallationConnected(context.Context,string)(bool,error) { return false,nil }
func (githubSetupInbox) ClaimGitHubInbound(context.Context,time.Time,int,time.Duration)([]store.GitHubInboundEvent,error){return nil,nil}
func (githubSetupInbox) SettleGitHubInbound(context.Context,int64,string,bool,string,time.Time)(bool,error){return false,nil}
func (githubSetupInbox) GitHubInboundSummary(context.Context)(store.GitHubInboundCounts,error){return store.GitHubInboundCounts{},nil}
func (githubSetupInbox) ListDeadGitHubInbound(context.Context,int64,int)([]store.GitHubInboundDeadDelivery,error){return nil,nil}
func (githubSetupInbox) RequeueGitHubInbound(context.Context,int64,int64)error{return nil}
func (githubSetupInbox) MaintainGitHubInbound(context.Context,time.Time)error{return nil}
func (githubSetupInbox) ReconcileGitHubInboundRepositories(context.Context,string,[]integration.Resource)error{return nil}
func (githubSetupInbox) DisableGitHubInboundInstallation(context.Context,string)error{return nil}

type githubSetupRoundTrip func(*http.Request)(*http.Response,error)
func (f githubSetupRoundTrip)RoundTrip(r *http.Request)(*http.Response,error){return f(r)}

func testGitHubPrivateKey(t *testing.T) []byte {
	t.Helper()
	key,err:=rsa.GenerateKey(rand.Reader,2048)
	if err!=nil{t.Fatal(err)}
	return pem.EncodeToMemory(&pem.Block{Type:"RSA PRIVATE KEY",Bytes:x509.MarshalPKCS1PrivateKey(key)})
}

func testGitHubConversionClient(t *testing.T, key []byte) *http.Client {
	t.Helper()
	body,err:=json.Marshal(map[string]any{
		"id":int64(73482),"slug":"scrumboy-instance-example",
		"pem":string(key),"webhook_secret":strings.Repeat("s",40),
	})
	if err!=nil{t.Fatal(err)}
	return &http.Client{Transport:githubSetupRoundTrip(func(r *http.Request)(*http.Response,error){
		if r.URL.String()!="https://api.github.com/app-manifests/1234567890abcdef/conversions" || r.Method!=http.MethodPost {
			t.Fatalf("unexpected manifest conversion: %s %s",r.Method,r.URL)
		}
		return &http.Response{StatusCode:http.StatusCreated,Header:make(http.Header),Body:io.NopCloser(strings.NewReader(string(body)))},nil
	})}
}

func testCompleteGitHubManifest(t *testing.T, manifest *githubmanifest.Service) error {
	t.Helper()
	reg,err:=manifest.Begin(1,"https://instance.example")
	if err!=nil{t.Fatal(err)}
	state:=strings.TrimPrefix(reg.Action,"https://github.com/settings/apps/new?state=")
	return manifest.Complete(context.Background(),1,state,"1234567890abcdef")
}

func TestGuidedGitHubManifestPersistsAndReloads(t *testing.T) {
	dir:=t.TempDir()
	cfg:=config.Config{DataDir:dir,ScrumboyMode:"full",PublicBaseURL:"https://instance.example"}
	activated:=0
	setup,err:=prepareGitHub(cfg,githubSetupInbox{},log.New(io.Discard,"",0),func(p integration.Provider,slug string,w *githubwebhook.Service)error{
		if p==nil||w==nil||slug!="scrumboy-instance-example"{t.Fatal("wrong activated GitHub App")}
		activated++
		return nil
	},testGitHubConversionClient(t,testGitHubPrivateKey(t)))
	if err!=nil||setup.manifest==nil||setup.provider!=nil||setup.webhook!=nil{t.Fatalf("unexpected initial state: %v",err)}
	if err:=testCompleteGitHubManifest(t,setup.manifest);err!=nil{t.Fatal(err)}
	if activated!=1{t.Fatal("GitHub runtime not activated")}
	storeOnDisk,err:=githubcredentials.New(dir)
	if err!=nil{t.Fatal(err)}
	saved,found,err:=storeOnDisk.Load()
	if err!=nil||!found||saved.AppID!=73482||saved.Slug!="scrumboy-instance-example"{t.Fatalf("credential bundle missing: %v",err)}
	restart,err:=prepareGitHub(cfg,githubSetupInbox{},nil,nil,nil)
	if err!=nil||restart.provider==nil||restart.webhook==nil||restart.manifest!=nil||restart.appSlug!=saved.Slug{
		t.Fatalf("restart did not restore GitHub runtime: %v",err)
	}
}

func TestGuidedGitHubActivationInterruptedRecoversFromDisk(t *testing.T) {
	cfg:=config.Config{DataDir:t.TempDir(),ScrumboyMode:"full",PublicBaseURL:"https://instance.example"}
	setup,err:=prepareGitHub(cfg,githubSetupInbox{},nil,func(integration.Provider,string,*githubwebhook.Service)error{
		return errors.New("instance shut down during callback")
	},testGitHubConversionClient(t,testGitHubPrivateKey(t)))
	if err!=nil{t.Fatal(err)}
	if err:=testCompleteGitHubManifest(t,setup.manifest);!errors.Is(err,githubmanifest.ErrConversion){
		t.Fatalf("interrupted activation did not fail safely: %v",err)
	}
	restart,err:=prepareGitHub(cfg,githubSetupInbox{},nil,nil,nil)
	if err!=nil||restart.provider==nil||restart.webhook==nil{t.Fatalf("interrupted activation was not recoverable: %v",err)}
}

func TestGuidedGitHubManualOverrideAndCorruptBundle(t *testing.T) {
	dir:=t.TempDir()
	bundleStore,err:=githubcredentials.New(dir)
	if err!=nil{t.Fatal(err)}
	if err:=bundleStore.Save(githubmanifest.Credentials{AppID:8432,Slug:"stored-instance",PrivateKey:testGitHubPrivateKey(t),WebhookSecret:strings.Repeat("x",40)});err!=nil{t.Fatal(err)}
	keyPath:=filepath.Join(dir,"manual.pem")
	if err:=os.WriteFile(keyPath,testGitHubPrivateKey(t),0600);err!=nil{t.Fatal(err)}
	cfg:=config.Config{DataDir:dir,ScrumboyMode:"full",GitHubAppID:strconv.FormatInt(9731,10),GitHubAppSlug:"manual-app",GitHubAppPrivateKeyFile:keyPath}
	manual,err:=prepareGitHub(cfg,githubSetupInbox{},nil,nil,nil)
	if err!=nil||manual.provider==nil||manual.appSlug!="manual-app"||manual.manifest!=nil{
		t.Fatalf("manual config did not override bundle: %v",err)
	}
	cfg.GitHubAppID=""
	cfg.GitHubAppSlug=""
	cfg.GitHubAppPrivateKeyFile=""
	recovered,err:=prepareGitHub(cfg,githubSetupInbox{},nil,nil,nil)
	if err!=nil||recovered.provider==nil||recovered.appSlug!="stored-instance"{t.Fatalf("guided bundle not recovered: %v",err)}
	cfg.GitHubWebhookSecret=strings.Repeat("m",40)
	if _,err:=prepareGitHub(cfg,githubSetupInbox{},nil,nil,nil);err==nil{t.Fatal("manual secret overrode guided bundle")}
	if err:=os.WriteFile(filepath.Join(dir,".scrumboy-github-app","credentials.json"),[]byte("bad-json"),0600);err!=nil{t.Fatal(err)}
	cfg.GitHubWebhookSecret=""
	if _,err:=prepareGitHub(cfg,githubSetupInbox{},nil,nil,nil);err==nil{t.Fatal("corrupt credential bundle accepted")}
}
