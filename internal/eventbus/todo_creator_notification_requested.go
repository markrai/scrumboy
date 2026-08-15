package eventbus

const TodoCreatorNotificationRequestedEventType = "todo.creator_notification_requested"

// TodoCreatorNotificationRequestedPayload is the factual JSON payload for an
// internal request to consider a todo's historical creator for later delivery.
// It does not assert current access, preference eligibility, or delivery.
type TodoCreatorNotificationRequestedPayload struct {
	ProjectID             int64  `json:"projectId"`
	ProjectSlug           string `json:"projectSlug"`
	TodoID                int64  `json:"todoId"`
	LocalID               int64  `json:"localId"`
	Title                 string `json:"title"`
	ActivityReason        string `json:"activityReason"`
	CreatedByUserID       int64  `json:"createdByUserId"`
	ActorUserID           int64  `json:"actorUserId"`
	MaterialChanged       bool   `json:"materialChanged"`
	AssignmentChanged     bool   `json:"assignmentChanged"`
	ToAssigneeUserID      *int64 `json:"toAssigneeUserId,omitempty"`
	CardActivityCandidate bool   `json:"cardActivityCandidate"`
}
