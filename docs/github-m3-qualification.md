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

**Self-hosted product requirement:** An instance administrator owns one GitHub App per instance. Project members do not create Apps, supply PEM keys or manage webhooks. App Manifest onboarding is planned in [#371](https://github.com/markrai/scrumboy/issues/371); it is not yet released.

## Scenario E: independent-instance isolation (live acceptance pending)

- [ ] Deploy two independent Scrumboy instances, each with its own GitHub App and installation, and demonstrate no cross-instance installation discovery, repository metadata access or event matching.
- [ ] Confirm only instance Owners/Admins configure installation connections; project Maintainers associate repositories and project Contributors link cards, with public/anonymous routes denied.
- [ ] Confirm two GitHub accounts' repositories, issues and pull requests cannot be enumerated across unrelated Scrumboy installations, including private resources.
- [ ] Verify App suspension/uninstall, repository removal, key rotation and token expiry do not leak credentials or silently delete card references.
- [ ] Verify genuine signed GitHub issues and pull request webhooks reach the instance's public HTTPS destination; unsigned/forged requests fail. The rule must be enabled before event admission.
- [ ] Verify no calls to scrumboy.com or a centrally operated broker are required, and capture redacted acceptance evidence.


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
- [ ] Complete independent-instance and signed webhook live acceptance before general release.

**Non-negotiable:** The legacy `execution_approved_at` column grants no permission. Only an enabled rule receiving a future signed, authorized event can produce an in-app notification. No GitHub or card writes are executed.
