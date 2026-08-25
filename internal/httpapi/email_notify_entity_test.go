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
		FromName:    entity.FromName,
		ToName:      entity.ToName,
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
		wantBody := "Card updated\n\nUpdated by: Alice\nCard: #42 Fix login\nProject: Roadmap\n\nView card:\nhttps://scrumboy.example.com/roadmap/t/42\n"
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
		wantBody := "Card updated\n\nUpdated by: Alice\nProject: Roadmap\n\nView project:\nhttps://scrumboy.example.com/roadmap\n"
		if m.Body != wantBody {
			t.Fatalf("expected exact generic structured body, got %q", m.Body)
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
		wantBody := "Sprint closed\n\nSprint: Sprint 12\nProject: Roadmap\n\nView project:\nhttps://scrumboy.example.com/roadmap\n"
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
	wantBody := "Column added\n\nAdded by: Alice\nColumn: Review\nProject: Roadmap\n\nView project:\nhttps://scrumboy.example.com/roadmap\n"
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
	wantBody := "Tag deleted\n\nDeleted by: Alice\nTag: blocked\nProject: Roadmap\n\nView project:\nhttps://scrumboy.example.com/roadmap\n"
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
	if activity.Body != "Card created\n\nCreated by: Alice\nCard: #4 Ship it\nProject: Roadmap\n\nView card:\nhttps://scrumboy.example.com/roadmap/t/4\n" {
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
	}, emailCategoryCardActivity, "")
	if !ok {
		t.Fatal("expected creator card-activity render to succeed")
	}
	if subject != `Roadmap: card updated — #42 Fix login` {
		t.Fatalf("unexpected subject: %q", subject)
	}
	if body != "Card updated\n\nCard: #42 Fix login\nProject: Roadmap\n\nView card:\nhttps://scrumboy.example.com/roadmap/t/42\n" {
		t.Fatalf("expected enriched passive creator copy, got %q", body)
	}
}

func TestActivityEntity_DomainLabels(t *testing.T) {
	tests := []struct {
		reason     string
		entity     refresh.Entity
		wantLabel  string
		wantValue  string
		wantSuffix string
	}{
		{"todo_created", refresh.Entity{LocalID: 42, Title: "Fix login"}, "Card", "#42 Fix login", "#42 Fix login"},
		{"todo_updated", refresh.Entity{LocalID: 42, Title: "Fix login"}, "Card", "#42 Fix login", "#42 Fix login"},
		{"todo_moved", refresh.Entity{LocalID: 42, Title: "Fix login"}, "Card", "#42 Fix login", "#42 Fix login"},
		{"todo_deleted", refresh.Entity{LocalID: 42, Title: "Fix login"}, "Card", "#42 Fix login", "#42 Fix login"},
		{"todo_links_updated", refresh.Entity{LocalID: 42, Title: "Fix login"}, "Card", "#42 Fix login", "#42 Fix login"},
		{"sprint_created", refresh.Entity{Name: "Sprint 12"}, "Sprint", "Sprint 12", "Sprint 12"},
		{"sprint_updated", refresh.Entity{Name: "Sprint 12"}, "Sprint", "Sprint 12", "Sprint 12"},
		{"sprint_deleted", refresh.Entity{Name: "Sprint 12"}, "Sprint", "Sprint 12", "Sprint 12"},
		{"sprint_closed", refresh.Entity{Name: "Sprint 12"}, "Sprint", "Sprint 12", "Sprint 12"},
		{"workflow_column_added", refresh.Entity{Name: "Review"}, "Column", "Review", "Review"},
		{"workflow_column_updated", refresh.Entity{Name: "Review"}, "Column", "Review", "Review"},
		{"tag_color_updated", refresh.Entity{Name: "blocked"}, "Tag", "blocked", "blocked"},
		{"tag_deleted", refresh.Entity{Name: "blocked"}, "Tag", "blocked", "blocked"},
	}
	for _, tt := range tests {
		label, value, suffix, ok := activityEntity(tt.reason, tt.entity)
		if !ok {
			t.Fatalf("%s: expected enrichment", tt.reason)
		}
		if label != tt.wantLabel || value != tt.wantValue || suffix != tt.wantSuffix {
			t.Fatalf("%s: label=%q value=%q suffix=%q, want %q / %q / %q",
				tt.reason, label, value, suffix, tt.wantLabel, tt.wantValue, tt.wantSuffix)
		}
	}
}

