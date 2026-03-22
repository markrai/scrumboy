package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"
)

// configureSQLiteForImport configures SQLite PRAGMAs for optimal import performance.
// Best-effort for WAL/cache/mmap (logs but doesn't fail), critical for synchronous/temp_store.
func configureSQLiteForImport(ctx context.Context, conn *sql.Conn) error {
	// Best-effort: Try to set WAL mode, but don't fail if it can't be set
	// (Some SQLite builds/drivers restrict journal_mode changes)
	_, _ = conn.ExecContext(ctx, "PRAGMA journal_mode=WAL")
	// Note: We don't check error - WAL is nice-to-have, not critical

	// Optional performance boosters for NAS (best-effort)
	_, _ = conn.ExecContext(ctx, "PRAGMA cache_size = -20000")   // ~20MB
	_, _ = conn.ExecContext(ctx, "PRAGMA mmap_size = 268435456") // 256MB (if supported)

	// Critical: These must succeed (they're the real performance levers)
	if _, err := conn.ExecContext(ctx, "PRAGMA synchronous=NORMAL"); err != nil {
		return fmt.Errorf("set synchronous: %w", err)
	}

	if _, err := conn.ExecContext(ctx, "PRAGMA temp_store=MEMORY"); err != nil {
		return fmt.Errorf("set temp_store: %w", err)
	}

	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		return fmt.Errorf("set foreign_keys: %w", err)
	}

	return nil
}

// restoreSQLiteDefaults restores SQLite PRAGMAs to defaults.
// Best-effort: logs errors but doesn't fail (import already succeeded).
func restoreSQLiteDefaults(ctx context.Context, conn *sql.Conn) error {
	// Best-effort restore: log errors but don't fail
	// The import succeeded, user doesn't care about PRAGMA restore on a closing connection
	if _, err := conn.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
		// Log but don't return error
		log.Printf("warning: failed to restore PRAGMA synchronous: %v", err)
	}
	return nil
}

// resolveImportColumnKey maps export Status (legacy enum or actual column_key) to a valid column_key for the target project.
// Export writes actual column_key as Status (uppercased); import must support both legacy enum and custom workflow columns.
// Returns (columnKey, warned) where warned is true when the value was unknown and we defaulted to backlog.
func resolveImportColumnKey(ctx context.Context, tx *sql.Tx, projectID int64, statusFromExport string) (columnKey string, warned bool) {
	// 1. Legacy status enum (BACKLOG, NOT_STARTED, IN_PROGRESS, TESTING, DONE)
	if s, ok := ParseStatus(statusFromExport); ok {
		return StatusToColumnKey(s), false
	}
	// 2. Try raw column_key (export preserves actual project column_key as Status for custom workflows)
	candidate := strings.ToLower(strings.TrimSpace(statusFromExport))
	if candidate != "" {
		if _, err := validateProjectColumnKeyQueryer(ctx, tx, projectID, candidate); err == nil {
			return candidate, false
		}
	}
	return DefaultColumnBacklog, true
}

