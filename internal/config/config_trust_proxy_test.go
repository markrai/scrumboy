package config

import "testing"

func TestTrustProxyFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want bool
	}{
		{"empty", "", false},
		{"whitespace", "   ", false},
		{"one", "1", true},
		{"one spaced", " 1 ", true},
		{"true", "true", true},
		{"TRUE", "TRUE", true},
		{"on", "on", true},
		{"yes", "yes", true},
		{"false", "false", false},
		{"off", "off", false},
		{"garbage", "maybe", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SCRUMBOY_TRUST_PROXY", tc.env)
			if got := trustProxyFromEnv(); got != tc.want {
				t.Fatalf("trustProxyFromEnv() = %v, want %v (SCRUMBOY_TRUST_PROXY=%q)", got, tc.want, tc.env)
			}
		})
	}
}
