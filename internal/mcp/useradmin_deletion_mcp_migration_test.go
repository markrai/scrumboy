package mcp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

type userAdminMCPDeletionContextKey struct{}

type userAdminMCPDeletionMigrationStore struct {
	storeAPI

	trace []string

	requesterID  int64
	requester    store.User
	requesterErr error
	deletionErr  error

	requesterReadID   int64
	deleteRequesterID int64
	deleteTargetID    int64
	contextValues     []any
	deleteCalls       int
}

func (s *userAdminMCPDeletionMigrationStore) CountUsers(context.Context) (int, error) {
	return 1, nil
}

func (s *userAdminMCPDeletionMigrationStore) GetUser(
	ctx context.Context,
	userID int64,
) (store.User, error) {
	s.trace = append(s.trace, "requester-read")
	s.requesterReadID = userID
	s.contextValues = append(s.contextValues, ctx.Value(userAdminMCPDeletionContextKey{}))
	return s.requester, s.requesterErr
}

func (s *userAdminMCPDeletionMigrationStore) DeleteUser(
	ctx context.Context,
	requesterID int64,
	targetUserID int64,
) error {
	s.trace = append(s.trace, "delete")
	s.deleteCalls++
	s.deleteRequesterID = requesterID
	s.deleteTargetID = targetUserID
	s.contextValues = append(s.contextValues, ctx.Value(userAdminMCPDeletionContextKey{}))
	return s.deletionErr
}

func newUserAdminMCPDeletionMigrationAdapter(
	fake *userAdminMCPDeletionMigrationStore,
) *Adapter {
	return New(fake, Options{Mode: "full"})
}

func userAdminMCPDeletionContext(actorID int64, marker any) context.Context {
	return store.WithUserID(
		context.WithValue(context.Background(), userAdminMCPDeletionContextKey{}, marker),
		actorID,
	)
}

func TestUserAdminMCPDeletionMigrationNewComposesDeletionService(t *testing.T) {
	adapter := newUserAdminMCPDeletionMigrationAdapter(&userAdminMCPDeletionMigrationStore{})
	if adapter.userDeletions == nil {
		t.Fatal("MCP deletion service was not composed")
	}
}

func TestUserAdminMCPDeletionMigrationValidationPreventsServiceEntry(t *testing.T) {
	tests := []struct {
		name  string
		input any
	}{
		{name: "missing user ID", input: map[string]any{}},
		{name: "zero user ID", input: map[string]any{"userId": 0}},
		{name: "negative user ID", input: map[string]any{"userId": -1}},
		{name: "malformed user ID", input: map[string]any{"userId": "71"}},
		{name: "unknown field", input: map[string]any{"userId": 71, "extra": true}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &userAdminMCPDeletionMigrationStore{
				requesterID: 41,
				requester:   store.User{ID: 41, SystemRole: store.SystemRoleOwner},
			}
			adapter := newUserAdminMCPDeletionMigrationAdapter(fake)

			_, _, adapterErr := adapter.handleAdminDeleteUser(
				userAdminMCPDeletionContext(41, "validation-request"),
				tc.input,
			)
			if adapterErr == nil || adapterErr.Status != 400 || adapterErr.Code != CodeValidationError {
				t.Fatalf("validation error = %+v", adapterErr)
			}
			if len(fake.trace) != 0 || fake.deleteCalls != 0 {
				t.Fatalf("validation reached deletion service: trace=%v deleteCalls=%d", fake.trace, fake.deleteCalls)
			}
		})
	}
}

