package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"scrumboy/internal/version"
)

// ExportData represents the complete export structure
type ExportData struct {
	Version    string          `json:"version"`
	ExportedAt time.Time       `json:"exportedAt"`
	Mode       string          `json:"mode"`
	Scope      string          `json:"scope"`
	ExportedBy *string         `json:"exportedBy,omitempty"`
	Projects   []ProjectExport `json:"projects"`
}

// WorkflowColumnExport represents a workflow column for backup.
type WorkflowColumnExport struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Color   string `json:"color"`
	Position int   `json:"position"`
	IsDone  bool   `json:"isDone"`
}

// SprintExport represents a sprint for backup (project-scoped).
type SprintExport struct {
	Number        int64      `json:"number"`
	Name          string     `json:"name"`
	PlannedStartAt int64     `json:"plannedStartAt"` // Unix ms
	PlannedEndAt   int64     `json:"plannedEndAt"`   // Unix ms
	State         string     `json:"state"`
	StartedAt     *int64     `json:"startedAt,omitempty"` // Unix ms
	ClosedAt      *int64     `json:"closedAt,omitempty"`  // Unix ms
}

// ProjectExport represents a project with its todos and tags.
// EstimationMode is exported for readability only; on import it is ignored and we always use EstimationModeModifiedFibonacci (v1).
type ProjectExport struct {
	Slug                string                 `json:"slug"`
	Name                string                 `json:"name"`
	EstimationMode      string                 `json:"estimationMode,omitempty"`
	Image               *string                `json:"image,omitempty"`
	DominantColor       string                 `json:"dominantColor,omitempty"`
	DefaultSprintWeeks  int                    `json:"defaultSprintWeeks,omitempty"`
	ExpiresAt           *time.Time             `json:"expiresAt"`
	CreatedAt           time.Time              `json:"createdAt"`
	UpdatedAt           time.Time              `json:"updatedAt"`
	WorkflowColumns     []WorkflowColumnExport `json:"workflowColumns,omitempty"`
	Sprints             []SprintExport        `json:"sprints,omitempty"`
	Todos               []TodoExport           `json:"todos"`
	Tags                []TagExport            `json:"tags"`
	Links               []LinkExport           `json:"links,omitempty"`
	Wall                *WallExport            `json:"wall,omitempty"`
}

