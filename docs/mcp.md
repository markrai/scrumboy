# Scrumboy MCP Interface

Designed for use by AI agents (Claude, custom MCP clients) and automation workflows.

Scrumboy provides an MCP-compatible tool interface over HTTP for managing projects, todos, sprints, tags, and members.

## Quick Start

1. Start Scrumboy
2. Obtain a session cookie or API token
3. Call MCP:

curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sb_YOUR_TOKEN" \
  -d '{"tool":"projects_list","input":{}}'

Example response (success; `data.items` is an array of `projectItem` objects when you have projects; it may be empty `[]`):

```json
{
  "ok": true,
  "data": {
    "items": []
  },
  "meta": {}
}
```

For **`projects_list`**, expect **`ok: false`** when you are not signed in, the instance is in anonymous mode, or (full mode) the DB has no users yet — see **Response Format** / **Error Handling**. An **invalid** `Authorization: Bearer` token returns **401** / **`AUTH_REQUIRED`** / **`Authentication required`** before any tool body runs (including capabilities).

### Minimal Example

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{"tool":"system_getCapabilities","input":{}}'
```

## Overview

Each tool is invoked by name (e.g. `todos_create`) with a JSON input object and returns a structured JSON response.

Scrumboy exposes a fixed catalog of **named tools** over **HTTP**. Clients call tools by posting JSON and receive JSON success or error envelopes (legacy surface), or use **JSON-RPC 2.0** on a separate path (MCP-style `tools/list` and `tools/call`).

Tool inputs **generally** reject unknown fields where the handler uses **`decodeInput`** (JSON decoding uses **`DisallowUnknownFields`** there and on legacy `POST /mcp` bodies via **`readJSON`** in `internal/mcp/http_handler.go`). The tool catalog roots set **`additionalProperties: false`** in `internal/mcp/tool_catalog.go` to describe that contract. **Exceptions:** handlers that do not call **`decodeInput`** still accept extra keys in `input` / `arguments` without failing decode — today **`system_getCapabilities`**, **`projects_list`**, and **`tags_listMine`**. JSON-RPC **`tools/call`** unmarshals **`params`** with standard **`json.Unmarshal`**, so unknown keys **beside** `name` / `arguments` on `params` are ignored (only **`arguments`** are validated per tool).

This is not a stdio-based MCP server. All interactions occur over HTTP. Any client that can send `GET`/`POST` with JSON bodies and cookies or `Authorization` headers can integrate.

## Response Format

All **legacy** MCP responses (`GET /mcp`, `POST /mcp`) use a standard JSON envelope (`internal/mcp/types.go`, `writeSuccess` / `writeError` in `internal/mcp/http_handler.go`).

**Success** (HTTP status is typically **200**; tool-dependent errors use **4xx** with the error envelope instead):

```json
{
  "ok": true,
  "data": {},
  "meta": {}
}
```

- **`data`** — tool result payload (object shape varies by tool).
- **`meta`** — always present; often `{}`, or e.g. `{"adapterVersion":1}` for `system_getCapabilities` / `GET /mcp`.

**Error:**

```json
{
  "ok": false,
  "error": {
    "code": "STRING",
    "message": "STRING",
    "details": {}
  }
}
```

- **`code`** — machine-readable string (see Error Handling).
- **`details`** — always present; empty object `{}` when there are no extra fields.

JSON-RPC responses on **`POST /mcp/rpc`** use JSON-RPC **`result`** / **`error`** framing instead; see **Error Handling** and **JSON-RPC Example**.

## Base URL

The MCP handler exposes two deliberately separate interfaces on the same origin:

- **Legacy tool API:** `GET /mcp`, `POST /mcp` (and optional trailing slash).
- **Canonical MCP Streamable HTTP transport:** `/mcp/rpc`. Native MCP clients must be configured with this exact URL. `/mcp/rpc/` redirects to `/mcp/rpc` and is not a second resource identity.

There are no other MCP paths under `/mcp/` in the current implementation.

## Agoragentic HTTP adapter (Agora)

For **Agoragentic**-style listings (HTTP envelope and fixed paths), Scrumboy exposes **`POST /agora/v1/discover`** and **`POST /agora/v1/invoke`**, which delegate in-process to the same JSON-RPC **`tools/list`** and **`tools/call`** flow as **`POST /mcp/rpc`**. **`/mcp`** remains the legacy Scrumboy surface and **`/mcp/rpc`** is the canonical standards-based MCP transport; this layer is an edge adapter only. Request/response shapes, required fields, and schema notes are documented in **`docs/agoragentic.md`**, with a minimal example manifest at **`docs/examples/agoragentic-manifest.json`**.

## Choosing an Interface

Claude and other MCP-style clients should use the **JSON-RPC** interface (**`/mcp/rpc`**).

- Use **legacy HTTP** (`POST /mcp` for tools; **`GET /mcp`** returns the same capabilities payload as **`system_getCapabilities`**) for simple integrations and scripting.
- Use **JSON-RPC** (`POST /mcp/rpc`) for MCP-compatible clients and structured tool calling.

Both interfaces expose the **same** underlying tools.

## Example Use Cases

- Automating task creation or updates from external systems via HTTP.
- Integrating Scrumboy with **AI agents** (e.g. Claude or other LLM-driven clients) and **custom MCP-oriented HTTP clients** that use JSON-RPC **`tools/list`** and **`tools/call`**.
- Building custom dashboards or workflows on top of **`projects_list`**, **`board_get`**, **`todos.*`**, and related tools.

## Authentication

Behavior is implemented at the endpoint boundary in `internal/mcp`.

**Full mode (`SCRUMBOY_MODE=full`):**

| Endpoint | Session cookie | Static `sb_…` Bearer | Scrumboy OAuth Bearer |
|---|---:|---:|---:|
| `/mcp` | Yes | Yes | No |
| `/mcp/rpc` | Yes | Yes | Yes, only when bound to `<origin>/mcp/rpc` |

If `Authorization: Bearer` is present, a failed token never falls back to a valid cookie. On legacy `/mcp`, a rejected Bearer returns the existing **401** `AUTH_REQUIRED` envelope and no OAuth discovery challenge. OAuth tokens are deliberately rejected there. Without a Bearer, `/mcp` preserves its existing cookie and unauthenticated capability/bootstrap behavior.

In full mode, authentication protects the entire `/mcp/rpc` transport, including `initialize`, `tools/list`, and `GET`. Missing credentials return an empty **401** with:

```text
WWW-Authenticate: Bearer resource_metadata="<origin>/.well-known/oauth-protected-resource/mcp/rpc"
```

Malformed, invalid, expired, revoked, unbound, or wrong-resource Bearer tokens return the same empty **401** with `error="invalid_token"`. Valid session cookies, static API tokens, and Scrumboy-issued OAuth tokens bound to `/mcp/rpc` continue. Upstream OIDC-provider tokens are never accepted as MCP credentials.

**Anonymous mode (`SCRUMBOY_MODE=anonymous`):**

- Session cookies and `Authorization` are **ignored** for MCP (same boundary as the rest of the HTTP API).
- Capabilities report `auth.mode` as **`disabled`** and `authenticatedToolsUsable` as **`false`**.
- Tools that require a signed-in user or multi-user instance return **`CAPABILITY_UNAVAILABLE`** (HTTP **403** on the legacy surface) with messages such as *unavailable in anonymous mode*.

**Bootstrap:** When the user table is empty (`CountUsers == 0`), capabilities include `bootstrapAvailable: true` and `auth.authenticatedToolsUsable: false`. Most tools return **`CAPABILITY_UNAVAILABLE`** (*unavailable before bootstrap*) until the first user exists.

**OAuth 2.1 (full mode only):** `/mcp/rpc` is the sole OAuth protected resource. Clients discover its Scrumboy authorization server through the path-derived RFC 9728 metadata URL in the 401 challenge. See **[`docs/oauth.md`](oauth.md)** for DCR, PKCE, resource binding, authorize/token/revoke, and token lifetimes. `/mcp` never accepts these OAuth tokens.

## Capabilities

**Legacy (recommended for a quick probe):**

- **`GET /mcp`** — same successful response shape as calling tool `system_getCapabilities` with `POST /mcp`: **`200`** with body `{"ok":true,"data":{...},"meta":{...}}` (see **Response Format**). Uses the same auth resolution as other MCP requests.

**Legacy POST:**

- **`POST /mcp`** with body `{"tool":"system_getCapabilities","input":{}}` (or any JSON object for `input` — the handler accepts it; decoding uses the tool’s schema).

**JSON-RPC:**

- After authentication and `initialize`, call **`tools/list`** to receive the catalog (`name`, `description`, `inputSchema` per tool), implemented in `internal/mcp/jsonrpc_handler.go` / `internal/mcp/tool_catalog.go`.
- Calling **`system_getCapabilities`** through `tools/call` returns the same
  capability fields plus top-level **`adapterVersion`** in
  `structuredContent` and its JSON text block. Legacy `/mcp` keeps
  `adapterVersion` under `meta`.

**Example `data` object** (structure from `internal/mcp/types.go` `capabilitiesData`; values below match a **full-mode, pre-bootstrap** instance as asserted in tests — your `serverMode`, `bootstrapAvailable`, and `implementedTools` may differ):

```json
{
  "serverMode": "full",
  "auth": {
    "mode": "sessionCookie",
    "authenticated": false,
    "authenticatedToolsUsable": false,
    "reason": "bootstrap required before authenticated MCP tools are available",
    "authMethods": ["sessionCookie", "bearer"]
  },
  "bootstrapAvailable": true,
  "identity": {
    "project": "projectSlug",
    "todo": ["projectSlug", "localId"],
    "projectMember": ["projectSlug", "userId"],
    "availableUser": ["userId"]
  },
  "pagination": {
    "defaultInput": ["limit", "cursor"],
    "defaultOutput": ["nextCursor", "hasMore"],
    "futureSpecialCases": ["board_get"]
  },
  "implementedTools": [
    "system_getCapabilities",
    "projects_list",
    "projects_create",
    "projects_update",
    "projects_delete",
    "todos_create",
    "todos_get",
    "todos_search",
    "todos_update",
    "todos_delete",
    "todos_move",
    "todos_linksList",
    "todos_linkAdd",
    "todos_linkRemove",
    "sprints_list",
    "sprints_get",
    "sprints_getActive",
    "sprints_create",
    "sprints_activate",
    "sprints_close",
    "sprints_update",
    "sprints_delete",
    "tags_listProject",
    "tags_listMine",
    "tags_updateMineColor",
    "tags_deleteMine",
    "tags_updateProjectColor",
    "tags_deleteProject",
    "members_list",
    "members_listAvailable",
    "members_add",
    "members_updateRole",
    "members_remove",
    "board_get",
    "workflow_list",
    "workflow_create",
    "workflow_update",
    "workflow_delete",
    "priorities_list",
    "priorities_create",
    "priorities_update",
    "priorities_delete",
    "dashboard_getSummary",
    "dashboard_listTodos",
    "metrics_getBurndown",
    "metrics_getBacklogSize",
    "admin_listUsers",
    "admin_updateUserRole",
    "admin_deleteUser"
  ]
}
```

Successful **`GET /mcp`** responses also include **`meta`** (e.g. `{"adapterVersion":1}` from `system_getCapabilities`).

When there are no planned tools, **`plannedTools`** is omitted from JSON (`omitempty`).

## Available Tools

Exact names match `internal/mcp/registry.go` / `implementedTools()` (50 tools).

> **Deprecated dotted names (compatibility shim, kept indefinitely).** Tool names were
> renamed from dot-separated (`todos.create`, `board.get`, ...) to
> underscore-separated (`todos_create`, `board_get`, ...) because Claude's MCP
> client validates every tool name in `tools/list` against
> `^[a-zA-Z0-9_-]{1,64}$`, and dots fail that pattern -- a single invalid name in
> the array broke tool-calling for *every* MCP server in the session, not just
> Scrumboy. The old dotted names are still accepted for direct tool invocation
> (`tools/call` and the legacy `POST /mcp {"tool": "..."}` endpoint) via
> dispatch-only aliases in `internal/mcp/registry.go`, so existing integrations
> keep working. They are **no longer advertised** in `tools/list` or
> `system_getCapabilities` -- new integrations must use the underscore names.
> The dotted aliases are kept indefinitely as a compatibility shim; there is no
> planned removal. See `CHANGELOG.md`.

**System**

- `system_getCapabilities`

**Projects**

- `projects_list`

**Todos**

- `todos_create`
- `todos_get`
- `todos_search`
- `todos_update`
- `todos_delete`
- `todos_move`
- `todos_linksList`
- `todos_linkAdd`
- `todos_linkRemove`

Linked stories are **directed** from `localId` to `targetLocalId`, and `linkType` describes `localId` as
the subject (`blocks` = localId blocks target; `parent` = localId is parent of target; `duplicates` =
localId duplicates target; `relates_to` is the default). `todos_linkRemove` deletes only that directed
edge. See [API.md](../API.md#todos) for the full semantics.

**Sprints**

- `sprints_list`
- `sprints_get`
- `sprints_getActive`
- `sprints_create`
- `sprints_activate`
- `sprints_close`
- `sprints_update`
- `sprints_delete`

**Tags**

- `tags_listProject`
- `tags_listMine`
- `tags_updateMineColor`
- `tags_deleteMine`
- `tags_updateProjectColor`
- `tags_deleteProject`

> **Durable-project tags are grouped by canonical name.** On durable projects,
> `tags_listProject` returns one logical entry per canonical name — names are compared
> after canonicalization, so legacy rows such as `make space` and `make-space` collapse
> into a single `make-space` entry. Each entry carries `deleteScope` (`"mine"`,
> `"project"`, or `"none"`) plus `canDeleteMine` / `canDeleteProject` / `canUpdateColor`; a `tagId` appears
> only for board-scoped tags (personal groups omit it). The legacy `canDelete` boolean
> is gone — a personal group is never `"project"`, so it never advertises a deletion
> that `tags_deleteProject` refuses.
>
> **Temporary boards are not grouped.** Any project with an expiry keeps the previous
> row-level projection: one entry per tag row, each with a real `tagId`. Their colors
> and deletions are still addressed by `tagId`, so grouping would strand those writes.
> For authenticated Full-mode callers, `tags_updateProjectColor` with `tagId` matches
> REST link-holder semantics (`UpdateTagColorForTemporaryBoard`, no Maintainer gate).
> Anonymous MCP mode remains unavailable; unauthenticated pastebin visitors use REST.
>
> A grouped entry is labelled by its canonical name. A legacy row whose stored name
> cannot be canonicalized at all keeps its raw stored name as the label, and that label
> is what `tagName` and the board `tag` filter accept for it.
>
> `tags_updateProjectColor` takes **exactly one** of `tagId` or `tagName`, decided by
> what was supplied rather than by what is valid: sending a malformed `tagId` or an
> empty `tagName` alongside the other field is rejected instead of silently falling
> through to one path. An explicitly empty `tagName` counts as supplied. `tagName`
> sets only the caller's own per-viewer color for a personal label on a durable project
> and is allowed for any authenticated project member (non-members are rejected;
> temporary boards reject `tagName`). On durable projects, `tagId` updates a
> board-scoped tag's shared color and requires maintainer or above; on temporary
> boards, `tagId` uses link-holder color semantics (no Maintainer gate).
>
> **Known limitation:** a per-viewer color set by `tagName` lands on backing tag rows
> the caller also uses in their other projects. Only the targeted project emits a
> refresh event; the caller's other boards show the new color on their next load. The
> change is invisible to other members, so no refresh is broadcast to them.

**Members**

- `members_list`
- `members_listAvailable`
- `members_add`
- `members_updateRole`
- `members_remove`

**Board**

- `board_get`

`board_get` accepts an optional string `assignee` filter: `"me"` for the
authenticated caller, `"unassigned"` for todos without an assignee, or a
positive user ID encoded as a string. For example:

```json
{
  "projectSlug": "example",
  "assignee": "me"
}
```

Use `"42"`, not JSON number `42`, for a concrete user ID. Invalid values,
including non-string JSON values, return `VALIDATION_ERROR` with
`details.field: "assignee"` instead of silently returning an unfiltered board.
A valid unknown or non-member user ID returns an empty board.

`board_get` accepts an optional string `priority` filter. Omit it or send an
empty string for all priorities, use `"**none**"` for todos without a priority,
or pass a literal priority-tier key. Priority keys use lowercase letters,
digits, and underscores, so the `*` characters keep the sentinel outside that
grammar and a real tier key such as `"none"` remains filterable. An unknown tier
key returns an empty board.

`board_get` also accepts an optional string `sort`: `"newest"` or `"oldest"`
orders items within each lane by creation time with a stable ID tie-break.
Omit `sort` to preserve manual drag-rank order.

The optional `sprintId` filter is the stored sprint row ID returned as
`sprintId` by `sprints_list`. It is not the project-local `number` in the same
sprint item. Omit it (or send `null`) for no sprint filter. A supplied value
must be positive and must identify a sprint in `projectSlug`; missing and
cross-project IDs both return `NOT_FOUND`. This stored-ID convention is shared
by MCP sprint mutations, todo sprint assignment, and sprint-scoped metrics.
REST board URLs intentionally use the project-local sprint number instead.

The optional `columnKey` filter restricts the board read to a single workflow
column key (as returned by `workflow_list`). Surrounding whitespace is trimmed.
Omit `columnKey` to preserve the existing all-columns behavior. An unknown or
nonexistent column key returns `VALIDATION_ERROR` with `details.field:
"columnKey"`. When set, `data.columns` and the pagination meta maps
(`nextCursorByColumn`, `hasMoreByColumn`, `totalCountByColumn`) contain only
that column; other workflow columns are omitted and are not queried. Pagination
still uses column keys in `cursorByColumn`; entries for other valid workflow
columns are ignored and are not decoded when `columnKey` scopes the request.

`board_get` uses explicit validation tiers. Authentication/capability checks,
input shape, required `projectSlug`, `limit`, assignee type/grammar, and `sort`
are checked before project access because they are target-independent. Project
access then precedes sprint resolution, workflow/`columnKey` validation, and
`cursorByColumn` validation. As a result, a bad pre-access field still returns
its exact `VALIDATION_ERROR` when the slug is denied, missing, or expired,
while bad `sprintId`, `columnKey`, and `cursorByColumn` values are masked by
`NOT_FOUND` for those targets. Cursor values are decoded in workflow order for
columns that are actually read, so a malformed cursor for a later lane can
follow successful reads of earlier lanes. The permanent `board.get` alias and
both MCP transports use this same ordering.

REST slug board reads intentionally differ: they resolve access before query
validation, so an inaccessible REST target masks all later query errors. This
is a first-error ordering difference, not a difference in permissions or
validation grammar.

`projectSlug` is a lookup identifier, not a request-echo field. Lookup accepts
normalization-equivalent values such as uppercase or surrounding whitespace.
On success, `project.projectSlug` and every returned todo's `projectSlug` use
the persisted canonical slug over legacy and JSON-RPC transports, including
calls made through the permanent `board.get` alias.

For an expiring Temporary Board, `board_get` performs its throttled activity
refresh only after the workflow and every requested lane/count have loaded.
That refresh is best-effort maintenance: a failure is logged by the server,
while both legacy and JSON-RPC clients still receive the completed board
without a warning field. A successful read does not guarantee that this
particular request persisted a new expiry. Durable boards skip the refresh;
expired or inaccessible boards still fail during access.

**Workflow**

- `workflow_list`
- `workflow_create`
- `workflow_update`
- `workflow_delete`

Manage a project's workflow columns (board lanes). `workflow_create`/`workflow_update`/`workflow_delete`
require **maintainer role or higher**. `workflow_update` requires **both** `name` and `color` (not a
partial update). `workflow_delete` removes an empty non-done column and rejects the done column, non-empty
columns, and deletes that would leave fewer than 2 columns. See [API.md](../API.md#workflow) for full
semantics.

**Priorities**

- `priorities_list`
- `priorities_create`
- `priorities_update`
- `priorities_delete`

`priorities_list` is available to any project Viewer or above.
Create/update/delete require Maintainer. Tiers use immutable `key`, editable
`name` and `#RRGGBB` `color`, and stable `position` order. Projects support at
most 12 tiers and must retain one; an in-use tier cannot be deleted.

