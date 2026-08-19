package calendar

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type passthroughCipher struct{}

func (passthroughCipher) EncryptSecret(plain []byte) (string, error) {
	return string(plain), nil
}

func (passthroughCipher) DecryptSecret(enc string) ([]byte, error) {
	return []byte(enc), nil
}

type gatedFetcher struct {
	mu       sync.Mutex
	started  chan struct{}
	release  chan struct{}
	firstErr error
	first    FetchResponse
	rest     FetchResponse
	calls    int
	reqs     []FetchRequest
}

func (f *gatedFetcher) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.reqs = append(f.reqs, req)
	firstErr := f.firstErr
	first := f.first
	rest := f.rest
	f.mu.Unlock()

	if call == 1 {
		close(f.started)
		select {
		case <-f.release:
		case <-ctx.Done():
			return FetchResponse{}, ctx.Err()
		}
		if firstErr != nil {
			return FetchResponse{}, firstErr
		}
		return first, nil
	}
	return rest, nil
}

func (f *gatedFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *gatedFetcher) request(i int) FetchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i < 0 || i >= len(f.reqs) {
		return FetchRequest{}
	}
	return f.reqs[i]
}

func testICSNamed(day time.Time, uid, title string) []byte {
	stamp := day.UTC().Format("20060102") + "T150000Z"
	end := day.UTC().Format("20060102") + "T160000Z"
	return []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTART:" + stamp + "\r\nDTEND:" + end + "\r\nSUMMARY:" + title + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
}

func waitFetcherCalls(t *testing.T, f *gatedFetcher, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f.callCount() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d fetch calls, got %d", n, f.callCount())
}

func waitSnapshotOKContaining(t *testing.T, snaps *snapshotStoreFake, sourceID int64, needle string) store.CalendarFeedSnapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap, ok := snaps.lookup(sourceID)
		if ok && snap.Status == store.CalendarSnapshotStatusOK && strings.Contains(snap.EventsJSON, needle) {
			return snap
		}
		time.Sleep(5 * time.Millisecond)
	}
	snap, ok := snaps.lookup(sourceID)
	t.Fatalf("timed out waiting for %q snapshot ok=%v snap=%+v", needle, ok, snap)
	return store.CalendarFeedSnapshot{}
}

func assertObsoleteNotCurrent(t *testing.T, snaps *snapshotStoreFake, sourceID int64, obsoleteNeedle string) {
	t.Helper()
	snap, ok := snaps.lookup(sourceID)
	if !ok {
		return
	}
	if strings.Contains(snap.EventsJSON, obsoleteNeedle) {
		t.Fatalf("obsolete result became current: %+v", snap)
	}
	if snap.Status == store.CalendarSnapshotStatusError {
		t.Fatalf("obsolete failure recreated error snapshot: %+v", snap)
	}
}

func TestInFlightURLRefreshDoesNotResurrectInvalidatedSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	const urlA = "https://calendar.example.com/a.ics"
	const urlB = "https://calendar.example.com/b.ics"
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "UTC"},
	}
	snaps := &snapshotStoreFake{sources: sources}
	sources.invalidator = snaps
	cipher := passthroughCipher{}
	prepared := preparedCalendar(t, RESTServiceDependencies{
		Projects: &calendarProjectFake{project: store.Project{ID: 9}},
		Roles:    &calendarRoleFake{role: store.RoleMaintainer},
		Cipher:   cipher,
		Sources:  sources,
	})
	created, err := prepared.Create(CreateSourceCommand{Name: "Family", URL: urlA})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := snaps.UpsertCalendarFeedSnapshot(context.Background(), store.CalendarFeedSnapshot{
		SourceID:     created.ID,
		ETag:         `"from-a"`,
		LastModified: "Mon, 17 Aug 2026 12:00:00 GMT",
		FetchedAt:    now.Add(-time.Hour),
		Status:       store.CalendarSnapshotStatusOK,
		EventsJSON:   `[{"uid":"feed-a","title":"Feed A","startsAt":"2026-08-17T15:00:00Z","endsAt":"2026-08-17T16:00:00Z","allDay":false,"location":""}]`,
	}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	fetcher := &gatedFetcher{
		started: make(chan struct{}),
		release: make(chan struct{}),
		first:   FetchResponse{Body: testICSNamed(now, "feed-a", "Feed A"), ETag: `"from-a"`},
		rest:    FetchResponse{Body: testICSNamed(now, "feed-b", "Feed B"), ETag: `"from-b"`},
	}
	refresh := &calendarRefreshFake{}
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
	case <-fetcher.started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for URL A fetch")
	}

	urlBCopy := urlB
	if _, err := prepared.Update(UpdateSourceCommand{SourceID: created.ID, URL: &urlBCopy}); err != nil {
		t.Fatalf("Update URL: %v", err)
	}
	if _, ok := snaps.lookup(created.ID); ok {
		t.Fatal("snapshot still present after URL change")
	}
	svc.MaybeRefresh(context.Background(), 9)
	if fetcher.callCount() != 1 {
		t.Fatalf("MaybeRefresh started a second fetch during in-flight A, calls=%d", fetcher.callCount())
	}

	close(fetcher.release)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("obsolete URL A refresh surfaced error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for URL A refresh to finish")
	}
	assertObsoleteNotCurrent(t, snaps, created.ID, "Feed A")

	waitFetcherCalls(t, fetcher, 2)
	snap := waitSnapshotOKContaining(t, snaps, created.ID, "Feed B")
	if strings.Contains(snap.EventsJSON, "Feed A") {
		t.Fatalf("Feed A remained in current snapshot: %s", snap.EventsJSON)
	}
	bReq := fetcher.request(1)
	if bReq.URL != urlB {
		t.Fatalf("B fetch URL=%q, want %q", bReq.URL, urlB)
	}
	if bReq.ETag != "" || bReq.LastModified != "" {
		t.Fatalf("B reused A validators etag=%q lastModified=%q", bReq.ETag, bReq.LastModified)
	}
	if refresh.callCount() != 1 {
		t.Fatalf("publish calls=%d, want 1 from B only", refresh.callCount())
	}
}

