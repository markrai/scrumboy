package board

import (
	"context"

	"scrumboy/internal/store"
)

// LaneResult names the values returned by a lane continuation read.
type LaneResult struct {
	Items      []store.Todo
	NextCursor string
	HasMore    bool
}

// LaneReadStore is the persistence capability required by the lane
// continuation use case.
type LaneReadStore interface {
	ListTodosForBoardLane(
		ctx context.Context,
		projectID int64,
		columnKey string,
		limit int,
		afterA int64,
		afterB int64,
		tagFilter string,
		searchFilter string,
		assigneeFilter store.AssigneeFilter,
		priorityFilter store.PriorityFilter,
		sprintFilter store.SprintFilter,
		sortOrder store.SortOrder,
	) ([]store.Todo, string, bool, error)
}

// LaneService provides lane continuation application operations.
type LaneService struct {
	store LaneReadStore
}

func NewLaneService(store LaneReadStore) *LaneService {
	return &LaneService{store: store}
}

// Read performs a lane continuation read without changing
// transport-normalized input or store behavior.
func (s *LaneService) Read(ctx context.Context, pc *store.ProjectContext, query LaneQuery) (LaneResult, error) {
	items, nextCursor, hasMore, err := s.store.ListTodosForBoardLane(
		ctx,
		pc.Project.ID,
		query.ColumnKey,
		query.Limit,
		query.AfterA,
		query.AfterB,
		query.TagFilter,
		query.SearchFilter,
		query.AssigneeFilter,
		query.PriorityFilter,
		query.SprintFilter,
		query.SortOrder,
	)
	if err != nil {
		return LaneResult{}, err
	}
	suppressDisabledSprintAssignmentsInTodos(pc.Project, items)

	return LaneResult{
		Items:      items,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}
