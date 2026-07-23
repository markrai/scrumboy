package mcp

import "context"

type toolHandler func(ctx context.Context, input any) (any, map[string]any, *adapterError)

type toolRegistry map[string]toolHandler

func (a *Adapter) registerTools() {
	a.tools["system_getCapabilities"] = a.handleSystemGetCapabilities
	a.tools["projects_list"] = a.handleProjectsList
	a.tools["todos_create"] = a.handleTodosCreate
	a.tools["todos_get"] = a.handleTodosGet
	a.tools["todos_search"] = a.handleTodosSearch
	a.tools["todos_update"] = a.handleTodosUpdate
	a.tools["todos_delete"] = a.handleTodosDelete
	a.tools["todos_move"] = a.handleTodosMove
	a.tools["sprints_list"] = a.handleSprintsList
	a.tools["sprints_get"] = a.handleSprintsGet
	a.tools["sprints_getActive"] = a.handleSprintsGetActive
	a.tools["sprints_create"] = a.handleSprintsCreate
	a.tools["sprints_activate"] = a.handleSprintsActivate
	a.tools["sprints_close"] = a.handleSprintsClose
	a.tools["sprints_update"] = a.handleSprintsUpdate
	a.tools["sprints_delete"] = a.handleSprintsDelete
	a.tools["tags_listProject"] = a.handleTagsListProject
	a.tools["tags_listMine"] = a.handleTagsListMine
	a.tools["tags_updateMineColor"] = a.handleTagsUpdateMineColor
	a.tools["tags_deleteMine"] = a.handleTagsDeleteMine
	a.tools["tags_updateProjectColor"] = a.handleTagsUpdateProjectColor
	a.tools["tags_deleteProject"] = a.handleTagsDeleteProject
	a.tools["members_list"] = a.handleMembersList
	a.tools["members_listAvailable"] = a.handleMembersListAvailable
	a.tools["members_add"] = a.handleMembersAdd
	a.tools["members_updateRole"] = a.handleMembersUpdateRole
	a.tools["members_remove"] = a.handleMembersRemove
	a.tools["board_get"] = a.handleBoardGet
}
