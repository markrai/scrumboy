package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	todoapp "scrumboy/internal/application/todo"
	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

const refreshNotifyDebounce = 2 * time.Minute

type emailNotifyStore interface {
	GetEmailNotifyPref(ctx context.Context, userID int64) (store.EmailNotifyPref, error)
	GetProject(ctx context.Context, projectID int64) (store.Project, error)
	GetProjectRole(ctx context.Context, projectID int64, userID int64) (store.ProjectRole, error)
	GetUser(ctx context.Context, userID int64) (store.User, error)
	ListProjectMembers(ctx context.Context, projectID int64, userID int64) ([]store.ProjectMember, error)
}

type todoAssignedMutationFactsContextKey struct{}

func withTodoAssignedMutationFacts(ctx context.Context, facts store.TodoAssignedMutationFacts) context.Context {
	if facts.CreatedByUserID != nil {
		creatorID := *facts.CreatedByUserID
		facts.CreatedByUserID = &creatorID
	}
	return context.WithValue(ctx, todoAssignedMutationFactsContextKey{}, facts)
}

func todoAssignedMutationFactsFromContext(ctx context.Context) (store.TodoAssignedMutationFacts, bool) {
	facts, ok := ctx.Value(todoAssignedMutationFactsContextKey{}).(store.TodoAssignedMutationFacts)
	if ok && facts.CreatedByUserID != nil {
		creatorID := *facts.CreatedByUserID
		facts.CreatedByUserID = &creatorID
	}
	return facts, ok
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
	store             emailNotifyStore
	mailQueue         *mailQueue
	publicBaseURL     string
	smtpConfigured    bool
	logger            *log.Logger
	creatorAuthorizer *todoapp.CreatorNotificationAuthorizationService

	mu       sync.Mutex
	lastSent map[notifyDebounceKey]time.Time
}

func newEmailNotifier(st emailNotifyStore, mq *mailQueue, publicBaseURL string, smtpConfigured bool, logger *log.Logger) *emailNotifier {
	return &emailNotifier{
		store:             st,
		mailQueue:         mq,
		publicBaseURL:     publicBaseURL,
		smtpConfigured:    smtpConfigured,
		logger:            logger,
		creatorAuthorizer: todoapp.NewCreatorNotificationAuthorizationService(st),
		lastSent:          make(map[notifyDebounceKey]time.Time),
	}
}

// emailCategory identifies which user-facing opt-in checkbox governs a given event.
type emailCategory string

const (
	emailCategoryAssigned        emailCategory = "assigned"
	emailCategoryCreatedByMe     emailCategory = "createdByMe"
	emailCategoryCardActivity    emailCategory = "cardActivity"
	emailCategorySprintActivity  emailCategory = "sprintActivity"
	emailCategoryProjectActivity emailCategory = "projectActivity"
	emailCategoryAddedToProject  emailCategory = "addedToProject"
)

// reasonInfo maps board.refresh_needed's `reason` string to a notification
// category plus the copy used to describe it. Reasons absent from this map
// (e.g. purely-cosmetic or wall-note reasons) never generate email.
type reasonInfo struct {
	category emailCategory
	subject  string // short phrase for the subject line, e.g. "card moved"
	actorLed string // phrase following the actor's name, e.g. "moved a card"
	passive  string // fallback phrase when no actor name is available, e.g. "A card was moved"
}

