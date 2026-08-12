package board

import "scrumboy/internal/store"

// LaneQuery is the normalized input for a lane continuation read.
//
// Transport adapters remain responsible for parsing and validating their
// public request formats before constructing a LaneQuery.
type LaneQuery struct {
	ColumnKey      string
	Limit          int
	AfterA         int64
	AfterB         int64
	TagFilter      string
	SearchFilter   string
	AssigneeFilter store.AssigneeFilter
	PriorityFilter store.PriorityFilter
	SprintFilter   store.SprintFilter
	SortOrder      store.SortOrder
}
