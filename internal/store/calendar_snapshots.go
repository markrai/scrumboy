package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	CalendarSnapshotStatusOK    = "ok"
	CalendarSnapshotStatusError = "error"
)

type CalendarFeedSnapshot struct {
	SourceID     int64
	ETag         string
	LastModified string
	FetchedAt    time.Time
	Status       string
	Error        string
	EventsJSON   string
}

func (s *Store) GetCalendarFeedSnapshot(ctx context.Context, sourceID int64) (CalendarFeedSnapshot, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT source_id, IFNULL(etag, ''), IFNULL(last_modified, ''), fetched_at, status, IFNULL(error, ''), events_json
FROM calendar_feed_snapshots
WHERE source_id = ?`, sourceID)
	return scanCalendarFeedSnapshot(row)
}

func (s *Store) ListCalendarFeedSnapshots(ctx context.Context, projectID int64) ([]CalendarFeedSnapshot, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT s.source_id, IFNULL(s.etag, ''), IFNULL(s.last_modified, ''), s.fetched_at, s.status, IFNULL(s.error, ''), s.events_json
FROM calendar_feed_snapshots s
INNER JOIN calendar_sources c ON c.id = s.source_id
WHERE c.project_id = ?
ORDER BY s.source_id ASC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list calendar snapshots: %w", err)
	}
	defer rows.Close()
	out := make([]CalendarFeedSnapshot, 0)
	for rows.Next() {
		snap, err := scanCalendarFeedSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list calendar snapshots: %w", err)
	}
	return out, nil
}

func (s *Store) UpsertCalendarFeedSnapshot(ctx context.Context, snap CalendarFeedSnapshot) error {
	if err := validateCalendarFeedSnapshot(snap); err != nil {
		return err
	}
	if snap.EventsJSON == "" {
		snap.EventsJSON = "[]"
	}
	fetchedAt := snap.FetchedAt.UTC().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO calendar_feed_snapshots(source_id, etag, last_modified, fetched_at, status, error, events_json)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(source_id) DO UPDATE SET
  etag = excluded.etag,
  last_modified = excluded.last_modified,
  fetched_at = excluded.fetched_at,
  status = excluded.status,
  error = excluded.error,
  events_json = excluded.events_json`,
		snap.SourceID, nullIfEmpty(snap.ETag), nullIfEmpty(snap.LastModified), fetchedAt, snap.Status, nullIfEmpty(snap.Error), snap.EventsJSON)
	if err != nil {
		return fmt.Errorf("upsert calendar snapshot: %w", err)
	}
	return nil
}

