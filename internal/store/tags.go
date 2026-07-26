package store

import (
	"context"
	"database/sql"
	"errors"
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

// normalizeTagFilter maps a caller-supplied board filter onto the label the chip list
// displays for the given project scope.
//
// Durable projects compare through TagGroupKey so "make space" and "make-space" are the
// same filter. Temporary boards keep the raw trimmed stored name: their chips are still
// one entry per tag row, so rewriting "make space" to "make-space" before the exact-name
// resolver would select the wrong backing row (or none).
//
// A blank filter means "no filter".
func normalizeTagFilter(tag string, durable bool) string {
	trimmed := strings.TrimSpace(tag)
	if trimmed == "" {
		return ""
	}
	if durable {
		return TagGroupKey(trimmed)
	}
	return trimmed
}

// boardTagFilter is a tag filter resolved once for a board request.
// NoFilter means show every todo; otherwise TagIDs are the matching backing rows
// (empty means the filter matches nothing — callers must not fall back to unfiltered).
type boardTagFilter struct {
	NoFilter bool
	TagIDs   []int64
}

// resolveBoardTagFilter normalizes and resolves a raw tag filter for one board request.
// The result is safe to reuse across the full-board, soft-cap count, window, and every
// paged lane list/count for that request.
func (s *Store) resolveBoardTagFilter(ctx context.Context, projectID int64, durable bool, rawFilter string) (boardTagFilter, error) {
	key := normalizeTagFilter(rawFilter, durable)
	if key == "" {
		return boardTagFilter{NoFilter: true}, nil
	}
	ids, err := s.resolveTagFilterRowIDs(ctx, projectID, key, durable)
	if err != nil {
		return boardTagFilter{}, err
	}
	return boardTagFilter{TagIDs: ids}, nil
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
	return s.listTagCounts(ctx, pc.Project.ID, viewerUserID, &pc.Role, pc.Project.ExpiresAt == nil)
}

// TagGroupKey is the canonical grouping key for a stored tag name. Legacy rows can
// hold non-canonical names (e.g. one owner's "make space" alongside another's
// "make-space"), and those must collapse into a single logical label. A name that
// cannot be canonicalized falls back to its raw stored value so no row is silently
// dropped from the listing.
//
// This is the single key function for tag identity. Reads (grouping, counting),
// writes (name-based color/delete) and board filtering all route through it, so a row
// the listing can display is a row those paths can also address.
func TagGroupKey(name string) string {
	if canonical := CanonicalizeTag(name); canonical != "" {
		return canonical
	}
	return name
}

// tagWriteKey maps a caller-supplied label to the key TagGroupKey derives from stored
// names. Canonicalization is not required: the listing falls back to the raw stored
// name for a row that cannot be canonicalized, and rejecting that label here would
// display a grouped entry with working color and delete controls that every write
// refuses. Only genuinely blank input is invalid.
func tagWriteKey(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("%w: invalid tag name", ErrValidation)
	}
	return TagGroupKey(name), nil
}

// tagRowMeta is one backing row for a canonical-name group, used to build the
// grouped projection returned by listTagCounts.
type tagRowMeta struct {
	tagID       int64
	name        string
	userID      sql.NullInt64
	projectID   sql.NullInt64
	boardColor  sql.NullString // tags.color (shared board-scoped color)
	viewerColor sql.NullString // user_tag_colors.color for the current viewer
}

// listTagCounts returns the project's tags for display.
//
// groupByName selects the projection and must be true only for durable projects:
//
//   - true: one entry per canonical name. Two members' personal "bug" rows collapse
//     into a single logical entry that carries no representative TagID, because the
//     durable name-based color/delete routes address it by name.
//   - false: one entry per tag row, each with a real TagID. Temporary boards
//     (ExpiresAt != nil) still resolve colors and deletes through tag_id and the
//     board-scoped name resolver, so collapsing their rows would strand those writes.
//
// Read inclusion is identical in both projections: board-scoped rows are listed even
// when unused, while personal rows are listed only when a todo in the project uses them.
//
// When viewerRole is non-nil, it is used (avoids GetProjectRole query); otherwise role is fetched.
func (s *Store) listTagCounts(ctx context.Context, projectID int64, viewerUserID *int64, viewerRole *ProjectRole, groupByName bool) ([]TagCount, error) {
	if viewerRole == nil && viewerUserID != nil {
		role, _ := s.GetProjectRole(ctx, projectID, *viewerUserID)
		viewerRole = &role
	}
	if !groupByName {
		return s.listTagCountsRowLevel(ctx, projectID, viewerUserID, viewerRole)
	}
	return s.listTagCountsGrouped(ctx, projectID, viewerUserID, viewerRole)
}

