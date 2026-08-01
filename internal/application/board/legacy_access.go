package board

import (
	"context"

	"scrumboy/internal/store"
)

// LegacyReadTarget identifies the numeric-ID board whose read access must be
// resolved before transport query validation runs.
type LegacyReadTarget struct {
	ProjectID int64
	Mode      store.Mode
}

// LegacyReadAccessStore is the persistence capability required to resolve an
// authorized project context for the numeric-ID board-read use case.
type LegacyReadAccessStore interface {
	GetProjectContextForRead(
		ctx context.Context,
		projectID int64,
		mode store.Mode,
	) (store.ProjectContext, error)
}

// PreparedLegacyRead is a request-scoped capability that binds the context
// used to authorize the read to the subsequent data operation.
type PreparedLegacyRead struct {
	ctx            context.Context
	legacy         LegacyReadStore
	projectContext store.ProjectContext
}

func (s *ReadService) PrepareLegacy(
	ctx context.Context,
	target LegacyReadTarget,
) (*PreparedLegacyRead, error) {
	pc, err := s.legacyAccess.GetProjectContextForRead(ctx, target.ProjectID, target.Mode)
	if err != nil {
		return nil, err
	}

	return &PreparedLegacyRead{
		ctx:            ctx,
		legacy:         s.legacy,
		projectContext: pc,
	}, nil
}

func (r *PreparedLegacyRead) Read(
	query LegacyQuery,
) (LegacyResult, error) {
	return readLegacy(r.ctx, r.legacy, &r.projectContext, query)
}

func readLegacy(
	ctx context.Context,
	legacy LegacyReadStore,
	pc *store.ProjectContext,
	query LegacyQuery,
) (LegacyResult, error) {
	project, tags, workflow, columns, err := legacy.GetBoard(
		ctx,
		pc,
		query.TagFilter,
		query.SearchFilter,
		query.AssigneeFilter,
		query.SprintFilter,
		query.SortOrder,
	)
	if err != nil {
		return LegacyResult{}, err
	}

	return LegacyResult{
		Project:  project,
		Tags:     tags,
		Workflow: workflow,
		Columns:  columns,
	}, nil
}
