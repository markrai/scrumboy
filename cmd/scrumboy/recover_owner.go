package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
	"scrumboy/internal/config"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

func openRecoveryDatabase(path string, busyTimeout int) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("configured SQLite database is unavailable: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(%d)", path, busyTimeout)
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	if err := database.Ping(); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

func runRecoverOwner(cfg config.Config, args []string, stdin io.Reader, stdout io.Writer) error {
	for _, arg := range args {
		if arg == "--password" || strings.HasPrefix(arg, "--password=") {
			return fmt.Errorf("passwords are never accepted as command-line arguments; use the hidden prompt or --password-stdin")
		}
	}
	flags := flag.NewFlagSet("recover-owner", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	email := flags.String("email", "", "existing owner email")
	passwordStdin := flags.Bool("password-stdin", false, "read one password line from standard input")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("invalid recover-owner arguments: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments; passwords must not appear in argv")
	}
	if strings.TrimSpace(*email) == "" {
		return fmt.Errorf("--email is required")
	}
	var password string
	if *passwordStdin {
		reader := bufio.NewReader(io.LimitReader(stdin, 4097))
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read password from stdin: %w", err)
		}
		if len(line) > 4096 {
			return fmt.Errorf("password input is too long")
		}
		password = strings.TrimRight(line, "\r\n")
	} else {
		file, ok := stdin.(*os.File)
		if !ok || !term.IsTerminal(int(file.Fd())) {
			return fmt.Errorf("interactive recovery requires a terminal; use --password-stdin deliberately for containers or automation")
		}
		_, _ = fmt.Fprint(stdout, "New Scrumboy password: ")
		first, err := term.ReadPassword(int(file.Fd()))
		_, _ = fmt.Fprintln(stdout)
		if err != nil {
			return fmt.Errorf("read hidden password: %w", err)
		}
		_, _ = fmt.Fprint(stdout, "Confirm new Scrumboy password: ")
		second, err := term.ReadPassword(int(file.Fd()))
		_, _ = fmt.Fprintln(stdout)
		if err != nil {
			return fmt.Errorf("read hidden password confirmation: %w", err)
		}
		if string(first) != string(second) {
			return fmt.Errorf("password confirmation does not match")
		}
		password = string(first)
	}
	busyTimeout := cfg.SQLiteBusyTimeout
	if busyTimeout <= 0 || busyTimeout > 5000 {
		busyTimeout = 5000
	}
	sqlDB, err := openRecoveryDatabase(cfg.DBPath, busyTimeout)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "locked") || strings.Contains(strings.ToLower(err.Error()), "busy") {
			return fmt.Errorf("database is locked; stop the active Scrumboy service, verify the configured SQLite volume, and retry: %w", err)
		}
		return fmt.Errorf("open configured database: %w", err)
	}
	defer sqlDB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := migrate.CheckRecoverySchema(ctx, sqlDB); err != nil {
		return err
	}
	st := store.New(sqlDB, nil)
	if err := st.RecoverOwnerPassword(ctx, *email, password); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "locked") || strings.Contains(strings.ToLower(err.Error()), "busy") {
			return fmt.Errorf("database is locked; stop the active Scrumboy service and retry: %w", err)
		}
		return err
	}
	_, _ = fmt.Fprintln(stdout, "Owner local password recovered; all Scrumboy sessions and pending login challenges for the owner were revoked.")
	if cfg.OIDCEnabled() && cfg.OIDCLocalAuthDisabled {
		_, _ = fmt.Fprintln(stdout, "WARNING: local authentication is disabled. Re-enable local authentication before the recovered password can be used.")
	}
	return nil
}
