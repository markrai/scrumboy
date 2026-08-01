package board

import (
	"context"

	"scrumboy/internal/store"
)

// Result names the values returned by an initial board read.
type Result struct {
	Project     store.Project
	Tags        []store.TagCount
	Workflow    []store.WorkflowColumn
	Columns     map[string][]store.Todo
	ColumnsMeta map[string]store.LaneMeta
}

// ReadStore is the persistence capability required by the initial board-read
// use case.
type ReadStore interface {
	GetBoardPaged(
		ctx context.Context,
		pc *store.ProjectContext,
		tagFilter string,
		searchFilter string,
		assigneeFilter store.AssigneeFilter,
		sprintFilter store.SprintFilter,
		sortOrder store.SortOrder,
		limitPerLane int,
	) (
		store.Project,
		[]store.TagCount,
		[]store.WorkflowColumn,
		map[string][]store.Todo,
		map[string]store.LaneMeta,
		error,
	)
}

// Service provides board-read application operations.
type Service struct {
	store ReadStore
}

func NewService(store ReadStore) *Service {
	return &Service{store: store}
}

// ReadInitial performs the initial, optionally paged board read without
// changing transport-normalized input or store behavior.
func (s *Service) ReadInitial(ctx context.Context, pc *store.ProjectContext, query Query) (Result, error) {
	project, tags, workflow, columns, columnsMeta, err := s.store.GetBoardPaged(
		ctx,
		pc,
		query.TagFilter,
		query.SearchFilter,
		query.AssigneeFilter,
		query.SprintFilter,
		query.SortOrder,
		query.LimitPerLane,
	)
	if err != nil {
		return Result{}, err
	}

	return Result{
		Project:     project,
		Tags:        tags,
		Workflow:    workflow,
		Columns:     columns,
		ColumnsMeta: columnsMeta,
	}, nil
}
