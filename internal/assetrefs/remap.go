package assetrefs

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/yuin/goldmark/util"
)

// MaxRemapCandidates bounds the work RemapImageDestinations does on one body.
const MaxRemapCandidates = 2000

// MaxRemapBodyBytes is the longest note whose images RemapImageDestinations rewrites (50 times
// the 20,000-byte limit the editor enforces). Longer notes that embed a mapped image fail with
// ErrNoteTooLarge; longer notes without one are returned unchanged after a single parse.
const MaxRemapBodyBytes = 1 << 20

const (
	// maxSingleProbes bounds the one-candidate-at-a-time fallback probes (each is a full parse).
	maxSingleProbes = 64
	// singleProbeBudget bounds the body bytes all fallback probes of one call may parse, so the
	// fallback costs at most a small multiple of a single parse however long the body is.
	singleProbeBudget = 4 << 20
	// maxReferenceLen bounds how far a character reference ("&...;") is scanned for.
	maxReferenceLen = 64
	// decodeChunk is the target raw size decoded at once while looking for candidates; peak
	// decoder memory is proportional to it, not to the body.
	decodeChunk = 64 << 10
	// boundarySearch is how far back from a chunk's target end a safe split point is sought.
	boundarySearch = 4 << 10
	// candidateUnits is the decoded length of "/api/assets/" plus a UUID.
	candidateUnits = len(CanonicalPathPrefix) + 36
)

var (
	// ErrTooManyReferences reports a body with more remap candidates than MaxRemapCandidates.
	ErrTooManyReferences = errors.New("assetrefs: too many asset references in one note")
	// ErrRemapInvariant reports that rewriting would change something other than image destinations.
	ErrRemapInvariant = errors.New("assetrefs: remap postcondition failed")
	// ErrUnmappedReference reports an embedded image (per Extract) whose destination is in the
	// mapping but could not be located for rewriting. Callers must fail rather than drop it.
	ErrUnmappedReference = errors.New("assetrefs: embedded image reference could not be remapped")
	// ErrNoteTooLarge reports a note over MaxRemapBodyBytes that embeds a mapped image.
	ErrNoteTooLarge = errors.New("assetrefs: note is too long to remap its images")
	// ErrSharedLinkDestination reports that rewriting an image destination would also change an
	// ordinary link (for example, a link reference definition shared by an image and a link).
	ErrSharedLinkDestination = errors.New("assetrefs: an ordinary link shares the image destination")
)

