package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"scrumboy/internal/store"
)

const calendarSecretURL = "https://calendar.example.com/private/super-secret-token.ics"

func TestCalendarSources_MaintainerCRUDRedactsURL(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		EncryptionKey:  testEncryptionKey,
	})
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "calendar-owner@example.com", "password123")

	var project struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "Calendar Board"}, &project)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, body = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+project.Slug+"/settings", map[string]any{
		"agendaEnabled":  true,
		"agendaTimezone": "America/New_York",
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch agenda settings: status=%d body=%s", resp.StatusCode, string(body))
	}
	if strings.Contains(string(body), calendarSecretURL) || strings.Contains(string(body), "super-secret-token") {
		t.Fatal("settings response leaked calendar URL")
	}

	var created struct {
		ID            int64  `json:"id"`
		Name          string `json:"name"`
		Type          string `json:"type"`
		Enabled       bool   `json:"enabled"`
		URLConfigured bool   `json:"urlConfigured"`
		URLPreview    string `json:"urlPreview"`
	}
	resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/calendar-sources", map[string]any{
		"name": "Family",
		"url":  calendarSecretURL,
	}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create source: status=%d body=%s", resp.StatusCode, string(body))
	}
	if !created.URLConfigured || created.URLPreview != "https://calendar.example.com/…" {
		t.Fatalf("created = %+v", created)
	}
	if strings.Contains(string(body), "super-secret-token") || strings.Contains(string(body), calendarSecretURL) {
		t.Fatal("create response leaked calendar URL")
	}

	var listed struct {
		AgendaEnabled  bool   `json:"agendaEnabled"`
		AgendaTimezone string `json:"agendaTimezone"`
		AgendaTitle    string `json:"agendaTitle"`
		AgendaColor    string `json:"agendaColor"`
		Sources        []struct {
			ID         int64  `json:"id"`
			URLPreview string `json:"urlPreview"`
		} `json:"sources"`
	}
	resp, body = doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"/calendar-sources", nil, &listed)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list sources: status=%d body=%s", resp.StatusCode, string(body))
	}
	if !listed.AgendaEnabled || listed.AgendaTimezone != "America/New_York" || listed.AgendaTitle != "Agenda" || listed.AgendaColor != store.DefaultAgendaColor || len(listed.Sources) != 1 {
		t.Fatalf("listed = %+v", listed)
	}
	if strings.Contains(string(body), "super-secret-token") {
		t.Fatal("list response leaked calendar URL")
	}

	var patched map[string]any
	resp, body = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+project.Slug+"/settings", map[string]any{
		"agendaTitle": "Team calendar",
	}, &patched)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch title: status=%d body=%s", resp.StatusCode, string(body))
	}
	if patched["agendaTitle"] != "Team calendar" {
		t.Fatalf("patched=%v", patched)
	}
	resp, body = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+project.Slug+"/settings", map[string]any{
		"agendaColor": "#aabbcc",
	}, &patched)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch color: status=%d body=%s", resp.StatusCode, string(body))
	}
	if patched["agendaColor"] != "#aabbcc" {
		t.Fatalf("patched color=%v", patched)
	}
	resp, body = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+project.Slug+"/settings", map[string]any{
		"agendaColor": "indigo",
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid color: status=%d body=%s", resp.StatusCode, string(body))
	}
	resp, body = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+project.Slug+"/settings", map[string]any{
		"agendaTitle": "  ",
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty title: status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/calendar-sources", map[string]any{
		"name": "Duplicate",
		"url":  calendarSecretURL,
	}, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate: status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, body = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+project.Slug+"/calendar-sources/"+strconv.FormatInt(created.ID, 10), map[string]any{
		"enabled": false,
		"name":    "Family Shared",
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch source: status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, body = doJSON(t, client, http.MethodDelete, ts.URL+"/api/board/"+project.Slug+"/calendar-sources/"+strconv.FormatInt(created.ID, 10), nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete source: status=%d body=%s", resp.StatusCode, string(body))
	}

	st := store.New(sqlDB, &store.StoreOptions{EncryptionKey: testEncryptionKey})
	count, err := st.CountCalendarSources(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("CountCalendarSources: %v", err)
	}
	if count != 0 {
		t.Fatalf("count after delete = %d", count)
	}
}

