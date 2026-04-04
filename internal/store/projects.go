package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ensureProjectHasMaintainer ensures a project has at least one maintainer.
// If project has no members and has a creator, inserts creator as maintainer.
// This is a one-time backfill for existing projects.
func (s *Store) ensureProjectHasMaintainer(ctx context.Context, projectID int64) error {
	// Check if project has any members
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM project_members WHERE project_id = ?
	`, projectID).Scan(&count)
	if err != nil {
		return fmt.Errorf("count members: %w", err)
	}

	if count > 0 {
		return nil // Already has members
	}

	// Get project to check creator
	p, err := s.getProject(ctx, projectID)
	if err != nil {
		return err
	}

	// If project has creator, insert as maintainer
	if p.CreatorUserID != nil {
		return s.EnsureMaintainerMembership(ctx, projectID, *p.CreatorUserID)
	}

	return nil // No creator, no backfill needed
}

// userHasProjectRole checks project-level permissions via project_members.
// This is independent of system roles (Owner/Admin/User).
// System roles govern system-level actions, not project access.
func (s *Store) userHasProjectRole(ctx context.Context, projectID int64, userID int64, requiredRole ProjectRole) (bool, error) {
	// Temporary boards bypass role checks (same as ownership checks)
	p, err := s.getProject(ctx, projectID)
	if err != nil {
		return false, err
	}
	if p.ExpiresAt != nil {
		return true, nil // Temporary boards are accessible
	}

	// Pre-bootstrap: no auth required
	enabled, err := s.authEnabled(ctx)
	if err != nil {
		return false, err
	}
	if !enabled {
		return true, nil
	}

	// Get user's role
	role, err := s.GetProjectRole(ctx, projectID, userID)
	if err != nil {
		return false, err
	}
	if role == "" {
		return false, nil // Not a member
	}

	// Check if role meets minimum requirement
	return role.HasMinimumRole(requiredRole), nil
}

// CheckProjectRole verifies that userID has at least the required role for the project.
// Returns ErrNotFound when the user lacks access to the project.
// Other errors may be returned for database failures.
// Store functions must never call UserIDFromContext or HTTP helpers.
func (s *Store) CheckProjectRole(ctx context.Context, projectID int64, userID int64, requiredRole ProjectRole) error {
	hasAccess, err := s.userHasProjectRole(ctx, projectID, userID, requiredRole)
	if err != nil {
		return err
	}
	if !hasAccess {
		return ErrNotFound
	}
	return nil
}

func (s *Store) getProjectForRead(ctx context.Context, projectID int64, mode Mode) (Project, error) {
	p, err := s.getProject(ctx, projectID)
	if err != nil {
		return Project{}, err
	}

	// Temporary boards are share-links (pastebin-style): bypass ownership checks because the project is temporary/unowned,
	// not because of request mode. Request mode remains orthogonal and request-scoped.
	if p.ExpiresAt != nil {
		return p, nil
	}

	// Backfill: ensure project has maintainer if it has a creator but no members
	if err := s.ensureProjectHasMaintainer(ctx, projectID); err != nil {
		return Project{}, err
	}

	enabled, err := s.authEnabled(ctx)
	if err != nil {
		return Project{}, err
	}
	if !enabled {
		// Pre-bootstrap: preserve existing behavior.
		return p, nil
	}

	userID, ok := UserIDFromContext(ctx)
	if !ok {
		return Project{}, ErrUnauthorized
	}

	hasAccess, err := s.userHasProjectRole(ctx, projectID, userID, RoleViewer)
	if err != nil {
		return Project{}, err
	}
	if !hasAccess {
		// Hide existence across users.
		return Project{}, ErrNotFound
	}
	return p, nil
}

// GetProjectContextBySlug resolves slug to project and runs auth checks once.
// Returns ProjectContext for use by GetBoard, GetBoardPaged, listTagCounts.
func (s *Store) GetProjectContextBySlug(ctx context.Context, slug string, mode Mode) (ProjectContext, error) {
	p, err := s.GetProjectBySlug(ctx, slug)
	if err != nil {
		return ProjectContext{}, err
	}
	return s.buildProjectContext(ctx, p, mode)
}

// GetProjectContextForRead resolves projectID to project and runs auth checks once.
// Returns ProjectContext for use by GetBoard, GetBoardPaged, listTagCounts.
func (s *Store) GetProjectContextForRead(ctx context.Context, projectID int64, mode Mode) (ProjectContext, error) {
	p, err := s.getProject(ctx, projectID)
	if err != nil {
		return ProjectContext{}, err
	}
	return s.buildProjectContext(ctx, p, mode)
}

// buildProjectContext runs auth checks and builds ProjectContext.
func (s *Store) buildProjectContext(ctx context.Context, p Project, mode Mode) (ProjectContext, error) {
	pc := ProjectContext{Project: p}

	// Temporary boards bypass ownership checks
	if p.ExpiresAt != nil {
		pc.AuthEnabled = true // arbitrary; role unused for temp boards
		return pc, nil
	}

	if err := s.ensureProjectHasMaintainer(ctx, p.ID); err != nil {
		return ProjectContext{}, err
	}

	enabled, err := s.authEnabled(ctx)
	if err != nil {
		return ProjectContext{}, err
	}
	pc.AuthEnabled = enabled

	if !enabled {
		return pc, nil
	}

	userID, ok := UserIDFromContext(ctx)
	if !ok {
		return ProjectContext{}, ErrUnauthorized
	}

	role, err := s.GetProjectRole(ctx, p.ID, userID)
	if err != nil {
		return ProjectContext{}, err
	}
	pc.Role = role

	if !role.HasMinimumRole(RoleViewer) {
		return ProjectContext{}, ErrNotFound
	}
	return pc, nil
}

func (s *Store) getProjectForWrite(ctx context.Context, projectID int64, mode Mode) (Project, error) {
	// Write requires contributor role or higher
	p, err := s.getProject(ctx, projectID)
	if err != nil {
		return Project{}, err
	}

	// Temporary boards bypass role checks
	if p.ExpiresAt != nil {
		return p, nil
	}

	enabled, err := s.authEnabled(ctx)
	if err != nil {
		return Project{}, err
	}
	if !enabled {
		return p, nil
	}

	userID, ok := UserIDFromContext(ctx)
	if !ok {
		return Project{}, ErrUnauthorized
	}

	hasAccess, err := s.userHasProjectRole(ctx, projectID, userID, RoleContributor)
	if err != nil {
		return Project{}, err
	}
	if !hasAccess {
		return Project{}, ErrNotFound
	}
	return p, nil
}

// isAnonymousTemporaryBoard checks if a project is an anonymous temporary board.
// Anonymous temp boards are identified by: expires_at IS NOT NULL AND creator_user_id IS NULL.
// These boards are immutable at the project level - only todo-level operations are allowed.
func isAnonymousTemporaryBoard(p Project) bool {
	return p.ExpiresAt != nil && p.CreatorUserID == nil
}

// effectiveTagModeForProject determines tag scoping based on project state.
// Request mode stays request-scoped and is not rewritten. Tag scoping is orthogonal: unowned temporary boards
// should have project-scoped tags to avoid cross-board leakage.
func effectiveTagModeForProject(p Project, requestMode Mode) Mode {
	if p.ExpiresAt != nil && p.CreatorUserID == nil {
		return ModeAnonymous
	}
	return requestMode
}

func (s *Store) ListProjects(ctx context.Context) ([]ProjectListEntry, error) {
	enabled, err := s.authEnabled(ctx)
	if err != nil {
		return nil, err
	}

	var (
		rows *sql.Rows
	)
	if enabled {
		userID, ok := UserIDFromContext(ctx)
		if !ok {
			return nil, ErrUnauthorized
		}
		// Full mode: show creator's temp boards + authenticated temp boards shared via project_members + durable projects (via project_members).
		// Role: temp creator => maintainer; otherwise use project_members (covers invited maintainers/contributors/viewers).
		// IMPORTANT: Anonymous temp boards (creator_user_id IS NULL) stay out of listings — including the membership branch below
		// so orphan project_members rows cannot surface unowned paste boards in a user's project list.
		rows, err = s.db.QueryContext(ctx, `
SELECT p.id, p.name, p.image, p.slug, p.dominant_color, p.estimation_mode, p.default_sprint_weeks, p.owner_user_id, p.creator_user_id, p.last_activity_at, p.expires_at, p.created_at, p.updated_at,
  CASE
    WHEN p.expires_at IS NOT NULL AND p.creator_user_id = ? THEN 'maintainer'
    ELSE (SELECT pm.role FROM project_members pm WHERE pm.project_id = p.id AND pm.user_id = ? LIMIT 1)
  END AS role
FROM projects p
WHERE (
  (p.expires_at IS NOT NULL AND p.creator_user_id = ?) OR
  (p.expires_at IS NULL AND EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id = p.id AND pm.user_id = ?)) OR
  (p.expires_at IS NOT NULL AND p.creator_user_id IS NOT NULL AND EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id = p.id AND pm.user_id = ?))
) AND p.import_batch_id IS NULL
ORDER BY p.updated_at DESC, p.id DESC`, userID, userID, userID, userID, userID)
	} else {
		// Anonymous mode: no authenticated project listings - return empty result explicitly
		rows, err = s.db.QueryContext(ctx, `
