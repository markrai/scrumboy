# SMTP and self-service password reset in Scrumboy

**SMTP** credentials are **optional server config** for self-service password-reset email delivery. The request route (`POST /api/auth/request-password-reset`) exists in full mode even when delivery settings are absent, but the sign-in control appears only when the required static settings are present. Without SMTP, password reset is still possible through the existing **admin-generated reset link** (Settings → Users → Password), which an admin must hand-deliver out of band.

This document explains what SMTP enables, how to configure it, how to verify it, and the HTTP contracts for the password-reset endpoints. [`API.md`](../API.md) documents the **MCP HTTP API only** — it does not cover these auth routes.

**Product scope:** Scrumboy ships the request-reset API, a capability-gated in-place **Forgot password?** step on local-password sign-in, outbound email, and the `/auth/reset-password` page (for links from email or an admin). The control is hidden during first-time setup, on OIDC-only or anonymous deployments, and whenever a required static setting is missing.

---

## Do I need this?

**Usually optional.** The admin-generated reset link (`POST /api/admin/users/{id}/password-reset`) always remains available regardless of SMTP configuration — it's unaffected by anything in this document. SMTP is purely a self-service convenience upgrade on top of that: it lets a user request their own reset link by email instead of an admin generating and sending one manually.

Scrumboy does **not** ship default SMTP credentials. Each self-hosted instance supplies its own relay.

---

## Minimum required env vars

Self-service password-reset email needs **all four** of these set and valid — miss any one and `selfServicePasswordResetEnabled` silently stays `false` (generic 200 response, no email sent, admin-generated link still works). There's no single "here's what's missing" error; check each one individually if the feature isn't turning on.

