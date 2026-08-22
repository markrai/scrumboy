package mcp_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/httpapi"
	"scrumboy/internal/mcp"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

// adminMCPMutationStore is a test-only transport-boundary spy. Embedding the
// real store keeps persistence and transaction behavior real while allowing
// deterministic sequencing failures without production fault injection.
type adminMCPMutationStore struct {
	*store.Store
	calls                 []string
	updateCalls           int
	deleteCalls           int
	getUserErrByID        map[int64]error
	failTargetAfterUpdate int64
	failAfterUpdateErr    error
	updateErr             error
	deleteErr             error
}

func (s *adminMCPMutationStore) GetUser(ctx context.Context, userID int64) (store.User, error) {
	s.calls = append(s.calls, "get-user:"+strconv.FormatInt(userID, 10))
	if err := s.getUserErrByID[userID]; err != nil {
		return store.User{}, err
	}
	if userID == s.failTargetAfterUpdate && s.updateCalls > 0 && s.failAfterUpdateErr != nil {
		return store.User{}, s.failAfterUpdateErr
	}
	return s.Store.GetUser(ctx, userID)
}

func (s *adminMCPMutationStore) UpdateUserRole(ctx context.Context, requesterID, targetUserID int64, role store.SystemRole) error {
	s.calls = append(s.calls, fmt.Sprintf("update-role:%d:%d:%s", requesterID, targetUserID, role))
	s.updateCalls++
	if s.updateErr != nil {
		return s.updateErr
	}
	return s.Store.UpdateUserRole(ctx, requesterID, targetUserID, role)
}

func (s *adminMCPMutationStore) DeleteUser(ctx context.Context, requesterID, targetUserID int64) error {
	s.calls = append(s.calls, fmt.Sprintf("delete-user:%d:%d", requesterID, targetUserID))
	s.deleteCalls++
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.Store.DeleteUser(ctx, requesterID, targetUserID)
}

type adminMCPFixture struct {
	ts          *httptest.Server
	db          *sql.DB
	store       *store.Store
	spy         *adminMCPMutationStore
	owner       store.User
	admin       store.User
	user        store.User
	project     store.Project
	ownerClient *http.Client
	adminClient *http.Client
	userClient  *http.Client
}

func newAdminMCPFixture(t *testing.T, mode string, bootstrapped bool) *adminMCPFixture {
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
	spy := &adminMCPMutationStore{Store: realStore, getUserErrByID: map[int64]error{}}
	adapter := mcp.New(spy, mcp.Options{Mode: mode})
	srv := httpapi.NewServer(spy, httpapi.Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   mode,
		MCPHandler:     adapter,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		_ = sqlDB.Close()
	})

	fx := &adminMCPFixture{ts: ts, db: sqlDB, store: realStore, spy: spy}
	if !bootstrapped {
		return fx
	}
	ctx := context.Background()
	fx.owner, err = realStore.BootstrapUser(ctx, "owner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("bootstrap owner: %v", err)
	}
	fx.admin, err = realStore.CreateUser(ctx, "admin-mcp-characterization@example.com", "password123", "Admin")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := realStore.UpdateUserRole(ctx, fx.owner.ID, fx.admin.ID, store.SystemRoleAdmin); err != nil {
		t.Fatalf("promote admin: %v", err)
	}
	fx.user, err = realStore.CreateUser(ctx, "user-mcp-characterization@example.com", "password123", "User")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	projectCtx := store.WithUserID(ctx, fx.owner.ID)
	fx.project, err = realStore.CreateProject(projectCtx, "Admin Mutation Event Observer")
	if err != nil {
		t.Fatalf("create event observer project: %v", err)
	}
	fx.ownerClient = newAdminMCPClientForUser(t, ts, realStore, fx.owner.ID)
	fx.adminClient = newAdminMCPClientForUser(t, ts, realStore, fx.admin.ID)
	fx.userClient = newAdminMCPClientForUser(t, ts, realStore, fx.user.ID)
	return fx
}

