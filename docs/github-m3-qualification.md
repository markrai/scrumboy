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
3. Remove that disposable repository from the GitHub App's installation permissions. Explicitly click **Refresh repositories**; confirm the resource and its card/project references are removed only after a successful complete scan.
4. Simulate GitHub API denial, app suspension or incomplete pagination; expect a visible error and unchanged local references.
5. Disconnect an account (only on the disposable test setup); confirm that local references are deleted and GitHub installation remains installed until removed separately on GitHub.

## Scenario C: credentials and security
1. Rotate a disposable App RSA private key and restart with the replacement key. Confirm authentication recovers; invalid keys prevent startup.
2. Check HTTP responses, logs, backup exports and browser bundles for accidental exposure of PEM contents, JWTs, installation access tokens, or webhook credentials.
3. Confirm anonymous or published-board routes cannot enumerate or mutate private integration resources, and instance Owner/Admin status never bypasses project membership.

## Automated tests and CI
- `go test ./internal/integration/... ./internal/store/... ./internal/httpapi/... ./internal/mcp/... ./internal/migrate/...`
- Frontend `npm test` in `internal/httpapi/web`, the generated-code parity check and browser checks.
- Migration checks on an isolated old-schema database, including project/card association cascade and restore compatibility.
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
