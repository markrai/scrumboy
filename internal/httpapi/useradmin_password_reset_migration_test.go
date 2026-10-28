package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	useradminapp "scrumboy/internal/application/useradmin"
	"scrumboy/internal/store"
)

type userAdminRESTPasswordResetContextKey struct{}

type userAdminRESTPasswordResetRecorder struct {
	trace []string

	users    map[int64]store.User
	userErrs map[int64]error

	hash      string
	hashErr   error
	hashCalls int
	hashID    int64
	hashMark  any

	token       string
	expires     time.Time
	tokenErr    error
	tokenCalls  int
	tokenUserID int64
	tokenHash   string
	tokenSecret []byte
}

func (r *userAdminRESTPasswordResetRecorder) GetUser(ctx context.Context, userID int64) (store.User, error) {
	r.trace = append(r.trace, fmt.Sprintf("get-user:%d", userID))
	if r.userErrs != nil {
		if err := r.userErrs[userID]; err != nil {
			return store.User{}, err
		}
	}
	if r.users == nil {
		return store.User{}, nil
	}
	return r.users[userID], nil
}

func passwordResetMigrationServer(t *testing.T, ts *httptest.Server) *Server {
	t.Helper()
	server, ok := ts.Config.Handler.(*Server)
	if !ok {
		t.Fatalf("HTTP fixture handler type = %T, want *Server", ts.Config.Handler)
	}
	return server
}

func (r *userAdminRESTPasswordResetRecorder) GetUserPasswordHash(ctx context.Context, userID int64) (string, error) {
	r.trace = append(r.trace, "password-hash")
	r.hashCalls++
	r.hashID = userID
	r.hashMark = ctx.Value(userAdminRESTPasswordResetContextKey{})
	return r.hash, r.hashErr
}

func (r *userAdminRESTPasswordResetRecorder) GeneratePasswordResetToken(
	secret []byte,
	userID int64,
	passwordHash string,
) (string, time.Time, error) {
	r.trace = append(r.trace, "token")
	r.tokenCalls++
	r.tokenSecret = append([]byte(nil), secret...)
	r.tokenUserID = userID
	r.tokenHash = passwordHash
	return r.token, r.expires, r.tokenErr
}

func newUserAdminRESTPasswordResetService(
	recorder *userAdminRESTPasswordResetRecorder,
) *useradminapp.RESTPasswordResetService {
	return useradminapp.NewRESTPasswordResetService(useradminapp.RESTPasswordResetServiceDependencies{
		RequesterRead: recorder,
		TargetRead:    recorder,
		Hashes:        recorder,
		Tokens:        recorder,
	})
}

func TestUserAdminRESTPasswordResetMigrationNewServerComposesService(t *testing.T) {
	fixture := newAdminPasswordResetFixture(t, Options{EncryptionKey: testEncryptionKey, ScrumboyMode: "full"})
	server := passwordResetMigrationServer(t, fixture.ts)
	if server.userPasswordResets == nil {
		t.Fatal("NewServer userPasswordResets = nil")
	}
}

func TestUserAdminRESTPasswordResetMigrationHTTPGatesPreventDelegation(t *testing.T) {
	t.Run("missing encryption key", func(t *testing.T) {
		fixture := newAdminUserRESTFixture(t)
		server := userAdminRESTFixtureServer(t, fixture)
		recorder := &userAdminRESTPasswordResetRecorder{}
		server.userPasswordResets = newUserAdminRESTPasswordResetService(recorder)

		resp, body := doJSON(
			t,
			fixture.ownerClient,
			http.MethodPost,
			fmt.Sprintf("%s/api/admin/users/%d/password-reset", fixture.ts.URL, fixture.user.ID),
			map[string]any{},
			nil,
		)
		assertAdminRESTError(
			t, resp, body,
			http.StatusServiceUnavailable,
			"SERVICE_UNAVAILABLE",
			"Password reset is not configured. Set SCRUMBOY_ENCRYPTION_KEY (e.g. openssl rand -base64 32) and restart.",
		)
		if len(recorder.trace) != 0 || recorder.hashCalls != 0 || recorder.tokenCalls != 0 {
			t.Fatalf("encryption gate reached password-reset service: %+v", recorder)
		}
	})

	fixture := newAdminPasswordResetFixture(t, Options{EncryptionKey: testEncryptionKey, ScrumboyMode: "full"})
	server := passwordResetMigrationServer(t, fixture.ts)
	recorder := &userAdminRESTPasswordResetRecorder{}
	server.userPasswordResets = newUserAdminRESTPasswordResetService(recorder)
	baseURL := fixture.ts.URL + "/api/admin/users/"

	tests := []struct {
		name        string
		method      string
		target      string
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name: "wrong method", method: http.MethodGet, target: "1",
			wantStatus: http.StatusMethodNotAllowed, wantCode: "METHOD_NOT_ALLOWED", wantMessage: "method not allowed",
		},
		{
			name: "malformed target", method: http.MethodPost, target: "not-an-id",
			wantStatus: http.StatusBadRequest, wantCode: "BAD_REQUEST", wantMessage: "invalid user id",
		},
		{
			name: "zero target", method: http.MethodPost, target: "0",
			wantStatus: http.StatusBadRequest, wantCode: "BAD_REQUEST", wantMessage: "invalid user id",
		},
		{
			name: "negative target", method: http.MethodPost, target: "-1",
			wantStatus: http.StatusBadRequest, wantCode: "BAD_REQUEST", wantMessage: "invalid user id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beforeTrace := append([]string(nil), recorder.trace...)
			resp, body := doJSON(t, fixture.ownerClient, tt.method, baseURL+tt.target+"/password-reset", map[string]any{}, nil)
			assertAdminRESTError(t, resp, body, tt.wantStatus, tt.wantCode, tt.wantMessage)
			if !reflect.DeepEqual(recorder.trace, beforeTrace) || recorder.hashCalls != 0 || recorder.tokenCalls != 0 {
				t.Fatalf("validation reached password-reset service: trace=%v", recorder.trace)
			}
		})
	}
}

