package eventbus

// TodoAssignedPayload is the JSON shape for domain event type "todo.assigned" (event bus payload).
// Keep in sync with:
//   - httpapi.Server.PublishTodoAssigned (marshal)
//   - httpapi.sseBridge todo.assigned branch (unmarshal → SSE wire)
type TodoAssignedPayload struct {
	ProjectID       int64  `json:"projectId"`
	ProjectSlug     string `json:"projectSlug,omitempty"`
	TodoID          int64  `json:"todoId"`
	LocalID         int64  `json:"localId"`
	Title           string `json:"title"`
	Reason          string `json:"reason,omitempty"`
	FromAssigneeUID *int64 `json:"fromAssigneeUserId,omitempty"`
	ToAssigneeUID   *int64 `json:"toAssigneeUserId,omitempty"`
	ActorUserID     int64  `json:"actorUserId"`
}
