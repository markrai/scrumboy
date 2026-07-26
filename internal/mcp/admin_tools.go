package mcp

import (
	"context"
	"net/http"

	"scrumboy/internal/store"
)

type adminUpdateUserRoleInput struct {
	UserId int64  `json:"userId"`
	Role   string `json:"role"`
}

type adminDeleteUserInput struct {
	UserId int64 `json:"userId"`
}

func adminUserToItem(u store.User) adminUserItem {
	return adminUserItem{
		UserID:      u.ID,
		Email:       u.Email,
		Name:        u.Name,
		SystemRole:  string(u.SystemRole),
		IsBootstrap: u.IsBootstrap,
		CreatedAt:   u.CreatedAt,
	}
}

// parseAdminUpdatableSystemRole validates the role a caller of
// admin_updateUserRole may set. Mirrors handleAdminUsersUpdateRole in
// internal/httpapi/routing_admin.go, which deliberately only allows
// "admin" or "user" via the API -- promotion to "owner" is not exposed
// through this route, even though the store-level UpdateUserRole would
// otherwise accept it. Pure function so it is unit testable without a store.
func parseAdminUpdatableSystemRole(role string) (store.SystemRole, *adapterError) {
	if role != "admin" && role != "user" {
		return "", newAdapterError(http.StatusBadRequest, CodeValidationError, "role must be 'admin' or 'user'", map[string]any{"field": "role"})
	}
	parsed, ok := store.ParseSystemRole(role)
	if !ok {
		return "", newAdapterError(http.StatusBadRequest, CodeValidationError, "unsupported role", map[string]any{"field": "role"})
	}
	return parsed, nil
}

func requesterHasAnySystemRole(u store.User, allowed ...store.SystemRole) bool {
	for _, role := range allowed {
		if u.SystemRole == role {
			return true
		}
	}
	return false
}

func (a *Adapter) requireRequesterAdminOrOwner(ctx context.Context, requesterID int64) *adapterError {
	u, err := a.store.GetUser(ctx, requesterID)
	if err != nil {
		return mapStoreError(err)
	}
	if !requesterHasAnySystemRole(u, store.SystemRoleOwner, store.SystemRoleAdmin) {
		return newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
	}
	return nil
}

func (a *Adapter) requireRequesterOwner(ctx context.Context, requesterID int64) *adapterError {
	u, err := a.store.GetUser(ctx, requesterID)
	if err != nil {
		return mapStoreError(err)
	}
	if u.SystemRole != store.SystemRoleOwner {
		return newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
	}
	return nil
}

func (a *Adapter) handleAdminListUsers(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "admin_listUsers is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "admin_listUsers is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	requesterID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	if roleErr := a.requireRequesterAdminOrOwner(ctx, requesterID); roleErr != nil {
		return nil, nil, roleErr
	}

	users, listErr := a.store.ListUsers(ctx, requesterID)
	if listErr != nil {
		return nil, nil, mapPrivilegedStoreError(listErr)
	}

	items := make([]adminUserItem, 0, len(users))
	for _, u := range users {
		items = append(items, adminUserToItem(u))
	}

	return map[string]any{
		"items": items,
	}, map[string]any{}, nil
}

func (a *Adapter) handleAdminUpdateUserRole(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "admin_updateUserRole is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "admin_updateUserRole is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in adminUpdateUserRoleInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.UserId <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid userId", map[string]any{"field": "userId"})
	}

	newRole, roleErr := parseAdminUpdatableSystemRole(in.Role)
	if roleErr != nil {
		return nil, nil, roleErr
	}

	requesterID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	if roleErr := a.requireRequesterOwner(ctx, requesterID); roleErr != nil {
		return nil, nil, roleErr
	}

	if updErr := a.store.UpdateUserRole(ctx, requesterID, in.UserId, newRole); updErr != nil {
		return nil, nil, mapPrivilegedStoreError(updErr)
	}

	updated, getErr := a.store.GetUser(ctx, in.UserId)
	if getErr != nil {
		return nil, nil, mapStoreError(getErr)
	}

	return map[string]any{
		"user": adminUserToItem(updated),
	}, map[string]any{}, nil
}

func (a *Adapter) handleAdminDeleteUser(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "admin_deleteUser is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "admin_deleteUser is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in adminDeleteUserInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.UserId <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid userId", map[string]any{"field": "userId"})
	}

	requesterID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	if roleErr := a.requireRequesterOwner(ctx, requesterID); roleErr != nil {
		return nil, nil, roleErr
	}

	if delErr := a.store.DeleteUser(ctx, requesterID, in.UserId); delErr != nil {
		return nil, nil, mapPrivilegedStoreError(delErr)
	}

	return map[string]any{
		"status": "deleted",
		"userId": in.UserId,
	}, map[string]any{}, nil
}
