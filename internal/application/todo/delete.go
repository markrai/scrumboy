package todo

import (
	"context"

	"scrumboy/internal/store"
)

const RefreshReasonTodoDeleted = "todo_deleted"

// DeleteCommand identifies a todo by its project-local ID within the project
// already bound by a prepared deletion service.
type DeleteCommand struct {
	LocalID int64
}

// DeleteStore is the persistence capability required to delete a todo
// addressed by its project-local identifier. Authorization, audit, related
// data cleanup, project touch, and board activity remain store-owned.
type DeleteStore interface {
	DeleteTodoByLocalID(
		ctx context.Context,
		projectID int64,
		localID int64,
		mode store.Mode,
	) error
}
