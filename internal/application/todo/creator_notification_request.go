package todo

import (
	"context"

	"scrumboy/internal/store"
)

// CreatorNotificationRequest is an internal request to consider notifying a
// todo's historical creator about a committed mutation. It does not assert
// current access, preference eligibility, queueing, sending, or delivery.
type CreatorNotificationRequest struct {
	ProjectID       int64
	ProjectSlug     string
	TodoID          int64
	LocalID         int64
	Title           string
	ActivityReason  string
	CreatedByUserID int64
	ActorUserID     int64
}

// CreatorNotificationRequestPublisher receives best-effort, post-commit
// requests. Publication must not change an already-successful mutation result.
type CreatorNotificationRequestPublisher interface {
	PublishCreatorNotificationRequest(context.Context, CreatorNotificationRequest)
}

// CreatorNotificationRequestPublisherFunc adapts a function to the publisher
// capability used by prepared todo mutation services.
type CreatorNotificationRequestPublisherFunc func(context.Context, CreatorNotificationRequest)

func (f CreatorNotificationRequestPublisherFunc) PublishCreatorNotificationRequest(ctx context.Context, request CreatorNotificationRequest) {
	if f != nil {
		f(ctx, request)
	}
}

type nopCreatorNotificationRequestPublisher struct{}

func (nopCreatorNotificationRequestPublisher) PublishCreatorNotificationRequest(context.Context, CreatorNotificationRequest) {
}

// publishCreatorNotificationRequest applies the feature-local request rules.
// Current membership is deliberately not checked here: CreatedByUserID is
// historical attribution, and a later delivery phase must authorize the
// recipient immediately before disclosing project or todo data.
func publishCreatorNotificationRequest(
	ctx context.Context,
	publisher CreatorNotificationRequestPublisher,
	project store.Project,
	todo store.Todo,
	activityReason string,
) {
	if project.ExpiresAt != nil || !shouldRequestCreatorNotification(ctx, todo) {
		return
	}
	actorUserID, _ := store.UserIDFromContext(ctx)

	publisher.PublishCreatorNotificationRequest(ctx, CreatorNotificationRequest{
		ProjectID:       project.ID,
		ProjectSlug:     project.Slug,
		TodoID:          todo.ID,
		LocalID:         todo.LocalID,
		Title:           todo.Title,
		ActivityReason:  activityReason,
		CreatedByUserID: *todo.CreatedByUserID,
		ActorUserID:     actorUserID,
	})
}

// shouldRequestCreatorNotification applies the predicates that can be decided
// from committed todo facts and the prepared actor context. Legacy global-ID
// services use it before their ancillary project lookup so ineligible
// mutations retain their existing post-write read behavior.
func shouldRequestCreatorNotification(ctx context.Context, todo store.Todo) bool {
	if todo.CreatedByUserID == nil {
		return false
	}
	actorUserID, ok := store.UserIDFromContext(ctx)
	return ok && actorUserID > 0 && actorUserID != *todo.CreatedByUserID
}
