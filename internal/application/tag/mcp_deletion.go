package tag

import (
	"context"

	"scrumboy/internal/store"
)

// MCPDeletionServiceDependencies contains only the access, read, and row
// deletion capabilities required by MCP tag deletion operations.
type MCPDeletionServiceDependencies struct {
	Access        MCPProjectAccessStore
	MineRead      MineTagReadStore
	ProjectScoped ProjectScopedTagReadStore
	Rows          LegacyRowDeletionStore
}

// MCPDeletionService prepares MCP-specific tag deletion operations. It has no
// publisher, affected-project result, or post-delete read capability.
type MCPDeletionService struct {
	access        MCPProjectAccessStore
	mineRead      MineTagReadStore
	projectScoped ProjectScopedTagReadStore
	rows          LegacyRowDeletionStore
}

// NewMCPDeletionService constructs the additive MCP deletion service.
func NewMCPDeletionService(deps MCPDeletionServiceDependencies) *MCPDeletionService {
	return &MCPDeletionService{
		access:        deps.Access,
		mineRead:      deps.MineRead,
		projectScoped: deps.ProjectScoped,
		rows:          deps.Rows,
	}
}

// MCPMineIDDeletionTarget contains already-validated transport-neutral input
// for one mine tag deletion.
type MCPMineIDDeletionTarget struct {
	TagID int64
}

// PreparedMCPMineIDDeletion binds the actor and exact mine tag ID selected by
// the characterized pre-read.
type PreparedMCPMineIDDeletion struct {
	ctx     context.Context
	service *MCPDeletionService
	actorID int64
	tagID   int64
}

// PrepareMineID extracts the actor, reads the mine library exactly once, and
// selects the requested row by exact ID.
func (s *MCPDeletionService) PrepareMineID(
	ctx context.Context,
	target MCPMineIDDeletionTarget,
) (*PreparedMCPMineIDDeletion, error) {
	actorID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, ErrActorRequired
	}

	tags, err := s.mineRead.ListUserTags(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if _, found := findMineTagByID(tags, target.TagID); !found {
		return nil, store.ErrNotFound
	}

	return &PreparedMCPMineIDDeletion{
		ctx:     ctx,
		service: s,
		actorID: actorID,
		tagID:   target.TagID,
	}, nil
}

// Delete performs the legacy row-oriented mine deletion exactly once.
func (p *PreparedMCPMineIDDeletion) Delete() error {
	return p.service.rows.DeleteTag(p.ctx, p.actorID, p.tagID, false)
}

// MCPProjectIDDeletionTarget contains already-validated transport-neutral
// input for one project-scoped tag deletion.
type MCPProjectIDDeletionTarget struct {
	ProjectSlug string
	Mode        store.Mode
	TagID       int64
}

// PreparedMCPProjectIDDeletion binds the actor, exact tag ID, and anonymous
// board classification derived after the characterized access checks.
type PreparedMCPProjectIDDeletion struct {
	ctx              context.Context
	service          *MCPDeletionService
	actorID          int64
	tagID            int64
	isAnonymousBoard bool
}

// PrepareProjectID preserves the access, actor, Maintainer, and project-scoped
// target ordering of the existing MCP adapter.
func (s *MCPDeletionService) PrepareProjectID(
	ctx context.Context,
	target MCPProjectIDDeletionTarget,
) (*PreparedMCPProjectIDDeletion, error) {
	projectContext, err := s.access.GetProjectContextBySlug(ctx, target.ProjectSlug, target.Mode)
	if err != nil {
		return nil, err
	}

	actorID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, ErrActorRequired
	}
	if !projectContext.Role.HasMinimumRole(store.RoleMaintainer) {
		return nil, ErrMaintainerRequired
	}
	if _, err := s.projectScoped.GetProjectScopedTagByID(
		ctx,
		projectContext.Project.ID,
		target.TagID,
	); err != nil {
		return nil, err
	}

	return &PreparedMCPProjectIDDeletion{
		ctx:              ctx,
		service:          s,
		actorID:          actorID,
		tagID:            target.TagID,
		isAnonymousBoard: projectContext.Project.ExpiresAt != nil && projectContext.Project.CreatorUserID == nil,
	}, nil
}

// Delete performs the prepared project-scoped row deletion exactly once.
func (p *PreparedMCPProjectIDDeletion) Delete() error {
	return p.service.rows.DeleteTag(p.ctx, p.actorID, p.tagID, p.isAnonymousBoard)
}
