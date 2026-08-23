package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/version"
)

func TestBackupExportsAgendaFlagsAndOmitsCalendarSources(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "backup-agenda@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(ownerCtx, "Agenda Backup")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	tz := "America/New_York"
	title := "Family calendar"
	color := "#aabbcc"
	if _, err := st.UpdateProjectAgendaSettings(ownerCtx, project.ID, boolPtr(true), &tz, &title, &color); err != nil {
		t.Fatalf("UpdateProjectAgendaSettings: %v", err)
	}
	secretURL := "https://calendar.example.com/private/super-secret-token.ics"
	enc, err := st.EncryptSecret([]byte(secretURL))
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	if _, err := st.CreateCalendarSource(ownerCtx, project.ID, CreateCalendarSourceInput{
		Name:      "Family",
		Enabled:   true,
		SecretEnc: enc,
		URLHash:   "hash-family",
	}); err != nil {
		t.Fatalf("CreateCalendarSource: %v", err)
	}

	data, err := st.ExportAllProjects(ownerCtx, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}
	if len(data.Projects) != 1 {
		t.Fatalf("exported projects = %d, want 1", len(data.Projects))
	}
	exported := data.Projects[0]
	if exported.AgendaEnabled == nil || !*exported.AgendaEnabled {
		t.Fatalf("exported agendaEnabled=%v, want true", exported.AgendaEnabled)
	}
	if exported.AgendaTimezone != "America/New_York" {
		t.Fatalf("exported agendaTimezone=%q", exported.AgendaTimezone)
	}
	if exported.AgendaTitle != "Family calendar" {
		t.Fatalf("exported agendaTitle=%q", exported.AgendaTitle)
	}
	if exported.AgendaColor != "#aabbcc" {
		t.Fatalf("exported agendaColor=%q", exported.AgendaColor)
	}

	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}
	body := string(raw)
	if strings.Contains(body, "calendarSources") {
		t.Fatal("export contained calendarSources")
	}
	if strings.Contains(body, secretURL) || strings.Contains(body, "super-secret-token") {
		t.Fatal("export leaked calendar URL")
	}

	if _, err := st.ImportProjects(ownerCtx, data, ModeFull, "replace"); err != nil {
		t.Fatalf("replace import: %v", err)
	}

	st2, cleanup2 := newTestStoreWith2FA(t)
	defer cleanup2()
	user2, err := st2.BootstrapUser(ctx, "backup-agenda-import@example.com", "password123", "Importer")
	if err != nil {
		t.Fatalf("BootstrapUser import store: %v", err)
	}
	importCtx := WithUserID(ctx, user2.ID)
	if _, err := st2.ImportProjects(importCtx, data, ModeFull, "replace"); err != nil {
		t.Fatalf("import into empty store: %v", err)
	}
	imported, err := st2.GetProjectBySlug(importCtx, project.Slug)
	if err != nil {
		t.Fatalf("GetProjectBySlug: %v", err)
	}
	settings, err := st2.GetProjectAgendaSettings(importCtx, imported.ID)
	if err != nil {
		t.Fatalf("GetProjectAgendaSettings: %v", err)
	}
	if !settings.Enabled || settings.Timezone != "America/New_York" || settings.Title != "Family calendar" || settings.Color != "#aabbcc" {
		t.Fatalf("imported settings = %+v", settings)
	}
	count, err := st2.CountCalendarSources(importCtx, imported.ID)
	if err != nil {
		t.Fatalf("CountCalendarSources: %v", err)
	}
	if count != 0 {
		t.Fatalf("imported calendar sources = %d, want 0", count)
	}
}

func TestBackupRejectsInvalidAgendaTimezone(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "backup-bad-tz@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	enabled := true
	now := time.Now().UTC()
	data := &ExportData{
		Version:    version.ExportFormatVersion,
		Mode:       ModeFull.String(),
		Scope:      "full",
		ExportedAt: now,
		Projects: []ProjectExport{{
			Slug:           "bad-tz",
			Name:           "Bad TZ",
			EstimationMode: EstimationModeModifiedFibonacci,
			AgendaEnabled:  &enabled,
			AgendaTimezone: "Not/A_Zone",
			CreatedAt:      now,
			UpdatedAt:      now,
			WorkflowColumns: []WorkflowColumnExport{
				{Key: "todo", Name: "To Do", Color: "#94a3b8", Position: 0, IsDone: false},
				{Key: "done", Name: "Done", Color: "#ef4444", Position: 1, IsDone: true},
			},
		}},
	}
	if _, err := st.ImportProjects(ownerCtx, data, ModeFull, "replace"); err == nil || !errors.Is(err, ErrValidation) {
		t.Fatalf("import err=%v, want ErrValidation", err)
	}
}

func TestBackupMissingAgendaColorKeepsDefault(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "backup-default-color@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	enabled := true
	now := time.Now().UTC()
	data := &ExportData{
		Version:    version.ExportFormatVersion,
		Mode:       ModeFull.String(),
		Scope:      "full",
		ExportedAt: now,
		Projects: []ProjectExport{{
			Slug:           "legacy-agenda-color",
			Name:           "Legacy Color",
			EstimationMode: EstimationModeModifiedFibonacci,
			AgendaEnabled:  &enabled,
			AgendaTimezone: "UTC",
			AgendaTitle:    "Team calendar",
			CreatedAt:      now,
			UpdatedAt:      now,
			WorkflowColumns: []WorkflowColumnExport{
				{Key: "todo", Name: "To Do", Color: "#94a3b8", Position: 0, IsDone: false},
				{Key: "done", Name: "Done", Color: "#ef4444", Position: 1, IsDone: true},
			},
		}},
	}
	if _, err := st.ImportProjects(ownerCtx, data, ModeFull, "replace"); err != nil {
		t.Fatalf("import: %v", err)
	}
	imported, err := st.GetProjectBySlug(ownerCtx, "legacy-agenda-color")
	if err != nil {
		t.Fatalf("GetProjectBySlug: %v", err)
	}
	settings, err := st.GetProjectAgendaSettings(ownerCtx, imported.ID)
	if err != nil {
		t.Fatalf("GetProjectAgendaSettings: %v", err)
	}
	if settings.Color != DefaultAgendaColor || settings.Title != "Team calendar" {
		t.Fatalf("legacy import settings = %+v", settings)
	}
}

func TestBackupRejectsInvalidAgendaColor(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "backup-bad-color@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	enabled := true
	now := time.Now().UTC()
	data := &ExportData{
		Version:    version.ExportFormatVersion,
		Mode:       ModeFull.String(),
		Scope:      "full",
		ExportedAt: now,
		Projects: []ProjectExport{{
			Slug:           "bad-color",
			Name:           "Bad Color",
			EstimationMode: EstimationModeModifiedFibonacci,
			AgendaEnabled:  &enabled,
			AgendaTimezone: "UTC",
			AgendaColor:    "indigo",
			CreatedAt:      now,
			UpdatedAt:      now,
			WorkflowColumns: []WorkflowColumnExport{
				{Key: "todo", Name: "To Do", Color: "#94a3b8", Position: 0, IsDone: false},
				{Key: "done", Name: "Done", Color: "#ef4444", Position: 1, IsDone: true},
			},
		}},
	}
	if _, err := st.ImportProjects(ownerCtx, data, ModeFull, "replace"); err == nil || !errors.Is(err, ErrValidation) {
		t.Fatalf("import err=%v, want ErrValidation", err)
	}
}
