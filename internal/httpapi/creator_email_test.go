package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	todoapp "scrumboy/internal/application/todo"
	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

func creatorEmailCandidateEvent(t *testing.T, materialChanged bool) eventbus.Event {
	t.Helper()
	payload, err := json.Marshal(eventbus.TodoCreatorNotificationRecipientAuthorizedPayload{
		ProjectID:             7,
		ProjectSlug:           "stale-slug",
		TodoID:                11,
		LocalID:               4,
		Title:                 "Ship it",
		ActivityReason:        todoapp.RefreshReasonTodoUpdated,
		RecipientUserID:       2,
		ActorUserID:           1,
		MaterialChanged:       materialChanged,
		CardActivityCandidate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return eventbus.Event{
		Type:      eventbus.TodoCreatorNotificationRecipientAuthorizedEventType,
		ProjectID: 7,
		Payload:   payload,
	}
}

func queuedCreatorEmailCandidate(t *testing.T, st *emailNotifyFakeStore) mailDelivery {
	t.Helper()
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://example.test", true, discardLogger())
	n.OnEvent(context.Background(), creatorEmailCandidateEvent(t, true))
	items := drainAfterAsync(q)
	if len(items) != 1 {
		t.Fatalf("queued items = %d, want 1", len(items))
	}
	return items[0]
}

func TestCreatorEmailCandidateQueuesNoRenderedSensitiveData(t *testing.T) {
	st := newEmailNotifyFake()
	st.prefs[2] = store.EmailNotifyPref{V: 2, Enabled: true, CreatedByMe: true}
	delivery := queuedCreatorEmailCandidate(t, st)
	if delivery.Prepare == nil {
		t.Fatal("creator candidate must defer send-time preparation")
	}
	if delivery.To != "" || delivery.Subject != "" || delivery.Body != "" {
		t.Fatalf("candidate leaked rendered mail before send-time authorization: %+v", delivery)
	}
}

func TestCreatorEmailCandidateSemanticNoOpQueuesNothing(t *testing.T) {
	st := newEmailNotifyFake()
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://example.test", true, discardLogger())
	n.OnEvent(context.Background(), creatorEmailCandidateEvent(t, false))
	if got := len(drainAfterAsync(q)); got != 0 {
		t.Fatalf("semantic no-op queued %d creator candidates, want 0", got)
	}
}

func TestCreatorEmailCandidateReauthorizesBeforeQueue(t *testing.T) {
	st := newEmailNotifyFake()
	delete(st.roles, 2)
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://example.test", true, discardLogger())
	n.handleCreatorCandidate(context.Background(), creatorEmailCandidateEvent(t, true))
	if got := len(q.Drain()); got != 0 {
		t.Fatalf("removed creator produced %d queued candidates, want 0", got)
	}
}

func TestCreatorEmailPrepareFreshChecksAndRendering(t *testing.T) {
	st := newEmailNotifyFake()
	st.prefs[2] = store.EmailNotifyPref{V: 2, Enabled: true, CreatedByMe: true, CardActivity: true}
	delivery := queuedCreatorEmailCandidate(t, st)
	st.project = store.Project{ID: 7, Name: "Fresh roadmap", Slug: "fresh-roadmap"}
	user := st.users[2]
	user.Email = "fresh-assignee@example.com"
	st.users[2] = user

	prepared, ok, err := delivery.Prepare(context.Background())
	if err != nil || !ok {
		t.Fatalf("prepare = ok %v err %v, want authorized delivery", ok, err)
	}
	if prepared.To != "fresh-assignee@example.com" {
		t.Fatalf("recipient = %q", prepared.To)
	}
	if !strings.Contains(prepared.Subject, "Ship it") ||
		!strings.Contains(prepared.Body, "Fresh roadmap") ||
		!strings.Contains(prepared.Body, "https://example.test/fresh-roadmap") {
		t.Fatalf("mail did not use committed title and freshly loaded project: %+v", prepared)
	}
	if !strings.Contains(prepared.LogRef, "category=createdByMe") {
		t.Fatalf("log ref = %q, want createdByMe", prepared.LogRef)
	}
}

func TestCreatorEmailPrepareFailsClosedAfterQueue(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*emailNotifyFakeStore)
	}{
		{name: "membership removed", mutate: func(st *emailNotifyFakeStore) { delete(st.roles, 2) }},
		{name: "project deleted or unavailable", mutate: func(st *emailNotifyFakeStore) { st.projectErr = store.ErrNotFound }},
		{name: "temporary project", mutate: func(st *emailNotifyFakeStore) { st.project.ExpiresAt = ptrTimeForCreatorEmail() }},
		{name: "recipient deleted", mutate: func(st *emailNotifyFakeStore) { delete(st.users, 2) }},
		{name: "recipient email missing", mutate: func(st *emailNotifyFakeStore) { user := st.users[2]; user.Email = ""; st.users[2] = user }},
		{name: "preference lookup error", mutate: func(st *emailNotifyFakeStore) { st.prefErrs[2] = store.ErrNotFound }},
		{name: "project identity mismatch", mutate: func(st *emailNotifyFakeStore) { st.project.ID = 8 }},
		{name: "role below viewer", mutate: func(st *emailNotifyFakeStore) { st.roles[2] = "" }},
		{name: "creator preference disabled", mutate: func(st *emailNotifyFakeStore) { st.prefs[2] = store.EmailNotifyPref{V: 2, Enabled: true} }},
		{name: "master disabled", mutate: func(st *emailNotifyFakeStore) { st.prefs[2] = store.EmailNotifyPref{V: 2, CreatedByMe: true} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newEmailNotifyFake()
			st.prefs[2] = store.EmailNotifyPref{V: 2, Enabled: true, CreatedByMe: true}
			delivery := queuedCreatorEmailCandidate(t, st)
			tt.mutate(st)
			prepared, ok, err := delivery.Prepare(context.Background())
			if err != nil || ok || prepared.To != "" || prepared.Subject != "" || prepared.Body != "" {
				t.Fatalf("prepare = %+v ok %v err %v, want fail-closed drop", prepared, ok, err)
			}
		})
	}
}