func TestEmailNotifier_RefreshNeeded_MovedCardFullyEnriched(t *testing.T) {
	st := newEmailNotifyFake()
	st.project.Name = "Scrumboy"
	st.project.Slug = "scrumboy"
	st.members[0].Name = "Mark Rai"
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())

	n.handle(context.Background(), refreshEventWithEntity(t, 1, todoapp.RefreshReasonTodoMoved, refresh.Entity{
		LocalID: 332, Title: "Make mobile save bottom longer", FromName: "Testing", ToName: "Done",
	}))

	got := q.Drain()
	if len(got) != 2 {
		t.Fatalf("expected emails to non-actor members, got %+v", got)
	}
	wantSubject := "Scrumboy: card moved — #332 Make mobile save bottom longer"
	wantBody := "Card moved\n\nMoved by: Mark Rai\nCard: #332 Make mobile save bottom longer\nProject: Scrumboy\nStatus: Testing → Done\n\nView card:\nhttps://scrumboy.example.com/scrumboy/t/332\n"
	for _, delivery := range got {
		if delivery.Subject != wantSubject || delivery.Body != wantBody {
			t.Fatalf("moved-card delivery = subject %q body %q, want %q / %q", delivery.Subject, delivery.Body, wantSubject, wantBody)
		}
	}
	if st.projectCalls != 1 || st.memberCalls != 1 || st.userCalls != 0 {
		t.Fatalf("unexpected notifier reads: project=%d members=%d users=%d", st.projectCalls, st.memberCalls, st.userCalls)
	}
}

func TestEmailNotifier_RefreshNeeded_MovedCardMissingTransitionOmitsStatus(t *testing.T) {
	st := newEmailNotifyFake()
	st.members[0].Name = "Alice"
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())

	n.handle(context.Background(), refreshEventWithEntity(t, 1, todoapp.RefreshReasonTodoMoved, refresh.Entity{
		LocalID: 42, Title: "Fix login", FromName: "Testing",
	}))

	got := q.Drain()
	if len(got) != 2 {
		t.Fatalf("expected emails to non-actor members, got %+v", got)
	}
	wantBody := "Card moved\n\nMoved by: Alice\nCard: #42 Fix login\nProject: Roadmap\n\nView card:\nhttps://scrumboy.example.com/roadmap/t/42\n"
	for _, delivery := range got {
		if delivery.Body != wantBody {
			t.Fatalf("missing-transition body = %q, want %q", delivery.Body, wantBody)
		}
		if strings.Contains(delivery.Body, "Status:") || strings.Contains(delivery.Body, "→") {
			t.Fatalf("missing transition emitted malformed status: %q", delivery.Body)
		}
	}
}

func TestEmailNotifier_RefreshNeeded_SameColumnReorderOmitsStatus(t *testing.T) {
	st := newEmailNotifyFake()
	st.members[0].Name = "Alice"
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())

	n.handle(context.Background(), refreshEventWithEntity(t, 1, todoapp.RefreshReasonTodoMoved, refresh.Entity{
		LocalID: 42, Title: "Fix login", FromName: " Testing ", ToName: "Testing",
	}))

	got := q.Drain()
	if len(got) != 2 {
		t.Fatalf("expected emails to non-actor members, got %+v", got)
	}
	wantBody := "Card moved\n\nMoved by: Alice\nCard: #42 Fix login\nProject: Roadmap\n\nView card:\nhttps://scrumboy.example.com/roadmap/t/42\n"
	for _, delivery := range got {
		if delivery.Body != wantBody {
			t.Fatalf("same-column reorder body = %q, want %q", delivery.Body, wantBody)
		}
		if strings.Contains(delivery.Body, "Status:") || strings.Contains(delivery.Body, "Testing → Testing") {
			t.Fatalf("same-column reorder emitted status: %q", delivery.Body)
		}
	}
}

func TestEmailNotifier_RefreshNeeded_DeletedCardUsesProjectLink(t *testing.T) {
	st := newEmailNotifyFake()
	st.members[0].Name = "Alice"
	q := newMailQueue(discardLogger())
	n := newEmailNotifier(st, q, "https://scrumboy.example.com", true, discardLogger())

	n.handle(context.Background(), refreshEventWithEntity(t, 1, "todo_deleted", refresh.Entity{
		LocalID: 42, Title: "Fix login",
	}))

	got := q.Drain()
	if len(got) != 2 {
		t.Fatalf("expected emails to non-actor members, got %+v", got)
	}
	wantBody := "Card deleted\n\nDeleted by: Alice\nCard: #42 Fix login\nProject: Roadmap\n\nView project:\nhttps://scrumboy.example.com/roadmap\n"
	for _, delivery := range got {
		if delivery.Body != wantBody {
			t.Fatalf("deleted-card body = %q, want %q", delivery.Body, wantBody)
		}
		if strings.Contains(delivery.Body, "/t/42") || strings.Contains(delivery.Body, "View card:") {
			t.Fatalf("deleted-card body contains a dead card link: %q", delivery.Body)
		}
	}
}

