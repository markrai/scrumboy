package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func TestParseProjectUpdatePatch_name(t *testing.T) {
	t.Parallel()
	patch, aerr := parseProjectUpdatePatch(json.RawMessage(`{"name":"Renamed"}`))
	if aerr != nil {
		t.Fatalf("parseProjectUpdatePatch: %v", aerr)
	}
	if patch.Name == nil || *patch.Name != "Renamed" {
		t.Fatalf("expected name Renamed, got %#v", patch.Name)
	}
	if patch.DefaultSprintWeeks != nil {
		t.Fatalf("expected DefaultSprintWeeks nil, got %#v", patch.DefaultSprintWeeks)
	}
}

func TestParseProjectUpdatePatch_defaultSprintWeeks(t *testing.T) {
	t.Parallel()
	patch, aerr := parseProjectUpdatePatch(json.RawMessage(`{"defaultSprintWeeks":2}`))
	if aerr != nil {
		t.Fatalf("parseProjectUpdatePatch: %v", aerr)
	}
	if patch.DefaultSprintWeeks == nil || *patch.DefaultSprintWeeks != 2 {
		t.Fatalf("expected DefaultSprintWeeks 2, got %#v", patch.DefaultSprintWeeks)
	}
	if patch.Name != nil {
		t.Fatalf("expected Name nil, got %#v", patch.Name)
	}
}

func TestParseProjectUpdatePatch_nameNull_rejected(t *testing.T) {
	t.Parallel()
	_, aerr := parseProjectUpdatePatch(json.RawMessage(`{"name":null}`))
	if aerr == nil {
		t.Fatal("expected adapter error for null name")
	}
	if aerr.Code != CodeValidationError {
		t.Fatalf("expected validation error code, got %q", aerr.Code)
	}
}

func TestParseProjectUpdatePatch_unsupportedField(t *testing.T) {
	t.Parallel()
	_, aerr := parseProjectUpdatePatch(json.RawMessage(`{"slug":"new-slug"}`))
	if aerr == nil {
		t.Fatal("expected adapter error for unsupported field")
	}
	if aerr.Code != CodeValidationError {
		t.Fatalf("expected validation error code, got %q", aerr.Code)
	}
}

func TestParseProjectUpdatePatch_empty(t *testing.T) {
	t.Parallel()
	_, aerr := parseProjectUpdatePatch(json.RawMessage(`{}`))
	if aerr == nil {
		t.Fatal("expected adapter error for empty patch")
	}
	if aerr.Code != CodeValidationError {
		t.Fatalf("expected validation error code, got %q", aerr.Code)
	}
}

func TestParseProjectUpdatePatch_invalidType(t *testing.T) {
	t.Parallel()
	_, aerr := parseProjectUpdatePatch(json.RawMessage(`{"defaultSprintWeeks":"two"}`))
	if aerr == nil {
		t.Fatal("expected adapter error for invalid defaultSprintWeeks type")
	}
	if aerr.Code != CodeValidationError {
		t.Fatalf("expected validation error code, got %q", aerr.Code)
	}
}

func TestParseProjectUpdatePatch_invalidWeeksValue(t *testing.T) {
	t.Parallel()
	_, aerr := parseProjectUpdatePatch(json.RawMessage(`{"defaultSprintWeeks":3}`))
	if aerr == nil {
		t.Fatal("expected adapter error for invalid defaultSprintWeeks value")
	}
	if aerr.Code != CodeValidationError {
		t.Fatalf("expected validation error code, got %q", aerr.Code)
	}
}