### Tool Index (Flat)

One tool name per line (same order as `implementedTools()` in code):

```
system_getCapabilities
projects_list
projects_create
projects_update
projects_delete
todos_create
todos_get
todos_search
todos_update
todos_delete
todos_move
todos_linksList
todos_linkAdd
todos_linkRemove
sprints_list
sprints_get
sprints_getActive
sprints_create
sprints_activate
sprints_close
sprints_update
sprints_delete
tags_listProject
tags_listMine
tags_updateMineColor
tags_deleteMine
tags_updateProjectColor
tags_deleteProject
members_list
members_listAvailable
members_add
members_updateRole
members_remove
board_get
workflow_list
workflow_create
workflow_update
workflow_delete
priorities_list
priorities_create
priorities_update
priorities_delete
dashboard_getSummary
dashboard_listTodos
metrics_getBurndown
metrics_getBacklogSize
admin_listUsers
admin_updateUserRole
admin_deleteUser
```

## Tool Schemas (Representative)

Tool arguments must match the published shape only — **no extra keys** (see **Overview**). Schemas are defined in code in `internal/mcp/tool_catalog.go` (JSON Schema-like objects with `additionalProperties: false` on the root, aligned with strict JSON decoding).

### Minimal Tool Input Example

**`todos_create`** — required fields only (`internal/mcp/tool_catalog.go` marks `projectSlug` and `title` as required):

