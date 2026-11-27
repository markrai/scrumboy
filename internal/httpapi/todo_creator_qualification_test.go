package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

type creatorQualificationEmailConsumer struct {
	notifier *emailNotifier
}

func (c *creatorQualificationEmailConsumer) OnEvent(ctx context.Context, event eventbus.Event) {
	switch event.Type {
	case "todo.assigned":
		c.notifier.handleTodoAssigned(ctx, event)
	case "board.refresh_needed":
		c.notifier.handleRefreshNeeded(ctx, event)
	case eventbus.TodoCreatorNotificationRequestedEventType:
		c.notifier.handleCreatorCandidate(ctx, event)
	}
}

type creatorQualificationCapturedEvent struct {
	event                   eventbus.Event
	assignmentFacts         store.TodoAssignedMutationFacts
	hasAssignmentFacts      bool
	publicProjectionChanged bool
}

type creatorQualificationConsumer struct {
	events []creatorQualificationCapturedEvent
}

func (c *creatorQualificationConsumer) OnEvent(ctx context.Context, event eventbus.Event) {
	facts, hasFacts := todoAssignedMutationFactsFromContext(ctx)
	c.events = append(c.events, creatorQualificationCapturedEvent{
		event:                   event,
		assignmentFacts:         facts,
		hasAssignmentFacts:      hasFacts,
		publicProjectionChanged: publicProjectionChangedFromContext(ctx),
	})
}

type creatorQualificationFixture struct {
	server        *Server
	store         *store.Store
	db            *sql.DB
	test          *httptest.Server
	client        *http.Client
	owner         store.User
	oldCreator    store.User
	newCreator    store.User
	otherMember   store.User
	project       store.Project
	todo          store.Todo
	mailQueue     *mailQueue
	capture       *creatorQualificationConsumer
	projectEvents <-chan []byte
	oldEvents     <-chan []byte
	newEvents     <-chan []byte
	publicEvents  *PublicSubscription
}

func newCreatorQualificationFixture(t *testing.T) *creatorQualificationFixture {
	t.Helper()
	ts, database, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
	})
	srv, ok := ts.Config.Handler.(*Server)
	if !ok {
		cleanup()
		t.Fatalf("test handler type=%T, want *Server", ts.Config.Handler)
	}
	st, ok := srv.store.(*store.Store)
	if !ok {
		cleanup()
		t.Fatalf("test store type=%T, want *store.Store", srv.store)
	}
	t.Cleanup(func() {
		srv.Close(context.Background())
		cleanup()
	})

	client := newCookieClient(t)
	ownerJSON := bootstrapUserClient(t, client, ts.URL, "Qualification Owner", "qualification-owner@example.com", "password123")
	ownerID := int64(ownerJSON["id"].(float64))
	owner, err := st.GetUser(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("load owner: %v", err)
	}
	ownerCtx := store.WithUserID(context.Background(), owner.ID)
	project, err := st.CreateProject(ownerCtx, "Creator qualification")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	oldCreator, err := st.CreateUser(context.Background(), "qualification-old@example.com", "password123", "Old Creator")
	if err != nil {
		t.Fatalf("create old creator: %v", err)
	}
	newCreator, err := st.CreateUser(context.Background(), "qualification-new@example.com", "password123", "New Creator")
	if err != nil {
		t.Fatalf("create new creator: %v", err)
	}
	otherMember, err := st.CreateUser(context.Background(), "qualification-member@example.com", "password123", "Other Member")
	if err != nil {
		t.Fatalf("create other member: %v", err)
	}
	for _, member := range []struct {
		user store.User
		role store.ProjectRole
	}{
		{user: oldCreator, role: store.RoleMaintainer},
		{user: newCreator, role: store.RoleViewer},
		{user: otherMember, role: store.RoleViewer},
	} {
		if err := st.AddProjectMember(ownerCtx, owner.ID, project.ID, member.user.ID, member.role); err != nil {
			t.Fatalf("add %s: %v", member.user.Name, err)
		}
	}
	points := int64(3)
	todo, err := st.CreateTodo(store.WithUserID(context.Background(), oldCreator.ID), project.ID, store.CreateTodoInput{
		Title:            "Creator qualification card",
		Body:             "initial body",
		Tags:             []string{"qualification"},
		ColumnKey:        store.DefaultColumnBacklog,
		EstimationPoints: &points,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}

	queue := newNotificationMailQueue(srv.logger)
	notifier := newEmailNotifier(st, queue, "https://scrumboy.example.com", true, srv.logger)
	capture := &creatorQualificationConsumer{}
	srv.fanout = eventbus.NewFanout(
		newSSEBridge(srv.hub, srv.creatorNotificationAuthorizer),
		newPublicSSEBridge(srv.publicHub),
		&creatorQualificationEmailConsumer{notifier: notifier},
		capture,
	)
	st.SetTodoAssignedPublisher(srv.PublishTodoAssigned)
	st.SetTodoLifecyclePublisher(srv.PublishTodoLifecycle)
	projectEvents, unsubscribeProject := srv.hub.Subscribe(project.ID)
	oldEvents, unsubscribeOld := srv.hub.SubscribeUser(oldCreator.ID)
	newEvents, unsubscribeNew := srv.hub.SubscribeUser(newCreator.ID)
	publicEvents, err := srv.publicHub.Subscribe(project.ID, "creator-qualification")
	if err != nil {
		t.Fatalf("subscribe public events: %v", err)
	}
	t.Cleanup(func() {
		unsubscribeProject()
		unsubscribeOld()
		unsubscribeNew()
		publicEvents.Unsubscribe()
	})

	return &creatorQualificationFixture{
		server: srv, store: st, db: database, test: ts, client: client,
		owner: owner, oldCreator: oldCreator, newCreator: newCreator, otherMember: otherMember,
		project: project, todo: todo, mailQueue: queue, capture: capture,
		projectEvents: projectEvents, oldEvents: oldEvents, newEvents: newEvents, publicEvents: publicEvents,
	}
}

