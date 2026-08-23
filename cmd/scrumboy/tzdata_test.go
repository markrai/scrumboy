package main

import (
	"path/filepath"
	"testing"
	"time"
)

// Simulate a minimal runtime with no system tzdata (production Alpine image).
// The executable must embed IANA zones via _ "time/tzdata" in main.go.
func TestNamedZonesAvailableWithoutSystemTZData(t *testing.T) {
	t.Setenv("ZONEINFO", filepath.Join(t.TempDir(), "empty-zoneinfo"))
	for _, name := range []string{"UTC", "America/New_York", "America/Chicago"} {
		if _, err := time.LoadLocation(name); err != nil {
			t.Fatalf("LoadLocation(%q) with empty ZONEINFO: %v", name, err)
		}
	}
}
