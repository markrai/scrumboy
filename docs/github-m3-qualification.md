# M3 GitHub App qualification (PR #366)

This is the release gate for the live GitHub App integration before automatic GitHub webhook handling is enabled in M4.

## Isolated environment
- Use `scrumboy-test` on Vega, port 9447, not production.
- Confirm the running container's `org.opencontainers.image.revision` equals the PR commit being qualified.
- Preserve the existing `/data` volume and private-key bind mount. Keep GitHub PEM and webhook secrets outside the source tree and out of the database.
- Authenticate as the local Scrumboy instance Owner; the GitHub installation owner and the Scrumboy project Maintainer are separate identities.
- For destructive GitHub access/revocation cases, use a disposable repository or installation and a disposable Scrumboy card.

## Scenario A: repository-to-card journey
1. Settings > GitHub must show **App configured** and the connected `markrai` account.
2. Select a maintained durable Scrumboy project by its displayed name; **Load project** and slug entry should not be required.
3. Choose `markrai/scrumboy` from the discovered repository list and add it to the project.
4. Open a disposable card. Add the GitHub repository under **External references**.
5. Verify the reference URL, close/reopen the card, and confirm persistence.
6. Unlink the reference; confirm original card body/title are unchanged.
7. Confirm a Viewer can read references, Contributor can link/unlink, Maintainer can attach/detach project repositories, and unrelated project users cannot enumerate private integrations.

## Scenario B: idempotency and lifecycle
1. Refresh repositories twice without permission changes; expect no duplicate connection/resource/association.
2. Rename a disposable GitHub repository; refresh and confirm it retains its Scrumboy resource ID.
3. Remove that disposable repository from the GitHub App's installation permissions. Explicitly click **Refresh repositories**; confirm its status becomes **unavailable**, its last-known URL remains visible, and all existing card/project references survive. Attempting to create a new reference must fail.
4. Restore access to the same repository, refresh again, and confirm it becomes **available** with the same internal resource ID and existing card/project references intact.
5. Simulate GitHub API denial, app suspension or incomplete pagination; expect a visible error and unchanged local references.
6. Preview disconnect impact using the UI or admin impact endpoint; verify resource, project-association and card-reference counts. Reject missing/stale confirmation and non-admin requests; on a disposable setup only, confirm that a fully verified disconnect explicitly deletes local references without uninstalling the GitHub App.

## Scenario C: credentials and security
1. Rotate a disposable App RSA private key and restart with the replacement key. Confirm authentication recovers; invalid keys prevent startup.
2. Check HTTP responses, logs, backup exports and browser bundles for accidental exposure of PEM contents, JWTs, installation access tokens, or webhook credentials.
3. Confirm anonymous or published-board routes cannot enumerate or mutate private integration resources, and instance Owner/Admin status never bypasses project membership.

## Scenario D: M3.2 actionable issue/PR references
1. In the GitHub App Settings, grant **Issues: Read-only** and **Pull requests: Read-only**. Have the installation owner approve the expanded permissions. Confirm repository-only refresh still works when these additional scopes are absent or pending.
2. On the isolated Vega test project, verify the repository reference **Add** button is visible and functional on desktop and mobile.
3. On a disposable Scrumboy card, paste a canonical issue URL and PR URL from an associated repository. Verify each becomes a distinct structured card reference with number, title, state and safe clickable URL.
4. Search by title with **Issues** and **Pull requests** selected independently. Verify results come only from the selected associated repository. Link a result; repeat linking and verify no duplicates.
5. Change a disposable issue/PR's state on GitHub, choose **Refresh** in Scrumboy and verify that cached status changes without altering card title/body or writing to GitHub. Verify unlink.
6. Confirm Viewer sees cached metadata without Link/Search/Refresh/Remove controls, Contributor can modify only their project's card references, and an unrelated user cannot search or enumerate private issue/PR metadata.
7. Attempt URL injection, a different GitHub hostname, a repository not attached to the project, and a repository attached to another project. None may reach the GitHub provider.
8. Inspect request metadata in test logs/fakes: GitHub issue/PR tokens must request only read permissions and `repository_ids` restricted to the associated repository ID. Do not expose tokens, PEM contents or JWTs in logs or responses.
9. Remove GitHub App access to the disposable repository, verify cached linked item metadata remains visible as unavailable, then restore the installation's access and test refresh. No GitHub-side or card-content mutation occurs.

**Shared-App product requirement:** Ordinary users must eventually connect their own selected GitHub repositories through the shared Scrumboy Connect App **without creating an App or receiving the shared PEM key**. The existing M3.2 direct mode is restricted to trusted instance administrators and is **not qualified** for unrelated tenants. See [shared-App implementation and acceptance gate](github-shared-app.md); do not declare the shared experience supported until that separate gate passes.

