package board

import (
	"errors"
	"strings"
)

// MaxTagFilters is the transport-level cap on unique board tag filters.
const MaxTagFilters = 20

// ErrTooManyTagFilters is returned when more than MaxTagFilters unique
// non-empty tag filters remain after transport-level normalization.
var ErrTooManyTagFilters = errors.New("too many tag filters")

// NormalizeTagFilters performs transport-level normalization only. Canonical
// durable identity remains store-owned because temporary boards intentionally
// use exact stored names.
//
// Normalization trims surrounding whitespace, drops empty strings, preserves
// first-seen order, and suppresses case-insensitive duplicates so the first
// spelling wins.
func NormalizeTagFilters(values []string) ([]string, error) {
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
		if len(out) >= MaxTagFilters {
			return nil, ErrTooManyTagFilters
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out, nil
}
