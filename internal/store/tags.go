package store

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

var tagRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

var hyphenRe = regexp.MustCompile(`-+`)

// CanonicalizeTag applies the canonical tag name rule: lowercase, collapse spaces to hyphens,
// collapse repeated hyphens, trim. Returns "" if the result is invalid (empty or does not match tagRe).
// All tag names used in DB lookups/inserts should go through this for consistency.
func CanonicalizeTag(name string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		return ""
	}
	s = strings.ToLower(s)
	s = strings.Join(strings.Fields(s), "-")
	s = hyphenRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" || !tagRe.MatchString(s) {
		return ""
	}
	return s
}

// defaultTagsForAnonymousBoards defines tags and their colors to auto-populate on anonymous boards.
//
// IMPORTANT: These defaults are intentionally hardcoded and anonymous-only.
// They are part of the anonymous board UX, not a general "starter tag" feature.
// These defaults must NEVER be reused for durable boards or authenticated projects.
// They are NOT a general tag system feature and must not be called elsewhere.
var defaultTagsForAnonymousBoards = map[string]string{
	"bug":                 "#FF0000", // red
	"feature":             "#00FF00", // green
	"enhancement":         "#800080", // purple
	"tech-debt":           "#808080", // gray
	"infrastructure":      "#A52A2A", // brown
	"performance":         "#ADD8E6", // light blue
	"security":            "#00008B", // dark blue
	"ui":                  "#FF00FF", // fuchsia
	"ux":                  "#FFC0CB", // pink
	"backend":             "#FFFF00", // yellow
	"frontend":            "#FFA500", // orange
	"api":                 "#DDA0DD", // plum
	"database":            "#008080", // teal
	"testing":             "#00CED1", // dark turquoise
	"devops":              "#708090", // slate gray
	"documentation":       "#87CEEB", // sky blue
	"blocking":            "#FF4500", // orange red
	"needs-investigation": "#FFD700", // gold
	"regression":          "#8B0000", // dark red
	"cleanup":             "#D3D3D3", // light gray
}

func normalizeTagFilter(tag string) string {
	return CanonicalizeTag(tag)
}

