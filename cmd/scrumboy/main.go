package main

import (
	"context"
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"scrumboy/internal/config"
	"scrumboy/internal/crypto"
	"scrumboy/internal/db"
	"scrumboy/internal/httpapi"
	"scrumboy/internal/mcp"
	"scrumboy/internal/migrate"
	"scrumboy/internal/oidc"
	"scrumboy/internal/projectcolor"
	"scrumboy/internal/store"
	"scrumboy/internal/tlsredirect"
)

func main() {
	cfg := config.FromEnv()

	logger := log.New(os.Stdout, "", log.LstdFlags)

	sqlDB, err := db.Open(cfg.DBPath, db.Options{
		BusyTimeout: cfg.SQLiteBusyTimeout,
		JournalMode: cfg.SQLiteJournalMode,
		Synchronous: cfg.SQLiteSynchronous,
	})
	if err != nil {
		logger.Fatalf("open db: %v", err)
	}
	defer sqlDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := migrate.Apply(ctx, sqlDB); err != nil {
		logger.Fatalf("migrate: %v", err)
	}

	// Fail fast if 2FA is in use but encryption key is missing. Do not silently run.
	if cfg.TwoFactorEncryptionKey == "" {
		var n int
		if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE two_factor_enabled = 1`).Scan(&n); err != nil {
			logger.Printf("warning: could not check 2FA users: %v", err)
		} else if n > 0 {
			logger.Fatalf("2FA is enabled for %d user(s) but SCRUMBOY_ENCRYPTION_KEY is not set. Set the key (e.g. openssl rand -base64 32) or disable 2FA for affected users via recovery code + DB.", n)
		}
	}

	var encKey []byte
	if cfg.TwoFactorEncryptionKey != "" {
		var err error
		encKey, err = crypto.DecodeKey(cfg.TwoFactorEncryptionKey)
		if err != nil {
			logger.Fatalf("invalid SCRUMBOY_ENCRYPTION_KEY: %v", err)
		}
	}
	var storeOpts *store.StoreOptions
	if len(encKey) > 0 {
		storeOpts = &store.StoreOptions{EncryptionKey: encKey}
	}
	st := store.New(sqlDB, storeOpts)

	// One-time backfill: extract dominant colors for projects that have an image but still
	// carry the migration default '#888888'. Runs at startup and is a no-op once complete.
	if n, err := st.BackfillDominantColors(ctx, projectcolor.ExtractFromDataURL); err != nil {
		logger.Printf("backfill dominant colors: %v", err)
	} else if n > 0 {
		logger.Printf("backfilled dominant colors for %d projects", n)
	}

	var oidcSvc *oidc.Service
	if cfg.OIDCEnabled() {
		oidcSvc = oidc.New(oidc.Config{
			IssuerCanonical:   cfg.OIDCIssuerCanonical,
			ClientID:          cfg.OIDCClientID,
			ClientSecret:      cfg.OIDCClientSecret,
			RedirectURL:       cfg.OIDCRedirectURL,
			LocalAuthDisabled: cfg.OIDCLocalAuthDisabled,
		})
		logger.Printf("OIDC enabled (issuer: %s)", cfg.OIDCIssuerCanonical)
	}

	srv := httpapi.NewServer(st, httpapi.Options{
		Logger:         logger,
		MaxRequestBody: cfg.MaxRequestBodyBytes,
		ScrumboyMode:   cfg.ScrumboyMode,
		MCPHandler:     mcp.New(st, mcp.Options{Mode: cfg.ScrumboyMode}),
		EncryptionKey:  encKey,
		OIDCService:    oidcSvc,
		VAPIDPublicKey:   cfg.VAPIDPublicKey,
		VAPIDPrivateKey:  cfg.VAPIDPrivateKey,
		VAPIDSubscriber:  cfg.VAPIDSubscriber,
		PushDebug:        cfg.PushDebug,
	})
	st.SetTodoAssignedPublisher(srv.PublishTodoAssigned)

	httpServer := &http.Server{
		Addr:              cfg.BindAddr,
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
	}

	_, port, _ := net.SplitHostPort(cfg.BindAddr)
	if port == "" {
		port = "8080"
	}
	useTLS := false
	if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		if _, err := os.Stat(cfg.TLSCertFile); err == nil {
			if _, err := os.Stat(cfg.TLSKeyFile); err == nil {
				useTLS = true
			}
		}
	}

	go func() {
		protocol := "http"
		if useTLS {
			protocol = "https"
		}
		logger.Printf("listening on %s", cfg.BindAddr)
		logger.Printf("  Local:    %s://127.0.0.1:%s/", protocol, port)
		logger.Printf("  Intranet: %s://%s:%s/", protocol, cfg.IntranetIP, port)
		if useTLS {
			logger.Printf("HTTPS enabled (secure context).")
			logger.Printf("Plain http:// on this port is redirected to https:// (same host and path).")
		} else {
			logger.Printf("HTTP mode. To enable HTTPS for intranet: install mkcert, run mkcert -install, then mkcert %s localhost", cfg.IntranetIP)
		}
		var err error
		if useTLS {
			cert, tlsErr := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
			if tlsErr != nil {
				logger.Fatalf("load tls: %v", tlsErr)
			}
			tlsCfg := &tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
			}
			baseLn, listenErr := net.Listen("tcp", cfg.BindAddr)
			if listenErr != nil {
				logger.Fatalf("listen: %v", listenErr)
			}
			ln := &tlsredirect.Listener{
				Inner:     baseLn,
				TLSConfig: tlsCfg,
				Log:       logger,
			}
			err = httpServer.Serve(ln)
		} else {
			err = httpServer.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			logger.Fatalf("listen: %v", err)
		}
	}()

	// Start background cleanup process for expired anonymous boards
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		ticker := time.NewTicker(1 * time.Hour) // Run every hour
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

				// Cleanup expired anonymous boards
				deleted, err := st.DeleteExpiredProjects(ctx)
				if err != nil {
					logger.Printf("cleanup expired projects: %v", err)
				} else if deleted > 0 {
					logger.Printf("deleted %d expired projects", deleted)
				}

				// WAL checkpoint to prevent unbounded WAL growth
				// TRUNCATE mode: checkpoint and truncate WAL file
				// This prevents the "week later it's slow" problem by keeping WAL small
				if _, err := sqlDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
					logger.Printf("WAL checkpoint: %v", err)
				} else {
					logger.Printf("WAL checkpoint completed")
				}

				cancel()
			case <-stop:
				return
			}
		}
	}()

	<-stop

	// Drain in-flight HTTP requests first so any final todo.assigned events
	// are published and enqueued before the webhook worker is stopped.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Printf("shutdown: %v", err)
	}
	srv.Close()
}
