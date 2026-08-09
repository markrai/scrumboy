package mcp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	membershipapp "scrumboy/internal/application/membership"
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

func mapMembershipMutationPrepareError(err error) *adapterError {
	switch {
	case errors.Is(err, membershipapp.ErrMaintainerRequired):
		return newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	case errors.Is(err, membershipapp.ErrActorRequired):
		return newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	default:
		return mapStoreError(err)
	}
}

func mapMembershipAddError(err error) *adapterError {
	switch {
	case errors.Is(err, membershipapp.ErrAddedMemberMissing):
		return newAdapterError(http.StatusInternalServerError, CodeInternal, "member not found after add", nil)
	case errors.Is(err, store.ErrUnauthorized):
		return newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	default:
		return mapStoreError(err)
	}
}

func mapMembershipUpdateRoleError(err error) *adapterError {
	switch {
	case errors.Is(err, membershipapp.ErrUpdatedMemberMissing):
		return newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
	case errors.Is(err, store.ErrUnauthorized):
		return newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	case errors.Is(err, store.ErrConflict):
		return newAdapterError(http.StatusConflict, CodeConflict, err.Error(), nil)
	case errors.Is(err, store.ErrNotFound):
		return newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
	case errors.Is(err, store.ErrValidation):
		return newAdapterError(http.StatusBadRequest, CodeValidationError, err.Error(), map[string]any{"field": "role"})
	default:
		return mapStoreError(err)
	}
}

func mapMembershipRemoveError(err error) *adapterError {
	switch {
	case errors.Is(err, store.ErrUnauthorized):
		return newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	case errors.Is(err, store.ErrNotFound):
		return newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
	case errors.Is(err, store.ErrValidation):
		return newAdapterError(http.StatusBadRequest, CodeValidationError, err.Error(), nil)
	default:
		return mapStoreError(err)
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

	prepared, prepareErr := a.membershipMutations.Prepare(ctx, membershipapp.MCPMutationTarget{
		ProjectSlug: in.ProjectSlug,
		Mode:        a.storeMode(),
	})
	if prepareErr != nil {
		return nil, nil, mapMembershipMutationPrepareError(prepareErr)
	}

	member, addErr := prepared.Add(membershipapp.AddCommand{
		TargetUserID: in.UserID,
		Role:         pr,
	})
	if addErr != nil {
		return nil, nil, mapMembershipAddError(addErr)
	}

	item := projectMemberItem{
		ProjectSlug: in.ProjectSlug,
		UserID:      member.UserID,
		Email:       member.Email,
		Name:        member.Name,
		Image:       member.Image,
		Role:        normalizeProjectMemberRoleForMCP(string(member.Role)),
		CreatedAt:   member.CreatedAt,
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

	prepared, prepareErr := a.membershipMutations.Prepare(ctx, membershipapp.MCPMutationTarget{
		ProjectSlug: in.ProjectSlug,
		Mode:        a.storeMode(),
	})
	if prepareErr != nil {
		return nil, nil, mapMembershipMutationPrepareError(prepareErr)
	}

	member, updateErr := prepared.UpdateRole(membershipapp.UpdateRoleCommand{
		TargetUserID: in.UserID,
		Role:         pr,
	})
	if updateErr != nil {
		return nil, nil, mapMembershipUpdateRoleError(updateErr)
	}

	item := projectMemberItem{
		ProjectSlug: in.ProjectSlug,
		UserID:      member.UserID,
		Email:       member.Email,
		Name:        member.Name,
		Image:       member.Image,
		Role:        normalizeProjectMemberRoleForMCP(string(member.Role)),
		CreatedAt:   member.CreatedAt,
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

	prepared, prepareErr := a.membershipMutations.Prepare(ctx, membershipapp.MCPMutationTarget{
		ProjectSlug: in.ProjectSlug,
		Mode:        a.storeMode(),
	})
	if prepareErr != nil {
		return nil, nil, mapMembershipMutationPrepareError(prepareErr)
	}

	removeErr := prepared.Remove(membershipapp.RemoveCommand{TargetUserID: in.UserID})
	if removeErr != nil {
		return nil, nil, mapMembershipRemoveError(removeErr)
	}

	return map[string]any{
		"removed": map[string]any{
			"projectSlug": in.ProjectSlug,
			"userId":      in.UserID,
		},
	}, map[string]any{}, nil
}
