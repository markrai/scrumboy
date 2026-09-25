package store

import (
	"context"
	"fmt"
	"time"
)

// CountCompletedTodosForProject counts the complete current-project set using
// authoritative completion timestamps and the project's current done-lane
// semantics. It never depends on the paginated board projection.
func (s *Store) CountCompletedTodosForProject(
	ctx context.Context,
	projectID int64,
	start time.Time,
	end time.Time,
) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM todos t
JOIN project_workflow_columns done_wc
  ON done_wc.project_id = t.project_id
 AND done_wc.key = t.column_key
 AND done_wc.is_done = 1
WHERE t.project_id = ?
  AND t.done_at >= ?
  AND t.done_at < ?
`, projectID, start.UnixMilli(), end.UnixMilli()).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count project completed todos: %w", err)
	}
	return count, nil
}
