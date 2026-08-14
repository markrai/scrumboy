package eventbus

// TodoCreatorNotifiedPayload is the JSON shape for domain event type "todo.creator_notified"
// (event bus payload). Fired whenever a todo is updated by someone other than its creator, so
// the creator can be notified (email + in-app), independent of whether the assignee also changed.
// Keep in sync with:
//   - httpapi.Server.PublishTodoCreatorNotified (marshal)
//   - httpapi.sseBridge todo.creator_notified branch (unmarshal → SSE wire)
type TodoCreatorNotifiedPayload struct {
	ProjectID      int64  `json:"projectId"`
	ProjectSlug    string `json:"projectSlug,omitempty"`
	TodoID         int64  `json:"todoId"`
	LocalID        int64  `json:"localId"`
	Title          string `json:"title"`
	ActivityReason string `json:"activityReason,omitempty"`
	CreatedByUID   int64  `json:"createdByUserId"`
	ActorUserID    int64  `json:"actorUserId"`
}
