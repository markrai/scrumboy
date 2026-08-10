package board

import "scrumboy/internal/store"

func suppressDisabledSprintAssignments(project store.Project, columns map[string][]store.Todo) {
	if project.SprintsEnabled {
		return
	}
	for key, todos := range columns {
		for i := range todos {
			todos[i].SprintID = nil
		}
		columns[key] = todos
	}
}

func suppressDisabledSprintAssignmentsInTodos(project store.Project, todos []store.Todo) {
	if project.SprintsEnabled {
		return
	}
	for i := range todos {
		todos[i].SprintID = nil
	}
}