func TestNormalizeProjectsListLimit(t *testing.T) {
	t.Parallel()

	one, zero, negative, hundred, tooLarge := 1, 0, -1, 100, 101
	tests := []struct {
		name      string
		input     *int
		want      int
		wantError bool
	}{
		{name: "omitted defaults to twenty", input: nil, want: 20},
		{name: "minimum", input: &one, want: 1},
		{name: "maximum", input: &hundred, want: 100},
		{name: "zero rejected", input: &zero, wantError: true},
		{name: "negative rejected", input: &negative, wantError: true},
		{name: "over maximum rejected", input: &tooLarge, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, adapterErr := normalizeProjectsListLimit(tt.input)
			if tt.wantError {
				if adapterErr == nil {
					t.Fatal("normalizeProjectsListLimit error=nil, want validation error")
				}
				if adapterErr.Status != http.StatusBadRequest || adapterErr.Code != CodeValidationError {
					t.Fatalf("normalizeProjectsListLimit error=%+v, want 400 VALIDATION_ERROR", adapterErr)
				}
				details, ok := adapterErr.Details.(map[string]any)
				if !ok || details["field"] != "limit" {
					t.Fatalf("normalizeProjectsListLimit details=%#v, want field=limit", adapterErr.Details)
				}
				return
			}
			if adapterErr != nil || got != tt.want {
				t.Fatalf("normalizeProjectsListLimit=(%d,%v), want (%d,nil)", got, adapterErr, tt.want)
			}
		})
	}
}

func TestProjectSummaryToItemRetainsAgentFieldsAndCannotExposeImage(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	createdAt := expiresAt.Add(-48 * time.Hour)
	updatedAt := expiresAt.Add(-24 * time.Hour)
	summary := store.ProjectSummary{
		ID:                 42,
		Slug:               "payload-safe",
		Name:               "Payload Safe",
		DominantColor:      "#123456",
		DefaultSprintWeeks: 2,
		ExpiresAt:          &expiresAt,
		CreatedAt:          createdAt,
		UpdatedAt:          updatedAt,
		Role:               store.RoleContributor,
	}

	got := projectSummaryToItem(summary)
	want := projectItem{
		ProjectSlug:        summary.Slug,
		ProjectID:          summary.ID,
		Name:               summary.Name,
		DominantColor:      summary.DominantColor,
		DefaultSprintWeeks: summary.DefaultSprintWeeks,
		ExpiresAt:          summary.ExpiresAt,
		CreatedAt:          summary.CreatedAt,
		UpdatedAt:          summary.UpdatedAt,
		Role:               summary.Role.String(),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projectSummaryToItem=%+v, want %+v", got, want)
	}
	if _, exists := reflect.TypeOf(projectItem{}).FieldByName("Image"); exists {
		t.Fatal("projectItem must not contain an Image field")
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal project item: %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("unmarshal project item: %v", err)
	}
	if _, exists := object["image"]; exists {
		t.Fatalf("project item unexpectedly contains image: %s", encoded)
	}
}

type recordingProjectsListStore struct {
	*store.Store
	countUsers int
	summaries  []store.ProjectSummary
	nextCursor *string
	gotLimit   int
	gotCursor  *string
	calls      int
}

func (s *recordingProjectsListStore) CountUsers(context.Context) (int, error) {
	return s.countUsers, nil
}

func (s *recordingProjectsListStore) ListProjectSummaries(_ context.Context, limit int, cursor *string) ([]store.ProjectSummary, *string, error) {
	s.calls++
	s.gotLimit = limit
	if cursor != nil {
		value := *cursor
		s.gotCursor = &value
	} else {
		s.gotCursor = nil
	}
	return s.summaries, s.nextCursor, nil
}

