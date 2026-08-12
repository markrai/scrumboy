package board

import "scrumboy/internal/store"

// Query is the normalized input for an initial board read.
//
// Transport adapters remain responsible for parsing and validating their
// public request formats before constructing a Query.
type Query struct {
	TagFilter      string
	SearchFilter   string
	AssigneeFilter store.AssigneeFilter
	PriorityFilter store.PriorityFilter
	SprintFilter   store.SprintFilter
	SortOrder      store.SortOrder
	LimitPerLane   int
}
