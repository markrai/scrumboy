# Progressive Web App (PWA) and Web Push (VAPID)

Normative enablement, status/reason contract, and assignment **payload fields**: [`docs/vapid.md`](vapid.md). This document covers PWA install, Docker wiring, verification, client auto-subscribe, and notification click deep-links.

Scrumboy can be installed as a PWA. **Background assignment notifications** need Web Push **effectively enabled** on the server (validated matching VAPID key pair in full mode — not merely non-empty env strings). Users must still **allow notifications** in the browser; there is no bypass for OS/browser permission.

## Enablement (summary)

- Generate a matching VAPID key pair; set `SCRUMBOY_VAPID_PUBLIC_KEY` and `SCRUMBOY_VAPID_PRIVATE_KEY`.
- Optional `SCRUMBOY_VAPID_SUBSCRIBER` is the push-service contact (`sub` claim)—not login identity. Details: [`vapid.md`](vapid.md).
- Push is active only when prepared status is **`enabled`** (full mode + valid matching keys + valid/default subscriber). Then subscribe APIs work, assignment events can be pushed, `pushConfigured` is true on auth status, and `GET /api/push/vapid-public-key` returns `{ "publicKey": "..." }`.
- Anonymous mode keeps Web Push unavailable even if keys are present.

## Docker setup and verification

The stock `docker-compose.yml` keeps Web Push optional. Compose must inject VAPID variables into the container; setting them only in your shell or an unused `.env` is not enough.

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

- Both public and private keys are required for enablement; one without the other yields prepared status **`invalid`**. Startup may still log `web push: partial config ignored`.
- After changing Compose/env, recreate the container so the process sees the new values:

```bash
docker compose up -d --build --force-recreate
```

Verify:

```bash
docker exec scrumboy env | grep SCRUMBOY_VAPID
curl -sS -D- http://127.0.0.1:8080/api/push/vapid-public-key
```

- `200` with `publicKey` → Web Push **effectively enabled**.
- `503` with `PUSH_UNAVAILABLE` → not effectively enabled (see [`vapid.md`](vapid.md#effective-enablement-not-just-keys-present)).

Prefer signed-in **`GET /api/auth/status`** (`pushConfigured`, `push.state` / `push.reason`) over the presence-oriented startup `web push: …` banner alone.

## Auto-subscribe after sign-in

After sign-in (full mode, same origin), the SPA checks **`GET /api/auth/status`**. If **`pushConfigured: true`**, `maybeAutoSubscribePushAfterLogin`:

1. Checks browser support (`serviceWorker`, `PushManager`).
2. Fetches **`GET /api/push/vapid-public-key`**.
3. Attempts **`subscribeToPush()`** unless a **per-user** autosub outcome is already in **localStorage** (`scrumboy_push_autosub_v1_u{userId}`): **`done`** or **`denied`**. Transient failures and dismissed prompts (`default` permission) do not lock the path.

This is **per browser / per device**, not a server-side default. The legacy global key **`scrumboy_push_autosub_v1`** is no longer read.

**Settings → Customization** still exposes a **Web Push** checkbox to opt out or re-enable. Settings uses `pushConfigured` from auth status (no VAPID probe on render).

### Trade-offs

- Permission prompts on first sign-in can feel aggressive.
- Shared machines / kiosks may not want auto prompts.
- Blocked prompts require fixing site settings in the browser.

## Notification click deep-link

When the user opens an assignment notification, the service worker (`sw.js`) deep-links to:

```text
/{projectSlug}?openTodoId={todoId}
```

when the push payload includes both `projectSlug` and `todoId`; otherwise it opens `/`. Payload field contract and privacy notes: [`vapid.md`](vapid.md). **Automated tests do not cover** this click → URL contract today; treat it as a manual/browser check before release when changing push payloads or `sw.js` click handling.

## Related environment variables

| Variable | Purpose |
|----------|---------|
| `SCRUMBOY_VAPID_PUBLIC_KEY` | Public key (required with private for push). |
| `SCRUMBOY_VAPID_PRIVATE_KEY` | Private key. |
| `SCRUMBOY_VAPID_SUBSCRIBER` | Contact for VAPID `sub` (plain email or `mailto:` / `https:` URL). |
| `SCRUMBOY_DEBUG_PUSH` | `1` - server logs for push send/prune. |

See the main [README](../README.md#config) env table and [`docs/vapid.md`](vapid.md).

## User-facing controls

- **Desktop notifications** (in-page / tab background): Settings → **Enable notifications** (Notification API).
- **Background Web Push** (installed PWA / closed app): automatic attempt when Web Push is effectively enabled; **Web Push on this device** in Settings to opt out or opt back in.

Both can be used together; Web Push reaches users when SSE is throttled in the background.

## Automated tests

There is no browser test suite wired for `push.ts` auto-subscribe or notification-click deep-links today; behavior is covered by code review and manual checks. Adding unit tests around storage-key helpers, payload fields, or a headless click flow would reduce regression risk.
