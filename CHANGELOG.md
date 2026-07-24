# Changelog

> **Upgrades:** No breaking changes in **3.7.x** / **3.8.x** / **3.9.x** / **3.10.x** / **3.11.x** / **3.12.x** / **3.13.x** / **3.14.x** / **3.15.x** / **3.16.x** / **3.17.x** / **3.18.x** / **3.19.x** / **3.20.x** / **3.21.x** / **3.22.x** / **3.23.x** unless noted below. **3.22.0** has MCP/OAuth upgrade impact — see that release.

## [3.23.2] - 2026-07-24

### Added

- **Admin-configurable default email notification preferences for new users** - Admins/owners can set an org-wide default that newly created users inherit, via `GET`/`PUT`/`DELETE /api/admin/settings/email-notify-default`. Seeding happens at creation time only and never rewrites existing users. When no override is configured, no preference row is created for new users, so an untouched instance behaves exactly as before. `DELETE` resets to the unconfigured state (`204`, idempotent). See [docs/notifications.md](docs/notifications.md).

### Changed

- **`user_preferences` provenance** - New `provenance` column (migration 059; `legacy` / `user` / `org_default`) records how each preference row was written. Existing rows become `legacy`; explicit user saves are tagged `user`; org-seeded rows are `org_default`. This is groundwork so a future bulk-apply can safely target only org-seeded rows and never overwrite user-customized ones. **Upgrade note:** remove any hand-rolled `AFTER INSERT ON users` preference trigger before upgrading — explicit-column triggers keep working, but positional `INSERT INTO user_preferences VALUES (...)` triggers become structurally invalid once the column is added. Running an older binary against a 059-migrated database is unsupported (forward-only upgrades).

## [3.23.1] - 2026-07-24

### Fixed

- **`tags.updateProjectColor` on durable projects** - The MCP tool no longer 404s for every user-owned tag on authenticated projects. Existence is checked against the same project tag set as `tags.listProject` (not board-scoped-only `GetProjectScopedTagByID`), and clearing an unset user-owned color preference is treated as a successful no-op (matching `tags.updateMineColor`). `tags.deleteProject` remains board-scoped-only.

### Documentation

- **`tags.updateProjectColor` color semantics** - `API.md` and the MCP `tools/list` description clarify that board-scoped tags update shared `tags.color`, while user-owned tags update this viewer's per-viewer preference.

## [3.23.0] - 2026-07-22

### Added

- **Opt-in email notifications** - Builds on the SMTP infrastructure added in 3.19.0. Each user can enable email notifications under Settings → Customization and choose per-category opt-in: card assigned to them and added to a project (on by default), plus card activity, sprint activity, and project/workflow/tag activity (off by default). No email sends unless both the master toggle and the relevant category are enabled for that user; the instance also needs `SCRUMBOY_SMTP_*` and `SCRUMBOY_PUBLIC_BASE_URL` configured (no `SCRUMBOY_ENCRYPTION_KEY` required). `GET /api/auth/status` reports readiness via a new `emailNotifyAvailable` flag. See [docs/notifications.md](docs/notifications.md).
- **`project.membership` domain event** - New event bus event published on project member add/remove/role-change, carrying the affected user and actor, so non-realtime consumers (like the email notifier) can target the specific member without a board-wide payload.

### Changed

- **`board.refresh_needed` payload** - Now includes a best-effort `actorUserId` (from the ambient request actor) alongside the existing `reason` string. Additive; existing SSE consumers are unaffected.

## [3.22.7] - 2026-07-22

### Fixed

- **Settings Create User dialog lifecycle** - Escape / native dismiss no longer leaves an orphaned `<dialog>` in the DOM with duplicate IDs, which previously broke Cancel, password reveal, and Create (submit closed without calling `POST /api/admin/users`). Dynamic Settings dialogs now use scoped lookups and an idempotent remove-on-close teardown; Create User also clears the temporary password on close.
- **Keybindings chord parsing** - `chordFromKeyboardEvent` null-guards missing `code`/`key` so IME or synthetic keydowns no longer throw in the capture-phase global handler.

## [3.22.6] - 2026-07-22

### Security

- **Temporary Board claim authorization (GHSA-vph4-pmmh-ch6x)** — `POST /api/board/{slug}/claim` now permits conversion only by the recorded creator of a Full Mode Temporary Board. Previously, claim authorization checked `owner_user_id`, which is null before a Temporary Board is claimed, instead of verifying `creator_user_id`; an authenticated user with the shared slug could therefore claim another user's board and lock the creator out. Anonymous Boards cannot be claimed, and conversion now uses an atomic state transition before maintainer membership is granted.

## [3.22.5] - 2026-07-21

### Changed

- **Frontend dependency upgrades** - Bump `dompurify` to `3.4.12`, `markdown-it` to `14.3.0`, `mermaid` to `11.16.0`, and `happy-dom` to `20.10.6`; sync vendored `/vendor` browser assets.

## [3.22.4] - 2026-07-20

### Security

- **Pin CI/CD dependencies** - Pin GitHub Actions and reusable OSV workflows to immutable commit SHAs, and pin Dockerfile base images (`golang:1.25.12-alpine`, `alpine:3.20`) to multi-platform manifest digests.

## [3.22.3] - 2026-07-20

### Added

- **OSV Scanner CI** - GitHub Actions workflow scans Go and npm lockfiles with OSV Scanner on dependency-path PRs, merges, pushes, and a weekly schedule.

### Changed

- **Go dependency upgrades** - Bump `golang.org/x/crypto` to `v0.54.0`, `golang.org/x/oauth2` to `v0.36.0`, `golang.org/x/term` to `v0.45.0`, and transitive `golang.org/x/sys` to `v0.47.0`; promote `golang-jwt/jwt/v5` to a direct require.

### Security

- **OSV ignore for `GO-2026-5932`** - `osv-scanner.toml` documents that Scrumboy does not import `golang.org/x/crypto/openpgp` or its affected subpackages.

## [3.22.2] - 2026-07-19

### Changed

- **Frontend test toolchain remediation** - Pin `vitest` to `3.2.6` and add a direct `vite` `6.4.3` pin for the web package; add `vitest.config.ts` so Vitest 3 fake timers match the prior Vitest 2 set (avoid faking `performance.now()`).

## [3.22.1] - 2026-07-19

### Added

- **Web Push status on auth status** - Authenticated `GET /api/auth/status` includes a `push` object (`state` / `reason`) so Settings can distinguish not-configured, invalid, unavailable, and enabled VAPID setups. Existing `pushConfigured` remains.
- **Settings Web Push diagnostics** - Owners/admins see reason-specific notices when push is disabled by bad VAPID keys, subscriber, or initialization failure; other signed-in users get a generic unavailable notice.

### Changed

- **Dependency upgrades** - Go module bumps include `webpush-go` `v1.4.0`, `go-jose/v4`, `golang.org/x/crypto`, and JWT via `golang-jwt/jwt/v5`; Docker build image is `golang:1.25.12-alpine`; frontend vendors `dompurify@3.4.11` and `markdown-it@14.2.0`; web package documents Node/npm engines and Docker publish uses `npm ci`.
- **Prepared VAPID configuration** - Server startup validates and normalizes public/private keys and subscriber together, enables the notifier only when status is `enabled`, and serves the trimmed public key from `/api/push/vapid-public-key`.

### Fixed

- **VAPID key whitespace** - Surrounding whitespace in configured VAPID keys no longer prevents enablement or leaves dirty keys in the notifier and public-key API.

### Documentation

- **Frontend toolchain** - `CONTRIBUTING.md` notes required Node versions and canonical npm for lockfile maintenance; markdown/mermaid pin notes match the vendored library versions.

### Tests

- **Web Push status and prep** - Coverage for auth-status push states/reasons, admin vs non-admin Settings notices, whitespace/padded-base64 key normalization, and a cancellable timeout around the async VAPID request capture test.

## [3.22.0] - 2026-07-18

### Breaking / upgrade impact

These are intentional compatibility breaks for remote MCP OAuth and Streamable HTTP. Cookies, static `sb_…` API tokens, human OIDC login, and DCR client registrations are unaffected unless noted.

- **Reauthorize MCP OAuth clients** - Migration `057_bind_oauth_tokens_to_mcp_resource.sql` invalidates existing unbound authorization codes, access tokens, and refresh tokens. Clear stale client credentials and complete consent again. DCR registrations remain; cookies and static API tokens keep working.
- **Point native clients at `/mcp/rpc`, not `/mcp`** - Claude Code, Cursor, and other Streamable HTTP clients must use `https://<host>/mcp/rpc` (for example `claude mcp add --transport http scrumboy https://<host>/mcp/rpc`). Legacy `/mcp` still serves the `{ "tool", "input" }` API with cookies or static tokens, but **rejects OAuth Bearers** and emits no OAuth discovery challenge.
- **Require `resource=<origin>/mcp/rpc`** - Authorize, token, and refresh requests must send exactly one absolute resource URI for the trusted public origin's `/mcp/rpc`. Missing, duplicate, or wrong values fail with `invalid_target`. Tokens minted for another audience are rejected on `/mcp/rpc`.
- **OAuth tokens are `/mcp/rpc`-only** - A valid `/mcp/rpc` OAuth access token does not authorize legacy `/mcp` or `/agora/v1/*`.
- **Drop Streamable HTTP protocol `2024-11-05`** - Supported versions are `2025-03-26`, `2025-06-18`, and `2025-11-25`.
- **Stricter `/mcp/rpc` transport and initialize** - Invalid browser `Origin` returns empty 403; media-type / Accept / protocol-version rules are enforced; accepted notifications return empty 202; authenticated GET returns 405. `initialize` requires nonempty `clientInfo.name` and `clientInfo.version`.

See [`docs/oauth.md`](docs/oauth.md), [`docs/mcp.md`](docs/mcp.md), and [`docs/mcp-oauth-acceptance.md`](docs/mcp-oauth-acceptance.md).

### Added

- **Resource-bound remote MCP OAuth** - `/mcp/rpc` is the sole OAuth protected resource. Authorization codes, access tokens, and refresh tokens are bound to the trusted canonical `<origin>/mcp/rpc` URI throughout authorization, exchange, refresh, storage, and validation. Invalid client, redirect, PKCE, or resource attempts no longer consume otherwise valid grants.
- **Path-derived protected-resource discovery** - `/.well-known/oauth-protected-resource/mcp/rpc` publishes the canonical resource and authorization server. The root RFC 9728 endpoint remains a compatibility alias with the same identity, and authorization-server metadata advertises `protected_resources`.
- **Streamable HTTP transport normalization** - `/mcp/rpc` now enforces Origin, method, JSON media, Accept, JSON-RPC message classification, and stable MCP protocol-version rules. Accepted notifications return empty 202 responses; authenticated GET returns 405 because Scrumboy remains stateless and JSON-only.

