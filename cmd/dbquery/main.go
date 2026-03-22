package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"scrumboy/internal/config"
	"scrumboy/internal/db"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: dbquery <query>")
		fmt.Println("Example: dbquery 'SELECT version FROM schema_migrations'")
		os.Exit(1)
	}

	cfg := config.FromEnv()
	sqlDB, err := db.Open(cfg.DBPath, db.Options{
		BusyTimeout: cfg.SQLiteBusyTimeout,
		JournalMode: cfg.SQLiteJournalMode,
		Synchronous: cfg.SQLiteSynchronous,
	})
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer sqlDB.Close()

	query := os.Args[1]
	ctx := context.Background()

	rows, err := sqlDB.QueryContext(ctx, query)
	if err != nil {
		log.Fatalf("query: %v", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		log.Fatalf("columns: %v", err)
	}

	// Print header
	for i, col := range cols {
		if i > 0 {
			fmt.Print(" | ")
		}
		fmt.Print(col)
	}
	fmt.Println()

	// Print rows
	values := make([]interface{}, len(cols))
	valuePtrs := make([]interface{}, len(cols))
	for i := range values {
		valuePtrs[i] = &values[i]
	}

	for rows.Next() {
		if err := rows.Scan(valuePtrs...); err != nil {
			log.Fatalf("scan: %v", err)
		}
		for i, val := range values {
			if i > 0 {
				fmt.Print(" | ")
			}
			if val == nil {
				fmt.Print("NULL")
			} else {
				fmt.Print(val)
			}
		}
		fmt.Println()
	}
	if err := rows.Err(); err != nil {
		log.Fatalf("rows: %v", err)
	}
}
