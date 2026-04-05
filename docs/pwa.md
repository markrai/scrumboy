# Progressive Web App (PWA) and Web Push (VAPID)

Scrumboy can be installed as a PWA. For **background assignment notifications** (when the app is closed or not focused), the server must expose **VAPID** keys. Users still must **allow notifications** in the browser when prompted; there is no way to bypass OS/browser permission.

## VAPID keys and the subscriber contact

- Generate a key pair (for example with [`web-push` npm](https://www.npmjs.com/package/web-push), the [VapidKeys.com](https://vapidkeys.com/) generator, or any VAPID tool).
- Set **`SCRUMBOY_VAPID_PUBLIC_KEY`** and **`SCRUMBOY_VAPID_PRIVATE_KEY`** (URL-safe base64, as typically output by generators).

When **both** keys are set, the server enables Web Push for the instance: subscribe/unsubscribe APIs work, assignment events can be pushed, and **`GET /api/push/vapid-public-key`** returns **`{ "publicKey": "..." }`**. That is treated as operator intent to offer push on this deployment.

### `SCRUMBOY_VAPID_SUBSCRIBER`

This value becomes the JWT **`sub`** (subject) claim on outbound Web Push requests. It is a **contact hint for push services** (Mozilla/Google), not login identity.

- **Any stable contact is fine** - operations email, `admin@yourcompany.com`, a `https://` policy URL, etc.
- It does **not** need to match your OIDC issuer, user emails, or IdP.
- Use a **plain email** in the environment variable, e.g. `ops@example.com`. The server normalizes it to `mailto:ops@example.com`.
- If you already use a full URL, use **`mailto:...`** or **`https://...`** explicitly - do **not** prefix a bare email with `mailto:` in env if you are also pasting a full `mailto:user@host` (avoid `mailto:mailto:...`).

If unset, the server falls back to an internal default contact for `sub` (see `internal/httpapi/push_notify.go`).

## Auto-subscribe after sign-in

After a user signs in (full mode, same origin), the SPA calls **`maybeAutoSubscribePushAfterLogin`**, which:

1. Checks **browser support** (`serviceWorker`, `PushManager`).
2. Fetches **`GET /api/push/vapid-public-key`**. If the response is OK and includes **`publicKey`**, it proceeds (meaning VAPID is configured on the server).
3. Attempts **`subscribeToPush()`** unless a **per-user** autosub outcome is already stored in **localStorage** (`scrumboy_push_autosub_v1_u{userId}`): **`done`** (already subscribed or subscribe succeeded) or **`denied`** (notification permission blocked). **Transient failures** and **dismissed prompts** (permission still **`default`**) do **not** lock the path, so a later reload can retry without opening Settings.

This is **per browser / per device**, not a server-side default stored in the database.

The legacy key **`scrumboy_push_autosub_v1`** (global) is **no longer read**; it can be removed from storage manually if present.

**Settings → Customization** still exposes a **Web Push** checkbox: optional override to **disable** (unsubscribe) or **re-enable** after the user turned push off. It is not required onboarding when VAPID is configured.

### Trade-offs (operator awareness)

- **Permission prompts** on first sign-in can feel aggressive; users may block notifications.
- **Shared machines / kiosks**: auto prompts may be unwelcome; users can deny or disable in Settings.
- **Browser variance**: blocked prompts require fixing site settings in the browser; the app cannot override that.

## Related environment variables

| Variable | Purpose |
|----------|---------|
| `SCRUMBOY_VAPID_PUBLIC_KEY` | Public key (required with private for push). |
| `SCRUMBOY_VAPID_PRIVATE_KEY` | Private key. |
| `SCRUMBOY_VAPID_SUBSCRIBER` | Contact for VAPID `sub` (plain email or `mailto:` / `https:` URL). |
| `SCRUMBOY_DEBUG_PUSH` | `1` - server logs for push send/prune. |

See also the main [README](../README.md#config) env table.

## User-facing controls

- **Desktop notifications** (in-page / tab background): Settings -> **Enable notifications** (Notification API).
- **Background Web Push** (installed PWA / closed app): automatic attempt when VAPID is configured; **Web Push on this device** in Settings to opt out or opt back in.

Both can be used together; Web Push is what reaches users when SSE is throttled in the background.

## Automated tests

There is no browser test suite wired for `push.ts` auto-subscribe state today; behavior is covered by code review and manual checks. Adding a small unit test around storage-key helpers or a headless flow would reduce regression risk as this logic grows.
