package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/eventbus"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store.New(sqlDB, nil)
}

type collectingConsumer struct {
	events []eventbus.Event
}

func (c *collectingConsumer) OnEvent(_ context.Context, e eventbus.Event) {
	c.events = append(c.events, e)
}

func newTestServerWithCollector(t *testing.T) (*Server, *store.Store, *collectingConsumer) {
	t.Helper()
	st := newTestStore(t)
	collector := &collectingConsumer{}
	hub := NewHub(defaultSubscriberBuffer)
	bridge := newSSEBridge(hub)
	fanout := eventbus.NewFanout(bridge, collector)
	srv := &Server{
		store:  st,
		hub:    hub,
		sink:   hub,
		fanout: fanout,
		mode:   "full",
	}
	st.SetTodoAssignedPublisher(srv.PublishTodoAssigned)
	return srv, st, collector
}

// setupAuthenticatedProject creates a bootstrap user and a project owned by
// that user. Returns the user-scoped context, user, and project.
func setupAuthenticatedProject(t *testing.T, st *store.Store) (context.Context, store.User, store.Project) {
	t.Helper()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "a@b.com", "pass1234A!", "Test")
	if err != nil {
		t.Fatalf("bootstrap user: %v", err)
	}
	userCtx := store.WithUserID(ctx, user.ID)
	p, err := st.CreateProject(userCtx, "test")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return userCtx, user, p
}

// --- Test 1: create todo without assignee => one refresh_needed with reason todo_created ---