SELECT id, name, image, slug, dominant_color, estimation_mode, default_sprint_weeks, owner_user_id, creator_user_id, last_activity_at, expires_at, created_at, updated_at, '' AS role
FROM projects
WHERE 1=0`)
	}
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var out []ProjectListEntry
	for rows.Next() {
		var e ProjectListEntry
		var createdAtMs, updatedAtMs, lastActivityAtMs int64
		var expiresAtMs sql.NullInt64
		var ownerUserID sql.NullInt64
		var creatorUserID sql.NullInt64
		var image sql.NullString
		var role string
		if err := rows.Scan(&e.Project.ID, &e.Project.Name, &image, &e.Project.Slug, &e.Project.DominantColor, &e.Project.EstimationMode, &e.Project.DefaultSprintWeeks, &ownerUserID, &creatorUserID, &lastActivityAtMs, &expiresAtMs, &createdAtMs, &updatedAtMs, &role); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		if image.Valid && image.String != "" {
			e.Project.Image = &image.String
		}
		if ownerUserID.Valid {
			v := ownerUserID.Int64
			e.Project.OwnerUserID = &v
		}
		if creatorUserID.Valid {
			v := creatorUserID.Int64
			e.Project.CreatorUserID = &v
		}
		e.Project.LastActivityAt = time.UnixMilli(lastActivityAtMs).UTC()
		if expiresAtMs.Valid {
			expiresAt := time.UnixMilli(expiresAtMs.Int64).UTC()
			e.Project.ExpiresAt = &expiresAt
		}
		e.Project.CreatedAt = time.UnixMilli(createdAtMs).UTC()
		e.Project.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
		e.Role = ProjectRole(role)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows projects: %w", err)
	}
	return out, nil
}

// slugExists checks if a slug already exists in the database.
func (s *Store) slugExists(ctx context.Context, slug string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE slug = ? AND import_batch_id IS NULL)`, slug).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check slug exists: %w", err)
	}
	return exists, nil
}

