# GitHub App integration (M3)

M3 provides an opt-in GitHub App connection and repository reference workflow in Scrumboy Full Mode. It registers GitHub repositories as external resources and lets project Maintainers associate them with durable projects, so Contributors can link cards to those repositories. M3.2 adds explicit, read-only GitHub issue and pull-request references and title search. It does **not** modify GitHub issues or pull requests, synchronize card content, or automatically change a Scrumboy card when GitHub state changes. Webhooks and optional automations have separate milestones.

## Intended user experience: shared `scrumboy-connector` GitHub App

**Product decision:** Scrumboy users will piggyback on the Scrumboy-managed **`scrumboy-connector`** GitHub App. An ordinary user should **not** need to register a GitHub App, generate a private key, or configure an App ID to connect their own repositories.

The intended flow is **Settings → GitHub → Connect GitHub → approve installation on your own account/organization → select repositories → link issues/PRs to a Scrumboy project**. A user grants GitHub access to the repositories they choose; their installation is separate from the maintainer's `markrai` installation and does not grant access to that account's repositories. Publishing a Scrumboy board remains the board owner's decision and must not bypass project authorization.

**This is the required product direction, not a working capability yet.** PR #366 currently implements a trusted, administrator-managed, direct-GitHub-App integration. It can discover every installation of the configured App and does not bind a GitHub identity/installation to an individual Scrumboy user or tenant. **Do not publish the shared App for unrelated Scrumboy users or advertise a self-service Connect GitHub flow until [the shared-App security gate](github-shared-app.md) is implemented and qualified.**

The GitHub App's **App ID and slug are public identifiers**; they do not authenticate a caller. The PEM private key and installation tokens must remain on infrastructure operated by the Scrumboy integration service, never in a browser, Scrumboy card, Git repository, or a downloadable self-hosted distribution.

For the target multi-user design and self-hosted support, see [Shared `scrumboy-connector` architecture and rollout](github-shared-app.md).

## Current trusted-operator setup (temporary M3 test mode)

The steps below document the **existing direct integration**, which is useful for isolated Vega qualification and trusted instance administrators. They are **not** the intended setup instructions for ordinary Scrumboy users when the shared-App experience ships.

### Register a GitHub App for an isolated operator deployment

1. For current operator-only tests, register a GitHub App owned by an account or organization you control, or use the privately provisioned Scrumboy test App on infrastructure you administer. **End users must not create their own Apps under the target shared-App design.**
2. Grant **Metadata: Read-only** for installation and repository discovery. To enable M3.2 issue/PR references and search, also grant **Issues: Read-only** and **Pull requests: Read-only**. Existing GitHub App installations must approve the expanded permissions. No write scopes, account permissions, organization permissions, or Contents scope are required.
3. Install the App on selected repositories. For a first test, select a disposable repository. Webhooks can remain disabled until M4 provides a validated webhook receiver.
4. Record the App ID and App slug. Generate and download the GitHub App RSA private key (a PEM file).

Never commit the PEM file to a repository, place its content into a Scrumboy card, or send it to the browser.

## Local configuration (trusted operator tests only)

**Not required for ordinary users in the intended shared-App flow.** These variables configure the existing direct-mode backend and must not be filled using a shared private key on arbitrary user-operated Scrumboy servers.

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

**Current implementation and required next step:** M3.2 supports **trusted instance-administrator-managed connections only**. For broad user adoption, Scrumboy will use the shared **`scrumboy-connector`** App, not require users to create their own Apps. That **requires** a separately authenticated, ownership-bound installation flow and server-side credential broker, described in [the shared-App design](github-shared-app.md). Neither the direct-mode code nor this documentation change implements that broker. Do not share the App's PEM key, accept arbitrary installation IDs as authorization, or expose the shared installation directory to unrelated tenants.

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

- GitHub App private keys exist only in the operator's PEM file and the application process. GitHub App installation tokens exist only in memory, with expiry-aware refresh and cache invalidation. Tokens, PEM data and JWTs are never persisted in the integration database.
- On GitHub denial or revocation, Scrumboy invalidates the cached token and reports the failure; it does not delete or change synchronized references based on an unsuccessful or incomplete fetch.
- On a **successful complete synchronization**, repositories missing from the installation's accessible set are marked **unavailable**. Their last-known metadata, Scrumboy resource IDs, project associations, and card references are retained. Existing links are displayed as unavailable and may be explicitly unlinked, but no new links or project associations may be added until GitHub access is restored. A subsequent complete scan that sees the same immutable GitHub repository ID restores availability without losing references. Repository renames retain the same stable ID.
- Disconnecting locally deletes associated Scrumboy references and attempts to revoke the installation token currently cached by the process. It does not uninstall the App on GitHub or revoke other tokens issued elsewhere. To remove GitHub-side access altogether, uninstall the App or change its permissions in GitHub settings.
- Provider API access is deliberately fixed to GitHub's public API. GitHub Enterprise Server is not supported in M3.
- Shared-App end-user installation, self-hosted service-to-service authorization, and per-user installation binding are **planned and not yet implemented**; the existing instance-global administrator connection model is not a safe substitute for them.

For provider API details see the [GitHub App REST API documentation](https://docs.github.com/en/rest/apps/apps), [installation API](https://docs.github.com/en/rest/apps/installations) and [installation-token guide](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app).

## Optional GitHub webhook receiver (M4)

M4 adds an independent, opt-in **inbound** webhook receiver. The M3 integration and its normal manual repository refresh work without this feature. For signed webhook setup, HTTPS staging, failure semantics and administration, see [GitHub inbound webhooks](github-m4-webhooks.md).
