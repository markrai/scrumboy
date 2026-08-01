package mcp

import (
	"errors"
	"net/http"

	"scrumboy/internal/store"
)

const (
	CodeAuthRequired          = "AUTH_REQUIRED"
	CodeForbidden             = "FORBIDDEN"
	CodeNotFound              = "NOT_FOUND"
	CodeValidationError       = "VALIDATION_ERROR"
	CodeConflict              = "CONFLICT"
	CodeCapabilityUnavailable = "CAPABILITY_UNAVAILABLE"
	CodeInternal              = "INTERNAL"
	CodeMethodNotAllowed      = "METHOD_NOT_ALLOWED"
)

type adapterError struct {
	Status  int
	Code    string
	Message string
	Details any
	Cause   error `json:"-"`
}

func (e *adapterError) Error() string {
	return e.Message
}

func newAdapterError(status int, code, message string, details any) *adapterError {
	err := &adapterError{
		Status:  status,
		Code:    code,
		Message: message,
		Details: details,
	}
	if code == CodeInternal {
		if detailMap, ok := details.(map[string]any); ok {
			if detail, ok := detailMap["detail"].(string); ok && detail != "" {
				err.Cause = errors.New(detail)
			}
		}
	}
	return err
}

func mapStoreError(err error) *adapterError {
	switch {
	case errors.Is(err, store.ErrUnauthorized):
		return newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	case errors.Is(err, store.ErrForbidden):
		return newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
	case errors.Is(err, store.ErrNotFound):
		return newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
	case errors.Is(err, store.ErrValidation):
		return newAdapterError(http.StatusBadRequest, CodeValidationError, err.Error(), nil)
	case errors.Is(err, store.ErrConflict):
		return newAdapterError(http.StatusConflict, CodeConflict, err.Error(), nil)
	default:
		return newAdapterError(http.StatusInternalServerError, CodeInternal, "internal error", map[string]any{"detail": err.Error()})
	}
}

// mapPrivilegedStoreError maps store errors for authenticated callers of privileged tools.
// Insufficient privilege after successful authentication returns 403 FORBIDDEN, not 401.
func mapPrivilegedStoreError(err error) *adapterError {
	if errors.Is(err, store.ErrUnauthorized) {
		return newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
	}
	return mapStoreError(err)
}

var clientErrorDetailKeys = map[string]struct{}{
	"columnKey": {},
	"detail":    {},
	"field":     {},
	"fields":    {},
	"localId":   {},
	"tool":      {},
}

// clientErrorDetails is the only adapter-error detail projection used on the
// wire. Internal failures never expose details. Other error classes retain
// only the explicitly reviewed keys above, so a newly added internal value
// cannot become public merely by being attached to adapterError.Details.
func clientErrorDetails(err *adapterError) map[string]any {
	details := map[string]any{}
	if err == nil || err.Code == CodeInternal {
		return details
	}
	raw, ok := err.Details.(map[string]any)
	if !ok {
		return details
	}
	for key, value := range raw {
		if _, allowed := clientErrorDetailKeys[key]; allowed {
			details[key] = value
		}
	}
	return details
}

func clientErrorResponseBody(err *adapterError) errorResponseBody {
	message := err.Message
	if err.Code == CodeInternal {
		message = "internal error"
	}
	return errorResponseBody{
		Code:    err.Code,
		Message: message,
		Details: clientErrorDetails(err),
	}
}
