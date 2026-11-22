# GitHub App integration (M3)

M3 provides an opt-in GitHub App connection and repository reference workflow in Scrumboy Full Mode. It registers GitHub repositories as external resources and lets project Maintainers associate them with durable projects, so Contributors can link cards to those repositories. M3.2 adds explicit, read-only GitHub issue and pull-request references and title search. It does **not** modify GitHub issues or pull requests, synchronize card content, or automatically change a Scrumboy card when GitHub state changes. Webhooks and optional automations have separate milestones.

## Self-hosted GitHub App ownership

A self-hosted Scrumboy Full Mode instance uses a **GitHub App owned by its administrator**, not a central Scrumboy Connect service. One administrator configures the App for their Scrumboy instance. Other Scrumboy project members can link permitted repositories and issues/PRs under the existing project roles without creating Apps or handling credentials. There is no shared broker, central webhook relay or dependency on scrumboy.com.

**Current implementation on PR #366:** Readiness diagnostics, an instance-owned GitHub App Manifest setup flow, protected file-based credential persistence, automatic provider/webhook activation, and existing manual configuration are implemented. **End-to-end registration against GitHub and public webhook delivery remain live acceptance requirements**, tracked in [#371](https://github.com/markrai/scrumboy/issues/371). The App belongs to the self-hosted administrator; there is no central Scrumboy service.

### Register an instance-owned GitHub App (current manual setup)

1. Create a GitHub App on an account or organization the Scrumboy **instance administrator** controls. Request **Metadata: Read-only**, **Issues: Read-only**, and **Pull requests: Read-only**. These support repository discovery, issue/PR references and event subscriptions. No GitHub write or Contents permission is needed for notifications.
2. Install the App only on selected repositories. Record the App ID and slug, and generate its RSA private key (PEM).
3. Keep the PEM file on the server outside the Git repository, SQLite database, cards, browser assets and general backups. Configure the server using the variables below, restart and use Settings → GitHub to connect and sync the installation.
4. For real-time events, separately configure [the signed webhook endpoint](github-m4-webhooks.md) and subscribe to Issues and Pull request events. GitHub must reach the HTTPS endpoint from the public internet; private Tailscale-only addresses cannot accept GitHub deliveries. Manual repository and issue/PR refresh work without webhooks.


### S2.2 Guided GitHub App setup (Full Mode)

The instance **Owner** can open **Settings → GitHub → Set up GitHub** when no App is configured and `SCRUMBOY_PUBLIC_BASE_URL` is a canonical, pathless HTTPS origin. GitHub displays the App registration and permission review. The App is owned by that GitHub account, not by Scrumboy.com.

1. Scrumboy constructs the read-only App manifest and an expiring, Owner-bound enrollment state. The manifest requests Metadata, Issues and Pull requests read permissions and subscribes to installation, installation repositories, repository, Issues and Pull request events.
2. After GitHub redirects back to the Scrumboy callback, the server exchanges the one-time code and validates the App ID, slug, RSA private key and webhook secret **without exposing them to the browser or database**.
3. The credentials are written together as one private bundle and made durable before the GitHub provider and signed webhook worker are activated. The success page links directly to the new App installation flow. Back in Scrumboy, select **Connect and sync**.
4. The next Scrumboy start loads the bundle and recreates the provider and webhook receiver. Setup does not need a container restart. It does not retroactively make a private-only webhook endpoint reachable.

**Credentials and backup.** Guided credentials reside at `DATA_DIR/.scrumboy-github-app/credentials.json`. On POSIX, Scrumboy creates the directory with permissions `0700` and the file with `0600`, verifies permissions when loading, rejects linked directories and files, and refuses to overwrite an existing bundle. File contents include the GitHub App private key **and** the webhook secret as JSON, **not encrypted by Scrumboy**. Use encrypted host storage or volume encryption where required; restrict who can inspect host backups and containers. On Windows, verify and restrict inherited NTFS ACLs yourself; POSIX permission bits do not replace Windows access controls.

Back up the **entire protected bundle** alongside the instance's SQLite database when making a host-level disaster-recovery backup. Scrumboy board exports, SQLite-only backups, and moving `app.db` alone **do not contain this secret bundle**. Restore the protected directory and file with the original restrictive permissions before restarting the server. If the bundle is corrupt, inaccessible or permissive, startup stops rather than silently discarding the App identity.

