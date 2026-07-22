package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

const refreshNotifyDebounce = 2 * time.Minute

type emailNotifyStore interface {
	GetEmailNotifyPref(ctx context.Context, userID int64) (store.EmailNotifyPref, error)
	GetProject(ctx context.Context, projectID int64) (store.Project, error)
	GetUser(ctx context.Context, userID int64) (store.User, error)
	ListProjectMembers(ctx context.Context, projectID int64, userID int64) ([]store.ProjectMember, error)
}

type notifyDebounceKey struct {
	projectID int64
	category  emailCategory
	userID    int64
}

// emailNotifier sends opt-in email notifications for board activity (async;
// does not block fanout). Modeled on pushNotifier (push_notify.go), but reads
// each candidate recipient's per-category preference before sending, since
// unlike web push there is no separate subscribe step that already implies consent.
type emailNotifier struct {
	store          emailNotifyStore
	mailQueue      *mailQueue
	publicBaseURL  string
	smtpConfigured bool
	logger         *log.Logger

	mu       sync.Mutex
	lastSent map[notifyDebounceKey]time.Time
}

func newEmailNotifier(st emailNotifyStore, mq *mailQueue, publicBaseURL string, smtpConfigured bool, logger *log.Logger) *emailNotifier {
	return &emailNotifier{
		store:          st,
		mailQueue:      mq,
		publicBaseURL:  publicBaseURL,
		smtpConfigured: smtpConfigured,
		logger:         logger,
		lastSent:       make(map[notifyDebounceKey]time.Time),
	}
}

// emailCategory identifies which user-facing opt-in checkbox governs a given event.
type emailCategory string

const (
	emailCategoryAssigned        emailCategory = "assigned"
	emailCategoryCardActivity    emailCategory = "cardActivity"
	emailCategorySprintActivity  emailCategory = "sprintActivity"
	emailCategoryProjectActivity emailCategory = "projectActivity"
	emailCategoryAddedToProject  emailCategory = "addedToProject"
)

// refreshReasonCategory maps board.refresh_needed's `reason` string to a
// notification category. Reasons absent from this map (e.g. purely-cosmetic
// or wall-note reasons) never generate email.
var refreshReasonCategory = map[string]emailCategory{
	"todo_created":       emailCategoryCardActivity,
	"todo_updated":       emailCategoryCardActivity,
	"todo_moved":         emailCategoryCardActivity,
	"todo_deleted":       emailCategoryCardActivity,
	"todo_links_updated": emailCategoryCardActivity,

	"sprint_created":   emailCategorySprintActivity,
	"sprint_updated":   emailCategorySprintActivity,
	"sprint_deleted":   emailCategorySprintActivity,
	"sprint_activated": emailCategorySprintActivity,
	"sprint_closed":    emailCategorySprintActivity,

	"project_updated":          emailCategoryProjectActivity,
	"project_deleted":          emailCategoryProjectActivity,
	"project_settings_updated": emailCategoryProjectActivity,
	"board_claimed":            emailCategoryProjectActivity,
	"workflow_column_added":    emailCategoryProjectActivity,
	"workflow_column_updated":  emailCategoryProjectActivity,
	"workflow_column_deleted":  emailCategoryProjectActivity,
	"tag_color_updated":        emailCategoryProjectActivity,
	"tag_deleted":              emailCategoryProjectActivity,
}

func (n *emailNotifier) OnEvent(_ context.Context, e eventbus.Event) {
	if !n.smtpConfigured || n.publicBaseURL == "" {
		return
	}
	switch e.Type {
	case "todo.assigned", "board.refresh_needed", "project.membership":
		// Never block the fanout / SSE path — same pattern as pushNotifier and the webhook dispatcher.
		go n.handle(context.Background(), e)
	}
}

func (n *emailNotifier) handle(ctx context.Context, e eventbus.Event) {
	switch e.Type {
	case "todo.assigned":
		n.handleTodoAssigned(ctx, e)
	case "board.refresh_needed":
		n.handleRefreshNeeded(ctx, e)
	case "project.membership":
		n.handleMembership(ctx, e)
	}
}

