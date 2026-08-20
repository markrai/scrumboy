package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"scrumboy/internal/application/refresh"
	todoapp "scrumboy/internal/application/todo"
	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

func refreshEventWithEntity(t *testing.T, actorID int64, reason string, entity refresh.Entity) eventbus.Event {
	t.Helper()
	payload, err := json.Marshal(refreshNeededPayload{
		Reason:      reason,
		ActorUserID: actorID,
		LocalID:     entity.LocalID,
		Title:       entity.Title,
		Name:        entity.Name,
	})
	if err != nil {
		t.Fatal(err)
	}
	return eventbus.Event{Type: "board.refresh_needed", ProjectID: 7, Payload: payload}
}

func TestEmailNotifier_RefreshNeeded_EnrichedCardCopy(t *testing.T) {
	st := newEmailNotifyFake()
	st.members[0].Name = "Alice"
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())

	n.handle(context.Background(), refreshEventWithEntity(t, 1, "todo_updated", refresh.Entity{
		LocalID: 42, Title: "Fix login",
	}))

	got := q.Drain()
	if len(got) != 2 {
		t.Fatalf("expected emails to non-actor members, got %+v", got)
	}
	for _, m := range got {
		if m.Subject != `Roadmap: card updated — #42 Fix login` {
			t.Fatalf("unexpected subject: %q", m.Subject)
		}
		wantBody := "Alice updated card #42 Fix login in Roadmap.\n\nView the board:\nhttps://scrumboy.example.com/roadmap\n"
		if m.Body != wantBody {
			t.Fatalf("expected exact actor-led body, got %q", m.Body)
		}
	}
}

func TestEmailNotifier_RefreshNeeded_PartialCardFallsBackToGeneric(t *testing.T) {
	st := newEmailNotifyFake()
	st.members[0].Name = "Alice"
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())

	n.handle(context.Background(), refreshEventWithEntity(t, 1, "todo_updated", refresh.Entity{
		LocalID: 42,
	}))

	got := q.Drain()
	if len(got) != 2 {
		t.Fatalf("expected emails to non-actor members, got %+v", got)
	}
	for _, m := range got {
		if m.Subject != "Roadmap: card updated" {
			t.Fatalf("unexpected generic subject: %q", m.Subject)
		}
		if !strings.Contains(m.Body, "Alice updated a card in Roadmap.") {
			t.Fatalf("expected generic actor-led body, got %q", m.Body)
		}
		if strings.Contains(m.Body, "#42") || strings.Contains(m.Subject, "—") {
			t.Fatalf("partial entity leaked into copy: subject=%q body=%q", m.Subject, m.Body)
		}
	}
}

func TestEmailNotifier_RefreshNeeded_EnrichedSprintPassiveCopy(t *testing.T) {
	st := newEmailNotifyFake()
	st.members[0].Name = ""
	st.users[1] = store.User{ID: 1, Email: "actor@example.com", Name: ""}
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())

	n.handle(context.Background(), refreshEventWithEntity(t, 1, "sprint_closed", refresh.Entity{
		Name: "Sprint 12",
	}))

	got := q.Drain()
	if len(got) != 2 {
		t.Fatalf("expected emails to non-actor members, got %+v", got)
	}
	for _, m := range got {
		if m.Subject != `Roadmap: sprint closed — Sprint 12` {
			t.Fatalf("unexpected subject: %q", m.Subject)
		}
		wantBody := "Sprint 12 was closed in Roadmap.\n\nView the board:\nhttps://scrumboy.example.com/roadmap\n"
		if m.Body != wantBody {
			t.Fatalf("expected exact enriched passive body, got %q", m.Body)
		}
	}
}

