// Package assetrefs is the single server-side authority for which Scrumboy-managed image assets a
// Markdown note embeds. Its rules must match the browser renderer in
// internal/httpapi/web/modules/markdown-preview.ts (markdown-it "default" preset, html:false,
// linkify:false); testdata/fixtures.json is the shared parity corpus.
package assetrefs

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// CanonicalPathPrefix is the only Markdown image destination form that embeds a Scrumboy asset.
const CanonicalPathPrefix = "/api/assets/"

// markdown-it "default" enables tables and strikethrough; linkify and raw HTML stay off there,
// and goldmark never turns raw HTML into Image nodes.
var markdown = goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough))

// Extract returns the distinct canonical asset UUIDs embedded as Markdown images in body, in
// first-appearance order. It never errors; unparseable input yields no references.
func Extract(body string) []string {
	if !strings.Contains(body, "!") {
		return nil
	}
	return canonicalIDs(imageDestinations(body))
}

// imageDestinations returns the decoded destination of every outermost Markdown image.
// Images nested in another image's alt text are skipped: markdown-it renders them as text.
func imageDestinations(body string) []string {
	images, _ := destinations(body, false)
	return images
}

// destinations parses body once and returns the decoded destinations of every outermost image
// and, when withLinks is set, of every ordinary link and autolink outside image alt text, each
// in document order.
func destinations(body string, withLinks bool) (images, links []string) {
	src := []byte(body)
	doc := markdown.Parser().Parse(text.NewReader(src))
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Image:
			images = append(images, decodeDestination(v.Destination))
			return ast.WalkSkipChildren, nil
		case *ast.Link:
			if withLinks {
				links = append(links, decodeDestination(v.Destination))
			}
		case *ast.AutoLink:
			if withLinks {
				links = append(links, string(v.URL(src)))
			}
		}
		return ast.WalkContinue, nil
	})
	return images, links
}

// canonicalIDs returns the distinct canonical asset UUIDs among dests, in first-appearance order.
func canonicalIDs(dests []string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, dest := range dests {
		id, ok := CanonicalAssetID(dest)
		if !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// decodeDestination applies the same backslash-escape and character-reference decoding that
// markdown-it applies to link destinations before rendering.
func decodeDestination(raw []byte) string {
	d := util.UnescapePunctuations(raw)
	d = util.ResolveNumericReferences(d)
	d = util.ResolveEntityNames(d)
	return string(d)
}

// CanonicalAssetID returns the asset UUID if dest is exactly "/api/assets/<lowercase uuidv4>".
func CanonicalAssetID(dest string) (string, bool) {
	if !strings.HasPrefix(dest, CanonicalPathPrefix) {
		return "", false
	}
	id := dest[len(CanonicalPathPrefix):]
	if !IsCanonicalUUID(id) {
		return "", false
	}
	return id, true
}

// IsCanonicalUUID reports whether s is a lowercase canonical UUIDv4.
func IsCanonicalUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := range 36 {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !isLowerHex(c) {
				return false
			}
		}
	}
	if s[14] != '4' {
		return false
	}
	switch s[19] {
	case '8', '9', 'a', 'b':
		return true
	}
	return false
}

func isLowerHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}
