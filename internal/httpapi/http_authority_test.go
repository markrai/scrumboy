package httpapi

import "testing"

func TestParseHTTPAuthority(t *testing.T) {
	t.Parallel()

	accepted := []struct {
		authority string
		hostname  string
	}{
		{authority: "example.com", hostname: "example.com"},
		{authority: "example.com:8443", hostname: "example.com"},
		{authority: "example.com:1", hostname: "example.com"},
		{authority: "example.com:65535", hostname: "example.com"},
		{authority: "127.0.0.1:8080", hostname: "127.0.0.1"},
		{authority: "localhost", hostname: "localhost"},
		{authority: "[::1]", hostname: "::1"},
		{authority: "[::1]:8080", hostname: "::1"},
		{authority: "[2001:db8::1]:443", hostname: "2001:db8::1"},
	}
	for _, tt := range accepted {
		t.Run("accept_"+tt.authority, func(t *testing.T) {
			authority, hostname, ok := parseHTTPAuthority(tt.authority)
			if !ok {
				t.Fatalf("parseHTTPAuthority(%q) unexpectedly rejected", tt.authority)
			}
			if authority != tt.authority || hostname != tt.hostname {
				t.Fatalf("parseHTTPAuthority(%q) = (%q, %q, true), want (%q, %q, true)",
					tt.authority, authority, hostname, tt.authority, tt.hostname)
			}
		})
	}

	rejected := []string{
		"",
		" example.com",
		"example.com ",
		"example\t.com",
		"example\n.com",
		"example\x00.com",
		"example\u00a0.com",
		"https://evil.example",
		"evil.example/path",
		`evil.example\path`,
		"user@evil.example",
		"evil.example?query",
		"evil.example#fragment",
		"host1.example,host2.example",
		"evil%2f.example",
		"evil%40example.com",
		"%65vil.example",
		"evil.example:",
		"evil.example:0",
		"evil.example:65536",
		"evil.example:99999",
		"evil.example:bad",
		"evil.example:-1",
		"evil.example:+443",
		"evil.example:80:90",
		"::1",
		"[::1",
		"::1]",
		"[]",
		"[example.com]",
		"[127.0.0.1]",
		"[::1]extra",
		"[::1]:",
		"[::1]:0",
		"[::1]:65536",
		"[::1]:bad",
		`evil".example`,
	}
	for _, raw := range rejected {
		t.Run("reject_"+raw, func(t *testing.T) {
			if authority, hostname, ok := parseHTTPAuthority(raw); ok {
				t.Fatalf("parseHTTPAuthority(%q) = (%q, %q, true), want rejection", raw, authority, hostname)
			}
		})
	}
}
