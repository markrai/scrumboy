package config

import (
	"os"
	"strings"
	"testing"
)

var noteFeatureVars = []string{"SCRUMBOY_MARKDOWN_NOTES_ENABLED", "SCRUMBOY_MERMAID_NOTES_ENABLED", "SCRUMBOY_INLINE_IMAGES_ENABLED"}

func TestDefaultOnFeatureFromEnv(t *testing.T) {
	cases := []struct {
		env  string
		want bool
		warn bool
	}{
		{"", true, false},
		{"   ", true, false},
		{"1", true, false},
		{" 1 ", true, false},
		{"true", true, false},
		{"TRUE", true, false},
		{"On", true, false},
		{"yEs", true, false},
		{"0", false, false},
		{" 0 ", false, false},
		{"false", false, false},
		{"FALSE", false, false},
		{"Off", false, false},
		{"NO", false, false},
		{"maybe", false, true},
		{"2", false, true},
		{"disabled", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv("SCRUMBOY_TEST_FEATURE", tc.env)
			var warnings []string
			if got := defaultOnFeatureFromEnv("SCRUMBOY_TEST_FEATURE", &warnings); got != tc.want {
				t.Fatalf("defaultOnFeatureFromEnv(%q) = %v, want %v", tc.env, got, tc.want)
			}
			if (len(warnings) > 0) != tc.warn {
				t.Fatalf("warnings = %v, want warning %v", warnings, tc.warn)
			}
		})
	}
	unsetEnv(t, "SCRUMBOY_TEST_FEATURE")
	var warnings []string
	if !defaultOnFeatureFromEnv("SCRUMBOY_TEST_FEATURE", &warnings) || len(warnings) != 0 {
		t.Fatal("unset variable must enable without a warning")
	}
}

// The notes features are on by default in Full Mode, each can be opted out independently, and
// Mermaid and inline images depend on Markdown notes.
func TestNoteFeatureDefaultsMatrix(t *testing.T) {
	type flags struct{ markdown, mermaid, images bool }
	cases := []struct {
		name string
		env  map[string]string // missing key = unset
		want flags
	}{
		{"all unset", nil, flags{true, true, true}},
		{"all empty", map[string]string{"SCRUMBOY_MARKDOWN_NOTES_ENABLED": "", "SCRUMBOY_MERMAID_NOTES_ENABLED": "", "SCRUMBOY_INLINE_IMAGES_ENABLED": ""}, flags{true, true, true}},
		{"all explicit 1", map[string]string{"SCRUMBOY_MARKDOWN_NOTES_ENABLED": "1", "SCRUMBOY_MERMAID_NOTES_ENABLED": "1", "SCRUMBOY_INLINE_IMAGES_ENABLED": "1"}, flags{true, true, true}},
		{"all explicit 0", map[string]string{"SCRUMBOY_MARKDOWN_NOTES_ENABLED": "0", "SCRUMBOY_MERMAID_NOTES_ENABLED": "0", "SCRUMBOY_INLINE_IMAGES_ENABLED": "0"}, flags{}},
		{"markdown 0 overrides explicit 1", map[string]string{"SCRUMBOY_MARKDOWN_NOTES_ENABLED": "0", "SCRUMBOY_MERMAID_NOTES_ENABLED": "1", "SCRUMBOY_INLINE_IMAGES_ENABLED": "1"}, flags{}},
		{"markdown 0 alone", map[string]string{"SCRUMBOY_MARKDOWN_NOTES_ENABLED": "0"}, flags{}},
		{"mermaid 0 alone", map[string]string{"SCRUMBOY_MERMAID_NOTES_ENABLED": "0"}, flags{true, false, true}},
		{"images 0 alone", map[string]string{"SCRUMBOY_INLINE_IMAGES_ENABLED": "0"}, flags{true, true, false}},
		{"mixed-case truthy", map[string]string{"SCRUMBOY_MARKDOWN_NOTES_ENABLED": "TrUe", "SCRUMBOY_MERMAID_NOTES_ENABLED": "ON", "SCRUMBOY_INLINE_IMAGES_ENABLED": " Yes "}, flags{true, true, true}},
		{"mixed-case falsy", map[string]string{"SCRUMBOY_MARKDOWN_NOTES_ENABLED": "On", "SCRUMBOY_MERMAID_NOTES_ENABLED": "False", "SCRUMBOY_INLINE_IMAGES_ENABLED": "OFF"}, flags{true, false, false}},
		{"unrecognized disables", map[string]string{"SCRUMBOY_INLINE_IMAGES_ENABLED": "enable"}, flags{true, true, false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATA_DIR", t.TempDir())
			t.Setenv("SQLITE_PATH", "")
			t.Setenv("SCRUMBOY_MODE", "full")
			for _, key := range noteFeatureVars {
				if v, ok := tc.env[key]; ok {
					t.Setenv(key, v)
				} else {
					unsetEnv(t, key)
				}
			}
			cfg := FromEnv()
			got := flags{cfg.MarkdownNotesEnabled, cfg.MermaidNotesEnabled, cfg.InlineImagesActive()}
			if got != tc.want {
				t.Fatalf("effective (markdown, mermaid, images) = %+v, want %+v", got, tc.want)
			}
			if tc.name == "unrecognized disables" {
				if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "SCRUMBOY_INLINE_IMAGES_ENABLED") {
					t.Fatalf("Warnings = %v", cfg.Warnings)
				}
			} else if len(cfg.Warnings) != 0 {
				t.Fatalf("unexpected warnings: %v", cfg.Warnings)
			}
		})
	}
}

// Inline images stay Full Mode only: the default does not enable them in anonymous mode.
func TestInlineImagesInactiveInAnonymousMode(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("SQLITE_PATH", "")
	t.Setenv("SCRUMBOY_MODE", "anonymous")
	for _, key := range noteFeatureVars {
		unsetEnv(t, key)
	}
	cfg := FromEnv()
	if !cfg.InlineImagesEnabled || cfg.InlineImagesActive() {
		t.Fatalf("anonymous: InlineImagesEnabled=%v InlineImagesActive=%v, want true/false", cfg.InlineImagesEnabled, cfg.InlineImagesActive())
	}
}

// The sample environment file must not override the built-in defaults with active settings.
func TestEnvExampleLeavesNoteFeaturesAtDefault(t *testing.T) {
	contents, err := os.ReadFile("../../scrumboy.env.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		for _, key := range noteFeatureVars {
			if strings.HasPrefix(line, key+"=") {
				t.Fatalf("scrumboy.env.example sets %q; leave it commented so the default applies", line)
			}
		}
	}
}
