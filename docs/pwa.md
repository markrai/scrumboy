# Progressive Web App (PWA) and Web Push (VAPID)

Scrumboy can be installed as a PWA. For **background assignment notifications** (when the app is closed or not focused), the server must expose **VAPID** keys and users must allow notifications in the browser.

## VAPID keys and the subscriber contact

- Generate a key pair (for example with [`web-push` npm](https://www.npmjs.com/package/web-push) or any VAPID tool).
- Set **`SCRUMBOY_VAPID_PUBLIC_KEY`** and **`SCRUMBOY_VAPID_PRIVATE_KEY`** (URL-safe base64, as typically output by generators).

### `SCRUMBOY_VAPID_SUBSCRIBER`

This value becomes the JWT **`sub`** (subject) claim on outbound Web Push requests. It is a **contact hint for push services** (Mozilla/Google), not login identity.

- **Any stable contact is fine** - operations email, `admin@yourcompany.com`, a `https://` policy URL, etc.
- It does **not** need to match your OIDC issuer, user emails, or IdP.
- Use a **plain email** in the environment variable, e.g. `ops@example.com`. The server normalizes it to `mailto:ops@example.com`.
- If you already use a full URL, use **`mailto:...`** or **`https://...`** explicitly - do **not** prefix a bare email with `mailto:` in env if you are also pasting a full `mailto:user@host` (avoid `mailto:mailto:...`).

If unset, the server falls back to an internal default contact for `sub` (see `internal/httpapi/push_notify.go`).

## Optional: auto-enable Web Push for new sessions

When **`SCRUMBOY_PUSH_BY_DEFAULT_IF_VAPID=true`** (or `1` / `yes`) **and** both VAPID keys are set, the API adds **`pushByDefaultIfVapid: true`** to **`GET /api/push/vapid-public-key`**. After login, the SPA may **attempt automatic Web Push subscription** for that signed-in user (same browser), which can trigger the OS/browser notification prompt.

- Leave unset or `false` to keep today's behavior: users opt in only via **Settings -> Customization -> Enable Web Push**.
- Auto-subscribe progress is stored in **localStorage per user id** (`scrumboy_push_autosub_v1_u{userId}`), so **different accounts on the same browser** each get their own automatic attempt. Durable states are **`done`** (subscribed or already subscribed) and **`denied`** (notifications blocked). **Transient failures** and **dismissed prompts** (permission still `default`) do **not** consume the auto path, so a later reload can retry without requiring Settings.
- This is still **client-side / per device**, not a server-side "default user preference" flag in the database.

The legacy key **`scrumboy_push_autosub_v1`** (global) is **no longer read**; it can be removed from storage manually if present.

## Defaulting notifications "on" for every new user (design note)

**Not implemented as a global product default** - operators can opt in with `SCRUMBOY_PUSH_BY_DEFAULT_IF_VAPID` above.

### Pros of pushing notifications toward "on" by default

- Fewer missed assignments when the PWA is in the background.
- Aligns with how many chat / mail apps are experienced (prompt once, then notifications work).

### Cons

- **Permission prompts** on first visit can feel aggressive or train users to click "Block."
- **Compliance and expectations**: some teams want explicit opt-in for any cross-device signal.
- **Shared machines / kiosks**: auto prompts are unwelcome.
- **Browser variance**: blocked or dismissed prompts still require users to fix settings manually; auto-subscribe does not bypass `Notification` permission.

**Recommendation:** use **`SCRUMBOY_PUSH_BY_DEFAULT_IF_VAPID`** only on instances where your audience expects assignment alerts; keep it off for public or low-trust deployments.

## Related environment variables

| Variable | Purpose |
|----------|---------|
| `SCRUMBOY_VAPID_PUBLIC_KEY` | Public key (required with private for push). |
| `SCRUMBOY_VAPID_PRIVATE_KEY` | Private key. |
| `SCRUMBOY_VAPID_SUBSCRIBER` | Contact for VAPID `sub` (plain email or `mailto:` / `https:` URL). |
| `SCRUMBOY_PUSH_BY_DEFAULT_IF_VAPID` | `true` / `1` / `yes` - advertise auto-subscribe when VAPID is configured. |
| `SCRUMBOY_DEBUG_PUSH` | `1` - server logs for push send/prune. |

See also the main [README](../README.md#config) env table.

## User-facing controls

- **Desktop notifications** (in-page / tab background): Settings -> **Enable notifications** (Notification API).
- **Background Web Push** (installed PWA / closed app): same tab -> **Enable Web Push** (requires VAPID on the server).

Both can be used together; Web Push is what reaches users when SSE is throttled in the background.

## Automated tests

There is no browser test suite wired for `push.ts` auto-subscribe state today; behavior is covered by code review and manual checks. Adding a small unit test around storage-key helpers or a headless flow would reduce regression risk as this logic grows.
