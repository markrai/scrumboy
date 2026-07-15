package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const EmailNotifyPrefVersion = 1

// EmailNotifyPref is stored as JSON in user_preferences.key = "emailNotifications".
type EmailNotifyPref struct {
	V               int  `json:"v"`
	Enabled         bool `json:"enabled"`         // master opt-in; no category fires unless this is true
	Assigned        bool `json:"assigned"`        // a card is assigned to me
	CardActivity    bool `json:"cardActivity"`    // card created/updated/moved/deleted/links changed
	SprintActivity  bool `json:"sprintActivity"`  // sprint created/updated/deleted/activated/closed
	ProjectActivity bool `json:"projectActivity"` // project/workflow/tag changes
	AddedToProject  bool `json:"addedToProject"`  // I was added to a project
}

// DefaultEmailNotifyPref matches the opt-in defaults surfaced in Settings: the
// two "about me" categories default on, broader project activity defaults off.
func DefaultEmailNotifyPref() EmailNotifyPref {
	return EmailNotifyPref{
		V:              EmailNotifyPrefVersion,
		Enabled:        false,
		Assigned:       true,
		AddedToProject: true,
	}
}

// ParseEmailNotifyPref parses and validates email-notification preference JSON.
func ParseEmailNotifyPref(raw string) (EmailNotifyPref, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultEmailNotifyPref(), nil
	}
	var p EmailNotifyPref
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return EmailNotifyPref{}, fmt.Errorf("%w: email notification preference JSON", ErrValidation)
	}
	if p.V != 0 && p.V != EmailNotifyPrefVersion {
		return EmailNotifyPref{}, fmt.Errorf("%w: unsupported email notification preference version", ErrValidation)
	}
	p.V = EmailNotifyPrefVersion
	return p, nil
}

// ValidateEmailNotifyPrefJSON validates JSON for SetUserPreference when key is emailNotifications.
func ValidateEmailNotifyPrefJSON(value string) error {
	_, err := ParseEmailNotifyPref(value)
	return err
}

// GetEmailNotifyPref loads and parses the caller's email-notification preference,
// falling back to defaults when unset.
func (s *Store) GetEmailNotifyPref(ctx context.Context, userID int64) (EmailNotifyPref, error) {
	raw, err := s.GetUserPreference(ctx, userID, "emailNotifications")
	if err != nil {
		return EmailNotifyPref{}, err
	}
	p, err := ParseEmailNotifyPref(raw)
	if err != nil {
		// Stored value should already be valid (validated on write); fail safe to defaults.
		return DefaultEmailNotifyPref(), nil
	}
	return p, nil
}