**Interruption and rotation.** If GitHub registration finishes but Scrumboy shuts down after the bundle is saved, restart the server and the saved App is recovered. If the bundle was **not** saved, delete the incomplete GitHub App registration or complete a deliberate manual recovery; do not repeatedly create Apps without checking GitHub. For key rotation, generate a replacement private key from the App's GitHub settings and perform a controlled offline credential backup/update and restart. The guided setup is one-time enrollment and does **not** yet offer a live key rotation UI or overwrite an existing App.

**Configuration precedence.** Explicit `SCRUMBOY_GITHUB_APP_ID` and `SCRUMBOY_GITHUB_APP_PRIVATE_KEY_FILE` take precedence over any guided bundle without changing it. Remove both variables to reactivate the saved guided identity. Do not combine the managed bundle with `SCRUMBOY_GITHUB_WEBHOOK_SECRET` or `SCRUMBOY_GITHUB_WEBHOOK_SECRET_FILE`; the saved webhook secret is authoritative for the guided App. Manual App setup remains supported and is shown as an advanced option.

**Network limitation.** A valid HTTPS origin is needed for the browser callback, but GitHub also needs to reach the webhook destination from the public internet to deliver events. Tailscale-only hosts can still use GitHub repository discovery, but cannot receive direct GitHub webhooks. A successful App registration is **not** evidence of event-delivery readiness. See [GitHub inbound webhooks](github-m4-webhooks.md).

## Local configuration (existing manual mode)

The manual path is a supported fallback for deployments that already have an App or prefer operator-managed PEM mounts. Guided setup does not require manually setting these variables.

The server accepts three environment variables:

| Variable | Required | Meaning |
| --- | --- | --- |
| `SCRUMBOY_GITHUB_APP_ID` | Yes | Numeric GitHub App ID |
| `SCRUMBOY_GITHUB_APP_PRIVATE_KEY_FILE` | Yes | Absolute path to a readable RSA PEM file on the Scrumboy server |
| `SCRUMBOY_GITHUB_APP_SLUG` | No | GitHub App slug, used only to show the install link |

On Windows PowerShell, using an isolated Scrumboy database:

```powershell
$env:DATA_DIR = "$env:TEMP\scrumboy-github-m3"
$env:SCRUMBOY_MODE = "full"
$env:SCRUMBOY_GITHUB_APP_ID = "123456"
$env:SCRUMBOY_GITHUB_APP_PRIVATE_KEY_FILE = "C:\secure\github-app.private-key.pem"
$env:SCRUMBOY_GITHUB_APP_SLUG = "your-github-app-slug"
go run ./cmd/scrumboy
```

Replace the example ID and paths with your own. The server rejects partial or invalid App configuration on startup. It does not require a GitHub personal access token or `SCRUMBOY_ENCRYPTION_KEY` for M3, since no integration secrets are stored in SQLite. Existing Scrumboy features may still require the latter.

For Docker Compose, the supplied `docker-compose.yml` passes through the three environment variables. You must additionally **mount the PEM file into the container** and point `SCRUMBOY_GITHUB_APP_PRIVATE_KEY_FILE` at that container path. For example, in a private local Compose override:

```yaml
services:
  scrumboy:
    environment:
      SCRUMBOY_GITHUB_APP_PRIVATE_KEY_FILE: /run/secrets/scrumboy-github-app.pem
    volumes:
      - /secure/github-app.private-key.pem:/run/secrets/scrumboy-github-app.pem:ro
```

Keep the private key outside the repository and the `./data` backup export path; protect it with host filesystem permissions.

## Connect and test (M3)

1. Start Scrumboy in Full Mode, sign in as Owner/Admin, and open **Settings > GitHub**.
2. Install the App at GitHub with only the repositories you choose. Use **Install App** and **Refresh** in Scrumboy if the installation is not listed yet.
3. Click **Connect and sync** once. A connected account appears with **Refresh repositories** and **Disconnect** controls. Use **Refresh repositories** only when GitHub installation access or repository metadata changes. Disconnect previews the exact numbers of local repository records, project associations and card references that will be deleted, and requires explicit confirmation. If those counts change before deletion, Scrumboy rejects the request and requires a fresh preview. It does not uninstall the App on GitHub.
4. The repository management section defaults to the current Scrumboy project when Settings is opened from a project. The **Scrumboy project** selector remains available for Owners/Admins who maintain several durable projects; only projects where they are a **Maintainer** are shown. GitHub App installations remain instance-wide and are never copied per project.
5. Under **Linked Repositories**, review the selected project's existing associations. The **Add Repositories** section appears immediately below, with a repository picker and **Add to project** button only when there is something available to add.
6. When one GitHub installation is connected, the Installations section already identifies its account and no redundant account field appears below. If multiple accounts are connected, the **GitHub account** selector appears in repository management to choose the source for additions. Adding a repository only makes it available as a card reference in the selected project; it does not modify GitHub.
7. Open a card in the project. In **External references**, link the associated repository, verify the URL, then unlink it as an independent step. Test the card with a Viewer, Contributor and Maintainer to verify read/write boundaries.

