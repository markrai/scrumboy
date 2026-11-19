# Shared Scrumboy Connect GitHub App: required product architecture

> **Status: decided design, NOT IMPLEMENTED.** This is the intended end-user integration experience. PR #366 currently has only a trusted administrator-managed, direct-App integration (M3/M3.2). Do not treat the shared integration as deployed or qualified.

**Name vs. identifiers:** **Scrumboy Connect** is the GitHub App's display name. Its numeric App ID and installation URL slug are separate GitHub-assigned identifiers and must be read from the actual App configuration; do not derive either from the display name.

## Product contract

Scrumboy offers **one centrally maintained, public Scrumboy Connect GitHub App**. Each user or organization installs that same App **into their own GitHub account or organization** and selects the repositories the App may access. Ordinary Scrumboy users never create a GitHub App, provide an App ID, upload a PEM key, or receive a shared App token.

The maintainer's `markrai` installation and another account's installation are **distinct**. Installing the App under a different GitHub account **does not grant GitHub access to `markrai` repositories**. A Scrumboy project may display linked private-repository metadata to its authorized members after an authorized Maintainer associates that repository, even if those members do not individually have GitHub access. This is a deliberate Scrumboy collaboration visibility decision, not GitHub permission delegation.

**Important:** A GitHub App private key is powerful across installations of that App. GitHub's separation of installations does not automatically impose Scrumboy tenant isolation on a server that holds the App key. Never distribute that PEM key to users or arbitrary self-hosted servers.

## M3.3 implementation progress

**First security slice implemented in PR #366:** `internal/integration/githubconnect` now provides a bounded, one-time GitHub App user OAuth/PKCE flow and provider-authenticated installation verification. A trusted caller supplies a pre-authenticated, opaque Scrumboy subject; the verifier binds the OAuth state to that subject, rejects replay and expiry, exchanges a GitHub authorization code only once, reads the authenticated GitHub user identity, and checks the selected installation against the paginated `GET /user/installations` response and configured App ID. It refuses suspended, unknown or wrong-App installations and never returns or persists the GitHub user token.

**Important boundary:** This is a reusable internal primitive, **not** a released connection flow. The subject is an input from a future authenticated broker endpoint; the package itself does not authenticate Scrumboy accounts. In-memory pending states are intentionally invalidated by process restart. The implementation does not yet expose HTTP callbacks, persist verified installation bindings, host your App credentials, issue self-hosted service credentials, connect users through the UI or perform on-behalf-of authorization after OAuth completion. Those remain blocked until the remaining M3.3 slices are implemented and tested.

The next code slices must add: (A) a separately deployable central service that owns Scrumboy Connect secrets and authenticates callers, (B) a trusted browser/session callback binding and durable per-instance/per-user installation ownership, (C) strictly scoped broker API operations, and (D) revocable self-hosted instance credentials. **Do not expose the App-global installation directory, accept a user-supplied installation ID as proof, or distribute App private-key material in the meantime.**

## Required components before launch

1. **Central App credential service.** Operate the App ID/slug, private-key rotation, GitHub installation-token minting, per-request rate limits, audit logs and revocation in a trusted Scrumboy-managed service. Keep installation tokens server-side and restrict GitHub API access to the exact authorized repository ID and read permissions. Do not embed the PEM in the Scrumboy binary, Docker image, GitHub repository or browser.
2. **Verified user installation binding.** Initiate an authenticated, CSRF-protected connection flow from Scrumboy. Verify the logged-in GitHub user's authorization for the selected installation using a GitHub user authorization flow and GitHub's user-accessible installation APIs, including organization-specific authorization. Treat a GitHub setup callback's `installation_id` or `setup_action` only as a *hint*, never proof that the caller owns or administers an installation. Bind the verified provider account/installation ID to the appropriate Scrumboy identity and tenant.
3. **Tenant/project isolation.** Authenticate every Scrumboy request, resolve its tenant/project membership and repository association in an authoritative store, and authorize each installation/repository operation on the server. Never expose the App's global installation list to ordinary users. An App installation on account B must never expose account A's repositories or issue/PR metadata through Scrumboy's APIs or caches.
4. **Self-hosted Scrumboy support.** Self-hosted operators use a securely registered, authenticated connection to the **central service**, not a copy of the shared GitHub App PEM. Distinguish each self-hosted server and its authorized user/tenant; validate signed connection state, request origin, token audience, expiration and revocation. Prefer a narrowly scoped server-side data proxy to exporting arbitrary GitHub installation tokens. Protect service abuse and preserve explicit revocation.
5. **Permissions and lifecycle.** Request only Metadata: Read-only, Issues: Read-only, and Pull requests: Read-only for M3.2. An existing installation must approve newly granted permissions. Preserve links as unavailable when GitHub access is lost; disconnect must not silently alter cards or other tenants. The future M4 signed webhook receiver and M5 user-defined automations are separately gated.

## Intended workflow

1. User opens **Settings → GitHub → Connect GitHub**.
2. Scrumboy starts an authenticated GitHub connection flow to install/authorize Scrumboy Connect under the user's own account or an organization they administer.
3. On return, the trusted service independently verifies the caller's right to that exact installation and records a tenant-bound connection.
4. The user selects repositories the GitHub App installation can access, then a Scrumboy project Maintainer explicitly associates permitted repositories with a project.
5. Project Contributors link GitHub issues or pull requests by URL or bounded repository-scoped search; Viewers see permitted read-only metadata.
6. Uninstalling the App or revoking access stops future provider queries but retains last-known card references as unavailable until an authorized user explicitly removes them.

## Explicit non-goals and release restrictions

- **Do not** require every Scrumboy user to create their own GitHub App.
- **Do not** hand out the shared PEM/private key to self-hosters or third-party integrators.
- **Do not** let a user supply any arbitrary installation ID or repository name and rely on its existence as authorization.
- **Do not** equate GitHub access to a repository with automatic Scrumboy project membership, or vice versa.
- **Do not** claim this user experience is available merely because the M3.2 trusted-admin UI passes CI.
- **Do not** expand the integration into automatic card status changes; any such behavior requires separately opted-in M5 rules.

## Required acceptance evidence

The shared experience is **not releasable** until tests prove two unrelated users and two unrelated GitHub installations cannot enumerate, search or link to each other's private repositories; an invalid/replayed setup callback cannot bind another user's installation; self-hosted servers cannot obtain the shared private key or unscoped installation tokens; project Viewer/Contributor/Maintainer permissions hold on every request; uninstall, credential rotation, and reconnection are safe; and both hosted and self-hosted end-to-end flows work.

Track the separate shared-App release gate in [M3 qualification](github-m3-qualification.md). **Design and documentation are ready; implementation, security testing and live acceptance are still pending.**
