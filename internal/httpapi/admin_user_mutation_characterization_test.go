package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"scrumboy/internal/db"
	"scrumboy/internal/eventbus"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

// adminUserMutationStore is a test-only adapter-boundary spy. It deliberately
// embeds the real store so characterization exercises current persistence
// behavior while allowing deterministic pre/post-read failures.
type adminUserMutationStore struct {
	*store.Store
	calls                 []string
	updateCalls           int
	deleteCalls           int
	createCalls           int
	getUserErrByID        map[int64]error
	failTargetAfterUpdate int64
	failTargetAfterUpdErr error
	updateErr             error
	deleteErr             error
	createErr             error
}

func (s *adminUserMutationStore) GetUser(ctx context.Context, userID int64) (store.User, error) {
	s.calls = append(s.calls, "get-user:"+strconv.FormatInt(userID, 10))
	if err := s.getUserErrByID[userID]; err != nil {
		return store.User{}, err
	}
	if userID == s.failTargetAfterUpdate && s.updateCalls > 0 && s.failTargetAfterUpdErr != nil {
		return store.User{}, s.failTargetAfterUpdErr
	}
	return s.Store.GetUser(ctx, userID)
}

func (s *adminUserMutationStore) UpdateUserRole(ctx context.Context, requesterID, targetUserID int64, role store.SystemRole) error {
	s.calls = append(s.calls, fmt.Sprintf("update-role:%d:%d:%s", requesterID, targetUserID, role))
	s.updateCalls++
	if s.updateErr != nil {
		return s.updateErr
	}
	return s.Store.UpdateUserRole(ctx, requesterID, targetUserID, role)
}

func (s *adminUserMutationStore) DeleteUser(ctx context.Context, requesterID, targetUserID int64) error {
	s.calls = append(s.calls, fmt.Sprintf("delete-user:%d:%d", requesterID, targetUserID))
	s.deleteCalls++
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.Store.DeleteUser(ctx, requesterID, targetUserID)
}

func (s *adminUserMutationStore) CreateUser(ctx context.Context, email, password, name string) (store.User, error) {
	s.calls = append(s.calls, "create-user:"+email)
	s.createCalls++
	if s.createErr != nil {
		return store.User{}, s.createErr
	}
	return s.Store.CreateUser(ctx, email, password, name)
}

type adminUserRESTFixture struct {
	ts          *httptest.Server
	db          *sql.DB
	store       *store.Store
	spy         *adminUserMutationStore
	collector   *capturingEventConsumer
	owner       store.User
	admin       store.User
	user        store.User
	ownerClient *http.Client
	adminClient *http.Client
	userClient  *http.Client
}

func newAdminUserRESTFixture(t *testing.T) *adminUserRESTFixture {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}
	realStore := store.New(sqlDB, nil)
	ctx := context.Background()
	owner, err := realStore.BootstrapUser(ctx, "owner-admin-characterization@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("bootstrap owner: %v", err)
	}
	admin, err := realStore.CreateUser(ctx, "admin-admin-characterization@example.com", "password123", "Admin")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := realStore.UpdateUserRole(ctx, owner.ID, admin.ID, store.SystemRoleAdmin); err != nil {
		t.Fatalf("promote admin: %v", err)
	}
	user, err := realStore.CreateUser(ctx, "user-admin-characterization@example.com", "password123", "User")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	spy := &adminUserMutationStore{Store: realStore, getUserErrByID: map[int64]error{}}
	srv := NewServer(spy, Options{MaxRequestBody: 1 << 20, ScrumboyMode: "full"})
	collector := &capturingEventConsumer{}
	srv.fanout = eventbus.NewFanout(collector)
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		_ = sqlDB.Close()
	})

	login := func(email string) *http.Client {
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("cookie jar: %v", err)
		}
		client := &http.Client{Transport: ts.Client().Transport, Jar: jar}
		loginUserClient(t, client, ts.URL, email, "password123")
		return client
	}

	return &adminUserRESTFixture{
		ts: ts, db: sqlDB, store: realStore, spy: spy, collector: collector,
		owner: owner, admin: admin, user: user,
		ownerClient: login(owner.Email), adminClient: login(admin.Email), userClient: login(user.Email),
	}
}

