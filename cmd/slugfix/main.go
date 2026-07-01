package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	"scrumboy/internal/config"
	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
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

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := run(ctx, sqlDB, logger); err != nil {
		logger.Fatal(err)
	}
}

func run(ctx context.Context, sqlDB *sql.DB, logger *log.Logger) error {
	if err := migrate.Apply(ctx, sqlDB); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	st := store.New(sqlDB, nil)
	n, err := st.RewriteDurableProjectSlugs(ctx)
	if err != nil {
		return fmt.Errorf("rewrite slugs: %w", err)
	}
	logger.Printf("rewrote %d durable project slug(s)", n)
	return nil
}
