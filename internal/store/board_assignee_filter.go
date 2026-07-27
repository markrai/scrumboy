package store

import (
	"errors"
	"strconv"
	"strings"
)

var errInvalidAssigneeFilter = errors.New("invalid assignee")
var errInvalidSortOrder = errors.New("invalid sort")

// ParseAssigneeFilter validates the public board assignee filter grammar.
//
// Empty input applies no filter, "unassigned" matches todos without an
// assignee, "me" resolves to actorUserID, and a positive decimal integer
// matches that concrete user ID. Sentinels are case-sensitive after trimming.
func ParseAssigneeFilter(raw string, actorUserID *int64) (AssigneeFilter, error) {
	value := strings.TrimSpace(raw)
	switch value {
	case "":
		return AssigneeFilter{}, nil
	case "unassigned":
		return AssigneeFilter{mode: assigneeFilterUnassigned}, nil
	case "me":
		if actorUserID == nil || *actorUserID < 1 {
			return AssigneeFilter{}, errInvalidAssigneeFilter
		}
		return AssigneeFilter{mode: assigneeFilterUser, userID: *actorUserID}, nil
	default:
		userID, err := strconv.ParseInt(value, 10, 64)
		if err != nil || userID < 1 {
			return AssigneeFilter{}, errInvalidAssigneeFilter
		}
		return AssigneeFilter{mode: assigneeFilterUser, userID: userID}, nil
	}
}

// ParseSortOrder validates the public board sort-order grammar.
//
// Empty input keeps the default manual drag-rank order; "newest"/"oldest"
// order lanes by created_at (with id as a stable tiebreaker). Any other
// value is rejected.
func ParseSortOrder(raw string) (SortOrder, error) {
	value := strings.TrimSpace(raw)
	switch value {
	case "":
		return SortOrderDefault, nil
	case string(SortOrderNewest):
		return SortOrderNewest, nil
	case string(SortOrderOldest):
		return SortOrderOldest, nil
	default:
		return "", errInvalidSortOrder
	}
}
