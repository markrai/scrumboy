package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"scrumboy/internal/safehttp"
)

const maxWebhooksPerProject = 20

type Webhook struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"userId"`
	ProjectID int64     `json:"projectId"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"`
	CreatedAt time.Time `json:"createdAt"`
	// Secret is never returned in list/get responses.
}

type CreateWebhookInput struct {
	ProjectID int64
	URL       string
	Events    []string
	Secret    *string
}

func (s *Store) CreateWebhook(ctx context.Context, userID int64, in CreateWebhookInput) (Webhook, error) {
	if userID <= 0 {
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

	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhooks WHERE project_id = ?`, in.ProjectID).Scan(&count); err != nil {
		return Webhook{}, fmt.Errorf("count webhooks: %w", err)
	}
	if count >= maxWebhooksPerProject {
		return Webhook{}, fmt.Errorf("%w: maximum webhooks per project reached", ErrValidation)
	}

	eventsJSON, err := json.Marshal(in.Events)
	if err != nil {
		return Webhook{}, fmt.Errorf("marshal events: %w", err)
	}

	nowMs := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO webhooks(user_id, project_id, url, events_json, secret, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, userID, in.ProjectID, u.String(), string(eventsJSON), in.Secret, nowMs)
	if err != nil {
		return Webhook{}, fmt.Errorf("insert webhook: %w", err)
	}
	id, _ := res.LastInsertId()
	return Webhook{
		ID:        id,
		UserID:    userID,
		ProjectID: in.ProjectID,
		URL:       u.String(),
		Events:    in.Events,
		CreatedAt: time.UnixMilli(nowMs).UTC(),
	}, nil
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

// ListWebhooksByProject returns all webhook subscriptions for a given project,
// used by the delivery pipeline to match events against subscriptions.
func (s *Store) ListWebhooksByProject(ctx context.Context, projectID int64) ([]WebhookRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, url, events_json, secret
		FROM webhooks
		WHERE project_id = ?
	`, projectID)
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

func (s *Store) DeleteWebhook(ctx context.Context, userID, webhookID int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM webhooks WHERE id = ? AND user_id = ?`, webhookID, userID)
	if err != nil {
		return fmt.Errorf("delete webhook: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
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