// resolveImportAssignee returns assigneeUserID if that user is a project member in the target DB; otherwise nil.
// Used when importing todos so assignees are restored only when the user exists and has access to the project.
func resolveImportAssignee(ctx context.Context, tx *sql.Tx, projectID int64, assigneeUserID *int64) *int64 {
	if assigneeUserID == nil {
		return nil
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM project_members WHERE project_id = ? AND user_id = ?`, projectID, *assigneeUserID).Scan(&exists); err != nil || !exists {
		return nil
	}
	return assigneeUserID
}

// generateUUID generates a simple UUID-like string for batch IDs.
func generateUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// insertProjectWithBatchID inserts a project with import_batch_id set.
// Handles slug generation if missing, ensures uniqueness within staging batch.
func insertProjectWithBatchID(ctx context.Context, tx *sql.Tx, pExport ProjectExport, mode Mode, batchID string) (int64, error) {
	// Name is required (validated earlier)
	if pExport.Name == "" {
		return 0, fmt.Errorf("%w: project missing name", ErrValidation)
	}

	// Slug is optional: use imported slug, or generate from name
	var slug string
	var baseSlug string

	if pExport.Slug != "" {
		slug = pExport.Slug
		baseSlug = slug // Keep stable base for suffix generation
	} else {
		// Generate slug from name
		var err error
		baseSlug, err = generateSlugFromName(pExport.Name)
		if err != nil {
			// Fallback to random slug
			baseSlug, err = randomSlug(8)
			if err != nil {
				return 0, fmt.Errorf("generate slug: %w", err)
			}
		}
		slug = baseSlug
	}

	// Check uniqueness only within staging (same batch_id); case-insensitive
	var exists bool
	err := tx.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM projects WHERE import_batch_id = ? AND LOWER(slug) = LOWER(?))",
		batchID, slug).Scan(&exists)
	if err != nil {
		return 0, fmt.Errorf("check slug in staging: %w", err)
	}

	if exists {
		// Generate unique slug within staging batch
		// CRITICAL: Keep baseSlug stable, derive candidate each iteration
		for i := 0; i < 100; i++ {
			var candidate string
			suffix := fmt.Sprintf("-%d", i+1)
			maxBaseLen := 32 - len(suffix)
			if len(baseSlug) > maxBaseLen {
				candidate = strings.TrimRight(baseSlug[:maxBaseLen], "-") + suffix
			} else {
				candidate = baseSlug + suffix
			}

			err := tx.QueryRowContext(ctx,
				"SELECT EXISTS(SELECT 1 FROM projects WHERE import_batch_id = ? AND LOWER(slug) = LOWER(?))",
				batchID, candidate).Scan(&exists)
			if err != nil {
				return 0, fmt.Errorf("check slug: %w", err)
			}
			if !exists {
				slug = candidate
				break
			}
			if i == 99 {
				return 0, fmt.Errorf("%w: could not generate unique slug in staging", ErrConflict)
			}
		}
	}

	// Determine owner
	var ownerUserID *int64
	if mode == ModeFull {
		enabled, err := authEnabledTx(ctx, tx)
		if err != nil {
			return 0, err
		}
		if enabled {
			userID, ok := UserIDFromContext(ctx)
			if !ok {
				return 0, ErrUnauthorized
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
		expiresAtMs.Valid = true
		expiresAtMs.Int64 = pExport.ExpiresAt.UnixMilli()
	}

	defaultSprintWeeks := 2
	if pExport.DefaultSprintWeeks == 1 || pExport.DefaultSprintWeeks == 2 {
		defaultSprintWeeks = pExport.DefaultSprintWeeks
	}

	// Set creator_user_id for temporary boards (importing user becomes creator)
	var creatorUserID *int64
	if pExport.ExpiresAt != nil {
		userID, ok := UserIDFromContext(ctx)
		if ok {
			creatorUserID = &userID
		}
	}

	// Insert project with import_batch_id
	res, err := tx.ExecContext(ctx, `
		INSERT INTO projects(name, image, dominant_color, slug, estimation_mode, default_sprint_weeks, owner_user_id, creator_user_id, last_activity_at, expires_at, created_at, updated_at, import_batch_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			pExport.Name, image, dominantColor, slug, EstimationModeModifiedFibonacci, defaultSprintWeeks, ownerUserID, creatorUserID, nowMs, expiresAtMs, createdAtMs, updatedAtMs, batchID)
	if err != nil {
		return 0, fmt.Errorf("insert project: %w", err)
	}

	projectID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}

	// For full projects, add importing user as maintainer so they remain in export scope after promotion
	if !expiresAtMs.Valid && ownerUserID != nil {
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO project_members (project_id, user_id, role, created_at) VALUES (?, ?, 'maintainer', ?)`, projectID, *ownerUserID, nowMs)
		if err != nil {
			return 0, fmt.Errorf("add project member: %w", err)
		}
	}

	return projectID, nil
}

// insertSprintsForImport creates sprints from export data.
// Returns map from sprint number to sprint id for resolving todo sprint_id.
// Sprint number is stable identity: on conflict (project_id, number), updates existing sprint from backup.
// This ensures merge mode correctly restores sprint metadata when target project already has sprints.
func insertSprintsForImport(ctx context.Context, tx *sql.Tx, projectID int64, sprints []SprintExport) (map[int64]int64, error) {
	sprintIDByNumber := make(map[int64]int64)
	if len(sprints) == 0 {
		return sprintIDByNumber, nil
	}
	nowMs := time.Now().UTC().UnixMilli()
	for _, se := range sprints {
		state := se.State
		if state == "" {
			state = SprintStatePlanned
		}
		var startedAtMs, closedAtMs sql.NullInt64
		if se.StartedAt != nil {
			startedAtMs.Valid = true
			startedAtMs.Int64 = *se.StartedAt
		}
		if se.ClosedAt != nil {
			closedAtMs.Valid = true
			closedAtMs.Int64 = *se.ClosedAt
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO sprints (project_id, number, name, planned_start_at, planned_end_at, state, started_at, closed_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(project_id, number) DO UPDATE SET
				name = excluded.name,
				planned_start_at = excluded.planned_start_at,
				planned_end_at = excluded.planned_end_at,
				state = excluded.state,
				started_at = excluded.started_at,
				closed_at = excluded.closed_at,
				updated_at = excluded.updated_at`,
			projectID, se.Number, se.Name, se.PlannedStartAt, se.PlannedEndAt, state, startedAtMs, closedAtMs, nowMs, nowMs)
		if err != nil {
			return nil, fmt.Errorf("insert sprint %d: %w", se.Number, err)
		}
		var sprintID int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM sprints WHERE project_id = ? AND number = ?`, projectID, se.Number).Scan(&sprintID); err != nil {
			return nil, fmt.Errorf("get sprint id for number %d: %w", se.Number, err)
		}
		sprintIDByNumber[se.Number] = sprintID
	}
	return sprintIDByNumber, nil
}

// bulkInsertTodos inserts todos in bulk for a project.
// Returns a map from local_id to todo_id.
// If strict is true, errors immediately on local_id collision (for Replace mode).
// If warnings is non-nil, appends a warning when an unknown status is defaulted to backlog.
// sprintIDByNumber maps export sprint number to sprint id; nil means no sprint resolution.
func bulkInsertTodos(ctx context.Context, tx *sql.Tx, projectID int64, todos []TodoExport, strict bool, warnings *[]string, sprintIDByNumber map[int64]int64) (map[int64]int64, error) {
	todoIDMap := make(map[int64]int64)

	if len(todos) == 0 {
		return todoIDMap, nil
	}

	// For strict mode, check for collisions first
	if strict {
		for _, tExport := range todos {
			if tExport.LocalID == 0 {
				return nil, fmt.Errorf("%w: todo missing localId", ErrValidation)
			}
			var exists bool
			err := tx.QueryRowContext(ctx,
				"SELECT EXISTS(SELECT 1 FROM todos WHERE project_id = ? AND local_id = ?)",
				projectID, tExport.LocalID).Scan(&exists)
			if err != nil {
				return nil, fmt.Errorf("check local_id collision: %w", err)
			}
			if exists {
				return nil, fmt.Errorf("%w: local_id %d already exists in project %d", ErrConflict, tExport.LocalID, projectID)
			}
		}
	}

	// Insert todos one by one (SQLite doesn't support multi-row INSERT with RETURNING easily)
	// We could batch with VALUES, but then we'd need to query back for IDs
	for _, tExport := range todos {
		// Validate required fields
		if tExport.LocalID == 0 {
			return nil, fmt.Errorf("%w: todo missing localId", ErrValidation)
		}
		if tExport.Title == "" {
			return nil, fmt.Errorf("%w: todo missing title", ErrValidation)
		}

		columnKey, warned := resolveImportColumnKey(ctx, tx, projectID, tExport.Status)
		if warned && warnings != nil {
			*warnings = append(*warnings, fmt.Sprintf("Unknown status %q for todo %q, defaulting to backlog", tExport.Status, tExport.Title))
		}
		if err := validateEstimationPoints(tExport.EstimationPoints); err != nil {
			return nil, err
		}

		status := StatusBacklog // for resolveImportDoneAt; columnKey already resolved
		if s, ok := ParseStatus(tExport.Status); ok {
			status = s
		}

		// Use the imported rank, or compute a default
		rank := tExport.Rank
		if rank == 0 {
			var maxRank int64
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rank), 0) FROM todos WHERE project_id = ? AND column_key = ?`, projectID, columnKey).Scan(&maxRank); err != nil {
				return nil, fmt.Errorf("get max rank: %w", err)
			}
			rank = maxRank + 1000
		}

		createdAtMs := tExport.CreatedAt.UnixMilli()
		updatedAtMs := tExport.UpdatedAt.UnixMilli()
		var estimationPoints any
		if tExport.EstimationPoints != nil {
			estimationPoints = *tExport.EstimationPoints
		}
		assigneeVal := resolveImportAssignee(ctx, tx, projectID, tExport.AssigneeUserId)
		var assigneeForSQL any
		if assigneeVal != nil {
			assigneeForSQL = *assigneeVal
		}
		doneAtForInsert := resolveImportDoneAt(tExport.DoneAt, status, updatedAtMs)

		var sprintIDForSQL any
		if tExport.SprintNumber != nil && sprintIDByNumber != nil {
			if id, ok := sprintIDByNumber[*tExport.SprintNumber]; ok {
				sprintIDForSQL = id
			} else if warnings != nil {
				*warnings = append(*warnings, fmt.Sprintf("Sprint number %d for todo %q not found, using backlog", *tExport.SprintNumber, tExport.Title))
			}
		}

		// Insert todo (schema uses column_key, not status)
		res, err := tx.ExecContext(ctx, `
			INSERT INTO todos(project_id, local_id, title, body, column_key, rank, estimation_points, assignee_user_id, sprint_id, created_at, updated_at, done_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			projectID, tExport.LocalID, tExport.Title, tExport.Body, columnKey, rank, estimationPoints, assigneeForSQL, sprintIDForSQL, createdAtMs, updatedAtMs, doneAtForInsert)
		if err != nil {
			if strict {
				return nil, fmt.Errorf("insert todo (strict mode): %w", err)
			}
			// Non-strict: try to regenerate local_id
			if strings.Contains(err.Error(), "UNIQUE constraint failed: todos.project_id, todos.local_id") {
				var maxLocalID int64
				if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(local_id), 0) FROM todos WHERE project_id = ?`, projectID).Scan(&maxLocalID); err != nil {
					return nil, fmt.Errorf("get max local_id: %w", err)
				}
				newLocalID := maxLocalID + 1
				res, err = tx.ExecContext(ctx, `
					INSERT INTO todos(project_id, local_id, title, body, column_key, rank, estimation_points, assignee_user_id, sprint_id, created_at, updated_at, done_at)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					projectID, newLocalID, tExport.Title, tExport.Body, columnKey, rank, estimationPoints, assigneeForSQL, sprintIDForSQL, createdAtMs, updatedAtMs, doneAtForInsert)
				if err != nil {
					return nil, fmt.Errorf("insert todo with regenerated local_id: %w", err)
				}
				tExport.LocalID = newLocalID
			} else {
				return nil, fmt.Errorf("insert todo: %w", err)
			}
		}

		// Get todo ID
		todoID, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("last insert id: %w", err)
		}

		todoIDMap[tExport.LocalID] = todoID
	}

	return todoIDMap, nil
}

// bulkUpsertTags upserts tags for a project with importing user's user_id.
// Returns a map from tag name to tag_id.
// mode parameter kept for backward compatibility but not used
func bulkUpsertTags(ctx context.Context, tx *sql.Tx, projectID int64, tags []TagExport, mode Mode) (map[string]int64, error) {
	// Get userID from context (importing user)
	userID, ok := UserIDFromContext(ctx)
	if !ok {
		// For anonymous/pre-bootstrap, try to get first user
		var firstUserID int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM users LIMIT 1`).Scan(&firstUserID); err != nil {
			// No users - return empty map (tags will be skipped)
			return make(map[string]int64), nil
		}
		userID = firstUserID
	}

	tagIDMap := make(map[string]int64)

	if len(tags) == 0 {
		return tagIDMap, nil
	}

	nowMs := time.Now().UTC().UnixMilli()

	// Get or create tags with user_id
	for _, tagExport := range tags {
		// Normalize tag name
		normalized, err := normalizeTags([]string{tagExport.Name})
		if err != nil {
			return nil, fmt.Errorf("normalize tag %q: %w", tagExport.Name, err)
		}
		if len(normalized) == 0 {
			continue // Skip invalid tags
		}
		tagName := normalized[0]

		// Get or create tag with user_id
		tagID, err := GetOrCreateTag(ctx, tx, userID, tagName)
		if err != nil {
			return nil, fmt.Errorf("get or create tag %q: %w", tagName, err)
		}

		// Link tag to project via project_tags if not already linked
		_, err = tx.ExecContext(ctx, `
INSERT OR IGNORE INTO project_tags(project_id, tag_id, created_at)
VALUES (?, ?, ?)`, projectID, tagID, nowMs)
		if err != nil {
			return nil, fmt.Errorf("link tag to project %q: %w", tagName, err)
		}

		// Set color preference if provided and valid
		if tagExport.Color != nil && *tagExport.Color != "" {
			colorTrimmed := strings.TrimSpace(*tagExport.Color)
			if colorHexRe.MatchString(colorTrimmed) {
				_, err = tx.ExecContext(ctx, `
INSERT INTO user_tag_colors(user_id, tag_id, color)
VALUES (?, ?, ?)
ON CONFLICT(user_id, tag_id) DO UPDATE SET color = excluded.color`, userID, tagID, colorTrimmed)
				if err != nil {
					return nil, fmt.Errorf("set tag color preference %q: %w", tagName, err)
				}
			}
		}

		tagIDMap[tagName] = tagID
	}

	return tagIDMap, nil
}

