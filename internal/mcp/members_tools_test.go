package mcp

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"

	membershipapp "scrumboy/internal/application/membership"
	"scrumboy/internal/store"
)

func TestNormalizeProjectMemberRoleForMCP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{"owner", "maintainer"},
		{"OWNER", "maintainer"},
		{"editor", "contributor"},
		{"maintainer", "maintainer"},
		{"contributor", "contributor"},
		{"viewer", "viewer"},
	}
	for _, tc := range tests {
		if got := normalizeProjectMemberRoleForMCP(tc.in); got != tc.want {
			t.Fatalf("normalizeProjectMemberRoleForMCP(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func assertMembershipAdapterError(
	t *testing.T,
	got *adapterError,
	wantStatus int,
	wantCode string,
	wantMessage string,
	wantDetails any,
) {
	t.Helper()
	if got == nil {
		t.Fatalf("adapter error=nil, want status=%d code=%q message=%q details=%#v", wantStatus, wantCode, wantMessage, wantDetails)
	}
	if got.Status != wantStatus || got.Code != wantCode || got.Message != wantMessage ||
		!reflect.DeepEqual(got.Details, wantDetails) {
		t.Fatalf("adapter error=%+v details=%#v, want status=%d code=%q message=%q details=%#v", got, got.Details, wantStatus, wantCode, wantMessage, wantDetails)
	}
}

func TestMapMembershipMutationPrepareError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name: "maintainer required", err: fmt.Errorf("prepare: %w", membershipapp.ErrMaintainerRequired),
			wantStatus: http.StatusForbidden, wantCode: CodeForbidden, wantMessage: "maintainer or higher required",
		},
		{
			name: "actor required", err: fmt.Errorf("prepare: %w", membershipapp.ErrActorRequired),
			wantStatus: http.StatusUnauthorized, wantCode: CodeAuthRequired, wantMessage: "Sign-in required for this tool",
		},
		{
			name: "access not found", err: fmt.Errorf("access: %w", store.ErrNotFound),
			wantStatus: http.StatusNotFound, wantCode: CodeNotFound, wantMessage: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertMembershipAdapterError(t, mapMembershipMutationPrepareError(tt.err), tt.wantStatus, tt.wantCode, tt.wantMessage, nil)
		})
	}
}

func TestMapMembershipAddError(t *testing.T) {
	t.Run("missing target remains internal and client sanitized", func(t *testing.T) {
		mapped := mapMembershipAddError(fmt.Errorf("projection: %w", membershipapp.ErrAddedMemberMissing))
		assertMembershipAdapterError(t, mapped, http.StatusInternalServerError, CodeInternal, "member not found after add", nil)

		client := clientErrorResponseBody(mapped)
		details, ok := client.Details.(map[string]any)
		if client.Code != CodeInternal || client.Message != "internal error" || !ok || len(details) != 0 {
			t.Fatalf("client error=%+v, want generic INTERNAL with empty details", client)
		}
	})

	t.Run("mutation unauthorized remains forbidden", func(t *testing.T) {
		assertMembershipAdapterError(
			t,
			mapMembershipAddError(store.ErrUnauthorized),
			http.StatusForbidden,
			CodeForbidden,
			"maintainer or higher required",
			nil,
		)
	})

	t.Run("other store errors retain generic mapping", func(t *testing.T) {
		err := fmt.Errorf("%w: already a member", store.ErrConflict)
		assertMembershipAdapterError(t, mapMembershipAddError(err), http.StatusConflict, CodeConflict, err.Error(), nil)
	})
}

func TestMapMembershipUpdateRoleError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantMessage string
		wantDetails any
	}{
		{
			name: "missing projected target", err: fmt.Errorf("projection: %w", membershipapp.ErrUpdatedMemberMissing),
			wantStatus: http.StatusNotFound, wantCode: CodeNotFound, wantMessage: "not found",
		},
		{
			name: "unauthorized", err: store.ErrUnauthorized,
			wantStatus: http.StatusForbidden, wantCode: CodeForbidden, wantMessage: "maintainer or higher required",
		},
		{
			name: "not found", err: store.ErrNotFound,
			wantStatus: http.StatusNotFound, wantCode: CodeNotFound, wantMessage: "not found",
		},
		{
			name: "conflict", err: fmt.Errorf("%w: cannot demote yourself", store.ErrConflict),
			wantStatus: http.StatusConflict, wantCode: CodeConflict, wantMessage: "conflict: cannot demote yourself",
		},
		{
			name: "validation", err: fmt.Errorf("%w: invalid role", store.ErrValidation),
			wantStatus: http.StatusBadRequest, wantCode: CodeValidationError, wantMessage: "validation: invalid role",
			wantDetails: map[string]any{"field": "role"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertMembershipAdapterError(t, mapMembershipUpdateRoleError(tt.err), tt.wantStatus, tt.wantCode, tt.wantMessage, tt.wantDetails)
		})
	}
}

func TestMapMembershipRemoveError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name: "unauthorized", err: store.ErrUnauthorized,
			wantStatus: http.StatusForbidden, wantCode: CodeForbidden, wantMessage: "maintainer or higher required",
		},
		{
			name: "not found", err: store.ErrNotFound,
			wantStatus: http.StatusNotFound, wantCode: CodeNotFound, wantMessage: "not found",
		},
		{
			name: "validation", err: fmt.Errorf("%w: cannot remove last maintainer", store.ErrValidation),
			wantStatus: http.StatusBadRequest, wantCode: CodeValidationError, wantMessage: "validation: cannot remove last maintainer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertMembershipAdapterError(t, mapMembershipRemoveError(tt.err), tt.wantStatus, tt.wantCode, tt.wantMessage, nil)
		})
	}
}
