package errs

import "errors"

// NOTE: All shared sentinel errors MUST be defined here.
// Do not redefine errors.New(...) for these concepts elsewhere.
// Use errs.ErrX (or store re-exports that alias errs.ErrX) to preserve identity across packages.
var (
	ErrValidation                 = errors.New("validation")
	ErrUnauthorized               = errors.New("unauthorized")
	ErrForbidden                  = errors.New("forbidden")
	ErrNotFound                   = errors.New("not found")
	ErrConflict                   = errors.New("conflict")
	ErrTooManyAttempts            = errors.New("too many attempts")
	Err2FAEncryptionNotConfigured = errors.New("2FA encryption not configured")
	ErrEncryptionNotConfigured    = errors.New("encryption not configured")
)