// bulkInsertLinks inserts todo links for a project. Skips invalid pairs (self-link, missing todos, invalid link_type).
func bulkInsertLinks(ctx context.Context, tx *sql.Tx, projectID int64, links []LinkExport) error {
	if len(links) == 0 {
		return nil
	}
	nowMs := time.Now().UTC().UnixMilli()
	linkTypeAllowed := map[string]bool{"relates_to": true, "blocks": true, "duplicates": true, "parent": true}
	for _, l := range links {
		if l.FromLocalID == l.ToLocalID {
			continue
		}
		lt := l.LinkType
		if lt == "" {
			lt = "relates_to"
		}
		if !linkTypeAllowed[lt] {
			continue
		}
		var fromExists, toExists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM todos WHERE project_id = ? AND local_id = ?`, projectID, l.FromLocalID).Scan(&fromExists); err != nil || fromExists != 1 {
			continue
		}
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM todos WHERE project_id = ? AND local_id = ?`, projectID, l.ToLocalID).Scan(&toExists); err != nil || toExists != 1 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO todo_links(project_id, from_local_id, to_local_id, link_type, created_at)
			VALUES (?, ?, ?, ?, ?)`, projectID, l.FromLocalID, l.ToLocalID, lt, nowMs); err != nil {
			return fmt.Errorf("insert link %d->%d: %w", l.FromLocalID, l.ToLocalID, err)
		}
	}
	return nil
}

// buildTodoTagMap builds a map from todo_id to tag names for bulk linking.
func buildTodoTagMap(todos []TodoExport, todoIDMap map[int64]int64) map[int64][]string {
	todoTagMap := make(map[int64][]string)

	for _, tExport := range todos {
		todoID, ok := todoIDMap[tExport.LocalID]
		if !ok {
			continue // Skip if todo wasn't inserted
		}
		if len(tExport.Tags) > 0 {
			todoTagMap[todoID] = tExport.Tags
		}
	}

	return todoTagMap
}

// bulkLinkTodoTags links todos to tags in bulk using INSERT OR IGNORE.
func bulkLinkTodoTags(ctx context.Context, tx *sql.Tx, todoTagMap map[int64][]string, tagIDMap map[string]int64) error {
	// Process in chunks to avoid huge SQL statements
	const chunkSize = 100

	for todoID, tagNames := range todoTagMap {
		// Normalize tags
		normalized, err := normalizeTags(tagNames)
		if err != nil {
			return fmt.Errorf("normalize tags for todo %d: %w", todoID, err)
		}

		// Build values for bulk insert
		var values []string
		var args []any
		for _, tagName := range normalized {
			tagID, ok := tagIDMap[tagName]
			if !ok {
				continue // Skip if tag doesn't exist
			}
			values = append(values, "(?, ?)")
			args = append(args, todoID, tagID)
		}

		if len(values) == 0 {
			continue
		}

		// Insert in chunks
		for i := 0; i < len(values); i += chunkSize {
			end := i + chunkSize
			if end > len(values) {
				end = len(values)
			}

			chunkValues := values[i:end]
			chunkArgs := args[i*2 : end*2]

			query := fmt.Sprintf(
				"INSERT OR IGNORE INTO todo_tags(todo_id, tag_id) VALUES %s",
				strings.Join(chunkValues, ", "))

			_, err := tx.ExecContext(ctx, query, chunkArgs...)
			if err != nil {
				return fmt.Errorf("bulk link todo tags: %w", err)
			}
		}
	}

	return nil
}
