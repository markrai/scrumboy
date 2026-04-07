# Changelog

> **Upgrades:** No breaking changes in **3.7.x** / **3.8.x** / **3.9.x** / **3.10.x** / **3.11.x** unless noted below.


## [3.11.10] - 2026-04-07

### Improvements

- **Board activity** - **`UpdateBoardActivity`** uses a single conditional **`UPDATE`** (throttled **`last_activity_at`**, rolling **`expires_at`** when expiring) instead of read-then-write. A missing project **`id`** still returns **`ErrNotFound`**; throttled calls return nil.

---

## [3.11.9] - 2026-04-07

### Improvements

- **Board reads (durable projects)** - Full board loads (`GetBoard`, `GetBoardPaged`, including the high-card-count per-lane path) and **MCP `board.get`** no longer call **`UpdateBoardActivity`** when **`expires_at`** is unset. **Expiring** boards keep the same throttled **`last_activity_at` / rolling `expires_at`** behavior on those reads; **lane-only** pagination was already unchanged. Todo mutations still refresh activity as before.

### Tests

- **`internal/store`** - `TestDurableBoardRead_DoesNotRefreshLastActivityAt`, `TestExpiringBoardRead_RefreshesLastActivityWhenStale`.
- **`internal/httpapi`** - **`TestGetBoard_ActivityTrackingBestEffort`** asserts the **`backlog`** column key in board JSON (workflow `column_key`), not legacy **`BACKLOG`**.

---

## [3.11.8] - 2026-04-07

### Fixes

- **Board - lane pagination** - `ListTodosForBoardLane` now derives **`nextCursor` from the last returned row** after trimming to `limit` (aligned with `flushLane`), fixing skipped rows, duplicate IDs across pages, and incorrect **filtered drag/drop** boundary fetches that used `limit=1` with the lane cursor.
- **Assignments - notifications** - **`todo.assigned`** SSE includes **`projectSlug`** (from the project row already loaded on assignee create/update; no extra DB round trip). Client uses centralized **`resolveNotificationProjectSlug`** (map → catalog → event) with **persisted row reconciliation** when the catalog slug changes.

### Tests

- **`internal/store`** - Pagination boundary contract, tag-filtered multi-page invariants, and related lane tests.
- **`internal/httpapi`** - Eventbus regression asserts **`projectSlug`** on wire; **`TestAPI_BoardPagedAndLaneEndpoint`** uses canonical **`backlog`** column keys in JSON and lane URL.
- **`internal/httpapi/web`** - Vitest for **`resolveNotificationProjectSlugCore`**.

---

## [3.11.7] - 2026-04-05

### Fixes

- **Router (full mode)** - Logged-out visitors opening a **board URL** (`/{slug}`) are no longer sent to the **login UI** before the app loads the board. The client-side gate still applies to **`/projects`** and the **dashboard** only; board access for anonymous users (e.g. shareable **temporary** boards) is enforced by **`GET /api/board/{slug}`** as before.

---

## [3.11.6] - 2026-04-05

### Fixes

- **Board (mobile)** - Lane tab **rounded corners** apply to **all** workflow keys via a generic **`.mobile-tab`** rule (not only the five built-in `[data-tab="…"]` presets). **Workflow color** changes from Settings update **mobile tabs and tab drop zones** on in-place board refresh (shared **`mobile-lane-tabs`** helpers); **`initDnD`** runs after the strip is final so Sortable targets stay valid. In-place sync uses **key→element maps** instead of repeated DOM scans.
- **Board (workflow keys)** - Client fallback **`columnsSpec()`** and default **lane meta / mobile tab** state use the same **canonical column keys** as the store/API (**`backlog`**, **`doing`**, etc.). **Legacy `mobileTab_*` localStorage** values (uppercase) map to current keys. **`.card--doing`** matches API todo **`DOING`** border styling; optimistic drag styling maps **`doing`** → existing **`in_progress`** card classes.

### Improvements

