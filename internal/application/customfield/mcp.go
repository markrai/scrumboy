package customfield

import (
	"context"

	"scrumboy/internal/store"
)

// MCPAccessStore resolves the project slug used by MCP definition mutations.
type MCPAccessStore interface {
	GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error)
}

// MCPTarget identifies the slug whose access is resolved after semantic
// validation, matching the current MCP tool order.
type MCPTarget struct {
	ProjectSlug string
	Mode        store.Mode
}

// MCPService owns MCP name/type/key validation, slug access, Maintainer
// authorization from the resolved ProjectContext, and the store mutation.
type MCPService struct {
	access    MCPAccessStore
	mutations MutationStore
}

func NewMCPService(access MCPAccessStore, mutations MutationStore) *MCPService {
	return &MCPService{access: access, mutations: mutations}
}

func (s *MCPService) Create(ctx context.Context, target MCPTarget, command CreateCommand) (store.CustomField, error) {
	name, err := normalizeName(command.Name)
	if err != nil {
		return store.CustomField{}, err
	}
	typ, err := normalizeType(command.Type)
	if err != nil {
		return store.CustomField{}, err
	}
	projectContext, err := s.access.GetProjectContextBySlug(ctx, target.ProjectSlug, target.Mode)
	if err != nil {
		return store.CustomField{}, err
	}
	if err := authorizeMaintainer(projectContext.Role); err != nil {
		return store.CustomField{}, err
	}
	return s.mutations.CreateCustomField(ctx, projectContext.Project.ID, command.Key, name, typ)
}

func (s *MCPService) Delete(ctx context.Context, target MCPTarget, command DeleteCommand) error {
	key, err := normalizeDeleteKey(command.Key)
	if err != nil {
		return err
	}
	projectContext, err := s.access.GetProjectContextBySlug(ctx, target.ProjectSlug, target.Mode)
	if err != nil {
		return err
	}
	if err := authorizeMaintainer(projectContext.Role); err != nil {
		return err
	}
	return s.mutations.DeleteCustomField(ctx, projectContext.Project.ID, key)
}
