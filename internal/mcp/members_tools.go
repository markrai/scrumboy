package mcp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"scrumboy/internal/store"
)

// normalizeProjectMemberRoleForMCP maps legacy stored role strings to canonical MCP output.
func normalizeProjectMemberRoleForMCP(role string) string {
	s := strings.TrimSpace(role)
	switch strings.ToLower(s) {
	case "owner":
		return "maintainer"
	case "editor":
		return "contributor"
	default:
		return s
	}
}

func (a *Adapter) handleMembersList(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "members_list is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "members_list is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in sprintProjectInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}

	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	members, mErr := a.store.ListProjectMembers(ctx, pc.Project.ID, userID)
	if mErr != nil {
		return nil, nil, mapStoreError(mErr)
	}

	items := make([]projectMemberItem, 0, len(members))
	for _, m := range members {
		items = append(items, projectMemberItem{
			ProjectSlug: in.ProjectSlug,
			UserID:      m.UserID,
			Email:       m.Email,
			Name:        m.Name,
			Image:       m.Image,
			Role:        normalizeProjectMemberRoleForMCP(string(m.Role)),
			CreatedAt:   m.CreatedAt,
		})
	}

	return map[string]any{
		"items": items,
	}, map[string]any{}, nil
}

func (a *Adapter) handleMembersListAvailable(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "members_listAvailable is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "members_listAvailable is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in sprintProjectInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}

	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	users, uErr := a.store.ListAvailableUsersForProject(ctx, userID, pc.Project.ID)
	if uErr != nil {
		if errors.Is(uErr, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
		}
		return nil, nil, mapStoreError(uErr)
	}

	items := make([]availableUserItem, 0, len(users))
	for _, u := range users {
		items = append(items, availableUserItem{
			UserID:      u.ID,
			Email:       u.Email,
			Name:        u.Name,
			SystemRole:  string(u.SystemRole),
			IsBootstrap: u.IsBootstrap,
			CreatedAt:   u.CreatedAt,
		})
	}

	return map[string]any{
		"items": items,
	}, map[string]any{}, nil
}

func (a *Adapter) handleMembersAdd(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "members_add is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "members_add is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in membersAddInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if in.UserID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid userId", map[string]any{"field": "userId"})
	}
	if strings.TrimSpace(in.Role) == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing role", map[string]any{"field": "role"})
	}

	pr, ok := store.ParseMemberRole(in.Role)
	if !ok {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "unsupported role", map[string]any{"field": "role"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}

	if !pc.Role.HasMinimumRole(store.RoleMaintainer) {
		return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	}

	requesterID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	if err := a.store.AddProjectMember(ctx, requesterID, pc.Project.ID, in.UserID, pr); err != nil {
		if errors.Is(err, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
		}
		return nil, nil, mapStoreError(err)
	}

	members, mErr := a.store.ListProjectMembers(ctx, pc.Project.ID, requesterID)
	if mErr != nil {
		return nil, nil, mapStoreError(mErr)
	}
	var found *store.ProjectMember
	for i := range members {
		if members[i].UserID == in.UserID {
			found = &members[i]
			break
		}
	}
	if found == nil {
		return nil, nil, newAdapterError(http.StatusInternalServerError, CodeInternal, "member not found after add", nil)
	}

	item := projectMemberItem{
		ProjectSlug: in.ProjectSlug,
		UserID:      found.UserID,
		Email:       found.Email,
		Name:        found.Name,
		Image:       found.Image,
		Role:        normalizeProjectMemberRoleForMCP(string(found.Role)),
		CreatedAt:   found.CreatedAt,
	}

	return map[string]any{
		"member": item,
	}, map[string]any{}, nil
}

func (a *Adapter) handleMembersUpdateRole(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "members_updateRole is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "members_updateRole is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in membersAddInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if in.UserID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid userId", map[string]any{"field": "userId"})
	}
	if strings.TrimSpace(in.Role) == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing role", map[string]any{"field": "role"})
	}

	pr, ok := store.ParseMemberRole(in.Role)
	if !ok {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "unsupported role", map[string]any{"field": "role"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}

	if !pc.Role.HasMinimumRole(store.RoleMaintainer) {
		return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	}

	requesterID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	if err := a.store.UpdateProjectMemberRole(ctx, requesterID, pc.Project.ID, in.UserID, pr); err != nil {
		switch {
		case errors.Is(err, store.ErrUnauthorized):
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
		case errors.Is(err, store.ErrConflict):
			return nil, nil, newAdapterError(http.StatusConflict, CodeConflict, err.Error(), nil)
		case errors.Is(err, store.ErrNotFound):
			return nil, nil, newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
		case errors.Is(err, store.ErrValidation):
			return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, err.Error(), map[string]any{"field": "role"})
		default:
			return nil, nil, mapStoreError(err)
		}
	}

	members, mErr := a.store.ListProjectMembers(ctx, pc.Project.ID, requesterID)
	if mErr != nil {
		return nil, nil, mapStoreError(mErr)
	}
	var found *store.ProjectMember
	for i := range members {
		if members[i].UserID == in.UserID {
			found = &members[i]
			break
		}
	}
	if found == nil {
		return nil, nil, newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
	}

	item := projectMemberItem{
		ProjectSlug: in.ProjectSlug,
		UserID:      found.UserID,
		Email:       found.Email,
		Name:        found.Name,
		Image:       found.Image,
		Role:        normalizeProjectMemberRoleForMCP(string(found.Role)),
		CreatedAt:   found.CreatedAt,
	}

	return map[string]any{
		"member": item,
	}, map[string]any{}, nil
}

func (a *Adapter) handleMembersRemove(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "members_remove is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "members_remove is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in membersRemoveInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if in.UserID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid userId", map[string]any{"field": "userId"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}

	if !pc.Role.HasMinimumRole(store.RoleMaintainer) {
		return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	}

	requesterID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	if err := a.store.RemoveProjectMember(ctx, requesterID, pc.Project.ID, in.UserID); err != nil {
		switch {
		case errors.Is(err, store.ErrUnauthorized):
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
		case errors.Is(err, store.ErrNotFound):
			return nil, nil, newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
		case errors.Is(err, store.ErrValidation):
			return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, err.Error(), nil)
		default:
			return nil, nil, mapStoreError(err)
		}
	}

	return map[string]any{
		"removed": map[string]any{
			"projectSlug": in.ProjectSlug,
			"userId":      in.UserID,
		},
	}, map[string]any{}, nil
}