func TestEmailNotifier_RefreshNeeded_EnrichedColumnCopy(t *testing.T) {
	st := newEmailNotifyFake()
	st.members[0].Name = "Alice"
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())

	n.handle(context.Background(), refreshEventWithEntity(t, 1, "workflow_column_added", refresh.Entity{
		Name: "Review",
	}))

	got := q.Drain()
	if len(got) != 2 {
		t.Fatalf("expected emails to non-actor members, got %+v", got)
	}
	wantBody := "Alice added column Review in Roadmap.\n\nView the board:\nhttps://scrumboy.example.com/roadmap\n"
	for _, m := range got {
		if m.Subject != `Roadmap: column added — Review` {
			t.Fatalf("unexpected subject: %q", m.Subject)
		}
		if m.Body != wantBody {
			t.Fatalf("expected exact column body, got %q", m.Body)
		}
	}
}

func TestEmailNotifier_RefreshNeeded_EnrichedTagCopy(t *testing.T) {
	st := newEmailNotifyFake()
	st.members[0].Name = "Alice"
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())

	n.handle(context.Background(), refreshEventWithEntity(t, 1, "tag_deleted", refresh.Entity{
		Name: "blocked",
	}))

	got := q.Drain()
	if len(got) != 2 {
		t.Fatalf("expected emails to non-actor members, got %+v", got)
	}
	wantBody := "Alice deleted tag blocked in Roadmap.\n\nView the board:\nhttps://scrumboy.example.com/roadmap\n"
	for _, m := range got {
		if m.Subject != `Roadmap: tag deleted — blocked` {
			t.Fatalf("unexpected subject: %q", m.Subject)
		}
		if m.Body != wantBody {
			t.Fatalf("expected exact tag body, got %q", m.Body)
		}
	}
}

func TestEmailNotifier_AssignmentActivityUsesCardIdentity(t *testing.T) {
	st := newEmailNotifyFake()
	st.members[0].Name = "Alice"
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())
	assigneeID := int64(2)

	n.handle(context.Background(), assignedEvent(t, 1, &assigneeID, "todo_created"))

	got := q.Drain()
	byRecipient := map[string]mailDelivery{}
	for _, m := range got {
		byRecipient[m.To] = m
	}
	activity := byRecipient["member@example.com"]
	if activity.Subject != `Roadmap: card created — #4 Ship it` {
		t.Fatalf("unexpected activity subject: %q", activity.Subject)
	}
	if !strings.Contains(activity.Body, `Alice created card #4 Ship it in Roadmap.`) {
		t.Fatalf("expected assignment activity to reuse title/localId, got %q", activity.Body)
	}
}

func TestEmailNotifier_CreatorCardActivityUsesPassiveEnrichedCopy(t *testing.T) {
	n := newEmailNotifier(newEmailNotifyFake(), newMailQueue(discardLogger()), "https://scrumboy.example.com", true, discardLogger())
	subject, body, ok := n.renderCreatorEmail(todoapp.AuthorizedCreatorNotification{
		ProjectName:    "Roadmap",
		ProjectSlug:    "roadmap",
		LocalID:        42,
		Title:          "Fix login",
		ActivityReason: todoapp.RefreshReasonTodoUpdated,
	}, emailCategoryCardActivity)
	if !ok {
		t.Fatal("expected creator card-activity render to succeed")
	}
	if subject != `Roadmap: card updated — #42 Fix login` {
		t.Fatalf("unexpected subject: %q", subject)
	}
	if !strings.Contains(body, `Card #42 Fix login was updated in Roadmap.`) {
		t.Fatalf("expected enriched passive creator copy, got %q", body)
	}
}

