package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	CalendarSourceTypeICSFeed = "ics_feed"
	CalendarHostKindGoogle    = "google"
	CalendarHostKindApple     = "apple"
	CalendarHostKindOther     = "other"
	MaxCalendarSources        = 8
	maxCalendarSourceNameLen  = 200
	maxAgendaTitleLen         = 200
	DefaultAgendaTitle        = "Agenda"
	DefaultAgendaColor        = "#6366F1"
)

const (
	ReasonInvalidCalendarSourceName = "invalid_calendar_source_name"
	ReasonInvalidCalendarSourceType = "invalid_calendar_source_type"
	ReasonCalendarSourceLimit       = "calendar_source_limit_reached"
	ReasonInvalidAgendaTimezone     = "invalid_agenda_timezone"
	ReasonInvalidAgendaTitle        = "invalid_agenda_title"
	ReasonInvalidAgendaColor        = "invalid_agenda_color"
)

// CalendarSource is a persisted ICS feed configuration. SecretEnc is ciphertext
// and must never be logged or exported.
type CalendarSource struct {
	ID        int64
	ProjectID int64
	Type      string
	Name      string
	Enabled   bool
	SecretEnc string
	URLHash   string
	HostKind  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ProjectAgendaSettings is the project-owned Agenda configuration.
type ProjectAgendaSettings struct {
	Enabled  bool
	Timezone string
	Title    string
	Color    string
}

type CreateCalendarSourceInput struct {
	Type      string
	Name      string
	Enabled   bool
	SecretEnc string
	URLHash   string
	HostKind  string
}

type UpdateCalendarSourceInput struct {
	Name      *string
	Enabled   *bool
	SecretEnc *string
	URLHash   *string
	HostKind  *string
}

func (s *Store) GetProjectAgendaSettings(ctx context.Context, projectID int64) (ProjectAgendaSettings, error) {
	return getProjectAgendaSettingsQueryer(ctx, s.db, projectID)
}

func getProjectAgendaSettingsQueryer(ctx context.Context, q sqlRowQueryer, projectID int64) (ProjectAgendaSettings, error) {
	var enabledInt int
	var timezone, title, color string
	err := q.QueryRowContext(ctx, `
SELECT agenda_enabled, agenda_timezone, agenda_title, agenda_color
FROM projects
WHERE id = ? AND import_batch_id IS NULL`, projectID).Scan(&enabledInt, &timezone, &title, &color)
	if err != nil {
		if err == sql.ErrNoRows {
			return ProjectAgendaSettings{}, ErrNotFound
		}
		return ProjectAgendaSettings{}, fmt.Errorf("get project agenda settings: %w", err)
	}
	return ProjectAgendaSettings{
		Enabled:  enabledInt == 1,
		Timezone: timezone,
		Title:    normalizeAgendaTitle(title),
		Color:    normalizeAgendaColor(color),
	}, nil
}

func (s *Store) UpdateProjectAgendaSettings(ctx context.Context, projectID int64, enabled *bool, timezone *string, title *string, color *string) (ProjectAgendaSettings, error) {
	if enabled == nil && timezone == nil && title == nil && color == nil {
		return s.GetProjectAgendaSettings(ctx, projectID)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return ProjectAgendaSettings{}, fmt.Errorf("begin update project agenda settings: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := serializeProjectWriteTx(ctx, tx, projectID); err != nil {
		return ProjectAgendaSettings{}, err
	}
	updated, err := applyProjectAgendaSettingsTx(ctx, tx, projectID, enabled, timezone, title, color)
	if err != nil {
		return ProjectAgendaSettings{}, err
	}
	if err := tx.Commit(); err != nil {
		return ProjectAgendaSettings{}, fmt.Errorf("commit update project agenda settings: %w", err)
	}
	return updated, nil
}

func applyProjectAgendaSettingsTx(ctx context.Context, tx *sql.Tx, projectID int64, enabled *bool, timezone *string, title *string, color *string) (ProjectAgendaSettings, error) {
	if enabled == nil && timezone == nil && title == nil && color == nil {
		return getProjectAgendaSettingsQueryer(ctx, tx, projectID)
	}
	existing, err := getProjectAgendaSettingsQueryer(ctx, tx, projectID)
	if err != nil {
		return ProjectAgendaSettings{}, err
	}
	enabledVal := existing.Enabled
	if enabled != nil {
		enabledVal = *enabled
	}
	tzVal := existing.Timezone
	if timezone != nil {
		tz, err := validateAgendaTimezone(*timezone)
		if err != nil {
			return ProjectAgendaSettings{}, err
		}
		tzVal = tz
	}
	titleVal := existing.Title
	if title != nil {
		normalized, err := validateAgendaTitle(*title)
		if err != nil {
			return ProjectAgendaSettings{}, err
		}
		titleVal = normalized
	}
	colorVal := existing.Color
	if color != nil {
		normalized, err := validateAgendaColor(*color)
		if err != nil {
			return ProjectAgendaSettings{}, err
		}
		colorVal = normalized
	}

	nowMs := time.Now().UTC().UnixMilli()
	_, err = tx.ExecContext(ctx, `
UPDATE projects
SET agenda_enabled = ?, agenda_timezone = ?, agenda_title = ?, agenda_color = ?, updated_at = ?
WHERE id = ? AND import_batch_id IS NULL`, boolToInt(enabledVal), tzVal, titleVal, colorVal, nowMs, projectID)
	if err != nil {
		return ProjectAgendaSettings{}, fmt.Errorf("update project agenda settings: %w", err)
	}
	if timezone != nil && existing.Timezone != tzVal {
		if err := deleteCalendarFeedSnapshotsForProjectExec(ctx, tx, projectID); err != nil {
			return ProjectAgendaSettings{}, err
		}
	}
	return getProjectAgendaSettingsQueryer(ctx, tx, projectID)
}

func validateAgendaTimezone(raw string) (string, error) {
	tz := strings.TrimSpace(raw)
	if tz == "" {
		return "", priorityError(ErrValidation, ReasonInvalidAgendaTimezone, "invalid agenda timezone")
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return "", priorityError(ErrValidation, ReasonInvalidAgendaTimezone, "invalid agenda timezone")
	}
	return tz, nil
}

func validateAgendaTitle(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || len(name) > maxAgendaTitleLen {
		return "", priorityError(ErrValidation, ReasonInvalidAgendaTitle, "invalid agenda title")
	}
	return name, nil
}

func normalizeAgendaTitle(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		return DefaultAgendaTitle
	}
	return name
}

func validateAgendaColor(raw string) (string, error) {
	color := strings.TrimSpace(raw)
	if !ValidWorkflowColumnColor(color) {
		return "", priorityError(ErrValidation, ReasonInvalidAgendaColor, "invalid agenda color")
	}
	return color, nil
}

func normalizeAgendaColor(raw string) string {
	color := strings.TrimSpace(raw)
	if !ValidWorkflowColumnColor(color) {
		return DefaultAgendaColor
	}
	return color
}

func (s *Store) ListCalendarSources(ctx context.Context, projectID int64) ([]CalendarSource, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, project_id, type, name, enabled, secret_enc, url_hash, host_kind, created_at, updated_at
FROM calendar_sources
WHERE project_id = ?
ORDER BY id ASC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list calendar sources: %w", err)
	}
	defer rows.Close()

	out := make([]CalendarSource, 0)
	for rows.Next() {
		src, err := scanCalendarSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list calendar sources: %w", err)
	}
	return out, nil
}

