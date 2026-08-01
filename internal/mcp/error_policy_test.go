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