func TestEventbus_CreateWithoutAssignee_SingleRefresh(t *testing.T) {
	srv, st, _ := newTestServerWithCollector(t)

	// No users => auth disabled => no user context needed
	p, err := st.CreateProject(context.Background(), "test")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	hubCh, unsub := srv.hub.Subscribe(p.ID)
	defer unsub()

	todo, err := st.CreateTodo(context.Background(), p.ID, store.CreateTodoInput{
		Title:     "no assignee",
		ColumnKey: store.DefaultColumnBacklog,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}
	if todo.AssignmentChanged {
		t.Fatalf("expected AssignmentChanged=false for todo without assignee")
	}

	if !todo.AssignmentChanged {
		srv.emitRefreshNeeded(context.Background(), p.ID, "todo_created")
	}

	select {
	case msg := <-hubCh:
		var ev struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(msg, &ev); err != nil {
			t.Fatalf("unmarshal hub msg: %v", err)
		}
		if ev.Type != "refresh_needed" {
			t.Fatalf("expected SSE type refresh_needed, got %s", ev.Type)
		}
		if ev.Reason != "todo_created" {
			t.Fatalf("expected SSE reason todo_created, got %s", ev.Reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for hub event")
	}

	select {
	case msg := <-hubCh:
		t.Fatalf("unexpected second hub event: %s", string(msg))
	case <-time.After(100 * time.Millisecond):
	}
}

// --- Test 2: create todo with assignee => refresh_needed + todo.assigned on hub ---

func TestEventbus_CreateWithAssignee_SingleRefresh(t *testing.T) {
	srv, st, collector := newTestServerWithCollector(t)

	userCtx, user, p := setupAuthenticatedProject(t, st)

	// Subscribe to the hub to count actual SSE emissions
	hubCh, unsub := srv.hub.Subscribe(p.ID)
	defer unsub()

	assignee := user.ID
	todo, err := st.CreateTodo(userCtx, p.ID, store.CreateTodoInput{
		Title:          "with assignee",
		ColumnKey:      store.DefaultColumnBacklog,
		AssigneeUserID: &assignee,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}
	if !todo.AssignmentChanged {
		t.Fatalf("expected AssignmentChanged=true for todo with assignee")
	}

	// Simulate handler gating: skip emitRefreshNeeded when AssignmentChanged
	if !todo.AssignmentChanged {
		srv.emitRefreshNeeded(userCtx, p.ID, "todo_created")
	}

	// The store publisher fired todo.assigned through the fanout.
	// The SSE bridge translated it to a hub.Emit(refresh_needed).
	// No additional board.refresh_needed should have been published through fanout.
	assignedEvents := filterEvents(collector.events, "todo.assigned")
	fanoutRefreshEvents := filterEvents(collector.events, "board.refresh_needed")

	if len(assignedEvents) != 1 {
		t.Fatalf("expected 1 todo.assigned event, got %d", len(assignedEvents))
	}
	if len(fanoutRefreshEvents) != 0 {
		t.Fatalf("expected 0 board.refresh_needed through fanout (gated), got %d", len(fanoutRefreshEvents))
	}

	select {
	case msg := <-hubCh:
		var ev struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(msg, &ev); err != nil {
			t.Fatalf("unmarshal hub msg: %v", err)
		}
		if ev.Type != "refresh_needed" {
			t.Fatalf("expected SSE type refresh_needed, got %s", ev.Type)
		}
		if ev.Reason != "todo_assigned" {
			t.Fatalf("expected SSE reason todo_assigned, got %s", ev.Reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for hub event")
	}

	select {
	case msg := <-hubCh:
		var ev struct {
			Type        string `json:"type"`
			ProjectID   int64  `json:"projectId"`
			ProjectSlug string `json:"projectSlug"`
			Payload     struct {
				TodoID     int64  `json:"todoId"`
				Title      string `json:"title"`
				AssigneeID int64  `json:"assigneeId"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(msg, &ev); err != nil {
			t.Fatalf("unmarshal hub msg 2: %v", err)
		}
		if ev.Type != "todo.assigned" {
			t.Fatalf("expected SSE type todo.assigned, got %s", ev.Type)
		}
		if ev.ProjectSlug != p.Slug {
			t.Fatalf("expected projectSlug %q on wire, got %q", p.Slug, ev.ProjectSlug)
		}
		if ev.Payload.Title != "with assignee" {
			t.Fatalf("expected title in payload, got %q", ev.Payload.Title)
		}
		if ev.Payload.AssigneeID != user.ID {
			t.Fatalf("assigneeId mismatch")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for todo.assigned hub event")
	}

	select {
	case msg := <-hubCh:
		t.Fatalf("unexpected third hub event: %s", string(msg))
	case <-time.After(100 * time.Millisecond):
	}
}

// --- Test 3: update todo without assignee change => one refresh_needed with reason todo_updated ---

func TestEventbus_UpdateWithoutAssigneeChange_SingleRefresh(t *testing.T) {
	srv, st, _ := newTestServerWithCollector(t)

	userCtx, _, p := setupAuthenticatedProject(t, st)

	todo, err := st.CreateTodo(userCtx, p.ID, store.CreateTodoInput{
		Title:     "update me",
		ColumnKey: store.DefaultColumnBacklog,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}

	hubCh, unsub := srv.hub.Subscribe(p.ID)
	defer unsub()
	drainHub(hubCh)

	updated, err := st.UpdateTodo(userCtx, todo.ID, store.UpdateTodoInput{
		Title: "updated title",
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("update todo: %v", err)
	}
	if updated.AssignmentChanged {
		t.Fatalf("expected AssignmentChanged=false")
	}

	if !updated.AssignmentChanged {
		srv.emitRefreshNeeded(userCtx, p.ID, "todo_updated")
	}

	select {
	case msg := <-hubCh:
		var ev struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(msg, &ev); err != nil {
			t.Fatalf("unmarshal hub msg: %v", err)
		}
		if ev.Type != "refresh_needed" {
			t.Fatalf("expected SSE type refresh_needed, got %s", ev.Type)
		}
		if ev.Reason != "todo_updated" {
			t.Fatalf("expected SSE reason todo_updated, got %s", ev.Reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for hub event")
	}

	select {
	case msg := <-hubCh:
		t.Fatalf("unexpected second hub event: %s", string(msg))
	case <-time.After(100 * time.Millisecond):
	}
}

// --- Test 4: update todo with assignee change => refresh_needed + todo.assigned (no duplicate refresh via fanout) ---

func TestEventbus_UpdateWithAssigneeChange_SingleRefresh(t *testing.T) {
	srv, st, collector := newTestServerWithCollector(t)

	userCtx, user, p := setupAuthenticatedProject(t, st)

	todo, err := st.CreateTodo(userCtx, p.ID, store.CreateTodoInput{
		Title:     "assign me",
		ColumnKey: store.DefaultColumnBacklog,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}

	// Subscribe to hub and reset collector after create
	hubCh, unsub := srv.hub.Subscribe(p.ID)
	defer unsub()
	// Drain any hub event from create
	drainHub(hubCh)
	collector.events = nil

	assignee := user.ID
	updated, err := st.UpdateTodo(userCtx, todo.ID, store.UpdateTodoInput{
		Title:          "assigned",
		AssigneeUserID: &assignee,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("update todo: %v", err)
	}
	if !updated.AssignmentChanged {
		t.Fatalf("expected AssignmentChanged=true")
	}

	// Handler gating
	if !updated.AssignmentChanged {
		srv.emitRefreshNeeded(userCtx, p.ID, "todo_updated")
	}

	assignedEvents := filterEvents(collector.events, "todo.assigned")
	fanoutRefreshEvents := filterEvents(collector.events, "board.refresh_needed")

	if len(assignedEvents) != 1 {
		t.Fatalf("expected 1 todo.assigned, got %d", len(assignedEvents))
	}
	if len(fanoutRefreshEvents) != 0 {
		t.Fatalf("expected 0 board.refresh_needed through fanout (gated), got %d; double-refresh detected", len(fanoutRefreshEvents))
	}

	select {
	case msg := <-hubCh:
		var ev struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(msg, &ev); err != nil {
			t.Fatalf("unmarshal hub msg: %v", err)
		}
		if ev.Type != "refresh_needed" {
			t.Fatalf("expected SSE type refresh_needed, got %s", ev.Type)
		}
		if ev.Reason != "todo_assigned" {
			t.Fatalf("expected SSE reason todo_assigned, got %s", ev.Reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for hub event")
	}

	select {
	case msg := <-hubCh:
		var ev struct {
			Type        string `json:"type"`
			ProjectSlug string `json:"projectSlug"`
			Payload     struct {
				TodoID     int64  `json:"todoId"`
				Title      string `json:"title"`
				AssigneeID int64  `json:"assigneeId"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(msg, &ev); err != nil {
			t.Fatalf("unmarshal hub msg 2: %v", err)
		}
		if ev.Type != "todo.assigned" {
			t.Fatalf("expected todo.assigned, got %s", ev.Type)
		}
		if ev.ProjectSlug != p.Slug {
			t.Fatalf("expected projectSlug %q on wire, got %q", p.Slug, ev.ProjectSlug)
		}
		if ev.Payload.Title != "assigned" {
			t.Fatalf("expected title assigned, got %q", ev.Payload.Title)
		}
		if ev.Payload.AssigneeID != user.ID {
			t.Fatalf("assigneeId mismatch")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for todo.assigned hub event")
	}

	select {
	case msg := <-hubCh:
		t.Fatalf("unexpected third hub event: %s", string(msg))
	case <-time.After(100 * time.Millisecond):
	}
}

// --- Test 5: webhook signature header present and correctly formatted ---

func TestWebhookWorker_SignatureHeader(t *testing.T) {
	secret := "test-secret-key"
	body := []byte(`{"id":"evt-1","type":"todo.assigned"}`)

	var mu sync.Mutex
	var gotSig, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		gotSig = r.Header.Get("X-Scrumboy-Signature")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	q := newWebhookQueue(log.New(io.Discard, "", 0))
	w := newWebhookWorker(q, log.New(io.Discard, "", 0))

	q.Enqueue(webhookDelivery{
		WebhookID: 1,
		URL:       ts.URL,
		Secret:    &secret,
		EventID:   "evt-1",
		EventType: "todo.assigned",
		Timestamp: time.Now(),
		Body:      body,
	})

	w.flush()

	mu.Lock()
	defer mu.Unlock()

	if gotBody != string(body) {
		t.Fatalf("body mismatch: got %q, want %q", gotBody, string(body))
	}
	if !strings.HasPrefix(gotSig, "sha256=") {
		t.Fatalf("signature missing sha256= prefix: %q", gotSig)
	}
	hexPart := strings.TrimPrefix(gotSig, "sha256=")
	if _, err := hex.DecodeString(hexPart); err != nil {
		t.Fatalf("signature hex portion invalid: %v", err)
	}
	if len(hexPart) != 64 {
		t.Fatalf("expected 64-char hex for SHA256, got %d", len(hexPart))
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSig != expected {
		t.Fatalf("signature mismatch: got %s, want %s", gotSig, expected)
	}
}

// --- Test 6: webhook enqueue still happens when request context is canceled ---

func TestWebhookDispatcher_UsesBackgroundContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	collector := &collectingConsumer{}
	hub := NewHub(4)
	bridge := newSSEBridge(hub)
	fanout := eventbus.NewFanout(bridge, collector)

	e := eventbus.Event{
		Type:      "todo.assigned",
		ProjectID: 1,
		Payload:   json.RawMessage(`{}`),
	}

	cancel()

	_ = fanout.Publish(ctx, e)

	if len(collector.events) == 0 {
		t.Fatalf("expected event delivery even with cancelled context")
	}
	if collector.events[0].Type != "todo.assigned" {
		t.Fatalf("expected todo.assigned, got %s", collector.events[0].Type)
	}
}

// --- Test 7: Server.Close cancels the worker ---

func TestServer_CloseStopsWorker(t *testing.T) {
	workerCtx, workerCancel := context.WithCancel(context.Background())
	srv := &Server{
		webhookCancel: workerCancel,
	}

	select {
	case <-workerCtx.Done():
		t.Fatalf("worker context should not be cancelled before Close()")
	default:
	}

	srv.Close()

	select {
	case <-workerCtx.Done():
	default:
		t.Fatalf("worker context should be cancelled after Close()")
	}
}

// --- Helpers ---

func drainHub(ch <-chan []byte) {
	for {
		select {
		case <-ch:
		case <-time.After(50 * time.Millisecond):
			return
		}
	}
}

func filterEvents(events []eventbus.Event, eventType string) []eventbus.Event {
	var out []eventbus.Event
	for _, e := range events {
		if e.Type == eventType {
			out = append(out, e)
		}
	}
	return out
}

func assertReason(t *testing.T, e eventbus.Event, expectedReason string) {
	t.Helper()
	var p struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if p.Reason != expectedReason {
		t.Fatalf("expected reason %q, got %q", expectedReason, p.Reason)
	}
}
