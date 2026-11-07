package mcp

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"scrumboy/internal/store"
)

type webhookCreateInput struct {
	ProjectSlug          string   `json:"projectSlug"`
	URL                  string   `json:"url"`
	Events               []string `json:"events"`
	Secret               *string  `json:"secret"`
	ServiceAccountUserID *int64   `json:"serviceAccountUserId"`
}

type webhookListInput struct {
	ProjectSlug string  `json:"projectSlug"`
	Limit       *int    `json:"limit"`
	Cursor      *string `json:"cursor"`
}

type webhookDeleteInput struct {
	ProjectSlug string `json:"projectSlug"`
	WebhookID   int64  `json:"webhookId"`
}

type webhookManagementItem struct {
	WebhookID                int64     `json:"webhookId"`
	ProjectID                int64     `json:"projectId"`
	ProjectSlug              string    `json:"projectSlug"`
	OwnerUserID              int64     `json:"ownerUserId"`
	OwnerStableUserID        string    `json:"ownerStableUserId"`
	OwnerName                string    `json:"ownerName"`
	OwnerIsService           bool      `json:"ownerIsService"`
	RegisteredByUserID       *int64    `json:"registeredByUserId,omitempty"`
	RegisteredByStableUserID *string   `json:"registeredByStableUserId,omitempty"`
	RegisteredByName         *string   `json:"registeredByName,omitempty"`
	URL                      string    `json:"url"`
	Events                   []string  `json:"events"`
	CreatedAt                time.Time `json:"createdAt"`
}

func webhookToManagementItem(webhook store.Webhook) webhookManagementItem {
	return webhookManagementItem{
		WebhookID:                webhook.ID,
		ProjectID:                webhook.ProjectID,
		ProjectSlug:              webhook.ProjectSlug,
		OwnerUserID:              webhook.UserID,
		OwnerStableUserID:        webhook.OwnerPublicID,
		OwnerName:                webhook.OwnerName,
		OwnerIsService:           webhook.OwnerIsService,
		RegisteredByUserID:       webhook.RegisteredByUserID,
		RegisteredByStableUserID: webhook.RegisteredByUserPublicID,
		RegisteredByName:         webhook.RegisteredByUserName,
		URL:                      webhook.URL,
		Events:                   webhook.Events,
		CreatedAt:                webhook.CreatedAt,
	}
}

func (a *Adapter) webhookToolRequester(ctx context.Context, tool string) (int64, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return 0, err
	}
	if a.mode == "anonymous" {
		return 0, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, tool+" is unavailable in anonymous mode", nil)
	}
	if bootstrapAvailable {
		return 0, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, tool+" is unavailable before bootstrap", nil)
	}
	if !auth.Authenticated {
		return 0, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}
	requesterID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return 0, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}
	return requesterID, nil
}

func normalizeWebhookLimit(value *int) (int, *adapterError) {
	if value == nil {
		return 20, nil
	}
	if *value < 1 || *value > store.MaxWebhookManagementPageSize {
		return 0, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid limit", map[string]any{"field": "limit"})
	}
	return *value, nil
}

func parseWebhookCursor(cursor *string) (int64, *adapterError) {
	if cursor == nil || strings.TrimSpace(*cursor) == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(strings.TrimSpace(*cursor), 10, 64)
	if err != nil || value <= 0 {
		return 0, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid cursor", map[string]any{"field": "cursor"})
	}
	return value, nil
}

func (a *Adapter) webhookProject(ctx context.Context, projectSlug string) (store.Project, *adapterError) {
	if strings.TrimSpace(projectSlug) == "" {
		return store.Project{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	project, err := a.store.GetProjectBySlug(ctx, strings.TrimSpace(projectSlug))
	if err != nil {
		return store.Project{}, mapStoreError(err)
	}
	return project, nil
}

func (a *Adapter) handleWebhooksCreate(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	requesterID, requestErr := a.webhookToolRequester(ctx, "webhooks_create")
	if requestErr != nil {
		return nil, nil, requestErr
	}
	var in webhookCreateInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	project, projectErr := a.webhookProject(ctx, in.ProjectSlug)
	if projectErr != nil {
		return nil, nil, projectErr
	}
	webhook, err := a.store.CreateWebhook(ctx, requesterID, store.CreateWebhookInput{
		ProjectID:          project.ID,
		URL:                in.URL,
		Events:             in.Events,
		Secret:             in.Secret,
		ServiceOwnerUserID: in.ServiceAccountUserID,
	})
	if err != nil {
		return nil, nil, mapStoreError(err)
	}
	return map[string]any{"webhook": webhookToManagementItem(webhook)}, map[string]any{}, nil
}

func (a *Adapter) handleWebhooksList(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	requesterID, requestErr := a.webhookToolRequester(ctx, "webhooks_list")
	if requestErr != nil {
		return nil, nil, requestErr
	}
	var in webhookListInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	limit, limitErr := normalizeWebhookLimit(in.Limit)
	if limitErr != nil {
		return nil, nil, limitErr
	}
	beforeID, cursorErr := parseWebhookCursor(in.Cursor)
	if cursorErr != nil {
		return nil, nil, cursorErr
	}
	project, projectErr := a.webhookProject(ctx, in.ProjectSlug)
	if projectErr != nil {
		return nil, nil, projectErr
	}
	webhooks, nextBeforeID, err := a.store.ListProjectWebhooks(ctx, requesterID, project.ID, limit, beforeID)
	if err != nil {
		return nil, nil, mapStoreError(err)
	}
	items := make([]webhookManagementItem, 0, len(webhooks))
	for _, webhook := range webhooks {
		items = append(items, webhookToManagementItem(webhook))
	}
	var nextCursor *string
	if nextBeforeID != nil {
		value := strconv.FormatInt(*nextBeforeID, 10)
		nextCursor = &value
	}
	return map[string]any{"items": items}, map[string]any{"nextCursor": nextCursor, "hasMore": nextCursor != nil}, nil
}

func (a *Adapter) handleWebhooksDelete(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	requesterID, requestErr := a.webhookToolRequester(ctx, "webhooks_delete")
	if requestErr != nil {
		return nil, nil, requestErr
	}
	var in webhookDeleteInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.WebhookID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid webhookId", map[string]any{"field": "webhookId"})
	}
	project, projectErr := a.webhookProject(ctx, in.ProjectSlug)
	if projectErr != nil {
		return nil, nil, projectErr
	}
	if err := a.store.DeleteProjectWebhook(ctx, requesterID, project.ID, in.WebhookID); err != nil {
		return nil, nil, mapStoreError(err)
	}
	return map[string]any{"status": "deleted", "webhookId": in.WebhookID, "projectSlug": project.Slug}, map[string]any{}, nil
}
