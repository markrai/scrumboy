package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	calendarapp "scrumboy/internal/application/calendar"
	"scrumboy/internal/calendar/ics"
	"scrumboy/internal/store"
)

type agendaHTTPFetchFake struct {
	mu          sync.Mutex
	calls       int
	body        []byte
	err         error
	notModified bool
}

func (f *agendaHTTPFetchFake) Fetch(context.Context, calendarapp.FetchRequest) (calendarapp.FetchResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return calendarapp.FetchResponse{}, f.err
	}
	if f.notModified {
		return calendarapp.FetchResponse{NotModified: true}, nil
	}
	return calendarapp.FetchResponse{Body: f.body, ETag: `"v1"`}, nil
}

func (f *agendaHTTPFetchFake) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestBoardGet_AgendaIsAdditiveAndCacheOnly(t *testing.T) {
	fetcher := &agendaHTTPFetchFake{body: []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:pickup
DTSTART:20260817T150000Z
DTEND:20260817T160000Z
SUMMARY:Pickup
END:VEVENT
END:VCALENDAR
`)}
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody:      1 << 20,
		ScrumboyMode:        "full",
		EncryptionKey:       testEncryptionKey,
		CalendarFeedFetcher: fetcher,
	})
	defer cleanup()

	client := newCookieClient(t)
	ownerJSON := bootstrapUserClient(t, client, ts.URL, "Owner", "agenda-board-get@example.com", "password123")
	ownerID := int64(ownerJSON["id"].(float64))
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, &store.StoreOptions{EncryptionKey: testEncryptionKey})

	project, err := st.CreateProject(ctxOwner, "Agenda Board")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := st.UpdateProjectAgendaSettings(ctxOwner, project.ID, boolPtr(true), strPtr("UTC"), nil, nil); err != nil {
		t.Fatalf("UpdateProjectAgendaSettings: %v", err)
	}
	enc, err := st.EncryptSecret([]byte("https://calendar.example.com/private/super-secret-token.ics"))
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	src, err := st.CreateCalendarSource(ctxOwner, project.ID, store.CreateCalendarSourceInput{
		Type:      store.CalendarSourceTypeICSFeed,
		Name:      "Family",
		Enabled:   true,
		SecretEnc: enc,
		URLHash:   "hash-family",
	})
	if err != nil {
		t.Fatalf("CreateCalendarSource: %v", err)
	}
	today := time.Now().UTC()
	startsAt := time.Date(today.Year(), today.Month(), today.Day(), 15, 0, 0, 0, time.UTC)
	endsAt := startsAt.Add(time.Hour)
	if err := st.UpsertCalendarFeedSnapshot(ctxOwner, store.CalendarFeedSnapshot{
		SourceID:   src.ID,
		FetchedAt:  today,
		Status:     store.CalendarSnapshotStatusOK,
		EventsJSON: fmt.Sprintf(`[{"uid":"pickup","title":"Pickup","startsAt":%q,"endsAt":%q,"allDay":false,"location":""}]`, startsAt.Format(time.RFC3339), endsAt.Format(time.RFC3339)),
	}); err != nil {
		t.Fatalf("UpsertCalendarFeedSnapshot: %v", err)
	}

	workflow, err := st.GetProjectWorkflow(ctxOwner, project.ID)
	if err != nil {
		t.Fatalf("GetProjectWorkflow: %v", err)
	}

	var board map[string]any
	resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug, nil, &board)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET board: status=%d body=%s", resp.StatusCode, string(body))
	}
	if strings.Contains(string(body), "super-secret-token") || strings.Contains(string(body), "calendar.example.com/private") {
		t.Fatal("board GET leaked calendar URL")
	}
	if strings.Contains(string(body), "hash-family") {
		t.Fatal("board GET leaked url_hash")
	}
	if fetcher.callCount() != 0 {
		t.Fatalf("board GET fetch calls=%d, want 0", fetcher.callCount())
	}

	order, _ := board["columnOrder"].([]any)
	if len(order) != len(workflow) {
		t.Fatalf("columnOrder len=%d want %d", len(order), len(workflow))
	}
	for i, col := range workflow {
		item := order[i].(map[string]any)
		if item["key"] != col.Key {
			t.Fatalf("columnOrder[%d]=%v want %q", i, item["key"], col.Key)
		}
	}
	columns := board["columns"].(map[string]any)
	if _, ok := columns["agenda"]; ok {
		t.Fatal("columns must not contain agenda")
	}
	agenda, ok := board["agenda"].(map[string]any)
	if !ok || agenda["enabled"] != true {
		t.Fatalf("agenda=%v", board["agenda"])
	}
	if agenda["timezone"] != "UTC" {
		t.Fatalf("timezone=%v", agenda["timezone"])
	}
	if agenda["title"] != "Agenda" {
		t.Fatalf("title=%v, want Agenda", agenda["title"])
	}
	if agenda["color"] != store.DefaultAgendaColor {
		t.Fatalf("color=%v, want %s", agenda["color"], store.DefaultAgendaColor)
	}
	events, _ := agenda["events"].([]any)
	if len(events) < 1 {
		t.Fatalf("agenda events=%v", agenda["events"])
	}
	ev, _ := events[0].(map[string]any)
	if ev["provider"] != "ics_feed" {
		t.Fatalf("provider=%v", ev["provider"])
	}
	if ev["hostKind"] != "other" {
		t.Fatalf("hostKind=%v, want other", ev["hostKind"])
	}
	if _, ok := ev["urlHash"]; ok {
		t.Fatal("event leaked urlHash")
	}

	disabled, err := st.UpdateProjectAgendaSettings(ctxOwner, project.ID, boolPtr(false), nil, nil, nil)
	if err != nil || disabled.Enabled {
		t.Fatalf("disable agenda: %+v %v", disabled, err)
	}
	resp, body = doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug, nil, &board)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET disabled: status=%d body=%s", resp.StatusCode, string(body))
	}
	agenda = board["agenda"].(map[string]any)
	if agenda["enabled"] != false {
		t.Fatalf("disabled agenda=%v", agenda)
	}

	tempBoard, err := st.CreateAnonymousBoard(ctxOwner)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	var tempPayload map[string]any
	resp, body = doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+tempBoard.Slug, nil, &tempPayload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET temp: status=%d body=%s", resp.StatusCode, string(body))
	}
	if _, ok := tempPayload["agenda"]; ok {
		t.Fatalf("temp board should omit agenda, got %v", tempPayload["agenda"])
	}
}

func TestCalendarSourceRefresh_PublishesAgendaUpdatedOnce(t *testing.T) {
	now := time.Now().UTC()
	stamp := now.Format("20060102") + "T150000Z"
	end := now.Format("20060102") + "T160000Z"
	fetcher := &agendaHTTPFetchFake{body: []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:pickup\r\nDTSTART:" + stamp + "\r\nDTEND:" + end + "\r\nSUMMARY:Pickup\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")}
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody:      1 << 20,
		ScrumboyMode:        "full",
		EncryptionKey:       testEncryptionKey,
		CalendarFeedFetcher: fetcher,
	})
	defer cleanup()

	client := newCookieClient(t)
	ownerJSON := bootstrapUserClient(t, client, ts.URL, "Owner", "agenda-refresh@example.com", "password123")
	ownerID := int64(ownerJSON["id"].(float64))
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, &store.StoreOptions{EncryptionKey: testEncryptionKey})
	project, err := st.CreateProject(ctxOwner, "Agenda Refresh")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := st.UpdateProjectAgendaSettings(ctxOwner, project.ID, boolPtr(true), strPtr("UTC"), nil, nil); err != nil {
		t.Fatalf("UpdateProjectAgendaSettings: %v", err)
	}
	enc, err := st.EncryptSecret([]byte("https://calendar.example.com/private/token.ics"))
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	src, err := st.CreateCalendarSource(ctxOwner, project.ID, store.CreateCalendarSourceInput{
		Type:      store.CalendarSourceTypeICSFeed,
		Name:      "Family",
		Enabled:   true,
		SecretEnc: enc,
		URLHash:   "hash-family",
	})
	if err != nil {
		t.Fatalf("CreateCalendarSource: %v", err)
	}

	stream := subscribeTodoUpdateEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/calendar-sources/"+strconv.FormatInt(src.ID, 10)+"/refresh", map[string]any{}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh: status=%d body=%s", resp.StatusCode, string(body))
	}
	events := collectTodoUpdateEvents(t, stream)
	if len(events) != 1 || events[0].Type != "refresh_needed" || events[0].Reason != "agenda_updated" {
		t.Fatalf("events=%+v, want one refresh_needed agenda_updated", events)
	}

	fetcher.notModified = true
	stream2 := subscribeTodoUpdateEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
	resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/calendar-sources/"+strconv.FormatInt(src.ID, 10)+"/refresh", map[string]any{}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh 304: status=%d body=%s", resp.StatusCode, string(body))
	}
	if got := collectTodoUpdateEvents(t, stream2); len(got) != 0 {
		t.Fatalf("304 published SSE: %+v", got)
	}
}

func TestEmailNotifier_AgendaUpdatedIsNotEmailed(t *testing.T) {
	if _, ok := refreshReasonInfo["agenda_updated"]; ok {
		t.Fatal("agenda_updated must not be added to the email reason map")
	}
}

func TestCalendarSourceRefresh_ProviderFailureKeepsLastGoodAndDoesNotSucceed(t *testing.T) {
	lastGood := `[{"uid":"pickup","title":"Pickup","startsAt":"2026-08-17T15:00:00Z","endsAt":"2026-08-17T16:00:00Z","allDay":false,"location":""}]`
	fetcher := &agendaHTTPFetchFake{err: calendarapp.ErrFeedRequest}
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody:      1 << 20,
		ScrumboyMode:        "full",
		EncryptionKey:       testEncryptionKey,
		CalendarFeedFetcher: fetcher,
	})
	defer cleanup()

	client := newCookieClient(t)
	ownerJSON := bootstrapUserClient(t, client, ts.URL, "Owner", "agenda-refresh-fail@example.com", "password123")
	ownerID := int64(ownerJSON["id"].(float64))
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, &store.StoreOptions{EncryptionKey: testEncryptionKey})
	project, err := st.CreateProject(ctxOwner, "Agenda Refresh Fail")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := st.UpdateProjectAgendaSettings(ctxOwner, project.ID, boolPtr(true), strPtr("UTC"), nil, nil); err != nil {
		t.Fatalf("UpdateProjectAgendaSettings: %v", err)
	}
	enc, err := st.EncryptSecret([]byte("https://calendar.example.com/private/super-secret-token.ics"))
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	src, err := st.CreateCalendarSource(ctxOwner, project.ID, store.CreateCalendarSourceInput{
		Type:      store.CalendarSourceTypeICSFeed,
		Name:      "Family",
		Enabled:   true,
		SecretEnc: enc,
		URLHash:   "hash-family-fail",
	})
	if err != nil {
		t.Fatalf("CreateCalendarSource: %v", err)
	}
	if err := st.UpsertCalendarFeedSnapshot(ctxOwner, store.CalendarFeedSnapshot{
		SourceID:   src.ID,
		FetchedAt:  time.Now().UTC().Add(-time.Hour),
		Status:     store.CalendarSnapshotStatusOK,
		EventsJSON: lastGood,
	}); err != nil {
		t.Fatalf("UpsertCalendarFeedSnapshot: %v", err)
	}

	stream := subscribeTodoUpdateEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
	var envelope apiErrorEnvelope
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/calendar-sources/"+strconv.FormatInt(src.ID, 10)+"/refresh", map[string]any{}, &envelope)
	if resp.StatusCode < 400 {
		t.Fatalf("refresh failure: status=%d body=%s", resp.StatusCode, string(body))
	}
	if strings.Contains(string(body), "super-secret-token") || strings.Contains(string(body), "https://calendar.example.com") {
		t.Fatalf("refresh error leaked calendar URL: %s", body)
	}
	if envelope.Error.Message == "" {
		t.Fatalf("missing error message: %s", body)
	}
	if got := collectTodoUpdateEvents(t, stream); len(got) != 0 {
		t.Fatalf("failed refresh published SSE: %+v", got)
	}

	snap, err := st.GetCalendarFeedSnapshot(ctxOwner, src.ID)
	if err != nil {
		t.Fatalf("GetCalendarFeedSnapshot: %v", err)
	}
	if snap.EventsJSON != lastGood {
		t.Fatalf("last-good overwritten: %s", snap.EventsJSON)
	}
	if snap.Status != store.CalendarSnapshotStatusError {
		t.Fatalf("status=%q", snap.Status)
	}
}

func TestCalendarSourceRefresh_DistinguishesOversizedFeedFromOccurrenceExplosion(t *testing.T) {
	tests := []struct {
		name        string
		fetchErr    error
		wantStatus  int
		wantCode    string
		wantMessage string
		wantReason  string
	}{
		{
			name:        "oversized_input",
			fetchErr:    calendarapp.ErrFeedTooLarge,
			wantStatus:  http.StatusRequestEntityTooLarge,
			wantCode:    "PAYLOAD_TOO_LARGE",
			wantMessage: calendarapp.ErrFeedTooLarge.Error(),
		},
		{
			name:        "occurrence_explosion",
			fetchErr:    ics.ErrTooManyOccurrences,
			wantStatus:  http.StatusBadRequest,
			wantCode:    "VALIDATION_ERROR",
			wantMessage: ics.ErrTooManyOccurrences.Error(),
			wantReason:  "too_many_calendar_occurrences",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fetcher := &agendaHTTPFetchFake{err: tc.fetchErr}
			ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
				MaxRequestBody:      1 << 20,
				ScrumboyMode:        "full",
				EncryptionKey:       testEncryptionKey,
				CalendarFeedFetcher: fetcher,
			})
			defer cleanup()

			client := newCookieClient(t)
			ownerJSON := bootstrapUserClient(t, client, ts.URL, "Owner", "agenda-refresh-"+tc.name+"@example.com", "password123")
			ownerID := int64(ownerJSON["id"].(float64))
			ctxOwner := store.WithUserID(context.Background(), ownerID)
			st := store.New(sqlDB, &store.StoreOptions{EncryptionKey: testEncryptionKey})
			project, err := st.CreateProject(ctxOwner, "Agenda Refresh "+tc.name)
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			if _, err := st.UpdateProjectAgendaSettings(ctxOwner, project.ID, boolPtr(true), strPtr("UTC"), nil, nil); err != nil {
				t.Fatalf("UpdateProjectAgendaSettings: %v", err)
			}
			enc, err := st.EncryptSecret([]byte("https://calendar.example.com/private/super-secret-token.ics"))
			if err != nil {
				t.Fatalf("EncryptSecret: %v", err)
			}
			src, err := st.CreateCalendarSource(ctxOwner, project.ID, store.CreateCalendarSourceInput{
				Type:      store.CalendarSourceTypeICSFeed,
				Name:      "Family",
				Enabled:   true,
				SecretEnc: enc,
				URLHash:   "hash-" + tc.name,
			})
			if err != nil {
				t.Fatalf("CreateCalendarSource: %v", err)
			}

			var envelope apiErrorEnvelope
			resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/calendar-sources/"+strconv.FormatInt(src.ID, 10)+"/refresh", map[string]any{}, &envelope)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status=%d want %d body=%s", resp.StatusCode, tc.wantStatus, string(body))
			}
			if envelope.Error.Code != tc.wantCode || envelope.Error.Message != tc.wantMessage {
				t.Fatalf("error=%+v want code=%q message=%q body=%s", envelope.Error, tc.wantCode, tc.wantMessage, string(body))
			}
			if tc.wantReason != "" {
				gotReason, _ := envelope.Error.Details["reason"].(string)
				if gotReason != tc.wantReason {
					t.Fatalf("reason=%q want %q body=%s", gotReason, tc.wantReason, string(body))
				}
			}
			if envelope.Error.Message == calendarapp.ErrFeedTooLarge.Error() && tc.fetchErr == ics.ErrTooManyOccurrences {
				t.Fatal("occurrence explosion mapped to oversized-input message")
			}
			if envelope.Error.Message == ics.ErrTooManyOccurrences.Error() && tc.fetchErr == calendarapp.ErrFeedTooLarge {
				t.Fatal("oversized input mapped to occurrence-explosion message")
			}
			if strings.Contains(string(body), "super-secret-token") {
				t.Fatalf("refresh error leaked calendar URL: %s", body)
			}
		})
	}
}

func boolPtr(v bool) *bool    { return &v }
func strPtr(v string) *string { return &v }