// TodoExport represents a todo in export format
type TodoExport struct {
	LocalID          int64     `json:"localId"`
	Title            string    `json:"title"`
	Body             string    `json:"body"`
	Status           string    `json:"status"`
	Rank             int64     `json:"rank"`
	SprintNumber     *int64    `json:"sprintNumber,omitempty"` // project-local sprint number; nil = backlog
	EstimationPoints *int64    `json:"estimationPoints,omitempty"`
	AssigneeUserId   *int64    `json:"assigneeUserId,omitempty"`
	Tags             []string  `json:"tags"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
	DoneAt           *int64    `json:"doneAt,omitempty"` // Unix ms; last completion time (set on transition into DONE, preserved on reopen)
}

// TagExport represents a tag in export format
type TagExport struct {
	Name  string  `json:"name"`
	Color *string `json:"color,omitempty"`
}

// LinkExport represents a todo link in export format (project-scoped by from/to local IDs).
type LinkExport struct {
	FromLocalID int64  `json:"fromLocalId"`
	ToLocalID   int64  `json:"toLocalId"`
	LinkType    string `json:"linkType"`
}

// WallExport represents the sticky-note wall document (Scrumbaby) for a project.
// The wall is a single JSON blob per project (one-to-one with projects); IDs
// inside are opaque strings, so whole-document copy is safe across projects
// without id remapping. UpdatedAt is intentionally omitted: each import stamps
// its own import time on write. A missing field on an imported project means
// "no wall data in this backup" - existing walls on the target are preserved.
type WallExport struct {
	Notes   []WallNote `json:"notes"`
	Edges   []WallEdge `json:"edges,omitempty"`
	Version int64      `json:"version,omitempty"`
}

// resolveImportDoneAt returns the done_at value for import. Uses export DoneAt when present;
// for DONE rows without DoneAt (legacy export), falls back to updatedAtMs.
func resolveImportDoneAt(exportDoneAt *int64, status Status, updatedAtMs int64) any {
	if exportDoneAt != nil {
		return *exportDoneAt
	}
	if status == StatusDone {
		return updatedAtMs
	}
	return nil
}

// ImportResult represents the result of an import operation
type ImportResult struct {
	Imported int      `json:"imported"`
	Updated  int      `json:"updated"`
	Created  int      `json:"created"`
	Warnings []string `json:"warnings,omitempty"`
}

// PreviewResult represents preview counts
type PreviewResult struct {
	Projects   int      `json:"projects"`
	Todos      int      `json:"todos"`
	Tags       int      `json:"tags"`
	Links      int      `json:"links,omitempty"`
	WillDelete int      `json:"willDelete,omitempty"`
	WillUpdate int      `json:"willUpdate,omitempty"`
	WillCreate int      `json:"willCreate,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

// getExportableProjectsSelector returns the SQL WHERE clause and args for projects that should be exported.
// CRITICAL: Export scope is ownership-based, not visibility-based. This invariant must be preserved.
// This selector is shared between export and Replace All delete to ensure exact match.
func (s *Store) getExportableProjectsSelector(ctx context.Context, mode Mode) (string, []any, error) {
	enabled, err := s.authEnabled(ctx)
	if err != nil {
		return "", nil, err
	}
	return exportableProjectsSelector(ctx, mode, enabled)
}

func (s *Store) getExportableProjectsSelectorTx(ctx context.Context, tx *sql.Tx, mode Mode) (string, []any, error) {
	enabled, err := authEnabledTx(ctx, tx)
	if err != nil {
		return "", nil, err
	}
	return exportableProjectsSelector(ctx, mode, enabled)
}

func exportableProjectsSelector(ctx context.Context, mode Mode, authEnabled bool) (string, []any, error) {
	if mode == ModeAnonymous {
		// In anonymous mode, export the current project (single board)
		// We'll handle this differently in ExportAllProjects
		return "", nil, fmt.Errorf("anonymous mode selector should not be called directly")
	}

	// Full mode: Only projects where user is MAINTAINER (project role), or temp boards (creator).
	// System roles (Owner/Admin/User) do NOT grant export/import; only project maintainer does.
	if authEnabled {
		userID, ok := UserIDFromContext(ctx)
		if !ok {
			return "", nil, ErrUnauthorized
		}
		return `(
  (expires_at IS NOT NULL AND creator_user_id = ?) OR
  (expires_at IS NULL AND EXISTS (
    SELECT 1 FROM project_members pm
    WHERE pm.project_id = projects.id AND pm.user_id = ? AND pm.role = 'maintainer'
  ))
)`, []any{userID, userID}, nil
	}

	// Pre-bootstrap: export all projects
	return `1=1`, []any{}, nil
}

// ExportAllProjects exports all projects that the current user owns or temporary boards.
func (s *Store) ExportAllProjects(ctx context.Context, mode Mode) (*ExportData, error) {
	var projects []Project

	if mode == ModeAnonymous {
		// In anonymous mode, export the single current project
		// We need to get it from the context or find the most recent one
		rows, err := s.db.QueryContext(ctx, `
			SELECT id, name, image, slug, dominant_color, estimation_mode, default_sprint_weeks, owner_user_id, creator_user_id, last_activity_at, expires_at, created_at, updated_at
			FROM projects
			WHERE expires_at IS NOT NULL AND import_batch_id IS NULL
			ORDER BY updated_at DESC, id DESC
			LIMIT 1`)
		if err != nil {
			return nil, fmt.Errorf("list anonymous project: %w", err)
		}
		defer rows.Close()

		if rows.Next() {
			var p Project
			var createdAtMs, updatedAtMs, lastActivityAtMs int64
			var expiresAtMs sql.NullInt64
			var ownerUserID sql.NullInt64
			var creatorUserID sql.NullInt64
			var image sql.NullString
			if err := rows.Scan(&p.ID, &p.Name, &image, &p.Slug, &p.DominantColor, &p.EstimationMode, &p.DefaultSprintWeeks, &ownerUserID, &creatorUserID, &lastActivityAtMs, &expiresAtMs, &createdAtMs, &updatedAtMs); err != nil {
				return nil, fmt.Errorf("scan project: %w", err)
			}
			if image.Valid && image.String != "" {
				p.Image = &image.String
			}
			if ownerUserID.Valid {
				v := ownerUserID.Int64
				p.OwnerUserID = &v
			}
			if creatorUserID.Valid {
				v := creatorUserID.Int64
				p.CreatorUserID = &v
			}
			p.LastActivityAt = time.UnixMilli(lastActivityAtMs).UTC()
			if expiresAtMs.Valid {
				expiresAt := time.UnixMilli(expiresAtMs.Int64).UTC()
				p.ExpiresAt = &expiresAt
			}
			p.CreatedAt = time.UnixMilli(createdAtMs).UTC()
			p.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
			projects = []Project{p}
		}
	} else {
		// Full mode: use shared selector
		whereClause, args, err := s.getExportableProjectsSelector(ctx, mode)
		if err != nil {
			return nil, err
		}

		rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
			SELECT id, name, image, slug, dominant_color, estimation_mode, default_sprint_weeks, owner_user_id, last_activity_at, expires_at, created_at, updated_at
			FROM projects
			WHERE %s
			ORDER BY updated_at DESC, id DESC`, whereClause), args...)
		if err != nil {
			return nil, fmt.Errorf("list projects: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var p Project
			var createdAtMs, updatedAtMs, lastActivityAtMs int64
			var expiresAtMs sql.NullInt64
			var ownerUserID sql.NullInt64
			var image sql.NullString
			if err := rows.Scan(&p.ID, &p.Name, &image, &p.Slug, &p.DominantColor, &p.EstimationMode, &p.DefaultSprintWeeks, &ownerUserID, &lastActivityAtMs, &expiresAtMs, &createdAtMs, &updatedAtMs); err != nil {
				return nil, fmt.Errorf("scan project: %w", err)
			}
			if image.Valid && image.String != "" {
				p.Image = &image.String
			}
			if ownerUserID.Valid {
				v := ownerUserID.Int64
				p.OwnerUserID = &v
			}
			p.LastActivityAt = time.UnixMilli(lastActivityAtMs).UTC()
			if expiresAtMs.Valid {
				expiresAt := time.UnixMilli(expiresAtMs.Int64).UTC()
				p.ExpiresAt = &expiresAt
			}
			p.CreatedAt = time.UnixMilli(createdAtMs).UTC()
			p.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
			projects = append(projects, p)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("rows projects: %w", err)
		}
	}

	// Export each project with todos and tags
	exportProjects := make([]ProjectExport, 0, len(projects))
	for _, p := range projects {
		// Get all todos for this project (all statuses)
		todos, err := s.exportAllTodosForProject(ctx, p.ID, mode)
		if err != nil {
			return nil, fmt.Errorf("export todos for project %d: %w", p.ID, err)
		}

		// Get workflow and sprints for export
		workflow, err := s.GetProjectWorkflow(ctx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("export workflow for project %d: %w", p.ID, err)
		}
		sprints, err := s.ListSprints(ctx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("export sprints for project %d: %w", p.ID, err)
		}
		sprintIDToNumber := make(map[int64]int64)
		for _, sp := range sprints {
			sprintIDToNumber[sp.ID] = sp.Number
		}

		// Get all tags used in project (from tag counts - shows all tags, grouped by name)
		var viewerUserID *int64
		if userID, ok := UserIDFromContext(ctx); ok {
			viewerUserID = &userID
		}
		tagCounts, err := s.listTagCounts(ctx, p.ID, viewerUserID, nil)
		if err != nil {
			return nil, fmt.Errorf("export tags for project %d: %w", p.ID, err)
		}

		// Convert to export format
		todoExports := make([]TodoExport, 0, len(todos))
		for _, t := range todos {
			var doneAtMs *int64
			if t.DoneAt != nil {
				ms := t.DoneAt.UnixMilli()
				doneAtMs = &ms
			}
			var sprintNumber *int64
			if t.SprintID != nil {
				if num, ok := sprintIDToNumber[*t.SprintID]; ok {
					sprintNumber = &num
				}
			}
			todoExports = append(todoExports, TodoExport{
				LocalID:          t.LocalID,
				Title:            t.Title,
				Body:             t.Body,
				Status:           strings.ToUpper(t.ColumnKey),
				Rank:             t.Rank,
				SprintNumber:     sprintNumber,
				EstimationPoints: cloneInt64Ptr(t.EstimationPoints),
				AssigneeUserId:   cloneInt64Ptr(t.AssigneeUserID),
				Tags:             t.Tags,
				CreatedAt:        t.CreatedAt,
				UpdatedAt:        t.UpdatedAt,
				DoneAt:           doneAtMs,
			})
		}

		tagExports := make([]TagExport, 0, len(tagCounts))
		for _, tc := range tagCounts {
			tagExports = append(tagExports, TagExport{
				Name:  tc.Name,
				Color: tc.Color,
			})
		}
		sort.Slice(tagExports, func(i, j int) bool {
			return tagExports[i].Name < tagExports[j].Name
		})

		linkExports, err := s.exportLinksForProject(ctx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("export links for project %d: %w", p.ID, err)
		}

		wallExport, err := s.exportWallForProject(ctx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("export wall for project %d: %w", p.ID, err)
		}

		dominantColor := p.DominantColor
		if dominantColor == "" {
			dominantColor = "#888888"
		}

		// Workflow columns: export only when non-default
		var workflowColExports []WorkflowColumnExport
		if !workflowMatchesDefault(workflow) {
			for _, c := range workflow {
				workflowColExports = append(workflowColExports, WorkflowColumnExport{
					Key:      c.Key,
					Name:     c.Name,
					Color:    c.Color,
					Position: c.Position,
					IsDone:   c.IsDone,
				})
			}
		}

		// Sprints
		sprintExports := make([]SprintExport, 0, len(sprints))
		for _, sp := range sprints {
			var startedAtMs, closedAtMs *int64
			if sp.StartedAt != nil {
				ms := sp.StartedAt.UnixMilli()
				startedAtMs = &ms
			}
			if sp.ClosedAt != nil {
				ms := sp.ClosedAt.UnixMilli()
				closedAtMs = &ms
			}
			sprintExports = append(sprintExports, SprintExport{
				Number:         sp.Number,
				Name:           sp.Name,
				PlannedStartAt: sp.PlannedStartAt.UnixMilli(),
				PlannedEndAt:   sp.PlannedEndAt.UnixMilli(),
				State:          sp.State,
				StartedAt:      startedAtMs,
				ClosedAt:       closedAtMs,
			})
		}

		defaultSprintWeeks := p.DefaultSprintWeeks
		if defaultSprintWeeks != 1 && defaultSprintWeeks != 2 {
			defaultSprintWeeks = 2
		}

		exportProjects = append(exportProjects, ProjectExport{
			Slug:               p.Slug,
			Name:               p.Name,
			EstimationMode:     EstimationModeModifiedFibonacci, // always canonical; never emit from DB to avoid case/typo drift
			Image:              p.Image,
			DominantColor:      dominantColor,
			DefaultSprintWeeks: defaultSprintWeeks,
			ExpiresAt:          p.ExpiresAt,
			CreatedAt:          p.CreatedAt,
			UpdatedAt:          p.UpdatedAt,
			WorkflowColumns:    workflowColExports,
			Sprints:            sprintExports,
			Todos:              todoExports,
			Tags:               tagExports,
			Links:              linkExports,
			Wall:               wallExport,
		})
	}

	// Get exportedBy (optional)
	var exportedBy *string
	if email, ok := UserEmailFromContext(ctx); ok {
		exportedBy = &email
	}

	scope := "full"
	if mode == ModeAnonymous {
		scope = "project"
	}

	return &ExportData{
		Version:    version.ExportFormatVersion,
		ExportedAt: time.Now().UTC(),
		Mode:       mode.String(),
		Scope:      scope,
		ExportedBy: exportedBy,
		Projects:   exportProjects,
	}, nil
}

// workflowMatchesDefault returns true if the project workflow matches the default columns.
func workflowMatchesDefault(workflow []WorkflowColumn) bool {
	defaults := defaultWorkflowColumns()
	if len(workflow) != len(defaults) {
		return false
	}
	for i, c := range workflow {
		d := defaults[i]
		if c.Key != d.Key || c.Name != d.Name || c.Color != d.Color || c.Position != d.Position || c.IsDone != d.IsDone {
			return false
		}
	}
	return true
}

// exportAllTodosForProject exports all todos for a project.
// Shows ALL tags on todos (no user filter - collaboration-friendly)
func (s *Store) exportAllTodosForProject(ctx context.Context, projectID int64, mode Mode) ([]Todo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
		  t.id, t.project_id, t.local_id, t.title, t.body, t.column_key, t.rank, t.estimation_points, t.assignee_user_id, t.sprint_id, t.created_at, t.updated_at, t.done_at,
		  COALESCE(GROUP_CONCAT(g.name, ','), '') AS tags_csv
		FROM todos t
		LEFT JOIN todo_tags tt ON tt.todo_id = t.id
		LEFT JOIN tags g ON g.id = tt.tag_id
		LEFT JOIN sprints s ON s.id = t.sprint_id
		WHERE t.project_id = ?
		GROUP BY t.id
		ORDER BY t.rank ASC, t.id ASC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list all todos: %w", err)
	}
	defer rows.Close()

	var out []Todo
	for rows.Next() {
		var t Todo
		var columnKey string
		var createdAtMs, updatedAtMs int64
		var localID sql.NullInt64
		var estimationPoints sql.NullInt64
		var assigneeUserID sql.NullInt64
		var sprintID sql.NullInt64
		var doneAtMs sql.NullInt64
		var tagsCSV string
		if err := rows.Scan(&t.ID, &t.ProjectID, &localID, &t.Title, &t.Body, &columnKey, &t.Rank, &estimationPoints, &assigneeUserID, &sprintID, &createdAtMs, &updatedAtMs, &doneAtMs, &tagsCSV); err != nil {
			return nil, fmt.Errorf("scan todo: %w", err)
		}
		if sprintID.Valid {
			v := sprintID.Int64
			t.SprintID = &v
		}
		if !localID.Valid {
			return nil, fmt.Errorf("%w: todos.local_id is NULL (migration incomplete)", ErrConflict)
		}
		t.LocalID = localID.Int64
		t.ColumnKey = columnKey
		if estimationPoints.Valid {
			v := estimationPoints.Int64
			t.EstimationPoints = &v
		}
		if assigneeUserID.Valid {
			v := assigneeUserID.Int64
			t.AssigneeUserID = &v
		}
		t.CreatedAt = time.UnixMilli(createdAtMs).UTC()
		t.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
		if doneAtMs.Valid {
			dt := time.UnixMilli(doneAtMs.Int64).UTC()
			t.DoneAt = &dt
		}

		if tagsCSV != "" {
			// Remove duplicates and sort (GROUP_CONCAT with DISTINCT may still have duplicates if same name appears multiple times)
			tagSet := make(map[string]struct{})
			for _, tag := range strings.Split(tagsCSV, ",") {
				tagSet[tag] = struct{}{}
			}
			t.Tags = make([]string, 0, len(tagSet))
			for tag := range tagSet {
				t.Tags = append(t.Tags, tag)
			}
			sort.Strings(t.Tags)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows todos: %w", err)
	}
	return out, nil
}

// exportWallForProject returns the sticky-note wall (Scrumbaby) for a project
// as an export payload, or nil if the project has no wall row or only an empty
// document. The nil case keeps legacy-looking JSON for projects that never used
// the wall feature.
func (s *Store) exportWallForProject(ctx context.Context, projectID int64) (*WallExport, error) {
	wall, err := s.GetWall(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("get wall: %w", err)
	}
	if len(wall.Notes) == 0 && len(wall.Edges) == 0 {
		return nil, nil
	}
	return &WallExport{
		Notes:   wall.Notes,
		Edges:   wall.Edges,
		Version: wall.Version,
	}, nil
}

// exportLinksForProject returns all todo links for a project for backup export.
func (s *Store) exportLinksForProject(ctx context.Context, projectID int64) ([]LinkExport, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT from_local_id, to_local_id, link_type FROM todo_links WHERE project_id = ?
		ORDER BY from_local_id, to_local_id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}
	defer rows.Close()
	var out []LinkExport
	for rows.Next() {
		var l LinkExport
		if err := rows.Scan(&l.FromLocalID, &l.ToLocalID, &l.LinkType); err != nil {
			return nil, fmt.Errorf("scan link: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows links: %w", err)
	}
	return out, nil
}

// ImportProjects is the main import handler that routes to appropriate import mode
func (s *Store) ImportProjects(ctx context.Context, data *ExportData, mode Mode, importMode string) (*ImportResult, error) {
	return s.ImportProjectsWithTarget(ctx, data, mode, importMode, "")
}

// workflowColumnsFromExport converts export columns to WorkflowColumn, sorted by Position.
// Honors exported Position by sorting; array order in JSON may not match.
func workflowColumnsFromExport(cols []WorkflowColumnExport) []WorkflowColumn {
	sorted := make([]WorkflowColumnExport, len(cols))
	copy(sorted, cols)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Position < sorted[j].Position })

	out := make([]WorkflowColumn, 0, len(sorted))
	for i, c := range sorted {
		color := strings.TrimSpace(c.Color)
		if color == "" || !colorHexRe.MatchString(color) {
			color = "#64748b"
		}
		out = append(out, WorkflowColumn{
			Key:      strings.ToLower(strings.TrimSpace(c.Key)),
			Name:     strings.TrimSpace(c.Name),
			Color:    color,
			Position: i,
			IsDone:   c.IsDone,
			System:   false,
		})
	}
	return out
}

// validColumnKeysFromWorkflowExport builds the set of column keys from export columns.
// Assumes cols are already validated (at least 2 columns, unique keys). Used by orphan check and PreviewImport.
func validColumnKeysFromWorkflowExport(cols []WorkflowColumnExport) map[string]struct{} {
	out := make(map[string]struct{}, len(cols))
	for _, c := range cols {
		key := strings.ToLower(strings.TrimSpace(c.Key))
		if key != "" {
			out[key] = struct{}{}
		}
	}
	return out
}

// statusResolvesInWorkflow returns true if statusFromExport maps to a key in validColumnKeys.
// Used when WorkflowColumns are present: rejects non-resolvable status (validation error, not fallback).
func statusResolvesInWorkflow(statusFromExport string, validColumnKeys map[string]struct{}) bool {
	statusKey := strings.ToLower(strings.TrimSpace(statusFromExport))
	if statusKey == "" {
		return false
	}
	if _, ok := validColumnKeys[statusKey]; ok {
		return true
	}
	if s, ok := ParseStatus(statusFromExport); ok {
		mapped := StatusToColumnKey(s)
		_, ok := validColumnKeys[mapped]
		return ok
	}
	return false
}

// validateImportPreflight runs all validation before any import writes.
// Replace mode must not delete anything until this passes.
func (s *Store) validateImportPreflight(ctx context.Context, data *ExportData, mode Mode, importMode string) error {
	if data == nil || len(data.Projects) == 0 {
		return nil // Empty import is allowed
	}

	linkTypesAllowed := map[string]bool{"relates_to": true, "blocks": true, "duplicates": true, "parent": true}
	slugSet := make(map[string]bool)

	for i, pExport := range data.Projects {
		// Required: project name
		if pExport.Name == "" {
			return fmt.Errorf("%w: project at index %d missing name", ErrValidation, i)
		}
		// Slug: required for merge/copy when matching; must be unique within import (case-insensitive)
		if pExport.Slug != "" {
			slug := strings.ToLower(strings.TrimSpace(pExport.Slug))
			if slugSet[slug] {
				return fmt.Errorf("%w: duplicate slug %q in import file", ErrValidation, pExport.Slug)
			}
			slugSet[slug] = true
		} else if importMode == "merge" {
			return fmt.Errorf("%w: project %q missing slug (required for merge)", ErrValidation, pExport.Name)
		}

		// Validate workflow columns when provided
		var validColumnKeys map[string]struct{}
		if len(pExport.WorkflowColumns) > 0 {
			if len(pExport.WorkflowColumns) < 2 {
				return fmt.Errorf("%w: project %q workflowColumns must have at least 2 columns", ErrValidation, pExport.Name)
			}
			seenKeys := make(map[string]struct{})
			doneCount := 0
			for j, c := range pExport.WorkflowColumns {
				key := strings.TrimSpace(c.Key)
				if key == "" {
					return fmt.Errorf("%w: project %q workflow column at index %d has empty key", ErrValidation, pExport.Name, j)
				}
				keyLower := strings.ToLower(key)
				if !isValidColumnKey(key) {
					return fmt.Errorf("%w: project %q workflow column key %q is invalid", ErrValidation, pExport.Name, key)
				}
				if _, ok := seenKeys[keyLower]; ok {
					return fmt.Errorf("%w: project %q workflow column key %q is duplicate", ErrValidation, pExport.Name, key)
				}
				seenKeys[keyLower] = struct{}{}
				if strings.TrimSpace(c.Name) == "" {
					return fmt.Errorf("%w: project %q workflow column %q has empty name", ErrValidation, pExport.Name, key)
				}
				color := strings.TrimSpace(c.Color)
				if color != "" && !colorHexRe.MatchString(color) {
					return fmt.Errorf("%w: project %q workflow column %q has invalid color %q", ErrValidation, pExport.Name, key, c.Color)
				}
				if c.IsDone {
					doneCount++
				}
			}
			if doneCount != 1 {
				return fmt.Errorf("%w: project %q workflow must have exactly one done column, got %d", ErrValidation, pExport.Name, doneCount)
			}
			validColumnKeys = seenKeys
		}

		// Validate sprints when provided
		var validSprintNumbers map[int64]struct{}
		if len(pExport.Sprints) > 0 {
			validSprintNumbers = make(map[int64]struct{})
			sprintNumbers := make(map[int64]struct{})
			sprintStates := map[string]bool{SprintStatePlanned: true, SprintStateActive: true, SprintStateClosed: true}
			for _, sp := range pExport.Sprints {
				if _, ok := sprintNumbers[sp.Number]; ok {
					return fmt.Errorf("%w: project %q has duplicate sprint number %d", ErrValidation, pExport.Name, sp.Number)
				}
				sprintNumbers[sp.Number] = struct{}{}
				validSprintNumbers[sp.Number] = struct{}{}
				if sp.PlannedEndAt < sp.PlannedStartAt {
					return fmt.Errorf("%w: project %q sprint %d has plannedEndAt before plannedStartAt", ErrValidation, pExport.Name, sp.Number)
				}
				state := strings.TrimSpace(sp.State)
				if state != "" && !sprintStates[state] {
					return fmt.Errorf("%w: project %q sprint %d has invalid state %q", ErrValidation, pExport.Name, sp.Number, sp.State)
				}
			}
		}

		// Build todo localId set for link validation
		todoLocalIDs := make(map[int64]struct{})
		for _, t := range pExport.Todos {
			if t.LocalID == 0 {
				return fmt.Errorf("%w: todo in project %q missing localId", ErrValidation, pExport.Name)
			}
			if t.Title == "" {
				return fmt.Errorf("%w: todo localId %d in project %q missing title", ErrValidation, t.LocalID, pExport.Name)
			}
			if err := validateEstimationPoints(t.EstimationPoints); err != nil {
				return fmt.Errorf("%w: todo %q: %v", ErrValidation, t.Title, err)
			}
			if validColumnKeys != nil {
				if !statusResolvesInWorkflow(t.Status, validColumnKeys) {
					return fmt.Errorf("%w: todo %q in project %q: unknown workflow column %q (not in backup workflowColumns)", ErrValidation, t.Title, pExport.Name, t.Status)
				}
			}
			if t.SprintNumber != nil {
				if validSprintNumbers == nil {
					return fmt.Errorf("%w: todo %q in project %q: SprintNumber %d but project has no sprints in backup", ErrValidation, t.Title, pExport.Name, *t.SprintNumber)
				}
				if _, ok := validSprintNumbers[*t.SprintNumber]; !ok {
					return fmt.Errorf("%w: todo %q in project %q: SprintNumber %d does not match any sprint in backup", ErrValidation, t.Title, pExport.Name, *t.SprintNumber)
				}
			}
			todoLocalIDs[t.LocalID] = struct{}{}
		}

		// Validate links reference existing todos
		for _, l := range pExport.Links {
			if l.FromLocalID == l.ToLocalID {
				return fmt.Errorf("%w: invalid link in project %q: self-link %d->%d", ErrValidation, pExport.Name, l.FromLocalID, l.ToLocalID)
			}
			if _, ok := todoLocalIDs[l.FromLocalID]; !ok {
				return fmt.Errorf("%w: invalid link in project %q: fromLocalId %d does not match any todo", ErrValidation, pExport.Name, l.FromLocalID)
			}
			if _, ok := todoLocalIDs[l.ToLocalID]; !ok {
				return fmt.Errorf("%w: invalid link in project %q: toLocalId %d does not match any todo", ErrValidation, pExport.Name, l.ToLocalID)
			}
			lt := l.LinkType
			if lt == "" {
				lt = "relates_to"
			}
			if !linkTypesAllowed[lt] {
				return fmt.Errorf("%w: invalid link type %q in project %q (link %d->%d)", ErrValidation, l.LinkType, pExport.Name, l.FromLocalID, l.ToLocalID)
			}
		}

		// Validate tag names (basic)
		for _, tag := range pExport.Tags {
			if tag.Name == "" {
				return fmt.Errorf("%w: project %q has tag with empty name", ErrValidation, pExport.Name)
			}
		}
	}

	// Orphan check: merge mode must not replace workflow if existing todos would reference removed columns
	// Skip for anonymous mode (no durable projects to merge into).
	if importMode == "merge" && mode != ModeAnonymous && len(data.Projects) > 0 {
		whereClause, args, err := s.getExportableProjectsSelector(ctx, mode)
		if err != nil {
			return fmt.Errorf("orphan check: %w", err)
		}
		for _, pExport := range data.Projects {
			if pExport.Slug == "" || len(pExport.WorkflowColumns) < 2 {
				continue
			}
			var projectID int64
			if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT id FROM projects WHERE slug = ? AND (%s)`, whereClause), append([]any{pExport.Slug}, args...)...).Scan(&projectID); err == sql.ErrNoRows {
				continue // new project, no orphan risk
			} else if err != nil {
				return fmt.Errorf("orphan check project %q: %w", pExport.Slug, err)
			}
			backupLocalIDs := make(map[int64]struct{})
			for _, t := range pExport.Todos {
				backupLocalIDs[t.LocalID] = struct{}{}
			}
			newKeys := validColumnKeysFromWorkflowExport(pExport.WorkflowColumns)
			rows, err := s.db.QueryContext(ctx, `SELECT local_id, column_key FROM todos WHERE project_id = ?`, projectID)
			if err != nil {
				return fmt.Errorf("orphan check todos: %w", err)
			}
			var stranded []string
			strandedSeen := make(map[string]struct{})
			for rows.Next() {
				var localID int64
				var columnKey string
				if err := rows.Scan(&localID, &columnKey); err != nil {
					rows.Close()
					return fmt.Errorf("orphan check scan: %w", err)
				}
				if _, inBackup := backupLocalIDs[localID]; inBackup {
					continue
				}
				keyLower := strings.ToLower(strings.TrimSpace(columnKey))
				if keyLower == "" {
					continue
				}
				if _, ok := newKeys[keyLower]; ok {
					continue
				}
				if _, seen := strandedSeen[keyLower]; !seen {
					strandedSeen[keyLower] = struct{}{}
					stranded = append(stranded, keyLower)
				}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return fmt.Errorf("orphan check rows: %w", err)
			}
			if len(stranded) > 0 {
				sort.Strings(stranded)
				return fmt.Errorf("%w: cannot replace workflow for project %q: existing todos reference removed columns: %s", ErrValidation, pExport.Slug, strings.Join(stranded, ", "))
			}
		}
	}

	// Auth: for replace/merge in full mode, verify maintainer on all existing target projects
	if mode == ModeFull && (importMode == "replace" || importMode == "merge") {
		userID, hasUser := UserIDFromContext(ctx)
		for _, p := range data.Projects {
			if p.Slug == "" {
				continue
			}
			var projectID int64
			var expiresAt sql.NullInt64
			if err := s.db.QueryRowContext(ctx, `SELECT id, expires_at FROM projects WHERE slug = ? AND import_batch_id IS NULL`, p.Slug).Scan(&projectID, &expiresAt); err == sql.ErrNoRows {
				continue
			} else if err != nil {
				return fmt.Errorf("check existing project: %w", err)
			}
			if !expiresAt.Valid {
				if !hasUser {
					return fmt.Errorf("%w: cannot import into project %q: authentication required", ErrUnauthorized, p.Slug)
				}
				ok, err := s.userHasProjectRole(ctx, projectID, userID, RoleMaintainer)
				if err != nil {
					return fmt.Errorf("check project role: %w", err)
				}
				if !ok {
					return fmt.Errorf("%w: cannot import into project %q: maintainer role required", ErrUnauthorized, p.Slug)
				}
			}
		}
	}

	return nil
}