func TestEnrichActivityCopy_NormalizedEnglish(t *testing.T) {
	tests := []struct {
		reason     string
		actor      string
		entity     refresh.Entity
		wantAction string
		wantSuffix string
	}{
		{"todo_created", "Alice", refresh.Entity{LocalID: 42, Title: "Fix login"}, "Alice created card #42 Fix login", "#42 Fix login"},
		{"todo_updated", "", refresh.Entity{LocalID: 42, Title: "Fix login"}, "Card #42 Fix login was updated", "#42 Fix login"},
		{"todo_moved", "Alice", refresh.Entity{LocalID: 42, Title: "Fix login"}, "Alice moved card #42 Fix login", "#42 Fix login"},
		{"todo_deleted", "Alice", refresh.Entity{LocalID: 42, Title: "Fix login"}, "Alice deleted card #42 Fix login", "#42 Fix login"},
		{"todo_links_updated", "Alice", refresh.Entity{LocalID: 42, Title: "Fix login"}, "Alice updated links on card #42 Fix login", "#42 Fix login"},
		{"todo_links_updated", "", refresh.Entity{LocalID: 42, Title: "Fix login"}, "Links on card #42 Fix login were updated", "#42 Fix login"},
		{"sprint_created", "Alice", refresh.Entity{Name: "Sprint 12"}, "Alice created Sprint 12", "Sprint 12"},
		{"sprint_updated", "", refresh.Entity{Name: "Sprint 12"}, "Sprint 12 was updated", "Sprint 12"},
		{"sprint_deleted", "Alice", refresh.Entity{Name: "Sprint 12"}, "Alice deleted Sprint 12", "Sprint 12"},
		{"sprint_closed", "Alice", refresh.Entity{Name: "Sprint 12"}, "Alice closed Sprint 12", "Sprint 12"},
		{"workflow_column_added", "Alice", refresh.Entity{Name: "Review"}, "Alice added column Review", "Review"},
		{"workflow_column_updated", "", refresh.Entity{Name: "Review"}, "Column Review was updated", "Review"},
		{"tag_color_updated", "Alice", refresh.Entity{Name: "blocked"}, "Alice changed the color of tag blocked", "blocked"},
		{"tag_color_updated", "", refresh.Entity{Name: "blocked"}, "The color of tag blocked was changed", "blocked"},
		{"tag_deleted", "Alice", refresh.Entity{Name: "blocked"}, "Alice deleted tag blocked", "blocked"},
	}
	for _, tt := range tests {
		action, suffix, ok := enrichActivityCopy(tt.reason, tt.actor, tt.entity)
		if !ok {
			t.Fatalf("%s actor=%q: expected enrichment", tt.reason, tt.actor)
		}
		if action != tt.wantAction || suffix != tt.wantSuffix {
			t.Fatalf("%s actor=%q: action=%q suffix=%q, want %q / %q",
				tt.reason, tt.actor, action, suffix, tt.wantAction, tt.wantSuffix)
		}
		if strings.Contains(action, `"`) {
			t.Fatalf("%s leaked quotes: %q", tt.reason, action)
		}
	}
}

func TestEnrichActivityCopy_DoesNotEnrichGenericPublishPaths(t *testing.T) {
	if _, _, ok := enrichActivityCopy("sprint_activated", "Alice", refresh.Entity{Name: "Sprint 12"}); ok {
		t.Fatal("sprint_activated must stay generic even when a name is present")
	}
	if _, _, ok := enrichActivityCopy("workflow_column_deleted", "Alice", refresh.Entity{Name: "Review"}); ok {
		t.Fatal("workflow_column_deleted must stay generic even when a name is present")
	}
}

func TestRefreshReasonInfo_ColumnAndTagColorVocabulary(t *testing.T) {
	column := refreshReasonInfo["workflow_column_added"]
	if column.subject != "column added" || column.actorLed != "added a column" || column.passive != "A column was added" {
		t.Fatalf("column added phrases: %+v", column)
	}
	updated := refreshReasonInfo["workflow_column_updated"]
	if updated.subject != "column updated" || updated.actorLed != "updated a column" {
		t.Fatalf("column updated phrases: %+v", updated)
	}
	deleted := refreshReasonInfo["workflow_column_deleted"]
	if deleted.subject != "column deleted" || deleted.actorLed != "deleted a column" {
		t.Fatalf("column deleted phrases: %+v", deleted)
	}
	tagColor := refreshReasonInfo["tag_color_updated"]
	if tagColor.subject != "tag color changed" || tagColor.actorLed != "changed a tag color" || tagColor.passive != "A tag color was changed" {
		t.Fatalf("tag color phrases: %+v", tagColor)
	}
}

