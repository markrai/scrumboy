package eventbus

const TodoCreatorNotificationRecipientAuthorizedEventType = "todo.creator_notification_recipient_authorized"

// TodoCreatorNotificationRecipientAuthorizedPayload records that a fresh,
// point-in-time project access check resolved the historical creator as a
// current recipient. It does not assert preferences, queueing, sending, or
// delivery. It remains internal; channel adapters must freshly reauthorize
// before any disclosure rather than exposing this event directly.
type TodoCreatorNotificationRecipientAuthorizedPayload struct {
	ProjectID             int64  `json:"projectId"`
	ProjectSlug           string `json:"projectSlug"`
	TodoID                int64  `json:"todoId"`
	LocalID               int64  `json:"localId"`
	Title                 string `json:"title"`
	ActivityReason        string `json:"activityReason"`
	RecipientUserID       int64  `json:"recipientUserId"`
	ActorUserID           int64  `json:"actorUserId"`
	MaterialChanged       bool   `json:"materialChanged"`
	AssignmentChanged     bool   `json:"assignmentChanged"`
	ToAssigneeUserID      *int64 `json:"toAssigneeUserId,omitempty"`
	CardActivityCandidate bool   `json:"cardActivityCandidate"`
}
