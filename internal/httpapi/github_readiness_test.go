package httpapi

import (
    "bytes"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "scrumboy/internal/integration/githubwebhook"
)

func readinessState(t *testing.T, readiness map[string]any, want string) {
    t.Helper()
    if got := readiness["state"]; got != want {
        t.Fatalf("readiness state = %v, want %s", got, want)
    }
}

func TestGitHubReadinessStagesAndVerifiedHMAC(t *testing.T) {
    provider := &githubFakeProvider{}
    server := &Server{mode: "full", publicBaseURL: "https://scrumboy.example", githubProvider: provider}
    readinessState(t, server.githubReadiness(true, 0), "events_unavailable")
    secret := []byte("unit-test-github-webhook-secret-32-bytes")
    receiver, err := githubwebhook.New(secret, &routeGitHubInbox{}, provider, nil)
    if err != nil {
        t.Fatal(err)
    }
    server.githubWebhook = receiver
    readinessState(t, server.githubReadiness(true, 0), "installation_required")
    readinessState(t, server.githubReadiness(true, 1), "awaiting_verified_event")

    unsigned := httptest.NewRequest(http.MethodPost, githubWebhookEndpoint, bytes.NewBufferString("{\"zen\":\"hello\"}"))
    unsigned.Header.Set("Content-Type", "application/json")
    unsigned.Header.Set("X-GitHub-Event", "ping")
    rejection := httptest.NewRecorder()
    receiver.ServeHTTP(rejection, unsigned)
    if rejection.Code != http.StatusUnauthorized || !receiver.LastVerifiedAt().IsZero() {
        t.Fatalf("unsigned event affected readiness: %d", rejection.Code)
    }

    body := []byte("{\"zen\":\"hello\"}")
    signed := httptest.NewRequest(http.MethodPost, githubWebhookEndpoint, bytes.NewReader(body))
    signed.Header.Set("Content-Type", "application/json")
    signed.Header.Set("X-GitHub-Event", "ping")
    mac := hmac.New(sha256.New, secret)
    _, _ = mac.Write(body)
    signed.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
    accepted := httptest.NewRecorder()
    receiver.ServeHTTP(accepted, signed)
    if accepted.Code != http.StatusAccepted || receiver.LastVerifiedAt().IsZero() {
        t.Fatalf("valid signed event did not advance readiness: %d", accepted.Code)
    }
    result := server.githubReadiness(true, 1)
    readinessState(t, result, "verified_delivery")
    if _, ok := result["lastVerifiedAt"].(string); !ok {
        t.Fatal("last verified timestamp was not reported")
    }
    if result["webhookUrl"] != "https://scrumboy.example/api/integrations/github/webhook" {
        t.Fatalf("invalid webhook URL %v", result["webhookUrl"])
    }
    projectResult := server.githubReadiness(false, -1)
    if projectResult["webhookUrl"] != nil || projectResult["activeInstallations"] != nil || projectResult["webhookPath"] != nil {
        t.Fatal("project readiness exposed administrator-only fields")
    }
    readinessState(t, projectResult, "verified_delivery")
}

func TestGitHubReadinessRefusesUntrustedCanonicalURL(t *testing.T) {
    server := &Server{mode: "full", githubProvider: &githubFakeProvider{}}
    cases := []string{
        "",
        "http://github.example",
        "https://user:password@github.example",
        "https://github.example/app",
        "https://github.example/?next=evil",
        "https://github.example/#fragment",
        "//github.example",
    }
    for _, raw := range cases {
        server.publicBaseURL = raw
        if target := server.canonicalGitHubWebhookURL(); target != "" {
            t.Fatalf("unsafe base URL %q produced %q", raw, target)
        }
    }
    server.publicBaseURL = "https://github.example"
    if !strings.HasSuffix(server.canonicalGitHubWebhookURL(), githubWebhookEndpoint) {
        t.Fatal("valid origin did not produce exact webhook endpoint")
    }
    server.githubProvider = nil
    readinessState(t, server.githubReadiness(true, 0), "not_configured")
}