// RewriteDurableProjectSlugs rewrites slugs for all durable (non-expiring) projects to be
// human-readable slugs derived from the project name.
//
// NOTE: This changes existing slugs and will break old /{slug} links for durable projects.
// It is intended as a one-time backfill for legacy installations where slugs were randomized.
func (s *Store) RewriteDurableProjectSlugs(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin rewrite slugs tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	type row struct {
		id   int64
		name string
	}
	var rowsToUpdate []row

	rows, err := tx.QueryContext(ctx, `SELECT id, name FROM projects WHERE expires_at IS NULL AND import_batch_id IS NULL ORDER BY id ASC`)
	if err != nil {
		return 0, fmt.Errorf("list durable projects: %w", err)
	}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.name); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan durable project: %w", err)
		}
		rowsToUpdate = append(rowsToUpdate, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("rows durable projects: %w", err)
	}
	_ = rows.Close()

	existsExcludingID := func(slug string, id int64) (bool, error) {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE slug = ? AND id <> ? AND import_batch_id IS NULL)`, slug, id).Scan(&exists); err != nil {
			return false, fmt.Errorf("check slug exists: %w", err)
		}
		return exists, nil
	}

	updated := 0
	for _, r := range rowsToUpdate {
		baseSlug, err := generateSlugFromName(r.name)
		if err != nil {
			return 0, fmt.Errorf("%w: cannot generate slug for project id=%d name=%q: %v", ErrValidation, r.id, r.name, err)
		}

		var chosen string
		for i := 0; i < 100; i++ {
			candidate := baseSlug
			if i > 0 {
				suffix := fmt.Sprintf("-%d", i+1)
				maxBaseLen := maxSlugLen - len(suffix)
				base := baseSlug
				if len(base) > maxBaseLen {
					base = strings.TrimRight(base[:maxBaseLen], "-")
				}
				if base == "" {
					continue
				}
				candidate = base + suffix
			}

			exists, err := existsExcludingID(candidate, r.id)
			if err != nil {
				return 0, err
			}
			if exists {
				continue
			}
			chosen = candidate
			break
		}
		if chosen == "" {
			return 0, fmt.Errorf("%w: could not generate unique slug for project id=%d", ErrConflict, r.id)
		}

		if _, err := tx.ExecContext(ctx, `UPDATE projects SET slug = ? WHERE id = ?`, chosen, r.id); err != nil {
			return 0, fmt.Errorf("update project slug id=%d: %w", r.id, err)
		}
		updated++
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit rewrite slugs tx: %w", err)
	}
	return updated, nil
}

func (s *Store) CreateProject(ctx context.Context, name string) (Project, error) {
	return s.CreateProjectWithWorkflow(ctx, name, nil)
}

func (s *Store) CreateProjectWithWorkflow(ctx context.Context, name string, workflow []WorkflowColumn) (Project, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return Project{}, fmt.Errorf("%w: invalid project name", ErrValidation)
	}

	enabled, err := s.authEnabled(ctx)
	if err != nil {
		return Project{}, err
	}
	var ownerUserID *int64
	if enabled {
		uid, ok := UserIDFromContext(ctx)
		if !ok {
			return Project{}, ErrUnauthorized
		}
		ownerUserID = &uid
	}

	// Generate base slug from name
	baseSlug, err := generateSlugFromName(name)
	if err != nil {
		return Project{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}

	nowMs := time.Now().UTC().UnixMilli()
	defaultImage := "/scrumboy.png"
	var (
		id   int64
		slug string
	)

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return Project{}, fmt.Errorf("begin create project tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Try base slug, then baseSlug-2, baseSlug-3, etc. until we find a unique one
	inserted := false
	for i := 0; i < 100; i++ {
		if i == 0 {
			slug = baseSlug
		} else {
			// Append -2, -3, etc. (max length 32, so truncate base if needed)
			suffix := fmt.Sprintf("-%d", i+1)
			maxBaseLen := 32 - len(suffix)
			if len(baseSlug) > maxBaseLen {
				base := strings.TrimRight(baseSlug[:maxBaseLen], "-")
				slug = base + suffix
			} else {
				slug = baseSlug + suffix
			}
		}

		// Check if slug exists before attempting insert
		exists, err := slugExistsTx(ctx, tx, slug)
		if err != nil {
			return Project{}, fmt.Errorf("check slug: %w", err)
		}
		if exists {
			continue
		}

		// Durable project: expires_at = NULL, last_activity_at = now
		res, err := tx.ExecContext(ctx, `INSERT INTO projects(name, image, slug, estimation_mode, owner_user_id, last_activity_at, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?)`, name, defaultImage, slug, EstimationModeModifiedFibonacci, ownerUserID, nowMs, nowMs, nowMs)
		if err != nil {
			// If we collide on the slug unique index (race condition), retry.
			if strings.Contains(err.Error(), "UNIQUE constraint failed: projects.slug") {
				continue
			}
			return Project{}, fmt.Errorf("insert project: %w", err)
		}
		lastID, err := res.LastInsertId()
		if err != nil {
			return Project{}, fmt.Errorf("last insert id project: %w", err)
		}
		id = lastID
		inserted = true
		break
	}
	if !inserted {
		return Project{}, fmt.Errorf("%w: could not generate unique slug after 100 attempts", ErrConflict)
	}

	// Create maintainer membership if project has an owner.
	if ownerUserID != nil {
		if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO project_members (project_id, user_id, role, created_at)
		VALUES (?, ?, ?, ?)`, id, *ownerUserID, RoleMaintainer, nowMs); err != nil {
			return Project{}, fmt.Errorf("ensure maintainer membership: %w", err)
		}
	}

	if workflow == nil {
		if err := s.ensureDefaultWorkflowColumnsTx(ctx, tx, id); err != nil {
			return Project{}, err
		}
	} else {
		if err := s.insertWorkflowColumnsTx(ctx, tx, id, workflow); err != nil {
			return Project{}, err
		}
	}

	actorUserID := ownerUserID
	meta := map[string]any{"name": name, "is_anonymous": false}
	if err := insertAuditEventTx(ctx, tx, id, actorUserID, "project_created", "project", &id, meta); err != nil {
		return Project{}, fmt.Errorf("audit project_created: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Project{}, fmt.Errorf("commit create project tx: %w", err)
	}

	return Project{
		ID:                 id,
		Name:               name,
		Image:              &defaultImage,
		DominantColor:      "#888888",
		EstimationMode:     EstimationModeModifiedFibonacci,
		DefaultSprintWeeks: 2,
		Slug:               slug,
		OwnerUserID:        ownerUserID,
		LastActivityAt:     time.UnixMilli(nowMs).UTC(),
		ExpiresAt:          nil, // Full mode projects never expire
		CreatedAt:          time.UnixMilli(nowMs).UTC(),
		UpdatedAt:          time.UnixMilli(nowMs).UTC(),
	}, nil
}

