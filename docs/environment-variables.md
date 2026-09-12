# Scrumboy environment variables

Scrumboy works out of the box without any environment variables.

Use environment variables only when you want to change the default server or storage settings, or enable optional features such as SSO, email, Web Push, TLS, or encrypted calendar and authentication data.

This page is the complete reference for supported Scrumboy environment variables.

> **Note:** Scrumboy does not automatically load `.env` files. Environment variables must be supplied by your shell, service manager, Docker/Compose, or another launcher.

Some Scrumboy helper scripts may set variables for you. For example, the Windows launchers can manage `SCRUMBOY_ENCRYPTION_KEY` through `data/scrumboy.env`.

Values shown below are Scrumboy's built-in defaults unless otherwise noted. Docker, Compose, or launcher scripts may override them for a particular deployment.

Some variables are only required when the feature that uses them is enabled.

---

## Quick reference

Defaults below are Scrumboy's built-in defaults unless otherwise noted. Docker/Compose or helper tooling may override some values for a particular environment. Details for each category follow later in this document.

### Server and data


| Variable                  | Default                    | Required? | Purpose                                                                            |
| ------------------------- | -------------------------- | --------- | ---------------------------------------------------------------------------------- |
| `BIND_ADDR`               | `:8080`                    | Optional  | HTTP(S) listen address                                                             |
| `DATA_DIR`                | `./data`                   | Optional  | Instance data directory (SQLite + file-backed uploads) when `SQLITE_PATH` is unset |
| `SQLITE_PATH`             | empty → `$DATA_DIR/app.db` | Optional  | Full path to the SQLite database file                                              |
| `SQLITE_BUSY_TIMEOUT_MS`  | `30000`                    | Optional  | SQLite busy timeout (milliseconds)                                                 |
| `SQLITE_JOURNAL_MODE`     | `WAL`                      | Optional  | SQLite journal mode                                                                |
| `SQLITE_SYNCHRONOUS`      | `FULL`                     | Optional  | SQLite synchronous setting                                                         |
| `MAX_REQUEST_BODY_BYTES`  | `1048576` (1 MiB)          | Optional  | Max request body size for ordinary API requests                                    |
| `MAX_TRELLO_IMPORT_BYTES` | `33554432` (32 MiB)        | Optional  | Max Trello JSON import upload size                                                 |
| `SCRUMBOY_MODE`           | `full`                     | Optional  | `full` (auth-capable) or `anonymous`                                               |
| `SCRUMBOY_INTRANET_IP`    | `192.168.1.250`            | Optional  | LAN IP printed in startup logs for intranet access hints                           |


### Encryption and TLS


| Variable                  | Default      | Required?              | Purpose                                                                    |
| ------------------------- | ------------ | ---------------------- | -------------------------------------------------------------------------- |
| `SCRUMBOY_ENCRYPTION_KEY` | empty        | Conditionally required | Base64 32-byte key for 2FA, password-reset tokens, ICS feed URL encryption |
| `SCRUMBOY_TLS_CERT`       | `./cert.pem` | Optional               | TLS certificate path (HTTPS only if cert **and** key files exist)          |
| `SCRUMBOY_TLS_KEY`        | `./key.pem`  | Optional               | TLS private key path                                                       |


### OIDC / SSO


| Variable                            | Default                                       | Required?                     | Purpose                                                                         |
| ----------------------------------- | --------------------------------------------- | ----------------------------- | ------------------------------------------------------------------------------- |
| `SCRUMBOY_OIDC_ISSUER`              | empty                                         | Conditionally required (OIDC) | OIDC issuer URL                                                                 |
| `SCRUMBOY_OIDC_CLIENT_ID`           | empty                                         | Conditionally required (OIDC) | OIDC client ID                                                                  |
| `SCRUMBOY_OIDC_CLIENT_SECRET`       | empty                                         | Conditionally required (OIDC) | OIDC client secret                                                              |
| `SCRUMBOY_OIDC_REDIRECT_URL`        | empty                                         | Conditionally required (OIDC) | Absolute OIDC callback URL                                                      |
| `SCRUMBOY_OIDC_LOCAL_AUTH_DISABLED` | empty (local auth stays on when OIDC enabled) | Optional                      | Set to `true` to disable local password login/bootstrap when OIDC is configured |


### Wall (Sticky Note), Markdown, & Mermaid