### Changed

- **Endpoint-specific MCP authentication** - Legacy `/mcp` continues to accept cookies and static `sb_…` tokens. `/mcp/rpc` accepts cookies, static tokens, and Scrumboy OAuth tokens bound exactly to its canonical resource. Agora remains cookie/static only and cannot act as a deputy for `/mcp/rpc` OAuth tokens.
- **MCP protocol versions** - Streamable HTTP advertises and accepts `2025-03-26`, `2025-06-18`, and `2025-11-25`.

### Fixed

- **Native client OAuth discovery** - Full-mode unauthenticated `/mcp/rpc` requests now return an empty HTTP 401 with a path-derived RFC 9728 `WWW-Authenticate` challenge. Invalid Bearers add `error="invalid_token"`; transport authentication failures never masquerade as successful JSON-RPC tool results.
- **MCP JSON-RPC lifecycle and parsing** - `/mcp/rpc` handles `ping`, requires nonempty `clientInfo.version` on `initialize`, rejects non-object/array `params`, and echoes recoverable request IDs on Invalid Request errors.
- **OAuth token exchange atomicity** - Authorization-code and refresh-token exchanges consume/revoke the grant and insert the new token pair in one database transaction.
- **OAuth form parse errors** - Malformed authorize/token form bodies return `invalid_request`; `invalid_target` is reserved for resource parameter problems after a successful parse.

### Documentation

- **Remote client and deployment acceptance** - Updated MCP/OAuth/API guidance to configure native clients directly with `/mcp/rpc`; added a provider-neutral release checklist using Keycloak only as Vega's documented upstream OIDC provider.

### Tests

- **Security and compatibility boundary** - Added trusted-origin/resource canonicalization, migration invalidation, non-burning concurrent grant redemption, OAuth continuation, metadata/challenge, transport, Origin, protocol-version, Agora, legacy `/mcp`, cookie/static, wrong-resource, and no-cookie-fallback coverage.

## [3.21.0] - 2026-07-18

### Added

- **Explicit SSO account linking and first Scrumboy password** - Authenticated users can connect the configured OIDC provider to a local-password account (`POST /api/auth/oidc/link/start`) or establish a first Scrumboy password after fresh SSO reauthentication (`/api/auth/oidc/set-password/*`). Sensitive flows require CSRF protection, short-lived hashed single-use server state, session binding, PKCE/nonce/`max_age=0`/`auth_time`, and Scrumboy 2FA when enabled. Migration `056_add_first_password_grants.sql` stores first-password grants.
- **`recover-owner` break-glass CLI** - Host-side owner recovery that can set or replace a local password without the IdP, without clearing 2FA configuration, and with revocation of that owner's sessions and pending local-login 2FA challenges. Does not run migrations.
- **Profile authentication method UI** - Settings → Profile surfaces effective local/SSO/dual state, owner outage warnings, **Set Scrumboy password**, and **Connect SSO**, with localized copy across public locales.

### Changed

- **OIDC login no longer auto-links by email** - Normal SSO login identifies accounts only by `(issuer, subject)`. Matching verified IdP email no longer silently attaches an OIDC identity to an existing local-password user; those users must sign in locally and use **Connect SSO**. Canonical `users.email` stays independent of mutable IdP email.
- **Local-auth-disabled gating** - When `SCRUMBOY_OIDC_LOCAL_AUTH_DISABLED` is set, local login/reset surfaces and admin password-reset for local passwords stay unavailable; User Management hides nonfunctional Scrumboy-password actions.

### Documentation

- **Human auth** - Added `docs/authentication-api.md`, `docs/recovery.md`, and `docs/security.md`; rewrote `docs/oidc.md` for method model, canonical email, Connect SSO, and first-password flows; updated `README.md`, `FAQ.md`, and `docs/smtp.md`.

### Tests

- **Auth methods / recovery** - Store and HTTP coverage for first-password grants, explicit linking, rate limits, recovery-code consumption, and `recover-owner`; frontend i18n coverage for authentication wording and Settings profile/users/backup surfaces.

## [3.20.0] - 2026-07-15

### Added

