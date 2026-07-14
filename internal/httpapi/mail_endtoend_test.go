package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"scrumboy/internal/mailer/mailertest"
)

// TestRequestPasswordReset_PublicBaseURLOverridesSpoofedHost guards against
// password-reset-link poisoning: with SCRUMBOY_PUBLIC_BASE_URL (Options.PublicBaseURL)
// configured, an attacker-supplied X-Forwarded-Proto/Host on the inbound
// request must be ignored when building the reset link delivered by email.
func TestRequestPasswordReset_PublicBaseURLOverridesSpoofedHost(t *testing.T) {
	fake, err := mailertest.Start(mailertest.Options{})
	if err != nil {
		t.Fatalf("start fake smtp server: %v", err)
	}
	defer fake.Close()
	host, port := fake.HostPort()

	ts, _, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		EncryptionKey:  testEncryptionKey,
		SMTPTLSMode:    "none",
		SMTPHost:       host,
		SMTPPort:       port,
		SMTPFrom:       "no-reply@example.com",
		PublicBaseURL:  "https://scrumboy.example.com",
	})
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Alice", "base-url-check@example.com", "password123")

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/request-password-reset",
		strings.NewReader(`{"email":"base-url-check@example.com"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Scrumboy", "1")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Host", "attacker.evil")
	req.Host = "attacker.evil"

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
	if strings.Contains(body, "attacker.evil") {
		t.Fatalf("reset URL leaked spoofed Host header, got body: %s", body)
	}
	if !strings.Contains(body, "https://scrumboy.example.com/auth/reset-password?token=") {
		t.Fatalf("expected reset URL to use configured PublicBaseURL, got body: %s", body)
	}
}
