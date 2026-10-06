package mcp

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type userAdminMCPRoleContextKey struct{}

type userAdminMCPRoleMigrationStore struct {
	storeAPI

	trace []string

	requesterID   int64
	requester     store.User
	requesterErr  error
	projection    store.User
	projectionErr error
	mutationErr   error

	mutationRequesterID int64
	mutationTargetID    int64
	mutationRole        store.SystemRole
	contextValues       []string
}

func (s *userAdminMCPRoleMigrationStore) CountUsers(context.Context) (int, error) {
	return 1, nil
}

func (s *userAdminMCPRoleMigrationStore) GetUser(ctx context.Context, userID int64) (store.User, error) {
	s.contextValues = append(s.contextValues, ctx.Value(userAdminMCPRoleContextKey{}).(string))
	if userID == s.requesterID {
		s.trace = append(s.trace, "requester-read")
		return s.requester, s.requesterErr
	}

	s.trace = append(s.trace, "projection-read")
	return s.projection, s.projectionErr
}

func (s *userAdminMCPRoleMigrationStore) UpdateUserRole(
	ctx context.Context,
	requesterID int64,
	targetUserID int64,
	newRole store.SystemRole,
) error {
	s.trace = append(s.trace, "update")
	s.contextValues = append(s.contextValues, ctx.Value(userAdminMCPRoleContextKey{}).(string))
	s.mutationRequesterID = requesterID
	s.mutationTargetID = targetUserID
	s.mutationRole = newRole
	return s.mutationErr
}

func TestUserAdminMCPRoleMigrationNewComposesRoleService(t *testing.T) {
	adapter := New(&userAdminMCPRoleMigrationStore{}, Options{Mode: "full"})
	if adapter.userRoleMutations == nil {
		t.Fatal("MCP role service was not composed")
	}
}

func TestUserAdminMCPRoleMigrationDelegatesOneExactSequence(t *testing.T) {
	createdAt := time.Date(2026, time.August, 24, 12, 30, 0, 0, time.UTC)
	fake := &userAdminMCPRoleMigrationStore{
		requesterID: 41,
		requester: store.User{
			ID:         41,
			SystemRole: store.SystemRoleOwner,
		},
		projection: store.User{
			ID:          71,
			Email:       "target@example.com",
			Name:        "Target User",
			SystemRole:  store.SystemRoleAdmin,
			IsBootstrap: false,
			CreatedAt:   createdAt,
		},
	}
	adapter := New(fake, Options{Mode: "full"})
	ctx := store.WithUserID(
		context.WithValue(context.Background(), userAdminMCPRoleContextKey{}, "original-request"),
		41,
	)

	data, meta, adapterErr := adapter.handleAdminUpdateUserRole(ctx, map[string]any{
		"userId": int64(71),
		"role":   "admin",
	})
	if adapterErr != nil {
		t.Fatalf("handleAdminUpdateUserRole() error = %+v", adapterErr)
	}
	if !reflect.DeepEqual(fake.trace, []string{"requester-read", "update", "projection-read"}) {
		t.Fatalf("role mutation trace = %v", fake.trace)
	}
	if fake.mutationRequesterID != 41 || fake.mutationTargetID != 71 || fake.mutationRole != store.SystemRoleAdmin {
		t.Fatalf(
			"role mutation capture = requester:%d target:%d role:%q",
			fake.mutationRequesterID,
			fake.mutationTargetID,
			fake.mutationRole,
		)
	}
	if !reflect.DeepEqual(fake.contextValues, []string{"original-request", "original-request", "original-request"}) {
		t.Fatalf("captured context values = %v", fake.contextValues)
	}
	if len(meta) != 0 {
		t.Fatalf("role mutation metadata = %+v", meta)
	}
	wantUser := adminUserItem{
		UserID:      71,
		Email:       "target@example.com",
		Name:        "Target User",
		SystemRole:  "admin",
		IsBootstrap: false,
		CreatedAt:   createdAt,
	}
	if got := data.(map[string]any)["user"]; !reflect.DeepEqual(got, wantUser) {
		t.Fatalf("role mutation user = %#v, want %#v", got, wantUser)
	}
}

