package mcp

// mcpToolDef is the MCP-spec shape returned by tools/list for each tool.
type mcpToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// toolCatalogDefinitions holds MCP metadata for every callable tool.
func toolCatalogDefinitions() map[string]mcpToolDef {
	return map[string]mcpToolDef{
		"system.getCapabilities": {
			Name:        "system.getCapabilities",
			Description: "Return adapter capabilities, auth mode, and the list of implemented MCP tools.",
			InputSchema: jsonSchema("object", map[string]any{}, nil),
		},
		"projects.list": {
			Name:        "projects.list",
			Description: "List all projects visible to the authenticated user, with their role in each project.",
			InputSchema: jsonSchema("object", map[string]any{}, nil),
		},
		"todos.create": {
			Name:        "todos.create",
			Description: "Create a new todo item in a project.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug":      jsonProp("string", "Project identifier (slug)"),
				"title":            jsonProp("string", "Title of the todo"),
				"body":             jsonProp("string", "Body or description of the todo"),
				"tags":             jsonArrayProp("string", "Tags to attach to the todo"),
				"columnKey":        jsonProp("string", "Workflow column key (for example backlog, doing, or done)"),
				"estimationPoints": jsonPropWithNull("integer", "Story points estimate"),
				"sprintId":         jsonPropWithNull("integer", "Sprint ID to assign"),
				"assigneeUserId":   jsonPropWithNull("integer", "User ID to assign"),
				"position": jsonObjectProp("Position hint within the target column", map[string]any{
					"afterLocalId":  jsonPropWithNull("integer", "Place after this todo local ID"),
					"beforeLocalId": jsonPropWithNull("integer", "Place before this todo local ID"),
				}, nil),
			}, []string{"projectSlug", "title"}),
		},
		"todos.get": {
			Name:        "todos.get",
			Description: "Get a single todo by its project-scoped local ID.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
				"localId":     jsonProp("integer", "Project-scoped todo ID"),
			}, []string{"projectSlug", "localId"}),
		},
		"todos.search": {
			Name:        "todos.search",
			Description: "Search todo link targets in a project.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug":     jsonProp("string", "Project identifier (slug)"),
				"query":           jsonProp("string", "Search query"),
				"limit":           jsonPropWithNull("integer", "Maximum results to return"),
				"excludeLocalIds": jsonArrayProp("integer", "Todo local IDs to exclude"),
			}, []string{"projectSlug"}),
		},
		"todos.update": {
			Name:        "todos.update",
			Description: "Update fields on an existing todo. Only the fields present in the patch object are changed.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
				"localId":     jsonProp("integer", "Project-scoped todo ID"),
				"patch": jsonObjectProp("Fields to update. Only included fields are changed.", map[string]any{
					"title":            jsonProp("string", "New title"),
					"body":             jsonProp("string", "New body or description"),
					"tags":             jsonArrayProp("string", "Replace tags with this list"),
					"estimationPoints": jsonPropWithNull("integer", "Story points estimate"),
					"assigneeUserId":   jsonPropWithNull("integer", "Assignee user ID"),
					"sprintId":         jsonPropWithNull("integer", "Sprint ID"),
				}, nil),
			}, []string{"projectSlug", "localId", "patch"}),
		},
		"todos.delete": {
			Name:        "todos.delete",
			Description: "Delete a todo by its project-scoped local ID.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
				"localId":     jsonProp("integer", "Project-scoped todo ID"),
			}, []string{"projectSlug", "localId"}),
		},
		"todos.move": {
			Name:        "todos.move",
			Description: "Move a todo to another workflow column, optionally relative to a neighbor.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug":   jsonProp("string", "Project identifier (slug)"),
				"localId":       jsonProp("integer", "Project-scoped todo ID"),
				"toColumnKey":   jsonProp("string", "Target workflow column key"),
				"afterLocalId":  jsonPropWithNull("integer", "Place after this todo local ID"),
				"beforeLocalId": jsonPropWithNull("integer", "Place before this todo local ID"),
			}, []string{"projectSlug", "localId", "toColumnKey"}),
		},
		"sprints.list": {
			Name:        "sprints.list",
			Description: "List sprints for a project.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
			}, []string{"projectSlug"}),
		},
		"sprints.get": {
			Name:        "sprints.get",
			Description: "Get a sprint by ID within a project.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
				"sprintId":    jsonProp("integer", "Sprint ID"),
			}, []string{"projectSlug", "sprintId"}),
		},
		"sprints.getActive": {
			Name:        "sprints.getActive",
			Description: "Get the currently active sprint for a project, if any.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
			}, []string{"projectSlug"}),
		},
		"sprints.create": {
			Name:        "sprints.create",
			Description: "Create a new sprint in a project.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug":    jsonProp("string", "Project identifier (slug)"),
				"name":           jsonProp("string", "Sprint name"),
				"plannedStartAt": jsonProp("string", "Planned start timestamp in RFC3339 format"),
				"plannedEndAt":   jsonProp("string", "Planned end timestamp in RFC3339 format"),
			}, []string{"projectSlug", "name", "plannedStartAt", "plannedEndAt"}),
		},
		"sprints.activate": {
			Name:        "sprints.activate",
			Description: "Activate a planned sprint.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
				"sprintId":    jsonProp("integer", "Sprint ID"),
			}, []string{"projectSlug", "sprintId"}),
		},
		"sprints.close": {
			Name:        "sprints.close",
			Description: "Close an active sprint.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
				"sprintId":    jsonProp("integer", "Sprint ID"),
			}, []string{"projectSlug", "sprintId"}),
		},
		"sprints.update": {
			Name:        "sprints.update",
			Description: "Update a sprint. Only the fields present in patch are changed.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
				"sprintId":    jsonProp("integer", "Sprint ID"),
				"patch": jsonObjectProp("Fields to update on the sprint.", map[string]any{
					"name":           jsonProp("string", "New sprint name"),
					"plannedStartAt": jsonProp("integer", "Planned start timestamp in Unix milliseconds"),
					"plannedEndAt":   jsonProp("integer", "Planned end timestamp in Unix milliseconds"),
				}, nil),
			}, []string{"projectSlug", "sprintId", "patch"}),
		},
		"sprints.delete": {
			Name:        "sprints.delete",
			Description: "Delete a sprint from a project.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
				"sprintId":    jsonProp("integer", "Sprint ID"),
			}, []string{"projectSlug", "sprintId"}),
		},
		"tags.listProject": {
			Name:        "tags.listProject",
			Description: "List project-scoped tags and their counts.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
			}, []string{"projectSlug"}),
		},
		"tags.listMine": {
			Name:        "tags.listMine",
			Description: "List the signed-in user's personal tag library.",
			InputSchema: jsonSchema("object", map[string]any{}, nil),
		},
		"tags.updateMineColor": {
			Name:        "tags.updateMineColor",
			Description: "Set or clear the current user's color preference for a personal tag.",
			InputSchema: jsonSchema("object", map[string]any{
				"tagId": jsonProp("integer", "Tag ID"),
				"color": jsonPropWithNull("string", "Color value; null clears the preference"),
			}, []string{"tagId"}),
		},
		"tags.deleteMine": {
			Name:        "tags.deleteMine",
			Description: "Delete a personal tag.",
			InputSchema: jsonSchema("object", map[string]any{
				"tagId": jsonProp("integer", "Tag ID"),
			}, []string{"tagId"}),
		},
		"tags.updateProjectColor": {
			Name:        "tags.updateProjectColor",
			Description: "Set or clear the project-scoped color for a tag.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
				"tagId":       jsonProp("integer", "Tag ID"),
				"color":       jsonPropWithNull("string", "Color value; null clears the color"),
			}, []string{"projectSlug", "tagId"}),
		},
		"tags.deleteProject": {
			Name:        "tags.deleteProject",
			Description: "Delete a project-scoped tag.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
				"tagId":       jsonProp("integer", "Tag ID"),
			}, []string{"projectSlug", "tagId"}),
		},
		"members.list": {
			Name:        "members.list",
			Description: "List members of a project.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
			}, []string{"projectSlug"}),
		},
		"members.listAvailable": {
			Name:        "members.listAvailable",
			Description: "List users who can be added to a project.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
			}, []string{"projectSlug"}),
		},
		"members.add": {
			Name:        "members.add",
			Description: "Add a user to a project with the given role.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
				"userId":      jsonProp("integer", "User ID"),
				"role":        jsonProp("string", "Project role"),
			}, []string{"projectSlug", "userId", "role"}),
		},
		"members.updateRole": {
			Name:        "members.updateRole",
			Description: "Change a project member's role.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
				"userId":      jsonProp("integer", "User ID"),
				"role":        jsonProp("string", "Project role"),
			}, []string{"projectSlug", "userId", "role"}),
		},
		"members.remove": {
			Name:        "members.remove",
			Description: "Remove a user from a project.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
				"userId":      jsonProp("integer", "User ID"),
			}, []string{"projectSlug", "userId"}),
		},
		"board.get": {
			Name:        "board.get",
			Description: "Get board columns and paginated todo items for a project board view.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
				"tag":         jsonProp("string", "Filter by tag"),
				"search":      jsonProp("string", "Filter by search text"),
				"sprintId":    jsonPropWithNull("integer", "Filter to a sprint"),
				"limit":       jsonProp("integer", "Maximum items per column"),
				"cursorByColumn": map[string]any{
					"type":                 "object",
					"description":          "Pagination cursor token per workflow column key",
					"additionalProperties": map[string]any{"type": "string"},
				},
			}, []string{"projectSlug"}),
		},
	}
}

