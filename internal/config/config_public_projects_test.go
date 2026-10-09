package config

import (
	"os"
	"strings"
	"testing"
)

func TestPublicBoardFeatureFlagsFromEnv(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "empty", value: "", want: false},
		{name: "whitespace", value: " \t ", want: false},
		{name: "one", value: "1", want: true},
		{name: "true mixed case", value: " TrUe ", want: true},
		{name: "on mixed case", value: " ON ", want: true},
		{name: "yes mixed case", value: " YeS ", want: true},
		{name: "zero", value: "0", want: false},
		{name: "false", value: "false", want: false},
		{name: "off", value: "off", want: false},
		{name: "no", value: "no", want: false},
		{name: "invalid", value: "enabled", want: false},
	}

	for _, key := range []string{"SCRUMBOY_PUBLIC_PROJECTS_ENABLED", "SCRUMBOY_LANDING_PAGE_ENABLED"} {
		t.Run(key+" unset", func(t *testing.T) {
			previous, present := os.LookupEnv(key)
			if err := os.Unsetenv(key); err != nil {
				t.Fatalf("Unsetenv(%q): %v", key, err)
			}
			t.Cleanup(func() {
				if present {
					_ = os.Setenv(key, previous)
				} else {
					_ = os.Unsetenv(key)
				}
			})
			if optInFeatureEnabledFromEnv(key) {
				t.Fatalf("optInFeatureEnabledFromEnv(%q) = true, want false when unset", key)
			}
		})

		for _, tc := range tests {
			t.Run(key+" "+tc.name, func(t *testing.T) {
				t.Setenv(key, tc.value)
				if got := optInFeatureEnabledFromEnv(key); got != tc.want {
					t.Fatalf("optInFeatureEnabledFromEnv(%q) = %v, want %v for %q", key, got, tc.want, tc.value)
				}
			})
		}
	}
}

func TestPublicBoardFeatureFlagsAreIndependentOfEachOtherAndMode(t *testing.T) {
	for _, tc := range []struct {
		name           string
		mode           string
		publicEnabled  string
		landingEnabled string
		wantPublic     bool
		wantLanding    bool
	}{
		{name: "full both disabled", mode: "full"},
		{name: "full public only", mode: "full", publicEnabled: "yes", wantPublic: true},
		{name: "full landing only", mode: "full", landingEnabled: "on", wantLanding: true},
		{name: "full both enabled", mode: "full", publicEnabled: "true", landingEnabled: "1", wantPublic: true, wantLanding: true},
		{name: "anonymous both enabled", mode: "anonymous", publicEnabled: "true", landingEnabled: "true", wantPublic: true, wantLanding: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATA_DIR", t.TempDir())
			t.Setenv("SQLITE_PATH", "")
			t.Setenv("SCRUMBOY_MODE", tc.mode)
			t.Setenv("SCRUMBOY_PUBLIC_PROJECTS_ENABLED", tc.publicEnabled)
			t.Setenv("SCRUMBOY_LANDING_PAGE_ENABLED", tc.landingEnabled)
			t.Setenv("SCRUMBOY_MARKDOWN_NOTES_ENABLED", "1")
			t.Setenv("SCRUMBOY_WALL_ENABLED", "0")

			cfg := FromEnv()
			if cfg.PublicProjectsEnabled != tc.wantPublic || cfg.LandingPageEnabled != tc.wantLanding {
				t.Fatalf("flags = (%v, %v), want (%v, %v)", cfg.PublicProjectsEnabled, cfg.LandingPageEnabled, tc.wantPublic, tc.wantLanding)
			}
			if cfg.ScrumboyMode != tc.mode {
				t.Fatalf("ScrumboyMode = %q, want %q", cfg.ScrumboyMode, tc.mode)
			}
			if !cfg.MarkdownNotesEnabled || cfg.WallEnabled {
				t.Fatalf("existing settings changed: MarkdownNotesEnabled=%v WallEnabled=%v", cfg.MarkdownNotesEnabled, cfg.WallEnabled)
			}
		})
	}
}

func TestDockerComposeForwardsPublicBoardFeatureFlags(t *testing.T) {
	contents, err := os.ReadFile("../../docker-compose.yml")
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}
	compose := string(contents)
	for _, want := range []string{
		"SCRUMBOY_PUBLIC_PROJECTS_ENABLED=${SCRUMBOY_PUBLIC_PROJECTS_ENABLED:-}",
		"SCRUMBOY_LANDING_PAGE_ENABLED=${SCRUMBOY_LANDING_PAGE_ENABLED:-}",
	} {
		if strings.Count(compose, want) != 1 {
			t.Fatalf("docker-compose.yml count for %q = %d, want 1", want, strings.Count(compose, want))
		}
	}
}
