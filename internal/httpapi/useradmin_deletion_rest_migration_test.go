package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	useradminapp "scrumboy/internal/application/useradmin"
	"scrumboy/internal/store"
)

type userAdminRESTDeletionContextKey struct{}

type userAdminRESTDeletionRecorder struct {
	trace []string

	deleteCalls int
	requesterID int64
	targetID    int64
	contextMark any
	deleteErr   error
}

func (r *userAdminRESTDeletionRecorder) DeleteUser(
	ctx context.Context,
	requesterID int64,
	targetUserID int64,
) error {
	r.trace = append(r.trace, "delete-user")
	r.deleteCalls++
	r.requesterID = requesterID
	r.targetID = targetUserID
	r.contextMark = ctx.Value(userAdminRESTDeletionContextKey{})
	return r.deleteErr
}

func newUserAdminRESTDeletionService(
	recorder *userAdminRESTDeletionRecorder,
) *useradminapp.RESTDeletionService {
	return useradminapp.NewRESTDeletionService(useradminapp.RESTDeletionServiceDependencies{
		Deletions: recorder,
	})
}

func TestUserAdminRESTDeletionMigrationNewServerComposesService(t *testing.T) {
	fixture := newAdminUserRESTFixture(t)
	server := userAdminRESTFixtureServer(t, fixture)
	if server.userDeletions == nil {
		t.Fatal("NewServer userDeletions = nil")
	}
}

func TestUserAdminRESTDeletionMigrationValidationPreventsDelegation(t *testing.T) {
	fixture := newAdminUserRESTFixture(t)
	server := userAdminRESTFixtureServer(t, fixture)
	recorder := &userAdminRESTDeletionRecorder{}
	server.userDeletions = newUserAdminRESTDeletionService(recorder)
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
			name: "wrong method", method: http.MethodPost, target: "1",
			wantStatus: http.StatusMethodNotAllowed, wantCode: "METHOD_NOT_ALLOWED", wantMessage: "method not allowed",
		},
		{
			name: "malformed target", method: http.MethodDelete, target: "not-an-id",
			wantStatus: http.StatusBadRequest, wantCode: "BAD_REQUEST", wantMessage: "invalid user id",
		},
		{
			name: "zero target", method: http.MethodDelete, target: "0",
			wantStatus: http.StatusBadRequest, wantCode: "BAD_REQUEST", wantMessage: "invalid user id",
		},
		{
			name: "negative target", method: http.MethodDelete, target: "-1",
			wantStatus: http.StatusBadRequest, wantCode: "BAD_REQUEST", wantMessage: "invalid user id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := doJSON(
				t,
				fixture.ownerClient,
				tt.method,
				baseURL+tt.target,
				nil,
				nil,
			)
			assertAdminRESTError(t, resp, body, tt.wantStatus, tt.wantCode, tt.wantMessage)
			if recorder.deleteCalls != 0 || len(recorder.trace) != 0 {
				t.Fatalf("validation reached deletion service: recorder=%+v", recorder)
			}
		})
	}
}

func TestUserAdminRESTDeletionMigrationDelegatesOnceWithOuterReadOnly(t *testing.T) {
	fixture := newAdminUserRESTFixture(t)
	server := userAdminRESTFixtureServer(t, fixture)
	recorder := &userAdminRESTDeletionRecorder{}
	server.userDeletions = newUserAdminRESTDeletionService(recorder)
	fixture.spy.calls = nil
	fixture.spy.deleteCalls = 0
	fixture.collector.events = nil

	resp, body := doJSON(
		t,
		fixture.ownerClient,
		http.MethodDelete,
		fmt.Sprintf("%s/api/admin/users/%d", fixture.ts.URL, fixture.user.ID),
		nil,
		nil,
	)
	if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
		t.Fatalf("status=%d body=%q, want 204 empty", resp.StatusCode, body)
	}
	if recorder.deleteCalls != 1 || recorder.requesterID != fixture.owner.ID ||
		recorder.targetID != fixture.user.ID ||
		!reflect.DeepEqual(recorder.trace, []string{"delete-user"}) {
		t.Fatalf("application deletion recorder = %+v", recorder)
	}
	wantStoreTrace := []string{"get-user:" + fmt.Sprint(fixture.owner.ID)}
	if !reflect.DeepEqual(fixture.spy.calls, wantStoreTrace) || fixture.spy.deleteCalls != 0 {
		t.Fatalf(
			"adapter store trace=%v deleteCalls=%d, want outer requester read only",
			fixture.spy.calls,
			fixture.spy.deleteCalls,
		)
	}
	if len(fixture.collector.events) != 0 {
		t.Fatalf("REST deletion migration emitted realtime events: %+v", fixture.collector.events)
	}
}

