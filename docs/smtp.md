# SMTP and self-service password reset in Scrumboy

SMTP is optional. Without it, administrators can always generate password reset links manually. Configuring SMTP additionally enables self-service password reset via email.

## Contents

- [Required env vars](#required-env-vars)
  - [Also configure for real delivery](#also-configure-for-real-delivery)
  - [Quoting](#quoting-scrumboy_smtp_from) `SCRUMBOY_SMTP_FROM`
- [What SMTP enables](#what-smtp-enables)
- [HTTP endpoints](#http-endpoints)
- [TLS modes](#tls-modes)
- [Reset-link URL](#reset-link-url)
- [Quick verification](#quick-verification)
- [Example SMTP providers (optional)](#example-smtp-providers-optional)
  - [API-key providers: username is a literal, not your account login](#api-key-providers-username-is-a-literal-not-your-account-login)
  - [Tested providers](#tested-providers)
- [Related documentation](#related-documentation)

---



## Required env vars

Self-service password-reset email is gated by **four** environment variables. Miss any one and `selfServicePasswordResetEnabled` silently stays `false` (generic 200 response, no email sent, admin-generated link still works). There's no single "here's what's missing" error; check each one if the **Forgot password?** control never appears.

Those four turn the feature **on**. They do **not** guarantee delivery: hosted relays almost always also need SMTP auth (and often a verified sending domain). Scrumboy's readiness check does not dial the relay or validate credentials.


| Variable                   | What happens if it's missing/invalid                                                                                                                                                                                                                                                                                                                                                                                                           |
| -------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `SCRUMBOY_SMTP_HOST`       | Startup log shows `smtp: disabled` or `smtp: partial or invalid config ignored`.                                                                                                                                                                                                                                                                                                                                                               |
| `SCRUMBOY_ENCRYPTION_KEY`  | Reset tokens can't be signed; `selfServicePasswordResetEnabled` stays `false` even with SMTP fully configured. Generate on Linux/Windows: [FAQ](../FAQ.md#how-do-i-generate-scrumboy_encryption_key). Also `[README.md](../README.md#encryption-key-optional)`.                                                                                                                                                                                |
| `SCRUMBOY_PUBLIC_BASE_URL` | Startup log shows `smtp: SCRUMBOY_PUBLIC_BASE_URL is missing or invalid...`; self-service emails disabled even with everything else set. See [Reset-link URL](#reset-link-url).                                                                                                                                                                                                                                                                |
| `SCRUMBOY_SMTP_FROM`       | Same as host missing: `smtp: disabled` or `smtp: partial or invalid config ignored`. Must be a parseable RFC 5322 address (no CR/LF) or the capability stays `false` even with `SCRUMBOY_SMTP_HOST` set. Bare address (`no-reply@example.com`) or display name (`Scrumboy <no-reply@example.com>`). Display-name forms need careful [quoting](#quoting-scrumboy_smtp_from) in shells, Compose, and `.env` files because of the angle brackets. |




### Also configure for real delivery

These are optional in the sense that the server can mark capability `true` without them - but a real mailbox usually will not receive mail unless you set them correctly for your relay:


| Variable                 | Typical need                                                                                                                                                                                                                                                                                                                     |
| ------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `SCRUMBOY_SMTP_USERNAME` | Required by almost all hosted SMTP providers. Omit only for trusted-network or local catchers that allow unauthenticated submission (e.g. Mailpit). For API-key SMTP, this is a provider **literal** (e.g. `resend`), not your login - see [API-key providers](#api-key-providers-username-is-a-literal-not-your-account-login). |
| `SCRUMBOY_SMTP_PASSWORD` | Pair with username (or API key as password for providers like Resend). Readiness does not verify login; wrong credentials → UI may show, sends fail in logs.                                                                                                                                                                     |
| `SCRUMBOY_SMTP_PORT`     | Defaults to `587`. Set explicitly when your relay uses another port (e.g. `465` with `implicit` TLS, or `1025` for Mailpit). If set, must be 1-65535; invalid values disable SMTP.                                                                                                                                               |
| `SCRUMBOY_SMTP_TLS_MODE` | Defaults to `starttls`. Use `implicit` for port 465, `none` only for local/dev catchers. See [TLS modes](#tls-modes).                                                                                                                                                                                                            |
| `SCRUMBOY_SMTP_DEBUG`    | Optional: set to `1` while testing to log send attempts (never credentials or bodies).                                                                                                                                                                                                                                           |


Scrumboy does **not** auto-load `.env` files inside the process. Your process manager, Compose, or Kubernetes must inject these into the running server. See `scrumboy.env.example` and `docker-compose.yml` at the repo root for the exact block to copy.

### Quoting `SCRUMBOY_SMTP_FROM`

Display-name forms use angle brackets (`Scrumboy <no-reply@example.com>`). Shells, PowerShell, and Compose YAML often treat `<` / `>` as redirection or special characters, so the value that reaches Scrumboy can be truncated or rejected as invalid From.

**Where quoting is not required**

- **Portainer container UI** - env vars entered as separate name/value fields. Paste `Scrumboy <no-reply@example.com>` in the value box as-is (no surrounding quotes).

**Where quoting is required** (including PowerShell, Compose, and Portainer stacks)

- **Portainer stack editor** / any Compose YAML - quote the value (same as `docker-compose.yml` below).
- **bash / zsh**, **PowerShell**, **Windows cmd**, and **`.env` files** consumed by Compose.

**Safe patterns:**

```bash
# Bare address - no quoting needed
export SCRUMBOY_SMTP_FROM=no-reply@example.com

# Display name - quote so the shell keeps <address>
export SCRUMBOY_SMTP_FROM='Scrumboy <no-reply@example.com>'
```

```powershell
# PowerShell - quote the value (angle brackets are special unquoted)
$env:SCRUMBOY_SMTP_FROM = 'Scrumboy <no-reply@example.com>'
```

```yaml
# docker-compose.yml or Portainer stack editor - quote the whole value
environment:
  SCRUMBOY_SMTP_FROM: "Scrumboy <no-reply@example.com>"
```

```bash
# .env file consumed by Compose - quote the value
SCRUMBOY_SMTP_FROM="Scrumboy <no-reply@example.com>"
```

```bat
REM Windows cmd - quote the entire NAME=value assignment
set "SCRUMBOY_SMTP_FROM=Scrumboy <no-reply@example.com>"
```

Do **not** leave literal quote characters *inside* the address Scrumboy parses (e.g. avoid a value that is `"Scrumboy <…>"` including the `"` characters). Aim for the process env to contain exactly `Scrumboy <no-reply@example.com>` or a bare email. If capability stays off with host set, check startup for `smtp: partial or invalid config ignored` and inspect the From value the container actually received (`docker compose exec … env`).

---



## What SMTP enables

When the [required env vars](#required-env-vars) are set, Scrumboy can show **Forgot password?** and deliver reset mail. Route shapes, status codes, and the enumeration-safe generic `200` live under [HTTP endpoints](#http-endpoints). This section is only the delivery and ops behavior that those contracts do not cover.

**Delivery.** Accepted reset requests enqueue mail on an in-memory queue (same pattern as outbound webhooks). Transient failures (dial errors, timeouts, SMTP 4xx) retry up to 3 times with backoff while the worker is running. Permanent failures (SMTP 5xx and local validation/config errors such as invalid `SCRUMBOY_SMTP_FROM`, CR/LF in headers, or STARTTLS required but not advertised) are logged once and not retried. Each send has a single timeout (default 10s) from dial through quit. On shutdown, `Server.Close(ctx)` seals the queue and drains against `ctx`; in-flight sends may finish under their own timeout, and some queued items may remain undelivered if the process exits first.

**Rate limits.** `POST /api/auth/request-password-reset` is limited to 5/minute per IP and 5/minute per submitted email. Per-IP keys use `RemoteAddr` by default. Set `SCRUMBOY_TRUST_PROXY=1` only when a reverse proxy is the sole path to Scrumboy and overwrites/strips client `X-Forwarded-For`; then the first XFF hop is used. Without that flag, clients cannot spoof the per-IP limiter via forged XFF.

**Startup logs.** The server logs one of:

- `smtp: enabled (host=... port=...)`
- `smtp: disabled (set SCRUMBOY_SMTP_HOST and SCRUMBOY_SMTP_FROM to enable password-reset emails; SCRUMBOY_SMTP_PORT defaults to 587 when omitted)`
- `smtp: partial or invalid config ignored (set SCRUMBOY_SMTP_HOST and SCRUMBOY_SMTP_FROM; SCRUMBOY_SMTP_PORT defaults to 587 and, when set, must be between 1 and 65535)`

`smtp: enabled` means host/from/port look valid for the mailer - not that Forgot password is fully ready. You still need a valid encryption key and `SCRUMBOY_PUBLIC_BASE_URL`; confirm with `selfServicePasswordResetEnabled` on `GET /api/auth/status`. If that base URL is missing or invalid, a separate startup line says so (see [Reset-link URL](#reset-link-url)).

---



## HTTP endpoints

These auth routes are **not** documented in `[API.md](../API.md)` (MCP-only). Shapes below reflect current server behavior.

### `GET /api/auth/status`

- **Capability:** `selfServicePasswordResetEnabled` is `true` only in full mode when the required SMTP host/from/port settings are present and valid, `SCRUMBOY_ENCRYPTION_KEY` is present, and `SCRUMBOY_PUBLIC_BASE_URL` is a valid normalized origin. `SCRUMBOY_SMTP_FROM` must be a parseable RFC 5322 address (no CR/LF); empty or malformed values keep the capability false.
- **Scope:** this is a static settings-readiness signal. It does not validate SMTP credentials, contact the relay, verify TLS support, or guarantee delivery.
- **Privacy:** this flag describes instance configuration only. It never reflects whether a submitted email belongs to an account.
- **UI:** the SPA also requires normal local-password sign-in (not bootstrap or OIDC-only) before showing **Forgot password?**. Anonymous mode reports the capability as `false`.



### `POST /api/auth/request-password-reset`

- **Body:** `{"email": "user@example.com"}`
- **Success:** always `200` with `{"message": "If that account exists, a password reset email has been sent."}` - identical whether the account exists, SMTP is configured, or `SCRUMBOY_PUBLIC_BASE_URL` is set. A 200 does **not** confirm an email was sent.
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

Set `SCRUMBOY_SMTP_TLS_MODE` explicitly - it is never inferred from the port number:


| Mode                 | Typical port | Behavior                                                                                                                                                                                         |
| -------------------- | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `starttls` (default) | 587          | Connects in plaintext, then upgrades via STARTTLS before authenticating or sending. Fails closed if the server doesn't advertise STARTTLS support - it will not silently fall back to plaintext. |
| `implicit`           | 465          | The entire connection is TLS from the first byte (no STARTTLS negotiation).                                                                                                                      |
| `none`               | -            | Plaintext throughout. Only appropriate for local/dev catchers (e.g. MailHog/Mailpit) on a trusted network - never use against a real relay over the public internet.                             |


---



## Reset-link URL

`SCRUMBOY_PUBLIC_BASE_URL` **is required for self-service password-reset emails.** Set it to a fixed public origin (e.g. `https://scrumboy.example.com`). The value must be an absolute `http` or `https` URL with a hostname, optional TCP port (1-65535), and **no** path (other than `/`), query, fragment, or userinfo. Invalid values are treated like unset.

When set to a valid origin, reset links use it for both the self-service email and the admin-generated link; the inbound request's `Host`/`X-Forwarded-Proto` headers are ignored.

If `SCRUMBOY_PUBLIC_BASE_URL` is **missing or invalid**, the self-service endpoint still returns the same generic success response, but **no email is sent**. The server logs at startup:

`smtp: SCRUMBOY_PUBLIC_BASE_URL is missing or invalid; self-service password-reset emails are disabled until a valid public origin is configured`

**Admin-generated reset links** (`POST /api/admin/users/{id}/password-reset`) do not require this variable. When unset, that authenticated owner-only endpoint still builds its link from the inbound request's `Host`/`X-Forwarded-Proto` (returned in JSON, not emailed). Behind a reverse proxy, ensure `Host` and `X-Forwarded-Proto` are forwarded correctly for that path, or set `SCRUMBOY_PUBLIC_BASE_URL` so admin links also use a fixed origin.

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


| Provider                            | Free tier (approx.)         | Notes                                                                     |
| ----------------------------------- | --------------------------- | ------------------------------------------------------------------------- |
| [SMTP2GO](https://www.smtp2go.com/) | 1,000/month (also ~200/day) | SMTP-focused; a solid default when you want classic relay credentials     |
| [Resend](https://resend.com/)       | 3,000/month (also ~100/day) | Strong developer tooling; SMTP at `smtp.resend.com` (API key as password) |
| [Brevo](https://www.brevo.com/)     | 300/day                     | Long-standing service; free plan is daily-capped                          |
| [Mailjet](https://www.mailjet.com/) | 200/day (~6,000/month)      | Another SMTP-capable option with a free forever plan                      |


For Scrumboy’s password-reset use case, daily caps are rarely the bottleneck; domain/sender verification and correct `SCRUMBOY_SMTP_FROM` / TLS mode matter more. Local catchers such as [Mailpit](https://github.com/axllent/mailpit) remain the recommended path for development (see [Quick verification](#quick-verification)).

### API-key providers: username is a literal, not your account login

Several providers above (Resend included) authenticate SMTP with an **API key**, not a normal username/password pair. In that case:

- `SCRUMBOY_SMTP_PASSWORD` = the API key.
- `SCRUMBOY_SMTP_USERNAME` = whatever literal string the provider's SMTP docs say to use (often the provider's own name, e.g. `resend` for Resend) - **not** your account email or login.

Check the provider's SMTP-specific docs for the exact required username; it's easy to assume it should be your account email and get a rejected auth instead.

### Tested providers

Confirmed working end-to-end against a live Scrumboy instance (bootstrap → request-password-reset → email received → reset-password consumed) as of July 15th 2026:

- **Resend** (`smtp.resend.com:587`, `starttls`, `SCRUMBOY_SMTP_USERNAME=resend`) - full round trip confirmed on two separate runs against a real inbox. One gotcha: Resend's newer **domain-restricted** API keys (scoped to one verified domain in their dashboard) are rejected by the SMTP relay with `535 Authentication credentials invalid`. Only an **unrestricted** (account-wide) sending key works over SMTP - if auth fails with a key that works fine via Resend's HTTP API, this is almost certainly why.
- **SMTP2Go** - confirmed working with a normal username/password pair (not an API key).

---



## Related documentation

- `[docs/vapid.md](vapid.md)` - the parallel optional-feature model this design mirrors (config gate, startup log states, partial-config handling).
- `[FAQ.md](../FAQ.md)` - [Do I need to configure SMTP? What happens if I don't?](../FAQ.md#do-i-need-to-configure-smtp-what-happens-if-i-dont); [I configured SMTP - why don't I see Forgot password?](../FAQ.md#i-configured-smtp---why-dont-i-see-forgot-password).
- `[README.md](../README.md#config)` - env variable table.

