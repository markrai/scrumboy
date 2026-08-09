package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	sprintapp "scrumboy/internal/application/sprint"
	"scrumboy/internal/store"
)

func TestMapSprintDefinitionPrepareErrorSentinels(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		status  int
		code    string
		message string
	}{
		{
			name:    "actor",
			err:     sprintapp.ErrActorRequired,
			status:  http.StatusUnauthorized,
			code:    CodeAuthRequired,
			message: "Sign-in required for this tool",
		},
		{
			name:    "maintainer",
			err:     sprintapp.ErrMaintainerRequired,
			status:  http.StatusForbidden,
			code:    CodeForbidden,
			message: "maintainer or higher required",
		},
		{
			name:    "project mismatch",
			err:     sprintapp.ErrSprintNotInProject,
			status:  http.StatusNotFound,
			code:    CodeNotFound,
			message: "not found",
		},
		{
			name:    "wrapped actor",
			err:     fmt.Errorf("outer diagnostic: %w", sprintapp.ErrActorRequired),
			status:  http.StatusUnauthorized,
			code:    CodeAuthRequired,
			message: "Sign-in required for this tool",
		},
		{
			name:    "wrapped maintainer",
			err:     fmt.Errorf("outer diagnostic: %w", sprintapp.ErrMaintainerRequired),
			status:  http.StatusForbidden,
			code:    CodeForbidden,
			message: "maintainer or higher required",
		},
		{
			name:    "wrapped project mismatch",
			err:     fmt.Errorf("outer diagnostic: %w", sprintapp.ErrSprintNotInProject),
			status:  http.StatusNotFound,
			code:    CodeNotFound,
			message: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapSprintDefinitionPrepareError(tt.err)
			if got.Status != tt.status || got.Code != tt.code || got.Message != tt.message {
				t.Fatalf("mapped = status %d code %q message %q", got.Status, got.Code, got.Message)
			}
			if got.Details != nil || got.Cause != nil {
				t.Fatalf("mapped details/cause = %#v/%v, want nil/nil", got.Details, got.Cause)
			}
			if details := clientErrorDetails(got); len(details) != 0 {
				t.Fatalf("public details = %#v, want empty", details)
			}
		})
	}
}

func TestMapSprintDefinitionPrepareErrorDelegatesRawErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "unauthorized", err: store.ErrUnauthorized},
		{name: "forbidden", err: store.ErrForbidden},
		{name: "not found", err: store.ErrNotFound},
		{name: "validation", err: fmt.Errorf("%w: invalid sprint", store.ErrValidation)},
		{name: "conflict", err: fmt.Errorf("%w: duplicate sprint", store.ErrConflict)},
		{name: "private", err: errors.New("private sprint dependency failure")},
		{name: "cancellation", err: context.Canceled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapSprintDefinitionPrepareError(tt.err)
			want := mapStoreError(tt.err)
			if got.Status != want.Status || got.Code != want.Code || got.Message != want.Message ||
				!reflect.DeepEqual(got.Details, want.Details) {
				t.Fatalf("mapped = %#v, want mapStoreError result %#v", got, want)
			}
			switch {
			case got.Cause == nil && want.Cause == nil:
			case got.Cause != nil && want.Cause != nil && got.Cause.Error() == want.Cause.Error():
			default:
				t.Fatalf("mapped cause = %v, want %v", got.Cause, want.Cause)
			}
			if got.Code == CodeInternal {
				if details := clientErrorDetails(got); len(details) != 0 {
					t.Fatalf("internal public details = %#v, want empty", details)
				}
			}
		})
	}
}