| Variable | What happens if it's missing/invalid |
|----------|---------------------------------------|
| `SCRUMBOY_SMTP_HOST` | Startup log shows `smtp: disabled` or `smtp: partial or invalid config ignored`. |
| `SCRUMBOY_SMTP_FROM` | Same as above; also must be a parseable RFC 5322 address (no CR/LF) or the capability stays `false` even with `SCRUMBOY_SMTP_HOST` set. |
| `SCRUMBOY_ENCRYPTION_KEY` | Reset tokens can't be signed; `selfServicePasswordResetEnabled` stays `false` even with SMTP fully configured. See [`README.md`](../README.md#config). |
| `SCRUMBOY_PUBLIC_BASE_URL` | Startup log shows `smtp: SCRUMBOY_PUBLIC_BASE_URL is missing or invalid...`; self-service emails disabled even with everything else set. See [Reset-link URL](#reset-link-url). |

`SCRUMBOY_SMTP_PORT` (default `587`), `SCRUMBOY_SMTP_USERNAME`/`PASSWORD`, and `SCRUMBOY_SMTP_TLS_MODE` (default `starttls`) round out the config — see the full [Environment variables](#environment-variables) table below for all of them at once.

---

## What SMTP enables

When **`SCRUMBOY_SMTP_HOST` and `SCRUMBOY_SMTP_FROM` are set** (`SCRUMBOY_SMTP_PORT` defaults to `587` when omitted; if explicitly set, it must be between 1 and 65535), `SCRUMBOY_ENCRYPTION_KEY` is configured, and `SCRUMBOY_PUBLIC_BASE_URL` is a valid public origin, Scrumboy:

1. Reports **`selfServicePasswordResetEnabled: true`** from `GET /api/auth/status`, allowing the SPA to show **Forgot password?** without probing mail endpoints.
2. Accepts **`POST /api/auth/request-password-reset`** with `{"email": "..."}` and emails a reset link when the address matches a registered account. The route itself remains available in full mode when delivery is unconfigured.
3. Always returns the same generic response (`"If that account exists, a password reset email has been sent."`) regardless of whether the email is registered, whether SMTP is configured, or whether the encryption key is set. **This is intentional** — a different status code or body per case would let an attacker enumerate registered accounts. A 200 response does **not** confirm an email was actually sent.
4. Delivers the email **asynchronously** via an in-memory queue with retry: up to 3 attempts with backoff for **transient** failures (dial errors, timeouts, SMTP 4xx) while the worker is running, mirroring the architecture already used for outbound webhooks. **Permanent** failures — SMTP 5xx replies and local validation/config errors (invalid `SCRUMBOY_SMTP_FROM`, CR/LF in headers, STARTTLS required but not advertised) — are logged once and not retried. Each SMTP send is bounded by a single timeout (default 10s) covering dial through quit; a stuck relay fails that attempt instead of hanging the worker indefinitely. On shutdown, `Server.Close(ctx)` seals the queue (no new entries) and links drain/retry work to `ctx`. Once a worker observes that close-context cancellation, it starts no further queued item or send attempt (an in-flight send may finish under its own transport timeout). `Close` also waits up to that deadline for the flush to finish. Some queued items may remain undelivered if the process exits before the queue drains.
5. Rate-limits requests to 5/minute per IP and 5/minute per submitted email, reusing the same dual-key limiter used elsewhere in auth. Per-IP keys use the connection `RemoteAddr` by default. Set `SCRUMBOY_TRUST_PROXY=1` only when a reverse proxy is the sole path to Scrumboy and overwrites/strips client `X-Forwarded-For`; then the first XFF hop is used for the IP key. Without that flag, clients cannot spoof the per-IP limiter via forged XFF headers.

If any required static setting is missing or invalid, `selfServicePasswordResetEnabled` is false and the SPA hides the control. The request endpoint still returns the same generic success response — but no email is sent, and the admin manual-reset endpoint remains the working fallback. At startup the server logs one of:

- `smtp: enabled (host=... port=...)`
- `smtp: disabled (set SCRUMBOY_SMTP_HOST and SCRUMBOY_SMTP_FROM to enable password-reset emails; SCRUMBOY_SMTP_PORT defaults to 587 when omitted)`
- `smtp: partial or invalid config ignored (set SCRUMBOY_SMTP_HOST and SCRUMBOY_SMTP_FROM; SCRUMBOY_SMTP_PORT defaults to 587 and, when set, must be between 1 and 65535)`

There is no anonymous-mode-specific log line: `request-password-reset` (like `reset-password`) already returns 404 in anonymous mode regardless of SMTP configuration, since anonymous mode has no authenticated accounts to reset.

---

## HTTP endpoints

These auth routes are **not** documented in [`API.md`](../API.md) (MCP-only). Shapes below reflect current server behavior.

### `GET /api/auth/status`

- **Capability:** `selfServicePasswordResetEnabled` is `true` only in full mode when the required SMTP host/from/port settings are present and valid, `SCRUMBOY_ENCRYPTION_KEY` is present, and `SCRUMBOY_PUBLIC_BASE_URL` is a valid normalized origin. `SCRUMBOY_SMTP_FROM` must be a parseable RFC 5322 address (no CR/LF); empty or malformed values keep the capability false.
- **Scope:** this is a static settings-readiness signal. It does not validate SMTP credentials, contact the relay, verify TLS support, or guarantee delivery.
- **Privacy:** this flag describes instance configuration only. It never reflects whether a submitted email belongs to an account.
- **UI:** the SPA also requires normal local-password sign-in (not bootstrap or OIDC-only) before showing **Forgot password?**. Anonymous mode reports the capability as `false`.

### `POST /api/auth/request-password-reset`

- **Body:** `{"email": "user@example.com"}`
- **Success:** always `200` with `{"message": "If that account exists, a password reset email has been sent."}` — identical whether the account exists, SMTP is configured, or `SCRUMBOY_PUBLIC_BASE_URL` is set. A 200 does **not** confirm an email was sent.
- **Other:** `404` in anonymous mode; `429` when rate-limited (5/min per IP and per email).
- **Sends email only when:** user exists, SMTP host/from/port settings are present and valid (including a parseable `SCRUMBOY_SMTP_FROM`), `SCRUMBOY_ENCRYPTION_KEY` set, valid `SCRUMBOY_PUBLIC_BASE_URL` set.

### `POST /api/auth/reset-password`

- **Body:** `{"token": "...", "new_password": "..."}` (token from the reset link query string)
- **Success:** `200` with empty body; existing sessions for that user are cleared.
- **Other:** `400` invalid/expired token; `404` in anonymous mode; `429` rate-limited; `503` if encryption key not configured.
- **SPA:** users can also complete reset at `/auth/reset-password?token=...` (same API under the hood).

### `POST /api/admin/users/{id}/password-reset`

- **Auth:** owner session required.
- **Response:** JSON with a reset URL (not emailed). Unaffected by SMTP. When `SCRUMBOY_PUBLIC_BASE_URL` is unset, the link uses the request's `Host`/`X-Forwarded-Proto`.

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
| `SCRUMBOY_SMTP_HOST` | Yes | SMTP relay hostname. |
| `SCRUMBOY_SMTP_PORT` | No (default `587`) | SMTP relay port. If explicitly set, must be between 1 and 65535; invalid values disable SMTP. |
| `SCRUMBOY_SMTP_USERNAME` | No | SMTP auth username. Omit for relays that allow unauthenticated submission from a trusted network. |
| `SCRUMBOY_SMTP_PASSWORD` | No | SMTP auth password. **Keep secret; never logged.** |
| `SCRUMBOY_SMTP_FROM` | Yes | Envelope + header `From` address, e.g. `Scrumboy <no-reply@example.com>`. |
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
4. After at least one user exists, sign out from a local-password deployment. Confirm **Forgot password?** is visible, submit the user's email, and check the catcher's UI (`http://localhost:8025` for Mailpit) for the delivered email.
5. Optionally verify the low-level contract with `curl -X POST http://localhost:8080/api/auth/request-password-reset -H 'Content-Type: application/json' -H 'X-Scrumboy: 1' -d '{"email":"you@example.com"}'`. The response remains generic. Mutating auth JSON routes require the custom header (same as login).
6. Follow the link in the email (or POST its token to `/api/auth/reset-password`) to confirm the full reset loop works end to end.

---

## Example SMTP providers (optional)

Scrumboy only needs a normal SMTP relay for infrequent password-reset mail. It does **not** require a marketing ESP, an HTTP send API, or high monthly volume. Any relay that gives you host/port/username/password (and TLS) works with the env vars above.

The table below lists a few public providers that expose SMTP and publish a free tier suitable for small, self-hosted instances. Figures are approximate and change; confirm on each provider’s pricing page before relying on them. This is not an endorsement or affiliation. *(Free-tier figures last checked: 2026-07-14.)*

| Provider | Free tier (approx.) | Notes |
|----------|--------------------:|-------|
| [SMTP2GO](https://www.smtp2go.com/) | 1,000/month (also ~200/day) | SMTP-focused; a solid default when you want classic relay credentials |
| [Resend](https://resend.com/) | 3,000/month (also ~100/day) | Strong developer tooling; SMTP at `smtp.resend.com` (API key as password) |
| [Brevo](https://www.brevo.com/) | 300/day | Long-standing service; free plan is daily-capped |
| [Mailjet](https://www.mailjet.com/) | 200/day (~6,000/month) | Another SMTP-capable option with a free forever plan |

For Scrumboy’s password-reset use case, daily caps are rarely the bottleneck; domain/sender verification and correct `SCRUMBOY_SMTP_FROM` / TLS mode matter more. Local catchers such as [Mailpit](https://github.com/axllent/mailpit) remain the recommended path for development (see [Quick verification](#quick-verification)).

### API-key providers: username is a literal, not your account login

Several providers above (Resend included) authenticate SMTP with an **API key**, not a normal username/password pair. In that case:

- `SCRUMBOY_SMTP_PASSWORD` = the API key.
- `SCRUMBOY_SMTP_USERNAME` = whatever literal string the provider's SMTP docs say to use (often the provider's own name, e.g. `resend` for Resend) — **not** your account email or login.

Check the provider's SMTP-specific docs for the exact required username; it's easy to assume it should be your account email and get a rejected auth instead.

### Tested providers

Confirmed working end-to-end against a live Scrumboy instance (bootstrap → request-password-reset → email received → reset-password consumed):

- **Resend** (`smtp.resend.com:587`, `starttls`, `SCRUMBOY_SMTP_USERNAME=resend`) — full round trip confirmed on two separate runs against a real inbox. One gotcha: Resend's newer **domain-restricted** API keys (scoped to one verified domain in their dashboard) are rejected by the SMTP relay with `535 Authentication credentials invalid`. Only an **unrestricted** (account-wide) sending key works over SMTP — if auth fails with a key that works fine via Resend's HTTP API, this is almost certainly why.
- **SMTP2Go** — confirmed working with a normal username/password pair (not an API key).

---

## Related documentation

- [`docs/vapid.md`](vapid.md) — the parallel optional-feature model this design mirrors (config gate, startup log states, partial-config handling).
- [`FAQ.md`](../FAQ.md) — "Do I need SMTP?" entry.
- [`README.md`](../README.md#config) — env variable table.
