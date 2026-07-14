# SMTP and self-service password reset in Scrumboy

**SMTP** credentials are **optional server config** that enable a self-service **"forgot password"** email flow: `POST /api/auth/request-password-reset`. Without SMTP, password reset is still possible, but only via the existing **admin-generated reset link** (Settings → Users → Password), which an admin must hand-deliver out of band.

This document explains what SMTP enables, how to configure it, and how to verify it. For the token/reset mechanics themselves, see [`API.md`](../API.md).

---

## Do I need this?

**Usually optional.** The admin-generated reset link (`POST /api/admin/users/{id}/password-reset`) always remains available regardless of SMTP configuration — it's unaffected by anything in this document. SMTP is purely a self-service convenience upgrade on top of that: it lets a user request their own reset link by email instead of an admin generating and sending one manually.

Scrumboy does **not** ship default SMTP credentials. Each self-hosted instance supplies its own relay.

---

## What SMTP enables

When Host, Port, and From are **all** set (Username/Password are optional — some relays allow trusted-network submission without auth) **and** `SCRUMBOY_ENCRYPTION_KEY` is also configured (used to sign the reset token, same as the admin flow), Scrumboy:

1. Exposes **`POST /api/auth/request-password-reset`**, which accepts `{"email": "..."}` and emails a reset link when the address matches a registered account.
2. Always returns the same generic response (`"If that account exists, a password reset email has been sent."`) regardless of whether the email is registered, whether SMTP is configured, or whether the encryption key is set. **This is intentional** — a different status code or body per case would let an attacker enumerate registered accounts. A 200 response does **not** confirm an email was actually sent.
3. Delivers the email **asynchronously** via an in-memory queue with retry: up to 3 attempts with backoff, mirroring the architecture already used for outbound webhooks. Transient SMTP hiccups (a relay timeout, a momentary DNS blip) self-heal without the user re-submitting.
4. Rate-limits requests to 5/minute per IP and 5/minute per submitted email, reusing the same dual-key limiter used elsewhere in auth.

If SMTP is not configured (or only partially configured), the endpoint still returns the same generic success response — but no email is sent, and the admin manual-reset endpoint remains the only working path. At startup the server logs one of:

- `smtp: enabled (host=... port=...)`
- `smtp: disabled (set SCRUMBOY_SMTP_HOST, SCRUMBOY_SMTP_PORT, and SCRUMBOY_SMTP_FROM to enable password-reset emails)`
- `smtp: partial config ignored (set SCRUMBOY_SMTP_HOST, SCRUMBOY_SMTP_PORT, and SCRUMBOY_SMTP_FROM together)`

There is no anonymous-mode-specific log line: `request-password-reset` (like `reset-password`) already returns 404 in anonymous mode regardless of SMTP configuration, since anonymous mode has no authenticated accounts to reset.

---

## TLS modes

Set `SCRUMBOY_SMTP_TLS_MODE` explicitly — it is never inferred from the port number:

| Mode | Typical port | Behavior |
|------|--------------|----------|
| `starttls` (default) | 587 | Connects in plaintext, then upgrades via STARTTLS before authenticating or sending. Fails closed if the server doesn't advertise STARTTLS support — it will not silently fall back to plaintext. |
| `implicit` | 465 | The entire connection is TLS from the first byte (no STARTTLS negotiation). |
| `none` | — | Plaintext throughout. Only appropriate for local/dev catchers (e.g. MailHog/Mailpit) on a trusted network — never use against a real relay over the public internet. |

---

## Reset-link URL

**`SCRUMBOY_PUBLIC_BASE_URL` is required for self-service password-reset emails.** Set it to a fixed public origin (e.g. `https://scrumboy.example.com`). The value must be an absolute `http` or `https` URL with a hostname, optional TCP port (1–65535), and **no** path (other than `/`), query, fragment, or userinfo. Invalid values are treated like unset.