func (f *creatorQualificationFixture) setEmailPreference(t *testing.T, userID int64, pref store.EmailNotifyPref) {
	t.Helper()
	raw, err := json.Marshal(pref)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetUserPreference(context.Background(), userID, "emailNotifications", string(raw)); err != nil {
		t.Fatalf("set email preference for user %d: %v", userID, err)
	}
}

func (f *creatorQualificationFixture) patch(t *testing.T, payload map[string]any) store.Todo {
	t.Helper()
	response, body := doJSON(t, f.client, http.MethodPatch, creatorUpdateRESTURL(f.test.URL, f.project, f.todo), payload, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("creator qualification patch status=%d body=%s", response.StatusCode, body)
	}
	updated, err := f.store.GetTodoByLocalID(store.WithUserID(context.Background(), f.owner.ID), f.project.ID, f.todo.LocalID, store.ModeFull)
	if err != nil {
		t.Fatalf("reload todo: %v", err)
	}
	f.todo = updated
	return updated
}

func (f *creatorQualificationFixture) resetEffects() {
	f.capture.events = nil
	f.mailQueue.Drain()
	drainCreatorQualificationMessages(f.projectEvents)
	drainCreatorQualificationMessages(f.oldEvents)
	drainCreatorQualificationMessages(f.newEvents)
	drainCreatorQualificationPublicEvents(f.publicEvents.Events)
}

func drainCreatorQualificationMessages(events <-chan []byte) [][]byte {
	var messages [][]byte
	for {
		select {
		case message := <-events:
			messages = append(messages, message)
		default:
			return messages
		}
	}
}

func drainCreatorQualificationPublicEvents(events <-chan PublicEvent) []PublicEvent {
	var result []PublicEvent
	for {
		select {
		case event := <-events:
			result = append(result, event)
		default:
			return result
		}
	}
}

func creatorQualificationWireTypes(t *testing.T, events <-chan []byte) []string {
	t.Helper()
	var types []string
	for _, message := range drainCreatorQualificationMessages(events) {
		var wire struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(message, &wire); err != nil {
			t.Fatalf("decode SSE message: %v", err)
		}
		types = append(types, wire.Type)
	}
	return types
}

func creatorQualificationEvents(events []creatorQualificationCapturedEvent, eventType string) []creatorQualificationCapturedEvent {
	var result []creatorQualificationCapturedEvent
	for _, event := range events {
		if event.event.Type == eventType {
			result = append(result, event)
		}
	}
	return result
}