```json
{
  "projectSlug": "string",
  "title": "string"
}
```

Use real values in place of the placeholders (e.g. `"my-project"`, `"Example title"`). Full legacy request:

```json
{
  "tool": "todos_create",
  "input": {
    "projectSlug": "my-project",
    "title": "Example title"
  }
}
```

**1. `projects_list`** — input: empty object `{}`. Success data (legacy `data`):

```json
{
  "items": [
    {
      "projectSlug": "my-project",
      "projectId": 1,
      "name": "My project",
      "image": null,
      "dominantColor": "#445566",
      "defaultSprintWeeks": 2,
      "expiresAt": null,
      "createdAt": "2026-04-04T12:00:00Z",
      "updatedAt": "2026-04-04T12:00:00Z",
      "role": "maintainer"
    }
  ]
}
```

(`projectItem` in `internal/mcp/types.go`; **`role`** is the project member role string from `store.ProjectRole.String()` — e.g. `maintainer`, `contributor`, `viewer`, lowercase.)

**2. `todos_create`** — required: `projectSlug`, `title`. Optional fields include `body`, `tags`, `columnKey`, `estimationPoints`, `sprintId`, `assigneeUserId`, `position` (`afterLocalId` / `beforeLocalId`). Success data:

```json
{
  "todo": {
    "projectSlug": "my-project",
    "localId": 1,
    "title": "Example",
    "body": "",
    "columnKey": "backlog",
    "tags": [],
    "estimationPoints": null,
    "assigneeUserId": null,
    "sprintId": null,
    "createdAt": "2026-04-04T12:00:00Z",
    "updatedAt": "2026-04-04T12:00:00Z",
    "doneAt": null
  }
}
```

