<p align="center">
  <img width="372" src="internal/httpapi/web/githublogo.png" alt="scrumboy logo" />
  <br />
  <img src="https://img.shields.io/badge/version-v3.34.0-blue" alt="version" />
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
  - [Run the macOS executable](#run-the-macos-executable)
- [Why Scrumboy?](#why-scrumboy)
- [Modes](#modes)
- [Features](#features)
- [Optional Configuration](#optional-configuration)
  - [Environment variables](#environment-variables)
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

### Run the macOS executable

macOS users can download architecture-specific archives from [GitHub Releases](https://github.com/markrai/scrumboy/releases):

- Apple Silicon: `scrumboy-<tag>-darwin-arm64.tar.gz`
- Intel: `scrumboy-<tag>-darwin-amd64.tar.gz`

These binaries require **macOS 12 Monterey or later** (the Go 1.26 Darwin floor). That minimum is not raised by the GitHub Actions runner that produced the build.

The matching `.sha256` file is published beside each archive for checksum verification. Release builds also publish a matching `.intoto.jsonl` provenance bundle. The macOS archives are not Apple-signed or notarized.

The `.sha256` file checks file integrity. The attestation verifies the artifact's signed build provenance and expected repository identity:

```bash
shasum -a 256 -c scrumboy-<tag>-darwin-arm64.tar.gz.sha256
gh attestation verify scrumboy-<tag>-darwin-arm64.tar.gz -R markrai/scrumboy
```

Extract the archive, then run the `scrumboy` binary from a dedicated writable folder (runtime data defaults to `./data`, including `app.db`):

```bash
tar -xzf scrumboy-<tag>-darwin-arm64.tar.gz
./scrumboy
```

Open [http://localhost:8080](http://localhost:8080) after it starts. Set `DATA_DIR` if you want the database and uploads in a different directory.

**Troubleshooting:** if macOS blocks launch of a binary you downloaded from GitHub Releases **after** checksum and attestation verification, clear quarantine for that file only:

```bash
xattr -d com.apple.quarantine ./scrumboy
```

Do not disable Gatekeeper or other system-wide protections.

---

# Why Scrumboy?

Simplicity of a light Kanban, with the power of structured systems: Roles, sprints, audit trails & customizable workflows - without being locked into SaaS tools.  Centered around the self-hosted & privacy-focused community, as well as small to medium-sized teams & solo builders

# Modes

- **Full** (`SCRUMBOY_MODE=full`, default): Auth can be enabled. First user via bootstrap; then login/session. Backup/export, tags, multi-project. Projects can be user-owned (project_members) or anonymous (shareable by URL): `/anon` (or `/temp`) creates a throwaway board and redirects to `/{slug}`.
- **Anonymous** (`SCRUMBOY_MODE=anonymous`): No auth. Landing at `/`; live deployment at: [https://scrumboy.com/](https://scrumboy.com/)

# Features

- Custom Workflows: You can create any combination of workflow you want, per project, with user-defined "Done" lane.
- Custom Priority Tiers: each project has ordered, color-coded priority definitions with stable keys; maintainers configure tiers and assign them to todos.
- Realtime SSE enabled boards for instant multi-user actions.
- **Webhooks (API-only, full mode):** Register URLs per project so Scrumboy can POST JSON when subscribed domain events fire (e.g. `todo.assigned`). For your own automations, not in-app or browser notifications. See [Integrations](#integrations--api-access).
- Customizable Tags: Users can inherit and customize tag colors.
- Advanced filtering: Search todos based on text or tags.
- Sprints: create, activate, close; sprint filter on board; default sprint weeks (1 or 2) per project. Maintainers can disable sprints per project without deleting sprint history or todo assignments, then re-enable them later.
- Authentication & 2FA: TOTP supported when `SCRUMBOY_ENCRYPTION_KEY` is set.
- Self-service password reset email (optional, requires SMTP + `SCRUMBOY_ENCRYPTION_KEY` + `SCRUMBOY_PUBLIC_BASE_URL`): see [docs/smtp.md](docs/smtp.md).
- Audit trail: append-only `audit_events` table; todo/member/project/link actions logged (see [docs/audit-trail.md](docs/audit-trail.md)).
- Backup: export/import JSON; merge or replace; scope full or single project. JSON export is not a complete `DATA_DIR` disaster-recovery backup (uploaded wallpapers and `audit_events` are omitted); see `[docs/diagrams/scrumboy_deployment_ops.md](docs/diagrams/scrumboy_deployment_ops.md)`.
- PWA: Excellent UX for mobile users.
- Multi-language Support: English, 简体中文, हिन्दी, Español (Latinoamérica), العربية, Français, বাংলা, Português (Brasil), Bahasa Indonesia, اردو, Русский, Deutsch, 日本語, Kiswahili, Tiếng Việt, Türkçe, 한국어, فارسی, ไทย, Italiano, Bahasa Melayu, Polski, and Українська.
- Anonymous shareable boards can be created in both Full & Anonymous deployments.
- VoiceFlow - deterministic voice commands (see [docs/voiceflow.md](docs/voiceflow.md)).
- Sticky-Note Wall - per-project scratchpad of draggable sticky notes on the board (see [docs/wall.md](docs/wall.md)).
- Agenda - today's events from subscribed ICS feeds on durable boards (see [docs/calendar.md](docs/calendar.md)). Requires `SCRUMBOY_ENCRYPTION_KEY`.
- Todo notes Markdown preview (optional) - **markdown** / **preview** tabs in the todo Notes field; optional Mermaid diagrams in fenced ````mermaid`blocks in preview only (see`[FAQ.md](FAQ.md)`,` [docs/markdown-and-mermaid.md](docs/markdown-and-mermaid.md)`).

---

## Optional Configuration

### Environment variables

A fresh Scrumboy install needs no environment variables. Set them only when you want to change deployment defaults or turn on optional features (SSO, email, Web Push, TLS, encryption, and similar).

Scrumboy does **not** automatically load `.env` files. Inject variables through your shell, process manager, Docker/Compose, or another launcher.

Full names, defaults, requirements, and interactions: [`docs/environment-variables.md`](docs/environment-variables.md).

---

### Encryption key for 2FA/password reset

`SCRUMBOY_ENCRYPTION_KEY` is **not** required for basic Scrumboy startup. It is required for features that store encrypted authentication/security or calendar data, including 2FA and Agenda calendar feeds. Once encrypted data exists, back up and restore the key with the Scrumboy data; do not casually replace it. Details: [`docs/environment-variables.md`](docs/environment-variables.md#scrumboy_encryption_key) and [`FAQ.md`](FAQ.md#how-do-i-generate-scrumboy_encryption_key).

### SMTP for self-service password reset (optional)

Optional SMTP enables self-service password-reset email (**Forgot your Scrumboy password?**) for users with a usable Scrumboy-local password; it also depends on `SCRUMBOY_ENCRYPTION_KEY` and a valid `SCRUMBOY_PUBLIC_BASE_URL`. SSO credential recovery remains the identity provider's responsibility. Setup and troubleshooting: [`docs/smtp.md`](docs/smtp.md).

### Email notifications (optional)

Optional email notifications use the same SMTP configuration as password-reset email. They are opt-in per user under Settings → Customization, including per-category choices, and do not require `SCRUMBOY_ENCRYPTION_KEY`. Setup and category/recipient details: [`docs/notifications.md`](docs/notifications.md).

### OIDC / SSO login (optional)

Optional OpenID Connect / SSO with a standards-compliant identity provider. Accounts may use a local password, SSO, or both; existing local users connect SSO explicitly, and matching emails are not silently linked. Local authentication remains available unless explicitly disabled. See [`docs/oidc.md`](docs/oidc.md), [`docs/authentication-api.md`](docs/authentication-api.md), [`docs/recovery.md`](docs/recovery.md), and [`docs/security.md`](docs/security.md).

### Owner disaster recovery

If the identity provider is unavailable, a host operator can recover an existing owner's local password via an offline, host-side break-glass path. Stop Scrumboy and back up the database first. Instructions: [`docs/recovery.md`](docs/recovery.md).

### TLS / HTTPS (optional)

- TLS is optional.
- HTTPS is enabled only when both `SCRUMBOY_TLS_CERT` and `SCRUMBOY_TLS_KEY` files exist.
- Otherwise, the server runs on HTTP by default.

### PWA / Web Push (optional)

Install Scrumboy from the browser as a **PWA** for a standalone, mobile-friendly experience. Background assignment notifications are available through **Web Push**, which requires server-side **VAPID** configuration and browser notification permission. See [docs/pwa.md](docs/pwa.md) and [docs/vapid.md](docs/vapid.md).

### Frontend build note

The Docker image and `go run` embed prebuilt assets under `internal/httpapi/web/dist`. If they are missing, build them:

```bash
cd internal/httpapi/web
npm install
npm run build
```

Then run `docker compose up --build` (local image build) or `go run ./cmd/scrumboy` again from the repository root.

## Integrations & API Access

Scrumboy supports API access tokens for automation and integrations, including programmatic MCP and API access without a browser session. Both the legacy HTTP MCP interface and the native JSON-RPC MCP interface are supported. See [docs/mcp.md](docs/mcp.md) and [API.md](API.md).

### MCP (JSON-RPC) for AI agents

Scrumboy supports native MCP clients such as Cursor and Claude Code over **HTTP** Streamable JSON-RPC at `/mcp/rpc` (stdio is not supported). The older `/mcp` HTTP interface remains available for legacy and programmatic use. Details: [docs/mcp.md](docs/mcp.md), [docs/oauth.md](docs/oauth.md), and [API.md](API.md). Compatible agent workspaces can also use the optional Board Operator package in [plugins/scrumboy-board-operator](plugins/scrumboy-board-operator).

### Webhooks (outbound HTTP)

Scrumboy can POST JSON event payloads to URLs you register for **server-side integrations** (your script, gateway, queue worker, and similar). Webhooks are available in **Full mode**; project **maintainers** configure them through the HTTP API. They are separate from in-app notifications, browser/Web Push notifications, and realtime board updates via SSE. Full operator and integration details are in [docs/webhooks.md](docs/webhooks.md).

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
- **Create/edit/move/delete todos, assign, sprints, priorities**: Maintainer only. Contributor cannot create, delete, move, assign, or configure priorities; cannot edit title, tags, sprint, priority, or estimation.
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

Backup format 1.1 remains backward-compatible with backups created before
priorities existed. New exports explicitly emit `priorityTiers` (`[]` means
the canonical defaults) and todo `priorityKey` (`null` means no priority).
During matched-project merge, absent legacy fields preserve target definitions
or assignments, while explicit `null` clears a todo assignment.

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

Scrumboy is an independent open-source project and is not affiliated with, sponsored by, or endorsed by Scrum.org, Scrum Alliance, Inc., or any other organization associated with Scrum training or certification. Any reference to "scrum" is made solely to describe the project management methodology that the software is intended to support. Google Calendar is a trademark of Google LLC. Apple and iCloud are trademarks of Apple Inc. Scrumboy is an independent project and is not affiliated with, sponsored by, or endorsed by Google LLC or Apple Inc. Provider names and icons are used only to identify the inferred host of a configured ICS feed. Third-party trademarks remain the property of their respective owners.
