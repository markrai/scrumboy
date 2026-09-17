package httpapi

import (
	"fmt"
	"strings"
)

const maxBoardTagFilters = 20

// parseBoardTagFilters performs transport-level normalization only. Canonical
// durable identity remains store-owned because temporary boards intentionally
// use exact stored names.
func parseBoardTagFilters(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		if len(out) >= maxBoardTagFilters {
			return nil, fmt.Errorf("too many tag filters")
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out, nil
}