func normalizeTags(in []string) ([]string, error) {
	if len(in) > 20 {
		return nil, fmt.Errorf("%w: too many tags", ErrValidation)
	}

	seen := make(map[string]struct{})
	var out []string
	for _, raw := range in {
		t := CanonicalizeTag(raw)
		if t == "" {
			return nil, fmt.Errorf("%w: invalid tag %q", ErrValidation, raw)
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}

// listTodoTagsTx returns ALL tags on a todo, regardless of owner (collaboration-friendly)
func listTodoTagsTx(ctx context.Context, tx *sql.Tx, todoID int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT g.name
FROM todo_tags tt
JOIN tags g ON g.id = tt.tag_id
WHERE tt.todo_id = ?
ORDER BY g.name`, todoID)
	if err != nil {
		return nil, fmt.Errorf("list todo tags: %w", err)
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan todo tag: %w", err)
		}
		tags = append(tags, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows todo tags: %w", err)
	}

	return tags, nil
}

// listTagsForTodos returns tag names for the given todo IDs. Used by board queries to avoid
// tag joins in the main todo query. Returns map[todoID][]tagNames (sorted, deduped).
// Empty todoIDs returns empty map. Batches at 500 IDs to stay under SQLite placeholder limit.
func (s *Store) listTagsForTodos(ctx context.Context, todoIDs []int64) (map[int64][]string, error) {
	if len(todoIDs) == 0 {
		return map[int64][]string{}, nil
	}
	const batchSize = 500
	out := make(map[int64][]string)
	for i := 0; i < len(todoIDs); i += batchSize {
		end := i + batchSize
		if end > len(todoIDs) {
			end = len(todoIDs)
		}
		batch := todoIDs[i:end]
		ph := make([]string, len(batch))
		args := make([]any, len(batch))
		for j, id := range batch {
			ph[j] = "?"
			args[j] = id
		}
		rows, err := s.db.QueryContext(ctx, `
SELECT tt.todo_id, g.name
FROM todo_tags tt
JOIN tags g ON g.id = tt.tag_id
WHERE tt.todo_id IN (`+strings.Join(ph, ",")+`)
ORDER BY tt.todo_id, g.name`, args...)
		if err != nil {
			return nil, fmt.Errorf("list tags for todos: %w", err)
		}
		// Collect per-todo; use map for dedupe per todo
		seen := make(map[int64]map[string]struct{})
		for rows.Next() {
			var todoID int64
			var name string
			if err := rows.Scan(&todoID, &name); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan todo tag: %w", err)
			}
			if seen[todoID] == nil {
				seen[todoID] = make(map[string]struct{})
			}
			seen[todoID][name] = struct{}{}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("rows todo tags: %w", err)
		}
		for todoID, names := range seen {
			sl := make([]string, 0, len(names))
			for n := range names {
				sl = append(sl, n)
			}
			sort.Strings(sl)
			out[todoID] = sl
		}
	}
	return out, nil
}

// ListTagCounts returns all tags used in the project. Use when ProjectContext is available (e.g. tags endpoint).
func (s *Store) ListTagCounts(ctx context.Context, pc *ProjectContext) ([]TagCount, error) {
	var viewerUserID *int64
	if userID, ok := UserIDFromContext(ctx); ok {
		viewerUserID = &userID
	}
	return s.listTagCounts(ctx, pc.Project.ID, viewerUserID, &pc.Role)
}

// listTagCounts returns all tags used in the project, one row per tag_id.
// When viewerRole is non-nil, it is used (avoids GetProjectRole query); otherwise role is fetched.
// All permission checks and delete actions are tag_id-based; tag names are display-only.
func (s *Store) listTagCounts(ctx context.Context, projectID int64, viewerUserID *int64, viewerRole *ProjectRole) ([]TagCount, error) {
	if viewerRole == nil && viewerUserID != nil {
		role, _ := s.GetProjectRole(ctx, projectID, *viewerUserID)
		viewerRole = &role
	}

	// This query MUST use UNION ALL instead of OR.
	// OR over LEFT JOINs with GROUP BY can cause SQLite to hang indefinitely.
	// Do not refactor into a single SELECT.
	//
	// CRITICAL: The user_tag_colors join MUST be in the query, not in a nested
	// query inside the rows loop. With SetMaxOpenConns(1), executing a query
	// while rows are open causes a connection pool self-deadlock (the open Rows
	// holds the only connection; the nested query waits for a connection forever).
	var rows *sql.Rows
	var err error
	if viewerUserID != nil {
		rows, err = s.db.QueryContext(ctx, `
SELECT
  g.id,
  g.name,
  g.user_id,
  g.project_id,
  g.color AS board_color,
  COUNT(DISTINCT t.id) AS c
FROM tags g
LEFT JOIN todo_tags tt ON tt.tag_id = g.id
LEFT JOIN todos t ON t.id = tt.todo_id AND t.project_id = ?
WHERE g.project_id = ? AND g.user_id IS NULL
GROUP BY g.id

UNION ALL

SELECT
  g.id,
  g.name,
  g.user_id,
  g.project_id,
  utc.color AS board_color,
  COUNT(DISTINCT t.id) AS c
FROM todos t
JOIN todo_tags tt ON tt.todo_id = t.id
JOIN tags g ON g.id = tt.tag_id
LEFT JOIN user_tag_colors utc ON utc.tag_id = g.id AND utc.user_id = ?
WHERE t.project_id = ? AND g.user_id IS NOT NULL
GROUP BY g.id

ORDER BY name`, projectID, projectID, *viewerUserID, projectID)
	} else {
		rows, err = s.db.QueryContext(ctx, `
SELECT
  g.id,
  g.name,
  g.user_id,
  g.project_id,
  g.color AS board_color,
  COUNT(DISTINCT t.id) AS c
FROM tags g
LEFT JOIN todo_tags tt ON tt.tag_id = g.id
LEFT JOIN todos t ON t.id = tt.todo_id AND t.project_id = ?
WHERE g.project_id = ? AND g.user_id IS NULL
GROUP BY g.id

UNION ALL

SELECT
  g.id,
  g.name,
  g.user_id,
  g.project_id,
  NULL AS board_color,
  COUNT(DISTINCT t.id) AS c
FROM todos t
JOIN todo_tags tt ON tt.todo_id = t.id
JOIN tags g ON g.id = tt.tag_id
WHERE t.project_id = ? AND g.user_id IS NOT NULL
GROUP BY g.id

ORDER BY name`, projectID, projectID, projectID)
	}
	if err != nil {
		return nil, fmt.Errorf("list tag counts: %w", err)
	}
	defer rows.Close()

	var out []TagCount
	for rows.Next() {
		var tc TagCount
		var tagUserID sql.NullInt64
		var tagProjectID sql.NullInt64
		var boardColor sql.NullString
		if err := rows.Scan(&tc.TagID, &tc.Name, &tagUserID, &tagProjectID, &boardColor, &tc.Count); err != nil {
			return nil, fmt.Errorf("scan tag count: %w", err)
		}
		if tagProjectID.Valid && !tagUserID.Valid {
			tc.Color = nil
			if boardColor.Valid && boardColor.String != "" {
				c := boardColor.String
				tc.Color = &c
			}
			// Project-scoped: canDelete = requester is project maintainer/admin
			if viewerRole != nil && viewerRole.HasMinimumRole(RoleMaintainer) {
				tc.CanDelete = true
			}
		} else if tagUserID.Valid {
			// User-owned: canDelete = requester is tag owner
			if viewerUserID != nil && *viewerUserID == tagUserID.Int64 {
				tc.CanDelete = true
			}
			// Color comes from the query's LEFT JOIN user_tag_colors (no nested query)
			if boardColor.Valid && boardColor.String != "" {
				c := boardColor.String
				tc.Color = &c
			}
		}
		out = append(out, tc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows tag counts: %w", err)
	}
	return out, nil
}

type TagWithColor struct {
	TagID     int64 // Authority and mutations are tag_id-based
	Name      string
	Color     *string // Hex color code, nil if no custom color
	CanDelete bool    // Computed per tag_id from role/ownership; never from name groups
}

// ListUserTags returns all tags owned by user (cross-project tag library).
// All are user-owned so CanDelete = true for every row.
func (s *Store) ListUserTags(ctx context.Context, userID int64) ([]TagWithColor, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT g.id, g.name, utc.color
FROM tags g
LEFT JOIN user_tag_colors utc ON g.id = utc.tag_id AND utc.user_id = ?
WHERE g.user_id = ?
ORDER BY g.name`, userID, userID)
	if err != nil {
		return nil, fmt.Errorf("list user tags: %w", err)
	}
	defer rows.Close()

	tags := make([]TagWithColor, 0)
	for rows.Next() {
		var twc TagWithColor
		var color sql.NullString
		if err := rows.Scan(&twc.TagID, &twc.Name, &color); err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
		}
		twc.CanDelete = true
		if color.Valid && color.String != "" {
			twc.Color = &color.String
		}
		tags = append(tags, twc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows tags: %w", err)
	}

	return tags, nil
}

// ListUserTagsForProject returns tags owned by user that are attached to or used in the project.
// Used for autocomplete/tag picker. CanDelete = true (all are user's own).
func (s *Store) ListUserTagsForProject(ctx context.Context, userID int64, projectID int64) ([]TagWithColor, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT g.id, g.name, utc.color
FROM tags g
LEFT JOIN project_tags pt ON g.id = pt.tag_id AND pt.project_id = ?
LEFT JOIN todo_tags tt ON g.id = tt.tag_id
LEFT JOIN todos t ON tt.todo_id = t.id AND t.project_id = ?
LEFT JOIN user_tag_colors utc ON g.id = utc.tag_id AND utc.user_id = ?
WHERE g.user_id = ?
  AND (pt.project_id IS NOT NULL OR t.project_id IS NOT NULL)
ORDER BY pt.created_at DESC NULLS LAST, g.created_at DESC`, projectID, projectID, userID, userID)
	if err != nil {
		return nil, fmt.Errorf("list user tags for project: %w", err)
	}
	defer rows.Close()

	tags := make([]TagWithColor, 0)
	for rows.Next() {
		var twc TagWithColor
		var color sql.NullString
		if err := rows.Scan(&twc.TagID, &twc.Name, &color); err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
		}
		twc.CanDelete = true
		if color.Valid && color.String != "" {
			twc.Color = &color.String
		}
		tags = append(tags, twc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows tags: %w", err)
	}

	return tags, nil
}

// ListBoardTagsForProject returns board-scoped tags for anonymous boards.
// Used for autocomplete on anonymous temporary boards. TagID set; CanDelete left false (caller may not have role context).
func (s *Store) ListBoardTagsForProject(ctx context.Context, projectID int64) ([]TagWithColor, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, color
FROM tags
WHERE project_id = ? AND user_id IS NULL
ORDER BY name`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list board tags for project: %w", err)
	}
	defer rows.Close()

	tags := make([]TagWithColor, 0)
	for rows.Next() {
		var twc TagWithColor
		var color sql.NullString
		if err := rows.Scan(&twc.TagID, &twc.Name, &color); err != nil {
			return nil, fmt.Errorf("scan board tag: %w", err)
		}
		if color.Valid && color.String != "" {
			twc.Color = &color.String
		}
		tags = append(tags, twc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows board tags: %w", err)
	}

	return tags, nil
}

// ListTags is deprecated - use ListUserTagsForProject instead
// Kept for backward compatibility during transition
func (s *Store) ListTags(ctx context.Context, projectID int64, mode Mode) ([]TagWithColor, error) {
	// This method is deprecated but kept for API compatibility
	// In the new model, we'd need userID to list user's tags
	// For now, return empty list - API should use ListUserTagsForProject
	return []TagWithColor{}, nil
}

// ResolveTagForColorUpdate resolves a tag for color update operations by tag name; all authority is then enforced by tag_id.
// Anonymous-only no-auth: project-scoped tag color update with no auth is allowed only when isAnonymousBoard is true
// (ExpiresAt != nil && CreatorUserID == nil). Durable/authenticated projects require maintainer/admin for project-scoped tags.
func (s *Store) ResolveTagForColorUpdate(ctx context.Context, projectID int64, viewerUserID *int64, tagName string, isAnonymousBoard bool) (int64, error) {
	normalizedName := CanonicalizeTag(tagName)

	var boardTagID int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM tags 
		WHERE project_id = ? AND name = ? AND user_id IS NULL`,
		projectID, normalizedName).Scan(&boardTagID)
	if err == nil {
		if !isAnonymousBoard {
			if viewerUserID == nil {
				return 0, fmt.Errorf("%w: project maintainer required for project-scoped tag", ErrUnauthorized)
			}
			role, err := s.GetProjectRole(ctx, projectID, *viewerUserID)
			if err != nil || !role.HasMinimumRole(RoleMaintainer) {
				return 0, fmt.Errorf("%w: project maintainer required for project-scoped tag", ErrUnauthorized)
			}
		}
		return boardTagID, nil
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("check board-scoped tag: %w", err)
	}

	if viewerUserID == nil {
		return 0, fmt.Errorf("%w: user-owned tag requires authentication", ErrUnauthorized)
	}

	var userTagID int64
	err = s.db.QueryRowContext(ctx, `
		SELECT id FROM tags 
		WHERE name = ? AND user_id = ?`,
		normalizedName, *viewerUserID).Scan(&userTagID)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("%w: tag not found", ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("get user-owned tag: %w", err)
	}

	return userTagID, nil
}

// UpdateTagColor updates tag color
// For user-owned tags: updates user_tag_colors (per-viewer preference)
// For board-scoped tags: updates tags.color directly (board-wide color)
func (s *Store) UpdateTagColor(ctx context.Context, viewerUserID *int64, tagID int64, color *string) error {
	// Check if tag is user-owned or board-scoped
	var tagUserID sql.NullInt64
	var tagProjectID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
SELECT user_id, project_id FROM tags WHERE id = ?`, tagID).Scan(&tagUserID, &tagProjectID)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: tag not found", ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("get tag: %w", err)
	}

	// When color is set (not nil, not empty), validate
	if color != nil && *color != "" {
		colorTrimmed := strings.TrimSpace(*color)
		if !colorHexRe.MatchString(colorTrimmed) {
			return fmt.Errorf("%w: invalid tag color %q", ErrValidation, *color)
		}
		*color = colorTrimmed // normalize
	}

	if tagUserID.Valid {
		// User-owned tag: update user_tag_colors (per-viewer preference)
		if viewerUserID == nil {
			return fmt.Errorf("%w: user-owned tag requires viewerUserID", ErrUnauthorized)
		}
		if color == nil || *color == "" {
			// Remove color preference
			result, err := s.db.ExecContext(ctx, `
DELETE FROM user_tag_colors 
WHERE user_id = ? AND tag_id = ?`, *viewerUserID, tagID)
			if err != nil {
				return fmt.Errorf("delete tag color: %w", err)
			}
			rowsAffected, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("get rows affected: %w", err)
			}
			if rowsAffected == 0 {
				return fmt.Errorf("%w: tag color preference not found", ErrNotFound)
			}
			return nil
		}

		// Insert or update color preference
		_, err := s.db.ExecContext(ctx, `
INSERT INTO user_tag_colors(user_id, tag_id, color)
VALUES (?, ?, ?)
ON CONFLICT(user_id, tag_id) DO UPDATE SET color = excluded.color`, *viewerUserID, tagID, *color)
		if err != nil {
			return fmt.Errorf("update tag color: %w", err)
		}
		return nil
	} else if tagProjectID.Valid {
		// Board-scoped tag: update tags.color directly (board-wide color)
		if color == nil || *color == "" {
			// Remove color
			_, err := s.db.ExecContext(ctx, `UPDATE tags SET color = NULL WHERE id = ?`, tagID)
			if err != nil {
				return fmt.Errorf("clear board tag color: %w", err)
			}
			return nil
		}

		// Set color
		_, err := s.db.ExecContext(ctx, `UPDATE tags SET color = ? WHERE id = ?`, *color, tagID)
		if err != nil {
			return fmt.Errorf("update board tag color: %w", err)
		}
		return nil
	}

	return fmt.Errorf("%w: tag has neither user_id nor project_id", ErrConflict)
}