func TestActivityEntity_DoesNotEnrichGenericPublishPaths(t *testing.T) {
	if _, _, _, ok := activityEntity("sprint_activated", refresh.Entity{Name: "Sprint 12"}); ok {
		t.Fatal("sprint_activated must stay generic even when a name is present")
	}
	if _, _, _, ok := activityEntity("workflow_column_deleted", refresh.Entity{Name: "Review"}); ok {
		t.Fatal("workflow_column_deleted must stay generic even when a name is present")
	}
}

func TestRefreshReasonInfo_ColumnAndTagColorVocabulary(t *testing.T) {
	column := refreshReasonInfo["workflow_column_added"]
	if column.subject != "column added" || column.actorLabel != "Added by" {
		t.Fatalf("column added phrases: %+v", column)
	}
	updated := refreshReasonInfo["workflow_column_updated"]
	if updated.subject != "column updated" || updated.actorLabel != "Updated by" {
		t.Fatalf("column updated phrases: %+v", updated)
	}
	deleted := refreshReasonInfo["workflow_column_deleted"]
	if deleted.subject != "column deleted" || deleted.actorLabel != "Deleted by" {
		t.Fatalf("column deleted phrases: %+v", deleted)
	}
	tagColor := refreshReasonInfo["tag_color_updated"]
	if tagColor.subject != "tag color changed" || tagColor.actorLabel != "Changed by" {
		t.Fatalf("tag color phrases: %+v", tagColor)
	}
}

func TestEmailNotifier_AssignmentBodyUsesSingleSentenceIdentity(t *testing.T) {
	st := newEmailNotifyFake()
	assignee := st.users[2]
	assignee.Name = "Taylor"
	st.users[2] = assignee
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
	want := "Card assigned\n\nAssigned to: Taylor\nCard: #4 Ship it\nProject: Roadmap\n\nView card:\nhttps://scrumboy.example.com/roadmap/t/4\n"
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
	want := "Card assigned\n\nProject: Roadmap\n\nView project:\nhttps://scrumboy.example.com/roadmap\n"
	if assigned.Body != want {
		t.Fatalf("expected generic assignment body %q, got %q", want, assigned.Body)
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
	}, emailCategoryCreatedByMe, "")
	if !ok {
		t.Fatal("expected createdByMe render to succeed")
	}
	if subject != "A card you opened was updated: Fix login" {
		t.Fatalf("unexpected subject: %q", subject)
	}
	want := "Card updated\n\nCard: #42 Fix login\nProject: Roadmap\n\nView card:\nhttps://scrumboy.example.com/roadmap/t/42\n"
	if body != want {
		t.Fatalf("createdByMe body = %q, want %q", body, want)
	}
}

func TestEmailNotifier_CreatorOpenedMovedBodyIncludesExactStatus(t *testing.T) {
	n := newEmailNotifier(newEmailNotifyFake(), newMailQueue(discardLogger()), "https://scrumboy.example.com", true, discardLogger())
	subject, body, ok := n.renderCreatorEmail(todoapp.AuthorizedCreatorNotification{
		ProjectName:    "Roadmap",
		ProjectSlug:    "roadmap",
		LocalID:        42,
		Title:          "Fix login",
		ActivityReason: todoapp.RefreshReasonTodoMoved,
		FromName:       " Testing ",
		ToName:         " Done ",
	}, emailCategoryCreatedByMe, "")
	if !ok {
		t.Fatal("expected createdByMe moved render to succeed")
	}
	if subject != "A card you opened was moved: Fix login" {
		t.Fatalf("unexpected subject: %q", subject)
	}
	want := "Card moved\n\nCard: #42 Fix login\nProject: Roadmap\nStatus: Testing → Done\n\nView card:\nhttps://scrumboy.example.com/roadmap/t/42\n"
	if body != want {
		t.Fatalf("createdByMe moved body = %q, want %q", body, want)
	}
}

