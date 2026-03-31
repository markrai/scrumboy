package mcp

import "context"

type toolHandler func(ctx context.Context, input any) (any, map[string]any, *adapterError)

type toolRegistry map[string]toolHandler

func (a *Adapter) registerTools() {
	a.tools["system.getCapabilities"] = a.handleSystemGetCapabilities
	a.tools["projects.list"] = a.handleProjectsList
	a.tools["todos.create"] = a.handleTodosCreate
	a.tools["todos.get"] = a.handleTodosGet
	a.tools["todos.search"] = a.handleTodosSearch
	a.tools["todos.update"] = a.handleTodosUpdate
	a.tools["todos.delete"] = a.handleTodosDelete
	a.tools["todos.move"] = a.handleTodosMove
	a.tools["sprints.list"] = a.handleSprintsList
	a.tools["sprints.get"] = a.handleSprintsGet
	a.tools["sprints.getActive"] = a.handleSprintsGetActive
	a.tools["sprints.create"] = a.handleSprintsCreate
	a.tools["sprints.activate"] = a.handleSprintsActivate
	a.tools["sprints.close"] = a.handleSprintsClose
	a.tools["sprints.update"] = a.handleSprintsUpdate
	a.tools["sprints.delete"] = a.handleSprintsDelete
	a.tools["tags.listProject"] = a.handleTagsListProject
	a.tools["tags.listMine"] = a.handleTagsListMine
	a.tools["tags.updateMineColor"] = a.handleTagsUpdateMineColor
}
