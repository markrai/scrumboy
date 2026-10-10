package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	publicboardapp "scrumboy/internal/application/publicboard"
)

const maxPublicRawQueryLength = 2048

func setPublicResponseHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (s *Server) writePublicBoardError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, publicboardapp.ErrPublicNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
	case errors.Is(err, publicboardapp.ErrInvalidPublicQuery):
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request", nil)
	default:
		s.logger.Printf("public board request failed: %v", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL", "internal error", nil)
	}
}

func (s *Server) handlePublicBoard(w http.ResponseWriter, r *http.Request, parts []string) {
	setPublicResponseHeaders(w)
	if s.publicReadRateLimit != nil && !s.publicReadRateLimit.Allow("ip:"+s.clientIP(r), "") {
		s.logger.Printf("public board rate limit exceeded")
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests", nil)
		return
	}
	if r.Method != http.MethodGet || len(parts) < 2 || parts[0] != "board" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	isEvents := len(parts) == 3 && parts[2] == "events"
	if isEvents && s.publicStreamAttemptRateLimit != nil && !s.publicStreamAttemptRateLimit.Allow("ip:"+s.clientIP(r), "") {
		s.logger.Printf("public board stream attempt rate limit exceeded")
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests", nil)
		return
	}

	prepared, err := s.publicBoardReads.Resolve(r.Context(), parts[1])
	if err != nil {
		s.writePublicBoardError(w, err)
		return
	}

	switch {
	case isEvents:
		if len(r.URL.Query()) != 0 {
			s.writePublicBoardError(w, publicboardapp.ErrInvalidPublicQuery)
			return
		}
		s.handlePublicBoardEvents(w, r, prepared)
	case len(parts) == 2:
		input, err := parsePublicBoardQuery(r.URL.Query(), true, r.URL.RawQuery)
		if err != nil {
			s.writePublicBoardError(w, err)
			return
		}
		snapshot, err := prepared.ReadSnapshot(r.Context(), input)
		if err != nil {
			s.writePublicBoardError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, publicSnapshotToJSON(snapshot))
	case len(parts) == 4 && parts[2] == "lanes":
		input, err := parsePublicBoardQuery(r.URL.Query(), false, r.URL.RawQuery)
		if err != nil {
			s.writePublicBoardError(w, err)
			return
		}
		lane, err := prepared.ReadLane(r.Context(), parts[3], input)
		if err != nil {
			s.writePublicBoardError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, publicLaneToJSON(lane))
	case len(parts) == 4 && parts[2] == "todos":
		localID, ok := parseInt64(parts[3])
		if !ok || localID < 1 || len(r.URL.Query()) != 0 {
			s.writePublicBoardError(w, publicboardapp.ErrInvalidPublicQuery)
			return
		}
		todo, err := prepared.ReadTodo(r.Context(), localID)
		if err != nil {
			s.writePublicBoardError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, publicTodoDetailJSON{Todo: publicTodoToJSON(todo)})
	case len(parts) == 5 && parts[2] == "todos" && parts[4] == "links":
		localID, ok := parseInt64(parts[3])
		if !ok || localID < 1 || len(r.URL.Query()) != 0 {
			s.writePublicBoardError(w, publicboardapp.ErrInvalidPublicQuery)
			return
		}
		links, err := prepared.ReadLinks(r.Context(), localID)
		if err != nil {
			s.writePublicBoardError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, publicLinksToJSON(links))
	case len(parts) == 3 && parts[2] == "sprints":
		if len(r.URL.Query()) != 0 {
			s.writePublicBoardError(w, publicboardapp.ErrInvalidPublicQuery)
			return
		}
		sprints, err := prepared.ReadSprints(r.Context())
		if err != nil {
			s.writePublicBoardError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, publicSprintsToJSON(sprints))
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
	}
}

func parsePublicBoardQuery(values url.Values, snapshot bool, rawQuery string) (publicboardapp.QueryInput, error) {
	if len(rawQuery) > maxPublicRawQueryLength {
		return publicboardapp.QueryInput{}, publicboardapp.ErrInvalidPublicQuery
	}
	allowed := map[string]bool{
		"search": true, "tag": true, "sprintNumber": true, "priority": true, "sort": true,
	}
	if snapshot {
		allowed["limitPerLane"] = true
	} else {
		allowed["limit"] = true
		allowed["afterCursor"] = true
	}
	for key := range values {
		if !allowed[key] {
			return publicboardapp.QueryInput{}, publicboardapp.ErrInvalidPublicQuery
		}
	}
	single := func(key string) (string, error) {
		items := values[key]
		if len(items) > 1 {
			return "", publicboardapp.ErrInvalidPublicQuery
		}
		if len(items) == 0 {
			return "", nil
		}
		return items[0], nil
	}
	search, err := single("search")
	if err != nil || !utf8.ValidString(search) {
		return publicboardapp.QueryInput{}, publicboardapp.ErrInvalidPublicQuery
	}
	priority, err := single("priority")
	if err != nil {
		return publicboardapp.QueryInput{}, err
	}
	sortValue, err := single("sort")
	if err != nil || (sortValue != "" && strings.ToLower(strings.TrimSpace(sortValue)) != "manual") {
		return publicboardapp.QueryInput{}, publicboardapp.ErrInvalidPublicQuery
	}
	input := publicboardapp.QueryInput{Search: search, Tags: values["tag"], PriorityKey: priority}
	if sprintRaw, err := single("sprintNumber"); err != nil {
		return publicboardapp.QueryInput{}, err
	} else if sprintRaw != "" {
		number, err := strconv.ParseInt(sprintRaw, 10, 64)
		if err != nil {
			return publicboardapp.QueryInput{}, publicboardapp.ErrInvalidPublicQuery
		}
		input.SprintNumber = &number
	}
	limitKey := "limit"
	if snapshot {
		limitKey = "limitPerLane"
	}
	if limitRaw, err := single(limitKey); err != nil {
		return publicboardapp.QueryInput{}, err
	} else if limitRaw != "" {
		limit, err := strconv.Atoi(limitRaw)
		if err != nil {
			return publicboardapp.QueryInput{}, publicboardapp.ErrInvalidPublicQuery
		}
		input.Limit = limit
	}
	if !snapshot {
		cursor, err := single("afterCursor")
		if err != nil {
			return publicboardapp.QueryInput{}, err
		}
		input.AfterCursor = cursor
	}
	return input, nil
}
