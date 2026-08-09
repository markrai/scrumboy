package httpapi

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"

	boardapp "scrumboy/internal/application/board"
	"scrumboy/internal/store"
)

// handleSlugBoardRead early-dispatches only the exact initial and lane GET
// shapes. All other board routes retain the shared access and dispatch path.
func (s *Server) handleSlugBoardRead(
	w http.ResponseWriter,
	r *http.Request,
	rest []string,
	normalizedSlug string,
) bool {
	isInitial := len(rest) == 1 && r.Method == http.MethodGet
	isLane := len(rest) == 3 && rest[1] == "lanes" && r.Method == http.MethodGet
	if !isInitial && !isLane {
		return false
	}

	ctx := s.requestContext(r)
	prepared, err := s.boardReads.PrepareSlugRead(ctx, boardapp.SlugReadTarget{
		Slug: normalizedSlug,
		Mode: s.storeMode(),
	})
	if err != nil {
		writeStoreErr(w, err, true)
		return true
	}

	if isInitial {
		s.handlePreparedSlugBoardInitial(w, r, ctx, prepared)
		return true
	}

	s.handlePreparedSlugBoardLane(w, r, ctx, rest[2], prepared)
	return true
}

func (s *Server) handlePreparedSlugBoardInitial(
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
	prepared *boardapp.PreparedSlugRead,
) {
	tag := r.URL.Query().Get("tag")
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	if search == "" {
		search = ""
	}
	assigneeFilter, err := s.parseAssigneeFilterFromQuery(ctx, r)
	if err != nil {
		writeValidationError(w, "invalid assignee", "invalid_assignee", map[string]any{"field": "assignee"})
		return
	}
	sortOrder, err := s.parseSortOrderFromQuery(r)
	if err != nil {
		writeValidationError(w, "invalid sort", "invalid_sort", map[string]any{"field": "sort"})
		return
	}
	if r.URL.Query().Get("sprintId") != "" && !prepared.SprintsEnabled() {
		if _, err := s.parseSprintFilterFromQuery(r); err != nil {
			writeValidationError(w, err.Error(), "invalid_sprint_id", map[string]any{"field": "sprintId"})
			return
		}
		writeStoreErr(w, store.ErrSprintsDisabled, true)
		return
	}
	hasSprints, err := prepared.HasSprints()
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	var sprintFilter store.SprintFilter
	if !hasSprints {
		sprintFilter = store.SprintFilter{Mode: "none"}
	} else {
		sprintFilter, err = s.parseSprintFilterFromQuery(r)
		if err != nil {
			writeValidationError(w, err.Error(), "invalid_sprint_id", map[string]any{"field": "sprintId"})
			return
		}
	}
	limitPerLane := 20
	if v := r.URL.Query().Get("limitPerLane"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limitPerLane = n
		}
	}
	result, err := prepared.ReadInitial(boardapp.Query{
		TagFilter:      tag,
		SearchFilter:   search,
		AssigneeFilter: assigneeFilter,
		SprintFilter:   sprintFilter,
		SortOrder:      sortOrder,
		LimitPerLane:   limitPerLane,
	})
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	writeJSON(w, http.StatusOK, boardToJSONWithMeta(
		result.Project,
		result.Workflow,
		result.Tags,
		result.Columns,
		result.ColumnsMeta,
	))
}

func (s *Server) handlePreparedSlugBoardLane(
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
	rawColumnKey string,
	prepared *boardapp.PreparedSlugRead,
) {
	columnKey := normalizeLaneKey(rawColumnKey)
	tag := r.URL.Query().Get("tag")
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	assigneeFilter, err := s.parseAssigneeFilterFromQuery(ctx, r)
	if err != nil {
		writeValidationError(w, "invalid assignee", "invalid_assignee", map[string]any{"field": "assignee"})
		return
	}
	sprintFilter, err := s.parseSprintFilterFromQuery(r)
	if err != nil {
		writeValidationError(w, err.Error(), "invalid_sprint_id", map[string]any{"field": "sprintId"})
		return
	}
	if sprintFilter.Mode != "none" && !prepared.SprintsEnabled() {
		writeStoreErr(w, store.ErrSprintsDisabled, true)
		return
	}
	sortOrder, err := s.parseSortOrderFromQuery(r)
	if err != nil {
		writeValidationError(w, "invalid sort", "invalid_sort", map[string]any{"field": "sort"})
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	afterCursor := r.URL.Query().Get("afterCursor")
	var afterA, afterB int64
	if strings.TrimSpace(afterCursor) == "" {
		// No cursor yet (first page): use a bound that matches every row for the
		// active sortOrder's comparison direction. For the default/oldest
		// ascending order this is (0, 0), same as today's behavior. For
		// newest (descending), a low bound would incorrectly exclude every row.
		if sortOrder == store.SortOrderNewest {
			afterA, afterB = math.MaxInt64, math.MaxInt64
		}
	} else {
		afterA, afterB = store.ParseLaneCursor(afterCursor)
	}

	result, err := prepared.ReadLane(boardapp.LaneQuery{
		ColumnKey:      columnKey,
		Limit:          limit,
		AfterA:         afterA,
		AfterB:         afterB,
		TagFilter:      tag,
		SearchFilter:   search,
		AssigneeFilter: assigneeFilter,
		SprintFilter:   sprintFilter,
		SortOrder:      sortOrder,
	})
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	writeJSON(w, http.StatusOK, lanePageToJSON(result.Items, result.NextCursor, result.HasMore))
}