// UpdateTagColorForProject updates tag color, resolving the tag and enforcing ownership rules.
// isAnonymousBoard: true only when project is anonymous temp board (ExpiresAt != nil && CreatorUserID == nil).
func (s *Store) UpdateTagColorForProject(ctx context.Context, projectID int64, viewerUserID *int64, tagName string, color *string, isAnonymousBoard bool) error {
	tagID, err := s.ResolveTagForColorUpdate(ctx, projectID, viewerUserID, tagName, isAnonymousBoard)
	if err != nil {
		return err
	}
	return s.UpdateTagColor(ctx, viewerUserID, tagID, color)
}

// GetTagColor returns viewer's color preference for a tag, or nil if not set
func (s *Store) GetTagColor(ctx context.Context, userID int64, tagID int64) (*string, error) {
	var color sql.NullString
	err := s.db.QueryRowContext(ctx, `
SELECT color FROM user_tag_colors 
WHERE user_id = ? AND tag_id = ?`, userID, tagID).Scan(&color)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get tag color: %w", err)
	}
	if color.Valid && color.String != "" {
		return &color.String, nil
	}
	return nil, nil
}

// projectsWhereTagIsUsed returns the distinct project IDs where the tag is used
// (via todo_tags+todos or project_tags). Used for maintainer-override checks on user-owned tags.
func (s *Store) projectsWhereTagIsUsed(ctx context.Context, tagID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT project_id FROM (
			SELECT t.project_id FROM todo_tags tt JOIN todos t ON tt.todo_id = t.id WHERE tt.tag_id = ?
			UNION
			SELECT project_id FROM project_tags WHERE tag_id = ?
		)`, tagID, tagID)
	if err != nil {
		return nil, fmt.Errorf("projects where tag used: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan project id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// DeleteTag deletes a tag by tag_id. All permission checks and mutations are tag_id-based.
// Names are display-only and must never be used to infer authorization.
//
// Anonymous-only no-auth: No-auth delete is allowed only when isAnonymousBoard is true
// (ExpiresAt != nil && CreatorUserID == nil). All other projects require auth + maintainer/admin.
//
// Atomic delete: One transaction — DELETE FROM todo_tags WHERE tag_id = ?,
// then DELETE FROM tags WHERE id = ?. We delete todo_tags ourselves; project_tags and
// user_tag_colors are removed by FK ON DELETE CASCADE when the tag row is deleted.
func (s *Store) DeleteTag(ctx context.Context, userID int64, tagID int64, isAnonymousBoard bool) error {
	var tagUserID sql.NullInt64
	var tagProjectID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT user_id, project_id FROM tags WHERE id = ?`, tagID).Scan(&tagUserID, &tagProjectID)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: tag not found", ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("get tag: %w", err)
	}

	if tagUserID.Valid {
		// User-owned tag: requester must be owner or maintainer of every project where tag is used
		if tagUserID.Int64 != userID {
			projectIDs, err := s.projectsWhereTagIsUsed(ctx, tagID)
			if err != nil {
				return err
			}
			if len(projectIDs) == 0 {
				return fmt.Errorf("%w: tag not owned by user", ErrUnauthorized)
			}
			for _, pid := range projectIDs {
				role, err := s.GetProjectRole(ctx, pid, userID)
				if err != nil || !role.HasMinimumRole(RoleMaintainer) {
					return fmt.Errorf("%w: tag not owned by user", ErrUnauthorized)
				}
			}
		}
		// Proceed with atomic delete
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
		if err != nil {
			return fmt.Errorf("begin tx: %w", err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `DELETE FROM todo_tags WHERE tag_id = ?`, tagID); err != nil {
			return fmt.Errorf("delete todo_tags: %w", err)
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE id = ? AND user_id = ?`, tagID, userID)
		if err != nil {
			return fmt.Errorf("delete tag: %w", err)
		}
		affected, _ := result.RowsAffected()
		if affected == 0 {
			return fmt.Errorf("%w: tag not found", ErrNotFound)
		}
		return tx.Commit()
	}

	if tagProjectID.Valid {
		// Project-scoped tag: anonymous-only no-auth. Durable/authenticated require maintainer.
		if !isAnonymousBoard {
			if userID == 0 {
				return fmt.Errorf("%w: unauthorized", ErrUnauthorized)
			}
			role, err := s.GetProjectRole(ctx, tagProjectID.Int64, userID)
			if err != nil || !role.HasMinimumRole(RoleMaintainer) {
				return fmt.Errorf("%w: project maintainer required", ErrUnauthorized)
			}
		} else {
			// Optional hardening: assert project state matches caller's claim (ExpiresAt != nil && CreatorUserID == nil)
			p, err := s.GetProject(ctx, tagProjectID.Int64)
			if err != nil {
				return fmt.Errorf("get project: %w", err)
			}
			if p.ExpiresAt == nil || p.CreatorUserID != nil {
				return fmt.Errorf("%w: project is not anonymous", ErrUnauthorized)
			}
		}
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
		if err != nil {
			return fmt.Errorf("begin tx: %w", err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `DELETE FROM todo_tags WHERE tag_id = ?`, tagID); err != nil {
			return fmt.Errorf("delete todo_tags: %w", err)
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE id = ? AND project_id = ?`, tagID, tagProjectID.Int64)
		if err != nil {
			return fmt.Errorf("delete board tag: %w", err)
		}
		affected, _ := result.RowsAffected()
		if affected == 0 {
			return fmt.Errorf("%w: tag not found", ErrNotFound)
		}
		return tx.Commit()
	}

	return fmt.Errorf("%w: tag has neither user_id nor project_id", ErrConflict)
}

