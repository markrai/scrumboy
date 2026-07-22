package oidc

import (
	"strings"
	"testing"
)

func FuzzSanitizeReturnTo(f *testing.F) {
	seeds := []string{
		"",
		"/",
		"/dashboard",
		"/board?slug=abc",
		"/oauth/authorize?response_type=code&redirect_uri=https%3A%2F%2Fclient.example%2Fcallback&return_to=https://attacker.example&state=a%2Bb",
		"//evil.com",
		"https://evil.com",
		"/foo\\bar",
		"%2f%2f",
		"/%2f%2fevil.example/path",
		"/../../etc/passwd",
		"/foo/./bar",
		"/foo\nbar",
		"/foo\x00bar",
		"/foo#bar",
		"dashboard",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		got := SanitizeReturnTo(raw)

		if got == "" {
			t.Fatalf("SanitizeReturnTo(%q) returned empty string", raw)
		}
		if !strings.HasPrefix(got, "/") {
			t.Fatalf("SanitizeReturnTo(%q) = %q does not start with '/'", raw, got)
		}
		if strings.HasPrefix(got, "//") {
			t.Fatalf("SanitizeReturnTo(%q) = %q is scheme-relative", raw, got)
		}
		if strings.ContainsAny(got, "\\\r\n\x00") || strings.Contains(got, "#") {
			t.Fatalf("SanitizeReturnTo(%q) = %q retained a rejected character", raw, got)
		}

		path := got
		if idx := strings.IndexByte(got, '?'); idx >= 0 {
			path = got[:idx]
		}
		if strings.Contains(path, "://") {
			t.Fatalf("SanitizeReturnTo(%q) = %q has absolute-URL path", raw, got)
		}
		for _, seg := range strings.Split(path, "/") {
			if seg == "." || seg == ".." {
				t.Fatalf("SanitizeReturnTo(%q) = %q retained dot segment", raw, got)
			}
		}
	})
}