func (s *Store) CountCalendarSources(ctx context.Context, projectID int64) (int, error) {
	return countCalendarSourcesQueryer(ctx, s.db, projectID)
}

func countCalendarSourcesQueryer(ctx context.Context, q sqlRowQueryer, projectID int64) (int, error) {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM calendar_sources WHERE project_id = ?`, projectID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count calendar sources: %w", err)
	}
	return n, nil
}

func (s *Store) GetCalendarSource(ctx context.Context, projectID, sourceID int64) (CalendarSource, error) {
	return getCalendarSourceQueryer(ctx, s.db, projectID, sourceID)
}

func getCalendarSourceQueryer(ctx context.Context, q sqlRowQueryer, projectID, sourceID int64) (CalendarSource, error) {
	row := q.QueryRowContext(ctx, `
SELECT id, project_id, type, name, enabled, secret_enc, url_hash, host_kind, created_at, updated_at
FROM calendar_sources
WHERE id = ? AND project_id = ?`, sourceID, projectID)
	src, err := scanCalendarSource(row)
	if err != nil {
		return CalendarSource{}, err
	}
	return src, nil
}

func (s *Store) CreateCalendarSource(ctx context.Context, projectID int64, in CreateCalendarSourceInput) (CalendarSource, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > maxCalendarSourceNameLen {
		return CalendarSource{}, priorityError(ErrValidation, ReasonInvalidCalendarSourceName, "invalid calendar source name")
	}
	srcType := strings.TrimSpace(in.Type)
	if srcType == "" {
		srcType = CalendarSourceTypeICSFeed
	}
	if srcType != CalendarSourceTypeICSFeed {
		return CalendarSource{}, priorityError(ErrValidation, ReasonInvalidCalendarSourceType, "invalid calendar source type")
	}
	if strings.TrimSpace(in.SecretEnc) == "" || strings.TrimSpace(in.URLHash) == "" {
		return CalendarSource{}, fmt.Errorf("%w: calendar source secret required", ErrValidation)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return CalendarSource{}, fmt.Errorf("begin create calendar source: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := serializeProjectWriteTx(ctx, tx, projectID); err != nil {
		return CalendarSource{}, err
	}
	count, err := countCalendarSourcesQueryer(ctx, tx, projectID)
	if err != nil {
		return CalendarSource{}, err
	}
	if count >= MaxCalendarSources {
		return CalendarSource{}, priorityError(ErrValidation, ReasonCalendarSourceLimit, "calendar source limit reached")
	}

	nowMs := time.Now().UTC().UnixMilli()
	res, err := tx.ExecContext(ctx, `
INSERT INTO calendar_sources(project_id, type, name, enabled, secret_enc, url_hash, host_kind, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		projectID, srcType, name, boolToInt(in.Enabled), in.SecretEnc, in.URLHash, normalizeCalendarHostKind(in.HostKind), nowMs, nowMs)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return CalendarSource{}, fmt.Errorf("%w: calendar source already exists", ErrConflict)
		}
		return CalendarSource{}, fmt.Errorf("create calendar source: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return CalendarSource{}, fmt.Errorf("calendar source id: %w", err)
	}
	src, err := getCalendarSourceQueryer(ctx, tx, projectID, id)
	if err != nil {
		return CalendarSource{}, err
	}
	if err := tx.Commit(); err != nil {
		return CalendarSource{}, fmt.Errorf("commit create calendar source: %w", err)
	}
	return src, nil
}

