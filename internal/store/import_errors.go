package store

import (
	"fmt"
	"strings"
)

// importValidation returns an ErrValidation with a stable reason and optional
// structured details. User-supplied names belong in details as plain strings
// for safe i18n interpolation (never HTML).
func importValidation(reason, message string, details map[string]any) error {
	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = ErrValidation.Error()
	} else if !strings.HasPrefix(msg, ErrValidation.Error()) {
		msg = ErrValidation.Error() + ": " + msg
	}
	return reasonedWithDetails(ErrValidation, reason, msg, details)
}

// redactImportInternal replaces an unexpected internal failure with a safe
// client-facing error that does not disclose SQL, paths, or stack details.
// The original cause is retained via Unwrap for server-side logging.
func redactImportInternal(cause error) error {
	base := cause
	if base == nil {
		base = fmt.Errorf("internal error")
	}
	return reasonedWithDetails(
		base,
		ReasonImportInternalRedacted,
		"Import failed due to an internal error.",
		nil,
	)
}

// NewReasonedValidationError constructs a public ErrValidation-based error
// with an explicit reason code and optional structured details. Used in tests
// and in applications that compose store-level validation errors.
func NewReasonedValidationError(reason, message string, details map[string]any) error {
	return importValidation(reason, message, details)
}

// RedactedImportError returns a safe import-internal error (no SQL, paths, or
// stack details) with reason=import_internal_redacted. Use in tests to verify
// that the error propagation path redacts correctly.
func RedactedImportError() error {
	return redactImportInternal(nil)
}
