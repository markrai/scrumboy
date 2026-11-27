package fixture

import "net/http"

const namedPayloadReason = "named_payload_reason"

func emit(w http.ResponseWriter) {
	writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "archive too large", validationDetails("literal_payload_reason", nil))

	writeError(
		w,
		http.StatusRequestEntityTooLarge,
		"PAYLOAD_TOO_LARGE",
		"multiline message",
		validationDetails(namedPayloadReason, nil),
	)

	dynamic := "skipped_dynamic_reason"
	writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "dynamic", validationDetails(dynamic, nil))
}

func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {}

func validationDetails(reason string, extra map[string]any) map[string]any {
	return nil
}
