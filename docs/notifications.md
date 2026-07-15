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
- [HTTP endpoints](#http-endpoints)
- [Quick verification](#quick-verification)
- [Related documentation](#related-documentation)

---

## Prerequisites

Email notifications require the same instance-level configuration as self-service password
reset, minus the encryption key (notification email carries no token):

- `SCRUMBOY_SMTP_HOST`, `SCRUMBOY_SMTP_FROM` (and `SCRUMBOY_SMTP_PORT` if not 587) — see
  [Required env vars](smtp.md#required-env-vars).
- `SCRUMBOY_PUBLIC_BASE_URL` — used to build the board link included in every notification email.

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
| Project activity      | Project settings, workflow columns, or tags change                                            | off     |
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

Each recipient's own preference is checked independently — a project can have five members with
five different opt-in configurations, and each gets email only for what they've enabled.

**Debouncing.** Card/sprint/project activity is coalesced per (project, category): a burst of
mutations (e.g. reordering many cards) sends at most one round of activity email per category
every 2 minutes, rather than one email per member per mutation. Assignment and added-to-project
notifications are not debounced — they already target a single recipient per event.

---

## User settings

Settings → Customization → **Email notifications**:

- A master **Email notifications on** toggle (off by default). No category fires while this is
  off, even if individual categories are checked.
- Five category checkboxes, enabled only once the master toggle is on.

Preferences are stored as a JSON blob under the existing generic `user_preferences` table (key
`emailNotifications`), the same mechanism used for wallpaper and other structured preferences —
no dedicated database table.

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

Malformed or unsupported-version JSON is rejected with a validation error; unset preferences fall
back to the defaults above.

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
