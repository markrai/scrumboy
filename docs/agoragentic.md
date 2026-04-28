# Scrumboy and Agoragentic (Agora adapter)

## What this is

**Agora** is a small HTTP **edge adapter** in front of Scrumboy’s existing MCP (Model Context Protocol) support. It exposes two paths that return a fixed JSON **envelope** (`ok` / `result` / `error`) suited for **Agoragentic v1**-style listings, while the real tool catalog and tool execution still run on Scrumboy’s **unchanged** MCP implementation.

You do **not** call Agora instead of MCP for other clients. **Claude, curl, and MCP-style clients** that already use `GET /mcp`, `POST /mcp`, or `POST /mcp/rpc` should keep using those—behavior there is the same with or without Agora enabled.

| Surface | Role |
|--------|------|
| **`/mcp`**, **`/mcp/rpc`** | Canonical Scrumboy MCP: legacy and JSON-RPC tools. |
| **`/agora/v1/discover`**, **`/agora/v1/invoke`** | Agoragentic-oriented adapter: same tools in-process, different URL and response envelope. |

The adapter does **not** reimplement tools. It forwards each call to the same in-process **JSON-RPC** flow as `POST /mcp/rpc` (`tools/list` and `tools/call`), reusing the same server handler and auth as a normal MCP request.

---

## Agoragentic v1 → Scrumboy

| Agoragentic listing | HTTP (Scrumboy) | MCP JSON-RPC (internal) |
|--------------------|-----------------|-------------------------|
| `scrumboy.discover_tools` | `POST /agora/v1/discover` | `tools/list` |
| `scrumboy.invoke_tool` | `POST /agora/v1/invoke` | `tools/call` |

A minimal machine-readable stub for these two listings is in [`docs/examples/agoragentic-manifest.json`](examples/agoragentic-manifest.json).

---

## Endpoints

| Method + path | Purpose |
|---------------|---------|
| `POST /agora/v1/discover` | List tools (names, descriptions, `inputSchema`). |
| `POST /agora/v1/invoke` | Call one tool by name with a JSON `arguments` object. |

**Other paths under `/agora/v1/…`** that are not these two return a JSON **404** (or **405** if the method is not `POST` where a POST is required). The bare path `/agora/v1` (no trailing subpath) also returns JSON **404**. Only use the two POST routes above for normal operation.

**Base URL:** same host and port as your Scrumboy app (e.g. `https://your-host`).

---

## Authentication

Agora uses the **same rules as `POST /mcp/rpc`**:

- **Session:** send `Cookie: scrumboy_session=…` when the user is signed in through the app.
- **API token (full / normal mode):** `Authorization: Bearer <token>` (the token string Scrumboy issues, e.g. `sb_…`).

The adapter **does not** add or strip credentials. It passes your request through to the MCP stack, so an invalid or missing Bearer token behaves like a direct `tools/call` (e.g. tool error text for auth failures, depending on tool and mode). See `docs/mcp.md` for full MCP auth behavior, anonymous mode, and bootstrap edge cases.

---

## Response envelope (all Agora JSON responses)

Every response body is a single JSON object with these **top-level keys always present**:

- **`ok`** — boolean
- **`result`** — success payload, or JSON **`null` on failure**
- **`error`** — JSON **`null` on success**, or an object on failure

**Success:**

```json
{
  "ok": true,
  "result": {},
  "error": null
}
```

(`result` is not always an empty object—see below.)

**Failure (adapter validation, gateway error, or mapped tool/RPC error):**

```json
{
  "ok": false,
  "result": null,
  "error": {
    "message": "human-readable message"
  }
}
```

If the failure is a **JSON-RPC protocol error** from the server, `error` may also include:

- **`code`** — number (e.g. JSON-RPC error code)
- **`data`** — any JSON value, when the server provided it

Tool-level failures from `tools/call` are usually surfaced as `ok: false` with a **`message`** from the tool, not a top-level JSON-RPC `error` frame on the wire you sent—unless the MCP layer returns a protocol error the adapter maps through.

