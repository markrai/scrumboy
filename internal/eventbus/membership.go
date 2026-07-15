package eventbus

// MembershipPayload is the JSON shape for domain event type "project.membership".
// Emitted alongside "board.members_updated" whenever a project's membership
// changes, carrying enough detail for non-realtime consumers (e.g. the email
// notifier) to target the affected user without re-querying membership state.
type MembershipPayload struct {
	ProjectID      int64  `json:"projectId"`
	AffectedUserID int64  `json:"affectedUserId"`
	Action         string `json:"action"` // "added" | "removed" | "role_changed"
	ActorUserID    int64  `json:"actorUserId"`
}