func (s *Store) UpsertCalendarFeedSnapshotIfCurrent(ctx context.Context, snap CalendarFeedSnapshot, urlHash, timezone string) error {
	if err := validateCalendarFeedSnapshot(snap); err != nil {
		return err
	}
	if snap.EventsJSON == "" {
		snap.EventsJSON = "[]"
	}
	fetchedAt := snap.FetchedAt.UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
INSERT INTO calendar_feed_snapshots(source_id, etag, last_modified, fetched_at, status, error, events_json)
SELECT ?, ?, ?, ?, ?, ?, ?
FROM calendar_sources c
INNER JOIN projects p ON p.id = c.project_id AND p.import_batch_id IS NULL
WHERE c.id = ? AND c.url_hash = ? AND p.agenda_timezone = ?
ON CONFLICT(source_id) DO UPDATE SET
  etag = excluded.etag,
  last_modified = excluded.last_modified,
  fetched_at = excluded.fetched_at,
  status = excluded.status,
  error = excluded.error,
  events_json = excluded.events_json
WHERE EXISTS (
  SELECT 1
  FROM calendar_sources c
  INNER JOIN projects p ON p.id = c.project_id AND p.import_batch_id IS NULL
  WHERE c.id = calendar_feed_snapshots.source_id
    AND c.url_hash = ?
    AND p.agenda_timezone = ?
)`,
		snap.SourceID, nullIfEmpty(snap.ETag), nullIfEmpty(snap.LastModified), fetchedAt, snap.Status, nullIfEmpty(snap.Error), snap.EventsJSON,
		snap.SourceID, urlHash, timezone, urlHash, timezone)
	if err != nil {
		return fmt.Errorf("upsert current calendar snapshot: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("upsert current calendar snapshot: %w", err)
	}
	if n == 0 {
		return ErrSnapshotSuperseded
	}
	return nil
}

func (s *Store) DeleteCalendarFeedSnapshot(ctx context.Context, sourceID int64) error {
	return deleteCalendarFeedSnapshotExec(ctx, s.db, sourceID)
}

func (s *Store) DeleteCalendarFeedSnapshotsForProject(ctx context.Context, projectID int64) error {
	return deleteCalendarFeedSnapshotsForProjectExec(ctx, s.db, projectID)
}

func deleteCalendarFeedSnapshotExec(ctx context.Context, exec sqlExecer, sourceID int64) error {
	if _, err := exec.ExecContext(ctx, `DELETE FROM calendar_feed_snapshots WHERE source_id = ?`, sourceID); err != nil {
		return fmt.Errorf("delete calendar snapshot: %w", err)
	}
	return nil
}

func deleteCalendarFeedSnapshotsForProjectExec(ctx context.Context, exec sqlExecer, projectID int64) error {
	if _, err := exec.ExecContext(ctx, `
DELETE FROM calendar_feed_snapshots
WHERE source_id IN (
  SELECT id FROM calendar_sources WHERE project_id = ?
)`, projectID); err != nil {
		return fmt.Errorf("delete project calendar snapshots: %w", err)
	}
	return nil
}

func (s *Store) TouchCalendarFeedSnapshot(ctx context.Context, sourceID int64, fetchedAt time.Time, etag, lastModified string) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE calendar_feed_snapshots
SET fetched_at = ?, etag = COALESCE(?, etag), last_modified = COALESCE(?, last_modified), status = 'ok', error = NULL
WHERE source_id = ?`,
		fetchedAt.UTC().UnixMilli(), nullIfEmpty(etag), nullIfEmpty(lastModified), sourceID)
	if err != nil {
		return fmt.Errorf("touch calendar snapshot: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("touch calendar snapshot: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) TouchCalendarFeedSnapshotIfCurrent(ctx context.Context, sourceID int64, fetchedAt time.Time, etag, lastModified, urlHash, timezone string) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE calendar_feed_snapshots
SET fetched_at = ?, etag = COALESCE(?, etag), last_modified = COALESCE(?, last_modified), status = 'ok', error = NULL
WHERE source_id = ?
  AND EXISTS (
    SELECT 1
    FROM calendar_sources c
    INNER JOIN projects p ON p.id = c.project_id AND p.import_batch_id IS NULL
    WHERE c.id = calendar_feed_snapshots.source_id
      AND c.url_hash = ?
      AND p.agenda_timezone = ?
  )`,
		fetchedAt.UTC().UnixMilli(), nullIfEmpty(etag), nullIfEmpty(lastModified), sourceID, urlHash, timezone)
	if err != nil {
		return fmt.Errorf("touch current calendar snapshot: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("touch current calendar snapshot: %w", err)
	}
	if n == 0 {
		return ErrSnapshotSuperseded
	}
	return nil
}

func validateCalendarFeedSnapshot(snap CalendarFeedSnapshot) error {
	if snap.Status != CalendarSnapshotStatusOK && snap.Status != CalendarSnapshotStatusError {
		return fmt.Errorf("%w: invalid calendar snapshot status", ErrValidation)
	}
	return nil
}

func scanCalendarFeedSnapshot(row calendarSourceRow) (CalendarFeedSnapshot, error) {
	var snap CalendarFeedSnapshot
	var fetchedAtMs int64
	if err := row.Scan(&snap.SourceID, &snap.ETag, &snap.LastModified, &fetchedAtMs, &snap.Status, &snap.Error, &snap.EventsJSON); err != nil {
		if err == sql.ErrNoRows {
			return CalendarFeedSnapshot{}, ErrNotFound
		}
		return CalendarFeedSnapshot{}, fmt.Errorf("scan calendar snapshot: %w", err)
	}
	snap.FetchedAt = time.UnixMilli(fetchedAtMs).UTC()
	return snap, nil
}
