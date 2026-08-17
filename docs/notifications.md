# Email notifications in Scrumboy

Email notifications are opt-in, per-user, and off by default. They build on the SMTP
infrastructure described in [`docs/smtp.md`](smtp.md) — no separate SMTP config exists for
notifications; the same `SCRUMBOY_SMTP_*` variables and `SCRUMBOY_PUBLIC_BASE_URL` gate both
self-service password reset and notification email.

## Contents

- [Prerequisites](#prerequisites)
- [Categories](#categories)
- [Recipients](#recipients)
- [User settings](#user-settings)
- [Org-wide default for new users](#org-wide-default-for-new-users)
- [Delivery isolation](#delivery-isolation)
- [HTTP endpoints](#http-endpoints)
- [Quick verification](#quick-verification)
- [Related documentation](#related-documentation)

---

## Prerequisites

Email notifications require the same instance-level configuration as self-service password
reset, minus the encryption key (notification email carries no token):

- `SCRUMBOY_SMTP_HOST`, `SCRUMBOY_SMTP_FROM` (and `SCRUMBOY_SMTP_PORT` if not 587) — see
  [Required env vars](smtp.md#required-env-vars).
- `SCRUMBOY_PUBLIC_BASE_URL` — used to build board links for notifications about projects that
  still exist. Project-deletion messages intentionally have no board action link.

`GET /api/auth/status` reports both readiness signals independently:
`selfServicePasswordResetEnabled` (also needs `SCRUMBOY_ENCRYPTION_KEY`) and
`emailNotifyAvailable` (SMTP + base URL only). When `emailNotifyAvailable` is `false`, the
Settings → Customization toggle is shown disabled with a hint to configure SMTP.

Even when the instance is fully configured, **no email sends until a user opts in** — see
[User settings](#user-settings).

---

## Categories

Each user chooses which categories they want to be emailed about, independently of the master
toggle:

| Category           | Fires on                                                                                   | Default |
| ------------------ | -------------------------------------------------------------------------------------------- | ------- |
| Assigned to me      | A card is assigned to you (mirrors the existing Web Push assignment notification)             | on      |
| Cards I opened      | A card whose historical creator is you is materially updated or moved while you still have access | off     |
| Card activity        | A card is created, updated, moved, deleted, or its links change                              | off     |
| Sprint activity       | A sprint is created, updated, deleted, activated, or closed                                   | off     |
| Project activity      | A project is updated or deleted, or its settings, workflow columns, or tags change             | off     |
| Added to a project | You are added as a member of a project                                                        | on      |

Categories map onto the server's existing event taxonomy (the same `reason` values already used
for realtime board refresh) rather than introducing a parallel event system — see the
`refreshReasonInfo` table in `internal/httpapi/email_notify.go` for the exact mapping.

A todo's `createdByUserId` is historical data only. A committed update or move by another
authenticated user can publish the internal `todo.creator_notification_requested` consideration
request from the prepared application service. Historical attribution only nominates a candidate.
When the request is consumed, the creator-specific authorization service freshly resolves the
durable project and requires the creator to have a current viewer-or-higher project membership.
Success produces the point-in-time internal `todo.creator_notification_recipient_authorized`
decision; a removed, deleted, or nonexistent creator, temporary or missing project, self request,
or lookup failure produces no recipient. Membership is evaluated in its current state at that
instant, so membership added before authorization permits the decision and membership removed
before authorization denies it.

Neither internal event is a preference or delivery decision, and neither is exposed to browsers or
webhooks. The SSE bridge treats the authorized-recipient event only as a candidate and freshly
loads the durable project and the recipient's current role again immediately before disclosure. If
the creator is no longer a viewer-or-higher member, the project or user is unavailable, the project
is temporary, the event is malformed or self-directed, the lookup fails, or the fanout context is
cancelled, delivery fails closed. The earlier authorization is not a durable entitlement. The
delivery check uses the current project slug rather than the slug carried by the internal event.

After that delivery-time check succeeds, the bridge emits `todo.creator_activity` only on the
creator's private user SSE channel. Its minimum-disclosure payload contains the current project
identity, todo identity, committed title, and activity reason; actor and recipient user IDs remain
internal. The authenticated frontend shows one localized toast using the committed todo title, or
the existing todo-title fallback. It does not reload the board, navigate, change notification
counters, play a sound, or create a desktop notification. Creator SSE is best-effort; cancellation
or a missing connection can drop it without changing the successful todo mutation.

Independently of SSE, the authorized-recipient event starts a non-blocking fresh access check before
it may enqueue a non-sensitive creator email candidate for a material mutation. The queued item
contains no destination, rendered subject, or rendered body. At every SMTP attempt the notification
worker reauthorizes current durable
project access, reads the current master and category preferences, resolves the current user email,
and only then renders the message using the fresh project name and slug. Removal, deletion,
temporary/missing projects, lookup failures, preference changes, or a cancelled worker context all
drop the best-effort email without affecting the committed mutation. Web Push and webhook delivery
remain disabled for creator activity.

---

## Recipients

- **Assigned to me:** the new assignee only. No email on self-assignment.
- **Cards I opened (`createdByMe`):** the historical creator, only after fresh current-access checks.
- **Card / sprint / project activity:** every member of the affected project, except the user who
  made the change.
- **Added to a project:** the newly added user only. No email if you add yourself.

For a historical creator who qualifies for overlapping categories on one mutation, the server
selects at most one email using `Assigned to me` > `Cards I opened` > `Card activity`. A disabled
higher-priority category falls through to the next category that this adapter actually produces.
REST and legacy update/move paths can fall back to card activity; ordinary MCP/Agora mutations do
not invent a `board.refresh_needed` candidate. The selected category is fixed for the queued work
item across SMTP retries: if that category is later disabled, the retry is dropped rather than
switching categories. Semantic update no-ops do not queue creator email. Other eligible members
retain the existing card-activity behavior.

Assignment overlap is decided from private transaction-authoritative creator and durable-project
facts carried by the existing post-commit assignment publication; those facts are not added to the
public `todo.assigned` payload. The email notifier does not reread the todo to guess creator
identity. Temporary projects therefore retain ordinary assignment-email behavior, while a durable
creator candidate that later loses authorization fails closed instead of falling back to a
potentially unauthorized assignment or activity disclosure.

Project deletion is captured from a committed pre-deletion snapshot. Eligible members are checked
against their server-side preferences after the deletion succeeds, the actor is skipped, and the
message has no link to the now-deleted board. The recipient snapshot is passed directly to the
internal email notifier and is never included in SSE, webhook, or Push event payloads. A failed
deletion sends no email.

Each recipient's own preference is checked independently — a project can have five members with
five different opt-in configurations, and each gets email only for what they've enabled.

**Debouncing.** Card/sprint/project activity is suppressed per project, category, and recipient:
a recipient receives at most one activity email for the same project and category every 2 minutes.
The window starts only after that recipient's message is accepted by the notification queue.
Lookup failures, opt-outs, a lack of eligible recipients, and queue rejection do not consume the
window. Assignment and added-to-project notifications are not debounced because they already
target a single recipient per event. This is repeat suppression, not a digest or aggregation.
When creator precedence selects `Card activity` as its fallback, its first send preparation claims
this same window (the category is not known when the minimal candidate initially enters the queue).
That creator work item retains its claim across SMTP retries, so retrying one delivery is not
mistaken for a second activity event.

---

## User settings

Settings → Customization → **Email notifications**:

- A master **Email notifications on** toggle (off by default). No category fires while this is
  off, even if individual categories are checked.
- Six category checkboxes, including the email-only **Cards I opened** option, enabled only once
  the master toggle is on.

Preferences are stored as a JSON blob under the existing generic `user_preferences` table (key
`emailNotifications`), the same mechanism used for wallpaper and other structured preferences —
no dedicated database table.

While signed in, the server value is authoritative. The browser does not use or update its local
anonymous preference cache for an authenticated account. Settings are shown as saved only after a
successful server write; a failed write restores the previous visible value and shows generic,
localized failure copy. A failed initial load leaves the authenticated controls disabled rather
than showing defaults or state from another account. Signing out or changing accounts clears the
in-memory authenticated preference state. Signed-out/anonymous use may retain a local-only value.

## Org-wide default for new users

By default, every newly created user starts with the hardcoded defaults shown in
[Categories](#categories) (**Assigned to me** and **Added to a project** on, everything else off,
master toggle off). An admin or owner can override this per-instance default via
`GET`/`PUT /api/admin/settings/email-notify-default` (see [HTTP endpoints](#http-endpoints)) so
that new users start with the organization's preferred configuration instead.

This is a **seed at creation time only**, not a live-applied policy:

- Changing the org default has **no effect on existing users** — each user's own
  `emailNotifications` row, once written, is never rewritten by a later org-default change.
- **When no override is configured, new users are seeded no row at all** — they behave exactly
  like an instance that never used this feature, resolving to the hardcoded default lazily. A row
  is only written when an admin/owner has actually configured an override.
- A user created **before** an override existed keeps that rowless state and is unaffected by any
  later change.
- A user created **after** an override is set is seeded with the current value at creation, tagged
  internally as an org-seeded row (provenance `org_default`). When that user later saves their own
  preferences, the row is re-tagged as user-owned (`user`).
- The very first (bootstrap) user is never seeded from an org default — there is no admin yet to
  have configured one — and instead falls back to the hardcoded default via the normal unset-value
  path described in [HTTP endpoints](#http-endpoints).
- A **corrupt** stored override (only reachable by editing the database directly) is non-fatal:
  new users are simply not seeded (no fallback row is invented) so account creation still succeeds,
  while admin `GET` continues to surface the corruption as an error.
- To return to the unconfigured state, use `DELETE /api/admin/settings/email-notify-default`
  (see [HTTP endpoints](#http-endpoints)). Re-`PUT`ting the hardcoded values is not the same as
  clearing, because `customized` stays `true` until the override is deleted.
- There is currently no bulk-apply action to retroactively push an org-default change onto
  existing users who haven't customized their own preferences; that and an admin-facing settings
  UI panel are tracked as follow-up work. The provenance tagging above exists so a future
  bulk-apply can safely target only org-seeded rows and never overwrite user-customized ones.

> **Upgrading from a hand-rolled trigger:** some self-hosted operators worked around the lack of
> this feature with a custom `AFTER INSERT ON users` SQLite trigger that inserts an
> `emailNotifications` row. Remove that trigger before upgrading. A trigger that lists columns
> explicitly (`INSERT INTO user_preferences (user_id, key, value, updated_at) VALUES (...)`) will
> keep working (the app's seed upsert overwrites its row when an override is configured), but a
> **positional** trigger (`INSERT INTO user_preferences VALUES (...)`) becomes structurally invalid
> once the `provenance` column is added and must be removed.

## Delivery isolation

Transactional account mail and bulk notification mail use separate bounded queues and separate
workers. Password-reset mail is accepted and delivered through the transactional lane; assignment,
membership, and activity mail use the notification lane. Filling or slowing the notification lane
therefore cannot consume password-reset queue capacity or place a reset behind its backlog. Both
lanes use the same SMTP configuration and retry behavior. Queue rejection is logged internally;
the public password-reset response remains enumeration-safe and unchanged.

---

## HTTP endpoints

Email notification preferences reuse the existing generic preference endpoints (not documented
separately per feature):

- `GET /api/user/preferences?key=emailNotifications` → `{"value": "<JSON>"}`
- `PUT /api/user/preferences` with `{"key": "emailNotifications", "value": "<JSON>"}`

The JSON shape is:

```json
{
  "v": 2,
  "enabled": false,
  "assigned": true,
  "createdByMe": false,
  "cardActivity": false,
  "sprintActivity": false,
  "projectActivity": false,
  "addedToProject": true
}
```

Unset or empty stored values use the complete defaults above. Otherwise the value must be a JSON
object. Missing `v` is accepted as legacy v1 and normalized to v2 with `createdByMe: false`; an
explicit v1 value is migrated the same way. The creator field is valid only in v2, preventing an
old or versionless value from silently opting in. Known boolean fields may be omitted and inherit
their canonical defaults, while explicit `false` is preserved. Unknown fields, `null`, arrays,
malformed JSON, unsupported or invalid versions, and non-boolean category values are rejected.
Every write emits the complete canonical v2 object shown above in stable field order.

**Org-wide default** (admin/owner only — see
[Org-wide default for new users](#org-wide-default-for-new-users)):

- `GET /api/admin/settings/email-notify-default` → `{"value": "<JSON>", "customized": bool}`.
  `value` is always the complete canonical object currently in effect (the hardcoded default when
  no override exists); `customized` reports whether an admin has actually set one.
- `PUT /api/admin/settings/email-notify-default` with `{"value": "<JSON>"}` — same JSON shape and
  validation rules as the per-user preference above. Requires `system_role` of `admin` or `owner`;
  returns `403` otherwise.
- `DELETE /api/admin/settings/email-notify-default` — clears the override, returning `GET` to
  `customized: false` and the hardcoded default. Existing users' preferences are untouched;
  subsequently created users are seeded no row. Requires `system_role` of `admin` or `owner`
  (`403` otherwise); returns `204 No Content` and is idempotent (deleting when unset still `204`).

---

## Quick verification

1. Configure SMTP and `SCRUMBOY_PUBLIC_BASE_URL` as in [SMTP quick verification](smtp.md#quick-verification)
   (a local catcher like Mailpit is easiest for testing).
2. Confirm `GET /api/auth/status` reports `"emailNotifyAvailable": true`.
3. Sign in as two users on the same project. As user A, enable **Email notifications on** and the
   **Card activity** category in Settings → Customization.
4. As user B, create or move a card on the shared project. Confirm user A receives exactly one
   email with a working link back to the project, and user B (the actor) does not.
5. Assign a card to user A as user B. Confirm a second email arrives for the assignment (default
   **Assigned to me** category), and that self-assigning never sends mail.
6. Turn the master toggle off and repeat — confirm no email sends.

---

## Related documentation

- [`docs/smtp.md`](smtp.md) — the SMTP configuration this feature is built on (env vars, TLS
  modes, delivery/retry behavior, provider examples).
- [`FAQ.md`](../FAQ.md) — notification-related entries.
