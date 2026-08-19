package calendar

import (
	"context"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type gatedDecryptCipher struct {
	passthroughCipher
	once     sync.Once
	started  chan struct{}
	release  chan struct{}
	decCalls int
	mu       sync.Mutex
}

func (c *gatedDecryptCipher) DecryptSecret(enc string) ([]byte, error) {
	c.mu.Lock()
	c.decCalls++
	c.mu.Unlock()
	c.once.Do(func() { close(c.started) })
	select {
	case <-c.release:
	case <-time.After(5 * time.Second):
		return nil, context.DeadlineExceeded
	}
	return c.passthroughCipher.DecryptSecret(enc)
}

func TestStaleRefreshDoesNotOverwriteHostKindAfterURLChange(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	const urlGoogle = "https://calendar.google.com/calendar/ical/family/private-token/basic.ics"
	const urlApple = "https://p12-caldav.icloud.com/published/2/guid"
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "UTC"},
	}
	snaps := &snapshotStoreFake{sources: sources}
	sources.invalidator = snaps
	prepared := preparedCalendar(t, RESTServiceDependencies{
		Projects: &calendarProjectFake{project: store.Project{ID: 9}},
		Roles:    &calendarRoleFake{role: store.RoleMaintainer},
		Cipher:   passthroughCipher{},
		Sources:  sources,
	})
	created, err := prepared.Create(CreateSourceCommand{Name: "Family", URL: urlGoogle})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := snaps.UpsertCalendarFeedSnapshot(context.Background(), store.CalendarFeedSnapshot{
		SourceID:   created.ID,
		ETag:       `"v1"`,
		FetchedAt:  now.Add(-time.Hour),
		Status:     store.CalendarSnapshotStatusOK,
		EventsJSON: `[{"uid":"pickup","title":"Pickup","startsAt":"2026-08-17T15:00:00Z","endsAt":"2026-08-17T16:00:00Z","allDay":false,"location":""}]`,
	}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	fetcher := &countingFetcher{notModified: true}
	refresh := &calendarRefreshFake{}
	cipher := &gatedDecryptCipher{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	svc := NewAgendaService(AgendaServiceDependencies{
		Sources:   sources,
		Snapshots: snaps,
		Cipher:    cipher,
		Fetcher:   fetcher,
		Refresh:   refresh,
		Now:       func() time.Time { return now },
	})

	errCh := make(chan error, 1)
	go func() { errCh <- svc.RefreshSource(context.Background(), 9, created.ID) }()
	select {
	case <-cipher.started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for decrypt")
	}

	apple := urlApple
	if _, err := prepared.Update(UpdateSourceCommand{SourceID: created.ID, URL: &apple}); err != nil {
		t.Fatalf("Update URL: %v", err)
	}
	src, err := sources.GetCalendarSource(context.Background(), 9, created.ID)
	if err != nil {
		t.Fatalf("Get after URL update: %v", err)
	}
	if src.HostKind != store.CalendarHostKindApple {
		t.Fatalf("host_kind after URL update=%q, want apple", src.HostKind)
	}

	close(cipher.release)
	select {
	case <-errCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for refresh")
	}

	src, err = sources.GetCalendarSource(context.Background(), 9, created.ID)
	if err != nil {
		t.Fatalf("Get after stale refresh: %v", err)
	}
	if src.HostKind != store.CalendarHostKindApple {
		t.Fatalf("stale refresh overwrote host_kind=%q, want apple", src.HostKind)
	}
	if refresh.reasonCount(refreshReasonAgendaUpdated) != 0 {
		t.Fatalf("obsolete classification published agenda_updated (%v)", refresh.reasons)
	}
}

func TestMigratedSourceBackfillPublishesOnNotModified(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	googleURL := "https://calendar.google.com/calendar/ical/family/private-token/basic.ics"
	hash := hashCalendarURL(googleURL)
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "UTC"},
		sources: []store.CalendarSource{{
			ID:        3,
			Name:      "Family",
			Enabled:   true,
			SecretEnc: googleURL,
			URLHash:   hash,
			HostKind:  store.CalendarHostKindOther,
			Type:      SourceTypeICSFeed,
		}},
	}
	snaps := &snapshotStoreFake{snaps: map[int64]store.CalendarFeedSnapshot{
		3: {
			SourceID:   3,
			ETag:       `"v1"`,
			FetchedAt:  now.Add(-time.Hour),
			Status:     store.CalendarSnapshotStatusOK,
			EventsJSON: `[{"uid":"pickup","title":"Pickup","startsAt":"2026-08-17T15:00:00Z","endsAt":"2026-08-17T16:00:00Z","allDay":false,"location":""}]`,
		},
	}}
	refresh := &calendarRefreshFake{}
	svc := NewAgendaService(AgendaServiceDependencies{
		Sources:   sources,
		Snapshots: snaps,
		Cipher:    passthroughCipher{},
		Fetcher:   &countingFetcher{notModified: true},
		Refresh:   refresh,
		Now:       func() time.Time { return now },
	})
	if err := svc.RefreshSource(context.Background(), 9, 3); err != nil {
		t.Fatalf("RefreshSource: %v", err)
	}
	src, err := sources.GetCalendarSource(context.Background(), 9, 3)
	if err != nil {
		t.Fatalf("GetCalendarSource: %v", err)
	}
	if src.HostKind != store.CalendarHostKindGoogle {
		t.Fatalf("host_kind=%q, want google", src.HostKind)
	}
	if refresh.reasonCount(refreshReasonAgendaUpdated) != 1 {
		t.Fatalf("agenda_updated count=%d reasons=%v, want 1", refresh.reasonCount(refreshReasonAgendaUpdated), refresh.reasons)
	}

	if err := svc.RefreshSource(context.Background(), 9, 3); err != nil {
		t.Fatalf("second RefreshSource: %v", err)
	}
	if refresh.reasonCount(refreshReasonAgendaUpdated) != 1 {
		t.Fatalf("second 304 republished agenda_updated: %v", refresh.reasons)
	}
}

func TestReadAgendaCopiesPersistedHostKind(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "UTC"},
		sources: []store.CalendarSource{{
			ID:        3,
			Name:      "Family",
			Enabled:   true,
			SecretEnc: "v1:ciphertext",
			Type:      SourceTypeICSFeed,
			HostKind:  store.CalendarHostKindGoogle,
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
	svc := newAgendaTestService(t, now, sources, snaps, &countingFetcher{}, &calendarRefreshFake{})
	view, err := svc.ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda: %v", err)
	}
	if len(view.Events) != 1 {
		t.Fatalf("events=%d", len(view.Events))
	}
	if view.Events[0].Provider != SourceTypeICSFeed {
		t.Fatalf("provider=%q", view.Events[0].Provider)
	}
	if view.Events[0].HostKind != store.CalendarHostKindGoogle {
		t.Fatalf("hostKind=%q, want google", view.Events[0].HostKind)
	}
}
