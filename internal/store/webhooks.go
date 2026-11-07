package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"scrumboy/internal/safehttp"
)

const (
	maxWebhooksPerProject        = 20
	MaxWebhookManagementPageSize = 100
)

type Webhook struct {
	ID                       int64     `json:"id"`
	UserID                   int64     `json:"userId"`
	OwnerPublicID            string    `json:"ownerPublicId,omitempty"`
	OwnerName                string    `json:"ownerName,omitempty"`
	OwnerIsService           bool      `json:"ownerIsService"`
	ProjectID                int64     `json:"projectId"`
	ProjectSlug              string    `json:"projectSlug,omitempty"`
	RegisteredByUserID       *int64    `json:"registeredByUserId,omitempty"`
	RegisteredByUserPublicID *string   `json:"registeredByUserPublicId,omitempty"`
	RegisteredByUserName     *string   `json:"registeredByUserName,omitempty"`
	URL                      string    `json:"url"`
	DestinationPreview       string    `json:"destinationPreview,omitempty"`
	DestinationRedacted      bool      `json:"destinationRedacted,omitempty"`
	Events                   []string  `json:"events"`
	CreatedAt                time.Time `json:"createdAt"`
	// Secret is never returned in list/get responses.
}

type CreateWebhookInput struct {
	ProjectID          int64
	URL                string
	Events             []string
	Secret             *string
	ServiceOwnerUserID *int64
}

