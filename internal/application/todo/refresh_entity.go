package todo

import (
	"scrumboy/internal/application/refresh"
	"scrumboy/internal/store"
)

func todoRefreshEntity(todo store.Todo) refresh.Entity {
	return refresh.Entity{LocalID: todo.LocalID, Title: todo.Title}
}