func (s *Store) UpdateCalendarSource(ctx context.Context, projectID, sourceID int64, in UpdateCalendarSourceInput) (CalendarSource, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return CalendarSource{}, fmt.Errorf("begin update calendar source: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := serializeProjectWriteTx(ctx, tx, projectID); err != nil {
		return CalendarSource{}, err
	}
	existing, err := getCalendarSourceQueryer(ctx, tx, projectID, sourceID)
	if err != nil {
		return CalendarSource{}, err
	}
	name := existing.Name
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
		if name == "" || len(name) > maxCalendarSourceNameLen {
			return CalendarSource{}, priorityError(ErrValidation, ReasonInvalidCalendarSourceName, "invalid calendar source name")
		}
	}
	enabled := existing.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	secretEnc := existing.SecretEnc
	urlHash := existing.URLHash
	hostKind := existing.HostKind
	if in.SecretEnc != nil || in.URLHash != nil {
		if in.SecretEnc == nil || in.URLHash == nil || strings.TrimSpace(*in.SecretEnc) == "" || strings.TrimSpace(*in.URLHash) == "" {
			return CalendarSource{}, fmt.Errorf("%w: calendar source secret required", ErrValidation)
		}
		secretEnc = *in.SecretEnc
		urlHash = *in.URLHash
	}
	if in.HostKind != nil {
		hostKind = *in.HostKind
	}

	nowMs := time.Now().UTC().UnixMilli()
	_, err = tx.ExecContext(ctx, `
UPDATE calendar_sources
SET name = ?, enabled = ?, secret_enc = ?, url_hash = ?, host_kind = ?, updated_at = ?
WHERE id = ? AND project_id = ?`,
		name, boolToInt(enabled), secretEnc, urlHash, normalizeCalendarHostKind(hostKind), nowMs, sourceID, projectID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return CalendarSource{}, fmt.Errorf("%w: calendar source already exists", ErrConflict)
		}
		return CalendarSource{}, fmt.Errorf("update calendar source: %w", err)
	}
	if existing.URLHash != urlHash {
		if err := deleteCalendarFeedSnapshotExec(ctx, tx, sourceID); err != nil {
			return CalendarSource{}, err
		}
	}
	updated, err := getCalendarSourceQueryer(ctx, tx, projectID, sourceID)
	if err != nil {
		return CalendarSource{}, err
	}
	if err := tx.Commit(); err != nil {
		return CalendarSource{}, fmt.Errorf("commit update calendar source: %w", err)
	}
	return updated, nil
}

