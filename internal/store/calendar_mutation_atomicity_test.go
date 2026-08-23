package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUpdateProjectAgendaSettingsTimezoneChangeDeletesSnapshot(t *testing.T) {
	st, ownerCtx, project, src := calendarMutationFixture(t)

	if err := st.UpsertCalendarFeedSnapshot(ownerCtx, CalendarFeedSnapshot{
		SourceID:   src.ID,
		FetchedAt:  time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC),
		Status:     CalendarSnapshotStatusOK,
		EventsJSON: `[]`,
	}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	if _, err := st.UpdateProjectAgendaSettings(ownerCtx, project.ID, nil, strPtr("America/Chicago"), nil, nil); err != nil {
		t.Fatalf("timezone update: %v", err)
	}
	if _, err := st.GetCalendarFeedSnapshot(ownerCtx, src.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("snapshot after timezone change = %v, want ErrNotFound", err)
	}
	settings, err := st.GetProjectAgendaSettings(ownerCtx, project.ID)
	if err != nil {
		t.Fatalf("GetProjectAgendaSettings: %v", err)
	}
	if settings.Timezone != "America/Chicago" {
		t.Fatalf("timezone=%q", settings.Timezone)
	}
}

func TestUpdateProjectAgendaSettingsTimezoneChangeRollsBackWhenSnapshotDeleteFails(t *testing.T) {
	st, ownerCtx, project, src := calendarMutationFixture(t)
	seed := CalendarFeedSnapshot{
		SourceID:   src.ID,
		ETag:       `"keep"`,
		FetchedAt:  time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC),
		Status:     CalendarSnapshotStatusOK,
		EventsJSON: `[{"uid":"a"}]`,
	}
	if err := st.UpsertCalendarFeedSnapshot(ownerCtx, seed); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	if _, err := st.db.Exec(`CREATE TRIGGER fail_snapshot_delete BEFORE DELETE ON calendar_feed_snapshots BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatalf("install trigger: %v", err)
	}

	_, err := st.UpdateProjectAgendaSettings(ownerCtx, project.ID, nil, strPtr("America/Chicago"), nil, nil)
	if err == nil {
		t.Fatal("expected timezone update to fail")
	}
	settings, err := st.GetProjectAgendaSettings(ownerCtx, project.ID)
	if err != nil {
		t.Fatalf("GetProjectAgendaSettings: %v", err)
	}
	if settings.Timezone != "UTC" {
		t.Fatalf("timezone mutated after failed delete = %q", settings.Timezone)
	}
	got, err := st.GetCalendarFeedSnapshot(ownerCtx, src.ID)
	if err != nil {
		t.Fatalf("snapshot missing after rollback: %v", err)
	}
	if got.ETag != seed.ETag || got.EventsJSON != seed.EventsJSON {
		t.Fatalf("snapshot mutated = %+v", got)
	}
}

func TestUpdateCalendarSourceURLChangeDeletesSnapshot(t *testing.T) {
	st, ownerCtx, project, src := calendarMutationFixture(t)
	if err := st.UpsertCalendarFeedSnapshot(ownerCtx, CalendarFeedSnapshot{
		SourceID:   src.ID,
		FetchedAt:  time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC),
		Status:     CalendarSnapshotStatusOK,
		EventsJSON: `[]`,
	}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	secret := "v1:new"
	hash := "hash-new"
	kind := CalendarHostKindGoogle
	if _, err := st.UpdateCalendarSource(ownerCtx, project.ID, src.ID, UpdateCalendarSourceInput{
		SecretEnc: &secret,
		URLHash:   &hash,
		HostKind:  &kind,
	}); err != nil {
		t.Fatalf("URL update: %v", err)
	}
	if _, err := st.GetCalendarFeedSnapshot(ownerCtx, src.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("snapshot after URL change = %v, want ErrNotFound", err)
	}
}

func TestUpdateCalendarSourceURLChangeRollsBackWhenSnapshotDeleteFails(t *testing.T) {
	st, ownerCtx, project, src := calendarMutationFixture(t)
	if err := st.UpsertCalendarFeedSnapshot(ownerCtx, CalendarFeedSnapshot{
		SourceID:   src.ID,
		FetchedAt:  time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC),
		Status:     CalendarSnapshotStatusOK,
		EventsJSON: `[]`,
	}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	if _, err := st.db.Exec(`CREATE TRIGGER fail_snapshot_delete BEFORE DELETE ON calendar_feed_snapshots BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatalf("install trigger: %v", err)
	}

	secret := "v1:new"
	hash := "hash-new"
	kind := CalendarHostKindGoogle
	if _, err := st.UpdateCalendarSource(ownerCtx, project.ID, src.ID, UpdateCalendarSourceInput{
		SecretEnc: &secret,
		URLHash:   &hash,
		HostKind:  &kind,
	}); err == nil {
		t.Fatal("expected URL update to fail")
	}
	got, err := st.GetCalendarSource(ownerCtx, project.ID, src.ID)
	if err != nil {
		t.Fatalf("GetCalendarSource: %v", err)
	}
	if got.URLHash != src.URLHash || got.SecretEnc != src.SecretEnc || got.HostKind != src.HostKind {
		t.Fatalf("URL config mutated after failed delete: %+v", got)
	}
	if _, err := st.GetCalendarFeedSnapshot(ownerCtx, src.ID); err != nil {
		t.Fatalf("snapshot missing after rollback: %v", err)
	}
}