func TestEmailNotifier_CreatorOpenedMovedBodyOmitsIncompleteStatus(t *testing.T) {
	n := newEmailNotifier(newEmailNotifyFake(), newMailQueue(discardLogger()), "https://scrumboy.example.com", true, discardLogger())
	want := "Card moved\n\nCard: #42 Fix login\nProject: Roadmap\n\nView card:\nhttps://scrumboy.example.com/roadmap/t/42\n"
	for _, transition := range []struct {
		name     string
		fromName string
		toName   string
	}{
		{name: "missing"},
		{name: "missing destination", fromName: "Testing"},
		{name: "missing source", toName: "Done"},
	} {
		t.Run(transition.name, func(t *testing.T) {
			_, body, ok := n.renderCreatorEmail(todoapp.AuthorizedCreatorNotification{
				ProjectName:    "Roadmap",
				ProjectSlug:    "roadmap",
				LocalID:        42,
				Title:          "Fix login",
				ActivityReason: todoapp.RefreshReasonTodoMoved,
				FromName:       transition.fromName,
				ToName:         transition.toName,
			}, emailCategoryCreatedByMe, "")
			if !ok || body != want {
				t.Fatalf("incomplete transition body = %q ok=%v, want %q", body, ok, want)
			}
		})
	}
}

func TestEmailNotifier_CreatorOpenedBodyFallsBackWhenIdentityInvalid(t *testing.T) {
	n := newEmailNotifier(newEmailNotifyFake(), newMailQueue(discardLogger()), "https://scrumboy.example.com", true, discardLogger())
	_, body, ok := n.renderCreatorEmail(todoapp.AuthorizedCreatorNotification{
		ProjectName:    "Roadmap",
		ProjectSlug:    "roadmap",
		Title:          "Fix login",
		ActivityReason: todoapp.RefreshReasonTodoMoved,
	}, emailCategoryCreatedByMe, "")
	if !ok {
		t.Fatal("expected createdByMe render to succeed")
	}
	want := "Card moved\n\nProject: Roadmap\n\nView project:\nhttps://scrumboy.example.com/roadmap\n"
	if body != want {
		t.Fatalf("expected generic createdByMe body %q, got %q", want, body)
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

func TestRefreshNeededPublisherPassesMoveTransitionNames(t *testing.T) {
	cap := &capturingEventConsumer{}
	s := &Server{fanout: eventbus.NewFanout(cap)}
	s.emitRefreshNeeded(context.Background(), 7, todoapp.RefreshReasonTodoMoved, refresh.Entity{
		LocalID: 42, Title: "Fix login", FromName: "Testing", ToName: "Done",
	})
	if len(cap.events) != 1 {
		t.Fatalf("events=%d, want 1", len(cap.events))
	}
	var payload refreshNeededPayload
	if err := json.Unmarshal(cap.events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.LocalID != 42 || payload.Title != "Fix login" || payload.FromName != "Testing" || payload.ToName != "Done" {
		t.Fatalf("move refresh payload = %+v", payload)
	}
}

func TestTagDeletionPublisherPassesNameEntity(t *testing.T) {
	cap := &capturingEventConsumer{}
	s := &Server{fanout: eventbus.NewFanout(cap)}
	tagDeletionPublisher{server: s}.PublishTagDeleted(context.Background(), 7, "blocked")
	if len(cap.events) != 1 {
		t.Fatalf("events=%d, want 1", len(cap.events))
	}
	e := cap.events[0]
	if e.Type != "board.refresh_needed" {
		t.Fatalf("type=%q", e.Type)
	}
	if e.ProjectID != 7 {
		t.Fatalf("project=%d, want 7", e.ProjectID)
	}
	var p refreshNeededPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Reason != "tag_deleted" || p.Name != "blocked" {
		t.Fatalf("payload=%+v, want tag_deleted name=blocked", p)
	}
}

func TestTagDeletionPublisherIDBasedPassesZeroEntity(t *testing.T) {
	cap := &capturingEventConsumer{}
	s := &Server{fanout: eventbus.NewFanout(cap)}
	tagDeletionPublisher{server: s}.PublishTagDeleted(context.Background(), 7, "")
	if len(cap.events) != 1 {
		t.Fatalf("events=%d, want 1", len(cap.events))
	}
	var p refreshNeededPayload
	if err := json.Unmarshal(cap.events[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Name != "" || p.LocalID != 0 || p.Title != "" {
		t.Fatalf("expected zero entity, got %+v", p)
	}
	assertRefreshNeededEntityOmitted(t, cap.events[0].Payload)
}

func assertRefreshNeededEntityOmitted(t *testing.T, payload []byte) {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"name", "localId", "title", "fromName", "toName"} {
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
		FromName:    "Testing",
		ToName:      "Done",
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
		if _, ok := wire["fromName"]; ok {
			t.Fatalf("fromName leaked onto SSE: %#v", wire)
		}
		if _, ok := wire["toName"]; ok {
			t.Fatalf("toName leaked onto SSE: %#v", wire)
		}
		if _, ok := wire["actorUserId"]; ok {
			t.Fatalf("actorUserId leaked onto SSE: %#v", wire)
		}
	default:
		t.Fatal("expected SSE event")
	}
}