func TestHandleProjectsListPassesPaginationAndReturnsNonNilItems(t *testing.T) {
	nextCursor := "1000:7"
	fake := &recordingProjectsListStore{
		countUsers: 1,
		summaries: []store.ProjectSummary{{
			ID:                 8,
			Slug:               "summary",
			Name:               "Summary",
			DominantColor:      "#abcdef",
			DefaultSprintWeeks: 1,
			CreatedAt:          time.UnixMilli(900).UTC(),
			UpdatedAt:          time.UnixMilli(1000).UTC(),
			Role:               store.RoleMaintainer,
		}},
		nextCursor: &nextCursor,
	}
	adapter := New(fake, Options{Mode: "full"})
	ctx := store.WithUserID(context.Background(), 99)
	data, meta, adapterErr := adapter.handleProjectsList(ctx, map[string]any{
		"limit":  1,
		"cursor": "2000:9",
	})
	if adapterErr != nil {
		t.Fatalf("handleProjectsList: %v", adapterErr)
	}
	if fake.calls != 1 || fake.gotLimit != 1 || fake.gotCursor == nil || *fake.gotCursor != "2000:9" {
		t.Fatalf("store call count=%d limit=%d cursor=%v", fake.calls, fake.gotLimit, fake.gotCursor)
	}
	items, ok := data.(map[string]any)["items"].([]projectItem)
	if !ok || len(items) != 1 || items[0].ProjectID != 8 {
		t.Fatalf("projects_list data=%#v, want one projected summary", data)
	}
	if meta["nextCursor"] != &nextCursor || meta["hasMore"] != true {
		t.Fatalf("projects_list meta=%#v, want continuation", meta)
	}

	fake.summaries = nil
	fake.nextCursor = nil
	data, meta, adapterErr = adapter.handleProjectsList(ctx, map[string]any{})
	if adapterErr != nil {
		t.Fatalf("handleProjectsList empty: %v", adapterErr)
	}
	items, ok = data.(map[string]any)["items"].([]projectItem)
	if !ok || items == nil || len(items) != 0 {
		t.Fatalf("empty projects_list items=%#v, want non-nil empty slice", data.(map[string]any)["items"])
	}
	if fake.gotLimit != 20 || fake.gotCursor != nil || meta["nextCursor"] != (*string)(nil) || meta["hasMore"] != false {
		t.Fatalf("empty projects_list call/meta limit=%d cursor=%v meta=%#v", fake.gotLimit, fake.gotCursor, meta)
	}
}

func TestHandleProjectsListValidationAndGatesPrecedeStoreRead(t *testing.T) {
	fake := &recordingProjectsListStore{countUsers: 1}
	ctx := store.WithUserID(context.Background(), 99)

	adapter := New(fake, Options{Mode: "full"})
	_, _, adapterErr := adapter.handleProjectsList(ctx, map[string]any{"unknown": true})
	if adapterErr == nil || adapterErr.Code != CodeValidationError || fake.calls != 0 {
		t.Fatalf("unknown-field result error=%+v calls=%d, want validation before store", adapterErr, fake.calls)
	}

	unauthenticated := New(fake, Options{Mode: "full"})
	_, _, adapterErr = unauthenticated.handleProjectsList(context.Background(), map[string]any{"unknown": true})
	if adapterErr == nil || adapterErr.Status != http.StatusUnauthorized || fake.calls != 0 {
		t.Fatalf("unauthenticated result error=%+v calls=%d, want auth gate before input", adapterErr, fake.calls)
	}

	anonymous := New(fake, Options{Mode: "anonymous"})
	_, _, adapterErr = anonymous.handleProjectsList(ctx, map[string]any{"unknown": true})
	if adapterErr == nil || adapterErr.Code != CodeCapabilityUnavailable || fake.calls != 0 {
		t.Fatalf("anonymous result error=%+v calls=%d, want capability gate", adapterErr, fake.calls)
	}

	bootstrapFake := &recordingProjectsListStore{countUsers: 0}
	bootstrap := New(bootstrapFake, Options{Mode: "full"})
	_, _, adapterErr = bootstrap.handleProjectsList(ctx, map[string]any{"unknown": true})
	if adapterErr == nil || adapterErr.Code != CodeCapabilityUnavailable || bootstrapFake.calls != 0 {
		t.Fatalf("bootstrap result error=%+v calls=%d, want capability gate", adapterErr, bootstrapFake.calls)
	}
}
