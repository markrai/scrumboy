package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"scrumboy/internal/calendar/ics"
	"scrumboy/internal/store"
)

const (
	refreshReasonAgendaUpdated = "agenda_updated"
	snapshotFreshFor           = 15 * time.Minute
	snapshotStaleAfter         = 30 * time.Minute
	backgroundRefreshTimeout   = 30 * time.Second
)

type cachedEvent struct {
	UID      string    `json:"uid"`
	Title    string    `json:"title"`
	StartsAt time.Time `json:"startsAt"`
	EndsAt   time.Time `json:"endsAt"`
	AllDay   bool      `json:"allDay"`
	Location string    `json:"location"`
}

type AgendaEvent struct {
	ID           string
	SourceID     int64
	CalendarName string
	Title        string
	StartsAt     time.Time
	EndsAt       time.Time
	AllDay       bool
	Location     string
	Provider     string
	HostKind     string
}

type AgendaView struct {
	Enabled   bool
	Timezone  string
	Title     string
	Color     string
	Stale     bool
	FetchedAt *time.Time
	Error     string
	Events    []AgendaEvent
}

type SnapshotInvalidator interface {
	DeleteCalendarFeedSnapshot(ctx context.Context, sourceID int64) error
	DeleteCalendarFeedSnapshotsForProject(ctx context.Context, projectID int64) error
}

type SnapshotStore interface {
	SnapshotInvalidator
	GetCalendarFeedSnapshot(ctx context.Context, sourceID int64) (store.CalendarFeedSnapshot, error)
	ListCalendarFeedSnapshots(ctx context.Context, projectID int64) ([]store.CalendarFeedSnapshot, error)
	UpsertCalendarFeedSnapshot(ctx context.Context, snap store.CalendarFeedSnapshot) error
	UpsertCalendarFeedSnapshotIfCurrent(ctx context.Context, snap store.CalendarFeedSnapshot, urlHash, timezone string) error
	TouchCalendarFeedSnapshot(ctx context.Context, sourceID int64, fetchedAt time.Time, etag, lastModified string) error
	TouchCalendarFeedSnapshotIfCurrent(ctx context.Context, sourceID int64, fetchedAt time.Time, etag, lastModified, urlHash, timezone string) error
}

type AgendaServiceDependencies struct {
	Sources   SourceStore
	Snapshots SnapshotStore
	Cipher    SecretCipher
	Fetcher   FeedFetcher
	Refresh   BoardRefreshPublisher
	Now       func() time.Time
	Go        func(func())
}

type AgendaService struct {
	sources   SourceStore
	snapshots SnapshotStore
	cipher    SecretCipher
	fetcher   FeedFetcher
	refresh   BoardRefreshPublisher
	now       func() time.Time
	goFunc    func(func())

	flightMu sync.Mutex
	flights  map[int64]*refreshFlight
}

