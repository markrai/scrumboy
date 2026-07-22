package httpapi

import (
	"net/http"
	"testing"
)

func TestEmailNotifyPreference_FirstAuthenticatedGetReturnsEmptyStoredValue(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "First User", "first-email-pref@example.com", "password123")

	var result map[string]any
	response, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/user/preferences?key=emailNotifications", nil, &result)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.StatusCode, body)
	}
	value, exists := result["value"]
	if !exists {
		t.Fatalf("expected explicit value field, got %s", body)
	}
	stored, ok := value.(string)
	if !ok || stored != "" {
		t.Fatalf("expected legitimate no-preference representation as an empty string, got %#v", value)
	}
}
