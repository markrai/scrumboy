# Scrumboy MCP Interface

Updated: 2026-04-23 14:28:12 -04:00

Designed for use by AI agents (Claude, custom MCP clients) and automation workflows.

Scrumboy provides an MCP-compatible tool interface over HTTP for managing projects, todos, sprints, tags, and members.

## Quick Start

1. Start Scrumboy
2. Obtain a session cookie or API token
3. Call MCP:

curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sb_YOUR_TOKEN" \
  -d '{"tool":"projects.list","input":{}}'

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

For **`projects.list`**, expect **`ok: false`** when you are not signed in, the instance is in anonymous mode, or (full mode) the DB has no users yet — see **Response Format** / **Error Handling**. An **invalid** `Authorization: Bearer` token returns **401** / **`AUTH_REQUIRED`** / **`Authentication required`** before any tool body runs (including capabilities).

### Minimal Example

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{"tool":"system.getCapabilities","input":{}}'
```

## Overview

Each tool is invoked by name (e.g. `todos.create`) with a JSON input object and returns a structured JSON response.

Scrumboy exposes a fixed catalog of **named tools** over **HTTP**. Clients call tools by posting JSON and receive JSON success or error envelopes (legacy surface), or use **JSON-RPC 2.0** on a separate path (MCP-style `tools/list` and `tools/call`).

Tool inputs **generally** reject unknown fields where the handler uses **`decodeInput`** (JSON decoding uses **`DisallowUnknownFields`** there and on legacy `POST /mcp` bodies via **`readJSON`** in `internal/mcp/http_handler.go`). The tool catalog roots set **`additionalProperties: false`** in `internal/mcp/tool_catalog.go` to describe that contract. **Exceptions:** handlers that do not call **`decodeInput`** still accept extra keys in `input` / `arguments` without failing decode — today **`system.getCapabilities`**, **`projects.list`**, and **`tags.listMine`**. JSON-RPC **`tools/call`** unmarshals **`params`** with standard **`json.Unmarshal`**, so unknown keys **beside** `name` / `arguments` on `params` are ignored (only **`arguments`** are validated per tool).

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
- **`meta`** — always present; often `{}`, or e.g. `{"adapterVersion":1}` for `system.getCapabilities` / `GET /mcp`.

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

The MCP handler is mounted at **`/mcp`** on the same origin as the Scrumboy HTTP server (see `internal/httpapi/server.go`: requests where the path is `/mcp` or `/mcp/` or `/mcp/rpc` are dispatched to the MCP handler).

- **Legacy tool API:** `GET /mcp`, `POST /mcp` (and optional trailing slash).
- **JSON-RPC API:** `POST /mcp/rpc` only.

There are no other MCP paths under `/mcp/` in the current implementation.

## Agoragentic HTTP adapter (Agora)

For **Agoragentic**-style listings (HTTP envelope and fixed paths), Scrumboy exposes **`POST /agora/v1/discover`** and **`POST /agora/v1/invoke`**, which delegate in-process to the same JSON-RPC **`tools/list`** and **`tools/call`** flow as **`POST /mcp/rpc`**. **`/mcp`** and **`/mcp/rpc`** remain the canonical MCP surfaces; this layer is an edge adapter only. Request/response shapes, required fields, and schema notes are documented in **`docs/agoragentic.md`**, with a minimal example manifest at **`docs/examples/agoragentic-manifest.json`**.

## Choosing an Interface

Claude and other MCP-style clients should use the **JSON-RPC** interface (**`/mcp/rpc`**).

- Use **legacy HTTP** (`POST /mcp` for tools; **`GET /mcp`** returns the same capabilities payload as **`system.getCapabilities`**) for simple integrations and scripting.
- Use **JSON-RPC** (`POST /mcp/rpc`) for MCP-compatible clients and structured tool calling.

Both interfaces expose the **same** underlying tools.

## Example Use Cases

- Automating task creation or updates from external systems via HTTP.
- Integrating Scrumboy with **AI agents** (e.g. Claude or other LLM-driven clients) and **custom MCP-oriented HTTP clients** that use JSON-RPC **`tools/list`** and **`tools/call`**.
- Building custom dashboards or workflows on top of **`projects.list`**, **`board.get`**, **`todos.*`**, and related tools.

## Authentication

Behavior is implemented in `internal/mcp/adapter.go` (`resolveRequestAuth`).

**Full mode (`SCRUMBOY_MODE=full`):**

1. If `Authorization` is present and the scheme is **`Bearer`** (case-insensitive, with a space after `Bearer` per normal header parsing), the remainder of the header is treated as a **user API token** (same secret the app issues, including the `sb_` prefix). Valid token → request context gets that user. Invalid or missing token after `Bearer` → **401** with `AUTH_REQUIRED` and message **`Authentication required`** on the legacy surface (`resolveAndValidateAuth` in `internal/mcp/http_handler.go`); **no** tool runs, **including** **`GET /mcp`** and **`system.getCapabilities`**. JSON-RPC **`tools/call`** uses the same auth resolution and returns a tool error result with text **`authentication required`**. **Invalid Bearer does not fall back to the session cookie.**
2. If Bearer is **not** sent, the **`scrumboy_session`** cookie is read. Valid session → user is attached to context. Missing or invalid cookie → request is **unauthenticated** (no user in context). In that case **`GET /mcp`** and tool **`system.getCapabilities`** still run and return capabilities; other tools may return **401** / `AUTH_REQUIRED` with **`Sign-in required for this tool`** (or **`CAPABILITY_UNAVAILABLE`** when anonymous / pre-bootstrap).

**Anonymous mode (`SCRUMBOY_MODE=anonymous`):**

- Session cookies and `Authorization` are **ignored** for MCP (same boundary as the rest of the HTTP API).
- Capabilities report `auth.mode` as **`disabled`** and `authenticatedToolsUsable` as **`false`**.
- Tools that require a signed-in user or multi-user instance return **`CAPABILITY_UNAVAILABLE`** (HTTP **403** on the legacy surface) with messages such as *unavailable in anonymous mode*.

**Bootstrap:** When the user table is empty (`CountUsers == 0`), capabilities include `bootstrapAvailable: true` and `auth.authenticatedToolsUsable: false`. Most tools return **`CAPABILITY_UNAVAILABLE`** (*unavailable before bootstrap*) until the first user exists.

## Capabilities

**Legacy (recommended for a quick probe):**

- **`GET /mcp`** — same successful response shape as calling tool `system.getCapabilities` with `POST /mcp`: **`200`** with body `{"ok":true,"data":{...},"meta":{...}}` (see **Response Format**). Uses the same auth resolution as other MCP requests.

**Legacy POST:**

- **`POST /mcp`** with body `{"tool":"system.getCapabilities","input":{}}` (or any JSON object for `input` — the handler accepts it; decoding uses the tool’s schema).

**JSON-RPC:**

- After `initialize`, call **`tools/list`** to receive the catalog (`name`, `description`, `inputSchema` per tool), implemented in `internal/mcp/jsonrpc_handler.go` / `internal/mcp/tool_catalog.go`. **`tools/list`** does **not** invoke **`resolveRequestAuth`**; the catalog is returned without an MCP-layer auth check (any caller that can `POST /mcp/rpc` receives the tool list and schemas).

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
    "futureSpecialCases": ["board.get"]
  },
  "implementedTools": [
    "system.getCapabilities",
    "projects.list",
    "todos.create",
    "todos.get",
    "todos.search",
    "todos.update",
    "todos.delete",
    "todos.move",
    "sprints.list",
    "sprints.get",
    "sprints.getActive",
    "sprints.create",
    "sprints.activate",
    "sprints.close",
    "sprints.update",
    "sprints.delete",
    "tags.listProject",
    "tags.listMine",
    "tags.updateMineColor",
    "tags.deleteMine",
    "tags.updateProjectColor",
    "tags.deleteProject",
    "members.list",
    "members.listAvailable",
    "members.add",
    "members.updateRole",
    "members.remove",
    "board.get"
  ]
}
```

