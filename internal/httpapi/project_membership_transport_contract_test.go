package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"scrumboy/internal/db"
	"scrumboy/internal/eventbus"
	"scrumboy/internal/mcp"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

type membershipEventCollector struct {
	mu     sync.Mutex
	events []eventbus.Event
}

func (c *membershipEventCollector) OnEvent(_ context.Context, event eventbus.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
}

func (c *membershipEventCollector) snapshot() []eventbus.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]eventbus.Event(nil), c.events...)
}

type membershipRESTFixture struct {
	ts        *httptest.Server
	db        *sql.DB
	st        *store.Store
	client    *http.Client
	ctx       context.Context
	ownerID   int64
	project   store.Project
	collector *membershipEventCollector
}

func newMembershipRESTFixture(t *testing.T, name string) *membershipRESTFixture {
	t.Helper()
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	t.Cleanup(cleanup)
	client := newCookieClient(t)
	owner := bootstrapUserClient(t, client, ts.URL, "Membership Owner", name+"-owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	ctx := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, nil)
	project, err := st.CreateProject(ctx, name)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	srv, ok := ts.Config.Handler.(*Server)
	if !ok {
		t.Fatalf("test handler type=%T, want *Server", ts.Config.Handler)
	}
	collector := &membershipEventCollector{}
	// Keep the production fanout and SSE translation in the route contract while
	// adding an in-process observer for the structured membership event.
	srv.fanout = eventbus.NewFanout(newSSEBridge(srv.hub), collector)

	return &membershipRESTFixture{
		ts: ts, db: sqlDB, st: st, client: client, ctx: ctx,
		ownerID: ownerID, project: project, collector: collector,
	}
}

func (fx *membershipRESTFixture) membersURL() string {
	return fmt.Sprintf("%s/api/projects/%d/members", fx.ts.URL, fx.project.ID)
}

func createMembershipUser(t *testing.T, st *store.Store, key string) store.User {
	t.Helper()
	user, err := st.CreateUser(context.Background(), key+"@example.com", "password123", key)
	if err != nil {
		t.Fatalf("create user %q: %v", key, err)
	}
	return user
}

func membershipAuditCount(t *testing.T, sqlDB *sql.DB, projectID, targetUserID int64, action string) int {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(`
		SELECT COUNT(*)
		FROM audit_events
		WHERE project_id = ? AND target_type = 'member' AND target_id = ? AND action = ?
	`, projectID, targetUserID, action).Scan(&count); err != nil {
		t.Fatalf("count %s audits: %v", action, err)
	}
	return count
}

func membershipLedgerCount(t *testing.T, sqlDB *sql.DB, todoID int64) int {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(`
		SELECT COUNT(*)
		FROM todo_assignee_events
		WHERE todo_id = ? AND reason = 'member_removed'
	`, todoID).Scan(&count); err != nil {
		t.Fatalf("count member_removed assignment events: %v", err)
	}
	return count
}

func findMembershipRESTMember(members []projectMemberJSON, userID int64) (projectMemberJSON, bool) {
	for _, member := range members {
		if member.UserID == userID {
			return member, true
		}
	}
	return projectMemberJSON{}, false
}

func assertMembershipRESTPublications(
	t *testing.T,
	fx *membershipRESTFixture,
	stream *todoUpdateEventStream,
	targetUserID int64,
	action string,
) {
	t.Helper()
	events := fx.collector.snapshot()
	if len(events) != 2 {
		t.Fatalf("fanout events=%+v, want exactly board.members_updated then project.membership", events)
	}
	if events[0].Type != "board.members_updated" || events[0].ProjectID != fx.project.ID {
		t.Fatalf("first fanout event=%+v, want board.members_updated for project %d", events[0], fx.project.ID)
	}
	if events[1].Type != "project.membership" || events[1].ProjectID != fx.project.ID {
		t.Fatalf("second fanout event=%+v, want project.membership for project %d", events[1], fx.project.ID)
	}

	var payload map[string]any
	if err := json.Unmarshal(events[1].Payload, &payload); err != nil {
		t.Fatalf("decode membership payload: %v", err)
	}
	assertExactJSONKeys(t, payload, "projectId", "affectedUserId", "action", "actorUserId")
	if int64(payload["projectId"].(float64)) != fx.project.ID ||
		int64(payload["affectedUserId"].(float64)) != targetUserID ||
		payload["action"] != action ||
		int64(payload["actorUserId"].(float64)) != fx.ownerID {
		t.Fatalf("membership payload=%+v", payload)
	}

	sseEvents := collectTodoUpdateEvents(t, stream)
	if len(sseEvents) != 1 || sseEvents[0].Type != "members_updated" || sseEvents[0].ProjectID != fx.project.ID {
		t.Fatalf("board SSE events=%+v, want exactly one members_updated for project %d", sseEvents, fx.project.ID)
	}
}

func assertMembershipRESTSilence(t *testing.T, collector *membershipEventCollector, stream *todoUpdateEventStream) {
	t.Helper()
	if events := collector.snapshot(); len(events) != 0 {
		t.Fatalf("membership failure emitted fanout events: %+v", events)
	}
	if events := collectTodoUpdateEvents(t, stream); len(events) != 0 {
		t.Fatalf("membership failure emitted SSE events: %+v", events)
	}
}

func TestProjectMembershipRESTMutationContracts(t *testing.T) {
	t.Run("add publishes ordered membership events", func(t *testing.T) {
		fx := newMembershipRESTFixture(t, "membership-rest-add")
		target := createMembershipUser(t, fx.st, "membership-rest-add-target")
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

		var members []projectMemberJSON
		resp, body := doJSON(t, fx.client, http.MethodPost, fx.membersURL(), map[string]any{
			"user_id": target.ID,
			"role":    "contributor",
		}, &members)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("add status=%d body=%s", resp.StatusCode, body)
		}
		member, ok := findMembershipRESTMember(members, target.ID)
		if !ok || member.Role != "contributor" || len(members) != 2 {
			t.Fatalf("add response members=%+v", members)
		}
		if role, err := fx.st.GetProjectRole(fx.ctx, fx.project.ID, target.ID); err != nil || role != store.RoleContributor {
			t.Fatalf("persisted role=%q err=%v", role, err)
		}
		if got := membershipAuditCount(t, fx.db, fx.project.ID, target.ID, "member_added"); got != 1 {
			t.Fatalf("member_added audit count=%d want=1", got)
		}
		assertMembershipRESTPublications(t, fx, stream, target.ID, "added")
	})

	t.Run("update publishes ordered membership events", func(t *testing.T) {
		fx := newMembershipRESTFixture(t, "membership-rest-update")
		target := createMembershipUser(t, fx.st, "membership-rest-update-target")
		if err := fx.st.AddProjectMember(fx.ctx, fx.ownerID, fx.project.ID, target.ID, store.RoleContributor); err != nil {
			t.Fatalf("seed member: %v", err)
		}
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

		var members []projectMemberJSON
		resp, body := doJSON(t, fx.client, http.MethodPatch, fx.membersURL()+fmt.Sprintf("/%d", target.ID), map[string]any{
			"role": "viewer",
		}, &members)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("update status=%d body=%s", resp.StatusCode, body)
		}
		member, ok := findMembershipRESTMember(members, target.ID)
		if !ok || member.Role != "viewer" || len(members) != 2 {
			t.Fatalf("update response members=%+v", members)
		}
		if got := membershipAuditCount(t, fx.db, fx.project.ID, target.ID, "member_role_changed"); got != 1 {
			t.Fatalf("member_role_changed audit count=%d want=1", got)
		}
		assertMembershipRESTPublications(t, fx, stream, target.ID, "role_changed")
	})

	t.Run("remove clears assignment and publishes no todo event", func(t *testing.T) {
		fx := newMembershipRESTFixture(t, "membership-rest-remove")
		target := createMembershipUser(t, fx.st, "membership-rest-remove-target")
		if err := fx.st.AddProjectMember(fx.ctx, fx.ownerID, fx.project.ID, target.ID, store.RoleContributor); err != nil {
			t.Fatalf("seed member: %v", err)
		}
		assigneeID := target.ID
		todo, err := fx.st.CreateTodo(fx.ctx, fx.project.ID, store.CreateTodoInput{
			Title: "Unassign on member removal", AssigneeUserID: &assigneeID,
		}, store.ModeFull)
		if err != nil {
			t.Fatalf("create assigned todo: %v", err)
		}
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

		var members []projectMemberJSON
		resp, body := doJSON(t, fx.client, http.MethodDelete, fx.membersURL()+fmt.Sprintf("/%d", target.ID), nil, &members)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("remove status=%d body=%s", resp.StatusCode, body)
		}
		if _, ok := findMembershipRESTMember(members, target.ID); ok || len(members) != 1 {
			t.Fatalf("remove response members=%+v", members)
		}
		persisted, err := fx.st.GetTodoByLocalID(fx.ctx, fx.project.ID, todo.LocalID, store.ModeFull)
		if err != nil || persisted.AssigneeUserID != nil {
			t.Fatalf("removed member assignment persisted=%+v err=%v", persisted, err)
		}
		if got := membershipLedgerCount(t, fx.db, todo.ID); got != 1 {
			t.Fatalf("member_removed assignment ledger count=%d want=1", got)
		}
		if got := membershipAuditCount(t, fx.db, fx.project.ID, target.ID, "member_removed"); got != 1 {
			t.Fatalf("member_removed audit count=%d want=1", got)
		}
		assertMembershipRESTPublications(t, fx, stream, target.ID, "removed")
	})
}