(`todoItem` in `internal/mcp/types.go`; default column when omitted is `store.DefaultColumnBacklog` = **`backlog`** after `normalizeColumnKey` in `internal/mcp/adapter.go`.)

**3. `todos_update`** — required: `projectSlug`, `localId`, `patch` (object). Only fields present in `patch` are updated; some fields may be set to JSON `null` to clear where the store allows it. For `priorityKey`, omission preserves, `null` clears, and a string assigns a tier from the same project. Success data uses the same `todo` object shape as `todos_create` / `todos_get`.

## Examples

### 1. List projects (legacy `POST /mcp`)

With a valid session cookie (replace host and cookie value):

```bash
curl -sS -X POST 'https://YOUR_HOST/mcp' \
  -H 'Content-Type: application/json' \
  -H 'Cookie: scrumboy_session=YOUR_SESSION_TOKEN' \
  -d '{"tool":"projects_list","input":{}}'
```

Success shape:

```json
{
  "ok": true,
  "data": {
    "items": []
  },
  "meta": {}
}
```

Same tool with **Bearer** (full API token string after `Bearer `):

```bash
curl -sS -X POST 'https://YOUR_HOST/mcp' \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer sb_YOUR_TOKEN' \
  -d '{"tool":"projects_list","input":{}}'
```

