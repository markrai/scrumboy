package mcp

import (
	"net/http"
	"reflect"
	"testing"

	todoapp "scrumboy/internal/application/todo"
	"scrumboy/internal/store"
)

func TestMapMCPCreateErrorPreservesLocalReferenceDetails(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantMessage string
		wantDetails map[string]any
	}{
		{
			name: "nonpositive after reference omits local ID",
			err: &todoapp.MCPCreateValidationError{
				Kind:    todoapp.MCPCreateInvalidLocalReference,
				Field:   "afterLocalId",
				LocalID: -7,
			},
			wantMessage: "invalid local todo reference",
			wantDetails: map[string]any{"field": "afterLocalId"},
		},
		{
			name: "nonpositive before reference omits local ID",
			err: &todoapp.MCPCreateValidationError{
				Kind:    todoapp.MCPCreateInvalidLocalReference,
				Field:   "beforeLocalId",
				LocalID: 0,
			},
			wantMessage: "invalid local todo reference",
			wantDetails: map[string]any{"field": "beforeLocalId"},
		},
		{
			name: "missing positive reference includes local ID",
			err: &todoapp.MCPCreateValidationError{
				Kind:       todoapp.MCPCreateInvalidLocalReference,
				Field:      "afterLocalId",
				LocalID:    41,
				HasLocalID: true,
			},
			wantMessage: "invalid local todo reference",
			wantDetails: map[string]any{"field": "afterLocalId", "localId": int64(41)},
		},
		{
			name: "missing positive before reference includes local ID",
			err: &todoapp.MCPCreateValidationError{
				Kind:       todoapp.MCPCreateInvalidLocalReference,
				Field:      "beforeLocalId",
				LocalID:    42,
				HasLocalID: true,
			},
			wantMessage: "invalid local todo reference",
			wantDetails: map[string]any{
				"field":   "beforeLocalId",
				"localId": int64(42),
			},
		},
		{
			name: "wrong-column reference includes local ID",
			err: &todoapp.MCPCreateValidationError{
				Kind:       todoapp.MCPCreateReferenceInWrongColumn,
				Field:      "beforeLocalId",
				LocalID:    17,
				HasLocalID: true,
			},
			wantMessage: "position reference must be in target column",
			wantDetails: map[string]any{"field": "beforeLocalId", "localId": int64(17)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapMCPCreateError(tt.err)
			if got.Status != http.StatusBadRequest || got.Code != CodeValidationError || got.Message != tt.wantMessage {
				t.Fatalf("mapMCPCreateError() = status %d code %q message %q, want %d %q %q", got.Status, got.Code, got.Message, http.StatusBadRequest, CodeValidationError, tt.wantMessage)
			}
			if !reflect.DeepEqual(got.Details, tt.wantDetails) {
				t.Fatalf("mapMCPCreateError() details = %#v, want %#v", got.Details, tt.wantDetails)
			}
		})
	}
}

func TestMapMCPCreateErrorPreservesPrivilegedAuthorizationMapping(t *testing.T) {
	got := mapMCPCreateError(store.ErrUnauthorized)
	if got.Status != http.StatusForbidden || got.Code != CodeForbidden || got.Message != "forbidden" {
		t.Fatalf("mapMCPCreateError(ErrUnauthorized) = status %d code %q message %q, want %d %q %q", got.Status, got.Code, got.Message, http.StatusForbidden, CodeForbidden, "forbidden")
	}
	if got.Details != nil {
		t.Fatalf("mapMCPCreateError(ErrUnauthorized) details = %#v, want nil", got.Details)
	}
}