func TestCalendarSources_AuthorizationMatrix(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		EncryptionKey:  testEncryptionKey,
	})
	defer cleanup()

	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "calendar-auth-owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))

	st := store.New(sqlDB, &store.StoreOptions{EncryptionKey: testEncryptionKey})
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctxOwner, "Calendar Auth")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	contributor, err := st.CreateUser(context.Background(), "calendar-contrib@example.com", "password123", "Contributor")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, ownerID, project.ID, contributor.ID, store.RoleContributor); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}
	contributorClient := newCookieClient(t)
	loginUserClient(t, contributorClient, ts.URL, "calendar-contrib@example.com", "password123")

	outsiderClient := newCookieClient(t)
	_, err = st.CreateUser(context.Background(), "calendar-outsider@example.com", "password123", "Outsider")
	if err != nil {
		t.Fatalf("CreateUser outsider: %v", err)
	}
	loginUserClient(t, outsiderClient, ts.URL, "calendar-outsider@example.com", "password123")

	anonClient := newCookieClient(t)

	url := ts.URL + "/api/board/" + project.Slug + "/calendar-sources"
	payload := map[string]any{"name": "Family", "url": calendarSecretURL}

	resp, body := doJSON(t, contributorClient, http.MethodPost, url, payload, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("contributor: status=%d body=%s", resp.StatusCode, string(body))
	}
	resp, body = doJSON(t, outsiderClient, http.MethodPost, url, payload, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("outsider: status=%d body=%s", resp.StatusCode, string(body))
	}
	resp, body = doJSON(t, anonClient, http.MethodPost, url, payload, nil)
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("anonymous: status=%d body=%s", resp.StatusCode, string(body))
	}

	tempBoard, err := st.CreateAnonymousBoard(ctxOwner)
	if err != nil {
		t.Fatalf("CreateTemporaryBoard: %v", err)
	}
	resp, body = doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/board/"+tempBoard.Slug+"/calendar-sources", payload, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("temp board: status=%d body=%s", resp.StatusCode, string(body))
	}
}

func TestCalendarSources_WithoutEncryptionKeyReturns503(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "calendar-nokey@example.com", "password123")
	var project struct {
		Slug string `json:"slug"`
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "No Key"}, &project)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: status=%d body=%s", resp.StatusCode, string(body))
	}

	var envelope apiErrorEnvelope
	resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/calendar-sources", map[string]any{
		"name": "Family",
		"url":  calendarSecretURL,
	}, &envelope)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(body))
	}
	if envelope.Error.Code != "SERVICE_UNAVAILABLE" {
		t.Fatalf("code=%q body=%s", envelope.Error.Code, string(body))
	}
	if strings.Contains(envelope.Error.Message, "Two-factor") {
		t.Fatal("503 used 2FA copy")
	}
	if !strings.Contains(envelope.Error.Message, "Calendar feeds") {
		t.Fatalf("unexpected 503 message %q", envelope.Error.Message)
	}
}

func TestCalendarSources_InvalidURLRejected(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		EncryptionKey:  testEncryptionKey,
	})
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "calendar-url@example.com", "password123")
	var project struct {
		Slug string `json:"slug"`
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "URL Validation"}, &project)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: status=%d body=%s", resp.StatusCode, string(body))
	}

	var envelope apiErrorEnvelope
	resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/calendar-sources", map[string]any{
		"name": "Bad",
		"url":  "http://example.com/feed.ics",
	}, &envelope)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(body))
	}
	assertAPIError(t, envelope, "VALIDATION_ERROR", "", "invalid_calendar_url")
	if strings.Contains(string(body), "example.com") {
		t.Fatal("validation error included host")
	}
}

