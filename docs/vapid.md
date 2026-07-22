# VAPID keys and Web Push in Scrumboy

**VAPID** (Voluntary Application Server Identification) is the standard way a web application identifies itself to browser push services (Mozilla, Google, Apple, and others). In Scrumboy, VAPID keys are **optional server credentials** that enable **Web Push** — background alerts when someone **assigns you a todo** while the app tab is in the background or the PWA is not focused.

For operator setup (Docker wiring, verification commands, PWA install, and auto-subscribe behavior), see [`docs/pwa.md`](pwa.md). This document is the normative guide for enablement validation, status/reason fields, and what assignment push payloads contain.

---

## Do I need VAPID keys?

**Usually no.** Boards, live SSE updates, in-tab notifications, and normal use work without VAPID. Set keys only when you want **background assignment push** on installed or backgrounded clients.

Scrumboy does **not** ship default VAPID keys. Each self-hosted instance generates or supplies its own pair.

---

## Effective enablement (not just keys present)

Non-empty `SCRUMBOY_VAPID_*` strings are **not** enough. The server prepares configuration with `prepareWebPushConfiguration` and treats push as **effectively enabled** only when the prepared status `state` is `enabled`. That requires **all** of:

1. **Full mode** (anonymous mode never enables push).
2. A **decodable 65-byte** uncompressed P-256 VAPID **public** key (URL-safe base64).
3. A **decodable 32-byte** VAPID **private** key (URL-safe base64), scalar in range.
4. The configured public key **matches** the public key derived from the private key.
5. A **valid subscriber** value (or the built-in default when unset) — see below.

When effectively enabled, Scrumboy:

1. Sets **`pushConfigured: true`** on **`GET /api/auth/status`** (this flag is true only when `state === "enabled"`).
2. For **signed-in** users in full mode, also returns **`push: { state, reason }`** with the detailed status.
3. Serves the public key at **`GET /api/push/vapid-public-key`** for browser subscription.
4. Accepts **subscribe / unsubscribe** requests from signed-in users and stores push endpoints per device.
5. Sends **assignment notifications** through the browser’s push network when a todo is assigned to a subscribed user.

### Status and reason contract

| `state` | `reason` | Meaning |
|---------|----------|---------|
| `enabled` | `null` | All gates passed; push APIs and sends are active |
| `not_configured` | `null` | Both public and private keys empty/whitespace |
| `invalid` | `invalid_vapid_public_key` | Public missing, undecodable, wrong length, or not a valid P-256 point |
| `invalid` | `invalid_vapid_private_key` | Private missing, undecodable, wrong length, or out of range |
| `invalid` | `invalid_subscriber` | `SCRUMBOY_VAPID_SUBSCRIBER` failed validation |
| `unavailable` | `initialization_failed` | Keys decode but the public key does **not** match the private key (pair mismatch) |
| `unavailable` | `null` | Keys and subscriber OK, but mode is not `full` (anonymous) |

Partial keys (only one of public/private set) yield **`invalid`** with the corresponding key reason — not `not_configured`.

### Startup logs vs effective status

At startup the process may log one of (presence-oriented helper `PushConfigured`):

- `web push: enabled`
- `web push: disabled (anonymous mode)`
- `web push: disabled (set SCRUMBOY_VAPID_PUBLIC_KEY and SCRUMBOY_VAPID_PRIVATE_KEY)`
- `web push: partial config ignored`

That banner can disagree with effective enablement (for example non-empty but invalid or mismatched keys may still log `web push: enabled`). When preparation fails with a reason, the server also logs `push: disabled: <reason>`. **Confirm** with signed-in **`GET /api/auth/status`** (`pushConfigured` and `push`) and **`GET /api/push/vapid-public-key`** (`200` with `publicKey`, or `503` / `PUSH_UNAVAILABLE` when push is not effectively enabled — not only when keys are missing).

Anonymous mode: `pushConfigured` is forced false and the detailed `push` object is omitted.

---

## Two different notification paths

Do not confuse these — they solve different problems:

| Setting / feature | What it does |
|-------------------|--------------|
| **Enable notifications** (Settings) | In-tab / desktop alerts while the browser still has Scrumboy open (Notification API). |
| **Web Push** (needs effective VAPID on the server) | Can reach you when the tab is in the background or the PWA is not focused; uses the browser’s push service. |

Turning on desktop notifications does **not** replace VAPID. Setting VAPID does **not** bypass the browser permission prompt — each user must still **allow notifications** on each device.

After sign-in, the SPA may attempt auto-subscribe when **`pushConfigured`** is true. Users can opt out or back in under **Settings → Customization → Web Push**.

---

## Environment variables

