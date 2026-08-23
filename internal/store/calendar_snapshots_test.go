package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCalendarFeedSnapshotUpsertTouchAndLastGood(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "calendar-snap@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(ownerCtx, "Snapshots")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	enc, err := st.EncryptSecret([]byte("https://calendar.example.com/private/token.ics"))
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	src, err := st.CreateCalendarSource(ownerCtx, project.ID, CreateCalendarSourceInput{
		Type:      CalendarSourceTypeICSFeed,
		Name:      "Family",
		Enabled:   true,
		SecretEnc: enc,
		URLHash:   "hash-family",
	})
	if err != nil {
		t.Fatalf("CreateCalendarSource: %v", err)
	}

	if _, err := st.GetCalendarFeedSnapshot(ownerCtx, src.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing snapshot err=%v, want ErrNotFound", err)
	}

	firstFetched := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	if err := st.UpsertCalendarFeedSnapshot(ownerCtx, CalendarFeedSnapshot{
		SourceID:     src.ID,
		ETag:         `"v1"`,
		LastModified: "Mon, 17 Aug 2026 12:00:00 GMT",
		FetchedAt:    firstFetched,
		Status:       CalendarSnapshotStatusOK,
		EventsJSON:   `[{"uid":"a","title":"Pickup"}]`,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := st.GetCalendarFeedSnapshot(ownerCtx, src.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ETag != `"v1"` || got.EventsJSON != `[{"uid":"a","title":"Pickup"}]` || got.Status != CalendarSnapshotStatusOK {
		t.Fatalf("got=%+v", got)
	}

	touched := firstFetched.Add(15 * time.Minute)
	if err := st.TouchCalendarFeedSnapshot(ownerCtx, src.ID, touched, `"v1"`, ""); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	got, err = st.GetCalendarFeedSnapshot(ownerCtx, src.ID)
	if err != nil {
		t.Fatalf("Get after touch: %v", err)
	}
	if !got.FetchedAt.Equal(touched) || got.EventsJSON != `[{"uid":"a","title":"Pickup"}]` || got.LastModified == "" {
		t.Fatalf("touched=%+v", got)
	}

	if err := st.UpsertCalendarFeedSnapshot(ownerCtx, CalendarFeedSnapshot{
		SourceID:   src.ID,
		ETag:       `"v1"`,
		FetchedAt:  touched.Add(time.Minute),
		Status:     CalendarSnapshotStatusError,
		Error:      "unsupported calendar timezone",
		EventsJSON: `[{"uid":"a","title":"Pickup"}]`,
	}); err != nil {
		t.Fatalf("Upsert error keeping last-good: %v", err)
	}
	got, err = st.GetCalendarFeedSnapshot(ownerCtx, src.ID)
	if err != nil {
		t.Fatalf("Get after error: %v", err)
	}
	if got.Status != CalendarSnapshotStatusError || got.EventsJSON != `[{"uid":"a","title":"Pickup"}]` {
		t.Fatalf("last-good overwritten: %+v", got)
	}

	listed, err := st.ListCalendarFeedSnapshots(ownerCtx, project.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].SourceID != src.ID {
		t.Fatalf("listed=%+v", listed)
	}

	if err := st.DeleteCalendarSource(ownerCtx, project.ID, src.ID); err != nil {
		t.Fatalf("DeleteCalendarSource: %v", err)
	}
	if _, err := st.GetCalendarFeedSnapshot(ownerCtx, src.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("snapshot after source delete = %v, want ErrNotFound", err)
	}
}

func TestDeleteCalendarFeedSnapshotsSourceAndProject(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "calendar-snap-del@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	keepProject, err := st.CreateProject(ownerCtx, "Keep")
	if err != nil {
		t.Fatalf("CreateProject keep: %v", err)
	}
	dropProject, err := st.CreateProject(ownerCtx, "Drop")
	if err != nil {
		t.Fatalf("CreateProject drop: %v", err)
	}
	enc, err := st.EncryptSecret([]byte("https://calendar.example.com/private/token.ics"))
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}

	keepSrc, err := st.CreateCalendarSource(ownerCtx, keepProject.ID, CreateCalendarSourceInput{
		Type:      CalendarSourceTypeICSFeed,
		Name:      "Keep",
		Enabled:   true,
		SecretEnc: enc,
		URLHash:   "hash-keep",
	})
	if err != nil {
		t.Fatalf("CreateCalendarSource keep: %v", err)
	}
	dropSrc, err := st.CreateCalendarSource(ownerCtx, dropProject.ID, CreateCalendarSourceInput{
		Type:      CalendarSourceTypeICSFeed,
		Name:      "Drop",
		Enabled:   true,
		SecretEnc: enc,
		URLHash:   "hash-drop",
	})
	if err != nil {
		t.Fatalf("CreateCalendarSource drop: %v", err)
	}
	fetched := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	for _, src := range []CalendarSource{keepSrc, dropSrc} {
		if err := st.UpsertCalendarFeedSnapshot(ownerCtx, CalendarFeedSnapshot{
			SourceID:   src.ID,
			ETag:       `"v1"`,
			FetchedAt:  fetched,
			Status:     CalendarSnapshotStatusOK,
			EventsJSON: `[{"uid":"a","title":"Pickup"}]`,
		}); err != nil {
			t.Fatalf("Upsert source %d: %v", src.ID, err)
		}
	}

	if err := st.DeleteCalendarFeedSnapshot(ownerCtx, dropSrc.ID); err != nil {
		t.Fatalf("DeleteCalendarFeedSnapshot: %v", err)
	}
	if _, err := st.GetCalendarFeedSnapshot(ownerCtx, dropSrc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted source snapshot err=%v, want ErrNotFound", err)
	}
	if _, err := st.GetCalendarFeedSnapshot(ownerCtx, keepSrc.ID); err != nil {
		t.Fatalf("keep snapshot after source delete: %v", err)
	}

	if err := st.UpsertCalendarFeedSnapshot(ownerCtx, CalendarFeedSnapshot{
		SourceID:   dropSrc.ID,
		ETag:       `"v2"`,
		FetchedAt:  fetched,
		Status:     CalendarSnapshotStatusOK,
		EventsJSON: `[{"uid":"b","title":"Other"}]`,
	}); err != nil {
		t.Fatalf("reseed drop snapshot: %v", err)
	}
	if err := st.DeleteCalendarFeedSnapshotsForProject(ownerCtx, dropProject.ID); err != nil {
		t.Fatalf("DeleteCalendarFeedSnapshotsForProject: %v", err)
	}
	if _, err := st.GetCalendarFeedSnapshot(ownerCtx, dropSrc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("project delete left drop snapshot err=%v", err)
	}
	if _, err := st.GetCalendarFeedSnapshot(ownerCtx, keepSrc.ID); err != nil {
		t.Fatalf("keep snapshot after project delete: %v", err)
	}
}

func TestCalendarFeedSnapshotWritesRequireCurrentConfig(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "calendar-snap-fence@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(ownerCtx, "Fence")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	enc, err := st.EncryptSecret([]byte("https://calendar.example.com/a.ics"))
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	src, err := st.CreateCalendarSource(ownerCtx, project.ID, CreateCalendarSourceInput{
		Type:      CalendarSourceTypeICSFeed,
		Name:      "Family",
		Enabled:   true,
		SecretEnc: enc,
		URLHash:   "hash-a",
	})
	if err != nil {
		t.Fatalf("CreateCalendarSource: %v", err)
	}

	fetched := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	okSnap := CalendarFeedSnapshot{
		SourceID:     src.ID,
		ETag:         `"a"`,
		LastModified: "Mon, 17 Aug 2026 12:00:00 GMT",
		FetchedAt:    fetched,
		Status:       CalendarSnapshotStatusOK,
		EventsJSON:   `[{"uid":"a","title":"Feed A"}]`,
	}
	if err := st.UpsertCalendarFeedSnapshotIfCurrent(ownerCtx, okSnap, "hash-a", "UTC"); err != nil {
		t.Fatalf("matching upsert: %v", err)
	}

	stale := okSnap
	stale.EventsJSON = `[{"uid":"a","title":"Stale A"}]`
	stale.ETag = `"stale"`
	encB, err := st.EncryptSecret([]byte("https://calendar.example.com/b.ics"))
	if err != nil {
		t.Fatalf("EncryptSecret B: %v", err)
	}
	hashB := "hash-b"
	if _, err := st.UpdateCalendarSource(ownerCtx, project.ID, src.ID, UpdateCalendarSourceInput{
		SecretEnc: &encB,
		URLHash:   &hashB,
	}); err != nil {
		t.Fatalf("UpdateCalendarSource: %v", err)
	}
	if err := st.UpsertCalendarFeedSnapshotIfCurrent(ownerCtx, stale, "hash-a", "UTC"); !errors.Is(err, ErrSnapshotSuperseded) {
		t.Fatalf("stale hash upsert err=%v, want ErrSnapshotSuperseded", err)
	}
	if _, err := st.GetCalendarFeedSnapshot(ownerCtx, src.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale writer recreated snapshot: %v", err)
	}

	if err := st.DeleteCalendarFeedSnapshot(ownerCtx, src.ID); err != nil {
		t.Fatalf("DeleteCalendarFeedSnapshot: %v", err)
	}
	if err := st.UpsertCalendarFeedSnapshotIfCurrent(ownerCtx, stale, "hash-a", "UTC"); !errors.Is(err, ErrSnapshotSuperseded) {
		t.Fatalf("stale recreate err=%v, want ErrSnapshotSuperseded", err)
	}
	if _, err := st.GetCalendarFeedSnapshot(ownerCtx, src.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale writer recreated snapshot: %v", err)
	}

	okSnap.ETag = `"b"`
	okSnap.EventsJSON = `[{"uid":"b","title":"Feed B"}]`
	if err := st.UpsertCalendarFeedSnapshotIfCurrent(ownerCtx, okSnap, "hash-b", "UTC"); err != nil {
		t.Fatalf("current hash upsert: %v", err)
	}

	chicago := "America/Chicago"
	if _, err := st.UpdateProjectAgendaSettings(ownerCtx, project.ID, nil, &chicago, nil, nil); err != nil {
		t.Fatalf("UpdateProjectAgendaSettings: %v", err)
	}
	if err := st.TouchCalendarFeedSnapshotIfCurrent(ownerCtx, src.ID, fetched.Add(time.Minute), `"b"`, "", "hash-b", "UTC"); !errors.Is(err, ErrSnapshotSuperseded) {
		t.Fatalf("stale tz touch err=%v, want ErrSnapshotSuperseded", err)
	}
	if _, err := st.GetCalendarFeedSnapshot(ownerCtx, src.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale tz touch recreated snapshot: %v", err)
	}

	// Snapshot was invalidated by timezone change, so Touch cannot update until
	// a current snapshot exists again.
	if err := st.UpsertCalendarFeedSnapshotIfCurrent(ownerCtx, okSnap, "hash-b", chicago); err != nil {
		t.Fatalf("current tz upsert: %v", err)
	}

	if err := st.TouchCalendarFeedSnapshotIfCurrent(ownerCtx, src.ID, fetched.Add(time.Minute), `"b2"`, "", "hash-b", "America/Chicago"); err != nil {
		t.Fatalf("current tz touch: %v", err)
	}
	got, err := st.GetCalendarFeedSnapshot(ownerCtx, src.ID)
	if err != nil {
		t.Fatalf("Get after current tz touch: %v", err)
	}
	if !got.FetchedAt.Equal(fetched.Add(time.Minute)) || got.ETag != `"b2"` {
		t.Fatalf("current tz touch=%+v", got)
	}
}