// listTagCountsRowLevel returns one TagCount per tag row, always with a real TagID.
// This is the projection temporary boards keep: their mutation surface is tag_id-based
// (plus board-scoped name resolution), so every listed entry must carry an addressable row.
func (s *Store) listTagCountsRowLevel(ctx context.Context, projectID int64, viewerUserID *int64, viewerRole *ProjectRole) ([]TagCount, error) {
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
  g.color AS board_color,
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
			// Board-scoped row: deleting it removes the board's tag for everyone.
			tc.CanDeleteProject = viewerRole != nil && viewerRole.HasMinimumRole(RoleMaintainer)
		} else if tagUserID.Valid {
			// User-owned row: only the owner may delete it, and the delete is global to them.
			tc.CanDeleteMine = viewerUserID != nil && *viewerUserID == tagUserID.Int64
			// Color comes from the query's LEFT JOIN user_tag_colors (no nested query)
			if boardColor.Valid && boardColor.String != "" {
				c := boardColor.String
				tc.Color = &c
			}
		}
		// Temporary boards keep the previous row-level color UX: any holder of the
		// board link may update colors through UpdateTagColorForTemporaryBoard.
		tc.CanUpdateColor = true
		out = append(out, tc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows tag counts: %w", err)
	}
	return out, nil
}