func creatorQualificationPreference(assigned, createdByMe, cardActivity bool) store.EmailNotifyPref {
	pref := store.DefaultEmailNotifyPref()
	pref.Enabled = true
	pref.Assigned = assigned
	pref.CreatedByMe = createdByMe
	pref.CardActivity = cardActivity
	return pref
}

func TestTodoCreatorQualificationCreatorOnlyMutation(t *testing.T) {
	fixture := newCreatorQualificationFixture(t)
	fixture.resetEffects()

	updated := fixture.patch(t, creatorUpdateRESTPayload(fixture.todo, fixture.newCreator.ID))
	if updated.CreatedByUserID == nil || *updated.CreatedByUserID != fixture.newCreator.ID || updated.AssigneeUserID != nil {
		t.Fatalf("persisted todo=%+v", updated)
	}
	if got := creatorUpdateAuditCount(t, fixture.db, fixture.todo.ID); got != 1 {
		t.Fatalf("creator audit count=%d want=1", got)
	}
	if got := creatorQualificationEvents(fixture.capture.events, eventbus.TodoCreatorNotificationRequestedEventType); len(got) != 0 {
		t.Fatalf("creator-only correction published notification request: %+v", got)
	}
	if got := creatorQualificationEvents(fixture.capture.events, eventbus.TodoCreatorNotificationRecipientAuthorizedEventType); len(got) != 0 {
		t.Fatalf("creator-only correction published authorized recipient: %+v", got)
	}
	lifecycle := creatorQualificationEvents(fixture.capture.events, eventbus.TodoUpdatedEventType)
	if len(lifecycle) != 1 {
		t.Fatalf("todo.updated events=%+v, want one", lifecycle)
	}
	var lifecyclePayload eventbus.TodoLifecyclePayload
	if err := json.Unmarshal(lifecycle[0].event.Payload, &lifecyclePayload); err != nil {
		t.Fatal(err)
	}
	if len(lifecyclePayload.ChangedFields) != 1 || lifecyclePayload.ChangedFields[0] != "creator" {
		t.Fatalf("lifecycle changedFields=%v want [creator]", lifecyclePayload.ChangedFields)
	}
	refreshes := creatorQualificationEvents(fixture.capture.events, "board.refresh_needed")
	if len(refreshes) != 1 || refreshes[0].publicProjectionChanged {
		t.Fatalf("private refreshes=%+v, want one non-public refresh", refreshes)
	}
	if got := creatorQualificationWireTypes(t, fixture.projectEvents); fmt.Sprint(got) != "[refresh_needed]" {
		t.Fatalf("project SSE types=%v", got)
	}
	if got := creatorQualificationWireTypes(t, fixture.oldEvents); len(got) != 0 {
		t.Fatalf("old creator private SSE types=%v", got)
	}
	if got := creatorQualificationWireTypes(t, fixture.newEvents); len(got) != 0 {
		t.Fatalf("new creator private SSE types=%v", got)
	}
	if got := drainCreatorQualificationPublicEvents(fixture.publicEvents.Events); len(got) != 0 {
		t.Fatalf("creator-only correction published public invalidation: %v", got)
	}
	if deliveries := fixture.mailQueue.Drain(); len(deliveries) != 0 {
		t.Fatalf("creator-only correction queued mail: %+v", deliveries)
	}
}