func (n *emailNotifier) handleTodoAssigned(ctx context.Context, e eventbus.Event) {
	var domain eventbus.TodoAssignedPayload
	if err := json.Unmarshal(e.Payload, &domain); err != nil {
		return
	}
	if domain.ToAssigneeUID != nil {
		n.handleAssignment(ctx, e.ProjectID, domain)
	}
	excluded := make(map[int64]bool)
	if domain.ToAssigneeUID != nil {
		excluded[*domain.ToAssigneeUID] = true
	}
	n.handleActivity(ctx, e.ProjectID, domain.ActivityReason, domain.ActorUserID, excluded)
}

func (n *emailNotifier) handleAssignment(ctx context.Context, projectID int64, domain eventbus.TodoAssignedPayload) {
	assigneeID := *domain.ToAssigneeUID
	// ActorUserID == 0 means the actor wasn't captured (should not happen in normal
	// authenticated flows); we can't prove self-assignment then, so we don't skip.
	// This is the opposite fail-safe direction from handleRefreshNeeded, which
	// requires a known actor before sending at all — there, an unknown actor can't
	// authorize the ListProjectMembers lookup, so no email path exists to skip.
	if domain.ActorUserID != 0 && domain.ActorUserID == assigneeID {
		return // no email for self-assignment
	}

	pref, err := n.getPref(ctx, assigneeID)
	if err != nil || !pref.Enabled || !pref.Assigned {
		return
	}

	proj, err := n.store.GetProject(ctx, projectID)
	if err != nil {
		return
	}
	user, err := n.store.GetUser(ctx, assigneeID)
	if err != nil || user.Email == "" {
		return
	}

	subject := fmt.Sprintf("Assigned to you: %s", domain.Title)
	body := fmt.Sprintf(
		"A card was assigned to you in %s.\n\n%s\n\nView the board:\n%s\n",
		proj.Name, domain.Title, n.projectURL(proj.Slug),
	)
	n.send(user.Email, subject, body, fmt.Sprintf("email-notify category=%s user=%d", emailCategoryAssigned, assigneeID))
}

