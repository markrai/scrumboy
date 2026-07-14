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

**Set `SCRUMBOY_PUBLIC_BASE_URL`** (e.g. `https://scrumboy.example.com`, no trailing slash) to a fixed, trusted origin. When set, it is used verbatim for both the self-service reset link and the admin-generated one, and the inbound request's `Host`/`X-Forwarded-Proto` headers are ignored entirely.

If `SCRUMBOY_PUBLIC_BASE_URL` is **not** set, the reset link's scheme and host fall back to being derived from **the inbound request** (`X-Forwarded-Proto` header, falling back to whether the connection itself is TLS; and `Host`). This fallback exists for backward compatibility but is **not safe for the self-service flow**: `Host` and `X-Forwarded-Proto` on an unauthenticated request are attacker-controlled. An attacker can `POST /api/auth/request-password-reset` with a spoofed `Host` header and a real user's email address; the server still generates a valid reset token for that user and emails them a link built from the attacker's `Host`, i.e. password-reset-link poisoning. If the victim follows the link (or if the attacker-controlled page proxies the flow), the token can be captured and used to take over the account. Set `SCRUMBOY_PUBLIC_BASE_URL` before enabling SMTP self-service reset to close this off. The server logs a startup warning if SMTP is configured without it.

**Operational implication if you rely on the fallback anyway:** if Scrumboy sits behind a reverse proxy, that proxy must forward the correct `Host` header and set `X-Forwarded-Proto` accurately. If it doesn't (e.g. an SSL-terminating proxy that fails to set `X-Forwarded-Proto: https`), reset links will be generated with the wrong scheme or hostname even though the request itself arrived correctly — another reason to prefer `SCRUMBOY_PUBLIC_BASE_URL`.

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
| `SCRUMBOY_PUBLIC_BASE_URL` | Strongly recommended | Fixed origin for reset links, e.g. `https://scrumboy.example.com`. See [Reset-link URL](#reset-link-url) above. |

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
