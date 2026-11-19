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
	"syscall"
	"time"

	"scrumboy/internal/integration/githubapp"
	"scrumboy/internal/integration/githubbroker"
)

func run() error {
	var address, certFile, keyFile, clientCAFile, grantsFile, appKeyFile string
	var appID int64
	flag.StringVar(&address, "listen", "127.0.0.1:9448", "broker HTTPS listen address")
	flag.StringVar(&certFile, "tls-cert", "", "broker TLS certificate file")
	flag.StringVar(&keyFile, "tls-key", "", "broker TLS private key file")
	flag.StringVar(&clientCAFile, "client-ca", "", "CA that signs authorized instance client certificates")
	flag.StringVar(&grantsFile, "grants", "", "operator-provisioned qualification grant snapshot")
	flag.StringVar(&appKeyFile, "github-key", "", "server-only GitHub App RSA PEM file")
	flag.Int64Var(&appID, "github-app-id", 0, "Scrumboy Connect GitHub App numeric ID")
	flag.Parse()

	if address == "" || certFile == "" || keyFile == "" || clientCAFile == "" ||
		grantsFile == "" || appKeyFile == "" || appID <= 0 {
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
	snapshot, err := githubbroker.LoadSnapshot(grantsFile)
	if err != nil {
		return errors.New("invalid or expired broker grants")
	}
	appKey, err := os.ReadFile(appKeyFile)
	if err != nil {
		return errors.New("could not read Scrumboy Connect private key")
	}
	provider, err := githubapp.New(appID, appKey, nil)
	if err != nil {
		return errors.New("invalid Scrumboy Connect configuration")
	}
	handler, err := githubbroker.New(snapshot, snapshot, provider)
	if err != nil {
		return errors.New("could not construct broker")
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
	log.Printf("Scrumboy Connect qualification broker listening on %s with mTLS and expiring operator grants; user connection flow disabled", address)
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