func TestProjectMembershipRESTSemanticNoOpStillPublishes(t *testing.T) {
	fx := newMembershipRESTFixture(t, "membership-rest-noop")
	target := createMembershipUser(t, fx.st, "membership-rest-noop-target")
	if err := fx.st.AddProjectMember(fx.ctx, fx.ownerID, fx.project.ID, target.ID, store.RoleViewer); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

	var members []projectMemberJSON
	resp, body := doJSON(t, fx.client, http.MethodPatch, fx.membersURL()+fmt.Sprintf("/%d", target.ID), map[string]any{
		"role": " viewer ",
	}, &members)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("semantic no-op status=%d body=%s", resp.StatusCode, body)
	}
	if member, ok := findMembershipRESTMember(members, target.ID); !ok || member.Role != "viewer" {
		t.Fatalf("semantic no-op response=%+v", members)
	}
	if got := membershipAuditCount(t, fx.db, fx.project.ID, target.ID, "member_role_changed"); got != 0 {
		t.Fatalf("semantic no-op audit count=%d want=0", got)
	}
	assertMembershipRESTPublications(t, fx, stream, target.ID, "role_changed")
}

func TestProjectMembershipRESTFailureSilence(t *testing.T) {
	t.Run("validation", func(t *testing.T) {
		fx := newMembershipRESTFixture(t, "membership-rest-validation")
		target := createMembershipUser(t, fx.st, "membership-rest-validation-target")
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")
		resp, body := doJSON(t, fx.client, http.MethodPost, fx.membersURL(), map[string]any{"user_id": target.ID, "role": "invalid"}, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("validation status=%d body=%s", resp.StatusCode, body)
		}
		assertMembershipRESTSilence(t, fx.collector, stream)
	})

	t.Run("authorization", func(t *testing.T) {
		fx := newMembershipRESTFixture(t, "membership-rest-auth")
		contributor := createMembershipUser(t, fx.st, "membership-rest-auth-contributor")
		target := createMembershipUser(t, fx.st, "membership-rest-auth-target")
		if err := fx.st.AddProjectMember(fx.ctx, fx.ownerID, fx.project.ID, contributor.ID, store.RoleContributor); err != nil {
			t.Fatalf("seed contributor: %v", err)
		}
		client := newCookieClient(t)
		loginUserClient(t, client, fx.ts.URL, contributor.Email, "password123")
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")
		resp, body := doJSON(t, client, http.MethodPost, fx.membersURL(), map[string]any{"user_id": target.ID, "role": "viewer"}, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("authorization status=%d want concealed 404 body=%s", resp.StatusCode, body)
		}
		assertMembershipRESTSilence(t, fx.collector, stream)
	})

	t.Run("store rejection", func(t *testing.T) {
		fx := newMembershipRESTFixture(t, "membership-rest-store-rejection")
		target := createMembershipUser(t, fx.st, "membership-rest-store-rejection-target")
		if err := fx.st.AddProjectMember(fx.ctx, fx.ownerID, fx.project.ID, target.ID, store.RoleViewer); err != nil {
			t.Fatalf("seed member: %v", err)
		}
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")
		resp, body := doJSON(t, fx.client, http.MethodPost, fx.membersURL(), map[string]any{"user_id": target.ID, "role": "viewer"}, nil)
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("store rejection status=%d body=%s", resp.StatusCode, body)
		}
		assertMembershipRESTSilence(t, fx.collector, stream)
	})
}

