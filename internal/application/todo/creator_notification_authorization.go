package todo

import (
	"context"

	"scrumboy/internal/store"
)

// CreatorNotificationAuthorizationStore supplies the current facts needed to
// turn historical attribution into a point-in-time recipient decision.
type CreatorNotificationAuthorizationStore interface {
	GetTodoCreatorNotificationFacts(ctx context.Context, projectID, todoID, recipientUserID int64) (store.TodoCreatorNotificationFacts, bool, error)
}

// AuthorizedCreatorNotification is a creator-specific, point-in-time decision
// that the historical creator currently has access to the durable project. It
// does not assert notification preferences, queueing, sending, or delivery.
type AuthorizedCreatorNotification struct {
	ProjectID             int64
	ProjectSlug           string
	ProjectName           string
	TodoID                int64
	LocalID               int64
	Title                 string
	ActivityReason        string
	FromName              string
	ToName                string
	RecipientUserID       int64
	ActorUserID           int64
	MaterialChanged       bool
	AssignmentChanged     bool
	ToAssigneeUserID      *int64
	CardActivityCandidate bool
}

type CreatorNotificationRecipientAccess struct {
	Notification   AuthorizedCreatorNotification
	CurrentCreator bool
}

type CreatorNotificationAuthorizationService struct {
	store CreatorNotificationAuthorizationStore
}

func NewCreatorNotificationAuthorizationService(access CreatorNotificationAuthorizationStore) *CreatorNotificationAuthorizationService {
	return &CreatorNotificationAuthorizationService{store: access}
}

// Authorize resolves current access when the request is consumed. Historical
// attribution merely nominates the candidate; a viewer-or-higher membership
// on the current durable project is required to produce a recipient.
//
// The bool is false for ordinary denials. Store lookup errors are returned so
// the adapter can observe them, but callers must still fail closed and must not
// let them alter an already-successful todo mutation.
func (s *CreatorNotificationAuthorizationService) Authorize(
	ctx context.Context,
	request CreatorNotificationRequest,
) (AuthorizedCreatorNotification, bool, error) {
	if s == nil || s.store == nil || !validCreatorNotificationAuthorizationRequest(request) {
		return AuthorizedCreatorNotification{}, false, nil
	}

	access, ok, err := s.authorizeCurrentRecipientAccess(ctx, AuthorizedCreatorNotification{
		ProjectID:             request.ProjectID,
		ProjectSlug:           request.ProjectSlug,
		TodoID:                request.TodoID,
		LocalID:               request.LocalID,
		Title:                 request.Title,
		ActivityReason:        request.ActivityReason,
		FromName:              request.FromName,
		ToName:                request.ToName,
		RecipientUserID:       request.CreatedByUserID,
		ActorUserID:           request.ActorUserID,
		MaterialChanged:       request.MaterialChanged,
		AssignmentChanged:     request.AssignmentChanged,
		ToAssigneeUserID:      cloneUpdateInt64Ptr(request.ToAssigneeUserID),
		CardActivityCandidate: request.CardActivityCandidate,
	})
	if err != nil || !ok || !access.CurrentCreator {
		return AuthorizedCreatorNotification{}, false, err
	}
	return access.Notification, true, nil
}

// ReauthorizeRecipient performs the fresh access check required immediately
// before creator-directed disclosure. An earlier AuthorizedCreatorNotification
// is only a point-in-time fact and is never treated as a durable entitlement.
func (s *CreatorNotificationAuthorizationService) ReauthorizeRecipient(
	ctx context.Context,
	authorized AuthorizedCreatorNotification,
) (AuthorizedCreatorNotification, bool, error) {
	if s == nil || s.store == nil || !validAuthorizedCreatorNotification(authorized) {
		return AuthorizedCreatorNotification{}, false, nil
	}
	access, ok, err := s.authorizeCurrentRecipientAccess(ctx, authorized)
	if err != nil || !ok || !access.CurrentCreator {
		return AuthorizedCreatorNotification{}, false, err
	}
	return access.Notification, true, nil
}

func (s *CreatorNotificationAuthorizationService) ReauthorizeRecipientAccess(
	ctx context.Context,
	candidate AuthorizedCreatorNotification,
) (CreatorNotificationRecipientAccess, bool, error) {
	if s == nil || s.store == nil || !validAuthorizedCreatorNotification(candidate) {
		return CreatorNotificationRecipientAccess{}, false, nil
	}
	return s.authorizeCurrentRecipientAccess(ctx, candidate)
}

func (s *CreatorNotificationAuthorizationService) authorizeCurrentRecipientAccess(
	ctx context.Context,
	candidate AuthorizedCreatorNotification,
) (CreatorNotificationRecipientAccess, bool, error) {
	facts, found, err := s.store.GetTodoCreatorNotificationFacts(
		ctx,
		candidate.ProjectID,
		candidate.TodoID,
		candidate.RecipientUserID,
	)
	if err != nil {
		return CreatorNotificationRecipientAccess{}, false, err
	}
	if !found ||
		facts.ProjectID != candidate.ProjectID ||
		!facts.DurableProject ||
		facts.ProjectSlug == "" ||
		facts.TodoID != candidate.TodoID ||
		facts.TodoProjectID != candidate.ProjectID ||
		facts.TodoLocalID != candidate.LocalID ||
		!facts.RecipientRole.HasMinimumRole(store.RoleViewer) {
		return CreatorNotificationRecipientAccess{}, false, nil
	}

	currentCreator := facts.CreatedByUserID != nil && *facts.CreatedByUserID == candidate.RecipientUserID
	return CreatorNotificationRecipientAccess{Notification: AuthorizedCreatorNotification{
		ProjectID:             facts.ProjectID,
		ProjectSlug:           facts.ProjectSlug,
		ProjectName:           facts.ProjectName,
		TodoID:                candidate.TodoID,
		LocalID:               candidate.LocalID,
		Title:                 candidate.Title,
		ActivityReason:        candidate.ActivityReason,
		FromName:              candidate.FromName,
		ToName:                candidate.ToName,
		RecipientUserID:       candidate.RecipientUserID,
		ActorUserID:           candidate.ActorUserID,
		MaterialChanged:       candidate.MaterialChanged,
		AssignmentChanged:     candidate.AssignmentChanged,
		ToAssigneeUserID:      cloneUpdateInt64Ptr(candidate.ToAssigneeUserID),
		CardActivityCandidate: candidate.CardActivityCandidate,
	}, CurrentCreator: currentCreator}, true, nil
}

func validCreatorNotificationAuthorizationRequest(request CreatorNotificationRequest) bool {
	if request.ProjectID <= 0 || request.TodoID <= 0 || request.LocalID <= 0 ||
		request.CreatedByUserID <= 0 || request.ActorUserID <= 0 ||
		request.CreatedByUserID == request.ActorUserID {
		return false
	}
	return request.ActivityReason == RefreshReasonTodoUpdated || request.ActivityReason == RefreshReasonTodoMoved
}

func validAuthorizedCreatorNotification(authorized AuthorizedCreatorNotification) bool {
	if authorized.ProjectID <= 0 || authorized.TodoID <= 0 || authorized.LocalID <= 0 ||
		authorized.RecipientUserID <= 0 || authorized.ActorUserID <= 0 ||
		authorized.RecipientUserID == authorized.ActorUserID {
		return false
	}
	return authorized.ActivityReason == RefreshReasonTodoUpdated || authorized.ActivityReason == RefreshReasonTodoMoved
}