func TestEmailNotifier_AssignmentBodyUsesSingleSentenceIdentity(t *testing.T) {
	st := newEmailNotifyFake()
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())
	assigneeID := int64(2)

	n.handle(context.Background(), assignedEvent(t, 1, &assigneeID, "todo_created"))

	got := q.Drain()
	byRecipient := map[string]mailDelivery{}
	for _, m := range got {
		byRecipient[m.To] = m
	}
	assigned := byRecipient["assignee@example.com"]
	if assigned.Subject != "Assigned to you: Ship it" {
		t.Fatalf("unexpected assignment subject: %q", assigned.Subject)
	}
	want := "Card #4 Ship it was assigned to you in Roadmap.\n\nView the board:\nhttps://scrumboy.example.com/roadmap\n"
	if assigned.Body != want {
		t.Fatalf("assignment body = %q, want %q", assigned.Body, want)
	}
}

func TestEmailNotifier_AssignmentBodyFallsBackWhenIdentityInvalid(t *testing.T) {
	st := newEmailNotifyFake()
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())
	assigneeID := int64(2)
	payload, err := json.Marshal(eventbus.TodoAssignedPayload{
		ProjectID: 7, TodoID: 11, LocalID: 0, Title: "Ship it", Reason: "todo_assigned",
		ActivityReason: "todo_created", ActorUserID: 1, ToAssigneeUID: &assigneeID,
	})
	if err != nil {
		t.Fatal(err)
	}

	n.handle(context.Background(), eventbus.Event{Type: "todo.assigned", ProjectID: 7, Payload: payload})

	got := q.Drain()
	byRecipient := map[string]mailDelivery{}
	for _, m := range got {
		byRecipient[m.To] = m
	}
	assigned := byRecipient["assignee@example.com"]
	if !strings.Contains(assigned.Body, "A card was assigned to you in Roadmap.") {
		t.Fatalf("expected generic assignment body, got %q", assigned.Body)
	}
	if strings.Contains(assigned.Body, "#") {
		t.Fatalf("partial identity leaked into assignment body: %q", assigned.Body)
	}
}

func TestEmailNotifier_CreatorOpenedBodyUsesSingleSentenceIdentity(t *testing.T) {
	n := newEmailNotifier(newEmailNotifyFake(), newMailQueue(discardLogger()), "https://scrumboy.example.com", true, discardLogger())
	subject, body, ok := n.renderCreatorEmail(todoapp.AuthorizedCreatorNotification{
		ProjectName:    "Roadmap",
		ProjectSlug:    "roadmap",
		LocalID:        42,
		Title:          "Fix login",
		ActivityReason: todoapp.RefreshReasonTodoUpdated,
	}, emailCategoryCreatedByMe)
	if !ok {
		t.Fatal("expected createdByMe render to succeed")
	}
	if subject != "A card you opened was updated: Fix login" {
		t.Fatalf("unexpected subject: %q", subject)
	}
	want := "Card #42 Fix login was updated in Roadmap.\n\nView the board:\nhttps://scrumboy.example.com/roadmap\n"
	if body != want {
		t.Fatalf("createdByMe body = %q, want %q", body, want)
	}
}

func TestEmailNotifier_CreatorOpenedBodyFallsBackWhenIdentityInvalid(t *testing.T) {
	n := newEmailNotifier(newEmailNotifyFake(), newMailQueue(discardLogger()), "https://scrumboy.example.com", true, discardLogger())
	_, body, ok := n.renderCreatorEmail(todoapp.AuthorizedCreatorNotification{
		ProjectName:    "Roadmap",
		ProjectSlug:    "roadmap",
		Title:          "Fix login",
		ActivityReason: todoapp.RefreshReasonTodoMoved,
	}, emailCategoryCreatedByMe)
	if !ok {
		t.Fatal("expected createdByMe render to succeed")
	}
	if !strings.Contains(body, "A card you opened was moved in Roadmap.") {
		t.Fatalf("expected generic createdByMe body, got %q", body)
	}
	if strings.Contains(body, "#") {
		t.Fatalf("partial identity leaked into createdByMe body: %q", body)
	}
}

type capturingEventConsumer struct {
	events []eventbus.Event
}

