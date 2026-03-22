package main

import (
	"context"
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

	if err := migrate.Apply(ctx, sqlDB); err != nil {
		logger.Fatalf("migrate: %v", err)
	}

	st := store.New(sqlDB, nil)
	n, err := st.RewriteDurableProjectSlugs(ctx)
	if err != nil {
		logger.Fatalf("rewrite slugs: %v", err)
	}
	logger.Printf("rewrote %d durable project slug(s)", n)
}

