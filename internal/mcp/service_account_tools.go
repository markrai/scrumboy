package mcp

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"scrumboy/internal/store"
)

type serviceAccountItem struct {
	UserID       int64     `json:"userId"`
	StableUserID string    `json:"stableUserId"`
	Name         string    `json:"name"`
	IsService    bool      `json:"isService"`
	CreatedAt    time.Time `json:"createdAt"`
}

type serviceTokenItem struct {
	TokenID              int64      `json:"tokenId"`
	UserID               int64      `json:"userId"`
	StableUserID         string     `json:"stableUserId"`
	Name                 *string    `json:"name,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	LastUsedAt           *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt            *time.Time `json:"revokedAt,omitempty"`
	IssuedByUserID       *int64     `json:"issuedByUserId,omitempty"`
	IssuedByStableUserID *string    `json:"issuedByStableUserId,omitempty"`
}

type serviceTokenCreatedItem struct {
	TokenID   int64     `json:"tokenId"`
	UserID    int64     `json:"userId"`
	Name      *string   `json:"name,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	Token     string    `json:"token"`
}

type serviceAccountsListInput struct {
	Limit  *int    `json:"limit"`
	Cursor *string `json:"cursor"`
}

type serviceAccountUserInput struct {
	UserID int64 `json:"userId"`
}

type createServiceAccountInput struct {
	Name string `json:"name"`
}

type createServiceTokenInput struct {
	UserID int64   `json:"userId"`
	Name   *string `json:"name"`
}

type listServiceTokensInput struct {
	UserID int64   `json:"userId"`
	Limit  *int    `json:"limit"`
	Cursor *string `json:"cursor"`
}

type revokeServiceTokenInput struct {
	TokenID int64 `json:"tokenId"`
}

func serviceAccountToItem(account store.User) serviceAccountItem {
	return serviceAccountItem{
		UserID:       account.ID,
		StableUserID: account.PublicID,
		Name:         account.Name,
		IsService:    account.IsService,
		CreatedAt:    account.CreatedAt,
	}
}

func serviceTokenToItem(token store.APITokenMeta) serviceTokenItem {
	return serviceTokenItem{
		TokenID:              token.ID,
		UserID:               token.UserID,
		StableUserID:         token.UserPublicID,
		Name:                 token.Name,
		CreatedAt:            token.CreatedAt,
		LastUsedAt:           token.LastUsedAt,
		RevokedAt:            token.RevokedAt,
		IssuedByUserID:       token.CreatedByUserID,
		IssuedByStableUserID: token.CreatedByUserPublicID,
	}
}

func (a *Adapter) serviceAccountToolRequester(ctx context.Context, tool string) (int64, *adapterError) {
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

func normalizeServiceAdminLimit(value *int, maximum int) (int, *adapterError) {
	if value == nil {
		return 20, nil
	}
	if *value < 1 || *value > maximum {
		return 0, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid limit", map[string]any{"field": "limit"})
	}
	return *value, nil
}

func parseServiceTokenCursor(cursor *string) (int64, *adapterError) {
	if cursor == nil || strings.TrimSpace(*cursor) == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(strings.TrimSpace(*cursor), 10, 64)
	if err != nil || value <= 0 {
		return 0, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid cursor", map[string]any{"field": "cursor"})
	}
	return value, nil
}

func (a *Adapter) handleAdminCreateServiceAccount(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	requesterID, requestErr := a.serviceAccountToolRequester(ctx, "admin_createServiceAccount")
	if requestErr != nil {
		return nil, nil, requestErr
	}
	var in createServiceAccountInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing name", map[string]any{"field": "name"})
	}
	account, err := a.store.CreateServiceAccount(ctx, requesterID, in.Name)
	if err != nil {
		return nil, nil, mapPrivilegedStoreError(err)
	}
	return map[string]any{"account": serviceAccountToItem(account)}, map[string]any{}, nil
}

