package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	publicboardapp "scrumboy/internal/application/publicboard"
	"scrumboy/internal/db"
	"scrumboy/internal/httpapi/ratelimit"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

const (
	publicSSERefreshLine = `data: {"type":"refresh_needed"}`
	publicSSERevokedLine = `data: {"type":"access_revoked"}`
	publicSSEPassword    = "password123"
)

var errPublicSSETransient = errors.New("transient eligibility failure sentinel")

// publicSSEFaultStore injects transient eligibility failures without changing
// any other store behavior.
type publicSSEFaultStore struct {
	*store.Store
	failEligibility atomic.Int32
}

func (f *publicSSEFaultStore) ResolveEligiblePublicProject(ctx context.Context, slug string) (int64, error) {
	for {
		remaining := f.failEligibility.Load()
		if remaining <= 0 {
			break
		}
		if f.failEligibility.CompareAndSwap(remaining, remaining-1) {
			return 0, errPublicSSETransient
		}
	}
	return f.Store.ResolveEligiblePublicProject(ctx, slug)
}

type publicSSEFixture struct {
	ts          *httptest.Server
	server      *Server
	st          *store.Store
	fault       *publicSSEFaultStore
	db          *sql.DB
	owner       store.User
	project     store.Project
	ownerClient *http.Client
}

func newPublicSSEFixture(t *testing.T, options ...func(*Options)) *publicSSEFixture {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "app.db"), db.Options{BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(sqlDB, nil)
	fault := &publicSSEFaultStore{Store: st}
	opts := Options{
		ScrumboyMode:                   "full",
		PublicProjectsEnabled:          true,
		MaxRequestBody:                 1 << 20,
		PublicReadRateLimit:            ratelimit.New(10_000, time.Minute),
		PublicStreamAttemptRateLimit:   ratelimit.New(10_000, time.Minute),
		PublicStreamHeartbeatInterval:  time.Hour,
		PublicStreamRevalidateInterval: time.Hour,
	}
	for _, apply := range options {
		apply(&opts)
	}
	server := NewServer(fault, opts)
	st.SetTodoAssignedPublisher(server.PublishTodoAssigned)
	ts := httptest.NewServer(server)
	t.Cleanup(func() {
		// Close public streams first so httptest can drain active handlers.
		server.ShutdownPublicStreams()
		ts.Close()
		server.Close(context.Background())
		_ = sqlDB.Close()
	})

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "sse-owner@example.com", publicSSEPassword, "Owner Identity Sentinel")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	project, err := st.CreateProject(store.WithUserID(ctx, owner.ID), "Realtime Public")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := st.UpdateProjectPublicViewing(ctx, project.ID, owner.ID, true); err != nil {
		t.Fatalf("publish: %v", err)
	}
	ownerClient := newCookieClient(t)
	loginUserClient(t, ownerClient, ts.URL, "sse-owner@example.com", publicSSEPassword)
	return &publicSSEFixture{ts: ts, server: server, st: st, fault: fault, db: sqlDB, owner: owner, project: project, ownerClient: ownerClient}
}

func (f *publicSSEFixture) eventsURL(slug string) string {
	return f.ts.URL + "/api/public/board/" + slug + "/events"
}

func (f *publicSSEFixture) publishedProject(t *testing.T, name string) store.Project {
	t.Helper()
	ctx := context.Background()
	project, err := f.st.CreateProject(store.WithUserID(ctx, f.owner.ID), name)
	if err != nil {
		t.Fatalf("CreateProject %s: %v", name, err)
	}
	if _, err := f.st.UpdateProjectPublicViewing(ctx, project.ID, f.owner.ID, true); err != nil {
		t.Fatalf("publish %s: %v", name, err)
	}
	return project
}

func (f *publicSSEFixture) setPublication(t *testing.T, projectID int64, enabled bool) {
	t.Helper()
	result, err := f.server.publicBoardPublications.SetPublication(
		store.WithUserID(context.Background(), f.owner.ID),
		publicboardapp.PublicationCommand{ProjectID: projectID, Enabled: enabled},
	)
	if err != nil || !result.Changed || result.Enabled != enabled {
		t.Fatalf("SetPublication(%v) = %+v, %v", enabled, result, err)
	}
}

func (f *publicSSEFixture) createUser(t *testing.T, email string) (store.User, *http.Client) {
	t.Helper()
	user, err := f.st.CreateUser(store.WithUserID(context.Background(), f.owner.ID), email, publicSSEPassword, "Member Identity Sentinel")
	if err != nil {
		t.Fatalf("CreateUser %s: %v", email, err)
	}
	client := newCookieClient(t)
	loginUserClient(t, client, f.ts.URL, email, publicSSEPassword)
	return user, client
}

func (f *publicSSEFixture) ownerJSON(t *testing.T, method, path string, body any, want int) []byte {
	t.Helper()
	resp, raw := doJSON(t, f.ownerClient, method, f.ts.URL+path, body, nil)
	if resp.StatusCode != want {
		t.Fatalf("%s %s status=%d want %d body=%s", method, path, resp.StatusCode, want, raw)
	}
	return raw
}