- **Board** - **`data-tab` / `data-status` / `data-column`** (and similar) use **`escapeHTML`** on lane keys in full render and incremental updates for parity with rebuild paths.
- **Dashboard** - **Load more**: mobile uses the same **▼** affordance as board lanes (centered); desktop keeps a ghost **Load more** button; **`aria-busy`** and clearer **`aria-label`**; glyph as explicit **Unicode** (**`\u25BC`**) in source.

### Tests

- **`internal/httpapi/web_assets_test.go`** - Asserts **CSS** preset selectors match canonical default keys, **`columnsSpec`** keys, **`buildMobileTabsInnerHtml`** structure (tabs + drop zones), board sync helpers, and **`.card--doing`**. Comments note these are **embedded-source checks**, not browser/E2E coverage (manual QA still required for DnD and workflow mutations).

---

## [3.11.5] - 2026-04-05

### Features

- **Wallpaper** - Optional built-in image at **`/wallpapers/default.jpg`**: empty preference tries to load it; if the file is missing or fails to load, wallpaper stays **off** (no bundled placeholder). **Builtin** mode is client-only in **localStorage**; server prefs remain **off** / **color** / **image** as before.
- **Settings → Customization** - When a wallpaper is active, the Settings dialog uses a lighter **backdrop** and a slightly translucent panel so the same background shows through; tuned for readability (stronger panel and backdrop than the first pass).

### Improvements

- **Board** - Lane **column** backgrounds use a light **`color-mix`** tint from each workflow lane’s **`color`** when the API provides it (**`col--lane-tint`** / **`--lane-accent`**), so custom lane keys and themed projects match the header again-not only the five fixed **`data-column`** CSS rules in light mode.

---

## [3.11.4] - 2026-04-05

### Features

- **Assignments - notification panel** - The bottom-right badge **toggles** an inbox panel (`#global-notification-panel`) instead of clearing the count on click. **localStorage** list **`scrumboy_notifications_v1_{userId}`** stores up to **100** assignment rows (prepend, dedupe by event **id** or **projectId + todoId + type**), with **read/unread** state and **“Mark all as read”**. Rows open **`/{slug}?openTodoId={id}`** via the SPA router when a project slug is known; slugs are filled from the existing **projects** cache (dashboard / project list / board load) or resolved on demand when needed.
- **Web Push (PWA)** - **Service worker** **`notificationclick`** opens **`/{projectSlug}?openTodoId={todoId}`** when the push payload includes both fields (otherwise **`/`**), focusing an existing window and using **`WindowClient.navigate`** when supported.

### Improvements

- **Assignments - performance** - Inbox updates stay off the realtime hot path: **no `GET /api/projects`** during **`todo.assigned`** handling; **debounced** persistence and **`notifications:updated`** emissions reduce **localStorage** and UI churn during bursty SSE. Legacy **`incrementUnread()`** / **`scrumboy_unread_v1_`** remain for migration; the badge count is driven by **unread rows in the inbox list**.

---

## [3.11.3] - 2026-04-05

### Features

- **Board (mobile)** - When a todo drag starts, lane tabs briefly flash (**300ms**) so it is obvious they accept drops; tab labels stay readable above the drop overlays.
- **Web Push (PWA)** - After sign-in, the client auto-subscribes when **both** VAPID keys are set on the server; **`SCRUMBOY_PUSH_BY_DEFAULT_IF_VAPID`** removed (VAPID presence is the operator signal). Per-user autosub progress in **localStorage** with resilient retry when the permission prompt is dismissed vs blocked.

### Fixes

- **Board (drag-and-drop)** - Success toast **“Todo moved to …”** only when the todo changes **lane**; same-lane reorder no longer shows a redundant toast (lane titles still come from the board workflow, not hardcoded names).

### Improvements

- **Settings → Customization** - **Background notifications (PWA)** is grayed out with a one-line notice when Web Push is unavailable (no VAPID on the server, or anonymous board mode).

### Documentation