func newAdminMCPClientForUser(t *testing.T, ts *httptest.Server, st *store.Store, userID int64) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	token, expiresAt, err := st.CreateSession(context.Background(), userID, 24*time.Hour)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	baseURL, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	jar.SetCookies(baseURL, []*http.Cookie{{Name: "scrumboy_session", Value: token, Path: "/", Expires: expiresAt}})
	return &http.Client{Transport: ts.Client().Transport, Jar: jar}
}

func adminMCPData(t *testing.T, transport string, resp *http.Response, out map[string]any) map[string]any {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s status=%d response=%+v", transport, resp.StatusCode, out)
	}
	if transport == "legacy" {
		if got, want := sortedMapKeys(out), []string{"data", "meta", "ok"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("legacy envelope keys=%v want=%v response=%+v", got, want, out)
		}
		if out["ok"] != true {
			t.Fatalf("legacy response not successful: %+v", out)
		}
		if meta := out["meta"].(map[string]any); len(meta) != 0 {
			t.Fatalf("legacy metadata=%+v want empty", meta)
		}
		return out["data"].(map[string]any)
	}
	if got, want := sortedMapKeys(out), []string{"id", "jsonrpc", "result"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON-RPC envelope keys=%v want=%v response=%+v", got, want, out)
	}
	result := out["result"].(map[string]any)
	if got, want := sortedMapKeys(result), []string{"content", "structuredContent"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON-RPC result keys=%v want=%v result=%+v", got, want, result)
	}
	data := result["structuredContent"].(map[string]any)
	content := result["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["type"] != "text" {
		t.Fatalf("JSON-RPC content=%+v", content)
	}
	return data
}

func TestMCPAdminUserMutationTransportProjectionAndRealtimeSilence(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport+"/role", func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			stream := subscribeTodoUpdateMCPEvents(t, fx.ownerClient, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")
			defer stream.close()
			resp, out := callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_updateUserRole", map[string]any{
				"userId": fx.user.ID, "role": "admin",
			})
			data := adminMCPData(t, transport, resp, out)
			if got, want := sortedMapKeys(data), []string{"user"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("data keys=%v want=%v", got, want)
			}
			user := data["user"].(map[string]any)
			if got, want := sortedMapKeys(user), []string{"createdAt", "email", "isBootstrap", "name", "systemRole", "userId"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("user keys=%v want=%v user=%+v", got, want, user)
			}
			if int64(user["userId"].(float64)) != fx.user.ID || user["systemRole"] != "admin" {
				t.Fatalf("updated user=%+v", user)
			}
			if fx.spy.updateCalls != 1 {
				t.Fatalf("update calls=%d want 1; calls=%v", fx.spy.updateCalls, fx.spy.calls)
			}
			wantCalls := []string{
				"get-user:" + strconv.FormatInt(fx.owner.ID, 10),
				fmt.Sprintf("update-role:%d:%d:admin", fx.owner.ID, fx.user.ID),
				"get-user:" + strconv.FormatInt(fx.user.ID, 10),
			}
			if !reflect.DeepEqual(fx.spy.calls, wantCalls) {
				t.Fatalf("MCP role sequence=%v want=%v", fx.spy.calls, wantCalls)
			}
			assertNoTodoUpdateMCPEvents(t, collectTodoUpdateMCPEvents(t, stream))
		})

		t.Run(transport+"/delete", func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			stream := subscribeTodoUpdateMCPEvents(t, fx.ownerClient, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")
			defer stream.close()
			resp, out := callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_deleteUser", map[string]any{"userId": fx.user.ID})
			data := adminMCPData(t, transport, resp, out)
			if got, want := sortedMapKeys(data), []string{"status", "userId"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("data keys=%v want=%v data=%+v", got, want, data)
			}
			if data["status"] != "deleted" || int64(data["userId"].(float64)) != fx.user.ID {
				t.Fatalf("delete result=%+v", data)
			}
			if fx.spy.deleteCalls != 1 {
				t.Fatalf("delete calls=%d want 1; calls=%v", fx.spy.deleteCalls, fx.spy.calls)
			}
			wantCalls := []string{
				"get-user:" + strconv.FormatInt(fx.owner.ID, 10),
				fmt.Sprintf("delete-user:%d:%d", fx.owner.ID, fx.user.ID),
			}
			if !reflect.DeepEqual(fx.spy.calls, wantCalls) {
				t.Fatalf("MCP delete sequence=%v want=%v", fx.spy.calls, wantCalls)
			}
			assertNoTodoUpdateMCPEvents(t, collectTodoUpdateMCPEvents(t, stream))
		})
	}
}

