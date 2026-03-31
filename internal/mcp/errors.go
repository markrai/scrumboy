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
}

func (e *adapterError) Error() string {
	return e.Message
}

func newAdapterError(status int, code, message string, details any) *adapterError {
	return &adapterError{
		Status:  status,
		Code:    code,
		Message: message,
		Details: details,
	}
}

func mapStoreError(err error) *adapterError {
	switch {
	case errors.Is(err, store.ErrUnauthorized):
		return newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
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
