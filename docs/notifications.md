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
| Card activity        | A card is created, updated, moved, deleted, or its links change                              | off     |
| Sprint activity       | A sprint is created, updated, deleted, activated, or closed                                   | off     |
| Project activity      | A project is updated or deleted, or its settings, workflow columns, or tags change             | off     |
| Added to a project | You are added as a member of a project                                                        | on      |

Categories map onto the server's existing event taxonomy (the same `reason` values already used
for realtime board refresh) rather than introducing a parallel event system — see the
`refreshReasonCategory` table in `internal/httpapi/email_notify.go` for the exact mapping.

---

## Recipients

- **Assigned to me:** the new assignee only. No email on self-assignment.
- **Card / sprint / project activity:** every member of the affected project, except the user who
  made the change.
- **Added to a project:** the newly added user only. No email if you add yourself.

When a card mutation also changes its assignee, the new assignee receives only the targeted
**Assigned to me** message for that event. Other eligible members can receive the separate
**Card activity** message. Removing an assignment still counts as card activity. This keeps the
assignment and activity meanings distinct without duplicating mail to the new assignee.

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

---

## User settings

Settings → Customization → **Email notifications**:

- A master **Email notifications on** toggle (off by default). No category fires while this is
  off, even if individual categories are checked.
- Five category checkboxes, enabled only once the master toggle is on.

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
  "v": 1,
  "enabled": false,
  "assigned": true,
  "cardActivity": false,
  "sprintActivity": false,
  "projectActivity": false,
  "addedToProject": true
}
```

Unset or empty stored values use the complete defaults above. Otherwise the value must be a JSON
object. Missing `v` is accepted as legacy v1 and normalized to `v: 1`, so `{}` also means the
canonical defaults. An explicit `v` must be numeric `1`. Known boolean fields may be omitted and
inherit their canonical defaults, while explicit `false` is preserved. Unknown fields, `null`,
arrays, malformed JSON, unsupported or invalid versions, and non-boolean category values are
rejected. Every write emits the complete canonical v1 object shown above in stable field order.

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