func NewAgendaService(deps AgendaServiceDependencies) *AgendaService {
	refresh := deps.Refresh
	if refresh == nil {
		refresh = nopBoardRefreshPublisher{}
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	goFunc := deps.Go
	if goFunc == nil {
		goFunc = func(fn func()) { go fn() }
	}
	return &AgendaService{
		sources:   deps.Sources,
		snapshots: deps.Snapshots,
		cipher:    deps.Cipher,
		fetcher:   deps.Fetcher,
		refresh:   refresh,
		now:       now,
		goFunc:    goFunc,
		flights:   make(map[int64]*refreshFlight),
	}
}

func (s *AgendaService) ReadAgenda(ctx context.Context, projectID int64) (AgendaView, error) {
	settings, err := s.sources.GetProjectAgendaSettings(ctx, projectID)
	if err != nil {
		return AgendaView{}, err
	}
	if !settings.Enabled {
		return AgendaView{Enabled: false}, nil
	}
	loc, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		return AgendaView{
			Enabled:  true,
			Timezone: settings.Timezone,
			Title:    settings.Title,
			Color:    settings.Color,
			Stale:    true,
			Error:    "unsupported calendar timezone",
			Events:   []AgendaEvent{},
		}, nil
	}
	sources, err := s.sources.ListCalendarSources(ctx, projectID)
	if err != nil {
		return AgendaView{}, err
	}
	snapshots, err := s.snapshots.ListCalendarFeedSnapshots(ctx, projectID)
	if err != nil {
		return AgendaView{}, err
	}
	snapBySource := make(map[int64]store.CalendarFeedSnapshot, len(snapshots))
	for _, snap := range snapshots {
		snapBySource[snap.SourceID] = snap
	}

	now := s.now().In(loc)
	todayStart, todayEnd := localDayBounds(now, loc)

	view := AgendaView{
		Enabled:  true,
		Timezone: settings.Timezone,
		Title:    settings.Title,
		Color:    settings.Color,
		Events:   []AgendaEvent{},
	}
	var latestFetched time.Time
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		snap, ok := snapBySource[src.ID]
		if !ok {
			view.Stale = true
			if view.Error == "" {
				view.Error = "calendar feed has not been fetched yet"
			}
			continue
		}
		if snap.Status == store.CalendarSnapshotStatusError {
			view.Stale = true
			if view.Error == "" && strings.TrimSpace(snap.Error) != "" {
				view.Error = snap.Error
			}
		} else if now.Sub(snap.FetchedAt) >= snapshotStaleAfter {
			view.Stale = true
		}
		if snap.FetchedAt.After(latestFetched) {
			latestFetched = snap.FetchedAt
		}
		cached, err := decodeCachedEvents(snap.EventsJSON)
		if err != nil {
			view.Stale = true
			if view.Error == "" {
				view.Error = "invalid calendar data"
			}
			continue
		}
		for _, ev := range cached {
			if !overlapsWindow(ev.StartsAt, ev.EndsAt, todayStart, todayEnd) {
				continue
			}
			view.Events = append(view.Events, AgendaEvent{
				ID:           agendaEventID(src.ID, ev),
				SourceID:     src.ID,
				CalendarName: src.Name,
				Title:        ev.Title,
				StartsAt:     ev.StartsAt.UTC(),
				EndsAt:       ev.EndsAt.UTC(),
				AllDay:       ev.AllDay,
				Location:     ev.Location,
				Provider:     SourceTypeICSFeed,
				HostKind:     persistedHostKind(src.HostKind),
			})
		}
	}
	if !latestFetched.IsZero() {
		fetched := latestFetched.UTC()
		view.FetchedAt = &fetched
	}
	sort.SliceStable(view.Events, func(i, j int) bool {
		if !view.Events[i].StartsAt.Equal(view.Events[j].StartsAt) {
			return view.Events[i].StartsAt.Before(view.Events[j].StartsAt)
		}
		if view.Events[i].Title != view.Events[j].Title {
			return view.Events[i].Title < view.Events[j].Title
		}
		return view.Events[i].ID < view.Events[j].ID
	})
	return view, nil
}

func (s *AgendaService) MaybeRefresh(ctx context.Context, projectID int64) {
	settings, err := s.sources.GetProjectAgendaSettings(ctx, projectID)
	if err != nil || !settings.Enabled {
		return
	}
	sources, err := s.sources.ListCalendarSources(ctx, projectID)
	if err != nil {
		return
	}
	snapshots, err := s.snapshots.ListCalendarFeedSnapshots(ctx, projectID)
	if err != nil {
		return
	}
	snapBySource := make(map[int64]store.CalendarFeedSnapshot, len(snapshots))
	for _, snap := range snapshots {
		snapBySource[snap.SourceID] = snap
	}
	now := s.now()
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		snap, ok := snapBySource[src.ID]
		if ok && now.Sub(snap.FetchedAt) < snapshotFreshFor {
			continue
		}
		s.enqueueRefresh(src, projectID)
	}
}