func TestTodoCreatorQualificationCreatorAndAssignmentOverlap(t *testing.T) {
	fixture := newCreatorQualificationFixture(t)
	fixture.setEmailPreference(t, fixture.newCreator.ID, creatorQualificationPreference(true, true, true))
	fixture.setEmailPreference(t, fixture.otherMember.ID, creatorQualificationPreference(false, false, true))
	fixture.resetEffects()

	payload := creatorUpdateRESTPayload(fixture.todo, fixture.newCreator.ID)
	payload["assigneeUserId"] = fixture.newCreator.ID
	updated := fixture.patch(t, payload)
	if updated.CreatedByUserID == nil || *updated.CreatedByUserID != fixture.newCreator.ID || updated.AssigneeUserID == nil || *updated.AssigneeUserID != fixture.newCreator.ID {
		t.Fatalf("persisted combined update=%+v", updated)
	}
	if got := creatorUpdateAuditCount(t, fixture.db, fixture.todo.ID); got != 1 {
		t.Fatalf("creator audit count=%d want=1", got)
	}
	if got := countTodoAssignmentRows(t, fixture.db, fixture.todo.ID); got != 1 {
		t.Fatalf("assignment row count=%d want=1", got)
	}
	assigned := creatorQualificationEvents(fixture.capture.events, "todo.assigned")
	if len(assigned) != 1 || !assigned[0].hasAssignmentFacts || assigned[0].assignmentFacts.CreatedByUserID == nil || *assigned[0].assignmentFacts.CreatedByUserID != fixture.newCreator.ID || !assigned[0].assignmentFacts.DurableProject || assigned[0].publicProjectionChanged {
		t.Fatalf("assignment publication facts=%+v", assigned)
	}
	requests := creatorQualificationEvents(fixture.capture.events, eventbus.TodoCreatorNotificationRequestedEventType)
	if len(requests) != 1 {
		t.Fatalf("creator requests=%+v, want one", requests)
	}
	var request eventbus.TodoCreatorNotificationRequestedPayload
	if err := json.Unmarshal(requests[0].event.Payload, &request); err != nil {
		t.Fatal(err)
	}
	if request.CreatedByUserID != fixture.newCreator.ID || !request.AssignmentChanged || request.ToAssigneeUserID == nil || *request.ToAssigneeUserID != fixture.newCreator.ID {
		t.Fatalf("creator request=%+v", request)
	}
	if got := creatorQualificationEvents(fixture.capture.events, eventbus.TodoCreatorNotificationRecipientAuthorizedEventType); len(got) != 1 {
		t.Fatalf("authorized creator events=%+v, want one", got)
	}

	deliveries := fixture.mailQueue.Drain()
	if len(deliveries) != 2 {
		t.Fatalf("queued deliveries=%+v, want one creator candidate and one ordinary activity", deliveries)
	}
	var candidate, ordinary *mailDelivery
	for index := range deliveries {
		if deliveries[index].Prepare != nil {
			candidate = &deliveries[index]
		} else {
			ordinary = &deliveries[index]
		}
	}
	if candidate == nil || ordinary == nil {
		t.Fatalf("queued deliveries=%+v", deliveries)
	}
	if ordinary.To != fixture.otherMember.Email || !strings.Contains(ordinary.LogRef, "category=cardActivity") {
		t.Fatalf("ordinary member activity=%+v", *ordinary)
	}
	prepared, ok, err := candidate.Prepare(context.Background())
	if err != nil || !ok {
		t.Fatalf("prepare creator overlap delivery ok=%v err=%v", ok, err)
	}
	if prepared.To != fixture.newCreator.Email || !strings.Contains(prepared.LogRef, "category=assigned") || strings.Contains(prepared.LogRef, "createdByMe") {
		t.Fatalf("creator overlap precedence=%+v", prepared)
	}
	if got := creatorQualificationWireTypes(t, fixture.projectEvents); fmt.Sprint(got) != "[refresh_needed todo.assigned]" {
		t.Fatalf("project SSE types=%v", got)
	}
	if got := creatorQualificationWireTypes(t, fixture.newEvents); fmt.Sprint(got) != "[todo.assigned todo.creator_activity]" {
		t.Fatalf("new creator private SSE types=%v", got)
	}
	if got := creatorQualificationWireTypes(t, fixture.oldEvents); len(got) != 0 {
		t.Fatalf("old creator private SSE types=%v", got)
	}
	if got := drainCreatorQualificationPublicEvents(fixture.publicEvents.Events); len(got) != 0 {
		t.Fatalf("creator and assignment correction published public invalidation: %v", got)
	}
}