Successful **`GET /mcp`** responses also include **`meta`** (e.g. `{"adapterVersion":1}` from `system.getCapabilities`).

When there are no planned tools, **`plannedTools`** is omitted from JSON (`omitempty`).

## Available Tools

Exact names match `internal/mcp/registry.go` / `implementedTools()` (28 tools).

**System**

- `system.getCapabilities`

**Projects**

- `projects.list`

**Todos**

- `todos.create`
- `todos.get`
- `todos.search`
- `todos.update`
- `todos.delete`
- `todos.move`

**Sprints**

- `sprints.list`
- `sprints.get`
- `sprints.getActive`
- `sprints.create`
- `sprints.activate`
- `sprints.close`
- `sprints.update`
- `sprints.delete`

**Tags**

- `tags.listProject`
- `tags.listMine`
- `tags.updateMineColor`
- `tags.deleteMine`
- `tags.updateProjectColor`
- `tags.deleteProject`

**Members**

- `members.list`
- `members.listAvailable`
- `members.add`
- `members.updateRole`
- `members.remove`

**Board**

- `board.get`

### Tool Index (Flat)

One tool name per line (same order as `implementedTools()` in code):

```
system.getCapabilities
projects.list
todos.create
todos.get
todos.search
todos.update
todos.delete
todos.move
sprints.list
sprints.get
sprints.getActive
sprints.create
sprints.activate
sprints.close
sprints.update
sprints.delete
tags.listProject
tags.listMine
tags.updateMineColor
tags.deleteMine
tags.updateProjectColor
tags.deleteProject
members.list
members.listAvailable
members.add
members.updateRole
members.remove
board.get
```

