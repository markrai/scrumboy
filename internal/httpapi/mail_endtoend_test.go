package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// TestRequestPasswordReset_ResetURLReflectsForwardedProto is the flagship
// end-to-end check: a real fake SMTP listener + a real NewServer + a seeded
// user, asserting the delivered email's reset link uses the scheme/host from
// X-Forwarded-Proto/Host on the inbound request (the same base-URL mechanism
// handleAdminUsersPasswordReset already relies on).
func TestRequestPasswordReset_ResetURLReflectsForwardedProto(t *testing.T) {
	ts, fake, cleanup := newRequestPasswordResetTestServer(t, true)
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Alice", "proto-check@example.com", "password123")

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/request-password-reset",
		strings.NewReader(`{"email":"proto-check@example.com"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Scrumboy", "1")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Host", "scrumboy.example.com")
	req.Host = "scrumboy.example.com"

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	msgs := waitForMessages(t, fake, 1)
	body := msgs[0].Body
	if !strings.Contains(body, "https://scrumboy.example.com/auth/reset-password?token=") {
		t.Fatalf("expected reset URL to use forwarded proto/host, got body: %s", body)
	}
}