### 2. Create a todo (legacy `POST /mcp`)

```bash
curl -sS -X POST 'https://YOUR_HOST/mcp' \
  -H 'Content-Type: application/json' \
  -H 'Cookie: scrumboy_session=YOUR_SESSION_TOKEN' \
  -d '{
    "tool": "todos_create",
    "input": {
      "projectSlug": "my-project",
      "title": "Ship MCP docs",
      "body": "From codebase only",
      "tags": ["docs"]
    }
  }'
```

Minimal input requires at least `projectSlug` and `title`. Success includes `data.todo` as in the schema section above. Todo results include read-only `createdByUserId`: the authenticated creation actor's historical user ID, or explicit JSON `null` when no safe attribution exists. The value does not imply current project membership or notification eligibility and cannot be supplied in create/update inputs. Successful MCP update/move mutations may publish an internal creator-consideration request through their prepared application services, while still publishing no `board.refresh_needed`. The request nominates only the historical creator; a separate fresh project/member check may produce an internal point-in-time authorized-recipient decision. The SSE bridge then repeats that access check before it may emit one private `todo.creator_activity` event to the current creator. Separately, a material mutation may create a creator-email candidate; the mail worker freshly reauthorizes access and rechecks the email-only `createdByMe` preference immediately before rendering each send attempt. MCP/Agora do not gain card-activity fallback because they still emit no board refresh. The internal events are never exposed verbatim, and creator Web Push and webhooks remain absent.

