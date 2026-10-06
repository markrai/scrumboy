package project

import (
	"context"

	"scrumboy/internal/store"
)

// mcpManagedProjectPreparer owns only the access and management authorization
// sequence shared by MCP update and deletion.
type mcpManagedProjectPreparer struct {
	access ProjectAccessStore
	manage ProjectManageAuthorizationStore
}

type preparedMCPManagedProject struct {
	ctx            context.Context
	projectContext store.ProjectContext
	actorUserID    int64
}

func (p mcpManagedProjectPreparer) prepare(
	ctx context.Context,
	target ProjectSlugTarget,
) (preparedMCPManagedProject, error) {
	projectContext, err := p.access.GetProjectContextBySlug(ctx, target.ProjectSlug, target.Mode)
	if err != nil {
		return preparedMCPManagedProject{}, err
	}

	actorUserID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return preparedMCPManagedProject{}, ErrActorRequired
	}
	if err := p.manage.CheckCanManageProject(ctx, projectContext.Project.ID, actorUserID); err != nil {
		return preparedMCPManagedProject{}, err
	}

	projectContext = cloneProjectContext(projectContext)
	if projectContext.Project.ExpiresAt != nil &&
		projectContext.Project.CreatorUserID != nil &&
		*projectContext.Project.CreatorUserID == actorUserID {
		projectContext.Role = store.RoleMaintainer
	}

	return preparedMCPManagedProject{
		ctx:            ctx,
		projectContext: projectContext,
		actorUserID:    actorUserID,
	}, nil
}
