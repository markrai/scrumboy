package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scrumboy/internal/store"
)

type apiErrorTestBody struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func decodeAPIErrorTestBody(t *testing.T, rr *httptest.ResponseRecorder) apiErrorTestBody {
	t.Helper()
	var body apiErrorTestBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v\nbody=%s", err, rr.Body.String())
	}
	return body
}

func TestWriteValidationErrorAddsReasonAndPreservesDetails(t *testing.T) {
	rr := httptest.NewRecorder()
	writeValidationError(rr, "name required", "name_required", map[string]any{"field": "name"})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	body := decodeAPIErrorTestBody(t, rr)
	if body.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("code = %q", body.Error.Code)
	}
	if body.Error.Message != "name required" {
		t.Fatalf("message = %q", body.Error.Message)
	}
	if got := body.Error.Details["reason"]; got != "name_required" {
		t.Fatalf("reason = %v", got)
	}
	if got := body.Error.Details["field"]; got != "name" {
		t.Fatalf("field = %v", got)
	}
}

func TestWriteStoreErrAddsClassifiedValidationReason(t *testing.T) {
	cases := []struct {
		message string
		reason  string
	}{
		{"project missing name", "project_missing_name"},
		{"todo missing title", "todo_missing_title"},
		{"invalid workflow column color", "invalid_workflow_column_color"},
	}

	for _, tc := range cases {
		t.Run(tc.message, func(t *testing.T) {
			rr := httptest.NewRecorder()
			err := fmt.Errorf("%w: %s", store.ErrValidation, tc.message)
			writeStoreErr(rr, err, true)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
			}
			body := decodeAPIErrorTestBody(t, rr)
			if body.Error.Message != "validation: "+tc.message {
				t.Fatalf("message = %q", body.Error.Message)
			}
			if got := body.Error.Details["reason"]; got != tc.reason {
				t.Fatalf("reason = %v", got)
			}
		})
	}
}

func TestWriteStoreErrMapsForbidden(t *testing.T) {
	rr := httptest.NewRecorder()
	writeStoreErr(rr, store.ErrForbidden, true)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
	body := decodeAPIErrorTestBody(t, rr)
	if body.Error.Code != "FORBIDDEN" {
		t.Fatalf("code = %q", body.Error.Code)
	}
	if body.Error.Message != "forbidden" {
		t.Fatalf("message = %q", body.Error.Message)
	}
}

func TestWriteStoreErrLeavesUnknownValidationDetailsNull(t *testing.T) {
	rr := httptest.NewRecorder()
	err := fmt.Errorf("%w: dynamic imported project detail", store.ErrValidation)
	writeStoreErr(rr, err, false)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	body := decodeAPIErrorTestBody(t, rr)
	if body.Error.Message != "validation: dynamic imported project detail" {
		t.Fatalf("message = %q", body.Error.Message)
	}
	if body.Error.Details != nil {
		t.Fatalf("details = %#v, want nil", body.Error.Details)
	}
}

func TestWriteStoreErrLeavesDynamicValidationDetailsNull(t *testing.T) {
	messages := []string{
		"project at index 2 missing name",
		"project at index 2 missing slug",
		"todo in project alpha missing localId",
		"todo localId 7 missing title",
		"some object missing name",
	}

	for _, message := range messages {
		t.Run(message, func(t *testing.T) {
			rr := httptest.NewRecorder()
			err := fmt.Errorf("%w: %s", store.ErrValidation, message)
			writeStoreErr(rr, err, false)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
			}
			body := decodeAPIErrorTestBody(t, rr)
			if body.Error.Message != "validation: "+message {
				t.Fatalf("message = %q", body.Error.Message)
			}
			if body.Error.Details != nil {
				t.Fatalf("details = %#v, want nil", body.Error.Details)
			}
		})
	}
}

func TestWriteImportStoreErrRedactsUnexpectedTooLargeText(t *testing.T) {
	rr := httptest.NewRecorder()
	err := fmt.Errorf("import tag: injected: value too large SELECT * FROM secret_credentials")
	writeImportStoreErr(rr, err)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	body := decodeAPIErrorTestBody(t, rr)
	if body.Error.Code != "INTERNAL" {
		t.Fatalf("code = %q, want INTERNAL (string 'too large' must not become PAYLOAD_TOO_LARGE)", body.Error.Code)
	}
	raw := rr.Body.String()
	for _, leak := range []string{"secret_credentials", "too large", "SELECT"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("response leaks %q: %s", leak, raw)
		}
	}
}

func TestWriteImportStoreErrPreservesTypedUploadTooLarge(t *testing.T) {
	rr := httptest.NewRecorder()
	writeImportStoreErr(rr, errUploadTooLarge)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusRequestEntityTooLarge)
	}
	body := decodeAPIErrorTestBody(t, rr)
	if body.Error.Code != "PAYLOAD_TOO_LARGE" {
		t.Fatalf("code = %q", body.Error.Code)
	}
}

func TestFormatErrorWithCauseIncludesUnwrapChain(t *testing.T) {
	cause := errors.New(`sql: injected: value too large SELECT * FROM secret_credentials`)
	wrapped := fmt.Errorf("import tags: %w", cause)
	got := formatErrorWithCause(wrapped)
	if !strings.Contains(got, "import tags") || !strings.Contains(got, "secret_credentials") {
		t.Fatalf("expected public wrapper and diagnostic cause, got %q", got)
	}
	if !errors.Is(wrapped, cause) {
		t.Fatalf("unwrap broken")
	}
}
