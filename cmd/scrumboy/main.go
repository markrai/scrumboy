package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"scrumboy/internal/agora"
	"scrumboy/internal/config"
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

	keyResolution, err := store.ResolveStartupEncryptionKey(ctx, sqlDB, cfg.TwoFactorEncryptionKey)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrStartupEncryptionKeyRequired):
			logger.Fatalf("SCRUMBOY_ENCRYPTION_KEY is required because existing encrypted auth/security data is present. Restore the correct original SCRUMBOY_ENCRYPTION_KEY from backup or follow documented recovery guidance before changing DB state.")
		case errors.Is(err, store.ErrStartupEncryptionKeyInvalid):
			logger.Fatalf("invalid SCRUMBOY_ENCRYPTION_KEY for existing encrypted auth/security data. Restore the correct original SCRUMBOY_ENCRYPTION_KEY from backup or follow documented recovery guidance before changing DB state. Details: %v", err)
		default:
			logger.Fatalf("resolve SCRUMBOY_ENCRYPTION_KEY: %v", err)
		}
	}
	if keyResolution.InvalidIgnored {
		logger.Printf("warning: invalid SCRUMBOY_ENCRYPTION_KEY ignored because no encrypted auth/security data exists; continuing with 2FA setup and password reset disabled until a valid key is configured")
	}
	encKey := keyResolution.Key
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
	logWebPushConfiguration(logger, cfg.ScrumboyMode, cfg.VAPIDPublicKey, cfg.VAPIDPrivateKey)
	logSMTPConfiguration(logger, cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPFrom, cfg.SMTPPortExplicit, cfg.PublicBaseURL)

	maxB := cfg.MaxRequestBodyBytes
	if maxB <= 0 {
		maxB = 1 << 20
	}
	mcpH := mcp.New(st, mcp.Options{Mode: cfg.ScrumboyMode})
	srv := httpapi.NewServer(st, httpapi.Options{
		Logger:               logger,
		MaxRequestBody:       cfg.MaxRequestBodyBytes,
		MaxTrelloImportBody:  cfg.MaxTrelloImportBytes,
		ScrumboyMode:         cfg.ScrumboyMode,
		DataDir:              cfg.DataDir,
		MCPHandler:           mcpH,
		AgoraHandler:         agora.New(mcpH, agora.Options{MaxRequestBytes: maxB}),
		EncryptionKey:        encKey,
		OIDCService:          oidcSvc,
		VAPIDPublicKey:       cfg.VAPIDPublicKey,
		VAPIDPrivateKey:      cfg.VAPIDPrivateKey,
		VAPIDSubscriber:      cfg.VAPIDSubscriber,
		PushDebug:            cfg.PushDebug,
		WallEnabled:          cfg.WallEnabled,
		MarkdownNotesEnabled: cfg.MarkdownNotesEnabled,
		MermaidNotesEnabled:  cfg.MermaidNotesEnabled,
		SMTPHost:             cfg.SMTPHost,
		SMTPPort:             cfg.SMTPPort,
		SMTPUsername:         cfg.SMTPUsername,
		SMTPPassword:         cfg.SMTPPassword,
		SMTPFrom:             cfg.SMTPFrom,
		SMTPTLSMode:          cfg.SMTPTLSMode,
		SMTPDebug:            cfg.SMTPDebug,
		PublicBaseURL:        cfg.PublicBaseURL,
		TrustProxy:           cfg.TrustProxy,
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

	// Start background cleanup for expired temporary boards (any project with expires_at in the past).
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		ticker := time.NewTicker(1 * time.Hour) // Run every hour
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

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

	closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer closeCancel()
	srv.Close(closeCtx)
}

func logWebPushConfiguration(logger *log.Logger, mode, publicKey, privateKey string) {
	pub := strings.TrimSpace(publicKey)
	priv := strings.TrimSpace(privateKey)
	switch {
	case httpapi.PushConfigured(mode, publicKey, privateKey):
		logger.Printf("web push: enabled")
	case strings.TrimSpace(mode) == "anonymous" && pub != "" && priv != "":
		logger.Printf("web push: disabled (anonymous mode)")
	case pub != "" || priv != "":
		logger.Printf("web push: partial config ignored")
	default:
		logger.Printf("web push: disabled (set SCRUMBOY_VAPID_PUBLIC_KEY and SCRUMBOY_VAPID_PRIVATE_KEY)")
	}
}

func logSMTPConfiguration(logger *log.Logger, host string, port int, from string, portExplicit bool, publicBaseURL string) {
	switch {
	case httpapi.SMTPConfigured(host, port, from):
		logger.Printf("smtp: enabled (host=%s port=%d)", host, port)
		if strings.TrimSpace(publicBaseURL) == "" {
			logger.Printf("smtp: SCRUMBOY_PUBLIC_BASE_URL is missing or invalid; self-service password-reset emails are disabled until a valid public origin is configured (e.g. https://scrumboy.example.com)")
		}
	case httpapi.SMTPPartiallyConfigured(host, port, from, portExplicit):
		logger.Printf("smtp: partial or invalid config ignored (set SCRUMBOY_SMTP_HOST and SCRUMBOY_SMTP_FROM; SCRUMBOY_SMTP_PORT defaults to 587 and, when set, must be between 1 and 65535)")
	default:
		logger.Printf("smtp: disabled (set SCRUMBOY_SMTP_HOST and SCRUMBOY_SMTP_FROM to enable password-reset emails; SCRUMBOY_SMTP_PORT defaults to 587 when omitted)")
	}
}
