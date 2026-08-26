package wall

import (
	"context"

	"scrumboy/internal/store"
)

// preparedRESTWriter binds the authorization/mutation context separately from
// the raw publication context. The split preserves the existing HTTP boundary:
// role and store calls receive actor enrichment, while event fanout receives
// the exact raw request context.
type preparedRESTWriter struct {
	mutationCtx context.Context
	effectCtx   context.Context
	actorID     int64
	projectID   int64
}

// prepareRESTWriter performs the fresh Contributor gate shared by REST Wall
// mutation families. It does not inspect mutation input, read a Wall, or
// publish an effect.
func prepareRESTWriter(
	mutationCtx context.Context,
	effectCtx context.Context,
	target ResolvedRESTTarget,
	roles RESTWriterRoleStore,
) (preparedRESTWriter, error) {
	actorID, ok := store.UserIDFromContext(mutationCtx)
	if !ok {
		return preparedRESTWriter{}, ErrActorRequired
	}

	role, err := roles.GetProjectRole(mutationCtx, target.ProjectID, actorID)
	if err != nil || !role.HasMinimumRole(store.RoleContributor) {
		return preparedRESTWriter{}, ErrContributorRequired
	}

	return preparedRESTWriter{
		mutationCtx: mutationCtx,
		effectCtx:   effectCtx,
		actorID:     actorID,
		projectID:   target.ProjectID,
	}, nil
}