func TestUserAdminMCPDeletionMigrationDelegatesOneExactSequence(t *testing.T) {
	fake := &userAdminMCPDeletionMigrationStore{
		requesterID: 41,
		requester:   store.User{ID: 41, SystemRole: store.SystemRoleOwner},
	}
	adapter := newUserAdminMCPDeletionMigrationAdapter(fake)

	data, meta, adapterErr := adapter.handleAdminDeleteUser(
		userAdminMCPDeletionContext(41, "original-request"),
		map[string]any{"userId": int64(71)},
	)
	if adapterErr != nil {
		t.Fatalf("handleAdminDeleteUser() error = %+v", adapterErr)
	}
	if !reflect.DeepEqual(fake.trace, []string{"requester-read", "delete"}) {
		t.Fatalf("deletion trace = %v", fake.trace)
	}
	if fake.requesterReadID != 41 || fake.deleteRequesterID != 41 ||
		fake.deleteTargetID != 71 || fake.deleteCalls != 1 {
		t.Fatalf(
			"deletion capture = read:%d requester:%d target:%d calls:%d",
			fake.requesterReadID,
			fake.deleteRequesterID,
			fake.deleteTargetID,
			fake.deleteCalls,
		)
	}
	if !reflect.DeepEqual(fake.contextValues, []any{"original-request", "original-request"}) {
		t.Fatalf("captured context values = %v", fake.contextValues)
	}
	if len(meta) != 0 {
		t.Fatalf("deletion metadata = %+v", meta)
	}
	wantData := map[string]any{"status": "deleted", "userId": int64(71)}
	if !reflect.DeepEqual(data, wantData) {
		t.Fatalf("deletion result = %#v, want %#v", data, wantData)
	}
}

func TestUserAdminMCPDeletionMigrationPreservesStageMappings(t *testing.T) {
	tests := []struct {
		name          string
		requesterRole store.SystemRole
		requesterErr  error
		deletionErr   error
		wantTrace     []string
		wantStatus    int
		wantCode      string
		wantMessage   string
	}{
		{
			name:          "requester unauthorized uses ordinary mapper",
			requesterRole: store.SystemRoleOwner,
			requesterErr:  store.ErrUnauthorized,
			wantTrace:     []string{"requester-read"},
			wantStatus:    401,
			wantCode:      CodeAuthRequired,
			wantMessage:   "Sign-in required for this tool",
		},
		{
			name:          "Admin requester uses exact Owner forbidden projection",
			requesterRole: store.SystemRoleAdmin,
			wantTrace:     []string{"requester-read"},
			wantStatus:    403,
			wantCode:      CodeForbidden,
			wantMessage:   "forbidden",
		},
		{
			name:          "User requester uses exact Owner forbidden projection",
			requesterRole: store.SystemRoleUser,
			wantTrace:     []string{"requester-read"},
			wantStatus:    403,
			wantCode:      CodeForbidden,
			wantMessage:   "forbidden",
		},
		{
			name:          "mutation unauthorized uses privileged mapper",
			requesterRole: store.SystemRoleOwner,
			deletionErr:   store.ErrUnauthorized,
			wantTrace:     []string{"requester-read", "delete"},
			wantStatus:    403,
			wantCode:      CodeForbidden,
			wantMessage:   "forbidden",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &userAdminMCPDeletionMigrationStore{
				requesterID:  41,
				requester:    store.User{ID: 41, SystemRole: tc.requesterRole},
				requesterErr: tc.requesterErr,
				deletionErr:  tc.deletionErr,
			}
			adapter := newUserAdminMCPDeletionMigrationAdapter(fake)

			_, _, adapterErr := adapter.handleAdminDeleteUser(
				userAdminMCPDeletionContext(41, "stage-request"),
				map[string]any{"userId": int64(71)},
			)
			if adapterErr == nil || adapterErr.Status != tc.wantStatus ||
				adapterErr.Code != tc.wantCode || adapterErr.Message != tc.wantMessage {
				t.Fatalf("stage error = %+v", adapterErr)
			}
			if !reflect.DeepEqual(fake.trace, tc.wantTrace) {
				t.Fatalf("stage trace = %v, want %v", fake.trace, tc.wantTrace)
			}
			if fake.deleteCalls > 1 {
				t.Fatalf("delete calls = %d, want at most one", fake.deleteCalls)
			}
			if details := clientErrorDetails(adapterErr); len(details) != 0 {
				t.Fatalf("public error details = %#v, want empty", details)
			}
		})
	}
}

