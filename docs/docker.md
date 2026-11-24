# Docker

Run Scrumboy from the published container image on GitHub Container Registry (GHCR). This page covers pull-and-run usage, `/data` persistence, and Docker-facing backup. It is not a source-build or contributor guide.

## Image

Published image:

`ghcr.io/markrai/scrumboy:latest`

Published architectures:

- `linux/amd64`
- `linux/arm64`

Docker automatically selects the variant that matches the host architecture.

## Docker run

Named volume for persistent data under `/data`:

```bash
docker run -d \
  --name scrumboy \
  -p 127.0.0.1:8080:8080 \
  -v scrumboy-data:/data \
  ghcr.io/markrai/scrumboy:latest
```

This creates or reuses the named Docker volume `scrumboy-data` mounted at `/data`. Open [http://localhost:8080](http://localhost:8080) after the container starts.

## Docker Compose

Minimal Compose file that uses the **published GHCR image** (not the repository’s developer `docker-compose.yml`, which uses `build: .`):

```yaml
services:
  scrumboy:
    image: ghcr.io/markrai/scrumboy:latest
    container_name: scrumboy
    ports:
      - "127.0.0.1:8080:8080"
    volumes:
      - ./data:/data
    restart: unless-stopped
```

```bash
docker compose up -d
```

Open [http://localhost:8080](http://localhost:8080). This example binds a host directory `./data` to `/data`.

## Persistent data

The image sets:

- `DATA_DIR=/data`
- `SQLITE_PATH=/data/app.db`

Mount a named volume or host directory on `/data` if you want data to survive container recreation. That path holds the SQLite database, any WAL/SHM sidecars while the server is running, file-backed uploads such as `user-wallpapers/` and note images in `assets/`, and the protected guided GitHub App credential bundle under `.scrumboy-github-app/` when that setup path is used.

## Backup

Where practical, back up the whole persisted `/data` storage (named volume or host directory). That keeps SQLite state, file-backed uploads and any guided GitHub App credential bundle together. Treat backups containing `.scrumboy-github-app/` as secrets.

JSON export/import is **not** a complete replacement for a `/data` backup. See [backup-and-import.md](backup-and-import.md). For deeper disaster-recovery layout (WAL/SHM, wallpapers, encryption key when used), see [diagrams/scrumboy_deployment_ops.md](diagrams/scrumboy_deployment_ops.md).

## Configuration

Supply Scrumboy environment variables through normal container or Compose `environment` configuration. See [environment-variables.md](environment-variables.md).

## GitHub integration

GitHub integration is optional and available in Full Mode. Scrumboy uses an **instance-owned GitHub App**; there is no required Scrumboy.com account, shared production App, or hosted relay.

For the guided setup, expose Scrumboy through a canonical HTTPS `SCRUMBOY_PUBLIC_BASE_URL`, then use **Settings → GitHub → Set up GitHub**. Guided credentials are stored under `/data/.scrumboy-github-app/`, so a persistent `/data` mount is required and host-level backups must protect that directory.

For conservative/manual setup, mount the App private key read-only and provide the App ID and PEM path:

```yaml
services:
  scrumboy:
    environment:
      SCRUMBOY_MODE: full
      SCRUMBOY_GITHUB_APP_ID: "123456"
      SCRUMBOY_GITHUB_APP_PRIVATE_KEY_FILE: /run/secrets/scrumboy-github-app.pem
      SCRUMBOY_GITHUB_APP_SLUG: your-app-slug
    volumes:
      - ./data:/data
      - /secure/github-app.private-key.pem:/run/secrets/scrumboy-github-app.pem:ro
```

Repository browsing and story References work without inbound webhooks. For real-time GitHub events and notification automations, GitHub must be able to reach `/api/integrations/github/webhook` over public HTTPS. Prefer a mounted `SCRUMBOY_GITHUB_WEBHOOK_SECRET_FILE` for manual production setup rather than an inline environment secret.

Full setup, permissions, backup/recovery and lifecycle details: [github-app.md](github-app.md). Webhook routing and troubleshooting: [github-m4-webhooks.md](github-m4-webhooks.md).

## Notes features

Markdown preview, Mermaid diagrams and [inline images](inline-images.md) in story notes are on by default in Full Mode. To opt out, merge one or more of these into your `environment` block and recreate the container:

```yaml
environment:
  SCRUMBOY_MERMAID_NOTES_ENABLED: "0"   # Mermaid only
  SCRUMBOY_INLINE_IMAGES_ENABLED: "0"   # images only
  SCRUMBOY_MARKDOWN_NOTES_ENABLED: "0"  # all three
```

The stock `docker-compose.yml` forwards these three variables from the host environment; leaving them unset keeps the defaults.

## Public boards

[Public read-only boards](public-boards.md) are gated by two opt-in flags that
default to off. Merge these variables into your **existing** Compose or
Portainer configuration (do not replace the whole `environment` block):

```yaml
environment:
  SCRUMBOY_MODE: full
  SCRUMBOY_PUBLIC_PROJECTS_ENABLED: "1"
  SCRUMBOY_LANDING_PAGE_ENABLED: "1"
```

`SCRUMBOY_LANDING_PAGE_ENABLED` is optional and independent: it only controls
the Full Mode marketing landing at `/` (workspace moves to `/_app`). Omit it
or set it to `"0"` to enable public boards without the landing page.

Upgrade steps:

1. Take a pre-upgrade backup of the `/data` volume (or host directory). JSON
   export is not a substitute; see [backup-and-import.md](backup-and-import.md).
2. Pull or deploy an image version that **contains the public-board
   implementation**. The flags do nothing on older images — the image
   determines the feature, not the variables.
3. Preserve the `/data` volume mount, then recreate (Compose) or redeploy
   (Portainer, with repull) the container so it picks up the new image and
   variables.

The GitHub Actions Docker publishing workflow publishes `latest` from `main`
only — a feature branch never updates `latest` automatically. For temporary
branch testing, the manual workflow can publish a **`test-build`** image, but
that tag is **mutable** (every manual run overwrites it), so verify the image
digest before use and never treat `test-build` as a pinned production
release.

Operational feature rollback: set `SCRUMBOY_PUBLIC_PROJECTS_ENABLED` back to `"0"` (or remove it; the landing-page flag is independent and may stay enabled)
and recreate/redeploy. New public API requests immediately return not-found, while streams open at redeploy time are terminated by the restart itself (they do not turn into `404` responses). The `/{slug}` page address still serves the SPA shell (HTTP `200`); its client-side UI then reports the board as unavailable once the public API denies access.
Stored publication preferences are kept. That is distinct from
binary/database rollback, which may require restoring the pre-upgrade
database backup from step 1.