func (s *Store) DeleteCalendarSource(ctx context.Context, projectID, sourceID int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM calendar_sources WHERE id = ? AND project_id = ?`, sourceID, projectID)
	if err != nil {
		return fmt.Errorf("delete calendar source: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete calendar source: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdateCalendarSourceHostKindIfURLHashCurrent(ctx context.Context, sourceID int64, expectedURLHash, hostKind string) (bool, error) {
	hash := strings.TrimSpace(expectedURLHash)
	if hash == "" {
		return false, nil
	}
	kind := normalizeCalendarHostKind(hostKind)
	res, err := s.db.ExecContext(ctx, `
UPDATE calendar_sources
SET host_kind = ?
WHERE id = ? AND url_hash = ? AND host_kind <> ?`,
		kind, sourceID, hash, kind)
	if err != nil {
		return false, fmt.Errorf("update calendar source host kind: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("update calendar source host kind rows: %w", err)
	}
	return n == 1, nil
}

type calendarSourceRow interface {
	Scan(dest ...any) error
}

func scanCalendarSource(row calendarSourceRow) (CalendarSource, error) {
	var src CalendarSource
	var enabledInt int
	var createdAtMs, updatedAtMs int64
	if err := row.Scan(&src.ID, &src.ProjectID, &src.Type, &src.Name, &enabledInt, &src.SecretEnc, &src.URLHash, &src.HostKind, &createdAtMs, &updatedAtMs); err != nil {
		if err == sql.ErrNoRows {
			return CalendarSource{}, ErrNotFound
		}
		return CalendarSource{}, fmt.Errorf("scan calendar source: %w", err)
	}
	src.Enabled = enabledInt == 1
	src.HostKind = normalizeCalendarHostKind(src.HostKind)
	src.CreatedAt = time.UnixMilli(createdAtMs).UTC()
	src.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
	return src, nil
}

func normalizeCalendarHostKind(raw string) string {
	switch strings.TrimSpace(raw) {
	case CalendarHostKindGoogle, CalendarHostKindApple, CalendarHostKindOther:
		return strings.TrimSpace(raw)
	default:
		return CalendarHostKindOther
	}
}
