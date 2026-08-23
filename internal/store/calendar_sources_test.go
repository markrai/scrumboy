package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"scrumboy/internal/crypto"
)

func TestCalendarSourceEncryptDecryptAndUniqueness(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "calendar-src@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(ownerCtx, "Calendar Sources")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	enc, err := st.EncryptSecret([]byte("https://calendar.example.com/private/token.ics"))
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	if !strings.HasPrefix(enc, "v1:") {
		t.Fatalf("ciphertext prefix = %q, want v1:", enc)
	}
	plain, err := st.DecryptSecret(enc)
	if err != nil {
		t.Fatalf("DecryptSecret: %v", err)
	}
	if string(plain) != "https://calendar.example.com/private/token.ics" {
		t.Fatalf("roundtrip = %q", plain)
	}

	first, err := st.CreateCalendarSource(ownerCtx, project.ID, CreateCalendarSourceInput{
		Type:      CalendarSourceTypeICSFeed,
		Name:      "Family",
		Enabled:   true,
		SecretEnc: enc,
		URLHash:   "hash-family",
	})
	if err != nil {
		t.Fatalf("CreateCalendarSource: %v", err)
	}
	if first.Name != "Family" || first.Type != CalendarSourceTypeICSFeed || !first.Enabled {
		t.Fatalf("created = %+v", first)
	}
	if first.HostKind != CalendarHostKindOther {
		t.Fatalf("default host_kind=%q, want other", first.HostKind)
	}

	_, err = st.CreateCalendarSource(ownerCtx, project.ID, CreateCalendarSourceInput{
		Name:      "Duplicate",
		Enabled:   true,
		SecretEnc: enc,
		URLHash:   "hash-family",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate hash error = %v, want ErrConflict", err)
	}

	for i := 0; i < MaxCalendarSources-1; i++ {
		if _, err := st.CreateCalendarSource(ownerCtx, project.ID, CreateCalendarSourceInput{
			Name:      "Feed",
			Enabled:   true,
			SecretEnc: enc,
			URLHash:   "hash-" + strings.Repeat("x", i+1),
		}); err != nil {
			t.Fatalf("CreateCalendarSource extra %d: %v", i, err)
		}
	}
	count, err := st.CountCalendarSources(ownerCtx, project.ID)
	if err != nil {
		t.Fatalf("CountCalendarSources: %v", err)
	}
	if count != MaxCalendarSources {
		t.Fatalf("count = %d, want %d", count, MaxCalendarSources)
	}

	listed, err := st.ListCalendarSources(ownerCtx, project.ID)
	if err != nil {
		t.Fatalf("ListCalendarSources: %v", err)
	}
	if len(listed) != MaxCalendarSources {
		t.Fatalf("listed = %d, want %d", len(listed), MaxCalendarSources)
	}

	if _, err := st.CreateCalendarSource(ownerCtx, project.ID, CreateCalendarSourceInput{
		Name:      "Overflow",
		Enabled:   true,
		SecretEnc: enc,
		URLHash:   "hash-overflow",
	}); !errors.Is(err, ErrValidation) || ErrorReason(err) != ReasonCalendarSourceLimit {
		t.Fatalf("ninth create error = %v, want ErrValidation/%s", err, ReasonCalendarSourceLimit)
	}
	count, err = st.CountCalendarSources(ownerCtx, project.ID)
	if err != nil {
		t.Fatalf("CountCalendarSources after overflow: %v", err)
	}
	if count != MaxCalendarSources {
		t.Fatalf("count after overflow = %d, want %d", count, MaxCalendarSources)
	}

	settings, err := st.UpdateProjectAgendaSettings(ownerCtx, project.ID, boolPtr(true), strPtr("America/New_York"), nil, nil)
	if err != nil {
		t.Fatalf("UpdateProjectAgendaSettings: %v", err)
	}
	if !settings.Enabled || settings.Timezone != "America/New_York" || settings.Title != DefaultAgendaTitle || settings.Color != DefaultAgendaColor {
		t.Fatalf("settings = %+v", settings)
	}

	custom := "Family calendar"
	settings, err = st.UpdateProjectAgendaSettings(ownerCtx, project.ID, nil, nil, &custom, nil)
	if err != nil {
		t.Fatalf("UpdateProjectAgendaSettings title: %v", err)
	}
	if settings.Title != custom || settings.Timezone != "America/New_York" {
		t.Fatalf("after title update = %+v", settings)
	}
	empty := "   "
	if _, err := st.UpdateProjectAgendaSettings(ownerCtx, project.ID, nil, nil, &empty, nil); err == nil || !errors.Is(err, ErrValidation) {
		t.Fatalf("empty title error = %v, want ErrValidation", err)
	}
	settings, err = st.UpdateProjectAgendaSettings(ownerCtx, project.ID, nil, strPtr("UTC"), nil, nil)
	if err != nil {
		t.Fatalf("timezone-only update: %v", err)
	}
	if settings.Title != custom || settings.Timezone != "UTC" {
		t.Fatalf("timezone-only mutated title = %+v", settings)
	}

	if err := st.DeleteCalendarSource(ownerCtx, project.ID, first.ID); err != nil {
		t.Fatalf("DeleteCalendarSource: %v", err)
	}
	if _, err := st.GetCalendarSource(ownerCtx, project.ID, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
}

func TestCalendarSourceHostKindFenceDoesNotBumpUpdatedAt(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "calendar-hostkind@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(ownerCtx, "Host Kind")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	enc, err := st.EncryptSecret([]byte("https://calendar.google.com/calendar/ical/x/basic.ics"))
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	src, err := st.CreateCalendarSource(ownerCtx, project.ID, CreateCalendarSourceInput{
		Type:      CalendarSourceTypeICSFeed,
		Name:      "Family",
		Enabled:   true,
		SecretEnc: enc,
		URLHash:   "hash-google",
		HostKind:  CalendarHostKindOther,
	})
	if err != nil {
		t.Fatalf("CreateCalendarSource: %v", err)
	}
	if src.HostKind != CalendarHostKindOther {
		t.Fatalf("host_kind=%q, want other", src.HostKind)
	}
	updatedAt := src.UpdatedAt

	changed, err := st.UpdateCalendarSourceHostKindIfURLHashCurrent(ownerCtx, src.ID, "hash-google", CalendarHostKindGoogle)
	if err != nil {
		t.Fatalf("fenced update: %v", err)
	}
	if !changed {
		t.Fatal("expected host_kind change")
	}
	got, err := st.GetCalendarSource(ownerCtx, project.ID, src.ID)
	if err != nil {
		t.Fatalf("GetCalendarSource: %v", err)
	}
	if got.HostKind != CalendarHostKindGoogle {
		t.Fatalf("host_kind=%q, want google", got.HostKind)
	}
	if !got.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("updated_at bumped: %v -> %v", updatedAt, got.UpdatedAt)
	}

	changed, err = st.UpdateCalendarSourceHostKindIfURLHashCurrent(ownerCtx, src.ID, "hash-google", CalendarHostKindGoogle)
	if err != nil {
		t.Fatalf("idempotent fenced update: %v", err)
	}
	if changed {
		t.Fatal("same kind should not report changed")
	}

	changed, err = st.UpdateCalendarSourceHostKindIfURLHashCurrent(ownerCtx, src.ID, "hash-stale", CalendarHostKindApple)
	if err != nil {
		t.Fatalf("stale hash fenced update: %v", err)
	}
	if changed {
		t.Fatal("stale url_hash must not change host_kind")
	}
	got, err = st.GetCalendarSource(ownerCtx, project.ID, src.ID)
	if err != nil {
		t.Fatalf("Get after stale: %v", err)
	}
	if got.HostKind != CalendarHostKindGoogle {
		t.Fatalf("stale write mutated host_kind=%q", got.HostKind)
	}
}

func TestEncryptSecretWithoutKey(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	if _, err := st.EncryptSecret([]byte("https://example.com/feed.ics")); !errors.Is(err, ErrEncryptionNotConfigured) {
		t.Fatalf("EncryptSecret without key = %v, want ErrEncryptionNotConfigured", err)
	}
}

func TestCalendarSourceEncryptSecretUsesAESGCMFraming(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	enc, err := st.EncryptSecret([]byte("ics-url"))
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	key, err := crypto.DecodeKey("YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY=")
	if err != nil {
		t.Fatalf("DecodeKey: %v", err)
	}
	plain, err := crypto.DecryptSecret(key, enc)
	if err != nil {
		t.Fatalf("crypto.DecryptSecret: %v", err)
	}
	if string(plain) != "ics-url" {
		t.Fatalf("plain = %q", plain)
	}
}

func TestProjectAgendaColorPersistsAndValidates(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "agenda-color@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(ownerCtx, "Agenda Color")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	settings, err := st.GetProjectAgendaSettings(ownerCtx, project.ID)
	if err != nil {
		t.Fatalf("GetProjectAgendaSettings: %v", err)
	}
	if settings.Color != DefaultAgendaColor {
		t.Fatalf("default color = %q, want %q", settings.Color, DefaultAgendaColor)
	}

	custom := "#aabbcc"
	settings, err = st.UpdateProjectAgendaSettings(ownerCtx, project.ID, nil, nil, nil, &custom)
	if err != nil {
		t.Fatalf("UpdateProjectAgendaSettings color: %v", err)
	}
	if settings.Color != custom {
		t.Fatalf("after color update = %+v", settings)
	}

	title := "Kept"
	settings, err = st.UpdateProjectAgendaSettings(ownerCtx, project.ID, nil, nil, &title, nil)
	if err != nil {
		t.Fatalf("title-only update: %v", err)
	}
	if settings.Color != custom || settings.Title != title {
		t.Fatalf("title-only mutated color = %+v", settings)
	}

	for _, bad := range []string{"indigo", "  ", "#fff", "#GGGGGG"} {
		if _, err := st.UpdateProjectAgendaSettings(ownerCtx, project.ID, nil, nil, nil, strPtr(bad)); err == nil || !errors.Is(err, ErrValidation) {
			t.Fatalf("invalid color %q error = %v, want ErrValidation", bad, err)
		}
	}

	got, err := st.GetProjectAgendaSettings(ownerCtx, project.ID)
	if err != nil {
		t.Fatalf("Get after invalid: %v", err)
	}
	if got.Color != custom {
		t.Fatalf("invalid color mutated stored value = %+v", got)
	}
}

func TestUpdateProjectAgendaSettingsRejectsInvalidIANATimezone(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "agenda-tz@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(ownerCtx, "Agenda TZ")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if _, err := st.UpdateProjectAgendaSettings(ownerCtx, project.ID, nil, strPtr("Not/A_Zone"), nil, nil); !errors.Is(err, ErrValidation) || ErrorReason(err) != ReasonInvalidAgendaTimezone {
		t.Fatalf("invalid timezone error = %v, want ErrValidation/%s", err, ReasonInvalidAgendaTimezone)
	}
	got, err := st.GetProjectAgendaSettings(ownerCtx, project.ID)
	if err != nil {
		t.Fatalf("GetProjectAgendaSettings: %v", err)
	}
	if got.Timezone != "UTC" {
		t.Fatalf("invalid timezone mutated stored value = %+v", got)
	}
}

func boolPtr(v bool) *bool    { return &v }
func strPtr(v string) *string { return &v }
