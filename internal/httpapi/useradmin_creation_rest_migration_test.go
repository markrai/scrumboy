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

type userAdminRESTCreationContextKey struct{}

// userAdminRESTCreationRecorder is deliberately below the real application
// service: it implements the persistence port that production Store.CreateUser
// satisfies, so captured values are exactly those that would reach the store.
type userAdminRESTCreationRecorder struct {
	calls       int
	email       string
	password    string
	name        string
	contextMark any
	user        store.User
	err         error
}

func (r *userAdminRESTCreationRecorder) CreateUser(
	ctx context.Context,
	email string,
	password string,
	name string,
) (store.User, error) {
	r.calls++
	r.email = email
	r.password = password
	r.name = name
	r.contextMark = ctx.Value(userAdminRESTCreationContextKey{})
	return r.user, r.err
}

func newUserAdminRESTCreationService(
	recorder *userAdminRESTCreationRecorder,
) *useradminapp.RESTCreationService {
	return useradminapp.NewRESTCreationService(useradminapp.RESTCreationServiceDependencies{
		Creations: recorder,
	})
}

func doUserAdminRESTCreationRaw(
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

func TestUserAdminRESTCreationMigrationNewServerComposesService(t *testing.T) {
	fixture := newAdminUserRESTFixture(t)
	server := userAdminRESTFixtureServer(t, fixture)
	if server.userCreations == nil {
		t.Fatal("NewServer userCreations = nil")
	}
}

func TestUserAdminRESTCreationMigrationValidationPreventsDelegation(t *testing.T) {
	fixture := newAdminUserRESTFixture(t)
	server := userAdminRESTFixtureServer(t, fixture)
	recorder := &userAdminRESTCreationRecorder{}
	server.userCreations = newUserAdminRESTCreationService(recorder)
	url := fixture.ts.URL + "/api/admin/users"

	tests := []struct {
		name        string
		method      string
		body        string
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name: "unsupported method", method: http.MethodPut, body: `{}`,
			wantStatus: http.StatusMethodNotAllowed, wantCode: "METHOD_NOT_ALLOWED", wantMessage: "method not allowed",
		},
		{
			name: "malformed JSON", method: http.MethodPost, body: `{bad`,
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "invalid json",
		},
		{
			name: "unknown field", method: http.MethodPost,
			body:       `{"email":"user@example.com","name":"User","password":"password123","extra":true}`,
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "invalid json",
		},
		{
			name: "trailing JSON", method: http.MethodPost,
			body:       `{"email":"user@example.com","name":"User","password":"password123"} {}`,
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: "invalid json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := doUserAdminRESTCreationRaw(t, fixture.ownerClient, tt.method, url, tt.body)
			assertAdminRESTError(t, resp, body, tt.wantStatus, tt.wantCode, tt.wantMessage)
			if recorder.calls != 0 {
				t.Fatalf("validation reached creation port: recorder=%+v", recorder)
			}
		})
	}
}

func TestUserAdminRESTCreationMigrationRawValuesReachPersistencePort(t *testing.T) {
	fixture := newAdminUserRESTFixture(t)
	server := userAdminRESTFixtureServer(t, fixture)
	image := "data:image/png;base64,user-creation"
	projected := store.User{
		ID:               71,
		Email:            "mixed.case@example.com",
		Name:             "Created Name",
		Image:            &image,
		SystemRole:       store.SystemRoleUser,
		CreatedAt:        time.Date(2026, time.August, 24, 20, 45, 0, 0, time.UTC),
		HasLocalPassword: true,
		OIDCLinked:       true,
	}
	recorder := &userAdminRESTCreationRecorder{user: projected}
	server.userCreations = newUserAdminRESTCreationService(recorder)
	fixture.spy.calls = nil
	fixture.spy.createCalls = 0
	fixture.collector.events = nil

	var got userJSON
	resp, body := doJSON(
		t,
		fixture.adminClient,
		http.MethodPost,
		fixture.ts.URL+"/api/admin/users",
		map[string]any{
			"email":    "  Mixed.Case@Example.COM  ",
			"name":     "  Created Name  ",
			"password": "  password123  ",
		},
		&got,
	)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if want := userToJSON(projected); !reflect.DeepEqual(got, want) {
		t.Fatalf("response = %+v, want %+v", got, want)
	}
	if recorder.calls != 1 || recorder.email != "  Mixed.Case@Example.COM  " ||
		recorder.password != "  password123  " || recorder.name != "  Created Name  " {
		t.Fatalf("underlying UserCreationStore recorder = %+v", recorder)
	}
	wantStoreTrace := []string{"get-user:" + fmt.Sprint(fixture.admin.ID)}
	if !reflect.DeepEqual(fixture.spy.calls, wantStoreTrace) || fixture.spy.createCalls != 0 {
		t.Fatalf(
			"adapter store trace=%v createCalls=%d, want outer requester read only",
			fixture.spy.calls,
			fixture.spy.createCalls,
		)
	}
	if len(fixture.collector.events) != 0 {
		t.Fatalf("REST creation migration emitted realtime events: %+v", fixture.collector.events)
	}
}