func TestUserAdminRESTDeletionMigrationAdminSelfTargetReachesDeletionPort(t *testing.T) {
	fixture := newAdminUserRESTFixture(t)
	server := userAdminRESTFixtureServer(t, fixture)
	recorder := &userAdminRESTDeletionRecorder{
		deleteErr: fmt.Errorf("%w: cannot delete yourself", store.ErrValidation),
	}
	server.userDeletions = newUserAdminRESTDeletionService(recorder)
	fixture.spy.calls = nil
	fixture.spy.deleteCalls = 0
	fixture.collector.events = nil

	resp, body := doJSON(
		t,
		fixture.adminClient,
		http.MethodDelete,
		fmt.Sprintf("%s/api/admin/users/%d", fixture.ts.URL, fixture.admin.ID),
		nil,
		nil,
	)
	envelope := assertAdminRESTError(
		t,
		resp,
		body,
		http.StatusBadRequest,
		"VALIDATION_ERROR",
		"validation: cannot delete yourself",
	)
	if got := envelope.Error.Details["reason"]; got != "cannot_delete_self" {
		t.Fatalf("error details=%+v, want reason cannot_delete_self", envelope.Error.Details)
	}
	if recorder.deleteCalls != 1 || recorder.requesterID != fixture.admin.ID ||
		recorder.targetID != fixture.admin.ID ||
		!reflect.DeepEqual(recorder.trace, []string{"delete-user"}) {
		t.Fatalf("Admin self-target deletion recorder = %+v", recorder)
	}
	wantStoreTrace := []string{"get-user:" + fmt.Sprint(fixture.admin.ID)}
	if !reflect.DeepEqual(fixture.spy.calls, wantStoreTrace) || fixture.spy.deleteCalls != 0 {
		t.Fatalf(
			"adapter store trace=%v deleteCalls=%d, want outer Admin read only",
			fixture.spy.calls,
			fixture.spy.deleteCalls,
		)
	}
	if len(fixture.collector.events) != 0 {
		t.Fatalf("Admin self-delete emitted realtime events: %+v", fixture.collector.events)
	}
}

