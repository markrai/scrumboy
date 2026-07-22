package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

type emailNotifyFakeStore struct {
	mu           sync.Mutex
	project      store.Project
	members      []store.ProjectMember
	users        map[int64]store.User
	prefs        map[int64]store.EmailNotifyPref
	projectErr   error
	membersErr   error
	prefErrs     map[int64]error
	projectCalls int
	memberCalls  int
}

func (s *emailNotifyFakeStore) GetEmailNotifyPref(_ context.Context, userID int64) (store.EmailNotifyPref, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prefErrs[userID]; err != nil {
		return store.EmailNotifyPref{}, err
	}
	return s.prefs[userID], nil
}

func (s *emailNotifyFakeStore) GetProject(_ context.Context, _ int64) (store.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projectCalls++
	if s.projectErr != nil {
		return store.Project{}, s.projectErr
	}
	return s.project, nil
}

func (s *emailNotifyFakeStore) GetUser(_ context.Context, userID int64) (store.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return u, nil
}

func (s *emailNotifyFakeStore) ListProjectMembers(_ context.Context, _ int64, _ int64) ([]store.ProjectMember, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memberCalls++
	if s.membersErr != nil {
		return nil, s.membersErr
	}
	return append([]store.ProjectMember(nil), s.members...), nil
}

func enabledEmailNotifyPref() store.EmailNotifyPref {
	p := store.DefaultEmailNotifyPref()
	p.Enabled = true
	p.CardActivity = true
	p.SprintActivity = true
	p.ProjectActivity = true
	return p
}

func newEmailNotifyFake() *emailNotifyFakeStore {
	pref := enabledEmailNotifyPref()
	return &emailNotifyFakeStore{
		project: store.Project{ID: 7, Name: "Roadmap", Slug: "roadmap"},
		members: []store.ProjectMember{
			{UserID: 1, Email: "actor@example.com"},
			{UserID: 2, Email: "assignee@example.com"},
			{UserID: 3, Email: "member@example.com"},
		},
		users: map[int64]store.User{
			1: {ID: 1, Email: "actor@example.com"},
			2: {ID: 2, Email: "assignee@example.com"},
			3: {ID: 3, Email: "member@example.com"},
		},
		prefs:    map[int64]store.EmailNotifyPref{1: pref, 2: pref, 3: pref},
		prefErrs: make(map[int64]error),
	}
}

func assignedEvent(t *testing.T, actorID int64, to *int64, reason string) eventbus.Event {
	t.Helper()
	payload, err := json.Marshal(eventbus.TodoAssignedPayload{
		ProjectID: 7, TodoID: 11, LocalID: 4, Title: "Ship it", Reason: "todo_assigned", ActivityReason: reason,
		ActorUserID: actorID, ToAssigneeUID: to,
	})
	if err != nil {
		t.Fatal(err)
	}
	return eventbus.Event{Type: "todo.assigned", ProjectID: 7, Payload: payload}
}

func refreshEvent(t *testing.T, actorID int64, reason string) eventbus.Event {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"reason": reason, "actorUserId": actorID})
	if err != nil {
		t.Fatal(err)
	}
	return eventbus.Event{Type: "board.refresh_needed", ProjectID: 7, Payload: payload}
}

func TestEmailNotifier_AssignmentAndActivityAreIndependent(t *testing.T) {
	st := newEmailNotifyFake()
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())
	assigneeID := int64(2)

	n.handle(context.Background(), assignedEvent(t, 1, &assigneeID, "todo_created"))

	got := q.Drain()
	if len(got) != 2 {
		t.Fatalf("expected assignment and card-activity deliveries, got %+v", got)
	}
	byRecipient := map[string]mailDelivery{got[0].To: got[0], got[1].To: got[1]}
	if !strings.Contains(byRecipient["assignee@example.com"].Subject, "Assigned to you") {
		t.Fatalf("expected assignment category for assignee, got %+v", got)
	}
	if !strings.Contains(byRecipient["member@example.com"].Subject, "activity update") {
		t.Fatalf("expected card activity for other member, got %+v", got)
	}
	if _, ok := byRecipient["actor@example.com"]; ok {
		t.Fatalf("actor received a self-notification: %+v", got)
	}
}

func TestEmailNotifier_UnassignmentStillProcessesActivity(t *testing.T) {
	st := newEmailNotifyFake()
	st.members = st.members[:2]
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())

	n.handle(context.Background(), assignedEvent(t, 1, nil, "todo_updated"))

	got := q.Drain()
	if len(got) != 1 || got[0].To != "assignee@example.com" || !strings.Contains(got[0].Subject, "activity update") {
		t.Fatalf("expected one card-activity delivery after unassignment, got %+v", got)
	}
}

