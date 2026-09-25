package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	useradminapp "scrumboy/internal/application/useradmin"
	"scrumboy/internal/store"
)

type userAdminRESTRoleContextKey struct{}

type userAdminRESTRoleRecorder struct {
	trace []string

	mutationCalls int
	requesterID   int64
	targetID      int64
	role          store.SystemRole
	mutationMark  any
	mutationErr   error

	projectionCalls int
	projectionID    int64
	projectionMark  any
	projectionUser  store.User
	projectionErr   error
}

func (r *userAdminRESTRoleRecorder) UpdateUserRole(
	ctx context.Context,
	requesterID int64,
	targetUserID int64,
	newRole store.SystemRole,
) error {
	r.trace = append(r.trace, "update-role")
	r.mutationCalls++
	r.requesterID = requesterID
	r.targetID = targetUserID
	r.role = newRole
	r.mutationMark = ctx.Value(userAdminRESTRoleContextKey{})
	return r.mutationErr
}

func (r *userAdminRESTRoleRecorder) GetUser(
	ctx context.Context,
	userID int64,
) (store.User, error) {
	r.trace = append(r.trace, "projection-read")
	r.projectionCalls++
	r.projectionID = userID
	r.projectionMark = ctx.Value(userAdminRESTRoleContextKey{})
	return r.projectionUser, r.projectionErr
}

func newUserAdminRESTRoleService(
	recorder *userAdminRESTRoleRecorder,
) *useradminapp.RESTRoleService {
	return useradminapp.NewRESTRoleService(useradminapp.RESTRoleServiceDependencies{
		Mutations:      recorder,
		ProjectionRead: recorder,
	})
}

func userAdminRESTFixtureServer(t *testing.T, fixture *adminUserRESTFixture) *Server {
	t.Helper()
	server, ok := fixture.ts.Config.Handler.(*Server)
	if !ok {
		t.Fatalf("HTTP fixture handler type = %T, want *Server", fixture.ts.Config.Handler)
	}
	return server
}

func doUserAdminRESTRoleRaw(
	t *testing.T,
	client *http.Client,
	method string,
	url string,
	body string,
) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Scrumboy", "1")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return resp, responseBody
}

func TestUserAdminRESTRoleMigrationNewServerComposesService(t *testing.T) {
	fixture := newAdminUserRESTFixture(t)
	server := userAdminRESTFixtureServer(t, fixture)
	if server.userRoleMutations == nil {
		t.Fatal("NewServer userRoleMutations = nil")
	}
}

func TestUserAdminRESTRoleMigrationDelegatesAfterValidation(t *testing.T) {
	fixture := newAdminUserRESTFixture(t)
	server := userAdminRESTFixtureServer(t, fixture)
	recorder := &userAdminRESTRoleRecorder{}
	server.userRoleMutations = newUserAdminRESTRoleService(recorder)
	baseURL := fixture.ts.URL + "/api/admin/users/"

	tests := []struct {
		name        string
		method      string
		target      string
		body        string
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name: "wrong method", method: http.MethodPost, target: "1", body: `{"role":"admin"}`,
			wantStatus: http.StatusMethodNotAllowed, wantCode: "METHOD_NOT_ALLOWED", wantMessage: "method not allowed",
		},
		{
			name: "malformed target", method: http.MethodPatch, target: "not-an-id", body: `{"role":"admin"}`,
			wantStatus: http.StatusBadRequest, wantCode: "BAD_REQUEST", wantMessage: "invalid user id",
		},
		{
			name: "zero target", method: http.MethodPatch, target: "0", body: `{"role":"admin"}`,
			wantStatus: http.StatusBadRequest, wantCode: "BAD_REQUEST", wantMessage: "invalid user id",
		},
		{
			name: "negative target", method: http.MethodPatch, target: "-1", body: `{"role":"admin"}`,
			wantStatus: http.StatusBadRequest, wantCode: "BAD_REQUEST", wantMessage: "invalid user id",
		},
		{
			name: "malformed body", method: http.MethodPatch, target: "1", body: `{bad`,
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "invalid json",
		},
		{
			name: "unknown field", method: http.MethodPatch, target: "1", body: `{"role":"admin","extra":true}`,
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "invalid json",
		},
		{
			name: "missing role", method: http.MethodPatch, target: "1", body: `{}`,
			wantStatus: http.StatusBadRequest, wantCode: "BAD_REQUEST", wantMessage: "role must be 'admin' or 'user'",
		},
		{
			name: "owner role", method: http.MethodPatch, target: "1", body: `{"role":"owner"}`,
			wantStatus: http.StatusBadRequest, wantCode: "BAD_REQUEST", wantMessage: "role must be 'admin' or 'user'",
		},
		{
			name: "uppercase role", method: http.MethodPatch, target: "1", body: `{"role":"ADMIN"}`,
			wantStatus: http.StatusBadRequest, wantCode: "BAD_REQUEST", wantMessage: "role must be 'admin' or 'user'",
		},
		{
			name: "whitespace role", method: http.MethodPatch, target: "1", body: `{"role":" admin "}`,
			wantStatus: http.StatusBadRequest, wantCode: "BAD_REQUEST", wantMessage: "role must be 'admin' or 'user'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beforeTrace := append([]string(nil), recorder.trace...)
			resp, body := doUserAdminRESTRoleRaw(
				t,
				fixture.ownerClient,
				tt.method,
				baseURL+tt.target+"/role",
				tt.body,
			)
			assertAdminRESTError(t, resp, body, tt.wantStatus, tt.wantCode, tt.wantMessage)
			if !reflect.DeepEqual(recorder.trace, beforeTrace) ||
				recorder.mutationCalls != 0 || recorder.projectionCalls != 0 {
				t.Fatalf(
					"validation reached role service: trace=%v mutation/projection=%d/%d",
					recorder.trace,
					recorder.mutationCalls,
					recorder.projectionCalls,
				)
			}
		})
	}
}

