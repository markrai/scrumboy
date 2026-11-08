package config

import "testing"

func TestInstanceAuditEnabledFromEnv(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("SQLITE_PATH", "")
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", false},
		{"invalid", false},
		{"1", true},
		{" true ", true},
	} {
		t.Setenv("SCRUMBOY_INSTANCE_AUDIT_ENABLED", tc.value)
		if got := FromEnv().InstanceAuditEnabled; got != tc.want {
			t.Fatalf("value %q: got %v want %v", tc.value, got, tc.want)
		}
	}
}