// setTodoTags sets tags for a todo
// For user-owned tags: users can only attach tags they own (userID required)
// For board-scoped tags: tags are scoped to project (anonymous boards only, no userID required)
func setTodoTags(ctx context.Context, tx *sql.Tx, projectID, todoID int64, userID *int64, isAnonymousBoard bool, tags []string) error {
	// Clear existing tags for this todo
	if _, err := tx.ExecContext(ctx, `DELETE FROM todo_tags WHERE todo_id=?`, todoID); err != nil {
		return fmt.Errorf("clear todo tags: %w", err)
	}

	nowMs := time.Now().UTC().UnixMilli()
	for _, name := range tags {
		normalizedName := CanonicalizeTag(name)
		if normalizedName == "" {
			continue // skip invalid tags (callers should have validated via normalizeTags)
		}

		var tagID int64

		if isAnonymousBoard {
			// Board-scoped tags: project_id IS NOT NULL, user_id IS NULL
			// UNIQUE constraint: (project_id, name) WHERE user_id IS NULL
			err := tx.QueryRowContext(ctx, `
SELECT id FROM tags WHERE project_id = ? AND name = ? AND user_id IS NULL`, projectID, normalizedName).Scan(&tagID)
			if err == sql.ErrNoRows {
				// Create new board-scoped tag
				res, err := tx.ExecContext(ctx, `
INSERT INTO tags(user_id, project_id, name, created_at, color)
VALUES (NULL, ?, ?, ?, NULL)`, projectID, normalizedName, nowMs)
				if err != nil {
					return fmt.Errorf("create board-scoped tag %q: %w", name, err)
				}
				tagID, err = res.LastInsertId()
				if err != nil {
					return fmt.Errorf("last insert id tag: %w", err)
				}
			} else if err != nil {
				return fmt.Errorf("get board-scoped tag %q: %w", name, err)
			}
		} else {
			// User-owned tags: user_id IS NOT NULL, project_id IS NULL
			// UNIQUE constraint: (user_id, name) WHERE user_id IS NOT NULL
			if userID == nil {
				return fmt.Errorf("userID required for user-owned tags")
			}
			err := tx.QueryRowContext(ctx, `
SELECT id FROM tags WHERE user_id = ? AND name = ?`, *userID, normalizedName).Scan(&tagID)
			if err == sql.ErrNoRows {
				// Create new tag for this user
				res, err := tx.ExecContext(ctx, `
INSERT INTO tags(user_id, name, created_at, project_id, color)
VALUES (?, ?, ?, NULL, NULL)`, *userID, normalizedName, nowMs)
				if err != nil {
					return fmt.Errorf("create user-owned tag %q: %w", name, err)
				}
				tagID, err = res.LastInsertId()
				if err != nil {
					return fmt.Errorf("last insert id tag: %w", err)
				}
			} else if err != nil {
				return fmt.Errorf("get user-owned tag %q: %w", name, err)
			}
		}

		// Link tag to project via project_tags if not already linked (project-wide tag set)
		_, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO project_tags(project_id, tag_id, created_at)
VALUES (?, ?, ?)`, projectID, tagID, nowMs)
		if err != nil {
			return fmt.Errorf("link tag to project %q: %w", name, err)
		}

		// Link tag to todo via todo_tags (UNIQUE constraint prevents duplicates)
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, todoID, tagID)
		if err != nil {
			return fmt.Errorf("insert todo_tag %q: %w", name, err)
		}
	}
	return nil
}

// GetOrCreateTag gets or creates a tag for a user (used by setTodoTags)
func GetOrCreateTag(ctx context.Context, tx *sql.Tx, userID int64, name string) (int64, error) {
	normalizedName := CanonicalizeTag(name)
	if normalizedName == "" {
		return 0, fmt.Errorf("%w: invalid tag name", ErrValidation)
	}

	var tagID int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id = ? AND name = ?`, userID, normalizedName).Scan(&tagID)
	if err == sql.ErrNoRows {
		nowMs := time.Now().UTC().UnixMilli()
		res, err := tx.ExecContext(ctx, `
INSERT INTO tags(user_id, name, created_at)
VALUES (?, ?, ?)`, userID, normalizedName, nowMs)
		if err != nil {
			return 0, fmt.Errorf("create tag: %w", err)
		}
		tagID, err = res.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("last insert id tag: %w", err)
		}
	} else if err != nil {
		return 0, fmt.Errorf("get tag: %w", err)
	}

	return tagID, nil
}

