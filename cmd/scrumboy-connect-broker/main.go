package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
    "strings"
	"syscall"
	"time"

	"scrumboy/internal/integration/githubapp"
	"scrumboy/internal/integration/githubbroker"
    "scrumboy/internal/integration/githubconnect"
)

func run() error {
	var address, certFile, keyFile, clientCAFile, grantsFile, identitiesFile, bindingsFile, appKeyFile string
    var clientID,clientSecretFile,redirectURI string
	var appID int64
	flag.StringVar(&address, "listen", "127.0.0.1:9448", "broker HTTPS listen address")
	flag.StringVar(&certFile, "tls-cert", "", "broker TLS certificate file")
	flag.StringVar(&keyFile, "tls-key", "", "broker TLS private key file")
	flag.StringVar(&clientCAFile, "client-ca", "", "CA that signs authorized instance client certificates")
	flag.StringVar(&grantsFile, "grants", "", "operator-provisioned qualification grant snapshot")
    flag.StringVar(&identitiesFile,"client-identities","","mTLS client identities without operator grants")
    flag.StringVar(&bindingsFile,"binding-db","","absolute path to durable verified ownership SQLite database")
    flag.StringVar(&clientID,"github-client-id","","Scrumboy Connect OAuth client ID for user-bound mode")
    flag.StringVar(&clientSecretFile,"github-client-secret-file","","server-only GitHub App OAuth client secret file")
    flag.StringVar(&redirectURI,"github-redirect-uri","","configured GitHub App OAuth redirect URL")
	flag.StringVar(&appKeyFile, "github-key", "", "server-only GitHub App RSA PEM file")
	flag.Int64Var(&appID, "github-app-id", 0, "Scrumboy Connect GitHub App numeric ID")
	flag.Parse()

	if address == "" || certFile == "" || keyFile == "" || clientCAFile == "" ||
		appKeyFile == "" || appID <= 0 || 
        !((grantsFile!="" && identitiesFile=="" && bindingsFile=="" &&
             clientID=="" && clientSecretFile=="" && redirectURI=="") ||
          (grantsFile=="" && identitiesFile!="" && bindingsFile!="" &&
             clientID!="" && clientSecretFile!="" && redirectURI!="")) {
		return errors.New("broker configuration incomplete")
	}
	serverCertificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return errors.New("invalid broker TLS identity")
	}
	caPEM, err := os.ReadFile(clientCAFile)
	if err != nil {
		return errors.New("could not read broker client CA")
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return errors.New("invalid broker client CA")
	}
	appKey, err := os.ReadFile(appKeyFile)
	if err != nil {
		return errors.New("could not read Scrumboy Connect private key")
	}
	provider, err := githubapp.New(appID, appKey, nil)
	if err != nil {
		return errors.New("invalid Scrumboy Connect configuration")
	}
    var handler http.Handler
    mode:="operator qualification"
    if bindingsFile!="" {
        identities,err:=githubbroker.LoadClientIdentities(identitiesFile)
        if err!=nil{return errors.New("invalid broker client identities")}
        bindings,err:=githubbroker.OpenBindingsDB(bindingsFile)
        if err!=nil{return errors.New("invalid verified ownership database")}
        defer bindings.Close()
        broker,err:=githubbroker.NewUserBound(identities,bindings,provider)
        if err!=nil{return errors.New("could not construct user-bound broker")}
        secret,err:=os.ReadFile(clientSecretFile)
        if err!=nil{return errors.New("cannot read GitHub OAuth client secret file")}
        verifier,err:=githubconnect.New(githubconnect.Config{
            AppID:appID,ClientID:clientID,ClientSecret:strings.TrimSpace(string(secret)),
            RedirectURI:redirectURI,
        },nil)
        if err!=nil{return errors.New("invalid GitHub OAuth broker configuration")}
        coordinator,err:=githubbroker.NewBindingCoordinator(verifier,provider,bindings)
        if err!=nil{return errors.New("could not construct verified binding coordinator")}
        if err:=broker.EnableEnrollment(coordinator);err!=nil{
            return errors.New("could not enable internal OAuth enrollment")
        }
        handler=broker
        mode="user-bound (mTLS-only OAuth enrollment; browser callback not enabled)"
    } else {
        snapshot,err:=githubbroker.LoadSnapshot(grantsFile)
        if err!=nil{return errors.New("invalid or expired broker grants")}
        broker,err:=githubbroker.New(snapshot,snapshot,provider)
        if err!=nil{return errors.New("could not construct qualification broker")}
        handler=broker
    }
	server := &http.Server{
		Addr: address,
		Handler: handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second,
		WriteTimeout: 20 * time.Second,
		IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 8192,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
			ClientAuth: tls.RequireAndVerifyClientCert,
			ClientCAs: caPool,
			Certificates: []tls.Certificate{serverCertificate},
		},
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("Scrumboy Connect broker listening on %s (%s) with mTLS; user connection flow disabled", address,mode)
	if err := server.ListenAndServeTLS("", "");err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.New("broker HTTPS listener failed")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal("scrumboy-connect-broker: " + err.Error())
	}
}