func TestUpdateProjectBoardSettingsInvalidTimezoneLeavesSprintFieldsUnchanged(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "board-settings@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(ownerCtx, "Board Settings")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := st.UpdateProjectSprintsEnabled(ownerCtx, project.ID, user.ID, true); err != nil {
		t.Fatalf("enable sprints: %v", err)
	}

	weeks := 1
	enabled := false
	_, err = st.UpdateProjectBoardSettings(ownerCtx, project.ID, user.ID, ProjectBoardSettingsPatch{
		DefaultSprintWeeks: &weeks,
		SprintsEnabled:     &enabled,
		AgendaTimezone:     strPtr("Not/A_Zone"),
	})
	if !errors.Is(err, ErrValidation) || ErrorReason(err) != ReasonInvalidAgendaTimezone {
		t.Fatalf("mixed patch error = %v, want ErrValidation/%s", err, ReasonInvalidAgendaTimezone)
	}

	got, err := st.GetProject(ownerCtx, project.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.DefaultSprintWeeks != 2 {
		t.Fatalf("defaultSprintWeeks=%d, want 2", got.DefaultSprintWeeks)
	}
	if !got.SprintsEnabled {
		t.Fatal("sprintsEnabled mutated")
	}
	settings, err := st.GetProjectAgendaSettings(ownerCtx, project.ID)
	if err != nil {
		t.Fatalf("GetProjectAgendaSettings: %v", err)
	}
	if settings.Timezone != "UTC" {
		t.Fatalf("timezone mutated = %q", settings.Timezone)
	}
}

func TestUpdateProjectBoardSettingsUnchangedSprintSkipsAudit(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "board-settings-audit@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(ownerCtx, "Board Settings Audit")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	var before int
	if err := st.db.QueryRowContext(ownerCtx, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action IN ('project_default_sprint_weeks_updated', 'project_sprints_enabled_updated')`, project.ID).Scan(&before); err != nil {
		t.Fatalf("count audits: %v", err)
	}
	weeks := project.DefaultSprintWeeks
	enabled := project.SprintsEnabled
	title := "Team calendar"
	if _, err := st.UpdateProjectBoardSettings(ownerCtx, project.ID, user.ID, ProjectBoardSettingsPatch{
		DefaultSprintWeeks: &weeks,
		SprintsEnabled:     &enabled,
		AgendaTitle:        &title,
	}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	var after int
	if err := st.db.QueryRowContext(ownerCtx, `SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action IN ('project_default_sprint_weeks_updated', 'project_sprints_enabled_updated')`, project.ID).Scan(&after); err != nil {
		t.Fatalf("count audits after: %v", err)
	}
	if after != before {
		t.Fatalf("unchanged sprint fields wrote audit: before=%d after=%d", before, after)
	}
}

func TestUpdateProjectBoardSettingsTimezoneChangeDeletesSnapshot(t *testing.T) {
	st, ownerCtx, project, src := calendarMutationFixture(t)
	userID, ok := UserIDFromContext(ownerCtx)
	if !ok {
		t.Fatal("missing user")
	}
	if err := st.UpsertCalendarFeedSnapshot(ownerCtx, CalendarFeedSnapshot{
		SourceID:   src.ID,
		FetchedAt:  time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC),
		Status:     CalendarSnapshotStatusOK,
		EventsJSON: `[]`,
	}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	weeks := 1
	if _, err := st.UpdateProjectBoardSettings(ownerCtx, project.ID, userID, ProjectBoardSettingsPatch{
		DefaultSprintWeeks: &weeks,
		AgendaTimezone:     strPtr("America/Chicago"),
	}); err != nil {
		t.Fatalf("mixed patch: %v", err)
	}
	if _, err := st.GetCalendarFeedSnapshot(ownerCtx, src.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("snapshot after mixed timezone change = %v, want ErrNotFound", err)
	}
	got, err := st.GetProject(ownerCtx, project.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.DefaultSprintWeeks != 1 {
		t.Fatalf("defaultSprintWeeks=%d, want 1", got.DefaultSprintWeeks)
	}
}

func calendarMutationFixture(t *testing.T) (*Store, context.Context, Project, CalendarSource) {
	t.Helper()
	st, cleanup := newTestStoreWith2FA(t)
	t.Cleanup(cleanup)

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "calendar-atomic@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(ownerCtx, "Calendar Atomic")
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
		HostKind:  CalendarHostKindOther,
	})
	if err != nil {
		t.Fatalf("CreateCalendarSource: %v", err)
	}
	return st, ownerCtx, project, src
}
