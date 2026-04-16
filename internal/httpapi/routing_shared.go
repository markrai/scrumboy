package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"scrumboy/internal/store"
)

// requireProjectRole checks that the user in ctx has at least requiredRole for the project.
// Returns ErrUnauthorized if unauthenticated; ErrNotFound if no project access (caller maps to 404).
func (s *Server) requireProjectRole(ctx context.Context, projectID int64, requiredRole store.ProjectRole) error {
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return store.ErrUnauthorized
	}
	return s.store.CheckProjectRole(ctx, projectID, userID, requiredRole)
}

// requireProjectMaintainerOrHigher checks that the user has Maintainer+ for the project.
func (s *Server) requireProjectMaintainerOrHigher(ctx context.Context, projectID int64) error {
	return s.requireProjectRole(ctx, projectID, store.RoleMaintainer)
}

// parseSprintFilterFromQuery parses sprintId from the request query and returns a SprintFilter.
// Absence of sprintId -> Mode "none" (no sprint filter).
// sprintId=scheduled -> Mode "scheduled" (sprint_id IS NOT NULL, i.e. "Scheduled" view). "assigned" is accepted for backward compatibility.
// sprintId=unscheduled -> Mode "unscheduled" (sprint_id IS NULL).
// sprintId=<number> -> project-local sprint number (resolved inline by board queries).
// Returns error for invalid values (caller should respond 400).
func (s *Server) parseSprintFilterFromQuery(r *http.Request, projectID int64) (store.SprintFilter, error) {
	v := r.URL.Query().Get("sprintId")
	if v == "" {
		return store.SprintFilter{Mode: "none"}, nil
	}
	if v == "scheduled" || v == "assigned" {
		return store.SprintFilter{Mode: "scheduled"}, nil
	}
	if v == "unscheduled" {
		return store.SprintFilter{Mode: "unscheduled"}, nil
	}
	n, ok := parseInt64(v)
	if !ok {
		return store.SprintFilter{}, fmt.Errorf("invalid sprintId: %q", v)
	}
	if n < 1 {
		return store.SprintFilter{}, fmt.Errorf("invalid sprintId: %q", v)
	}
	// Do not pre-query the sprint table here. Board SQL resolves sprint number inline,
	// avoiding an extra DB round-trip per board load when sprint filtering is active.
	return store.SprintFilter{Mode: "sprint_number", SprintNumber: n}, nil
}

func normalizeLaneKey(v string) string {
	switch strings.TrimSpace(strings.ToUpper(v)) {
	case "BACKLOG":
		return store.DefaultColumnBacklog
	case "NOT_STARTED":
		return store.DefaultColumnNotStarted
	case "IN_PROGRESS":
		return store.DefaultColumnDoing
	case "TESTING":
		return store.DefaultColumnTesting
	case "DONE":
		return store.DefaultColumnDone
	default:
		return strings.ToLower(strings.TrimSpace(v))
	}
}
