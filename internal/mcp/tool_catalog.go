package mcp

// mcpToolDef is the MCP-spec shape returned by tools/list for each tool.
type mcpToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// toolCatalog returns the tools that have full MCP-spec metadata.
// Tools are added here incrementally; the legacy /mcp endpoint continues to
// serve all 28 tools regardless of catalog coverage.
func toolCatalog() []mcpToolDef {
	return []mcpToolDef{
		{
			Name:        "projects.list",
			Description: "List all projects visible to the authenticated user, with their role in each project.",
			InputSchema: jsonSchema("object", map[string]any{}, nil),
		},
		{
			Name:        "todos.create",
			Description: "Create a new todo item in a project.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug":      jsonProp("string", "Project identifier (slug)"),
				"title":            jsonProp("string", "Title of the todo"),
				"body":             jsonProp("string", "Body / description of the todo"),
				"tags":             jsonArrayProp("string", "Tags to attach to the todo"),
				"columnKey":        jsonProp("string", "Workflow column key (e.g. backlog, doing, done). Defaults to backlog if omitted."),
				"estimationPoints": jsonPropWithNull("integer", "Story points estimate"),
				"sprintId":         jsonPropWithNull("integer", "Sprint ID to assign the todo to"),
				"assigneeUserId":   jsonPropWithNull("integer", "User ID to assign the todo to"),
				"position": map[string]any{
					"type":                 "object",
					"description":        "Position hint within the target column",
					"properties": map[string]any{
						"afterLocalId":  jsonProp("integer", "Place after this todo's localId"),
						"beforeLocalId": jsonProp("integer", "Place before this todo's localId"),
					},
					"additionalProperties": false,
				},
			}, []string{"projectSlug", "title"}),
		},
		{
			Name:        "todos.get",
			Description: "Get a single todo by its project-scoped local ID.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
				"localId":     jsonProp("integer", "Project-scoped todo ID"),
			}, []string{"projectSlug", "localId"}),
		},
		{
			Name:        "todos.update",
			Description: "Update fields on an existing todo. Only the fields present in the patch object are changed.",
			InputSchema: jsonSchema("object", map[string]any{
				"projectSlug": jsonProp("string", "Project identifier (slug)"),
				"localId":     jsonProp("integer", "Project-scoped todo ID"),
				"patch": map[string]any{
					"type":        "object",
					"description": "Fields to update. Only included fields are changed.",
					"properties": map[string]any{
						"title":            jsonProp("string", "New title"),
						"body":             jsonProp("string", "New body / description"),
						"tags":             jsonArrayProp("string", "Replace tags with this list"),
						"estimationPoints": jsonPropWithNull("integer", "Story points estimate (null to clear)"),
						"assigneeUserId":   jsonPropWithNull("integer", "Assignee user ID (null to clear)"),
						"sprintId":         jsonPropWithNull("integer", "Sprint ID (null to clear)"),
					},
					"additionalProperties": false,
				},
			}, []string{"projectSlug", "localId", "patch"}),
		},
	}
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
