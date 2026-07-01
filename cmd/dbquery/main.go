package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
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
	if err := run(context.Background(), sqlDB, query, os.Stdout); err != nil {
		log.Fatalf("%v", err)
	}
}

func run(ctx context.Context, sqlDB *sql.DB, query string, out io.Writer) error {
	rows, err := sqlDB.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("columns: %w", err)
	}

	// Print header
	for i, col := range cols {
		if i > 0 {
			fmt.Fprint(out, " | ")
		}
		fmt.Fprint(out, col)
	}
	fmt.Fprintln(out)

	// Print rows
	values := make([]interface{}, len(cols))
	valuePtrs := make([]interface{}, len(cols))
	for i := range values {
		valuePtrs[i] = &values[i]
	}

	for rows.Next() {
		if err := rows.Scan(valuePtrs...); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		for i, val := range values {
			if i > 0 {
				fmt.Fprint(out, " | ")
			}
			if val == nil {
				fmt.Fprint(out, "NULL")
			} else {
				fmt.Fprint(out, val)
			}
		}
		fmt.Fprintln(out)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}
	return nil
}