func TestBoardSettings_SprintOnlyPatchStillWorksWithoutAgendaFields(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "calendar-sprint-settings@example.com", "password123")
	var project struct {
		Slug string `json:"slug"`
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "Sprint Settings"}, &project)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: status=%d body=%s", resp.StatusCode, string(body))
	}

	var out map[string]any
	resp, body = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+project.Slug+"/settings", map[string]any{
		"sprintsEnabled": false,
	}, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sprint-only patch: status=%d body=%s", resp.StatusCode, string(body))
	}
	if enabled, _ := out["sprintsEnabled"].(bool); enabled {
		t.Fatalf("sprintsEnabled=%v, want false", out["sprintsEnabled"])
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "agendaEnabled") {
		t.Fatalf("sprint-only response included agenda fields: %s", raw)
	}
}

func TestCalendarSources_LoopbackURLRejectedByDefault(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		EncryptionKey:  testEncryptionKey,
	})
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "calendar-loopback@example.com", "password123")
	var project struct {
		Slug string `json:"slug"`
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "Loopback URL"}, &project)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: status=%d body=%s", resp.StatusCode, string(body))
	}

	var envelope apiErrorEnvelope
	resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/calendar-sources", map[string]any{
		"name": "Local",
		"url":  "http://127.0.0.1/feed.ics",
	}, &envelope)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(body))
	}
	assertAPIError(t, envelope, "VALIDATION_ERROR", "", "invalid_calendar_url")
}

func TestBoardSettings_MixedInvalidTimezoneLeavesSprintWeeksUnchanged(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		EncryptionKey:  testEncryptionKey,
	})
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "calendar-mixed-settings@example.com", "password123")
	var project struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "Mixed Settings"}, &project)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: status=%d body=%s", resp.StatusCode, string(body))
	}

	var envelope apiErrorEnvelope
	resp, body = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+project.Slug+"/settings", map[string]any{
		"defaultSprintWeeks": 1,
		"agendaTimezone":     "Not/A_Zone",
	}, &envelope)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("mixed patch: status=%d body=%s", resp.StatusCode, string(body))
	}

	var weeks int
	if err := sqlDB.QueryRow(`SELECT default_sprint_weeks FROM projects WHERE id = ?`, project.ID).Scan(&weeks); err != nil {
		t.Fatalf("read default_sprint_weeks: %v", err)
	}
	if weeks != 2 {
		t.Fatalf("default_sprint_weeks=%d, want 2", weeks)
	}
}

func TestRESTPatchBoardSettings_agendaTimezoneOnly(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "tz-only@example.com", "password123")

	var project struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "TZ Only"}, &project)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: status=%d body=%s", resp.StatusCode, string(body))
	}

	var listed struct {
		AgendaEnabled  bool   `json:"agendaEnabled"`
		AgendaTimezone string `json:"agendaTimezone"`
	}
	resp, body = doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"/calendar-sources", nil, &listed)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", resp.StatusCode, string(body))
	}
	if listed.AgendaTimezone != "UTC" {
		t.Fatalf("initial tz=%q, want UTC", listed.AgendaTimezone)
	}

	var patched map[string]any
	resp, body = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+project.Slug+"/settings", map[string]any{
		"agendaTimezone": "America/New_York",
	}, &patched)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH status=%d body=%s", resp.StatusCode, string(body))
	}
	if patched["agendaTimezone"] != "America/New_York" {
		t.Fatalf("patched agendaTimezone=%v, want America/New_York", patched["agendaTimezone"])
	}

	resp, body = doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"/calendar-sources", nil, &listed)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", resp.StatusCode, string(body))
	}
	if listed.AgendaTimezone != "America/New_York" {
		t.Fatalf("after PATCH GET tz=%q, want America/New_York", listed.AgendaTimezone)
	}

	resp, body = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+project.Slug+"/settings", map[string]any{
		"agendaTimezone": "America/Chicago",
	}, &patched)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH 2 status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, body = doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"/calendar-sources", nil, &listed)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET 2 status=%d body=%s", resp.StatusCode, string(body))
	}
	if listed.AgendaTimezone != "America/Chicago" {
		t.Fatalf("after PATCH 2 GET tz=%q, want America/Chicago", listed.AgendaTimezone)
	}
}