## Scenario E: shared-App release gate (required; implementation NOT started)

- [ ] Host the one shared Scrumboy Connect App's private key solely in the trusted Scrumboy integration service. Never distribute it to self-hosted Scrumboy instances.
- [ ] Authenticate the actual GitHub installer/account and bind each installation ID to an authorized Scrumboy user, tenant and project context; reject spoofed `installation_id` and callback/replay attacks.
- [ ] Allow a user to select only their own authorized installation and its selected repositories. Ensure an unrelated Scrumboy user cannot list another installation's account names, repository metadata or issue/PR search results.
- [ ] Enforce project-level Scrumboy role checks, server-to-service authentication, scoped backend access and revocation independently of GitHub App permissions, including on self-hosted deployments.
- [ ] Test at least two unrelated GitHub accounts with separate installations, including private repositories and negative cross-account/cross-tenant attempts; verify the maintainer's `markrai` repositories never appear for another user.
- [ ] Verify GitHub uninstall/suspension, account disconnect, repository removal, private-key rotation and token expiry do not leak credentials or silently destroy card references.
- [ ] Document the privacy boundaries: Scrumboy project members may see metadata that a project administrator deliberately associates with their project, but installing the App alone never shares the maintainer's repositories.
- [ ] Complete end-to-end hosted and self-hosted qualification before documenting `Connect GitHub` as available to ordinary users.

**Status:** Architecture decision documented only. Scenario E is not passed by the existing M3.2 automated tests or Vega's trusted-owner test installation.

## Scenario F: isolated broker qualification (implemented; live acceptance pending)

- [x] Separate executable requiring TLS 1.3, client CA verification and exact client certificate fingerprint pinning.
- [x] Explicit expiring server-owned grants for installation and repository ID; no global installation listing, repository URL parameters, token export or generic GitHub API path.
- [x] Bounded JSON, rate budgets and canonical result validation with cross-instance, cross-repository, expired-grant, provider-error and spoofed client identity tests.
- [ ] Verify broker server and client TLS connection against test certificates on isolated infrastructure; confirm unpinned clients cannot connect.
- [ ] Qualify a disposable installation and repository with a short-lived operator grant; verify safe success, denial, expiry and restart-driven revocation.
- [ ] Replace the temporary operator-snapshot grant source with durable verified user-to-installation ownership and online revocation before any general release.
- [ ] Integrate a verified identity-bound instance client; run two-tenant end-to-end tests without publishing credentials.

**Status:** Code and automated tests added. No publicly usable central shared-App service, self-hosted enrollment or end-user connection flow is implemented. See [broker staging documentation](github-broker.md).

## Scenario G: M3.3.B durable verified ownership (backend code implemented; acceptance pending)

- [x] Separate broker ownership SQLite database and explicit user-bound request mode; no static-grant fallback, no App PEM exposure.
- [x] Internal coordinator takes one-time GitHub OAuth verification and intersects authenticated-user repository IDs with installation resources before atomic persistence.
- [x] Enforce instance, local user, installation and repository identity on every lookup. Reject forged subjects, wrong providers, duplicate IDs, missing metadata and unverified calls.
- [x] Replace removed repositories atomically, retain per-instance/user isolation, persist across process restarts, and support live user/installation/instance/repository revocation.
- [x] Provisional 24-hour expiration and deny-by-default startup; zero user-owned grants when configured but not enrolled.
- [x] mTLS-only user/install/repository revocation endpoints with one-time OAuth state invalidation and per-subject completion/revocation serialization.
- [x] Add bounded, mTLS-only OAuth begin/complete endpoints to the user-bound broker for operator qualification, with secret-bearing code exchange only on the central broker.
- [ ] Implement verified Scrumboy session/authenticated-user initiation and browser-bound OAuth callback; operator mTLS endpoints alone are not proof of the end user's session.
- [ ] Wire verified self-hosted instances to the user-bound broker, enforce project membership at the instance boundary, and prevent fabricated `userId` access.
- [ ] Qualify real GitHub installation revocation, user membership loss and reauthorization; determine secure automatic refresh behavior.
- [ ] Complete a two-tenant end-to-end staging test before enabling any public Connect GitHub button.

**Status:** The core persistence/authorization service is implemented; this is **not** live M3.3.B or public user connection acceptance. See [broker modes](github-broker.md).

## Scenario H: M3.3.D self-hosted transport (implemented; live acceptance pending)