func (f *publicSSEFixture) createTodo(t *testing.T, slug, title string) int64 {
	t.Helper()
	raw := f.ownerJSON(t, http.MethodPost, "/api/board/"+slug+"/todos", map[string]any{
		"title": title, "body": "body", "tags": []string{}, "columnKey": "backlog",
	}, http.StatusCreated)
	var created struct {
		LocalID int64 `json:"localId"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.LocalID <= 0 {
		t.Fatalf("decode created todo %s: %v", raw, err)
	}
	return created.LocalID
}

func (f *publicSSEFixture) probe(t *testing.T, projectID int64) *PublicSubscription {
	t.Helper()
	subscription, err := f.server.publicHub.Subscribe(projectID, "ip:probe")
	if err != nil {
		t.Fatalf("probe Subscribe: %v", err)
	}
	t.Cleanup(subscription.Unsubscribe)
	return subscription
}

type publicSSEStream struct {
	resp   *http.Response
	lines  chan string
	cancel context.CancelFunc
	done   chan struct{}
}

// openPublicSSE returns once response headers arrive. For a 200 stream the
// hub registration therefore already exists, which makes later emissions
// deterministic without sleeps.
func openPublicSSE(t *testing.T, client *http.Client, target string, headers map[string]string) (*publicSSEStream, []byte) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		cancel()
		t.Fatalf("NewRequest: %v", err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("GET %s: %v", target, err)
	}
	stream := &publicSSEStream{resp: resp, lines: make(chan string, 64), cancel: cancel, done: make(chan struct{})}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
		close(stream.lines)
		close(stream.done)
		return stream, body
	}
	go func() {
		defer close(stream.done)
		defer close(stream.lines)
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			if trimmed := strings.TrimRight(line, "\r\n"); trimmed != "" {
				stream.lines <- trimmed
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = resp.Body.Close()
		<-stream.done
	})
	return stream, nil
}

func (s *publicSSEStream) next(t *testing.T) (string, bool) {
	t.Helper()
	select {
	case line, open := <-s.lines:
		return line, open
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for public SSE line")
		return "", false
	}
}

func (s *publicSSEStream) expectLine(t *testing.T, want string) {
	t.Helper()
	got, open := s.next(t)
	if !open || got != want {
		t.Fatalf("public SSE line = %q (open=%v), want %q", got, open, want)
	}
}

func (s *publicSSEStream) expectEOF(t *testing.T) {
	t.Helper()
	if got, open := s.next(t); open {
		t.Fatalf("public SSE line = %q, want closed stream", got)
	}
}

func expectProbeEvents(t *testing.T, probe *PublicSubscription, want int) {
	t.Helper()
	for index := 0; index < want; index++ {
		select {
		case event, open := <-probe.Events:
			if !open || event != PublicEventRefreshNeeded {
				t.Fatalf("probe event %d = %v (open=%v)", index, event, open)
			}
		default:
			t.Fatalf("probe received %d public events, want %d", index, want)
		}
	}
	select {
	case event := <-probe.Events:
		t.Fatalf("probe received unexpected extra public event %v", event)
	default:
	}
}

func waitForPublicCondition(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func assertPublicStreamHeaders(t *testing.T, resp *http.Response) {
	t.Helper()
	for key, want := range map[string]string{
		"Content-Type":           "text/event-stream",
		"Cache-Control":          "no-store",
		"X-Robots-Tag":           "noindex, nofollow",
		"X-Content-Type-Options": "nosniff",
		"X-Accel-Buffering":      "no",
	} {
		if got := resp.Header.Get(key); got != want {
			t.Fatalf("header %s = %q, want %q", key, got, want)
		}
	}
}

func genericPublicNotFoundBody(t *testing.T, f *publicSSEFixture) []byte {
	t.Helper()
	resp, body := publicHTTP(t, f.ts.Client(), http.MethodGet, f.ts.URL+"/api/public/board/missing-generic-board", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("generic 404 status=%d", resp.StatusCode)
	}
	return body
}

func TestPublicSSEPhase3AnonymousAndSignedInNonmemberShareExactContract(t *testing.T) {
	f := newPublicSSEFixture(t)
	anonymous, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	if anonymous.resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous status=%d", anonymous.resp.StatusCode)
	}
	assertPublicStreamHeaders(t, anonymous.resp)

	_, nonmemberClient := f.createUser(t, "sse-nonmember@example.com")
	nonmember, _ := openPublicSSE(t, nonmemberClient, f.eventsURL(f.project.Slug), nil)
	if nonmember.resp.StatusCode != http.StatusOK {
		t.Fatalf("nonmember status=%d", nonmember.resp.StatusCode)
	}
	assertPublicStreamHeaders(t, nonmember.resp)

	// The signed-in nonmember gains no member stream.
	memberStream, memberBody := openPublicSSE(t, nonmemberClient, f.ts.URL+"/api/board/"+f.project.Slug+"/events", nil)
	if memberStream.resp.StatusCode != http.StatusNotFound {
		t.Fatalf("nonmember private stream status=%d body=%s", memberStream.resp.StatusCode, memberBody)
	}

	f.createTodo(t, f.project.Slug, "Private Title Sentinel")
	anonymous.expectLine(t, publicSSERefreshLine)
	nonmember.expectLine(t, publicSSERefreshLine)
	if counts := f.server.publicHub.counts(); counts.Global != 2 || counts.ByProject[f.project.ID] != 2 {
		t.Fatalf("hub counts = %+v", counts)
	}
}

func TestPublicSSEPhase3IneligibleSubscriptionsReturnGeneric404(t *testing.T) {
	f := newPublicSSEFixture(t)
	ctx := context.Background()
	ownerCtx := store.WithUserID(ctx, f.owner.ID)
	private, _ := f.st.CreateProject(ownerCtx, "SSE Private")
	staging, _ := f.st.CreateProject(ownerCtx, "SSE Staging")
	reserved, _ := f.st.CreateProject(ownerCtx, "SSE Reserved")
	temporary, err := f.st.CreateAnonymousBoard(ownerCtx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	if _, err := f.db.ExecContext(ctx, `
UPDATE projects SET public_view_enabled = 1, import_batch_id = 'pending' WHERE id = ?;
UPDATE projects SET public_view_enabled = 1, slug = 'dashboard' WHERE id = ?;
UPDATE projects SET public_view_enabled = 1 WHERE id = ?`, staging.ID, reserved.ID, temporary.ID); err != nil {
		t.Fatalf("seed ineligible: %v", err)
	}
	generic := genericPublicNotFoundBody(t, f)
	for _, slug := range []string{private.Slug, staging.Slug, "dashboard", temporary.Slug, "missing-public-board"} {
		stream, body := openPublicSSE(t, f.ts.Client(), f.eventsURL(slug), nil)
		if stream.resp.StatusCode != http.StatusNotFound || !bytes.Equal(body, generic) {
			t.Fatalf("slug %q status=%d body=%s", slug, stream.resp.StatusCode, body)
		}
		assertPublicHeaders(t, stream.resp)
	}
	if counts := f.server.publicHub.counts(); counts.Global != 0 {
		t.Fatalf("ineligible attempts leaked subscribers: %+v", counts)
	}

	// Query strings are validated only after eligibility.
	privateQuery, body := openPublicSSE(t, f.ts.Client(), f.eventsURL(private.Slug)+"?x=1", nil)
	if privateQuery.resp.StatusCode != http.StatusNotFound || !bytes.Equal(body, generic) {
		t.Fatalf("private query status=%d body=%s", privateQuery.resp.StatusCode, body)
	}
	publicQuery, body := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug)+"?x=1", nil)
	if publicQuery.resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("public query status=%d body=%s", publicQuery.resp.StatusCode, body)
	}

	// Non-GET methods never reach the stream.
	resp, body := publicHTTPWithHeaders(t, f.ts.Client(), http.MethodPost, f.eventsURL(f.project.Slug), map[string]string{"X-Scrumboy": "1"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST events status=%d body=%s", resp.StatusCode, body)
	}

	for name, opts := range map[string]Options{
		"operator gate off": {ScrumboyMode: "full", PublicProjectsEnabled: false},
		"anonymous mode":    {ScrumboyMode: "anonymous", PublicProjectsEnabled: true},
	} {
		t.Run(name, func(t *testing.T) {
			server := NewServer(f.st, opts)
			defer server.Close(context.Background())
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/public/board/"+f.project.Slug+"/events", nil))
			if recorder.Code != http.StatusNotFound || !bytes.Equal(recorder.Body.Bytes(), generic) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.Bytes())
			}
			if counts := server.publicHub.counts(); counts.Global != 0 {
				t.Fatalf("disabled capability registered subscribers: %+v", counts)
			}
		})
	}
}

func TestPublicSSEPhase3PublicMutationsEmitOneMinimalRefresh(t *testing.T) {
	f := newPublicSSEFixture(t)
	slug := f.project.Slug
	localID := f.createTodo(t, slug, "Seed")
	stream, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(slug), nil)
	if stream.resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", stream.resp.StatusCode)
	}
	probe := f.probe(t, f.project.ID)

	// A board-scoped (shared) tag on a durable project carries the public color.
	result, err := f.db.Exec(`INSERT INTO tags(user_id, name, created_at, project_id, color) VALUES (NULL, 'board-scoped', ?, ?, NULL)`, time.Now().UTC().UnixMilli(), f.project.ID)
	if err != nil {
		t.Fatalf("insert board tag: %v", err)
	}
	boardTagID, _ := result.LastInsertId()

	mutations := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"todo create", func(t *testing.T) { f.createTodo(t, slug, "Created") }},
		{"todo content update", func(t *testing.T) {
			f.ownerJSON(t, http.MethodPatch, fmt.Sprintf("/api/board/%s/todos/%d", slug, localID), map[string]any{
				"title": "Seed edited", "body": "body", "tags": []string{}, "assigneeUserId": nil,
			}, http.StatusOK)
		}},
		{"todo move", func(t *testing.T) {
			f.ownerJSON(t, http.MethodPost, fmt.Sprintf("/api/board/%s/todos/%d/move", slug, localID), map[string]any{"toColumnKey": "doing"}, http.StatusOK)
		}},
		{"shared tag color", func(t *testing.T) {
			if _, err := f.db.Exec(`INSERT INTO todo_tags(todo_id, tag_id) SELECT id, ? FROM todos WHERE project_id = ? AND local_id = ?`, boardTagID, f.project.ID, localID); err != nil {
				t.Fatalf("link board tag: %v", err)
			}
			f.ownerJSON(t, http.MethodPatch, fmt.Sprintf("/api/board/%s/tags/id/%d/color", slug, boardTagID), map[string]any{"color": "#112233"}, http.StatusNoContent)
		}},
		{"workflow lane add", func(t *testing.T) {
			f.ownerJSON(t, http.MethodPost, "/api/board/"+slug+"/workflow", map[string]any{"name": "Review"}, http.StatusCreated)
		}},
		{"sprint create", func(t *testing.T) {
			start := time.Now().UTC()
			f.ownerJSON(t, http.MethodPost, "/api/board/"+slug+"/sprints", map[string]any{
				"name": "Sprint Public", "plannedStartAt": start.UnixMilli(), "plannedEndAt": start.Add(7 * 24 * time.Hour).UnixMilli(),
			}, http.StatusCreated)
		}},
		{"sprints enabled setting", func(t *testing.T) {
			f.ownerJSON(t, http.MethodPatch, "/api/board/"+slug+"/settings", map[string]any{"sprintsEnabled": false}, http.StatusOK)
		}},
		{"todo archive", func(t *testing.T) {
			f.ownerJSON(t, http.MethodPost, fmt.Sprintf("/api/board/%s/todos/%d/archive", slug, localID), nil, http.StatusOK)
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			mutation.run(t)
			expectProbeEvents(t, probe, 1)
			stream.expectLine(t, publicSSERefreshLine)
		})
	}
}

func TestPublicSSEPhase3PrivateMutationsEmitNoPublicInvalidation(t *testing.T) {
	f := newPublicSSEFixture(t, func(opts *Options) { opts.WallEnabled = true })
	slug := f.project.Slug
	localID := f.createTodo(t, slug, "Assignable")
	member, _ := f.createUser(t, "sse-member@example.com")
	newcomer, _ := f.createUser(t, "sse-newcomer@example.com")
	if err := f.st.AddProjectMember(store.WithUserID(context.Background(), f.owner.ID), f.owner.ID, f.project.ID, member.ID, store.RoleContributor); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}
	stream, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(slug), nil)
	if stream.resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", stream.resp.StatusCode)
	}

	// A user-owned tag on a project todo: its color is a personal preference.
	now := time.Now().UTC().UnixMilli()
	result, err := f.db.Exec(`INSERT INTO tags(user_id, name, created_at, project_id, color) VALUES (?, 'personal', ?, NULL, NULL)`, f.owner.ID, now)
	if err != nil {
		t.Fatalf("insert personal tag: %v", err)
	}
	personalTagID, _ := result.LastInsertId()
	if _, err := f.db.Exec(`INSERT INTO todo_tags(todo_id, tag_id) SELECT id, ? FROM todos WHERE project_id = ? AND local_id = ?`, personalTagID, f.project.ID, localID); err != nil {
		t.Fatalf("link personal tag: %v", err)
	}
	probe := f.probe(t, f.project.ID)

	privateMutations := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"assignment only", func(t *testing.T) {
			f.ownerJSON(t, http.MethodPatch, fmt.Sprintf("/api/board/%s/todos/%d", slug, localID), map[string]any{
				"title": "Assignable", "body": "body", "tags": []string{"personal"}, "assigneeUserId": member.ID,
			}, http.StatusOK)
		}},
		{"unassignment only", func(t *testing.T) {
			f.ownerJSON(t, http.MethodPatch, fmt.Sprintf("/api/board/%s/todos/%d", slug, localID), map[string]any{
				"title": "Assignable", "body": "body", "tags": []string{"personal"}, "assigneeUserId": nil,
			}, http.StatusOK)
		}},
		{"membership add", func(t *testing.T) {
			f.ownerJSON(t, http.MethodPost, fmt.Sprintf("/api/projects/%d/members", f.project.ID), map[string]any{"user_id": newcomer.ID, "role": "viewer"}, http.StatusOK)
		}},
		{"personal tag color by id", func(t *testing.T) {
			f.ownerJSON(t, http.MethodPatch, fmt.Sprintf("/api/board/%s/tags/id/%d/color", slug, personalTagID), map[string]any{"color": "#445566"}, http.StatusNoContent)
		}},
		{"personal tag color by name", func(t *testing.T) {
			f.ownerJSON(t, http.MethodPatch, "/api/board/"+slug+"/tags/personal/color", map[string]any{"color": "#778899"}, http.StatusNoContent)
		}},
		{"wall note", func(t *testing.T) {
			f.ownerJSON(t, http.MethodPost, "/api/board/"+slug+"/wall/notes", map[string]any{
				"x": 1.0, "y": 2.0, "width": 180, "height": 140, "color": "#ffd966", "text": "wall secret sentinel",
			}, http.StatusCreated)
		}},
		{"wall transient", func(t *testing.T) {
			resp, body := doJSON(t, f.ownerClient, http.MethodPost, f.ts.URL+"/api/board/"+slug+"/wall/transient", map[string]any{"noteId": "n1", "x": 3.0, "y": 4.0}, nil)
			if resp.StatusCode >= 300 {
				t.Fatalf("wall transient status=%d body=%s", resp.StatusCode, body)
			}
		}},
		{"default sprint weeks setting", func(t *testing.T) {
			f.ownerJSON(t, http.MethodPatch, "/api/board/"+slug+"/settings", map[string]any{"defaultSprintWeeks": 1}, http.StatusOK)
		}},
	}
	for _, mutation := range privateMutations {
		t.Run(mutation.name, func(t *testing.T) {
			mutation.run(t)
			expectProbeEvents(t, probe, 0)
		})
	}

	// The stream received nothing from the private mutations: the first line
	// after a public sentinel mutation is exactly one refresh.
	f.createTodo(t, slug, "Sentinel")
	expectProbeEvents(t, probe, 1)
	stream.expectLine(t, publicSSERefreshLine)
}

func TestPublicSSEPhase3AssignmentWithContentChangeEmitsOnce(t *testing.T) {
	f := newPublicSSEFixture(t)
	slug := f.project.Slug
	localID := f.createTodo(t, slug, "Before")
	member, _ := f.createUser(t, "sse-assignee@example.com")
	if err := f.st.AddProjectMember(store.WithUserID(context.Background(), f.owner.ID), f.owner.ID, f.project.ID, member.ID, store.RoleContributor); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}
	probe := f.probe(t, f.project.ID)
	f.ownerJSON(t, http.MethodPatch, fmt.Sprintf("/api/board/%s/todos/%d", slug, localID), map[string]any{
		"title": "After", "body": "body", "tags": []string{}, "assigneeUserId": member.ID,
	}, http.StatusOK)
	expectProbeEvents(t, probe, 1)
}

func TestPublicSSEPhase3UnpublishRevokesAllStreamsAndRepublishNeedsNewSubscription(t *testing.T) {
	f := newPublicSSEFixture(t)
	other := f.publishedProject(t, "Other Realtime Public")
	first, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	second, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	otherStream, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(other.Slug), nil)
	for _, stream := range []*publicSSEStream{first, second, otherStream} {
		if stream.resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d", stream.resp.StatusCode)
		}
	}

	f.setPublication(t, f.project.ID, false)
	for _, stream := range []*publicSSEStream{first, second} {
		stream.expectLine(t, publicSSERevokedLine)
		stream.expectEOF(t)
	}
	waitForPublicCondition(t, "revoked counters released", func() bool {
		counts := f.server.publicHub.counts()
		return counts.ByProject[f.project.ID] == 0 && counts.Global == 1
	})

	// Cross-project isolation: the other project's stream is untouched.
	f.createTodo(t, other.Slug, "Other change")
	otherStream.expectLine(t, publicSSERefreshLine)

	generic := genericPublicNotFoundBody(t, f)
	reconnect, body := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	if reconnect.resp.StatusCode != http.StatusNotFound || !bytes.Equal(body, generic) {
		t.Fatalf("reconnect after unpublish status=%d body=%s", reconnect.resp.StatusCode, body)
	}

	f.setPublication(t, f.project.ID, true)
	// The old streams stay closed; only a new subscription is admitted.
	fresh, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	if fresh.resp.StatusCode != http.StatusOK {
		t.Fatalf("republished status=%d", fresh.resp.StatusCode)
	}
	f.createTodo(t, f.project.Slug, "After republish")
	fresh.expectLine(t, publicSSERefreshLine)
	if counts := f.server.publicHub.counts(); counts.ByProject[f.project.ID] != 1 {
		t.Fatalf("republish resurrected old subscribers: %+v", counts)
	}
}

func TestPublicSSEPhase3ProjectDeletionRevokesStreams(t *testing.T) {
	f := newPublicSSEFixture(t)
	stream, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	if stream.resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", stream.resp.StatusCode)
	}
	f.ownerJSON(t, http.MethodDelete, fmt.Sprintf("/api/projects/%d", f.project.ID), nil, http.StatusNoContent)
	stream.expectLine(t, publicSSERevokedLine)
	stream.expectEOF(t)
	waitForPublicCondition(t, "deleted project counters released", func() bool {
		return f.server.publicHub.counts().Global == 0
	})
}

func TestPublicSSEPhase3TransientRevalidationFailureClosesOnlyAffectedStream(t *testing.T) {
	f := newPublicSSEFixture(t)
	first, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	second, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	if first.resp.StatusCode != http.StatusOK || second.resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d/%d", first.resp.StatusCode, second.resp.StatusCode)
	}

	// Exactly one of the two pre-delivery revalidations fails transiently.
	f.fault.failEligibility.Store(1)
	f.server.publicHub.RefreshPublicProject(f.project.ID)

	var survivor *publicSSEStream
	survivors := 0
	for _, stream := range []*publicSSEStream{first, second} {
		line, open := stream.next(t)
		switch {
		case !open:
			// The failed stream closed silently: no access_revoked, so the
			// client reconnects through full admission.
		case line == publicSSERefreshLine:
			survivors++
			survivor = stream
		default:
			t.Fatalf("unexpected line after transient failure: %q", line)
		}
	}
	if survivors != 1 {
		t.Fatalf("survivors = %d, want exactly 1", survivors)
	}
	waitForPublicCondition(t, "failed stream released", func() bool {
		return f.server.publicHub.counts().ByProject[f.project.ID] == 1
	})

	// The project remains eligible: the survivor keeps receiving and a
	// reconnect is admitted.
	f.createTodo(t, f.project.Slug, "Still public")
	survivor.expectLine(t, publicSSERefreshLine)
	reconnect, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	if reconnect.resp.StatusCode != http.StatusOK {
		t.Fatalf("reconnect after transient failure status=%d", reconnect.resp.StatusCode)
	}

	// A transient failure at admission is a generic 500, not an admitted stream.
	f.fault.failEligibility.Store(2)
	failed, body := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	if failed.resp.StatusCode != http.StatusInternalServerError || bytes.Contains(body, []byte(errPublicSSETransient.Error())) {
		t.Fatalf("admission transient failure status=%d body=%s", failed.resp.StatusCode, body)
	}
	f.fault.failEligibility.Store(0)
	if counts := f.server.publicHub.counts(); counts.ByProject[f.project.ID] != 2 {
		t.Fatalf("failed admission leaked a subscriber: %+v", counts)
	}
}

func TestPublicSSEPhase3PeriodicRevalidationAndHeartbeat(t *testing.T) {
	t.Run("heartbeat is a content-free comment", func(t *testing.T) {
		f := newPublicSSEFixture(t, func(opts *Options) { opts.PublicStreamHeartbeatInterval = 10 * time.Millisecond })
		stream, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
		stream.expectLine(t, ": heartbeat")
	})

	t.Run("out-of-band unpublish is detected per connection", func(t *testing.T) {
		f := newPublicSSEFixture(t, func(opts *Options) { opts.PublicStreamRevalidateInterval = 10 * time.Millisecond })
		stream, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
		if stream.resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d", stream.resp.StatusCode)
		}
		if _, err := f.db.Exec(`UPDATE projects SET public_view_enabled = 0 WHERE id = ?`, f.project.ID); err != nil {
			t.Fatalf("out-of-band unpublish: %v", err)
		}
		stream.expectLine(t, publicSSERevokedLine)
		stream.expectEOF(t)
	})

	t.Run("slug rename invalidates the old-slug stream", func(t *testing.T) {
		f := newPublicSSEFixture(t)
		stream, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
		if _, err := f.db.Exec(`UPDATE projects SET slug = 'renamed-public-board' WHERE id = ?`, f.project.ID); err != nil {
			t.Fatalf("rename: %v", err)
		}
		f.server.publicHub.RefreshPublicProject(f.project.ID)
		stream.expectLine(t, publicSSERevokedLine)
		stream.expectEOF(t)
	})
}

func TestPublicSSEPhase3ConcurrencyCapsRejectSafelyAndRelease(t *testing.T) {
	f := newPublicSSEFixture(t, func(opts *Options) {
		opts.TrustProxy = true
		opts.PublicStreamLimits = PublicHubLimits{Global: 3, PerIP: 2, PerProject: 2}
	})
	other := f.publishedProject(t, "Capped Other")
	open := func(slug, ip string) (*publicSSEStream, []byte) {
		return openPublicSSE(t, f.ts.Client(), f.eventsURL(slug), map[string]string{"X-Forwarded-For": ip})
	}
	assertLimited := func(stream *publicSSEStream, body []byte) {
		t.Helper()
		if stream.resp.StatusCode != http.StatusTooManyRequests || stream.resp.Header.Get("Retry-After") != "60" ||
			!bytes.Contains(body, []byte(`"code":"RATE_LIMITED"`)) {
			t.Fatalf("limited status=%d retry=%q body=%s", stream.resp.StatusCode, stream.resp.Header.Get("Retry-After"), body)
		}
		assertPublicHeaders(t, stream.resp)
	}

	a1, _ := open(f.project.Slug, "198.51.100.1")
	a2, _ := open(f.project.Slug, "198.51.100.1")
	if a1.resp.StatusCode != http.StatusOK || a2.resp.StatusCode != http.StatusOK {
		t.Fatalf("initial status=%d/%d", a1.resp.StatusCode, a2.resp.StatusCode)
	}
	assertLimited(open(other.Slug, "198.51.100.1"))     // per IP
	assertLimited(open(f.project.Slug, "198.51.100.2")) // per project
	b1, _ := open(other.Slug, "198.51.100.2")           // global now 3
	if b1.resp.StatusCode != http.StatusOK {
		t.Fatalf("other project status=%d", b1.resp.StatusCode)
	}
	assertLimited(open(other.Slug, "198.51.100.3")) // global
	if counts := f.server.publicHub.counts(); counts.Global != 3 || counts.ByIP["ip:198.51.100.1"] != 2 || counts.ByProject[f.project.ID] != 2 {
		t.Fatalf("rejections changed counters: %+v", counts)
	}

	a1.cancel()
	waitForPublicCondition(t, "client disconnect release", func() bool { return f.server.publicHub.counts().Global == 2 })
	c1, _ := open(other.Slug, "198.51.100.3")
	if c1.resp.StatusCode != http.StatusOK {
		t.Fatalf("after release status=%d", c1.resp.StatusCode)
	}

	t.Run("untrusted forwarded header cannot bypass per-IP cap", func(t *testing.T) {
		g := newPublicSSEFixture(t, func(opts *Options) {
			opts.TrustProxy = false
			opts.PublicStreamLimits = PublicHubLimits{PerIP: 1}
		})
		first, _ := openPublicSSE(t, g.ts.Client(), g.eventsURL(g.project.Slug), map[string]string{"X-Forwarded-For": "203.0.113.1"})
		if first.resp.StatusCode != http.StatusOK {
			t.Fatalf("first status=%d", first.resp.StatusCode)
		}
		second, body := openPublicSSE(t, g.ts.Client(), g.eventsURL(g.project.Slug), map[string]string{"X-Forwarded-For": "203.0.113.2"})
		if second.resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("spoofed second status=%d body=%s", second.resp.StatusCode, body)
		}
	})
}

func TestPublicSSEPhase3StreamAttemptLimitIsUniformAndIsolated(t *testing.T) {
	f := newPublicSSEFixture(t, func(opts *Options) { opts.PublicStreamAttemptRateLimit = ratelimit.New(2, time.Minute) })
	private, _ := f.st.CreateProject(store.WithUserID(context.Background(), f.owner.ID), "Attempt Private")
	first, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(private.Slug), nil)
	if first.resp.StatusCode != http.StatusNotFound {
		t.Fatalf("private attempt status=%d", first.resp.StatusCode)
	}
	second, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	if second.resp.StatusCode != http.StatusOK {
		t.Fatalf("eligible attempt status=%d", second.resp.StatusCode)
	}
	var limitedBody []byte
	for _, slug := range []string{f.project.Slug, private.Slug, "missing-after-limit"} {
		stream, body := openPublicSSE(t, f.ts.Client(), f.eventsURL(slug), nil)
		if stream.resp.StatusCode != http.StatusTooManyRequests || stream.resp.Header.Get("Retry-After") != "60" {
			t.Fatalf("slug %q status=%d body=%s", slug, stream.resp.StatusCode, body)
		}
		if limitedBody == nil {
			limitedBody = body
		} else if !bytes.Equal(body, limitedBody) {
			t.Fatalf("distinguishable stream-attempt 429: %s vs %s", body, limitedBody)
		}
	}
	// Public JSON reads and authenticated realtime keep independent budgets.
	if resp, body := publicHTTP(t, f.ts.Client(), http.MethodGet, f.ts.URL+"/api/public/board/"+f.project.Slug, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("public JSON after stream limit status=%d body=%s", resp.StatusCode, body)
	}
	member, _ := openPublicSSE(t, f.ownerClient, f.ts.URL+"/api/board/"+f.project.Slug+"/events", nil)
	if member.resp.StatusCode != http.StatusOK {
		t.Fatalf("member stream after public stream limit status=%d", member.resp.StatusCode)
	}
}

func TestPublicSSEPhase3DisconnectAndShutdownCleanUp(t *testing.T) {
	f := newPublicSSEFixture(t)
	first, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	second, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	if first.resp.StatusCode != http.StatusOK || second.resp.StatusCode != http.StatusOK {
		t.Fatal("streams not admitted")
	}
	first.cancel()
	<-first.done
	waitForPublicCondition(t, "disconnect cleanup", func() bool { return f.server.publicHub.counts().Global == 1 })

	f.server.ShutdownPublicStreams()
	second.expectEOF(t)
	if counts := f.server.publicHub.counts(); counts.Global != 0 || len(counts.ByIP) != 0 || len(counts.ByProject) != 0 {
		t.Fatalf("shutdown left counters: %+v", counts)
	}
	f.server.ShutdownPublicStreams()
	after, body := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	if after.resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("admission after shutdown status=%d body=%s", after.resp.StatusCode, body)
	}
}

func TestPublicSSEPhase3SlowSubscriberEvictedWhileHealthyStreamContinues(t *testing.T) {
	f := newPublicSSEFixture(t)
	stream, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	if stream.resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", stream.resp.StatusCode)
	}
	slow, err := f.server.publicHub.Subscribe(f.project.ID, "ip:slow")
	if err != nil {
		t.Fatalf("slow Subscribe: %v", err)
	}
	defer slow.Unsubscribe()
	for index := 0; index <= defaultPublicSubscriberBuffer; index++ {
		f.server.publicHub.RefreshPublicProject(f.project.ID)
		stream.expectLine(t, publicSSERefreshLine)
	}
	if slow.Active() {
		t.Fatal("slow subscriber was not evicted after buffer overflow")
	}
	for index := 0; index < defaultPublicSubscriberBuffer; index++ {
		if event, open := <-slow.Events; !open || event != PublicEventRefreshNeeded {
			t.Fatalf("slow buffered event %d = %v/%v", index, event, open)
		}
	}
	if _, open := <-slow.Events; open {
		t.Fatal("evicted channel remained open")
	}
	if counts := f.server.publicHub.counts(); counts.ByProject[f.project.ID] != 1 {
		t.Fatalf("eviction did not release counters: %+v", counts)
	}
	f.server.publicHub.RefreshPublicProject(f.project.ID)
	stream.expectLine(t, publicSSERefreshLine)
}

func TestPublicSSEPhase3ConcurrentEmitRevokeAndDisconnect(t *testing.T) {
	f := newPublicSSEFixture(t, func(opts *Options) {
		opts.PublicStreamLimits = PublicHubLimits{Global: 64, PerIP: 64, PerProject: 64}
	})
	streams := make([]*publicSSEStream, 0, 24)
	for index := 0; index < 24; index++ {
		stream, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
		if stream.resp.StatusCode != http.StatusOK {
			t.Fatalf("stream %d status=%d", index, stream.resp.StatusCode)
		}
		streams = append(streams, stream)
	}

	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(3)
	go func() {
		defer wait.Done()
		<-start
		for range 200 {
			f.server.publicHub.RefreshPublicProject(f.project.ID)
		}
	}()
	go func() {
		defer wait.Done()
		<-start
		for _, stream := range streams[:8] {
			stream.cancel()
		}
	}()
	go func() {
		defer wait.Done()
		<-start
		f.server.publicHub.RevokePublicProject(f.project.ID)
	}()
	close(start)
	wait.Wait()

	for index, stream := range streams {
		select {
		case <-stream.done:
		case <-time.After(5 * time.Second):
			t.Fatalf("stream %d did not terminate after revoke", index)
		}
	}
	// A revoked stream may have delivered already-dequeued refreshes, but its
	// last line, if any, is access_revoked and nothing follows it.
	for index, stream := range streams[8:] {
		var last string
		for line := range stream.lines {
			if last == publicSSERevokedLine {
				t.Fatalf("stream %d delivered %q after access_revoked", index, line)
			}
			last = line
		}
		if last != "" && last != publicSSERevokedLine && last != publicSSERefreshLine {
			t.Fatalf("stream %d unexpected line %q", index, last)
		}
	}
	waitForPublicCondition(t, "all counters released", func() bool { return f.server.publicHub.counts().Global == 0 })
}

func TestPublicSSEPhase3ExistingContractsUnchanged(t *testing.T) {
	f := newPublicSSEFixture(t)
	member, _ := openPublicSSE(t, f.ownerClient, f.ts.URL+"/api/board/"+f.project.Slug+"/events", nil)
	if member.resp.StatusCode != http.StatusOK || member.resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("member stream status=%d cache=%q", member.resp.StatusCode, member.resp.Header.Get("Cache-Control"))
	}
	localID := f.createTodo(t, f.project.Slug, "Member visible")
	line, open := member.next(t)
	if !open || !strings.HasPrefix(line, "data: ") {
		t.Fatalf("member line = %q", line)
	}
	var frame map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame); err != nil {
		t.Fatalf("member frame: %v", err)
	}
	if frame["type"] != "refresh_needed" || frame["reason"] != "todo_created" || frame["projectId"] != float64(f.project.ID) {
		t.Fatalf("member refresh frame changed: %v", frame)
	}
	if _, leaked := frame["publicProjectionChanged"]; leaked {
		t.Fatalf("internal marker on private wire: %v", frame)
	}

	resp, body := publicHTTP(t, f.ts.Client(), http.MethodGet, f.ts.URL+"/api/public/board/"+f.project.Slug+"/todos/"+strconv.FormatInt(localID, 10), nil)
	if resp.StatusCode != http.StatusOK || bytes.Contains(body, []byte("ownerIdentity")) {
		t.Fatalf("public JSON detail status=%d body=%s", resp.StatusCode, body)
	}
	assertPublicHeaders(t, resp)
}