func assertAdminRESTError(t *testing.T, resp *http.Response, body []byte, status int, code, message string) apiErrorEnvelope {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("status=%d want=%d body=%s", resp.StatusCode, status, body)
	}
	var got apiErrorEnvelope
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}
	if got.Error.Code != code || got.Error.Message != message {
		t.Fatalf("error=%+v want code=%q message=%q", got.Error, code, message)
	}
	return got
}

func doAdminJSONWithoutCSRF(t *testing.T, client *http.Client, method, url string, body any) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	var bodyBytes bytes.Buffer
	if _, err := bodyBytes.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp, bodyBytes.Bytes()
}

func TestAdminUserMutationsRESTAuthorizationValidationAndTargetPrecedence(t *testing.T) {
	fx := newAdminUserRESTFixture(t)
	missingID := int64(999999)

	tests := []struct {
		name        string
		client      *http.Client
		method      string
		path        string
		body        any
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{"unauthenticated precedes malformed id", fx.ts.Client(), http.MethodPatch, "/api/admin/users/not-an-id/role", map[string]any{"role": "owner"}, 401, "UNAUTHORIZED", "unauthorized"},
		{"regular role precedes malformed id", fx.userClient, http.MethodPatch, "/api/admin/users/not-an-id/role", map[string]any{"role": "owner"}, 403, "FORBIDDEN", "admin or owner role required"},
		{"admin reaches syntactic id validation", fx.adminClient, http.MethodPatch, "/api/admin/users/not-an-id/role", map[string]any{"role": "admin"}, 400, "BAD_REQUEST", "invalid user id"},
		{"zero id is invalid", fx.ownerClient, http.MethodPatch, "/api/admin/users/0/role", map[string]any{"role": "admin"}, 400, "BAD_REQUEST", "invalid user id"},
		{"negative id is invalid", fx.ownerClient, http.MethodDelete, "/api/admin/users/-1", nil, 400, "BAD_REQUEST", "invalid user id"},
		{"missing role is not normalized", fx.ownerClient, http.MethodPatch, fmt.Sprintf("/api/admin/users/%d/role", missingID), map[string]any{}, 400, "BAD_REQUEST", "role must be 'admin' or 'user'"},
		{"unknown role field fails strict json", fx.ownerClient, http.MethodPatch, fmt.Sprintf("/api/admin/users/%d/role", missingID), map[string]any{"role": "admin", "extra": true}, 400, "VALIDATION_ERROR", "invalid json"},
		{"owner malformed role precedes missing target", fx.ownerClient, http.MethodPatch, fmt.Sprintf("/api/admin/users/%d/role", missingID), map[string]any{"role": " owner "}, 400, "BAD_REQUEST", "role must be 'admin' or 'user'"},
		{"admin malformed role precedes owner authorization", fx.adminClient, http.MethodPatch, fmt.Sprintf("/api/admin/users/%d/role", missingID), map[string]any{"role": "ADMIN"}, 400, "BAD_REQUEST", "role must be 'admin' or 'user'"},
		{"admin owner authorization precedes missing target", fx.adminClient, http.MethodPatch, fmt.Sprintf("/api/admin/users/%d/role", missingID), map[string]any{"role": "admin"}, 401, "UNAUTHORIZED", "unauthorized"},
		{"owner reaches missing target", fx.ownerClient, http.MethodPatch, fmt.Sprintf("/api/admin/users/%d/role", missingID), map[string]any{"role": "admin"}, 404, "NOT_FOUND", "not found"},
		{"admin delete authorization precedes missing target", fx.adminClient, http.MethodDelete, fmt.Sprintf("/api/admin/users/%d", missingID), nil, 401, "UNAUTHORIZED", "unauthorized"},
		{"owner delete reaches missing target", fx.ownerClient, http.MethodDelete, fmt.Sprintf("/api/admin/users/%d", missingID), nil, 404, "NOT_FOUND", "not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := doJSON(t, tt.client, tt.method, fx.ts.URL+tt.path, tt.body, nil)
			assertAdminRESTError(t, resp, body, tt.wantStatus, tt.wantCode, tt.wantMessage)
		})
	}

	t.Run("csrf precedes authentication and route validation", func(t *testing.T) {
		resp, body := doAdminJSONWithoutCSRF(t, fx.ts.Client(), http.MethodPatch, fx.ts.URL+"/api/admin/users/not-an-id/role", map[string]any{"role": "owner"})
		assertAdminRESTError(t, resp, body, 403, "FORBIDDEN", "missing X-Scrumboy header")
	})

	t.Run("outer actor pre-read failure precedes malformed id", func(t *testing.T) {
		forced := errors.New("forced requester pre-read failure")
		fx.spy.getUserErrByID[fx.owner.ID] = forced
		defer delete(fx.spy.getUserErrByID, fx.owner.ID)
		resp, body := doJSON(t, fx.ownerClient, http.MethodPatch, fx.ts.URL+"/api/admin/users/not-an-id/role", map[string]any{"role": "owner"}, nil)
		got := assertAdminRESTError(t, resp, body, 500, "INTERNAL", "internal error")
		if got.Error.Details["detail"] != forced.Error() {
			t.Fatalf("details=%+v want detail=%q", got.Error.Details, forced)
		}
	})

	if fx.spy.updateCalls != 2 || fx.spy.deleteCalls != 2 {
		t.Fatalf("mutation call counts update=%d delete=%d want 2/2; calls=%v", fx.spy.updateCalls, fx.spy.deleteCalls, fx.spy.calls)
	}
	if len(fx.collector.events) != 0 {
		t.Fatalf("admin failures emitted realtime events: %+v", fx.collector.events)
	}
}