func TestEmailNotifier_DebounceAllowsAlternatingActors(t *testing.T) {
	st := newEmailNotifyFake()
	st.members = st.members[:2]
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())

	n.handle(context.Background(), refreshEvent(t, 1, "todo_created"))
	n.handle(context.Background(), refreshEvent(t, 2, "todo_updated"))

	got := q.Drain()
	if len(got) != 2 || got[0].To != "assignee@example.com" || got[1].To != "actor@example.com" {
		t.Fatalf("expected both alternating actors to receive the other's event, got %+v", got)
	}
}

func TestEmailNotifier_DebounceDoesNotConsumeFailedOrIneligibleAttempts(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*emailNotifyFakeStore)
		repair  func(*emailNotifyFakeStore)
	}{
		{
			name:    "project lookup",
			prepare: func(st *emailNotifyFakeStore) { st.projectErr = errors.New("project failed") },
			repair:  func(st *emailNotifyFakeStore) { st.projectErr = nil },
		},
		{
			name:    "member lookup",
			prepare: func(st *emailNotifyFakeStore) { st.membersErr = errors.New("members failed") },
			repair:  func(st *emailNotifyFakeStore) { st.membersErr = nil },
		},
		{
			name:    "preference lookup",
			prepare: func(st *emailNotifyFakeStore) { st.prefErrs[2] = errors.New("preference failed") },
			repair:  func(st *emailNotifyFakeStore) { delete(st.prefErrs, 2) },
		},
		{
			name:    "no eligible recipient",
			prepare: func(st *emailNotifyFakeStore) { st.prefs[2] = store.DefaultEmailNotifyPref() },
			repair:  func(st *emailNotifyFakeStore) { st.prefs[2] = enabledEmailNotifyPref() },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := newEmailNotifyFake()
			st.members = st.members[:2]
			q := newMailQueue(discardLogger())
			n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())
			tc.prepare(st)
			n.handle(context.Background(), refreshEvent(t, 1, "todo_created"))
			if got := q.Drain(); len(got) != 0 {
				t.Fatalf("expected first attempt not to enqueue, got %+v", got)
			}
			tc.repair(st)
			n.handle(context.Background(), refreshEvent(t, 1, "todo_created"))
			if got := q.Drain(); len(got) != 1 || got[0].To != "assignee@example.com" {
				t.Fatalf("expected immediate valid retry, got %+v", got)
			}
		})
	}
}

func TestEmailNotifier_DebounceDoesNotConsumeQueueRejection(t *testing.T) {
	st := newEmailNotifyFake()
	st.members = st.members[:2]
	q := newMailQueueWithCapacity(discardLogger(), 1)
	n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())
	if !q.Enqueue(mailDelivery{To: "occupied@example.com"}) {
		t.Fatal("expected queue prefill to succeed")
	}

	n.handle(context.Background(), refreshEvent(t, 1, "todo_created"))
	q.Drain()
	n.handle(context.Background(), refreshEvent(t, 1, "todo_created"))

	got := q.Drain()
	if len(got) != 1 || got[0].To != "assignee@example.com" {
		t.Fatalf("expected retry after queue rejection, got %+v", got)
	}
}

func TestEmailNotifier_DebounceSuppressesRepeatedRecipientConcurrently(t *testing.T) {
	st := newEmailNotifyFake()
	st.members = st.members[:2]
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())
	e := refreshEvent(t, 1, "todo_created")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n.handle(context.Background(), e)
		}()
	}
	wg.Wait()

	if got := q.Drain(); len(got) != 1 || got[0].To != "assignee@example.com" {
		t.Fatalf("expected one delivery for a concurrent burst, got %+v", got)
	}
}

func TestEmailNotifier_ProjectDeletionUsesSnapshotOnly(t *testing.T) {
	st := newEmailNotifyFake()
	optedOut := store.DefaultEmailNotifyPref()
	st.prefs[2] = optedOut
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())
	n.handleProjectDeleted(context.Background(), store.DeletedProjectSnapshot{
		ProjectID: 7, Name: "Roadmap", MemberUserIDs: []int64{1, 2, 3},
	}, 1)

	got := q.Drain()
	if len(got) != 1 || got[0].To != "member@example.com" {
		t.Fatalf("expected only opted-in non-actor deletion recipient, got %+v", got)
	}
	if strings.Contains(got[0].Body, "http") {
		t.Fatalf("deletion mail included a dead action link: %q", got[0].Body)
	}
	if st.projectCalls != 0 || st.memberCalls != 0 {
		t.Fatalf("post-delete notifier queried deleted project state: project=%d members=%d", st.projectCalls, st.memberCalls)
	}
}
