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

// PreparedMCPCreate binds validated create fields to an authorized project.
type PreparedMCPCreate struct {
	ctx     context.Context
	service *MCPService
	project store.Project
	key     string
	name    string
	typ     store.CustomFieldType
}

// PreparedMCPDelete binds a validated delete key to an authorized project.
type PreparedMCPDelete struct {
	ctx     context.Context
	service *MCPService
	project store.Project
	key     string
}

func (p *PreparedMCPCreate) Project() store.Project {
	if p == nil {
		return store.Project{}
	}
	return p.project
}

func (p *PreparedMCPDelete) Project() store.Project {
	if p == nil {
		return store.Project{}
	}
	return p.project
}

// PrepareCreate validates name/type, then resolves slug access and Maintainer
// authorization. It does not persist.
func (s *MCPService) PrepareCreate(ctx context.Context, target MCPTarget, command CreateCommand) (*PreparedMCPCreate, error) {
	name, err := normalizeName(command.Name)
	if err != nil {
		return nil, err
	}
	typ, err := normalizeType(command.Type)
	if err != nil {
		return nil, err
	}
	projectContext, err := s.access.GetProjectContextBySlug(ctx, target.ProjectSlug, target.Mode)
	if err != nil {
		return nil, err
	}
	if err := authorizeMaintainer(projectContext.Role); err != nil {
		return nil, err
	}
	return &PreparedMCPCreate{
		ctx:     ctx,
		service: s,
		project: projectContext.Project,
		key:     command.Key,
		name:    name,
		typ:     typ,
	}, nil
}

// PrepareDelete validates the key, then resolves slug access and Maintainer
// authorization. It does not persist.
func (s *MCPService) PrepareDelete(ctx context.Context, target MCPTarget, command DeleteCommand) (*PreparedMCPDelete, error) {
	key, err := normalizeDeleteKey(command.Key)
	if err != nil {
		return nil, err
	}
	projectContext, err := s.access.GetProjectContextBySlug(ctx, target.ProjectSlug, target.Mode)
	if err != nil {
		return nil, err
	}
	if err := authorizeMaintainer(projectContext.Role); err != nil {
		return nil, err
	}
	return &PreparedMCPDelete{
		ctx:     ctx,
		service: s,
		project: projectContext.Project,
		key:     key,
	}, nil
}

func (p *PreparedMCPCreate) Create() (store.CustomField, error) {
	return p.service.mutations.CreateCustomField(p.ctx, p.project.ID, p.key, p.name, p.typ)
}

func (p *PreparedMCPDelete) Delete() error {
	return p.service.mutations.DeleteCustomField(p.ctx, p.project.ID, p.key)
}

func (s *MCPService) Create(ctx context.Context, target MCPTarget, command CreateCommand) (store.CustomField, error) {
	prepared, err := s.PrepareCreate(ctx, target, command)
	if err != nil {
		return store.CustomField{}, err
	}
	return prepared.Create()
}

func (s *MCPService) Delete(ctx context.Context, target MCPTarget, command DeleteCommand) error {
	prepared, err := s.PrepareDelete(ctx, target, command)
	if err != nil {
		return err
	}
	return prepared.Delete()
}
