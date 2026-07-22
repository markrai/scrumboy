package oidc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiscoverProvider(t *testing.T) {
	tests := []struct {
		name             string
		advertisedIssuer func(base string) string
		wantErr          bool
		wantRequests     int
	}{
		{
			name: "canonical issuer succeeds first attempt",
			advertisedIssuer: func(base string) string {
				return base
			},
			wantRequests: 1,
		},
		{
			name: "trailing slash issuer succeeds second attempt",
			advertisedIssuer: func(base string) string {
				return base + "/"
			},
			wantRequests: 2,
		},
		{
			name: "mismatched host fails",
			advertisedIssuer: func(base string) string {
				return "http://evil.example.com"
			},
			wantErr:      true,
			wantRequests: 2,
		},
		{
			name: "mismatched scheme fails",
			advertisedIssuer: func(base string) string {
				return "https://" + strings.TrimPrefix(base, "http://")
			},
			wantErr:      true,
			wantRequests: 2,
		},
		{
			name: "mismatched path fails",
			advertisedIssuer: func(base string) string {
				return base + "/other"
			},
			wantErr:      true,
			wantRequests: 2,
		},
		{
			name: "unrelated issuer fails",
			advertisedIssuer: func(base string) string {
				return "https://attacker.example/realms/x"
			},
			wantErr:      true,
			wantRequests: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			var baseURL string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/.well-known/openid-configuration" {
					http.NotFound(w, r)
					return
				}

				issuer := tt.advertisedIssuer(baseURL)
				endpointBase := strings.TrimRight(issuer, "/")
				discovery := map[string]any{
					"issuer":                                issuer,
					"authorization_endpoint":                endpointBase + "/authorize",
					"token_endpoint":                        endpointBase + "/token",
					"jwks_uri":                              endpointBase + "/jwks",
					"response_types_supported":              []string{"code"},
					"subject_types_supported":               []string{"public"},
					"id_token_signing_alg_values_supported": []string{"RS256"},
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(discovery)
			}))
			defer srv.Close()
			baseURL = srv.URL

			provider, err := discoverProvider(context.Background(), srv.URL)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected discovery error, got nil")
				}
				msg := err.Error()
				if !strings.Contains(msg, `oidc discovery failed for "`) {
					t.Fatalf("expected discovery prefix in error, got %q", msg)
				}
				if !strings.Contains(msg, srv.URL+"/") {
					t.Fatalf("expected trailing-slash variant in error, got %q", msg)
				}
			} else {
				if err != nil {
					t.Fatalf("discoverProvider: %v", err)
				}
				if provider == nil {
					t.Fatal("expected provider, got nil")
				}
			}

			if got := int(requests.Load()); got != tt.wantRequests {
				t.Fatalf("discovery requests = %d, want %d", got, tt.wantRequests)
			}
		})
	}
}

func TestSanitizeReturnTo(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "/"},
		{"root", "/", "/"},
		{"simple path", "/dashboard", "/dashboard"},
		{"path with query", "/board?slug=abc", "/board?slug=abc"},
		{
			"oauth authorize with encoded redirect and external-looking query value",
			"/oauth/authorize?response_type=code&redirect_uri=https%3A%2F%2Fclient.example%2Fcallback&return_to=https://attacker.example&state=a%2Bb",
			"/oauth/authorize?response_type=code&redirect_uri=https%3A%2F%2Fclient.example%2Fcallback&return_to=https://attacker.example&state=a%2Bb",
		},
		{"double slash", "//evil.com", "/"},
		{"scheme", "https://evil.com", "/"},
		{"backslash", "/foo\\bar", "/"},
		{"encoded double slash", "%2f%2f", "/"},
		{"encoded protocol-relative path", "/%2f%2fevil.example/path", "/"},
		{"dot traversal", "/../../etc/passwd", "/"},
		{"single dot segment", "/foo/./bar", "/"},
		{"double dot segment", "/foo/../bar", "/"},
		{"newline", "/foo\nbar", "/"},
		{"carriage return", "/foo\rbar", "/"},
		{"null byte", "/foo\x00bar", "/"},
		{"fragment", "/foo#bar", "/"},
		{"no leading slash", "dashboard", "/"},
		{"protocol relative", "//evil.com/path", "/"},
		{"contains scheme mid-path", "/foo://bar", "/"},
		{"encoded dot dot", "%2e%2e", "/"},
		{"nested path", "/projects/123/board", "/projects/123/board"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeReturnTo(tt.in)
			if got != tt.want {
				t.Errorf("SanitizeReturnTo(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeIssuer(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"https://auth.example.com", "https://auth.example.com"},
		{"https://auth.example.com/", "https://auth.example.com"},
		{"https://auth.example.com///", "https://auth.example.com"},
		{"  https://auth.example.com/  ", "https://auth.example.com"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := NormalizeIssuer(tt.in)
			if got != tt.want {
				t.Errorf("NormalizeIssuer(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsEmailVerified(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want bool
	}{
		{"bool true", true, true},
		{"bool false", false, false},
		{"string true", "true", true},
		{"string false", "false", false},
		{"string True", "True", false},
		{"nil", nil, false},
		{"number", 1.0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isEmailVerified(tt.in)
			if got != tt.want {
				t.Errorf("isEmailVerified(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestStateStoreSingleUse(t *testing.T) {
	s := newStateStore(10 * time.Minute)
	ls := &loginState{Nonce: "n1", PKCEVerifier: "v1", ReturnTo: "/", CreatedAt: time.Now()}
	s.Put("abc", ls)

	got := s.Take("abc")
	if got == nil {
		t.Fatal("expected login state, got nil")
	}
	if got.Nonce != "n1" {
		t.Errorf("nonce = %q, want %q", got.Nonce, "n1")
	}

	// Second take should return nil (single-use)
	got = s.Take("abc")
	if got != nil {
		t.Fatal("expected nil on second take, got non-nil")
	}
}

func TestStateStoreExpiry(t *testing.T) {
	s := newStateStore(0) // 0 TTL = immediately expired
	ls := &loginState{Nonce: "n1", PKCEVerifier: "v1", ReturnTo: "/", CreatedAt: time.Now().Add(-time.Second)}
	s.Put("abc", ls)

	got := s.Take("abc")
	if got != nil {
		t.Fatal("expected nil for expired state, got non-nil")
	}
}

func TestStateStoreUnknownKey(t *testing.T) {
	s := newStateStore(10 * time.Minute)
	got := s.Take("nonexistent")
	if got != nil {
		t.Fatal("expected nil for unknown key, got non-nil")
	}
}
