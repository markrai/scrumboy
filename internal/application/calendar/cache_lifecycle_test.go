package calendar

import (
	"context"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func preparedCalendarWithSnapshots(t *testing.T, sources *calendarSourceStoreFake, snaps *snapshotStoreFake) *PreparedREST {
	t.Helper()
	sources.invalidator = snaps
	return preparedCalendar(t, RESTServiceDependencies{
		Projects: &calendarProjectFake{project: store.Project{ID: 9}},
		Roles:    &calendarRoleFake{role: store.RoleMaintainer},
		Cipher:   &calendarCipherFake{},
		Sources:  sources,
	})
}

func TestURLChangeInvalidatesCachedSnapshotAndValidators(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "UTC"},
	}
	snaps := &snapshotStoreFake{}
	prepared := preparedCalendarWithSnapshots(t, sources, snaps)
	created, err := prepared.Create(CreateSourceCommand{
		Name: "Family",
		URL:  "https://calendar.example.com/old.ics",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	oldEvents := `[{"uid":"old-feed","title":"Old Feed Event","startsAt":"2026-08-17T15:00:00Z","endsAt":"2026-08-17T16:00:00Z","allDay":false,"location":""}]`
	if err := snaps.UpsertCalendarFeedSnapshot(context.Background(), store.CalendarFeedSnapshot{
		SourceID:     created.ID,
		ETag:         `"old-etag"`,
		LastModified: "Mon, 17 Aug 2026 12:00:00 GMT",
		FetchedAt:    now.Add(-time.Minute),
		Status:       store.CalendarSnapshotStatusOK,
		EventsJSON:   oldEvents,
	}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	agenda := newAgendaTestService(t, now, sources, snaps, &countingFetcher{resp: FetchResponse{Body: testICSForDay(now)}}, &calendarRefreshFake{})
	before, err := agenda.ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda before URL change: %v", err)
	}
	if before.Stale || len(before.Events) != 1 || before.Events[0].Title != "Old Feed Event" {
		t.Fatalf("cached view=%+v", before)
	}

	newURL := "https://calendar.example.com/new.ics"
	if _, err := prepared.Update(UpdateSourceCommand{SourceID: created.ID, URL: &newURL}); err != nil {
		t.Fatalf("Update URL: %v", err)
	}
	if _, ok := snaps.lookup(created.ID); ok {
		t.Fatal("snapshot still present after URL change")
	}
	after, err := agenda.ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda after URL change: %v", err)
	}
	if !after.Stale || len(after.Events) != 0 {
		t.Fatalf("old events still current: %+v", after)
	}

	fetcher := &countingFetcher{resp: FetchResponse{Body: testICSForDay(now), ETag: `"new-etag"`}}
	refreshed := newAgendaTestService(t, now, sources, snaps, fetcher, &calendarRefreshFake{})
	if err := refreshed.RefreshSource(context.Background(), 9, created.ID); err != nil {
		t.Fatalf("RefreshSource: %v", err)
	}
	etag, lastModified := fetcher.lastValidators()
	if etag != "" || lastModified != "" {
		t.Fatalf("next fetch sent old validators etag=%q lastModified=%q", etag, lastModified)
	}
}

func TestRenameDoesNotInvalidateSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "UTC"},
	}
	snaps := &snapshotStoreFake{}
	prepared := preparedCalendarWithSnapshots(t, sources, snaps)
	created, err := prepared.Create(CreateSourceCommand{
		Name: "Family",
		URL:  "https://calendar.example.com/feed.ics",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	seed := store.CalendarFeedSnapshot{
		SourceID:     created.ID,
		ETag:         `"keep"`,
		LastModified: "Mon, 17 Aug 2026 12:00:00 GMT",
		FetchedAt:    now.Add(-time.Minute),
		Status:       store.CalendarSnapshotStatusOK,
		EventsJSON:   `[{"uid":"pickup","title":"Pickup","startsAt":"2026-08-17T15:00:00Z","endsAt":"2026-08-17T16:00:00Z","allDay":false,"location":""}]`,
	}
	if err := snaps.UpsertCalendarFeedSnapshot(context.Background(), seed); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	renamed := "Family renamed"
	if _, err := prepared.Update(UpdateSourceCommand{SourceID: created.ID, Name: &renamed}); err != nil {
		t.Fatalf("Update name: %v", err)
	}
	got, ok := snaps.lookup(created.ID)
	if !ok {
		t.Fatal("rename-only dropped snapshot")
	}
	if got.ETag != seed.ETag || got.LastModified != seed.LastModified || got.EventsJSON != seed.EventsJSON {
		t.Fatalf("rename mutated snapshot: %+v", got)
	}
	view, err := newAgendaTestService(t, now, sources, snaps, &countingFetcher{}, &calendarRefreshFake{}).ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda: %v", err)
	}
	if view.Stale || len(view.Events) != 1 || view.Events[0].Title != "Pickup" {
		t.Fatalf("view=%+v", view)
	}
}

func TestTimezoneChangeInvalidatesSnapshotAndReexpandsFloating(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 17, 16, 0, 0, 0, time.UTC)
	floating := []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:float\r\nDTSTART:20260817T090000\r\nDTEND:20260817T100000\r\nSUMMARY:Floating\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "America/New_York"},
		sources: []store.CalendarSource{{
			ID:        3,
			Name:      "Family",
			Enabled:   true,
			SecretEnc: "v1:x",
			Type:      SourceTypeICSFeed,
		}},
	}
	snaps := &snapshotStoreFake{}
	fetcher := &countingFetcher{resp: FetchResponse{
		Body:         floating,
		ETag:         `"tz-a"`,
		LastModified: "Mon, 17 Aug 2026 12:00:00 GMT",
	}}
	agenda := newAgendaTestService(t, now, sources, snaps, fetcher, &calendarRefreshFake{})
	if err := agenda.RefreshSource(context.Background(), 9, 3); err != nil {
		t.Fatalf("RefreshSource NY: %v", err)
	}
	nyView, err := agenda.ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda NY: %v", err)
	}
	if nyView.Stale || len(nyView.Events) != 1 || nyView.Events[0].StartsAt.In(ny).Hour() != 9 {
		t.Fatalf("NY view=%+v", nyView)
	}
	if nyView.Events[0].StartsAt.In(chicago).Hour() == 9 {
		t.Fatal("NY-normalized event already 09:00 in Chicago")
	}

	prepared := preparedCalendarWithSnapshots(t, sources, snaps)
	chicagoTZ := "America/Chicago"
	if _, err := prepared.PatchSettings(PatchSettingsCommand{Timezone: &chicagoTZ}); err != nil {
		t.Fatalf("PatchSettings timezone: %v", err)
	}
	if _, ok := snaps.lookup(3); ok {
		t.Fatal("snapshot still present after timezone change")
	}
	staleView, err := agenda.ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda after timezone change: %v", err)
	}
	if !staleView.Stale || len(staleView.Events) != 0 {
		t.Fatalf("old snapshot reused: %+v", staleView)
	}

	if err := agenda.RefreshSource(context.Background(), 9, 3); err != nil {
		t.Fatalf("RefreshSource Chicago: %v", err)
	}
	etag, lastModified := fetcher.lastValidators()
	if etag != "" || lastModified != "" {
		t.Fatalf("timezone refresh was conditional etag=%q lastModified=%q", etag, lastModified)
	}
	chicagoView, err := agenda.ReadAgenda(context.Background(), 9)
	if err != nil {
		t.Fatalf("ReadAgenda Chicago: %v", err)
	}
	if chicagoView.Stale || len(chicagoView.Events) != 1 {
		t.Fatalf("Chicago view=%+v", chicagoView)
	}
	if chicagoView.Events[0].StartsAt.In(chicago).Hour() != 9 {
		t.Fatalf("Chicago local hour=%d, want 9", chicagoView.Events[0].StartsAt.In(chicago).Hour())
	}
	if chicagoView.Events[0].StartsAt.Equal(nyView.Events[0].StartsAt) {
		t.Fatalf("normalized instant reused from NY: %s", chicagoView.Events[0].StartsAt)
	}
}

