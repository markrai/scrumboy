package mcp

import (
	"context"
	"net/http"

	"scrumboy/internal/store"
)

// meGetData is the me_get result: the identity the request authenticated as.
type meGetData struct {
	UserID int64 `json:"userId"`
	// StableUserID is the user's permanent UUID. userId can be reused by a later user after this
	// one is deleted (SQLite reuses INTEGER PRIMARY KEY values), so an integration that links an
	// external account to a Scrumboy user must store stableUserId, not userId.
	StableUserID string `json:"stableUserId"`
	Email        string `json:"email"`
	Name         string `json:"name"`
}

// handleMeGet returns the user behind the current credential (session cookie, API token, or OAuth
// access token), so an external client can learn which user a token belongs to.
func (a *Adapter) handleMeGet(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "me_get is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "me_get is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in struct{}
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}

	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}
	u, getErr := a.store.GetUser(ctx, userID)
	if getErr != nil {
		return nil, nil, mapStoreError(getErr)
	}

	return meGetData{UserID: u.ID, StableUserID: u.PublicID, Email: u.Email, Name: u.Name}, map[string]any{}, nil
}