func slugExistsTx(ctx context.Context, tx *sql.Tx, slug string) (bool, error) {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE slug = ? AND import_batch_id IS NULL)`, slug).Scan(&exists); err != nil {
		return false, fmt.Errorf("check slug exists: %w", err)
	}
	return exists, nil
}

func (s *Store) DeleteProject(ctx context.Context, projectID int64, userID int64) error {
	// Load project to check if it's an anonymous temp board
	p, err := s.getProject(ctx, projectID)
	if err != nil {
		return err
	}

	// CRITICAL: Anonymous temporary boards are immutable at the project level.
	// They can only be deleted by expiration, not by user action.
	// Return ErrNotFound to avoid leaking existence of anonymous temp boards.
	if isAnonymousTemporaryBoard(p) {
		return ErrNotFound
	}

	if err := s.CheckProjectRole(ctx, projectID, userID, RoleMaintainer); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin delete project: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var name string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM projects WHERE id = ?`, projectID).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("get project name: %w", err)
	}
	actorUserID := &userID
	meta := map[string]any{"name": name}
	if err := insertAuditEventTx(ctx, tx, projectID, actorUserID, "project_deleted", "project", &projectID, meta); err != nil {
		return fmt.Errorf("audit project_deleted: %w", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, projectID)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected delete project: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete project: %w", err)
	}
	return nil
}