func TestAgendaEnabledOnlyDoesNotInvalidateSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: false, Timezone: "America/New_York"},
		sources: []store.CalendarSource{{
			ID:      3,
			Name:    "Family",
			Enabled: true,
		}},
	}
	seed := store.CalendarFeedSnapshot{
		SourceID:     3,
		ETag:         `"keep"`,
		LastModified: "Mon, 17 Aug 2026 12:00:00 GMT",
		FetchedAt:    now.Add(-time.Minute),
		Status:       store.CalendarSnapshotStatusOK,
		EventsJSON:   `[{"uid":"pickup","title":"Pickup","startsAt":"2026-08-17T15:00:00Z","endsAt":"2026-08-17T16:00:00Z","allDay":false,"location":""}]`,
	}
	snaps := &snapshotStoreFake{snaps: map[int64]store.CalendarFeedSnapshot{3: seed}}
	enabled := true
	if _, err := preparedCalendarWithSnapshots(t, sources, snaps).PatchSettings(PatchSettingsCommand{Enabled: &enabled}); err != nil {
		t.Fatalf("PatchSettings enabled: %v", err)
	}
	got, ok := snaps.lookup(3)
	if !ok {
		t.Fatal("agendaEnabled-only dropped snapshot")
	}
	if got.ETag != seed.ETag || got.LastModified != seed.LastModified || got.EventsJSON != seed.EventsJSON {
		t.Fatalf("enabled-only mutated snapshot: %+v", got)
	}
}

func TestAgendaColorOnlyDoesNotInvalidateSnapshot(t *testing.T) {
	assertAgendaPresentationPatchKeepsSnapshot(t, PatchSettingsCommand{Color: strPtr("#aabbcc")})
}

func TestAgendaTitleOnlyDoesNotInvalidateSnapshot(t *testing.T) {
	assertAgendaPresentationPatchKeepsSnapshot(t, PatchSettingsCommand{Title: strPtr("Team calendar")})
}

func TestAgendaTitleAndColorDoNotInvalidateSnapshot(t *testing.T) {
	assertAgendaPresentationPatchKeepsSnapshot(t, PatchSettingsCommand{
		Title: strPtr("Team calendar"),
		Color: strPtr("#aabbcc"),
	})
}

func assertAgendaPresentationPatchKeepsSnapshot(t *testing.T, command PatchSettingsCommand) {
	t.Helper()
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{
			Enabled:  true,
			Timezone: "America/New_York",
			Title:    store.DefaultAgendaTitle,
			Color:    store.DefaultAgendaColor,
		},
		sources: []store.CalendarSource{{
			ID:      3,
			Name:    "Family",
			Enabled: true,
		}},
	}
	seed := store.CalendarFeedSnapshot{
		SourceID:     3,
		ETag:         `"keep"`,
		LastModified: "Mon, 17 Aug 2026 12:00:00 GMT",
		FetchedAt:    now.Add(-time.Minute),
		Status:       store.CalendarSnapshotStatusOK,
		EventsJSON:   `[{"uid":"pickup","title":"Pickup","startsAt":"2026-08-17T15:00:00Z","endsAt":"2026-08-17T16:00:00Z","allDay":false,"location":""}]`,
	}
	snaps := &snapshotStoreFake{snaps: map[int64]store.CalendarFeedSnapshot{3: seed}}
	if _, err := preparedCalendarWithSnapshots(t, sources, snaps).PatchSettings(command); err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}
	got, ok := snaps.lookup(3)
	if !ok {
		t.Fatal("presentation-only patch dropped snapshot")
	}
	if got.ETag != seed.ETag || got.LastModified != seed.LastModified || got.EventsJSON != seed.EventsJSON {
		t.Fatalf("presentation-only mutated snapshot: %+v", got)
	}
}

func strPtr(v string) *string { return &v }
