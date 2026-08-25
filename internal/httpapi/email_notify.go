package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"scrumboy/internal/application/refresh"
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
	category   emailCategory
	subject    string // short phrase for the subject line, e.g. "card moved"
	actorLabel string // action-specific metadata label, e.g. "Moved by"
}

var refreshReasonInfo = map[string]reasonInfo{
	"todo_created":       {emailCategoryCardActivity, "card created", "Created by"},
	"todo_updated":       {emailCategoryCardActivity, "card updated", "Updated by"},
	"todo_moved":         {emailCategoryCardActivity, "card moved", "Moved by"},
	"todo_deleted":       {emailCategoryCardActivity, "card deleted", "Deleted by"},
	"todo_links_updated": {emailCategoryCardActivity, "card links updated", "Updated by"},

	"sprint_created":   {emailCategorySprintActivity, "sprint created", "Created by"},
	"sprint_updated":   {emailCategorySprintActivity, "sprint updated", "Updated by"},
	"sprint_deleted":   {emailCategorySprintActivity, "sprint deleted", "Deleted by"},
	"sprint_activated": {emailCategorySprintActivity, "sprint activated", "Activated by"},
	"sprint_closed":    {emailCategorySprintActivity, "sprint closed", "Closed by"},

	"project_updated":          {emailCategoryProjectActivity, "project updated", "Updated by"},
	"project_deleted":          {emailCategoryProjectActivity, "project deleted", "Deleted by"},
	"project_settings_updated": {emailCategoryProjectActivity, "project settings updated", "Updated by"},
	"board_claimed":            {emailCategoryProjectActivity, "board claimed", "Claimed by"},
	"workflow_column_added":    {emailCategoryProjectActivity, "column added", "Added by"},
	"workflow_column_updated":  {emailCategoryProjectActivity, "column updated", "Updated by"},
	"workflow_column_deleted":  {emailCategoryProjectActivity, "column deleted", "Deleted by"},
	"tag_color_updated":        {emailCategoryProjectActivity, "tag color changed", "Changed by"},
	"tag_deleted":              {emailCategoryProjectActivity, "tag deleted", "Deleted by"},
}

type notificationField struct {
	label string
	value string
}

type plainTextNotification struct {
	heading  string
	fields   []notificationField
	ctaLabel string
	ctaURL   string
}

