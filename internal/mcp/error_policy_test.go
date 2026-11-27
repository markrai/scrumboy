package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"scrumboy/internal/store"
)


func TestClientErrorDetailsAllowlist(t *testing.T) {
	err := newAdapterError(
		http.StatusBadRequest,
		CodeValidationError,
		"invalid cursor",
		map[string]any{
			"field":       "cursor",
			"columnKey":   "building",
			"unreviewed":  "must not escape",
			"anotherLeak": 42,
		},
	)

	if got, want := clientErrorDetails(err), map[string]any{
		"field":     "cursor",
		"columnKey": "building",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("client details = %#v, want %#v", got, want)
	}
}

func TestLegacyInternalErrorSanitizesClientAndLogsCause(t *testing.T) {
	const sensitive = "sqlite: no such table secret_records"
	err := mapStoreError(errors.New(sensitive))

	recorder := httptest.NewRecorder()
	writeError(recorder, err)

	var response errorResponse
	if decodeErr := json.Unmarshal(recorder.Body.Bytes(), &response); decodeErr != nil {
		t.Fatalf("decode response: %v", decodeErr)
	}
	if response.Error.Code != CodeInternal ||
		response.Error.Message != "internal error" ||
		!reflect.DeepEqual(response.Error.Details, map[string]any{}) {
		t.Fatalf("client error = %#v", response.Error)
	}
	if strings.Contains(recorder.Body.String(), sensitive) {
		t.Fatalf("legacy response leaked internal cause: %s", recorder.Body.String())
	}

	var logs bytes.Buffer
	adapter := &Adapter{logger: log.New(&logs, "", 0)}
	adapter.logAdapterError("legacy", "board_get", err)
	if got := logs.String(); !strings.Contains(got, "transport=legacy tool=board_get") || !strings.Contains(got, sensitive) {
		t.Fatalf("internal error log = %q", got)
	}
}

func TestJSONRPCInternalErrorSanitizesStructuredContent(t *testing.T) {
	const sensitive = "database host=db.internal password=do-not-expose"
	err := newAdapterError(
		http.StatusInternalServerError,
		CodeInternal,
		"member not found after add",
		map[string]any{"detail": sensitive},
	)

	recorder := httptest.NewRecorder()
	writeJSONRPCToolErrorResult(recorder, json.RawMessage("9"), err)

	var response map[string]any
	if decodeErr := json.Unmarshal(recorder.Body.Bytes(), &response); decodeErr != nil {
		t.Fatalf("decode response: %v", decodeErr)
	}
	result := response["result"].(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	if structured["code"] != CodeInternal ||
		structured["message"] != "internal error" ||
		!reflect.DeepEqual(structured["details"], map[string]any{}) {
		t.Fatalf("structured internal error = %#v", structured)
	}
	if strings.Contains(recorder.Body.String(), sensitive) {
		t.Fatalf("JSON-RPC response leaked internal cause: %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "member not found after add") {
		t.Fatalf("JSON-RPC response leaked internal invariant: %s", recorder.Body.String())
	}
	if _, ok := structured["status"]; ok {
		t.Fatalf("structured tool error copied legacy HTTP status: %#v", structured)
	}
}

// TestReasonDetailsMergesStoreErrorDetails asserts that reasonDetails merges
// store.ErrorDetails into the returned map so structured details reach the wire.
func TestReasonDetailsMergesStoreErrorDetails(t *testing.T) {
	// Build a store validation error that has both a reason and extra details.
	// Use the custom_field_number_out_of_range reason which emits max.
	storeErr := store.NewReasonedValidationError(store.ReasonCustomFieldNumberRange, "out of range", map[string]any{"max": float64(1 << 53)})
	got := mapStoreError(storeErr)
	if got.Code != CodeValidationError {
		t.Fatalf("code = %q, want %q", got.Code, CodeValidationError)
	}
	details, ok := got.Details.(map[string]any)
	if !ok {
		t.Fatalf("details type = %T, want map[string]any", got.Details)
	}
	if details["reason"] != store.ReasonCustomFieldNumberRange {
		t.Fatalf("details.reason = %v, want %q", details["reason"], store.ReasonCustomFieldNumberRange)
	}
	if details["max"] != float64(1<<53) {
		t.Fatalf("details.max = %v, want %v", details["max"], float64(1<<53))
	}
}

// TestMaxAllowedThroughClientErrorDetails asserts that the "max" key passes
// through the clientErrorDetails allowlist (custom field number range, work limits).
func TestMaxAllowedThroughClientErrorDetails(t *testing.T) {
	err := newAdapterError(
		http.StatusBadRequest,
		CodeValidationError,
		"number out of range",
		map[string]any{
			"reason":    "custom_field_number_out_of_range",
			"max":       float64(1 << 53),
			"sensitive": "must-not-appear",
		},
	)
	got := clientErrorDetails(err)
	want := map[string]any{
		"reason": "custom_field_number_out_of_range",
		"max":    float64(1 << 53),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("clientErrorDetails = %#v, want %#v", got, want)
	}
}

// TestImportInternalRedactedReturnsSafeInternal asserts that import_internal_redacted
// errors from the store produce a bare INTERNAL response (no detail field).
func TestImportInternalRedactedReturnsSafeInternal(t *testing.T) {
	redactedErr := store.RedactedImportError()
	got := mapStoreError(redactedErr)
	if got.Code != CodeInternal {
		t.Fatalf("code = %q, want INTERNAL", got.Code)
	}
	// No detail must escape on the wire.
	if details := clientErrorDetails(got); len(details) > 0 {
		t.Fatalf("clientErrorDetails leaked details for redacted internal: %#v", details)
	}
}