// ImportProjectsWithTarget imports with an optional target slug for merging into existing board
func (s *Store) ImportProjectsWithTarget(ctx context.Context, data *ExportData, mode Mode, importMode string, targetSlug string) (*ImportResult, error) {
	// Validate JSON structure and version
	if data.Version != version.ExportFormatVersion {
		return nil, fmt.Errorf("%w: unsupported export version %q (expected %s)", ErrValidation, data.Version, version.ExportFormatVersion)
	}

	// Validate scope compatibility
	if data.Scope == "full" && mode == ModeAnonymous {
		return nil, fmt.Errorf("%w: cannot import full scope into anonymous mode", ErrValidation)
	}

	// Reject Replace All in anonymous mode
	if importMode == "replace" && mode == ModeAnonymous {
		return nil, fmt.Errorf("%w: Replace All is forbidden in anonymous mode", ErrValidation)
	}

	// If targetSlug is provided in anonymous mode, import into that board
	if targetSlug != "" && mode == ModeAnonymous {
		log.Printf("ImportProjectsWithTarget: Importing into target board %s", targetSlug)
		return s.importIntoBoard(ctx, data, mode, targetSlug)
	}

	// Preflight validation: must pass before any destructive action (replace delete, merge/replace writes)
	if err := s.validateImportPreflight(ctx, data, mode, importMode); err != nil {
		return nil, err
	}

	// Route to appropriate import handler
	switch importMode {
	case "replace":
		return s.importReplaceAll(ctx, data, mode)
	case "merge":
		return s.importMergeUpdate(ctx, data, mode)
	case "copy":
		return s.importCreateCopy(ctx, data, mode)
	default:
		return nil, fmt.Errorf("%w: invalid import mode %q", ErrValidation, importMode)
	}
}