func (c *capturingEventConsumer) OnEvent(_ context.Context, e eventbus.Event) {
	c.events = append(c.events, e)
}

func TestEmitTagDeletedRefreshFansSameEntityAndDedupesProjects(t *testing.T) {
	cap := &capturingEventConsumer{}
	s := &Server{fanout: eventbus.NewFanout(cap)}
	s.emitTagDeletedRefresh(context.Background(), 7, []int64{7, 9, 9, 11}, refresh.Entity{Name: "blocked"})
	if len(cap.events) != 3 {
		t.Fatalf("events=%d, want 3 unique projects", len(cap.events))
	}
	seen := map[int64]bool{}
	for _, e := range cap.events {
		if e.Type != "board.refresh_needed" {
			t.Fatalf("type=%q", e.Type)
		}
		if seen[e.ProjectID] {
			t.Fatalf("duplicate project %d", e.ProjectID)
		}
		seen[e.ProjectID] = true
		var p refreshNeededPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.Reason != "tag_deleted" || p.Name != "blocked" {
			t.Fatalf("payload=%+v, want tag_deleted name=blocked", p)
		}
	}
	if !seen[7] || !seen[9] || !seen[11] {
		t.Fatalf("projects=%v, want 7,9,11", seen)
	}
}

func TestEmitTagDeletedRefreshIDBasedPassesZeroEntity(t *testing.T) {
	cap := &capturingEventConsumer{}
	s := &Server{fanout: eventbus.NewFanout(cap)}
	s.emitTagDeletedRefresh(context.Background(), 7, []int64{9}, refresh.Entity{})
	if len(cap.events) != 2 {
		t.Fatalf("events=%d, want 2", len(cap.events))
	}
	for _, e := range cap.events {
		var p refreshNeededPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.Name != "" || p.LocalID != 0 || p.Title != "" {
			t.Fatalf("expected zero entity, got %+v", p)
		}
		assertRefreshNeededEntityOmitted(t, e.Payload)
	}
}

func assertRefreshNeededEntityOmitted(t *testing.T, payload []byte) {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"name", "localId", "title"} {
		if _, ok := raw[key]; ok {
			t.Fatalf("%s must be omitted for zero entity, payload=%v", key, raw)
		}
	}
}

func assertRefreshNeededName(t *testing.T, payload []byte, name string) {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["name"] != name {
		t.Fatalf("name=%v, want %q payload=%v", raw["name"], name, raw)
	}
}

func TestSSEBridgeRefreshNeededForwardsReasonOnly(t *testing.T) {
	hub := NewHub(8)
	ch, unsub := hub.Subscribe(7)
	defer unsub()
	bridge := newSSEBridge(hub, nil)
	payload, err := json.Marshal(refreshNeededPayload{
		Reason:      "todo_updated",
		ActorUserID: 1,
		LocalID:     42,
		Title:       "Fix login",
		Name:        "should-not-appear",
	})
	if err != nil {
		t.Fatal(err)
	}
	bridge.OnEvent(context.Background(), eventbus.Event{
		ID:        "evt-1",
		Type:      "board.refresh_needed",
		ProjectID: 7,
		Payload:   payload,
	})
	select {
	case data := <-ch:
		var wire map[string]any
		if err := json.Unmarshal(data, &wire); err != nil {
			t.Fatalf("sse json: %v", err)
		}
		if wire["type"] != "refresh_needed" || wire["reason"] != "todo_updated" {
			t.Fatalf("sse wire = %#v", wire)
		}
		if _, ok := wire["localId"]; ok {
			t.Fatalf("localId leaked onto SSE: %#v", wire)
		}
		if _, ok := wire["title"]; ok {
			t.Fatalf("title leaked onto SSE: %#v", wire)
		}
		if _, ok := wire["name"]; ok {
			t.Fatalf("name leaked onto SSE: %#v", wire)
		}
		if _, ok := wire["actorUserId"]; ok {
			t.Fatalf("actorUserId leaked onto SSE: %#v", wire)
		}
	default:
		t.Fatal("expected SSE event")
	}
}
