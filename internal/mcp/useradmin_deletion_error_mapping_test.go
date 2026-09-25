package mcp

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	useradminapp "scrumboy/internal/application/useradmin"
	"scrumboy/internal/store"
)

func assertUserAdminDeletionAdapterErrorMatches(t *testing.T, got, want *adapterError) {
	t.Helper()
	if got == nil || want == nil {
		if got != want {
			t.Fatalf("mapped error = %+v, want %+v", got, want)
		}
		return
	}
	if got.Status != want.Status || got.Code != want.Code || got.Message != want.Message ||
		!reflect.DeepEqual(got.Details, want.Details) {
		t.Fatalf("mapped error = %+v, want %+v", got, want)
	}
}

func TestMapUserAdminDeletionPrepareError(t *testing.T) {
	requesterFailure := errors.New("requester read failed")
	tests := []struct {
		name string
		err  error
		want *adapterError
	}{
		{
			name: "actor required",
			err:  useradminapp.ErrActorRequired,
			want: newAdapterError(401, CodeAuthRequired, "Sign-in required for this tool", nil),
		},
		{
			name: "wrapped actor required",
			err:  fmt.Errorf("prepare deletion: %w", useradminapp.ErrActorRequired),
			want: newAdapterError(401, CodeAuthRequired, "Sign-in required for this tool", nil),
		},
		{
			name: "owner required",
			err:  useradminapp.ErrOwnerRequired,
			want: newAdapterError(403, CodeForbidden, "forbidden", nil),
		},
		{
			name: "wrapped owner required",
			err:  fmt.Errorf("prepare deletion: %w", useradminapp.ErrOwnerRequired),
			want: newAdapterError(403, CodeForbidden, "forbidden", nil),
		},
		{
			name: "requester unauthorized",
			err:  store.ErrUnauthorized,
			want: mapStoreError(store.ErrUnauthorized),
		},
		{
			name: "requester forbidden",
			err:  store.ErrForbidden,
			want: mapStoreError(store.ErrForbidden),
		},
		{
			name: "requester missing",
			err:  store.ErrNotFound,
			want: mapStoreError(store.ErrNotFound),
		},
		{
			name: "requester validation",
			err:  store.ErrValidation,
			want: mapStoreError(store.ErrValidation),
		},
		{
			name: "requester conflict",
			err:  store.ErrConflict,
			want: mapStoreError(store.ErrConflict),
		},
		{
			name: "requester unexpected",
			err:  requesterFailure,
			want: mapStoreError(requesterFailure),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertUserAdminDeletionAdapterErrorMatches(
				t,
				mapUserAdminDeletionPrepareError(tc.err),
				tc.want,
			)
		})
	}
}