func TestUserAdminRESTCreationMigrationCapturesExactContextAndCommand(t *testing.T) {
	projected := store.User{ID: 81, Email: "result@example.com", Name: "Result", SystemRole: store.SystemRoleUser}
	recorder := &userAdminRESTCreationRecorder{user: projected}
	server := &Server{
		maxBody:       1 << 20,
		mode:          "full",
		userCreations: newUserAdminRESTCreationService(recorder),
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/users",
		strings.NewReader(`{"email":" RAW@Example.COM ","name":" Raw Name ","password":" raw password "}`),
	)
	request = request.WithContext(context.WithValue(
		request.Context(),
		userAdminRESTCreationContextKey{},
		"captured-context",
	))
	response := httptest.NewRecorder()

	server.handleAdminUsersListOrCreate(response, request, 17)

	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if recorder.calls != 1 || recorder.email != " RAW@Example.COM " ||
		recorder.password != " raw password " || recorder.name != " Raw Name " ||
		recorder.contextMark != "captured-context" {
		t.Fatalf("underlying UserCreationStore recorder = %+v", recorder)
	}
}

func TestUserAdminRESTCreationMigrationGETRemainsReadSide(t *testing.T) {
	fixture := newAdminUserRESTFixture(t)
	server := userAdminRESTFixtureServer(t, fixture)
	recorder := &userAdminRESTCreationRecorder{}
	server.userCreations = newUserAdminRESTCreationService(recorder)
	fixture.spy.calls = nil
	fixture.spy.createCalls = 0

	resp, body := doJSON(
		t,
		fixture.ownerClient,
		http.MethodGet,
		fixture.ts.URL+"/api/admin/users",
		nil,
		nil,
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if recorder.calls != 0 || fixture.spy.createCalls != 0 {
		t.Fatalf("GET reached creation: recorder=%+v spy.createCalls=%d", recorder, fixture.spy.createCalls)
	}
	wantStoreTrace := []string{"get-user:" + fmt.Sprint(fixture.owner.ID)}
	if !reflect.DeepEqual(fixture.spy.calls, wantStoreTrace) {
		t.Fatalf("GET adapter store trace=%v, want %v", fixture.spy.calls, wantStoreTrace)
	}
}

func TestUserAdminRESTCreationMigrationPreservesExecutionErrorMapping(t *testing.T) {
	validationErr := fmt.Errorf("%w: invalid email", store.ErrValidation)
	unexpectedErr := errors.New("forced creation failure")
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantMessage string
		wantDetails map[string]any
	}{
		{
			name: "validation", err: validationErr,
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR", wantMessage: validationErr.Error(),
			wantDetails: map[string]any{"reason": "invalid_email"},
		},
		{
			name: "conflict", err: store.ErrConflict,
			wantStatus: http.StatusConflict, wantCode: "CONFLICT", wantMessage: store.ErrConflict.Error(),
		},
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
			name: "unexpected", err: unexpectedErr,
			wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL", wantMessage: "internal error",
			wantDetails: map[string]any{"detail": unexpectedErr.Error()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newAdminUserRESTFixture(t)
			server := userAdminRESTFixtureServer(t, fixture)
			recorder := &userAdminRESTCreationRecorder{err: tt.err}
			server.userCreations = newUserAdminRESTCreationService(recorder)
			fixture.spy.calls = nil
			fixture.spy.createCalls = 0
			fixture.collector.events = nil

			resp, body := doJSON(
				t,
				fixture.ownerClient,
				http.MethodPost,
				fixture.ts.URL+"/api/admin/users",
				map[string]any{"email": "user@example.com", "name": "User", "password": "password123"},
				nil,
			)
			envelope := assertAdminRESTError(t, resp, body, tt.wantStatus, tt.wantCode, tt.wantMessage)
			if !reflect.DeepEqual(envelope.Error.Details, tt.wantDetails) {
				t.Fatalf("error details=%+v, want %+v", envelope.Error.Details, tt.wantDetails)
			}
			if recorder.calls != 1 {
				t.Fatalf("creation calls=%d, want 1", recorder.calls)
			}
			wantStoreTrace := []string{"get-user:" + fmt.Sprint(fixture.owner.ID)}
			if !reflect.DeepEqual(fixture.spy.calls, wantStoreTrace) || fixture.spy.createCalls != 0 {
				t.Fatalf(
					"adapter store trace=%v createCalls=%d, want outer requester read only",
					fixture.spy.calls,
					fixture.spy.createCalls,
				)
			}
			if len(fixture.collector.events) != 0 {
				t.Fatalf("creation error emitted realtime events: %+v", fixture.collector.events)
			}
		})
	}
}

func TestUserAdminRESTCreationMigrationStoreResultIsProjectedWithoutPostRead(t *testing.T) {
	created := store.User{ID: 91, Email: "created@example.com", Name: "Created", SystemRole: store.SystemRoleUser}
	recorder := &userAdminRESTCreationRecorder{user: created}
	server := &Server{
		maxBody:       1 << 20,
		mode:          "full",
		userCreations: newUserAdminRESTCreationService(recorder),
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/users",
		strings.NewReader(`{"email":"input@example.com","name":"Input","password":"password123"}`),
	)
	response := httptest.NewRecorder()

	server.handleAdminUsersListOrCreate(response, request, 17)

	var got userJSON
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != http.StatusCreated || !reflect.DeepEqual(got, userToJSON(created)) || recorder.calls != 1 {
		t.Fatalf("response status=%d user=%+v recorder=%+v", response.Code, got, recorder)
	}
}
