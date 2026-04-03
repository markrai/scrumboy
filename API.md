# Scrumboy MCP HTTP API

This API is intended for programmatic clients (e.g., agents or integrations), not direct browser use.

This document describes the **Model Context Protocol (MCP) HTTP surface** implemented under `internal/mcp` and mounted by the Scrumboy HTTP server. It reflects **current behavior only**, not a roadmap.

**Base path:** `/mcp` (exactly; paths like `/mcp/foo` return 404).

The MCP adapter is constructed in `cmd/scrumboy/main.go` with server mode from configuration and registered on the main `httpapi` server.

---

## Transport

- **`GET /mcp`** - Capabilities discovery (same `data` as `system.getCapabilities` via POST).
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

**Bearer (API access token):** In `full` mode, clients may send `Authorization: Bearer <token>` using an opaque secret minted via [`/api/me/tokens`](#api-access-tokens-rest) (prefix `sb_`, stored as a hash server-side).

**Precedence:** If the request includes a **`Bearer` authorization attempt** (scheme `Bearer` per RFC 9110, with the credential in the segment after the first space; trim applies **only** to that credential string), the adapter validates that token and **does not** fall back to the session cookie when validation fails. A failed Bearer attempt yields **401** with `AUTH_REQUIRED` for the **entire** MCP request (including `GET /mcp` and `system.getCapabilities` over GET). If there is no Bearer attempt, the adapter uses the session cookie as before.

**Anonymous mode:** Session cookies and Bearer tokens are **not** applied for MCP (same anonymous boundary as the documented HTTP API for cookies).

**Bootstrap:** If there are **no users** in the database, authenticated MCP tools are treated as unavailable until bootstrap completes (`CountUsers == 0`).

**Capabilities `auth` object:** Field **`mode`** keeps the existing meaning (`sessionCookie` or `disabled`). Field **`authMethods`** (e.g. `["sessionCookie","bearer"]` in `full` mode) lists mechanisms the adapter supports; clients should not treat `mode` as an exhaustive list of auth options.

**Typical codes:** `AUTH_REQUIRED` when the transport rejects the principal (failed Bearer, or tool needs a signed-in user but none is in context) or when a tool requires sign-in without a session/API token. `CAPABILITY_UNAVAILABLE` when the server is in **anonymous mode**, or **before bootstrap** (no users yet), or the tool is otherwise gated as unavailable.

**Practical rule:** Almost all project-scoped tools (todos, sprints, tags, members, board) require **full mode**, **post-bootstrap**, and a **valid session or valid API bearer token**. When no `Authorization: Bearer` header is sent, **`GET /mcp`** / **`system.getCapabilities`** still run without sign-in so clients can inspect the server; **if** a Bearer attempt is present and invalid, that rule does not apply - the request fails at **401** first.

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
  -d '{"tool":"projects.list","input":{}}'
```

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
  -d '{"tool":"projects.list","input":{}}'
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
- **Available user (invite list):** `userId` (from `members.listAvailable`)

`system.getCapabilities` includes an `identity` object echoing some of these patterns.

**Note:** `projects.list` returns **`projectId`** on each item in addition to `projectSlug`. MCP mutations still key off **`projectSlug`**. `projectId` is returned for informational purposes only and is not used as an input identifier in MCP tools.

---

## Implemented tools (summary)

Grouped by domain. All are listed in `implementedTools` from capabilities.

**system**

- `system.getCapabilities` - Server mode, auth snapshot, identity/pagination hints, full tool list.

**projects**

- `projects.list` - Projects visible to the user (with role).

**board**

- `board.get` - Paged board view per workflow column (special pagination; see below).

**todos**

- `todos.create`, `todos.get`, `todos.search`, `todos.update`, `todos.delete`, `todos.move`

**sprints**

- `sprints.list`, `sprints.get`, `sprints.getActive`, `sprints.create`, `sprints.activate`, `sprints.close`, `sprints.update`, `sprints.delete`

**tags**

- `tags.listProject`, `tags.listMine`, `tags.updateMineColor`, `tags.deleteMine`, `tags.updateProjectColor`, `tags.deleteProject`

**members**

- `members.list`, `members.listAvailable`, `members.add`, `members.updateRole`, `members.remove`

**Planned tools:** none exposed in capabilities today (`plannedTools` omitted when empty).

---

## Tool reference

Conventions:

- Inputs use **camelCase** JSON keys matching the Go structs; unknown keys are rejected where `decodeInput` is used.
- Auth gates omitted below repeat: **anonymous mode** → `CAPABILITY_UNAVAILABLE`; **pre-bootstrap** → `CAPABILITY_UNAVAILABLE`; **no authenticated principal** (no valid session or API token on the request) → `AUTH_REQUIRED` for tools that require it.

### `system.getCapabilities`

- **Purpose:** Describe server, auth, identities, pagination notes, and implemented tools.
- **Input:** `{}` (use empty object for POST).
- **Output:** `data` = capabilities object: `serverMode`, `auth`, `bootstrapAvailable`, `identity`, `pagination`, `implementedTools`, optional `plannedTools`.
- **Meta:** e.g. `adapterVersion` (integer).
- **Example (GET or POST):**  
  `POST /mcp` `{"tool":"system.getCapabilities","input":{}}`  
  → `ok: true`, `data.implementedTools` = full tool array.

### `projects.list`

- **Purpose:** List projects for the current user with role.
- **Input:** `{}`
- **Output:** `data.items` - array of projects (`projectSlug`, `projectId`, `name`, `image`, `dominantColor`, `defaultSprintWeeks`, `expiresAt`, `createdAt`, `updatedAt`, `role`).

### `board.get`

- **Purpose:** Board snapshot with optional tag/search/sprint filters and **per-column** pagination.
- **Input:** `projectSlug` (required); optional `tag`, `search`, `sprintId` (sprint row id; must belong to the project when set); optional `limit` (default 20, max 100); optional `cursorByColumn` (map column key → opaque cursor string). Omitting `sprintId` applies no sprint-based filter on the board query (internal mode `none`).
- **Output:** `data.project` (`projectSlug`, `name`, `role`), `data.columns` (each: `key`, `name`, `isDone`, `items` as todo-shaped objects).
- **Meta:** `nextCursorByColumn`, `hasMoreByColumn`, `totalCountByColumn` (per column key). See **Board pagination** below.
- **Note:** Not available in anonymous mode or before bootstrap; requires sign-in.

### Todos

| Tool | Input (summary) | Output (summary) |
|------|-----------------|------------------|
| `todos.create` | `projectSlug`, `title`, optional `body`, `tags`, `columnKey`, `estimationPoints`, `sprintId`, `assigneeUserId`, `position` | `data.todo` |
| `todos.get` | `projectSlug`, `localId` | `data.todo` |
| `todos.search` | `projectSlug`, `query`, optional `limit`, `excludeLocalIds` | `data.items` (lightweight search hits) |
| `todos.update` | `projectSlug`, `localId`, `patch` (JSON patch object) | `data.todo` |
| `todos.delete` | `projectSlug`, `localId` | `data` with `status: "deleted"`, `projectSlug`, `localId` |
| `todos.move` | `projectSlug`, `localId`, `toColumnKey`, optional `afterLocalId`, `beforeLocalId` | `data.todo` |

Column keys accept common aliases (normalized internally). Todo payloads use **`localId`** and **`projectSlug`**; they do not expose the internal global todo id.

### Sprints

Shared inputs: many tools use `projectSlug` only or `projectSlug` + `sprintId` (stored id).

| Tool | Input | Output |
|------|-------|--------|
| `sprints.list` | `projectSlug` | `data.items` (sprint rows + counts), `meta.unscheduledCount` |
| `sprints.get` | `projectSlug`, `sprintId` | `data.sprint` |
| `sprints.getActive` | `projectSlug` | `data.sprint` - sprint object or JSON `null` when there is no active sprint |
| `sprints.create` | `projectSlug`, `name`, `plannedStartAt`, `plannedEndAt` (ISO-8601 strings) | `data.sprint` |
| `sprints.activate` | `projectSlug`, `sprintId` | `data.sprint` |
| `sprints.close` | `projectSlug`, `sprintId` | `data.sprint` (closed) |
| `sprints.update` | `projectSlug`, `sprintId`, `patch` | `data.sprint` |
| `sprints.delete` | `projectSlug`, `sprintId` (maintainer+) | `data` with `status: "deleted"`, `projectSlug`, `sprintId` |

Activate/close enforce sprint state (e.g. planned vs active); violations return `VALIDATION_ERROR` with details.

### Tags

| Tool | Input | Output |
|------|-------|--------|
| `tags.listProject` | `projectSlug` | `data.items` (`tagId`, `name`, `count`, `color`, `canDelete`) |
| `tags.listMine` | `{}` | `data.items` (mine tags; no `count`) |
| `tags.updateMineColor` | `tagId`, `color` (hex or `null` to clear) | `data.tag` |
| `tags.deleteMine` | `tagId` | `data.deleted` `{ tagId }` - only if tag is in the viewer’s mine list, then store delete |
| `tags.updateProjectColor` | `projectSlug`, `tagId`, `color` | `data.tag` - **maintainer+**; tag must be **project-scoped** in that project |
| `tags.deleteProject` | `projectSlug`, `tagId` | `data.deleted` `{ projectSlug, tagId }` - **maintainer+**; tag must exist as a **project-scoped** tag in that project |

### Members

| Tool | Input | Output |
|------|-------|--------|
| `members.list` | `projectSlug` | `data.items` (member rows with normalized roles where implemented) |
| `members.listAvailable` | `projectSlug` | `data.items` (users not in project) - **maintainer+** |
| `members.add` | `projectSlug`, `userId`, `role` (`maintainer` \| `contributor` \| `viewer` only) | `data.member` |
| `members.updateRole` | `projectSlug`, `userId`, `role` (same three) | `data.member` |
| `members.remove` | `projectSlug`, `userId` | `data.removed` `{ projectSlug, userId }` |

Member list payloads normalize legacy role strings where the adapter applies mapping (`owner`→`maintainer`, `editor`→`contributor`).

`members.updateRole`: self-demotion and last-maintainer demotion → `CONFLICT`.  
`members.remove`: last maintainer removal → `VALIDATION_ERROR` (store mapping).

---

## Board pagination (`board.get`)

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