// importIntoBoard imports todos and tags into an existing board (anonymous mode only).
// Wall (Scrumbaby) payloads are intentionally skipped here: the target is an
// anonymous temporary board (enforced below via ExpiresAt != nil), and the wall
// feature is durable-project only. Folding multiple source walls into one
// target wall has no unambiguous merge policy either, so we leave the field
// alone rather than guess.
func (s *Store) importIntoBoard(ctx context.Context, data *ExportData, mode Mode, targetSlug string) (*ImportResult, error) {
	result := &ImportResult{Warnings: []string{}}

	// Get target project by slug
	targetProject, err := s.GetProjectBySlug(ctx, targetSlug)
	if err != nil {
		return nil, fmt.Errorf("get target project: %w", err)
	}

	// Verify it's an anonymous board
	if targetProject.ExpiresAt == nil {
		return nil, fmt.Errorf("%w: target board is not an anonymous board", ErrValidation)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Import all todos from all projects in the export into the target board
	for _, pExport := range data.Projects {
		for _, tExport := range pExport.Todos {
			// Create todo with new local_id (find max and increment)
			var maxLocalID sql.NullInt64
			if err := tx.QueryRowContext(ctx, `SELECT MAX(local_id) FROM todos WHERE project_id = ?`, targetProject.ID).Scan(&maxLocalID); err != nil {
				return nil, fmt.Errorf("get max local_id: %w", err)
			}
			newLocalID := int64(1)
			if maxLocalID.Valid {
				newLocalID = maxLocalID.Int64 + 1
			}

			columnKey, warned := resolveImportColumnKey(ctx, tx, targetProject.ID, tExport.Status)
			if warned {
				result.Warnings = append(result.Warnings, fmt.Sprintf("Unknown status %q for todo %q, defaulting to backlog", tExport.Status, tExport.Title))
			}
			if err := validateEstimationPoints(tExport.EstimationPoints); err != nil {
				return nil, err
			}

			createdAtMs := tExport.CreatedAt.UnixMilli()
			updatedAtMs := tExport.UpdatedAt.UnixMilli()
			var estimationPoints any
			if tExport.EstimationPoints != nil {
				estimationPoints = *tExport.EstimationPoints
			}
			assigneeVal := resolveImportAssignee(ctx, tx, targetProject.ID, tExport.AssigneeUserId)
			var assigneeForSQL any
			if assigneeVal != nil {
				assigneeForSQL = *assigneeVal
			}

			res, err := tx.ExecContext(ctx, `
				INSERT INTO todos(project_id, local_id, title, body, column_key, rank, estimation_points, assignee_user_id, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				targetProject.ID, newLocalID, tExport.Title, tExport.Body, columnKey, tExport.Rank, estimationPoints, assigneeForSQL, createdAtMs, updatedAtMs)
			if err != nil {
				return nil, fmt.Errorf("insert todo: %w", err)
			}

			todoID, err := res.LastInsertId()
			if err != nil {
				return nil, fmt.Errorf("get todo id: %w", err)
			}

			// Import tags for this todo
			if err := s.importTodoTags(ctx, tx, targetProject.ID, todoID, tExport.Tags, mode); err != nil {
				return nil, fmt.Errorf("import todo tags: %w", err)
			}

			result.Created++
		}

		// Import tag color preferences
		for _, tagExport := range pExport.Tags {
			if err := s.importTag(ctx, tx, targetProject.ID, tagExport.Name, tagExport.Color, mode); err != nil {
				return nil, fmt.Errorf("import tag: %w", err)
			}
		}

		if err := bulkInsertLinks(ctx, tx, targetProject.ID, pExport.Links); err != nil {
			return nil, fmt.Errorf("import links: %w", err)
		}
	}

	// Update last_activity_at
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET last_activity_at = ? WHERE id = ?`, time.Now().UTC().UnixMilli(), targetProject.ID); err != nil {
		return nil, fmt.Errorf("update last_activity_at: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	result.Imported = len(data.Projects)
	log.Printf("importIntoBoard: Imported %d todos into board %s", result.Created, targetSlug)
	return result, nil
}

// importReplaceAll implements Replace All mode (nuke & restore) using staging approach
func (s *Store) importReplaceAll(ctx context.Context, data *ExportData, mode Mode) (*ImportResult, error) {
	result := &ImportResult{Warnings: []string{}}

	// Early validation: check for duplicate slugs (skip empty slugs - they'll be generated)
	// Name is required, slug is optional
	slugSet := make(map[string]bool)
	for i, pExport := range data.Projects {
		// Name is required
		if pExport.Name == "" {
			return nil, fmt.Errorf("%w: project at index %d missing name", ErrValidation, i)
		}

		// Slug is optional, but if provided, must be unique within import file (case-insensitive)
		if pExport.Slug != "" {
			slug := strings.ToLower(strings.TrimSpace(pExport.Slug))
			if slugSet[slug] {
				return nil, fmt.Errorf("%w: duplicate slug %q in import file", ErrValidation, pExport.Slug)
			}
			slugSet[slug] = true
		}
	}

	// Generate unique batch ID
	batchID := generateUUID()
	cleanupNeeded := true

	// Acquire dedicated connection
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Close()

	// Defer cleanup: single point of failure handling
	defer func() {
		if cleanupNeeded {
			// Best-effort cleanup: log but don't fail
			if _, err := conn.ExecContext(ctx, "DELETE FROM projects WHERE import_batch_id = ?", batchID); err != nil {
				log.Printf("warning: failed to cleanup staging projects: %v", err)
			}
		}
	}()

	// Configure SQLite
	if err := configureSQLiteForImport(ctx, conn); err != nil {
		return nil, fmt.Errorf("configure SQLite: %w", err)
	}
	defer func() {
		if err := restoreSQLiteDefaults(ctx, conn); err != nil {
			log.Printf("warning: failed to restore SQLite defaults: %v", err)
		}
	}()

	// Phase A: Import into staging (per-project transactions)
	for _, pExport := range data.Projects {
		// Validate: Name is required, slug is optional (will be generated if missing)
		if pExport.Name == "" {
			return nil, fmt.Errorf("%w: project missing name", ErrValidation)
		}
		// Note: pExport.Slug can be empty - will be generated in insertProjectWithBatchID

		tx, err := conn.BeginTx(ctx, &sql.TxOptions{})
		if err != nil {
			return nil, fmt.Errorf("begin transaction: %w", err)
		}

		// Insert project with import_batch_id
		projectID, err := insertProjectWithBatchID(ctx, tx, pExport, mode, batchID)
		if err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("insert project %q: %w", pExport.Name, err)
		}

		// Workflow columns: custom from export or default
		if len(pExport.WorkflowColumns) >= 2 {
			if err := s.deleteProjectWorkflowColumnsExec(ctx, tx, projectID); err != nil {
				tx.Rollback()
				return nil, fmt.Errorf("clear workflow columns for project %q: %w", pExport.Name, err)
			}
			cols := workflowColumnsFromExport(pExport.WorkflowColumns)
			if err := s.insertWorkflowColumnsExec(ctx, tx, projectID, cols); err != nil {
				tx.Rollback()
				return nil, fmt.Errorf("import workflow columns for project %q: %w", pExport.Name, err)
			}
		} else {
			if err := s.ensureDefaultWorkflowColumnsExec(ctx, tx, tx, projectID); err != nil {
				tx.Rollback()
				return nil, fmt.Errorf("ensure workflow columns for project %q: %w", pExport.Name, err)
			}
		}

		sprintIDByNumber, err := insertSprintsForImport(ctx, tx, projectID, pExport.Sprints)
		if err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("import sprints for project %q: %w", pExport.Name, err)
		}

		// Bulk insert todos (strict mode for Replace)
		todoIDMap, err := bulkInsertTodos(ctx, tx, projectID, pExport.Todos, true, &result.Warnings, sprintIDByNumber)
		if err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("import todos for project %q: %w", pExport.Name, err)
		}

		// Bulk upsert tags (mode parameter kept for backward compatibility but not used)
		tagIDMap, err := bulkUpsertTags(ctx, tx, projectID, pExport.Tags, mode)
		if err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("import tags for project %q: %w", pExport.Name, err)
		}

		// Bulk link todos to tags
		todoTagMap := buildTodoTagMap(pExport.Todos, todoIDMap)
		if err := bulkLinkTodoTags(ctx, tx, todoTagMap, tagIDMap); err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("link tags for project %q: %w", pExport.Name, err)
		}

		if err := bulkInsertLinks(ctx, tx, projectID, pExport.Links); err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("import links for project %q: %w", pExport.Name, err)
		}

		// Replace mode: a missing wall field in the backup produces no wall row,
		// which matches the "nuke & restore" semantics (old rows were deleted by
		// the project-level swap; ON DELETE CASCADE clears the wall with them).
		if err := upsertWallForImportTx(ctx, tx, projectID, pExport.Wall); err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("import wall for project %q: %w", pExport.Name, err)
		}

		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit project %q: %w", pExport.Name, err)
		}

		result.Created++
	}

	// Pre-promotion validation: verify staging slugs are unique (case-insensitive)
	// This prevents promotion failure when import_batch_id is set to NULL
	var duplicateSlug string
	err = conn.QueryRowContext(ctx, `
		SELECT slug FROM projects
		WHERE import_batch_id = ?
		GROUP BY LOWER(slug)
		HAVING COUNT(*) > 1
		LIMIT 1
	`, batchID).Scan(&duplicateSlug)
	if err == nil {
		// Found duplicate slug in staging
		return nil, fmt.Errorf("%w: duplicate slug %q in staging (promotion would fail)", ErrValidation, duplicateSlug)
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("check staging slug uniqueness: %w", err)
	}

	// Phase B: Atomic swap (single short transaction)
	txSwap, err := conn.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin swap transaction: %w", err)
	}

	// Delete old export-scope projects (CRITICAL: exclude staging)
	whereClause, args, err := s.getExportableProjectsSelectorTx(ctx, txSwap, mode)
	if err != nil {
		txSwap.Rollback()
		return nil, err
	}

	deleteQuery := fmt.Sprintf(
		"DELETE FROM projects WHERE (%s) AND import_batch_id IS NULL",
		whereClause)

	_, err = txSwap.ExecContext(ctx, deleteQuery, args...)
	if err != nil {
		txSwap.Rollback()
		return nil, fmt.Errorf("delete old projects: %w", err)
	}

	// Promote staging to production
	_, err = txSwap.ExecContext(ctx,
		"UPDATE projects SET import_batch_id = NULL WHERE import_batch_id = ?", batchID)
	if err != nil {
		txSwap.Rollback()
		return nil, fmt.Errorf("promote staging: %w", err)
	}

	if err := txSwap.Commit(); err != nil {
		return nil, fmt.Errorf("commit swap: %w", err)
	}

	// Success: disable cleanup
	cleanupNeeded = false

	return result, nil
}