func (a *Adapter) handleAdminListServiceAccounts(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	requesterID, requestErr := a.serviceAccountToolRequester(ctx, "admin_listServiceAccounts")
	if requestErr != nil {
		return nil, nil, requestErr
	}
	var in serviceAccountsListInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	limit, limitErr := normalizeServiceAdminLimit(in.Limit, store.MaxServiceAccountPageSize)
	if limitErr != nil {
		return nil, nil, limitErr
	}
	accounts, nextCursor, err := a.store.ListServiceAccounts(ctx, requesterID, limit, in.Cursor)
	if err != nil {
		return nil, nil, mapPrivilegedStoreError(err)
	}
	items := make([]serviceAccountItem, 0, len(accounts))
	for _, account := range accounts {
		items = append(items, serviceAccountToItem(account))
	}
	return map[string]any{"items": items}, map[string]any{"nextCursor": nextCursor, "hasMore": nextCursor != nil}, nil
}

func (a *Adapter) handleAdminDeleteServiceAccount(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	requesterID, requestErr := a.serviceAccountToolRequester(ctx, "admin_deleteServiceAccount")
	if requestErr != nil {
		return nil, nil, requestErr
	}
	var in serviceAccountUserInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.UserID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid userId", map[string]any{"field": "userId"})
	}
	if err := a.store.DeleteServiceAccount(ctx, requesterID, in.UserID); err != nil {
		return nil, nil, mapPrivilegedStoreError(err)
	}
	return map[string]any{"status": "deleted", "userId": in.UserID}, map[string]any{}, nil
}

func (a *Adapter) handleAdminCreateServiceToken(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	requesterID, requestErr := a.serviceAccountToolRequester(ctx, "admin_createServiceToken")
	if requestErr != nil {
		return nil, nil, requestErr
	}
	var in createServiceTokenInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.UserID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid userId", map[string]any{"field": "userId"})
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			in.Name = nil
		} else {
			in.Name = &name
		}
	}
	tokenID, plaintext, createdAt, err := a.store.CreateServiceAccountToken(ctx, requesterID, in.UserID, in.Name)
	if err != nil {
		return nil, nil, mapPrivilegedStoreError(err)
	}
	return map[string]any{"token": serviceTokenCreatedItem{
		TokenID: tokenID, UserID: in.UserID, Name: in.Name, CreatedAt: createdAt, Token: plaintext,
	}}, map[string]any{}, nil
}

func (a *Adapter) handleAdminListServiceTokens(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	requesterID, requestErr := a.serviceAccountToolRequester(ctx, "admin_listServiceTokens")
	if requestErr != nil {
		return nil, nil, requestErr
	}
	var in listServiceTokensInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.UserID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid userId", map[string]any{"field": "userId"})
	}
	limit, limitErr := normalizeServiceAdminLimit(in.Limit, store.MaxServiceTokenPageSize)
	if limitErr != nil {
		return nil, nil, limitErr
	}
	beforeID, cursorErr := parseServiceTokenCursor(in.Cursor)
	if cursorErr != nil {
		return nil, nil, cursorErr
	}
	tokens, nextBeforeID, err := a.store.ListServiceAccountTokens(ctx, requesterID, in.UserID, limit, beforeID)
	if err != nil {
		return nil, nil, mapPrivilegedStoreError(err)
	}
	items := make([]serviceTokenItem, 0, len(tokens))
	for _, token := range tokens {
		items = append(items, serviceTokenToItem(token))
	}
	var nextCursor *string
	if nextBeforeID != nil {
		value := strconv.FormatInt(*nextBeforeID, 10)
		nextCursor = &value
	}
	return map[string]any{"items": items}, map[string]any{"nextCursor": nextCursor, "hasMore": nextCursor != nil}, nil
}

func (a *Adapter) handleAdminRevokeServiceToken(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	requesterID, requestErr := a.serviceAccountToolRequester(ctx, "admin_revokeServiceToken")
	if requestErr != nil {
		return nil, nil, requestErr
	}
	var in revokeServiceTokenInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.TokenID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid tokenId", map[string]any{"field": "tokenId"})
	}
	if err := a.store.RevokeServiceAccountToken(ctx, requesterID, in.TokenID); err != nil {
		return nil, nil, mapPrivilegedStoreError(err)
	}
	return map[string]any{"status": "revoked", "tokenId": in.TokenID}, map[string]any{}, nil
}
