# Public read-only boards

A durable board can be published as a **public read-only board**: anyone with
the link can view the board and its stories without signing in. Nothing else
about the project changes — members keep working exactly as before.

This guide is the canonical operator/user reference for the feature. Source of
truth: `internal/application/publicboard`, `internal/httpapi/routing_public_board.go`,
`internal/httpapi/routing_board_publication.go`, `internal/store/public_board.go`,
and the `public-board` web modules.

## Public boards versus temporary anonymous boards

Scrumboy already has **temporary anonymous boards**: short-lived boards created
from `/anon` that anyone with the link can open and edit until they expire.
They are designed for quick, disposable collaboration.

Public boards are different in every dimension that matters:

| | Temporary anonymous board | Public board |
|---|---|---|
| Lifetime | Expires automatically | Durable project, stays published until unpublished |
| Visitor access | Read **and write** | Read-only; no sign-in, no edits |
| What is shared | The whole working board | An allowlisted public projection (see below) |
| How it is enabled | Just create one from `/anon` | Operator flag **plus** per-board publication by a Maintainer |

A temporary board can never become public: publication requires a durable
project, and expired projects are rejected by the eligibility check.

## Requirements

Two independent conditions must both hold:

1. **Full Mode** (`SCRUMBOY_MODE=full`, the default). Anonymous Mode ignores
   the public-board capability entirely.
2. **Operator gate** `SCRUMBOY_PUBLIC_PROJECTS_ENABLED=1` (also accepts
   `true`/`on`/`yes`; anything else is off). The flag defaults to **off**.

Enabling the flag **does not publish anything by itself**. Every board stays
private until a Maintainer publishes it explicitly. Disabling the flag later
hides the publication controls and makes all public reads and streams return
the generic not-found response, while the stored per-project publication
preferences are retained.

## Publishing and unpublishing a board

A project **Maintainer** (exact role, on a durable project) opens
**Settings → Sharing** for that board:

- The tab shows the current state (**Private** or **Public**) and, when
  published, the shareable link plus a copy button.
- **Publish** asks for confirmation, then enables public access.
- **Unpublish** asks for confirmation, then disables it again.

The tab is shown only to Maintainers of eligible boards while the operator
gate is on; for anyone else the Sharing tab is hidden entirely. (The
dialog's "not allowed" state is only a fallback, for example when access
changes while Settings is already open.) A board whose
slug is reserved (see below) shows a notice and the publish action stays
disabled until the project is renamed.

Behind the tab are two Maintainer-only endpoints (see [API.md](../API.md)):

- `GET /api/board/{slug}/publication` → `{"enabled", "publishable"}`
- `PATCH /api/board/{slug}/publication` with `{"enabled": true|false}` →
  `{"enabled", "changed"}`

## Sharing the link

The public URL is:

```text
https://host/{slug}
```

It is the **same path** the board already uses — there is no separate
"public" address. Members who can already open the board see the normal
workspace; anonymous visitors who open the same URL get the read-only public
view after the member route declines access. Story deep links
(`/{slug}/t/{localId}`) work the same way. Copy the link from
Settings → Sharing and send it to anyone.

## What visitors can do

- Read the board: project name, workflow columns, priorities, tags, and the
  stories in each column (title, body, column, estimation points, priority,
  sprint number, tags).
- Open individual stories and follow story links (each link shows only the
  direction, story number, and title).
- Filter by **search text, tag, sprint number, and priority**. Board order is
  always manual; no other sorting is offered.
- Page through long columns (20 stories per page by default, up to 50; lane
  pages use opaque cursors that must be passed back unchanged).
- Receive **live updates**: the public view refreshes over a lightweight event
  stream when the board changes, and reconnects automatically on failure.
  Realtime scope: ordinary REST mutations that change the public projection
  trigger a refresh invalidation for visitors. MCP mutations and portable
  imports currently do not trigger public refresh events, so
  visitors may need to reload the page after such changes.

Visitors cannot sign in from the public view to gain more access to that
board; signing in takes them through the normal member route instead.

## What stays private

The public projection is an explicit allowlist. The following are **never**
exposed through public reads or the public stream, even when closely related
to visible content:

- Any mutation: creating, editing, moving, archiving, linking, or deleting
  stories, and all board settings.
- Membership, user identities, and anything that identifies who did what
  (no user IDs, emails, or per-user state).
- Internal numeric project IDs. Stories are addressed by their portable
  project-local number only.
- Archived stories (they are excluded from every public listing).
- The Wall, the Agenda/ICS feeds, user preferences, and private integrations
  (webhooks, push subscriptions, API tokens, email/SMTP state).
- Audit history.

