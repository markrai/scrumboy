package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
