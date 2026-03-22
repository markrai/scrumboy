package main

import (
	"context"
	"database/sql"
	"fmt"
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

	ctx := context.Background()

	// Check total tags
	var totalTags int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags`).Scan(&totalTags); err != nil {
		log.Fatalf("count tags: %v", err)
	}
	fmt.Printf("Total tags in database: %d\n\n", totalTags)

	// Check tags by scope
	rows, err := sqlDB.QueryContext(ctx, `SELECT scope, COUNT(*) as count FROM tags GROUP BY scope`)
	if err != nil {
		log.Fatalf("query scope: %v", err)
	}
	defer rows.Close()
	fmt.Println("Tags by scope:")
	for rows.Next() {
		var scope sql.NullString
		var count int
		if err := rows.Scan(&scope, &count); err != nil {
			log.Fatalf("scan: %v", err)
		}
		if scope.Valid {
			fmt.Printf("  scope='%s': %d tags\n", scope.String, count)
		} else {
			fmt.Printf("  scope=NULL: %d tags\n", count)
		}
	}
	fmt.Println()

	// Check GLOBAL tags
	var globalTags int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE scope = 'GLOBAL' AND project_id IS NULL`).Scan(&globalTags); err != nil {
		log.Fatalf("count global: %v", err)
	}
	fmt.Printf("GLOBAL tags (scope='GLOBAL' AND project_id IS NULL): %d\n\n", globalTags)

	// Check todo_tags relationships
	var totalTodoTags int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM todo_tags`).Scan(&totalTodoTags); err != nil {
		log.Fatalf("count todo_tags: %v", err)
	}
	fmt.Printf("Total todo_tags relationships: %d\n", totalTodoTags)

	// Check orphaned todo_tags
	var orphaned int
	if err := sqlDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM todo_tags tt 
		LEFT JOIN tags t ON t.id = tt.tag_id 
		WHERE t.id IS NULL
	`).Scan(&orphaned); err != nil {
		log.Fatalf("count orphaned: %v", err)
	}
	fmt.Printf("Orphaned todo_tags (referencing non-existent tags): %d\n\n", orphaned)

	// Check tags with todo relationships
	var tagsWithTodos int
	if err := sqlDB.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT t.id) 
		FROM tags t 
		INNER JOIN todo_tags tt ON t.id = tt.tag_id
	`).Scan(&tagsWithTodos); err != nil {
		log.Fatalf("count tags with todos: %v", err)
	}
	fmt.Printf("Tags that are actually used by todos: %d\n\n", tagsWithTodos)

	// Show some sample tags
	fmt.Println("\nSample tags (first 10):")
	rows, err = sqlDB.QueryContext(ctx, `
		SELECT t.id, t.name, t.scope, t.project_id, COUNT(tt.todo_id) as todo_count 
		FROM tags t 
		LEFT JOIN todo_tags tt ON t.id = tt.tag_id 
		GROUP BY t.id 
		ORDER BY t.id 
		LIMIT 10
	`)
	if err != nil {
		log.Fatalf("query sample: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		var scope sql.NullString
		var projectID sql.NullInt64
		var todoCount int
		if err := rows.Scan(&id, &name, &scope, &projectID, &todoCount); err != nil {
			log.Fatalf("scan sample: %v", err)
		}
		scopeStr := "NULL"
		if scope.Valid {
			scopeStr = scope.String
		}
		projStr := "NULL"
		if projectID.Valid {
			projStr = fmt.Sprintf("%d", projectID.Int64)
		}
		fmt.Printf("  id=%d name='%s' scope=%s project_id=%s todo_count=%d\n", id, name, scopeStr, projStr, todoCount)
	}

	// Check if there are tags that should be GLOBAL but aren't
	var wrongScope int
	if err := sqlDB.QueryRowContext(ctx, `
		SELECT COUNT(*) 
		FROM tags 
		WHERE (scope IS NULL OR scope != 'GLOBAL' OR project_id IS NOT NULL)
		AND id IN (SELECT DISTINCT tag_id FROM todo_tags)
	`).Scan(&wrongScope); err != nil {
		log.Fatalf("count wrong scope: %v", err)
	}
	if wrongScope > 0 {
		fmt.Printf("\n⚠️  WARNING: %d tags used by todos have wrong scope (not GLOBAL with project_id=NULL)\n", wrongScope)
		fmt.Println("   These tags exist but won't show up in full mode queries!")
	}
}