// listTagCountsGrouped returns durable-project tags collapsed to one entry per
// canonical name. Two members' personal rows named "bug" become a single logical
// entry; pure board-scoped groups keep their real tag_id.
func (s *Store) listTagCountsGrouped(ctx context.Context, projectID int64, viewerUserID *int64, viewerRole *ProjectRole) ([]TagCount, error) {
	// Query A and Query B run sequentially, never with the other's Rows open.
	// CRITICAL: with SetMaxOpenConns(1), executing a query while a Rows is open
	// self-deadlocks (the open Rows holds the only connection). We also avoid OR
	// over LEFT JOINs with GROUP BY, which can hang SQLite indefinitely.

	// Query A: the (stored name, todo) pairs used in this project. Counting is done in
	// Go rather than with GROUP BY because the grouping key is CanonicalizeTag(name),
	// which SQLite cannot compute. Aggregating raw names and summing would also
	// double-count a todo carrying both "make space" and "make-space".
	todosByKey := make(map[string]map[int64]struct{})
	countRows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT g.name, tt.todo_id
FROM tags g
JOIN todo_tags tt ON tt.tag_id = g.id
JOIN todos t ON t.id = tt.todo_id
WHERE t.project_id = ?`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list tag counts: %w", err)
	}
	for countRows.Next() {
		var name string
		var todoID int64
		if err := countRows.Scan(&name, &todoID); err != nil {
			countRows.Close()
			return nil, fmt.Errorf("scan tag count: %w", err)
		}
		key := TagGroupKey(name)
		if todosByKey[key] == nil {
			todosByKey[key] = make(map[int64]struct{})
		}
		todosByKey[key][todoID] = struct{}{}
	}
	countRows.Close()
	if err := countRows.Err(); err != nil {
		return nil, fmt.Errorf("rows tag counts: %w", err)
	}

	// Query B: one row per backing tag row that satisfies the read inclusion rule.
	// Board-scoped rows are listed even when unused; personal rows only when used
	// by a todo in the project. viewer_color is resolved in-query for the viewer.
	var metaRows *sql.Rows
	if viewerUserID != nil {
		metaRows, err = s.db.QueryContext(ctx, `
SELECT g.id, g.name, g.user_id, g.project_id, g.color, NULL AS viewer_color
FROM tags g
WHERE g.project_id = ? AND g.user_id IS NULL

UNION ALL

SELECT DISTINCT g.id, g.name, g.user_id, g.project_id, g.color, utc.color AS viewer_color
FROM tags g
JOIN todo_tags tt ON tt.tag_id = g.id
JOIN todos t ON t.id = tt.todo_id AND t.project_id = ?
LEFT JOIN user_tag_colors utc ON utc.tag_id = g.id AND utc.user_id = ?
WHERE g.user_id IS NOT NULL

ORDER BY id`, projectID, projectID, *viewerUserID)
	} else {
		metaRows, err = s.db.QueryContext(ctx, `
SELECT g.id, g.name, g.user_id, g.project_id, g.color, NULL AS viewer_color
FROM tags g
WHERE g.project_id = ? AND g.user_id IS NULL

UNION ALL

SELECT DISTINCT g.id, g.name, g.user_id, g.project_id, g.color, NULL AS viewer_color
FROM tags g
JOIN todo_tags tt ON tt.tag_id = g.id
JOIN todos t ON t.id = tt.todo_id AND t.project_id = ?
WHERE g.user_id IS NOT NULL

ORDER BY id`, projectID, projectID)
	}
	if err != nil {
		return nil, fmt.Errorf("list tag rows: %w", err)
	}
	defer metaRows.Close()

	// Bucket by canonical key, preserving first-seen (lowest tag_id, due to ORDER BY id)
	// order so color precedence over the lowest backing row is stable.
	grouped := make(map[string][]tagRowMeta)
	var keyOrder []string
	for metaRows.Next() {
		var m tagRowMeta
		if err := metaRows.Scan(&m.tagID, &m.name, &m.userID, &m.projectID, &m.boardColor, &m.viewerColor); err != nil {
			return nil, fmt.Errorf("scan tag row: %w", err)
		}
		key := TagGroupKey(m.name)
		if _, ok := grouped[key]; !ok {
			keyOrder = append(keyOrder, key)
		}
		grouped[key] = append(grouped[key], m)
	}
	if err := metaRows.Err(); err != nil {
		return nil, fmt.Errorf("rows tag rows: %w", err)
	}

	isMaintainer := viewerRole != nil && viewerRole.HasMinimumRole(RoleMaintainer)

	out := make([]TagCount, 0, len(keyOrder))
	for _, key := range keyOrder {
		rowsForName := grouped[key]
		// Rows arrive ordered by tag_id (ORDER BY id) so lowest-id color precedence is stable.
		personal := false
		for _, r := range rowsForName {
			if r.userID.Valid {
				personal = true
				break
			}
		}

		tc := TagCount{
			// The canonical key is the displayed label, so legacy non-canonical rows
			// surface under the same name the write routes resolve.
			Name:  key,
			Count: len(todosByKey[key]),
			Color: pickGroupedTagColor(rowsForName, viewerUserID),
		}
		if personal {
			// Personal-label group (includes mixed board+personal): no representative tag_id.
			if viewerUserID != nil {
				for _, r := range rowsForName {
					if r.userID.Valid && r.userID.Int64 == *viewerUserID {
						tc.CanDeleteMine = true
						break
					}
				}
				// Any authenticated member may set their own per-viewer color via the
				// name route (or a compatibility id preference write).
				tc.CanUpdateColor = true
			}
		} else {
			// Pure board-scoped group: real tag_id, shared color/delete for maintainers.
			tc.TagID = rowsForName[0].tagID
			tc.CanDeleteProject = isMaintainer
			tc.CanUpdateColor = isMaintainer
		}
		out = append(out, tc)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// pickGroupedTagColor resolves the color for a grouped tag using deterministic,
// viewer-scoped precedence: (1) the viewer's preference on a row the viewer owns,
// (2) the viewer's preference on the lowest backing tag_id, (3) the board-scoped
// shared color (mixed claimed-board groups), (4) nil. rows must be ordered by tag_id.
func pickGroupedTagColor(rows []tagRowMeta, viewerUserID *int64) *string {
	if viewerUserID != nil {
		for _, r := range rows {
			if r.userID.Valid && r.userID.Int64 == *viewerUserID && r.viewerColor.Valid && r.viewerColor.String != "" {
				c := r.viewerColor.String
				return &c
			}
		}
	}
	for _, r := range rows {
		if r.viewerColor.Valid && r.viewerColor.String != "" {
			c := r.viewerColor.String
			return &c
		}
	}
	for _, r := range rows {
		if !r.userID.Valid && r.boardColor.Valid && r.boardColor.String != "" {
			c := r.boardColor.String
			return &c
		}
	}
	return nil
}

type TagWithColor struct {
	TagID     int64 // Authority and mutations are tag_id-based
	Name      string
	Color     *string // Hex color code, nil if no custom color
	CanDelete bool    // Computed per tag_id from role/ownership; never from name groups
}

// GetProjectScopedTagByID resolves a project-scoped tag by tag_id and project_id.
// It returns only tags with user_id IS NULL, so callers can safely distinguish
// project-scoped mutation targets from user-owned tags.
func (s *Store) GetProjectScopedTagByID(ctx context.Context, projectID, tagID int64) (TagWithColor, error) {
	var (
		twc   TagWithColor
		color sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
SELECT id, name, color
FROM tags
WHERE id = ? AND project_id = ? AND user_id IS NULL
`, tagID, projectID).Scan(&twc.TagID, &twc.Name, &color)
	if err == sql.ErrNoRows {
		return TagWithColor{}, fmt.Errorf("%w: tag not found", ErrNotFound)
	}
	if err != nil {
		return TagWithColor{}, fmt.Errorf("get project-scoped tag: %w", err)
	}
	if color.Valid && color.String != "" {
		twc.Color = &color.String
	}
	return twc, nil
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
// linkTemporaryBoard: true when the project has expires_at (any temporary / link board: unowned anonymous OR creator-owned FULL-mode temp).
// In that case, anonymous link holders may resolve board-scoped tag names without maintainer role.
// Durable projects require maintainer/admin for project-scoped tags when linkTemporaryBoard is false.
func (s *Store) ResolveTagForColorUpdate(ctx context.Context, projectID int64, viewerUserID *int64, tagName string, linkTemporaryBoard bool) (int64, error) {
	normalizedName := CanonicalizeTag(tagName)

	var boardTagID int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM tags 
		WHERE project_id = ? AND name = ? AND user_id IS NULL`,
		projectID, normalizedName).Scan(&boardTagID)
	if err == nil {
		if !linkTemporaryBoard {
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

// normalizeTagColor trims and validates a hex tag color. A nil, empty, or
// whitespace-only color is treated as a "clear" request and returns ("", nil).
// Invalid hex returns ErrValidation.
func normalizeTagColor(color *string) (string, error) {
	if color == nil {
		return "", nil
	}
	c := strings.TrimSpace(*color)
	if c == "" {
		return "", nil
	}
	if !colorHexRe.MatchString(c) {
		return "", fmt.Errorf("%w: invalid tag color %q", ErrValidation, *color)
	}
	return c, nil
}

// UpdateTagColorForDurableProjectByID updates a tag color addressed by tag_id on a
// durable project. Unlike UpdateTagColor it is project-aware:
//
//   - caller must be an authenticated project member (non-members → ErrNotFound);
//   - the tag must belong to the requested project (board-scoped project_id match, or
//     a user-owned row used by a todo in the project); otherwise ErrNotFound;
//   - board-scoped tags require Maintainer+ and update the shared tags.color;
//   - user-owned compatibility IDs let any member change only their own
//     user_tag_colors preference.
//
// Temporary boards must keep using UpdateTagColorForTemporaryBoard.
func (s *Store) UpdateTagColorForDurableProjectByID(ctx context.Context, projectID int64, viewerUserID int64, tagID int64, color *string) error {
	if err := s.requireGroupedTagAccess(ctx, projectID, viewerUserID); err != nil {
		return err
	}

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

	if tagProjectID.Valid && !tagUserID.Valid {
		if tagProjectID.Int64 != projectID {
			return fmt.Errorf("%w: tag not found", ErrNotFound)
		}
		role, err := s.GetProjectRole(ctx, projectID, viewerUserID)
		if err != nil {
			return err
		}
		if !role.HasMinimumRole(RoleMaintainer) {
			return fmt.Errorf("%w: project maintainer required", ErrUnauthorized)
		}
		return s.UpdateTagColor(ctx, &viewerUserID, tagID, color)
	}

	if tagUserID.Valid {
		var n int
		err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM todo_tags tt
INNER JOIN todos t ON t.id = tt.todo_id
WHERE tt.tag_id = ? AND t.project_id = ?`, tagID, projectID).Scan(&n)
		if err != nil {
			return fmt.Errorf("check tag on project: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("%w: tag not found", ErrNotFound)
		}
		// Preference write for this viewer only; UpdateTagColor never mutates another
		// user's user_tag_colors and does not touch tags.color for user-owned rows.
		return s.UpdateTagColor(ctx, &viewerUserID, tagID, color)
	}

	return fmt.Errorf("%w: tag has neither user_id nor project_id", ErrConflict)
}

