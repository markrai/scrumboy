package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"scrumboy/internal/store"
)

func TestAgendaStartOfDayPreference_DoesNotModifyProjectAgendaSettings(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		EncryptionKey:  testEncryptionKey,
	})
	defer cleanup()

	ownerClient := newCookieClient(t)
	ownerJSON := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "agenda-start-of-day-owner@example.com", "password123")
	ownerID := int64(ownerJSON["id"].(float64))
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, &store.StoreOptions{EncryptionKey: testEncryptionKey})

	project, err := st.CreateProject(ctxOwner, "Agenda Pref Board")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := st.UpdateProjectAgendaSettings(ctxOwner, project.ID, boolPtr(true), strPtr("America/New_York"), strPtr("Team calendar"), nil); err != nil {
		t.Fatalf("UpdateProjectAgendaSettings: %v", err)
	}
	before, err := st.GetProjectAgendaSettings(ctxOwner, project.ID)
	if err != nil {
		t.Fatalf("GetProjectAgendaSettings: %v", err)
	}
	sourceCount, err := st.CountCalendarSources(ctxOwner, project.ID)
	if err != nil {
		t.Fatalf("CountCalendarSources: %v", err)
	}

	resp, body := doJSON(t, ownerClient, http.MethodPut, ts.URL+"/api/user/preferences", map[string]any{
		"key":   "agendaStartOfDay",
		"value": "07:30",
	}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT preference: status=%d body=%s", resp.StatusCode, string(body))
	}

	after, err := st.GetProjectAgendaSettings(ctxOwner, project.ID)
	if err != nil {
		t.Fatalf("GetProjectAgendaSettings after: %v", err)
	}
	if after.Enabled != before.Enabled || after.Timezone != before.Timezone || after.Title != before.Title {
		t.Fatalf("project agenda settings changed: before=%+v after=%+v", before, after)
	}
	afterCount, err := st.CountCalendarSources(ctxOwner, project.ID)
	if err != nil {
		t.Fatalf("CountCalendarSources after: %v", err)
	}
	if afterCount != sourceCount {
		t.Fatalf("calendar source count changed: %d -> %d", sourceCount, afterCount)
	}

	var board map[string]any
	resp, body = doJSON(t, ownerClient, http.MethodGet, ts.URL+"/api/board/"+project.Slug, nil, &board)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET board: status=%d body=%s", resp.StatusCode, string(body))
	}
	if strings.Contains(string(body), "agendaStartOfDay") || strings.Contains(string(body), "agendaTimeline") || strings.Contains(string(body), `"timeline"`) {
		t.Fatalf("board payload included start-of-day preference: %s", body)
	}
	agenda, _ := board["agenda"].(map[string]any)
	if _, ok := agenda["timeline"]; ok {
		t.Fatalf("agenda.timeline leaked into board payload: %v", agenda)
	}
	if _, ok := agenda["startOfDay"]; ok {
		t.Fatalf("agenda.startOfDay leaked into board payload: %v", agenda)
	}
}

func TestAgendaStartOfDayPreference_IsPerUser(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "agenda-start-of-day-a@example.com", "password123")

	resp, body := doJSON(t, ownerClient, http.MethodPut, ts.URL+"/api/user/preferences", map[string]any{
		"key":   "agendaStartOfDay",
		"value": "07:30",
	}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT owner preference: status=%d body=%s", resp.StatusCode, string(body))
	}

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "agenda-start-of-day-b@example.com", "password123", "Other")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	otherClient := newCookieClient(t)
	loginUserClient(t, otherClient, ts.URL, other.Email, "password123")

	var ownerPref map[string]any
	resp, body = doJSON(t, ownerClient, http.MethodGet, ts.URL+"/api/user/preferences?key=agendaStartOfDay", nil, &ownerPref)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET owner preference: status=%d body=%s", resp.StatusCode, string(body))
	}
	if ownerPref["value"] != "07:30" {
		t.Fatalf("owner preference=%v, want 07:30", ownerPref["value"])
	}

	var otherPref map[string]any
	resp, body = doJSON(t, otherClient, http.MethodGet, ts.URL+"/api/user/preferences?key=agendaStartOfDay", nil, &otherPref)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET other preference: status=%d body=%s", resp.StatusCode, string(body))
	}
	if otherPref["value"] != "" {
		t.Fatalf("other preference=%v, want empty default", otherPref["value"])
	}
}

func TestAgendaNowLinePreference_DoesNotModifyProjectAgendaSettings(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		EncryptionKey:  testEncryptionKey,
	})
	defer cleanup()

	ownerClient := newCookieClient(t)
	ownerJSON := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "agenda-now-line-owner@example.com", "password123")
	ownerID := int64(ownerJSON["id"].(float64))
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, &store.StoreOptions{EncryptionKey: testEncryptionKey})

	project, err := st.CreateProject(ctxOwner, "Agenda Now Line Pref Board")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := st.UpdateProjectAgendaSettings(ctxOwner, project.ID, boolPtr(true), strPtr("America/New_York"), strPtr("Team calendar"), nil); err != nil {
		t.Fatalf("UpdateProjectAgendaSettings: %v", err)
	}
	before, err := st.GetProjectAgendaSettings(ctxOwner, project.ID)
	if err != nil {
		t.Fatalf("GetProjectAgendaSettings: %v", err)
	}

	resp, body := doJSON(t, ownerClient, http.MethodPut, ts.URL+"/api/user/preferences", map[string]any{
		"key":   "agendaNowLine",
		"value": "prominent",
	}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT now-line preference: status=%d body=%s", resp.StatusCode, string(body))
	}

	after, err := st.GetProjectAgendaSettings(ctxOwner, project.ID)
	if err != nil {
		t.Fatalf("GetProjectAgendaSettings after: %v", err)
	}
	if after.Enabled != before.Enabled || after.Timezone != before.Timezone || after.Title != before.Title {
		t.Fatalf("project agenda settings changed: before=%+v after=%+v", before, after)
	}
}

func TestAgendaNowLinePreference_IsPerUser(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "agenda-now-line-a@example.com", "password123")

	resp, body := doJSON(t, ownerClient, http.MethodPut, ts.URL+"/api/user/preferences", map[string]any{
		"key":   "agendaNowLine",
		"value": "prominent",
	}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT owner preference: status=%d body=%s", resp.StatusCode, string(body))
	}

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "agenda-now-line-b@example.com", "password123", "Other")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	otherClient := newCookieClient(t)
	loginUserClient(t, otherClient, ts.URL, other.Email, "password123")

	var ownerPref map[string]any
	resp, body = doJSON(t, ownerClient, http.MethodGet, ts.URL+"/api/user/preferences?key=agendaNowLine", nil, &ownerPref)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET owner preference: status=%d body=%s", resp.StatusCode, string(body))
	}
	if ownerPref["value"] != "prominent" {
		t.Fatalf("owner preference=%v, want prominent", ownerPref["value"])
	}

	var otherPref map[string]any
	resp, body = doJSON(t, otherClient, http.MethodGet, ts.URL+"/api/user/preferences?key=agendaNowLine", nil, &otherPref)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET other preference: status=%d body=%s", resp.StatusCode, string(body))
	}
	if otherPref["value"] != "" {
		t.Fatalf("other preference=%v, want empty default", otherPref["value"])
	}
}
