package project

import (
	"context"

	"scrumboy/internal/store"
)

// MCPDurableCreationService delegates MCP durable creation to the store's
// default-workflow operation and deliberately has no read or publisher.
type MCPDurableCreationService struct {
	projects ProjectCreationStore
}

// NewMCPDurableCreationService constructs the additive MCP durable creation
// service from its one required persistence capability.
func NewMCPDurableCreationService(projects ProjectCreationStore) *MCPDurableCreationService {
	return &MCPDurableCreationService{projects: projects}
}

// Create performs exactly one default-workflow project creation and returns
// the characterized synthetic Maintainer role without a post-read.
func (s *MCPDurableCreationService) Create(
	ctx context.Context,
	command MCPDurableCreationCommand,
) (MCPProjectResult, error) {
	created, err := s.projects.CreateProject(ctx, command.Name)
	if err != nil {
		return MCPProjectResult{}, err
	}
	return newMCPProjectResult(created, store.RoleMaintainer), nil
}