// DeleteTagForDurableProjectByID deletes a tag addressed by tag_id on a durable project.
// Unlike DeleteTag it is project-aware:
//
//   - caller must be an authenticated project member (non-members → ErrNotFound);
//   - the tag must belong to the requested project (board-scoped project_id match, or
//     a user-owned row used by a todo in the project); otherwise ErrNotFound;
//   - board-scoped tags require Maintainer+ and are deleted for that project only;
//   - user-owned compatibility IDs require ownership by the caller (no maintainer
//     override on another member's personal tag) and return every project that
//     referenced the row so callers can refresh those boards.
//
// Temporary/anonymous boards must keep using DeleteTag.
func (s *Store) DeleteTagForDurableProjectByID(ctx context.Context, projectID, userID, tagID int64) ([]int64, error) {
	if err := s.requireGroupedTagAccess(ctx, projectID, userID); err != nil {
		return nil, err
	}

	var tagUserID sql.NullInt64
	var tagProjectID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
SELECT user_id, project_id FROM tags WHERE id = ?`, tagID).Scan(&tagUserID, &tagProjectID)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: tag not found", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get tag: %w", err)
	}

	if tagProjectID.Valid && !tagUserID.Valid {
		if tagProjectID.Int64 != projectID {
			return nil, fmt.Errorf("%w: tag not found", ErrNotFound)
		}
		role, err := s.GetProjectRole(ctx, projectID, userID)
		if err != nil {
			return nil, err
		}
		if !role.HasMinimumRole(RoleMaintainer) {
			return nil, fmt.Errorf("%w: project maintainer required", ErrUnauthorized)
		}
		if err := s.DeleteTag(ctx, userID, tagID, false); err != nil {
			return nil, err
		}
		return []int64{projectID}, nil
	}

	if tagUserID.Valid {
		if tagUserID.Int64 != userID {
			// Compatibility ID delete is owner-only; do not allow a maintainer to
			// remove another member's cross-project personal tag via this route.
			return nil, fmt.Errorf("%w: tag not found", ErrNotFound)
		}
		var n int
		err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM todo_tags tt
INNER JOIN todos t ON t.id = tt.todo_id
WHERE tt.tag_id = ? AND t.project_id = ?`, tagID, projectID).Scan(&n)
		if err != nil {
			return nil, fmt.Errorf("check tag on project: %w", err)
		}
		if n == 0 {
			return nil, fmt.Errorf("%w: tag not found", ErrNotFound)
		}
		return s.DeleteMyTagByID(ctx, userID, tagID)
	}

	return nil, fmt.Errorf("%w: tag has neither user_id nor project_id", ErrConflict)
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
		// Board-scoped tag: update tags.color directly (board-wide color).
		// viewerUserID is not used for this branch; the shared column is updated for all viewers.
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