func TestUserAdminMCPDeletionMigrationPreservesSelfAndTargetOrdering(t *testing.T) {
	selfValidation := fmt.Errorf("%w: cannot delete yourself", store.ErrValidation)
	tests := []struct {
		name          string
		requesterRole store.SystemRole
		targetID      int64
		deletionErr   error
		wantTrace     []string
		wantStatus    int
		wantCode      string
		wantMessage   string
	}{
		{
			name:          "Admin self-target stops before store self check",
			requesterRole: store.SystemRoleAdmin,
			targetID:      41,
			wantTrace:     []string{"requester-read"},
			wantStatus:    403,
			wantCode:      CodeForbidden,
			wantMessage:   "forbidden",
		},
		{
			name:          "Owner self-target reaches store self check",
			requesterRole: store.SystemRoleOwner,
			targetID:      41,
			deletionErr:   selfValidation,
			wantTrace:     []string{"requester-read", "delete"},
			wantStatus:    400,
			wantCode:      CodeValidationError,
			wantMessage:   selfValidation.Error(),
		},
		{
			name:          "Admin missing target stops at Owner classification",
			requesterRole: store.SystemRoleAdmin,
			targetID:      999999,
			wantTrace:     []string{"requester-read"},
			wantStatus:    403,
			wantCode:      CodeForbidden,
			wantMessage:   "forbidden",
		},
		{
			name:          "Owner missing target reaches store",
			requesterRole: store.SystemRoleOwner,
			targetID:      999999,
			deletionErr:   store.ErrNotFound,
			wantTrace:     []string{"requester-read", "delete"},
			wantStatus:    404,
			wantCode:      CodeNotFound,
			wantMessage:   "not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &userAdminMCPDeletionMigrationStore{
				requesterID: 41,
				requester:   store.User{ID: 41, SystemRole: tc.requesterRole},
				deletionErr: tc.deletionErr,
			}
			adapter := newUserAdminMCPDeletionMigrationAdapter(fake)

			_, _, adapterErr := adapter.handleAdminDeleteUser(
				userAdminMCPDeletionContext(41, "ordering-request"),
				map[string]any{"userId": tc.targetID},
			)
			if adapterErr == nil || adapterErr.Status != tc.wantStatus ||
				adapterErr.Code != tc.wantCode || adapterErr.Message != tc.wantMessage {
				t.Fatalf("ordering error = %+v", adapterErr)
			}
			if !reflect.DeepEqual(fake.trace, tc.wantTrace) {
				t.Fatalf("ordering trace = %v, want %v", fake.trace, tc.wantTrace)
			}
			wantDeleteCalls := 0
			if reflect.DeepEqual(tc.wantTrace, []string{"requester-read", "delete"}) {
				wantDeleteCalls = 1
			}
			if fake.deleteCalls != wantDeleteCalls {
				t.Fatalf("delete calls = %d, want %d", fake.deleteCalls, wantDeleteCalls)
			}
		})
	}
}

func TestUserAdminMCPDeletionMigrationMutationErrorsAreNotRetried(t *testing.T) {
	for _, deletionErr := range []error{
		store.ErrUnauthorized,
		store.ErrForbidden,
		store.ErrNotFound,
		fmt.Errorf("%w: cannot delete yourself", store.ErrValidation),
		fmt.Errorf("%w: delete conflict", store.ErrConflict),
		errors.New("delete failed"),
	} {
		t.Run(deletionErr.Error(), func(t *testing.T) {
			fake := &userAdminMCPDeletionMigrationStore{
				requesterID: 41,
				requester:   store.User{ID: 41, SystemRole: store.SystemRoleOwner},
				deletionErr: deletionErr,
			}
			adapter := newUserAdminMCPDeletionMigrationAdapter(fake)

			_, _, got := adapter.handleAdminDeleteUser(
				userAdminMCPDeletionContext(41, "error-request"),
				map[string]any{"userId": int64(71)},
			)
			want := mapPrivilegedStoreError(deletionErr)
			assertUserAdminDeletionAdapterErrorMatches(t, got, want)
			if fake.deleteCalls != 1 || !reflect.DeepEqual(fake.trace, []string{"requester-read", "delete"}) {
				t.Fatalf("mutation error trace=%v deleteCalls=%d", fake.trace, fake.deleteCalls)
			}
		})
	}
}
