package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const EmailNotifyPrefVersion = 2

// EmailNotifyPref is stored as JSON in user_preferences.key = "emailNotifications".
type EmailNotifyPref struct {
	V               int  `json:"v"`
	Enabled         bool `json:"enabled"`         // master opt-in; no category fires unless this is true
	Assigned        bool `json:"assigned"`        // a card is assigned to me
	CreatedByMe     bool `json:"createdByMe"`     // a card I created is updated or moved
	CardActivity    bool `json:"cardActivity"`    // card created/updated/moved/deleted/links changed
	SprintActivity  bool `json:"sprintActivity"`  // sprint created/updated/deleted/activated/closed
	ProjectActivity bool `json:"projectActivity"` // project/workflow/tag changes
	AddedToProject  bool `json:"addedToProject"`  // I was added to a project
}

// DefaultEmailNotifyPref matches the opt-in defaults surfaced in Settings.
// Existing targeted categories retain their defaults; creator email is an
// explicit opt-in so legacy or absent preferences never enable it silently.
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
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil || fields == nil {
		return EmailNotifyPref{}, fmt.Errorf("%w: email notification preference JSON", ErrValidation)
	}
	allowed := map[string]bool{
		"v": true, "enabled": true, "assigned": true, "createdByMe": true, "cardActivity": true,
		"sprintActivity": true, "projectActivity": true, "addedToProject": true,
	}
	for key := range fields {
		if !allowed[key] {
			return EmailNotifyPref{}, fmt.Errorf("%w: unknown email notification preference field", ErrValidation)
		}
	}
	version := 1 // Missing versions are legacy v1 values.
	if value, ok := fields["v"]; ok {
		var decoded float64
		if err := json.Unmarshal(value, &decoded); err != nil || (decoded != 1 && decoded != EmailNotifyPrefVersion) {
			return EmailNotifyPref{}, fmt.Errorf("%w: unsupported email notification preference version", ErrValidation)
		}
		version = int(decoded)
	}
	if version == 1 {
		if _, ok := fields["createdByMe"]; ok {
			return EmailNotifyPref{}, fmt.Errorf("%w: createdByMe requires email notification preference version 2", ErrValidation)
		}
	} else if version != EmailNotifyPrefVersion {
		return EmailNotifyPref{}, fmt.Errorf("%w: unsupported email notification preference version", ErrValidation)
	}
	p := DefaultEmailNotifyPref()
	for key, target := range map[string]*bool{
		"enabled": &p.Enabled, "assigned": &p.Assigned, "createdByMe": &p.CreatedByMe, "cardActivity": &p.CardActivity,
		"sprintActivity": &p.SprintActivity, "projectActivity": &p.ProjectActivity,
		"addedToProject": &p.AddedToProject,
	} {
		if value, ok := fields[key]; ok {
			var decoded any
			if err := json.Unmarshal(value, &decoded); err != nil {
				return EmailNotifyPref{}, fmt.Errorf("%w: invalid email notification preference field", ErrValidation)
			}
			boolean, ok := decoded.(bool)
			if !ok {
				return EmailNotifyPref{}, fmt.Errorf("%w: invalid email notification preference field", ErrValidation)
			}
			*target = boolean
		}
	}
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
		return EmailNotifyPref{}, err
	}
	return p, nil
}
