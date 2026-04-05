package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

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
	nowMs := time.Now().UTC().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO user_preferences (user_id, key, value, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (user_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
`, userID, key, value, nowMs)
	if err != nil {
		return fmt.Errorf("set user preference: %w", err)
	}
	return nil
}
