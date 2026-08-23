package mcp

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"

	tagapp "scrumboy/internal/application/tag"
	"scrumboy/internal/store"
)

func assertTagColorAdapterError(
	t *testing.T,
	got *adapterError,
	wantStatus int,
	wantCode string,
	wantMessage string,
	wantDetails any,
) {
	t.Helper()
	if got.Status != wantStatus || got.Code != wantCode || got.Message != wantMessage || !reflect.DeepEqual(got.Details, wantDetails) {
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

func TestMapTagColorPrepareError(t *testing.T) {
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
			err:         fmt.Errorf("prepare mine: %w", tagapp.ErrActorRequired),
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
			err:         fmt.Errorf("prepare project: %w", tagapp.ErrMaintainerRequired),
			wantStatus:  http.StatusForbidden,
			wantCode:    CodeForbidden,
			wantMessage: "maintainer or higher required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertTagColorAdapterError(
				t,
				mapTagColorPrepareError(tc.err),
				tc.wantStatus,
				tc.wantCode,
				tc.wantMessage,
				nil,
			)
		})
	}

	t.Run("store errors retain shared mapping", func(t *testing.T) {
		got := mapTagColorPrepareError(store.ErrNotFound)
		want := mapStoreError(store.ErrNotFound)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("mapped = %#v, want shared store mapping %#v", got, want)
		}
	})
}

func TestMapTagColorUpdateError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "projection missing", err: tagapp.ErrColorProjectionMissing},
		{name: "wrapped projection missing", err: fmt.Errorf("post-read: %w", tagapp.ErrColorProjectionMissing)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mapTagColorUpdateError(tc.err)
			wantDetails := map[string]any{"detail": "updated project tag not found in post-read"}
			assertTagColorAdapterError(
				t,
				got,
				http.StatusInternalServerError,
				CodeInternal,
				"internal error",
				wantDetails,
			)
			if got.Cause == nil || got.Cause.Error() != wantDetails["detail"] {
				t.Fatalf("internal cause = %v, want legacy post-read diagnostic", got.Cause)
			}
			if details := clientErrorDetails(got); len(details) != 0 {
				t.Fatalf("public internal details = %#v, want empty", details)
			}
		})
	}

	t.Run("store errors retain shared mapping", func(t *testing.T) {
		got := mapTagColorUpdateError(store.ErrNotFound)
		want := mapStoreError(store.ErrNotFound)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("mapped = %#v, want shared store mapping %#v", got, want)
		}
	})
}
