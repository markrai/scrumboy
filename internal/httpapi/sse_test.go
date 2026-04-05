package httpapi

import (
	"encoding/json"
	"testing"
)

func TestSsePingPayloadJSON(t *testing.T) {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(ssePingPayload, &m); err != nil {
		t.Fatalf("ssePingPayload: %v", err)
	}
	if m["type"] != "ping" {
		t.Fatalf("expected type ping, got %q", m["type"])
	}
}
