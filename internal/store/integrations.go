package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type IntegrationConnection struct {
	ID                int64
	Provider          string
	ExternalAccountID string
	Name              string
	Status            string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type CreateIntegrationConnectionInput struct {
	Provider          string
	ExternalAccountID string
	Name              string
}

type IntegrationResource struct {
	ID           int64
	ConnectionID int64
	Kind         string
	ExternalID   string
	Name         string
	URL          string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type RegisterIntegrationResourceInput struct {
	Kind       string
	ExternalID string
	Name       string
	URL        string
}

func validIntegrationKey(s string) bool {
	if len(s) == 0 || len(s) > 32 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

func validIntegrationValue(s string, max int) bool {
	if len(s) == 0 || len(s) > max || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

func validIntegrationURL(raw string) bool {
	if !validIntegrationValue(raw, 2048) {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

func scanIntegrationConnection(row interface{ Scan(...any) error }) (IntegrationConnection, error) {
	var c IntegrationConnection
	var created, updated int64
	err := row.Scan(&c.ID, &c.Provider, &c.ExternalAccountID, &c.Name, &c.Status, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	c.CreatedAt = time.UnixMilli(created).UTC()
	c.UpdatedAt = time.UnixMilli(updated).UTC()
	return c, nil
}

func scanIntegrationResource(row interface{ Scan(...any) error }) (IntegrationResource, error) {
	var r IntegrationResource
	var created, updated int64
	err := row.Scan(&r.ID, &r.ConnectionID, &r.Kind, &r.ExternalID, &r.Name, &r.URL, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	r.CreatedAt = time.UnixMilli(created).UTC()
	r.UpdatedAt = time.UnixMilli(updated).UTC()
	return r, nil
}

const integrationConnectionColumns = "id, provider, external_account_id, name, status, created_at, updated_at"
const integrationResourceColumns = "id, connection_id, kind, external_id, name, url, created_at, updated_at"

func (s *Store) CreateIntegrationConnection(ctx context.Context, actorID int64, in CreateIntegrationConnectionInput) (IntegrationConnection, error) {
	in.Provider = strings.ToLower(strings.TrimSpace(in.Provider))
	if !validIntegrationKey(in.Provider) || !validIntegrationValue(in.ExternalAccountID, 256) || !validIntegrationValue(in.Name, 200) {
		return IntegrationConnection{}, fmt.Errorf("%w: invalid integration connection", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IntegrationConnection{}, err
	}
	defer tx.Rollback()
	if err := requireAdminTx(ctx, tx, actorID); err != nil {
		return IntegrationConnection{}, err
	}
	now := time.Now().UTC().UnixMilli()
	result, err := tx.ExecContext(ctx, "INSERT INTO integration_connections(provider, external_account_id, name, status, created_at, updated_at) VALUES (?, ?, ?, 'active', ?, ?) ON CONFLICT(provider, external_account_id) DO NOTHING", in.Provider, in.ExternalAccountID, in.Name, now, now)
	if err != nil {
		return IntegrationConnection{}, fmt.Errorf("create integration connection: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		if err != nil {
			return IntegrationConnection{}, err
		}
		return IntegrationConnection{}, ErrConflict
	}
	id, err := result.LastInsertId()
	if err != nil {
		return IntegrationConnection{}, err
	}
	connection, err := scanIntegrationConnection(tx.QueryRowContext(ctx, "SELECT "+integrationConnectionColumns+" FROM integration_connections WHERE id = ?", id))
	if err != nil {
		return IntegrationConnection{}, err
	}
	return connection, tx.Commit()
}

func (s *Store) ListIntegrationConnections(ctx context.Context, actorID int64) ([]IntegrationConnection, error) {
	if err := s.requireAdmin(ctx, actorID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+integrationConnectionColumns+" FROM integration_connections ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]IntegrationConnection, 0)
	for rows.Next() {
		c, err := scanIntegrationConnection(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func (s *Store) UpdateIntegrationConnection(ctx context.Context, actorID, connectionID int64, name, status string) error {
	if connectionID <= 0 || !validIntegrationValue(name, 200) || (status != "active" && status != "disabled") {
		return fmt.Errorf("%w: invalid integration connection update", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireAdminTx(ctx, tx, actorID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "UPDATE integration_connections SET name = ?, status = ?, updated_at = ? WHERE id = ?", name, status, time.Now().UTC().UnixMilli(), connectionID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) DeleteIntegrationConnection(ctx context.Context, actorID, connectionID int64) error {
	if connectionID <= 0 {
		return fmt.Errorf("%w: invalid connection ID", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireAdminTx(ctx, tx, actorID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM integration_connections WHERE id = ?", connectionID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) RegisterIntegrationResource(ctx context.Context, actorID, connectionID int64, in RegisterIntegrationResourceInput) (IntegrationResource, error) {
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	if connectionID <= 0 || !validIntegrationKey(in.Kind) || !validIntegrationValue(in.ExternalID, 256) || !validIntegrationValue(in.Name, 200) || !validIntegrationURL(in.URL) {
		return IntegrationResource{}, fmt.Errorf("%w: invalid integration resource", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IntegrationResource{}, err
	}
	defer tx.Rollback()
	if err := requireAdminTx(ctx, tx, actorID); err != nil {
		return IntegrationResource{}, err
	}
	var exists int64
	if err := tx.QueryRowContext(ctx, "SELECT id FROM integration_connections WHERE id = ?", connectionID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IntegrationResource{}, ErrNotFound
		}
		return IntegrationResource{}, err
	}
	now := time.Now().UTC().UnixMilli()
	result, err := tx.ExecContext(ctx, "INSERT INTO integration_resources(connection_id, kind, external_id, name, url, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(connection_id, kind, external_id) DO NOTHING", connectionID, in.Kind, in.ExternalID, in.Name, in.URL, now, now)
	if err != nil {
		return IntegrationResource{}, err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		if err != nil {
			return IntegrationResource{}, err
		}
		return IntegrationResource{}, ErrConflict
	}
	id, err := result.LastInsertId()
	if err != nil {
		return IntegrationResource{}, err
	}
	resource, err := scanIntegrationResource(tx.QueryRowContext(ctx, "SELECT "+integrationResourceColumns+" FROM integration_resources WHERE id = ?", id))
	if err != nil {
		return IntegrationResource{}, err
	}
	return resource, tx.Commit()
}

func (s *Store) UpdateIntegrationResource(ctx context.Context, actorID, resourceID int64, name, rawURL string) error {
	if resourceID <= 0 || !validIntegrationValue(name, 200) || !validIntegrationURL(rawURL) {
		return fmt.Errorf("%w: invalid integration resource update", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireAdminTx(ctx, tx, actorID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "UPDATE integration_resources SET name = ?, url = ?, updated_at = ? WHERE id = ?", name, rawURL, time.Now().UTC().UnixMilli(), resourceID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) DeleteIntegrationResource(ctx context.Context, actorID, resourceID int64) error {
	if resourceID <= 0 {
		return fmt.Errorf("%w: invalid integration resource ID", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireAdminTx(ctx, tx, actorID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM integration_resources WHERE id = ?", resourceID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) ListIntegrationResources(ctx context.Context, actorID, connectionID int64) ([]IntegrationResource, error) {
	if err := s.requireAdmin(ctx, actorID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+integrationResourceColumns+" FROM integration_resources WHERE connection_id = ? ORDER BY id", connectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]IntegrationResource, 0)
	for rows.Next() {
		resource, err := scanIntegrationResource(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, resource)
	}
	return result, rows.Err()
}

func (s *Store) checkIntegrationProjectTx(ctx context.Context, tx *sql.Tx, actorID, projectID int64, manage bool) error {
	if projectID <= 0 || actorID <= 0 {
		return ErrNotFound
	}
	project, err := getProjectTx(ctx, tx, projectID, s)
	if err != nil {
		return err
	}
	if project.ExpiresAt != nil {
		return ErrNotFound
	}
	enabled, err := authEnabledTx(ctx, tx)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrUnauthorized
	}
	role, err := s.getProjectRoleTx(ctx, tx, projectID, actorID)
	if err != nil {
		return err
	}
	if role.HasMinimumRole(RoleMaintainer) || (!manage && role.HasMinimumRole(RoleViewer)) {
		return nil
	}
	if role.HasMinimumRole(RoleViewer) {
		return ErrForbidden
	}
	return ErrNotFound
}

func (s *Store) AttachIntegrationResource(ctx context.Context, actorID, projectID, resourceID int64) error {
	if resourceID <= 0 {
		return fmt.Errorf("%w: invalid integration resource ID", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.checkIntegrationProjectTx(ctx, tx, actorID, projectID, true); err != nil {
		return err
	}
	var exists int64
	if err := tx.QueryRowContext(ctx, "SELECT id FROM integration_resources WHERE id = ?", resourceID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO integration_project_resources(project_id, resource_id, created_at) VALUES (?, ?, ?)", projectID, resourceID, time.Now().UTC().UnixMilli())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DetachIntegrationResource(ctx context.Context, actorID, projectID, resourceID int64) error {
	if resourceID <= 0 {
		return fmt.Errorf("%w: invalid integration resource ID", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.checkIntegrationProjectTx(ctx, tx, actorID, projectID, true); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM integration_project_resources WHERE project_id = ? AND resource_id = ?", projectID, resourceID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) ListProjectIntegrationResources(ctx context.Context, actorID, projectID int64) ([]IntegrationResource, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := s.checkIntegrationProjectTx(ctx, tx, actorID, projectID, false); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT r."+strings.ReplaceAll(integrationResourceColumns, ", ", ", r.")+" FROM integration_resources r JOIN integration_project_resources pr ON pr.resource_id = r.id WHERE pr.project_id = ? ORDER BY r.id", projectID)
	if err != nil {
		return nil, err
	}
	result := make([]IntegrationResource, 0)
	for rows.Next() {
		resource, err := scanIntegrationResource(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, resource)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
