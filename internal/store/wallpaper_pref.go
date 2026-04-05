package store

import (
	"encoding/json"
	"fmt"
	"strings"
)

const WallpaperPrefVersion = 1

// WallpaperPref is stored as JSON in user_preferences.key = "wallpaper".
type WallpaperPref struct {
	V    int    `json:"v"`
	Mode string `json:"mode"`           // off | color | image
	Hex  string `json:"hex,omitempty"`  // #RRGGBB when mode=color
	Rev  int64  `json:"rev,omitempty"` // cache-bust when mode=image
}

// ParseWallpaperPref parses and validates wallpaper preference JSON.
func ParseWallpaperPref(raw string) (WallpaperPref, error) {
	var p WallpaperPref
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return WallpaperPref{Mode: "off"}, nil
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return WallpaperPref{}, fmt.Errorf("%w: wallpaper preference JSON", ErrValidation)
	}
	if p.V != 0 && p.V != WallpaperPrefVersion {
		return WallpaperPref{}, fmt.Errorf("%w: unsupported wallpaper version", ErrValidation)
	}
	switch p.Mode {
	case "", "off":
		p.Mode = "off"
		return WallpaperPref{V: WallpaperPrefVersion, Mode: "off"}, nil
	case "color":
		h := strings.TrimSpace(p.Hex)
		if !colorHexRe.MatchString(h) {
			return WallpaperPref{}, fmt.Errorf("%w: invalid wallpaper hex color", ErrValidation)
		}
		return WallpaperPref{V: WallpaperPrefVersion, Mode: "color", Hex: h}, nil
	case "image":
		if p.Rev <= 0 {
			return WallpaperPref{}, fmt.Errorf("%w: invalid wallpaper rev", ErrValidation)
		}
		return WallpaperPref{V: WallpaperPrefVersion, Mode: "image", Rev: p.Rev}, nil
	default:
		return WallpaperPref{}, fmt.Errorf("%w: invalid wallpaper mode", ErrValidation)
	}
}

// ValidateWallpaperPrefJSON validates JSON for SetUserPreference when key is wallpaper.
func ValidateWallpaperPrefJSON(value string) error {
	_, err := ParseWallpaperPref(value)
	return err
}