type membershipListFailureStore struct {
	*store.Store
	err       error
	listCalls int
}

const membershipPostReadFailureMessage = "forced membership post-read failure"

func (s *membershipListFailureStore) ListProjectMembers(context.Context, int64, int64) ([]store.ProjectMember, error) {
	s.listCalls++
	return nil, s.err
}

func newMembershipListFailureRESTServer(t *testing.T) (*httptest.Server, *sql.DB, *membershipListFailureStore, *membershipEventCollector) {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "app.db"), db.Options{
		BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}
	wrapper := &membershipListFailureStore{Store: store.New(sqlDB, nil), err: errors.New(membershipPostReadFailureMessage)}
	srv := NewServer(wrapper, Options{MaxRequestBody: 1 << 20, ScrumboyMode: "full"})
	collector := &membershipEventCollector{}
	srv.fanout = eventbus.NewFanout(newSSEBridge(srv.hub), collector)
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		_ = sqlDB.Close()
	})
	return ts, sqlDB, wrapper, collector
}

func TestProjectMembershipRESTPostReadFailureOccursAfterCommit(t *testing.T) {
	for _, operation := range []string{"add", "update", "remove"} {
		t.Run(operation, func(t *testing.T) {
			ts, sqlDB, wrapped, collector := newMembershipListFailureRESTServer(t)
			client := newCookieClient(t)
			owner := bootstrapUserClient(t, client, ts.URL, "Membership Owner", "membership-postread-owner@example.com", "password123")
			ownerID := int64(owner["id"].(float64))
			ctx := store.WithUserID(context.Background(), ownerID)
			project, err := wrapped.Store.CreateProject(ctx, "membership-rest-postread")
			if err != nil {
				t.Fatalf("create project: %v", err)
			}
			target := createMembershipUser(t, wrapped.Store, "membership-rest-postread-target")
			method := http.MethodPost
			url := fmt.Sprintf("%s/api/projects/%d/members", ts.URL, project.ID)
			body := any(map[string]any{"user_id": target.ID, "role": "viewer"})
			wantRole := store.RoleViewer
			wantAudit := "member_added"
			if operation != "add" {
				if err := wrapped.Store.AddProjectMember(ctx, ownerID, project.ID, target.ID, store.RoleContributor); err != nil {
					t.Fatalf("seed member: %v", err)
				}
				url += fmt.Sprintf("/%d", target.ID)
			}
			switch operation {
			case "update":
				method = http.MethodPatch
				body = map[string]any{"role": "viewer"}
				wantAudit = "member_role_changed"
			case "remove":
				method = http.MethodDelete
				body = nil
				wantRole = ""
				wantAudit = "member_removed"
			}
			stream := subscribeTodoUpdateEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")

			var envelope apiErrorEnvelope
			resp, responseBody := doJSON(t, client, method, url, body, &envelope)
			if resp.StatusCode != http.StatusInternalServerError || envelope.Error.Code != "INTERNAL" || envelope.Error.Message != "internal error" {
				t.Fatalf("post-read status=%d error=%+v body=%s", resp.StatusCode, envelope, responseBody)
			}
			if len(envelope.Error.Details) != 1 || envelope.Error.Details["detail"] != membershipPostReadFailureMessage {
				t.Fatalf("post-read details=%+v want exact current detail", envelope.Error.Details)
			}
			if wrapped.listCalls != 1 {
				t.Fatalf("post-read calls=%d want=1", wrapped.listCalls)
			}
			if role, err := wrapped.Store.GetProjectRole(ctx, project.ID, target.ID); err != nil || role != wantRole {
				t.Fatalf("mutation did not commit before read failure: role=%q want=%q err=%v", role, wantRole, err)
			}
			if got := membershipAuditCount(t, sqlDB, project.ID, target.ID, wantAudit); got != 1 {
				t.Fatalf("post-read %s audit count=%d want=1", wantAudit, got)
			}
			assertMembershipRESTSilence(t, collector, stream)
		})
	}
}

