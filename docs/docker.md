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

Mount a named volume or host directory on `/data` if you want data to survive container recreation. That path holds the SQLite database, any WAL/SHM sidecars while the server is running, and file-backed uploads such as `user-wallpapers/`.

## Backup

Where practical, back up the whole persisted `/data` storage (named volume or host directory). That keeps SQLite state and file-backed uploads together.

JSON export/import is **not** a complete replacement for a `/data` backup. See [backup-and-import.md](backup-and-import.md). For deeper disaster-recovery layout (WAL/SHM, wallpapers, encryption key when used), see [diagrams/scrumboy_deployment_ops.md](diagrams/scrumboy_deployment_ops.md).

## Configuration

Supply Scrumboy environment variables through normal container or Compose `environment` configuration. See [environment-variables.md](environment-variables.md).