### Example Workflow

End-to-end cycle using the **legacy** `POST /mcp` surface (same tools work via JSON-RPC `tools/call`). Replace host, session, and values you read from each response.

**1. List projects** — discover a `projectSlug` (e.g. from `data.items[0].projectSlug`).

```bash
curl -sS -X POST 'https://YOUR_HOST/mcp' \
  -H 'Content-Type: application/json' \
  -H 'Cookie: scrumboy_session=YOUR_SESSION_TOKEN' \
  -d '{"tool":"projects_list","input":{}}'
```

**2. Create a todo** — use that slug and a title; read **`data.todo.localId`** from the success body.

```bash
curl -sS -X POST 'https://YOUR_HOST/mcp' \
  -H 'Content-Type: application/json' \
  -H 'Cookie: scrumboy_session=YOUR_SESSION_TOKEN' \
  -d '{
    "tool": "todos_create",
    "input": {
      "projectSlug": "YOUR_PROJECT_SLUG",
      "title": "Close the loop"
    }
  }'
```

**3. Move the todo to Done** — `todos_move` requires `projectSlug`, `localId`, and `toColumnKey`. The adapter accepts **`done`** (and normalizes synonyms like `DONE`) to the workflow **done** column (`internal/mcp/adapter.go` `normalizeColumnKey`).

```bash
curl -sS -X POST 'https://YOUR_HOST/mcp' \
  -H 'Content-Type: application/json' \
  -H 'Cookie: scrumboy_session=YOUR_SESSION_TOKEN' \
  -d '{
    "tool": "todos_move",
    "input": {
      "projectSlug": "YOUR_PROJECT_SLUG",
      "localId": 1,
      "toColumnKey": "done"
    }
  }'
```