func TestProjectMembershipRESTSelfRemovalCommitsThenPostReadFails(t *testing.T) {
	fx := newMembershipRESTFixture(t, "membership-rest-self-remove")
	secondMaintainer := createMembershipUser(t, fx.st, "membership-rest-second-maintainer")
	if err := fx.st.AddProjectMember(fx.ctx, fx.ownerID, fx.project.ID, secondMaintainer.ID, store.RoleMaintainer); err != nil {
		t.Fatalf("seed second maintainer: %v", err)
	}
	stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

	var envelope apiErrorEnvelope
	resp, body := doJSON(t, fx.client, http.MethodDelete, fx.membersURL()+fmt.Sprintf("/%d", fx.ownerID), nil, &envelope)
	if resp.StatusCode != http.StatusNotFound || envelope.Error.Code != "NOT_FOUND" || envelope.Error.Message != "not found" {
		t.Fatalf("self-removal status=%d error=%+v body=%s", resp.StatusCode, envelope, body)
	}
	if role, err := fx.st.GetProjectRole(fx.ctx, fx.project.ID, fx.ownerID); err != nil || role != "" {
		t.Fatalf("self-removal did not commit: role=%q err=%v", role, err)
	}
	if got := membershipAuditCount(t, fx.db, fx.project.ID, fx.ownerID, "member_removed"); got != 1 {
		t.Fatalf("self-removal audit count=%d want=1", got)
	}
	assertMembershipRESTSilence(t, fx.collector, stream)
}