func (n *emailNotifier) handleRefreshNeeded(ctx context.Context, e eventbus.Event) {
	var p struct {
		Reason      string `json:"reason"`
		ActorUserID int64  `json:"actorUserId"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return
	}
	if p.Reason == "project_deleted" {
		return
	}
	n.handleActivity(ctx, e.ProjectID, p.Reason, p.ActorUserID, nil)
}

func (n *emailNotifier) handleActivity(ctx context.Context, projectID int64, reason string, actorUserID int64, excluded map[int64]bool) {
	category, ok := refreshReasonCategory[reason]
	if !ok {
		return
	}
	if actorUserID == 0 {
		return // no known actor to authorize the ListProjectMembers lookup as
	}

	proj, err := n.store.GetProject(ctx, projectID)
	if err != nil {
		return
	}
	members, err := n.store.ListProjectMembers(ctx, projectID, actorUserID)
	if err != nil {
		return
	}

	subject := fmt.Sprintf("%s: activity update", proj.Name)
	for _, m := range members {
		if m.UserID == actorUserID || excluded[m.UserID] {
			continue // skip the person who made the change
		}
		pref, err := n.getPref(ctx, m.UserID)
		if err != nil || !pref.Enabled || !categoryEnabled(pref, category) || m.Email == "" {
			continue
		}
		body := fmt.Sprintf(
			"There was activity in %s.\n\nView the board:\n%s\n",
			proj.Name, n.projectURL(proj.Slug),
		)
		n.enqueueActivity(projectID, category, m.UserID, mailDelivery{
			To: m.Email, Subject: subject, Body: body,
			LogRef: fmt.Sprintf("email-notify category=%s user=%d", category, m.UserID),
		})
	}
}

func (n *emailNotifier) OnProjectDeleted(deleted store.DeletedProjectSnapshot, actorUserID int64) {
	if !n.smtpConfigured || n.publicBaseURL == "" {
		return
	}
	go n.handleProjectDeleted(context.Background(), deleted, actorUserID)
}

func (n *emailNotifier) handleProjectDeleted(ctx context.Context, deleted store.DeletedProjectSnapshot, actorUserID int64) {
	if actorUserID == 0 || deleted.Name == "" {
		return
	}
	subject := fmt.Sprintf("%s: project deleted", deleted.Name)
	body := fmt.Sprintf("The project \"%s\" was deleted from Scrumboy.\n", deleted.Name)
	for _, userID := range deleted.MemberUserIDs {
		if userID == actorUserID {
			continue
		}
		pref, err := n.getPref(ctx, userID)
		if err != nil || !pref.Enabled || !pref.ProjectActivity {
			continue
		}
		user, err := n.store.GetUser(ctx, userID)
		if err != nil || user.Email == "" {
			continue
		}
		n.enqueueActivity(deleted.ProjectID, emailCategoryProjectActivity, userID, mailDelivery{
			To: user.Email, Subject: subject, Body: body,
			LogRef: fmt.Sprintf("email-notify category=%s user=%d", emailCategoryProjectActivity, userID),
		})
	}
}

func (n *emailNotifier) enqueueActivity(projectID int64, category emailCategory, userID int64, delivery mailDelivery) bool {
	key := notifyDebounceKey{projectID: projectID, category: category, userID: userID}
	now := time.Now()
	n.mu.Lock()
	defer n.mu.Unlock()
	if last, ok := n.lastSent[key]; ok && now.Sub(last) < refreshNotifyDebounce {
		return false
	}
	if !n.mailQueue.Enqueue(delivery) {
		return false
	}
	n.lastSent[key] = now
	return true
}

func (n *emailNotifier) handleMembership(ctx context.Context, e eventbus.Event) {
	var p eventbus.MembershipPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return
	}
	if p.Action != "added" || p.AffectedUserID == 0 {
		return
	}
	if p.ActorUserID != 0 && p.ActorUserID == p.AffectedUserID {
		return // no email for adding yourself
	}

	pref, err := n.getPref(ctx, p.AffectedUserID)
	if err != nil || !pref.Enabled || !pref.AddedToProject {
		return
	}

	proj, err := n.store.GetProject(ctx, e.ProjectID)
	if err != nil {
		return
	}
	user, err := n.store.GetUser(ctx, p.AffectedUserID)
	if err != nil || user.Email == "" {
		return
	}

	subject := fmt.Sprintf("You were added to %s", proj.Name)
	body := fmt.Sprintf(
		"You were added to the project \"%s\" on Scrumboy.\n\nView the board:\n%s\n",
		proj.Name, n.projectURL(proj.Slug),
	)
	n.send(user.Email, subject, body, fmt.Sprintf("email-notify category=%s user=%d", emailCategoryAddedToProject, p.AffectedUserID))
}

func categoryEnabled(pref store.EmailNotifyPref, c emailCategory) bool {
	switch c {
	case emailCategoryCardActivity:
		return pref.CardActivity
	case emailCategorySprintActivity:
		return pref.SprintActivity
	case emailCategoryProjectActivity:
		return pref.ProjectActivity
	default:
		return false
	}
}

func (n *emailNotifier) getPref(ctx context.Context, userID int64) (store.EmailNotifyPref, error) {
	return n.store.GetEmailNotifyPref(ctx, userID)
}

func (n *emailNotifier) projectURL(slug string) string {
	return strings.TrimRight(n.publicBaseURL, "/") + "/" + slug
}

func (n *emailNotifier) send(to, subject, body, logRef string) bool {
	return n.mailQueue.Enqueue(mailDelivery{
		To:      to,
		Subject: subject,
		Body:    body,
		LogRef:  logRef,
	})
}
