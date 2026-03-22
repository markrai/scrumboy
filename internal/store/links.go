package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (s *Store) AddLink(ctx context.Context, projectID, fromLocalID, toLocalID int64, linkType string, mode Mode) error {
	if fromLocalID == toLocalID {
		return fmt.Errorf("%w: cannot link todo to itself", ErrValidation)
	}
	if linkType == "" {
		linkType = "relates_to"
	}
	if linkType != "relates_to" && linkType != "blocks" && linkType != "duplicates" && linkType != "parent" {
		return fmt.Errorf("%w: invalid link_type", ErrValidation)
	}

	// Explicitly verify read access for both endpoints before insert.
	if _, err := s.GetTodoByLocalID(ctx, projectID, fromLocalID, mode); err != nil {
		return err
	}
	if _, err := s.GetTodoByLocalID(ctx, projectID, toLocalID, mode); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin add link: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := s.getProjectForWriteTx(ctx, tx, projectID, mode); err != nil {
		return err
	}
	if _, err := getTodoIDByLocalIDTx(ctx, tx, projectID, fromLocalID); err != nil {
		return err
	}
	if _, err := getTodoIDByLocalIDTx(ctx, tx, projectID, toLocalID); err != nil {
		return err
	}

	nowMs := time.Now().UTC().UnixMilli()
	res, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO todo_links(project_id, from_local_id, to_local_id, link_type, created_at)
VALUES (?, ?, ?, ?, ?)`,
		projectID, fromLocalID, toLocalID, linkType, nowMs,
	)
	if err != nil {
		return fmt.Errorf("insert link: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		fromTodoID, _ := getTodoIDByLocalIDTx(ctx, tx, projectID, fromLocalID)
		toTodoID, _ := getTodoIDByLocalIDTx(ctx, tx, projectID, toLocalID)
		var actorUserID *int64
		if userID, ok := UserIDFromContext(ctx); ok {
			actorUserID = &userID
		}
		meta := map[string]any{"from_todo_id": fromTodoID, "to_todo_id": toTodoID, "from_local_id": fromLocalID, "to_local_id": toLocalID, "link_type": linkType}
		if err := insertAuditEventTx(ctx, tx, projectID, actorUserID, "link_added", "todo_link", nil, meta); err != nil {
			return fmt.Errorf("audit link_added: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit add link: %w", err)
	}
	return nil
}

func (s *Store) RemoveLink(ctx context.Context, projectID, fromLocalID, toLocalID int64, mode Mode) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin remove link: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := s.getProjectForWriteTx(ctx, tx, projectID, mode); err != nil {
		return err
	}
	var linkType string
	if err := tx.QueryRowContext(ctx, `SELECT link_type FROM todo_links WHERE project_id = ? AND from_local_id = ? AND to_local_id = ?`, projectID, fromLocalID, toLocalID).Scan(&linkType); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("get link: %w", err)
	}
	fromTodoID, _ := getTodoIDByLocalIDTx(ctx, tx, projectID, fromLocalID)
	toTodoID, _ := getTodoIDByLocalIDTx(ctx, tx, projectID, toLocalID)
	res, err := tx.ExecContext(ctx, `
DELETE FROM todo_links
WHERE project_id = ? AND from_local_id = ? AND to_local_id = ?`,
		projectID, fromLocalID, toLocalID,
	)
	if err != nil {
		return fmt.Errorf("delete link: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		var actorUserID *int64
		if userID, ok := UserIDFromContext(ctx); ok {
			actorUserID = &userID
		}
		meta := map[string]any{"from_todo_id": fromTodoID, "to_todo_id": toTodoID, "from_local_id": fromLocalID, "to_local_id": toLocalID, "link_type": linkType}
		if err := insertAuditEventTx(ctx, tx, projectID, actorUserID, "link_removed", "todo_link", nil, meta); err != nil {
			return fmt.Errorf("audit link_removed: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit remove link: %w", err)
	}
	return nil
}

func (s *Store) ListLinksForTodo(ctx context.Context, projectID, localID int64, mode Mode) ([]TodoLinkTarget, error) {
	if _, err := s.getProjectForRead(ctx, projectID, mode); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT t.local_id, t.title, l.link_type
FROM todo_links l
JOIN todos t ON t.project_id = l.project_id AND t.local_id = l.to_local_id
WHERE l.project_id = ? AND l.from_local_id = ?
ORDER BY t.local_id ASC`,
		projectID, localID,
	)
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}
	defer rows.Close()

	return scanTodoLinkTargets(rows)
}

func (s *Store) ListBacklinksForTodo(ctx context.Context, projectID, localID int64, mode Mode) ([]TodoLinkTarget, error) {
	if _, err := s.getProjectForRead(ctx, projectID, mode); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT t.local_id, t.title, l.link_type
FROM todo_links l
JOIN todos t ON t.project_id = l.project_id AND t.local_id = l.from_local_id
WHERE l.project_id = ? AND l.to_local_id = ?
ORDER BY t.local_id ASC`,
		projectID, localID,
	)
	if err != nil {
		return nil, fmt.Errorf("list backlinks: %w", err)
	}
	defer rows.Close()

	return scanTodoLinkTargets(rows)
}

func (s *Store) SearchTodosForLinkPicker(ctx context.Context, projectID int64, q string, limit int, excludeLocalIDs []int64, mode Mode) ([]TodoLinkTarget, error) {
	if _, err := s.getProjectForRead(ctx, projectID, mode); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	excluded := make(map[int64]struct{}, len(excludeLocalIDs))
	for _, id := range excludeLocalIDs {
		if id > 0 {
			excluded[id] = struct{}{}
		}
	}

	q = strings.TrimSpace(q)
	var (
		rows *sql.Rows
		err  error
	)

	// Empty q: recent first; idx_todos_project_updated supports this.
	if q == "" {
		rows, err = s.db.QueryContext(ctx, `
SELECT local_id, title
FROM todos
WHERE project_id = ?
ORDER BY updated_at DESC, local_id ASC
LIMIT ?`,
			projectID, limit,
		)
	} else if n, convErr := strconv.ParseInt(q, 10, 64); convErr == nil && n > 0 {
		rows, err = s.db.QueryContext(ctx, `
SELECT local_id, title
FROM todos
WHERE project_id = ? AND local_id = ?
LIMIT 1`,
			projectID, n,
		)
	} else {
		rows, err = s.db.QueryContext(ctx, `
SELECT local_id, title
FROM todos
WHERE project_id = ?
  AND LOWER(title) LIKE '%' || LOWER(?) || '%'
ORDER BY local_id ASC
LIMIT ?`,
			projectID, q, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("search todos for links: %w", err)
	}
	defer rows.Close()

	out := make([]TodoLinkTarget, 0, limit)
	for rows.Next() {
		var t TodoLinkTarget
		t.LinkType = "relates_to"
		if err := rows.Scan(&t.LocalID, &t.Title); err != nil {
			return nil, fmt.Errorf("scan link search result: %w", err)
		}
		if _, skip := excluded[t.LocalID]; skip {
			continue
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate link search results: %w", err)
	}
	return out, nil
}

func scanTodoLinkTargets(rows *sql.Rows) ([]TodoLinkTarget, error) {
	out := make([]TodoLinkTarget, 0, 8)
	for rows.Next() {
		var t TodoLinkTarget
		if err := rows.Scan(&t.LocalID, &t.Title, &t.LinkType); err != nil {
			return nil, fmt.Errorf("scan todo link target: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate todo link targets: %w", err)
	}
	return out, nil
}
