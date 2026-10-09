package store

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const slugAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
const maxSlugLen = 32

// reservedProjectSlugs is the single store-authoritative list of top-level
// application and localized landing paths that cannot safely identify a
// public project. Existing rows are not renamed; durable allocation skips the
// set and publication rejects legacy collisions.
var reservedProjectSlugs = map[string]struct{}{
	"_app":  {},
	"agora": {}, "anon": {}, "api": {}, "auth": {}, "dashboard": {},
	"healthz": {}, "mcp": {}, "oauth": {}, "p": {}, "temp": {},
	// English is the apex landing locale, pseudo is a claimed QA locale, and
	// the remaining entries are generated localized landing paths.
	"en": {}, "pseudo": {},
	"ar": {}, "bn": {}, "de": {}, "es": {}, "fa": {}, "fr": {},
	"hi": {}, "id": {}, "it": {}, "ja": {}, "ko": {}, "ms": {},
	"pl": {}, "pt": {}, "ru": {}, "sw": {}, "th": {}, "tr": {},
	"uk": {}, "ur": {}, "vi": {}, "zh": {},
}

// IsReservedProjectSlug reports whether slug conflicts with an application or
// landing route. It is exported so the public application resolver and HTTP
// route-synchronization tests use exactly the same policy as allocation.
func IsReservedProjectSlug(slug string) bool {
	_, reserved := reservedProjectSlugs[strings.ToLower(strings.TrimSpace(slug))]
	return reserved
}

func randomSlug(n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("invalid slug length")
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = slugAlphabet[int(b[i])%len(slugAlphabet)]
	}
	return string(out), nil
}

// generateSlugFromName generates a URL-friendly slug from a project name.
func generateSlugFromName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("name cannot be empty")
	}
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "_", "-")
	var builder strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' {
			if r <= 127 {
				builder.WriteRune(r)
			}
		} else {
			builder.WriteRune('-')
		}
	}
	name = builder.String()
	re := regexp.MustCompile(`-+`)
	name = re.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if len(name) == 0 {
		return "", fmt.Errorf("name cannot produce a valid slug")
	}
	if len(name) > maxSlugLen {
		name = name[:maxSlugLen]
		name = strings.TrimSuffix(name, "-")
	}
	if len(name) == 0 {
		return "", fmt.Errorf("name cannot produce a valid slug")
	}
	hasAlphanumeric := false
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			hasAlphanumeric = true
			break
		}
	}
	if !hasAlphanumeric {
		return "", fmt.Errorf("name cannot produce a valid slug")
	}
	return name, nil
}

func isValidSlug(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxSlugLen {
		return false
	}
	// Must be lowercase; no leading/trailing hyphen; no consecutive hyphens.
	if s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	prevHyphen := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' {
			if prevHyphen {
				return false
			}
			prevHyphen = true
			continue
		}
		prevHyphen = false
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}

// IsValidProjectSlug exposes the canonical project slug grammar to narrow
// application resolvers without duplicating it at transport boundaries.
func IsValidProjectSlug(slug string) bool {
	return isValidSlug(slug)
}
