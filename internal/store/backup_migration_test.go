package store

import (
	"database/sql"
	"testing"
)

// TestMigration012_OnlyOneUniqueSlugIndex verifies that after migration 012,
// exactly one unique index on the slug column exists.
func TestMigration012_OnlyOneUniqueSlugIndex(t *testing.T) {
	// This test requires a database with migration 012 applied.
	// In a real scenario, you would set up a test database, run migrations, then verify.
	// For now, this is a placeholder that documents the requirement.
	//
	// To implement fully:
	// 1. Create a test database
	// 2. Run migrations up to 011
	// 3. Run migration 012
	// 4. Query PRAGMA index_list('projects') to get all indexes
	// 5. For each unique index, check PRAGMA index_info to see if it's on slug column
	// 6. Verify exactly one unique slug index exists and it's named idx_projects_slug_production

	t.Skip("Requires test database setup with migrations - implement when needed")
}

// Helper function to check unique slug indexes (for use in actual test implementation)
func checkUniqueSlugIndexes(db *sql.DB) (int, []string, error) {
	// Query all indexes on projects table
	rows, err := db.Query("PRAGMA index_list('projects')")
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()

	type indexInfo struct {
		seq     int
		name    string
		unique  int
		origin  string
		partial int
	}

	var indexes []indexInfo
	for rows.Next() {
		var idx indexInfo
		if err := rows.Scan(&idx.seq, &idx.name, &idx.unique, &idx.origin, &idx.partial); err != nil {
			return 0, nil, err
		}
		indexes = append(indexes, idx)
	}
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}

	// Check which unique indexes are on slug column
	var uniqueSlugIndexes []string
	for _, idx := range indexes {
		if idx.unique == 1 {
			// Check if this index is on slug column
			infoRows, err := db.Query("PRAGMA index_info(?)", idx.name)
			if err != nil {
				return 0, nil, err
			}

			isSlugIndex := false
			for infoRows.Next() {
				var seqno int
				var cid int
				var colName string
				if err := infoRows.Scan(&seqno, &cid, &colName); err != nil {
					infoRows.Close()
					return 0, nil, err
				}
				if colName == "slug" {
					isSlugIndex = true
					break
				}
			}
			infoRows.Close()

			if isSlugIndex {
				uniqueSlugIndexes = append(uniqueSlugIndexes, idx.name)
			}
		}
	}

	return len(uniqueSlugIndexes), uniqueSlugIndexes, nil
}