func TestAdminUserMutationsRESTOwnerInvariantMatrix(t *testing.T) {
	t.Run("owner acts on non-owner", func(t *testing.T) {
		fx := newAdminUserRESTFixture(t)
		var updated map[string]any
		resp, body := doJSON(t, fx.ownerClient, http.MethodPatch, fmt.Sprintf("%s/api/admin/users/%d/role", fx.ts.URL, fx.user.ID), map[string]any{"role": "admin"}, &updated)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("update status=%d body=%s", resp.StatusCode, body)
		}
		if got, want := updated["systemRole"], "admin"; got != want {
			t.Fatalf("updated user=%+v want systemRole=%q", updated, want)
		}
		resp, body = doJSON(t, fx.ownerClient, http.MethodDelete, fmt.Sprintf("%s/api/admin/users/%d", fx.ts.URL, fx.admin.ID), nil, nil)
		if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
			t.Fatalf("delete status=%d body=%q want 204 empty", resp.StatusCode, body)
		}
		if fx.spy.updateCalls != 1 || fx.spy.deleteCalls != 1 {
			t.Fatalf("mutation calls update=%d delete=%d", fx.spy.updateCalls, fx.spy.deleteCalls)
		}
		wantCalls := []string{
			"get-user:" + strconv.FormatInt(fx.owner.ID, 10),
			fmt.Sprintf("update-role:%d:%d:admin", fx.owner.ID, fx.user.ID),
			"get-user:" + strconv.FormatInt(fx.user.ID, 10),
			"get-user:" + strconv.FormatInt(fx.owner.ID, 10),
			fmt.Sprintf("delete-user:%d:%d", fx.owner.ID, fx.admin.ID),
		}
		if !reflect.DeepEqual(fx.spy.calls, wantCalls) {
			t.Fatalf("adapter/store sequence=%v want=%v", fx.spy.calls, wantCalls)
		}
		if len(fx.collector.events) != 0 {
			t.Fatalf("successful admin mutations emitted events: %+v", fx.collector.events)
		}
	})

	t.Run("self and last-owner precedence", func(t *testing.T) {
		fx := newAdminUserRESTFixture(t)
		resp, body := doJSON(t, fx.ownerClient, http.MethodPatch, fmt.Sprintf("%s/api/admin/users/%d/role", fx.ts.URL, fx.owner.ID), map[string]any{"role": "admin"}, nil)
		assertAdminRESTError(t, resp, body, 400, "VALIDATION_ERROR", "validation: cannot demote the last owner")

		resp, body = doJSON(t, fx.ownerClient, http.MethodDelete, fmt.Sprintf("%s/api/admin/users/%d", fx.ts.URL, fx.owner.ID), nil, nil)
		assertAdminRESTError(t, resp, body, 400, "VALIDATION_ERROR", "validation: cannot delete yourself")

		resp, body = doJSON(t, fx.adminClient, http.MethodDelete, fmt.Sprintf("%s/api/admin/users/%d", fx.ts.URL, fx.admin.ID), nil, nil)
		assertAdminRESTError(t, resp, body, 400, "VALIDATION_ERROR", "validation: cannot delete yourself")
	})

	t.Run("owner acts on owner and self downgrade is allowed with two owners", func(t *testing.T) {
		fx := newAdminUserRESTFixture(t)
		owner2, err := fx.store.CreateUser(context.Background(), "second-owner-rest@example.com", "password123", "Owner Two")
		if err != nil {
			t.Fatalf("create second owner: %v", err)
		}
		if err := fx.store.UpdateUserRole(context.Background(), fx.owner.ID, owner2.ID, store.SystemRoleOwner); err != nil {
			t.Fatalf("promote second owner: %v", err)
		}
		resp, body := doJSON(t, fx.ownerClient, http.MethodPatch, fmt.Sprintf("%s/api/admin/users/%d/role", fx.ts.URL, fx.owner.ID), map[string]any{"role": "user"}, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("self downgrade status=%d body=%s", resp.StatusCode, body)
		}
		got, err := fx.store.GetUser(context.Background(), fx.owner.ID)
		if err != nil || got.SystemRole != store.SystemRoleUser {
			t.Fatalf("self downgrade result=%+v err=%v", got, err)
		}
	})

	t.Run("one owner may delete another when two owners exist", func(t *testing.T) {
		fx := newAdminUserRESTFixture(t)
		owner2, err := fx.store.CreateUser(context.Background(), "deletable-owner-rest@example.com", "password123", "Owner Two")
		if err != nil {
			t.Fatalf("create second owner: %v", err)
		}
		if err := fx.store.UpdateUserRole(context.Background(), fx.owner.ID, owner2.ID, store.SystemRoleOwner); err != nil {
			t.Fatalf("promote second owner: %v", err)
		}
		resp, body := doJSON(t, fx.ownerClient, http.MethodDelete, fmt.Sprintf("%s/api/admin/users/%d", fx.ts.URL, owner2.ID), nil, nil)
		if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
			t.Fatalf("delete owner status=%d body=%q", resp.StatusCode, body)
		}
	})
}

