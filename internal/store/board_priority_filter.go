package store

import (
	"strings"
)

// PriorityFilterNoPriorityValue is the public board-filter sentinel for todos
// without a priority. Asterisks are outside the priority-key grammar, so this
// value cannot collide with a persisted tier key.
const PriorityFilterNoPriorityValue = "**none**"

// ParsePriorityFilter validates the public board priority filter grammar.
//
// Empty input applies no filter, PriorityFilterNoPriorityValue matches todos
// without a priority_key, and any other value matches that literal priority_key
// (not validated against project_priorities; an unmatched key simply yields
// zero rows).
func ParsePriorityFilter(raw string) (PriorityFilter, error) {
	value := strings.TrimSpace(raw)
	switch value {
	case "":
		return PriorityFilter{}, nil
	case PriorityFilterNoPriorityValue:
		return PriorityFilter{mode: priorityFilterNoPriority}, nil
	default:
		return PriorityFilter{mode: priorityFilterKey, key: value}, nil
	}
}