func TestProjectMembershipRESTValidationRoleAndModeContracts(t *testing.T) {
	t.Run("body and role validation precede actor extraction", func(t *testing.T) {
		fx := newMembershipRESTFixture(t, "membership-rest-precedence")
		target := createMembershipUser(t, fx.st, "membership-rest-precedence-target")
		stateless := &http.Client{Transport: fx.ts.Client().Transport}
		var envelope apiErrorEnvelope
		resp, body := doJSON(t, stateless, http.MethodPost, fx.ts.URL+"/api/projects/not-an-id/members", "not-an-object", &envelope)
		if resp.StatusCode != http.StatusBadRequest || envelope.Error.Code != "VALIDATION_ERROR" || envelope.Error.Details["field"] != "projectId" {
			t.Fatalf("path precedence status=%d error=%+v body=%s", resp.StatusCode, envelope, body)
		}
		envelope = apiErrorEnvelope{}
		resp, body = doJSON(t, stateless, http.MethodPost, fx.membersURL(), map[string]any{"user_id": target.ID, "role": "invalid"}, &envelope)
		if resp.StatusCode != http.StatusBadRequest || envelope.Error.Code != "VALIDATION_ERROR" {
			t.Fatalf("invalid role precedence status=%d error=%+v body=%s", resp.StatusCode, envelope, body)
		}
		resp, body = doJSON(t, stateless, http.MethodPost, fx.membersURL(), map[string]any{"user_id": target.ID, "role": "viewer"}, &envelope)
		if resp.StatusCode != http.StatusUnauthorized || envelope.Error.Code != "UNAUTHORIZED" {
			t.Fatalf("actor precedence status=%d error=%+v body=%s", resp.StatusCode, envelope, body)
		}
	})

	t.Run("add accepts exact legacy owner and stores maintainer", func(t *testing.T) {
		fx := newMembershipRESTFixture(t, "membership-rest-owner-compat")
		target := createMembershipUser(t, fx.st, "membership-rest-owner-compat-target")
		resp, body := doJSON(t, fx.client, http.MethodPost, fx.membersURL(), map[string]any{"user_id": target.ID, "role": "owner"}, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("legacy owner status=%d body=%s", resp.StatusCode, body)
		}
		if role, err := fx.st.GetProjectRole(fx.ctx, fx.project.ID, target.ID); err != nil || role != store.RoleMaintainer {
			t.Fatalf("legacy owner stored role=%q err=%v", role, err)
		}
	})

	t.Run("add rejects non-exact legacy owner variants", func(t *testing.T) {
		fx := newMembershipRESTFixture(t, "membership-rest-owner-variants")
		for i, role := range []string{"Owner", " owner", "owner "} {
			t.Run(fmt.Sprintf("variant_%d", i), func(t *testing.T) {
				target := createMembershipUser(t, fx.st, fmt.Sprintf("membership-rest-owner-variant-%d", i))
				var envelope apiErrorEnvelope
				resp, body := doJSON(t, fx.client, http.MethodPost, fx.membersURL(), map[string]any{"user_id": target.ID, "role": role}, &envelope)
				if resp.StatusCode != http.StatusBadRequest || envelope.Error.Code != "VALIDATION_ERROR" || envelope.Error.Message != "invalid role" {
					t.Fatalf("role %q status=%d error=%+v body=%s", role, resp.StatusCode, envelope, body)
				}
				if envelope.Error.Details["field"] != "role" || envelope.Error.Details["reason"] != "invalid_role" {
					t.Fatalf("role %q details=%+v", role, envelope.Error.Details)
				}
				if gotRole, err := fx.st.GetProjectRole(fx.ctx, fx.project.ID, target.ID); err != nil || gotRole != "" {
					t.Fatalf("role %q unexpectedly mutated membership: stored=%q err=%v", role, gotRole, err)
				}
			})
		}
	})

	t.Run("Temporary Board creator is forbidden", func(t *testing.T) {
		ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
		t.Cleanup(cleanup)
		client := newCookieClient(t)
		owner := bootstrapUserClient(t, client, ts.URL, "Temporary Creator", "membership-temp@example.com", "password123")
		ownerID := int64(owner["id"].(float64))
		st := store.New(sqlDB, nil)
		board, err := st.CreateAnonymousBoard(store.WithUserID(context.Background(), ownerID))
		if err != nil {
			t.Fatalf("create Temporary Board: %v", err)
		}
		target := createMembershipUser(t, st, "membership-temp-target")
		resp, body := doJSON(t, client, http.MethodPost, fmt.Sprintf("%s/api/projects/%d/members", ts.URL, board.ID), map[string]any{"user_id": target.ID, "role": "viewer"}, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("Temporary Board status=%d want concealed 404 body=%s", resp.StatusCode, body)
		}
	})

	t.Run("Anonymous Mode hides numeric membership route", func(t *testing.T) {
		ts, sqlDB, cleanup := newTestHTTPServer(t, "anonymous")
		t.Cleanup(cleanup)
		st := store.New(sqlDB, nil)
		board, err := st.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatalf("create Anonymous Board: %v", err)
		}
		resp, body := doJSON(t, ts.Client(), http.MethodPost, fmt.Sprintf("%s/api/projects/%d/members", ts.URL, board.ID), map[string]any{"user_id": 1, "role": "viewer"}, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("Anonymous Mode status=%d want=404 body=%s", resp.StatusCode, body)
		}
	})
}

