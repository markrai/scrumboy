package httpapi

import (
	"context"
	"encoding/json"

	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

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
func (s *Server) emitRefreshNeeded(ctx context.Context, projectID int64, reason string) {
	var actorUserID int64
	if uid, ok := store.UserIDFromContext(ctx); ok {
		actorUserID = uid
	}
	payload, _ := json.Marshal(struct {
		Reason      string `json:"reason"`
		ActorUserID int64  `json:"actorUserId,omitempty"`
	}{Reason: reason, ActorUserID: actorUserID})
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
func (s *Server) emitTagDeletedRefresh(ctx context.Context, projectID int64, affectedProjectIDs []int64) {
	emitted := make(map[int64]struct{}, len(affectedProjectIDs)+1)
	s.emitRefreshNeeded(ctx, projectID, "tag_deleted")
	emitted[projectID] = struct{}{}
	for _, pid := range affectedProjectIDs {
		if _, ok := emitted[pid]; ok {
			continue
		}
		emitted[pid] = struct{}{}
		s.emitRefreshNeeded(ctx, pid, "tag_deleted")
	}
}

func (s *Server) emitProjectDeleted(ctx context.Context, deleted store.DeletedProjectSnapshot) {
	var actorUserID int64
	if uid, ok := store.UserIDFromContext(ctx); ok {
		actorUserID = uid
	}
	s.emitRefreshNeeded(ctx, deleted.ProjectID, "project_deleted")
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
func (s *Server) emitMembership(ctx context.Context, projectID, affectedUserID int64, action string) {
	var actorUserID int64
	if uid, ok := store.UserIDFromContext(ctx); ok {
		actorUserID = uid
	}
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
