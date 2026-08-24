package project

import (
	"context"

	"scrumboy/internal/store"
)

// RESTDurableCreationService prepares durable REST project creation without
// reconstructing the store-owned creation transaction.
type RESTDurableCreationService struct {
	projects ProjectWithWorkflowCreationStore
}

// NewRESTDurableCreationService constructs the additive REST durable creation
// service from its one required persistence capability.
func NewRESTDurableCreationService(projects ProjectWithWorkflowCreationStore) *RESTDurableCreationService {
	return &RESTDurableCreationService{projects: projects}
}

// PreparedRESTDurableCreation owns the workflow slice passed to the mutating
// store normalization path while preserving nil versus non-nil empty input.
type PreparedRESTDurableCreation struct {
	ctx      context.Context
	service  *RESTDurableCreationService
	name     string
	workflow []store.WorkflowColumn
}

// Prepare binds raw creation values without validation or persistence.
func (s *RESTDurableCreationService) Prepare(
	ctx context.Context,
	command RESTDurableCreationCommand,
) *PreparedRESTDurableCreation {
	return &PreparedRESTDurableCreation{
		ctx:      ctx,
		service:  s,
		name:     command.Name,
		workflow: cloneWorkflowColumns(command.Workflow),
	}
}

// Create executes the existing durable creation operation exactly once using
// a fresh workflow copy so store normalization cannot mutate prepared state.
func (p *PreparedRESTDurableCreation) Create() (store.Project, error) {
	created, err := p.service.projects.CreateProjectWithWorkflow(
		p.ctx,
		p.name,
		cloneWorkflowColumns(p.workflow),
	)
	if err != nil {
		return store.Project{}, err
	}
	return cloneProject(created), nil
}

// AnonymousBoardCreationService delegates one link-expiring board creation to
// the store, including its existing post-commit initialization semantics.
type AnonymousBoardCreationService struct {
	boards AnonymousBoardCreationStore
}

// NewAnonymousBoardCreationService constructs the additive Anonymous Board
// creation service from its one required persistence capability.
func NewAnonymousBoardCreationService(boards AnonymousBoardCreationStore) *AnonymousBoardCreationService {
	return &AnonymousBoardCreationService{boards: boards}
}

// Create calls CreateAnonymousBoard exactly once and returns its result or
// error without retry, compensation, cleanup, or post-read.
func (s *AnonymousBoardCreationService) Create(ctx context.Context) (store.Project, error) {
	created, err := s.boards.CreateAnonymousBoard(ctx)
	if err != nil {
		return store.Project{}, err
	}
	return cloneProject(created), nil
}