When set to a valid origin, reset links use it for both the self-service email and the admin-generated link; the inbound request's `Host`/`X-Forwarded-Proto` headers are ignored.

If `SCRUMBOY_PUBLIC_BASE_URL` is **missing or invalid**, the self-service endpoint still returns the same generic success response, but **no email is sent**. The server logs at startup:

`smtp: SCRUMBOY_PUBLIC_BASE_URL is missing or invalid; self-service password-reset emails are disabled until a valid public origin is configured`

**Admin-generated reset links** (`POST /api/admin/users/{id}/password-reset`) do not require this variable. When unset, that authenticated owner-only endpoint still builds its link from the inbound request's `Host`/`X-Forwarded-Proto` (returned in JSON, not emailed). Behind a reverse proxy, ensure `Host` and `X-Forwarded-Proto` are forwarded correctly for that path, or set `SCRUMBOY_PUBLIC_BASE_URL` so admin links also use a fixed origin.

---

## Environment variables

| Variable | Required | Purpose |
|----------|----------|---------|
| `SCRUMBOY_SMTP_HOST` | Yes (with Port, From) | SMTP relay hostname. |
| `SCRUMBOY_SMTP_PORT` | No (default `587`) | SMTP relay port. |
| `SCRUMBOY_SMTP_USERNAME` | No | SMTP auth username. Omit for relays that allow unauthenticated submission from a trusted network. |
| `SCRUMBOY_SMTP_PASSWORD` | No | SMTP auth password. **Keep secret; never logged.** |
| `SCRUMBOY_SMTP_FROM` | Yes (with Host, Port) | Envelope + header `From` address, e.g. `Scrumboy <no-reply@example.com>`. |
| `SCRUMBOY_SMTP_TLS_MODE` | No (default `starttls`) | `starttls` \| `implicit` \| `none`. See TLS modes above. |
| `SCRUMBOY_SMTP_DEBUG` | No | Set to `1` to log send attempts (never logs credentials or message bodies). |
| `SCRUMBOY_PUBLIC_BASE_URL` | Yes (for self-service email) | Canonical public origin for reset links, e.g. `https://scrumboy.example.com`. Missing or invalid → self-service emails disabled (generic response only). See [Reset-link URL](#reset-link-url). |

Also required: `SCRUMBOY_ENCRYPTION_KEY` (see [`README.md`](../README.md#config)) — used to sign the reset token, same as the admin-generated reset link.

Scrumboy does **not** auto-load `.env` files inside the process. Your process manager, Compose, or Kubernetes must inject these into the running server. See `scrumboy.env.example` and `docker-compose.yml` at the repo root for the exact block to copy.

---

## Quick verification

1. Confirm env vars are visible to the running process (e.g. `docker exec scrumboy env | grep SCRUMBOY_SMTP`).
2. Check the startup log for `smtp: enabled (...)`.
3. For local testing, run a catcher like [Mailpit](https://github.com/axllent/mailpit): `docker run --rm -p 1025:1025 -p 8025:8025 axllent/mailpit`, set `SCRUMBOY_SMTP_HOST=127.0.0.1`, `SCRUMBOY_SMTP_PORT=1025`, `SCRUMBOY_SMTP_TLS_MODE=none`.
4. `curl -X POST http://localhost:8080/api/auth/request-password-reset -H 'Content-Type: application/json' -d '{"email":"you@example.com"}'` and check the catcher's UI (`http://localhost:8025` for Mailpit) for the delivered email.
5. Follow the link in the email (or POST its token to `/api/auth/reset-password`) to confirm the full reset loop works end to end.

---

## Related documentation

- [`docs/vapid.md`](vapid.md) — the parallel optional-feature model this design mirrors (config gate, startup log states, partial-config handling).
- [`FAQ.md`](../FAQ.md) — "Do I need SMTP?" entry.
- [`API.md`](../API.md) — request/response shape for `request-password-reset` and `reset-password`.
- [`README.md`](../README.md#config) — env variable table.