func TestUserAdminRESTDeletionMigrationAdminNonSelfRetainsStoreAuthorityMapping(t *testing.T) {
	tests := []struct {
		name     string
		targetID func(*adminUserRESTFixture) int64
	}{
		{name: "existing target", targetID: func(f *adminUserRESTFixture) int64 { return f.user.ID }},
		{name: "missing target", targetID: func(*adminUserRESTFixture) int64 { return 999999 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newAdminUserRESTFixture(t)
			server := userAdminRESTFixtureServer(t, fixture)
			recorder := &userAdminRESTDeletionRecorder{deleteErr: store.ErrUnauthorized}
			server.userDeletions = newUserAdminRESTDeletionService(recorder)
			fixture.spy.calls = nil
			fixture.spy.deleteCalls = 0
			fixture.collector.events = nil
			targetID := tt.targetID(fixture)

			resp, body := doJSON(
				t,
				fixture.adminClient,
				http.MethodDelete,
				fmt.Sprintf("%s/api/admin/users/%d", fixture.ts.URL, targetID),
				nil,
				nil,
			)
			assertAdminRESTError(t, resp, body, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
			if recorder.deleteCalls != 1 || recorder.requesterID != fixture.admin.ID ||
				recorder.targetID != targetID {
				t.Fatalf("Admin deletion recorder = %+v", recorder)
			}
			wantStoreTrace := []string{"get-user:" + fmt.Sprint(fixture.admin.ID)}
			if !reflect.DeepEqual(fixture.spy.calls, wantStoreTrace) || fixture.spy.deleteCalls != 0 {
				t.Fatalf(
					"adapter store trace=%v deleteCalls=%d, want outer Admin read only",
					fixture.spy.calls,
					fixture.spy.deleteCalls,
				)
			}
			if len(fixture.collector.events) != 0 {
				t.Fatalf("Admin deletion failure emitted realtime events: %+v", fixture.collector.events)
			}
		})
	}
}

func TestUserAdminRESTDeletionMigrationCapturesContextAndMapsMissingActor(t *testing.T) {
	t.Run("exact context and command reach prepared operation", func(t *testing.T) {
		recorder := &userAdminRESTDeletionRecorder{}
		server := &Server{
			mode:          "full",
			userDeletions: newUserAdminRESTDeletionService(recorder),
		}
		request := httptest.NewRequest(http.MethodDelete, "/api/admin/users/41", nil)
		request = request.WithContext(store.WithUserID(
			context.WithValue(request.Context(), userAdminRESTDeletionContextKey{}, "captured-context"),
			17,
		))
		response := httptest.NewRecorder()

		server.handleAdminUsersDelete(response, request, "41")

		if response.Code != http.StatusNoContent {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if recorder.deleteCalls != 1 || recorder.requesterID != 17 || recorder.targetID != 41 ||
			recorder.contextMark != "captured-context" ||
			!reflect.DeepEqual(recorder.trace, []string{"delete-user"}) {
			t.Fatalf("captured deletion operation = %+v", recorder)
		}
	})

	t.Run("actor sentinel keeps existing unauthorized envelope", func(t *testing.T) {
		recorder := &userAdminRESTDeletionRecorder{}
		server := &Server{
			mode:          "full",
			userDeletions: newUserAdminRESTDeletionService(recorder),
		}
		request := httptest.NewRequest(http.MethodDelete, "/api/admin/users/41", nil)
		response := httptest.NewRecorder()

		server.handleAdminUsersDelete(response, request, "41")

		var envelope apiErrorEnvelope
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if response.Code != http.StatusUnauthorized ||
			envelope.Error.Code != "UNAUTHORIZED" || envelope.Error.Message != "unauthorized" {
			t.Fatalf("response status=%d error=%+v", response.Code, envelope.Error)
		}
		if strings.Contains(response.Body.String(), useradminapp.ErrActorRequired.Error()) ||
			recorder.deleteCalls != 0 || len(recorder.trace) != 0 {
			t.Fatalf("actor sentinel leaked or reached deletion: body=%s recorder=%+v", response.Body.String(), recorder)
		}
	})
}

func TestUserAdminRESTDeletionMigrationPreservesExecutionErrorMapping(t *testing.T) {
	validationErr := fmt.Errorf("%w: cannot delete yourself", store.ErrValidation)
	conflictErr := fmt.Errorf("%w: forced deletion conflict", store.ErrConflict)
	unexpectedErr := errors.New("forced deletion failure")
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantMessage string
		wantDetails map[string]any
	}{
		{
			name: "unauthorized", err: store.ErrUnauthorized,
			wantStatus: http.StatusUnauthorized, wantCode: "UNAUTHORIZED", wantMessage: "unauthorized",
		},
		{
			name: "forbidden", err: store.ErrForbidden,
			wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN", wantMessage: "forbidden",
		},
		{
			name: "not found", err: store.ErrNotFound,
			wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantMessage: "not found",
		},
		{
			name: "validation", err: validationErr,
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: validationErr.Error(),
			wantDetails: map[string]any{"reason": "cannot_delete_self"},
		},
		{
			name: "conflict", err: conflictErr,
			wantStatus: http.StatusConflict, wantCode: "CONFLICT", wantMessage: conflictErr.Error(),
		},
		{
			name: "unexpected", err: unexpectedErr,
			wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL", wantMessage: "internal error",
			wantDetails: map[string]any{"detail": unexpectedErr.Error()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newAdminUserRESTFixture(t)
			server := userAdminRESTFixtureServer(t, fixture)
			recorder := &userAdminRESTDeletionRecorder{deleteErr: tt.err}
			server.userDeletions = newUserAdminRESTDeletionService(recorder)
			fixture.spy.calls = nil
			fixture.spy.deleteCalls = 0
			fixture.collector.events = nil

			resp, body := doJSON(
				t,
				fixture.ownerClient,
				http.MethodDelete,
				fmt.Sprintf("%s/api/admin/users/%d", fixture.ts.URL, fixture.user.ID),
				nil,
				nil,
			)
			envelope := assertAdminRESTError(t, resp, body, tt.wantStatus, tt.wantCode, tt.wantMessage)
			if !reflect.DeepEqual(envelope.Error.Details, tt.wantDetails) {
				t.Fatalf("error details=%+v, want %+v", envelope.Error.Details, tt.wantDetails)
			}
			if recorder.deleteCalls != 1 ||
				!reflect.DeepEqual(recorder.trace, []string{"delete-user"}) {
				t.Fatalf("execution error recorder = %+v", recorder)
			}
			wantStoreTrace := []string{"get-user:" + fmt.Sprint(fixture.owner.ID)}
			if !reflect.DeepEqual(fixture.spy.calls, wantStoreTrace) || fixture.spy.deleteCalls != 0 {
				t.Fatalf(
					"adapter store trace=%v deleteCalls=%d, want outer requester read only",
					fixture.spy.calls,
					fixture.spy.deleteCalls,
				)
			}
			if len(fixture.collector.events) != 0 {
				t.Fatalf("deletion error emitted realtime events: %+v", fixture.collector.events)
			}
		})
	}
}
