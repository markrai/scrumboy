package store

// TodoEditScope indicates how much an actor may edit a todo.
type TodoEditScope int

const (
	TodoEditNone TodoEditScope = iota
	TodoEditBodyOnly
	TodoEditFull
)

// CanCreateTodo returns true if the role may create todos.
func CanCreateTodo(role ProjectRole) bool {
	return role == RoleMaintainer
}

// CanDeleteTodo returns true if the role may delete todos.
func CanDeleteTodo(role ProjectRole) bool {
	return role == RoleMaintainer
}

// CanMoveTodo returns true if the role may move todos.
func CanMoveTodo(role ProjectRole) bool {
	return role == RoleMaintainer
}

// GetTodoEditScope returns the edit scope for the given actor on the given todo.
// Maintainer: full edit. Contributor: body-only if assigned to this todo; else none.
func GetTodoEditScope(role ProjectRole, userID int64, todo *Todo) TodoEditScope {
	if role == RoleMaintainer {
		return TodoEditFull
	}
	if role == RoleContributor {
		if todo.AssigneeUserID == nil {
			return TodoEditNone
		}
		if *todo.AssigneeUserID == userID {
			return TodoEditBodyOnly
		}
	}
	return TodoEditNone
}