// createDefaultBoardScopedTags creates default tags for an anonymous board.
// Tags are created as board-scoped (user_id IS NULL, project_id IS NOT NULL).
// Colors are set directly in tags.color for tags that have color specifications.
// This function is idempotent - safe to call multiple times (uses INSERT OR IGNORE).
//
// CRITICAL: This function must ONLY be called from CreateAnonymousBoard().
// It must NEVER be called for durable projects or authenticated boards.
// Default tags are anonymous-only UX, not a general tag system feature.
func (s *Store) createDefaultBoardScopedTags(ctx context.Context, projectID int64) error {
	// Verify schema supports board-scoped tags (migration 019 applied)
	var projectIDColumnExists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) > 0 FROM pragma_table_info('tags') WHERE name = 'project_id'
	`).Scan(&projectIDColumnExists)
	if err != nil {
		return fmt.Errorf("check schema: %w", err)
	}
	if !projectIDColumnExists {
		return fmt.Errorf("schema not migrated: tags table missing project_id column (migration 019 required)")
	}

	nowMs := time.Now().UTC().UnixMilli()
	insertedCount := 0

	for tagName, colorHex := range defaultTagsForAnonymousBoards {
		// CRITICAL: Normalize exactly the same way as user-created tags (CanonicalizeTag).
		// This ensures UNIQUE constraint compatibility and prevents future "optimizations"
		// that might break normalization consistency.
		normalizedName := CanonicalizeTag(tagName)

		// All tags have colors, so always use the hex code
		colorValue := colorHex

		// Insert tag with color (or ignore if already exists due to UNIQUE constraint)
		result, err := s.db.ExecContext(ctx, `
			INSERT OR IGNORE INTO tags(user_id, project_id, name, created_at, color)
			VALUES (NULL, ?, ?, ?, ?)`, projectID, normalizedName, nowMs, colorValue)
		if err != nil {
			return fmt.Errorf("create default tag %q: %w", tagName, err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("get rows affected for tag %q: %w", tagName, err)
		}
		if rowsAffected > 0 {
			insertedCount++
		}
	}

	return nil
}

// GetTagIDByName gets a tag ID by name for a specific user
func (s *Store) GetTagIDByName(ctx context.Context, userID int64, tagName string) (int64, error) {
	normalizedName := CanonicalizeTag(tagName)
	var tagID int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id = ? AND name = ?`, userID, normalizedName).Scan(&tagID)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("%w: tag not found", ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("get tag id: %w", err)
	}
	return tagID, nil
}

