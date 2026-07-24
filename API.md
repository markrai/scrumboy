# Scrumboy MCP HTTP API

This API is intended for programmatic clients (e.g., agents or integrations), not direct browser use.

This document describes the **Model Context Protocol (MCP) HTTP surface** implemented under `internal/mcp` and mounted by the Scrumboy HTTP server. It reflects **current behavior only**, not a roadmap.

**Interfaces:** legacy `/mcp` and canonical MCP Streamable HTTP `/mcp/rpc`. They share tool implementations but not wire semantics or OAuth audience.

The MCP adapter is constructed in `cmd/scrumboy/main.go` with server mode from configuration and registered on the main `httpapi` server.

---

## Transport

- **`GET /mcp`** - Capabilities discovery (same `data` as `system_getCapabilities` via POST).
- **`POST /mcp`** - Invoke a single tool.

There are **no per-tool URL paths**. Every tool is invoked by posting a JSON body to `POST /mcp`.

Tool names are case-sensitive.

**POST body envelope:**

```json
{
  "tool": "tool.name",
  "input": {}
}
```

- `tool` (string, required): registered tool name.
- `input` (object, required for tools that decode structured input): pass `{}` when a tool expects no fields. Omitting `input` or sending JSON `null` may cause decoding errors for tools expecting an object.

Unknown top-level fields on the POST body are rejected (strict JSON decode).

**Other methods** on `/mcp` return **405** with error code `METHOD_NOT_ALLOWED`.

Responses use `Cache-Control: no-store` and `Content-Type: application/json; charset=utf-8`.

---

## JSON-RPC MCP endpoint (spec-compatible)

In addition to the **`/mcp`** HTTP interface above, Scrumboy exposes a Model Context Protocol (MCP) oriented endpoint that speaks **JSON-RPC 2.0**. This is a **separate** transport from the legacy `{ "tool", "input" }` POST body; both are mounted on the same MCP adapter.

**Endpoint:** `/mcp/rpc`. Native MCP clients must be configured with this exact URL. `/mcp/rpc/` returns a same-origin 308 redirect to the canonical path before authentication.

**Intended use:** MCP-style clients (e.g. Claude Desktop, agent frameworks) that expect JSON-RPC framing.

### Protocol

- Uses **JSON-RPC 2.0**.
- **`jsonrpc`:** must be the string `"2.0"`.
- **`method`:** required (string).
- **`id`:** required for supported request-only methods (`initialize`, `ping`, `tools/list`, `tools/call`). Omitted for **notifications** (see below). A request-only method without an `id` is rejected with HTTP **400** and is not dispatched. For **parse errors**, the response uses `"id": null` per JSON-RPC.
- **`params`:** `initialize` requires `protocolVersion`, `capabilities`, `clientInfo.name`, and `clientInfo.version`. `tools/list` may omit it. For **`tools/call`**, `params` must be a JSON object (see below).

After Origin validation and authentication, **non-POST** methods receive an empty **405 Method Not Allowed** with `Allow: POST`; authenticated GET does not open an SSE stream. **HTTP status** for normal JSON-RPC requests with IDs is **200** for both success and protocol error objects in the body. Accepted notifications, including **`notifications/initialized`**, receive empty **202 Accepted** responses. Structurally rejected notifications receive **400** and have no side effect; an emitted JSON-RPC error uses `id: null`.

