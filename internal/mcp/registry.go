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

	a.registerLegacyToolAliases()
}

// legacyToolAliases maps the deprecated dot-separated MCP tool names (used before
// the rename to underscore-separated names) to their current names. Claude's MCP
// client validates every tool name in tools/list against
// ^[a-zA-Z0-9_-]{1,64}$, which dots fail, so the dotted names were dropped from
// discovery. These are permanent backward-compatibility aliases so existing
// external MCP callers that still hardcode the old dotted names (via tools/call
// or the legacy POST /mcp {"tool": "..."} endpoint) keep working.
//
// This is dispatch-only: aliases are registered directly into a.tools and are
// deliberately NOT added to implementedTools()/toolCatalog(), so they never
// appear in tools/list or system_getCapabilities. The aliases are retained
// indefinitely (no planned removal, since the population of external callers
// still depending on the dotted names is not observable) -- see docs/mcp.md and
// CHANGELOG.md.
var legacyToolAliases = map[string]string{
	"system.getCapabilities":  "system_getCapabilities",
	"projects.list":           "projects_list",
	"todos.create":            "todos_create",
	"todos.get":               "todos_get",
	"todos.search":            "todos_search",
	"todos.update":            "todos_update",
	"todos.delete":            "todos_delete",
	"todos.move":              "todos_move",
	"sprints.list":            "sprints_list",
	"sprints.get":             "sprints_get",
	"sprints.getActive":       "sprints_getActive",
	"sprints.create":          "sprints_create",
	"sprints.activate":        "sprints_activate",
	"sprints.close":           "sprints_close",
	"sprints.update":          "sprints_update",
	"sprints.delete":          "sprints_delete",
	"tags.listProject":        "tags_listProject",
	"tags.listMine":           "tags_listMine",
	"tags.updateMineColor":    "tags_updateMineColor",
	"tags.deleteMine":         "tags_deleteMine",
	"tags.updateProjectColor": "tags_updateProjectColor",
	"tags.deleteProject":      "tags_deleteProject",
	"members.list":            "members_list",
	"members.listAvailable":   "members_listAvailable",
	"members.add":             "members_add",
	"members.updateRole":      "members_updateRole",
	"members.remove":          "members_remove",
	"board.get":               "board_get",
}

// registerLegacyToolAliases wires the deprecated dotted tool names to the same
// handlers as their underscore-separated replacements. Dispatch-only -- see
// legacyToolAliases doc comment above.
func (a *Adapter) registerLegacyToolAliases() {
	for oldName, newName := range legacyToolAliases {
		if handler, ok := a.tools[newName]; ok {
			a.tools[oldName] = handler
		}
	}
}