- **OAuth 2.1 authorization server for MCP clients** - Full-mode support for MCP clients that use automatic OAuth discovery, PKCE, and Dynamic Client Registration (e.g. Claude Code's `claude mcp add --transport http`). Adds RFC 8414/9728 discovery (`/.well-known/oauth-authorization-server`, `/.well-known/oauth-protected-resource`), RFC 7591 `POST /oauth/register` (public clients only), consent-gated `/oauth/authorize`, `/oauth/token` (authorization code + refresh rotation), and RFC 7009 `/oauth/revoke`. OAuth access tokens are accepted as Bearer credentials on `/mcp` and `/mcp/rpc` additively alongside existing static API tokens and session cookies. Migration `055_add_oauth_authorization_server.sql` adds `oauth_clients`, `oauth_auth_codes`, `oauth_access_tokens`, and `oauth_refresh_tokens`.

### Changed

- **`SCRUMBOY_TRUST_PROXY` consumers** - Per-IP auth rate limiting now also covers OAuth DCR (`POST /oauth/register`) and `POST /oauth/token`. As before, `X-Forwarded-For` is honored only when TrustProxy is enabled.
- **OAuth HTML `Referrer-Policy`** - Login, consent, and error pages send `Referrer-Policy: same-origin` (not `no-referrer`) so classic browser form Approve POSTs that omit `Origin` still carry a same-origin `Referer` for consent CSRF validation, while cross-origin navigations continue to withhold `Referer`. Exact-origin matching remains required, so sibling subdomains remain rejected.

### Fixed

- **OAuth consent Approve in browsers that omit `Origin`** - `Referrer-Policy: no-referrer` had suppressed the `Referer` fallback used by `oauthConsentOriginAllowed`, so legitimate consent submissions could fail with “Invalid request origin.”

### Documentation

- **OAuth for MCP** - Added `docs/oauth.md` (discovery, DCR, PKCE, authorize/token/revoke, token lifetimes, deliberately unimplemented items); linked from `README.md` and `docs/mcp.md`. Documents consent Origin/Referer CSRF checks and `Referrer-Policy: same-origin`.

### Tests

- **OAuth** - Happy-path register → authorize → consent → token → MCP call; PKCE mismatch; expired/replayed auth codes; redirect URI mismatch; anonymous-mode 404s; refresh rotation/reuse rejection; revoked-token rejection by MCP; DCR validation (malformed redirect URI, loopback HTTP allowed); strict JSON Content-Type on DCR; DCR rate limit ignores spoofed XFF by default; consent Origin/Referer matrix (including Referer-only Approve and missing-header rejection); OIDC continuation with Referer-only consent approval.
- **MCP** - Invalid Bearer (including OAuth-shaped tokens) does not fall back to the session cookie.

## [3.19.0] - 2026-07-14

### Added

- **SMTP email + self-service password reset** - New optional `SCRUMBOY_SMTP_*` configuration enables outbound email via `net/smtp`/`crypto/tls` (no new dependency). When configured, `POST /api/auth/request-password-reset` lets users request a reset link by email; delivery is asynchronous with retry/backoff, mirroring the existing webhook delivery architecture. The response is always generic to avoid leaking account existence. The admin-generated manual reset-link flow (`POST /api/admin/users/{id}/password-reset`) is unchanged and remains available regardless of SMTP configuration.
- **Forgot password UI** - Sign-in shows a Forgot password control when self-service reset is enabled (full mode, local password auth, and the SMTP/encryption/public-base-URL capability below). The form posts to `request-password-reset` and returns the same generic success copy as the API.
- **`SCRUMBOY_TRUST_PROXY`** - Auth rate-limit IP keys default to connection `RemoteAddr` and ignore client `X-Forwarded-For` unless explicitly enabled (`1`/`true`/`on`/`yes`). Enable only behind a reverse proxy that overwrites/strips client XFF. Applies to login, 2FA, and request-password-reset limiters.

### Changed

- **Self-service reset operator constraints** - Capability (`selfServicePasswordResetEnabled`) and actual email sends require valid `SCRUMBOY_SMTP_HOST` / `SCRUMBOY_SMTP_FROM` (parseable RFC 5322 address, no CR/LF; port 1–65535), `SCRUMBOY_ENCRYPTION_KEY`, and a valid `SCRUMBOY_PUBLIC_BASE_URL` origin. Missing or invalid public base URL fail closed: the request endpoint still returns the generic success body, but no email is sent and the Forgot control stays hidden. Transient SMTP failures (dial/timeouts/4xx) retry with backoff; permanent failures (5xx and local validation/config errors) are logged once and not retried.
- **Webhook/mail shutdown flush** - `Server.Close` seals the webhook and mail delivery queues (no new entries), then gives in-flight drain/retry work a bounded chance to finish against the close context before exit. Once a worker observes close-context cancellation, it starts no further queued item or send attempt (an in-flight send may still finish under its own transport timeout).
- **`request-password-reset` CSRF + Content-Type** - Same `X-Scrumboy: 1` gate as other mutating auth JSON routes (missing header → 403), plus required `Content-Type: application/json` (charset parameters allowed; wrong type → 400).

### Documentation

- **SMTP** - Added `docs/smtp.md` covering configuration, TLS modes (STARTTLS/implicit/none), public base URL / encryption requirements, startup log states, verification steps, and example providers; cross-linked from `README.md` and `FAQ.md`.

### Tests

- **Mailer** - Send success/failure/auth-failure/STARTTLS-vs-implicit-TLS coverage against a hand-rolled fake SMTP listener (`internal/mailer/mailertest`), including a header-injection guard.
- **Mail queue/worker** - Enqueue/drain, capacity drop, 3-attempt retry/backoff, and bounded shutdown flush behavior.
- **HTTP API** - `request-password-reset` enumeration-safety (identical response for existing/non-existing accounts), rate limiting, CSRF/Content-Type rejection cases, and an end-to-end test asserting a captured email contains a valid reset token/link using `SCRUMBOY_PUBLIC_BASE_URL` (admin-generated links still honor `X-Forwarded-Proto` when the base URL is unset).
- **Trust proxy** - `clientIP` ignores spoofed XFF by default; honors first XFF hop when `TrustProxy` is set.
- **Forgot password UI** - Capability-gated Forgot control, form submit/back flow, and i18n coverage for the new auth copy.

## [3.18.26] - 2026-07-12

### Added

- **Multi-architecture container images** - GHCR images now publish for both `linux/amd64` and `linux/arm64` under a single manifest, allowing Docker to select the native architecture automatically.

### CI

- **Docker publishing** - Multi-platform builds cross-compile the Go binary on the runner's native architecture and use QEMU only for target-platform runtime-stage commands.
- **GHCR package visibility** - Removed the obsolete workflow step that attempted to change the container package to private, so visibility stays managed via GitHub Package settings.



## [3.18.25] - 2026-07-05



### Fixed

- **OIDC trailing-slash issuer** - Discovery accepts the normalized configured issuer or exactly the same issuer with one trailing slash (e.g. Authentik) without disabling issuer validation; stored OIDC identities still use the canonical configured issuer.



### Documentation

- **OIDC** - Updated issuer matching, discovery, and troubleshooting notes for trailing-slash providers.



### Tests

- **OIDC** - Discovery fallback, trailing-slash login identity storage, and rejection of mismatched ID-token issuers.



## [3.18.24] - 2026-07-01



### Added

- **CI workflow** - GitHub Actions (`.github/workflows/ci.yml`) runs Go (`go test ./...`) and frontend (`npm test`) on every push and pull request to `main`, with parallel jobs and `workflow_dispatch` for manual runs.



### Tests

- **Maintenance CLI tools** - Coverage for `dbquery`, `slugfix`, `tagcheck`, and `tagrecover` (report formatting, error paths, and legacy slug/tag recovery); core logic extracted into testable `run()` helpers.
- **Auth** - Password validation rules and password-reset token generate/parse/verify flows.
- **Database layer** - `db.Open` directory creation, SQLite pragmas, connection pool limits, and migration runner idempotency with current-schema landmark checks.
- **Event bus** - Fan-out publish delivery order, context propagation, and missing-ID/timestamp defaults.
- **HTTP API** - Auth rate limiter per-IP and per-email windows, stale-entry cleanup, and email normalization.
- **Project color** - Dominant-color extraction from avatar data URLs (invalid input, PNG channels, clamping).



### Documentation

- **README** - Tests status shield linked to the CI workflow on `main`.



## [3.18.23] - 2026-06-30



### Added

- **Windows executable releases** - GitHub Actions workflow builds, smoke-tests, and attaches `scrumboy-*-windows-amd64.exe` plus a matching `.sha256` checksum to GitHub Releases on publish; manual `workflow_dispatch` runs upload build artifacts for ad-hoc testing.



### Documentation

- **README** - Quick Start section for downloading the Windows exe from GitHub Releases, runtime `./data` layout, and optional `DATA_DIR` / `BIND_ADDR` overrides.



## [3.18.22] - 2026-06-30



### Fixed

- **2FA recovery code login** - Consuming a recovery code during sign-in no longer runs the mark-used update while the recovery-code SELECT cursor is open, avoiding SQLite writer lock contention that could deadlock login.



## [3.18.21] - 2026-06-30



### Documentation

- **Architecture diagrams** - Refreshed Mermaid sources and self-contained viewer (`docs/diagrams/`) for June 30, 2026: GHCR image as recommended Docker path, 23-locale i18n and RTL (`fa`), anonymous apex landing and `/{locale}/` routes, auth overlays vs URL routes, wall REST under `/api/board/{slug}/wall`, dual SSE endpoints (`/api/me/realtime` and board events), todo-assigned publisher wiring, feature flags, and 2FA encryption-key scope.
- **README** - Link to the architecture diagram viewer from the documentation section.



## [3.18.20] - 2026-06-29



### Added

- **Apex landing locale negotiation** - In anonymous mode, `GET /` redirects (302) to a supported regional landing (e.g. `/fr/`) when `Accept-Language` matches a public locale; English and unsupported languages stay on the indexable root. Deliberate visits to `/<locale>/` are never re-redirected. Responses use `Cache-Control: private` and `Vary: Cookie, Accept-Language`.
- **Locale preference cookie** - Public locale picks in the Auth topbar and Settings → Customization now mirror `scrumboy.locale` to a cookie so server-side apex negotiation can honor an explicit choice; regional landing bootstrap still seeds `localStorage` when browser language matches and no saved preference exists.
- **Mobile landing hero mascot** - Scrumboy mascot image shown on mobile viewports for all public locale marketing pages; Playwright layout checks added for representative locales.



### Changed

- **Chinese (**`zh`**) landing page** - Hero accent, localized capabilities section title and desktop CSS so CJK hero lines do not split mid-word.
- **Japanese (**`ja`**) landing hero** - Mobile hero keeps top Hero accent on one line.



### Fixed

- **Language dropdown** - Auth topbar and Settings locale picker lists no longer clip when they exceed the viewport; shared `locale-select` positioning and overflow handling updated with regression tests.



## [3.18.19] - 2026-06-24



### Added

- **Scrumboy board operator agent plugin** - Installable plugin package under `plugins/scrumboy-board-operator` for Codex, Claude, and other compatible agent workspaces: `.claude-plugin` / `.codex-plugin` metadata, a board-operator Skill (read-first MCP and Agoragentic workflows with approval-gated mutations), and eval cases for agent harness testing.



### Documentation

- **README** - Links to the board operator plugin from the MCP and documentation sections.



## [3.18.18] - 2026-06-24



### Changed

- **Localized landing meta descriptions** - Non-English anonymous landing pages now emit each locale's `landing.meta.description` in `<meta name="description">`, `og:description`, and `twitter:description`; landing generator and server tests updated.
- **Landing meta description copy** - Refined share-preview descriptions for **bn**, **de**, **es**, **fa**, **hi**, **id**, **it**, **ms**, **pl**, **ru**, **sw**, **tr**, **uk**, and **ur** (more natural phrasing; fewer English loanwords where localized terms fit better).



## [3.18.17] - 2026-06-24



### Changed

- **Localized landing CTA cards** - Deploy and anonymous-board choice-card headings on non-English landing pages now use each locale's `landing.deploy.title` and `landing.anon.title` catalog strings; landing generator and server tests updated for all public locales.
- **Ukrainian landing page** - Localized section title and choice-card headings on the generated **uk** marketing page.
- **Deploy CTA catalog copy** - Replaced literal "deploy" loanwords with natural phrasing in several locale catalogs (`ar`, `bn`, `es`, `fa`, `fr`, `hi`, `id`, `it`, `ms`, `pl`, `pt`, `sw`, `th`, `tr`, `ur`).



### Fixed

- **Language dropdown** - Restored vertical scrolling when the locale picker list exceeds the viewport.



## [3.18.16] - 2026-06-21



### Added

- **Six new UI locales** - Full frontend catalogs for Bangla (`bn`), Swahili (`sw`), Farsi (`fa`, with RTL layout), Polish (`pl`), Ukrainian (`uk`), and Malay (`ms`), each with its flag in the public locale picker.



### Changed

- **Localized landing pages** - Generated marketing pages for **bn**, **fa**, **ms**, **pl**, **sw**, and **uk** with per-locale hero taglines and multilingual feature-card copy; landing generator registry updated for the new public locales.



## [3.18.15] - 2026-06-19



### Added

- **Wall keyboard shortcuts** - **W** on the board opens Scrumbaby wall (desktop, durable boards). **S** toggles Select/Pan canvas mode while the wall is open. **Delete** deletes selected notes with the existing confirmation prompt (single note or batch count).



### Changed

- **Wall note edit** - **Escape** commits in-place note edits (same as Enter/blur) instead of discarding changes.



### Documentation

- `docs/wall.md` - Open wall (**W**), canvas mode toggle (**S**), **Delete** on selection, and Escape commit behavior.



## [3.18.14] - 2026-06-19



### Changed

- **Localized landing SEO** - Non-root anonymous landing routes (e.g. `/de/`, `/hi/`) emit `noindex,follow` while visible copy remains mostly English; root `/` stays indexable. Noindexed locale routes do not participate in the hreflang cluster yet; `/` emits only `en` and `x-default` hreflang to the root URL.
- **Locale landing hero layout** - Desktop-only tagline line breaks for **de**, **es**, **fr**, **id**, **ru**, **th**, and **vi** (injected per-locale CSS only); **ja** native hero copy/colors plus localized section title and choice-card headings; **ur** bidi-safe three-line desktop hero; hero title stacks above the mascot on wide viewports.



## [3.18.13] - 2026-06-18



### Added

- **Localized landing pages** - Public locale routes (e.g. `/de/`, `/hi/`) serve generated marketing HTML with per-locale `lang`, canonical, and hreflang metadata; hero titles keep **Kanban Boards** in English with a locale-specific taglines.
- **Landing generator** - `landing.template.html` and `scripts/generate-landing.mjs` build `landing.html` and `landing.locales/*.html` from i18n catalogs; npm `generate:landing` / `verify:landing` wired into the web build and test scripts.



### Changed

- **Landing feature cards** - Multilingual card leads on localized pages (locale slot taglines, **i18n** on English, `earth.svg` backdrop); markdown card uses **markdown + mermaid** image and copy; footer disclaimer also disclaims Mermaid affiliation.



## [3.18.12] - 2026-06-18



### Documentation

- **Architecture diagrams** - Brought `docs/diagrams/` up to date through 3.18.x (i18n catalogs, locale picker, API error localization, encryption-key startup, field tooltips, pre-auth locale picker); viewer header date set to June 18th 2026.



## [3.18.11] - 2026-06-18



### Added

- **Italian UI** - Full frontend catalog (`it`) with Italy flag in the public locale picker.



## [3.18.10] - 2026-06-18



### Added

- **Spanish (Latin America) UI** - Full frontend catalog (`es`) with Mexico flag in the public locale picker.



## [3.18.9] - 2026-06-18



### Added

- **Hindi UI** - Full frontend catalog (`hi`) with India flag in the public locale picker.



### Changed

- **Locale flag SVGs** - Optimized all vendored flags under `internal/httpapi/web/assets/flags/` with SVGO (`--multipass --final-newline`); normalized UTF-16/BOM encodings on `cn.svg`, `kr.svg`, `tr.svg`, `pk.svg`, and `th.svg` so the assets parse cleanly. Total flag payload ~12% smaller with unchanged visuals.



## [3.18.8] - 2026-06-18



### Added

- **Urdu UI** - Full frontend catalog (`ur`) with RTL document direction and Pakistan flag in the public locale picker.



## [3.18.7] - 2026-06-18



### Added

- **Thai UI** - Full frontend catalog (`th`) with Thailand flag in the public locale picker.



### Fixed

- **Auth topbar on mobile** - Pre-auth language selector stays on the right and the Scrumboy logo on the left on narrow viewports, including Arabic/RTL, instead of inheriting board topbar flex ordering.



## [3.18.6] - 2026-06-18



### Added

- **Vietnamese UI** - Full frontend catalog (`vi`) with Vietnam flag in the public locale picker.



## [3.18.5] - 2026-06-18



### Added

- **Indonesian UI** - Full frontend catalog (`id`) with Indonesia flag in the public locale picker.



## [3.18.4] - 2026-06-17



### Fixed

- **Arabic Settings modal on mobile** - Settings dialog shell is explicitly viewport-centered on narrow RTL viewports so it stays fully visible; modal content remains RTL.



## [3.18.3] - 2026-06-17



### Fixed

- **Pre-bootstrap encryption key startup** - Invalid `SCRUMBOY_ENCRYPTION_KEY` values no longer crash startup before bootstrap when no encrypted auth/security data exists; encrypted state still requires the original valid key.
- **Windows launcher key handoff** - Hardened local launcher key resolution so invalid existing candidates reach the Go startup checks instead of failing before database-aware validation.



## [3.18.2] - 2026-06-17



### Added

- **Simplified Chinese UI** - Full frontend catalog (`zh`) with China flag in the public locale picker.
- **Korean UI** - Full frontend catalog (`ko`) with South Korea flag in the public locale picker.
- **Turkish UI** - Full frontend catalog (`tr`) with Turkey flag in the public locale picker.
- **Japanese UI** - Full frontend catalog (`ja`) with Japan flag in the public locale picker.
- **Russian UI** - Full frontend catalog (`ru`) with Russia flag in the public locale picker.
- **Arabic UI (Modern Standard Arabic)** - Full frontend catalog (`ar`) with RTL document direction and minimal shell layout fixes for locale picker, auth, and dialogs.
- **Pre-auth language selector** - Public locale picker on the auth shell (sign-in, bootstrap, 2FA, password reset) using the same shared helper as Settings.
- **SVG flag icons for language selectors** - Auth and Settings language pickers now show vendored 3x2 SVG flags (from `country-flag-icons`, MIT) instead of emoji that degrade to regional letter codes on Windows/Chromium.



### Improvements

- **Custom locale picker** - Replaced native `<select>` with a small accessible listbox (keyboard navigation, click-outside close, flag + autonym labels).
- **Browser translation opt-out** - App shell marks the document as non-translatable so Chrome/Google Translate does not double-translate Scrumboy’s own i18n UI.



### Fixed

- **Wallpaper import errors** - Preserve raw backend decode error text on wallpaper upload failures instead of replacing it with a generic localized fallback.



## [3.18.1] - 2026-06-12



### Fixed

- **Auth post-login redirect** - Bootstrap, login, and 2FA completion now redirect using the sanitized `next` path from auth state instead of a raw URL that could still carry `oidc_error` query noise after OIDC cleanup.
- **Settings dialog i18n listeners** - Dynamic settings dialogs (e.g. Enable 2FA) release their locale-change listener on native `cancel`/`close`, preventing stale relocalization after the dialog is dismissed.



## [3.18.0] - 2026-06-12



### Added

- **Multi-language UI (i18n)** - Frontend translation layer with locale catalogs for English, German, French, and Portuguese (Brazil), plus a pseudo-locale for QA. Language can be chosen in Settings or inferred from the browser; preference is persisted locally.
- **Localized surfaces** - User-facing copy across auth/login, dashboard, projects, board, settings (profile, 2FA, users, backup/Trello import, charts, workflow, sprints, tag colors, push/PWA, VoiceFlow), todo and bulk-edit dialogs, field tooltips, nav labels, and the sticky-note wall.



### Improvements

- **Locale-aware formatting** - Shared date/number helpers replace ad hoc `toLocaleString` usage (e.g. dashboard long dates, burndown charts).
- **i18n build checks** - Locale copy scripts verify catalog parity and sync `dist/` bundles during the web build.



### Fixed

- **OIDC settings drift** - Restored missing localized OIDC provider labels after settings refactor.



## [3.17.8] - 2026-06-08



### Added

- **Field hover tooltips** - Native `title` hints on agile/scrum fields across the todo dialog, bulk edit, sprint create form, workflow setup, board search and sprint filters, VoiceFlow command input, and member role picker. Copy is centralized in `field-tooltips.ts`.



## [3.17.7] - 2026-06-07



### Added

- **Architecture diagram viewer** - Interactive Mermaid viewer under `docs/diagrams/` with split-pane markdown and diagrams, color-coded category tabs, and 12 Scrumboy architecture diagrams (overview, HTTP routing, bootstrap, data model, auth, features, integrations, frontend).



### Documentation

- `docs/diagrams/` - Local static server (`serve-diagrams.bat` / `serve.py` on port 8775) so the viewer can fetch markdown over HTTP; semantic yes/no branch label coloring aligned with the SPA Mermaid helper.



## [3.17.6] - 2026-06-02



### Fixed

- **Wall edge lines in Chrome/Edge** - Shift-drag connections were created but invisible in Chromium: the edge SVG overlay lived inside the 0×0 `.wall-content` transform anchor with `width/height: 100%` (== 0), so lines never painted. The overlay now uses a non-zero box with `overflow: visible`.



### Improvements

- **Wall zoom modifier** - **Shift**+scroll is now the primary zoom control on the wall (Ctrl/Cmd+scroll and trackpad pinch still zoom).
- **Wall canvas mode preference** - The Select/Pan toggle is remembered globally in the browser and applies to every project's wall.



### Documentation

- `docs/wall.md` - Shift+scroll zoom, global canvas-mode preference.



## [3.17.5] - 2026-06-02



### Improvements

- **Wall canvas mode** - Added a Select/Pan toggle for touch-screen navigation: Select keeps marquee drag, Pan lets empty-canvas mouse/touch drag move the wall and supports touch pinch zoom.



### Documentation

- `docs/wall.md` - Select/Pan mode toggle and touch pinch zoom.
- `docs/wall-viewport-manual-checklist.md` - Manual sign-off for canvas mode, touch marquee, pan swipe, and pinch zoom.



## [3.17.4] - 2026-06-02



### Added

- **Wall pan and zoom** - The sticky-note wall is now an infinite canvas (Mural-style): scroll to pan, Ctrl/Cmd+scroll (or trackpad pinch) to zoom, middle-drag or Space+drag to pan. A **fit view** control (⊡ button or **F**) recenters on all notes. Pan/zoom is remembered per board in the browser (`localStorage`); no server or data migration changes—existing note positions are unchanged at the default view.



### Improvements

- **Wall coordinates** - Notes can be placed at negative canvas coordinates (matching the server’s ±100000 bound). Drag, resize, marquee select, edge preview, and create-at-pointer all use a shared screen-to-canvas transform so gestures stay correct at any zoom.
- **Wall keyboard pan** - **Arrow keys** pan the canvas (hold **Shift** for larger steps), complementing scroll-wheel, middle-drag, and Space+drag for users without horizontal scroll or a middle button. Suppressed while editing a note or when focus is in an input/button.



### Fixed

- **Wall fit view** - Fit-to-notes can zoom out below the manual zoom floor (`FIT_ZOOM_MIN`) so widely spread notes actually fit on screen; manual zoom still bottoms out at 0.2× for legibility.
- **Wall viewport persistence** - Saved and loaded pan/zoom clamp pan against the stored zoom (not a stale module zoom), so reload after low-zoom sessions restores a consistent view.
- **Wall pan while closing** - Middle-drag and Space+drag document listeners are torn down if the wall closes mid-gesture, preventing ghost panning or surprise viewport state on the next open.
- **Wall wheel pan on Firefox** - Scroll-wheel deltas are normalized for line/page `deltaMode` so pan and zoom speed match pixel-mode wheels in Chromium.



### Documentation

- `docs/wall.md` - Pan, zoom, and fit-view controls.
- `docs/wall-viewport-manual-checklist.md` - Manual browser sign-off checklist for pan/zoom (real-browser verification; Vitest/jsdom alone is not sufficient for layout transforms).



## [3.17.3] - 2026-06-01



### Changed

- **Temporary board lifetime** - Link-expiring boards now use a **90-day** rolling `expires_at` window (`TemporaryBoardLifetimeDays`), applied at creation, on import paths, and when `UpdateBoardActivity` refreshes activity (throttled to once every 5 minutes). Previously the window was 14 days.



### Fixed

- **Expired temporary boards** - Once `expires_at` is in the past, board reads and mutations return **404** until the project row is removed, including todo/tag routes and import-into-board targets. Rename and claim already refused expired boards; other paths now match.



### Improvements

- **Expiration cleanup scope** - Comments and operator docs clarify that `DeleteExpiredProjects` removes every expired temporary board (anonymous and authenticated), not only unowned paste boards.



### Tests

- **Temporary board expiration** - Store and HTTP coverage for 90-day initial expiry, rolling `UpdateBoardActivity` refresh, blocked anonymous project delete, expired-board **404**s, import replace forbidden in anonymous mode, anonymous-mode PATCH rename rules, authenticated temp cleanup, todo cascade on expiry, and append-only `audit_events` rows surviving project deletion.



### Documentation

- `FAQ.md` - New **“What is a temporary board?”** entry (sharing, 90-day rolling expiry, activity refresh).
- `docs/roles_and_permissions.md` and `docs/audit_trail.md` - Expiration wording and `audit_events` retention vs project cleanup.



## [3.17.2] - 2026-05-29



### Fixed

- **Dashboard sprint split** - Corrected SQL placeholder argument order for assigned sprint/backlog counts.



### Tests

- **Dashboard summary** - Coverage for unassigned work, active sprint assignment, and planned sprint backlog bucketing.



## [3.17.1] - 2026-05-28



### Fixed

- **Web Push / VAPID discovery** - Exposes `pushConfigured` on `GET /api/auth/status` so the SPA can gate auto-subscribe and Settings without probing `/api/push/vapid-public-key`. Push is enabled only in full mode with both VAPID keys set; partial or anonymous-mode key pairs are ignored.
- **Docker deployments** - `docker-compose.yml` forwards `SCRUMBOY_VAPID_`* and `SCRUMBOY_DEBUG_PUSH` into the container environment.



### Improvements

- **Startup logging** - Server logs whether Web Push is enabled, disabled, partially configured, or blocked by anonymous mode.



### Tests

- **Auth status / push config** - Coverage for `pushConfigured` across full, partial, and anonymous VAPID setups.
- **Router / Settings** - Auto-subscribe gating and Settings push UI use auth status instead of live VAPID endpoint probes.



### Documentation

- `docs/pwa.md`, `README.md`, `API.md`, and `scrumboy.env.example` - Docker verification steps and VAPID env wiring.



## [3.17.0] - 2026-05-28



### Features

- **Todo notes Mermaid preview (Phase 1B)** - Optional Mermaid rendering for fenced ````mermaid` blocks inside the existing todo Notes **preview** tab. Mermaid stays preview-only: notes still persist as raw `todos.body`, and board cards, notifications, exports, and server payloads do not render Markdown or diagrams.



### Improvements

- **Layered feature gates** - Added `SCRUMBOY_MERMAID_NOTES_ENABLED` as a Markdown sub-flag. Mermaid is active only when both `SCRUMBOY_MARKDOWN_NOTES_ENABLED=1` and `SCRUMBOY_MERMAID_NOTES_ENABLED=1` are set.
- **Preview safety** - Mermaid lazy-loads from a vendored runtime, initializes once with `securityLevel: "sandbox"`, strips user `%%{init: ...}%%` / `%%{initialize: ...}%%` directives before render, and falls back to escaped source for diagram-specific failures instead of dropping the user out of preview mode.



### Frontend

- **Markdown preview pipeline** - Mermaid fence placeholders are resolved after the existing Markdown sanitization step, keeping the normal allow-list unchanged for non-Mermaid Markdown.
- **Runtime loading** - Ships `/vendor/mermaid.min.js` through the vendor sync/verify pipeline, but does not eagerly load or service-worker precache Mermaid.



### Tests

- **Server/config** - Added coverage for `SCRUMBOY_MERMAID_NOTES_ENABLED` parsing and `mermaidNotesEnabled` on auth status responses.
- **Preview rendering** - Added Mermaid fence rendering, directive stripping, fallback behavior, and stale async rerender cancellation coverage.



### Documentation

- **Operator docs** - Updated `docs/markdown&mermaid.md`, `FAQ.md`, and `scrumboy.env.example` for Markdown + Mermaid preview enablement.



## [3.16.2] - 2026-05-26



### Improvements

- **Shared confirm and prompt dialogs** - `showConfirmDialog` and new `showPromptDialog` use the app `dialog` styling (header, footer, danger confirm) with intent-based close handling so outside-click dismissal and programmatic `dialog.close()` resolve to the correct choice instead of false negatives.
- **Todo dialog unsaved changes** - Closing the todo editor (X, Escape, outside click, or app-level close) prompts to discard when title, notes, tags, status, estimation, assignee, or sprint differ from the snapshot taken when the dialog opened.
- **Settings workflow tab** - Switching away from Workflow with a dirty lane draft prompts to discard unsaved changes before re-rendering.
- **Project rename** - Board and Projects rename flows use `showPromptDialog` instead of the browser `prompt()`.
- **Member management** - Demote and remove-member actions on the board use `showConfirmDialog` instead of `window.confirm()`.



### Frontend

- **Modal outside click** - Backdrop/outside closes dispatch a cancellable `scrumboy:dialog-request-close` event so dialogs with dirty-state guards can intercept close attempts.



### Tests

- **Todo close guard** - Dirty detection, discard/cancel paths, and interaction with the close-request event.
- **Utils dialogs** - Confirm/prompt intent resolution and `confirmDelete` wrapper.
- **Projects** - Rename prompt and delete confirmation wiring.
- **Modal outside click** - Close-request event behavior.



### Tooling

- `check-delete-confirms` - CI guard now rejects raw `alert()`, `confirm()`, and `prompt()` in maintained frontend sources (not only delete confirms).



## [3.16.1] - 2026-05-26



### Fixed

- **Real burndown chart (sprint domain)** - Sprint charts use UTC day boundaries for the time axis and subtitle range, filter sprint points with half-open day intervals, and show clear fallback copy when data is empty, sprint-scoped but unusable, or only a single sample (instead of mounting a misleading one-point chart).
- **Settings → Charts burndown** - Sprint list cache invalidates when the active board slug changes, so burndown navigation and sprint-scoped fetches stay aligned after switching projects.
- **Board realtime refresh during drag** - SSE-driven board reloads no longer schedule a forced refresh while a card drag is active, and drag-in-progress always defers (never force-flushes) pending refreshes, fixing intermittent freezes when moving notes between lanes.



### Tests

- **Burndown** - UTC sprint bounds, single-sample fallback, and mount guard behavior.
- **Settings charts** - Sprint cache keyed by slug and default sprint index selection.
- **Board realtime** - Drag vs non-drag guard deferral and force-timer behavior.



## [3.16.0] - 2026-05-15



### Features

- **Todo notes Markdown preview (Phase 1)** - When `SCRUMBOY_MARKDOWN_NOTES_ENABLED=1` (also accepts `true` / `on` / `yes`), the todo dialog Notes field gains **markdown** / **preview** tabs with a sanitized Markdown preview (headings, emphasis, lists, blockquotes, inline/fenced code, horizontal rules, and safe `http` / `https` links). Todo **Title** and board card titles stay plain text; notes still persist as the raw `todos.body` string with no schema changes.
- **Auth status flag** - `/api/auth/status` and bootstrap auth payloads expose `markdownNotesEnabled` so the UI only shows preview controls when the server has opted in.



### Improvements

- **Markdown preview hardening** - Preview rendering uses **markdown-it** with HTML disabled, **DOMPurify** with a tight tag/attribute allow-list, image syntax rendered as escaped text (no inline images), stripping of `iframe` / `object` / `embed` / `script`, and link filtering that allows `http` / `https` plus safe relative/hash/query links while rejecting protocol-relative URLs and non-web schemes (`javascript:`, `data:`, `mailto:`, `tel:`, etc.). External links get `rel="noopener noreferrer"` and `target="_blank"`.



### Frontend

- **Vendor assets** - Ships `/vendor/markdown-it.min.js` and `/vendor/purify.min.js` (eager-loaded, service-worker precached) with `verify-vendor` / `sync-vendor` scripts to guard missing browser dependencies.



### Tests

- **Markdown preview** - Supported subset rendering, safe vs rejected link schemes, and neutralization of raw HTML, dangerous links, and image syntax.
- **Todo dialog** - markdown/preview tab behavior gated on `markdownNotesEnabled`.
- **Server/config** - `SCRUMBOY_MARKDOWN_NOTES_ENABLED` parsing and `markdownNotesEnabled` on auth status responses.



## [3.15.4] - 2026-05-05



### Features

- **Trello import (v1)** - Import Trello boards into Scrumboy from exported Trello JSON so you can migrate existing projects without recreating cards by hand.



## [3.15.3] - 2026-05-05



### Improvements

- **Windows launcher key resolution** - Added a launcher helper flow to resolve and apply `SCRUMBOY_ENCRYPTION_KEY` from supported local sources for `win_run_full.bat` and `win_run_anonymous.bat`.



### Documentation

- **README and env example alignment** - Updated docs and examples to match the launcher-based encryption key guidance for local Windows runs.



## [3.15.2] - 2026-05-03



### Improvements

- **Anonymous mode landing (**`/`**)** - Replaced `web/landing.html` with redesigned page.
- **Landing assets** - Optimized image file sizes for `web/` resources used by the anonymous landing page (smaller JPEG/PNG payloads embedded in the binary).
- **Settings → Customization** - **VoiceFlow** is hidden when `/api/auth/status` is unavailable (**anonymous server mode**), aligned with wallpaper/profile gating so users do not see a preference that does not apply to anonymous boards.



### Frontend

- `dist/dialogs/settings.js` - Kept in sync with `modules/dialogs/settings.ts` for the VoiceFlow customization conditional.



## [3.15.1] - 2026-04-26



### Improvements

- **Agora (HTTP edge for MCP)** - `POST /agora/v1/invoke` requires an `arguments` field (**400** / `missing arguments` when absent). `arguments: null` normalizes to empty tool input where applicable. The outer invoke JSON always includes `arguments`. JSON-RPC `error.data` from MCP is preserved through the Agora adapter envelope.



### Documentation

- **Agoragentic** - `docs/agoragentic.md` (expanded guide), `docs/examples/agoragentic-manifest.json`, and a short pointer from `docs/mcp.md`.



### Tests

- **Agora** - Missing `arguments`, null `arguments`, JSON-RPC error `data` passthrough, stable discover envelope shape (`ok` / `result` / `error`), and array-shaped structured MCP results.
- **MCP** - Todo sprint patch schema and behavior in `adapter_test`, `jsonrpc_test`, and `todos_tools_test`.



## [3.15.0] - 2026-04-23



### Additions

- **Agora (HTTP edge for MCP)** - `POST /agora/v1/discover` and `POST /agora/v1/invoke` delegate in-process to the same MCP JSON-RPC path as `POST /mcp/rpc`, with an adapter outer envelope, JSON 404/405 for the `/agora/v1` namespace, and header auth passthrough. Wired ahead of the MCP route in the HTTP server.



## [3.14.5] - 2026-04-22



### Enhancements

- **Wall (Scrumbaby)** - **Right-click** a sticky note opens an in-dialog context menu: **Create Todo from Note** (opens New Todo with the note text seeded into the title field) and **Delete** (same confirmation pattern as drag-to-trash). Menu is mounted under the wall dialog and cleans up on every exit path via the wall `AbortSignal`.
- **Wall (Scrumbaby)** - **Multi-select delete** behavior is clearer: labels and prompts reflect how many notes are targeted; **right-click on a note that is not part of the current selection** deletes only that note without disturbing the rest of the selection; trash-drop and post-delete selection clearing align with the new flows.
- **Todo dialog** - `openTodoDialog` accepts optional `initialTitle` in create mode; seed text is collapsed to a single line (whitespace normalized) and respects the title input `maxLength` when set.



### Documentation

- `docs/WALL.md` - Documents the note right-click menu and distinguishes it from drag-to-trash delete.
- **README** - Adds a short **Sticky-Note Wall** bullet under Features (links to `docs/WALL.md`).



### Tests

- `wall-note-context-menu`, expanded `wall-interactions` / `wall-gesture-matrix`, and `todo-initial-title` coverage for the new behavior.

---



## [3.14.4] - 2026-04-22



### Improvements

- **Wall (Scrumbaby)** - SSE `wall.refresh_needed` handling is debounced in `wall-realtime` so bursts coalesce into a single `refetchDoc`/apply within a short window. While a local drag is in progress, the debounce re-arms via `isDragActive` / `setDragActive` (`wall-state`, `wall-drag-controller`) so the client does not refetch mid-drag; `wall` teardown clears the drag flag if the dialog closes during a drag.
- **Wall (Scrumbaby)** - After a successful wall document fetch, simple single-note field updates against an unchanged note and edge id set apply incrementally via `updateNoteElement` instead of wiping `innerHTML` and rebuilding the whole surface. Reduces hitching and visible blink after text saves when the server echoes `refresh_needed`. Full rebuild remains the fallback for structural changes. Covered by `wall-incremental-apply.test.ts`.



### Fixes

- **Wall (Scrumbaby)** - Right-click on a note for delete confirmation no longer arms the delayed single-click color cycle (primary-button guard before `armNoteInteraction`, defensive `cancelColorTimer` on the note `contextmenu` path). Adds a gesture-matrix test that replays `pointerdown` and `pointerup` with button 2 around `contextmenu`.

---



## [3.14.3] - 2026-04-22



### Improvements

- **Wall (Scrumbaby)** - Phase 1 drag transient coalescing: during multi-note drag, `wall-drag-controller` stores per-note positions each frame and drives a single group coalesce timer (`DRAG_TRANSIENT_COALESCE_MS`, 150ms) that flushes one `POST /wall/transient` per moved note when due, instead of per-participant per-`rAF` `scheduleTransient` calls. `TRANSIENT_COALESCE_MS` (100ms) is unchanged for drag-end and other callers (existing assertions preserved). Pointer-up clears any pending group timer before the existing drop-path `scheduleTransient` + flush so the final-position sequence stays the same.

---



## [3.14.2] - 2026-04-22



### Improvements

- **Wall (Scrumbaby)** - Wall client code split into focused modules (state, selection, API, drag/resize, realtime, edit controller) for easier maintenance and safer future changes.
- **Wall (Scrumbaby)** - Failed `POST /wall/transient` calls are counted and logged on a throttle; set `window.__scrumboyWallDebug = true` to surface `console.warn` for operator debugging without spamming normal sessions.
- **Tests** - Broader wall gesture and modal regression coverage (including confirm-dialog close paths and outside-click behavior).



### Fixes

- **Delete confirmation dialogs** - `showConfirmDialog` resolves reliably on every close path (including programmatic close), so follow-up flows such as drag-to-trash on desktop are not left with a hung promise.
- **Wall dialog** - Fullscreen wall uses an explicit modal contract (`data-dialog-content-root`, `data-no-outside-close`) so global outside-click handling does not treat in-canvas clicks as “outside” the dialog.

---



## [3.14.1] - 2026-04-21



### Enhancements

- **Wall (Scrumbaby)** - UX refinements on the sticky-note wall: visual branding polish and clearer wall guidance/instructions so note controls are easier to discover.



### Fixes

- **Delete confirmation dialogs** - Confirmation behavior is now wired consistently across the app; wall interactions include explicit right-click delete confirmation flow updates.

---



## [3.14.0] - 2026-04-21



### Enhancements

- **Wall (Scrumbaby)** - Sticky-note wall for **durable** project boards: full-viewport canvas, move and resize notes, single-click color cycle, double-click edit, right-click empty canvas to create, Shift-drag connections between notes, marquee and Ctrl/Meta multi-select with group drag and batch trash delete, real-time updates for collaborators. Desktop topbar entry; optional instance opt-out with `SCRUMBOY_WALL_ENABLED`. Controls summarized in `docs/WALL.md`.

---



## [3.13.1] - 2026-04-20



### Enhancements

- **VoiceFlow** - More reliable spoken **todo-by-title** handling: deterministic title references, clearer disambiguation, improved resolution (including title-target alternatives and suffix/number normalization in titles), with expanded coverage.

---



## [3.13.0] - 2026-04-20



### Features

- **VoiceFlow (voice commands)** - Board microphone opens a command modal supporting **create / move / assign / delete / open todo**, with Safe-Mode review and Hands-Free speech execution. Commands are parsed deterministically: speech alternatives are arbitrated into a single canonical command (or rejected as ambiguous), and spoken IDs like “number one” normalize to `1` before resolution/execution.

---



## [3.12.0] - 2026-04-16



### Improvements

- **Maintainability** - Backend router split into resource-focused files; frontend settings, todo dialog, and board modules decomposed behind stable facades; characterization tests added for extracted seams and key board routes.

---



## [3.11.10] - 2026-04-07



### Improvements

- **Board activity** - `UpdateBoardActivity` uses a single conditional `UPDATE` (throttled `last_activity_at`, rolling `expires_at` when expiring) instead of read-then-write. A missing project `id` still returns `ErrNotFound`; throttled calls return nil.

---



## [3.11.9] - 2026-04-07



### Improvements

- **Board reads (durable projects)** - Full board loads (`GetBoard`, `GetBoardPaged`, including the high-card-count per-lane path) and **MCP** `board.get` no longer call `UpdateBoardActivity` when `expires_at` is unset. **Expiring** boards keep the same throttled `last_activity_at` **/ rolling** `expires_at` behavior on those reads; **lane-only** pagination was already unchanged. Todo mutations still refresh activity as before.



### Tests

- `internal/store` - `TestDurableBoardRead_DoesNotRefreshLastActivityAt`, `TestExpiringBoardRead_RefreshesLastActivityWhenStale`.
- `internal/httpapi` - `TestGetBoard_ActivityTrackingBestEffort` asserts the `backlog` column key in board JSON (workflow `column_key`), not legacy `BACKLOG`.

---



## [3.11.8] - 2026-04-07



### Fixes

- **Board - lane pagination** - `ListTodosForBoardLane` now derives `nextCursor` **from the last returned row** after trimming to `limit` (aligned with `flushLane`), fixing skipped rows, duplicate IDs across pages, and incorrect **filtered drag/drop** boundary fetches that used `limit=1` with the lane cursor.
- **Assignments - notifications** - `todo.assigned` SSE includes `projectSlug` (from the project row already loaded on assignee create/update; no extra DB round trip). Client uses centralized `resolveNotificationProjectSlug` (map → catalog → event) with **persisted row reconciliation** when the catalog slug changes.



### Tests

- `internal/store` - Pagination boundary contract, tag-filtered multi-page invariants, and related lane tests.
- `internal/httpapi` - Eventbus regression asserts `projectSlug` on wire; `TestAPI_BoardPagedAndLaneEndpoint` uses canonical `backlog` column keys in JSON and lane URL.
- `internal/httpapi/web` - Vitest for `resolveNotificationProjectSlugCore`.

---



## [3.11.7] - 2026-04-05



### Fixes

- **Router (full mode)** - Logged-out visitors opening a **board URL** (`/{slug}`) are no longer sent to the **login UI** before the app loads the board. The client-side gate still applies to `/projects` and the **dashboard** only; board access for anonymous users (e.g. shareable **temporary** boards) is enforced by `GET /api/board/{slug}` as before.

---



## [3.11.6] - 2026-04-05



### Fixes

- **Board (mobile)** - Lane tab **rounded corners** apply to **all** workflow keys via a generic `.mobile-tab` rule (not only the five built-in `[data-tab="…"]` presets). **Workflow color** changes from Settings update **mobile tabs and tab drop zones** on in-place board refresh (shared `mobile-lane-tabs` helpers); `initDnD` runs after the strip is final so Sortable targets stay valid. In-place sync uses **key→element maps** instead of repeated DOM scans.
- **Board (workflow keys)** - Client fallback `columnsSpec()` and default **lane meta / mobile tab** state use the same **canonical column keys** as the store/API (`backlog`, `doing`, etc.). **Legacy** `mobileTab_`* **localStorage** values (uppercase) map to current keys. `.card--doing` matches API todo `DOING` border styling; optimistic drag styling maps `doing` → existing `in_progress` card classes.



### Improvements

- **Board** - `data-tab` **/** `data-status` **/** `data-column` (and similar) use `escapeHTML` on lane keys in full render and incremental updates for parity with rebuild paths.
- **Dashboard** - **Load more**: mobile uses the same **▼** affordance as board lanes (centered); desktop keeps a ghost **Load more** button; `aria-busy` and clearer `aria-label`; glyph as explicit **Unicode** (`\u25BC`) in source.



### Tests

- `internal/httpapi/web_assets_test.go` - Asserts **CSS** preset selectors match canonical default keys, `columnsSpec` keys, `buildMobileTabsInnerHtml` structure (tabs + drop zones), board sync helpers, and `.card--doing`. Comments note these are **embedded-source checks**, not browser/E2E coverage (manual QA still required for DnD and workflow mutations).

---



## [3.11.5] - 2026-04-05



### Features

- **Wallpaper** - Optional built-in image at `/wallpapers/default.jpg`: empty preference tries to load it; if the file is missing or fails to load, wallpaper stays **off** (no bundled placeholder). **Builtin** mode is client-only in **localStorage**; server prefs remain **off** / **color** / **image** as before.
- **Settings → Customization** - When a wallpaper is active, the Settings dialog uses a lighter **backdrop** and a slightly translucent panel so the same background shows through; tuned for readability (stronger panel and backdrop than the first pass).



### Improvements

- **Board** - Lane **column** backgrounds use a light `color-mix` tint from each workflow lane’s `color` when the API provides it (`col--lane-tint` / `--lane-accent`), so custom lane keys and themed projects match the header again-not only the five fixed `data-column` CSS rules in light mode.

---



## [3.11.4] - 2026-04-05



### Features

- **Assignments - notification panel** - The bottom-right badge **toggles** an inbox panel (`#global-notification-panel`) instead of clearing the count on click. **localStorage** list `scrumboy_notifications_v1_{userId}` stores up to **100** assignment rows (prepend, dedupe by event **id** or **projectId + todoId + type**), with **read/unread** state and **“Mark all as read”**. Rows open `/{slug}?openTodoId={id}` via the SPA router when a project slug is known; slugs are filled from the existing **projects** cache (dashboard / project list / board load) or resolved on demand when needed.
- **Web Push (PWA)** - **Service worker** `notificationclick` opens `/{projectSlug}?openTodoId={todoId}` when the push payload includes both fields (otherwise `/`), focusing an existing window and using `WindowClient.navigate` when supported.



### Improvements

- **Assignments - performance** - Inbox updates stay off the realtime hot path: **no** `GET /api/projects` during `todo.assigned` handling; **debounced** persistence and `notifications:updated` emissions reduce **localStorage** and UI churn during bursty SSE. Legacy `incrementUnread()` / `scrumboy_unread_v1_` remain for migration; the badge count is driven by **unread rows in the inbox list**.

---



## [3.11.3] - 2026-04-05



### Features

- **Board (mobile)** - When a todo drag starts, lane tabs briefly flash (**300ms**) so it is obvious they accept drops; tab labels stay readable above the drop overlays.
- **Web Push (PWA)** - After sign-in, the client auto-subscribes when **both** VAPID keys are set on the server; `SCRUMBOY_PUSH_BY_DEFAULT_IF_VAPID` removed (VAPID presence is the operator signal). Per-user autosub progress in **localStorage** with resilient retry when the permission prompt is dismissed vs blocked.



### Fixes

- **Board (drag-and-drop)** - Success toast **“Todo moved to …”** only when the todo changes **lane**; same-lane reorder no longer shows a redundant toast (lane titles still come from the board workflow, not hardcoded names).



### Improvements

- **Settings → Customization** - **Background notifications (PWA)** is grayed out with a one-line notice when Web Push is unavailable (no VAPID on the server, or anonymous board mode).



### Documentation

- `docs/mcp.md` - MCP documentation added/expanded.
- `docs/pwa.md` - Push flow and env vars aligned with streamlined enablement; key generation note includes **[VapidKeys.com](https://vapidkeys.com/)**.

---



## [3.11.2] - 2026-04-04



### Fixes

- **Web Push (PWA)** - `notificationclick` focuses an existing same-origin app window or opens `/`; no navigation by `projectSlug` / `todoId` (payload fields kept for a future notification center). `focus()` that does not return a client still falls through to `openWindow('/')`.
- **Assignment chime (mobile)** - `notify.mp3` added; `assignmentNotify` uses `<audio><source>` with **MP3 first** and **Ogg** second so **iOS Safari** (no Vorbis/Ogg decode) can play the sound. Toast and unread badge behavior unchanged.



### Improvements

- **Web Push API** - `GET /api/push/vapid-public-key` and `POST /api/push/subscribe` return **503** when VAPID is incomplete (either public or private key missing). `DELETE /api/push/unsubscribe` unchanged so rows can still be removed if keys are later disabled.
- **Router (anonymous mode)** - Initial load no longer calls `unsubscribeFromPush` (push is unavailable in anonymous mode; avoids pointless local churn).



### Other

- **README** - VAPID-related env table dashes normalized (encoding-safe).
- **Dependencies** - `github.com/SherClockHolmes/webpush-go` listed as a direct module dependency; `go mod tidy`.
- **Comments** - `router.ts`: logged-out push cleanup is best-effort per device; server DELETE may fail after auth teardown; stale DB rows are pruned when send fails.
- **Tests** - `internal/httpapi/push_routes_test.go`, `push_notify_test.go` for push routes and notifier edge cases.

---



## [3.11.1] - 2026-04-04



### Fixes

- **Project list** - Invited users now see **authenticated** temporary boards (with a creator) they belong to via `project_members`. The membership branch does not apply when `creator_user_id` is null, so anonymous paste boards never appear from stray membership rows alone.
- **Todo dialog (roles)** - **Viewers:** read-only title, status, body, links; Save off; “View Todo” when nothing to save. **Contributors:** title and status locked (body-only when assigned, same as API). Submit handler checks permissions; viewers no longer enter bulk-select via Ctrl/Cmd+click on cards.



### Other

- **Keycloak (local dev)** - `docs/keycloak/realm-scrumboy-local.json` import + `docs/keycloak/README.md` (issuer env, public-client secret placeholder).
- **Tests** - `internal/store/list_projects_test.go` for temp-board listing.

---



## [3.11.0] - 2026-04-04



### Features

- **App-wide realtime (full mode)** - `GET /api/me/realtime` merges the user hub stream with `hub.Subscribe` for every project from `ListProjects` (one `EventSource` while logged in). `Hub` adds `SubscribeUser` / `EmitUser`; `sseBridge` duplicates `todo.assigned` to the assignee’s user channel (same JSON as the project emit). Wire events include stable `id` for client dedupe; `refresh_needed` from the assignment path uses a distinct composite id so it does not collide with the assignment payload.
- **Frontend** - `core/realtime.ts`: global stream, `seenEvents` dedupe before side effects, `emit('realtime:event')`. Logged-in boards listen on the bus only (no per-board `EventSource`); anonymous boards keep `/api/board/{slug}/events`. Strict rule: never both connections at once.
- **Unread badge** - `core/notifications.ts`: count, optional per-user `localStorage`, `#global-notification-badge` (bottom-right), `notifications:updated` bus; increments only after dedupe and assignee match; skips increment when already on that project’s board; clear on badge click; hydrate/clear on user change in `router.ts`.



### Other

- **Settings / Customization** - Desktop notification status copy uses a regular hyphen after **Enabled** (was an em dash). Assignment badge hover `title` / `aria-label`: *N todos have been assigned to you* (singular phrasing for count **1**).

---



## [3.10.0] - 2026-04-04



### Features

- **Event bus + SSE** - `internal/eventbus` fanout; `PublishEvent` on the server. Board refresh / members events go through the bus; `sseBridge` keeps the same SSE JSON as before.
- `todo.assigned` - Published after commit from `CreateTodo` / `UpdateTodo` when assignee changes (non-anonymous temp boards). SSE uses reason `todo_assigned`; handlers skip duplicate `todo_created` / `todo_updated` refresh when `AssignmentChanged`.
- **Webhooks (full mode)** - `POST` **/** `GET` **/** `DELETE` `/api/webhooks` (maintainer, session; **404** in anonymous mode). Migration **050**; optional HMAC `X-Scrumboy-Signature`; async queue + worker, retries, JSON envelope with event `id` (for idempotency). Dispatcher enqueues in a goroutine with a detached context so SSE is not blocked.



### Fixes

- **Shutdown** - HTTP `Shutdown` before cancelling the webhook worker.
- **CreateTodo** - Same `!isAnonymousBoard` gate as `UpdateTodo` for assignment events.



### Other

- Tests: `eventbus_regression_test.go`. Docs: README webhooks section + TOC. Dep: `github.com/google/uuid`.

---



## [3.9.4] - 2026-04-04



### Fixes

- **OIDC / SSO - account linking for existing users** - When a user signs in with **Continue with SSO** and the IdP returns a **verified** email that already matches a `users` row (e.g. bootstrap owner or admin-created account from before OIDC), Scrumboy now **links** the `(issuer, subject)` identity in `user_oidc_identities` to that user instead of failing with a duplicate-email conflict. Local password hashes are unchanged; SSO and password login can both work for the same account when local auth remains enabled. Integration test `TestOIDCAutoLinkExistingUser` covers the full callback flow; the test **fake IdP** now relays `nonce` from authorize → token so end-to-end OIDC tests match real providers.

---



## [3.9.3] - 2026-04-05



### Improvements

- **Board search (Escape)** - While the search field is focused, **Esc** blurs it and, when there is text, clears the query using the same path as the clear control (`setSearchParam("")` + board reload). Escape handling runs **before** the global modal gate so search dismisses consistently.
- **Settings** - **Tab** cycles the visible settings tabs (wrapped); **Shift+Tab** is left for normal focus. Tab switching goes through a single `switchSettingsTab` helper (workflow dirty confirm, cache invalidation, re-render). Sprints tab empty copy now says **Create one above** (the form is above the list).
- **Main navigation** - **Shift+Tab** cycles **Dashboard → Projects → Temporary** in reverse (**Tab** still cycles forward). Tab vs Shift+Tab are dispatched explicitly by chord so the two actions cannot both run.
- **Dashboard** - Initial dashboard load also fetches `/api/projects` so chip counts stay correct on a direct `/dashboard` visit; failed project fetch does not wipe an existing in-memory list.
- **Projects / Dashboard chips** - **Temporary** vs **Temporary Boards** label uses one shared helper (`temporaryBoardsNavLabel`, **767px** breakpoint) so dashboard and projects stay aligned.

---



## [3.9.2] - (no release)



### Note

- **Version number skipped in git** - There is no commit in this repository that sets `internal/version/version.go` to **3.9.2**, and no `README` / `CHANGELOG` reference to **3.9.2** before this note. After **3.9.1**, the next bump was **3.9.3** (commit `2c5b576`, *multiple UX enhancements…*). No separate user-facing changes are recorded under **3.9.2**; see **3.9.1** (OIDC `dist/` rebuild) and **3.9.3** (UX items above) for work in that window.

---



## [3.9.1] - 2026-04-04



### Fixes

- **OIDC auth UI (embedded** `dist/`**)** - Rebuilt `internal/httpapi/web/dist/` so the compiled bundle matches `modules/`: router applies `oidcEnabled` / `localAuthEnabled` from `GET /api/auth/status`, and the login screen shows **Continue with SSO** when OIDC is configured (previously only TypeScript sources were updated in **3.9.0**, so production builds loading `dist/router.js` did not surface the SSO button).

---



## [3.9.0] - 2026-04-03



### Features

- **OIDC / SSO (optional)** - Single sign-on when all four env vars are set: `SCRUMBOY_OIDC_ISSUER`, `SCRUMBOY_OIDC_CLIENT_ID`, `SCRUMBOY_OIDC_CLIENT_SECRET`, `SCRUMBOY_OIDC_REDIRECT_URL`. Uses OAuth 2.0 Authorization Code with **PKCE (S256)** and a confidential client; **OIDC Discovery** and **JWKS** for the ID token; claims from the ID token only (no Userinfo). Successful login creates a normal `scrumboy_session` (no JWTs in the browser). Endpoints: `GET /api/auth/oidc/login` (optional `return_to`), `GET /api/auth/oidc/callback`. `GET /api/auth/status` adds `oidcEnabled` and `localAuthEnabled`. Optional `SCRUMBOY_OIDC_LOCAL_AUTH_DISABLED=true` disables password bootstrap/login while OIDC is configured. In **anonymous** mode, OIDC routes return **404** like other auth actions.
- **Auth UI** - **Continue with SSO** when OIDC is enabled; `oidc_error` query handling for failed callbacks.
- **Database** - New `user_oidc_identities` table (`UNIQUE(issuer, subject)`); `users.password_hash` is nullable for OIDC-only users (migration **049**).



### Documentation

- `docs/oidc.md` - Self-hosted operator guide: env vars, flow, constraints, reverse proxy, troubleshooting, security notes, explicit non-goals.
- `API.md`, `README.md`, `SECURITY.md` - OIDC endpoints, configuration, and session/security summary.



### Dependencies

- `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2` (OIDC client and token exchange); `github.com/go-jose/go-jose/v4` (integration tests for stub IdP JWTs).

---



## [3.8.0] - 2026-04-03



### Features

- **MCP JSON-RPC:** `tools/list` **and** `tools/call` on `POST /mcp/rpc` - Completes the spec-oriented MCP loop alongside existing `initialize` / `notifications/initialized`. `tools/list` returns tools with `name`, `description`, and `inputSchema` (JSON Schema with `required` and tight objects where defined); the catalog starts with four tools (`projects.list`, `todos.create`, `todos.get`, `todos.update`) and will grow over time. `tools/call` accepts `params.name` and `params.arguments`, reuses the same tool handlers as legacy `POST /mcp`, and returns success as `result.content[]` with `type: "json"` and the tool payload in `json`. Discovery and invocation are **stateless** (no `initialize` required for `tools/list` or `tools/call`). Errors use JSON-RPC codes (`-32601` unknown tool, `-32602` invalid params / validation, `-32603` internal); unknown tools may include `error.data` with `name`.



### Improvements

- **Catalog** `required` **handling** - Pre-call checks read the `required` array whether it is stored as `[]string` (in-memory catalog) or `[]any` (e.g. after JSON round-trip), avoiding silent skips.
- `tools/call` **shape errors** - Clearer `missing params` / `missing params.name` messages for invalid requests.



### Documentation

- `API.md` - New **JSON-RPC MCP endpoint (spec-compatible)** section for `POST /mcp/rpc`: protocol rules, supported methods, response shapes, auth (same as `/mcp`), and how this differs from the legacy `/mcp` envelope.
- `README.md` - **MCP (JSON-RPC) for AI agents** subsection with `curl` examples (`initialize`, `tools/list`, `tools/call`), pointer to `API.md`, and notes on HTTP JSON-RPC vs stdio MCP clients.

---



## [3.7.8] - 2026-04-03



### Features

- **MCP JSON-RPC (Phase 1)** - New `POST /mcp/rpc` endpoint using **JSON-RPC 2.0** alongside the existing `/mcp` `{ "tool", "input" }` API (unchanged). Supports `initialize` (protocol version **2024-11-05**, `capabilities.tools`, `serverInfo`), `notifications/initialized` and `initialized` as notifications (**204** empty body), and spec error codes (e.g. **-32601** method not found). `tools/list` and `tools/call` added in **3.8.0**.

---



## [3.7.7] - 2026-04-03



### Features

- **Dashboard todo sort** - Sort assigned todos by **Activity** (recently updated, default) or **Board order** (per project: workflow column position, then lane rank). `GET /api/dashboard/todos` supports optional query `sort=activity` or `sort=board`; pagination `cursor` is tied to the active sort, and a cursor from the wrong mode is rejected with **400** `VALIDATION_ERROR`.



### Improvements

- **Todo dialog (mobile)** - New/edit todo form scrolls inside the modal on narrow viewports so header, fields, and Save stay usable (aligned with Settings-style scrolling).
- **Dashboard sort preference (signed-in)** - Choice is saved under `user_preferences` key `dashboardTodoSort` and restored after login (still mirrored in **localStorage** for fast defaults). Server hydrate skips applying the stored value when it already matches in-memory state, and does not overwrite a sort the user changed locally before preferences finish loading.

---



## [3.7.6] - 2026-04-02



### Features

- **API access tokens** - create/manage tokens for CLI, CI, and integrations
- **Bearer Auth** - MCP now supports Bearer auth (`Authorization: Bearer sb_...`)

---



## [3.7.5] - 2026-04-02



### Features

- **MCP token** - Added MCP bearer token authentication support.

---



## [3.7.4] - 2026-04-02



### Features

- **Bulk edit** - Select multiple cards and update them together (desktop).

---



## [3.7.3] - 2026-04-02



### Improvements

- **Project header image** stays in sync when the board updates without a full reload.

---



## [3.7.2] - 2026-04-01



### Features

- **Keyboard shortcuts** for common actions.



### Improvements

- **Click outside** a modal to dismiss it.

---



## [3.7.1] - 2026-04-01



### Improvements

- **Workflow editing** modal aligned with project workflow customization.

---



## [3.7.0] - 2026-03-31



### Features

- Started work on **MCP (Model Context Protocol) API** - Automate Scrumboy via **agents** (Claude, IDEs, custom tooling).

---



## [3.6.1] - 2026-03-31



### Features

- **MCP adapter** - Automate todos, sprints, and tags; **board snapshot** (`board.get`); member tools; **tag delete**.
- **Lane colors** - Update workflow lane colors after creation.

---



## [3.6.0] - 2026-03-31



### Improvements

- **3.6.0** release following editable workflows (**3.5.8**).

---



## [3.5.8] - 2026-03-31



### Features

- **Editable workflows completed** - Add or remove lanes after creation, with updated dashboard and settings (including room for the Workflows tab).



### Fixes

- **Anonymous mode** - Fields that should stay editable were incorrectly blocked.

---



## [3.5.7] - 2026-03-25



### Fixes

- **Workflow lane “add” control** behaves correctly.

---



## [3.5.6] - 2026-03-25



### Improvements

- **Setup docs** - Clearer `scrumboy.env` and configuration guidance.

---



## [3.5.5] - 2026-03-23



### Improvements

- **Errors** - Consistent sentinel errors across packages (clearer behavior for callers).
- **Open-source docs** - README and repo presentation polished for the public release.



### Security

- **Contributions** - DCO (Developer Certificate of Origin) check.

---



## [3.5.3] - 2026-03-15



### Security

- **Project settings** - Only **maintainers** can rename or delete a project.



### Improvements

- **Toasts** when todos are created or updated.

---



## [3.5.1] - 2026-03-15



### Fixes

- **Backups** - Safer behavior when workflows merge and during backup previews.

---



## [3.5.0] - 2026-03-15



### Features

- **Import & export** - More reliable across edge cases.

---



## [3.4.12] - 2026-03-14



### Features

- **Admin password reset** - Reset user passwords from **Settings -> Users**.

---



## [3.4.10] - 2026-03-13



### Improvements

- **Governance** - **LICENSE**, **CLA**, and **Code of Conduct** for the open-source release.

---



## [3.4.9] - 2026-03-13



### Security

- **Tag colors** - Fixed an XSS vector in tag color handling.

---



## [3.4.7] - 2026-03-13



### Improvements

- **Cards** - Lane color updates immediately when you move a card to another column.

---



## [3.4.6] - 2026-03-13



### Improvements

- **Dashboard** - Status pills match your custom lane colors.

---



## [3.4.5] - 2026-03-13



### Fixes

- **Assignee avatar** no longer appears twice on the same card.

---



## [3.4.4] - 2026-03-13



### Fixes

- **Toolbar** - Race condition that could hide top board actions on first load.

---



## [3.4.3] - 2026-03-11



### Features

- **Viewer role** - Read-only project access when you need visibility without editing.

---



## [3.4.1] - 2026-03-11



### Fixes

- **Profile avatar** can be changed reliably.

---



## [3.4.0] - 2026-03-11



### Security

- **Permissions & audit** - Stronger rules for sensitive actions, with an **audit trail**.

---



## [3.3.3] - 2026-03-11



### Fixes

- **Members list** - Reliable visibility when permissions were ambiguous.

---



## [3.3.2] - 2026-03-11



### Features

- **Promote contributor** to **maintainer** where allowed.

---



## [3.3.1] - 2026-03-11



### Security

- **Contributors** - Clearer limits on creating/deleting stories and on assignment.

---



## [3.3.0] - 2026-03-10



### Improvements

- **Drag and drop** while the board is filtered - cards stay consistent with the active filter.

---



## [3.2.1] - 2026-03-10



### Performance

- **Live updates** - Fewer duplicate refreshes when returning to the desktop app (SSE / focus).

---



## [3.2.0] - 2026-03-10



### Security

- **Roles & UI** - Screens and flows aligned with owner, maintainer, and contributor rules.

---



## [3.1.0] - 2026-03-10



### Security

- **Team roles** - Broader permission and UI alignment for how roles work in the app.

---



## [0.x - 3.0.x] - Early development

*Versions through **3.0.0** and older **2.x / 1.x / 0.x**, summarized by theme.*

### Features

- **Kanban core** - Boards, columns, todos, drag-and-drop, filters, tags.
- **Projects** - Members, assignees, linked stories, points, **sprints**, dashboard, charts.
- **Live boards** - **SSE** updates without manual refresh.
- **Anonymous boards** - Shareable boards with slug URLs, improved privacy, and **import/export** (including NAS-friendly use).
- **2FA**, **PWA**, **custom lanes**, **search**, and a **role model** that grew into today’s permissions.



### Improvements

- **Mobile & desktop** - Touch DnD, tabs, scrolling, passwords, layout; avatars and sprint cues on cards.



### Performance

- **Speed** - Fewer round-trips, **debounced SSE** (less unnecessary reload), query merges, **SQLite tuning for NAS/self-hosted**, smarter caching and service worker behavior.



### Security

- **Auth & sessions** - Login/logout reliability (including tunnels), safer cache rules for auth routes, import confirmations, stricter handling of user-controlled tag data over time.



### Fixes

- Many **stability and UX** fixes across DnD, charts, anonymous mode, imports, and mobile.

---



## Highlights


| Area                  | Notes                                                      |
| --------------------- | ---------------------------------------------------------- |
| **Self-hosted / NAS** | Optimized SQLite usage for low-resource environments       |
| **Real-time**         | SSE-powered live board updates                             |
| **Anonymous boards**  | Shareable boards with slug URLs and evolving privacy model |
| **Import / export**   | Reliable backup and migration                              |
| **MCP**               | Automation via agents and external tools                   |
| **Roles & audit**     | Strong permission model with audit trail                   |