**HTTP status:** many successful adapter bodies still use **200**; some validation or size errors use **4xx** with the same JSON shape where applicable. Prefer reading **`ok`** and **`error`** in the body, not only HTTP status, for Agora.

---

## `POST /agora/v1/discover`

**Purpose:** Return the same tool list as JSON-RPC `tools/list` (`result.tools` from MCP), wrapped in the Agora envelope.

**Request**

- **Method:** `POST`
- **Header:** `Content-Type: application/json`
- **Body:** empty, whitespace-only, or a JSON object **`{}`**. No extra properties are allowed—only `{}` (or empty body treated like “no input”).

**Example**

```bash
curl -sS -X POST 'https://YOUR_HOST/agora/v1/discover' \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer sb_YOUR_TOKEN' \
  -d '{}'
```

**Success `result`**

```json
{
  "ok": true,
  "result": {
    "tools": [
      {
        "name": "system.getCapabilities",
        "description": "…",
        "inputSchema": { }
      }
    ]
  },
  "error": null
}
```

Each tool is whatever MCP `tools/list` provides. The field name is **`inputSchema`** (camelCase, MCP-native), not `input_schema`.

---

## `POST /agora/v1/invoke`

**Purpose:** Call a tool by name with **`arguments`**, same as JSON-RPC `params` on `tools/call` (`name` + `arguments`).

**Request**

- **Method:** `POST`
- **Header:** `Content-Type: application/json`
- **Body:** JSON object with **exactly** these top-level fields for normal use:
  - **`tool`** (string, required) — same name as in the catalog, e.g. `projects.list`
  - **`arguments`** (required) — a JSON **object** for tool inputs, or the JSON value **`null`**, which the server treats like **`{}`** for the underlying call. Unknown top-level fields are **rejected** (strict decode).

**Example**

```bash
curl -sS -X POST 'https://YOUR_HOST/agora/v1/invoke' \
  -H 'Content-Type: application/json' \
  -H 'Cookie: scrumboy_session=YOUR_SESSION' \
  -d '{"tool":"system.getCapabilities","arguments":{}}'
```

**Success `result`**

`result` can be **any JSON value** the tool’s MCP result normalizes to: object, array, string, number, boolean, or `null`—depending on structured content and text fallbacks. There is no fixed per-tool object shape in the envelope; inspect `result` for the tool you called.

**Failure `result`**

Always **`null`**, with **`error.message`** (and sometimes **`code` / `data`**) as above.

---

## Gotchas and requirements

1. **Both `tool` and `arguments` are required in the JSON body.** Omitting **`arguments`** entirely returns a **400**-style error with a clear message (not “silent `{}`”). Send at least `{"tool":"…","arguments":{}}` for no-arg tools.

2. **`arguments` must be a JSON object** (or JSON **`null`**, which is normalized to **`{}`** for the MCP call). Arrays, strings, or numbers at the top level of `arguments` are rejected. Inner keys are **not** modified by the adapter—only the outer `tool` / `arguments` shape is checked.

3. **No extra top-level keys** on the invoke body. A field like `metadata` will fail decode.

4. **Discover** accepts **`{}` or an empty/whitespace body** as “no content”; it must not include unknown properties if you send a non-empty object.

5. **Do not assume** the adapter replaces MCP documentation for tool **input** validation—each tool’s **`inputSchema`** in discover describes allowed keys; the server may still return tool errors for bad inputs.

6. For **integration listings**, align your static schema (e.g. `arguments: { "type": "object" }`) with the runtime rule that **JSON `null` for `arguments` is allowed** and normalized to an empty object.

---

## See also

- **MCP behavior and tools:** [`docs/mcp.md`](mcp.md)
- **MCP HTTP API reference (detailed):** `API.md` in the repository root
- **Example manifest fragment:** [`docs/examples/agoragentic-manifest.json`](examples/agoragentic-manifest.json)
