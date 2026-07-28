package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Preference provenance records how a user_preferences row was written, so a
// future bulk-apply can safely tell org-seeded rows apart from user-customized
// ones. Values are application-defined; unknown values are treated conservatively
// (never auto-updated). Kept unexported: bulk-apply is expected to live in this package.
const (
	preferenceProvenanceLegacy     = "legacy"
	preferenceProvenanceUser       = "user"
	preferenceProvenanceOrgDefault = "org_default"
)

// Allowed values for the "cardsPerLane" preference: the default number of cards
// shown per board lane before "Load more" is needed.
var allowedCardsPerLane = map[int]struct{}{
	20:  {},
	50:  {},
	75:  {},
	100: {},
}

// validateCardsPerLaneValue returns ErrValidation if value isn't an allowed preset.
func validateCardsPerLaneValue(value string) error {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("%w: cardsPerLane must be one of 20, 50, 75, or 100", ErrValidation)
	}
	if _, ok := allowedCardsPerLane[n]; !ok {
		return fmt.Errorf("%w: cardsPerLane must be one of 20, 50, 75, or 100", ErrValidation)
	}
	return nil
}

// validateTagColorsJSON returns ErrValidation if any color in the tagColors JSON is invalid.
func validateTagColorsJSON(value string) error {
	var m map[string]string
	if err := json.Unmarshal([]byte(value), &m); err != nil {
		return ErrValidation
	}
	for _, v := range m {
		if v == "" || !colorHexRe.MatchString(strings.TrimSpace(v)) {
			return fmt.Errorf("%w: invalid tag color in preferences", ErrValidation)
		}
	}
	return nil
}

// GetUserPreference retrieves a user preference value by key.
// Returns empty string if not found (not an error).
func (s *Store) GetUserPreference(ctx context.Context, userID int64, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `
SELECT value FROM user_preferences WHERE user_id = ? AND key = ?
`, userID, key).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil // Not found is not an error, return empty string
		}
		return "", fmt.Errorf("get user preference: %w", err)
	}
	return value, nil
}

// SetUserPreference sets or updates a user preference.
func (s *Store) SetUserPreference(ctx context.Context, userID int64, key, value string) error {
	if key == "tagColors" {
		if err := validateTagColorsJSON(value); err != nil {
			return err
		}
	}
	if key == "wallpaper" {
		if err := ValidateWallpaperPrefJSON(value); err != nil {
			return err
		}
	}
	if key == "cardsPerLane" {
		if err := validateCardsPerLaneValue(value); err != nil {
			return err
		}
	}
	if key == "emailNotifications" {
		pref, err := ParseEmailNotifyPref(value)
		if err != nil {
			return err
		}
		canonical, err := json.Marshal(pref)
		if err != nil {
			return fmt.Errorf("marshal email notification preference: %w", err)
		}
		value = string(canonical)
	}
	nowMs := time.Now().UTC().UnixMilli()
	// An explicit user write always marks the row as user-owned, including when
	// it overwrites an inherited org_default row with the same value.
	_, err := s.db.ExecContext(ctx, `
INSERT INTO user_preferences (user_id, key, value, updated_at, provenance)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (user_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at, provenance = excluded.provenance
`, userID, key, value, nowMs, preferenceProvenanceUser)
	if err != nil {
		return fmt.Errorf("set user preference: %w", err)
	}
	return nil
}