func (s *Store) CreateWebhook(ctx context.Context, actorUserID int64, in CreateWebhookInput) (Webhook, error) {
	if actorUserID <= 0 {
		return Webhook{}, fmt.Errorf("%w: invalid user id", ErrValidation)
	}
	if in.ProjectID <= 0 {
		return Webhook{}, fmt.Errorf("%w: invalid project id", ErrValidation)
	}
	u, err := parseWebhookURL(in.URL)
	if err != nil {
		return Webhook{}, err
	}
	if len(in.Events) == 0 || len(in.Events) > 50 {
		return Webhook{}, fmt.Errorf("%w: events must contain 1-50 entries", ErrValidation)
	}
	for _, e := range in.Events {
		if e == "" || len(e) > 100 {
			return Webhook{}, fmt.Errorf("%w: invalid event type", ErrValidation)
		}
	}

	eventsJSON, err := json.Marshal(in.Events)
	if err != nil {
		return Webhook{}, fmt.Errorf("marshal events: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return Webhook{}, fmt.Errorf("begin create webhook: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	project, err := getProjectTx(ctx, tx, in.ProjectID, s)
	if err != nil {
		return Webhook{}, err
	}
	if err := rejectIfExpiredTemporaryProject(project); err != nil {
		return Webhook{}, err
	}
	actor, actorRole, err := webhookProjectActorTx(ctx, tx, in.ProjectID, actorUserID)
	if err != nil {
		return Webhook{}, err
	}
	if !actorRole.HasMinimumRole(RoleMaintainer) {
		return Webhook{}, ErrForbidden
	}

	owner := actor
	if in.ServiceOwnerUserID != nil {
		if *in.ServiceOwnerUserID <= 0 {
			return Webhook{}, fmt.Errorf("%w: invalid service owner user id", ErrValidation)
		}
		owner, err = webhookUserTx(ctx, tx, *in.ServiceOwnerUserID)
		if err != nil {
			return Webhook{}, err
		}
		if !owner.IsService {
			return Webhook{}, ErrForbidden
		}
		ownerRole, roleErr := webhookExplicitProjectRoleTx(ctx, tx, in.ProjectID, owner.ID)
		if roleErr != nil {
			if errors.Is(roleErr, ErrNotFound) {
				return Webhook{}, ErrForbidden
			}
			return Webhook{}, roleErr
		}
		if !ownerRole.HasMinimumRole(RoleViewer) {
			return Webhook{}, ErrForbidden
		}
	}

	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhooks WHERE project_id = ?`, in.ProjectID).Scan(&count); err != nil {
		return Webhook{}, fmt.Errorf("count webhooks: %w", err)
	}
	if count >= maxWebhooksPerProject {
		return Webhook{}, fmt.Errorf("%w: maximum webhooks per project reached", ErrValidation)
	}

	nowMs := time.Now().UTC().UnixMilli()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO webhooks(user_id, project_id, url, events_json, secret, created_at, registered_by_user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, owner.ID, in.ProjectID, u.String(), string(eventsJSON), in.Secret, nowMs, actorUserID)
	if err != nil {
		return Webhook{}, fmt.Errorf("insert webhook: %w", err)
	}
	id, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return Webhook{}, fmt.Errorf("commit create webhook: %w", err)
	}
	registeredByID := actorUserID
	registeredByPublicID := actor.PublicID
	registeredByName := actor.Name
	return Webhook{
		ID:                       id,
		UserID:                   owner.ID,
		OwnerPublicID:            owner.PublicID,
		OwnerName:                owner.Name,
		OwnerIsService:           owner.IsService,
		ProjectID:                in.ProjectID,
		ProjectSlug:              project.Slug,
		RegisteredByUserID:       &registeredByID,
		RegisteredByUserPublicID: &registeredByPublicID,
		RegisteredByUserName:     &registeredByName,
		URL:                      u.String(),
		Events:                   in.Events,
		CreatedAt:                time.UnixMilli(nowMs).UTC(),
	}, nil
}

func webhookUserTx(ctx context.Context, tx *sql.Tx, userID int64) (User, error) {
	var user User
	if err := tx.QueryRowContext(ctx, `
		SELECT id, COALESCE(public_id, ''), name, is_service <> 0
		FROM users
		WHERE id = ?
	`, userID).Scan(&user.ID, &user.PublicID, &user.Name, &user.IsService); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("get webhook user: %w", err)
	}
	return user, nil
}

func webhookExplicitProjectRoleTx(ctx context.Context, tx *sql.Tx, projectID, userID int64) (ProjectRole, error) {
	var role ProjectRole
	if err := tx.QueryRowContext(ctx, `
		SELECT role
		FROM project_members
		WHERE project_id = ? AND user_id = ?
	`, projectID, userID).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("get webhook project role: %w", err)
	}
	return role, nil
}

func webhookProjectActorTx(ctx context.Context, tx *sql.Tx, projectID, actorUserID int64) (User, ProjectRole, error) {
	actor, err := webhookUserTx(ctx, tx, actorUserID)
	if err != nil {
		return User{}, "", err
	}
	role, err := webhookExplicitProjectRoleTx(ctx, tx, projectID, actorUserID)
	if err != nil {
		return User{}, "", err
	}
	return actor, role, nil
}

func (s *Store) ListWebhooks(ctx context.Context, userID int64) ([]Webhook, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, project_id, url, events_json, created_at
		FROM webhooks
		WHERE user_id = ?
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list webhooks: %w", err)
	}
	defer rows.Close()

	var out []Webhook
	for rows.Next() {
		var w Webhook
		var eventsJSON string
		var createdMs int64
		if err := rows.Scan(&w.ID, &w.UserID, &w.ProjectID, &w.URL, &eventsJSON, &createdMs); err != nil {
			return nil, fmt.Errorf("scan webhook: %w", err)
		}
		_ = json.Unmarshal([]byte(eventsJSON), &w.Events)
		w.CreatedAt = time.UnixMilli(createdMs).UTC()
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) ListProjectWebhooks(ctx context.Context, actorUserID, projectID int64, limit int, beforeID int64) ([]Webhook, *int64, error) {
	if actorUserID <= 0 || projectID <= 0 {
		return nil, nil, fmt.Errorf("%w: invalid webhook listing identity", ErrValidation)
	}
	if limit < 1 || limit > MaxWebhookManagementPageSize || beforeID < 0 {
		return nil, nil, fmt.Errorf("%w: invalid webhook pagination", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, fmt.Errorf("begin list project webhooks: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := getProjectTx(ctx, tx, projectID, s); err != nil {
		return nil, nil, err
	}
	if _, err := webhookUserTx(ctx, tx, actorUserID); err != nil {
		return nil, nil, err
	}
	visibility := "w.user_id = ?"
	actorRole, err := webhookExplicitProjectRoleTx(ctx, tx, projectID, actorUserID)
	if err == nil && actorRole.HasMinimumRole(RoleMaintainer) {
		visibility = "1 = 1"
	} else {
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, nil, err
		}
		if err == nil && !actorRole.HasMinimumRole(RoleViewer) {
			return nil, nil, ErrNotFound
		}
		var ownsSubscription int
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM webhooks WHERE project_id = ? AND user_id = ?
			)
		`, projectID, actorUserID).Scan(&ownsSubscription); err != nil {
			return nil, nil, fmt.Errorf("check webhook ownership: %w", err)
		}
		if errors.Is(err, ErrNotFound) && ownsSubscription == 0 {
			return nil, nil, ErrNotFound
		}
	}
	query := `
		SELECT w.id, w.user_id, owner.public_id, owner.name, owner.is_service <> 0,
		       w.project_id, p.slug, w.registered_by_user_id,
		       registrar.public_id, registrar.name, w.url, w.events_json, w.created_at
		FROM webhooks w
		JOIN users owner ON owner.id = w.user_id
		JOIN projects p ON p.id = w.project_id
		LEFT JOIN users registrar ON registrar.id = w.registered_by_user_id
		WHERE w.project_id = ? AND (` + visibility + `) AND (? = 0 OR w.id < ?)
		ORDER BY w.id DESC
		LIMIT ?`
	args := []any{projectID}
	if visibility != "1 = 1" {
		args = append(args, actorUserID)
	}
	args = append(args, beforeID, beforeID, limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list project webhooks: %w", err)
	}
	defer rows.Close()
	items := make([]Webhook, 0, limit+1)
	for rows.Next() {
		var item Webhook
		var eventsJSON string
		var createdAtMs int64
		var registeredByID sql.NullInt64
		var registeredByPublicID, registeredByName sql.NullString
		if err := rows.Scan(
			&item.ID, &item.UserID, &item.OwnerPublicID, &item.OwnerName, &item.OwnerIsService,
			&item.ProjectID, &item.ProjectSlug, &registeredByID,
			&registeredByPublicID, &registeredByName, &item.URL, &eventsJSON, &createdAtMs,
		); err != nil {
			return nil, nil, fmt.Errorf("scan project webhook: %w", err)
		}
		if err := json.Unmarshal([]byte(eventsJSON), &item.Events); err != nil {
			return nil, nil, fmt.Errorf("decode webhook events: %w", err)
		}
		if registeredByID.Valid {
			value := registeredByID.Int64
			item.RegisteredByUserID = &value
		}
		if registeredByPublicID.Valid {
			value := registeredByPublicID.String
			item.RegisteredByUserPublicID = &value
		}
		if registeredByName.Valid {
			value := registeredByName.String
			item.RegisteredByUserName = &value
		}
		if item.UserID != actorUserID {
			item.DestinationPreview = redactedWebhookDestination(item.URL)
			item.DestinationRedacted = true
			item.URL = ""
		}
		item.CreatedAt = time.UnixMilli(createdAtMs).UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate project webhooks: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, fmt.Errorf("close project webhooks: %w", err)
	}
	var nextBeforeID *int64
	if len(items) > limit {
		items = items[:limit]
		value := items[len(items)-1].ID
		nextBeforeID = &value
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit list project webhooks: %w", err)
	}
	return items, nextBeforeID, nil
}