func TestAdminUserMutationRESTRoleCommittedMutationSurvivesPostReadFailure(t *testing.T) {
	fx := newAdminUserRESTFixture(t)
	forced := errors.New("forced role post-read failure")
	fx.spy.failTargetAfterUpdate = fx.user.ID
	fx.spy.failTargetAfterUpdErr = forced

	resp, body := doJSON(t, fx.ownerClient, http.MethodPatch, fmt.Sprintf("%s/api/admin/users/%d/role", fx.ts.URL, fx.user.ID), map[string]any{"role": "admin"}, nil)
	got := assertAdminRESTError(t, resp, body, 500, "INTERNAL", "internal error")
	if got.Error.Details["detail"] != forced.Error() {
		t.Fatalf("details=%+v want detail=%q", got.Error.Details, forced)
	}
	updated, err := fx.store.GetUser(context.Background(), fx.user.ID)
	if err != nil || updated.SystemRole != store.SystemRoleAdmin {
		t.Fatalf("committed mutation was not retained: user=%+v err=%v", updated, err)
	}
	if fx.spy.updateCalls != 1 {
		t.Fatalf("update calls=%d want exactly 1; calls=%v", fx.spy.updateCalls, fx.spy.calls)
	}
	if len(fx.collector.events) != 0 {
		t.Fatalf("post-read failure emitted events: %+v", fx.collector.events)
	}
}

func TestAdminUserMutationRESTDeleteOwnedProjectFailureProjectionAndRollback(t *testing.T) {
	fx := newAdminUserRESTFixture(t)
	owner2, err := fx.store.CreateUser(context.Background(), "owned-project-delete-rest@example.com", "password123", "Project Owner")
	if err != nil {
		t.Fatalf("create second owner: %v", err)
	}
	if err := fx.store.UpdateUserRole(context.Background(), fx.owner.ID, owner2.ID, store.SystemRoleOwner); err != nil {
		t.Fatalf("promote second owner: %v", err)
	}
	project, err := fx.store.CreateProject(store.WithUserID(context.Background(), owner2.ID), "REST Owned Project Delete Failure")
	if err != nil {
		t.Fatalf("create owned project: %v", err)
	}

	resp, body := doJSON(t, fx.ownerClient, http.MethodDelete, fmt.Sprintf("%s/api/admin/users/%d", fx.ts.URL, owner2.ID), nil, nil)
	got := assertAdminRESTError(t, resp, body, 500, "INTERNAL", "internal error")
	detail, _ := got.Error.Details["detail"].(string)
	if !strings.Contains(detail, "FOREIGN KEY constraint failed") {
		t.Fatalf("details=%+v want foreign-key failure", got.Error.Details)
	}
	if _, err := fx.store.GetUser(context.Background(), owner2.ID); err != nil {
		t.Fatalf("user missing after failed delete: %v", err)
	}
	if _, err := fx.store.GetProject(context.Background(), project.ID); err != nil {
		t.Fatalf("project missing after failed delete: %v", err)
	}
	if fx.spy.deleteCalls != 1 || len(fx.collector.events) != 0 {
		t.Fatalf("delete calls=%d events=%+v", fx.spy.deleteCalls, fx.collector.events)
	}
}

