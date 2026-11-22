package httpapi

import (
    "net/url"
    "strings"
    "time"

    "scrumboy/internal/store"
)

const githubWebhookEndpoint = "/api/integrations/github/webhook"

func githubActiveConnectionCount(connections []store.IntegrationConnection) int {
    count := 0
    for _, connection := range connections {
        if connection.Provider == "github" && connection.Status == "active" {
            count++
        }
    }
    return count
}

func (s *Server) githubReadiness(admin bool, activeConnections int) map[string]any {
    provider, _, receiver := s.githubSnapshot()
    appConfigured := s.mode == "full" && provider != nil
    webhookConfigured := appConfigured && receiver != nil
    readiness := map[string]any{
        "appConfigured": appConfigured,
        "webhookConfigured": webhookConfigured,
        "externalReachability": "unverified",
        "subscriptions": "unverified",
        "state": "not_configured",
        "message": "The instance administrator must configure a GitHub App before linking repositories or enabling event notifications.",
    }
    lastVerified := time.Time{}
    if webhookConfigured {
        lastVerified = receiver.LastVerifiedAt()
        if !lastVerified.IsZero() {
            readiness["lastVerifiedAt"] = lastVerified.Format(time.RFC3339)
        }
    }
    if admin {
        readiness["activeInstallations"] = activeConnections
        readiness["webhookPath"] = githubWebhookEndpoint
        if target := s.canonicalGitHubWebhookURL(); target != "" {
            readiness["webhookUrl"] = target
        }
    }
    switch {
    case !appConfigured:
    case !webhookConfigured:
        readiness["state"] = "events_unavailable"
        readiness["message"] = "GitHub App configured, but incoming events are disabled. Ask the instance administrator to set a webhook secret and restart Scrumboy."
    case admin && activeConnections == 0:
        readiness["state"] = "installation_required"
        readiness["message"] = "Webhook receiver configured, but no GitHub App installation is connected. Install the App and use Connect and sync."
    case lastVerified.IsZero():
        readiness["state"] = "awaiting_verified_event"
        readiness["message"] = "Webhook receiver configured. No valid signed GitHub delivery has reached this process since startup. Check the GitHub webhook destination and event subscriptions."
    default:
        readiness["state"] = "verified_delivery"
        readiness["message"] = "A valid signed GitHub delivery reached this process. This does not prove ongoing public reachability, current subscriptions, or matching automation rules."
    }
    return readiness
}

func (s *Server) canonicalGitHubWebhookURL() string {
    raw := strings.TrimSpace(s.publicBaseURL)
    if raw == "" {
        return ""
    }
    parsed, err := url.Parse(raw)
    if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
        parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
        (parsed.Path != "" && parsed.Path != "/") {
        return ""
    }
    return "https://" + parsed.Host + githubWebhookEndpoint
}
