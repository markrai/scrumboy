package publicorigin

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"
)

func TestResolverOriginTrustLadder(t *testing.T) {
	t.Run("configured origin wins and canonicalizes case", func(t *testing.T) {
		resolver := New("HTTPS://VEGA.EXAMPLE.COM:8443", false)
		req := httptest.NewRequest("GET", "http://attacker.example/", nil)
		got, err := resolver.Origin(req)
		if err != nil || got != "https://vega.example.com:8443" {
			t.Fatalf("Origin() = %q, %v", got, err)
		}
	})

	t.Run("direct TLS uses validated host", func(t *testing.T) {
		resolver := New("", false)
		req := httptest.NewRequest("GET", "https://VEGA.EXAMPLE.COM:443/", nil)
		req.TLS = &tls.ConnectionState{}
		got, err := resolver.Origin(req)
		if err != nil || got != "https://vega.example.com:443" {
			t.Fatalf("Origin() = %q, %v", got, err)
		}
	})

	t.Run("trusted proxy requires forwarded HTTPS and host", func(t *testing.T) {
		resolver := New("", true)
		req := httptest.NewRequest("GET", "http://backend:8080/", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-Host", "VEGA.EXAMPLE.COM")
		got, err := resolver.Origin(req)
		if err != nil || got != "https://vega.example.com" {
			t.Fatalf("Origin() = %q, %v", got, err)
		}
	})

	t.Run("loopback HTTP is allowed", func(t *testing.T) {
		resolver := New("", false)
		req := httptest.NewRequest("GET", "http://LOCALHOST:8080/", nil)
		got, err := resolver.Origin(req)
		if err != nil || got != "http://localhost:8080" {
			t.Fatalf("Origin() = %q, %v", got, err)
		}
	})

	t.Run("untrusted non-loopback HTTP fails closed", func(t *testing.T) {
		resolver := New("", false)
		req := httptest.NewRequest("GET", "http://vega.example.com/", nil)
		if _, err := resolver.Origin(req); err == nil {
			t.Fatal("Origin() unexpectedly trusted plaintext non-loopback request")
		}
	})
}

func TestValidateMCPResourceNarrowCanonicalization(t *testing.T) {
	resolver := New("https://vega.example.com", false)
	req := httptest.NewRequest("GET", "http://ignored.invalid/", nil)

	accepted := []string{
		"https://vega.example.com/mcp/rpc",
		"HTTPS://VEGA.EXAMPLE.COM/mcp/rpc",
	}
	for _, raw := range accepted {
		got, err := resolver.ValidateMCPResource(req, raw)
		if err != nil || got != "https://vega.example.com/mcp/rpc" {
			t.Errorf("ValidateMCPResource(%q) = %q, %v", raw, got, err)
		}
	}

	rejected := []string{
		"/mcp/rpc",
		"https://vega.example.com/mcp/rpc/",
		"https://vega.example.com/MCP/rpc",
		"https://vega.example.com/mcp/%72pc",
		"https://vega.example.com/mcp/rpc?x=1",
		"https://vega.example.com/mcp/rpc#fragment",
		"https://user@vega.example.com/mcp/rpc",
		"https://vega.example.com:443/mcp/rpc",
		"https://other.example.com/mcp/rpc",
		"https://vega.example.com/mcp/../mcp/rpc",
	}
	for _, raw := range rejected {
		if got, err := resolver.ValidateMCPResource(req, raw); err == nil {
			t.Errorf("ValidateMCPResource(%q) = %q, expected rejection", raw, got)
		}
	}
}

func TestOriginAllowed(t *testing.T) {
	resolver := New("https://vega.example.com", false)
	for _, tc := range []struct {
		origin string
		want   bool
	}{
		{"", true},
		{"HTTPS://VEGA.EXAMPLE.COM", true},
		{"https://vega.example.com:443", false},
		{"https://other.example.com", false},
		{"null", false},
	} {
		req := httptest.NewRequest("POST", "http://ignored.invalid/mcp/rpc", nil)
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		got, err := resolver.OriginAllowed(req)
		if err != nil || got != tc.want {
			t.Errorf("OriginAllowed(%q) = %v, %v, want %v", tc.origin, got, err, tc.want)
		}
	}
}