var refreshReasonInfo = map[string]reasonInfo{
	"todo_created":       {emailCategoryCardActivity, "card created", "created a card", "A card was created"},
	"todo_updated":       {emailCategoryCardActivity, "card updated", "updated a card", "A card was updated"},
	"todo_moved":         {emailCategoryCardActivity, "card moved", "moved a card", "A card was moved"},
	"todo_deleted":       {emailCategoryCardActivity, "card deleted", "deleted a card", "A card was deleted"},
	"todo_links_updated": {emailCategoryCardActivity, "card links updated", "updated a card's links", "A card's links were updated"},

	"sprint_created":   {emailCategorySprintActivity, "sprint created", "created a sprint", "A sprint was created"},
	"sprint_updated":   {emailCategorySprintActivity, "sprint updated", "updated a sprint", "A sprint was updated"},
	"sprint_deleted":   {emailCategorySprintActivity, "sprint deleted", "deleted a sprint", "A sprint was deleted"},
	"sprint_activated": {emailCategorySprintActivity, "sprint activated", "activated a sprint", "A sprint was activated"},
	"sprint_closed":    {emailCategorySprintActivity, "sprint closed", "closed a sprint", "A sprint was closed"},

	"project_updated":          {emailCategoryProjectActivity, "project updated", "updated the project", "The project was updated"},
	"project_deleted":          {emailCategoryProjectActivity, "project deleted", "deleted the project", "The project was deleted"},
	"project_settings_updated": {emailCategoryProjectActivity, "project settings updated", "updated the project settings", "The project settings were updated"},
	"board_claimed":            {emailCategoryProjectActivity, "board claimed", "claimed the board", "The board was claimed"},
	"workflow_column_added":    {emailCategoryProjectActivity, "workflow column added", "added a workflow column", "A workflow column was added"},
	"workflow_column_updated":  {emailCategoryProjectActivity, "workflow column updated", "updated a workflow column", "A workflow column was updated"},
	"workflow_column_deleted":  {emailCategoryProjectActivity, "workflow column deleted", "deleted a workflow column", "A workflow column was deleted"},
	"tag_color_updated":        {emailCategoryProjectActivity, "tag color updated", "updated a tag color", "A tag color was updated"},
	"tag_deleted":              {emailCategoryProjectActivity, "tag deleted", "deleted a tag", "A tag was deleted"},
}