func TestInFlightTimezoneRefreshDoesNotResurrectInvalidatedSnapshot(t *testing.T) {
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
	}
	snaps := &snapshotStoreFake{sources: sources}
	sources.invalidator = snaps
	cipher := passthroughCipher{}
	prepared := preparedCalendar(t, RESTServiceDependencies{
		Projects: &calendarProjectFake{project: store.Project{ID: 9}},
		Roles:    &calendarRoleFake{role: store.RoleMaintainer},
		Cipher:   cipher,
		Sources:  sources,
	})
	created, err := prepared.Create(CreateSourceCommand{Name: "Family", URL: "https://calendar.example.com/float.ics"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	fetcher := &gatedFetcher{
		started: make(chan struct{}),
		release: make(chan struct{}),
		first:   FetchResponse{Body: floating, ETag: `"from-ny"`},
		rest:    FetchResponse{Body: floating, ETag: `"from-chi"`},
	}
	refresh := &calendarRefreshFake{}
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
	case <-fetcher.started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for NY fetch")
	}

	chicagoTZ := "America/Chicago"
	if _, err := prepared.PatchSettings(PatchSettingsCommand{Timezone: &chicagoTZ}); err != nil {
		t.Fatalf("PatchSettings timezone: %v", err)
	}
	if _, ok := snaps.lookup(created.ID); ok {
		t.Fatal("snapshot still present after timezone change")
	}
	svc.MaybeRefresh(context.Background(), 9)
	close(fetcher.release)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("obsolete NY refresh surfaced error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for NY refresh to finish")
	}

	if snap, ok := snaps.lookup(created.ID); ok {
		cached, decodeErr := decodeCachedEvents(snap.EventsJSON)
		if decodeErr == nil && len(cached) == 1 && cached[0].StartsAt.In(ny).Hour() == 9 && cached[0].StartsAt.In(chicago).Hour() != 9 {
			t.Fatalf("NY-normalized snapshot accepted as current: %+v", cached[0].StartsAt)
		}
	}

	waitFetcherCalls(t, fetcher, 2)
	snap := waitSnapshotOKContaining(t, snaps, created.ID, "Floating")
	cached, err := decodeCachedEvents(snap.EventsJSON)
	if err != nil || len(cached) != 1 {
		t.Fatalf("Chicago snapshot=%s err=%v", snap.EventsJSON, err)
	}
	if cached[0].StartsAt.In(chicago).Hour() != 9 {
		t.Fatalf("Chicago local hour=%d, want 9 (%s)", cached[0].StartsAt.In(chicago).Hour(), cached[0].StartsAt)
	}
	if cached[0].StartsAt.In(ny).Hour() == 9 {
		t.Fatal("current snapshot is still NY-normalized")
	}
	bReq := fetcher.request(1)
	if bReq.ETag != "" || bReq.LastModified != "" {
		t.Fatalf("Chicago fetch reused NY validators etag=%q lastModified=%q", bReq.ETag, bReq.LastModified)
	}
}

func TestObsoleteFailingRefreshDoesNotRecreateErrorSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	const urlA = "https://calendar.example.com/a.ics"
	const urlB = "https://calendar.example.com/b.ics"
	sources := &calendarSourceStoreFake{
		settings: store.ProjectAgendaSettings{Enabled: true, Timezone: "UTC"},
	}
	snaps := &snapshotStoreFake{sources: sources}
	sources.invalidator = snaps
	cipher := passthroughCipher{}
	prepared := preparedCalendar(t, RESTServiceDependencies{
		Projects: &calendarProjectFake{project: store.Project{ID: 9}},
		Roles:    &calendarRoleFake{role: store.RoleMaintainer},
		Cipher:   cipher,
		Sources:  sources,
	})
	created, err := prepared.Create(CreateSourceCommand{Name: "Family", URL: urlA})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	fetcher := &gatedFetcher{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		firstErr: ErrFeedRequest,
		rest:     FetchResponse{Body: testICSNamed(now, "feed-b", "Feed B"), ETag: `"from-b"`},
	}
	refresh := &calendarRefreshFake{}
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
	case <-fetcher.started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for failing A fetch")
	}

	urlBCopy := urlB
	if _, err := prepared.Update(UpdateSourceCommand{SourceID: created.ID, URL: &urlBCopy}); err != nil {
		t.Fatalf("Update URL: %v", err)
	}
	close(fetcher.release)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("obsolete failing refresh surfaced %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for failing A refresh to finish")
	}
	assertObsoleteNotCurrent(t, snaps, created.ID, "Feed A")

	waitFetcherCalls(t, fetcher, 2)
	waitSnapshotOKContaining(t, snaps, created.ID, "Feed B")
	if refresh.callCount() != 1 {
		t.Fatalf("publish calls=%d, want 1 from B only", refresh.callCount())
	}
}