Use the real `localId` from step 2, not a placeholder, unless it happens to be `1`.

This demonstrates a complete interaction cycle using MCP tools: discover context, create work, update board placement.

### JSON-RPC Example

The JSON-RPC interface follows **MCP-style** tool discovery and invocation: **`tools/list`** (catalog + `inputSchema`) and **`tools/call`** (invoke by name with `arguments`), plus **`initialize`** / optional **`notifications/initialized`** as implemented in `internal/mcp/jsonrpc_handler.go`.

All requests are **`POST /mcp/rpc`** with **`Content-Type: application/json`**, **`Accept: application/json, text/event-stream`**, credentials in full mode, and body **`{"jsonrpc":"2.0",...}`**. After initialization, send **`MCP-Protocol-Version`** on later requests. Responses are JSON-RPC **`result`** or **`error`**; normal protocol responses use **HTTP 200** (see Error Handling).

**1. `initialize`** (request must include **`id`**):

```bash
curl -sS -X POST 'https://YOUR_HOST/mcp/rpc' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Authorization: Bearer sb_YOUR_TOKEN' \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "initialize",
    "params": {
      "protocolVersion": "2025-11-25",
      "capabilities": {},
      "clientInfo": { "name": "my-agent", "version": "1.0.0" }
    }
  }'
```

Example **`result`** (values from `internal/mcp/jsonrpc_handler.go`):

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2025-11-25",
    "capabilities": {
      "tools": { "listChanged": false }
    },
    "serverInfo": {
      "name": "scrumboy",
      "version": "1.0.0"
    },
    "instructions": "Scrumboy MCP server. Use tools/list to discover available tools."
  }
}
```

**2. `notifications/initialized`** (optional client ack): POST body with **`method`** **`notifications/initialized`** or **`initialized`** (both accepted), **no `id`**. Accepted notifications return **202 Accepted** with an empty body. A structurally invalid notification (for example, scalar or `null` `params`) returns **400** and has no side effect.

**3. `tools/list`**:

```bash
curl -sS -X POST 'https://YOUR_HOST/mcp/rpc' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-11-25' \
  -H 'Authorization: Bearer sb_YOUR_TOKEN' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
```

**`result`** contains **`tools`**: an array of objects with **`name`**, **`description`**, **`inputSchema`** (one entry per implemented tool; length matches **`implementedTools`** in capabilities).

**4. `tools/call`** — **`params.name`** is the tool name; **`params.arguments`** is the tool input object (catalog **`required`** keys are checked before the handler; unknown keys in **`arguments`** fail **`decodeInput`** for most tools — see **Overview** for exceptions). Transport authentication has already completed before dispatch.

```bash
curl -sS -X POST 'https://YOUR_HOST/mcp/rpc' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-11-25' \
  -H 'Cookie: scrumboy_session=YOUR_SESSION_TOKEN' \
  -d '{
    "jsonrpc": "2.0",
    "id": 3,
    "method": "tools/call",
    "params": {
      "name": "projects_list",
      "arguments": {}
    }
  }'
```

Example success **`result`** for **`projects_list`** (empty list):

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\"items\":[]}"
      }
    ],
    "structuredContent": {
      "items": []
    }
  }
}
```

On tool failure, **`result.isError`** is **`true`**, **`content`** carries the
existing plain-text message, and **`structuredContent`** carries sanitized
machine-readable `code`, `message`, and `details` fields
(`internal/mcp/jsonrpc_handler.go`). The JSON-RPC representation never copies
the legacy HTTP status.

```json
{
  "jsonrpc": "2.0",
  "id": 4,
  "result": {
    "content": [{"type": "text", "text": "invalid sort"}],
    "structuredContent": {
      "code": "VALIDATION_ERROR",
      "message": "invalid sort",
      "details": {"field": "sort"}
    },
    "isError": true
  }
}
```

## Error Handling

### Legacy `POST /mcp` and `GET /mcp`

Errors use HTTP status on the wire and a JSON body **`{"ok":false,"error":{...}}`** (`internal/mcp/types.go` / `writeError` in `internal/mcp/http_handler.go`):

```json
{
  "ok": false,
  "error": {
    "code": "AUTH_REQUIRED",
    "message": "Sign-in required for this tool",
    "details": {}
  }
}
```

Same **`code`** for a rejected **Bearer** token, with **`message`: `Authentication required`** (before any tool runs).