| Variable                          | Default          | Required? | Purpose                                                        |
| --------------------------------- | ---------------- | --------- | -------------------------------------------------------------- |
| `SCRUMBOY_WALL_ENABLED`           | on (unset/empty) | Optional  | Sticky-note wall; opt out with `0`/`false`/`off`/`no`          |
| `SCRUMBOY_MARKDOWN_NOTES_ENABLED` | off              | Optional  | Todo notes Markdown preview; opt in with `1`/`true`/`on`/`yes` |
| `SCRUMBOY_MERMAID_NOTES_ENABLED`  | off              | Optional  | Mermaid in notes preview; requires Markdown notes enabled      |


### Web Push (VAPID)


| Variable                     | Default                                      | Required?                         | Purpose                             |
| ---------------------------- | -------------------------------------------- | --------------------------------- | ----------------------------------- |
| `SCRUMBOY_VAPID_PUBLIC_KEY`  | empty                                        | Conditionally required (Web Push) | VAPID public key (URL-safe base64)  |
| `SCRUMBOY_VAPID_PRIVATE_KEY` | empty                                        | Conditionally required (Web Push) | VAPID private key (URL-safe base64) |
| `SCRUMBOY_VAPID_SUBSCRIBER`  | empty → runtime default `scrumboy@localhost` | Optional                          | VAPID JWT `sub` contact             |
| `SCRUMBOY_DEBUG_PUSH`        | off                                          | Optional                          | Set to `1` to log push send/prune   |


### SMTP


| Variable                 | Default          | Required?                     | Purpose                                         |
| ------------------------ | ---------------- | ----------------------------- | ----------------------------------------------- |
| `SCRUMBOY_SMTP_HOST`     | empty            | Conditionally required (SMTP) | SMTP relay hostname                             |
| `SCRUMBOY_SMTP_PORT`     | `587` when unset | Optional                      | SMTP port; explicit invalid values disable SMTP |
| `SCRUMBOY_SMTP_USERNAME` | empty            | Optional                      | SMTP auth username                              |
| `SCRUMBOY_SMTP_PASSWORD` | empty            | Optional                      | SMTP auth password (never logged)               |
| `SCRUMBOY_SMTP_FROM`     | empty            | Conditionally required (SMTP) | Envelope/header From address                    |
| `SCRUMBOY_SMTP_TLS_MODE` | `starttls`       | Optional                      | `starttls`, `implicit`, or `none`               |
| `SCRUMBOY_SMTP_DEBUG`    | off              | Optional                      | Set to `1` to log SMTP send attempts            |


### Public URL and reverse proxy


| Variable                   | Default | Required?              | Purpose                                                                      |
| -------------------------- | ------- | ---------------------- | ---------------------------------------------------------------------------- |
| `SCRUMBOY_PUBLIC_BASE_URL` | empty   | Conditionally required | Canonical public origin for reset links, email notify links, OAuth discovery |
| `SCRUMBOY_TRUST_PROXY`     | off     | Optional               | Honor trusted forwarded headers for rate-limit IP / OAuth discovery          |


### Development / build helpers


| Variable           | Default                          | Required? | Purpose                                                                        |
| ------------------ | -------------------------------- | --------- | ------------------------------------------------------------------------------ |
| `SCRUMBOY_WEB_DIR` | unset → script-relative web tree | Optional  | Override web asset root for frontend build/verify scripts (not server runtime) |


---

## Server / networking

### `BIND_ADDR`

- **Default:** `:8080`
- **Component:** `cmd/scrumboy` via `config.FromEnv`
- **Purpose:** Address passed to the HTTP server listener.
- **Docker / Compose:** image and stock Compose set `BIND_ADDR=:8080` (same as the application default).

### `SCRUMBOY_INTRANET_IP`

- **Default:** `192.168.1.250`
- **Purpose:** Value used only in startup log lines that suggest an intranet URL and mkcert subject hints. It does not bind the listener by itself.

### `SCRUMBOY_TLS_CERT` / `SCRUMBOY_TLS_KEY`

- **Defaults:** `./cert.pem` / `./key.pem`
- **Behavior:** HTTPS is enabled only when **both** paths are non-empty **and both files exist** on disk. Otherwise the server listens in HTTP mode and logs how to enable HTTPS.
- **Security:** Prefer terminating TLS at a reverse proxy in production; app-level TLS is optional.

### `SCRUMBOY_PUBLIC_BASE_URL`

- **Default:** empty (invalid or empty → treated as unset)
- **Normalization:** absolute `http` or `https` origin with a hostname; optional port `1..65535`; no userinfo; path only empty or `/`; no query or fragment. Scheme is lowercased. See `config.NormalizeBaseURL`.
- **Conditionally required for:**
  - Self-service password-reset **email** delivery (with SMTP + encryption key + local auth)
  - Email notification board links (with SMTP)
  - Preferred OAuth discovery issuer origin when set
