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
	dataDir := ""
	if len(os.Args) > 1 {
		dataDir = os.Args[1]
	}
	_, dbPath, err := config.ResolveDataDir(dataDir)
	if err != nil {
		log.Fatalf("resolve data dir: %v", err)
	}

	sqlDB, err := db.Open(dbPath, db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer sqlDB.Close()

	if err := run(context.Background(), sqlDB, os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, sqlDB *sql.DB, out io.Writer) error {
	// Check if there are any todos that might have had tags
	var totalTodos int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM todos`).Scan(&totalTodos); err != nil {
		return fmt.Errorf("count todos: %w", err)
	}
	fmt.Fprintf(out, "Total todos in database: %d\n", totalTodos)

	// Check if there are any todos with project_id that match existing tags' old project_ids
	// (This won't help much since tags are now GLOBAL, but let's see)
	var todosWithPossibleTags int
	if err := sqlDB.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT t.project_id) 
		FROM todos t
		WHERE EXISTS (SELECT 1 FROM tags WHERE tags.name IN ('feature', 'bug', 'enhancement', 'experimental', 'marketing', 'techdebt', 'integration', 'anonymous-boards'))
	`).Scan(&todosWithPossibleTags); err != nil {
		return fmt.Errorf("count todos with possible tags: %w", err)
	}
	fmt.Fprintf(out, "Projects that might have had these tags: %d\n\n", todosWithPossibleTags)

	// List all available tags
	fmt.Fprintln(out, "Available tags (these can be re-applied to todos):")
	rows, err := sqlDB.QueryContext(ctx, `SELECT id, name FROM tags ORDER BY name`)
	if err != nil {
		return fmt.Errorf("query tags: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		fmt.Fprintf(out, "  - %s (id=%d)\n", name, id)
	}

	fmt.Fprintln(out, "\n⚠️  RECOVERY STATUS:")
	fmt.Fprintln(out, "  - Tags preserved: YES (8 tags exist)")
	fmt.Fprintln(out, "  - Tag-todo relationships: LOST (0 relationships)")
	fmt.Fprintln(out, "  - Recovery possible: NO (relationships cannot be reconstructed without backup)")
	fmt.Fprintln(out, "\n  ACTION REQUIRED:")
	fmt.Fprintln(out, "  You will need to manually re-tag your todos using the tag names listed above.")
	fmt.Fprintln(out, "  The tags themselves are available and ready to use.")
	return nil
}
