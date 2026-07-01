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
	// Check total tags
	var totalTags int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags`).Scan(&totalTags); err != nil {
		return fmt.Errorf("count tags: %w", err)
	}
	fmt.Fprintf(out, "Total tags in database: %d\n\n", totalTags)

	// Check tags by scope
	rows, err := sqlDB.QueryContext(ctx, `SELECT scope, COUNT(*) as count FROM tags GROUP BY scope`)
	if err != nil {
		return fmt.Errorf("query scope: %w", err)
	}
	fmt.Fprintln(out, "Tags by scope:")
	for rows.Next() {
		var scope sql.NullString
		var count int
		if err := rows.Scan(&scope, &count); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan: %w", err)
		}
		if scope.Valid {
			fmt.Fprintf(out, "  scope='%s': %d tags\n", scope.String, count)
		} else {
			fmt.Fprintf(out, "  scope=NULL: %d tags\n", count)
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close scope rows: %w", err)
	}
	fmt.Fprintln(out)

	// Check GLOBAL tags
	var globalTags int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE scope = 'GLOBAL' AND project_id IS NULL`).Scan(&globalTags); err != nil {
		return fmt.Errorf("count global: %w", err)
	}
	fmt.Fprintf(out, "GLOBAL tags (scope='GLOBAL' AND project_id IS NULL): %d\n\n", globalTags)

	// Check todo_tags relationships
	var totalTodoTags int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM todo_tags`).Scan(&totalTodoTags); err != nil {
		return fmt.Errorf("count todo_tags: %w", err)
	}
	fmt.Fprintf(out, "Total todo_tags relationships: %d\n", totalTodoTags)

	// Check orphaned todo_tags
	var orphaned int
	if err := sqlDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM todo_tags tt 
		LEFT JOIN tags t ON t.id = tt.tag_id 
		WHERE t.id IS NULL
	`).Scan(&orphaned); err != nil {
		return fmt.Errorf("count orphaned: %w", err)
	}
	fmt.Fprintf(out, "Orphaned todo_tags (referencing non-existent tags): %d\n\n", orphaned)

	// Check tags with todo relationships
	var tagsWithTodos int
	if err := sqlDB.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT t.id) 
		FROM tags t 
		INNER JOIN todo_tags tt ON t.id = tt.tag_id
	`).Scan(&tagsWithTodos); err != nil {
		return fmt.Errorf("count tags with todos: %w", err)
	}
	fmt.Fprintf(out, "Tags that are actually used by todos: %d\n\n", tagsWithTodos)

	// Show some sample tags
	fmt.Fprintln(out, "\nSample tags (first 10):")
	rows, err = sqlDB.QueryContext(ctx, `
		SELECT t.id, t.name, t.scope, t.project_id, COUNT(tt.todo_id) as todo_count 
		FROM tags t 
		LEFT JOIN todo_tags tt ON t.id = tt.tag_id 
		GROUP BY t.id 
		ORDER BY t.id 
		LIMIT 10
	`)
	if err != nil {
		return fmt.Errorf("query sample: %w", err)
	}
	for rows.Next() {
		var id int64
		var name string
		var scope sql.NullString
		var projectID sql.NullInt64
		var todoCount int
		if err := rows.Scan(&id, &name, &scope, &projectID, &todoCount); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan sample: %w", err)
		}
		scopeStr := "NULL"
		if scope.Valid {
			scopeStr = scope.String
		}
		projStr := "NULL"
		if projectID.Valid {
			projStr = fmt.Sprintf("%d", projectID.Int64)
		}
		fmt.Fprintf(out, "  id=%d name='%s' scope=%s project_id=%s todo_count=%d\n", id, name, scopeStr, projStr, todoCount)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close sample rows: %w", err)
	}

	// Check if there are tags that should be GLOBAL but aren't
	var wrongScope int
	if err := sqlDB.QueryRowContext(ctx, `
		SELECT COUNT(*) 
		FROM tags 
		WHERE (scope IS NULL OR scope != 'GLOBAL' OR project_id IS NOT NULL)
		AND id IN (SELECT DISTINCT tag_id FROM todo_tags)
	`).Scan(&wrongScope); err != nil {
		return fmt.Errorf("count wrong scope: %w", err)
	}
	if wrongScope > 0 {
		fmt.Fprintf(out, "\n⚠️  WARNING: %d tags used by todos have wrong scope (not GLOBAL with project_id=NULL)\n", wrongScope)
		fmt.Fprintln(out, "   These tags exist but won't show up in full mode queries!")
	}
	return nil
}
