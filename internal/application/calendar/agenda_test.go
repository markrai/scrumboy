package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/calendar/ics"
	"scrumboy/internal/store"
)

type snapshotStoreFake struct {
	mu      sync.Mutex
	snaps   map[int64]store.CalendarFeedSnapshot
	sources *calendarSourceStoreFake
}

func (f *snapshotStoreFake) GetCalendarFeedSnapshot(_ context.Context, sourceID int64) (store.CalendarFeedSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	snap, ok := f.snaps[sourceID]
	if !ok {
		return store.CalendarFeedSnapshot{}, store.ErrNotFound
	}
	return snap, nil
}

func (f *snapshotStoreFake) ListCalendarFeedSnapshots(context.Context, int64) ([]store.CalendarFeedSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.CalendarFeedSnapshot, 0, len(f.snaps))
	for _, snap := range f.snaps {
		out = append(out, snap)
	}
	return out, nil
}

func (f *snapshotStoreFake) UpsertCalendarFeedSnapshot(_ context.Context, snap store.CalendarFeedSnapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snaps == nil {
		f.snaps = map[int64]store.CalendarFeedSnapshot{}
	}
	f.snaps[snap.SourceID] = snap
	return nil
}

func (f *snapshotStoreFake) UpsertCalendarFeedSnapshotIfCurrent(_ context.Context, snap store.CalendarFeedSnapshot, urlHash, timezone string) error {
	if !f.configMatches(snap.SourceID, urlHash, timezone) {
		return store.ErrSnapshotSuperseded
	}
	return f.UpsertCalendarFeedSnapshot(context.Background(), snap)
}

func (f *snapshotStoreFake) TouchCalendarFeedSnapshot(_ context.Context, sourceID int64, fetchedAt time.Time, etag, lastModified string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	snap, ok := f.snaps[sourceID]
	if !ok {
		return store.ErrNotFound
	}
	snap.FetchedAt = fetchedAt
	if etag != "" {
		snap.ETag = etag
	}
	if lastModified != "" {
		snap.LastModified = lastModified
	}
	snap.Status = store.CalendarSnapshotStatusOK
	snap.Error = ""
	f.snaps[sourceID] = snap
	return nil
}

func (f *snapshotStoreFake) TouchCalendarFeedSnapshotIfCurrent(_ context.Context, sourceID int64, fetchedAt time.Time, etag, lastModified, urlHash, timezone string) error {
	if !f.configMatches(sourceID, urlHash, timezone) {
		return store.ErrSnapshotSuperseded
	}
	err := f.TouchCalendarFeedSnapshot(context.Background(), sourceID, fetchedAt, etag, lastModified)
	if errors.Is(err, store.ErrNotFound) {
		return store.ErrSnapshotSuperseded
	}
	return err
}

func (f *snapshotStoreFake) configMatches(sourceID int64, urlHash, timezone string) bool {
	if f.sources == nil {
		return true
	}
	settings, err := f.sources.GetProjectAgendaSettings(context.Background(), 0)
	if err != nil {
		return false
	}
	src, err := f.sources.GetCalendarSource(context.Background(), 0, sourceID)
	if err != nil {
		return false
	}
	tz := settings.Timezone
	if tz == "" {
		tz = "UTC"
	}
	return src.URLHash == urlHash && tz == timezone
}

func (f *snapshotStoreFake) DeleteCalendarFeedSnapshot(_ context.Context, sourceID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.snaps, sourceID)
	return nil
}

func (f *snapshotStoreFake) DeleteCalendarFeedSnapshotsForProject(context.Context, int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snaps = map[int64]store.CalendarFeedSnapshot{}
	return nil
}

func (f *snapshotStoreFake) lookup(sourceID int64) (store.CalendarFeedSnapshot, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	snap, ok := f.snaps[sourceID]
	return snap, ok
}

type countingFetcher struct {
	mu               sync.Mutex
	calls            int
	resp             FetchResponse
	err              error
	lastURL          string
	lastETag         string
	lastLastModified string
	notModified      bool
}