// importMergeUpdate implements Merge/Update mode (match by slug)
func (s *Store) importMergeUpdate(ctx context.Context, data *ExportData, mode Mode) (*ImportResult, error) {
	result := &ImportResult{Warnings: []string{}}

	// In anonymous mode, all imported projects are new (no matching).
	// Short-circuit before opening a transaction so we don't hold a connection
	// while delegating to importCreateCopy (which opens its own transaction).
	if mode == ModeAnonymous {
		return s.importCreateCopy(ctx, data, mode)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	whereClause, args, err := s.getExportableProjectsSelectorTx(ctx, tx, mode)
	if err != nil {
		return nil, err
	}

	// Authorization: verify maintainer on ALL existing target projects before any write.
	// If import would affect a project where user is not maintainer, fail atomically.
	userID, hasUser := UserIDFromContext(ctx)
	for _, pExport := range data.Projects {
		if pExport.Slug == "" {
			continue
		}
		var projectID int64
		var expiresAt sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT id, expires_at FROM projects WHERE slug = ? AND import_batch_id IS NULL`, pExport.Slug).Scan(&projectID, &expiresAt)
		if err == sql.ErrNoRows {
			continue // New project, no auth needed
		}
		if err != nil {
			return nil, fmt.Errorf("check existing project: %w", err)
		}
		// Full project: require maintainer (system roles do not grant this)
		if !expiresAt.Valid {
			if !hasUser {
				return nil, fmt.Errorf("%w: cannot merge into project %q: authentication required", ErrUnauthorized, pExport.Slug)
			}
			ok, err := s.userHasProjectRoleTx(ctx, tx, projectID, userID, RoleMaintainer)
			if err != nil {
				return nil, fmt.Errorf("check project role: %w", err)
			}
			if !ok {
				return nil, fmt.Errorf("%w: cannot merge into project %q: maintainer role required", ErrUnauthorized, pExport.Slug)
			}
		}
	}

	// Build project slug map for matching
	projectMap := make(map[string]*ProjectExport)
	for i := range data.Projects {
		projectMap[data.Projects[i].Slug] = &data.Projects[i]
	}

	// Process each project in import
	for _, pExport := range data.Projects {
		// Validate required fields
		if pExport.Slug == "" {
			return nil, fmt.Errorf("%w: project missing slug", ErrValidation)
		}
		if pExport.Name == "" {
			return nil, fmt.Errorf("%w: project missing name", ErrValidation)
		}

		// Try to find existing project by slug
		var existingProjectID sql.NullInt64
		var existingOwnerUserID sql.NullInt64
		var existingExpiresAt sql.NullInt64

		// Match by slug with ownership check
		err = tx.QueryRowContext(ctx, fmt.Sprintf(`
			SELECT id, owner_user_id, expires_at
			FROM projects
			WHERE slug = ? AND (%s)`, whereClause), append([]any{pExport.Slug}, args...)...).Scan(
			&existingProjectID, &existingOwnerUserID, &existingExpiresAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("find project: %w", err)
		}

		var projectID int64

		if existingProjectID.Valid {
			// Update existing project
			projectID = existingProjectID.Int64

			nowMs := time.Now().UTC().UnixMilli()
			image := pExport.Image
			if image == nil {
				defaultImage := "/scrumboy.png"
				image = &defaultImage
			}
			dominantColor := pExport.DominantColor
			if dominantColor == "" {
				dominantColor = "#888888"
			}

			// Update mutable fields (name, image, dominant_color, default_sprint_weeks, updated_at)
			defaultSprintWeeks := 2
			if pExport.DefaultSprintWeeks == 1 || pExport.DefaultSprintWeeks == 2 {
				defaultSprintWeeks = pExport.DefaultSprintWeeks
			}
			_, err = tx.ExecContext(ctx, `
				UPDATE projects SET name = ?, image = ?, dominant_color = ?, default_sprint_weeks = ?, updated_at = ?
				WHERE id = ?`,
				pExport.Name, image, dominantColor, defaultSprintWeeks, nowMs, projectID)
			if err != nil {
				return nil, fmt.Errorf("update project: %w", err)
			}

			// Workflow columns: custom from export or leave as-is
			if len(pExport.WorkflowColumns) >= 2 {
				if err := s.deleteProjectWorkflowColumnsExec(ctx, tx, projectID); err != nil {
					return nil, fmt.Errorf("clear workflow columns for project %q: %w", pExport.Name, err)
				}
				cols := workflowColumnsFromExport(pExport.WorkflowColumns)
				if err := s.insertWorkflowColumnsExec(ctx, tx, projectID, cols); err != nil {
					return nil, fmt.Errorf("import workflow columns for project %q: %w", pExport.Name, err)
				}
			}

			result.Updated++
		} else {
			// Create new project
			slug := pExport.Slug
			baseSlug, err := generateSlugFromName(pExport.Name)
			if err != nil {
				slug, err = randomSlug(8)
				if err != nil {
					return nil, fmt.Errorf("generate slug: %w", err)
				}
			} else {
				slug = baseSlug
			}

			// Ensure slug uniqueness
			for i := 0; i < 100; i++ {
				if i > 0 {
					suffix := fmt.Sprintf("-%d", i+1)
					maxBaseLen := 32 - len(suffix)
					if len(baseSlug) > maxBaseLen {
						base := strings.TrimRight(baseSlug[:maxBaseLen], "-")
						slug = base + suffix
					} else {
						slug = baseSlug + suffix
					}
				}

				var exists bool
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE slug = ?)`, slug).Scan(&exists); err != nil {
					return nil, fmt.Errorf("check slug: %w", err)
				}
				if !exists {
					break
				}
				if i == 99 {
					return nil, fmt.Errorf("%w: could not generate unique slug", ErrConflict)
				}
			}

			if slug != pExport.Slug {
				result.Warnings = append(result.Warnings, fmt.Sprintf("Slug %q rewritten to %q", pExport.Slug, slug))
			}

			var ownerUserID *int64
			if mode == ModeFull {
				enabled, err := authEnabledTx(ctx, tx)
				if err != nil {
					return nil, err
				}
				if enabled {
					userID, ok := UserIDFromContext(ctx)
					if !ok {
						return nil, ErrUnauthorized
					}
					ownerUserID = &userID
				}
			}

			nowMs := time.Now().UTC().UnixMilli()
			createdAtMs := pExport.CreatedAt.UnixMilli()
			updatedAtMs := pExport.UpdatedAt.UnixMilli()

			image := pExport.Image
			if image == nil {
				defaultImage := "/scrumboy.png"
				image = &defaultImage
			}
			dominantColor := pExport.DominantColor
			if dominantColor == "" {
				dominantColor = "#888888"
			}

			var expiresAtMs sql.NullInt64
			if pExport.ExpiresAt != nil {
				// For temporary boards, give a fresh expiration date instead of preserving the old one
				expiresAtMs.Valid = true
				if mode == ModeAnonymous {
					// Anonymous boards: 14 days from now
					expiresAtMs.Int64 = time.Now().UTC().Add(14 * 24 * time.Hour).UnixMilli()
				} else {
					// Full mode temp boards: preserve original expiration (user's choice)
					expiresAtMs.Int64 = pExport.ExpiresAt.UnixMilli()
				}
			}

			// Set creator_user_id for temporary boards (importing user becomes creator)
			var creatorUserID *int64
			if pExport.ExpiresAt != nil {
				userID, ok := UserIDFromContext(ctx)
				if ok {
					creatorUserID = &userID
				}
			}

			// Import always uses EstimationModeModifiedFibonacci; pExport.EstimationMode from JSON is ignored.
			defaultSprintWeeks := 2
			if pExport.DefaultSprintWeeks == 1 || pExport.DefaultSprintWeeks == 2 {
				defaultSprintWeeks = pExport.DefaultSprintWeeks
			}
			res, err := tx.ExecContext(ctx, `
				INSERT INTO projects(name, image, dominant_color, slug, estimation_mode, default_sprint_weeks, owner_user_id, creator_user_id, last_activity_at, expires_at, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				pExport.Name, image, dominantColor, slug, EstimationModeModifiedFibonacci, defaultSprintWeeks, ownerUserID, creatorUserID, nowMs, expiresAtMs, createdAtMs, updatedAtMs)
			if err != nil {
				return nil, fmt.Errorf("insert project: %w", err)
			}

			projectID, err = res.LastInsertId()
			if err != nil {
				return nil, fmt.Errorf("last insert id: %w", err)
			}

			// So ListProjects returns this project immediately (it filters by project_members).
			if ownerUserID != nil {
				_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO project_members (project_id, user_id, role, created_at) VALUES (?, ?, 'maintainer', ?)`, projectID, *ownerUserID, nowMs)
				if err != nil {
					return nil, fmt.Errorf("ensure maintainer membership for imported project: %w", err)
				}
			}

			// Workflow columns: custom from export or default
			if len(pExport.WorkflowColumns) >= 2 {
				if err := s.deleteProjectWorkflowColumnsExec(ctx, tx, projectID); err != nil {
					return nil, fmt.Errorf("clear workflow columns for project %q: %w", pExport.Name, err)
				}
				cols := workflowColumnsFromExport(pExport.WorkflowColumns)
				if err := s.insertWorkflowColumnsExec(ctx, tx, projectID, cols); err != nil {
					return nil, fmt.Errorf("import workflow columns for project %q: %w", pExport.Name, err)
				}
			} else {
				if err := s.ensureDefaultWorkflowColumnsExec(ctx, tx, tx, projectID); err != nil {
					return nil, fmt.Errorf("ensure workflow columns for project %q: %w", pExport.Name, err)
				}
			}

			result.Created++
		}

		sprintIDByNumber, err := insertSprintsForImport(ctx, tx, projectID, pExport.Sprints)
		if err != nil {
			return nil, fmt.Errorf("import sprints for project %q: %w", pExport.Name, err)
		}

		// Import todos - (project_id, local_id) is stable identity
		for _, tExport := range pExport.Todos {
			// Validate required fields
			if tExport.LocalID == 0 {
				return nil, fmt.Errorf("%w: todo missing localId", ErrValidation)
			}
			if tExport.Title == "" {
				return nil, fmt.Errorf("%w: todo missing title", ErrValidation)
			}

			columnKey, warned := resolveImportColumnKey(ctx, tx, projectID, tExport.Status)
			if warned {
				result.Warnings = append(result.Warnings, fmt.Sprintf("Unknown status %q for todo %q, defaulting to backlog", tExport.Status, tExport.Title))
			}
			if err := validateEstimationPoints(tExport.EstimationPoints); err != nil {
				return nil, err
			}
			var estimationPoints any
			if tExport.EstimationPoints != nil {
				estimationPoints = *tExport.EstimationPoints
			}

			status := StatusBacklog
			if s, ok := ParseStatus(tExport.Status); ok {
				status = s
			}

			// Check if todo exists
			var existingTodoID sql.NullInt64
			err := tx.QueryRowContext(ctx, `
				SELECT id FROM todos WHERE project_id = ? AND local_id = ?`,
				projectID, tExport.LocalID).Scan(&existingTodoID)

			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("check todo: %w", err)
			}

			updatedAtMs := tExport.UpdatedAt.UnixMilli()
			assigneeVal := resolveImportAssignee(ctx, tx, projectID, tExport.AssigneeUserId)
			doneAtMs := resolveImportDoneAt(tExport.DoneAt, status, updatedAtMs)

			var sprintIDForSQL any
			if tExport.SprintNumber != nil && sprintIDByNumber != nil {
				if id, ok := sprintIDByNumber[*tExport.SprintNumber]; ok {
					sprintIDForSQL = id
				}
			}

			if existingTodoID.Valid {
				// Update existing todo - mutable fields; set assignee only when backup has one and it resolves (otherwise preserve existing)
				if assigneeVal != nil {
					_, err = tx.ExecContext(ctx, `
						UPDATE todos SET title = ?, body = ?, column_key = ?, rank = ?, estimation_points = ?, assignee_user_id = ?, sprint_id = ?, updated_at = ?, done_at = ?
						WHERE id = ?`,
						tExport.Title, tExport.Body, columnKey, tExport.Rank, estimationPoints, *assigneeVal, sprintIDForSQL, updatedAtMs, doneAtMs, existingTodoID.Int64)
				} else {
					_, err = tx.ExecContext(ctx, `
						UPDATE todos SET title = ?, body = ?, column_key = ?, rank = ?, estimation_points = ?, sprint_id = ?, updated_at = ?, done_at = ?
						WHERE id = ?`,
						tExport.Title, tExport.Body, columnKey, tExport.Rank, estimationPoints, sprintIDForSQL, updatedAtMs, doneAtMs, existingTodoID.Int64)
				}
				if err != nil {
					return nil, fmt.Errorf("update todo: %w", err)
				}

				// Update tags
				if err := s.importTodoTags(ctx, tx, projectID, existingTodoID.Int64, tExport.Tags, mode); err != nil {
					return nil, fmt.Errorf("import todo tags: %w", err)
				}

				result.Updated++
			} else {
				// Create new todo with that localId
				createdAtMs := tExport.CreatedAt.UnixMilli()
				assigneeValNew := resolveImportAssignee(ctx, tx, projectID, tExport.AssigneeUserId)
				var assigneeForSQLNew any
				if assigneeValNew != nil {
					assigneeForSQLNew = *assigneeValNew
				}

				// Check for local_id collision
				var maxLocalID int64
				if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(local_id), 0) FROM todos WHERE project_id = ?`, projectID).Scan(&maxLocalID); err != nil {
					return nil, fmt.Errorf("get max local_id: %w", err)
				}
				if tExport.LocalID <= maxLocalID {
					// Collision - need to regenerate
					oldLocalID := tExport.LocalID
					tExport.LocalID = maxLocalID + 1
					result.Warnings = append(result.Warnings, fmt.Sprintf("Todo localId %d collided, regenerated to %d", oldLocalID, tExport.LocalID))
				}

				doneAtForInsert := resolveImportDoneAt(tExport.DoneAt, status, updatedAtMs)
				_, err = tx.ExecContext(ctx, `
					INSERT INTO todos(project_id, local_id, title, body, column_key, rank, estimation_points, assignee_user_id, sprint_id, created_at, updated_at, done_at)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					projectID, tExport.LocalID, tExport.Title, tExport.Body, columnKey, tExport.Rank, estimationPoints, assigneeForSQLNew, sprintIDForSQL, createdAtMs, updatedAtMs, doneAtForInsert)
				if err != nil {
					if strings.Contains(err.Error(), "UNIQUE constraint failed: todos.project_id, todos.local_id") {
						// Still collided, regenerate
						maxLocalID++
						tExport.LocalID = maxLocalID
						result.Warnings = append(result.Warnings, fmt.Sprintf("Todo localId collided again, regenerated to %d", tExport.LocalID))
						_, err = tx.ExecContext(ctx, `
							INSERT INTO todos(project_id, local_id, title, body, column_key, rank, estimation_points, assignee_user_id, sprint_id, created_at, updated_at, done_at)
							VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
							projectID, tExport.LocalID, tExport.Title, tExport.Body, columnKey, tExport.Rank, estimationPoints, assigneeForSQLNew, sprintIDForSQL, createdAtMs, updatedAtMs, doneAtForInsert)
						if err != nil {
							return nil, fmt.Errorf("insert todo with regenerated local_id: %w", err)
						}
					} else {
						return nil, fmt.Errorf("insert todo: %w", err)
					}
				}

				// Get todo ID
				var todoID int64
				if err := tx.QueryRowContext(ctx, `SELECT id FROM todos WHERE project_id = ? AND local_id = ?`, projectID, tExport.LocalID).Scan(&todoID); err != nil {
					return nil, fmt.Errorf("get todo id: %w", err)
				}

				// Import tags
				if err := s.importTodoTags(ctx, tx, projectID, todoID, tExport.Tags, mode); err != nil {
					return nil, fmt.Errorf("import todo tags: %w", err)
				}

				result.Created++
			}
		}

		// Import tags - upsert by (project_id, name)
		for _, tagExport := range pExport.Tags {
			if err := s.importTag(ctx, tx, projectID, tagExport.Name, tagExport.Color, mode); err != nil {
				return nil, fmt.Errorf("import tag: %w", err)
			}
		}

		// Merge mode: replace project links with backup so result strictly mirrors backup.
		// (Replace/Copy/ImportIntoBoard create new projects or add to target; only merge updates existing.)
		if _, err := tx.ExecContext(ctx, `DELETE FROM todo_links WHERE project_id = ?`, projectID); err != nil {
			return nil, fmt.Errorf("clear links for merge: %w", err)
		}
		if err := bulkInsertLinks(ctx, tx, projectID, pExport.Links); err != nil {
			return nil, fmt.Errorf("import links: %w", err)
		}

		// Merge mode wall policy: when the backup carries a wall payload,
		// replace the local wall with it (matches the links policy above).
		// When the backup has no wall field - e.g. a pre-3.14 export - leave
		// the target project's existing wall untouched so upgraders don't lose
		// work done between export and import.
		if pExport.Wall != nil {
			if err := upsertWallForImportTx(ctx, tx, projectID, pExport.Wall); err != nil {
				return nil, fmt.Errorf("import wall for project %q: %w", pExport.Name, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	result.Imported = len(data.Projects)
	return result, nil
}

// importCreateCopy implements Create Copy mode (duplicates allowed)
func (s *Store) importCreateCopy(ctx context.Context, data *ExportData, mode Mode) (*ImportResult, error) {
	result := &ImportResult{Warnings: []string{}}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Map old project slugs to new project IDs
	projectIDMap := make(map[string]int64)

	// Process each project
	for _, pExport := range data.Projects {
		// Validate required fields
		if pExport.Slug == "" {
			return nil, fmt.Errorf("%w: project missing slug", ErrValidation)
		}
		if pExport.Name == "" {
			return nil, fmt.Errorf("%w: project missing name", ErrValidation)
		}

		// Rewrite slug: append "-imported-2", "-imported-3", etc.
		baseSlug, err := generateSlugFromName(pExport.Name)
		if err != nil {
			baseSlug, err = randomSlug(8)
			if err != nil {
				return nil, fmt.Errorf("generate slug: %w", err)
			}
		}

		slug := baseSlug + "-imported"
		for i := 2; i < 102; i++ {
			if i > 2 {
				slug = fmt.Sprintf("%s-%d", baseSlug, i)
			}

			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE slug = ?)`, slug).Scan(&exists); err != nil {
				return nil, fmt.Errorf("check slug: %w", err)
			}
			if !exists {
				break
			}
			if i == 101 {
				return nil, fmt.Errorf("%w: could not generate unique slug", ErrConflict)
			}
		}

		if slug != pExport.Slug {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Slug %q rewritten to %q", pExport.Slug, slug))
		}

		var ownerUserID *int64
		if mode == ModeFull {
			enabled, err := authEnabledTx(ctx, tx)
			if err != nil {
				return nil, err
			}
			if enabled {
				userID, ok := UserIDFromContext(ctx)
				if !ok {
					return nil, ErrUnauthorized
				}
				ownerUserID = &userID
			}
		}

		nowMs := time.Now().UTC().UnixMilli()
		createdAtMs := pExport.CreatedAt.UnixMilli()
		updatedAtMs := pExport.UpdatedAt.UnixMilli()

		image := pExport.Image
		if image == nil {
			defaultImage := "/scrumboy.png"
			image = &defaultImage
		}
		dominantColor := pExport.DominantColor
		if dominantColor == "" {
			dominantColor = "#888888"
		}

		var expiresAtMs sql.NullInt64
		if pExport.ExpiresAt != nil {
			// For temporary boards, give a fresh expiration date instead of preserving the old one
			// This prevents importing boards that are already expired or about to expire
			expiresAtMs.Valid = true
			if mode == ModeAnonymous {
				// Anonymous boards: 14 days from now
				expiresAtMs.Int64 = time.Now().UTC().Add(14 * 24 * time.Hour).UnixMilli()
			} else {
				// Full mode temp boards: preserve original expiration (user's choice)
				expiresAtMs.Int64 = pExport.ExpiresAt.UnixMilli()
			}
		}

		// Set creator_user_id for temporary boards (importing user becomes creator)
		var creatorUserID *int64
		if pExport.ExpiresAt != nil {
			userID, ok := UserIDFromContext(ctx)
			if ok {
				creatorUserID = &userID
			}
		}

		// Create new project (regenerate ID). Import always uses EstimationModeModifiedFibonacci; pExport.EstimationMode from JSON is ignored.
		defaultSprintWeeks := 2
		if pExport.DefaultSprintWeeks == 1 || pExport.DefaultSprintWeeks == 2 {
			defaultSprintWeeks = pExport.DefaultSprintWeeks
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO projects(name, image, dominant_color, slug, estimation_mode, default_sprint_weeks, owner_user_id, creator_user_id, last_activity_at, expires_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			pExport.Name, image, dominantColor, slug, EstimationModeModifiedFibonacci, defaultSprintWeeks, ownerUserID, creatorUserID, nowMs, expiresAtMs, createdAtMs, updatedAtMs)
		if err != nil {
			return nil, fmt.Errorf("insert project: %w", err)
		}

		newProjectID, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("last insert id: %w", err)
		}

		// So ListProjects returns this project immediately (it filters by project_members).
		if ownerUserID != nil {
			_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO project_members (project_id, user_id, role, created_at) VALUES (?, ?, 'maintainer', ?)`, newProjectID, *ownerUserID, nowMs)
			if err != nil {
				return nil, fmt.Errorf("ensure maintainer membership for imported project: %w", err)
			}
		}

		// Workflow columns: custom from export or default
		if len(pExport.WorkflowColumns) >= 2 {
			if err := s.deleteProjectWorkflowColumnsExec(ctx, tx, newProjectID); err != nil {
				return nil, fmt.Errorf("clear workflow columns for project %q: %w", pExport.Name, err)
			}
			cols := workflowColumnsFromExport(pExport.WorkflowColumns)
			if err := s.insertWorkflowColumnsExec(ctx, tx, newProjectID, cols); err != nil {
				return nil, fmt.Errorf("import workflow columns for project %q: %w", pExport.Name, err)
			}
		} else {
			if err := s.ensureDefaultWorkflowColumnsExec(ctx, tx, tx, newProjectID); err != nil {
				return nil, fmt.Errorf("ensure workflow columns for project %q: %w", pExport.Name, err)
			}
		}

		projectIDMap[pExport.Slug] = newProjectID

		sprintIDByNumber, err := insertSprintsForImport(ctx, tx, newProjectID, pExport.Sprints)
		if err != nil {
			return nil, fmt.Errorf("import sprints for project %q: %w", pExport.Name, err)
		}

		// Import todos - preserve localId values from import
		for _, tExport := range pExport.Todos {
			// Validate required fields
			if tExport.LocalID == 0 {
				return nil, fmt.Errorf("%w: todo missing localId", ErrValidation)
			}
			if tExport.Title == "" {
				return nil, fmt.Errorf("%w: todo missing title", ErrValidation)
			}

			columnKey, warned := resolveImportColumnKey(ctx, tx, newProjectID, tExport.Status)
			if warned {
				result.Warnings = append(result.Warnings, fmt.Sprintf("Unknown status %q for todo %q, defaulting to backlog", tExport.Status, tExport.Title))
			}
			if err := validateEstimationPoints(tExport.EstimationPoints); err != nil {
				return nil, err
			}
			var estimationPoints any
			if tExport.EstimationPoints != nil {
				estimationPoints = *tExport.EstimationPoints
			}

			status := StatusBacklog
			if s, ok := ParseStatus(tExport.Status); ok {
				status = s
			}

			// Preserve localId, only regenerate if collision
			localID := tExport.LocalID

			// Check for collision
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM todos WHERE project_id = ? AND local_id = ?)`, newProjectID, localID).Scan(&exists); err != nil {
				return nil, fmt.Errorf("check local_id: %w", err)
			}

			if exists {
				// Collision - regenerate
				var maxLocalID int64
				if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(local_id), 0) FROM todos WHERE project_id = ?`, newProjectID).Scan(&maxLocalID); err != nil {
					return nil, fmt.Errorf("get max local_id: %w", err)
				}
				localID = maxLocalID + 1
				result.Warnings = append(result.Warnings, fmt.Sprintf("Todo localId %d collided, regenerated to %d", tExport.LocalID, localID))
			}

			createdAtMs := tExport.CreatedAt.UnixMilli()
			updatedAtMs := tExport.UpdatedAt.UnixMilli()
			assigneeVal := resolveImportAssignee(ctx, tx, newProjectID, tExport.AssigneeUserId)
			var assigneeForSQL any
			if assigneeVal != nil {
				assigneeForSQL = *assigneeVal
			}
			doneAtForInsert := resolveImportDoneAt(tExport.DoneAt, status, updatedAtMs)

			var sprintIDForSQL any
			if tExport.SprintNumber != nil && sprintIDByNumber != nil {
				if id, ok := sprintIDByNumber[*tExport.SprintNumber]; ok {
					sprintIDForSQL = id
				}
			}

			// Insert todo with remapped project_id, preserving localId (schema uses column_key, not status)
			_, err = tx.ExecContext(ctx, `
				INSERT INTO todos(project_id, local_id, title, body, column_key, rank, estimation_points, assignee_user_id, sprint_id, created_at, updated_at, done_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				newProjectID, localID, tExport.Title, tExport.Body, columnKey, tExport.Rank, estimationPoints, assigneeForSQL, sprintIDForSQL, createdAtMs, updatedAtMs, doneAtForInsert)
			if err != nil {
				return nil, fmt.Errorf("insert todo: %w", err)
			}

			// Get todo ID
			var todoID int64
			if err := tx.QueryRowContext(ctx, `SELECT id FROM todos WHERE project_id = ? AND local_id = ?`, newProjectID, localID).Scan(&todoID); err != nil {
				return nil, fmt.Errorf("get todo id: %w", err)
			}

			// Import tags
			if err := s.importTodoTags(ctx, tx, newProjectID, todoID, tExport.Tags, mode); err != nil {
				return nil, fmt.Errorf("import todo tags: %w", err)
			}
		}

		// Import tags with remapped project_id
		for _, tagExport := range pExport.Tags {
			if err := s.importTag(ctx, tx, newProjectID, tagExport.Name, tagExport.Color, mode); err != nil {
				return nil, fmt.Errorf("import tag: %w", err)
			}
		}

		if err := bulkInsertLinks(ctx, tx, newProjectID, pExport.Links); err != nil {
			return nil, fmt.Errorf("import links: %w", err)
		}

		// Copy mode: always write the wall block verbatim when present. The
		// target project was just created so there is no pre-existing wall
		// to merge with.
		if err := upsertWallForImportTx(ctx, tx, newProjectID, pExport.Wall); err != nil {
			return nil, fmt.Errorf("import wall for project %q: %w", pExport.Name, err)
		}

		result.Created++
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	result.Imported = len(data.Projects)
	return result, nil
}