## Tool Schemas (Representative)

Tool arguments must match the published shape only — **no extra keys** (see **Overview**). Schemas are defined in code in `internal/mcp/tool_catalog.go` (JSON Schema-like objects with `additionalProperties: false` on the root, aligned with strict JSON decoding).

### Minimal Tool Input Example

**`todos.create`** — required fields only (`internal/mcp/tool_catalog.go` marks `projectSlug` and `title` as required):

```json
{
  "projectSlug": "string",
  "title": "string"
}
```

Use real values in place of the placeholders (e.g. `"my-project"`, `"Example title"`). Full legacy request:

```json
{
  "tool": "todos.create",
  "input": {
    "projectSlug": "my-project",
    "title": "Example title"
  }
}
```

**1. `projects.list`** — input: empty object `{}`. Success data (legacy `data`):

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

**2. `todos.create`** — required: `projectSlug`, `title`. Optional fields include `body`, `tags`, `columnKey`, `estimationPoints`, `sprintId`, `assigneeUserId`, `position` (`afterLocalId` / `beforeLocalId`). Success data:

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

**3. `todos.update`** — required: `projectSlug`, `localId`, `patch` (object). Only fields present in `patch` are updated; some fields may be set to JSON `null` to clear where the store allows it. Success data uses the same `todo` object shape as `todos.create` / `todos.get`.

## Examples

### 1. List projects (legacy `POST /mcp`)

With a valid session cookie (replace host and cookie value):

```bash
curl -sS -X POST 'https://YOUR_HOST/mcp' \
  -H 'Content-Type: application/json' \
  -H 'Cookie: scrumboy_session=YOUR_SESSION_TOKEN' \
  -d '{"tool":"projects.list","input":{}}'
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
  -d '{"tool":"projects.list","input":{}}'
```

### 2. Create a todo (legacy `POST /mcp`)

```bash
curl -sS -X POST 'https://YOUR_HOST/mcp' \
  -H 'Content-Type: application/json' \
  -H 'Cookie: scrumboy_session=YOUR_SESSION_TOKEN' \
  -d '{
    "tool": "todos.create",
    "input": {
      "projectSlug": "my-project",
      "title": "Ship MCP docs",
      "body": "From codebase only",
      "tags": ["docs"]
    }
  }'
```

Minimal input requires at least `projectSlug` and `title`. Success includes `data.todo` as in the schema section above.

### Example Workflow

End-to-end cycle using the **legacy** `POST /mcp` surface (same tools work via JSON-RPC `tools/call`). Replace host, session, and values you read from each response.

**1. List projects** — discover a `projectSlug` (e.g. from `data.items[0].projectSlug`).

```bash
curl -sS -X POST 'https://YOUR_HOST/mcp' \
  -H 'Content-Type: application/json' \
  -H 'Cookie: scrumboy_session=YOUR_SESSION_TOKEN' \
  -d '{"tool":"projects.list","input":{}}'
```

**2. Create a todo** — use that slug and a title; read **`data.todo.localId`** from the success body.

```bash
curl -sS -X POST 'https://YOUR_HOST/mcp' \
  -H 'Content-Type: application/json' \
  -H 'Cookie: scrumboy_session=YOUR_SESSION_TOKEN' \
  -d '{
    "tool": "todos.create",
    "input": {
      "projectSlug": "YOUR_PROJECT_SLUG",
      "title": "Close the loop"
    }
  }'
```

**3. Move the todo to Done** — `todos.move` requires `projectSlug`, `localId`, and `toColumnKey`. The adapter accepts **`done`** (and normalizes synonyms like `DONE`) to the workflow **done** column (`internal/mcp/adapter.go` `normalizeColumnKey`).

