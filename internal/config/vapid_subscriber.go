package config

import (
	"strings"
)

// NormalizeVAPIDSubscriber prepares SCRUMBOY_VAPID_SUBSCRIBER for Web Push VAPID JWT "sub".
// Plain emails (e.g. ops@example.com) are prefixed with mailto:. Values that already use
// mailto: or https:// are returned trimmed, unchanged. Not tied to IdP user emails.
func NormalizeVAPIDSubscriber(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	low := strings.ToLower(s)
	if strings.HasPrefix(low, "mailto:") || strings.HasPrefix(low, "https://") {
		return s
	}
	if strings.Contains(s, "@") && !strings.ContainsAny(s, " \t\r\n") {
		return "mailto:" + s
	}
	return s
}
