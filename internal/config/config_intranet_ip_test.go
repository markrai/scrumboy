package config

import (
	"testing"
)

func TestFromEnv_IntranetIP(t *testing.T) {
	t.Run("unset is empty", func(t *testing.T) {
		t.Setenv("DATA_DIR", t.TempDir())
		unsetEnv(t, "SCRUMBOY_INTRANET_IP")
		cfg := FromEnv()
		if cfg.IntranetIP != "" {
			t.Fatalf("IntranetIP = %q, want empty when SCRUMBOY_INTRANET_IP is unset", cfg.IntranetIP)
		}
	})

	t.Run("explicit value is preserved", func(t *testing.T) {
		t.Setenv("DATA_DIR", t.TempDir())
		t.Setenv("SCRUMBOY_INTRANET_IP", "192.0.2.10")
		cfg := FromEnv()
		if cfg.IntranetIP != "192.0.2.10" {
			t.Fatalf("IntranetIP = %q, want %q", cfg.IntranetIP, "192.0.2.10")
		}
	})
}
