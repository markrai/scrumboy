package auth

import (
	"errors"
	"fmt"
	"strings"
)

// ErrValidation is returned when password validation fails.
var ErrValidation = errors.New("validation")

const minPasswordLength = 8

// ValidatePassword validates a password for signup or reset.
// Rules: min length 8, trim whitespace, reject empty.
// Returns ErrValidation on failure.
func ValidatePassword(password string) error {
	p := strings.TrimSpace(password)
	if p == "" {
		return fmt.Errorf("%w: password required", ErrValidation)
	}
	if len(p) < minPasswordLength {
		return fmt.Errorf("%w: password too short", ErrValidation)
	}
	return nil
}
