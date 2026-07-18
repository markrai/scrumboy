# Deployment and operations

Single-node self-hosting: one process, one SQLite database file, embedded SPA assets. No built-in clustering or HA; run one Scrumboy instance per database.

## Typical topology

```mermaid
flowchart TB
  User[Browser clients]
  Proxy[Optional reverse proxy Caddy nginx Traefik]
  App[scrumboy binary or container :8080]
  Vol["Persistent volume DATA_DIR"]
  DB[(app.db WAL journal)]
  IdP[Optional OIDC IdP]

  User --> Proxy
  Proxy -->|HTTP or HTTPS| App
  User -.->|LAN direct| App
  App --> Vol
  Vol --> DB
  App --> IdP
```

- **Docker (recommended):** pull `ghcr.io/markrai/scrumboy:latest`, publish `:8080`, mount a volume on `/data` (`DATA_DIR=/data`, `SQLITE_PATH=/data/app.db` are image defaults).
- **Docker (local clone):** `docker compose up --build` from the repo maps `./data:/data` (see `docker-compose.yml`, `Dockerfile`).
- **TLS:** terminate at the reverse proxy, or enable app TLS when both `SCRUMBOY_TLS_CERT` and `SCRUMBOY_TLS_KEY` exist.
- **Bind:** `BIND_ADDR` defaults to `:8080`.

## SQLite persistence

```mermaid
flowchart LR
  Env[DATA_DIR or SQLITE_PATH]
  Open[db.Open]
  Mig[migrate.Apply on startup]
  WAL[WAL mode default]
  Files["app.db plus -wal -shm sidecars"]
  Hourly[Hourly PRAGMA wal_checkpoint TRUNCATE]

  Env --> Open --> Mig --> WAL --> Files
  Hourly --> Files
```

WAL allows readers during writes but SQLite still has a **single writer**. Do not mount the same database from two running instances.

| Variable | Role |
|----------|------|
| `DATA_DIR` | Directory for `app.db` (default `./data`) |
| `SQLITE_PATH` | Full DB path override (Docker uses `/data/app.db`) |
| `SQLITE_JOURNAL_MODE` | Default `WAL` |
| `SQLITE_SYNCHRONOUS` | Default `FULL` |
| `SQLITE_BUSY_TIMEOUT_MS` | Writer lock wait (Compose sets `5000`) |

## Backup, restore, and upgrade

```mermaid
flowchart TB
  subgraph backup [Backup]
    FileCopy[Quiesce then copy app.db and WAL sidecars]
    Export[GET api backup export JSON]
    Key[SCRUMBOY_ENCRYPTION_KEY if 2FA enabled]
  end

  subgraph restore [Restore]
    Files[Replace DB files on disk]
    Import[POST api backup import replace merge or copy]
  end

  subgraph upgrade [Upgrade]
    NewBin[New container image or binary]
    SameData[Keep same DATA_DIR volume]
    AutoMig[Numbered SQL migrations on startup]
  end

  FileCopy --> Files
  Export --> Import
  Key --> Files
  Key --> Import
  NewBin --> SameData --> AutoMig
```

- **File backup:** stop the process (or ensure no concurrent writes), copy `app.db` and any `-wal` / `-shm` files together.
- **JSON backup:** scoped export via API; import supports replace, merge, or copy-as-new (see `scrumboy_backup_import.md`).
- **2FA:** back up `SCRUMBOY_ENCRYPTION_KEY` with the database; rotating it breaks stored TOTP secrets. Once encrypted auth/security data exists, startup requires the original valid key. On a fresh install with no encrypted data yet, an invalid key is ignored with a warning (2FA and password-reset encryption stay off until a valid key is set).
- **Owner recovery:** host-side `recover-owner` can establish or replace an owner local password without the IdP; see `docs/recovery.md` (stop the service and back up SQLite first).
- **Upgrade:** replace the binary or image, keep the data volume, restart; pending migrations apply automatically in `main.go`.

## Operational limits

- One instance per database file (no horizontal scale-out on shared SQLite).
- Suitable for small to medium teams on a single host; heavy concurrent write load is the main bottleneck.
- Optional features (OIDC, VAPID push, webhooks, MCP, wall canvas, markdown/mermaid notes) add env vars but are not required for core Kanban use.
