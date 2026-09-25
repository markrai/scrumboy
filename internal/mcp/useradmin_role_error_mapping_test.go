package mcp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	useradminapp "scrumboy/internal/application/useradmin"
	"scrumboy/internal/store"
)

func assertUserAdminRoleAdapterErrorMatches(t *testing.T, got, want *adapterError) {
	t.Helper()
	if got.Status != want.Status || got.Code != want.Code || got.Message != want.Message ||
		!reflect.DeepEqual(got.Details, want.Details) {
		t.Fatalf("mapped error = %+v, want %+v", got, want)
	}
}

func TestMapUserAdminRolePrepareError(t *testing.T) {
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
			err:  fmt.Errorf("prepare: %w", useradminapp.ErrActorRequired),
			want: newAdapterError(401, CodeAuthRequired, "Sign-in required for this tool", nil),
		},
		{
			name: "owner required",
			err:  useradminapp.ErrOwnerRequired,
			want: newAdapterError(403, CodeForbidden, "forbidden", nil),
		},
		{
			name: "wrapped owner required",
			err:  fmt.Errorf("prepare: %w", useradminapp.ErrOwnerRequired),
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
			err:  errors.New("requester read failed"),
			want: mapStoreError(errors.New("requester read failed")),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertUserAdminRoleAdapterErrorMatches(t, mapUserAdminRolePrepareError(tc.err), tc.want)
		})
	}
}

func TestMapUserAdminRoleUpdateErrorUsesPrivilegedMapperForMutation(t *testing.T) {
	for _, mutationErr := range []error{
		store.ErrUnauthorized,
		store.ErrForbidden,
		store.ErrNotFound,
		store.ErrValidation,
		store.ErrConflict,
		errors.New("mutation failed"),
	} {
		got := mapUserAdminRoleUpdateError(mutationErr)
		want := mapPrivilegedStoreError(mutationErr)
		assertUserAdminRoleAdapterErrorMatches(t, got, want)
	}
}

func TestMapUserAdminRoleUpdateErrorUnwrapsRealProjectionCauseBeforeMapping(t *testing.T) {
	causes := []error{
		store.ErrUnauthorized,
		store.ErrForbidden,
		store.ErrNotFound,
		store.ErrValidation,
		store.ErrConflict,
		errors.New("projection failed"),
	}

	for _, cause := range causes {
		t.Run(cause.Error(), func(t *testing.T) {
			fake := &userAdminMCPRoleMigrationStore{
				requesterID:   41,
				requester:     store.User{ID: 41, SystemRole: store.SystemRoleOwner},
				projectionErr: cause,
			}
			service := useradminapp.NewMCPRoleService(useradminapp.MCPRoleServiceDependencies{
				RequesterRead:  fake,
				Mutations:      fake,
				ProjectionRead: fake,
			})
			ctx := store.WithUserID(
				context.WithValue(context.Background(), userAdminMCPRoleContextKey{}, "mapper-request"),
				41,
			)
			prepared, err := service.Prepare(ctx, useradminapp.RoleChangeCommand{
				TargetUserID: 71,
				NewRole:      store.SystemRoleUser,
			})
			if err != nil {
				t.Fatalf("Prepare() error = %v", err)
			}
			_, projectionErr := prepared.Update()
			if projectionErr == nil {
				t.Fatal("Update() error = nil, want projection error")
			}
			if !errors.Is(projectionErr, useradminapp.ErrMCPRoleProjectionFailed) {
				t.Fatalf("projection classification missing: %v", projectionErr)
			}
			unwrapped := errors.Unwrap(projectionErr)
			if unwrapped == nil {
				t.Fatal("real projection classification has nil unwrap")
			}
			if !errors.Is(unwrapped, cause) {
				t.Fatalf("unwrapped cause = %v, want %v", unwrapped, cause)
			}

			got := mapUserAdminRoleUpdateError(projectionErr)
			want := mapStoreError(cause)
			assertUserAdminRoleAdapterErrorMatches(t, got, want)
			if errors.Is(cause, store.ErrUnauthorized) && (got.Status != 401 || got.Code != CodeAuthRequired) {
				t.Fatalf("unauthorized projection mapping = %+v, want ordinary 401", got)
			}
			if details := clientErrorDetails(got); len(details) != 0 {
				t.Fatalf("public projection error details = %#v, want empty", details)
			}
		})
	}
}