```bash
curl -sS -X POST 'https://YOUR_HOST/mcp' \
  -H 'Content-Type: application/json' \
  -H 'Cookie: scrumboy_session=YOUR_SESSION_TOKEN' \
  -d '{
    "tool": "todos.move",
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

All requests are **`POST /mcp/rpc`** with **`Content-Type: application/json`** and body **`{"jsonrpc":"2.0",...}`**. Responses are JSON-RPC **`result`** or **`error`**; normal protocol responses use **HTTP 200** (see Error Handling).

**1. `initialize`** (request must include **`id`**):

```bash
curl -sS -X POST 'https://YOUR_HOST/mcp/rpc' \
  -H 'Content-Type: application/json' \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "initialize",
    "params": {
      "protocolVersion": "2024-11-05",
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
    "protocolVersion": "2024-11-05",
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

**2. `notifications/initialized`** (optional client ack): POST body with **`method`** **`notifications/initialized`** or **`initialized`** (both accepted), **no `id`**. Server responds **204 No Content** and an empty body.

**3. `tools/list`**:

```bash
curl -sS -X POST 'https://YOUR_HOST/mcp/rpc' \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
```

**`result`** contains **`tools`**: an array of objects with **`name`**, **`description`**, **`inputSchema`** (one entry per implemented tool; length matches **`implementedTools`** in capabilities).

**4. `tools/call`** — same auth resolution as legacy **`POST /mcp`** (**`resolveRequestAuth`**: session cookie or **`Authorization: Bearer`**), but Bearer failures map to a JSON-RPC **tool** result with **`isError: true`** and message **`authentication required`**, not the legacy **`AUTH_REQUIRED`** envelope. **`params.name`** is the tool name; **`params.arguments`** is the tool input object (catalog **`required`** keys are checked before the handler; unknown keys in **`arguments`** fail **`decodeInput`** for most tools — see **Overview** for exceptions).

```bash
curl -sS -X POST 'https://YOUR_HOST/mcp/rpc' \
  -H 'Content-Type: application/json' \
  -H 'Cookie: scrumboy_session=YOUR_SESSION_TOKEN' \
  -d '{
    "jsonrpc": "2.0",
    "id": 3,
    "method": "tools/call",
    "params": {
      "name": "projects.list",
      "arguments": {}
    }
  }'
```

Example success **`result`** for **`projects.list`** (empty list):

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

On tool failure, **`result.isError`** is **`true`**, **`content`** carries a plain-text message, and **`structuredContent`** is omitted (`internal/mcp/jsonrpc_handler.go`).

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

`details` is always present (empty object when nil). Non-exhaustive **`code`** values from `internal/mcp/errors.go`: `AUTH_REQUIRED`, `FORBIDDEN`, `NOT_FOUND`, `VALIDATION_ERROR`, `CONFLICT`, `CAPABILITY_UNAVAILABLE`, `INTERNAL`, `METHOD_NOT_ALLOWED`.

### JSON-RPC `POST /mcp/rpc`

- **Protocol errors** (bad JSON, unknown method, etc.): response is JSON-RPC **`error`** with integer **`code`** (e.g. `-32700` parse error, `-32601` method not found). **HTTP status is 200** for these encoded responses (see `writeJSONRPCError`).
- **Tool execution failure** (`tools/call`): HTTP **200** with a **`result`** object containing **`isError: true`**, **`content`** (text), and no successful `structuredContent` in the error path (`writeJSONRPCToolErrorResult` in `internal/mcp/jsonrpc_handler.go`).
- **Tool success**: **`result`** includes **`content`** (JSON text of payload) and **`structuredContent`** (parsed tool `data`).

## Notes / Limitations

- This is not a stdio-based MCP server. All interactions occur over HTTP.
- **Two wire formats:** Legacy `{tool,input}` vs JSON-RPC `initialize` / `tools/list` / `tools/call`. Pick one consistently for a client; they share the same tool handlers and auth.
- **Anonymous mode:** Effectively no authenticated tools; capabilities still describe the server.
- **Pagination:** Global defaults in capabilities mention `limit` / `cursor` / `nextCursor` / `hasMore`; **`board.get`** uses **`cursorByColumn`** (per column key) — see `tool_catalog.go` and `pagination.futureSpecialCases` in capabilities.
- **`sprints.update` `patch`:** Catalog documents `plannedStartAt` / `plannedEndAt` as **Unix milliseconds** (integers), not RFC3339 strings (unlike `sprints.create`).
- **JSON-RPC `serverInfo.version`:** The value returned by `initialize` is the string **`1.0.0`** in code (`internal/mcp/jsonrpc_handler.go`), not necessarily the Scrumboy app version from `internal/version`.
- **`plannedTools`:** Currently always empty / omitted; there is no separate catalog of unimplemented tools in responses.