func TestMCPAdminUserMutationOwnerOnOwnerAndSelfDowngrade(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport+"/self-downgrade-with-two-owners", func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			owner2, err := fx.store.CreateUser(context.Background(), "second-owner-self-mcp-"+transport+"@example.com", "password123", "Owner Two")
			if err != nil {
				t.Fatalf("create second owner: %v", err)
			}
			if err := fx.store.UpdateUserRole(context.Background(), fx.owner.ID, owner2.ID, store.SystemRoleOwner); err != nil {
				t.Fatalf("promote second owner: %v", err)
			}
			resp, out := callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_updateUserRole", map[string]any{"userId": fx.owner.ID, "role": "user"})
			data := adminMCPData(t, transport, resp, out)
			if data["user"].(map[string]any)["systemRole"] != "user" {
				t.Fatalf("self downgrade result=%+v", data)
			}
		})

		t.Run(transport+"/owner-deletes-other-owner", func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			owner2, err := fx.store.CreateUser(context.Background(), "second-owner-delete-mcp-"+transport+"@example.com", "password123", "Owner Two")
			if err != nil {
				t.Fatalf("create second owner: %v", err)
			}
			if err := fx.store.UpdateUserRole(context.Background(), fx.owner.ID, owner2.ID, store.SystemRoleOwner); err != nil {
				t.Fatalf("promote second owner: %v", err)
			}
			resp, out := callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_deleteUser", map[string]any{"userId": owner2.ID})
			data := adminMCPData(t, transport, resp, out)
			if data["status"] != "deleted" || int64(data["userId"].(float64)) != owner2.ID {
				t.Fatalf("delete owner result=%+v", data)
			}
		})
	}
}

func TestMCPAdminUserMutationCapabilityAuthenticationAndInputPrecedence(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport+"/anonymous-before-input", func(t *testing.T) {
			fx := newAdminMCPFixture(t, "anonymous", false)
			resp, out := callTodoUpdateMCP(t, newStatelessClient(fx.ts), fx.ts.URL, transport, "admin_updateUserRole", map[string]any{"userId": "bad", "role": 7})
			assertTodoLinkMCPError(t, transport, resp, out, 403, "CAPABILITY_UNAVAILABLE", "admin_updateUserRole is unavailable in anonymous mode")
		})
	}

	t.Run("legacy/bootstrap-before-input", func(t *testing.T) {
		fx := newAdminMCPFixture(t, "full", false)
		resp, out := callTodoUpdateMCP(t, newStatelessClient(fx.ts), fx.ts.URL, "legacy", "admin_deleteUser", map[string]any{"userId": "bad"})
		assertTodoLinkMCPError(t, "legacy", resp, out, 403, "CAPABILITY_UNAVAILABLE", "admin_deleteUser is unavailable before bootstrap")
	})
	t.Run("legacy/authentication-before-input", func(t *testing.T) {
		fx := newAdminMCPFixture(t, "full", true)
		resp, out := callTodoUpdateMCP(t, newStatelessClient(fx.ts), fx.ts.URL, "legacy", "admin_updateUserRole", map[string]any{"userId": "bad", "role": 7})
		assertTodoLinkMCPError(t, "legacy", resp, out, 401, "AUTH_REQUIRED", "Sign-in required for this tool")
	})

	for _, bootstrapped := range []bool{false, true} {
		name := "prebootstrap"
		if bootstrapped {
			name = "bootstrapped"
		}
		t.Run("jsonrpc/full-transport-auth-precedes-tool-"+name, func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", false)
			if bootstrapped {
				fx = newAdminMCPFixture(t, "full", true)
			}
			req, err := http.NewRequest(http.MethodPost, fx.ts.URL+"/mcp/rpc", bytes.NewBufferString(`{"jsonrpc":"2.0","id":25,"method":"tools/call","params":{"name":"admin_updateUserRole","arguments":{"userId":"bad","role":7}}}`))
			if err != nil {
				t.Fatalf("new JSON-RPC request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("MCP-Protocol-Version", "2025-11-25")
			resp, err := newStatelessClient(fx.ts).Do(req)
			if err != nil {
				t.Fatalf("do JSON-RPC request: %v", err)
			}
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				t.Fatalf("read JSON-RPC response: %v", readErr)
			}
			if resp.StatusCode != http.StatusUnauthorized || len(body) != 0 {
				t.Fatalf("status=%d body=%q want raw 401 with empty body", resp.StatusCode, body)
			}
		})
	}
}