// userCanCreateTagsInProject checks if user has access to create tags in a project.
// Requires Contributor+ (owner, maintainer, or contributor). Viewers cannot create tags.
func (s *Store) userCanCreateTagsInProject(ctx context.Context, projectID int64, userID int64) (bool, error) {
	// Temporary boards bypass checks
	p, err := s.getProject(ctx, projectID)
	if err != nil {
		return false, err
	}
	if p.ExpiresAt != nil {
		return true, nil // Temporary boards are accessible
	}

	// Pre-bootstrap: no auth required
	enabled, err := s.authEnabled(ctx)
	if err != nil {
		return false, err
	}
	if !enabled {
		return true, nil
	}

	// Check if user is owner via owner_user_id
	if p.OwnerUserID != nil && *p.OwnerUserID == userID {
		return true, nil
	}

	// Check if user is owner or member via project_members
	role, err := s.GetProjectRole(ctx, projectID, userID)
	if err != nil {
		return false, err
	}
	if role != "" && role.HasMinimumRole(RoleContributor) {
		return true, nil // Contributor+ can create tags
	}

	return false, nil
}

// ensureProjectHasSlug generates and persists a slug for a project that lacks one.
// This is a safety check for projects created before slug generation was mandatory.
func (s *Store) ensureProjectHasSlug(ctx context.Context, projectID int64, name string) error {
	// Try to generate slug from name first
	slug, err := generateSlugFromName(name)
	if err != nil {
		// If name cannot produce valid slug, fallback to random
		slug, err = randomSlug(8)
		if err != nil {
			return fmt.Errorf("generate fallback slug: %w", err)
		}
	}

	// Check if slug exists, append suffix if needed
	baseSlug := slug
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
		exists, err := s.slugExists(ctx, slug)
		if err != nil {
			return fmt.Errorf("check slug exists: %w", err)
		}
		if !exists {
			break
		}
		if i == 99 {
			return fmt.Errorf("could not generate unique slug after 100 attempts")
		}
	}

	// Update project with generated slug
	_, err = s.db.ExecContext(ctx, `UPDATE projects SET slug = ? WHERE id = ?`, slug, projectID)
	if err != nil {
		return fmt.Errorf("update project slug: %w", err)
	}
	return nil
}

