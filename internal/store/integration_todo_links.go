package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const maxTodoIntegrationResources = 50

func (s *Store) integrationTodoTx(ctx context.Context, tx *sql.Tx, actorID, projectID, localID int64, mutate bool) (int64, error) {
	if localID <= 0 {
		return 0, fmt.Errorf("%w: invalid todo local ID", ErrValidation)
	}
	if err := s.checkIntegrationProjectTx(ctx, tx, actorID, projectID, false); err != nil {
		return 0, err
	}
	if mutate {
		role, err := s.getProjectRoleTx(ctx, tx, projectID, actorID)
		if err != nil {
			return 0, err
		}
		if !role.HasMinimumRole(RoleContributor) {
			return 0, ErrForbidden
		}
	}
	var todoID int64
	if err := tx.QueryRowContext(ctx, "SELECT id FROM todos WHERE project_id = ? AND local_id = ?", projectID, localID).Scan(&todoID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return todoID, nil
}

func (s *Store) ListTodoIntegrationResources(ctx context.Context, actorID, projectID, localID int64) ([]IntegrationResource, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	todoID, err := s.integrationTodoTx(ctx, tx, actorID, projectID, localID, false)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT r.id, r.connection_id, r.kind, r.external_id, r.name, r.url, r.availability, r.created_at, r.updated_at FROM integration_resources r JOIN integration_todo_resources tr ON tr.resource_id = r.id WHERE tr.project_id = ? AND tr.todo_id = ? ORDER BY r.id", projectID, todoID)
	if err != nil {
		return nil, err
	}
	resources := make([]IntegrationResource, 0)
	for rows.Next() {
		resource, scanErr := scanIntegrationResource(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		resources = append(resources, resource)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return resources, nil
}

func (s *Store) LinkTodoIntegrationResource(ctx context.Context, actorID, projectID, localID, resourceID int64) error {
	if resourceID <= 0 {
		return fmt.Errorf("%w: invalid resource ID", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	todoID, err := s.integrationTodoTx(ctx, tx, actorID, projectID, localID, true)
	if err != nil {
		return err
	}
	var association int64
	if err := tx.QueryRowContext(ctx, "SELECT pr.resource_id FROM integration_project_resources pr JOIN integration_resources r ON r.id=pr.resource_id AND r.availability='available' WHERE pr.project_id = ? AND pr.resource_id = ?", projectID, resourceID).Scan(&association); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM integration_todo_resources WHERE todo_id = ?", todoID).Scan(&count); err != nil {
		return err
	}
	var alreadyLinked int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM integration_todo_resources WHERE todo_id = ? AND resource_id = ?", todoID, resourceID).Scan(&alreadyLinked); err != nil {
		return err
	}
	if alreadyLinked == 0 && count >= maxTodoIntegrationResources {
		return fmt.Errorf("%w: too many external resources for todo", ErrValidation)
	}
	_, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO integration_todo_resources(project_id, todo_id, resource_id, created_at) VALUES (?, ?, ?, ?)", projectID, todoID, resourceID, time.Now().UTC().UnixMilli())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UnlinkTodoIntegrationResource(ctx context.Context, actorID, projectID, localID, resourceID int64) error {
	if resourceID <= 0 {
		return fmt.Errorf("%w: invalid resource ID", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	todoID, err := s.integrationTodoTx(ctx, tx, actorID, projectID, localID, true)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM integration_todo_resources WHERE project_id = ? AND todo_id = ? AND resource_id = ?", projectID, todoID, resourceID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}