func TestUserAdminRESTPasswordResetMigrationDelegatesOnceAndProjectsResult(t *testing.T) {
	fixture := newAdminPasswordResetFixture(t, Options{EncryptionKey: testEncryptionKey, ScrumboyMode: "full"})
	server := passwordResetMigrationServer(t, fixture.ts)
	expires := time.Date(2026, time.October, 26, 16, 51, 0, 0, time.UTC)
	recorder := &userAdminRESTPasswordResetRecorder{
		users: map[int64]store.User{
			fixture.owner.ID: {ID: fixture.owner.ID, SystemRole: store.SystemRoleOwner},
			fixture.user.ID:  {ID: fixture.user.ID, SystemRole: store.SystemRoleUser},
		},
		hash:    "migration-hash",
		token:   "migration-token",
		expires: expires,
	}
	server.userPasswordResets = newUserAdminRESTPasswordResetService(recorder)

	var got map[string]any
	resp, body := doJSON(
		t,
		fixture.ownerClient,
		http.MethodPost,
		fmt.Sprintf("%s/api/admin/users/%d/password-reset", fixture.ts.URL, fixture.user.ID),
		map[string]any{},
		&got,
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	resetURL, _ := got["reset_url"].(string)
	if !strings.HasPrefix(resetURL, fixture.ts.URL+"/auth/reset-password?token=") {
		t.Fatalf("reset_url=%q", resetURL)
	}
	parsedURL, err := url.Parse(resetURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsedURL.Query().Get("token") != "migration-token" {
		t.Fatalf("token query = %q", parsedURL.RawQuery)
	}
	if got["expires_at"] != expires.UTC().Format(time.RFC3339) {
		t.Fatalf("expires_at=%v", got["expires_at"])
	}
	if recorder.hashCalls != 1 || recorder.tokenCalls != 1 ||
		recorder.hashID != fixture.user.ID || recorder.tokenUserID != fixture.user.ID ||
		recorder.tokenHash != "migration-hash" {
		t.Fatalf("password-reset recorder = %+v", recorder)
	}
	if !reflect.DeepEqual(recorder.tokenSecret, testEncryptionKey) {
		t.Fatalf("token secret = %q", recorder.tokenSecret)
	}
	wantTrace := []string{
		fmt.Sprintf("get-user:%d", fixture.owner.ID),
		fmt.Sprintf("get-user:%d", fixture.user.ID),
		"password-hash",
		"token",
	}
	if !reflect.DeepEqual(recorder.trace, wantTrace) {
		t.Fatalf("application trace = %v, want %v", recorder.trace, wantTrace)
	}
}

func TestUserAdminRESTPasswordResetMigrationCapturesContextAndMapsMissingActor(t *testing.T) {
	t.Run("exact context and command reach prepared operation", func(t *testing.T) {
		expires := time.Date(2026, time.October, 26, 17, 0, 0, 0, time.UTC)
		recorder := &userAdminRESTPasswordResetRecorder{
			users: map[int64]store.User{
				17: {ID: 17, SystemRole: store.SystemRoleOwner},
				41: {ID: 41, SystemRole: store.SystemRoleUser},
			},
			hash:    "captured-hash",
			token:   "captured-token",
			expires: expires,
		}
		server := &Server{
			maxBody:            1 << 20,
			mode:               "full",
			encryptionKey:      testEncryptionKey,
			userPasswordResets: newUserAdminRESTPasswordResetService(recorder),
		}
		request := httptest.NewRequest(http.MethodPost, "/api/admin/users/41/password-reset", nil)
		request = request.WithContext(store.WithUserID(
			context.WithValue(request.Context(), userAdminRESTPasswordResetContextKey{}, "captured-context"),
			17,
		))
		response := httptest.NewRecorder()

		server.handleAdminUsersPasswordReset(response, request, "41")

		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if recorder.hashID != 41 || recorder.tokenUserID != 41 || recorder.tokenHash != "captured-hash" ||
			recorder.hashMark != "captured-context" {
			t.Fatalf("captured password-reset operation = %+v", recorder)
		}
		if !reflect.DeepEqual(recorder.trace, []string{"get-user:17", "get-user:41", "password-hash", "token"}) {
			t.Fatalf("trace = %v", recorder.trace)
		}
	})

	t.Run("actor sentinel keeps existing unauthorized envelope", func(t *testing.T) {
		recorder := &userAdminRESTPasswordResetRecorder{}
		server := &Server{
			maxBody:            1 << 20,
			mode:               "full",
			encryptionKey:      testEncryptionKey,
			userPasswordResets: newUserAdminRESTPasswordResetService(recorder),
		}
		request := httptest.NewRequest(http.MethodPost, "/api/admin/users/41/password-reset", nil)
		response := httptest.NewRecorder()

		server.handleAdminUsersPasswordReset(response, request, "41")

		var envelope apiErrorEnvelope
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if response.Code != http.StatusUnauthorized ||
			envelope.Error.Code != "UNAUTHORIZED" || envelope.Error.Message != "unauthorized" {
			t.Fatalf("response status=%d error=%+v", response.Code, envelope.Error)
		}
		if strings.Contains(response.Body.String(), useradminapp.ErrActorRequired.Error()) ||
			recorder.hashCalls != 0 || recorder.tokenCalls != 0 || len(recorder.trace) != 0 {
			t.Fatalf("actor sentinel leaked or reached capabilities: body=%s recorder=%+v", response.Body.String(), recorder)
		}
	})
}

func TestUserAdminRESTPasswordResetMigrationPreservesExecutionErrorMapping(t *testing.T) {
	t.Run("store hash failure uses existing mapper and stops token issuance", func(t *testing.T) {
		fixture := newAdminPasswordResetFixture(t, Options{EncryptionKey: testEncryptionKey, ScrumboyMode: "full"})
		server := passwordResetMigrationServer(t, fixture.ts)
		forced := errors.New("forced migration hash failure")
		recorder := &userAdminRESTPasswordResetRecorder{
			users: map[int64]store.User{
				fixture.owner.ID: {ID: fixture.owner.ID, SystemRole: store.SystemRoleOwner},
				fixture.user.ID:  {ID: fixture.user.ID, SystemRole: store.SystemRoleUser},
			},
			hashErr: forced,
		}
		server.userPasswordResets = newUserAdminRESTPasswordResetService(recorder)

		resp, body := doJSON(
			t,
			fixture.ownerClient,
			http.MethodPost,
			fmt.Sprintf("%s/api/admin/users/%d/password-reset", fixture.ts.URL, fixture.user.ID),
			map[string]any{},
			nil,
		)
		envelope := assertAdminRESTError(t, resp, body, http.StatusInternalServerError, "INTERNAL", "internal error")
		if envelope.Error.Details["detail"] != forced.Error() {
			t.Fatalf("details = %+v, want detail %q", envelope.Error.Details, forced)
		}
		if recorder.hashCalls != 1 || recorder.tokenCalls != 0 {
			t.Fatalf("hash failure recorder = %+v", recorder)
		}
	})

	t.Run("token generation retains raw internal detail", func(t *testing.T) {
		fixture := newAdminPasswordResetFixture(t, Options{EncryptionKey: testEncryptionKey, ScrumboyMode: "full"})
		server := passwordResetMigrationServer(t, fixture.ts)
		forced := errors.New("forced migration token failure")
		recorder := &userAdminRESTPasswordResetRecorder{
			users: map[int64]store.User{
				fixture.owner.ID: {ID: fixture.owner.ID, SystemRole: store.SystemRoleOwner},
				fixture.user.ID:  {ID: fixture.user.ID, SystemRole: store.SystemRoleUser},
			},
			hash:     "migration-hash",
			tokenErr: forced,
		}
		server.userPasswordResets = newUserAdminRESTPasswordResetService(recorder)

		resp, body := doJSON(
			t,
			fixture.ownerClient,
			http.MethodPost,
			fmt.Sprintf("%s/api/admin/users/%d/password-reset", fixture.ts.URL, fixture.user.ID),
			map[string]any{},
			nil,
		)
		envelope := assertAdminRESTError(t, resp, body, http.StatusInternalServerError, "INTERNAL", "internal error")
		if envelope.Error.Details["detail"] != forced.Error() {
			t.Fatalf("details = %+v, want detail %q", envelope.Error.Details, forced)
		}
		if recorder.hashCalls != 1 || recorder.tokenCalls != 1 {
			t.Fatalf("token failure recorder = %+v", recorder)
		}
	})
}