func (n *emailNotifier) OnEvent(ctx context.Context, e eventbus.Event) {
	if !n.smtpConfigured || n.publicBaseURL == "" {
		return
	}
	switch e.Type {
	case "todo.assigned", "board.refresh_needed", "project.membership":
		// Never block the fanout / SSE path — same pattern as pushNotifier and the webhook dispatcher.
		go n.handle(context.WithoutCancel(ctx), e)
	case eventbus.TodoCreatorNotificationRecipientAuthorizedEventType:
		// Reauthorize before queueing without blocking fanout. The worker repeats
		// authorization and performs preference, destination, and rendering checks
		// at every actual send attempt.
		go n.handleCreatorCandidate(context.WithoutCancel(ctx), e)
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
	excluded := make(map[int64]bool)
	creatorID, creatorCandidateExpected := creatorCandidateForAssignmentMutation(ctx, e.ProjectID, domain)
	if creatorCandidateExpected {
		excluded[creatorID] = true
	}
	assignmentSent := false
	if domain.ToAssigneeUID != nil && (!creatorCandidateExpected || *domain.ToAssigneeUID != creatorID) {
		assignmentSent = n.handleAssignment(ctx, e.ProjectID, domain)
	}
	if domain.ToAssigneeUID != nil && assignmentSent {
		excluded[*domain.ToAssigneeUID] = true
	}
	n.handleActivity(ctx, e.ProjectID, domain.ActivityReason, domain.ActorUserID, excluded)
}

func (n *emailNotifier) handleAssignment(ctx context.Context, projectID int64, domain eventbus.TodoAssignedPayload) bool {
	assigneeID := *domain.ToAssigneeUID
	// ActorUserID == 0 means the actor wasn't captured (should not happen in normal
	// authenticated flows); we can't prove self-assignment then, so we don't skip.
	// This is the opposite fail-safe direction from handleRefreshNeeded, which
	// requires a known actor before sending at all — there, an unknown actor can't
	// authorize the ListProjectMembers lookup, so no email path exists to skip.
	if domain.ActorUserID != 0 && domain.ActorUserID == assigneeID {
		return false // no email for self-assignment
	}

	pref, err := n.getPref(ctx, assigneeID)
	if err != nil || !pref.Enabled || !pref.Assigned {
		return false
	}

	proj, err := n.store.GetProject(ctx, projectID)
	if err != nil {
		return false
	}
	user, err := n.store.GetUser(ctx, assigneeID)
	if err != nil || user.Email == "" {
		return false
	}

	subject := fmt.Sprintf("Assigned to you: %s", domain.Title)
	body := fmt.Sprintf(
		"A card was assigned to you in %s.\n\n%s\n\nView the board:\n%s\n",
		proj.Name, domain.Title, n.projectURL(proj.Slug),
	)
	return n.send(user.Email, subject, body, fmt.Sprintf("email-notify category=%s user=%d", emailCategoryAssigned, assigneeID))
}

func creatorCandidateForAssignmentMutation(ctx context.Context, projectID int64, domain eventbus.TodoAssignedPayload) (int64, bool) {
	facts, ok := todoAssignedMutationFactsFromContext(ctx)
	if !ok || !facts.DurableProject || facts.CreatedByUserID == nil ||
		projectID <= 0 || domain.ProjectID != projectID || domain.TodoID <= 0 || domain.LocalID <= 0 ||
		domain.ActorUserID <= 0 || *facts.CreatedByUserID <= 0 || *facts.CreatedByUserID == domain.ActorUserID ||
		(domain.ActivityReason != todoapp.RefreshReasonTodoUpdated && domain.ActivityReason != todoapp.RefreshReasonTodoMoved) {
		return 0, false
	}
	return *facts.CreatedByUserID, true
}

type creatorEmailWork struct {
	notifier                *emailNotifier
	candidate               todoapp.AuthorizedCreatorNotification
	mu                      sync.Mutex
	category                emailCategory
	selected                bool
	activityDebounceClaimed bool
}

func (n *emailNotifier) handleCreatorCandidate(ctx context.Context, e eventbus.Event) {
	if n == nil || n.mailQueue == nil || n.creatorAuthorizer == nil {
		return
	}
	var payload eventbus.TodoCreatorNotificationRecipientAuthorizedPayload
	if err := json.Unmarshal(e.Payload, &payload); err != nil ||
		e.ProjectID <= 0 || e.ProjectID != payload.ProjectID || !payload.MaterialChanged {
		return
	}
	candidate, ok, err := n.creatorAuthorizer.ReauthorizeRecipient(ctx, todoapp.AuthorizedCreatorNotification{
		ProjectID:             payload.ProjectID,
		ProjectSlug:           payload.ProjectSlug,
		TodoID:                payload.TodoID,
		LocalID:               payload.LocalID,
		Title:                 payload.Title,
		ActivityReason:        payload.ActivityReason,
		RecipientUserID:       payload.RecipientUserID,
		ActorUserID:           payload.ActorUserID,
		MaterialChanged:       payload.MaterialChanged,
		AssignmentChanged:     payload.AssignmentChanged,
		ToAssigneeUserID:      payload.ToAssigneeUserID,
		CardActivityCandidate: payload.CardActivityCandidate,
	})
	if err != nil || !ok {
		return
	}
	work := &creatorEmailWork{notifier: n, candidate: candidate}
	n.mailQueue.Enqueue(mailDelivery{
		LogRef:  fmt.Sprintf("email-notify creator-candidate user=%d", payload.RecipientUserID),
		Prepare: work.prepare,
	})
}

func (w *creatorEmailWork) prepare(ctx context.Context) (mailDelivery, bool, error) {
	if w == nil || w.notifier == nil || ctx.Err() != nil {
		return mailDelivery{}, false, nil
	}
	authorized, ok, err := w.notifier.creatorAuthorizer.ReauthorizeRecipient(ctx, w.candidate)
	if err != nil || !ok || !authorized.MaterialChanged {
		return mailDelivery{}, false, nil
	}
	pref, err := w.notifier.getPref(ctx, authorized.RecipientUserID)
	if err != nil || !pref.Enabled {
		return mailDelivery{}, false, nil
	}

	w.mu.Lock()
	category := w.category
	if !w.selected {
		category, ok = selectCreatorEmailCategory(pref, authorized)
		if ok {
			w.category = category
			w.selected = true
		}
	} else {
		ok = categoryEnabled(pref, category)
	}
	w.mu.Unlock()
	if !ok {
		return mailDelivery{}, false, nil
	}

	user, err := w.notifier.store.GetUser(ctx, authorized.RecipientUserID)
	if err != nil || user.Email == "" {
		return mailDelivery{}, false, nil
	}
	subject, body, ok := w.notifier.renderCreatorEmail(authorized, category)
	if !ok {
		return mailDelivery{}, false, nil
	}
	if category == emailCategoryCardActivity && !w.claimActivityDebounce(authorized.ProjectID, authorized.RecipientUserID) {
		return mailDelivery{}, false, nil
	}
	return mailDelivery{
		To:      user.Email,
		Subject: subject,
		Body:    body,
		LogRef:  fmt.Sprintf("email-notify category=%s user=%d", category, authorized.RecipientUserID),
	}, true, nil
}

func (w *creatorEmailWork) claimActivityDebounce(projectID, userID int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.activityDebounceClaimed {
		return true
	}
	if !w.notifier.claimActivityDebounce(projectID, emailCategoryCardActivity, userID) {
		return false
	}
	w.activityDebounceClaimed = true
	return true
}

func selectCreatorEmailCategory(pref store.EmailNotifyPref, candidate todoapp.AuthorizedCreatorNotification) (emailCategory, bool) {
	if candidate.AssignmentChanged && candidate.ToAssigneeUserID != nil &&
		*candidate.ToAssigneeUserID == candidate.RecipientUserID && pref.Assigned {
		return emailCategoryAssigned, true
	}
	if pref.CreatedByMe {
		return emailCategoryCreatedByMe, true
	}
	if candidate.CardActivityCandidate && pref.CardActivity {
		return emailCategoryCardActivity, true
	}
	return "", false
}

func (n *emailNotifier) renderCreatorEmail(candidate todoapp.AuthorizedCreatorNotification, category emailCategory) (string, string, bool) {
	switch category {
	case emailCategoryAssigned:
		return fmt.Sprintf("Assigned to you: %s", candidate.Title), fmt.Sprintf(
			"A card was assigned to you in %s.\n\n%s\n\nView the board:\n%s\n",
			candidate.ProjectName, candidate.Title, n.projectURL(candidate.ProjectSlug),
		), true
	case emailCategoryCreatedByMe:
		action := "updated"
		if candidate.ActivityReason == todoapp.RefreshReasonTodoMoved {
			action = "moved"
		}
		return fmt.Sprintf("A card you opened was %s: %s", action, candidate.Title), fmt.Sprintf(
			"A card you opened was %s in %s.\n\n%s\n\nView the board:\n%s\n",
			action, candidate.ProjectName, candidate.Title, n.projectURL(candidate.ProjectSlug),
		), true
	case emailCategoryCardActivity:
		info, ok := refreshReasonInfo[candidate.ActivityReason]
		if !ok || info.category != emailCategoryCardActivity {
			return "", "", false
		}
		return fmt.Sprintf("%s: %s", candidate.ProjectName, info.subject), fmt.Sprintf(
			"%s in %s.\n\nView the board:\n%s\n",
			info.passive, candidate.ProjectName, n.projectURL(candidate.ProjectSlug),
		), true
	default:
		return "", "", false
	}
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
	excluded := make(map[int64]bool)
	if request, ok := todoapp.CreatorNotificationRequestFromContext(ctx); ok &&
		request.CardActivityCandidate &&
		request.ProjectID == e.ProjectID && request.ActivityReason == p.Reason {
		excluded[request.CreatedByUserID] = true
	}
	n.handleActivity(ctx, e.ProjectID, p.Reason, p.ActorUserID, excluded)
}

func (n *emailNotifier) handleActivity(ctx context.Context, projectID int64, reason string, actorUserID int64, excluded map[int64]bool) {
	info, ok := refreshReasonInfo[reason]
	if !ok {
		return
	}
	category := info.category
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

	// Prefer the actor name already joined into ListProjectMembers. Fall back to
	// GetUser only when the actor is absent from members (Temporary Boards bypass
	// role checks, so a signed-in link visitor can act without a membership row).
	action := info.passive
	actorName := ""
	actorInMembers := false
	for _, m := range members {
		if m.UserID == actorUserID {
			actorInMembers = true
			actorName = m.Name
			break
		}
	}
	if !actorInMembers {
		if actor, err := n.store.GetUser(ctx, actorUserID); err == nil {
			actorName = actor.Name
		}
	}
	if actorName != "" {
		action = actorName + " " + info.actorLed
	}

	subject := fmt.Sprintf("%s: %s", proj.Name, info.subject)
	body := fmt.Sprintf(
		"%s in %s.\n\nView the board:\n%s\n",
		action, proj.Name, n.projectURL(proj.Slug),
	)
	for _, m := range members {
		if m.UserID == actorUserID || excluded[m.UserID] {
			continue // skip the person who made the change
		}
		pref, err := n.getPref(ctx, m.UserID)
		if err != nil || !pref.Enabled || !categoryEnabled(pref, category) || m.Email == "" {
			continue
		}
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

func (n *emailNotifier) claimActivityDebounce(projectID int64, category emailCategory, userID int64) bool {
	key := notifyDebounceKey{projectID: projectID, category: category, userID: userID}
	now := time.Now()
	n.mu.Lock()
	defer n.mu.Unlock()
	if last, ok := n.lastSent[key]; ok && now.Sub(last) < refreshNotifyDebounce {
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
	case emailCategoryAssigned:
		return pref.Assigned
	case emailCategoryCreatedByMe:
		return pref.CreatedByMe
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
