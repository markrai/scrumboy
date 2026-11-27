package fixture

import "net/http"

const namedReason = "named_const_reason"

func emit(w http.ResponseWriter) {
	writeValidationError(w, "single", "single_line_reason", nil)

	writeValidationError(
		w,
		"multiline message",
		"multiline_reason",
		nil,
	)

	writeValidationError(w, "named", namedReason, nil)

	dynamic := "skipped_dynamic_reason"
	writeValidationError(w, "dynamic", dynamic, nil)
}

func writeValidationError(w http.ResponseWriter, message, reason string, details map[string]any) {}
