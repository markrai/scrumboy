package mcp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	customfieldapp "scrumboy/internal/application/customfield"
	"scrumboy/internal/store"
)

type customFieldListInput struct {
	ProjectSlug string `json:"projectSlug"`
}

type customFieldCreateInput struct {
	ProjectSlug string `json:"projectSlug"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Key         string `json:"key"`
}

type customFieldDeleteInput struct {
	ProjectSlug string `json:"projectSlug"`
	Key         string `json:"key"`
}

func customFieldToItem(f store.CustomField) customFieldItem {
	return customFieldItem{Key: f.Key, Name: f.Name, Type: string(f.Type), Position: f.Position}
}

func mapCustomFieldMutationError(err error) *adapterError {
	switch {
	case errors.Is(err, customfieldapp.ErrMaintainerRequired):
		return newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	case errors.Is(err, customfieldapp.ErrNameRequired):
		return newAdapterError(http.StatusBadRequest, CodeValidationError, "name required", map[string]any{"field": "name"})
	case errors.Is(err, customfieldapp.ErrInvalidType):
		return newAdapterError(http.StatusBadRequest, CodeValidationError, "type must be text, url or number", map[string]any{"field": "type"})
	case errors.Is(err, customfieldapp.ErrKeyRequired):
		return newAdapterError(http.StatusBadRequest, CodeValidationError, "missing key", map[string]any{"field": "key"})
	default:
		return mapStoreError(err)
	}
}

// customFieldsGate applies the same availability rules as the priorities tools.
func (a *Adapter) customFieldsGate(ctx context.Context, tool string) *adapterError {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return err
	}
	switch {
	case a.mode == "anonymous":
		return newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, tool+" is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, tool+" is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}
	return nil
}

func (a *Adapter) handleCustomFieldsList(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	if gateErr := a.customFieldsGate(ctx, "customFields_list"); gateErr != nil {
		return nil, nil, gateErr
	}
	var in customFieldListInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	pc, err := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if err != nil {
		return nil, nil, mapStoreError(err)
	}
	fields, err := a.store.ListProjectCustomFields(ctx, pc.Project.ID)
	if err != nil {
		return nil, nil, mapStoreError(err)
	}
	items := make([]customFieldItem, 0, len(fields))
	for _, f := range fields {
		items = append(items, customFieldToItem(f))
	}
	return map[string]any{"items": items}, map[string]any{}, nil
}

func (a *Adapter) handleCustomFieldsCreate(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	if gateErr := a.customFieldsGate(ctx, "customFields_create"); gateErr != nil {
		return nil, nil, gateErr
	}
	var in customFieldCreateInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	field, err := a.customFieldMutations.Create(ctx, customfieldapp.MCPTarget{
		ProjectSlug: in.ProjectSlug,
		Mode:        a.storeMode(),
	}, customfieldapp.CreateCommand{
		Key:  in.Key,
		Name: in.Name,
		Type: in.Type,
	})
	if err != nil {
		return nil, nil, mapCustomFieldMutationError(err)
	}
	return map[string]any{"field": customFieldToItem(field)}, map[string]any{}, nil
}

func (a *Adapter) handleCustomFieldsDelete(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	if gateErr := a.customFieldsGate(ctx, "customFields_delete"); gateErr != nil {
		return nil, nil, gateErr
	}
	var in customFieldDeleteInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if err := a.customFieldMutations.Delete(ctx, customfieldapp.MCPTarget{
		ProjectSlug: in.ProjectSlug,
		Mode:        a.storeMode(),
	}, customfieldapp.DeleteCommand{Key: in.Key}); err != nil {
		return nil, nil, mapCustomFieldMutationError(err)
	}
	return map[string]any{"deleted": map[string]any{"projectSlug": in.ProjectSlug, "key": strings.TrimSpace(in.Key)}}, map[string]any{}, nil
}
