package mcp

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"

	tagapp "scrumboy/internal/application/tag"
	"scrumboy/internal/store"
)

func assertTagDeletionAdapterError(
	t *testing.T,
	got *adapterError,
	wantStatus int,
	wantCode string,
	wantMessage string,
	wantDetails any,
) {
	t.Helper()
	if got.Status != wantStatus ||
		got.Code != wantCode ||
		got.Message != wantMessage ||
		!reflect.DeepEqual(got.Details, wantDetails) {
		t.Fatalf(
			"mapped error = {status:%d code:%q message:%q details:%#v}, want {status:%d code:%q message:%q details:%#v}",
			got.Status,
			got.Code,
			got.Message,
			got.Details,
			wantStatus,
			wantCode,
			wantMessage,
			wantDetails,
		)
	}
}

func TestMapTagDeletionPrepareError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name:        "actor required",
			err:         tagapp.ErrActorRequired,
			wantStatus:  http.StatusUnauthorized,
			wantCode:    CodeAuthRequired,
			wantMessage: "Sign-in required for this tool",
		},
		{
			name:        "wrapped actor required",
			err:         fmt.Errorf("prepare mine deletion: %w", tagapp.ErrActorRequired),
			wantStatus:  http.StatusUnauthorized,
			wantCode:    CodeAuthRequired,
			wantMessage: "Sign-in required for this tool",
		},
		{
			name:        "maintainer required",
			err:         tagapp.ErrMaintainerRequired,
			wantStatus:  http.StatusForbidden,
			wantCode:    CodeForbidden,
			wantMessage: "maintainer or higher required",
		},
		{
			name:        "wrapped maintainer required",
			err:         fmt.Errorf("prepare project deletion: %w", tagapp.ErrMaintainerRequired),
			wantStatus:  http.StatusForbidden,
			wantCode:    CodeForbidden,
			wantMessage: "maintainer or higher required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertTagDeletionAdapterError(
				t,
				mapTagDeletionPrepareError(tc.err),
				tc.wantStatus,
				tc.wantCode,
				tc.wantMessage,
				nil,
			)
		})
	}

	for _, err := range []error{store.ErrNotFound, store.ErrUnauthorized} {
		got := mapTagDeletionPrepareError(err)
		want := mapStoreError(err)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("preparation error %v mapped to %#v, want shared store mapping %#v", err, got, want)
		}
	}
}

func TestMapTagDeletionExecuteError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name:        "not found",
			err:         store.ErrNotFound,
			wantStatus:  http.StatusNotFound,
			wantCode:    CodeNotFound,
			wantMessage: "not found",
		},
		{
			name:        "wrapped not found",
			err:         fmt.Errorf("delete mine tag: %w", store.ErrNotFound),
			wantStatus:  http.StatusNotFound,
			wantCode:    CodeNotFound,
			wantMessage: "not found",
		},
		{
			name:        "unauthorized",
			err:         store.ErrUnauthorized,
			wantStatus:  http.StatusForbidden,
			wantCode:    CodeForbidden,
			wantMessage: store.ErrUnauthorized.Error(),
		},
		{
			name:       "wrapped unauthorized preserves input text",
			err:        fmt.Errorf("delete project tag: %w", store.ErrUnauthorized),
			wantStatus: http.StatusForbidden,
			wantCode:   CodeForbidden,
		},
		{
			name:        "conflict",
			err:         store.ErrConflict,
			wantStatus:  http.StatusConflict,
			wantCode:    CodeConflict,
			wantMessage: store.ErrConflict.Error(),
		},
		{
			name:       "wrapped conflict preserves input text",
			err:        fmt.Errorf("delete mine tag: %w", store.ErrConflict),
			wantStatus: http.StatusConflict,
			wantCode:   CodeConflict,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wantMessage := tc.wantMessage
			if wantMessage == "" {
				wantMessage = tc.err.Error()
			}
			assertTagDeletionAdapterError(
				t,
				mapTagDeletionExecuteError(tc.err),
				tc.wantStatus,
				tc.wantCode,
				wantMessage,
				nil,
			)
		})
	}

	t.Run("other store errors retain shared mapping", func(t *testing.T) {
		got := mapTagDeletionExecuteError(store.ErrForbidden)
		want := mapStoreError(store.ErrForbidden)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("mapped = %#v, want shared store mapping %#v", got, want)
		}
	})
}
