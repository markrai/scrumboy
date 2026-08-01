package board

import (
	"context"

	"scrumboy/internal/store"
)

// LegacyResult names the values returned by the unpaged numeric-ID board
// read.
type LegacyResult struct {
	Project  store.Project
	Tags     []store.TagCount
	Workflow []store.WorkflowColumn
	Columns  map[string][]store.Todo
}

// LegacyReadStore is the persistence capability required by the unpaged
// numeric-ID board-read use case.
type LegacyReadStore interface {
	GetBoard(
		ctx context.Context,
		pc *store.ProjectContext,
		tagFilter string,
		searchFilter string,
		assigneeFilter store.AssigneeFilter,
		sprintFilter store.SprintFilter,
		sortOrder store.SortOrder,
	) (
		store.Project,
		[]store.TagCount,
		[]store.WorkflowColumn,
		map[string][]store.Todo,
		error,
	)
}

// ReadService is the application surface for REST board reads.
//
// Initial and lane reads retain their existing service implementations.
// Access preparation and data reads remain behind their own narrow store ports.
type ReadService struct {
	initial      *Service
	lane         *LaneService
	legacyAccess LegacyReadAccessStore
	legacy       LegacyReadStore
	slugAccess   SlugReadAccessStore
	slugSprints  SlugReadSprintStore
}

// ReadServiceDependencies names the persistence role supplied to each board
// read operation.
type ReadServiceDependencies struct {
	Initial      ReadStore
	Lane         LaneReadStore
	LegacyAccess LegacyReadAccessStore
	Legacy       LegacyReadStore
	SlugAccess   SlugReadAccessStore
	SlugSprints  SlugReadSprintStore
}

func NewReadService(deps ReadServiceDependencies) *ReadService {
	return &ReadService{
		initial:      NewService(deps.Initial),
		lane:         NewLaneService(deps.Lane),
		legacyAccess: deps.LegacyAccess,
		legacy:       deps.Legacy,
		slugAccess:   deps.SlugAccess,
		slugSprints:  deps.SlugSprints,
	}
}