- [x] Self-hosted instance client with TLS 1.3, registered client certificate, verified server CA, no redirects, bounded responses and fixed read-only issue/PR/OAuth broker endpoints.
- [x] Broker-side SQLite registry of approved client certificate fingerprints with immediate per-certificate revocation and persistent restart-safe isolation.
- [x] Operator-only register, rotate (maximum two active certificates), revoke and atomic instance-disconnect operations. The shared GitHub App PEM and installation tokens never leave the central broker.
- [x] Separate `-dynamic-clients` user-bound mode, with no fallback to legacy operator grants or client-identity snapshots.
- [ ] Complete authenticated Scrumboy user/session and project authorization integration before exposing client operations to ordinary users (**M3.3.C deferred, not passed**).
- [ ] Qualify self-hosted instance registration, private repository authorization, certificate rotation/revocation and cross-account failures against two unrelated GitHub installations.
- [ ] Design safe self-service instance enrollment, distributed rate budgets and broker rate-limit/revocation handling before production rollout.

**Status:** M3.3.D foundational code implemented, with live and public-release acceptance still pending. See [self-hosted setup](github-self-hosted.md).

## Scenario I: M3.3.E operational broker safeguards (implementation complete; live acceptance pending)

- [x] User-bound broker requires atomic on-disk fixed-window rate admission for every read-only issue/PR and OAuth operation, alongside local process budgets.
- [x] Enforce instance and global limits across processes accessing one SQLite file; fail closed on missing database, clock rollback and budget exhaustion.
- [x] Keep redacted per-minute operation counts (24-hour retention) and transactional security history for verified bindings and certificate/connection changes (30-day retention).
- [x] Provide bounded operator-only activity retrieval, no public audit API and no secret-bearing payloads in event rows.
- [x] Regression tests for shared budget exhaustion, restart persistence, wrong-instance access, audit rollback, private-data omission and retention.
- [ ] Qualify multi-host deployment with a genuinely shared transactional budget backend and upstream GitHub rate-limit behavior.
- [ ] Qualify real broker restarts, restored SQLite data, operator audit review, certificate rotation and two-tenant live failure cases.
- [ ] Complete **M3.3.C** user/session-bound callbacks and authorization before any shared-App public release (**deferred, not accepted**).

**Status:** The operational code is implemented for a broker deployment using one authoritative SQLite file. Multi-host coordination, live acceptance and ordinary-user rollout are **not** complete. See [broker safeguards](github-broker.md).

## Automated tests and CI
- `go test ./internal/integration/... ./internal/store/... ./internal/httpapi/... ./internal/mcp/... ./internal/migrate/...`
- Frontend `npm test` in `internal/httpapi/web`, the generated-code parity check and browser checks.
- Migration checks on an isolated old-schema database, including retained resource availability, project/card association cascade for explicit detach/disconnect, M3.2 migration 094 issue/PR identity and cascade, and restore compatibility.
- Required GitHub checks (Go, frontend, CodeQL, DCO, Gradle, GHCR) must pass on the final qualified commit.

## Acceptance record
| Field | Value |
| --- | --- |
| PR commit SHA | Pending live test |
| Test instance and image digest | Pending live test |
| Test operator | Pending live test |
| Completed scenarios | Pending live test |
| Errors, screenshots, redacted logs | Pending live test |
| Final M3 verdict | **Pending live acceptance** |

M4 may be developed on this PR, but the M3 acceptance checkbox stays open until the live scenarios are actually completed. Do not run a destructive lifecycle test against production data.

## Scenario J: M5.B verified event matching (implementation present; CI and staging acceptance pending)

- [x] Raw HMAC-verified GitHub issue and pull-request metadata only; subscribe to Issues and Pull requests. Merge is distinct from unmerged close.
- [x] Separate migration 096 and durable inbox with delivery digest deduplication, bounded claim/lease/retry, cross-inbox collision rejection and 30-day retention.
- [x] Idempotent, atomic project/repository/installation-aware rule matching; no event can target a disconnected, unavailable or detached repository.
- [x] GET-only, project-role-scoped match summaries and redacted admin queue counts; no raw payload or credentials retained.
- [x] Unit and integration tests covering invalid signatures, malformed/unknown events, duplicates, revoked installations, stale claims, transient database errors, project boundaries and non-execution.
- [ ] Confirm latest Go, frontend, security and migration CI runs all pass.
- [ ] Validate live two-tenant GitHub webhooks, installed App permissions, revocation, replay and process restart behavior in staging before public rollout.
- [ ] Finish deferred M3.3.C browser-session authorization before ordinary users can connect to Scrumboy Connect.

**Non-negotiable:** \`execution_approved_at\` remains NULL, no action executor is wired, and enabling a rule does not authorize any automated action.
