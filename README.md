<p align="center">
  <img width="372" src="internal/httpapi/web/githublogo.png" alt="scrumboy logo" />
  <br />
  <img src="https://img.shields.io/badge/version-v3.23.2-blue" alt="version" />
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-AGPL--v3-orange" alt="license" /></a>
  <img src="https://img.shields.io/badge/i18n-23%20languages-yellow" alt="i18n" />
  <a href="https://github.com/markrai/scrumboy/actions/workflows/ci.yml"><img src="https://github.com/markrai/scrumboy/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI" /></a>
  <a href="SECURITY.md"><img src="https://img.shields.io/badge/Snyk-monitored-8A2BE2?logo=snyk&logoColor=white" alt="snyk monitored" /></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/markrai/scrumboy"><img src="https://api.scorecard.dev/projects/github.com/markrai/scrumboy/badge" alt="OpenSSF Scorecard" /></a>
</p>

#### Self-hosted project management & issue-tracking solution + instant shareable & customizable boards + realtime collaboration, automation, API access and MCP-compatible client support

<img width="2975" height="1078" alt="image" src="internal/httpapi/web/github_preview.jpg" />

## Table of contents

- [Quick Start](#quick-start)
  - [Run the official Docker image](#run-the-official-docker-image)
  - [Build locally from source](#build-locally-from-source)
  - [Run from source](#run-from-source)
  - [Run the Windows executable](#run-the-windows-executable)
- [Why Scrumboy?](#why-scrumboy)
- [Modes](#modes)
- [Features](#features)
- [Optional Configuration](#optional-configuration)
  - [Environment variables](#environment-variables)
  - [Server and data](#server-and-data)
  - [Encryption and TLS](#encryption-and-tls)
  - [OIDC / SSO](#oidc--sso)
  - [Feature flags](#feature-flags)
  - [Web Push (VAPID)](#web-push-vapid)
  - [SMTP (self-service password reset)](#smtp-self-service-password-reset-via-email)
  - [Public URL and reverse proxy](#public-url-and-reverse-proxy)
  - [Encryption key for 2FA/password reset](#encryption-key-for-2fapassword-reset)
  - [SMTP for self-service password reset (optional)](#smtp-for-self-service-password-reset-optional)
  - [OIDC / SSO login (optional)](#oidc--sso-login-optional)
  - [Owner disaster recovery](#owner-disaster-recovery)
  - [TLS / HTTPS (optional)](#tls--https-optional)
  - [PWA / Web Push (optional)](#pwa--web-push-optional)
  - [Frontend build note](#frontend-build-note)
- [Integrations & API Access](#integrations--api-access)
  - [MCP (JSON-RPC) for AI agents](#mcp-json-rpc-for-ai-agents)
  - [Webhooks (outbound HTTP)](#webhooks-outbound-http)
- [Roles](#roles)
  - [System roles (instance-wide)](#system-roles-instance-wide)
  - [Project roles (per project)](#project-roles-per-project)
- [Export scope](#export-scope)
- [Import modes](#import-modes)
- [Documentation](#documentation)
- [License and Contributions](#license-and-contributions)

## Quick Start

Runs in seconds. No setup required.

No `.env` file, TLS certificates, or encryption key are required to start the app.

Scrumboy creates runtime data under `./data` by default. The default SQLite database is `./data/app.db`, and SQLite may also create `app.db-wal` and `app.db-shm` while the server is running.  

### Run the official Docker image

Scrumboy is distributed as a container image on GitHub Container Registry:

`ghcr.io/markrai/scrumboy:latest`

Published images are multi-arch for `linux/amd64` and `linux/arm64`; Docker pulls the variant that matches your host.

**Docker run** (named volume for persistent SQLite data under `/data`):

```bash
docker run -d \
  --name scrumboy \
  -p 127.0.0.1:8080:8080 \
  -v scrumboy-data:/data \
  ghcr.io/markrai/scrumboy:latest
```

The image defaults to `DATA_DIR=/data` and `SQLITE_PATH=/data/app.db`. Mount a volume or host directory on `/data` so the database **and** file-backed uploads (for example `user-wallpapers/`) survive container recreation. Back up the whole `/data` volume (or at least `app.db` plus WAL/SHM sidecars **and** `user-wallpapers/`); see `[docs/diagrams/scrumboy_deployment_ops.md](docs/diagrams/scrumboy_deployment_ops.md)`.

**Docker Compose** (minimal example; save as `docker-compose.yml`):

```yaml
services:
  scrumboy:
    image: ghcr.io/markrai/scrumboy:latest
    container_name: scrumboy
    ports:
      - "127.0.0.1:8080:8080"
    volumes:
      - ./data:/data
    restart: unless-stopped
```

```bash
docker compose up -d
```

Open [http://localhost:8080](http://localhost:8080).  

### Build locally from source

To build from a local clone instead of pulling the published image:

```bash
docker compose up --build
```

The repository's `docker-compose.yml` uses `build: .` and maps `./data` to `/data`.

Open [http://localhost:8080](http://localhost:8080).  

### Run from source

```bash
go run ./cmd/scrumboy
```

Open [http://localhost:8080](http://localhost:8080).    

### Run the Windows executable

Windows users can download `scrumboy-*-windows-amd64.exe` from [GitHub Releases](https://github.com/markrai/scrumboy/releases). The matching `.sha256` file is published beside it for checksum verification. Release builds also publish a matching `.intoto.jsonl` provenance bundle.

The `.sha256` file checks file integrity. The attestation verifies the artifact's signed build provenance and expected repository identity:

```bash
gh attestation verify scrumboy-<tag>-windows-amd64.exe -R markrai/scrumboy
```

Put the exe in a dedicated writable folder before running it, for example `%USERPROFILE%\Scrumboy`. The exe starts a local Scrumboy server; open [http://localhost:8080](http://localhost:8080) after it starts.  

---

# Why Scrumboy?

Simplicity of a light Kanban, with the power of structured systems: Roles, sprints, audit trails & customizable workflows - without being locked into SaaS tools.  Centered around the self-hosted & privacy-focused community, as well as small to medium-sized teams & solo builders

# Modes

- **Full** (`SCRUMBOY_MODE=full`, default): Auth can be enabled. First user via bootstrap; then login/session. Backup/export, tags, multi-project. Projects can be user-owned (project_members) or anonymous (shareable by URL): `/anon` (or `/temp`) creates a throwaway board and redirects to `/{slug}`.
- **Anonymous** (`SCRUMBOY_MODE=anonymous`): No auth. Landing at `/`; live deployment at: [https://scrumboy.com/](https://scrumboy.com/)

# Features

- Custom Workflows: You can create any combination of workflow you want, per project, with user-defined "Done" lane.
- Realtime SSE enabled boards for instant multi-user actions.
- **Webhooks (API-only, full mode):** Register URLs per project so Scrumboy can POST JSON when subscribed domain events fire (e.g. `todo.assigned`). For your own automations, not in-app or browser notifications. See [Integrations](#integrations--api-access).
- Customizable Tags: Users can inherit and customize tag colors.
- Advanced filtering: Search todos based on text or tags.
- Sprints: create, activate, close; sprint filter on board; default sprint weeks (1 or 2) per project.
- Authentication & 2FA: TOTP supported when `SCRUMBOY_ENCRYPTION_KEY` is set.
- Self-service password reset email (optional, requires SMTP + `SCRUMBOY_ENCRYPTION_KEY` + `SCRUMBOY_PUBLIC_BASE_URL`): see [docs/smtp.md](docs/smtp.md).
- Audit trail: append-only `audit_events` table; todo/member/project/link actions logged (see [docs/audit-trail.md](docs/audit-trail.md)).
- Backup: export/import JSON; merge or replace; scope full or single project. JSON export is not a complete `DATA_DIR` disaster-recovery backup (uploaded wallpapers and `audit_events` are omitted); see `[docs/diagrams/scrumboy_deployment_ops.md](docs/diagrams/scrumboy_deployment_ops.md)`.
- PWA: Excellent UX for mobile users.
- Multi-language Support: English, 简体中文, हिन्दी, Español (Latinoamérica), العربية, Français, বাংলা, Português (Brasil), Bahasa Indonesia, اردو, Русский, Deutsch, 日本語, Kiswahili, Tiếng Việt, Türkçe, 한국어, فارسی, ไทย, Italiano, Bahasa Melayu, Polski, and Українська.
- Anonymous shareable boards can be created in both Full & Anonymous deployments.
- VoiceFlow - deterministic voice commands (see [docs/voiceflow.md](docs/voiceflow.md)).
- Sticky-Note Wall - per-project scratchpad of draggable sticky notes on the board (see [docs/wall.md](docs/wall.md)).
- Todo notes Markdown preview (optional) - **markdown** / **preview** tabs in the todo Notes field; optional Mermaid diagrams in fenced ````mermaid`blocks in preview only (see`[FAQ.md](FAQ.md)`,` [docs/markdown-and-mermaid.md](docs/markdown-and-mermaid.md)`).

---

## Optional Configuration

### Environment variables

- The app does **not** automatically load `.env` files.
- On Linux/macOS, export variables manually (for example: `export SCRUMBOY_ENCRYPTION_KEY=...`).
- On Windows, `win_run_full.bat` and `win_run_anonymous.bat` manage `data/scrumboy.env` automatically for local convenience.
- Precedence on Windows is: existing process env var `SCRUMBOY_ENCRYPTION_KEY`, then `data/scrumboy.env`, then legacy root `scrumboy.env`.
- The canonical Windows-managed local file format is `SCRUMBOY_ENCRYPTION_KEY=<base64-32-byte-key>`.
- Windows helper scripts still accept legacy raw single-line key files for backward compatibility.
- Env vars and defaults are defined in `internal/config/config.go`. ResolveDataDir uses `DATA_DIR` and `SQLITE_PATH` as documented there. None of these are required for basic startup.

### Server and data


| Variable                  | Default                                                                                          |
| ------------------------- | ------------------------------------------------------------------------------------------------ |
| `BIND_ADDR`               | `:8080`                                                                                          |
| `DATA_DIR`                | `./data` - Instance data directory for SQLite and file-backed uploads such as `user-wallpapers/` |
| `SQLITE_PATH`             | (empty; then `$DATA_DIR/app.db`)                                                                 |
| `SQLITE_BUSY_TIMEOUT_MS`  | `30000`                                                                                          |
| `SQLITE_JOURNAL_MODE`     | `WAL`                                                                                            |
| `SQLITE_SYNCHRONOUS`      | `FULL`                                                                                           |
| `MAX_REQUEST_BODY_BYTES`  | `1048576` (1 MiB)                                                                                |
| `MAX_TRELLO_IMPORT_BYTES` | `33554432` (32 MiB) - Max Trello JSON import upload size                                         |
| `SCRUMBOY_MODE`           | `full` (or `anonymous`)                                                                          |
| `SCRUMBOY_INTRANET_IP`    | `192.168.1.250` - LAN IP to log for intranet access                                              |


### Encryption and TLS


| Variable                  | Default                                                                                                                                                                                                                                                                                                                                             |
| ------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `SCRUMBOY_ENCRYPTION_KEY` | (empty) - **Required for 2FA and password reset.** Base64-encoded 32-byte key. Generate with `openssl rand -base64 32`. Without it, 2FA setup returns 503 and password-reset tokens cannot be issued. Back this key up with the instance `DATA_DIR` (including `data/app.db`); do not replace it casually once encrypted auth/security data exists. |
| `SCRUMBOY_TLS_CERT`       | `./cert.pem` - TLS cert for HTTPS                                                                                                                                                                                                                                                                                                                   |
| `SCRUMBOY_TLS_KEY`        | `./key.pem` - TLS key for HTTPS                                                                                                                                                                                                                                                                                                                     |


### OIDC / SSO


| Variable                            | Default                                                                                                                         |
| ----------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `SCRUMBOY_OIDC_ISSUER`              | (empty) - **OIDC / SSO.** Issuer URL. All four OIDC fields below must be set to enable SSO. See `[docs/oidc.md](docs/oidc.md)`. |
| `SCRUMBOY_OIDC_CLIENT_ID`           | (empty) - OIDC confidential client ID                                                                                           |
| `SCRUMBOY_OIDC_CLIENT_SECRET`       | (empty) - OIDC client secret                                                                                                    |
| `SCRUMBOY_OIDC_REDIRECT_URL`        | (empty) - Absolute callback URL (must match IdP registration), e.g. `https://scrumboy.example.com/api/auth/oidc/callback`       |
| `SCRUMBOY_OIDC_LOCAL_AUTH_DISABLED` | (empty) - Set to `true` to disable local password login/bootstrap when OIDC is configured                                       |


### Feature flags


| Variable                          | Default                                                                                                                                                                 |
| --------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `SCRUMBOY_WALL_ENABLED`           | (on) - Sticky-note wall. Unset/empty keeps wall enabled; `0`/`false`/`off`/`no` disables. Durable projects only. See `[docs/wall.md](docs/wall.md)`.                    |
| `SCRUMBOY_MARKDOWN_NOTES_ENABLED` | (off) - Todo notes Markdown preview. Set to `1`/`true`/`on`/`yes` to enable. See `[FAQ.md](FAQ.md)` and `[docs/markdown-and-mermaid.md](docs/markdown-and-mermaid.md)`. |
| `SCRUMBOY_MERMAID_NOTES_ENABLED`  | (off) - Mermaid diagrams in notes preview. Set to `1`/`true`/`on`/`yes`; effective only when Markdown notes are also enabled.                                           |


### Web Push (VAPID)


| Variable                     | Default                                                                                                                                                                                                                               |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `SCRUMBOY_VAPID_PUBLIC_KEY`  | (empty) - **Web Push.** VAPID public key (URL-safe base64). Required together with private key for PWA background assignment notifications and for post-login auto-subscribe in the SPA.                                              |
| `SCRUMBOY_VAPID_PRIVATE_KEY` | (empty) - VAPID private key (URL-safe base64).                                                                                                                                                                                        |
| `SCRUMBOY_VAPID_SUBSCRIBER`  | (empty) - Contact for VAPID JWT `sub` (not tied to IdP). Use a **plain email** (e.g. `ops@example.com`); the server adds `mailto:`. Or set a full `mailto:...` or `https://...` URL explicitly. If unset, a built-in default is used. |
| `SCRUMBOY_DEBUG_PUSH`        | (empty) - Set to `1` to log push send/prune on the server.                                                                                                                                                                            |


### SMTP (self-service password reset)


| Variable                 | Default                                                                                         |
| ------------------------ | ----------------------------------------------------------------------------------------------- |
| `SCRUMBOY_SMTP_HOST`     | (empty) - **Self-service password reset.** SMTP relay hostname. Required with From.             |
| `SCRUMBOY_SMTP_PORT`     | `587` - Defaults to 587 when omitted. If explicitly set, must be 1–65535.                       |
| `SCRUMBOY_SMTP_USERNAME` | (empty) - SMTP auth username; omit for relays allowing unauthenticated submission.              |
| `SCRUMBOY_SMTP_PASSWORD` | (empty) - SMTP auth password. Never logged.                                                     |
| `SCRUMBOY_SMTP_FROM`     | (empty) - Envelope + header `From`, e.g. `Scrumboy <no-reply@example.com>`. Required with Host. |
| `SCRUMBOY_SMTP_TLS_MODE` | `starttls` (or `implicit`, `none`) - see `[docs/smtp.md](docs/smtp.md)`.                        |
| `SCRUMBOY_SMTP_DEBUG`    | (empty) - Set to `1` to log SMTP send attempts (never credentials/body).                        |


### Public URL and reverse proxy


| Variable                   | Default                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| -------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `SCRUMBOY_PUBLIC_BASE_URL` | (empty) - **Required for self-service password-reset email.** Canonical public origin (e.g. `https://scrumboy.example.com`). Must be absolute `http`/`https` with hostname; no path, query, fragment, or userinfo. Missing or invalid → self-service emails disabled (generic API response only). Also used for admin-generated reset links and as the canonical OAuth discovery issuer when set. For **OAuth**, non-loopback issuers must be **HTTPS**; plain `http` is accepted only for loopback (`localhost` / `127.0.0.0/8` / `::1`). See `[docs/smtp.md](docs/smtp.md#reset-link-url)` and `[docs/oauth.md](docs/oauth.md#issuer--discovery-origin)`. |
| `SCRUMBOY_TRUST_PROXY`     | (empty) - Set to `1`/`true`/`on`/`yes` to honor `X-Forwarded-For` for auth/OAuth rate-limit IP keys and (for OAuth issuer discovery) trusted `X-Forwarded-Proto` / `X-Forwarded-Host` / `CF-Visitor`. Default off: use `RemoteAddr` only for IP keys. Enable only behind a reverse proxy that **overwrites or strips client-supplied** values for all of those headers — not only XFF. When enabled, OAuth issuer discovery requires either `SCRUMBOY_PUBLIC_BASE_URL` or a proxy-provided `X-Forwarded-Host` together with a forwarded HTTPS indication; `X-Forwarded-Proto` alone (or multi-value forwarded scheme/host fields) results in 503.           |


`docker-compose.yml` overrides some of these (e.g. `SQLITE_BUSY_TIMEOUT_MS=5000`).

---

### Encryption key for 2FA/password reset

- `SCRUMBOY_ENCRYPTION_KEY` is **not** required for basic startup.
- It becomes required for encrypted auth/security features, including:
  - 2FA
  - Password reset flows
- If an existing database already has 2FA-enabled users, startup fails without this key.
- The key is part of the same backup/restore unit as the instance `DATA_DIR` (including `data/app.db`). Back them up together.
- Do **not** regenerate or replace the key casually after encrypted auth/security data exists, or you can break access to 2FA/password-reset data.
- If a bad key is configured before any encrypted auth/security data exists, Scrumboy may warn and continue with 2FA setup/password reset disabled until you configure a valid key.

Generate a key with: `openssl rand -base64 32`

Example for Docker Compose secret injection:

```yaml
services:
  scrumboy:
    environment:
      - SCRUMBOY_ENCRYPTION_KEY=${SCRUMBOY_ENCRYPTION_KEY}
```

Example for systemd secret injection:

```ini
[Service]
Environment="SCRUMBOY_ENCRYPTION_KEY=REPLACE_WITH_BASE64_32_BYTE_KEY"
```

In both cases, the deployment manager is injecting the environment variable. Scrumboy itself does not auto-load these files.

### SMTP for self-service password reset (optional)

Optional SMTP lets users who already have a Scrumboy-local password request a reset email (**Forgot your Scrumboy password?**). You need a relay (`SCRUMBOY_SMTP_`*), `SCRUMBOY_ENCRYPTION_KEY`, a valid `SCRUMBOY_PUBLIC_BASE_URL`, and local authentication enabled. Owners can generate links only for users with a usable local password; SSO credential recovery belongs to the identity provider. Setup and troubleshooting: `[docs/smtp.md](docs/smtp.md)`.

### Email notifications (optional)

The same SMTP config above also enables opt-in email notifications: users choose per-category (card assigned to them, card/sprint/project activity, added to a project) under Settings → Customization, off by default. No `SCRUMBOY_ENCRYPTION_KEY` required. Setup and category/recipient details: [`docs/notifications.md`](docs/notifications.md).

### OIDC / SSO login (optional)

Optional OpenID Connect SSO with any standards-compliant IdP (Keycloak, Authentik, Auth0, Entra ID, etc.). Accounts may use a local password, SSO, or both. Local users connect SSO explicitly from Settings; SSO-only users can establish a local recovery password after fresh provider reauthentication. Matching email never silently links an identity, and `users.email` remains the canonical Scrumboy email. Local login stays available unless `SCRUMBOY_OIDC_LOCAL_AUTH_DISABLED=true`. See `[docs/oidc.md](docs/oidc.md)`, `[SECURITY.md](SECURITY.md)`, `[docs/authentication-api.md](docs/authentication-api.md)`, and `[docs/recovery.md](docs/recovery.md)`.

### Owner disaster recovery

If the identity provider is unavailable, a host operator can recover an existing owner's local password without starting the HTTP server or applying migrations:

```sh
./scrumboy recover-owner --email owner@example.com
```

Stop Scrumboy and back up the SQLite database/volume first. Container, bind-mount, named-volume, stdin automation, schema-compatibility, session-revocation, and local-auth-disabled instructions are in `[docs/recovery.md](docs/recovery.md)`.

### TLS / HTTPS (optional)

- TLS is optional.
- HTTPS is enabled only when both `SCRUMBOY_TLS_CERT` and `SCRUMBOY_TLS_KEY` files exist.
- Otherwise, the server runs on HTTP by default.

### PWA / Web Push (optional)

Install the app from the browser for a standalone window and better mobile UX. **Background assignment alerts** use the **Web Push API** with **VAPID** keys on the server. When Web Push is **effectively enabled** (validated matching key pair in full mode — not merely non-empty env strings), signed-in clients attempt to subscribe automatically (browser permission may be prompted). Docker users must pass the VAPID variables into the container environment and recreate the container after changes. Details, validation/status, Docker verification, and subscriber contact semantics: `[docs/vapid.md](docs/vapid.md)`, `[docs/pwa.md](docs/pwa.md)`.

### Frontend build note

The Docker image and `go run` embed prebuilt assets under `internal/httpapi/web/dist`. If they are missing, build them:

```bash
cd internal/httpapi/web
npm install
npm run build
```

Then run `docker compose up --build` (local image build) or `go run ./cmd/scrumboy` again from the repository root.

## Integrations & API Access

Scrumboy supports API access tokens for automation, integrations, and programmatic MCP access (legacy HTTP and JSON-RPC - see below). Full MCP guide for developers and agents: `[docs/mcp.md](docs/mcp.md)`.

You can create a token from the API and use it to call MCP endpoints directly - no browser session or cookies required.

**Create a token (requires login session):**

```bash
curl -b cookies.txt -X POST http://localhost:8080/api/me/tokens \
  -H "Content-Type: application/json" \
  -H "X-Scrumboy: 1" \
  -d '{"name":"cli"}'
```

Response includes a one-time token (starts with sb_).

Use it with MCP:

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sb_your_token_here" \
  -d '{"tool":"projects.list","input":{}}'
```

### MCP (JSON-RPC) for AI agents

Scrumboy exposes a standards-based, JSON-only Streamable HTTP endpoint for native MCP clients such as Cursor and Claude Code.

**Endpoint:** `POST /mcp/rpc`

This is separate from the `/mcp` HTTP endpoint above and follows **JSON-RPC 2.0** (`initialize`, `tools/list`, `tools/call`, etc.). See `[docs/mcp.md](docs/mcp.md)` for tools, auth, response shapes, and examples; `[API.md](API.md)` for the full HTTP/MCP behavior reference.

Native clients must be configured directly with `/mcp/rpc`. It is the sole OAuth protected resource; `/mcp` remains the legacy Scrumboy `{tool,input}` API.

```bash
claude mcp add --transport http scrumboy https://scrumboy.example.com/mcp/rpc
```

#### Example: `initialize`

```bash
curl -X POST http://localhost:8080/mcp/rpc \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer sb_your_token_here" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"curl","version":"1"}}}'
```

#### Example: list tools

```bash
curl -X POST http://localhost:8080/mcp/rpc \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "MCP-Protocol-Version: 2025-11-25" \
  -H "Authorization: Bearer sb_your_token_here" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
```

#### Example: call a tool

```bash
curl -X POST http://localhost:8080/mcp/rpc \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "MCP-Protocol-Version: 2025-11-25" \
  -H "Authorization: Bearer sb_your_token_here" \
  -d '{
    "jsonrpc":"2.0",
    "id":3,
    "method":"tools/call",
    "params":{
      "name":"todos.create",
      "arguments":{
        "projectSlug":"my-project",
        "title":"Created via MCP"
      }
    }
  }'
```

**Notes**

- Compatible with MCP clients that support **HTTP JSON-RPC** to this URL.
- Some MCP clients expect **stdio**-based servers - those are **not** supported here.
- `/mcp/rpc` accepts a **session cookie**, a static `sb_…` **Bearer** token, or a Scrumboy OAuth token bound to this exact resource. `/mcp` accepts cookies and static tokens only.
- In full mode, unauthenticated `/mcp/rpc` requests receive an empty 401 with RFC 9728 protected-resource discovery metadata.
- Compatible Codex/Claude-style agent workspaces that support local or manual
plugin loading can use the Scrumboy Board Operator plugin package in
`[plugins/scrumboy-board-operator](plugins/scrumboy-board-operator)`. The
package contains a focused Skill workflow guide; each harness still needs to
be configured to load it.

This enables:

- CLI usage
- CI/CD automation
- AI agents and MCP clients (use `POST /mcp/rpc` for JSON-RPC; `POST /mcp` remains available for the legacy `{ "tool", "input" }` envelope)
- Scripting/integrations without login flows

### Webhooks (outbound HTTP)

Scrumboy can **POST JSON to URLs you register** when certain events occur. This is for **server-side integrations** (your script, gateway, queue worker, etc.). It does **not** add notifications inside the Scrumboy UI; live boards still update via **SSE** as before.

- **Availability:** **Full mode only** (endpoints are disabled in anonymous mode).
- **Who can configure:** Project **maintainers**, via the HTTP API only - there is **no settings screen** for webhooks yet.
- **API:** `POST /api/webhooks` (create), `GET /api/webhooks` (list yours), `DELETE /api/webhooks/{id}` - same session cookie / CSRF header rules as other mutating `/api/`* calls.
- **Events:** Subscribe to specific types (e.g. `todo.assigned`) or `*` for all delivered types. The set may grow over time; unused types in your list are harmless.
- **Security:** Optional per-webhook **secret**; when set, requests include an `X-Scrumboy-Signature` header (`sha256=` HMAC of the raw JSON body).
- **Semantics:** Best-effort delivery with retries on failure; not a durable external queue - design for idempotent receivers using the event `id` in the JSON body.

Example create (replace cookie / project id / URL):

```bash
curl -b cookies.txt -X POST http://localhost:8080/api/webhooks \
  -H "Content-Type: application/json" \
  -H "X-Scrumboy: 1" \
  -d '{"projectId":1,"url":"https://example.com/scrumboy-hook","events":["todo.assigned"],"secret":"optional-shared-secret"}'
```

# Roles

In **full mode**, access is governed by two separate role systems. System roles do not grant project access; project access comes only from project membership.

### System roles (instance-wide)


| Role      | Who has it                                               | Allowed actions                                                                                                                                      |
| --------- | -------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Owner** | Bootstrap (first) user; can be assigned by another owner | List all users; create users (admin-only API); update any user’s system role (owner/admin/user); delete users (except cannot delete the last owner). |
| **Admin** | Assigned by an owner                                     | List all users; create users. Cannot change system roles or delete users.                                                                            |
| **User**  | Default for new users; assigned by owner                 | No system-level user management. Access to projects only via project membership.                                                                     |


### Project roles (per project)

A user must be a member of a project to access it; system role alone does not grant access.


| Role            | View board & todos | Create/edit/move/delete todos | Edit body when assigned | Manage members | Delete project | Tag delete/color (project-scoped) |
| --------------- | ------------------ | ----------------------------- | ----------------------- | -------------- | -------------- | --------------------------------- |
| **Maintainer**  | ✓                  | ✓                             | ✓                       | ✓              | ✓              | ✓ (maintainer)                    |
| **Contributor** | ✓                  | -                             | ✓ (body only)           | -              | -              | -                                 |
| **Viewer**      | ✓                  | -                             | -                       | -              | -              | -                                 |


- **View** (board, backlog, burndown, charts, etc.): Any project role (Viewer or above).
- **Create/edit/move/delete todos, assign, sprints**: Maintainer only. Contributor cannot create, delete, move, or assign; cannot edit title, tags, sprint, or estimation.
- **Edit body when assigned**: Contributor can edit the body field only when the todo is assigned to them. Maintainer has full edit.
- **Manage members** (add/remove members, change role): Maintainer only.
- **Delete project**: Maintainer only.
- **Delete/update tag** (project-scoped tags): Maintainer only. User-owned tags: owner of the tag or maintainer in all projects where the tag is used.
- **Create tags**: Contributor or Maintainer.

Temporary/anonymous boards (shareable by URL, no auth) do not use project roles; anyone with the link can view and edit. New Todo and drag-and-drop are enabled for anonymous boards.

---

# Export scope

- **Full**: All projects the user can access (full mode: projects where the user is a member, or temporary boards they created; anonymous mode: not applicable for full export).
- **Single project**: One board/project only (e.g. current board in anonymous mode).

---

# Import modes

When importing a backup JSON, you choose how it is applied:


| Mode            | Description                                                                                                                                                                                                                                                                                        |
| --------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Replace**     | Replace all: delete every project in your current export scope, then create projects from the backup. Effect is “nuke and restore” so the instance matches the backup. Not available in anonymous mode.                                                                                            |
| **Merge**       | Merge/update: for each project in the backup, match by slug. If a project with that slug exists (and you have access), update its todos, tags, and links to match the backup; otherwise create a new project. In anonymous mode, merge behaves like Create Copy (all projects are created as new). |
| **Create copy** | Create copy: create new projects for every project in the backup. Slugs are made unique (e.g. `name-imported-2`), so nothing is overwritten; you get duplicates.                                                                                                                                   |


In **anonymous mode**, full-scope import is not allowed; you can only import into the current board (todos and tags are added to that board).

---

# Documentation

- **Docs index:** `[docs/README.md](docs/README.md)` - audience-grouped index (ops, features, security, integrations, architecture, manual checks) and `node docs/scripts/verify-docs.mjs`
- **Security architecture:** `[docs/security.md](docs/security.md)` - authentication, authorization, data protection, scanning, and supply-chain practices (disclosure: `[SECURITY.md](SECURITY.md)`)
- **i18n architecture:** `[docs/i18n.md](docs/i18n.md)` - catalogs, locales, landings, change gates (rules SoT: `[AGENTS.md](AGENTS.md)`)
- **Architecture diagrams:** `[docs/diagrams/](docs/diagrams/)` - Mermaid sources and self-contained viewer (`serve-diagrams.bat` or `python serve.py` in that folder, then open `http://127.0.0.1:8775/`)
- **MCP (HTTP tools + JSON-RPC):** `[docs/mcp.md](docs/mcp.md)` - tool catalog, auth, legacy vs `/mcp/rpc`, examples (agents & automation). See also `[API.md](API.md)` for exhaustive MCP HTTP detail.
- **OAuth 2.1 for MCP clients:** `[docs/oauth.md](docs/oauth.md)` - resource discovery, Dynamic Client Registration, PKCE, and resource-bound authorize/token/revoke flows for native clients such as Cursor and Claude Code.
- **Remote MCP/OAuth release acceptance:** `[docs/mcp-oauth-acceptance.md](docs/mcp-oauth-acceptance.md)` - Vega/Keycloak evidence record plus Cursor, Claude Code, cookie/static, legacy, and negative-resource gates.
- **Agent plugin package:** `[plugins/scrumboy-board-operator](plugins/scrumboy-board-operator)` - local/manual plugin metadata, board-operator Skill, and seed eval cases for MCP/Agoragentic agent workflows.
- **PWA / Web Push (VAPID):** `[docs/pwa.md](docs/pwa.md)` - keys, subscriber contact, post-login auto-subscribe when VAPID is configured, Settings opt-out, tradeoffs.
- **Roles and permissions:** `[docs/roles-and-permissions.md](docs/roles-and-permissions.md)` - project roles, backend authorization, anonymous boards.
- **Audit trail:** `[docs/audit-trail.md](docs/audit-trail.md)` - action vocabulary, event model, integration points.

---

# License and Contributions

Scrumboy is licensed under the **GNU Affero General Public License v3** (AGPL v3). See [LICENSE](LICENSE) for the full text.

**Contributing:** Contributions use the [Developer Certificate of Origin (DCO)](https://developercertificate.org/). Sign off commits with `git commit -s` (see [CONTRIBUTING.md](CONTRIBUTING.md) for setup, build, and pull request guidelines).

**Code of Conduct:** [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)

**Bugs and enhancements:** [GitHub Issues](https://github.com/markrai/scrumboy/issues)

**Security vulnerabilities:** Report privately via [SECURITY.md](SECURITY.md) (do not open a public issue for security-sensitive bugs).  

For any other feedback, questions, or inquiries, please contact the maintainer at [markraidc@gmail.com](mailto:markraidc@gmail.com)

Scrumboy is an independent open-source project and is not affiliated with, sponsored by, or endorsed by Scrum.org, Scrum Alliance, Inc., or any other organization associated with Scrum training or certification. Any reference to "scrum" is made solely to describe the project management methodology that the software is intended to support.