func TestAdminUserMutationRESTRoleStoreErrorProjection(t *testing.T) {
	fx := newAdminUserRESTFixture(t)
	tests := []struct {
		name, code, message string
		err                 error
		status              int
	}{
		{"unauthorized", "UNAUTHORIZED", "unauthorized", store.ErrUnauthorized, 401},
		{"forbidden", "FORBIDDEN", "forbidden", store.ErrForbidden, 403},
		{"not found", "NOT_FOUND", "not found", store.ErrNotFound, 404},
		{"conflict", "CONFLICT", store.ErrConflict.Error(), store.ErrConflict, 409},
		{"validation", "VALIDATION_ERROR", store.ErrValidation.Error(), store.ErrValidation, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx.spy.updateErr = tt.err
			resp, body := doJSON(t, fx.ownerClient, http.MethodPatch, fmt.Sprintf("%s/api/admin/users/%d/role", fx.ts.URL, fx.user.ID), map[string]any{"role": "admin"}, nil)
			assertAdminRESTError(t, resp, body, tt.status, tt.code, tt.message)
		})
	}
	if fx.spy.updateCalls != len(tests) {
		t.Fatalf("update calls=%d want=%d", fx.spy.updateCalls, len(tests))
	}
	if len(fx.collector.events) != 0 {
		t.Fatalf("failed mutations emitted events: %+v", fx.collector.events)
	}
}

func TestAdminUserMutationRESTCreationNormalizationAuthorityAndProjection(t *testing.T) {
	fx := newAdminUserRESTFixture(t)
	var created map[string]any
	resp, body := doJSON(t, fx.adminClient, http.MethodPost, fx.ts.URL+"/api/admin/users", map[string]any{
		"email": "  Mixed.Case@Example.COM  ", "name": "  Created Name  ", "password": "  password123  ",
	}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", resp.StatusCode, body)
	}
	if got, want := created["email"], "mixed.case@example.com"; got != want {
		t.Fatalf("created email=%v want=%v", got, want)
	}
	if got, want := created["name"], "Created Name"; got != want {
		t.Fatalf("created name=%v want=%v", got, want)
	}
	wantKeys := []string{"createdAt", "email", "hasLocalPassword", "id", "image", "isBootstrap", "name", "oidcLinked", "systemRole", "twoFactorEnabled"}
	gotKeys := make([]string, 0, len(created))
	for key := range created {
		gotKeys = append(gotKeys, key)
	}
	// Reuse the package's stable helper rather than depending on map iteration.
	if got := sortedStrings(gotKeys); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("created user keys=%v want=%v", got, wantKeys)
	}
	if created["systemRole"] != "user" || created["isBootstrap"] != false || created["hasLocalPassword"] != true {
		t.Fatalf("created projection=%+v", created)
	}

	resp, body = doJSON(t, fx.adminClient, http.MethodPost, fx.ts.URL+"/api/admin/users", map[string]any{
		"email": "mixed.case@example.com", "name": "Duplicate", "password": "password123",
	}, nil)
	assertAdminRESTError(t, resp, body, 409, "CONFLICT", store.ErrConflict.Error())

	resp, body = doJSON(t, fx.userClient, http.MethodPost, fx.ts.URL+"/api/admin/users", map[string]any{"unexpected": true}, nil)
	assertAdminRESTError(t, resp, body, 403, "FORBIDDEN", "admin or owner role required")

	if fx.spy.createCalls != 2 {
		t.Fatalf("create calls=%d want 2; calls=%v", fx.spy.createCalls, fx.spy.calls)
	}
	if len(fx.collector.events) != 0 {
		t.Fatalf("user creation emitted realtime events: %+v", fx.collector.events)
	}
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