Repository names and links may be visible to authenticated members of your Scrumboy project independently of their GitHub access. Deliberately published Scrumboy boards do not automatically expose integration management APIs or private card-reference UI.

## M3.2: GitHub issues and pull requests on cards

In a durable project where an instance administrator connected the GitHub App and a Maintainer associated a repository, open an existing Scrumboy card and expand **External references**.

- The existing repository selector still supports simple repository references; its **Add** button is visible on desktop and mobile.
- Under **GitHub Issues & Pull Requests**, paste a canonical `https://github.com/OWNER/REPO/issues/123` or `https://github.com/OWNER/REPO/pull/123` URL and choose **Link URL**.
- Alternatively, choose a repository already associated with the project, select **Issues** or **Pull requests**, enter a title query of 2–100 characters and choose **Search**. The query returns at most 20 results from that selected repository. Choose **Link** to attach a result.
- A linked item displays its type, number, title, repository and read-only Open/Closed/Draft/Merged status. **Refresh** fetches its current metadata when GitHub access is available. **Remove** unlinks the item without modifying GitHub.
- Only Contributors and above may link, refresh or remove items. Project Viewers may read retained metadata. Unrelated users, anonymous viewers and public boards have no private search/link APIs.
- Failed GitHub lookups do not erase or alter existing references or cards. Repository access loss preserves last-known read-only item metadata and displays unavailable state.

**Existing installations need to approve the two additional read permissions**. Scrumboy requests new short-lived tokens only when issue/PR lookup or search is used. Those tokens are minted with GitHub's `repository_ids` restricted to the exact provider repository ID associated with the selected project. The original metadata-only installation-discovery token remains separate. No issue/PR access token or private key is persisted in SQLite, the browser or a card.

**Security boundary:** GitHub installations are instance-managed and can be discovered only by a Scrumboy Owner/Admin. Project Maintainers associate resources; project Contributors link cards. Neither a GitHub installation nor a Scrumboy public board makes private repository information available to unrelated users. Guided setup is Owner-only; operational delivery acceptance remains tracked in #371.

Read-only project API (normal Scrumboy authentication and mutation header required):

| Method | Endpoint | Purpose |
| --- | --- | --- |
| GET | `/api/board/{slug}/todos/{localId}/github-items` | List cached GitHub issue/PR links |
| POST | `/api/board/{slug}/todos/{localId}/github-items` | Link validated `{"url":"https://github.com/OWNER/REPO/issues/123"}` |
| DELETE | `/api/board/{slug}/todos/{localId}/github-items/{resourceId}/{kind}/{number}` | Unlink a specific issue/PR |
| POST | `/api/board/{slug}/todos/{localId}/github-items/{resourceId}/{kind}/{number}/refresh` | Explicitly refresh item metadata |
| GET | `/api/board/{slug}/integrations/github/search?resourceId=123&kind=issue&q=title` | Search issues or PRs in one associated repository |

`kind` is `issue` or `pr`. URL input cannot select arbitrary GitHub installations or external hosts. The server reauthorizes every repository and resource against the active project, and all outbound lookup tokens are restricted to one installation and one repository. Existing `integration_todo_resources` references remain compatible, while migration 094 stores structured item references separately from card title/body and preserves project/resource cascade behavior.

### Live M3 acceptance ledger

