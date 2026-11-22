package httpapi

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/integration/githubwebhook"
)

func TestGitHubRuntimeActivationIsAtomicAndSingleUse(t *testing.T) {
	server := &Server{mode:"full"}
	if provider,_,webhook:=server.githubSnapshot(); provider!=nil||webhook!=nil {
		t.Fatal("unconfigured server exposed GitHub state")
	}
	provider:=&githubFakeProvider{}
	webhook,err:=githubwebhook.New([]byte(strings.Repeat("w",40)),&routeGitHubInbox{},provider,nil)
	if err!=nil{t.Fatal(err)}
	var wg sync.WaitGroup
	start:=make(chan struct{})
	for i:=0;i<12;i++{
		wg.Add(1)
		go func(){
			defer wg.Done()
			<-start
			for j:=0;j<100;j++{
				p,slug,w:=server.githubSnapshot()
				if (p==nil)!=(w==nil) {
					t.Error("partially published GitHub provider and webhook")
					return
				}
				if p!=nil&&slug!="scrumboy-owned"{t.Error("partially published GitHub slug");return}
			}
		}()
	}
	close(start)
	if err:=server.ActivateGitHub(provider,"scrumboy-owned",webhook);err!=nil{t.Fatal(err)}
	wg.Wait()
	if err:=server.ActivateGitHub(provider,"second-app",webhook);!errors.Is(err,ErrGitHubAlreadyConfigured){
		t.Fatalf("existing App overwritten: %v",err)
	}
	server.githubMu.Lock()
	cancel:=server.githubWebhookCancel
	server.githubClosed=true
	server.githubMu.Unlock()
	if cancel==nil{t.Fatal("webhook worker not started")}
	cancel()
	select{
	case <-webhook.Done():
	case <-time.After(4*time.Second):t.Fatal("GitHub webhook worker did not shut down")
	}
}

func TestGitHubRuntimeRejectsActivationDuringShutdownAndAnonymousMode(t *testing.T){
	provider:=&githubFakeProvider{}
	webhook,err:=githubwebhook.New([]byte(strings.Repeat("a",40)),&routeGitHubInbox{},provider,nil)
	if err!=nil{t.Fatal(err)}
	closed:=&Server{mode:"full",githubClosed:true}
	if err:=closed.ActivateGitHub(provider,"closed",webhook);!errors.Is(err,ErrGitHubActivationUnavailable){
		t.Fatalf("activated after shutdown: %v",err)
	}
	anonymous:=&Server{mode:"anonymous"}
	if err:=anonymous.ActivateGitHub(provider,"anonymous",webhook);!errors.Is(err,ErrGitHubActivationUnavailable){
		t.Fatalf("activated in anonymous mode: %v",err)
	}
}