Publishing a board does not expose every board field — only the fields listed
above. That allowlist governs *fields*, not *content*: titles, bodies, and
tag names are user-authored text, so anything typed into them — including
names, email addresses, or other personally identifying information —
becomes public with the board. Excluding identity fields is not the same as
anonymizing content. The member API and MCP permissions are unchanged and
stay isolated from public access.

## Eligibility

A board is publishable only when **all** of these hold:

- The instance runs in Full Mode with the operator gate on.
- The project is **durable** (not a temporary/expiring board, not an import
  staging row).
- The slug is syntactically valid and **not reserved**. Reserved slugs are
  top-level application and landing paths such as `_app`, `api`, `auth`,
  `dashboard`, `mcp`, `oauth`, `anon`, `temp`, `p`, `healthz`, `agora`, `en`,
  `pseudo`, and the other localized landing locales. A project created before
  the reservation list existed keeps its slug but cannot be published until
  renamed.

## Unpublishing, deletion, and access denial

- After **unpublish**, the board's public API routes return the
  generic `404 NOT_FOUND` — the same response as for a board that was never
  public, so the denial itself reveals nothing. The `/{slug}` address itself
  still serves the SPA shell (HTTP `200`); its client-side UI then reports
  the board as unavailable (or offers sign-in) once the public API denies
  access.
- Open public streams for that board are **revoked immediately on the serving
  process**: each receives one terminal `access_revoked` event and is closed.
- **Deleting** the project revokes its public streams the same way.
- Streams also re-check eligibility about every 15 seconds, which bounds how
  long a stream can survive an eligibility change made by another server
  process or a direct database edit. Cross-process revocation is therefore
  fast but not instantaneous; do not treat it as a synchronous guarantee.

## Privacy limitations

Understand these before publishing:

- **Already-downloaded content cannot be recalled.** Unpublishing stops all
  future access, but anyone who loaded the board while it was public may have
  saved a copy. Treat publication as permanent disclosure of whatever was
  visible at the time.
- **Search-engine directives are not access control.** Public API responses
  carry `X-Robots-Tag: noindex, nofollow` and `Cache-Control: no-store` as a
  politeness signal to crawlers — but the `/{slug}` browser page itself
  currently sends no `X-Robots-Tag` header, so do not promise deindexing of
  the page. (Sending the header from the HTML route is a possible follow-up;
  it is not implemented.) Only unpublishing actually removes access. If
  content must never be retrievable, do not publish it.
- Rate limiting is intentionally coarse (120 public reads/minute/IP and
  20 stream attempts/minute/IP, evaluated before project lookup; at most
  500 concurrent public streams per process, 5 per IP, 100 per project).
  Over-limit responses are generic and identical for public, private, and
  nonexistent boards.

## Backup, export, and import

Portable JSON exports **never carry publication state**: the export format has
no publication field.

- **Create copy** and **Replace** create new projects, which always start
  private.
- **Merge** into an existing project leaves that project's publication state
  untouched — importing a backup can neither publish nor unpublish the merge
  target.

A full `/data` backup (SQLite file) does preserve the state, like any other
database column. See [backup-and-import.md](backup-and-import.md).

## The independent landing-page flag

`SCRUMBOY_LANDING_PAGE_ENABLED` is a **separate, independent** flag. It has
nothing to do with publishing boards:

| | `SCRUMBOY_PUBLIC_PROJECTS_ENABLED` | `SCRUMBOY_LANDING_PAGE_ENABLED` |
|---|---|---|
| Default | Off | Off |
| What it does | Gates publication + public reads | Serves a marketing landing at `/` |
| Publishes boards | Only via Maintainer action, never by itself | Never |

With the landing flag on in Full Mode, `/` serves the English marketing
landing (with a link that opens the workspace) and `/_app` serves the
workspace. Board URLs (`/{slug}`, `/{slug}/t/{localId}`) and localized
landing paths are unchanged, and Anonymous Mode ignores the flag. Turning the
landing page on does not publish any project. The marketing landing is
English; do not expect it to follow the workspace language.

## Operational rollback

To stop serving public boards without touching data, turn
`SCRUMBOY_PUBLIC_PROJECTS_ENABLED` off and restart. Disabling that one flag
is sufficient — the landing-page flag is independent and can remain enabled.
New public API requests return not-found, and streams open at restart time
are terminated by the restart itself (they do not turn into `404`
responses), while stored publication preferences are kept. The `/{slug}`
address still serves the SPA shell; its UI then reports boards as
unavailable. Rolling back the binary or database itself is a different
operation and may require restoring the pre-upgrade database backup. See
[docker.md](docker.md) and [environment-variables.md](environment-variables.md).
