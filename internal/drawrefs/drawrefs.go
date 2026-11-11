package drawrefs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"scrumboy/internal/assetrefs"
)

var (
	idRE        = regexp.MustCompile(`^drw_[A-Za-z0-9_-]{6,64}$`)
	widthRE     = regexp.MustCompile(`\|([1-9][0-9]{1,3})$`)
	tagRE       = regexp.MustCompile(`\|draw:(drw_[A-Za-z0-9_-]{6,64})$`)
	pathRE      = regexp.MustCompile(`^/api/board/([A-Za-z0-9_-]{1,128})/drawings/(drw_[A-Za-z0-9_-]{6,64})$`)
	occurrences = regexp.MustCompile(`drw_[A-Za-z0-9_-]{6,64}`)
	pathPrefix  = regexp.MustCompile(`/api/board/[A-Za-z0-9_-]{1,128}/drawings/$`)
	slugRE      = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

const (
	minWidth = 48
	maxWidth = 1600

	MaxRemapBodyBytes  = 1 << 20
	MaxRemapCandidates = 2000
)

var (
	ErrRemapInvariant    = errors.New("drawrefs: remap postcondition failed")
	ErrTooManyReferences = errors.New("drawrefs: too many drawing reference candidates")
	ErrNoteTooLarge      = errors.New("drawrefs: note is too long to remap its drawings")
	ErrInvalidMapping    = errors.New("drawrefs: invalid mapping")
)

var markdown = goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough))

func IsDrawingID(s string) bool { return idRE.MatchString(s) }

func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "drw_" + hex.EncodeToString(b[:])
}

func AltDrawingID(alt string) (string, bool) {
	rest := alt
	if m := widthRE.FindStringSubmatchIndex(rest); m != nil {
		n := 0
		for _, c := range rest[m[2]:m[3]] {
			n = n*10 + int(c-'0')
		}
		if n >= minWidth && n <= maxWidth {
			rest = rest[:m[0]]
		}
	}
	m := tagRE.FindStringSubmatch(rest)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func PathDrawingID(dest string) (slug string, id string, ok bool) {
	m := pathRE.FindStringSubmatch(dest)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

type image struct {
	alt  string
	dest string
}

func images(body string) []image {
	if !strings.Contains(body, "!") || !strings.Contains(body, "drw_") {
		return nil
	}
	src := []byte(body)
	doc := markdown.Parser().Parse(text.NewReader(src))
	var out []image
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if v, ok := n.(*ast.Image); ok {
			out = append(out, image{alt: altText(v, src), dest: decodeDestination(v.Destination)})
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return out
}

func altText(n ast.Node, src []byte) string {
	var b strings.Builder
	var walk func(ast.Node)
	walk = func(node ast.Node) {
		for c := node.FirstChild(); c != nil; c = c.NextSibling() {
			switch t := c.(type) {
			case *ast.Text:
				b.Write(t.Segment.Value(src))
			case *ast.String:
				b.Write(t.Value)
			default:
				walk(c)
			}
		}
	}
	walk(n)
	return b.String()
}

func decodeDestination(raw []byte) string {
	d := util.UnescapePunctuations(raw)
	d = util.ResolveNumericReferences(d)
	d = util.ResolveEntityNames(d)
	return string(d)
}

func Extract(body string) []string {
	var out []string
	seen := map[string]struct{}{}
	add := func(id string) {
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, img := range images(body) {
		_, pathID, isPath := PathDrawingID(img.dest)
		_, isAsset := assetrefs.CanonicalAssetID(img.dest)
		if !isPath && !isAsset {
			continue
		}
		if id, ok := AltDrawingID(img.alt); ok {
			add(id)
		}
		if isPath {
			add(pathID)
		}
	}
	return out
}

func StandaloneIDs(body string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, img := range images(body) {
		if _, id, ok := PathDrawingID(img.dest); ok {
			out[id] = struct{}{}
		}
	}
	return out
}

type candidate struct {
	start, end int
	old        string
	isPath     bool
}

func Remap(body string, mapping map[string]string, destSlug string) (string, error) {
	if !slugRE.MatchString(destSlug) {
		return "", ErrInvalidMapping
	}
	for old, repl := range mapping {
		if !IsDrawingID(old) || !IsDrawingID(repl) {
			return "", ErrInvalidMapping
		}
	}
	before := Extract(body)
	need := false
	for _, id := range before {
		if _, ok := mapping[id]; ok {
			need = true
			break
		}
	}
	if !need {
		return body, nil
	}
	if len(body) > MaxRemapBodyBytes {
		return "", ErrNoteTooLarge
	}
	var cands []candidate
	for _, loc := range occurrences.FindAllStringIndex(body, -1) {
		old := body[loc[0]:loc[1]]
		if _, ok := mapping[old]; !ok {
			continue
		}
		if loc[1] < len(body) && isIDChar(body[loc[1]]) {
			continue
		}
		if loc[0] > 0 && isIDChar(body[loc[0]-1]) {
			continue
		}
		c := candidate{start: loc[0], end: loc[1], old: old}
		prefixFrom := loc[0] - 200
		if prefixFrom < 0 {
			prefixFrom = 0
		}
		if m := pathPrefix.FindStringIndex(body[prefixFrom:loc[0]]); m != nil && prefixFrom+m[1] == loc[0] {
			c.start = prefixFrom + m[0]
			c.isPath = true
		}
		cands = append(cands, c)
		if len(cands) > MaxRemapCandidates {
			return "", ErrTooManyReferences
		}
	}
	sentinels := make([]string, len(cands))
	probeParts := make([]string, len(cands))
	for k, c := range cands {
		sentinels[k] = NewID()
		if c.isPath {
			probeParts[k] = "/api/board/" + destSlug + "/drawings/" + sentinels[k]
		} else {
			probeParts[k] = sentinels[k]
		}
	}
	probe := splice(body, cands, func(k int) string { return probeParts[k] })
	hits := map[string]struct{}{}
	for _, id := range Extract(probe) {
		hits[id] = struct{}{}
	}
	out := splice(body, cands, func(k int) string {
		c := cands[k]
		if _, ok := hits[sentinels[k]]; !ok {
			return body[c.start:c.end]
		}
		if c.isPath {
			return "/api/board/" + destSlug + "/drawings/" + mapping[c.old]
		}
		return mapping[c.old]
	})
	want := map[string]struct{}{}
	for _, id := range before {
		if repl, ok := mapping[id]; ok {
			want[repl] = struct{}{}
		} else {
			want[id] = struct{}{}
		}
	}
	after := Extract(out)
	if len(after) != len(want) {
		return "", ErrRemapInvariant
	}
	for _, id := range after {
		if _, ok := want[id]; !ok {
			return "", ErrRemapInvariant
		}
	}
	for _, img := range images(out) {
		if slug, id, ok := PathDrawingID(img.dest); ok {
			if _, remapped := mappingValues(mapping)[id]; remapped && slug != destSlug {
				return "", ErrRemapInvariant
			}
		}
	}
	return out, nil
}

func mappingValues(m map[string]string) map[string]struct{} {
	out := make(map[string]struct{}, len(m))
	for _, v := range m {
		out[v] = struct{}{}
	}
	return out
}

func splice(body string, cands []candidate, part func(int) string) string {
	var b strings.Builder
	b.Grow(len(body) + len(cands)*48)
	prev := 0
	for k, c := range cands {
		b.WriteString(body[prev:c.start])
		b.WriteString(part(k))
		prev = c.end
	}
	b.WriteString(body[prev:])
	return b.String()
}

func isIDChar(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
}
