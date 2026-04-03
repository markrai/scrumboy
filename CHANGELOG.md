# Changelog

> **Upgrades:** No breaking changes in **3.7.x** / **3.8.x** unless noted below.


## [3.8.0] - 2026-04-03

### Features

- **MCP JSON-RPC: `tools/list` and `tools/call`** on **`POST /mcp/rpc`** — Completes the spec-oriented MCP loop alongside existing **`initialize`** / **`notifications/initialized`**. **`tools/list`** returns tools with **`name`**, **`description`**, and **`inputSchema`** (JSON Schema with **`required`** and tight objects where defined); the catalog starts with four tools (**`projects.list`**, **`todos.create`**, **`todos.get`**, **`todos.update`**) and will grow over time. **`tools/call`** accepts **`params.name`** and **`params.arguments`**, reuses the same tool handlers as legacy **`POST /mcp`**, and returns success as **`result.content[]`** with **`type: "json"`** and the tool payload in **`json`**. Discovery and invocation are **stateless** (no **`initialize`** required for **`tools/list`** or **`tools/call`**). Errors use JSON-RPC codes (**`-32601`** unknown tool, **`-32602`** invalid params / validation, **`-32603`** internal); unknown tools may include **`error.data`** with **`name`**.

### Improvements

- **Catalog `required` handling** — Pre-call checks read the **`required`** array whether it is stored as **`[]string`** (in-memory catalog) or **`[]any`** (e.g. after JSON round-trip), avoiding silent skips.
- **`tools/call` shape errors** — Clearer **`missing params`** / **`missing params.name`** messages for invalid requests.

### Documentation

- **`API.md`** — New **JSON-RPC MCP endpoint (spec-compatible)** section for **`POST /mcp/rpc`**: protocol rules, supported methods, response shapes, auth (same as **`/mcp`**), and how this differs from the legacy **`/mcp`** envelope.
- **`README.md`** — **MCP (JSON-RPC) for AI agents** subsection with **`curl`** examples (**`initialize`**, **`tools/list`**, **`tools/call`**), pointer to **`API.md`**, and notes on HTTP JSON-RPC vs stdio MCP clients.

---

## [3.7.8] - 2026-04-03

### Features

- **MCP JSON-RPC (Phase 1)** - New **`POST /mcp/rpc`** endpoint using **JSON-RPC 2.0** alongside the existing **`/mcp`** `{ "tool", "input" }` API (unchanged). Supports **`initialize`** (protocol version **2024-11-05**, `capabilities.tools`, `serverInfo`), **`notifications/initialized`** and **`initialized`** as notifications (**204** empty body), and spec error codes (e.g. **-32601** method not found). **`tools/list`** and **`tools/call`** added in **3.8.0**.

---

## [3.7.7] - 2026-04-03

### Features

- **Dashboard todo sort** - Sort assigned todos by **Activity** (recently updated, default) or **Board order** (per project: workflow column position, then lane rank). **`GET /api/dashboard/todos`** supports optional query **`sort=activity`** or **`sort=board`**; pagination **`cursor`** is tied to the active sort, and a cursor from the wrong mode is rejected with **400** **`VALIDATION_ERROR`**.

### Improvements

- **Todo dialog (mobile)** - New/edit todo form scrolls inside the modal on narrow viewports so header, fields, and Save stay usable (aligned with Settings-style scrolling).
- **Dashboard sort preference (signed-in)** - Choice is saved under **`user_preferences`** key **`dashboardTodoSort`** and restored after login (still mirrored in **localStorage** for fast defaults). Server hydrate skips applying the stored value when it already matches in-memory state, and does not overwrite a sort the user changed locally before preferences finish loading.

---

## [3.7.6] - 2026-04-02

### Features

- **API access tokens** - create/manage tokens for CLI, CI, and integrations
- **Bearer Auth** - MCP now supports Bearer auth (`Authorization: Bearer sb_...`)

---

## [3.7.5] - 2026-04-02

### Features

- **MCP token** - Added MCP bearer token authentication support.

---

## [3.7.4] - 2026-04-02

### Features

- **Bulk edit** - Select multiple cards and update them together (desktop).

---

## [3.7.3] - 2026-04-02

### Improvements

- **Project header image** stays in sync when the board updates without a full reload.

---

## [3.7.2] - 2026-04-01

### Features

- **Keyboard shortcuts** for common actions.

### Improvements

- **Click outside** a modal to dismiss it.

---

## [3.7.1] - 2026-04-01

### Improvements

- **Workflow editing** modal aligned with project workflow customization.

---

## [3.7.0] - 2026-03-31

### Features

- Started work on **MCP (Model Context Protocol) API** - Automate Scrumboy via **agents** (Claude, IDEs, custom tooling).

---

## [3.6.1] - 2026-03-31

### Features

- **MCP adapter** - Automate todos, sprints, and tags; **board snapshot** (`board.get`); member tools; **tag delete**.
- **Lane colors** - Update workflow lane colors after creation.

---

## [3.6.0] - 2026-03-31

### Improvements

- **3.6.0** release following editable workflows (**3.5.8**).

---

