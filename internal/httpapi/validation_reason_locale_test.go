package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every validation/conflict reason the backend can emit must have an English
// message, otherwise the web client falls back to the generic "Please check the
// request and try again." and the user cannot tell which rule was broken.
func TestEveryEmittedReasonHasEnglishMessage(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("web", "modules", "i18n", "locales", "en.json"))
	if err != nil {
		t.Fatal(err)
	}
	var en map[string]string
	if err := json.Unmarshal(raw, &en); err != nil {
		t.Fatal(err)
	}
	has := func(reason string) bool {
		for _, code := range []string{"VALIDATION_ERROR", "CONFLICT", "PAYLOAD_TOO_LARGE"} {
			if _, ok := en["errors."+code+"."+reason]; ok {
				return true
			}
		}
		return false
	}

	reasons := map[string]bool{}
	for _, r := range validationReasonByMessage {
		reasons[r] = true
	}
	for _, p := range validationReasonPrefixes {
		reasons[p.reason] = true
	}
	// Reason literals passed to writeValidationError in the HTTP layer.
	httpRe := regexp.MustCompile(`writeValidationError\([^\n]*?,\s*"([a-z][a-z_0-9]*)"\s*,`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range httpRe.FindAllStringSubmatch(string(b), -1) {
			reasons[m[1]] = true
		}
	}
	// Reason constants exported by the store.
	constRe := regexp.MustCompile(`\bReason\w+\s*=\s*"([a-z][a-z_0-9]*)"`)
	storeFiles, _ := filepath.Glob(filepath.Join("..", "store", "*.go"))
	for _, f := range storeFiles {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range constRe.FindAllStringSubmatch(string(b), -1) {
			reasons[m[1]] = true
		}
	}
	if len(reasons) < 50 {
		t.Fatalf("reason scan found only %d reasons; the scan is broken", len(reasons))
	}

	var missing []string
	for r := range reasons {
		if !has(r) && !reasonsWithoutMessage[r] {
			missing = append(missing, r)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("reasons with no errors.<CODE>.<reason> key in en.json: %s", strings.Join(missing, ", "))
	}
}

// reasonsWithoutMessage is a ratchet: reasons that predate this test and still
// show the generic text (drawings, GitHub, automation, image upload, calendar
// feed and similar areas). Do not add to it; give new reasons an en.json key
// and remove entries here as their messages are written.
var reasonsWithoutMessage = map[string]bool{
	"calendar_feed_blocked":          true,
	"code_required":                  true,
	"conflicting_drawing_record":     true,
	"drawing_limit_reached":          true,
	"drawing_owned_elsewhere":        true,
	"drawing_too_large":              true,
	"drawings_disabled":              true,
	"image_dimensions":               true,
	"image_too_many_frames":          true,
	"image_trailing_data":            true,
	"invalid_assignee":               true,
	"invalid_automation_action_id":   true,
	"invalid_automation_rule_id":     true,
	"invalid_backup_archive":         true,
	"invalid_calendar_data":          true,
	"invalid_calendar_source_id":     true,
	"invalid_client_max_bytes":       true,
	"invalid_code_challenge":         true,
	"invalid_confirmation":           true,
	"invalid_connection_id":          true,
	"invalid_content_type":           true,
	"invalid_delivery_id":            true,
	"invalid_drawing":                true,
	"invalid_github_browse":          true,
	"invalid_github_reference":       true,
	"invalid_github_search":          true,
	"invalid_github_url":             true,
	"invalid_installation_id":        true,
	"invalid_limit":                  true,
	"invalid_multipart":              true,
	"invalid_priority":               true,
	"invalid_push_endpoint":          true,
	"invalid_resource_id":            true,
	"invalid_sort":                   true,
	"invalid_todo":                   true,
	"missing_lead_hours":             true,
	"publication_enabled_required":   true,
	"todo_archived":                  true,
	"too_many_calendar_occurrences":  true,
	"two_factor_not_enabled":         true,
	"unresolved_drawing_reference":   true,
	"unsupported_calendar_timezone":  true,
	"unsupported_image_type":         true,
	"wall_drawing_version_conflict":  true,
	"wall_transient_target_required": true,
}