func TestMCPAdminUserMutationAuthorityTargetAndOwnerInvariantPrecedence(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			missingID := int64(999999)
			cases := []struct {
				name, tool, code, message string
				client                    *http.Client
				args                      map[string]any
				status                    int
			}{
				{"invalid id before owner check", "admin_updateUserRole", "VALIDATION_ERROR", "invalid userId", fx.adminClient, map[string]any{"userId": 0, "role": "admin"}, 400},
				{"malformed role before owner check and target", "admin_updateUserRole", "VALIDATION_ERROR", "role must be 'admin' or 'user'", fx.adminClient, map[string]any{"userId": missingID, "role": "ADMIN"}, 400},
				{"admin owner check before missing target", "admin_updateUserRole", "FORBIDDEN", "forbidden", fx.adminClient, map[string]any{"userId": missingID, "role": "admin"}, 403},
				{"regular owner check before target", "admin_updateUserRole", "FORBIDDEN", "forbidden", fx.userClient, map[string]any{"userId": missingID, "role": "admin"}, 403},
				{"owner reaches missing target", "admin_updateUserRole", "NOT_FOUND", "not found", fx.ownerClient, map[string]any{"userId": missingID, "role": "admin"}, 404},
				{"owner spelling is rejected", "admin_updateUserRole", "VALIDATION_ERROR", "role must be 'admin' or 'user'", fx.ownerClient, map[string]any{"userId": fx.user.ID, "role": "owner"}, 400},
				{"whitespace is not normalized", "admin_updateUserRole", "VALIDATION_ERROR", "role must be 'admin' or 'user'", fx.ownerClient, map[string]any{"userId": fx.user.ID, "role": " admin "}, 400},
				{"last owner self downgrade reaches store", "admin_updateUserRole", "VALIDATION_ERROR", "validation: cannot demote the last owner", fx.ownerClient, map[string]any{"userId": fx.owner.ID, "role": "admin"}, 400},
				{"admin self delete is blocked before store self check", "admin_deleteUser", "FORBIDDEN", "forbidden", fx.adminClient, map[string]any{"userId": fx.admin.ID}, 403},
				{"owner self delete reaches store self check", "admin_deleteUser", "VALIDATION_ERROR", "validation: cannot delete yourself", fx.ownerClient, map[string]any{"userId": fx.owner.ID}, 400},
				{"admin delete owner check before missing target", "admin_deleteUser", "FORBIDDEN", "forbidden", fx.adminClient, map[string]any{"userId": missingID}, 403},
				{"owner delete reaches missing target", "admin_deleteUser", "NOT_FOUND", "not found", fx.ownerClient, map[string]any{"userId": missingID}, 404},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					resp, out := callTodoUpdateMCP(t, tc.client, fx.ts.URL, transport, tc.tool, tc.args)
					assertTodoLinkMCPError(t, transport, resp, out, tc.status, tc.code, tc.message)
				})
			}
			if fx.spy.updateCalls != 2 || fx.spy.deleteCalls != 2 {
				t.Fatalf("mutation calls update=%d delete=%d want 2/2; calls=%v", fx.spy.updateCalls, fx.spy.deleteCalls, fx.spy.calls)
			}
		})
	}
}