- **OAuth note:** non-loopback OAuth issuers must be **HTTPS**; plain `http` is accepted for OAuth only for loopback (`localhost` / `127.0.0.0/8` / `::1`). Password-reset link construction may still accept non-loopback `http` via the same variable. Details: [oauth.md](oauth.md#issuer--discovery-origin), [smtp.md](smtp.md#reset-link-url).

### `SCRUMBOY_TRUST_PROXY`

- **Default:** off
- **Truthy values:** `1`, `true`, `on`, `yes` (trimmed, case-insensitive)
- **When true:** auth/OAuth rate-limit IP keys may honor `X-Forwarded-For` (first hop); OAuth issuer discovery may use trusted `X-Forwarded-Proto` / `X-Forwarded-Host` / `CF-Visitor` when `SCRUMBOY_PUBLIC_BASE_URL` is unset.
- **Security:** enable only when a reverse proxy is the sole client path and **overwrites or strips** client-supplied values for those headers. Without a valid public base URL, discovery requires forwarded HTTPS plus an explicit forwarded host; weak/multi-value forwarded fields fail closed (`503`). See [oauth.md](oauth.md).

**Docker / Compose:** stock `docker-compose.yml` may pass through `SCRUMBOY_PUBLIC_BASE_URL` and `SCRUMBOY_TRUST_PROXY` from the host environment (empty if unset).

---

## Data / SQLite

### `DATA_DIR` / `SQLITE_PATH`

Resolved by `config.ResolveDataDir` (called from `FromEnv` with no override):


| Situation           | Data directory              | Database path      |
| ------------------- | --------------------------- | ------------------ |
| Neither set         | `./data`                    | `./data/app.db`    |
| Only `DATA_DIR` set | that directory              | `$DATA_DIR/app.db` |
| `SQLITE_PATH` set   | `filepath.Dir(SQLITE_PATH)` | `SQLITE_PATH`      |


When `SQLITE_PATH` is set, `DATA_DIR` **is not read** for this resolution; the data directory becomes the parent of the SQLite file. That directory must be creatable and writable (startup creates it and probes write access).

`DATA_DIR` holds SQLite **and** file-backed uploads such as `user-wallpapers/`. Back up the whole directory (including WAL/SHM sidecars when present). See [diagrams/scrumboy_deployment_ops.md](diagrams/scrumboy_deployment_ops.md).

**Docker image defaults** (`Dockerfile` `ENV`, not bare-process defaults):


| Variable                 | Docker image value |
| ------------------------ | ------------------ |
| `DATA_DIR`               | `/data`            |
| `SQLITE_PATH`            | `/data/app.db`     |
| `SQLITE_BUSY_TIMEOUT_MS` | `5000`             |
| `SQLITE_JOURNAL_MODE`    | `WAL`              |
| `SQLITE_SYNCHRONOUS`     | `FULL`             |
| `BIND_ADDR`              | `:8080`            |


Stock `docker-compose.yml` repeats those SQLite/data settings and also sets `MAX_REQUEST_BODY_BYTES=1048576`.

### `SQLITE_BUSY_TIMEOUT_MS`

- **Application default:** `30000`
- **Docker / Compose override:** `5000`
- Invalid or empty integer parsing falls back to the application default via `getenvInt`.

### `SQLITE_JOURNAL_MODE` / `SQLITE_SYNCHRONOUS`

- **Defaults:** `WAL` / `FULL`
- Passed through to SQLite open options. Empty env values fall back to those defaults.

### `MAX_REQUEST_BODY_BYTES` / `MAX_TRELLO_IMPORT_BYTES`

- **Defaults:** `1048576` / `33554432`
- Invalid integer strings fall back to the defaults.

---

## Application mode

### `SCRUMBOY_MODE`

- **Default:** `full`
- **Accepted:** `full` or `anonymous`. Any other non-empty value is coerced to `full`.
- `full`**:** authentication, OIDC, OAuth, Web Push, and related operator features may be enabled.
- `anonymous`**:** no auth; OIDC/OAuth/push stay unavailable. See [mcp.md](mcp.md) for mode-specific MCP behavior.
- **Windows helpers:** `win_run_full.bat` sets `SCRUMBOY_MODE=full`; `win_run_anonymous.bat` sets `SCRUMBOY_MODE=anonymous`.

---

## Encryption / security

### `SCRUMBOY_ENCRYPTION_KEY`

- **Default:** empty
- **Format:** standard Base64 encoding of **32 bytes** (e.g. `openssl rand -base64 32`). Leading/trailing whitespace is trimmed on load.
- **Used for:** TOTP secret encryption (2FA), password-reset token material, encrypted ICS calendar feed URLs (Agenda).
- **Not required** for basic startup on a database with no encrypted security/calendar data.
- **Conditionally required at startup** when the database already contains encrypted auth/security or calendar data: missing or invalid key → process exits. On a fresh DB with no such data, an invalid key is ignored with a warning; 2FA setup, password-reset encryption, and calendar URL encryption stay off until a valid key is configured.
- **Persistence:** treat the key as part of the same backup unit as `DATA_DIR` / `app.db`. Do not rotate casually after encrypted data exists.
- **Windows launcher precedence** (`scripts/resolve_scrumboy_encryption_key.ps1`, used by `win_run_*.bat`):
  1. Existing process environment `SCRUMBOY_ENCRYPTION_KEY`
  2. `data/scrumboy.env` (canonical line: `SCRUMBOY_ENCRYPTION_KEY=<base64…>`)
  3. Legacy repo-root `scrumboy.env` (assignment form or legacy raw single-line key)
  4. Otherwise generate and write `data/scrumboy.env`
- The server binary itself still only reads the process environment; the helpers inject the value before start.
- Related: [calendar.md](calendar.md), [smtp.md](smtp.md), FAQ encryption-key section.

---

## Authentication / OIDC / SSO

OIDC is enabled only when **all four** of these are non-empty after trim (issuer also normalized by stripping a trailing `/`):

- `SCRUMBOY_OIDC_ISSUER`
- `SCRUMBOY_OIDC_CLIENT_ID`
- `SCRUMBOY_OIDC_CLIENT_SECRET`
- `SCRUMBOY_OIDC_REDIRECT_URL` (absolute callback URL registered at the IdP)

See [oidc.md](oidc.md).

### `SCRUMBOY_OIDC_LOCAL_AUTH_DISABLED`

- **Default:** empty / not `true` → local password login and bootstrap remain available when OIDC is enabled.
- **Enable disablement:** value must equal `true` after trim + lowercasing. Unlike feature flags, `1` / `yes` / `on` are **not** accepted.
- When true **and** OIDC is configured, local password login/bootstrap and related local-only reset surfaces stay unavailable.

---

## Feature flags

Boolean parsing differs by flag. Values are trimmed and compared case-insensitively unless noted.


| Variable                          | Default | How to change                                                                                                                                                  |
| --------------------------------- | ------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `SCRUMBOY_WALL_ENABLED`           | **on**  | Disable with `0`, `false`, `off`, or `no`. Unset/empty/other → enabled. Durable projects only; anonymous/temp boards never expose the wall. [wall.md](wall.md) |
| `SCRUMBOY_MARKDOWN_NOTES_ENABLED` | **off** | Enable with `1`, `true`, `on`, or `yes`. [markdown-and-mermaid.md](markdown-and-mermaid.md)                                                                    |
| `SCRUMBOY_MERMAID_NOTES_ENABLED`  | **off** | Same truthy set as Markdown; **ignored unless** Markdown notes are already enabled.                                                                            |


---

## Web Push / VAPID

Both public and private keys must be set (and form a matching, valid P-256 pair) for Web Push to become effectively enabled, and only in `SCRUMBOY_MODE=full`. Details and status reasons: [vapid.md](vapid.md), [pwa.md](pwa.md).


| Variable                     | Notes                                                                                                                                                                                                                                                                                                                   |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `SCRUMBOY_VAPID_PUBLIC_KEY`  | URL-safe base64; 65-byte uncompressed P-256 public key when decoded                                                                                                                                                                                                                                                     |
| `SCRUMBOY_VAPID_PRIVATE_KEY` | URL-safe base64; 32-byte private scalar; **secret**                                                                                                                                                                                                                                                                     |
| `SCRUMBOY_VAPID_SUBSCRIBER`  | Optional contact for VAPID JWT `sub`. Plain email may be prefixed with `mailto:` at config load; preparation accepts plain mailbox, `mailto:…`, or unambiguous absolute `https://…`. If unset/empty, preparation uses built-in default `scrumboy@localhost`. Invalid subscriber → push `invalid` / `invalid_subscriber` |
| `SCRUMBOY_DEBUG_PUSH`        | Exactly `1` (after trim) enables server push send/prune debug logging                                                                                                                                                                                                                                                   |


**Docker / Compose:** stock Compose forwards VAPID vars and `SCRUMBOY_DEBUG_PUSH` from the host (optional).

Removed historically (not supported): `SCRUMBOY_PUSH_BY_DEFAULT_IF_VAPID` — VAPID presence is the operator signal for auto-subscribe eligibility.

---

## SMTP / email

SMTP enables outbound mail used by:

1. Self-service password-reset email (also needs `SCRUMBOY_ENCRYPTION_KEY`, valid `SCRUMBOY_PUBLIC_BASE_URL`, and local auth enabled)
2. Opt-in email notifications (needs SMTP + valid `SCRUMBOY_PUBLIC_BASE_URL`; **does not** need the encryption key)

Normative setup: [smtp.md](smtp.md), [notifications.md](notifications.md).


| Variable                                            | Default / rules                                                                                                                                                            |
| --------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `SCRUMBOY_SMTP_HOST`                                | Required (with From) for SMTP to be considered configured                                                                                                                  |
| `SCRUMBOY_SMTP_FROM`                                | Required (with Host); parseable RFC 5322 address, no CR/LF                                                                                                                 |
| `SCRUMBOY_SMTP_PORT`                                | **Unset** → `587`, `SMTPPortExplicit=false`. **Set** (including empty/invalid) → `SMTPPortExplicit=true`; invalid or out-of-range (`1..65535`) → port `0` (SMTP stays off) |
| `SCRUMBOY_SMTP_USERNAME` / `SCRUMBOY_SMTP_PASSWORD` | Optional; many hosted relays need them                                                                                                                                     |
| `SCRUMBOY_SMTP_TLS_MODE`                            | Unrecognized/empty → `starttls`. Accepted: `starttls`, `implicit`, `none` (mode is never inferred from port)                                                               |
| `SCRUMBOY_SMTP_DEBUG`                               | Exactly `1` (after trim) logs send attempts (never credentials/body)                                                                                                       |


**Compose note:** `docker-compose.yml` passes `SCRUMBOY_SMTP_PORT` as a bare key (no `${…:-}` default) so unset vs explicitly empty is preserved for the parser above.

---

## Development / build helpers

### `SCRUMBOY_WEB_DIR`

- **Consumers:** frontend packaging scripts under `internal/httpapi/web/scripts/` (`generate-landing.mjs`, `sync-vendor.mjs`, `verify-vendor.mjs`, `verify-i18n-locales.mjs`, `copy-i18n-locales.mjs`, and related tests).
- **Default:** when unset, scripts resolve the web tree relative to the script location.
- **Not read** by `config.FromEnv` or the `scrumboy` server process.

---

## Compatibility / launcher notes


| Mechanism                                 | Behavior                                                                                                                                       |
| ----------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| Process `.env` auto-load                  | **Not** implemented by Scrumboy                                                                                                                |
| `data/scrumboy.env` / root `scrumboy.env` | Used by Windows `win_run_*.bat` + `resolve_scrumboy_encryption_key.ps1` for `SCRUMBOY_ENCRYPTION_KEY` **only**                                 |
| Legacy raw single-line key file           | Still accepted by the Windows resolver for backward compatibility                                                                              |
| `scrumboy.env.example`                    | Example values for operators; not loaded by the server                                                                                         |
| Docker `ENV` / Compose `environment:`     | Supply process env inside the container; may differ from bare-binary defaults (especially `DATA_DIR`, `SQLITE_PATH`, `SQLITE_BUSY_TIMEOUT_MS`) |


---

## Related docs


| Topic                 | Doc                                                                        |
| --------------------- | -------------------------------------------------------------------------- |
| SMTP / password reset | [smtp.md](smtp.md)                                                         |
| Email notifications   | [notifications.md](notifications.md)                                       |
| VAPID / Web Push      | [vapid.md](vapid.md), [pwa.md](pwa.md)                                     |
| OIDC                  | [oidc.md](oidc.md)                                                         |
| OAuth / MCP discovery | [oauth.md](oauth.md)                                                       |
| Markdown / Mermaid    | [markdown-and-mermaid.md](markdown-and-mermaid.md)                         |
| Wall                  | [wall.md](wall.md)                                                         |
| Calendar / Agenda     | [calendar.md](calendar.md)                                                 |
| Persistence / backup  | [diagrams/scrumboy_deployment_ops.md](diagrams/scrumboy_deployment_ops.md) |