// UpdateTagColorForTemporaryBoard updates tag color for boards with expires_at (link collaboration).
// Board-scoped tags: same as UpdateTagColor. User-owned tags used on this project: updates tags.color for shared
// display when the viewer is not the tag owner (e.g. anonymous visitor); tag owner still uses user_tag_colors via UpdateTagColor.
func (s *Store) UpdateTagColorForTemporaryBoard(ctx context.Context, projectID int64, viewerUserID *int64, tagID int64, color *string) error {
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

	if color != nil && *color != "" {
		colorTrimmed := strings.TrimSpace(*color)
		if !colorHexRe.MatchString(colorTrimmed) {
			return fmt.Errorf("%w: invalid tag color %q", ErrValidation, *color)
		}
		*color = colorTrimmed
	}

	// Board-scoped: must belong to this project
	if tagProjectID.Valid && !tagUserID.Valid {
		if tagProjectID.Int64 != projectID {
			return fmt.Errorf("%w: tag not found", ErrNotFound)
		}
		return s.UpdateTagColor(ctx, viewerUserID, tagID, color)
	}

	if tagUserID.Valid {
		if viewerUserID != nil && *viewerUserID == tagUserID.Int64 {
			return s.UpdateTagColor(ctx, viewerUserID, tagID, color)
		}
		var n int
		err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM todo_tags tt
INNER JOIN todos t ON t.id = tt.todo_id
WHERE tt.tag_id = ? AND t.project_id = ?`, tagID, projectID).Scan(&n)
		if err != nil {
			return fmt.Errorf("check tag on project: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("%w: tag not found", ErrNotFound)
		}
		if color == nil || *color == "" {
			_, err = s.db.ExecContext(ctx, `UPDATE tags SET color = NULL WHERE id = ?`, tagID)
		} else {
			_, err = s.db.ExecContext(ctx, `UPDATE tags SET color = ? WHERE id = ?`, *color, tagID)
		}
		if err != nil {
			return fmt.Errorf("update tag display color: %w", err)
		}
		return nil
	}

	return fmt.Errorf("%w: tag has neither user_id nor project_id", ErrConflict)
}

// UpdateTagColorForProject updates tag color, resolving the tag and enforcing ownership rules.
// linkTemporaryBoard: true when project has ExpiresAt set (any temporary board); allows name resolution for link holders.
func (s *Store) UpdateTagColorForProject(ctx context.Context, projectID int64, viewerUserID *int64, tagName string, color *string, linkTemporaryBoard bool) error {
	tagID, err := s.ResolveTagForColorUpdate(ctx, projectID, viewerUserID, tagName, linkTemporaryBoard)
	if err != nil {
		return err
	}
	if linkTemporaryBoard {
		return s.UpdateTagColorForTemporaryBoard(ctx, projectID, viewerUserID, tagID, color)
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

// personalTagRowsForName returns the user-owned tag row ids whose grouping key is
// groupKey and that are used by a todo in the project. This matches the read
// inclusion rule of the grouped listing, so name-based writes resolve exactly the rows
// behind the entry the viewer sees. Rows of any owner are returned (the viewer may hold
// a color preference on another member's backing row); ordered by id for determinism.
//
// The name comparison happens in Go, not in SQL: legacy rows can store non-canonical
// names, and a row named "make space" must resolve alongside "make-space" exactly as
// the grouped listing collapses them.
func (s *Store) personalTagRowsForName(ctx context.Context, projectID int64, groupKey string) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT g.id, g.name
FROM tags g
JOIN todo_tags tt ON tt.tag_id = g.id
JOIN todos t ON t.id = tt.todo_id AND t.project_id = ?
WHERE g.user_id IS NOT NULL
ORDER BY g.id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("resolve personal tag rows: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("scan tag id: %w", err)
		}
		if TagGroupKey(name) == groupKey {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// resolveTagFilterRowIDs returns the ids of every tag row used by a todo in the project
// that the board tag filter should select (filterKey already normalized by normalizeTagFilter).
//
// Durable projects resolve by TagGroupKey so the filter agrees with the grouped chip:
// clicking a "make-space" chip whose count spans a legacy "make space" row must return
// both todos. Temporary boards match the raw stored name exactly, because their chips
// are still one entry per tag row and normalizeTagFilter preserved that displayed label.
//
// The candidate set is restricted to rows actually used in the project, which is also
// what keeps the resulting IN list small. An empty result means the filter matches no
// row: callers must return an empty page rather than falling back to an unfiltered query.
func (s *Store) resolveTagFilterRowIDs(ctx context.Context, projectID int64, filterKey string, durable bool) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT g.id, g.name
FROM tags g
JOIN todo_tags tt ON tt.tag_id = g.id
JOIN todos t ON t.id = tt.todo_id AND t.project_id = ?
ORDER BY g.id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("resolve tag filter: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("scan tag filter row: %w", err)
		}
		match := name == filterKey
		if durable {
			match = TagGroupKey(name) == filterKey
		}
		if match {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows tag filter: %w", err)
	}
	return ids, nil
}

// tagFilterPlaceholders renders the placeholder list and bind args for a resolved
// tag-id filter. Callers must have checked for a non-empty id list.
func tagFilterPlaceholders(tagIDs []int64) (string, []any) {
	ph := make([]string, len(tagIDs))
	args := make([]any, len(tagIDs))
	for i, id := range tagIDs {
		ph[i] = "?"
		args[i] = id
	}
	return strings.Join(ph, ","), args
}

// requireGroupedTagAccess authorizes a name-based grouped tag write.
//
// Grouping is a durable-project projection, so temporary boards are rejected outright:
// they still list one entry per tag row and mutate through tag_id. On durable projects
// the caller must hold at least viewer access; non-members get ErrNotFound so project
// existence is not disclosed. Authentication alone is not sufficient, because resolving
// a grouped label reaches other members' backing rows.
func (s *Store) requireGroupedTagAccess(ctx context.Context, projectID int64, userID int64) error {
	p, err := s.getProject(ctx, projectID)
	if err != nil {
		return err
	}
	if p.ExpiresAt != nil {
		return fmt.Errorf("%w: name-based tag operations require a durable project", ErrValidation)
	}
	enabled, err := s.authEnabled(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	role, err := s.GetProjectRole(ctx, projectID, userID)
	if err != nil {
		return err
	}
	if !role.HasMinimumRole(RoleViewer) {
		return fmt.Errorf("%w: project not found", ErrNotFound)
	}
	return nil
}

// SetViewerTagColorByName sets or clears the current viewer's color preference for a
// grouped personal label. It resolves every user-owned backing row for the canonical
// name used by a todo in the project (the same rows the grouped listing shows) and
// upserts this viewer's user_tag_colors on each, so a viewer who owns no backing row
// can still set a color they will see. It never writes tags.color. Clearing when no
// preference exists is an idempotent success. Returns ErrNotFound when no personal
// backing rows resolve for the name in the project.
//
// Requires durable-project membership: see requireGroupedTagAccess.
func (s *Store) SetViewerTagColorByName(ctx context.Context, projectID int64, viewerUserID int64, name string, color *string) error {
	groupKey, err := tagWriteKey(name)
	if err != nil {
		return err
	}
	normColor, err := normalizeTagColor(color)
	if err != nil {
		return err
	}
	if err := s.requireGroupedTagAccess(ctx, projectID, viewerUserID); err != nil {
		return err
	}

	tagIDs, err := s.personalTagRowsForName(ctx, projectID, groupKey)
	if err != nil {
		return err
	}
	if len(tagIDs) == 0 {
		return fmt.Errorf("%w: tag not found", ErrNotFound)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, tagID := range tagIDs {
		if normColor == "" {
			if _, err := tx.ExecContext(ctx, `
DELETE FROM user_tag_colors WHERE user_id = ? AND tag_id = ?`, viewerUserID, tagID); err != nil {
				return fmt.Errorf("clear tag color: %w", err)
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO user_tag_colors(user_id, tag_id, color)
VALUES (?, ?, ?)
ON CONFLICT(user_id, tag_id) DO UPDATE SET color = excluded.color`, viewerUserID, tagID, normColor); err != nil {
			return fmt.Errorf("set tag color: %w", err)
		}
	}
	return tx.Commit()
}