func (s *Store) getProject(ctx context.Context, projectID int64) (Project, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, image, slug, dominant_color, estimation_mode, default_sprint_weeks, owner_user_id, creator_user_id, last_activity_at, expires_at, created_at, updated_at FROM projects WHERE id=? AND import_batch_id IS NULL`, projectID)
	p, err := scanProject(row)
	if err != nil {
		return p, err
	}
	// Safety check: if slug is missing, generate and persist it
	if p.Slug == "" {
		if err := s.ensureProjectHasSlug(ctx, projectID, p.Name); err != nil {
			return Project{}, fmt.Errorf("ensure slug: %w", err)
		}
		// Re-fetch project to get the newly generated slug
		row = s.db.QueryRowContext(ctx, `SELECT id, name, image, slug, dominant_color, estimation_mode, default_sprint_weeks, owner_user_id, creator_user_id, last_activity_at, expires_at, created_at, updated_at FROM projects WHERE id=? AND import_batch_id IS NULL`, projectID)
		return scanProject(row)
	}
	return p, nil
}

func (s *Store) GetProject(ctx context.Context, projectID int64) (Project, error) {
	return s.getProject(ctx, projectID)
}

func (s *Store) GetProjectBySlug(ctx context.Context, slug string) (Project, error) {
	slug = strings.TrimSpace(strings.ToLower(slug))
	if !isValidSlug(slug) {
		return Project{}, fmt.Errorf("%w: invalid slug", ErrValidation)
	}
	// Keep the scan shape consistent with scanProject().
	row := s.db.QueryRowContext(ctx, `SELECT id, name, image, slug, dominant_color, estimation_mode, default_sprint_weeks, owner_user_id, creator_user_id, last_activity_at, expires_at, created_at, updated_at FROM projects WHERE slug=? AND import_batch_id IS NULL`, slug)
	return scanProject(row)
}

func getProjectTx(ctx context.Context, tx *sql.Tx, projectID int64, store *Store) (Project, error) {
	row := tx.QueryRowContext(ctx, `SELECT id, name, image, slug, dominant_color, estimation_mode, default_sprint_weeks, owner_user_id, creator_user_id, last_activity_at, expires_at, created_at, updated_at FROM projects WHERE id=? AND import_batch_id IS NULL`, projectID)
	p, err := scanProject(row)
	if err != nil {
		return p, err
	}
	// Safety check: if slug is missing, generate and persist it
	// Note: We need to use the store's db connection, not the transaction, for the update
	// to avoid transaction conflicts. This is acceptable since slug backfill is idempotent.
	if p.Slug == "" {
		if err := store.ensureProjectHasSlug(ctx, projectID, p.Name); err != nil {
			return Project{}, fmt.Errorf("ensure slug: %w", err)
		}
		// Re-fetch project to get the newly generated slug (using transaction)
		row = tx.QueryRowContext(ctx, `SELECT id, name, image, slug, dominant_color, estimation_mode, default_sprint_weeks, owner_user_id, creator_user_id, last_activity_at, expires_at, created_at, updated_at FROM projects WHERE id=? AND import_batch_id IS NULL`, projectID)
		return scanProject(row)
	}
	return p, nil
}

// userHasProjectRoleTx checks if user has at least the required role for a project (transaction version)
func (s *Store) userHasProjectRoleTx(ctx context.Context, tx *sql.Tx, projectID int64, userID int64, requiredRole ProjectRole) (bool, error) {
	// Temporary boards bypass role checks (same as ownership checks)
	p, err := getProjectTx(ctx, tx, projectID, s)
	if err != nil {
		return false, err
	}
	if p.ExpiresAt != nil {
		return true, nil // Temporary boards are accessible
	}

	// Pre-bootstrap: no auth required
	enabled, err := authEnabledTx(ctx, tx)
	if err != nil {
		return false, err
	}
	if !enabled {
		return true, nil
	}

	// Get user's role from project_members table
	var role string
	err = tx.QueryRowContext(ctx, `
		SELECT role FROM project_members
		WHERE project_id = ? AND user_id = ?
	`, projectID, userID).Scan(&role)

	if err == sql.ErrNoRows {
		return false, nil // Not a member
	}
	if err != nil {
		return false, fmt.Errorf("get project role: %w", err)
	}

	// Check if role meets minimum requirement
	return ProjectRole(role).HasMinimumRole(requiredRole), nil
}

func (s *Store) getProjectForReadTx(ctx context.Context, tx *sql.Tx, projectID int64, mode Mode) (Project, error) {
	p, err := getProjectTx(ctx, tx, projectID, s)
	if err != nil {
		return Project{}, err
	}
	// Temporary boards bypass ownership because of project state, not because of request mode.
	if p.ExpiresAt != nil {
		return p, nil
	}
	enabled, err := authEnabledTx(ctx, tx)
	if err != nil {
		return Project{}, err
	}
	if !enabled {
		return p, nil
	}
	userID, ok := UserIDFromContext(ctx)
	if !ok {
		return Project{}, ErrUnauthorized
	}

	hasAccess, err := s.userHasProjectRoleTx(ctx, tx, projectID, userID, RoleViewer)
	if err != nil {
		return Project{}, err
	}
	if !hasAccess {
		return Project{}, ErrNotFound
	}
	return p, nil
}

func (s *Store) getProjectForWriteTx(ctx context.Context, tx *sql.Tx, projectID int64, mode Mode) (Project, error) {
	// Write requires contributor role or higher
	p, err := getProjectTx(ctx, tx, projectID, s)
	if err != nil {
		return Project{}, err
	}

	// Temporary boards bypass role checks
	if p.ExpiresAt != nil {
		return p, nil
	}

	enabled, err := authEnabledTx(ctx, tx)
	if err != nil {
		return Project{}, err
	}
	if !enabled {
		return p, nil
	}

	userID, ok := UserIDFromContext(ctx)
	if !ok {
		return Project{}, ErrUnauthorized
	}

	hasAccess, err := s.userHasProjectRoleTx(ctx, tx, projectID, userID, RoleContributor)
	if err != nil {
		return Project{}, err
	}
	if !hasAccess {
		return Project{}, ErrNotFound
	}
	return p, nil
}

type projectRow interface {
	Scan(dest ...any) error
}

func scanProject(row projectRow) (Project, error) {
	var p Project
	var createdAtMs, updatedAtMs, lastActivityAtMs int64
	var expiresAtMs sql.NullInt64
	var ownerUserID sql.NullInt64
	var creatorUserID sql.NullInt64
	var image sql.NullString
	if err := row.Scan(&p.ID, &p.Name, &image, &p.Slug, &p.DominantColor, &p.EstimationMode, &p.DefaultSprintWeeks, &ownerUserID, &creatorUserID, &lastActivityAtMs, &expiresAtMs, &createdAtMs, &updatedAtMs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Project{}, ErrNotFound
		}
		return Project{}, fmt.Errorf("get project: %w", err)
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
	return p, nil
}

func touchProject(ctx context.Context, tx *sql.Tx, projectID int64, nowMs int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at=? WHERE id=?`, nowMs, projectID); err != nil {
		return fmt.Errorf("touch project: %w", err)
	}
	return nil
}

func (s *Store) UpdateProjectImage(ctx context.Context, projectID int64, userID int64, image *string, dominantColor string) error {
	// Load project to check if it's an anonymous temp board
	p, err := s.getProject(ctx, projectID)
	if err != nil {
		return err
	}

	// CRITICAL: Anonymous temporary boards are immutable at the project level.
	// Their image cannot be changed. Return ErrNotFound to avoid leaking existence.
	if isAnonymousTemporaryBoard(p) {
		return ErrNotFound
	}

	if err := s.CheckProjectRole(ctx, projectID, userID, RoleMaintainer); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin update project image: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	nowMs := time.Now().UTC().UnixMilli()
	if image == nil {
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET image = NULL, dominant_color = ?, updated_at = ? WHERE id = ?`, dominantColor, nowMs, projectID); err != nil {
			return fmt.Errorf("update project image: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET image = ?, dominant_color = ?, updated_at = ? WHERE id = ?`, *image, dominantColor, nowMs, projectID); err != nil {
			return fmt.Errorf("update project image: %w", err)
		}
	}
	changed := "dominant_color"
	if image != nil {
		changed = "image"
	}
	actorUserID := &userID
	meta := map[string]any{"changed": changed}
	if err := insertAuditEventTx(ctx, tx, projectID, actorUserID, "project_image_updated", "project", &projectID, meta); err != nil {
		return fmt.Errorf("audit project_image_updated: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update project image: %w", err)
	}
	return nil
}

