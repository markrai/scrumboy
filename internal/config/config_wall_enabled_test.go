package config

import "testing"

func TestWallEnabledFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want bool
	}{
		{"empty", "", true},
		{"whitespace", "   ", true},
		{"zero", "0", false},
		{"zero spaced", " 0 ", false},
		{"false", "false", false},
		{"FALSE", "FALSE", false},
		{"off", "off", false},
		{"no", "no", false},
		{"legacy one", "1", true},
		{"garbage", "maybe", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SCRUMBOY_WALL_ENABLED", tc.env)
			if got := wallEnabledFromEnv(); got != tc.want {
				t.Fatalf("wallEnabledFromEnv() = %v, want %v (SCRUMBOY_WALL_ENABLED=%q)", got, tc.want, tc.env)
			}
		})
	}
}