// importTodoTags imports tags for a todo and associates them
// For anonymous boards: creates board-scoped tags (project_id, no user_id)
// For authenticated boards: creates user-owned tags (user_id, no project_id)
// mode parameter kept for backward compatibility but not used
func (s *Store) importTodoTags(ctx context.Context, tx *sql.Tx, projectID, todoID int64, tagNames []string, mode Mode) error {
	// Check if project is anonymous temporary board
	p, err := getProjectTx(ctx, tx, projectID, s)
	if err != nil {
		return err
	}
	isAnonymousBoard := p.ExpiresAt != nil && p.CreatorUserID == nil

	// Get userID from context (importing user)
	userID, ok := UserIDFromContext(ctx)
	var userIDPtr *int64
	if !ok {
		if isAnonymousBoard {
			// Anonymous board: allow board-scoped tags without userID
			userIDPtr = nil
		} else {
			// For authenticated boards, try to get first user or skip tags
			enabled, err := s.authEnabled(ctx)
			if err != nil {
				return err
			}
			if enabled {
				return fmt.Errorf("%w: userID required for tag import", ErrUnauthorized)
			}
			// Pre-bootstrap: use first user or skip
			if err := tx.QueryRowContext(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
				// No users - skip tags
				return nil
			}
			userIDPtr = &userID
		}
	} else {
		userIDPtr = &userID
	}

	// Normalize tags
	tags, err := normalizeTags(tagNames)
	if err != nil {
		return err
	}

	// Use setTodoTags to handle both user-owned and board-scoped tags
	return setTodoTags(ctx, tx, projectID, todoID, userIDPtr, isAnonymousBoard, tags)
}

