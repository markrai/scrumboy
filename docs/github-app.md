# GitHub App integration (M3)

M3 provides an opt-in GitHub App connection and repository reference workflow in Scrumboy Full Mode. It registers GitHub repositories as external resources and lets project Maintainers associate them with durable projects, so Contributors can link cards to those repositories. It does **not** read or modify GitHub issues, accept GitHub webhooks, or synchronize card content. Those are later milestones.

## Create the GitHub App

1. In GitHub's Developer settings, create a GitHub App owned by an account or organization you control.
2. Grant the **Metadata: Read-only** repository permission, and no other repository, organization or account permissions for M3. Metadata read is mandatory for apps; no write scopes are required.
3. Install the App on selected repositories. For a first test, select a disposable repository. Webhooks can remain disabled until M4 provides a validated webhook receiver.
4. Record the App ID and App slug. Generate and download the GitHub App RSA private key (a PEM file).

Never commit the PEM file to a repository, place its content into a Scrumboy card, or send it to the browser.

## Local configuration

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
4. Under **Project repositories**, choose a **Scrumboy project** by its human-readable name. Only durable projects where your current user is a **Maintainer** are shown. Opening Settings from a project automatically selects that project. You no longer need to enter a project URL slug or click **Load project**.
5. If only one GitHub account is connected, its name is displayed without a dropdown. With multiple accounts, choose the GitHub account whose repositories you want to associate.
6. Once a project is selected, its available repositories load automatically. Select a **GitHub repository** and click **Add to project**. This permits cards in that project to reference the repository; it does not modify GitHub.
7. Open a card in the project. In **External references**, link the associated repository, verify the URL, then unlink it as an independent step. Test the card with a Viewer, Contributor and Maintainer to verify read/write boundaries.

Repository names and links may be visible to authenticated members of your Scrumboy project independently of their GitHub access. Deliberately published Scrumboy boards do not automatically expose integration management APIs or private card-reference UI.

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

For provider API details see the [GitHub App REST API documentation](https://docs.github.com/en/rest/apps/apps), [installation API](https://docs.github.com/en/rest/apps/installations) and [installation-token guide](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app).

## Optional GitHub webhook receiver (M4)

M4 adds an independent, opt-in **inbound** webhook receiver. The M3 integration and its normal manual repository refresh work without this feature. For signed webhook setup, HTTPS staging, failure semantics and administration, see [GitHub inbound webhooks](github-m4-webhooks.md).