func formatPlainTextNotification(notification plainTextNotification) string {
	sections := make([]string, 0, 3)
	if heading := strings.TrimSpace(notification.heading); heading != "" {
		sections = append(sections, heading)
	}
	lines := make([]string, 0, len(notification.fields))
	for _, field := range notification.fields {
		label := strings.TrimSpace(field.label)
		value := strings.TrimSpace(field.value)
		if label == "" || value == "" {
			continue
		}
		lines = append(lines, label+": "+value)
	}
	if len(lines) > 0 {
		sections = append(sections, strings.Join(lines, "\n"))
	}
	if label, target := strings.TrimSpace(notification.ctaLabel), strings.TrimSpace(notification.ctaURL); label != "" && target != "" {
		sections = append(sections, label+":\n"+target)
	}
	return strings.Join(sections, "\n\n") + "\n"
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
	n.handleActivity(ctx, e.ProjectID, domain.ActivityReason, domain.ActorUserID, refresh.Entity{
		LocalID: domain.LocalID,
		Title:   domain.Title,
	}, excluded)
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
	card, ok := cardIdentity(domain.LocalID, domain.Title)
	fields := []notificationField{{label: "Assigned to", value: user.Name}}
	ctaLabel, ctaURL := "View project", n.projectURL(proj.Slug)
	if ok {
		fields = append(fields, notificationField{label: "Card", value: card})
		ctaLabel, ctaURL = "View card", n.cardURL(proj.Slug, domain.LocalID)
	}
	fields = append(fields, notificationField{label: "Project", value: proj.Name})
	body := formatPlainTextNotification(plainTextNotification{
		heading: "Card assigned", fields: fields, ctaLabel: ctaLabel, ctaURL: ctaURL,
	})
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
		FromName:              payload.FromName,
		ToName:                payload.ToName,
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
	subject, body, ok := w.notifier.renderCreatorEmail(authorized, category, user.Name)
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

func (n *emailNotifier) renderCreatorEmail(candidate todoapp.AuthorizedCreatorNotification, category emailCategory, recipientName string) (string, string, bool) {
	switch category {
	case emailCategoryAssigned:
		subject := fmt.Sprintf("Assigned to you: %s", candidate.Title)
		card, ok := cardIdentity(candidate.LocalID, candidate.Title)
		fields := []notificationField{{label: "Assigned to", value: recipientName}}
		ctaLabel, ctaURL := "View project", n.projectURL(candidate.ProjectSlug)
		if ok {
			fields = append(fields, notificationField{label: "Card", value: card})
			ctaLabel, ctaURL = "View card", n.cardURL(candidate.ProjectSlug, candidate.LocalID)
		}
		fields = append(fields, notificationField{label: "Project", value: candidate.ProjectName})
		fields = appendMoveStatusField(fields, candidate.ActivityReason, candidate.FromName, candidate.ToName)
		body := formatPlainTextNotification(plainTextNotification{
			heading: "Card assigned", fields: fields, ctaLabel: ctaLabel, ctaURL: ctaURL,
		})
		return subject, body, true
	case emailCategoryCreatedByMe:
		action := "updated"
		if candidate.ActivityReason == todoapp.RefreshReasonTodoMoved {
			action = "moved"
		}
		subject := fmt.Sprintf("A card you opened was %s: %s", action, candidate.Title)
		card, ok := cardIdentity(candidate.LocalID, candidate.Title)
		fields := []notificationField{{label: "Project", value: candidate.ProjectName}}
		ctaLabel, ctaURL := "View project", n.projectURL(candidate.ProjectSlug)
		if ok {
			fields = append([]notificationField{{label: "Card", value: card}}, fields...)
			ctaLabel, ctaURL = "View card", n.cardURL(candidate.ProjectSlug, candidate.LocalID)
		}
		fields = appendMoveStatusField(fields, candidate.ActivityReason, candidate.FromName, candidate.ToName)
		body := formatPlainTextNotification(plainTextNotification{
			heading: "Card " + action, fields: fields, ctaLabel: ctaLabel, ctaURL: ctaURL,
		})
		return subject, body, true
	case emailCategoryCardActivity:
		info, ok := refreshReasonInfo[candidate.ActivityReason]
		if !ok || info.category != emailCategoryCardActivity {
			return "", "", false
		}
		entityLabel, entityValue, suffix, enriched := activityEntity(candidate.ActivityReason, refresh.Entity{
			LocalID: candidate.LocalID,
			Title:   candidate.Title,
		})
		subject := fmt.Sprintf("%s: %s", candidate.ProjectName, info.subject)
		if enriched && suffix != "" {
			subject = fmt.Sprintf("%s: %s — %s", candidate.ProjectName, info.subject, suffix)
		}
		fields := make([]notificationField, 0, 2)
		if enriched {
			fields = append(fields, notificationField{label: entityLabel, value: entityValue})
		}
		fields = append(fields, notificationField{label: "Project", value: candidate.ProjectName})
		fields = appendMoveStatusField(fields, candidate.ActivityReason, candidate.FromName, candidate.ToName)
		ctaLabel, ctaURL := "View project", n.projectURL(candidate.ProjectSlug)
		if enriched && isLiveCardReason(candidate.ActivityReason) {
			ctaLabel, ctaURL = "View card", n.cardURL(candidate.ProjectSlug, candidate.LocalID)
		}
		return subject, formatPlainTextNotification(plainTextNotification{
			heading: sentenceHeading(info.subject), fields: fields, ctaLabel: ctaLabel, ctaURL: ctaURL,
		}), true
	default:
		return "", "", false
	}
}

func (n *emailNotifier) handleRefreshNeeded(ctx context.Context, e eventbus.Event) {
	var p refreshNeededPayload
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
	n.handleActivity(ctx, e.ProjectID, p.Reason, p.ActorUserID, refresh.Entity{
		LocalID:  p.LocalID,
		Title:    p.Title,
		Name:     p.Name,
		FromName: p.FromName,
		ToName:   p.ToName,
	}, excluded)
}

func (n *emailNotifier) handleActivity(ctx context.Context, projectID int64, reason string, actorUserID int64, entity refresh.Entity, excluded map[int64]bool) {
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
	subject := fmt.Sprintf("%s: %s", proj.Name, info.subject)
	entityLabel, entityValue, suffix, enriched := activityEntity(reason, entity)
	if enriched && suffix != "" {
		subject = fmt.Sprintf("%s: %s — %s", proj.Name, info.subject, suffix)
	}
	fields := make([]notificationField, 0, 4)
	fields = append(fields, notificationField{label: info.actorLabel, value: actorName})
	if enriched {
		fields = append(fields, notificationField{label: entityLabel, value: entityValue})
	}
	fields = append(fields, notificationField{label: "Project", value: proj.Name})
	fields = appendMoveStatusField(fields, reason, entity.FromName, entity.ToName)
	ctaLabel, ctaURL := "View project", n.projectURL(proj.Slug)
	if enriched && isLiveCardReason(reason) {
		ctaLabel, ctaURL = "View card", n.cardURL(proj.Slug, entity.LocalID)
	}
	body := formatPlainTextNotification(plainTextNotification{
		heading: sentenceHeading(info.subject), fields: fields, ctaLabel: ctaLabel, ctaURL: ctaURL,
	})
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

func appendMoveStatusField(fields []notificationField, reason, fromName, toName string) []notificationField {
	if reason != todoapp.RefreshReasonTodoMoved {
		return fields
	}
	fromName, toName = strings.TrimSpace(fromName), strings.TrimSpace(toName)
	if fromName == "" || toName == "" || fromName == toName {
		return fields
	}
	return append(fields, notificationField{label: "Status", value: fromName + " → " + toName})
}

func cardIdentity(localID int64, title string) (string, bool) {
	title = strings.TrimSpace(title)
	if localID <= 0 || title == "" {
		return "", false
	}
	return fmt.Sprintf("#%d %s", localID, title), true
}

func activityEntity(reason string, entity refresh.Entity) (label, value, subjectSuffix string, ok bool) {
	switch reason {
	case "todo_created", "todo_updated", "todo_moved", "todo_deleted", "todo_links_updated":
		card, ok := cardIdentity(entity.LocalID, entity.Title)
		if !ok {
			return "", "", "", false
		}
		return "Card", card, card, true
	case "sprint_created", "sprint_updated", "sprint_deleted", "sprint_closed":
		name := strings.TrimSpace(entity.Name)
		if name == "" {
			return "", "", "", false
		}
		return "Sprint", name, name, true
	case "workflow_column_added", "workflow_column_updated":
		name := strings.TrimSpace(entity.Name)
		if name == "" {
			return "", "", "", false
		}
		return "Column", name, name, true
	case "tag_color_updated", "tag_deleted":
		name := strings.TrimSpace(entity.Name)
		if name == "" {
			return "", "", "", false
		}
		return "Tag", name, name, true
	}
	return "", "", "", false
}

func isLiveCardReason(reason string) bool {
	switch reason {
	case "todo_created", "todo_updated", "todo_moved", "todo_links_updated":
		return true
	default:
		return false
	}
}

func sentenceHeading(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return ""
	}
	return strings.ToUpper(subject[:1]) + subject[1:]
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
	body := formatPlainTextNotification(plainTextNotification{
		heading: "Project deleted",
		fields:  []notificationField{{label: "Project", value: deleted.Name}},
	})
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
	body := formatPlainTextNotification(plainTextNotification{
		heading:  "Added to project",
		fields:   []notificationField{{label: "Project", value: proj.Name}},
		ctaLabel: "View project",
		ctaURL:   n.projectURL(proj.Slug),
	})
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

func (n *emailNotifier) cardURL(slug string, localID int64) string {
	return fmt.Sprintf("%s/t/%d", n.projectURL(slug), localID)
}

func (n *emailNotifier) send(to, subject, body, logRef string) bool {
	return n.mailQueue.Enqueue(mailDelivery{
		To:      to,
		Subject: subject,
		Body:    body,
		LogRef:  logRef,
	})
}
