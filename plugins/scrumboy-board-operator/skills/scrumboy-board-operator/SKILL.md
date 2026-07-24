---
name: scrumboy-board-operator
description: Operate Scrumboy projects and boards through MCP or Agoragentic APIs. Use when an agent needs to inspect board state, plan sprint work, create todos, move cards, manage tags, or summarize project health.
---

# Scrumboy Board Operator Skill

Use this Skill to turn broad project-management requests into safe, auditable
Scrumboy MCP workflows. Prefer read-only inspection before proposing mutating
tool calls.

## Capabilities

- Discover Scrumboy MCP capabilities and available tools.
- Inspect projects, board state, todos, sprints, tags, and members.
- Summarize sprint health, assignment gaps, and blockers or stale items where
  they are represented by board content, tags, sprint dates, workflow columns,
  or visible metadata.
- Draft todo creation, updates, moves, tag changes, and sprint actions.
- Separate JSON-RPC (`/mcp/rpc`), legacy HTTP (`/mcp`), and Agoragentic
  (`/agora/v1/discover`, `/agora/v1/invoke`) request shapes.

## Required Output

Return a concise note with these sections:

- `Scope`: the Scrumboy instance, project slug, sprint, todo, tag, or member under review.
- `Interface`: MCP JSON-RPC, legacy MCP HTTP, or Agoragentic adapter.
- `Evidence`: read-only tools or curl commands used, with tokens and sensitive IDs redacted.
- `Findings`: board state, sprint state, inferred blockers or stale work, or configuration risk.
- `Plan`: recommended next steps, separated into read-only checks and mutating actions.
- `Approval Required`: every create, update, delete, move, sprint activation, sprint close, tag update, member change, or webhook change.
- `Verification`: follow-up reads to confirm board state after an approved mutation.
- `Risks`: missing auth, anonymous mode limits, bootstrap state, stale board data, or production impact.

When explicitly running plugin evals, also include `Plugin Eval Metadata`: eval
case id, expected pass criteria, and safe metadata events. Do not include this
section for normal board operations.

## Workflow

1. Confirm the request scope and whether the user wants read-only analysis or a board mutation.
2. Call `system_getCapabilities` or `tools/list` first when interface support is unclear.
3. Use `projects_list` and `board_get` before recommending todo or sprint changes.
4. For JSON-RPC, call `tools/call` with `params.name` and `params.arguments`.
5. For legacy MCP HTTP, call `/mcp` with `tool` and `input`.
6. For Agoragentic, call `/agora/v1/discover` or `/agora/v1/invoke` with `tool` and `arguments`.
7. Ask for human approval before any mutating action.
8. Verify after approved changes with `board_get`, `todos_get`, sprint reads, or tag/member reads.

## Acceptance Checks

- Identifies the Scrumboy interface and uses the correct request shape.
- Reads board or project state before proposing changes.
- Separates evidence from recommendations.
- Requires approval before mutating tools.
- Redacts credentials, board contents, todo descriptions, and user data from telemetry.
- Includes a verification step after approved changes.

## Privacy and Telemetry Boundary

Only emit metadata about plugin behavior, such as component name, outcome,
duration bucket, harness name, and sanitized error class. Do not emit prompts,
source files, todo descriptions, board contents, API tokens, session cookies,
user identifiers, tool arguments, or model outputs.
