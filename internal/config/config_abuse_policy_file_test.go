package config

import "testing"

func TestFromEnv_AbusePolicyFile(t *testing.T) {
	t.Run("unset is empty", func(t *testing.T) {
		t.Setenv("DATA_DIR", t.TempDir())
		unsetEnv(t, "SCRUMBOY_ABUSE_POLICY_FILE")
		cfg := FromEnv()
		if cfg.AbusePolicyFile != "" {
			t.Fatalf("AbusePolicyFile = %q, want empty", cfg.AbusePolicyFile)
		}
	})

	t.Run("trimmed path is preserved", func(t *testing.T) {
		t.Setenv("DATA_DIR", t.TempDir())
		t.Setenv("SCRUMBOY_ABUSE_POLICY_FILE", " /data/abuse.json ")
		cfg := FromEnv()
		if cfg.AbusePolicyFile != "/data/abuse.json" {
			t.Fatalf("AbusePolicyFile = %q", cfg.AbusePolicyFile)
		}
	})
}