## [3.5.8] - 2026-03-31

### Features

- **Editable workflows completed** - Add or remove lanes after creation, with updated dashboard and settings (including room for the Workflows tab).

### Fixes

- **Anonymous mode** - Fields that should stay editable were incorrectly blocked.

---

## [3.5.7] - 2026-03-25

### Fixes

- **Workflow lane “add” control** behaves correctly.

---

## [3.5.6] - 2026-03-25

### Improvements

- **Setup docs** - Clearer `scrumboy.env` and configuration guidance.

---

## [3.5.5] - 2026-03-23

### Improvements

- **Errors** - Consistent sentinel errors across packages (clearer behavior for callers).
- **Open-source docs** - README and repo presentation polished for the public release.

### Security

- **Contributions** - DCO (Developer Certificate of Origin) check.

---

## [3.5.3] - 2026-03-15

### Security

- **Project settings** - Only **maintainers** can rename or delete a project.

### Improvements

- **Toasts** when todos are created or updated.

---

## [3.5.1] - 2026-03-15

### Fixes

- **Backups** - Safer behavior when workflows merge and during backup previews.

---

## [3.5.0] - 2026-03-15

### Features

- **Import & export** - More reliable across edge cases.

---

## [3.4.12] - 2026-03-14

### Features

- **Admin password reset** - Reset user passwords from **Settings -> Users**.

---

## [3.4.10] - 2026-03-13

### Improvements

- **Governance** - **LICENSE**, **CLA**, and **Code of Conduct** for the open-source release.

---

## [3.4.9] - 2026-03-13

### Security

- **Tag colors** - Fixed an XSS vector in tag color handling.

---

## [3.4.7] - 2026-03-13

### Improvements

- **Cards** - Lane color updates immediately when you move a card to another column.

---

## [3.4.6] - 2026-03-13

### Improvements

- **Dashboard** - Status pills match your custom lane colors.

---

## [3.4.5] - 2026-03-13

### Fixes

- **Assignee avatar** no longer appears twice on the same card.

---

## [3.4.4] - 2026-03-13

### Fixes

- **Toolbar** - Race condition that could hide top board actions on first load.

---

## [3.4.3] - 2026-03-11

### Features

- **Viewer role** - Read-only project access when you need visibility without editing.

---

## [3.4.1] - 2026-03-11

### Fixes

- **Profile avatar** can be changed reliably.

---

## [3.4.0] - 2026-03-11

### Security

- **Permissions & audit** - Stronger rules for sensitive actions, with an **audit trail**.

---

## [3.3.3] - 2026-03-11

### Fixes

- **Members list** - Reliable visibility when permissions were ambiguous.

---

## [3.3.2] - 2026-03-11

### Features

- **Promote contributor** to **maintainer** where allowed.

---

## [3.3.1] - 2026-03-11

### Security

- **Contributors** - Clearer limits on creating/deleting stories and on assignment.

---

## [3.3.0] - 2026-03-10

### Improvements

- **Drag and drop** while the board is filtered - cards stay consistent with the active filter.

---

## [3.2.1] - 2026-03-10

### Performance

- **Live updates** - Fewer duplicate refreshes when returning to the desktop app (SSE / focus).

---

## [3.2.0] - 2026-03-10

### Security

- **Roles & UI** - Screens and flows aligned with owner, maintainer, and contributor rules.

---

## [3.1.0] - 2026-03-10

### Security

- **Team roles** - Broader permission and UI alignment for how roles work in the app.

---

## [0.x - 3.0.x] - Early development

*Versions through **3.0.0** and older **2.x / 1.x / 0.x**, summarized by theme.*

### Features

- **Kanban core** - Boards, columns, todos, drag-and-drop, filters, tags.
- **Projects** - Members, assignees, linked stories, points, **sprints**, dashboard, charts.
- **Live boards** - **SSE** updates without manual refresh.
- **Anonymous boards** - Shareable boards with slug URLs, improved privacy, and **import/export** (including NAS-friendly use).
- **2FA**, **PWA**, **custom lanes**, **search**, and a **role model** that grew into today’s permissions.

### Improvements

- **Mobile & desktop** - Touch DnD, tabs, scrolling, passwords, layout; avatars and sprint cues on cards.

### Performance

- **Speed** - Fewer round-trips, **debounced SSE** (less unnecessary reload), query merges, **SQLite tuning for NAS/self-hosted**, smarter caching and service worker behavior.

### Security

- **Auth & sessions** - Login/logout reliability (including tunnels), safer cache rules for auth routes, import confirmations, stricter handling of user-controlled tag data over time.

### Fixes

- Many **stability and UX** fixes across DnD, charts, anonymous mode, imports, and mobile.

---

## Highlights

| Area | Notes |
|------|--------|
| **Self-hosted / NAS** | Optimized SQLite usage for low-resource environments |
| **Real-time** | SSE-powered live board updates |
| **Anonymous boards** | Shareable boards with slug URLs and evolving privacy model |
| **Import / export** | Reliable backup and migration |
| **MCP** | Automation via agents and external tools |
| **Roles & audit** | Strong permission model with audit trail |