// GetAnyTagIDByName gets any tag ID by name (for read-only operations only).
// Returns the first tag found with that name (deterministic: MIN(id)).
//
// ⚠️ NEVER use this for mutations (color updates, deletions, etc.).
// Use ResolveTagForColorUpdate() or GetTagIDByName() instead, which enforce
// proper ownership rules and project scoping.
func (s *Store) GetAnyTagIDByName(ctx context.Context, tagName string) (int64, error) {
	normalizedName := CanonicalizeTag(tagName)
	var tagID int64
	err := s.db.QueryRowContext(ctx, `SELECT MIN(id) FROM tags WHERE name = ?`, normalizedName).Scan(&tagID)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("%w: tag not found", ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("get tag id: %w", err)
	}
	return tagID, nil
}

// GetBoardScopedTagIDByName gets a board-scoped tag ID by project and name.
// Used for deleting board-scoped tags on anonymous boards (no auth required).
func (s *Store) GetBoardScopedTagIDByName(ctx context.Context, projectID int64, tagName string) (int64, error) {
	normalizedName := CanonicalizeTag(tagName)
	var tagID int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM tags 
		WHERE project_id = ? AND name = ? AND user_id IS NULL`, projectID, normalizedName).Scan(&tagID)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("%w: tag not found", ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("get board-scoped tag id: %w", err)
	}
	return tagID, nil
}
