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

**`tools/call` uses the same tool registry as `POST /mcp`**. For tools that have a catalog definition, the server performs a **lightweight check** that JSON Schema `required` top-level properties are present in `arguments` before calling the handler; full JSON Schema validation is not performed. **Unknown tool names** produce an HTTP **200** JSON-RPC **`result`** with **`isError: true`**, a text **`content`** message **`tool not found`**, and sanitized machine-readable **`structuredContent`** (not a JSON-RPC top-level **`error`** object with `-32601`). Invalid/missing `params` shape still yields JSON-RPC **`error`** (e.g. `-32602` **invalid params**).

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

**`structuredContent`** is the tool’s result value (same conceptual payload as
legacy `data`). **`content`** is a single MCP-style **text** block whose
**`text`** field is a JSON **string** of that same payload (from `json.Marshal`
in `internal/mcp/jsonrpc_handler.go`). Four tools also copy explicitly approved,
public legacy metadata beside their existing data fields:
`system_getCapabilities` adds `adapterVersion`; `sprints_list` adds
`unscheduledCount`; `dashboard_listTodos` adds `nextCursor` and `hasMore`; and
`board_get` adds `nextCursorByColumn`, `hasMoreByColumn`, and
`totalCountByColumn`. The text block serializes the same enriched object.
Unlisted handler metadata is not exposed over JSON-RPC. If an existing data
field collides with an approved metadata name, the data field wins.

**Tool execution error:**

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "invalid sort"
      }
    ],
    "structuredContent": {
      "code": "VALIDATION_ERROR",
      "message": "invalid sort",
      "details": {
        "field": "sort"
      }
    },
    "isError": true
  }
}
```

Tool failures retain HTTP **200** and MCP **`isError: true`**. The text block
retains the existing human-readable message for compatibility.
**`structuredContent`** contains the stable adapter **`code`**, the same
**`message`**, and allowlisted **`details`**. It deliberately does not copy the
legacy transport's HTTP status. For **`INTERNAL`**, **`details`** is always
`{}` and **`message`** is always `internal error`; the full cause or invariant
message is written only to the server log.

**JSON-RPC protocol error:**

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
- Detail keys are allowlisted at the MCP serialization boundary. `INTERNAL`
  errors always return message `internal error` with `{}` details and never
  expose database, infrastructure, or invariant text; the underlying cause is
  recorded in the server log.
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

When the server is configured with OIDC environment variables (`SCRUMBOY_OIDC_ISSUER`, `SCRUMBOY_OIDC_CLIENT_ID`, `SCRUMBOY_OIDC_CLIENT_SECRET`, `SCRUMBOY_OIDC_REDIRECT_URL`), these endpoints become available:

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/api/auth/oidc/login?return_to=/` | Redirects browser/PWA to the IdP authorization endpoint |
| `GET` | `/api/auth/oidc/callback?code=...&state=...` | Handles the IdP callback; browser flows create a session and redirect to `return_to`; mobile-marked flows mint a one-time handoff and redirect to `com.markrai.scrumboy://oidc/callback` |
| `POST` | `/api/auth/oidc/mobile/start` | JSON start for the packaged Android app; returns `authorizationUrl` + `flowState` |
| `POST` | `/api/auth/oidc/mobile/exchange` | JSON exchange for the packaged Android app; sets the normal `scrumboy_session` cookie and returns `returnTo` only |

Browser login/callback remain redirect endpoints, not JSON APIs. Mobile start/exchange are unauthenticated JSON routes used by the Android shell; they reuse the same IdP client and HTTPS redirect URL. After successful OIDC login (browser or mobile exchange), the user receives a standard `scrumboy_session` cookie. MCP and REST access work identically to password-based sessions. See [`docs/oidc.md`](docs/oidc.md) and [`docs/authentication-api.md`](docs/authentication-api.md).