func (s *AgendaService) RefreshSource(ctx context.Context, projectID, sourceID int64) error {
	src, err := s.sources.GetCalendarSource(ctx, projectID, sourceID)
	if err != nil {
		return err
	}
	flight, leader := s.beginFlight(src.ID)
	if !leader {
		select {
		case <-flight.done:
			return flight.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	err = s.doRefresh(ctx, projectID, src)
	return s.finishRefresh(projectID, src.ID, flight, err)
}

func (s *AgendaService) enqueueRefresh(src store.CalendarSource, projectID int64) {
	flight, leader := s.beginFlight(src.ID)
	if !leader {
		return
	}
	s.goFunc(func() {
		ctx, cancel := context.WithTimeout(context.Background(), backgroundRefreshTimeout)
		defer cancel()
		err := s.doRefresh(ctx, projectID, src)
		_ = s.finishRefresh(projectID, src.ID, flight, err)
	})
}

func (s *AgendaService) finishRefresh(projectID, sourceID int64, flight *refreshFlight, err error) error {
	superseded := errors.Is(err, store.ErrSnapshotSuperseded)
	if superseded {
		err = nil
	}
	s.finishFlight(sourceID, flight, err)
	if superseded {
		s.enqueueCurrentSourceRefresh(projectID, sourceID)
	}
	return err
}

func (s *AgendaService) enqueueCurrentSourceRefresh(projectID, sourceID int64) {
	src, err := s.sources.GetCalendarSource(context.Background(), projectID, sourceID)
	if err != nil || !src.Enabled {
		return
	}
	settings, err := s.sources.GetProjectAgendaSettings(context.Background(), projectID)
	if err != nil || !settings.Enabled {
		return
	}
	s.enqueueRefresh(src, projectID)
}

func (s *AgendaService) doRefresh(ctx context.Context, projectID int64, src store.CalendarSource) error {
	settings, err := s.sources.GetProjectAgendaSettings(ctx, projectID)
	if err != nil {
		return err
	}
	expectedHash := src.URLHash
	expectedTZ := settings.Timezone
	loc, err := time.LoadLocation(expectedTZ)
	if err != nil {
		loc = time.UTC
	}

	existing, err := s.snapshots.GetCalendarFeedSnapshot(ctx, src.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	haveExisting := err == nil

	plain, err := s.cipher.DecryptSecret(src.SecretEnc)
	if err != nil {
		return s.failRefresh(ctx, src.ID, existing, haveExisting, err, expectedHash, expectedTZ)
	}
	kindChanged, err := s.backfillHostKind(ctx, src.ID, expectedHash, string(plain))
	if err != nil {
		return err
	}
	publishKindChange := func() {
		if kindChanged {
			s.refresh.PublishBoardRefresh(ctx, projectID, refreshReasonAgendaUpdated)
		}
	}
	if s.fetcher == nil {
		failErr := s.failRefresh(ctx, src.ID, existing, haveExisting, ErrFeedRequest, expectedHash, expectedTZ)
		publishKindChange()
		return failErr
	}
	fetched, err := s.fetcher.Fetch(ctx, FetchRequest{
		URL:          string(plain),
		ETag:         existing.ETag,
		LastModified: existing.LastModified,
	})
	now := s.now().UTC()
	if err != nil {
		failErr := s.failRefresh(ctx, src.ID, existing, haveExisting, err, expectedHash, expectedTZ)
		publishKindChange()
		return failErr
	}
	if fetched.NotModified {
		if !haveExisting {
			failErr := s.failRefresh(ctx, src.ID, existing, false, ErrFeedRequest, expectedHash, expectedTZ)
			publishKindChange()
			return failErr
		}
		if err := s.snapshots.TouchCalendarFeedSnapshotIfCurrent(ctx, src.ID, now, fetched.ETag, fetched.LastModified, expectedHash, expectedTZ); err != nil {
			return err
		}
		publishKindChange()
		return nil
	}

	windowStart, windowEnd := expansionWindow(s.now(), loc)
	expanded, err := ics.Expand(fetched.Body, loc, windowStart, windowEnd)
	if err != nil {
		failErr := s.failRefresh(ctx, src.ID, existing, haveExisting, err, expectedHash, expectedTZ)
		publishKindChange()
		return failErr
	}
	encoded, err := encodeCachedEvents(expanded)
	if err != nil {
		failErr := s.failRefresh(ctx, src.ID, existing, haveExisting, ics.ErrInvalidCalendar, expectedHash, expectedTZ)
		publishKindChange()
		return failErr
	}
	changed := !haveExisting || existing.EventsJSON != encoded
	etag := fetched.ETag
	lastModified := fetched.LastModified
	if etag == "" && haveExisting {
		etag = existing.ETag
	}
	if lastModified == "" && haveExisting {
		lastModified = existing.LastModified
	}
	if err := s.snapshots.UpsertCalendarFeedSnapshotIfCurrent(ctx, store.CalendarFeedSnapshot{
		SourceID:     src.ID,
		ETag:         etag,
		LastModified: lastModified,
		FetchedAt:    now,
		Status:       store.CalendarSnapshotStatusOK,
		EventsJSON:   encoded,
	}, expectedHash, expectedTZ); err != nil {
		return err
	}
	if changed || kindChanged {
		s.refresh.PublishBoardRefresh(ctx, projectID, refreshReasonAgendaUpdated)
	}
	return nil
}

func (s *AgendaService) backfillHostKind(ctx context.Context, sourceID int64, expectedHash, canonicalURL string) (bool, error) {
	kind := string(calendarHostKind(canonicalURL))
	return s.sources.UpdateCalendarSourceHostKindIfURLHashCurrent(ctx, sourceID, expectedHash, kind)
}

func persistedHostKind(raw string) string {
	switch strings.TrimSpace(raw) {
	case string(CalendarHostKindGoogle), string(CalendarHostKindApple), string(CalendarHostKindOther):
		return strings.TrimSpace(raw)
	default:
		return string(CalendarHostKindOther)
	}
}

func (s *AgendaService) failRefresh(ctx context.Context, sourceID int64, existing store.CalendarFeedSnapshot, haveExisting bool, cause error, urlHash, timezone string) error {
	if err := s.persistRefreshFailure(ctx, sourceID, existing, haveExisting, sanitizeCalendarError(cause), urlHash, timezone); err != nil {
		return err
	}
	return refreshOutcomeError(cause)
}

func (s *AgendaService) persistRefreshFailure(ctx context.Context, sourceID int64, existing store.CalendarFeedSnapshot, haveExisting bool, message, urlHash, timezone string) error {
	eventsJSON := "[]"
	etag := ""
	lastModified := ""
	if haveExisting {
		eventsJSON = existing.EventsJSON
		etag = existing.ETag
		lastModified = existing.LastModified
	}
	return s.snapshots.UpsertCalendarFeedSnapshotIfCurrent(ctx, store.CalendarFeedSnapshot{
		SourceID:     sourceID,
		ETag:         etag,
		LastModified: lastModified,
		FetchedAt:    s.now().UTC(),
		Status:       store.CalendarSnapshotStatusError,
		Error:        message,
		EventsJSON:   eventsJSON,
	}, urlHash, timezone)
}

type refreshFlight struct {
	done chan struct{}
	err  error
}

func (s *AgendaService) beginFlight(sourceID int64) (*refreshFlight, bool) {
	s.flightMu.Lock()
	defer s.flightMu.Unlock()
	if existing, ok := s.flights[sourceID]; ok {
		return existing, false
	}
	flight := &refreshFlight{done: make(chan struct{})}
	s.flights[sourceID] = flight
	return flight, true
}

func (s *AgendaService) finishFlight(sourceID int64, flight *refreshFlight, err error) {
	flight.err = err
	close(flight.done)
	s.flightMu.Lock()
	defer s.flightMu.Unlock()
	if s.flights[sourceID] == flight {
		delete(s.flights, sourceID)
	}
}

func localDayBounds(now time.Time, loc *time.Location) (time.Time, time.Time) {
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 0, 1)
}

func expansionWindow(now time.Time, loc *time.Location) (time.Time, time.Time) {
	today, _ := localDayBounds(now, loc)
	return today.AddDate(0, 0, -1), today.AddDate(0, 0, 2)
}

func encodeCachedEvents(events []ics.Event) (string, error) {
	out := make([]cachedEvent, 0, len(events))
	for _, ev := range events {
		out = append(out, cachedEvent{
			UID:      ev.UID,
			Title:    ev.Title,
			StartsAt: ev.StartsAt.UTC(),
			EndsAt:   ev.EndsAt.UTC(),
			AllDay:   ev.AllDay,
			Location: ev.Location,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].StartsAt.Equal(out[j].StartsAt) {
			return out[i].StartsAt.Before(out[j].StartsAt)
		}
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].UID < out[j].UID
	})
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeCachedEvents(raw string) ([]cachedEvent, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []cachedEvent{}, nil
	}
	var out []cachedEvent
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []cachedEvent{}
	}
	return out, nil
}