func (s *Store) UpdateProjectDefaultSprintWeeks(ctx context.Context, projectID int64, userID int64, weeks int) error {
	if err := s.CheckProjectRole(ctx, projectID, userID, RoleMaintainer); err != nil {
		return err
	}
	if weeks != 1 && weeks != 2 {
		return fmt.Errorf("%w: defaultSprintWeeks must be 1 or 2", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin update project default sprint weeks: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var fromWeeks int
	if err := tx.QueryRowContext(ctx, `SELECT default_sprint_weeks FROM projects WHERE id = ?`, projectID).Scan(&fromWeeks); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("get project default_sprint_weeks: %w", err)
	}
	nowMs := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET default_sprint_weeks = ?, updated_at = ? WHERE id = ?`, weeks, nowMs, projectID); err != nil {
		return fmt.Errorf("update project default sprint weeks: %w", err)
	}
	actorUserID := &userID
	meta := map[string]any{"from_weeks": fromWeeks, "to_weeks": weeks}
	if err := insertAuditEventTx(ctx, tx, projectID, actorUserID, "project_default_sprint_weeks_updated", "project", &projectID, meta); err != nil {
		return fmt.Errorf("audit project_default_sprint_weeks_updated: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update project default sprint weeks: %w", err)
	}
	return nil
}

func (s *Store) UpdateProjectName(ctx context.Context, projectID int64, userID int64, name string) error {
	p, err := s.getProject(ctx, projectID)
	if err != nil {
		return err
	}

	// Rename bypass auth only if: expires_at IS NOT NULL AND creator_user_id IS NULL (anonymous temp board).
	// Do not allow unauthenticated rename for any other case.
	isAnonymousTempBoard := p.ExpiresAt != nil && p.CreatorUserID == nil
	if isAnonymousTempBoard {
		// Check if board is expired - expired boards cannot be renamed
		now := time.Now().UTC()
		if p.ExpiresAt.Before(now) {
			return ErrNotFound
		}
		// Anonymous temp boards can be renamed by anyone (no auth required)
	} else {
		// Durable or authenticated temp board - require Maintainer+
		if err := s.CheckProjectRole(ctx, projectID, userID, RoleMaintainer); err != nil {
			return err
		}
	}

	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return fmt.Errorf("%w: invalid project name", ErrValidation)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin update project name: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var fromName string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM projects WHERE id = ?`, projectID).Scan(&fromName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("get project name: %w", err)
	}
	nowMs := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET name = ?, updated_at = ? WHERE id = ?`, name, nowMs, projectID); err != nil {
		return fmt.Errorf("update project name: %w", err)
	}
	actorUserID := &userID
	meta := map[string]any{"from_name": fromName, "to_name": name}
	if err := insertAuditEventTx(ctx, tx, projectID, actorUserID, "project_renamed", "project", &projectID, meta); err != nil {
		return fmt.Errorf("audit project_renamed: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update project name: %w", err)
	}
	return nil
}

// CreateAnonymousBoard creates a new project for anonymous board mode.
// Sets expires_at = now + 14 days and last_activity_at = now.
func (s *Store) CreateAnonymousBoard(ctx context.Context) (Project, error) {
	nowMs := time.Now().UTC().UnixMilli()
	expiresAtMs := nowMs + (14 * 24 * 60 * 60 * 1000) // 14 days in milliseconds
	defaultImage := "/scrumboy.png"

	var (
		id            int64
		slug          string
		creatorUserID *int64
	)
	// Check if user is authenticated - set creator_user_id if authenticated
	if userID, ok := UserIDFromContext(ctx); ok {
		creatorUserID = &userID
	}

	// Set name based on whether board is created in anonymous mode (no creator) or auth state (with creator)
	var name string
	if creatorUserID == nil {
		name = "Anonymous Board"
	} else {
		name = "Temporary Board"
	}
	for i := 0; i < 10; i++ {
		slugVal, err := randomSlug(8)
		if err != nil {
			return Project{}, err
		}
		slug = slugVal
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
		if err != nil {
			return Project{}, fmt.Errorf("begin create anonymous board: %w", err)
		}
		r, err := tx.ExecContext(ctx, `INSERT INTO projects(name, image, slug, estimation_mode, creator_user_id, last_activity_at, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, name, defaultImage, slug, EstimationModeModifiedFibonacci, creatorUserID, nowMs, expiresAtMs, nowMs, nowMs)
		if err != nil {
			_ = tx.Rollback()
			if strings.Contains(err.Error(), "UNIQUE constraint failed: projects.slug") {
				continue
			}
			return Project{}, fmt.Errorf("insert anonymous board: %w", err)
		}
		lastID, err := r.LastInsertId()
		if err != nil {
			_ = tx.Rollback()
			return Project{}, fmt.Errorf("last insert id anonymous board: %w", err)
		}
		id = lastID

		actorUserID := creatorUserID
		meta := map[string]any{"name": name, "is_anonymous": creatorUserID == nil}
		if err := insertAuditEventTx(ctx, tx, id, actorUserID, "project_created", "project", &id, meta); err != nil {
			_ = tx.Rollback()
			return Project{}, fmt.Errorf("audit project_created: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return Project{}, fmt.Errorf("commit create anonymous board: %w", err)
		}
		break
	}

	// Auto-populate default tags for anonymous boards (only for truly anonymous boards: creator_user_id IS NULL)
	// This ensures default tags are only created for anonymous temp boards, not authenticated temp boards
	if creatorUserID == nil {
		// Tags are optional - log but don't fail project creation
		_ = s.createDefaultBoardScopedTags(ctx, id)
	}
	if err := s.EnsureDefaultWorkflowColumns(ctx, id); err != nil {
		return Project{}, err
	}

	expiresAt := time.UnixMilli(expiresAtMs).UTC()
	return Project{
		ID:                 id,
		Name:               name,
		Image:              &defaultImage,
		DominantColor:      "#888888",
		EstimationMode:     EstimationModeModifiedFibonacci,
		DefaultSprintWeeks: 2,
		Slug:               slug,
		LastActivityAt:     time.UnixMilli(nowMs).UTC(),
		ExpiresAt:          &expiresAt,
		CreatedAt:          time.UnixMilli(nowMs).UTC(),
		UpdatedAt:          time.UnixMilli(nowMs).UTC(),
	}, nil
}

