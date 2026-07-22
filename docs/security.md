# Security architecture and practices

This document describes Scrumboy’s **current** security architecture, controls, scanning, and operational assumptions as implemented in the repository. It is for operators evaluating a deployment, contributors reviewing security-sensitive code, and maintainers tracking defense-in-depth.

Vulnerability **disclosure** (how to report a bug privately) lives in the root `[SECURITY.md](../SECURITY.md)`.


| Field                    | Value                                      |
| ------------------------ | ------------------------------------------ |
| **Last verified commit** | `92d3a9a921b72709a854bc93b065bbbefe78d235` |
| **Last verified date**   | 2026-07-21                                 |


Security depends on correct deployment and configuration (HTTPS, secrets, IdP hardening, host access, backups). **No security document, badge, or clean scan guarantees the absence of vulnerabilities.** Scrumboy does not claim formal certification, an external penetration test, or compliance with a named regulatory framework unless separately evidenced outside this repository.

## Table of contents

- [Security model and boundaries](#security-model-and-boundaries)
  - [Trust boundaries](#trust-boundaries)
  - [What application controls do and do not cover](#what-application-controls-do-and-do-not-cover)
  - [Threat-model summary](#threat-model-summary)
- [Human authentication](#human-authentication)
- [Password handling](#password-handling)
- [Sessions and cookies](#sessions-and-cookies)
- [Two-factor authentication and encryption](#two-factor-authentication-and-encryption)
- [Authorization and permissions](#authorization-and-permissions)
- [OIDC and external identity providers](#oidc-and-external-identity-providers)
- [OAuth for MCP clients](#oauth-for-mcp-clients)
- [API, MCP, and transport protections](#api-mcp-and-transport-protections)
- [Data at rest and persistence](#data-at-rest-and-persistence)
- [Audit trail](#audit-trail)
- [Outbound integrations and external data flow](#outbound-integrations-and-external-data-flow)
  - [Web Push](#web-push)
  - [SMTP](#smtp)
  - [Webhooks](#webhooks)
- [Security scanning and dependency monitoring](#security-scanning-and-dependency-monitoring)
  - [Interpreting scan results](#interpreting-scan-results)
- [Software supply-chain practices](#software-supply-chain-practices)
- [Secure development and review practices](#secure-development-and-review-practices)
- [Deployment responsibilities](#deployment-responsibilities)
- [Known limitations and non-goals](#known-limitations-and-non-goals)
- [Related documents](#related-documents)

---



## Security model and boundaries



### Trust boundaries


| Boundary                                    | Role                                                                                                |
| ------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| Browser / SPA client                        | Renders UI, holds session cookie and optional Web Push subscription; not an authorization authority |
| Scrumboy HTTP process                       | Enforces authentication, authorization, CSRF/Origin checks, rate limits, crypto                     |
| SQLite + `DATA_DIR`                         | Durable board/auth state and file uploads (for example wallpapers)                                  |
| Reverse proxy / TLS terminator              | Optional; trusted only when `SCRUMBOY_TRUST_PROXY` and forwarded headers are correctly set          |
| Upstream OIDC provider                      | Human SSO; MFA and account lifecycle for ordinary SSO are IdP responsibilities                      |
| Scrumboy OAuth authorization server         | Issues tokens for **MCP clients** on `/mcp/rpc` (not human browser login)                           |
| Browser push services                       | Deliver encrypted Web Push payloads when VAPID is effectively enabled                               |
| SMTP relay                                  | Delivers password-reset mail when configured                                                        |
| Webhook destinations                        | Receive outbound event HTTP from Scrumboy                                                           |
| Host administrator / filesystem / DB access | Full break-glass power (`recover-owner`, raw SQL, file copy)                                        |




### What application controls do and do not cover

**Application-level:** password hashing, hashed session/API/OAuth secrets, AES-GCM for TOTP secrets (when a valid encryption key is configured), store-layer authorization, CSRF custom header on mutating `/api/`*, OAuth/OIDC protocol checks, in-process rate limits, append-only audit triggers for ordinary SQL.

**Host-level (operator):** disk encryption, OS users, firewall, TLS certificates, backup custody, IdP MFA policy, who can read `DATA_DIR` and env secrets.

**Does not defend against** a fully privileged server or database administrator, physical disk access without OS protections, or compromise of the running Scrumboy process (which can mint sessions and read secrets in memory).

### Threat-model summary

**Intended protections:** limit damage from stolen database files (hashed secrets), stolen backups that omit hashes where designed, CSRF from foreign origins against cookie sessions, silent account takeover via email collision on OIDC, reuse of OAuth refresh tokens, and casual tampering with `audit_events` through the application.

**Non-goals:** multi-tenant cloud isolation between hostile co-tenants on one process; cryptographic proof against DBA forgery of audit rows; SSRF-proof webhooks to arbitrary maintainer URLs; formal assurance cases or signed every release artifact.

---



## Human authentication

Scrumboy derives account methods from authoritative records: a **usable bcrypt password hash** and/or an OIDC identity for the configured issuer. It does not keep duplicate “has password / has SSO” booleans as the source of truth (`store` auth helpers such as `IsUsablePasswordHash`).


| Method         | Notes                                                                      |
| -------------- | -------------------------------------------------------------------------- |
| Local password | bcrypt verify; may require Scrumboy 2FA when enabled                       |
| OIDC SSO       | Authorization Code + PKCE; session cookie after server-side validation     |
| Dual           | Usable local password **and** linked OIDC identity                         |
| SSO-only       | `password_hash` NULL / unusable; no Scrumboy password reset mail           |
| Bootstrap      | First owner/admin creation on empty instance (see bootstrap docs/diagrams) |
| Owner recovery | Host-side `recover-owner` break-glass (`[docs/recovery.md](recovery.md)`)  |


**Identity ownership:** normal OIDC login binds `(issuer, subject)`. Matching email alone does **not** silently link an identity. Explicit Connect SSO requires verified IdP email equal to canonical `users.email`.

**Verified email:** OIDC login requires `email_verified` true (boolean or string `"true"`).

**Sensitive method changes** (Connect SSO, set first local password, related high-risk flows) use short-lived hashed server-side state, session binding, OIDC state/nonce/PKCE, fresh `auth_time` (`max_age=0` where configured), Scrumboy 2FA when active, rate limits, and transactional updates. Ordinary SSO MFA is controlled by the IdP, not Scrumboy 2FA.

Operator setup detail: `[docs/oidc.md](oidc.md)`. HTTP surfaces: `[docs/authentication-api.md](authentication-api.md)`.

---



## Password handling

- Passwords are **hashed** with **bcrypt** (`bcrypt.DefaultCost`) via `golang.org/x/crypto/bcrypt` (`GenerateFromPassword` / `CompareHashAndPassword` in `internal/store`).
- Only the hash is stored in `users.password_hash`. Plaintext passwords are not persisted or logged by these paths.
- Malformed or unusable hashes fail `IsUsablePasswordHash` (for example via `bcrypt.Cost`); SSO-only accounts may have NULL hash.
- Self-service and admin reset flows issue time-bounded tokens (HMAC over user/timestamp/hash material keyed by the encryption secret); successful reset installs a new bcrypt hash and revokes sessions / pending login 2FA.
- First-password grants after sensitive OIDC step-up are short-lived, hashed, single-use, and session-bound (cookie `scrumboy_first_password_grant`, `SameSite=Strict`).
- Host `recover-owner` can set an owner local password without IdP or user 2FA; see `[docs/recovery.md](recovery.md)`.

Do not describe stored passwords as “encrypted.”

---



## Sessions and cookies


| Topic             | Behavior                                                                                                                                                                           |
| ----------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Generation        | `crypto/rand` (32 bytes), base64url cookie value (`store.CreateSession`)                                                                                                           |
| Database          | Only **SHA-256** hex of the token in `sessions.token_hash`; raw token is not stored                                                                                                |
| Cookie            | Name `scrumboy_session`; `HttpOnly`; `SameSite=Lax`; `Path=/`; `Secure` when the request is treated as HTTPS (`isSecureRequest`: direct TLS or `X-Forwarded-Proto` / `CF-Visitor`) |
| Lifetime          | Typically 30 days from creation                                                                                                                                                    |
| Multiple sessions | Supported; login does not revoke sibling sessions                                                                                                                                  |
| Revocation        | Logout deletes current session; password reset / recover-owner / related flows revoke user sessions                                                                                |


**Bounded claim:** hashing session tokens reduces risk if **only** the database file leaks. It does not help if an attacker already controls the running process or the client cookie jar.

**Operator note:** cookie `Secure` can follow forwarded HTTPS headers even when `SCRUMBOY_TRUST_PROXY` is off. Rate-limit client IP and OAuth/MCP public-origin resolution use TrustProxy more strictly—configure proxy headers carefully.

Mutating `/api/*` generally requires header `X-Scrumboy: 1` (custom-header CSRF pattern) except specific token-authenticated or form logout paths (`Server.handleAPI`).

---



## Two-factor authentication and encryption


| Topic                     | Behavior                                                                                                                                                                                             |
| ------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Mechanism                 | TOTP; recovery codes stored as bcrypt hashes                                                                                                                                                         |
| When Scrumboy 2FA applies | Local password login (pending → `/api/auth/login/2fa`); sensitive method-change finishes when the user has 2FA active                                                                                |
| When it does not          | Ordinary OIDC SSO login (IdP MFA only); `recover-owner` bypasses user 2FA and leaves 2FA configuration intact                                                                                        |
| At-rest protection        | TOTP secrets encrypted with **AES-256-GCM** (`internal/crypto`, `v1:` framing) under `SCRUMBOY_ENCRYPTION_KEY`                                                                                       |
| Key loss / rotation       | Once encrypted auth/security data exists, startup requires a valid key (`ResolveStartupEncryptionKey`). Rotating or losing the key breaks decrypting TOTP secrets and password-reset HMAC capability |


Encryption covers configured secrets such as TOTP material (and reset-token HMAC keying), **not** the whole SQLite file, board content, or session hashes.

---



## Authorization and permissions

- **System roles** (instance-wide) and **project roles** (per board) are enforced in the **store / HTTP handlers**, not by hiding UI controls.
- Maintainer+ gates membership and many board mutations; Owner/Admin gate instance user administration; anonymous/temporary boards have distinct rules and expiry.
- Access denials for boards often surface as **404** (avoid leaking existence).
- Static API tokens (`sb_…`) authenticate as the owning user for allowed API/MCP surfaces; they are not OAuth access tokens.

Full matrix: `[docs/roles-and-permissions.md](roles-and-permissions.md)`. Architecture sketch: `[docs/diagrams/scrumboy_auth_permissions.md](diagrams/scrumboy_auth_permissions.md)`.

---



## OIDC and external identity providers

Scrumboy is an OIDC **confidential client** (Authorization Code + **PKCE S256**). Token exchange and ID token validation are **server-side**; the browser receives a normal `scrumboy_session` afterward—not IdP access/ID tokens.

- State, nonce, and PKCE verifiers are short-lived **in-memory**, single-use, hashed—not written to SQLite.
- Sensitive reauthentication validates `auth_time` against state creation (skew bounded in code).
- Public base URL / redirect URI correctness is an operator concern (`SCRUMBOY_PUBLIC_BASE_URL`, proxy settings).
- Provider outage: local password (if usable) or host `recover-owner` for owners; see `[docs/recovery.md](recovery.md)`.
- Dev Keycloak notes: `[docs/keycloak/readme.md](keycloak/readme.md)` (fixture for local testing, not a production IdP endorsement).

**Operator responsibility:** IdP MFA, password policy, and account lifecycle for SSO users remain with the identity provider.

Details: `[docs/oidc.md](oidc.md)`.

---



## OAuth for MCP clients

This is **separate** from human OIDC login. Scrumboy runs an OAuth 2.1 **authorization server** for MCP clients (PKCE, Dynamic Client Registration for public clients).


| Topic              | Behavior                                                                                                              |
| ------------------ | --------------------------------------------------------------------------------------------------------------------- |
| Protected resource | Canonical resource URL `<origin>/mcp/rpc` (`publicorigin` validation)                                                 |
| Tokens             | Opaque secrets from `crypto/rand`; **SHA-256** stored; access ~1h; refresh ~30d with **rotation** and reuse detection |
| Auth codes         | Short TTL (~60s), single-use, resource-bound                                                                          |
| Where OAuth works  | Bearer on `/mcp/rpc` **only**                                                                                         |
| Where it does not  | Legacy `/mcp` and Agora accept session cookie or static `sb_…` tokens—not OAuth access tokens                         |
| DCR                | `POST /oauth/register`, rate-limited; HTTPS redirect URIs (loopback HTTP allowed)                                     |
| Token HTTP         | `Cache-Control: no-store` on token responses                                                                          |
| Cleanup            | Hourly expired OAuth artifact deletion                                                                                |


Guides: `[docs/oauth.md](oauth.md)`, `[docs/mcp.md](mcp.md)`, acceptance checklist `[docs/mcp-oauth-acceptance.md](mcp-oauth-acceptance.md)`.

OAuth access tokens are **not** interchangeable with human session cookies or static API tokens.

---



## API, MCP, and transport protections


| Surface                | Auth (summary)                                        |
| ---------------------- | ----------------------------------------------------- |
| Browser SPA / `/api/*` | Session cookie + `X-Scrumboy` on mutations            |
| Legacy `/mcp`          | Cookie or `Authorization: Bearer sb_…`                |
| `/mcp/rpc`             | Cookie, `sb_…`, or OAuth Bearer (resource-bound)      |
| Agora                  | Cookie or static token paths as implemented—not OAuth |


Additional controls include Origin checks for browser MCP JSON-RPC, JSON content-type expectations on relevant OAuth endpoints, duplicate-parameter rejection where implemented, protocol version negotiation for MCP JSON-RPC, and in-process rate limits (auth, password reset, OAuth DCR/token, sensitive method changes).

`SCRUMBOY_TRUST_PROXY` and `SCRUMBOY_PUBLIC_BASE_URL` affect client IP for limits and public origin / MCP resource URLs (`internal/publicorigin`). Misconfiguration can break OAuth discovery or weaken IP-based limits.

This section is not a full API reference—see `[API.md](../API.md)` and `[docs/mcp.md](mcp.md)`.

---



## Data at rest and persistence


| State                     | Location                                 | Notes                                                   |
| ------------------------- | ---------------------------------------- | ------------------------------------------------------- |
| Application DB            | `SQLITE_PATH` (default under `DATA_DIR`) | Board, auth, preferences, hashed secrets                |
| WAL/SHM                   | Beside the DB file                       | Copy with the DB when backing up live/quiesced files    |
| Wallpapers                | `DATA_DIR/user-wallpapers/<user-id>.jpg` | Preference JSON in SQLite; files are not in JSON export |
| Encryption key            | Environment / secret store               | Required once encrypted auth data exists                |
| Optional Mermaid override | `DATA_DIR/mermaid-semantic-edges.json`   | Only if operators deploy an override                    |


SQLite is **not** the sole source of truth for a full restore. JSON project backup/export is scoped and **omits** uploaded wallpapers, general preferences as a disaster-recovery unit, and `audit_events`. Prefer whole-`DATA_DIR` backups plus the encryption key when 2FA/reset encryption is in use.

See persistence matrix: `[docs/diagrams/scrumboy_deployment_ops.md](diagrams/scrumboy_deployment_ops.md#persistence-matrix)`, `[docs/recovery.md](recovery.md)`.

Anyone with filesystem or SQLite admin access can read board content and hashed credentials, delete or alter files outside application triggers, and run `recover-owner`.

---



## Audit trail

- Store-layer instrumentation writes `audit_events` for todo/member/project/link actions (canonical vocabulary in `[docs/audit-trail.md](audit-trail.md)`).
- Actor may be NULL for anonymous actions; metadata can include **full todo titles, project names, and tag names** on some events. Title/body **updates** often store lengths only. There is **no** “no PII” guarantee.
- SQLite triggers reject ordinary `UPDATE`/`DELETE` on `audit_events` (append-oriented). A DBA or file-level edit can still bypass that.
- There is **no** first-class UI or public HTTP read API for the audit log in the current product; inspection is via database access. Audit rows are not part of JSON backup/export.

Assignee history uses a separate `todo_assignee_events` table (also append-oriented at the SQL trigger layer).

---



## Outbound integrations and external data flow



### Web Push

- Active only when Web Push is **effectively enabled** (full mode + validated matching VAPID key pair + valid/default subscriber)—not merely because env strings are non-empty (`prepareWebPushConfiguration`).
- Assignment notifications use the Web Push protocol (encrypted to the browser endpoint). Payload fields include assignment type, fixed title text, **todo title as** `body`, `projectSlug`, `todoId`, and `scrumboyPush`. Push network operators can observe metadata consistent with Web Push; encryption is not the same as “content never leaves the instance.”
- Startup `web push: …` logging is presence-oriented; prefer `pushConfigured` / `push.state` on auth status. User permission is per browser/device.
- Details: `[docs/vapid.md](vapid.md)`, `[docs/pwa.md](pwa.md)`.



### SMTP

- Optional self-service password-reset email requires SMTP settings, encryption key, and a valid `SCRUMBOY_PUBLIC_BASE_URL` (among other gates).
- Mail is queued and retried in-process; TLS mode is configurable. Configuration validation does **not** prove the relay will accept or deliver mail.
- The relay operator can read message content (reset links). Generic API responses reduce account enumeration on request.
- Details: `[docs/smtp.md](smtp.md)`.



### Webhooks

- Maintainers configure outbound HTTP URLs (http/https with a host). Optional shared secret produces `X-Scrumboy-Signature` (`sha256=` HMAC of the body).
- Delivery uses a small retry worker and timeouts; secrets are stored as configured (not hashed like session tokens) and omitted from list API responses.
- **No** application denylist for private/link-local destinations: a maintainer-chosen URL is trusted. Treat webhook URLs as sensitive configuration (SSRF risk to internal networks if a privileged user is tricked or compromised).
- Shutdown drains in-flight mail/webhook work as implemented in process lifecycle.

---



## Security scanning and dependency monitoring


| Tool                                 | Coverage                                               | Execution model                                                                                             |
| ------------------------------------ | ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------- |
| **Snyk**                             | Go, npm, and container advisory scans when run locally | **Manual** / project-monitored badge; **no** public Actions workflow publishing results                     |
| **OSV-Scanner**                      | Advisory scanning via Google’s reusable workflows      | **CI** (`.github/workflows/osv-scanner.yml`; path filters + scheduled full scan with `fail-on-vuln`)        |
| **Trivy**                            | Filesystem scan + SARIF upload                         | **Workflow** on branch `security/trivy` only; `exit-code: 0` (does not gate `main` by default)              |
| **Dependabot**                       | gomod, npm (`internal/httpapi/web`), github-actions    | **GitHub** (`.github/dependabot.yml`; weekly/monthly schedules, cooldowns, grouped PRs)                     |
| **govulncheck**                      | Reachability-aware Go vuln analysis                    | **Manual** (`govulncheck ./...`); not wired in CI                                                           |
| **CodeQL**                           | Source-level query analysis                            | **Not configured** as `codeql-action` init/analyze; some workflows only **upload SARIF** for other tools    |
| **OpenSSF Scorecard**                | Repository / supply-chain heuristics                   | **CI** (`.github/workflows/scorecard.yml`, `publish_results: true`); README badge                           |
| **GitHub dependency graph / alerts** | Native GitHub monitoring                               | Expected when Dependabot ecosystems are enabled; exact alert UI settings are not proven by repo files alone |


Overlapping scanners matter because advisory databases, reachability analysis, transitive vs direct dependencies, test-only scopes, and container OS packages differ. A finding in one tool and a clean result in another is common.

### Interpreting scan results

- “Zero findings” means **that tool reported no applicable known issue in that scan at that time**—not that the application is free of defects.
- Scanners can disagree; triage for reachability, runtime exposure, development-only dependencies, and available fixed versions.
- Badges and point-in-time tables (including historical Snyk/`govulncheck` notes) are **not** continuous proof of security.

---



## Software supply-chain practices

Verified in this repository:

- GitHub Actions **pinned by commit SHA** with version comments (`# vX.Y.Z`).
- Workflow `permissions` generally start restrictive and escalate per job (`contents: read`, `packages: write`, `security-events: write`, `attestations: write` as needed).
- `persist-credentials: false` on Scorecard checkout; **not** set on every workflow’s checkout.
- Dependabot for Go, npm, and Actions (see above).
- DCO check workflow (`.github/workflows/dco.yml`); contributors sign off via `git commit -s` (`[CONTRIBUTING.md](../CONTRIBUTING.md)`).
- OpenSSF Scorecard workflow + published results.
- Multi-arch container publish to GHCR (`.github/workflows/docker-publish.yml`); Dockerfile base images digest-pinned.
- Windows release workflow builds artifacts, records SHA-256, and uses **SLSA-style attestations** (`actions/attest`, `gh attestation verify` documented for consumers). **Authenticode code signing is not implemented.** Docker publish does **not** attach the same attestation/provenance flow.
- Concurrency groups on some workflows (`ci`, `scorecard`, `docker-publish`); not on every workflow.

**Not evidenced from repo files alone:** branch-protection required checks/reviews (Scorecard may observe them remotely; `CODEOWNERS` does not prove enforcement).

These controls harden **how software is built and updated**, not the runtime authorization model of a deployed instance.

---



## Secure development and review practices

Verified or documented practices include:

- Automated tests around auth, OAuth, audit metadata, encryption key startup, and related flows under `*_test.go` / web tests.
- Transactional consumption of OAuth refresh tokens and sensitive grants.
- Rate limiting and malformed/unusable password-hash handling.
- Dependency update PRs via Dependabot; OSV CI on configured paths.
- DCO sign-off on contributions.
- Private vulnerability reporting via GitHub Security advisories (`[SECURITY.md](../SECURITY.md)`).

There is **no** repository-enforced rule that every change receives independent human security review.

---



## Deployment responsibilities

Operators should:

1. Terminate **HTTPS** for production; set `SCRUMBOY_PUBLIC_BASE_URL` when required (mail links, OAuth discovery behind proxies).
2. Enable `SCRUMBOY_TRUST_PROXY` **only** behind a correctly configured trusted proxy; spoofed `X-Forwarded-`* otherwise weakens IP limits and origin resolution.
3. Protect `DATA_DIR` (DB, WAL/SHM, `user-wallpapers/`) with OS permissions and backups; include `SCRUMBOY_ENCRYPTION_KEY` in secret/backup planning when 2FA/reset encryption is used.
4. Treat OIDC, SMTP, VAPID, and webhook secrets as credentials; rotate with an operational plan.
5. Harden the IdP (MFA, client secrets, redirect URIs).
6. Keep images/binaries and dependencies reasonably current; review Dependabot/OSV findings.
7. Restrict who can access the host, SQLite file, and container env.
8. Read startup warnings (SMTP, encryption key, web push presence vs status).
9. Test owner recovery on a **stopped**, backed-up volume before you need it (`[docs/recovery.md](recovery.md)`).

This is not a full environment-variable catalog—see the root `[README.md](../README.md)` config tables and feature docs.

---



## Known limitations and non-goals

- Single-node SQLite: no built-in multi-writer HA; one instance per database file.
- Host/DBA trust: application triggers and hashed tokens do not stop a privileged administrator.
- Audit storage is append-oriented in-app, **not** tamper-proof against filesystem or raw SQL privilege.
- No claim of formal security certification or third-party audit in this repository.
- Scanner coverage is incomplete relative to “every commit on every tool” (Snyk/govulncheck manual; Trivy branch-gated; no CodeQL analyze).
- External trust in IdPs, SMTP relays, browser push services, and webhook receivers.
- Webhook URLs are not SSRF-filtered beyond scheme/host parse rules.
- Some push payload / click deep-link contracts rely on manual or browser checks rather than automated field-contract tests (see ops docs).
- Historical point-in-time “0 findings” tables are snapshots, not continuous guarantees.

---



## Related documents

- `[SECURITY.md](../SECURITY.md)` — vulnerability reporting
- `[docs/oidc.md](oidc.md)` — human OIDC SSO
- `[docs/oauth.md](oauth.md)` — MCP OAuth authorization server
- `[docs/recovery.md](recovery.md)` — owner break-glass recovery
- `[docs/audit-trail.md](audit-trail.md)` — audit actions and metadata
- `[docs/roles-and-permissions.md](roles-and-permissions.md)` — role matrix
- `[docs/vapid.md](vapid.md)` / `[docs/pwa.md](pwa.md)` — Web Push
- `[docs/smtp.md](smtp.md)` — password-reset mail
- `[docs/mcp.md](mcp.md)` / `[docs/mcp-oauth-acceptance.md](mcp-oauth-acceptance.md)` — MCP and acceptance
- `[docs/authentication-api.md](authentication-api.md)` — auth HTTP surfaces
- `[docs/diagrams/scrumboy_deployment_ops.md](diagrams/scrumboy_deployment_ops.md)` — persistence / backup
- `[docs/diagrams/scrumboy_auth_permissions.md](diagrams/scrumboy_auth_permissions.md)` — auth architecture diagram
- `[CONTRIBUTING.md](../CONTRIBUTING.md)` — DCO and contribution expectations
- `[API.md](../API.md)` — HTTP/MCP API reference