func (f *countingFetcher) Fetch(_ context.Context, req FetchRequest) (FetchResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastURL = req.URL
	f.lastETag = req.ETag
	f.lastLastModified = req.LastModified
	if f.err != nil {
		return FetchResponse{}, f.err
	}
	if f.notModified {
		return FetchResponse{NotModified: true, ETag: req.ETag}, nil
	}
	return f.resp, nil
}

func (f *countingFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *countingFetcher) lastValidators() (etag, lastModified string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastETag, f.lastLastModified
}

func testICSForDay(day time.Time) []byte {
	stamp := day.UTC().Format("20060102") + "T150000Z"
	end := day.UTC().Format("20060102") + "T160000Z"
	return []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:pickup\r\nDTSTART:" + stamp + "\r\nDTEND:" + end + "\r\nSUMMARY:Pickup\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
}

func testICSUnknownTZ(day time.Time) []byte {
	stamp := day.Format("20060102") + "T150000"
	return []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:bad\r\nDTSTART;TZID=Not/A_Zone:" + stamp + "\r\nDTEND;TZID=Not/A_Zone:" + stamp + "\r\nSUMMARY:Nope\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
}

func newAgendaTestService(t *testing.T, now time.Time, sources *calendarSourceStoreFake, snaps *snapshotStoreFake, fetcher *countingFetcher, refresh *calendarRefreshFake) *AgendaService {
	t.Helper()
	if sources.settings.Timezone == "" {
		sources.settings.Timezone = "UTC"
	}
	snaps.sources = sources
	return NewAgendaService(AgendaServiceDependencies{
		Sources:   sources,
		Snapshots: snaps,
		Cipher:    &calendarCipherFake{dec: []byte("https://calendar.example.com/private/token.ics")},
		Fetcher:   fetcher,
		Refresh:   refresh,
		Now:       func() time.Time { return now },
		Go:        func(fn func()) { fn() },
	})
}

func TestReadAgendaDoesNotFetch(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	fetcher := &countingFetcher{resp: FetchResponse{Body: testICSForDay(now)}}
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "UTC"},
		sources: []store.CalendarSource{{
			ID:        3,
			Name:      "Family",
			Enabled:   true,
			SecretEnc: "v1:ciphertext",
			Type:      SourceTypeICSFeed,
		}},
	}
	snaps := &snapshotStoreFake{snaps: map[int64]store.CalendarFeedSnapshot{
		3: {
			SourceID:   3,
			FetchedAt:  now.Add(-time.Minute),
			Status:     store.CalendarSnapshotStatusOK,
			EventsJSON: `[{"uid":"pickup","title":"Pickup","startsAt":"2026-08-17T15:00:00Z","endsAt":"2026-08-17T16:00:00Z","allDay":false,"location":""}]`,
		},
	}}
	svc := newAgendaTestService(t, now, sources, snaps, fetcher, &calendarRefreshFake{})
	view, err := svc.ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda: %v", err)
	}
	if fetcher.callCount() != 0 {
		t.Fatalf("fetch calls=%d, want 0", fetcher.callCount())
	}
	if !view.Enabled || len(view.Events) != 1 || view.Events[0].Title != "Pickup" || view.Events[0].Provider != SourceTypeICSFeed {
		t.Fatalf("view=%+v", view)
	}
	if view.Events[0].ID != "3:pickup:1786986000" && !strings.HasPrefix(view.Events[0].ID, "3:pickup:") {
		t.Fatalf("id=%q", view.Events[0].ID)
	}
}

func TestReadAgendaDisabledOmitsEvents(t *testing.T) {
	svc := newAgendaTestService(t, time.Now(), &calendarSourceStoreFake{}, &snapshotStoreFake{}, &countingFetcher{}, &calendarRefreshFake{})
	view, err := svc.ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda: %v", err)
	}
	if view.Enabled || len(view.Events) != 0 {
		t.Fatalf("view=%+v", view)
	}
}

func TestMaybeRefreshSkipsFreshSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	fetcher := &countingFetcher{resp: FetchResponse{Body: testICSForDay(now)}}
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "UTC"},
		sources:  []store.CalendarSource{{ID: 3, Enabled: true, SecretEnc: "v1:x"}},
	}
	snaps := &snapshotStoreFake{snaps: map[int64]store.CalendarFeedSnapshot{
		3: {SourceID: 3, FetchedAt: now.Add(-time.Minute), Status: store.CalendarSnapshotStatusOK, EventsJSON: "[]"},
	}}
	svc := newAgendaTestService(t, now, sources, snaps, fetcher, &calendarRefreshFake{})
	svc.MaybeRefresh(context.Background(), 9)
	if fetcher.callCount() != 0 {
		t.Fatalf("fetch calls=%d, want 0", fetcher.callCount())
	}
}

func TestRefreshPublishesOnceWhenEventsChange(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	fetcher := &countingFetcher{resp: FetchResponse{Body: testICSForDay(now), ETag: `"v2"`}}
	refresh := &calendarRefreshFake{}
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "UTC"},
		sources:  []store.CalendarSource{{ID: 3, Name: "Family", Enabled: true, SecretEnc: "v1:x"}},
	}
	snaps := &snapshotStoreFake{}
	svc := newAgendaTestService(t, now, sources, snaps, fetcher, refresh)
	if err := svc.RefreshSource(context.Background(), 9, 3); err != nil {
		t.Fatalf("RefreshSource: %v", err)
	}
	if refresh.calls != 1 {
		t.Fatalf("publish calls=%d, want 1", refresh.calls)
	}
	if refresh.lastReason != refreshReasonAgendaUpdated {
		t.Fatalf("reason=%q", refresh.lastReason)
	}
	if fetcher.lastURL != "https://calendar.example.com/private/token.ics" {
		t.Fatalf("fetcher URL=%q", fetcher.lastURL)
	}
	view, err := svc.ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda: %v", err)
	}
	if len(view.Events) != 1 || view.Events[0].Title != "Pickup" || view.Stale {
		t.Fatalf("view=%+v", view)
	}
}

func TestRefreshNotModifiedDoesNotPublish(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	fetcher := &countingFetcher{notModified: true}
	refresh := &calendarRefreshFake{}
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "UTC"},
		sources:  []store.CalendarSource{{ID: 3, Enabled: true, SecretEnc: "v1:x"}},
	}
	snaps := &snapshotStoreFake{snaps: map[int64]store.CalendarFeedSnapshot{
		3: {SourceID: 3, ETag: `"v1"`, FetchedAt: now.Add(-time.Hour), Status: store.CalendarSnapshotStatusOK, EventsJSON: "[]"},
	}}
	svc := newAgendaTestService(t, now, sources, snaps, fetcher, refresh)
	if err := svc.RefreshSource(context.Background(), 9, 3); err != nil {
		t.Fatalf("RefreshSource: %v", err)
	}
	if refresh.calls != 0 {
		t.Fatalf("publish calls=%d, want 0", refresh.calls)
	}
	if snaps.snaps[3].FetchedAt.Equal(now.Add(-time.Hour)) {
		t.Fatal("fetched_at was not bumped on 304")
	}
}

func TestUnknownTimezoneKeepsLastGoodAndDoesNotPublish(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	lastGood := `[{"uid":"pickup","title":"Pickup","startsAt":"2026-08-17T15:00:00Z","endsAt":"2026-08-17T16:00:00Z","allDay":false,"location":""}]`
	fetcher := &countingFetcher{resp: FetchResponse{Body: testICSUnknownTZ(now)}}
	refresh := &calendarRefreshFake{}
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "UTC"},
		sources:  []store.CalendarSource{{ID: 3, Name: "Family", Enabled: true, SecretEnc: "v1:x"}},
	}
	snaps := &snapshotStoreFake{snaps: map[int64]store.CalendarFeedSnapshot{
		3: {SourceID: 3, FetchedAt: now.Add(-time.Hour), Status: store.CalendarSnapshotStatusOK, EventsJSON: lastGood},
	}}
	svc := newAgendaTestService(t, now, sources, snaps, fetcher, refresh)
	if err := svc.RefreshSource(context.Background(), 9, 3); !errors.Is(err, ics.ErrUnknownTimezone) {
		t.Fatalf("RefreshSource err=%v, want ErrUnknownTimezone", err)
	}
	if refresh.calls != 0 {
		t.Fatalf("publish calls=%d, want 0", refresh.calls)
	}
	if snaps.snaps[3].EventsJSON != lastGood {
		t.Fatalf("last-good overwritten: %s", snaps.snaps[3].EventsJSON)
	}
	if snaps.snaps[3].Status != store.CalendarSnapshotStatusError {
		t.Fatalf("status=%q", snaps.snaps[3].Status)
	}
	view, err := svc.ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda: %v", err)
	}
	if !view.Stale || len(view.Events) != 1 || view.Events[0].Title != "Pickup" {
		t.Fatalf("view=%+v", view)
	}
	if view.Error != ics.ErrUnknownTimezone.Error() {
		t.Fatalf("error=%q", view.Error)
	}
}