// UpdateBoardActivity updates last_activity_at and extends expires_at for anonymous boards.
// Throttle: only updates if last_activity_at is older than 5 minutes.
// This reduces database writes while still tracking activity and extending expiration.
func (s *Store) UpdateBoardActivity(ctx context.Context, projectID int64) error {
	nowMs := time.Now().UTC().UnixMilli()
	throttleMs := nowMs - (5 * 60 * 1000) // 5 minutes ago

	// Check current last_activity_at to determine if we should update
	var lastActivityAtMs int64
	err := s.db.QueryRowContext(ctx, `SELECT last_activity_at FROM projects WHERE id = ?`, projectID).Scan(&lastActivityAtMs)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("get last activity: %w", err)
	}

	// Only update if last_activity_at is older than 5 minutes (throttle both fields)
	if lastActivityAtMs < throttleMs {
		_, err = s.db.ExecContext(ctx, `
			UPDATE projects 
			SET last_activity_at = ?,
			    expires_at = CASE 
			        WHEN expires_at IS NOT NULL THEN ? 
			        ELSE expires_at 
			    END
			WHERE id = ?`, nowMs, nowMs+(14*24*60*60*1000), projectID)
		if err != nil {
			return fmt.Errorf("update board activity: %w", err)
		}
	}
	// If throttled, do nothing (no database write)
	return nil
}

// BackfillDominantColors extracts and stores dominant colors for projects that have an image
// but still carry the default '#888888' color from the initial migration. The extractor
// function receives the raw image value (data URL) stored in the database and returns a
// hex color string. Returns the number of projects updated.
func (s *Store) BackfillDominantColors(ctx context.Context, extractor func(string) string) (int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, image FROM projects WHERE image IS NOT NULL AND dominant_color = '#888888' AND import_batch_id IS NULL`)
	if err != nil {
		return 0, fmt.Errorf("backfill dominant colors query: %w", err)
	}
	defer rows.Close()

	type row struct {
		id    int64
		image string
	}
	var pending []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.image); err != nil {
			return 0, fmt.Errorf("backfill dominant colors scan: %w", err)
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("backfill dominant colors rows: %w", err)
	}

	nowMs := time.Now().UTC().UnixMilli()
	updated := 0
	for _, r := range pending {
		color := extractor(r.image)
		if color == "#888888" {
			continue
		}
		if _, err := s.db.ExecContext(ctx,
			`UPDATE projects SET dominant_color = ?, updated_at = ? WHERE id = ?`,
			color, nowMs, r.id); err != nil {
			return updated, fmt.Errorf("backfill dominant colors update %d: %w", r.id, err)
		}
		updated++
	}
	return updated, nil
}

// DeleteExpiredProjects deletes projects where expires_at IS NOT NULL AND expires_at < now().
// Returns the count of deleted projects.
func (s *Store) DeleteExpiredProjects(ctx context.Context) (int64, error) {
	nowMs := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE expires_at IS NOT NULL AND expires_at < ?`, nowMs)
	if err != nil {
		return 0, fmt.Errorf("delete expired projects: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected delete expired: %w", err)
	}
	return n, nil
}
