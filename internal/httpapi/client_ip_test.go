package httpapi

import (
	"net/http"
	"testing"
)

func TestClientIP_DefaultsToRemoteAddrIgnoresXFF(t *testing.T) {
	s := &Server{trustProxy: false}
	r := httptestRequest(t, "203.0.113.50:12345", "198.51.100.1")
	if got := s.clientIP(r); got != "203.0.113.50" {
		t.Fatalf("clientIP = %q, want RemoteAddr host 203.0.113.50", got)
	}
}

func TestClientIP_TrustProxyUsesFirstXFFHop(t *testing.T) {
	s := &Server{trustProxy: true}
	r := httptestRequest(t, "203.0.113.50:12345", "198.51.100.1, 203.0.113.50")
	if got := s.clientIP(r); got != "198.51.100.1" {
		t.Fatalf("clientIP = %q, want first XFF hop 198.51.100.1", got)
	}
}

func TestClientIP_TrustProxyFallsBackToRemoteAddr(t *testing.T) {
	s := &Server{trustProxy: true}
	r := httptestRequest(t, "203.0.113.50:12345", "")
	if got := s.clientIP(r); got != "203.0.113.50" {
		t.Fatalf("clientIP = %q, want RemoteAddr host when XFF empty", got)
	}
}

func TestClientIP_TrustProxySplitsHostPortInXFF(t *testing.T) {
	s := &Server{trustProxy: true}
	r := httptestRequest(t, "203.0.113.50:12345", "198.51.100.9:8443")
	if got := s.clientIP(r); got != "198.51.100.9" {
		t.Fatalf("clientIP = %q, want host without port", got)
	}
}

func httptestRequest(t *testing.T, remoteAddr, xff string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, "http://example.test/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	r.RemoteAddr = remoteAddr
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}
