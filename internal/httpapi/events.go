package httpapi

import (
	"context"
	"encoding/json"

	membershipapp "scrumboy/internal/application/membership"
	"scrumboy/internal/application/refresh"
	sprintapp "scrumboy/internal/application/sprint"
	todolinkapp "scrumboy/internal/application/todolink"
	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

type todoLinkMutationPublisher struct {
	server *Server
}

var _ todolinkapp.RESTMutationPublisher = todoLinkMutationPublisher{}

func (p todoLinkMutationPublisher) PublishTodoLinksUpdated(ctx context.Context, projectID int64) {
	p.server.emitRefreshNeeded(ctx, projectID, "todo_links_updated", refresh.Entity{})
}

type sprintDefinitionPublisher struct {
	server *Server
}

var _ sprintapp.RESTDefinitionPublisher = sprintDefinitionPublisher{}

func (p sprintDefinitionPublisher) PublishSprintCreated(ctx context.Context, projectID int64, name string) {
	p.server.emitRefreshNeeded(ctx, projectID, "sprint_created", refresh.Entity{Name: name})
}

func (p sprintDefinitionPublisher) PublishSprintUpdated(ctx context.Context, projectID int64, name string) {
	p.server.emitRefreshNeeded(ctx, projectID, "sprint_updated", refresh.Entity{Name: name})
}

type sprintTransitionPublisher struct {
	server *Server
}

var _ sprintapp.RESTTransitionPublisher = sprintTransitionPublisher{}

func (p sprintTransitionPublisher) PublishSprintActivated(ctx context.Context, projectID int64) {
	p.server.emitRefreshNeeded(ctx, projectID, "sprint_activated", refresh.Entity{})
}

func (p sprintTransitionPublisher) PublishSprintClosed(ctx context.Context, projectID int64, name string) {
	p.server.emitRefreshNeeded(ctx, projectID, "sprint_closed", refresh.Entity{Name: name})
}

type sprintDeletionPublisher struct {
	server *Server
}

var _ sprintapp.RESTDeletionPublisher = sprintDeletionPublisher{}

func (p sprintDeletionPublisher) PublishSprintDeleted(ctx context.Context, projectID int64, name string) {
	p.server.emitRefreshNeeded(ctx, projectID, "sprint_deleted", refresh.Entity{Name: name})
}

type membershipMutationPublisher struct {
	server *Server
}

var _ membershipapp.RESTMutationPublisher = membershipMutationPublisher{}

func (p membershipMutationPublisher) PublishMembersUpdated(ctx context.Context, projectID int64) {
	p.server.emitMembersUpdated(ctx, projectID)
}

func (p membershipMutationPublisher) PublishMembershipChanged(
	ctx context.Context,
	projectID int64,
	actorUserID int64,
	targetUserID int64,
	action membershipapp.MembershipAction,
) {
	p.server.emitMembership(ctx, projectID, actorUserID, targetUserID, string(action))
}

type refreshNeededPayload struct {
	Reason      string `json:"reason"`
	ActorUserID int64  `json:"actorUserId,omitempty"`
	LocalID     int64  `json:"localId,omitempty"`
	Title       string `json:"title,omitempty"`
	Name        string `json:"name,omitempty"`
}

type refreshNeededEvent struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type"`
	ProjectID int64  `json:"projectId"`
	Reason    string `json:"reason,omitempty"`
}

type membersUpdatedEvent struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type"`
	ProjectID int64  `json:"projectId"`
}

// emitRefreshNeeded is the generic board invalidation signal for board-affecting
// mutations and settings changes. `reason` is carried through to the SSE wire
// payload for characterization/debugging; the current frontend does not branch
// on it when deciding whether to reload the board. `actorUserId` (best-effort,
// from the ambient request actor) lets non-realtime consumers such as the email
// notifier skip notifying the person who made the change.
//
// Entity metadata is optional internal notification context. It is carried on
// board.refresh_needed for in-process consumers such as email only. The SSE
// bridge forwards reason alone, and board.refresh_needed is excluded from
// webhook delivery.
func (s *Server) emitRefreshNeeded(ctx context.Context, projectID int64, reason string, entity refresh.Entity) {
	var actorUserID int64
	if uid, ok := store.UserIDFromContext(ctx); ok {
		actorUserID = uid
	}
	payload, _ := json.Marshal(refreshNeededPayload{
		Reason:      reason,
		ActorUserID: actorUserID,
		LocalID:     entity.LocalID,
		Title:       entity.Title,
		Name:        entity.Name,
	})
	s.PublishEvent(ctx, eventbus.Event{
		Type:      "board.refresh_needed",
		ProjectID: projectID,
		Payload:   payload,
	})
}

// emitTagDeletedRefresh emits a "tag_deleted" refresh for the current project plus
// every other project affected by a cross-project personal-tag deletion. Deleting a
// personal tag row removes it from every project that reused it, so all their boards
// must refresh, not only the one the request targeted.
func (s *Server) emitTagDeletedRefresh(ctx context.Context, projectID int64, affectedProjectIDs []int64, entity refresh.Entity) {
	emitted := make(map[int64]struct{}, len(affectedProjectIDs)+1)
	s.emitRefreshNeeded(ctx, projectID, "tag_deleted", entity)
	emitted[projectID] = struct{}{}
	for _, pid := range affectedProjectIDs {
		if _, ok := emitted[pid]; ok {
			continue
		}
		emitted[pid] = struct{}{}
		s.emitRefreshNeeded(ctx, pid, "tag_deleted", entity)
	}
}

func (s *Server) emitProjectDeleted(ctx context.Context, deleted store.DeletedProjectSnapshot) {
	var actorUserID int64
	if uid, ok := store.UserIDFromContext(ctx); ok {
		actorUserID = uid
	}
	s.emitRefreshNeeded(ctx, deleted.ProjectID, "project_deleted", refresh.Entity{})
	if s.emailNotifier != nil {
		s.emailNotifier.OnProjectDeleted(deleted, actorUserID)
	}
}

func (s *Server) emitMembersUpdated(ctx context.Context, projectID int64) {
	s.PublishEvent(ctx, eventbus.Event{
		Type:      "board.members_updated",
		ProjectID: projectID,
	})
}

// emitMembership publishes a per-user membership change, distinct from the
// board-wide "board.members_updated" SSE invalidation signal, so consumers
// like the email notifier can target the one affected user.
func (s *Server) emitMembership(
	ctx context.Context,
	projectID int64,
	actorUserID int64,
	affectedUserID int64,
	action string,
) {
	payload, _ := json.Marshal(eventbus.MembershipPayload{
		ProjectID:      projectID,
		AffectedUserID: affectedUserID,
		Action:         action,
		ActorUserID:    actorUserID,
	})
	s.PublishEvent(ctx, eventbus.Event{
		Type:      "project.membership",
		ProjectID: projectID,
		Payload:   payload,
	})
}