func TestMCPAdminUserMutationInputShapeDifferences(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			cases := []struct {
				name, tool, message, field string
				args                       map[string]any
			}{
				{"negative id", "admin_updateUserRole", "invalid userId", "userId", map[string]any{"userId": -1, "role": "admin"}},
				{"extra role input", "admin_updateUserRole", "invalid input", "", map[string]any{"userId": fx.user.ID, "role": "admin", "extra": true}},
				{"extra delete input", "admin_deleteUser", "invalid input", "", map[string]any{"userId": fx.user.ID, "extra": true}},
			}
			missingMessage := "role must be 'admin' or 'user'"
			if transport == "jsonrpc" {
				missingMessage = "missing required field: role"
			}
			cases = append(cases, struct {
				name, tool, message, field string
				args                       map[string]any
			}{"missing role", "admin_updateUserRole", missingMessage, "role", map[string]any{"userId": fx.user.ID}})

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					resp, out := callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, tc.tool, tc.args)
					publicErr := assertTodoLinkMCPError(t, transport, resp, out, 400, "VALIDATION_ERROR", tc.message)
					if tc.field != "" {
						details := publicErr["details"].(map[string]any)
						if details["field"] != tc.field {
							t.Fatalf("details=%+v want field=%q", details, tc.field)
						}
					}
				})
			}
			if fx.spy.updateCalls != 0 || fx.spy.deleteCalls != 0 {
				t.Fatalf("invalid input reached mutation: calls=%v", fx.spy.calls)
			}
		})
	}
}

func TestMCPAdminRoleMutationPreReadAndMutationErrorMeaningDiffer(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			fx.spy.getUserErrByID[fx.owner.ID] = store.ErrUnauthorized
			resp, out := callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_updateUserRole", map[string]any{"userId": fx.user.ID, "role": "admin"})
			assertTodoLinkMCPError(t, transport, resp, out, 401, "AUTH_REQUIRED", "Sign-in required for this tool")
			if fx.spy.updateCalls != 0 {
				t.Fatalf("pre-read failure reached mutation: calls=%v", fx.spy.calls)
			}

			delete(fx.spy.getUserErrByID, fx.owner.ID)
			fx.spy.updateErr = store.ErrUnauthorized
			resp, out = callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_updateUserRole", map[string]any{"userId": fx.user.ID, "role": "admin"})
			assertTodoLinkMCPError(t, transport, resp, out, 403, "FORBIDDEN", "forbidden")
			if fx.spy.updateCalls != 1 {
				t.Fatalf("mutation calls=%d want 1", fx.spy.updateCalls)
			}
		})
	}
}

func TestMCPAdminRoleMutationStoreErrorProjection(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			cases := []struct {
				name, code, message string
				err                 error
				status              int
			}{
				{"unauthorized", "FORBIDDEN", "forbidden", store.ErrUnauthorized, 403},
				{"forbidden", "FORBIDDEN", "forbidden", store.ErrForbidden, 403},
				{"not found", "NOT_FOUND", "not found", store.ErrNotFound, 404},
				{"conflict", "CONFLICT", store.ErrConflict.Error(), store.ErrConflict, 409},
				{"validation", "VALIDATION_ERROR", store.ErrValidation.Error(), store.ErrValidation, 400},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					fx.spy.updateErr = tc.err
					resp, out := callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_updateUserRole", map[string]any{"userId": fx.user.ID, "role": "admin"})
					assertTodoLinkMCPError(t, transport, resp, out, tc.status, tc.code, tc.message)
				})
			}
		})
	}
}