func agendaEventID(sourceID int64, ev cachedEvent) string {
	return fmt.Sprintf("%d:%s:%d", sourceID, ev.UID, ev.StartsAt.UTC().Unix())
}

func overlapsWindow(start, end, windowStart, windowEnd time.Time) bool {
	if !end.After(start) {
		end = start.Add(time.Nanosecond)
	}
	return start.Before(windowEnd) && end.After(windowStart)
}

func sanitizeCalendarError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrFeedBlocked):
		return ErrFeedBlocked.Error()
	case errors.Is(err, ErrFeedTooLarge), errors.Is(err, ics.ErrTooLarge):
		return ErrFeedTooLarge.Error()
	case errors.Is(err, ics.ErrTooManyOccurrences):
		return ics.ErrTooManyOccurrences.Error()
	case errors.Is(err, ErrFeedTimeout):
		return ErrFeedTimeout.Error()
	case errors.Is(err, ics.ErrUnknownTimezone):
		return ics.ErrUnknownTimezone.Error()
	case errors.Is(err, ics.ErrInvalidCalendar):
		return ics.ErrInvalidCalendar.Error()
	case errors.Is(err, store.ErrEncryptionNotConfigured):
		return "calendar feeds are not configured"
	default:
		return ErrFeedRequest.Error()
	}
}

func refreshOutcomeError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrFeedBlocked):
		return ErrFeedBlocked
	case errors.Is(err, ErrFeedTooLarge), errors.Is(err, ics.ErrTooLarge):
		return ErrFeedTooLarge
	case errors.Is(err, ics.ErrTooManyOccurrences):
		return ics.ErrTooManyOccurrences
	case errors.Is(err, ErrFeedTimeout):
		return ErrFeedTimeout
	case errors.Is(err, ics.ErrUnknownTimezone):
		return ics.ErrUnknownTimezone
	case errors.Is(err, ics.ErrInvalidCalendar):
		return ics.ErrInvalidCalendar
	case errors.Is(err, store.ErrEncryptionNotConfigured):
		return store.ErrEncryptionNotConfigured
	default:
		return ErrFeedRequest
	}
}