// UpdateMyTagColor sets or clears the caller's color preference for a tag they own
// in their cross-project personal library. Ownership is required (404 otherwise).
// Clearing when no preference exists is an idempotent success.
func (s *Store) UpdateMyTagColor(ctx context.Context, userID, tagID int64, color *string) error {
	var ownerID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM tags WHERE id = ?`, tagID).Scan(&ownerID)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: tag not found", ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("get tag: %w", err)
	}
	if !ownerID.Valid || ownerID.Int64 != userID {
		return fmt.Errorf("%w: tag not found", ErrNotFound)
	}
	err = s.UpdateTagColor(ctx, &userID, tagID, color)
	if err != nil && isColorClear(color) && errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func isColorClear(color *string) bool {
	return color == nil || strings.TrimSpace(*color) == ""
}

// DeleteMyTagByID deletes a personal tag the caller owns and returns every project
// that referenced it (collected before deletion) so callers can refresh those boards.
// Strictly owner-only: unlike DeleteTag it never allows a maintainer override on
// someone else's row. Returns ErrNotFound when the tag is missing or not owned by
// the caller.
func (s *Store) DeleteMyTagByID(ctx context.Context, userID, tagID int64) ([]int64, error) {
	var ownerID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM tags WHERE id = ?`, tagID).Scan(&ownerID)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: tag not found", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get tag: %w", err)
	}
	if !ownerID.Valid || ownerID.Int64 != userID {
		return nil, fmt.Errorf("%w: tag not found", ErrNotFound)
	}

	affected, err := s.projectsWhereTagIsUsed(ctx, tagID)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM todo_tags WHERE tag_id = ?`, tagID); err != nil {
		return nil, fmt.Errorf("delete todo_tags: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE id = ? AND user_id = ?`, tagID, userID)
	if err != nil {
		return nil, fmt.Errorf("delete tag: %w", err)
	}
	affectedRows, _ := result.RowsAffected()
	if affectedRows == 0 {
		return nil, fmt.Errorf("%w: tag not found", ErrNotFound)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit delete: %w", err)
	}

	sort.Slice(affected, func(i, j int) bool { return affected[i] < affected[j] })
	return affected, nil
}

