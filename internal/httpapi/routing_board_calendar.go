package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	calendarapp "scrumboy/internal/application/calendar"
	"scrumboy/internal/calendar/ics"
	"scrumboy/internal/store"
)

func writeCalendarPrepareError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, calendarapp.ErrActorRequired):
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
	case errors.Is(err, calendarapp.ErrMaintainerRequired):
		writeError(w, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
	case errors.Is(err, calendarapp.ErrDurableRequired):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
	default:
		writeInternal(w, err)
	}
}

func writeCalendarRefreshError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound),
		errors.Is(err, store.ErrUnauthorized),
		errors.Is(err, store.ErrForbidden),
		errors.Is(err, store.ErrEncryptionNotConfigured):
		writeStoreErr(w, err, true)
	case errors.Is(err, calendarapp.ErrFeedTooLarge), errors.Is(err, ics.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", calendarapp.ErrFeedTooLarge.Error(), nil)
	case errors.Is(err, ics.ErrTooManyOccurrences):
		writeValidationError(w, ics.ErrTooManyOccurrences.Error(), "too_many_calendar_occurrences", nil)
	case errors.Is(err, calendarapp.ErrFeedBlocked):
		writeValidationError(w, calendarapp.ErrFeedBlocked.Error(), "calendar_feed_blocked", nil)
	case errors.Is(err, ics.ErrUnknownTimezone):
		writeValidationError(w, ics.ErrUnknownTimezone.Error(), "unsupported_calendar_timezone", nil)
	case errors.Is(err, ics.ErrInvalidCalendar):
		writeValidationError(w, ics.ErrInvalidCalendar.Error(), "invalid_calendar_data", nil)
	case errors.Is(err, calendarapp.ErrFeedTimeout):
		writeError(w, http.StatusGatewayTimeout, "GATEWAY_TIMEOUT", calendarapp.ErrFeedTimeout.Error(), nil)
	default:
		writeError(w, http.StatusBadGateway, "BAD_GATEWAY", calendarapp.ErrFeedRequest.Error(), nil)
	}
}

type calendarSourceJSON struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	Enabled       bool   `json:"enabled"`
	URLConfigured bool   `json:"urlConfigured"`
	URLPreview    string `json:"urlPreview"`
}

type calendarSourcesJSON struct {
	AgendaEnabled  bool                 `json:"agendaEnabled"`
	AgendaTimezone string               `json:"agendaTimezone"`
	AgendaTitle    string               `json:"agendaTitle"`
	AgendaColor    string               `json:"agendaColor"`
	Sources        []calendarSourceJSON `json:"sources"`
}

func calendarSourceToJSON(view calendarapp.SourceView) calendarSourceJSON {
	return calendarSourceJSON{
		ID:            view.ID,
		Name:          view.Name,
		Type:          view.Type,
		Enabled:       view.Enabled,
		URLConfigured: view.URLConfigured,
		URLPreview:    view.URLPreview,
	}
}

func calendarSourcesToJSON(view calendarapp.AgendaSettingsView) calendarSourcesJSON {
	sources := make([]calendarSourceJSON, 0, len(view.Sources))
	for _, src := range view.Sources {
		sources = append(sources, calendarSourceToJSON(src))
	}
	return calendarSourcesJSON{
		AgendaEnabled:  view.Enabled,
		AgendaTimezone: view.Timezone,
		AgendaTitle:    view.Title,
		AgendaColor:    view.Color,
		Sources:        sources,
	}
}

func (s *Server) handleBoardCalendarRoutes(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	if len(rest) < 2 || rest[1] != "calendar-sources" {
		return false
	}

	ctx := s.requestContext(r)
	prepared, err := s.calendarSources.Prepare(ctx, calendarapp.ResolvedRESTTarget{ProjectID: pc.Project.ID})
	if err != nil {
		writeCalendarPrepareError(w, err)
		return true
	}

	if len(rest) == 2 && r.Method == http.MethodGet {
		view, err := prepared.List()
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusOK, calendarSourcesToJSON(view))
		return true
	}

	if len(rest) == 2 && r.Method == http.MethodPost {
		var in struct {
			Name    string `json:"name"`
			Type    string `json:"type"`
			URL     string `json:"url"`
			Enabled *bool  `json:"enabled"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		view, err := prepared.Create(calendarapp.CreateSourceCommand{
			Name:    in.Name,
			Type:    in.Type,
			URL:     in.URL,
			Enabled: in.Enabled,
		})
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusCreated, calendarSourceToJSON(view))
		return true
	}

	if len(rest) == 4 && rest[3] == "refresh" {
		sourceID, err := strconv.ParseInt(strings.TrimSpace(rest[2]), 10, 64)
		if err != nil || sourceID <= 0 {
			writeValidationError(w, "invalid calendar source id", "invalid_calendar_source_id", map[string]any{"field": "id"})
			return true
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return true
		}
		if s.agenda == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			return true
		}
		if err := s.agenda.RefreshSource(ctx, pc.Project.ID, sourceID); err != nil {
			writeCalendarRefreshError(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return true
	}

	if len(rest) != 3 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return true
	}

	sourceID, err := strconv.ParseInt(strings.TrimSpace(rest[2]), 10, 64)
	if err != nil || sourceID <= 0 {
		writeValidationError(w, "invalid calendar source id", "invalid_calendar_source_id", map[string]any{"field": "id"})
		return true
	}

	if r.Method == http.MethodPatch {
		var in struct {
			Name    *string `json:"name"`
			Enabled *bool   `json:"enabled"`
			URL     *string `json:"url"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		view, err := prepared.Update(calendarapp.UpdateSourceCommand{
			SourceID: sourceID,
			Name:     in.Name,
			Enabled:  in.Enabled,
			URL:      in.URL,
		})
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusOK, calendarSourceToJSON(view))
		return true
	}

	if r.Method == http.MethodDelete {
		if err := prepared.Delete(sourceID); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return true
	}

	writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
	return true
}
