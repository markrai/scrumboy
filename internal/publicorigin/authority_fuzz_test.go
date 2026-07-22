package publicorigin

import (
	"strconv"
	"strings"
	"testing"
)

func FuzzParseHTTPAuthority(f *testing.F) {
	seeds := []string{
		"example.com",
		"example.com:8443",
		"example.com:1",
		"example.com:65535",
		"127.0.0.1:8080",
		"localhost",
		"[::1]",
		"[::1]:8080",
		"[2001:db8::1]:443",
		"",
		" example.com",
		"example.com ",
		"example\x00.com",
		"https://evil.example",
		"evil.example/path",
		"user@evil.example",
		"evil.example?query",
		"evil.example#fragment",
		"host1.example,host2.example",
		"evil%2f.example",
		"evil.example:",
		"evil.example:0",
		"evil.example:65536",
		"evil.example:bad",
		"::1",
		"[::1",
		"::1]",
		"[]",
		"[::1]extra",
		"[::1]:0",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		authority, hostname, ok := ParseHTTPAuthority(raw)

		if !ok {
			if authority != "" || hostname != "" {
				t.Fatalf("ParseHTTPAuthority(%q) rejected but returned (%q, %q)", raw, authority, hostname)
			}
			return
		}

		if authority != raw {
			t.Fatalf("ParseHTTPAuthority(%q) returned authority %q, want == input", raw, authority)
		}
		if hostname == "" {
			t.Fatalf("ParseHTTPAuthority(%q) accepted with empty hostname", raw)
		}
		if !strings.Contains(raw, hostname) {
			t.Fatalf("ParseHTTPAuthority(%q) hostname %q not contained in input", raw, hostname)
		}

		authority2, hostname2, ok2 := ParseHTTPAuthority(raw)
		if !ok2 || authority2 != authority || hostname2 != hostname {
			t.Fatalf("ParseHTTPAuthority(%q) not deterministic: (%q,%q,%v) vs (%q,%q,%v)",
				raw, authority, hostname, ok, authority2, hostname2, ok2)
		}

		var portPart string
		if strings.HasPrefix(authority, "[") {
			if idx := strings.IndexByte(authority, ']'); idx >= 0 {
				portPart = authority[idx+1:]
			}
		} else if idx := strings.LastIndexByte(authority, ':'); idx >= 0 {
			portPart = authority[idx:]
		}
		if portPart != "" {
			port, err := strconv.Atoi(strings.TrimPrefix(portPart, ":"))
			if err != nil || port < 1 || port > 65535 {
				t.Fatalf("ParseHTTPAuthority(%q) accepted out-of-range port %q", raw, portPart)
			}
		}
	})
}