// DeleteMyTagByName deletes the caller's own personal tag row(s) for a canonical name
// used in the project. It never touches another member's row, so the grouped label
// may remain visible while other members still use the name. Because a personal row
// is shared across the caller's projects, the delete is global to that user; the
// returned project IDs are every project that referenced a deleted row (collected
// before deletion) so callers can refresh those boards. Returns ErrNotFound when the
// caller owns no matching backing row used in the project.
//
// Requires durable-project membership: see requireGroupedTagAccess.
func (s *Store) DeleteMyTagByName(ctx context.Context, projectID int64, userID int64, name string) ([]int64, error) {
	groupKey, err := tagWriteKey(name)
	if err != nil {
		return nil, err
	}
	if err := s.requireGroupedTagAccess(ctx, projectID, userID); err != nil {
		return nil, err
	}

	// Name matching happens in Go so a legacy non-canonical row ("make space") is
	// deleted by the same canonical label the grouped listing displays.
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT g.id, g.name
FROM tags g
JOIN todo_tags tt ON tt.tag_id = g.id
JOIN todos t ON t.id = tt.todo_id AND t.project_id = ?
WHERE g.user_id = ?
ORDER BY g.id`, projectID, userID)
	if err != nil {
		return nil, fmt.Errorf("resolve my tag rows: %w", err)
	}
	var tagIDs []int64
	for rows.Next() {
		var id int64
		var rowName string
		if err := rows.Scan(&id, &rowName); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan tag id: %w", err)
		}
		if TagGroupKey(rowName) == groupKey {
			tagIDs = append(tagIDs, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows my tag ids: %w", err)
	}
	if len(tagIDs) == 0 {
		return nil, fmt.Errorf("%w: tag not found", ErrNotFound)
	}

	// Collect affected project IDs BEFORE deleting so boards can be refreshed.
	affected := make(map[int64]struct{})
	for _, tagID := range tagIDs {
		pids, err := s.projectsWhereTagIsUsed(ctx, tagID)
		if err != nil {
			return nil, err
		}
		for _, pid := range pids {
			affected[pid] = struct{}{}
		}
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	for _, tagID := range tagIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM todo_tags WHERE tag_id = ?`, tagID); err != nil {
			return nil, fmt.Errorf("delete todo_tags: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE id = ? AND user_id = ?`, tagID, userID); err != nil {
			return nil, fmt.Errorf("delete tag: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit delete: %w", err)
	}

	ids := make([]int64, 0, len(affected))
	for pid := range affected {
		ids = append(ids, pid)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
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
			if err := rejectIfExpiredTemporaryProject(p); err != nil {
				return err
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
	return getOrCreateTagExact(ctx, tx, userID, normalizedName)
}

// getOrCreateTagExact gets or creates a user-owned tag under the exact given name.
// Callers must pass a name that has already been validated (e.g. via CanonicalizeTag).
func getOrCreateTagExact(ctx context.Context, tx *sql.Tx, userID int64, name string) (int64, error) {
	var tagID int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id = ? AND name = ?`, userID, name).Scan(&tagID)
	if err == sql.ErrNoRows {
		nowMs := time.Now().UTC().UnixMilli()
		res, err := tx.ExecContext(ctx, `
INSERT INTO tags(user_id, name, created_at)
VALUES (?, ?, ?)`, userID, name, nowMs)
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
