package calendar

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

const (
	SourceTypeICSFeed = "ics_feed"
)

type CalendarHostKind string

const (
	CalendarHostKindGoogle CalendarHostKind = "google"
	CalendarHostKindApple  CalendarHostKind = "apple"
	CalendarHostKindOther  CalendarHostKind = "other"
)

const icloudDomain = "icloud.com"

func calendarHostKind(canonicalURL string) CalendarHostKind {
	parsed, err := url.Parse(strings.TrimSpace(canonicalURL))
	if err != nil {
		return CalendarHostKindOther
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return CalendarHostKindOther
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = parsed.Path
	}
	switch host {
	case "calendar.google.com":
		return CalendarHostKindGoogle
	case "google.com", "www.google.com":
		if strings.HasPrefix(path, "/calendar/ical/") {
			return CalendarHostKindGoogle
		}
		return CalendarHostKindOther
	case icloudDomain:
		return CalendarHostKindApple
	}
	if strings.HasSuffix(host, "."+icloudDomain) {
		return CalendarHostKindApple
	}
	return CalendarHostKindOther
}

func canonicalCalendarURL(raw string, allowLoopback bool) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("invalid calendar URL")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" || parsed.Scheme == "" {
		return "", fmt.Errorf("invalid calendar URL")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("invalid calendar URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", fmt.Errorf("invalid calendar URL")
	}
	if isLoopbackHost(host) && !allowLoopback {
		return "", fmt.Errorf("invalid calendar URL")
	}
	switch scheme {
	case "https":
	case "http":
		if !allowLoopback || !isLoopbackHost(host) {
			return "", fmt.Errorf("invalid calendar URL")
		}
	default:
		return "", fmt.Errorf("invalid calendar URL")
	}
	parsed.Scheme = scheme
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	return parsed.String(), nil
}

func hashCalendarURL(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func urlPreview(canonical string) string {
	parsed, err := url.Parse(canonical)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "…"
	}
	return parsed.Scheme + "://" + parsed.Host + "/…"
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateAgendaTimezone(raw string) (string, error) {
	tz := strings.TrimSpace(raw)
	if tz == "" {
		return "", fmt.Errorf("invalid agenda timezone")
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return "", fmt.Errorf("invalid agenda timezone")
	}
	return tz, nil
}
