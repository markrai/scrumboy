package assetrefs

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// MaxRemapCandidates bounds the work RemapImageDestinations does on one body.
const MaxRemapCandidates = 2000

var (
	// ErrTooManyReferences reports a body with more remap candidates than MaxRemapCandidates.
	ErrTooManyReferences = errors.New("assetrefs: too many asset references in one note")
	// ErrRemapInvariant reports that rewriting would change something other than image destinations.
	ErrRemapInvariant = errors.New("assetrefs: remap postcondition failed")
)

// RemapImageDestinations rewrites only the occurrences of "/api/assets/<old>" that are Markdown
// image destinations (inline, angle-bracket, or a link reference definition used by an image).
// Prose, code spans, fenced and indented code, raw HTML, autolinks and ordinary link destinations
// are preserved byte for byte. unmapped lists old IDs that Extract(body) reports as embedded but
// that could not be located for rewriting (for example entity-encoded destinations).
func RemapImageDestinations(body string, mapping map[string]string) (string, []string, error) {
	for old, repl := range mapping {
		if !IsCanonicalUUID(old) || !IsCanonicalUUID(repl) {
			return "", nil, fmt.Errorf("assetrefs: mapping entries must be canonical uuids")
		}
	}
	type candidate struct {
		offset int // start of the UUID within body
		old    string
	}
	var cands []candidate
	for i := 0; ; {
		j := strings.Index(body[i:], CanonicalPathPrefix)
		if j < 0 {
			break
		}
		start := i + j + len(CanonicalPathPrefix)
		i = start
		if start+36 > len(body) {
			continue
		}
		id := body[start : start+36]
		if _, ok := mapping[id]; !ok {
			continue
		}
		if end := start + 36; end < len(body) && (isLowerHex(body[end]) || body[end] == '-') {
			continue
		}
		cands = append(cands, candidate{offset: start, old: id})
		if len(cands) > MaxRemapCandidates {
			return "", nil, ErrTooManyReferences
		}
	}

	before := Extract(body)
	if len(cands) == 0 {
		return body, mappedIn(before, mapping), nil
	}

	// Replace every candidate with a distinct sentinel of identical shape. Equal length, identical
	// dash positions and lowercase hex elsewhere cannot change Markdown structure, so the parse of
	// probe tells us exactly which candidates are image destinations.
	sentinels := make([]string, len(cands))
	probe := []byte(body)
	for k, c := range cands {
		s, err := uuid.NewRandom()
		if err != nil {
			return "", nil, fmt.Errorf("assetrefs: sentinel: %w", err)
		}
		sentinels[k] = s.String()
		copy(probe[c.offset:c.offset+36], sentinels[k])
	}
	hits := map[string]struct{}{}
	for _, id := range Extract(string(probe)) {
		hits[id] = struct{}{}
	}

	out := []byte(body)
	rewritten := map[string]struct{}{}
	for k, c := range cands {
		if _, ok := hits[sentinels[k]]; !ok {
			continue
		}
		copy(out[c.offset:c.offset+36], mapping[c.old])
		rewritten[c.old] = struct{}{}
	}
	result := string(out)

	// Postcondition: the embedded set changes only by the mapping of rewritten IDs.
	want := make([]string, 0, len(before))
	seen := map[string]struct{}{}
	for _, id := range before {
		if _, ok := rewritten[id]; ok {
			id = mapping[id]
		}
		if _, dup := seen[id]; !dup {
			seen[id] = struct{}{}
			want = append(want, id)
		}
	}
	if !slices.Equal(Extract(result), want) {
		return "", nil, ErrRemapInvariant
	}

	var unmapped []string
	for _, id := range mappedIn(before, mapping) {
		if _, ok := rewritten[id]; !ok {
			unmapped = append(unmapped, id)
		}
	}
	return result, unmapped, nil
}

func mappedIn(ids []string, mapping map[string]string) []string {
	var out []string
	for _, id := range ids {
		if _, ok := mapping[id]; ok {
			out = append(out, id)
		}
	}
	return out
}