// importTag imports or updates a tag
// For anonymous boards: creates board-scoped tags (project_id, no user_id) with color in tags table
// For authenticated boards: creates user-owned tags (user_id, no project_id) and sets color preference
// mode parameter kept for backward compatibility but not used
func (s *Store) importTag(ctx context.Context, tx *sql.Tx, projectID int64, tagName string, color *string, mode Mode) error {
	// Check if project is anonymous temporary board
	p, err := getProjectTx(ctx, tx, projectID, s)
	if err != nil {
		return err
	}
	isAnonymousBoard := p.ExpiresAt != nil && p.CreatorUserID == nil

	// Get userID from context (importing user)
	userID, ok := UserIDFromContext(ctx)
	var userIDPtr *int64
	if !ok {
		if isAnonymousBoard {
			// Anonymous board: allow board-scoped tags without userID
			userIDPtr = nil
		} else {
			// For authenticated boards, try to get first user or skip
			enabled, err := s.authEnabled(ctx)
			if err != nil {
				return err
			}
			if enabled {
				return fmt.Errorf("%w: userID required for tag import", ErrUnauthorized)
			}
			// Pre-bootstrap: use first user or skip
			if err := tx.QueryRowContext(ctx, `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
				// No users - skip tag
				return nil
			}
			userIDPtr = &userID
		}
	} else {
		userIDPtr = &userID
	}

	// Normalize tag name
	tags, err := normalizeTags([]string{tagName})
	if err != nil {
		return err
	}
	if len(tags) == 0 {
		return fmt.Errorf("%w: invalid tag name", ErrValidation)
	}
	tagName = tags[0]

	nowMs := time.Now().UTC().UnixMilli()
	var tagID int64

	if isAnonymousBoard {
		// Create or get board-scoped tag
		err := tx.QueryRowContext(ctx, `
SELECT id FROM tags WHERE project_id = ? AND name = ? AND user_id IS NULL`, projectID, tagName).Scan(&tagID)
		if err == sql.ErrNoRows {
			// Create board-scoped tag with color
			res, err := tx.ExecContext(ctx, `
INSERT INTO tags(user_id, project_id, name, created_at, color)
VALUES (NULL, ?, ?, ?, ?)`, projectID, tagName, nowMs, color)
			if err != nil {
				return fmt.Errorf("create board tag: %w", err)
			}
			tagID, err = res.LastInsertId()
			if err != nil {
				return fmt.Errorf("last insert id tag: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("get board tag: %w", err)
		} else if color != nil && *color != "" {
			// Update color if tag exists
			_, err = tx.ExecContext(ctx, `UPDATE tags SET color = ? WHERE id = ?`, *color, tagID)
			if err != nil {
				return fmt.Errorf("update board tag color: %w", err)
			}
		}
	} else {
		// Create or get user-owned tag
		if userIDPtr == nil {
			return fmt.Errorf("userID required for user-owned tags")
		}
		tagID, err = GetOrCreateTag(ctx, tx, *userIDPtr, tagName)
		if err != nil {
			return fmt.Errorf("get or create tag: %w", err)
		}

		// Set color preference if provided
		if color != nil && *color != "" {
			_, err = tx.ExecContext(ctx, `
INSERT INTO user_tag_colors(user_id, tag_id, color)
VALUES (?, ?, ?)
ON CONFLICT(user_id, tag_id) DO UPDATE SET color = excluded.color`, *userIDPtr, tagID, *color)
			if err != nil {
				return fmt.Errorf("set tag color preference: %w", err)
			}
		}
	}

	// Link tag to project via project_tags if not already linked
	_, err = tx.ExecContext(ctx, `
INSERT OR IGNORE INTO project_tags(project_id, tag_id, created_at)
VALUES (?, ?, ?)`, projectID, tagID, nowMs)
	if err != nil {
		return fmt.Errorf("link tag to project: %w", err)
	}

	return nil
}

// PreviewImport returns preview counts without executing (dry-run)
func (s *Store) PreviewImport(ctx context.Context, data *ExportData, mode Mode, importMode string) (*PreviewResult, error) {
	// CRITICAL: Must use exact same resolution logic as import handlers
	// We'll use an in-memory resolver approach for safety

	log.Printf("PreviewImport: mode=%s, importMode=%s, projects=%d", mode, importMode, len(data.Projects))
	result := &PreviewResult{Warnings: []string{}}

	// Validate JSON structure and version
	if data.Version != version.ExportFormatVersion {
		log.Printf("PreviewImport: Version mismatch: got %s, expected %s", data.Version, version.ExportFormatVersion)
		return nil, fmt.Errorf("%w: unsupported export version %q (expected %s)", ErrValidation, data.Version, version.ExportFormatVersion)
	}

	// Validate scope compatibility
	if data.Scope == "full" && mode == ModeAnonymous {
		log.Printf("PreviewImport: Rejecting full scope into anonymous mode")
		return nil, fmt.Errorf("%w: cannot import full scope into anonymous mode", ErrValidation)
	}

	// Reject Replace All in anonymous mode
	if importMode == "replace" && mode == ModeAnonymous {
		log.Printf("PreviewImport: Rejecting replace mode in anonymous")
		return nil, fmt.Errorf("%w: Replace All is forbidden in anonymous mode", ErrValidation)
	}

	// Count projects in import
	result.Projects = len(data.Projects)
	log.Printf("PreviewImport: Counting %d projects", result.Projects)

	// Count todos and collect unknown status warnings (surfaced for preview)
	unknownStatusSeen := make(map[string]struct{})
	for _, p := range data.Projects {
		result.Todos += len(p.Todos)
		var validColumnKeys map[string]struct{}
		if len(p.WorkflowColumns) >= 2 {
			validColumnKeys = validColumnKeysFromWorkflowExport(p.WorkflowColumns)
		}
		for _, t := range p.Todos {
			warn := false
			if validColumnKeys != nil {
				warn = !statusResolvesInWorkflow(t.Status, validColumnKeys)
			} else {
				_, ok := ParseStatus(t.Status)
				warn = !ok && t.Status != ""
			}
			if warn && t.Status != "" {
				if _, seen := unknownStatusSeen[t.Status]; !seen {
					unknownStatusSeen[t.Status] = struct{}{}
					result.Warnings = append(result.Warnings, fmt.Sprintf("Unknown workflow column %q, defaulted to backlog", t.Status))
				}
			}
		}
	}
	log.Printf("PreviewImport: Counted %d todos", result.Todos)

	// Count unique tags
	tagSet := make(map[string]struct{})
	for _, p := range data.Projects {
		for _, tag := range p.Tags {
			tagSet[tag.Name] = struct{}{}
		}
	}
	result.Tags = len(tagSet)
	log.Printf("PreviewImport: Counted %d unique tags", result.Tags)

	// Count links
	for _, p := range data.Projects {
		result.Links += len(p.Links)
	}

	// For replace mode, count what will be deleted
	if importMode == "replace" {
		log.Printf("PreviewImport: Processing replace mode")
		// Anonymous mode doesn't support replace
		if mode != ModeAnonymous {
			whereClause, args, err := s.getExportableProjectsSelector(ctx, mode)
			if err != nil {
				log.Printf("PreviewImport: Error getting selector for replace: %v", err)
				return nil, err
			}

			var count int
			if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM projects WHERE %s`, whereClause), args...).Scan(&count); err != nil {
				log.Printf("PreviewImport: Error counting projects to delete: %v", err)
				return nil, fmt.Errorf("count projects to delete: %w", err)
			}
			result.WillDelete = count
			log.Printf("PreviewImport: Will delete %d projects", count)
		}
	}

	// For merge mode, count what will be updated/created
	if importMode == "merge" {
		log.Printf("PreviewImport: Processing merge mode")
		// In anonymous mode, all projects will be created (no matching slugs in temp boards)
		if mode == ModeAnonymous {
			result.WillCreate = len(data.Projects)
			log.Printf("PreviewImport: Anonymous merge - will create %d projects", result.WillCreate)
		} else {
			// Authorization: fail preview if any target project would be unauthorized (same as import)
			userID, hasUser := UserIDFromContext(ctx)
			for _, p := range data.Projects {
				if p.Slug == "" {
					continue
				}
				var projectID int64
				var expiresAt sql.NullInt64
				if err := s.db.QueryRowContext(ctx, `SELECT id, expires_at FROM projects WHERE slug = ? AND import_batch_id IS NULL`, p.Slug).Scan(&projectID, &expiresAt); err == sql.ErrNoRows {
					continue
				} else if err != nil {
					return nil, fmt.Errorf("check project: %w", err)
				}
				if !expiresAt.Valid {
					if !hasUser {
						return nil, fmt.Errorf("%w: cannot merge into project %q: authentication required", ErrUnauthorized, p.Slug)
					}
					ok, err := s.userHasProjectRole(ctx, projectID, userID, RoleMaintainer)
					if err != nil {
						return nil, fmt.Errorf("check project role: %w", err)
					}
					if !ok {
						return nil, fmt.Errorf("%w: cannot merge into project %q: maintainer role required", ErrUnauthorized, p.Slug)
					}
				}
			}

			whereClause, args, err := s.getExportableProjectsSelector(ctx, mode)
			if err != nil {
				log.Printf("PreviewImport: Error getting selector for merge: %v", err)
				return nil, err
			}

			// Count existing projects that match slugs
			slugMap := make(map[string]bool)
			for _, p := range data.Projects {
				var exists bool
				if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`
				SELECT EXISTS(SELECT 1 FROM projects WHERE slug = ? AND (%s))`, whereClause),
					append([]any{p.Slug}, args...)...).Scan(&exists); err != nil {
					return nil, fmt.Errorf("check project: %w", err)
				}
				if exists {
					result.WillUpdate++
					slugMap[p.Slug] = true
				} else {
					result.WillCreate++
				}
			}

			// Count todos that will be updated vs created
			for _, p := range data.Projects {
				// Get project ID if it exists
				var projectID sql.NullInt64
				if slugMap[p.Slug] {
					if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`
					SELECT id FROM projects WHERE slug = ? AND (%s)`, whereClause),
						append([]any{p.Slug}, args...)...).Scan(&projectID); err != nil {
						return nil, fmt.Errorf("get project id: %w", err)
					}
				}

				for _, t := range p.Todos {
					if projectID.Valid {
						var exists bool
						if err := s.db.QueryRowContext(ctx, `
						SELECT EXISTS(SELECT 1 FROM todos WHERE project_id = ? AND local_id = ?)`,
							projectID.Int64, t.LocalID).Scan(&exists); err != nil {
							return nil, fmt.Errorf("check todo: %w", err)
						}
						if exists {
							result.WillUpdate++
						} else {
							result.WillCreate++
						}
					} else {
						result.WillCreate++
					}
				}
			}
		}
	}

	// For copy mode, everything is created
	if importMode == "copy" {
		log.Printf("PreviewImport: Processing copy mode")
		result.WillCreate = result.Projects
		for _, p := range data.Projects {
			result.WillCreate += len(p.Todos)
		}
		log.Printf("PreviewImport: Copy mode - will create %d items", result.WillCreate)
	}

	log.Printf("PreviewImport: Returning preview - Projects:%d Todos:%d Tags:%d WillCreate:%d WillUpdate:%d WillDelete:%d",
		result.Projects, result.Todos, result.Tags, result.WillCreate, result.WillUpdate, result.WillDelete)
	return result, nil
}