- **`docs/mcp.md`** - MCP documentation added/expanded.
- **`docs/pwa.md`** - Push flow and env vars aligned with streamlined enablement; key generation note includes **[VapidKeys.com](https://vapidkeys.com/)**.

---

## [3.11.2] - 2026-04-04

### Fixes

- **Web Push (PWA)** - **`notificationclick`** focuses an existing same-origin app window or opens **`/`**; no navigation by **`projectSlug`** / **`todoId`** (payload fields kept for a future notification center). **`focus()`** that does not return a client still falls through to **`openWindow('/')`**.
- **Assignment chime (mobile)** - **`notify.mp3`** added; **`assignmentNotify`** uses **`<audio><source>`** with **MP3 first** and **Ogg** second so **iOS Safari** (no Vorbis/Ogg decode) can play the sound. Toast and unread badge behavior unchanged.

### Improvements

- **Web Push API** - **`GET /api/push/vapid-public-key`** and **`POST /api/push/subscribe`** return **503** when VAPID is incomplete (either public or private key missing). **`DELETE /api/push/unsubscribe`** unchanged so rows can still be removed if keys are later disabled.
- **Router (anonymous mode)** - Initial load no longer calls **`unsubscribeFromPush`** (push is unavailable in anonymous mode; avoids pointless local churn).

### Other

- **README** - VAPID-related env table dashes normalized (encoding-safe).
- **Dependencies** - **`github.com/SherClockHolmes/webpush-go`** listed as a direct module dependency; **`go mod tidy`**.
- **Comments** - **`router.ts`**: logged-out push cleanup is best-effort per device; server DELETE may fail after auth teardown; stale DB rows are pruned when send fails.
- **Tests** - **`internal/httpapi/push_routes_test.go`**, **`push_notify_test.go`** for push routes and notifier edge cases.

---

## [3.11.1] - 2026-04-04

### Fixes

- **Project list** - Invited users now see **authenticated** temporary boards (with a creator) they belong to via **`project_members`**. The membership branch does not apply when **`creator_user_id`** is null, so anonymous paste boards never appear from stray membership rows alone.
- **Todo dialog (roles)** - **Viewers:** read-only title, status, body, links; Save off; “View Todo” when nothing to save. **Contributors:** title and status locked (body-only when assigned, same as API). Submit handler checks permissions; viewers no longer enter bulk-select via Ctrl/Cmd+click on cards.

### Other

- **Keycloak (local dev)** - `docs/keycloak/realm-scrumboy-local.json` import + `docs/keycloak/README.md` (issuer env, public-client secret placeholder).
- **Tests** - `internal/store/list_projects_test.go` for temp-board listing.

---

## [3.11.0] - 2026-04-04

### Features

- **App-wide realtime (full mode)** - **`GET /api/me/realtime`** merges the user hub stream with **`hub.Subscribe`** for every project from **`ListProjects`** (one **`EventSource`** while logged in). **`Hub`** adds **`SubscribeUser`** / **`EmitUser`**; **`sseBridge`** duplicates **`todo.assigned`** to the assignee’s user channel (same JSON as the project emit). Wire events include stable **`id`** for client dedupe; **`refresh_needed`** from the assignment path uses a distinct composite id so it does not collide with the assignment payload.
- **Frontend** - **`core/realtime.ts`**: global stream, **`seenEvents`** dedupe before side effects, **`emit('realtime:event')`**. Logged-in boards listen on the bus only (no per-board **`EventSource`**); anonymous boards keep **`/api/board/{slug}/events`**. Strict rule: never both connections at once.
- **Unread badge** - **`core/notifications.ts`**: count, optional per-user **`localStorage`**, **`#global-notification-badge`** (bottom-right), **`notifications:updated`** bus; increments only after dedupe and assignee match; skips increment when already on that project’s board; clear on badge click; hydrate/clear on user change in **`router.ts`**.

### Other

- **Settings / Customization** - Desktop notification status copy uses a regular hyphen after **Enabled** (was an em dash). Assignment badge hover **`title`** / **`aria-label`**: *N todos have been assigned to you* (singular phrasing for count **1**).

---

## [3.10.0] - 2026-04-04

### Features

- **Event bus + SSE** - **`internal/eventbus`** fanout; **`PublishEvent`** on the server. Board refresh / members events go through the bus; **`sseBridge`** keeps the same SSE JSON as before.
- **`todo.assigned`** - Published after commit from **`CreateTodo`** / **`UpdateTodo`** when assignee changes (non-anonymous temp boards). SSE uses reason **`todo_assigned`**; handlers skip duplicate **`todo_created`** / **`todo_updated`** refresh when **`AssignmentChanged`**.
- **Webhooks (full mode)** - **`POST` / `GET` / `DELETE`** **`/api/webhooks`** (maintainer, session; **404** in anonymous mode). Migration **050**; optional HMAC **`X-Scrumboy-Signature`**; async queue + worker, retries, JSON envelope with event **`id`** (for idempotency). Dispatcher enqueues in a goroutine with a detached context so SSE is not blocked.

### Fixes

- **Shutdown** - HTTP **`Shutdown`** before cancelling the webhook worker.
- **CreateTodo** - Same **`!isAnonymousBoard`** gate as **`UpdateTodo`** for assignment events.

### Other

- Tests: **`eventbus_regression_test.go`**. Docs: README webhooks section + TOC. Dep: **`github.com/google/uuid`**.

---

## [3.9.4] - 2026-04-04

### Fixes

- **OIDC / SSO - account linking for existing users** - When a user signs in with **Continue with SSO** and the IdP returns a **verified** email that already matches a **`users`** row (e.g. bootstrap owner or admin-created account from before OIDC), Scrumboy now **links** the **`(issuer, subject)`** identity in **`user_oidc_identities`** to that user instead of failing with a duplicate-email conflict. Local password hashes are unchanged; SSO and password login can both work for the same account when local auth remains enabled. Integration test **`TestOIDCAutoLinkExistingUser`** covers the full callback flow; the test **fake IdP** now relays **`nonce`** from authorize → token so end-to-end OIDC tests match real providers.

---

## [3.9.3] - 2026-04-05

### Improvements

- **Board search (Escape)** - While the search field is focused, **Esc** blurs it and, when there is text, clears the query using the same path as the clear control (**`setSearchParam("")`** + board reload). Escape handling runs **before** the global modal gate so search dismisses consistently.
- **Settings** - **Tab** cycles the visible settings tabs (wrapped); **Shift+Tab** is left for normal focus. Tab switching goes through a single **`switchSettingsTab`** helper (workflow dirty confirm, cache invalidation, re-render). Sprints tab empty copy now says **Create one above** (the form is above the list).
- **Main navigation** - **Shift+Tab** cycles **Dashboard → Projects → Temporary** in reverse (**Tab** still cycles forward). Tab vs Shift+Tab are dispatched explicitly by chord so the two actions cannot both run.
- **Dashboard** - Initial dashboard load also fetches **`/api/projects`** so chip counts stay correct on a direct **`/dashboard`** visit; failed project fetch does not wipe an existing in-memory list.
- **Projects / Dashboard chips** - **Temporary** vs **Temporary Boards** label uses one shared helper (**`temporaryBoardsNavLabel`**, **767px** breakpoint) so dashboard and projects stay aligned.

---

## [3.9.2] - (no release)

### Note

- **Version number skipped in git** - There is no commit in this repository that sets **`internal/version/version.go`** to **3.9.2**, and no **`README`** / **`CHANGELOG`** reference to **3.9.2** before this note. After **3.9.1**, the next bump was **3.9.3** (commit **`2c5b576`**, *multiple UX enhancements…*). No separate user-facing changes are recorded under **3.9.2**; see **3.9.1** (OIDC **`dist/`** rebuild) and **3.9.3** (UX items above) for work in that window.

---

## [3.9.1] - 2026-04-04

### Fixes

- **OIDC auth UI (embedded `dist/`)** - Rebuilt **`internal/httpapi/web/dist/`** so the compiled bundle matches **`modules/`**: router applies **`oidcEnabled`** / **`localAuthEnabled`** from **`GET /api/auth/status`**, and the login screen shows **Continue with SSO** when OIDC is configured (previously only TypeScript sources were updated in **3.9.0**, so production builds loading **`dist/router.js`** did not surface the SSO button).

---

## [3.9.0] - 2026-04-03

### Features

- **OIDC / SSO (optional)** - Single sign-on when all four env vars are set: **`SCRUMBOY_OIDC_ISSUER`**, **`SCRUMBOY_OIDC_CLIENT_ID`**, **`SCRUMBOY_OIDC_CLIENT_SECRET`**, **`SCRUMBOY_OIDC_REDIRECT_URL`**. Uses OAuth 2.0 Authorization Code with **PKCE (S256)** and a confidential client; **OIDC Discovery** and **JWKS** for the ID token; claims from the ID token only (no Userinfo). Successful login creates a normal **`scrumboy_session`** (no JWTs in the browser). Endpoints: **`GET /api/auth/oidc/login`** (optional **`return_to`**), **`GET /api/auth/oidc/callback`**. **`GET /api/auth/status`** adds **`oidcEnabled`** and **`localAuthEnabled`**. Optional **`SCRUMBOY_OIDC_LOCAL_AUTH_DISABLED=true`** disables password bootstrap/login while OIDC is configured. In **anonymous** mode, OIDC routes return **404** like other auth actions.
- **Auth UI** - **Continue with SSO** when OIDC is enabled; **`oidc_error`** query handling for failed callbacks.
- **Database** - New **`user_oidc_identities`** table (**`UNIQUE(issuer, subject)`**); **`users.password_hash`** is nullable for OIDC-only users (migration **049**).

### Documentation

- **`docs/oidc.md`** - Self-hosted operator guide: env vars, flow, constraints, reverse proxy, troubleshooting, security notes, explicit non-goals.
- **`API.md`**, **`README.md`**, **`SECURITY.md`** - OIDC endpoints, configuration, and session/security summary.

### Dependencies

- **`github.com/coreos/go-oidc/v3`**, **`golang.org/x/oauth2`** (OIDC client and token exchange); **`github.com/go-jose/go-jose/v4`** (integration tests for stub IdP JWTs).

---

## [3.8.0] - 2026-04-03

### Features

- **MCP JSON-RPC: `tools/list` and `tools/call`** on **`POST /mcp/rpc`** - Completes the spec-oriented MCP loop alongside existing **`initialize`** / **`notifications/initialized`**. **`tools/list`** returns tools with **`name`**, **`description`**, and **`inputSchema`** (JSON Schema with **`required`** and tight objects where defined); the catalog starts with four tools (**`projects.list`**, **`todos.create`**, **`todos.get`**, **`todos.update`**) and will grow over time. **`tools/call`** accepts **`params.name`** and **`params.arguments`**, reuses the same tool handlers as legacy **`POST /mcp`**, and returns success as **`result.content[]`** with **`type: "json"`** and the tool payload in **`json`**. Discovery and invocation are **stateless** (no **`initialize`** required for **`tools/list`** or **`tools/call`**). Errors use JSON-RPC codes (**`-32601`** unknown tool, **`-32602`** invalid params / validation, **`-32603`** internal); unknown tools may include **`error.data`** with **`name`**.

### Improvements

- **Catalog `required` handling** - Pre-call checks read the **`required`** array whether it is stored as **`[]string`** (in-memory catalog) or **`[]any`** (e.g. after JSON round-trip), avoiding silent skips.
- **`tools/call` shape errors** - Clearer **`missing params`** / **`missing params.name`** messages for invalid requests.

### Documentation

- **`API.md`** - New **JSON-RPC MCP endpoint (spec-compatible)** section for **`POST /mcp/rpc`**: protocol rules, supported methods, response shapes, auth (same as **`/mcp`**), and how this differs from the legacy **`/mcp`** envelope.
- **`README.md`** - **MCP (JSON-RPC) for AI agents** subsection with **`curl`** examples (**`initialize`**, **`tools/list`**, **`tools/call`**), pointer to **`API.md`**, and notes on HTTP JSON-RPC vs stdio MCP clients.

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