**Example request:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-11-25",
    "capabilities": {},
    "clientInfo": {"name": "example", "version": "1"}
  }
}
```

### Supported methods

**`initialize`** — Initial handshake. Requires `id` and the fields shown above. Supported stable versions are `2025-03-26`, `2025-06-18`, and `2025-11-25`; a supported requested version is echoed, while an unsupported initialize version negotiates to latest stable `2025-11-25`.

**`notifications/initialized`** or **`initialized`** — Client acknowledgment after `initialize`. Must be sent **without** `id` (notification). Server responds with **202** and no JSON body. If an `id` is present, the server rejects the call.

**`tools/list`** — Requires `id`. Returns `{ "tools": [ ... ] }`. Each entry has `name`, `description`, and `inputSchema` (JSON Schema object). The array includes **every** tool in `implementedTools` from the in-code catalog (`internal/mcp/tool_catalog.go`), not a subset. The transport is stateless and does not enforce a prior `initialize`, but full-mode authentication protects this request like every other `/mcp/rpc` request.

**`tools/call`** — Invokes a tool by name. Requires `id` and a **`params` object** containing:

- **`name`** (string, required): exact registered tool name (same names as `POST /mcp`’s `tool` field).
- **`arguments`** (object, optional): tool input; if omitted, treated as `{}`.

`params` is unmarshaled with the Go JSON package **without** `DisallowUnknownFields`, so **extra keys** on `params` beside `name` / `arguments` are **ignored** (not rejected).

**`tools/call` uses the same tool registry as `POST /mcp`**. For tools that have a catalog definition, the server performs a **lightweight check** that JSON Schema `required` top-level properties are present in `arguments` before calling the handler; full JSON Schema validation is not performed. **Unknown tool names** produce an HTTP **200** JSON-RPC **`result`** with **`isError: true`** and a text **`content`** message **`tool not found`** (not a JSON-RPC top-level **`error`** object with `-32601`). Invalid/missing `params` shape still yields JSON-RPC **`error`** (e.g. `-32602` **invalid params**).

**Example `tools/call`:**

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "todos_create",
    "arguments": {
      "projectSlug": "my-project",
      "title": "New todo"
    }
  }
}
```

### Response format

All JSON-RPC **responses with a body** include `"jsonrpc": "2.0"` and preserve the request `id` (except parse errors → `id: null`). This endpoint **does not** use the legacy `ok` / `data` / `meta` envelope.

**Success (`tools/call` result shape):**

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\"todo\":{\"projectSlug\":\"my-project\",\"localId\":1}}"
      }
    ],
    "structuredContent": {
      "todo": {
        "projectSlug": "my-project",
        "localId": 1
      }
    }
  }
}
```

**`structuredContent`** is the tool’s result value (same conceptual payload as legacy `data`). **`content`** is a single MCP-style **text** block whose **`text`** field is JSON **string** of that same payload (from `json.Marshal` in `internal/mcp/jsonrpc_handler.go`).

**Error:**

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "error": {
    "code": -32602,
    "message": "invalid params"
  }
}
```

**Typical JSON-RPC error codes:** `-32700` parse error, `-32600` invalid request, `-32601` method/tool not found, `-32602` invalid params / validation, `-32603` internal error. The `message` string is human-readable; some errors include optional `data`.

### Notes

