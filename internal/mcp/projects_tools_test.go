package mcp

import (
	"encoding/json"
	"testing"
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
