# Progressive Web App (PWA) and Web Push (VAPID)

For what VAPID keys are, whether you need them, **effective enablement / status reasons**, and **what assignment payloads contain**, see [`docs/vapid.md`](vapid.md). This document covers PWA install, operator Docker setup, verification commands, and client auto-subscribe behavior.

Scrumboy can be installed as a PWA. For **background assignment notifications** (when the app is closed or not focused), the server must have **effectively enabled** Web Push (validated matching VAPID key pair in full mode — not merely non-empty env strings). Users still must **allow notifications** in the browser when prompted; there is no way to bypass OS/browser permission.

## VAPID keys and the subscriber contact

- Generate a key pair (for example with [`web-push` npm](https://www.npmjs.com/package/web-push), the [VapidKeys.com](https://vapidkeys.com/) generator, or any VAPID tool).
- Set **`SCRUMBOY_VAPID_PUBLIC_KEY`** and **`SCRUMBOY_VAPID_PRIVATE_KEY`** (URL-safe base64, as typically output by generators). Use a matching pair from the same generation.

Web Push is active only when prepared status is **`enabled`** (full mode + valid 65-byte public + valid 32-byte private + matching pair + valid/default subscriber). Then subscribe/unsubscribe APIs work, assignment events can be pushed, **`pushConfigured`** is true on auth status, and **`GET /api/push/vapid-public-key`** returns **`{ "publicKey": "..." }`**. See the status/reason table in [`docs/vapid.md`](vapid.md#effective-enablement-not-just-keys-present). Anonymous mode keeps Web Push unavailable even if keys are present.

### `SCRUMBOY_VAPID_SUBSCRIBER`

This value becomes the JWT **`sub`** (subject) claim on outbound Web Push requests. It is a **contact hint for push services** (Mozilla/Google), not login identity.

- **Any stable contact is fine** - operations email, `admin@yourcompany.com`, a `https://` policy URL, etc.
- It does **not** need to match your OIDC issuer, user emails, or IdP.
- Use a **plain email** in the environment variable, e.g. `ops@example.com`, or an explicit **`mailto:...`** / **`https://...`** URL. Do **not** create nested `mailto:mailto:...`.
- Invalid subscriber values leave push **`invalid`** / `invalid_subscriber` (push stays off).

If unset, the server falls back to an internal default contact for `sub` (see `prepareWebPushSubscriber` in `internal/httpapi/push_config.go`).

## Docker setup and verification

The stock `docker-compose.yml` keeps Web Push optional. To enable it, Docker must pass the VAPID variables into the running container; setting them only in your shell or in an unrelated `.env` file is not enough.

Example Compose wiring:

```yaml
services:
  scrumboy:
    environment:
      - SCRUMBOY_VAPID_PUBLIC_KEY=${SCRUMBOY_VAPID_PUBLIC_KEY:-}
      - SCRUMBOY_VAPID_PRIVATE_KEY=${SCRUMBOY_VAPID_PRIVATE_KEY:-}
      - SCRUMBOY_VAPID_SUBSCRIBER=${SCRUMBOY_VAPID_SUBSCRIBER:-}
```

Example host `.env` next to `docker-compose.yml`:

```env
SCRUMBOY_VAPID_PUBLIC_KEY=REPLACE_WITH_PUBLIC_KEY
SCRUMBOY_VAPID_PRIVATE_KEY=REPLACE_WITH_PRIVATE_KEY
SCRUMBOY_VAPID_SUBSCRIBER=ops@example.com
```

Notes:

- **Both** `SCRUMBOY_VAPID_PUBLIC_KEY` and `SCRUMBOY_VAPID_PRIVATE_KEY` are required for enablement. One without the other yields prepared status **`invalid`** (not “silently ignored”); startup may still log `web push: partial config ignored`.
- Scrumboy does **not** auto-load env files inside the container; Compose must inject the variables into the service.
- After changing Compose or the injected env values, recreate the container so the process starts with the new environment:

```bash
docker compose up -d --build --force-recreate
```

Verify the running container sees the variables:

```bash
docker exec scrumboy env | grep SCRUMBOY_VAPID
```

Verify the live API returns the public key:

```bash
curl -sS -D- http://127.0.0.1:8080/api/push/vapid-public-key
```

Expected results:

- `200` with `publicKey` means Web Push is **effectively enabled**.
- `503` with `PUSH_UNAVAILABLE` means push is **not** effectively enabled (missing keys, invalid/mismatched keys, bad subscriber, anonymous mode, or other non-`enabled` state) — not only “keys missing.”

At startup the server also logs a presence-oriented `web push: …` line and may log `push: disabled: <reason>` when validation fails. Prefer signed-in **`GET /api/auth/status`** (`pushConfigured` and `push.state` / `push.reason`) over the startup banner alone. Details: [`docs/vapid.md`](vapid.md#startup-logs-vs-effective-status).

## Auto-subscribe after sign-in

After a user signs in (full mode, same origin), the SPA checks **`GET /api/auth/status`**. If that response says **`pushConfigured: true`**, it then calls **`maybeAutoSubscribePushAfterLogin`**, which:

1. Checks **browser support** (`serviceWorker`, `PushManager`).
2. Fetches **`GET /api/push/vapid-public-key`** to get the actual public key for the subscribe flow.
3. Attempts **`subscribeToPush()`** unless a **per-user** autosub outcome is already stored in **localStorage** (`scrumboy_push_autosub_v1_u{userId}`): **`done`** (already subscribed or subscribe succeeded) or **`denied`** (notification permission blocked). **Transient failures** and **dismissed prompts** (permission still **`default`**) do **not** lock the path, so a later reload can retry without opening Settings.

This is **per browser / per device**, not a server-side default stored in the database.

The legacy key **`scrumboy_push_autosub_v1`** (global) is **no longer read**; it can be removed from storage manually if present.

**Settings → Customization** still exposes a **Web Push** checkbox: optional override to **disable** (unsubscribe) or **re-enable** after the user turned push off. It is not required onboarding when VAPID is configured. The Settings screen now uses `pushConfigured` from auth status instead of probing the VAPID endpoint on render.

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

See also the main [README](../README.md#config) env table and [`docs/vapid.md`](vapid.md) for validation rules.

## User-facing controls

- **Desktop notifications** (in-page / tab background): Settings -> **Enable notifications** (Notification API).
- **Background Web Push** (installed PWA / closed app): automatic attempt when Web Push is effectively enabled; **Web Push on this device** in Settings to opt out or opt back in.

Both can be used together; Web Push is what reaches users when SSE is throttled in the background.

## Automated tests

There is no browser test suite wired for `push.ts` auto-subscribe state today; behavior is covered by code review and manual checks. Adding a small unit test around storage-key helpers or a headless flow would reduce regression risk as this logic grows.