func (a *Adapter) toolCatalog() []mcpToolDef {
	defs := toolCatalogDefinitions()
	tools := make([]mcpToolDef, 0, len(a.tools))
	for _, name := range a.implementedTools() {
		if def, ok := defs[name]; ok {
			tools = append(tools, def)
		}
	}
	return tools
}

func (a *Adapter) toolDefinition(name string) (mcpToolDef, bool) {
	def, ok := toolCatalogDefinitions()[name]
	return def, ok
}

// jsonSchema builds a JSON Schema object with additionalProperties: false on the root,
// matching decodeInput/DisallowUnknownFields behavior for tool arguments.
func jsonSchema(typ string, properties map[string]any, required []string) map[string]any {
	s := map[string]any{
		"type":                 typ,
		"additionalProperties": false,
	}
	if properties != nil {
		s["properties"] = properties
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func jsonProp(typ, description string) map[string]any {
	return map[string]any{"type": typ, "description": description}
}

func jsonPropWithNull(typ, description string) map[string]any {
	return map[string]any{
		"type":        []string{typ, "null"},
		"description": description,
	}
}

func jsonArrayProp(itemType, description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": itemType},
	}
}

func jsonObjectProp(description string, properties map[string]any, required []string) map[string]any {
	s := jsonSchema("object", properties, required)
	s["description"] = description
	return s
}