`GET /api/auth/status` includes `oidcEnabled` (bool), `mobileOidcEnabled` (bool), and `localAuthEnabled` (bool) when OIDC is configured, plus `pushConfigured` (bool). `pushConfigured` is true only when Web Push is **effectively enabled** (validated matching VAPID key pair, valid/default subscriber, full mode) — not merely when env key strings are non-empty. In full mode, **signed-in** responses also include `push: { "state": "...", "reason": "..." | null }` with the prepared status (`enabled`, `not_configured`, `invalid`, `unavailable` and reasons such as `invalid_vapid_public_key`, `invalid_vapid_private_key`, `invalid_subscriber`, `initialization_failed`). Unauthenticated and anonymous-mode status omit the detailed `push` object. See [`docs/vapid.md`](docs/vapid.md#effective-enablement-not-just-keys-present).

### API access tokens (REST)

Manage opaque MCP/API tokens while logged in (session cookie). Mutating endpoints require `X-Scrumboy: 1` like other `/api` writes.

| Method | Path | Body | Success |
|--------|------|------|---------|
| `GET` | `/api/me/tokens` | — | `200` JSON `{ "items": [ { "id", "name?", "createdAt", "lastUsedAt?", "revokedAt?", "isService" } ] }` (no secret) |
| `POST` | `/api/me/tokens` | `{ "name": "optional label", "isService": false }` | `201` JSON `{ "id", "name?", "createdAt", "token", "isService" }` — **`token` is shown only on create** |
| `DELETE` | `/api/me/tokens/{id}` | — | `204` (revoke / soft-delete) |

Revoked tokens stay in the owning user's `GET /api/me/tokens` list with `revokedAt` set; there is no
endpoint that removes them.

`isService` (optional, default `false`; omitting it creates a personal token as before) marks a
normal user-owned token for bot/automation use. It is not a service account: while its user exists,
a service token authenticates as that user with exactly that user's permissions, like any other
token. The flag only changes what is kept when the user is deleted (REST `DELETE
/api/admin/users/{id}` or MCP `admin_deleteUser`). In the same transaction as the deletion:

- the metadata of each of the user's service tokens, active or already revoked, is copied into the
  owner-only [service token archive](#service-token-archive), together with a snapshot of who held
  it and which owner deleted them;
- all of the user's token rows, personal and service, are then deleted with the user, so every one
  of their secrets stops authenticating.

If the deletion fails, nothing is archived and the tokens are untouched. Service tokens are not
moved to another account: archived records never appear in anyone's `/api/me/tokens`, and the
archive holds no secret or hash, so an archived token cannot be reactivated. Automation that used a
deleted user's token needs a new token.

Create a token (after login, with session + header):

```bash
curl -b cookies.txt -X POST http://localhost:8080/api/me/tokens \
  -H "Content-Type: application/json" \
  -H "X-Scrumboy: 1" \
  -d '{"name":"Claude"}'
```

Create a service token for unattended automation (e.g. a CI job), so its metadata survives if the
minting user's account is later removed:

```bash
curl -b cookies.txt -X POST http://localhost:8080/api/me/tokens \
  -H "Content-Type: application/json" \
  -H "X-Scrumboy: 1" \
  -d '{"name":"wiki-freshness-check-ci","isService":true}'
```

Then call MCP with **Bearer** (no cookie required for this path):

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sb_paste_token_from_create_response" \
  -d '{"tool":"projects_list","input":{}}'
```

### Service token archive

Retained metadata about service tokens of deleted users, for offboarding and governance review. No
web UI for it exists yet. Owner-only, with a session cookie; `DELETE` also needs `X-Scrumboy: 1`.
Plain users get `403`; admins who are not owners get `401` (the same as other owner-only admin
actions).

| Method | Path | Success |
|--------|------|---------|
| `GET` | `/api/admin/service-token-archive?limit=50&before={id}` | `200` JSON `{ "items": [ … ], "nextBefore": id \| null }` |
| `DELETE` | `/api/admin/service-token-archive?archivedBefore={RFC 3339}` | `200` JSON `{ "deleted": n }` |

**Listing.** Items come newest archive first (descending `id`). `limit` is 1–200 (default 50).
`before` is an exclusive cursor: omit it for the first page, then pass the previous response's
`nextBefore`, which is `null` on the last page.

Each item is `{ "id", "tokenId", "name?", "createdAt", "lastUsedAt?", "revokedAt", "revokedOnArchive",
"originUser": { "id", "email", "name" }, "archivedAt", "archivedBy": { "id", "email" } }`:

- `id` is the archive record's own id (and the pagination cursor).
- `tokenId` is the historical id of the deleted token. It is not a credential; token ids are never
  reused, so it identifies one historical token.
- For a token that was already revoked before its user was deleted, `revokedOnArchive` is `false`
  and `revokedAt` is its original revocation time.
- For a token that was still active when its user was deleted, `revokedOnArchive` is `true` and
  `revokedAt` equals `archivedAt`, the time of the deletion.
- `originUser` (the user who held the token) and `archivedBy` (the owner who deleted that user) are
  snapshots taken at deletion time. User ids can be reused after an account is deleted, so the
  recorded `id` may now belong to a different account. Identify people by the recorded
  email and name, never by looking the id up among current users.

**Personal data and retention.** Archive records contain identifying information: the deleted
user's name and email and the deleting owner's email. These remain after both accounts are deleted,
until an owner purges them. Records cannot be edited, but they are not permanent. `DELETE`
permanently removes every record archived strictly before `archivedBefore` (an RFC 3339 timestamp
with a time zone, e.g. `2026-01-01T00:00:00Z`), so owners can apply their own retention policy. It
affects only archive records, never live tokens of existing users.

---

## Canonical identities (MCP)

Tools use these **public** identifiers as primary keys in inputs and outputs:

- **Project:** `projectSlug`
- **Todo:** `projectSlug` + `localId` (no global todo id in MCP todo/board shapes)
- **Sprint:** `projectSlug` + `sprintId` - `sprintId` is the **stored sprint row id** (see sprint list/get); sprint payloads also include the distinct project-local `number` for display ordering and REST board filtering
- **Mine-scope tag:** `tagId` (current user’s tag library)
- **Project-scope tag (durable projects):** grouped by **canonical name**. `tags_listProject` returns one logical entry per canonical name (names are compared after canonicalization, so legacy `make space` and `make-space` rows collapse into one `make-space` entry); a `tagId` is present **only** for board-scoped tags (not user-owned). Grouped personal labels omit `tagId` and are addressed by `projectSlug` + `tagName`. A legacy row whose stored name cannot be canonicalized keeps its raw stored name as the label, and that label is what `tagName` and the board `tag` filter accept for it.
- **Project-scope tag (temporary boards):** **not grouped.** Boards with an expiry keep the row-level projection — one entry per tag row, each with a real `tagId` — because their colors and deletions are still addressed by `tagId`.
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
- `projects_create`, `projects_update`, `projects_delete` - Project CRUD (maintainer+ for update/delete; the creator becomes maintainer).

**board**

- `board_get` - Paged board view per workflow column (special pagination; see below).

**dashboard**

- `dashboard_getSummary`, `dashboard_listTodos` - Cross-project "my work" summary and paginated assigned-todo list for the signed-in user.

**metrics**

- `metrics_getBurndown`, `metrics_getBacklogSize` - Project (or sprint-scoped) burndown and backlog-size time series.

**admin**

- `admin_listUsers`, `admin_updateUserRole`, `admin_deleteUser` - System-level user management (owner/admin system role, not project role).

**todos**

- `todos_create`, `todos_get`, `todos_search`, `todos_update`, `todos_delete`, `todos_move`, `todos_linksList`, `todos_linkAdd`, `todos_linkRemove`

**sprints**

- `sprints_list`, `sprints_get`, `sprints_getActive`, `sprints_create`, `sprints_activate`, `sprints_close`, `sprints_update`, `sprints_delete`

**tags**

- `tags_listProject`, `tags_listMine`, `tags_updateMineColor`, `tags_deleteMine`, `tags_updateProjectColor`, `tags_deleteProject`

**members**

- `members_list`, `members_listAvailable`, `members_add`, `members_updateRole`, `members_remove`

**workflow**

- `workflow_list`, `workflow_create`, `workflow_update`, `workflow_delete` - manage a project's workflow columns (board lanes).

**priorities**

- `priorities_list`, `priorities_create`, `priorities_update`, `priorities_delete` - list and manage a project's ordered priority tiers.

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
- **Metadata:** legacy `/mcp` returns `meta.adapterVersion` (integer);
  JSON-RPC returns `adapterVersion` beside the capability fields in
  `structuredContent` and text content.
- **Example (GET or POST):**  
  `POST /mcp` `{"tool":"system_getCapabilities","input":{}}`  
  → `ok: true`, `data.implementedTools` = full tool array.

### `projects_list`

- **Purpose:** List projects for the current user with role.
- **Input:** `{}`
- **Output:** `data.items` - array of projects (`projectSlug`, `projectId`, `name`, `image`, `dominantColor`, `defaultSprintWeeks`, `expiresAt`, `createdAt`, `updatedAt`, `role`).

### `projects_create`, `projects_update`, `projects_delete`

- **Purpose:** Project CRUD. Not available in anonymous mode or before bootstrap; requires sign-in.
- **`projects_create`:** `name` (required). Custom workflow columns are not set through this tool; use the `workflow_*` tools afterward. The creating user becomes **maintainer**. Output: `data.project` (same shape as a `projects_list` item).
- **`projects_update`:** `projectSlug`, `patch` (object). Only fields present in `patch` are changed: `name` (string), `defaultSprintWeeks` (integer, `1` or `2`). Neither field accepts `null`. **Maintainer+** required. Output: `data.project`.
- **`projects_delete`:** `projectSlug`. **Maintainer+** required. Anonymous temporary boards cannot be deleted this way (`NOT_FOUND`, matching the store's existence-hiding behavior). Output: `data` with `status: "deleted"`, `projectSlug`, `projectId`.

### `board_get`

- **Purpose:** Board snapshot with optional tag/search/sprint/assignee/priority filters and **per-column** pagination.
- **Input:** `projectSlug` (required); optional `tag` (legacy scalar), `tags` (preferred string array, up to 20 unique names after trim/dedup, logical AND), `search`, `assignee`, `priority`, `sprintId` (the stored sprint row id returned by `sprints_list`, not its project-local `number`; must belong to the project when set); optional `columnKey` (workflow column key; surrounding whitespace is trimmed; omit to return all workflow columns); optional `limit` (default 20, max 100); optional `cursorByColumn` (map column key → opaque cursor string). Omitting `sprintId` or sending `null` applies no sprint-based filter on the board query (internal mode `none`). Nonpositive values return `VALIDATION_ERROR`; missing and cross-project row IDs both return `NOT_FOUND`. An unknown or nonexistent `columnKey` returns `VALIDATION_ERROR` with `field: "columnKey"`.
- **Validation/access precedence:** after authentication and capability checks, malformed input shape, missing `projectSlug`, invalid `limit`, assignee type/grammar, invalid `sort`, invalid `tags` type/items, empty normalized `tags`, more than 20 unique `tags`, and supplying `tag` together with `tags` return their exact validation error before project access. Project access occurs before sprint resolution, workflow/`columnKey` validation, and `cursorByColumn` validation, so denied, missing, or expired projects mask bad `sprintId`, `columnKey`, and `cursorByColumn` values as `NOT_FOUND`. Cursor values are decoded in workflow order for columns that are actually read; a malformed later-lane cursor can follow reads of earlier lanes. When `columnKey` scopes the request to one column, cursors for other valid workflow columns in `cursorByColumn` are ignored and are not decoded. Both MCP transports and the permanent `board.get` alias use this order. REST slug board reads intentionally resolve access before all query validation, so cross-transport first-error precedence differs without changing access rules.
- **Tag filter:** Prefer `tags: ["feature", "ux"]` (logical AND, maximum 20 unique names after trim/dedup). Legacy `tag: "feature"` remains a backward-compatible scalar and is normalized internally to a one-element filter list. `tag` and `tags` are mutually exclusive (`VALIDATION_ERROR`). Neither field splits commas: `"feature,ux"` is one literal tag name. On durable projects, each selected name is matched on the same grouping key `tags_listProject` labels entries with, so filtering by `make-space` returns todos carrying either the canonical row or a legacy `make space` row and filtered counts agree with the chip counts. Temporary boards keep exact stored-name matching (row-level chips): the filter is not rewritten through `TagGroupKey`, so a `make space` chip selects only that row. If any selected logical tag matches no row, the board is empty rather than silently dropping that filter. The same normalized ordered list is applied to the initial page, every per-lane read, filtered counts, and cursor continuation.
- **Assignee filter:** `assignee` is a **string**. Use `"me"` for the authenticated caller, `"unassigned"` for todos with no assignee, or a positive user ID encoded as a string such as `"42"`. Sentinels are case-sensitive after surrounding whitespace is trimmed. Unknown/non-member positive IDs return an empty board; malformed values return `VALIDATION_ERROR` with `field: "assignee"`. A JSON number such as `42` is invalid.
- **Priority filter:** omit `priority` or send an empty string for all priorities, use `"**none**"` for todos without a priority, or use a literal priority-tier key. Tier keys contain only lowercase letters, digits, and underscores, while the no-priority sentinel contains `*`, so a real key such as `"none"` remains unambiguous. An unknown tier key returns an empty board.
- **Output:** `data.project` (`projectSlug`, `name`, `role`) and `data.columns`
  (each: `key`, `name`, `isDone`, `items` as todo-shaped objects). MCP
  `board_get` does not emit the REST board-tag projection; use
  `tags_listProject` for the archive-inclusive MCP tag catalog.
  Successful project and todo `projectSlug` fields always use the persisted
  canonical slug. Lookup accepts normalization-equivalent input such as
  uppercase or surrounding whitespace, but the response does not echo that
  spelling.
- **Meta:** `nextCursorByColumn`, `hasMoreByColumn`, `totalCountByColumn` (per column key). See **Board pagination** below.
- **Temporary Board activity:** after an expiring board is completely loaded,
  MCP makes a final, throttled best-effort activity refresh. A maintenance
  write failure is logged internally but does not discard the authorized board
  snapshot or add a public warning. Success therefore does not guarantee that
  this request persisted a new `expiresAt`.
- **Note:** Not available in anonymous mode or before bootstrap; requires sign-in.

### Todos

| Tool | Input (summary) | Output (summary) |
|------|-----------------|------------------|
| `todos_create` | `projectSlug`, `title`, optional `body`, `tags`, `columnKey`, `estimationPoints`, `sprintId`, `assigneeUserId`, `priorityKey`, `position` | `data.todo` |
| `todos_get` | `projectSlug`, `localId` | `data.todo` |
| `todos_search` | `projectSlug`, `query`, optional `limit`, `excludeLocalIds` | `data.items` (lightweight search hits) |
| `todos_update` | `projectSlug`, `localId`, `patch` (JSON patch object) | `data.todo` |
| `todos_delete` | `projectSlug`, `localId` | `data` with `status: "deleted"`, `projectSlug`, `localId` |
| `todos_move` | `projectSlug`, `localId`, `toColumnKey`, optional `afterLocalId`, `beforeLocalId` | `data.todo` |
| `todos_archive` | `projectSlug`, `localIds` (array, 1-500, unique, positive) | `data` batch result (see **Story archival**) |
| `todos_restore` | `projectSlug`, `localIds` (array, 1-500, unique, positive) | `data` batch result (see **Story archival**) |
| `todos_linksList` | `projectSlug`, `localId` | `data.outbound`, `data.inbound` (arrays of `{localId, title, linkType, archivedAt}`) |
| `todos_linkAdd` | `projectSlug`, `localId`, `targetLocalId`, optional `linkType` (default `relates_to`; also `blocks`, `duplicates`, `parent`) | `data.outbound`, `data.inbound` (refreshed) |
| `todos_linkRemove` | `projectSlug`, `localId`, `targetLocalId` | `data.outbound`, `data.inbound` (refreshed) |

Column keys accept common aliases (normalized internally). Todo payloads use **`localId`** and **`projectSlug`**; they do not expose the internal global todo id.

Todo objects include optional `priorityKey`. For `todos_update`, omitting
`patch.priorityKey` preserves the current assignment, JSON `null` clears it,
and a string assigns that project-local tier. Unknown and cross-project keys
return `VALIDATION_ERROR`. Priority filtering and priority-based sorting are
not supported in this release.

Todo objects also include `createdByUserId`, the immutable historical ID of the
authenticated user present when the todo was created. It is not proof of
current project membership or notification eligibility. MCP todo shapes keep
the field present with JSON `null` for unauthenticated, pre-migration, imported,
or deleted-user attribution. The field is read-only; todo mutation inputs cannot
set or clear it.

**Story archival** is orthogonal to workflow state. `todos_archive` and `todos_restore` take
**1-500** unique, positive project-local IDs and apply them **atomically**: one unknown ID
fails the whole request and transitions nothing. Both require **maintainer** access on a
durable project and are unavailable in anonymous and pre-bootstrap modes. The result reports
`targetState`, `requestedCount`, `transitionedCount`, `unchangedCount`, `transitionedLocalIds`,
`unchangedLocalIds` and `transitionedAt`; re-archiving an already-archived story is a no-op
counted as unchanged rather than an error. Both tools call the same atomic store batch
primitive as the REST routes.

Archiving preserves everything else about the story — `columnKey`, `rank`, `doneAt`, the
story's `updatedAt`, tags, links, sprint, priority, assignment and `createdByUserId` are
untouched — so metrics, burndown and sprint history are unaffected in both directions.

Archived stories are read-only: ordinary mutation returns **409** with reason `todo_archived`
until restored, and that check runs **after** authorization, so an unauthorized caller is
refused without learning the archive state. An empty `todos_update` patch (`{}`) is unaffected
— it remains a lookup/no-op that mutates nothing, so it still succeeds against an archived
story. `todos_linkAdd` and `todos_linkRemove` are rejected when either endpoint is archived,
but existing links stay readable: `todos_linksList` keeps returning them and reports each
target's `archivedAt`. Hard delete is a separate operation and remains permitted wherever its
own authorization already allowed it.

Direct reads still return archived stories (`todos_get`), while `board_get` and the default
`todos_search` exclude them. MCP todo shapes always emit `archivedAt` — an **RFC3339
timestamp string**, or JSON `null` for an active story. (The Unix-millisecond form of this
field appears only in backup/export payloads, not in tool responses.)

**No MCP archive listing exists.** The cursor-paginated archive page is REST-only
(`GET /api/board/{slug}/archive`); over MCP, archived stories are reachable individually via
`todos_get`.

**Realtime.** `todos_archive` and `todos_restore` publish **no** board refresh, matching every
other MCP mutation in Scrumboy. Only the REST routes publish one. This asymmetry is
deliberate, not an omission.

**Linked stories:** this is the same "Linked Stories" relation shown on the todo detail page in the
web UI (`GET/POST/DELETE /api/board/{slug}/todos/{localId}/links[/targetLocalId]`). `todos_linkAdd`
self-links (`targetLocalId == localId`) and links to a nonexistent todo both fail validation/not-found
the same way the REST endpoint does.

Links are **directed** from `localId` to `targetLocalId`, and the `linkType` describes `localId` as the
subject of the relation:

- `relates_to` (default) - `localId` is related to `targetLocalId` (directed related-to edge).
- `blocks` - `localId` blocks `targetLocalId`.
- `duplicates` - `localId` duplicates `targetLocalId`.
- `parent` - `localId` is the parent of `targetLocalId`.

`todos_linksList` reports `outbound` (edges where the todo is the source) separately from `inbound`
(edges where the todo is the target). `todos_linkRemove` deletes only the directed `localId ->
targetLocalId` edge with the same orientation used to add it; a reverse edge, if one exists, is left
intact.

### Sprints

Shared inputs: many tools use `projectSlug` only or `projectSlug` + `sprintId` (stored id).

| Tool | Input | Output |
|------|-------|--------|
| `sprints_list` | `projectSlug` | `data.items` (sprint rows + counts); legacy `meta.unscheduledCount`, JSON-RPC sibling `unscheduledCount` |
| `sprints_get` | `projectSlug`, `sprintId` | `data.sprint` |
| `sprints_getActive` | `projectSlug` | `data.sprint` - sprint object or JSON `null` when there is no active sprint |
| `sprints_create` | `projectSlug`, `name`, `plannedStartAt`, `plannedEndAt` (ISO-8601 strings) | `data.sprint` |
| `sprints_activate` | `projectSlug`, `sprintId` | `data.sprint` |
| `sprints_close` | `projectSlug`, `sprintId` | `data.sprint` (closed) |
| `sprints_update` | `projectSlug`, `sprintId`, `patch` | `data.sprint` |
| `sprints_delete` | `projectSlug`, `sprintId` (maintainer+) | `data` with `status: "deleted"`, `projectSlug`, `sprintId` |

Activate/close enforce sprint state (e.g. planned vs active); violations return `VALIDATION_ERROR` with details.

### Tags

Project tag catalog operations remain archive-inclusive and are distinct from the active
`data.tags` projection returned by board reads. They are the source for tag management,
story-editor autocomplete (hydrated asynchronously from the project catalog), Settings/MCP
catalog workflows, and export; selecting a tag from the catalog does not delete or detach
historical associations.

| Tool | Input | Output |
|------|-------|--------|
| `tags_listProject` | `projectSlug` | `data.items` (`name`, `count`, `color`, `deleteScope`, `canDeleteMine`, `canDeleteProject`, `canUpdateColor`). On **durable** projects items are grouped by canonical name and `tagId` is present only for board-scoped tags; on **temporary boards** items stay row-level and every item has a real `tagId`. `deleteScope` is `"mine"`, `"project"`, or `"none"`. `canUpdateColor` is false for durable board-scoped tags when the caller is below Maintainer. **Breaking:** the old `canDelete` boolean is replaced by `deleteScope` — a personal group never reports `"project"`, so it never advertises a deletion that `tags_deleteProject` refuses |
| `tags_listMine` | `{}` | `data.items` (mine tags; no `count`) |
| `tags_updateMineColor` | `tagId`, `color` (hex or `null` to clear) | `data.tag` |
| `tags_deleteMine` | `tagId` | `data.deleted` `{ tagId }` - only if tag is in the viewer’s mine list, then store delete |
| `tags_updateProjectColor` | `projectSlug`, **exactly one of** `tagId` / `tagName`, `color` | `data.tag`. Exactly-one is judged on what was **supplied**: a malformed `tagId` sent alongside `tagName` is rejected, never silently ignored. `tagName` sets **only the caller's own** per-viewer color for a personal label on a **durable** project and is allowed for **any authenticated project member** (non-members rejected; temporary boards rejected). `tagId` targets a **board-scoped** tag and updates the shared `tags.color` for everyone (**maintainer+**). The tag must appear in that project's `tags_listProject` set |
| `tags_deleteProject` | `projectSlug`, `tagId` | `data.deleted` `{ projectSlug, tagId }` - **maintainer+**; tag must exist as a **board-scoped** tag in that project (user-owned tags are never deleted here; use `tags_deleteMine` or the name-based HTTP delete) |

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

### Workflow

Manage a project's workflow columns (board lanes). These call the same store methods (`GetProjectWorkflow`, `AddWorkflowColumn`, `UpdateWorkflowColumn`, `DeleteWorkflowColumn`) as the cookie-only REST endpoints (`GET/POST/PATCH/DELETE /api/board/{slug}/workflow`), exposing them to `sb_` Bearer API tokens.

| Tool | Input | Output |
|------|-------|--------|
| `workflow_list` | `projectSlug` | `data.items` (columns in position order; each `key`, `name`, `color`, `position`, `isDone`, `system`) |
| `workflow_create` | `projectSlug`, `name` - **maintainer+** | `data.column` - new non-done column inserted before the done column |
| `workflow_update` | `projectSlug`, `columnKey`, `name`, `color` (`#RRGGBB`) - **maintainer+** | `data.column` - updated column |
| `workflow_delete` | `projectSlug`, `columnKey` - **maintainer+** | `data.deleted` `{ projectSlug, columnKey }` |

`workflow_update` requires **both** `name` and `color`; it is not a partial update. `color` must be a valid `#RRGGBB` hex value or the call returns `VALIDATION_ERROR`.

`workflow_delete` constraints (store-enforced): the **done** column cannot be deleted (`VALIDATION_ERROR`); a **non-empty** column cannot be deleted (`CONFLICT`); a project must keep **at least 2 columns** (`VALIDATION_ERROR`). System columns are not specially protected from deletion when empty.

Like other MCP mutations, workflow changes call the store directly and do not emit `board.refresh_needed`, so open web clients stay stale until another refresh.

### Priorities

Priority tiers are project definitions with shape
`{ "key", "name", "color", "position" }`. Keys are stable and immutable;
names and `#RRGGBB` colors are editable. A project supports at most 12 tiers
and must retain at least one.

REST routes:

| Method and path | Role | Response / behavior |
|---|---|---|
| `GET /api/board/{slug}/priorities` | Viewer+; active link-accessible temporary boards | `200 {"items":[...]}` in position order |
| `POST /api/board/{slug}/priorities` | Maintainer+ | Body `{"name":"Critical"}`; `201` tier |
| `PATCH /api/board/{slug}/priorities/{key}` | Maintainer+ | Body requires `name` and `color`; `204` |
| `DELETE /api/board/{slug}/priorities/{key}` | Maintainer+ | `204`; rejects the last or an in-use tier |
| `GET /api/board/{slug}/priorities/counts` | Maintainer+ | `200 {"slug", "countsByPriorityKey"}`; missing keys count as zero |

Both REST todo PATCH forms—preferred
`/api/board/{slug}/todos/{localId}` and legacy `/api/todos/{id}`—use the same
priority presence contract: omitted preserves, `null` clears, and a string
assigns. Other established replacement-style fields retain their existing
semantics. Initial REST board responses expose `priorityOrder`; lane pagination
does not repeat it.

MCP tools:

| Tool | Input | Output / role |
|---|---|---|
| `priorities_list` | `projectSlug` | `data.items`; Viewer+ |
| `priorities_create` | `projectSlug`, `name` | `data.priority`; Maintainer+ |
| `priorities_update` | `projectSlug`, `priorityKey`, `name`, `color` | `data.priority`; Maintainer+ |
| `priorities_delete` | `projectSlug`, `priorityKey` | `data.deleted`; Maintainer+ |

Stable priority validation reasons include `invalid_priority_key`,
`invalid_priority_tier_name`, `invalid_priority_tier_color`,
`priority_tier_limit_reached`, and `priority_tier_minimum_required`.
Deleting an assigned tier returns `CONFLICT` with
`reason: priority_tier_in_use`. Inaccessible durable projects retain the
shared `NOT_FOUND` existence-hiding behavior.

### Dashboard

Cross-project "my work" tools for the signed-in user. Not available in anonymous mode or before bootstrap; requires sign-in.

| Tool | Input | Output |
|------|-------|--------|
| `dashboard_getSummary` | optional `timezone` (IANA name; defaults to UTC for calendar-week boundaries) | `data.summary` - assigned counts/points, per-project sections with `activeSprint` (nullable) and `sprintSections`, completion/WIP/throughput analytics |
| `dashboard_listTodos` | optional `limit` (default 20, max 100), `cursor`, `sort` (`activity` default, or `board`) | `data.items` (todos assigned to the caller across all projects) |

**Pagination metadata (`dashboard_listTodos`):** `nextCursor` (opaque, `null`
when there is no next page) and `hasMore` are returned under legacy `meta` and
beside `items` in JSON-RPC structured/text content. Cursor shape depends on
`sort` (see the REST dashboard-todos section below for the underlying
encoding); a cursor from one `sort` is not valid for the other.

### Metrics

Project-level (or sprint-scoped) analytics. Not available in anonymous mode or before bootstrap; requires sign-in.

| Tool | Input | Output |
|------|-------|--------|
| `metrics_getBurndown` | `projectSlug`, optional `sprintId` (scopes to that sprint instead of the whole project) | `data.points` - real burndown series (`date`, `remainingWork`, `initialScope`, optional points-mode fields) |
| `metrics_getBacklogSize` | `projectSlug` | `data.points` - backlog-size series (`date`, `incompleteCount`, `totalScope`, `newTodosCount`, optional points-mode fields) |

### Admin

System-level user management, gated by **system role** (owner/admin), not project role - separate from the `members_*` tools above. Not available in anonymous mode or before bootstrap; requires sign-in.

| Tool | Input | Output |
|------|-------|--------|
| `admin_listUsers` | `{}` | `data.items` - all users (`userId`, `email`, `name`, `systemRole`, `isBootstrap`, `createdAt`); requires **owner or admin** |
| `admin_updateUserRole` | `userId`, `role` (`admin` \| `user` only) | `data.user` (updated); requires **owner**. Promotion to `owner` is not exposed through this tool, matching the REST admin API. Demoting the last owner → `VALIDATION_ERROR` |
| `admin_deleteUser` | `userId` | `data` with `status: "deleted"`, `userId`; requires **owner**. Self-deletion and deleting the last owner → `VALIDATION_ERROR` |

---

## Board pagination (`board_get`)

This is **not** a single cursor for the whole board.

- **`limit`:** Maximum todos returned **per workflow column** (default 20, clamped 1-100).
- **`columnKey`:** Optional. When set, the response and pagination metadata are scoped to that single workflow column; other columns are omitted and are not queried. Omit to preserve the existing all-columns behavior.
- **`cursorByColumn`:** Map from **column key** (string) to an **opaque** cursor token (base64url). Cursors are produced by the server; clients should not parse them. When `columnKey` is set, only the cursor for the selected column is applied; entries for other valid workflow columns are ignored.
- **`meta.nextCursorByColumn`:** Per-column next cursor, or `null` when there is no next page. When `columnKey` is set, only the selected column appears in this map.
- **`meta.hasMoreByColumn`:** Whether more todos exist in that column for the same filters. When `columnKey` is set, only the selected column appears in this map.
- **`meta.totalCountByColumn`:** Total matching todos in that column (independent of the current page). When `columnKey` is set, only the selected column appears in this map.

Unknown column keys in `cursorByColumn` or malformed cursors for columns that are actually read → `VALIDATION_ERROR` with field hints.

---

## REST: Board filters

The browser REST API accepts the same assignee and priority filters on:

- `GET /api/board/{slug}`
- `GET /api/board/{slug}/lanes/{status}`
- `GET /api/projects/{id}/board` (supported compatibility full-board route)

The `tag` query parameter may be repeated, and repeat order is preserved:

```text
?tag=feature&tag=ux&search=mobile
```

Multiple non-empty `tag` values use logical **AND** with each other and with
`search`, Sprint, Assignee, and Priority filters. Values are trimmed, empty
entries are ignored, and exact or case-insensitive duplicates keep their first
spelling and position. At most 20 unique values are accepted; a twenty-first
returns HTTP **400** with `details.reason: "too_many_tag_filters"`.

Durable projects resolve each selected value through canonical logical grouping:
physical aliases such as `make-space` and legacy `make space` are OR alternatives
inside one logical group, while different groups remain AND requirements.
Temporary projects use exact trimmed stored names instead. If any selected
logical tag has no backing row, the board is empty rather than silently dropping
that filter. `data.tags` remains the project-wide active vocabulary rather than
being narrowed to the current intersection; every selected inactive/historical
tag is additionally returned with `count: 0` so clients can display and clear it.

Use the `assignee` query parameter with `me`, `unassigned`, or a positive user ID string. Surrounding whitespace is trimmed; sentinels are otherwise case-sensitive. Invalid values return HTTP **400** with code `VALIDATION_ERROR`, `details.reason: "invalid_assignee"`, and `details.field: "assignee"`—they never disable the filter or return an unfiltered board. `me` also returns that validation error when the REST request has no authenticated actor. A valid unknown/non-member user ID returns an empty board without revealing membership.

For `priority`, omit the parameter or leave it empty for all priorities, use `**none**` for todos without a priority, or pass a literal tier key. Unknown tier keys return an empty board. The special value is outside the priority-key grammar, so a real tier key named `none` remains filterable.

The SPA preserves both parameters in board URLs and exposes them in its filter controls.

Initial REST board responses include `tags`, the board's current-work tag
projection in alphabetical name order. Active entries include optional
`lastActiveAt` RFC3339 metadata: the latest story `updatedAt` among
non-archived stories currently carrying that logical tag. It describes recent
board activity, not tag-association history. An explicitly selected historical
tag with no active stories remains in the projection with `count: 0` and omits
`lastActiveAt`. Archive-only and otherwise unused names remain excluded. This
does not change the archive-inclusive REST tag catalog or MCP
`tags_listProject` semantics.

---

## REST: Board read compatibility and migration

`GET /api/projects/{id}/board` is a supported compatibility endpoint in the current unversioned REST API. It is **not deprecated**, has no scheduled removal, and intentionally returns the complete matching board without `columnsMeta`. New clients should prefer the bounded, slug-based board reads:

- `GET /api/board/{slug}` returns the initial page for every lane plus per-lane `columnsMeta`.
- `GET /api/board/{slug}/lanes/{status}` follows a lane's opaque `nextCursor`.

Clients can obtain a project's numeric `id` and canonical `slug` together from `GET /api/projects` or project creation. The numeric board response also includes `project.slug`, allowing an existing client to migrate without a separate lookup.

To reproduce the numeric endpoint's unpaged `columns` result, pass the same repeated `tag` list, `search`, `assignee`, `priority`, `sprintId`, and `sort` values to the initial slug request and every lane request. For each lane in `columnOrder`, append its initial items, then request lane pages with `afterCursor=columnsMeta[status].nextCursor` until `hasMore` is false. Preserve tag repeat order and page order, and do not parse cursor values. Clients may then discard the pagination metadata or adopt the paged contract directly.

The numeric compatibility route is available only in Full Mode and remains hidden in Anonymous Mode. Slug board routes retain their existing Durable, active Temporary Board, and Anonymous Board access behavior.

Modern slug and legacy numeric REST todo projections both expose the read-only
`createdByUserId` field when attribution exists. They omit the field when the
stored value is `NULL`; this is intentionally different from MCP's explicit
`null`. Route access is resolved before todo projection, and the identifier does
not grant membership or additional access.

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

## REST: Story archival

Archival is exposed on the board routes. **The listing and the mutations have different
permission requirements** — see the Access column.

| Method | Path | Body | Access | Result |
|--------|------|------|--------|--------|
| `GET` | `/api/board/{slug}/archive` | - | board **read** | `{ todos, nextCursor, hasMore }` |
| `POST` | `/api/board/{slug}/todos/{localId}/archive` | - | board **write** + maintainer (durable) | batch result for one story |
| `POST` | `/api/board/{slug}/todos/{localId}/restore` | - | board **write** + maintainer (durable) | batch result for one story |
| `POST` | `/api/board/{slug}/todos/archive` | `{ "localIds": [1, 2, 3] }` | board **write** + maintainer (durable) | batch result |
| `POST` | `/api/board/{slug}/todos/restore` | `{ "localIds": [1, 2, 3] }` | board **write** + maintainer (durable) | batch result |

**Permissions.** Reading the archive needs only **read** access to the board, so a viewer can
list it; an unauthenticated caller gets **404**. The four mutation routes go through the
ordinary todo **write** boundary — including temporary-board expiry and capability semantics
— and then, on durable projects, additionally require **maintainer**. A viewer or contributor
is refused.

**Archive list.** Newest first, ordered by `(archivedAt DESC, id DESC)`. `limit` defaults to
**50** and is clamped to **100**; a non-numeric or non-positive `limit` returns **400**.
`afterCursor` is the opaque `nextCursor` from the previous page (`archivedAtMs:todoId`); a
malformed cursor returns **400**. `hasMore` and `nextCursor` come from a single consistent
snapshot, so a concurrent restore cannot truncate the remaining archive.

**Batch result** (the same shape for single and batch, and for both directions):

```json
{
  "targetState": "archived",
  "requestedCount": 3,
  "transitionedCount": 2,
  "unchangedCount": 1,
  "transitionedLocalIds": [1, 2],
  "unchangedLocalIds": [3],
  "transitionedAt": "2026-09-15T20:35:46.123Z"
}
```

`localIds` must hold **1-500** unique positive IDs; violations return **400**. The batch is
**atomic** — one unknown ID returns **404** and transitions nothing. A story already in the
requested state is counted as unchanged, not an error, and `transitionedAt` is `null` when
nothing moved.

**Realtime.** A REST archive/restore publishes exactly **one** board refresh per request that
actually transitions something, and **none** for a no-op or a failure — regardless of batch
size. The equivalent MCP tools publish **no** board refresh at all, matching every other MCP
mutation (see **Story archival** under *Tool reference -> Todos*). Both transports call the
same atomic store batch primitive, so only the realtime side effect differs.

**Archived stories are read-only.** Ordinary mutation — update, move, link add/remove, over
both the board routes and the legacy `/api/todos/{id}` paths — returns **409** with reason
`todo_archived` until the story is restored. Hard delete is a separate operation and is still
permitted wherever its own authorization already allowed it; it is not blocked by archival.

Authorization is evaluated **before** the archived check, so a caller who could not have
written the story anyway gets **403**/**404** and never learns whether it is archived. The
**409** is reserved for callers who did clear the write boundary.

**Projection.** REST todo payloads carry `archivedAt` as an RFC3339 timestamp and **omit the
field entirely for active stories**, matching the existing `doneAt` convention (every optional
field in the REST todo shape is `omitempty`). MCP instead always emits `archivedAt`, using
JSON `null` for active stories, matching *its* `doneAt` convention. Clients integrating both
transports should treat "absent" and "null" as equivalent. Direct reads still return archived
stories on both transports; it is the *collection* reads (board, default search) that exclude
them.

**Built-in UI consumer.** The shared web/Capacitor UI uses these REST routes for single and
batch Archive/Restore, a cursor-paginated project Archive, and read-only archived-story
detail. It uses the existing REST board-refresh stream; MCP archival remains realtime-silent
and still has no archive-listing tool. Nothing is archived automatically — every transition
is an explicit user or API action.

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

**Backup 1.1 priority presence:** new exports always emit project
`priorityTiers` and todo `priorityKey`. `priorityTiers: []` means the canonical
default tier set and `priorityKey: null` means intentionally unprioritized.
For matched-project merge, absence of either field identifies legacy/patch
input and preserves the existing target value; strings assign against the
effective project tier set. `priorityTiers: null` is invalid. New and
replace-mode projects with absent legacy definitions receive defaults. Import
commits only if every non-null todo key resolves to a tier in the same project.

**Backup 1.2 archive presence:** new exports are **format 1.2** and always emit todo
`archivedAt` — a Unix-millisecond timestamp for archived stories, explicit `null` for active
ones. Imports accept both **1.1** and **1.2**. For matched-project merge, an **absent**
`archivedAt` preserves the target story's archive state, explicit `null` clears it, and a
timestamp archives it; new/copy imports with no archive field create active stories. A
payload that declares **1.1** but carries `archivedAt` is **rejected** as mislabeled newer
data rather than silently gaining 1.2 semantics. `archivedAt` must be non-negative and not
implausibly far in the future. Software that only understands 1.1 should reject 1.2 files
rather than silently dropping archive state.

1. **Public identifiers first:** Mutations and reads are keyed by **`projectSlug`**, **`localId`**, and similar fields - not internal numeric ids for todos or projects in MCP command shapes (except `projectId` on list output as noted).
2. **Capabilities match implementation:** `implementedTools` is the authoritative list of POST tool names.
3. **Narrower than REST:** Some MCP tools intentionally pre-check scope (e.g. mine-tag delete via library membership) or map errors deterministically; behavior may differ from every REST edge case.
4. **Anonymous MCP:** Tag, member, board, todo, sprint, and workflow tools are **not** offered in anonymous server mode through MCP (`CAPABILITY_UNAVAILABLE`), even if anonymous boards exist elsewhere in the product.