func ptrTimeForCreatorEmail() *time.Time {
	value := time.Now().UTC().Add(time.Hour)
	return &value
}

func TestCreatorEmailPrepareCancelledContextFailsClosed(t *testing.T) {
	st := newEmailNotifyFake()
	st.prefs[2] = store.EmailNotifyPref{V: 2, Enabled: true, CreatedByMe: true}
	delivery := queuedCreatorEmailCandidate(t, st)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, ok, err := delivery.Prepare(ctx)
	if err != nil || ok {
		t.Fatalf("cancelled prepare = ok %v err %v, want drop", ok, err)
	}
}

func TestCreatorEmailCategoryPrecedenceAndFallback(t *testing.T) {
	recipient := int64(2)
	base := todoapp.AuthorizedCreatorNotification{
		RecipientUserID:       recipient,
		MaterialChanged:       true,
		CardActivityCandidate: true,
	}
	tests := []struct {
		name      string
		pref      store.EmailNotifyPref
		candidate todoapp.AuthorizedCreatorNotification
		want      emailCategory
		ok        bool
	}{
		{name: "assignment wins", pref: store.EmailNotifyPref{Assigned: true, CreatedByMe: true, CardActivity: true}, candidate: func() todoapp.AuthorizedCreatorNotification {
			c := base
			c.AssignmentChanged = true
			c.ToAssigneeUserID = &recipient
			return c
		}(), want: emailCategoryAssigned, ok: true},
		{name: "creator fallback", pref: store.EmailNotifyPref{CreatedByMe: true, CardActivity: true}, candidate: func() todoapp.AuthorizedCreatorNotification {
			c := base
			c.AssignmentChanged = true
			c.ToAssigneeUserID = &recipient
			return c
		}(), want: emailCategoryCreatedByMe, ok: true},
		{name: "activity fallback", pref: store.EmailNotifyPref{CardActivity: true}, candidate: base, want: emailCategoryCardActivity, ok: true},
		{name: "creator wins over activity", pref: store.EmailNotifyPref{CreatedByMe: true, CardActivity: true}, candidate: base, want: emailCategoryCreatedByMe, ok: true},
		{name: "mcp has no activity fallback", pref: store.EmailNotifyPref{CardActivity: true}, candidate: func() todoapp.AuthorizedCreatorNotification { c := base; c.CardActivityCandidate = false; return c }()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := selectCreatorEmailCategory(tt.pref, tt.candidate)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("category = %q ok %v, want %q ok %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestCreatorEmailRetryKeepsSelectedCategory(t *testing.T) {
	st := newEmailNotifyFake()
	st.prefs[2] = store.EmailNotifyPref{V: 2, Enabled: true, CreatedByMe: true, CardActivity: true}
	delivery := queuedCreatorEmailCandidate(t, st)
	first, ok, _ := delivery.Prepare(context.Background())
	if !ok || !strings.Contains(first.LogRef, "category=createdByMe") {
		t.Fatalf("first category = %+v ok %v", first, ok)
	}
	st.prefs[2] = store.EmailNotifyPref{V: 2, Enabled: true, CreatedByMe: true, CardActivity: true, Assigned: true}
	second, ok, _ := delivery.Prepare(context.Background())
	if !ok || !strings.Contains(second.LogRef, "category=createdByMe") {
		t.Fatalf("retry changed category: %+v ok %v", second, ok)
	}
	st.prefs[2] = store.EmailNotifyPref{V: 2, Enabled: true, CardActivity: true, Assigned: true}
	_, ok, _ = delivery.Prepare(context.Background())
	if ok {
		t.Fatal("retry must drop rather than switch category after selected category is disabled")
	}
}

func TestCreatorEmailCardActivityFallbackUsesDebounceButRetryKeepsClaim(t *testing.T) {
	st := newEmailNotifyFake()
	st.prefs[2] = store.EmailNotifyPref{V: 2, Enabled: true, CardActivity: true}
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://example.test", true, discardLogger())

	n.handleCreatorCandidate(context.Background(), creatorEmailCandidateEvent(t, true))
	firstItems := q.Drain()
	if len(firstItems) != 1 {
		t.Fatalf("first creator candidates = %d, want 1", len(firstItems))
	}
	sender := &fakeMailSender{failUntil: 1}
	newMailWorker(q, sender, discardLogger()).deliver(firstItems[0])
	if sender.callCount() != 2 || len(sender.sent) != 1 || !strings.Contains(sender.sent[0].Subject, "card updated") {
		t.Fatalf("same-work SMTP retry was blocked by its debounce claim: calls=%d sent=%+v", sender.callCount(), sender.sent)
	}

	n.handleCreatorCandidate(context.Background(), creatorEmailCandidateEvent(t, true))
	secondItems := q.Drain()
	if len(secondItems) != 1 {
		t.Fatalf("second creator candidates = %d, want 1", len(secondItems))
	}
	second, ok, err := secondItems[0].Prepare(context.Background())
	if err != nil || ok || second.To != "" || second.Subject != "" || second.Body != "" {
		t.Fatalf("second activity fallback bypassed debounce: %+v ok %v err %v", second, ok, err)
	}
}

type creatorEmailUpdateStore struct{ todo store.Todo }

func (s creatorEmailUpdateStore) UpdateTodoByLocalID(context.Context, int64, int64, store.UpdateTodoInput, store.Mode) (store.Todo, error) {
	return s.todo, nil
}

func TestCreatorEmailRESTActivityOverlapQueuesCreatorOnlyThroughCandidate(t *testing.T) {
	st := newEmailNotifyFake()
	st.prefs[2] = store.EmailNotifyPref{V: 2, Enabled: true, CreatedByMe: true, CardActivity: true}
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://example.test", true, discardLogger())
	creatorID := int64(2)
	updated := store.Todo{
		ID: 11, ProjectID: 7, LocalID: 4, Title: "Ship it", CreatedByUserID: &creatorID, MaterialChanged: true,
	}
	service := todoapp.NewUpdateService(todoapp.UpdateServiceDependencies{
		Update:          creatorEmailUpdateStore{todo: updated},
		CreatorRequests: todoapp.CreatorNotificationRequestPublisherFunc(func(context.Context, todoapp.CreatorNotificationRequest) {}),
		Refresh: todoapp.BoardRefreshPublisherFunc(func(ctx context.Context, projectID int64, reason string) {
			payload, _ := json.Marshal(map[string]any{"reason": reason, "actorUserId": int64(1)})
			n.handleRefreshNeeded(ctx, eventbus.Event{Type: "board.refresh_needed", ProjectID: projectID, Payload: payload})
		}),
	})
	_, err := service.Prepare(store.WithUserID(context.Background(), 1), todoapp.ResolvedUpdateTarget{
		ProjectContext: store.ProjectContext{Project: st.project}, Mode: store.ModeFull,
	}).Update(todoapp.UpdateCommand{LocalID: 4})
	if err != nil {
		t.Fatal(err)
	}
	activity := q.Drain()
	if len(activity) != 1 || activity[0].To != "member@example.com" {
		t.Fatalf("ordinary activity deliveries=%+v, want only unrelated member", activity)
	}

	n.OnEvent(context.Background(), creatorEmailCandidateEvent(t, true))
	candidates := drainAfterAsync(q)
	if len(candidates) != 1 || candidates[0].Prepare == nil {
		t.Fatalf("creator candidates=%+v, want exactly one deferred candidate", candidates)
	}
	prepared, ok, err := candidates[0].Prepare(context.Background())
	if err != nil || !ok || !strings.Contains(prepared.LogRef, "category=createdByMe") {
		t.Fatalf("creator delivery=%+v ok=%v err=%v", prepared, ok, err)
	}
}

func TestCreatorEmailMutationFactsSurviveAsyncFanoutBoundary(t *testing.T) {
	t.Run("refresh creator exclusion survives cancellation", func(t *testing.T) {
		st := newEmailNotifyFake()
		st.prefs[2] = store.EmailNotifyPref{V: 2, Enabled: true, CreatedByMe: true, CardActivity: true}
		q := newMailQueue(discardLogger())
		n := newEmailNotifier(st, q, "https://example.test", true, discardLogger())
		creatorID := int64(2)
		updated := store.Todo{
			ID: 11, ProjectID: 7, LocalID: 4, Title: "Ship it", CreatedByUserID: &creatorID, MaterialChanged: true,
		}
		service := todoapp.NewUpdateService(todoapp.UpdateServiceDependencies{
			Update:          creatorEmailUpdateStore{todo: updated},
			CreatorRequests: todoapp.CreatorNotificationRequestPublisherFunc(func(context.Context, todoapp.CreatorNotificationRequest) {}),
			Refresh: todoapp.BoardRefreshPublisherFunc(func(ctx context.Context, projectID int64, reason string) {
				payload, _ := json.Marshal(map[string]any{"reason": reason, "actorUserId": int64(1)})
				n.OnEvent(ctx, eventbus.Event{Type: "board.refresh_needed", ProjectID: projectID, Payload: payload})
			}),
		})
		bound := store.WithUserID(context.Background(), 1)
		cancelled, cancel := context.WithCancel(bound)
		cancel()
		if _, err := service.Prepare(cancelled, todoapp.ResolvedUpdateTarget{
			ProjectContext: store.ProjectContext{Project: st.project}, Mode: store.ModeFull,
		}).Update(todoapp.UpdateCommand{LocalID: 4}); err != nil {
			t.Fatal(err)
		}
		deliveries := drainAfterAsync(q)
		if len(deliveries) != 1 || deliveries[0].To != "member@example.com" {
			t.Fatalf("async refresh deliveries=%+v, want creator excluded and unrelated member retained", deliveries)
		}
	})

	t.Run("assignment creator arbitration survives cancellation", func(t *testing.T) {
		st := newEmailNotifyFake()
		creatorID := int64(2)
		st.prefs[2] = store.EmailNotifyPref{V: 2, Enabled: true, Assigned: true, CreatedByMe: true, CardActivity: true}
		q := newMailQueue(discardLogger())
		n := newEmailNotifier(st, q, "https://example.test", true, discardLogger())
		ctx := withTodoAssignedMutationFacts(context.Background(), store.TodoAssignedMutationFacts{
			CreatedByUserID: &creatorID,
			DurableProject:  true,
		})
		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		n.OnEvent(cancelled, assignedEvent(t, 1, &creatorID, todoapp.RefreshReasonTodoUpdated))
		deliveries := drainAfterAsync(q)
		if len(deliveries) != 1 || deliveries[0].To != "member@example.com" {
			t.Fatalf("async assignment deliveries=%+v, want creator deferred and unrelated member retained", deliveries)
		}
	})
}

func TestCreatorEmailRESTSemanticNoOpSuppressesCreatorCategoryAndFallback(t *testing.T) {
	st := newEmailNotifyFake()
	st.prefs[2] = store.EmailNotifyPref{V: 2, Enabled: true, CreatedByMe: true, CardActivity: true}
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://example.test", true, discardLogger())
	creatorID := int64(2)
	updated := store.Todo{ID: 11, ProjectID: 7, LocalID: 4, Title: "Ship it", CreatedByUserID: &creatorID}
	service := todoapp.NewUpdateService(todoapp.UpdateServiceDependencies{
		Update: creatorEmailUpdateStore{todo: updated},
		CreatorRequests: todoapp.CreatorNotificationRequestPublisherFunc(func(_ context.Context, request todoapp.CreatorNotificationRequest) {
			if request.MaterialChanged {
				t.Fatal("semantic no-op request was marked material")
			}
		}),
		Refresh: todoapp.BoardRefreshPublisherFunc(func(ctx context.Context, projectID int64, reason string) {
			payload, _ := json.Marshal(map[string]any{"reason": reason, "actorUserId": int64(1)})
			n.handleRefreshNeeded(ctx, eventbus.Event{Type: "board.refresh_needed", ProjectID: projectID, Payload: payload})
		}),
	})
	_, err := service.Prepare(store.WithUserID(context.Background(), 1), todoapp.ResolvedUpdateTarget{
		ProjectContext: store.ProjectContext{Project: st.project}, Mode: store.ModeFull,
	}).Update(todoapp.UpdateCommand{LocalID: 4})
	if err != nil {
		t.Fatal(err)
	}
	deliveries := q.Drain()
	if len(deliveries) != 1 || deliveries[0].To != "member@example.com" {
		t.Fatalf("semantic no-op deliveries=%+v, want no creator email and unchanged other-member activity", deliveries)
	}
}

func TestCreatorEmailAssignmentOverlapUsesSingleCreatorCandidate(t *testing.T) {
	st := newEmailNotifyFake()
	creatorID := int64(2)
	st.prefs[2] = store.EmailNotifyPref{V: 2, Enabled: true, Assigned: true, CreatedByMe: true, CardActivity: true}
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://example.test", true, discardLogger())

	assignmentCtx := withTodoAssignedMutationFacts(context.Background(), store.TodoAssignedMutationFacts{
		CreatedByUserID: &creatorID,
		DurableProject:  true,
	})
	n.handleTodoAssigned(assignmentCtx, assignedEvent(t, 1, &creatorID, todoapp.RefreshReasonTodoUpdated))
	activity := q.Drain()
	if len(activity) != 1 || activity[0].To != "member@example.com" {
		t.Fatalf("assignment/activity deliveries=%+v, want no direct creator email", activity)
	}
	event := creatorEmailCandidateEvent(t, true)
	var payload eventbus.TodoCreatorNotificationRecipientAuthorizedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	payload.AssignmentChanged = true
	payload.ToAssigneeUserID = &creatorID
	event.Payload, _ = json.Marshal(payload)
	n.OnEvent(context.Background(), event)
	candidates := drainAfterAsync(q)
	if len(candidates) != 1 {
		t.Fatalf("creator candidates=%+v", candidates)
	}
	prepared, ok, err := candidates[0].Prepare(context.Background())
	if err != nil || !ok || !strings.Contains(prepared.LogRef, "category=assigned") {
		t.Fatalf("precedence result=%+v ok=%v err=%v", prepared, ok, err)
	}
}

func TestCreatorEmailTemporaryProjectDoesNotSwallowAssignment(t *testing.T) {
	st := newEmailNotifyFake()
	creatorID := int64(2)
	st.prefs[2] = store.EmailNotifyPref{V: 2, Enabled: true, Assigned: true, CreatedByMe: true, CardActivity: true}
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://example.test", true, discardLogger())
	assignmentCtx := withTodoAssignedMutationFacts(context.Background(), store.TodoAssignedMutationFacts{
		CreatedByUserID: &creatorID,
		DurableProject:  false,
	})

	n.handleTodoAssigned(assignmentCtx, assignedEvent(t, 1, &creatorID, todoapp.RefreshReasonTodoUpdated))
	deliveries := q.Drain()
	if len(deliveries) != 2 {
		t.Fatalf("temporary-project deliveries=%+v, want assignment plus unrelated-member activity", deliveries)
	}
	byRecipient := map[string]mailDelivery{deliveries[0].To: deliveries[0], deliveries[1].To: deliveries[1]}
	if !strings.Contains(byRecipient["assignee@example.com"].Subject, "Assigned to you") {
		t.Fatalf("temporary-project creator assignment was swallowed: %+v", deliveries)
	}
}

func TestCreatorEmailDeferredAssignmentFailsClosedWhenAuthorizationDisappears(t *testing.T) {
	st := newEmailNotifyFake()
	creatorID := int64(2)
	st.prefs[2] = store.EmailNotifyPref{V: 2, Enabled: true, Assigned: true, CreatedByMe: true, CardActivity: true}
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://example.test", true, discardLogger())
	assignmentCtx := withTodoAssignedMutationFacts(context.Background(), store.TodoAssignedMutationFacts{
		CreatedByUserID: &creatorID,
		DurableProject:  true,
	})

	n.handleTodoAssigned(assignmentCtx, assignedEvent(t, 1, &creatorID, todoapp.RefreshReasonTodoUpdated))
	ordinary := q.Drain()
	if len(ordinary) != 1 || ordinary[0].To != "member@example.com" {
		t.Fatalf("ordinary deliveries=%+v, want creator deferred and unrelated member preserved", ordinary)
	}
	delete(st.roles, creatorID)
	event := creatorEmailCandidateEvent(t, true)
	var payload eventbus.TodoCreatorNotificationRecipientAuthorizedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	payload.AssignmentChanged = true
	payload.ToAssigneeUserID = &creatorID
	event.Payload, _ = json.Marshal(payload)
	n.handleCreatorCandidate(context.Background(), event)
	if got := q.Drain(); len(got) != 0 {
		t.Fatalf("authorization loss produced creator or fallback assignment mail: %+v", got)
	}
}