func TestTodoCreatorQualificationPendingCandidateTransitions(t *testing.T) {
	fixture := newCreatorQualificationFixture(t)
	fixture.setEmailPreference(t, fixture.oldCreator.ID, creatorQualificationPreference(false, true, false))
	fixture.setEmailPreference(t, fixture.newCreator.ID, creatorQualificationPreference(false, true, false))
	fixture.resetEffects()

	initialPayload := todoUpdateRESTPayload(fixture.todo.Title, "first material update", fixture.todo.Tags, fixture.todo.EstimationPoints, fixture.todo.AssigneeUserID)
	fixture.patch(t, initialPayload)
	requests := creatorQualificationEvents(fixture.capture.events, eventbus.TodoCreatorNotificationRequestedEventType)
	if len(requests) != 1 {
		t.Fatalf("initial creator requests=%+v, want one", requests)
	}
	var initialRequest eventbus.TodoCreatorNotificationRequestedPayload
	if err := json.Unmarshal(requests[0].event.Payload, &initialRequest); err != nil {
		t.Fatal(err)
	}
	if initialRequest.CreatedByUserID != fixture.oldCreator.ID {
		t.Fatalf("initial creator request=%+v", initialRequest)
	}
	queued := fixture.mailQueue.Drain()
	if len(queued) != 1 || queued[0].Prepare == nil {
		t.Fatalf("initial queued deliveries=%+v, want one deferred creator candidate", queued)
	}
	pending := queued[0]
	fixture.resetEffects()

	fixture.patch(t, creatorUpdateRESTPayload(fixture.todo, fixture.newCreator.ID))
	if got := creatorQualificationEvents(fixture.capture.events, eventbus.TodoCreatorNotificationRequestedEventType); len(got) != 0 {
		t.Fatalf("creator correction published notification request: %+v", got)
	}
	if deliveries := fixture.mailQueue.Drain(); len(deliveries) != 0 {
		t.Fatalf("creator correction queued mail: %+v", deliveries)
	}
	fixture.setEmailPreference(t, fixture.oldCreator.ID, creatorQualificationPreference(false, true, true))
	prepared, ok, err := pending.Prepare(context.Background())
	if err != nil || !ok {
		t.Fatalf("prepare stale creator candidate card fallback ok=%v err=%v", ok, err)
	}
	if prepared.To != fixture.oldCreator.Email || prepared.To == fixture.newCreator.Email || !strings.Contains(prepared.LogRef, "category=cardActivity") || strings.Contains(prepared.LogRef, "createdByMe") {
		t.Fatalf("stale creator candidate was not independently arbitrated: %+v", prepared)
	}
	ownerCtx := store.WithUserID(context.Background(), fixture.owner.ID)
	if err := fixture.store.RemoveProjectMember(ownerCtx, fixture.owner.ID, fixture.project.ID, fixture.oldCreator.ID); err != nil {
		t.Fatalf("remove old creator membership: %v", err)
	}
	if delivery, ok, err := pending.Prepare(context.Background()); err != nil || ok {
		t.Fatalf("removed creator candidate prepare=(%+v, %v, %v), want denied", delivery, ok, err)
	}

	fixture.resetEffects()
	subsequentPayload := todoUpdateRESTPayload(fixture.todo.Title, "subsequent material update", fixture.todo.Tags, fixture.todo.EstimationPoints, fixture.todo.AssigneeUserID)
	fixture.patch(t, subsequentPayload)
	requests = creatorQualificationEvents(fixture.capture.events, eventbus.TodoCreatorNotificationRequestedEventType)
	if len(requests) != 1 {
		t.Fatalf("subsequent creator requests=%+v, want one", requests)
	}
	var subsequentRequest eventbus.TodoCreatorNotificationRequestedPayload
	if err := json.Unmarshal(requests[0].event.Payload, &subsequentRequest); err != nil {
		t.Fatal(err)
	}
	if subsequentRequest.CreatedByUserID != fixture.newCreator.ID {
		t.Fatalf("subsequent creator request=%+v", subsequentRequest)
	}
	queued = fixture.mailQueue.Drain()
	if len(queued) != 1 || queued[0].Prepare == nil {
		t.Fatalf("subsequent queued deliveries=%+v, want one new-creator candidate", queued)
	}
	prepared, ok, err = queued[0].Prepare(context.Background())
	if err != nil || !ok {
		t.Fatalf("prepare new creator delivery ok=%v err=%v", ok, err)
	}
	if prepared.To != fixture.newCreator.Email || !strings.Contains(prepared.LogRef, "category=createdByMe") {
		t.Fatalf("new creator delivery=%+v", prepared)
	}
	if got := creatorQualificationWireTypes(t, fixture.oldEvents); len(got) != 0 {
		t.Fatalf("removed old creator received private SSE types=%v", got)
	}
	if got := creatorQualificationWireTypes(t, fixture.newEvents); fmt.Sprint(got) != "[todo.creator_activity]" {
		t.Fatalf("new creator private SSE types=%v", got)
	}
}
