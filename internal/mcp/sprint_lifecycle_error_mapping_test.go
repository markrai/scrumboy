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

func TestMapSprintLifecyclePrepareErrorSentinels(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		message string
		field   string
	}{
		{
			name:    "activation state",
			err:     sprintapp.ErrSprintMustBePlanned,
			message: "sprint must be PLANNED to activate",
			field:   "sprintId",
		},
		{
			name:    "activation schedule",
			err:     sprintapp.ErrSprintEndNotAfterNow,
			message: "sprint end date is on or before now; cannot activate",
			field:   "plannedEndAt",
		},
		{
			name:    "close state",
			err:     sprintapp.ErrSprintMustBeActive,
			message: "sprint must be ACTIVE to close",
			field:   "sprintId",
		},
		{
			name:    "wrapped activation state",
			err:     fmt.Errorf("outer diagnostic: %w", sprintapp.ErrSprintMustBePlanned),
			message: "sprint must be PLANNED to activate",
			field:   "sprintId",
		},
		{
			name:    "wrapped activation schedule",
			err:     fmt.Errorf("outer diagnostic: %w", sprintapp.ErrSprintEndNotAfterNow),
			message: "sprint end date is on or before now; cannot activate",
			field:   "plannedEndAt",
		},
		{
			name:    "wrapped close state",
			err:     fmt.Errorf("outer diagnostic: %w", sprintapp.ErrSprintMustBeActive),
			message: "sprint must be ACTIVE to close",
			field:   "sprintId",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapSprintLifecyclePrepareError(tt.err)
			if got.Status != http.StatusBadRequest || got.Code != CodeValidationError || got.Message != tt.message {
				t.Fatalf("mapped = status %d code %q message %q", got.Status, got.Code, got.Message)
			}
			wantDetails := map[string]any{"field": tt.field}
			if !reflect.DeepEqual(got.Details, wantDetails) {
				t.Fatalf("mapped details = %#v, want %#v", got.Details, wantDetails)
			}
			if got.Cause != nil {
				t.Fatalf("mapped cause = %v, want nil", got.Cause)
			}
			if details := clientErrorDetails(got); !reflect.DeepEqual(details, wantDetails) {
				t.Fatalf("public details = %#v, want %#v", details, wantDetails)
			}
		})
	}
}

func TestMapSprintLifecyclePrepareErrorDelegatesSharedAndRawErrors(t *testing.T) {
	mutationValidationErr := fmt.Errorf("%w: lifecycle mutation rejected", store.ErrValidation)
	tests := []struct {
		name string
		err  error
	}{
		{name: "actor", err: sprintapp.ErrActorRequired},
		{name: "maintainer", err: sprintapp.ErrMaintainerRequired},
		{name: "project mismatch", err: sprintapp.ErrSprintNotInProject},
		{name: "unauthorized store error", err: store.ErrUnauthorized},
		{name: "forbidden store error", err: store.ErrForbidden},
		{name: "not found store error", err: store.ErrNotFound},
		{name: "mutation-time validation", err: mutationValidationErr},
		{name: "conflict store error", err: fmt.Errorf("%w: lifecycle conflict", store.ErrConflict)},
		{name: "private dependency error", err: errors.New("private sprint dependency failure")},
		{name: "cancellation", err: context.Canceled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapSprintLifecyclePrepareError(tt.err)
			want := mapSprintDefinitionPrepareError(tt.err)
			if got.Status != want.Status || got.Code != want.Code || got.Message != want.Message ||
				!reflect.DeepEqual(got.Details, want.Details) {
				t.Fatalf("mapped = %#v, want definition-prepare result %#v", got, want)
			}
			switch {
			case got.Cause == nil && want.Cause == nil:
			case got.Cause != nil && want.Cause != nil && got.Cause.Error() == want.Cause.Error():
			default:
				t.Fatalf("mapped cause = %v, want %v", got.Cause, want.Cause)
			}
		})
	}

	got := mapSprintLifecyclePrepareError(mutationValidationErr)
	if got.Message != mutationValidationErr.Error() {
		t.Fatalf("mutation-time validation message = %q, want raw store mapping %q", got.Message, mutationValidationErr.Error())
	}
}