// RemapImageDestinations rewrites every Markdown image destination that Extract reports as
// embedding a mapped asset (inline, angle-bracket, or a link reference definition used by an
// image), including destinations written with backslash escapes or character references; the
// UUID part of such a destination is rewritten as plain text. Prose, code spans, fenced and
// indented code, raw HTML, autolinks and ordinary link destinations are preserved byte for byte.
//
// It never drops a reference silently and never retargets an ordinary link: if an embedded
// mapped ID cannot be rewritten it returns ErrUnmappedReference; if a rewrite would also change
// an ordinary link's destination it returns ErrSharedLinkDestination; if the result would embed
// anything other than the mapped set it returns ErrRemapInvariant.
//
// Cost: at most three full Markdown parses of at most MaxRemapBodyBytes plus a fallback bounded
// by singleProbeBudget bytes, and decoder memory bounded by decodeChunk; a longer note costs one
// parse.
func RemapImageDestinations(body string, mapping map[string]string) (string, error) {
	for old, repl := range mapping {
		if !IsCanonicalUUID(old) || !IsCanonicalUUID(repl) {
			return "", fmt.Errorf("assetrefs: mapping entries must be canonical uuids")
		}
	}
	if !strings.Contains(body, "!") {
		return body, nil
	}
	beforeImages, beforeLinks := destinations(body, true)
	before := canonicalIDs(beforeImages)
	need := mappedIn(before, mapping)
	if len(need) == 0 {
		return body, nil
	}
	if len(body) > MaxRemapBodyBytes {
		return "", ErrNoteTooLarge
	}

	cands, err := findCandidates(body, mapping)
	if err != nil {
		return "", err
	}

	// Replace every candidate's UUID with a distinct plain-text sentinel. Candidate spans hold
	// only characters that decode to lowercase hex and dashes, never at line start (the
	// "/api/assets/" prefix precedes them), so this cannot change Markdown structure; the parse
	// of the probe tells us which candidates are image destinations.
	sentinels := make([]string, len(cands))
	for k := range cands {
		s, err := uuid.NewRandom()
		if err != nil {
			return "", fmt.Errorf("assetrefs: sentinel: %w", err)
		}
		sentinels[k] = s.String()
	}
	hit := make([]bool, len(cands))
	all := make([]int, len(cands))
	for k := range cands {
		all[k] = k
	}
	probeHits := extractSet(splice(body, cands, all, sentinels))
	covered := map[string]bool{}
	for k, c := range cands {
		if probeHits[sentinels[k]] {
			hit[k] = true
			covered[c.old] = true
		}
	}

	// A batch probe misses a destination whose ID also appears in the reference label it is
	// resolved through (distinct sentinels break the label match). Probe those candidates one at
	// a time, within a parse budget.
	probes := min(maxSingleProbes, singleProbeBudget/max(len(body), 1))
	for k, c := range cands {
		if hit[k] || covered[c.old] {
			continue
		}
		if probes <= 0 {
			break
		}
		probes--
		if extractSet(splice(body, cands, []int{k}, sentinels))[sentinels[k]] {
			hit[k] = true
		}
	}
	for k, c := range cands {
		if hit[k] {
			covered[c.old] = true
		}
	}
	var missing []string
	for _, id := range need {
		if !covered[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("%w: %s", ErrUnmappedReference, strings.Join(missing, ", "))
	}

	var rewrite []int
	repl := make([]string, len(cands))
	for k, c := range cands {
		if hit[k] {
			rewrite = append(rewrite, k)
			repl[k] = mapping[c.old]
		}
	}
	result := splice(body, cands, rewrite, repl)

	// Postconditions: ordinary links keep their destinations, and the embedded set is exactly
	// the input set with every mapped ID replaced.
	afterImages, afterLinks := destinations(result, true)
	if !slices.Equal(beforeLinks, afterLinks) {
		return "", ErrSharedLinkDestination
	}
	want := make([]string, 0, len(before))
	seen := map[string]struct{}{}
	for _, id := range before {
		if r, ok := mapping[id]; ok {
			id = r
		}
		if _, dup := seen[id]; !dup {
			seen[id] = struct{}{}
			want = append(want, id)
		}
	}
	if !slices.Equal(canonicalIDs(afterImages), want) {
		return "", ErrRemapInvariant
	}
	return result, nil
}

// candidate is a raw byte span of body whose decoded text is a mapped UUID that follows a
// decoded "/api/assets/" prefix.
type candidate struct {
	start, end int
	old        string
}

// unit is one decoded character of body (after the same backslash-escape and character
// reference passes as decodeDestination) and the raw byte span it came from.
type unit struct {
	s          string
	start, end int
}

// findCandidates locates, in raw byte offsets, every UUID that decodes to a mapped ID directly
// after a decoded "/api/assets/". The body is decoded in chunks of about decodeChunk bytes; the
// last candidateUnits-1 units of each chunk are carried into the next so no candidate is split.
func findCandidates(body string, mapping map[string]string) ([]candidate, error) {
	var cands []candidate
	// One buffer is reused for every chunk: carried units, then the chunk's decoded units.
	units := make([]unit, 0, min(len(body), decodeChunk)+candidateUnits)
	lastEnd := 0
	for pos := 0; pos < len(body); {
		end := chunkEnd(body, pos)
		units = decodeUnits(units, body[pos:end], pos)
		i := 0
		for ; i+candidateUnits <= len(units); i++ {
			if units[i].start < lastEnd || !unitsSpell(units[i:], CanonicalPathPrefix) {
				continue
			}
			c, ok := matchUUID(units, i+len(CanonicalPathPrefix), mapping)
			if !ok {
				continue
			}
			cands = append(cands, c)
			if len(cands) > MaxRemapCandidates {
				return nil, ErrTooManyReferences
			}
			lastEnd = c.end
			i += candidateUnits - 1
		}
		if end == len(body) {
			break
		}
		// Starts from i on were not complete in this chunk; carry them (fewer than
		// candidateUnits units) into the next one.
		units = units[:copy(units, units[min(i, len(units)):])]
		pos = end
	}
	return cands, nil
}

// matchUUID reports whether the 36 units at k spell a mapped UUID not followed by more hex or
// dashes, and returns its raw span.
func matchUUID(units []unit, k int, mapping map[string]string) (candidate, bool) {
	var id [36]byte
	for j := range 36 {
		s := units[k+j].s
		if len(s) != 1 {
			return candidate{}, false
		}
		id[j] = s[0]
	}
	old := string(id[:])
	if _, ok := mapping[old]; !ok {
		return candidate{}, false
	}
	if next := k + 36; next < len(units) {
		if s := units[next].s; len(s) == 1 && (isLowerHex(s[0]) || s[0] == '-') {
			return candidate{}, false
		}
	}
	return candidate{start: units[k].start, end: units[k+35].end, old: old}, true
}

// chunkEnd picks where the chunk starting at pos ends. It prefers a split right after a byte
// that cannot belong to a backslash escape or character reference (anything but alphanumerics
// and \ & # ;), so decoding each chunk alone equals decoding the whole body. Without such a byte
// it splits anyway: a candidate mis-decoded at a forced split is either rejected by the probe
// parse or, if needed, reported by the coverage check (fail closed).
func chunkEnd(body string, pos int) int {
	target := pos + decodeChunk
	if target >= len(body) {
		return len(body)
	}
	for i := target; i > target-boundarySearch; i-- {
		if c := body[i-1]; !(isAlphaNumeric(c) || c == '\\' || c == '&' || c == '#' || c == ';') {
			return i
		}
	}
	return target
}

func isAlphaNumeric(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func unitsSpell(units []unit, s string) bool {
	if len(units) < len(s) {
		return false
	}
	for k := range len(s) {
		if units[k].s != s[k:k+1] {
			return false
		}
	}
	return true
}

// decodeUnits appends to dst the units of chunk (which starts at raw offset base), mirroring
// decodeDestination (UnescapePunctuations, ResolveNumericReferences, ResolveEntityNames, in that
// order). Units already in dst are kept as they are.
func decodeUnits(dst []unit, chunk string, base int) []unit {
	prefix := len(dst)
	units := dst
	// Backslash escapes of ASCII punctuation.
	for i := 0; i < len(chunk); i++ {
		if chunk[i] == '\\' && i+1 < len(chunk) && util.IsPunct(chunk[i+1]) {
			units = append(units, unit{s: chunk[i+1 : i+2], start: base + i, end: base + i + 2})
			i++
			continue
		}
		units = append(units, unit{s: chunk[i : i+1], start: base + i, end: base + i + 1})
	}
	tail := resolveReferences(units[prefix:], util.ResolveNumericReferences)
	tail = resolveReferences(tail, util.ResolveEntityNames)
	return units[:prefix+len(tail)]
}

// resolveReferences merges, in place, each "&...;" run that resolve decodes into a single unit.
func resolveReferences(units []unit, resolve func([]byte) []byte) []unit {
	out := units[:0]
	var raw []byte
	for i := 0; i < len(units); i++ {
		if units[i].s != "&" {
			out = append(out, units[i])
			continue
		}
		j := i + 1
		for j < len(units) && j-i < maxReferenceLen && units[j].s != ";" && units[j].s != "&" {
			j++
		}
		if j >= len(units) || units[j].s != ";" {
			out = append(out, units[i])
			continue
		}
		raw = raw[:0]
		for _, x := range units[i : j+1] {
			raw = append(raw, x.s...)
		}
		dec := resolve(raw)
		if string(dec) == string(raw) {
			out = append(out, units[i])
			continue
		}
		out = append(out, unit{s: string(dec), start: units[i].start, end: units[j].end})
		i = j
	}
	return out
}

// splice returns body with cands[k] replaced by repl[k] for each k in which (ascending).
func splice(body string, cands []candidate, which []int, repl []string) string {
	var b strings.Builder
	b.Grow(len(body))
	pos := 0
	for _, k := range which {
		b.WriteString(body[pos:cands[k].start])
		b.WriteString(repl[k])
		pos = cands[k].end
	}
	b.WriteString(body[pos:])
	return b.String()
}

func extractSet(body string) map[string]bool {
	out := map[string]bool{}
	for _, id := range Extract(body) {
		out[id] = true
	}
	return out
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
