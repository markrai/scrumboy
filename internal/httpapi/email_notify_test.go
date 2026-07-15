package httpapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

func newTestEmailNotifier(t *testing.T, st *store.Store, smtpConfigured bool) (*emailNotifier, *mailQueue) {
	t.Helper()
	mq := newMailQueue(discardLogger())
	n := newEmailNotifier(st, mq, "https://scrumboy.example.com", smtpConfigured, discardLogger())
	return n, mq
}

func drainAfterAsync(mq *mailQueue) []mailDelivery {
	time.Sleep(150 * time.Millisecond)
	return mq.Drain()
}

func TestEmailNotifier_NoOpWithoutSMTPConfigured(t *testing.T) {
	st := newTestStore(t)
	n, mq := newTestEmailNotifier(t, st, false)

	u, err := st.BootstrapUser(context.Background(), "assignee@example.com", "pass1234A!", "Assignee")
	if err != nil {
		t.Fatal(err)
	}
	assignee := u.ID
	payload, _ := json.Marshal(eventbus.TodoAssignedPayload{
		ProjectID: 1, TodoID: 1, Title: "t", ActorUserID: 999, ToAssigneeUID: &assignee,
	})
	n.OnEvent(context.Background(), eventbus.Event{Type: "todo.assigned", ProjectID: 1, Payload: payload})

	if got := drainAfterAsync(mq); len(got) != 0 {
		t.Fatalf("expected no email when SMTP not configured, got %+v", got)
	}
}