`details` is always present (empty object when nil). Detail keys are
allowlisted at serialization. `INTERNAL` always returns message
`internal error` with empty client details; database, infrastructure, and
invariant causes are available only in the server log.
Non-exhaustive **`code`** values from `internal/mcp/errors.go`:
`AUTH_REQUIRED`, `FORBIDDEN`, `NOT_FOUND`, `VALIDATION_ERROR`, `CONFLICT`,
`CAPABILITY_UNAVAILABLE`, `INTERNAL`, `METHOD_NOT_ALLOWED`.

### JSON-RPC `POST /mcp/rpc`

- **Transport authentication failure:** empty HTTP **401**, no JSON-RPC `result` or tool result, and a complete RFC 9728 `WWW-Authenticate` challenge. Invalid Bearers add `error="invalid_token"`.
- **Invalid Origin:** empty HTTP **403** without an OAuth challenge. Requests with no `Origin` remain valid for non-browser clients; a supplied Origin must match the trusted public origin.
- **Method/media/transport failures:** authenticated GET and unsupported methods return empty **405** with `Allow: POST`; restrictive `Accept` values that do not allow both JSON and SSE return **406**; unsupported JSON content types return **415**; accepted notifications return empty **202**. Structurally rejected notifications and known request-only methods (`initialize`, `ping`, `tools/list`, and `tools/call`) sent without an `id` return **400** and do not run a handler.
- **Protocol errors** (bad JSON, unknown method, etc.): response is JSON-RPC **`error`** with integer **`code`** (e.g. `-32700` parse error, `-32601` method not found). Valid requests with an `id` keep the normal **200** protocol-error response. Rejected no-`id` messages use **400**, with `id: null` when an error body is emitted.
- **Tool execution failure** (`tools/call`): HTTP **200** with a **`result`**
  object containing **`isError: true`**, **`content`** (the existing plain-text
  message), and sanitized **`structuredContent`** with `code`, `message`, and
  `details` (`writeJSONRPCToolErrorResult` in
  `internal/mcp/jsonrpc_handler.go`). `INTERNAL` uses message `internal error`
  with `{}` details, and the legacy HTTP status is not copied into the tool
  result.
- **Tool success**: **`result`** includes **`content`** (JSON text of payload)
  and **`structuredContent`** (parsed tool output). Most tools return their
  legacy `data` unchanged. A narrow allowlist adds already-public legacy
  metadata beside existing JSON-RPC data fields: `system_getCapabilities`
  adds `adapterVersion`; `sprints_list` adds `unscheduledCount`; and
  `dashboard_listTodos` adds `nextCursor` and `hasMore`. The JSON text and
  structured object are equivalent. Unapproved metadata is omitted, and
  existing data wins any top-level collision. Legacy `/mcp` keeps its
  `{data,meta}` separation.
- **`board_get` success**: `structuredContent` keeps `project` and `columns` at
  their existing locations and also includes `nextCursorByColumn`,
  `hasMoreByColumn`, and `totalCountByColumn`. The text content serializes the
  same enriched object. Legacy `/mcp` continues to return those maps under its
  separate top-level `meta`.

## Notes / Limitations

- This is not a stdio-based MCP server. All interactions occur over HTTP.
- **Two wire formats:** Legacy `{tool,input}` vs JSON-RPC `initialize` / `tools/list` / `tools/call`. Pick one consistently for a client; they share tool handlers but deliberately have different OAuth boundaries.
- **Stateless JSON-only transport:** Scrumboy does not issue MCP session IDs, offer an SSE GET stream, resumability, or server-initiated requests. An authenticated GET returns 405.
- **Protocol versions:** Streamable HTTP supports `2025-03-26`, `2025-06-18`, and `2025-11-25`. Unsupported initialize versions negotiate to `2025-11-25`. A missing post-initialize header defaults to `2025-03-26`; malformed or unsupported headers return 400.
- **Anonymous mode:** Effectively no authenticated tools; capabilities still describe the server.
- **Pagination:** Global defaults in capabilities mention `limit` / `cursor` / `nextCursor` / `hasMore`; **`board_get`** uses **`cursorByColumn`** (per column key) and returns `nextCursorByColumn`, `hasMoreByColumn`, and `totalCountByColumn` in JSON-RPC structured/text content (or legacy `meta`) — see `tool_catalog.go` and `pagination.futureSpecialCases` in capabilities.
- **`sprints_update` `patch`:** Catalog documents `plannedStartAt` / `plannedEndAt` as **Unix milliseconds** (integers), not RFC3339 strings (unlike `sprints_create`).
- **JSON-RPC `serverInfo.version`:** The value returned by `initialize` is the string **`1.0.0`** in code (`internal/mcp/jsonrpc_handler.go`), not necessarily the Scrumboy app version from `internal/version`.
- **`plannedTools`:** Currently always empty / omitted; there is no separate catalog of unimplemented tools in responses.