func TestProjectMembershipMCPMutationsAreStructurallyEventSilent(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "app.db"), db.Options{
		BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(sqlDB, nil)
	srv := NewServer(st, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		MCPHandler:     mcp.New(st, mcp.Options{Mode: "full"}),
	})
	st.SetTodoAssignedPublisher(srv.PublishTodoAssigned)
	collector := &membershipEventCollector{}
	srv.fanout = eventbus.NewFanout(newSSEBridge(srv.hub), collector)
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		_ = sqlDB.Close()
	})

	client := newCookieClient(t)
	owner := bootstrapUserClient(t, client, ts.URL, "Membership MCP Owner", "membership-mcp-fanout-owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	ctx := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctx, "membership MCP structural silence")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	target := createMembershipUser(t, st, "membership-mcp-fanout-target")
	stream := subscribeTodoUpdateEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")

	operations := []struct {
		tool  string
		input map[string]any
	}{
		{tool: "members_add", input: map[string]any{"projectSlug": project.Slug, "userId": target.ID, "role": "contributor"}},
		{tool: "members_updateRole", input: map[string]any{"projectSlug": project.Slug, "userId": target.ID, "role": "viewer"}},
		{tool: "members_remove", input: map[string]any{"projectSlug": project.Slug, "userId": target.ID}},
	}
	for _, operation := range operations {
		var output map[string]any
		resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/mcp", map[string]any{
			"tool": operation.tool, "input": operation.input,
		}, &output)
		if resp.StatusCode != http.StatusOK || output["ok"] != true {
			t.Fatalf("%s status=%d response=%+v body=%s", operation.tool, resp.StatusCode, output, body)
		}
		if events := collector.snapshot(); len(events) != 0 {
			t.Fatalf("%s emitted fanout events: %+v", operation.tool, events)
		}
	}
	if events := collectTodoUpdateEvents(t, stream); len(events) != 0 {
		t.Fatalf("MCP membership mutations emitted SSE events: %+v", events)
	}
}
