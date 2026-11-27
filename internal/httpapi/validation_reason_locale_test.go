package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestEveryEmittedReasonHasEnglishMessage is the locale ratchet for API reason
// codes. Discovery uses go/ast + go/parser (stdlib only); see helpers in
// validation_reason_discovery_test.go.
//
// Discovery sources (and default categories):
//   - validationReasonByMessage / validationReasonPrefixes → VALIDATION_ERROR
//   - writeValidationError reason args (string literal or same-package named
//     const with a string-literal value), including multiline calls → VALIDATION_ERROR
//   - writeError(..., "PAYLOAD_TOO_LARGE", ..., validationDetails("reason")) calls
//     where the reason arg is a string literal or same-package const → PAYLOAD_TOO_LARGE
//   - store Reason* consts (const ReasonFoo = "snake_case") in non-test
//     internal/store/*.go, categorized via reasoned/priorityError/
//     reasonedWithDetails/drawingConflict/drawingError/reasonedStoreError{...}
//     usage (or known CONFLICT / INTERNAL reason values when unused)
//
// Limitations (documented; this scanner does not claim exhaustive coverage):
//   - Dynamic reason args (variables, function results such as
//     validationReasonFromStoreError(err)) are not resolved.
//   - Reason args that are selectors from another package are ignored.
//   - Store reasons passed only through local wrappers other than
//     reasoned/priorityError/reasonedWithDetails/drawingError/drawingConflict
//     (e.g. importValidation(...)) are still discovered as consts but may fall
//     back to VALIDATION_ERROR / knownConflictReasons / knownInternalReasons
//     when no direct base-error use is visible.
//   - INTERNAL is included when a store Reason* is known/redacted-internal
//     (e.g. import_internal_redacted); it is not inferred from generic
//     writeInternal calls.
//   - Test files (*_test.go) are excluded from discovery.
func TestEveryEmittedReasonHasEnglishMessage(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("web", "modules", "i18n", "locales", "en.json"))
	if err != nil {
		t.Fatal(err)
	}
	var en map[string]string
	if err := json.Unmarshal(raw, &en); err != nil {
		t.Fatal(err)
	}
	found := discoverEmittedReasons(t)
	if len(found) < 50 {
		t.Fatalf("reason scan found only %d reasons; the scan is broken", len(found))
	}

	var missing []string
	seen := map[string]bool{}
	for _, er := range found {
		key := er.Reason + "@" + er.Code
		if seen[key] {
			continue
		}
		seen[key] = true
		if !localeCoverageHasKey(en, er.Reason, er.Code) && !reasonsWithoutMessage[er.Reason] {
			missing = append(missing, er.Code+"."+er.Reason)
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
	"calendar_feed_blocked":      true,
	"code_required":              true,
	"conflicting_drawing_record": true,
	"drawing_limit_reached":      true,
	"drawing_owned_elsewhere":    true,
	"drawing_too_large":          true,
	"drawings_disabled":          true,
	"image_dimensions":           true,
	// image_too_large has locale keys for both VALIDATION_ERROR and PAYLOAD_TOO_LARGE.
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