func TestUserAdminRESTRoleMigrationDelegatesOnceAndProjectsResult(t *testing.T) {
	fixture := newAdminUserRESTFixture(t)
	server := userAdminRESTFixtureServer(t, fixture)
	image := "data:image/png;base64,rest-role-migration"
	projected := store.User{
		ID:               fixture.user.ID,
		Email:            "projected-role@example.com",
		Name:             "Projected Role User",
		Image:            &image,
		SystemRole:       store.SystemRoleAdmin,
		CreatedAt:        time.Date(2026, time.August, 24, 13, 14, 15, 0, time.UTC),
		HasLocalPassword: true,
		OIDCLinked:       true,
	}
	recorder := &userAdminRESTRoleRecorder{projectionUser: projected}
	server.userRoleMutations = newUserAdminRESTRoleService(recorder)
	fixture.spy.calls = nil
	fixture.spy.updateCalls = 0
	fixture.collector.events = nil

	var got userJSON
	resp, body := doJSON(
		t,
		fixture.ownerClient,
		http.MethodPatch,
		fmt.Sprintf("%s/api/admin/users/%d/role", fixture.ts.URL, fixture.user.ID),
		map[string]any{"role": "admin"},
		&got,
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if want := userToJSON(projected); !reflect.DeepEqual(got, want) {
		t.Fatalf("response = %+v, want %+v", got, want)
	}
	if wantTrace := []string{"update-role", "projection-read"}; !reflect.DeepEqual(recorder.trace, wantTrace) {
		t.Fatalf("application trace = %v, want %v", recorder.trace, wantTrace)
	}
	if recorder.mutationCalls != 1 || recorder.projectionCalls != 1 ||
		recorder.requesterID != fixture.owner.ID || recorder.targetID != fixture.user.ID ||
		recorder.projectionID != fixture.user.ID || recorder.role != store.SystemRoleAdmin {
		t.Fatalf("role recorder = %+v", recorder)
	}
	wantStoreTrace := []string{"get-user:" + fmt.Sprint(fixture.owner.ID)}
	if !reflect.DeepEqual(fixture.spy.calls, wantStoreTrace) || fixture.spy.updateCalls != 0 {
		t.Fatalf("adapter store trace = %v updateCalls=%d, want outer requester read only", fixture.spy.calls, fixture.spy.updateCalls)
	}
	if len(fixture.collector.events) != 0 {
		t.Fatalf("role migration emitted realtime events: %+v", fixture.collector.events)
	}
}

func TestUserAdminRESTRoleMigrationCapturesContextAndMapsMissingActor(t *testing.T) {
	t.Run("exact context and command reach prepared operation", func(t *testing.T) {
		projected := store.User{ID: 41, SystemRole: store.SystemRoleUser}
		recorder := &userAdminRESTRoleRecorder{projectionUser: projected}
		server := &Server{
			maxBody:           1 << 20,
			mode:              "full",
			userRoleMutations: newUserAdminRESTRoleService(recorder),
		}
		request := httptest.NewRequest(
			http.MethodPatch,
			"/api/admin/users/41/role",
			strings.NewReader(`{"role":"user"}`),
		)
		request = request.WithContext(store.WithUserID(
			context.WithValue(request.Context(), userAdminRESTRoleContextKey{}, "captured-context"),
			17,
		))
		response := httptest.NewRecorder()

		server.handleAdminUsersUpdateRole(response, request, "41")

		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if recorder.requesterID != 17 || recorder.targetID != 41 ||
			recorder.projectionID != 41 || recorder.role != store.SystemRoleUser ||
			recorder.mutationMark != "captured-context" || recorder.projectionMark != "captured-context" {
			t.Fatalf("captured role operation = %+v", recorder)
		}
		if !reflect.DeepEqual(recorder.trace, []string{"update-role", "projection-read"}) {
			t.Fatalf("trace = %v", recorder.trace)
		}
	})

	t.Run("actor sentinel keeps existing unauthorized envelope", func(t *testing.T) {
		recorder := &userAdminRESTRoleRecorder{}
		server := &Server{
			maxBody:           1 << 20,
			mode:              "full",
			userRoleMutations: newUserAdminRESTRoleService(recorder),
		}
		request := httptest.NewRequest(
			http.MethodPatch,
			"/api/admin/users/41/role",
			strings.NewReader(`{"role":"admin"}`),
		)
		response := httptest.NewRecorder()

		server.handleAdminUsersUpdateRole(response, request, "41")

		var envelope apiErrorEnvelope
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if response.Code != http.StatusUnauthorized ||
			envelope.Error.Code != "UNAUTHORIZED" || envelope.Error.Message != "unauthorized" {
			t.Fatalf("response status=%d error=%+v", response.Code, envelope.Error)
		}
		if strings.Contains(response.Body.String(), useradminapp.ErrActorRequired.Error()) ||
			recorder.mutationCalls != 0 || recorder.projectionCalls != 0 || len(recorder.trace) != 0 {
			t.Fatalf("actor sentinel leaked or reached capabilities: body=%s recorder=%+v", response.Body.String(), recorder)
		}
	})
}

func TestUserAdminRESTRoleMigrationPreservesExecutionErrorMapping(t *testing.T) {
	t.Run("mutation failure uses existing mapper and stops projection", func(t *testing.T) {
		fixture := newAdminUserRESTFixture(t)
		server := userAdminRESTFixtureServer(t, fixture)
		recorder := &userAdminRESTRoleRecorder{mutationErr: store.ErrUnauthorized}
		server.userRoleMutations = newUserAdminRESTRoleService(recorder)

		resp, body := doJSON(
			t,
			fixture.ownerClient,
			http.MethodPatch,
			fmt.Sprintf("%s/api/admin/users/%d/role", fixture.ts.URL, fixture.user.ID),
			map[string]any{"role": "admin"},
			nil,
		)
		assertAdminRESTError(t, resp, body, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		if recorder.mutationCalls != 1 || recorder.projectionCalls != 0 ||
			!reflect.DeepEqual(recorder.trace, []string{"update-role"}) {
			t.Fatalf("mutation failure recorder = %+v", recorder)
		}
	})

	t.Run("projection failure retains raw internal detail without retry", func(t *testing.T) {
		fixture := newAdminUserRESTFixture(t)
		server := userAdminRESTFixtureServer(t, fixture)
		projectionErr := errors.New("forced migration projection failure")
		recorder := &userAdminRESTRoleRecorder{projectionErr: projectionErr}
		server.userRoleMutations = newUserAdminRESTRoleService(recorder)

		resp, body := doJSON(
			t,
			fixture.ownerClient,
			http.MethodPatch,
			fmt.Sprintf("%s/api/admin/users/%d/role", fixture.ts.URL, fixture.user.ID),
			map[string]any{"role": "admin"},
			nil,
		)
		envelope := assertAdminRESTError(t, resp, body, http.StatusInternalServerError, "INTERNAL", "internal error")
		if envelope.Error.Details["detail"] != projectionErr.Error() {
			t.Fatalf("details = %+v, want detail %q", envelope.Error.Details, projectionErr)
		}
		if recorder.mutationCalls != 1 || recorder.projectionCalls != 1 ||
			!reflect.DeepEqual(recorder.trace, []string{"update-role", "projection-read"}) {
			t.Fatalf("projection failure recorder = %+v", recorder)
		}
	})
}
