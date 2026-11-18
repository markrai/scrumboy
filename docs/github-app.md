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

## Connect and test

1. Start in Full Mode, sign in as an instance Owner or Admin, and open **Settings > GitHub**.
2. Install the GitHub App at GitHub (using the install link if the slug is configured), then choose **Refresh installations**.
3. Select **Connect and sync** (or **Sync repositories**) for the installation. The server verifies that the installation belongs to the configured App, then requests a read-only installation token and enumerates all accessible repositories.
4. In the same tab, choose a Scrumboy project for which you are a Maintainer, choose a discovered repository and click **Add to project**.
5. Open a card in that project. The **External references** section can now link or unlink the project-associated repository. GitHub continues to control access to the repository's website.

Repository names and links may be visible to authenticated members of your Scrumboy project, independently of their GitHub access. Deliberately published Scrumboy boards do not automatically expose this integration's private management endpoints or browser UI.

## Administration API

Endpoints below require an authenticated Owner or Admin, and Scrumboy's normal `X-Scrumboy: 1` header for mutations.

| Method | Path | Effect |
| --- | --- | --- |
| GET | `/api/admin/integrations/github` | Configuration availability and locally connected accounts |
| GET | `/api/admin/integrations/github/installations` | Discover GitHub App installations |
| POST | `/api/admin/integrations/github/installations/{installationId}/sync` | Verify and reconcile one installation's repositories |
| GET | `/api/admin/integrations/github/connections/{connectionId}/resources` | List synchronized repository resources |
| DELETE | `/api/admin/integrations/github/connections/{connectionId}` | Disconnect the local connection and its references |

Project Maintainers can `POST /api/board/{slug}/integrations/resources` with `{"resourceId":123}`, and `DELETE /api/board/{slug}/integrations/resources/{resourceId}`, to manage project associations. See [integration architecture](architecture/integrations-foundation.md) for card API and MCP operations.

## Security and lifecycle

- GitHub App private keys exist only in the operator's PEM file and the application process. GitHub App installation tokens exist only in memory, with expiry-aware refresh and cache invalidation. Tokens, PEM data and JWTs are never persisted in the integration database.
- On GitHub denial or revocation, Scrumboy invalidates the cached token and reports the failure; it does not delete synchronized references based on an unsuccessful fetch.
- On a **successful complete synchronization**, removed or unselected repositories disappear from the connection and their Scrumboy references are cleaned up. Repository renames retain the same stable ID.
- Disconnecting locally deletes associated Scrumboy references, but does not uninstall the App on GitHub. To revoke GitHub-side access, uninstall the App or change its permissions in GitHub settings.
- Provider API access is deliberately fixed to GitHub's public API. GitHub Enterprise Server is not supported in M3.

For provider API details see the [GitHub App REST API documentation](https://docs.github.com/en/rest/apps/apps), [installation API](https://docs.github.com/en/rest/apps/installations) and [installation-token guide](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app).
