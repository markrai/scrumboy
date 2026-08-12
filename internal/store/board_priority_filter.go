package store

import (
	"strings"
)

// ParsePriorityFilter validates the public board priority filter grammar.
//
// Empty input applies no filter, "none" matches todos without a priority_key,
// and any other value matches that literal priority_key (not validated against
// project_priorities; an unmatched key simply yields zero rows).
func ParsePriorityFilter(raw string) (PriorityFilter, error) {
	value := strings.TrimSpace(raw)
	switch value {
	case "":
		return PriorityFilter{}, nil
	case "none":
		return PriorityFilter{mode: priorityFilterNoPriority}, nil
	default:
		return PriorityFilter{mode: priorityFilterKey, key: value}, nil
	}
}