- **Stateless HTTP:** there is no server-side session between requests; behavior does not depend on having called `initialize` first for `tools/list` or `tools/call`.
- **`initialize` is supported** for clients that expect the handshake; it is **not** enforced before discovery or tool calls on this server.
- **Authentication** is endpoint-specific. `/mcp/rpc` accepts a session cookie, static `sb_…` API token, or a Scrumboy OAuth token bound to `<origin>/mcp/rpc`; `/mcp` accepts only the first two. See [Authentication and capability model](#authentication-and-capability-model).
- **Streamable HTTP media:** POST requires `Content-Type: application/json` (optional UTF-8 charset). Native clients send `Accept: application/json, text/event-stream`; missing Accept and `*/*` remain tolerated, while a restrictive value must allow both types. Accepted notifications return 202 without a body or content type.
- **Protocol header:** after initialization, send `MCP-Protocol-Version`. Missing defaults to `2025-03-26`; malformed, duplicate, or unsupported values return 400.
- **Origin:** validation runs before authentication. A supplied Origin must match the trusted public origin; invalid Origin returns empty 403 without an OAuth challenge. Non-browser requests may omit Origin.
- **Stateless JSON-only:** Scrumboy emits no MCP session ID and offers no SSE GET stream, resumability, or server-initiated requests.
- The **legacy `GET` / `POST /mcp`** endpoint remains **unchanged** and is documented in the sections above.

---

## Response envelopes

### Success

```json
{
  "ok": true,
  "data": {},
  "meta": {}
}
```

- `data` holds the tool result (shape varies by tool).
- `meta` is **always** a JSON object on success (empty if the tool has no metadata).
- List-style tools return their array under **`data.items`** unless noted otherwise.

### Error

```json
{
  "ok": false,
  "error": {
    "code": "NOT_FOUND",
    "message": "not found",
    "details": {}
  }
}
```

- `details` is always present; it is an object when the adapter has nothing to attach (`{}`).
- HTTP status codes generally align with error codes (e.g. 401 for `AUTH_REQUIRED`, 403 for `CAPABILITY_UNAVAILABLE`, 404 for `NOT_FOUND`), but exact mappings may vary by handler.

---

## Authentication and capability model

**Server mode** (`SCRUMBOY_MODE` / config): `full` or `anonymous`.

**Session (cookie):** In `full` mode, the adapter reads the `scrumboy_session` cookie and loads the user into request context when the cookie is valid.

**Bearer credentials:** In `full` mode, clients may send an opaque static secret minted via [`/api/me/tokens`](#api-access-tokens-rest) (prefix `sb_`, stored as a hash server-side). `/mcp/rpc` additionally accepts Scrumboy's own opaque OAuth access tokens only when the stored audience equals its trusted canonical `<origin>/mcp/rpc` resource.

**Precedence and endpoint boundary:** If the request includes a **`Bearer` authorization attempt** (scheme `Bearer` per RFC 9110, with the credential in the segment after the first space; trim applies **only** to that credential string), the adapter validates that token and **does not** fall back to the session cookie when validation fails. Legacy `GET /mcp` and `POST /mcp` accept static tokens only; an OAuth token or any other failed Bearer returns the existing **401** `AUTH_REQUIRED` envelope and no OAuth challenge. The whole `/mcp/rpc` transport accepts static or correctly resource-bound OAuth tokens; missing credentials return an empty **401** RFC 9728 challenge, while invalid Bearers add `error="invalid_token"`. No transport 401 contains a JSON-RPC `result` or `CallToolResult`.

**Anonymous mode:** Session cookies and Bearer tokens are **not** applied for MCP (same anonymous boundary as the documented HTTP API for cookies).

**Bootstrap:** If there are **no users** in the database, authenticated MCP tools are treated as unavailable until bootstrap completes (`CountUsers == 0`).

**Capabilities `auth` object:** Field **`mode`** keeps the existing meaning (`sessionCookie` or `disabled`). Field **`authMethods`** (e.g. `["sessionCookie","bearer"]` in `full` mode) lists mechanisms the adapter supports; clients should not treat `mode` as an exhaustive list of auth options.

**Typical codes:** `AUTH_REQUIRED` when the transport rejects the principal (failed Bearer, or tool needs a signed-in user but none is in context) or when a tool requires sign-in without a session/API token. `CAPABILITY_UNAVAILABLE` when the server is in **anonymous mode**, or **before bootstrap** (no users yet), or the tool is otherwise gated as unavailable.

**Practical rule:** Almost all project-scoped tools require full mode, post-bootstrap, and a valid principal. Legacy `/mcp` preserves unauthenticated capability/bootstrap inspection. In full mode, `/mcp/rpc` always authenticates before method, media, protocol, or JSON-RPC dispatch; use a cookie, static token, or correctly resource-bound Scrumboy OAuth token.

## Authentication example (curl)

Use cookie-jar mode for authenticated MCP tools, or a bearer token (see [API access tokens](#api-access-tokens-rest)).

If the server is not bootstrapped yet (no users), create the first user:

```bash
curl -c cookies.txt -X POST http://localhost:8080/api/auth/bootstrap \
  -H "Content-Type: application/json" \
  -H "X-Scrumboy: 1" \
  -d '{"email":"user@example.com","password":"password","name":"User"}'
```

If users already exist, log in:

```bash
curl -c cookies.txt -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -H "X-Scrumboy: 1" \
  -d '{"email":"user@example.com","password":"password"}'
```

Then call MCP with the session cookie:

```bash
curl -b cookies.txt -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{"tool":"projects_list","input":{}}'
```

### OIDC login (optional)

When the server is configured with OIDC environment variables (`SCRUMBOY_OIDC_ISSUER`, `SCRUMBOY_OIDC_CLIENT_ID`, `SCRUMBOY_OIDC_CLIENT_SECRET`, `SCRUMBOY_OIDC_REDIRECT_URL`), two additional endpoints become available:

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/api/auth/oidc/login?return_to=/` | Redirects browser to the IdP authorization endpoint |
| `GET` | `/api/auth/oidc/callback?code=...&state=...` | Handles the IdP callback, creates a session, and redirects to `return_to` |

These are browser-redirect endpoints, not JSON APIs. After successful OIDC login, the user receives a standard `scrumboy_session` cookie. MCP and REST access work identically to password-based sessions.

`GET /api/auth/status` includes `oidcEnabled` (bool) and `localAuthEnabled` (bool) when OIDC is configured, plus `pushConfigured` (bool). `pushConfigured` is true only when Web Push is **effectively enabled** (validated matching VAPID key pair, valid/default subscriber, full mode) — not merely when env key strings are non-empty. In full mode, **signed-in** responses also include `push: { "state": "...", "reason": "..." | null }` with the prepared status (`enabled`, `not_configured`, `invalid`, `unavailable` and reasons such as `invalid_vapid_public_key`, `invalid_vapid_private_key`, `invalid_subscriber`, `initialization_failed`). Unauthenticated and anonymous-mode status omit the detailed `push` object. See [`docs/vapid.md`](docs/vapid.md#effective-enablement-not-just-keys-present).

### API access tokens (REST)

Manage opaque MCP/API tokens while logged in (session cookie). Mutating endpoints require `X-Scrumboy: 1` like other `/api` writes.

| Method | Path | Body | Success |
|--------|------|------|---------|
| `GET` | `/api/me/tokens` | — | `200` JSON `{ "items": [ { "id", "name?", "createdAt", "lastUsedAt?", "revokedAt?" } ] }` (no secret) |
| `POST` | `/api/me/tokens` | `{ "name": "optional label" }` | `201` JSON `{ "id", "name?", "createdAt", "token" }` — **`token` is shown only on create** |
| `DELETE` | `/api/me/tokens/{id}` | — | `204` (revoke / soft-delete) |

Create a token (after login, with session + header):

```bash
curl -b cookies.txt -X POST http://localhost:8080/api/me/tokens \
  -H "Content-Type: application/json" \
  -H "X-Scrumboy: 1" \
  -d '{"name":"Claude"}'
```

Then call MCP with **Bearer** (no cookie required for this path):

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sb_paste_token_from_create_response" \
  -d '{"tool":"projects_list","input":{}}'
```

---

## Canonical identities (MCP)

Tools use these **public** identifiers as primary keys in inputs and outputs:

- **Project:** `projectSlug`
- **Todo:** `projectSlug` + `localId` (no global todo id in MCP todo/board shapes)
- **Sprint:** `projectSlug` + `sprintId` - `sprintId` is the **stored sprint row id** (see sprint list/get); sprint payloads also include `number` for display ordering
- **Mine-scope tag:** `tagId` (current user’s tag library)
- **Project-scope tag:** `projectSlug` + `tagId` (tag row scoped to that project; not user-owned)
- **Project member / membership target:** `projectSlug` + `userId`
- **Available user (invite list):** `userId` (from `members_listAvailable`)

`system_getCapabilities` includes an `identity` object echoing some of these patterns.

**Note:** `projects_list` returns **`projectId`** on each item in addition to `projectSlug`. MCP mutations still key off **`projectSlug`**. `projectId` is returned for informational purposes only and is not used as an input identifier in MCP tools.

---

## Implemented tools (summary)

Grouped by domain. All are listed in `implementedTools` from capabilities.

**system**

- `system_getCapabilities` - Server mode, auth snapshot, identity/pagination hints, full tool list.

**projects**

- `projects_list` - Projects visible to the user (with role).

**board**

- `board_get` - Paged board view per workflow column (special pagination; see below).

**todos**

- `todos_create`, `todos_get`, `todos_search`, `todos_update`, `todos_delete`, `todos_move`

**sprints**

- `sprints_list`, `sprints_get`, `sprints_getActive`, `sprints_create`, `sprints_activate`, `sprints_close`, `sprints_update`, `sprints_delete`

**tags**

- `tags_listProject`, `tags_listMine`, `tags_updateMineColor`, `tags_deleteMine`, `tags_updateProjectColor`, `tags_deleteProject`

**members**

- `members_list`, `members_listAvailable`, `members_add`, `members_updateRole`, `members_remove`

**Planned tools:** none exposed in capabilities today (`plannedTools` omitted when empty).

---

## Tool reference

Conventions:

- Inputs use **camelCase** JSON keys matching the Go structs; unknown keys are rejected where `decodeInput` is used.
- Auth gates omitted below repeat: **anonymous mode** → `CAPABILITY_UNAVAILABLE`; **pre-bootstrap** → `CAPABILITY_UNAVAILABLE`; **no authenticated principal** (no valid session or API token on the request) → `AUTH_REQUIRED` for tools that require it.

### `system_getCapabilities`

- **Purpose:** Describe server, auth, identities, pagination notes, and implemented tools.
- **Input:** `{}` (use empty object for POST).
- **Output:** `data` = capabilities object: `serverMode`, `auth`, `bootstrapAvailable`, `identity`, `pagination`, `implementedTools`, optional `plannedTools`.
- **Meta:** e.g. `adapterVersion` (integer).
- **Example (GET or POST):**  
  `POST /mcp` `{"tool":"system_getCapabilities","input":{}}`  
  → `ok: true`, `data.implementedTools` = full tool array.

### `projects_list`

- **Purpose:** List projects for the current user with role.
- **Input:** `{}`
- **Output:** `data.items` - array of projects (`projectSlug`, `projectId`, `name`, `image`, `dominantColor`, `defaultSprintWeeks`, `expiresAt`, `createdAt`, `updatedAt`, `role`).

### `board_get`

- **Purpose:** Board snapshot with optional tag/search/sprint filters and **per-column** pagination.
- **Input:** `projectSlug` (required); optional `tag`, `search`, `sprintId` (sprint row id; must belong to the project when set); optional `limit` (default 20, max 100); optional `cursorByColumn` (map column key → opaque cursor string). Omitting `sprintId` applies no sprint-based filter on the board query (internal mode `none`).
- **Output:** `data.project` (`projectSlug`, `name`, `role`), `data.columns` (each: `key`, `name`, `isDone`, `items` as todo-shaped objects).
- **Meta:** `nextCursorByColumn`, `hasMoreByColumn`, `totalCountByColumn` (per column key). See **Board pagination** below.
- **Note:** Not available in anonymous mode or before bootstrap; requires sign-in.

### Todos

| Tool | Input (summary) | Output (summary) |
|------|-----------------|------------------|
| `todos_create` | `projectSlug`, `title`, optional `body`, `tags`, `columnKey`, `estimationPoints`, `sprintId`, `assigneeUserId`, `position` | `data.todo` |
| `todos_get` | `projectSlug`, `localId` | `data.todo` |
| `todos_search` | `projectSlug`, `query`, optional `limit`, `excludeLocalIds` | `data.items` (lightweight search hits) |
| `todos_update` | `projectSlug`, `localId`, `patch` (JSON patch object) | `data.todo` |
| `todos_delete` | `projectSlug`, `localId` | `data` with `status: "deleted"`, `projectSlug`, `localId` |
| `todos_move` | `projectSlug`, `localId`, `toColumnKey`, optional `afterLocalId`, `beforeLocalId` | `data.todo` |

Column keys accept common aliases (normalized internally). Todo payloads use **`localId`** and **`projectSlug`**; they do not expose the internal global todo id.

### Sprints

Shared inputs: many tools use `projectSlug` only or `projectSlug` + `sprintId` (stored id).

| Tool | Input | Output |
|------|-------|--------|
| `sprints_list` | `projectSlug` | `data.items` (sprint rows + counts), `meta.unscheduledCount` |
| `sprints_get` | `projectSlug`, `sprintId` | `data.sprint` |
| `sprints_getActive` | `projectSlug` | `data.sprint` - sprint object or JSON `null` when there is no active sprint |
| `sprints_create` | `projectSlug`, `name`, `plannedStartAt`, `plannedEndAt` (ISO-8601 strings) | `data.sprint` |
| `sprints_activate` | `projectSlug`, `sprintId` | `data.sprint` |
| `sprints_close` | `projectSlug`, `sprintId` | `data.sprint` (closed) |
| `sprints_update` | `projectSlug`, `sprintId`, `patch` | `data.sprint` |
| `sprints_delete` | `projectSlug`, `sprintId` (maintainer+) | `data` with `status: "deleted"`, `projectSlug`, `sprintId` |

Activate/close enforce sprint state (e.g. planned vs active); violations return `VALIDATION_ERROR` with details.

### Tags

| Tool | Input | Output |
|------|-------|--------|
| `tags_listProject` | `projectSlug` | `data.items` (`tagId`, `name`, `count`, `color`, `canDelete`) |
| `tags_listMine` | `{}` | `data.items` (mine tags; no `count`) |
| `tags_updateMineColor` | `tagId`, `color` (hex or `null` to clear) | `data.tag` |
| `tags_deleteMine` | `tagId` | `data.deleted` `{ tagId }` - only if tag is in the viewer’s mine list, then store delete |
| `tags_updateProjectColor` | `projectSlug`, `tagId`, `color` | `data.tag` - **maintainer+**; tag must appear in that project's `tags_listProject` set. Color semantics follow tag scope: **board-scoped** tags update shared `tags.color` (visible to all members); **user-owned** tags update this viewer's per-viewer preference in `user_tag_colors` (same as durable HTTP board color updates / `tags_updateMineColor`) |
| `tags_deleteProject` | `projectSlug`, `tagId` | `data.deleted` `{ projectSlug, tagId }` - **maintainer+**; tag must exist as a **project-scoped** tag in that project |

### Members

| Tool | Input | Output |
|------|-------|--------|
| `members_list` | `projectSlug` | `data.items` (member rows with normalized roles where implemented) |
| `members_listAvailable` | `projectSlug` | `data.items` (users not in project) - **maintainer+** |
| `members_add` | `projectSlug`, `userId`, `role` (`maintainer` \| `contributor` \| `viewer` only) | `data.member` |
| `members_updateRole` | `projectSlug`, `userId`, `role` (same three) | `data.member` |
| `members_remove` | `projectSlug`, `userId` | `data.removed` `{ projectSlug, userId }` |

Member list payloads normalize legacy role strings where the adapter applies mapping (`owner`→`maintainer`, `editor`→`contributor`).

`members_updateRole`: self-demotion and last-maintainer demotion → `CONFLICT`.  
`members_remove`: last maintainer removal → `VALIDATION_ERROR` (store mapping).

---

## Board pagination (`board_get`)

This is **not** a single cursor for the whole board.

- **`limit`:** Maximum todos returned **per workflow column** (default 20, clamped 1-100).
- **`cursorByColumn`:** Map from **column key** (string) to an **opaque** cursor token (base64url). Cursors are produced by the server; clients should not parse them.
- **`meta.nextCursorByColumn`:** Per-column next cursor, or `null` when there is no next page.
- **`meta.hasMoreByColumn`:** Whether more todos exist in that column for the same filters.
- **`meta.totalCountByColumn`:** Total matching todos in that column (independent of the current page).

Invalid column keys in `cursorByColumn` or malformed cursors → `VALIDATION_ERROR` with field hints.

---

## REST: Dashboard assigned todos (`GET /api/dashboard/todos`)

The web app and other REST clients use this endpoint (separate from MCP). In **full** mode it requires a valid **session cookie** or **`Authorization: Bearer`** API token.

Query parameters:

- **`limit`** (optional): page size; default **20**, maximum **100**.
- **`sort`** (optional): **`activity`** (default) or **`board`**. Invalid or empty values are treated as **`activity`** (backward compatible).
- **`cursor`** (optional): pagination token from the previous JSON response’s **`nextCursor`** field.

**Activity sort** (default): rows are ordered by **`updated_at` DESC, `id` DESC**. The cursor is **`updatedAtMs:id`** (two integers, colon-separated, Unix ms for the todo’s `updated_at`).

**Board sort** (`sort=board`): rows are ordered by **`project_id` ASC, workflow column `position` ASC, `rank` ASC, `id` ASC**, matching board order within each project. Cross-project order follows numeric project id, not name or recency. The cursor is **`projectId:wcPosition:rank:todoId`** (four integers, colon-separated).

A **`cursor`** that does not match the selected **`sort`** (for example, an activity cursor while `sort=board`) is rejected with **HTTP 400** and error code **`VALIDATION_ERROR`**.

---

## Error codes

- **`AUTH_REQUIRED`** - Sign-in required (including some store unauthorized paths mapped from the store layer).
- **`CAPABILITY_UNAVAILABLE`** - Anonymous server mode, pre-bootstrap, or a tool that is unavailable in the current mode.
- **`NOT_FOUND`** - Unknown tool name, or resource not found in the requested scope.
- **`FORBIDDEN`** - Authenticated but not allowed (e.g. role too low for the operation).
- **`VALIDATION_ERROR`** - Invalid JSON input, missing fields, invalid values, or store validation (e.g. sprint state, last-maintainer removal rules).
- **`CONFLICT`** - Store-reported conflict (e.g. duplicate member, role demotion rules).
- **`INTERNAL`** - Unexpected server or store failure.
- **`METHOD_NOT_ALLOWED`** - Any HTTP method other than `GET` or `POST` on `/mcp`.

Some handlers return **`FORBIDDEN`** with a clear message where **`mapStoreError`** would map the same store error to **`AUTH_REQUIRED`**; both patterns exist in the current code.

---

## Notes and guarantees

1. **Public identifiers first:** Mutations and reads are keyed by **`projectSlug`**, **`localId`**, and similar fields - not internal numeric ids for todos or projects in MCP command shapes (except `projectId` on list output as noted).
2. **Capabilities match implementation:** `implementedTools` is the authoritative list of POST tool names.
3. **Narrower than REST:** Some MCP tools intentionally pre-check scope (e.g. mine-tag delete via library membership) or map errors deterministically; behavior may differ from every REST edge case.
4. **Anonymous MCP:** Tag, member, board, todo, and sprint tools are **not** offered in anonymous server mode through MCP (`CAPABILITY_UNAVAILABLE`), even if anonymous boards exist elsewhere in the product.