func TestTooManyOccurrencesKeepsLastGoodAndDoesNotPublish(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	lastGood := `[{"uid":"pickup","title":"Pickup","startsAt":"2026-08-17T15:00:00Z","endsAt":"2026-08-17T16:00:00Z","allDay":false,"location":""}]`
	dense := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:flood
DTSTART:20260817T000000Z
DTEND:20260817T000001Z
RRULE:FREQ=SECONDLY
SUMMARY:Flood
END:VEVENT
END:VCALENDAR
`)
	fetcher := &countingFetcher{resp: FetchResponse{Body: dense}}
	refresh := &calendarRefreshFake{}
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "UTC"},
		sources:  []store.CalendarSource{{ID: 3, Name: "Family", Enabled: true, SecretEnc: "v1:x"}},
	}
	snaps := &snapshotStoreFake{snaps: map[int64]store.CalendarFeedSnapshot{
		3: {SourceID: 3, FetchedAt: now.Add(-time.Hour), Status: store.CalendarSnapshotStatusOK, EventsJSON: lastGood},
	}}
	svc := newAgendaTestService(t, now, sources, snaps, fetcher, refresh)
	if err := svc.RefreshSource(context.Background(), 9, 3); !errors.Is(err, ics.ErrTooManyOccurrences) {
		t.Fatalf("RefreshSource err=%v, want ErrTooManyOccurrences", err)
	}
	if refresh.calls != 0 {
		t.Fatalf("publish calls=%d, want 0", refresh.calls)
	}
	if snaps.snaps[3].EventsJSON != lastGood {
		t.Fatalf("last-good overwritten: %s", snaps.snaps[3].EventsJSON)
	}
	if snaps.snaps[3].Status != store.CalendarSnapshotStatusError {
		t.Fatalf("status=%q", snaps.snaps[3].Status)
	}
	if strings.Contains(snaps.snaps[3].Error, "https://") || strings.Contains(snaps.snaps[3].Error, "BEGIN:VEVENT") {
		t.Fatalf("error leaked feed content: %q", snaps.snaps[3].Error)
	}
	view, err := svc.ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda: %v", err)
	}
	if !view.Stale || len(view.Events) != 1 || view.Events[0].Title != "Pickup" {
		t.Fatalf("view=%+v", view)
	}
}

func TestFirstFetchFailureReturnsEmptyStale(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	fetcher := &countingFetcher{err: ErrFeedRequest}
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "UTC"},
		sources:  []store.CalendarSource{{ID: 3, Enabled: true, SecretEnc: "v1:x"}},
	}
	snaps := &snapshotStoreFake{}
	svc := newAgendaTestService(t, now, sources, snaps, fetcher, &calendarRefreshFake{})
	if err := svc.RefreshSource(context.Background(), 9, 3); !errors.Is(err, ErrFeedRequest) {
		t.Fatalf("RefreshSource err=%v, want ErrFeedRequest", err)
	}
	view, err := svc.ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda: %v", err)
	}
	if !view.Stale || len(view.Events) != 0 || view.Error == "" {
		t.Fatalf("view=%+v", view)
	}
	if strings.Contains(view.Error, "https://") {
		t.Fatalf("error leaked URL: %q", view.Error)
	}
}

func TestSanitizeCalendarErrorNeverIncludesURL(t *testing.T) {
	msg := sanitizeCalendarError(errors.New("Get \"https://calendar.example.com/private/token.ics\": timeout"))
	if strings.Contains(msg, "https://") || strings.Contains(msg, "token.ics") {
		t.Fatalf("leaked: %q", msg)
	}
}

func mustCachedJSON(t *testing.T, events []cachedEvent) string {
	t.Helper()
	b, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReadAgendaSpringForwardExcludesNextCivilMorning(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 8, 10, 0, 0, 0, loc)
	todayStart, todayEnd := localDayBounds(now, loc)
	if todayEnd.Sub(todayStart) == 24*time.Hour {
		t.Fatal("test assumption: spring-forward civil day is not 24h")
	}
	nextMorning := time.Date(2026, 3, 9, 0, 30, 0, 0, loc)
	lateEvening := time.Date(2026, 3, 8, 23, 30, 0, 0, loc)
	if !nextMorning.Before(todayStart.Add(24 * time.Hour)) {
		t.Fatal("test assumption: 00:30 next morning is inside a +24h window")
	}
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "America/New_York"},
		sources:  []store.CalendarSource{{ID: 3, Name: "Family", Enabled: true, SecretEnc: "v1:x"}},
	}
	snaps := &snapshotStoreFake{snaps: map[int64]store.CalendarFeedSnapshot{
		3: {
			SourceID:  3,
			FetchedAt: now,
			Status:    store.CalendarSnapshotStatusOK,
			EventsJSON: mustCachedJSON(t, []cachedEvent{
				{UID: "late", Title: "Late", StartsAt: lateEvening, EndsAt: lateEvening.Add(15 * time.Minute)},
				{UID: "next", Title: "Next morning", StartsAt: nextMorning, EndsAt: nextMorning.Add(15 * time.Minute)},
			}),
		},
	}}
	svc := newAgendaTestService(t, now, sources, snaps, &countingFetcher{}, &calendarRefreshFake{})
	view, err := svc.ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda: %v", err)
	}
	if len(view.Events) != 1 || view.Events[0].Title != "Late" {
		t.Fatalf("spring-forward today must include 23:30 and exclude 00:30 next morning, got %+v", view.Events)
	}
}

func TestReadAgendaFallBackIncludesLateEvening(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, loc)
	todayStart, todayEnd := localDayBounds(now, loc)
	if todayEnd.Sub(todayStart) == 24*time.Hour {
		t.Fatal("test assumption: fall-back civil day is not 24h")
	}
	lateEvening := time.Date(2026, 11, 1, 23, 30, 0, 0, loc)
	if !lateEvening.After(todayStart.Add(24 * time.Hour)) {
		t.Fatal("test assumption: 23:30 is outside a +24h window on fall-back")
	}
	nextMidnight := time.Date(2026, 11, 2, 0, 0, 0, 0, loc)
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "America/New_York"},
		sources:  []store.CalendarSource{{ID: 3, Name: "Family", Enabled: true, SecretEnc: "v1:x"}},
	}
	snaps := &snapshotStoreFake{snaps: map[int64]store.CalendarFeedSnapshot{
		3: {
			SourceID:  3,
			FetchedAt: now,
			Status:    store.CalendarSnapshotStatusOK,
			EventsJSON: mustCachedJSON(t, []cachedEvent{
				{UID: "late", Title: "Late", StartsAt: lateEvening, EndsAt: lateEvening.Add(15 * time.Minute)},
				{UID: "next", Title: "Next day", StartsAt: nextMidnight, EndsAt: nextMidnight.Add(time.Hour)},
			}),
		},
	}}
	svc := newAgendaTestService(t, now, sources, snaps, &countingFetcher{}, &calendarRefreshFake{})
	view, err := svc.ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda: %v", err)
	}
	if len(view.Events) != 1 || view.Events[0].Title != "Late" {
		t.Fatalf("fall-back today must include 23:30 and exclude next midnight, got %+v", view.Events)
	}
}

func TestReadAgendaAllDaySpanningDSTSpringForward(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 8, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "America/New_York"},
		sources:  []store.CalendarSource{{ID: 3, Name: "Family", Enabled: true, SecretEnc: "v1:x"}},
	}
	snaps := &snapshotStoreFake{snaps: map[int64]store.CalendarFeedSnapshot{
		3: {
			SourceID:  3,
			FetchedAt: start,
			Status:    store.CalendarSnapshotStatusOK,
			EventsJSON: mustCachedJSON(t, []cachedEvent{
				{UID: "holiday", Title: "Holiday", StartsAt: start, EndsAt: end, AllDay: true},
			}),
		},
	}}
	on8, err := newAgendaTestService(t, start.Add(10*time.Hour), sources, snaps, &countingFetcher{}, &calendarRefreshFake{}).ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda Mar 8: %v", err)
	}
	if len(on8.Events) != 1 || on8.Events[0].Title != "Holiday" {
		t.Fatalf("all-day on spring-forward day missing: %+v", on8.Events)
	}
	on9, err := newAgendaTestService(t, end.Add(10*time.Hour), sources, snaps, &countingFetcher{}, &calendarRefreshFake{}).ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda Mar 9: %v", err)
	}
	if len(on9.Events) != 0 {
		t.Fatalf("exclusive all-day end leaked into next civil day: %+v", on9.Events)
	}
}

func TestExpansionWindowUsesCivilDaysAroundDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 8, 15, 0, 0, 0, loc)
	start, end := expansionWindow(now, loc)
	wantStart := time.Date(2026, 3, 7, 0, 0, 0, 0, loc)
	wantEnd := time.Date(2026, 3, 10, 0, 0, 0, 0, loc)
	if !start.Equal(wantStart) || !end.Equal(wantEnd) {
		t.Fatalf("window=%s %s want %s %s", start, end, wantStart, wantEnd)
	}
	today := time.Date(2026, 3, 8, 0, 0, 0, 0, loc)
	if today.Add(48 * time.Hour).Equal(wantEnd) {
		t.Fatal("test assumption: +48h is not +2 civil days on spring-forward")
	}
}

type blockingFetcher struct {
	mu        sync.Mutex
	calls     int
	started   chan struct{}
	startOnce sync.Once
	release   chan struct{}
	resp      FetchResponse
}

func (f *blockingFetcher) Fetch(ctx context.Context, _ FetchRequest) (FetchResponse, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	f.startOnce.Do(func() { close(f.started) })
	select {
	case <-f.release:
		return f.resp, nil
	case <-ctx.Done():
		return FetchResponse{}, ctx.Err()
	}
}

func (f *blockingFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type countingSourceStore struct {
	*calendarSourceStoreFake
	onGet func()
}

func (c *countingSourceStore) GetCalendarSource(ctx context.Context, projectID, sourceID int64) (store.CalendarSource, error) {
	src, err := c.calendarSourceStoreFake.GetCalendarSource(ctx, projectID, sourceID)
	if c.onGet != nil {
		c.onGet()
	}
	return src, err
}

func TestRefreshSourceCoalescesInFlightFetch(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	fetcher := &blockingFetcher{
		started: make(chan struct{}),
		release: make(chan struct{}),
		resp:    FetchResponse{Body: testICSForDay(now), ETag: `"v1"`},
	}
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "UTC"},
		sources:  []store.CalendarSource{{ID: 3, Name: "Family", Enabled: true, SecretEnc: "v1:x"}},
	}
	snaps := &snapshotStoreFake{}
	refresh := &calendarRefreshFake{}
	const n = 8
	errCh := make(chan error, n)
	var wg sync.WaitGroup
	var gets sync.WaitGroup
	gets.Add(n)
	sourcesCounted := &countingSourceStore{calendarSourceStoreFake: sources, onGet: gets.Done}
	snaps.sources = sources
	svc := NewAgendaService(AgendaServiceDependencies{
		Sources:   sourcesCounted,
		Snapshots: snaps,
		Cipher:    &calendarCipherFake{dec: []byte("https://calendar.example.com/private/token.ics")},
		Fetcher:   fetcher,
		Refresh:   refresh,
		Now:       func() time.Time { return now },
	})

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- svc.RefreshSource(context.Background(), 9, 3)
		}()
	}
	doneGets := make(chan struct{})
	go func() {
		gets.Wait()
		close(doneGets)
	}()
	select {
	case <-doneGets:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for refresh callers")
	}
	select {
	case <-fetcher.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for fetch to start")
	}
	time.Sleep(20 * time.Millisecond)
	svc.MaybeRefresh(context.Background(), 9)
	svc.MaybeRefresh(context.Background(), 9)
	close(fetcher.release)
	wg.Wait()
	close(errCh)
	if fetcher.callCount() != 1 {
		t.Fatalf("fetch calls=%d, want 1", fetcher.callCount())
	}
	for err := range errCh {
		if err != nil {
			t.Fatalf("RefreshSource: %v", err)
		}
	}
	if refresh.callCount() != 1 {
		t.Fatalf("publish calls=%d, want 1", refresh.callCount())
	}
}

func TestReadAgendaMarksAgedSnapshotStale(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	eventsJSON := `[{"uid":"pickup","title":"Pickup","startsAt":"2026-08-17T15:00:00Z","endsAt":"2026-08-17T16:00:00Z","allDay":false,"location":""}]`
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "UTC"},
		sources: []store.CalendarSource{{
			ID:        3,
			Name:      "Family",
			Enabled:   true,
			SecretEnc: "v1:x",
			Type:      SourceTypeICSFeed,
		}},
	}

	under := &snapshotStoreFake{snaps: map[int64]store.CalendarFeedSnapshot{
		3: {
			SourceID:   3,
			FetchedAt:  now.Add(-(snapshotStaleAfter - time.Millisecond)),
			Status:     store.CalendarSnapshotStatusOK,
			EventsJSON: eventsJSON,
		},
	}}
	underView, err := newAgendaTestService(t, now, sources, under, &countingFetcher{}, &calendarRefreshFake{}).ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda under threshold: %v", err)
	}
	if underView.Stale || len(underView.Events) != 1 || underView.Events[0].Title != "Pickup" {
		t.Fatalf("just-under stale view=%+v", underView)
	}

	over := &snapshotStoreFake{snaps: map[int64]store.CalendarFeedSnapshot{
		3: {
			SourceID:   3,
			FetchedAt:  now.Add(-(snapshotStaleAfter + time.Millisecond)),
			Status:     store.CalendarSnapshotStatusOK,
			EventsJSON: eventsJSON,
		},
	}}
	overView, err := newAgendaTestService(t, now, sources, over, &countingFetcher{}, &calendarRefreshFake{}).ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda over threshold: %v", err)
	}
	if !overView.Stale || len(overView.Events) != 1 || overView.Events[0].Title != "Pickup" {
		t.Fatalf("just-over stale view=%+v", overView)
	}
}

func TestRefreshNotModifiedWithoutSnapshotFails(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	fetcher := &countingFetcher{notModified: true}
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "UTC"},
		sources:  []store.CalendarSource{{ID: 3, Enabled: true, SecretEnc: "v1:x"}},
	}
	snaps := &snapshotStoreFake{}
	svc := newAgendaTestService(t, now, sources, snaps, fetcher, &calendarRefreshFake{})
	if err := svc.RefreshSource(context.Background(), 9, 3); !errors.Is(err, ErrFeedRequest) {
		t.Fatalf("RefreshSource err=%v, want ErrFeedRequest", err)
	}
	snap, ok := snaps.lookup(3)
	if !ok || snap.Status != store.CalendarSnapshotStatusError {
		t.Fatalf("error snapshot=%+v ok=%v", snap, ok)
	}
	if snap.Error != ErrFeedRequest.Error() {
		t.Fatalf("error=%q", snap.Error)
	}
	if strings.Contains(snap.Error, "https://") {
		t.Fatalf("error leaked URL: %q", snap.Error)
	}
	view, err := svc.ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda: %v", err)
	}
	if !view.Stale || len(view.Events) != 0 || view.Error != ErrFeedRequest.Error() {
		t.Fatalf("view=%+v", view)
	}
}