| Scenario | Status | Evidence / action |
| --- | --- | --- |
| App configured using ID, private-key mount and slug | Verified on isolated Vega test instance | Settings shows **App configured** |
| Installation discovery and connect/sync | Verified on isolated Vega test instance | Connected GitHub account `markrai` |
| Project picker and repository association | **Not yet live-verified** | Select a maintained project and associate `markrai/scrumboy` |
| Card reference link/unlink | **Not yet live-verified** | Link and unlink from a disposable Scrumboy card; verify no change to card content |
| Role boundaries | Automated tests exist; **live check pending** | Viewer read, Contributor link/unlink, Maintainer project association |
| Repository rename and removal | Automated tests exist; **live check pending** | Use disposable repository data; verify retained unavailable references on removal, stable IDs on rename and restoration on reauthorization |
| Revoked App access, API interruption or incomplete discovery | Automated tests exist; **live check pending** | Verify failed refresh preserves existing links, then restore access |
| Key rotation and app suspension | **Not yet live-verified** | Use a disposable App key/installation and verify safe failure and recovery |
| M3.2 URL paste, title search, status refresh and removal | **Not yet live-verified** | Approve issue/PR permissions, link disposable objects on Vega and verify view-only roles |
| M3.2 scoped token & installation authorization | **Automated tests added; live check pending** | Prove calls cannot access unassociated repositories or unrelated GitHub installations |

Do not mark live scenarios as passed without recording an actual result. The M3 code and CI can be complete while the live acceptance gate remains open.

## Administration API

Endpoints below require an authenticated Owner or Admin, and Scrumboy's normal `X-Scrumboy: 1` header for mutations.

| Method | Path | Effect |
| --- | --- | --- |
| GET | `/api/admin/integrations/github` | Configuration availability and locally connected accounts |
| GET | `/api/admin/integrations/github/installations` | Discover GitHub App installations |
| POST | `/api/admin/integrations/github/installations/{installationId}/sync` | Verify and reconcile one installation's repositories |
| GET | `/api/admin/integrations/github/connections/{connectionId}/resources` | List synchronized repository resources |
| GET | `/api/admin/integrations/github/connections/{connectionId}/impact` | Preview counts of repositories, project associations and card references |
| DELETE | `/api/admin/integrations/github/connections/{connectionId}` | Disconnect only with JSON `{"confirmConnectionId":123,"impact":{"repositories":1,"projectAssociations":2,"cardReferences":3}}` matching the current preview |

Project Maintainers can `POST /api/board/{slug}/integrations/resources` with `{"resourceId":123}`, and `DELETE /api/board/{slug}/integrations/resources/{resourceId}`, to manage project associations. See [integration architecture](architecture/integrations-foundation.md) for card API and MCP operations.

## Security and lifecycle

- GitHub App private keys exist in an operator-managed PEM file **or** in the local protected guided-credential bundle, plus process memory. Installation tokens are ephemeral in memory, with expiry-aware refresh and cache invalidation. No tokens, PEM data or JWTs are stored in SQLite, card content, or browser responses.
- On GitHub denial or revocation, Scrumboy invalidates the cached token and reports the failure; it does not delete or change synchronized references based on an unsuccessful or incomplete fetch.
- On a **successful complete synchronization**, repositories missing from the installation's accessible set are marked **unavailable**. Their last-known metadata, Scrumboy resource IDs, project associations, and card references are retained. Existing links are displayed as unavailable and may be explicitly unlinked, but no new links or project associations may be added until GitHub access is restored. A subsequent complete scan that sees the same immutable GitHub repository ID restores availability without losing references. Repository renames retain the same stable ID.
- Disconnecting locally deletes associated Scrumboy references and attempts to revoke the installation token currently cached by the process. It does not uninstall the App on GitHub or revoke other tokens issued elsewhere. To remove GitHub-side access altogether, uninstall the App or change its permissions in GitHub settings.
- Provider API access is deliberately fixed to GitHub's public API. GitHub Enterprise Server is not supported in M3.
- There is no central GitHub App broker, hosted relay, or service-to-service enrollment. Keep App private keys local to the instance and limit installation and project access independently.

For provider API details see the [GitHub App REST API documentation](https://docs.github.com/en/rest/apps/apps), [installation API](https://docs.github.com/en/rest/apps/installations) and [installation-token guide](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app).

## Optional GitHub webhook receiver and readiness (S1/M4)

M4 adds an independent, opt-in **inbound** webhook receiver. Repository browsing and explicit refresh still work without it. S1 surfaces a redacted readiness status in Settings → GitHub and project Automations. **An App being configured does not imply GitHub events can arrive.** The receiver accepts `SCRUMBOY_GITHUB_WEBHOOK_SECRET` or `SCRUMBOY_GITHUB_WEBHOOK_SECRET_FILE`, never both. It reports a canonical webhook URL when a pathless HTTPS `SCRUMBOY_PUBLIC_BASE_URL` is set. GitHub event subscriptions and external reachability remain unverified until genuine signed delivery is observed. See [GitHub inbound webhooks](github-m4-webhooks.md).