| Variable | Required | Purpose |
|----------|----------|---------|
| `SCRUMBOY_VAPID_PUBLIC_KEY` | Yes (with private) | Public key (URL-safe base64). Shared with browsers for subscription. |
| `SCRUMBOY_VAPID_PRIVATE_KEY` | Yes (with public) | Private key (URL-safe base64). Used server-side to sign outbound push requests. **Keep secret.** |
| `SCRUMBOY_VAPID_SUBSCRIBER` | No | Contact hint for the VAPID JWT **`sub`** claim (plain email or `mailto:` / `https:` URL). Not login identity — see below. |
| `SCRUMBOY_DEBUG_PUSH` | No | Set to `1` to log push send/prune behavior on the server. |

Scrumboy does **not** auto-load `.env` files inside the process. Your process manager, Compose, or Kubernetes must inject these into the running server. See [`docs/pwa.md`](pwa.md#docker-setup-and-verification) for Docker examples.

Example entries in `scrumboy.env.example` at the repo root.

---

## Generating a key pair

Use any VAPID-capable tool, for example:

- [`web-push` npm](https://www.npmjs.com/package/web-push) — `npx web-push generate-vapid-keys`
- [VapidKeys.com](https://vapidkeys.com/) or similar generators

Paste the **public** and **private** values into your environment as URL-safe base64 (as the tool outputs them). Restart or recreate the server after changing keys. Public and private must be a **matching pair** from the same generation; mixing keys from different runs yields `unavailable` / `initialization_failed`.

**Important:** Treat the private key like a secret. Do not commit it to git or expose it in client-side code. Only the public key is returned by the API.

---

## `SCRUMBOY_VAPID_SUBSCRIBER`

Push providers expect a stable **contact** on outbound requests. This value becomes the JWT **`sub`** (subject) claim after `prepareWebPushSubscriber` validation.

- Use a plain operations email (e.g. `ops@example.com`) — config load may add a `mailto:` prefix; preparation accepts plain mailbox, `mailto:…`, or an unambiguous absolute `https://…` URL (with host; no userinfo/fragment).
- It does **not** need to match OIDC, user emails, or who can sign in.
- Rejected forms include control characters, nested `mailto:mailto:…`, `http://`, display-name mailboxes, and ambiguous HTTPS URLs — these yield `invalid` / `invalid_subscriber`.
- If unset, the server uses the built-in default contact `scrumboy@localhost` (see `prepareWebPushSubscriber` in `internal/httpapi/push_config.go`).

---

## What gets sent (and what does not)

Web Push in Scrumboy is **not product telemetry**. VAPID identifies **your** Scrumboy server to the push network so assignment events can be delivered to devices that opted in. Board data is not sent to Scrumboy’s project maintainers.

For each `todo.assigned` event (not self-assign), the server builds an encrypted Web Push JSON payload with these fields:

| Field | Value |
|-------|--------|
| `type` | `"todo_assigned"` |
| `title` | `"Assigned to you"` (fixed UI string) |
| `body` | The **todo title** |
| `projectSlug` | Project slug (used by the service worker for deep-link click routing) |
| `todoId` | Internal todo id (same) |
| `scrumboyPush` | `true` |
| `debug` | Present only when `SCRUMBOY_DEBUG_PUSH` is enabled |

**Not included** in the current payload: assigner identity, assignee user ids, todo notes/body, tags, estimation, comments, sprint, status, or other full todo fields.

Payload encryption protects the message on the Web Push delivery path to the user’s browser. It does **not** mean the todo title stays only on your Scrumboy host — the title leaves the instance inside that encrypted payload and is shown as the OS notification body. Avoid assuming that “encrypted” equals “never leaves the instance.”

---

## Quick verification

1. Confirm env vars are visible to the running process (e.g. `docker exec scrumboy env | grep SCRUMBOY_VAPID`).
2. Prefer **`GET /api/auth/status`** while signed in — expect **`pushConfigured: true`** and **`push.state: "enabled"`** (reason `null`). If push is off, inspect `push.state` / `push.reason` and any `push: disabled: …` startup log.
3. Treat startup `web push: enabled` as a coarse presence hint only; it is not proof of effective enablement.
4. Call **`GET /api/push/vapid-public-key`** — expect **`200`** with `{ "publicKey": "..." }`, or **`503`** / `PUSH_UNAVAILABLE` when push is not effectively enabled.

See [`docs/pwa.md`](pwa.md) for full Docker and curl examples.

---

## Related documentation

- [`docs/pwa.md`](pwa.md) — PWA install, Docker setup, auto-subscribe, Settings opt-out, trade-offs.
- [`FAQ.md`](../FAQ.md#what-are-vapid-keys-and-do-i-need-them) — short operator FAQ entry.
- [`API.md`](../API.md) — `pushConfigured` / `push` on auth status and push API routes.
- [`README.md`](../README.md#config) — env variable table.
