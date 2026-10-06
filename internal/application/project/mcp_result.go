package project

import "scrumboy/internal/store"

// MCPProjectResult carries the transport-neutral project and role projection
// shared by MCP durable creation and project update.
type MCPProjectResult struct {
	Project store.Project
	Role    store.ProjectRole
}

func newMCPProjectResult(project store.Project, role store.ProjectRole) MCPProjectResult {
	return MCPProjectResult{Project: cloneProject(project), Role: role}
}

// MCPDeletionResult carries the canonical pre-delete slug and committed
// project identity needed by the MCP deletion response.
type MCPDeletionResult struct {
	ProjectSlug string
	ProjectID   int64
}
