package board

import "scrumboy/internal/store"

// LegacyQuery is the normalized input for the unpaged numeric-ID board read.
//
// Transport adapters remain responsible for parsing and validating their
// public request formats before constructing a LegacyQuery.
type LegacyQuery struct {
	TagFilter      string
	SearchFilter   string
	AssigneeFilter store.AssigneeFilter
	PriorityFilter store.PriorityFilter
	SprintFilter   store.SprintFilter
	SortOrder      store.SortOrder
}