// ListWebhooksByProject returns subscriptions whose human owner is a current
// Maintainer or whose service owner is a current explicit project member.
func (s *Store) ListWebhooksByProject(ctx context.Context, projectID int64) ([]WebhookRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT w.id, w.url, w.events_json, w.secret
		FROM webhooks w
		JOIN users u ON u.id = w.user_id
		JOIN projects p ON p.id = w.project_id
		JOIN project_members pm ON pm.project_id = w.project_id AND pm.user_id = w.user_id
		WHERE w.project_id = ? AND (
			(u.is_service = 0 AND pm.role IN (?, ?)) OR
			(u.is_service = 1 AND pm.role IN (?, ?, ?))
		)
	`, projectID, RoleMaintainer, RoleOwner, RoleViewer, RoleContributor, RoleMaintainer)
	if err != nil {
		return nil, fmt.Errorf("list webhooks by project: %w", err)
	}
	defer rows.Close()

	var out []WebhookRow
	for rows.Next() {
		var w WebhookRow
		var eventsJSON string
		if err := rows.Scan(&w.ID, &w.URL, &eventsJSON, &w.Secret); err != nil {
			return nil, fmt.Errorf("scan webhook row: %w", err)
		}
		_ = json.Unmarshal([]byte(eventsJSON), &w.Events)
		out = append(out, w)
	}
	return out, rows.Err()
}

type WebhookRow struct {
	ID     int64
	URL    string
	Events []string
	Secret *string
}

// WebhookDeliveryTarget is the authoritative destination and signing material
// for a queued delivery. It is intentionally resolved again by the worker so a
// queue entry cannot outlive the subscription or its owner's eligible membership.
type WebhookDeliveryTarget struct {
	URL    string
	Secret *string
}

// GetWebhookDeliveryTarget returns eligible=false unless the subscription,
// owner account, project, and eligible project membership all still exist. Database
// failures are returned separately so callers can fail closed without treating
// a temporary lookup failure as authorization.
func (s *Store) GetWebhookDeliveryTarget(ctx context.Context, webhookID int64) (WebhookDeliveryTarget, bool, error) {
	var target WebhookDeliveryTarget
	err := s.db.QueryRowContext(ctx, `
		SELECT w.url, w.secret
		FROM webhooks w
		JOIN users u ON u.id = w.user_id
		JOIN projects p ON p.id = w.project_id
		JOIN project_members pm ON pm.project_id = w.project_id AND pm.user_id = w.user_id
		WHERE w.id = ? AND (
			(u.is_service = 0 AND pm.role IN (?, ?)) OR
			(u.is_service = 1 AND pm.role IN (?, ?, ?))
		)
	`, webhookID, RoleMaintainer, RoleOwner, RoleViewer, RoleContributor, RoleMaintainer).Scan(&target.URL, &target.Secret)
	if errors.Is(err, sql.ErrNoRows) {
		return WebhookDeliveryTarget{}, false, nil
	}
	if err != nil {
		return WebhookDeliveryTarget{}, false, fmt.Errorf("authorize webhook delivery: %w", err)
	}
	return target, true, nil
}

func (s *Store) DeleteWebhook(ctx context.Context, userID, webhookID int64) error {
	if userID <= 0 || webhookID <= 0 {
		return fmt.Errorf("%w: invalid webhook deletion identity", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin owner webhook deletion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := webhookUserTx(ctx, tx, userID); err != nil {
		return err
	}
	target, err := webhookDeletionTargetTx(ctx, tx, webhookID)
	if err != nil {
		return err
	}
	if target.OwnerUserID != userID {
		return ErrNotFound
	}
	if err := deleteWebhookRowTx(ctx, tx, webhookID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit owner webhook deletion: %w", err)
	}
	return nil
}

func (s *Store) DeleteProjectWebhook(ctx context.Context, actorUserID, projectID, webhookID int64) error {
	if actorUserID <= 0 || projectID <= 0 || webhookID <= 0 {
		return fmt.Errorf("%w: invalid managed webhook deletion identity", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin managed webhook deletion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := webhookUserTx(ctx, tx, actorUserID); err != nil {
		return err
	}
	target, err := webhookDeletionTargetTx(ctx, tx, webhookID)
	if err != nil {
		return err
	}
	if target.ProjectID != projectID {
		return ErrNotFound
	}
	if actorUserID != target.OwnerUserID {
		actorRole, roleErr := webhookExplicitProjectRoleTx(ctx, tx, projectID, actorUserID)
		if roleErr != nil {
			return roleErr
		}
		if !actorRole.HasMinimumRole(RoleViewer) {
			return ErrNotFound
		}
		if !target.OwnerIsService || !actorRole.HasMinimumRole(RoleMaintainer) {
			return ErrForbidden
		}
	}
	if err := deleteWebhookRowTx(ctx, tx, webhookID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit managed webhook deletion: %w", err)
	}
	return nil
}

type webhookDeletionTarget struct {
	ProjectID      int64
	OwnerUserID    int64
	OwnerIsService bool
}

func webhookDeletionTargetTx(ctx context.Context, tx *sql.Tx, webhookID int64) (webhookDeletionTarget, error) {
	var target webhookDeletionTarget
	if err := tx.QueryRowContext(ctx, `
		SELECT w.project_id, w.user_id, u.is_service <> 0
		FROM webhooks w
		JOIN users u ON u.id = w.user_id
		WHERE w.id = ?
	`, webhookID).Scan(&target.ProjectID, &target.OwnerUserID, &target.OwnerIsService); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return webhookDeletionTarget{}, ErrNotFound
		}
		return webhookDeletionTarget{}, fmt.Errorf("get webhook for deletion: %w", err)
	}
	return target, nil
}

func deleteWebhookRowTx(ctx context.Context, tx *sql.Tx, webhookID int64) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM webhooks WHERE id = ?`, webhookID)
	if err != nil {
		return fmt.Errorf("delete webhook: %w", err)
	}
	if affected, err := res.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return fmt.Errorf("read deleted webhook count: %w", err)
		}
		return ErrNotFound
	}
	return nil
}

func redactedWebhookDestination(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "<redacted>"
	}
	return u.Scheme + "://" + u.Host + "/<redacted>"
}

// parseWebhookURL is fail-fast syntax/literal-IP checking. Delivery still
// resolves hostnames and refuses forbidden addresses at dial time.
func parseWebhookURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("%w: invalid webhook url", ErrValidation)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%w: url scheme must be http or https", ErrValidation)
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" {
		return nil, fmt.Errorf("%w: webhook destination is not allowed", ErrValidation)
	}
	if ip := net.ParseIP(host); ip != nil && safehttp.IsForbiddenIP(ip) {
		return nil, fmt.Errorf("%w: webhook destination is not allowed", ErrValidation)
	}
	return u, nil
}