func TestEmailNotifier_TodoAssigned_SendsWhenOptedIn(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	n, mq := newTestEmailNotifier(t, st, true)

	owner, err := st.BootstrapUser(ctx, "owner@example.com", "pass1234A!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	assignee, err := st.CreateUser(ctx, "assignee@example.com", "pass1234A!", "Assignee")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := store.WithUserID(ctx, owner.ID)
	proj, err := st.CreateProject(ownerCtx, "Test Project")
	if err != nil {
		t.Fatal(err)
	}

	// Default prefs have "assigned" on, so no explicit opt-in call needed.
	payload, _ := json.Marshal(eventbus.TodoAssignedPayload{
		ProjectID: proj.ID, TodoID: 1, Title: "Fix the bug", ActorUserID: owner.ID, ToAssigneeUID: &assignee.ID,
	})

	// Master toggle defaults to disabled; confirm no email until enabled.
	n.OnEvent(ctx, eventbus.Event{Type: "todo.assigned", ProjectID: proj.ID, Payload: payload})
	if got := drainAfterAsync(mq); len(got) != 0 {
		t.Fatalf("expected no email before master opt-in, got %+v", got)
	}

	pref := store.DefaultEmailNotifyPref()
	pref.Enabled = true
	prefJSON, _ := json.Marshal(pref)
	if err := st.SetUserPreference(ctx, assignee.ID, "emailNotifications", string(prefJSON)); err != nil {
		t.Fatal(err)
	}

	n.OnEvent(ctx, eventbus.Event{Type: "todo.assigned", ProjectID: proj.ID, Payload: payload})
	got := drainAfterAsync(mq)
	if len(got) != 1 {
		t.Fatalf("expected 1 email after opt-in, got %+v", got)
	}
	if got[0].To != "assignee@example.com" {
		t.Fatalf("expected email to assignee, got %+v", got[0])
	}
}

func TestEmailNotifier_TodoAssigned_SkipsSelfAssignment(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	n, mq := newTestEmailNotifier(t, st, true)

	u, err := st.BootstrapUser(ctx, "solo@example.com", "pass1234A!", "Solo")
	if err != nil {
		t.Fatal(err)
	}
	pref := store.DefaultEmailNotifyPref()
	pref.Enabled = true
	prefJSON, _ := json.Marshal(pref)
	if err := st.SetUserPreference(ctx, u.ID, "emailNotifications", string(prefJSON)); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(eventbus.TodoAssignedPayload{
		ProjectID: 1, TodoID: 1, Title: "t", ActorUserID: u.ID, ToAssigneeUID: &u.ID,
	})
	n.OnEvent(ctx, eventbus.Event{Type: "todo.assigned", ProjectID: 1, Payload: payload})

	if got := drainAfterAsync(mq); len(got) != 0 {
		t.Fatalf("expected no email for self-assignment, got %+v", got)
	}
}

func TestEmailNotifier_RefreshNeeded_CardActivity(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	n, mq := newTestEmailNotifier(t, st, true)

	owner, err := st.BootstrapUser(ctx, "owner2@example.com", "pass1234A!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.CreateUser(ctx, "member@example.com", "pass1234A!", "Member")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := store.WithUserID(ctx, owner.ID)
	proj, err := st.CreateProject(ownerCtx, "Card Activity Project")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectMember(ownerCtx, owner.ID, proj.ID, member.ID, store.RoleViewer); err != nil {
		t.Fatal(err)
	}

	pref := store.DefaultEmailNotifyPref()
	pref.Enabled = true
	pref.CardActivity = true
	prefJSON, _ := json.Marshal(pref)
	if err := st.SetUserPreference(ctx, member.ID, "emailNotifications", string(prefJSON)); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(struct {
		Reason      string `json:"reason"`
		ActorUserID int64  `json:"actorUserId"`
	}{Reason: "todo_created", ActorUserID: owner.ID})
	n.OnEvent(ctx, eventbus.Event{Type: "board.refresh_needed", ProjectID: proj.ID, Payload: payload})

	got := drainAfterAsync(mq)
	if len(got) != 1 {
		t.Fatalf("expected 1 email to opted-in member, got %+v", got)
	}
	if got[0].To != "member@example.com" {
		t.Fatalf("expected email to member, got %+v", got[0])
	}
}

func TestEmailNotifier_RefreshNeeded_SkipsOptedOutMember(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	n, mq := newTestEmailNotifier(t, st, true)

	owner, err := st.BootstrapUser(ctx, "owner-optout@example.com", "pass1234A!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.CreateUser(ctx, "optout-member@example.com", "pass1234A!", "Member")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := store.WithUserID(ctx, owner.ID)
	proj, err := st.CreateProject(ownerCtx, "Opt Out Project")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectMember(ownerCtx, owner.ID, proj.ID, member.ID, store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	// Member never opts in; default pref has cardActivity off and enabled off.

	payload, _ := json.Marshal(struct {
		Reason      string `json:"reason"`
		ActorUserID int64  `json:"actorUserId"`
	}{Reason: "todo_created", ActorUserID: owner.ID})
	n.OnEvent(ctx, eventbus.Event{Type: "board.refresh_needed", ProjectID: proj.ID, Payload: payload})

	if got := drainAfterAsync(mq); len(got) != 0 {
		t.Fatalf("expected no email to opted-out member, got %+v", got)
	}
}

func TestEmailNotifier_RefreshNeeded_SkipsMemberWithNoEmail(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	n, mq := newTestEmailNotifier(t, st, true)

	owner, err := st.BootstrapUser(ctx, "owner-noemail@example.com", "pass1234A!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.CreateProject(store.WithUserID(ctx, owner.ID), "No Extra Members Project")
	if err != nil {
		t.Fatal(err)
	}

	pref := store.DefaultEmailNotifyPref()
	pref.Enabled = true
	pref.CardActivity = true
	prefJSON, _ := json.Marshal(pref)
	// Owner opts in but is also the actor, so should still be skipped (self).
	if err := st.SetUserPreference(ctx, owner.ID, "emailNotifications", string(prefJSON)); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(struct {
		Reason      string `json:"reason"`
		ActorUserID int64  `json:"actorUserId"`
	}{Reason: "todo_created", ActorUserID: owner.ID})
	n.OnEvent(ctx, eventbus.Event{Type: "board.refresh_needed", ProjectID: proj.ID, Payload: payload})

	if got := drainAfterAsync(mq); len(got) != 0 {
		t.Fatalf("expected no email when the only member is the actor, got %+v", got)
	}
}

func TestEmailNotifier_RefreshNeeded_DebouncesBurstsPerProjectCategory(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	n, mq := newTestEmailNotifier(t, st, true)

	owner, err := st.BootstrapUser(ctx, "owner-debounce@example.com", "pass1234A!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.CreateUser(ctx, "debounce-member@example.com", "pass1234A!", "Member")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := store.WithUserID(ctx, owner.ID)
	proj, err := st.CreateProject(ownerCtx, "Debounce Project")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectMember(ownerCtx, owner.ID, proj.ID, member.ID, store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	pref := store.DefaultEmailNotifyPref()
	pref.Enabled = true
	pref.CardActivity = true
	prefJSON, _ := json.Marshal(pref)
	if err := st.SetUserPreference(ctx, member.ID, "emailNotifications", string(prefJSON)); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(struct {
		Reason      string `json:"reason"`
		ActorUserID int64  `json:"actorUserId"`
	}{Reason: "todo_created", ActorUserID: owner.ID})

	// Fire a burst of same-category events; only the first should send.
	for i := 0; i < 5; i++ {
		n.OnEvent(ctx, eventbus.Event{Type: "board.refresh_needed", ProjectID: proj.ID, Payload: payload})
	}

	got := drainAfterAsync(mq)
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 email from a debounced burst, got %d: %+v", len(got), got)
	}
}

func TestEmailNotifier_RefreshNeeded_UnmappedReasonIsNoOp(t *testing.T) {
	st := newTestStore(t)
	n, mq := newTestEmailNotifier(t, st, true)

	payload, _ := json.Marshal(struct {
		Reason      string `json:"reason"`
		ActorUserID int64  `json:"actorUserId"`
	}{Reason: "wall_note_created", ActorUserID: 1})
	n.OnEvent(context.Background(), eventbus.Event{Type: "board.refresh_needed", ProjectID: 1, Payload: payload})

	if got := drainAfterAsync(mq); len(got) != 0 {
		t.Fatalf("expected no email for unmapped reason, got %+v", got)
	}
}

func TestEmailNotifier_Membership_AddedToProject(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	n, mq := newTestEmailNotifier(t, st, true)

	owner, err := st.BootstrapUser(ctx, "owner3@example.com", "pass1234A!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	newMember, err := st.CreateUser(ctx, "newmember@example.com", "pass1234A!", "New Member")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := store.WithUserID(ctx, owner.ID)
	proj, err := st.CreateProject(ownerCtx, "Membership Project")
	if err != nil {
		t.Fatal(err)
	}

	// AddedToProject defaults to on.
	payload, _ := json.Marshal(eventbus.MembershipPayload{
		ProjectID: proj.ID, AffectedUserID: newMember.ID, Action: "added", ActorUserID: owner.ID,
	})
	n.OnEvent(ctx, eventbus.Event{Type: "project.membership", ProjectID: proj.ID, Payload: payload})
	if got := drainAfterAsync(mq); len(got) != 0 {
		t.Fatalf("expected no email before master opt-in, got %+v", got)
	}

	pref := store.DefaultEmailNotifyPref()
	pref.Enabled = true
	prefJSON, _ := json.Marshal(pref)
	if err := st.SetUserPreference(ctx, newMember.ID, "emailNotifications", string(prefJSON)); err != nil {
		t.Fatal(err)
	}

	n.OnEvent(ctx, eventbus.Event{Type: "project.membership", ProjectID: proj.ID, Payload: payload})
	got := drainAfterAsync(mq)
	if len(got) != 1 {
		t.Fatalf("expected 1 email to newly added member, got %+v", got)
	}
	if got[0].To != "newmember@example.com" {
		t.Fatalf("expected email to new member, got %+v", got[0])
	}
}

func TestEmailNotifier_Membership_SkipsSelfAdd(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	n, mq := newTestEmailNotifier(t, st, true)

	u, err := st.BootstrapUser(ctx, "self-add@example.com", "pass1234A!", "Self")
	if err != nil {
		t.Fatal(err)
	}
	pref := store.DefaultEmailNotifyPref()
	pref.Enabled = true
	prefJSON, _ := json.Marshal(pref)
	if err := st.SetUserPreference(ctx, u.ID, "emailNotifications", string(prefJSON)); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(eventbus.MembershipPayload{
		ProjectID: 1, AffectedUserID: u.ID, Action: "added", ActorUserID: u.ID,
	})
	n.OnEvent(ctx, eventbus.Event{Type: "project.membership", ProjectID: 1, Payload: payload})

	if got := drainAfterAsync(mq); len(got) != 0 {
		t.Fatalf("expected no email for self-add, got %+v", got)
	}
}