func TestMCPAdminRoleCommittedMutationSurvivesPostReadFailure(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			forced := errors.New("forced MCP role post-read failure")
			fx.spy.failTargetAfterUpdate = fx.user.ID
			fx.spy.failAfterUpdateErr = forced
			resp, out := callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_updateUserRole", map[string]any{"userId": fx.user.ID, "role": "admin"})
			publicErr := assertTodoLinkMCPError(t, transport, resp, out, 500, "INTERNAL", "internal error")
			assertEmptyTodoLinkMCPDetails(t, publicErr)
			updated, err := fx.store.GetUser(context.Background(), fx.user.ID)
			if err != nil || updated.SystemRole != store.SystemRoleAdmin {
				t.Fatalf("committed mutation missing: user=%+v err=%v", updated, err)
			}
			if fx.spy.updateCalls != 1 {
				t.Fatalf("update calls=%d want 1; calls=%v", fx.spy.updateCalls, fx.spy.calls)
			}
		})
	}
}

func TestMCPAdminDeleteOwnedProjectFailureProjectionAndRollback(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newAdminMCPFixture(t, "full", true)
			owner2, err := fx.store.CreateUser(context.Background(), "owned-project-delete-mcp-"+transport+"@example.com", "password123", "Project Owner")
			if err != nil {
				t.Fatalf("create second owner: %v", err)
			}
			if err := fx.store.UpdateUserRole(context.Background(), fx.owner.ID, owner2.ID, store.SystemRoleOwner); err != nil {
				t.Fatalf("promote second owner: %v", err)
			}
			project, err := fx.store.CreateProject(store.WithUserID(context.Background(), owner2.ID), "MCP Owned Project Delete Failure "+transport)
			if err != nil {
				t.Fatalf("create owned project: %v", err)
			}
			resp, out := callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, "admin_deleteUser", map[string]any{"userId": owner2.ID})
			publicErr := assertTodoLinkMCPError(t, transport, resp, out, 500, "INTERNAL", "internal error")
			assertEmptyTodoLinkMCPDetails(t, publicErr)
			if _, err := fx.store.GetUser(context.Background(), owner2.ID); err != nil {
				t.Fatalf("user missing after failed delete: %v", err)
			}
			if _, err := fx.store.GetProject(context.Background(), project.ID); err != nil {
				t.Fatalf("project missing after failed delete: %v", err)
			}
			if fx.spy.deleteCalls != 1 {
				t.Fatalf("delete calls=%d want 1", fx.spy.deleteCalls)
			}
		})
	}
}

func TestMCPAdminDottedNamesRemainUnavailable(t *testing.T) {
	fx := newAdminMCPFixture(t, "full", true)
	aliases := []struct {
		name string
		args map[string]any
	}{
		{"admin.updateUserRole", map[string]any{"userId": fx.user.ID, "role": "admin"}},
		{"admin.deleteUser", map[string]any{"userId": fx.user.ID}},
	}
	for _, transport := range []string{"legacy", "jsonrpc"} {
		for _, alias := range aliases {
			t.Run(transport+"/"+alias.name, func(t *testing.T) {
				resp, out := callTodoUpdateMCP(t, fx.ownerClient, fx.ts.URL, transport, alias.name, alias.args)
				assertTodoLinkMCPError(t, transport, resp, out, 404, "NOT_FOUND", "tool not found")
			})
		}
	}
	if fx.spy.updateCalls != 0 || fx.spy.deleteCalls != 0 {
		t.Fatalf("unavailable aliases reached mutations: calls=%v", fx.spy.calls)
	}
}