func TestUserAdminMCPRoleMigrationValidationPreventsServiceEntry(t *testing.T) {
	tests := []struct {
		name  string
		input any
	}{
		{name: "missing user ID", input: map[string]any{"role": "admin"}},
		{name: "negative user ID", input: map[string]any{"userId": -1, "role": "admin"}},
		{name: "malformed user ID", input: map[string]any{"userId": "71", "role": "admin"}},
		{name: "owner role", input: map[string]any{"userId": 71, "role": "owner"}},
		{name: "uppercase role", input: map[string]any{"userId": 71, "role": "Admin"}},
		{name: "whitespace role", input: map[string]any{"userId": 71, "role": " admin "}},
		{name: "unknown role", input: map[string]any{"userId": 71, "role": "maintainer"}},
		{name: "unknown field", input: map[string]any{"userId": 71, "role": "admin", "extra": true}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &userAdminMCPRoleMigrationStore{
				requesterID: 41,
				requester:   store.User{ID: 41, SystemRole: store.SystemRoleOwner},
			}
			adapter := New(fake, Options{Mode: "full"})
			ctx := store.WithUserID(
				context.WithValue(context.Background(), userAdminMCPRoleContextKey{}, "validation-request"),
				41,
			)

			_, _, adapterErr := adapter.handleAdminUpdateUserRole(ctx, tc.input)
			if adapterErr == nil || adapterErr.Status != 400 || adapterErr.Code != CodeValidationError {
				t.Fatalf("validation error = %+v", adapterErr)
			}
			if len(fake.trace) != 0 {
				t.Fatalf("service trace = %v, want empty", fake.trace)
			}
		})
	}
}

func TestUserAdminMCPRoleMigrationPreservesStageMappings(t *testing.T) {
	tests := []struct {
		name          string
		requesterRole store.SystemRole
		requesterErr  error
		mutationErr   error
		projectionErr error
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
			name:          "non-owner uses exact forbidden projection",
			requesterRole: store.SystemRoleAdmin,
			wantTrace:     []string{"requester-read"},
			wantStatus:    403,
			wantCode:      CodeForbidden,
			wantMessage:   "forbidden",
		},
		{
			name:          "mutation unauthorized uses privileged mapper",
			requesterRole: store.SystemRoleOwner,
			mutationErr:   store.ErrUnauthorized,
			wantTrace:     []string{"requester-read", "update"},
			wantStatus:    403,
			wantCode:      CodeForbidden,
			wantMessage:   "forbidden",
		},
		{
			name:          "projection unauthorized uses ordinary mapper",
			requesterRole: store.SystemRoleOwner,
			projectionErr: store.ErrUnauthorized,
			wantTrace:     []string{"requester-read", "update", "projection-read"},
			wantStatus:    401,
			wantCode:      CodeAuthRequired,
			wantMessage:   "Sign-in required for this tool",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &userAdminMCPRoleMigrationStore{
				requesterID:   41,
				requester:     store.User{ID: 41, SystemRole: tc.requesterRole},
				requesterErr:  tc.requesterErr,
				mutationErr:   tc.mutationErr,
				projectionErr: tc.projectionErr,
			}
			adapter := New(fake, Options{Mode: "full"})
			ctx := store.WithUserID(
				context.WithValue(context.Background(), userAdminMCPRoleContextKey{}, "stage-request"),
				41,
			)

			_, _, adapterErr := adapter.handleAdminUpdateUserRole(ctx, map[string]any{
				"userId": int64(71),
				"role":   "user",
			})
			if adapterErr == nil || adapterErr.Status != tc.wantStatus || adapterErr.Code != tc.wantCode ||
				adapterErr.Message != tc.wantMessage {
				t.Fatalf("stage error = %+v", adapterErr)
			}
			if !reflect.DeepEqual(fake.trace, tc.wantTrace) {
				t.Fatalf("stage trace = %v, want %v", fake.trace, tc.wantTrace)
			}
			if details := clientErrorDetails(adapterErr); len(details) != 0 {
				t.Fatalf("public error details = %#v, want empty", details)
			}
		})
	}
}

func TestUserAdminMCPRoleMigrationMutationOccursAtMostOnce(t *testing.T) {
	fake := &userAdminMCPRoleMigrationStore{
		requesterID:   41,
		requester:     store.User{ID: 41, SystemRole: store.SystemRoleOwner},
		projectionErr: errors.New("projection unavailable"),
	}
	adapter := New(fake, Options{Mode: "full"})
	ctx := store.WithUserID(
		context.WithValue(context.Background(), userAdminMCPRoleContextKey{}, "single-request"),
		41,
	)

	_, _, adapterErr := adapter.handleAdminUpdateUserRole(ctx, map[string]any{
		"userId": int64(71),
		"role":   "user",
	})
	if adapterErr == nil || adapterErr.Code != CodeInternal {
		t.Fatalf("projection error = %+v", adapterErr)
	}
	if got := countTraceValue(fake.trace, "update"); got != 1 {
		t.Fatalf("update count = %d, want 1; trace = %v", got, fake.trace)
	}
}

func countTraceValue(trace []string, value string) int {
	count := 0
	for _, entry := range trace {
		if entry == value {
			count++
		}
	}
	return count
}
